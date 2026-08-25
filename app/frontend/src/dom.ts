// Tiny DOM lookup helper shared by every layer that owns its own DOM refs.
// Each module queries the ids it owns at module-eval time — this bundle's
// script runs once, after the DOM the elements live in (same timing the
// F1 monolith relied on for its single `el` object).
export function byId<T extends HTMLElement = HTMLElement>(id: string): T {
  return document.getElementById(id) as T;
}

// INTERACTION-HARDENING (docs/plan/ROADMAP.md): every <img> this frontend
// puts in the document is created HERE, so "sprites are not draggable" is one
// line in one place instead of a rule every render module has to remember.
//
// `draggable = false` is the half CSS cannot do. `-webkit-user-drag: none`
// (game.css, "Interaction hardening") stops the drag GESTURE in WebKit and
// Blink, but the HTML `draggable` IDL attribute is what makes an <img> a
// native drag source in the first place — an image is draggable by DEFAULT,
// unlike almost every other element — and turning it off is what guarantees
// `dragstart` never fires and the browser never gets the chance to hand the
// image URL to a drop target. Dropping a sprite onto the same window used to
// NAVIGATE to it: a full-page /assets/dev_base_idle.png, the game gone, and
// the only way back a reload.
//
// Callers still set src/className/style themselves; this only fixes the two
// properties that must be true of every sprite in the product.
export function spriteImg(): HTMLImageElement {
  const img = document.createElement('img');
  img.draggable = false;
  return img;
}
