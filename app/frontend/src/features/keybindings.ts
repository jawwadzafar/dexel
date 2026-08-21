// FEATURE/LOGIC layer — global keyboard routing (ui-spec.md §5.2): [S] /
// Tab open the store, [A] opens the activity log, [H] opens the history
// modal (Analytics track Phase A3, docs/plan/A3-design.md §6/§7 Task
// TS-1), and while any one modal is open its own keydown handler owns the
// keyboard (Esc always falls through to native <dialog> behaviour, never
// intercepted here). This module knows the modal features' public
// open()/isOpen()/handleKeydown() surface — it never reaches into their
// DOM.
import * as storeModal from './store-modal';
import * as activityModal from './activity-modal';
import * as historyModal from './history-modal';

export function init(): void {
  document.addEventListener('keydown', function (e: KeyboardEvent) {
    if (storeModal.isOpen()) {
      storeModal.handleKeydown(e);
    } else if (activityModal.isOpen()) {
      activityModal.handleKeydown(e);
    } else if (historyModal.isOpen()) {
      historyModal.handleKeydown(e);
    } else {
      if (e.key === 's' || e.key === 'S') { e.preventDefault(); storeModal.open(); }
      else if (e.key === 'Tab') { e.preventDefault(); storeModal.open(); }
      else if (e.key === 'a' || e.key === 'A') { e.preventDefault(); activityModal.open(); }
      else if (e.key === 'h' || e.key === 'H') { e.preventDefault(); historyModal.open(); }
    }
  });
}
