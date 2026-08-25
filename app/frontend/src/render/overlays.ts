// RENDER layer — connection status overlay + the assets-missing banner.
// Both are pure "given a status, toggle a DOM node this module owns"; the
// one side effect (a diagnostic GET /api/health) exists only to put a
// helpful path in the banner's text, never to change any game state.
import { byId } from '../dom';
import { DEV_MODE } from '../env';

const connOverlay = byId<HTMLDivElement>('conn-overlay');
const assetsErrorOverlay = byId<HTMLDivElement>('assets-error-overlay');

// `stale` (docs/plan/BUGS-RESILIENCE.md R9) is set by the WS client after
// enough consecutive failed connects that "RECONNECTING..." has stopped
// being true: the runtime this page was loaded from is not answering on
// this port any more. The page keeps retrying underneath — the runtime's
// port is sticky, so a supervised crash-restart usually returns to THIS
// port and the overlay clears itself — so this message names the one
// action that fixes the case where it does not, instead of implying that
// more waiting will.
export function showConnOverlay(reconnecting: boolean, stale?: boolean): void {
  const label = stale ? 'CONNECTION LOST - RUN `dexel open`' : (reconnecting ? 'RECONNECTING...' : 'CONNECTING...');
  (connOverlay.querySelector('span') as HTMLElement).textContent = label;
  connOverlay.classList.add('visible');
}
export function hideConnOverlay(): void { connOverlay.classList.remove('visible'); }

let assetsErrorShown = false;
function showAssetsErrorBanner(detail?: string): void {
  if (assetsErrorShown) return; // one banner is enough; don't refetch per failed sprite
  assetsErrorShown = true;
  let msg = 'ASSETS NOT FOUND — the server could not locate the assets/ directory. ' +
    'Run from the repo, or set DEXEL_ASSETS_DIR.';
  if (detail) msg += ' (' + detail + ')';
  (assetsErrorOverlay.querySelector('span') as HTMLElement).textContent = msg;
  assetsErrorOverlay.classList.add('visible');
}

// room_back.png is internal/assets' own "does assets/ even exist"
// sentinel (see locate.go) — the render layer's scene compositor
// (./scene.ts) attaches this as that sprite's `error` listener: if this
// one 404s, every sprite in the scene is 404ing, and the player would
// otherwise just see a blank room with no explanation.
export function handleSpriteSentinelError(): void {
  if (DEV_MODE) return; // no backend / no /api/health to ask in dev mode
  fetch('/api/health').then(function (r) { return r.json(); }).then(function (h) {
    showAssetsErrorBanner('server assetsDir: ' + (h && h.assetsDir ? h.assetsDir : 'null'));
  }).catch(function () {
    showAssetsErrorBanner('/api/health unreachable');
  });
}
