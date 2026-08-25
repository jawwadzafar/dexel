// Tint mechanism (mask + multiply), art-direction.md "The CSS tint
// mechanism", plus the generic sprite-positioning primitives everything in
// the render layer (and the store feature's preview pane, which composes
// the same scene at a different scale) builds on. Pure DOM-node builders:
// given a file + geometry + colour, return a node ready to be positioned
// and appended by the caller — no state reads, no event wiring.
//
// The builders have companion MUTATORS (setSrc / updateTintLayer) because a
// caller that repaints on a hot path must keep element identity: a fresh
// <img> has no bitmap until its resource is decoded, and Chrome decodes even
// a cached image asynchronously, so replacing an element paints a hole for
// one or more frames. That is the whole cause of the character-flicker bug
// (BUG-1) — see render/scene.ts's header. Every mutator writes only when the
// value actually changed, so a no-op repaint invalidates nothing.
import type { Rect } from '../geometry';
import { assetUrl } from '../assets';
import { spriteImg } from '../dom';

// Assigns a sprite URL to an existing <img> only when it differs from what
// the element is already showing. Compares the ATTRIBUTE, not `img.src`:
// the property reflects the resolved absolute URL ("http://host/assets/x.png")
// and would never equal the relative string we assign, so comparing it would
// make this guard a no-op and re-trigger the image-update algorithm on every
// render.
//
// A null/absent file is a NO-OP, not a clear: `src = ''` resolves to the
// document's own URL and makes the browser fetch this page as an image. The
// callers that have no sprite to show hide the element instead, so whatever it
// still holds is never painted.
export function setSrc(img: HTMLImageElement, file: string | null | undefined): void {
  const next = assetUrl(file);
  if (next && img.getAttribute('src') !== next) img.src = next;
}

export function buildTintLayer(formFile: string | null | undefined, tintHex: string): HTMLDivElement {
  const wrap = document.createElement('div');
  wrap.className = 'tintable';
  wrap.style.position = 'absolute';
  wrap.style.setProperty('--tint', tintHex);
  const fill = document.createElement('div');
  fill.className = 'tint-fill';
  const shade = spriteImg();
  shade.className = 'tint-shade';
  shade.alt = '';
  // A null form file leaves both the mask and the <img> UNSET rather than
  // pointing them at "url(null)" / "" — an empty src resolves to the
  // document's own URL and would fetch this page as an image. The scene
  // compositor builds its persistent chair layer before it knows which chair
  // is equipped and fills it in via updateTintLayer() below, so null is a
  // real case here, not just defensiveness.
  if (formFile) {
    wrap.style.setProperty('--form', 'url(' + assetUrl(formFile) + ')');
    shade.src = assetUrl(formFile) as string;
  }
  wrap.appendChild(fill);
  wrap.appendChild(shade);
  return wrap;
}
// Re-points a layer built by buildTintLayer at a different form file and/or
// tint IN PLACE, keeping the same three elements alive. Each of the three
// values is written only when it changed, which matters most for `--form`:
// it is a CSS mask-image, and a mask whose new bitmap has not been decoded
// yet masks the fill away entirely, so a needless re-assignment can blank a
// tinted layer for a frame even though the file is already in cache.
export function updateTintLayer(wrap: HTMLDivElement, formFile: string | null | undefined, tintHex: string): void {
  if (wrap.style.getPropertyValue('--tint') !== tintHex) wrap.style.setProperty('--tint', tintHex);
  if (!formFile) return; // same no-op-not-clear rule as setSrc above
  const formUrl = 'url(' + assetUrl(formFile) + ')';
  if (wrap.style.getPropertyValue('--form') !== formUrl) wrap.style.setProperty('--form', formUrl);
  setSrc(wrap.querySelector('.tint-shade') as HTMLImageElement, formFile);
}
export function positionEl<T extends HTMLElement>(node: T, rect: Rect): T {
  node.style.left = rect.left + 'px';
  node.style.top = rect.top + 'px';
  node.style.width = rect.w + 'px';
  node.style.height = rect.h + 'px';
  return node;
}
export function plainImg(file: string | null | undefined, rect: Rect, cls?: string): HTMLImageElement {
  const img = spriteImg();
  img.className = 'layer sprite' + (cls ? ' ' + cls : '');
  img.alt = '';
  img.src = assetUrl(file) || '';
  img.style.position = 'absolute';
  positionEl(img, rect);
  return img;
}
// "swatch chip = tint * 0xd4/0xff" (art-direction.md, step-4 base fabric)
export function swatchColor(hex: string): string {
  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);
  const f = 0xd4 / 0xff;
  const rr = Math.round(r * f), gg = Math.round(g * f), bb = Math.round(b * f);
  return 'rgb(' + rr + ',' + gg + ',' + bb + ')';
}
