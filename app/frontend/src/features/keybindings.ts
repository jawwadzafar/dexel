// FEATURE/LOGIC layer — global keyboard routing (ui-spec.md §5.2): [S] /
// Tab open the store, [A] opens the activity log, and while either modal
// is open its own keydown handler owns the keyboard (Esc always falls
// through to native <dialog> behaviour, never intercepted here). This
// module knows the two modal features' public open()/isOpen()/
// handleKeydown() surface — it never reaches into their DOM.
import * as storeModal from './store-modal';
import * as activityModal from './activity-modal';

export function init(): void {
  document.addEventListener('keydown', function (e: KeyboardEvent) {
    if (storeModal.isOpen()) {
      storeModal.handleKeydown(e);
    } else if (activityModal.isOpen()) {
      activityModal.handleKeydown(e);
    } else {
      if (e.key === 's' || e.key === 'S') { e.preventDefault(); storeModal.open(); }
      else if (e.key === 'Tab') { e.preventDefault(); storeModal.open(); }
      else if (e.key === 'a' || e.key === 'A') { e.preventDefault(); activityModal.open(); }
    }
  });
}
