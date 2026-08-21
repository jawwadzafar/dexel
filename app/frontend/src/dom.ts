// Tiny DOM lookup helper shared by every layer that owns its own DOM refs.
// Each module queries the ids it owns at module-eval time — this bundle's
// script runs once, after the DOM the elements live in (same timing the
// F1 monolith relied on for its single `el` object).
export function byId<T extends HTMLElement = HTMLElement>(id: string): T {
  return document.getElementById(id) as T;
}
