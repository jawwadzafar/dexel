// STORE-2.0 (docs/plan/ROADMAP.md §STORE-2.0): the runtime tint SYSTEM — the
// wire fields, per-item colour ownership, the swatch picker, BUY_TINT and the
// whole economy path — is GONE. Colours are ordinary catalog ITEMS now
// (hoodie_classic_indigo, chair_racer_ember, monitor_neon, ...). What survives
// is a pure RENDER concern: an item id still ENCODES a colour, and the scene
// and the store recolour the shared grayscale "form" sprites with it via the
// existing CSS multiply mechanism (render/tint.ts) rather than baking one PNG
// per colour.
//
// This module is the ONE place the colour-token -> hex mapping lives now (it
// used to arrive on the wire as catalog.tints). The hexes MIRROR the internal
// tint table in tools/gen_assets.py — keep the two in sync.

// The slots whose items carry a colour token and are drawn by tinting a shared
// grayscale form: the two formerly-"tintable" slots, plus the new monitor
// bezel slot (STORE-2.0's ninth). Everything else names a real, already-
// coloured sprite straight off the catalog.
export const COLOUR_SLOTS: readonly string[] = ['hoodie', 'chair', 'monitor'];
export function isColourSlot(slotId: string): boolean {
  return COLOUR_SLOTS.indexOf(slotId) !== -1;
}

// token -> hex, mirroring gen_assets.py's tint palette.
const COLOURS: Record<string, string> = {
  slate: '#2b2b33',
  cobalt: '#4a7fa8',
  forest: '#4e8b4f',
  ember: '#a45c3a',
  neon: '#e86aa4',
  indigo: '#6a5aa0'
};

// The colour token an id ends with (hoodie_classic_indigo -> "indigo",
// monitor_neon -> "neon"), or null when the last segment is not a known colour
// (every non-colour-slot item, and any malformed id — which then renders
// untinted rather than crushed to black).
export function colourToken(itemId: string | null | undefined): string | null {
  if (!itemId) return null;
  const seg = itemId.slice(itemId.lastIndexOf('_') + 1);
  return Object.prototype.hasOwnProperty.call(COLOURS, seg) ? seg : null;
}

// The style segment of a two-part colour slot id (hoodie_classic_indigo ->
// "classic", chair_racer_ember -> "racer"). Monitor has no style. null when
// the id doesn't parse to <slot>_<style>_<colour>.
export function styleToken(itemId: string | null | undefined): string | null {
  if (!itemId) return null;
  const parts = itemId.split('_');
  if (parts.length < 3) return null;
  return parts[1];
}

// The tint hex for an item id, defaulting to white (#ffffff) — the
// multiplicative identity, so an unknown/colourless id shows the form's own
// neutral grayscale instead of a black silhouette (the same never-black rule
// the old wire-driven tintHexFor followed).
export function colourHexForItem(itemId: string | null | undefined): string {
  const tok = colourToken(itemId);
  return tok ? COLOURS[tok] : '#ffffff';
}

// Human label for a colour token ("indigo" -> "Indigo"), for any UI that names
// the colour. Empty string for a null/unknown token.
export function colourLabel(itemId: string | null | undefined): string {
  const tok = colourToken(itemId);
  return tok ? tok.charAt(0).toUpperCase() + tok.slice(1) : '';
}

// --- Grayscale-form filename derivation (the shared sprites the CSS tint
// recolours). item.sprite/item.thumb in the catalog name the per-COLOUR files
// (deferred stage-B art); the render layer instead composes the per-STYLE
// grayscale forms that already exist and tints them by the id's colour. ---

// The hoodie's palette-pure style overlay drawn over the tinted dev form
// (hoodie_classic_indigo -> "hoodie_classic.png").
export function hoodieOverlayFile(itemId: string): string {
  return 'hoodie_' + styleToken(itemId) + '.png';
}
// The chair's grayscale form + detail scene sprites for a style.
export function chairFormFile(itemId: string): string {
  return 'chair_' + styleToken(itemId) + '_form.png';
}
export function chairDetailFile(itemId: string): string {
  return 'chair_' + styleToken(itemId) + '_detail.png';
}
// The chair's grayscale store-thumbnail form + detail for a style.
export function chairThumbFormFile(itemId: string): string {
  return 'thumb_chair_' + styleToken(itemId) + '_form.png';
}
export function chairThumbDetailFile(itemId: string): string {
  return 'thumb_chair_' + styleToken(itemId) + '_detail.png';
}
// The hoodie's grayscale store-thumbnail form + detail for a style (the store
// card composes these two, tinted — the same recipe the pre-STORE-2.0 store
// used for a tintable slot, minus the per-colour swatch choice).
export function hoodieThumbFormFile(itemId: string): string {
  return 'thumb_hoodie_' + styleToken(itemId) + '_form.png';
}
export function hoodieThumbDetailFile(itemId: string): string {
  return 'thumb_hoodie_' + styleToken(itemId) + '_detail.png';
}
// The monitor's grayscale bezel overlay (scene) + thumbnail (store). One each,
// tinted by the equipped/selected colour; the screen rect stays fixed.
export const MONITOR_FRAME_FILE = 'monitor_frame.png';
export const MONITOR_THUMB_FILE = 'thumb_monitor_form.png';
