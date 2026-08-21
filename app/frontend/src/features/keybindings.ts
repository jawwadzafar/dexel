// FEATURE/LOGIC layer — global keyboard routing (ui-spec.md §5.2): [S] /
// Tab open the store, [A] opens the activity log, [H] opens the history
// modal (Analytics track Phase A3, docs/plan/A3-design.md §6/§7 Task
// TS-1), and while any one modal is open its own keydown handler owns the
// keyboard (Esc always falls through to native <dialog> behaviour, never
// intercepted here). This module knows the modal features' public
// open()/isOpen()/handleKeydown() surface — it never reaches into their
// DOM.
//
// The hamburger menu (./menu.ts) is deliberately NOT given the same
// keyboard-ownership tier as a modal: it never captures keys the way a
// <dialog> with real content does, it just closes on Esc, and it steps
// aside for [S]/[A]/[H]/Tab so those shortcuts keep working identically
// whether or not the menu happens to be open. [M] toggles the menu itself.
import * as storeModal from './store-modal';
import * as activityModal from './activity-modal';
import * as historyModal from './history-modal';
import * as menu from './menu';

export function init(): void {
  document.addEventListener('keydown', function (e: KeyboardEvent) {
    if (storeModal.isOpen()) {
      storeModal.handleKeydown(e);
      return;
    }
    if (activityModal.isOpen()) {
      activityModal.handleKeydown(e);
      return;
    }
    if (historyModal.isOpen()) {
      historyModal.handleKeydown(e);
      return;
    }
    if (menu.isOpen() && e.key === 'Escape') {
      e.preventDefault();
      menu.close();
      return;
    }
    if (e.key === 's' || e.key === 'S') { e.preventDefault(); menu.close(); storeModal.open(); }
    else if (e.key === 'Tab') { e.preventDefault(); menu.close(); storeModal.open(); }
    else if (e.key === 'a' || e.key === 'A') { e.preventDefault(); menu.close(); activityModal.open(); }
    else if (e.key === 'h' || e.key === 'H') { e.preventDefault(); menu.close(); historyModal.open(); }
    else if (e.key === 'm' || e.key === 'M') { e.preventDefault(); menu.toggle(); }
  });
}
