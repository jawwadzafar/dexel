// Tint mechanism (mask + multiply), art-direction.md "The CSS tint
// mechanism", plus the generic sprite-positioning primitives everything in
// the render layer (and the store feature's preview pane, which composes
// the same scene at a different scale) builds on. Pure DOM-node builders:
// given a file + geometry + colour, return a node ready to be positioned
// and appended by the caller — no state reads, no event wiring.
import type { Rect } from '../geometry';
import { assetUrl } from '../assets';

export function buildTintLayer(formFile: string | null | undefined, tintHex: string): HTMLDivElement {
  const wrap = document.createElement('div');
  wrap.className = 'tintable';
  wrap.style.position = 'absolute';
  wrap.style.setProperty('--tint', tintHex);
  wrap.style.setProperty('--form', 'url(' + assetUrl(formFile) + ')');
  const fill = document.createElement('div');
  fill.className = 'tint-fill';
  const shade = document.createElement('img');
  shade.className = 'tint-shade';
  shade.alt = '';
  shade.src = assetUrl(formFile) || '';
  wrap.appendChild(fill);
  wrap.appendChild(shade);
  return wrap;
}
export function positionEl<T extends HTMLElement>(node: T, rect: Rect): T {
  node.style.left = rect.left + 'px';
  node.style.top = rect.top + 'px';
  node.style.width = rect.w + 'px';
  node.style.height = rect.h + 'px';
  return node;
}
export function plainImg(file: string | null | undefined, rect: Rect, cls?: string): HTMLImageElement {
  const img = document.createElement('img');
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
