// Dexel frontend — thin entry point (F2, docs/plan/ROADMAP.md
// "Frontend architecture track"). Wires the three layers together and
// nothing else:
//   - DATA/STATE:   state/store.ts (central typed state) + state/ws-client.ts
//                   (connect/backoff/reconnect + sendAction + the STORE_OPEN
//                   re-assert)
//   - RENDER:       render/scene.ts, render/terminal.ts, render/chrome.ts,
//                   render/overlays.ts, render/flash.ts, render/tint.ts —
//                   given the current store state, update the DOM each owns;
//                   none of them send a ClientAction.
//   - FEATURE/LOGIC: features/store-modal.ts, features/activity-modal.ts,
//                   features/history-modal.ts, features/onboarding-modal.ts
//                   (Phase P1's first-launch identity modal),
//                   features/sessions-modal.ts (Phase P2's Sessions
//                   modal + session-complete card),
//                   features/menu.ts (the title-bar hamburger menu),
//                   features/keybindings.ts —
//                   each owns its own DOM/UI state, reads the store, and
//                   is the only place that sends ClientActions for that
//                   feature.
// The typed wire contract (wire.ts) is shared by every layer. See
// app/frontend/README.md for the full module map.
import { DEV_MODE } from './env';
import * as store from './state/store';
import { connectWsClient } from './state/ws-client';
import { renderChrome } from './render/chrome';
import { renderTerminal } from './render/terminal';
import { onCelebrate, renderScene } from './render/scene';
import { hideConnOverlay, showConnOverlay } from './render/overlays';
import { initViewport } from './render/viewport';
import { showFlash } from './render/flash';
import * as storeModal from './features/store-modal';
import * as activityModal from './features/activity-modal';
import * as historyModal from './features/history-modal';
import * as onboardingModal from './features/onboarding-modal';
import * as sessionsModal from './features/sessions-modal';
// Wires #menu-open/#menu-panel as a side effect on import. PR-5
// (docs/production-runtime/MIGRATION_PLAN.md §PR-5) gave it one piece
// of store-derived state to render — the pause/resume label — via the
// named renderPauseLabel() export called from renderAll() below.
import * as menu from './features/menu';
import * as keybindings from './features/keybindings';
import { installDevTools } from './dev/dev-tools';
import { DEV_SESSION_COMPLETE_SAMPLE, DEV_STATE_NO_SESSION } from './dev/dev-fixtures';
import type { SessionView, StateMessage } from './wire';

function renderAll(): void {
  if (!store.getState()) return;
  renderChrome();
  menu.renderPauseLabel();
  renderTerminal();
  renderScene();
  storeModal.refreshIfOpen();
  activityModal.refreshIfOpen();
  historyModal.refreshIfOpen();
  sessionsModal.refreshIfOpen();
  // Phase P1: the onboarding modal is opened and closed by the server's
  // `onboarding` flag alone (no button, no key), so its open/close
  // decision rides the same per-state render pass as everything else.
  onboardingModal.refreshIfOpen();
  onboardingModal.syncWithServer();
}

// Phase P2 (docs/plan/P2-design.md §3.3) — the celebration beat's single
// client-side seam. `sessionComplete` always arrives right after the
// `state` broadcast that cleared the session, so
// sessionsModal.showSummary reads the just-refreshed store for the
// "N THIS WEEK" line rather than needing a second copy of it on this
// message. The gold `flash{kind:"session"}` toast is a SEPARATE message
// the server sends alongside it (routed through the ordinary onFlash
// handler below — no code here composes that text, per ui-spec.md §6.1's
// "zero client-side assembly" rule).
//
// PHASE P3 — the hook §3.3 named is now real: render/scene.ts's
// onCelebrate() plays a ~1.4s two-frame bounce on the character. This is
// the ONLY place a session celebration is triggered, and it is reached
// only from a `sessionComplete` message, which the server sends only for a
// session it actually kept (a sub-60s session is discarded and never
// produces one — app/main.go's "Fork P2-E") — so the body language can
// never celebrate something that did not happen (ADR 0010).
function onSessionComplete(session: SessionView): void {
  sessionsModal.showSummary(session);
  onCelebrate('session');
}

// BUG-2 — fit the fixed-pixel layout to the real window before anything
// renders into it, so the first painted frame is already at the right scale
// (see render/viewport.ts for the scaling contract). Independent of DEV_MODE:
// a ?dev=1 harness screenshots the same scaled page a player sees.
initViewport();

keybindings.init();

if (DEV_MODE) {
  installDevTools(renderAll);
  // ?dev=1 fixture hook (P2-design.md §6.5.3) — fires a synthetic
  // sessionComplete locally so the summary card can be visually gated
  // without waiting for a real session or a running backend. Declared
  // here (not dev/dev-tools.ts, which this task does not own) since
  // main.ts is the one place that already knows how to answer a
  // sessionComplete: window.devSessionComplete().
  window.devSessionComplete = function (entry?: SessionView) {
    onSessionComplete(entry || DEV_SESSION_COMPLETE_SAMPLE);
  };
  // The idle-view fixture (§6.5 item 2) — window.devApply(window.devStateNoSession)
  // renders the Sessions modal's clean empty state without a backend.
  window.devStateNoSession = DEV_STATE_NO_SESSION;
} else {
  connectWsClient({
    onCatalog(msg) {
      store.setCatalog(msg);
      storeModal.onCatalogChanged();
    },
    onState(msg) {
      store.setState(msg);
      hideConnOverlay();
      renderAll();
    },
    onFlash(msg) {
      showFlash(msg);
      // PHASE P3 — the second honest celebration trigger. `kind:"sprint"`
      // is broadcast by app/main.go from exactly one place: the tick loop,
      // when Game.Tick reports a sprint actually completed. It is a
      // read-only signal already on the wire, so wiring the character's
      // bounce to it adds no field and asserts nothing the server did not
      // say. Deliberately NOT `kind:"session"` — that kind is also used
      // for "Session started." and the too-short-to-keep notice, so it is
      // not an event; the kept-session celebration rides the
      // `sessionComplete` message above instead.
      if (msg.kind === 'sprint') onCelebrate('sprint');
    },
    onSessionComplete(msg) {
      // GO-3 (docs/plan/P2-design.md §8): ws-client.ts now routes a real
      // server's `sessionComplete` message straight here — the exact
      // function the ?dev=1 fixture (window.devSessionComplete) above
      // already exercises.
      onSessionComplete(msg.session);
    },
    onConnecting(reconnecting) {
      showConnOverlay(reconnecting);
    }
  });
}

declare global {
  interface Window {
    // See the DEV_MODE branch above. Omit `entry` to use the bundled
    // sample fixture (dev/dev-fixtures.ts's DEV_SESSION_COMPLETE_SAMPLE).
    devSessionComplete?: (entry?: SessionView) => void;
    // The idle-view fixture — window.devApply(window.devStateNoSession).
    devStateNoSession?: Partial<StateMessage>;
  }
}
