// RENDER layer — the window-scaling contract (BUG-2, docs/ui-spec.md §0.1).
//
// THE PROBLEM. The whole UI is a 640x400 fixed-pixel layout (ui-spec.md §0:
// absolute positioning, integer px only, no reflow) inside a native window
// whose default inner size is 660x460 and which the user can resize freely.
// At 1:1 that left the layout pinned to the top-left with a band of dead
// window down the right and along the bottom — the HUD panels looked stranded
// mid-air and the extra space read as broken rather than as a margin.
//
// THE FIX. The fixed-pixel layout stays exactly as authored and the WHOLE of
// it is scaled as one unit to fit the window, aspect ratio preserved exactly,
// centred, with the leftover area letterboxed/pillarboxed in the same
// --shadow the layout already paints as its own ground (so the fill reads as
// the app's bezel, not as a gap). Nothing reflows, so no pixel of the layout
// can move relative to any other — which is the property a `%`/flex-based
// "responsive" pass would have destroyed.
//
// A `transform: scale()` and not `zoom`: a transform is applied AFTER layout,
// so the layout stays exactly integral in CSS px and the whole composited
// result is scaled uniformly. `zoom` re-lays-out at the scaled size, which at
// a fractional factor rounds every box independently and lets 8px cells drift
// apart by a pixel — visible as broken alignment in a design built on an 8px
// grid.
//
// THE SCALE RULE, and why:
//
//   exact = min(vw / 640, vh / 400)         — the largest fit, no clipping
//
//   * exact < 1 (window smaller than the layout): use `exact`. There is no
//     crisper factor available below 1x, and clipping instead would hide
//     controls the user cannot then reach.
//   * otherwise: snap DOWN to the nearest CRISP factor if that costs at most
//     1/8 of the size, else use `exact`.
//
// A "crisp" factor is one where a single art pixel covers a whole number of
// DEVICE pixels — an integer at dpr 1, and also 1.5x, 2.5x, ... on a retina
// display, where 1.5 CSS px is exactly 3 device px. At a non-crisp factor
// nearest-neighbour scaling gives art pixels uneven widths and the 8px pixel
// font is rasterised off its own grid; both are visibly mushy, so a crisp
// factor is worth giving up a little size for. 1/8 is where that trade
// turns: below it the size difference is not noticeable, and above it (a 2x
// window rendered at 1x, say) the empty margin is far worse than soft pixels.
// Snapping only ever goes DOWN — snapping up would overflow the window.
//
// At the DEFAULT 660x460 this yields exact = 1.03125 -> 1x, i.e. the shipping
// appearance is byte-for-byte the authored layout, centred in a 10px
// pillarbox and a 30px letterbox.

const BASE_W = 640;
const BASE_H = 400;
const SNAP_TOLERANCE = 0.125; // 1/8 — see the rule above

// The smallest scale increment that still lands art pixels on whole device
// pixels. A fractional devicePixelRatio (a Windows 125% display) has no such
// increment at all, so it falls back to whole CSS pixels.
function crispStep(): number {
  const dpr = window.devicePixelRatio || 1;
  return dpr >= 1 && dpr === Math.floor(dpr) ? 1 / dpr : 1;
}

// Exported for the ?dev=1 console and for reasoning about a reported size
// without having to resize a real window.
export function chooseScale(vw: number, vh: number, step: number): number {
  const exact = Math.min(vw / BASE_W, vh / BASE_H);
  if (!(exact > 0)) return 1; // a zero-sized viewport (minimised / detached)
  if (exact < 1) return exact;
  const snapped = Math.floor(exact / step) * step;
  return exact / snapped <= 1 + SNAP_TOLERANCE ? snapped : exact;
}

// The three custom properties this module owns. #root consumes all three;
// the modal dialogs consume them too, because a <dialog> opened with
// showModal() is promoted to the TOP LAYER and a top-layer element is NOT
// affected by an ancestor's transform (verified in this project's own
// headless Chromium: a dialog inside a scale(0.5) parent renders at 1:1).
// Custom-property INHERITANCE still reaches it — the top layer changes where
// an element paints, not where it sits in the DOM — so the same three values
// drive one shared transform rule in game.css and the modals track the scene
// exactly. Hit testing then lines up for free: a CSS transform is part of
// the box the browser hit-tests, so a click at a scaled position maps back
// through it without any viewport-coordinate maths in this frontend (there is
// none anywhere in app/frontend/src — no getBoundingClientRect, no clientX;
// the store's scrollbar thumb reads scrollTop/scrollHeight off #store-grid,
// which are element-local LAYOUT metrics in unscaled CSS px and are therefore
// untouched by a transform applied after layout).
function apply(): void {
  const doc = document.documentElement;
  // clientWidth/clientHeight, not innerWidth/innerHeight: this is the layout
  // viewport, and html/body carry `overflow: hidden` so it never includes a
  // scrollbar gutter.
  const vw = doc.clientWidth;
  const vh = doc.clientHeight;
  const scale = chooseScale(vw, vh, crispStep());
  // Integer offsets so an integer scale lands the whole layout on the pixel
  // grid; at 660x460 this is exactly (10, 30).
  const ox = Math.round((vw - BASE_W * scale) / 2);
  const oy = Math.round((vh - BASE_H * scale) / 2);
  doc.style.setProperty('--ui-scale', String(scale));
  doc.style.setProperty('--ui-ox', ox + 'px');
  doc.style.setProperty('--ui-oy', oy + 'px');
}

// Re-armed after every change, because the query is written against the
// CURRENT ratio: it fires exactly when the window lands on a display with a
// different one, which is the one way the crisp step can change without a
// resize event.
let dprQuery: MediaQueryList | null = null;
function onDprChange(): void {
  watchDpr();
  apply();
}
function watchDpr(): void {
  if (dprQuery) dprQuery.removeEventListener('change', onDprChange);
  dprQuery = window.matchMedia('(resolution: ' + (window.devicePixelRatio || 1) + 'dppx)');
  dprQuery.addEventListener('change', onDprChange);
}

export function initViewport(): void {
  apply();
  window.addEventListener('resize', apply);
  watchDpr();
}
