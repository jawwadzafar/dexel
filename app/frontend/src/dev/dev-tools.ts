// Dev-mode boot harness (`?dev=1`). Seeds the central store with the
// hardcoded fixtures instead of connecting over WS, and installs
// window.devApply — a verification hook so an external orchestrator can
// drive specific states for a screenshot without a running backend. Only
// installed in dev mode.
import * as store from '../state/store';
import { hideConnOverlay } from '../render/overlays';
import { DEV_CATALOG, DEV_STATE, DEV_STATE_ONBOARDING, DEV_STATE_PAUSED } from './dev-fixtures';
import type { Equipped, StateMessage } from '../wire';

declare global {
  interface Window {
    devApply?: (partialState: Partial<StateMessage> & { equipped?: Partial<Equipped> }) => void;
    devCatalog?: typeof DEV_CATALOG;
    // Phase P1: the fresh-install fixture, exposed so a harness can render
    // the onboarding modal with one call — window.devApply(window.devStateOnboarding).
    devStateOnboarding?: typeof DEV_STATE_ONBOARDING;
    // PR-5 (dev_docs/production-runtime/MIGRATION_PLAN.md §PR-5): the
    // paused fixture, exposed so a harness can render the PAUSED chrome
    // with one call — window.devApply(window.devStatePaused).
    devStatePaused?: typeof DEV_STATE_PAUSED;
  }
}

export function installDevTools(renderAll: () => void): void {
  document.title = 'Dexel [DEV MODE]';
  store.setCatalog(DEV_CATALOG);
  store.setState(DEV_STATE);
  hideConnOverlay();
  renderAll();

  // Validates partialState.equipped.*.itemId/tintId against the loaded
  // catalog before merging: an unknown item id gets dropped (console.warn
  // says which one and for which slot) so it can't overwrite a
  // previously-valid slot with a dangling reference, and an unknown tint
  // id is cleared to null rather than left pointing at nothing. Render
  // time (equippedItemFor / tintHexFor in state/store.ts) already
  // degrades gracefully either way — this is defense in depth plus a
  // paper trail for whoever is driving the harness.
  window.devApply = function (partialState) {
    const incoming = partialState || {};
    if (incoming.equipped) {
      Object.keys(incoming.equipped).forEach(function (slotId) {
        const eq = incoming.equipped![slotId];
        if (!eq) return;
        if (eq.itemId && !store.getItemById(eq.itemId)) {
          console.warn('[devApply] dropping unknown item id "' + eq.itemId + '" for slot "' + slotId + '" (not in loaded catalog)');
          delete incoming.equipped![slotId];
          return;
        }
        if (eq.tintId && !store.getTintById(eq.tintId)) {
          console.warn('[devApply] clearing unknown tint id "' + eq.tintId + '" for slot "' + slotId + '" (not in loaded catalog)');
          eq.tintId = null;
        }
      });
    }
    store.setState(Object.assign({}, store.getState(), incoming) as StateMessage);
    renderAll();
  };
  window.devCatalog = DEV_CATALOG;
  window.devStateOnboarding = DEV_STATE_ONBOARDING;
  window.devStatePaused = DEV_STATE_PAUSED;
}
