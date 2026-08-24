// FEATURE/LOGIC layer — the title-bar hamburger menu. Owns exactly two DOM
// nodes: #menu-open (the hamburger button) and #menu-panel (the dropdown
// list of section launchers) — nothing else. It never reaches into the
// store/activity/history modal modules and never calls their open()
// itself: those buttons (#store-open / #activity-open / #history-open)
// live inside #menu-panel in index.html and already carry their own
// click -> open() wiring from their own feature modules. This module's
// only job on a pick is to close ITSELF, which it does generically by
// listening for a click that lands on anything carrying the shared
// `.menu-item` class inside the panel — so a future entry (Sessions,
// Goals, ...) is just one more `.menu-item` button in index.html; this
// file needs zero changes to support it.
//
// PR-5 (docs/production-runtime/MIGRATION_PLAN.md §PR-5) is the one
// exception to "never reaches into ... modules": pause/resume has no
// dedicated feature module (no modal to open), so this file owns
// #pause-toggle directly — reading live state from ../state/store and
// sending the action via ../state/ws-client, same as any other feature
// module would for its own button.
import { byId } from '../dom';
import { getState } from '../state/store';
import { sendAction } from '../state/ws-client';

const el = {
  menuOpenBtn: byId<HTMLButtonElement>('menu-open'),
  panel: byId<HTMLDivElement>('menu-panel'),
  pauseToggleBtn: byId<HTMLButtonElement>('pause-toggle')
};

export function isOpen(): boolean {
  return el.panel.classList.contains('visible');
}

function onOutsideMouseDown(e: MouseEvent): void {
  const target = e.target as Node | null;
  if (target && (el.panel.contains(target) || el.menuOpenBtn.contains(target))) return;
  close();
}

export function open(): void {
  if (isOpen()) return;
  el.panel.classList.add('visible');
  el.menuOpenBtn.setAttribute('aria-expanded', 'true');
  // Capture phase so this always sees the click before anything downstream
  // could stop its propagation.
  document.addEventListener('mousedown', onOutsideMouseDown, true);
}

export function close(): void {
  if (!isOpen()) return;
  el.panel.classList.remove('visible');
  el.menuOpenBtn.setAttribute('aria-expanded', 'false');
  document.removeEventListener('mousedown', onOutsideMouseDown, true);
}

export function toggle(): void {
  if (isOpen()) close(); else open();
}

// PR-5 — the label flips on the server's own `paused` bool, never on a
// client-held mode (the same "server is the sole source of truth" rule
// every other feature module follows). Called from main.ts's renderAll,
// alongside render/chrome.ts's renderChrome(), so the label stays correct
// on every ~1s state tick and every mutation, whether or not the menu
// happens to be open.
export function renderPauseLabel(): void {
  const state = getState();
  const paused = !!(state && state.paused);
  el.pauseToggleBtn.textContent = paused ? '[P] RESUME' : '[P] PAUSE';
}

// The click always reads LIVE state at the moment of the click (never an
// assumption from the label it just rendered) — the server is the sole
// source of truth, so what gets sent is whatever is true right now, not
// whatever was true when this button was last painted.
el.pauseToggleBtn.addEventListener('click', function () {
  const state = getState();
  sendAction(state && state.paused ? { action: 'RESUME' } : { action: 'PAUSE' });
});

// The hamburger button toggles the panel; stopPropagation keeps this same
// click from also being seen as an "outside" click by the listener open()
// just installed (harmless either way, since the button is excluded from
// that check, but this keeps the two paths independent).
el.menuOpenBtn.addEventListener('click', function (e) {
  e.stopPropagation();
  toggle();
});

// Any pick inside the panel (present or future `.menu-item`) closes the
// menu. The item's own module (store-modal.ts etc.) owns what opening its
// modal actually does — this listener never calls it.
el.panel.addEventListener('click', function (e) {
  const target = e.target as HTMLElement;
  if (target.closest('.menu-item')) close();
});
