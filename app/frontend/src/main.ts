// dexel frontend — thin entry point (F2, docs/plan/ROADMAP.md
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
//                   features/history-modal.ts, features/keybindings.ts —
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
import { renderScene } from './render/scene';
import { hideConnOverlay, showConnOverlay } from './render/overlays';
import { showFlash } from './render/flash';
import * as storeModal from './features/store-modal';
import * as activityModal from './features/activity-modal';
import * as historyModal from './features/history-modal';
import * as keybindings from './features/keybindings';
import { installDevTools } from './dev/dev-tools';

function renderAll(): void {
  if (!store.getState()) return;
  renderChrome();
  renderTerminal();
  renderScene();
  storeModal.refreshIfOpen();
  activityModal.refreshIfOpen();
  historyModal.refreshIfOpen();
}

keybindings.init();

if (DEV_MODE) {
  installDevTools(renderAll);
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
    },
    onConnecting(reconnecting) {
      showConnOverlay(reconnecting);
    }
  });
}
