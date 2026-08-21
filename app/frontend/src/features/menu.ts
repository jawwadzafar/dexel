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
import { byId } from '../dom';

const el = {
  menuOpenBtn: byId<HTMLButtonElement>('menu-open'),
  panel: byId<HTMLDivElement>('menu-panel')
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
