// DATA/STATE layer — the WebSocket client (ui-spec.md §6): connect,
// backoff/reconnect, sendAction, and the STORE_OPEN re-assert. This module
// knows the wire contract (wire.ts) — that IS the data layer's job — but
// nothing about modals or the DOM: it never touches an element and never
// imports a feature module. A feature that wants its STORE_OPEN hold kept
// alive across a dropped connection or a stray server snapshot calls
// setStoreOpenHoldDesired(true) when it opens and (false) when it closes;
// this client is the one place that watches for the hold slipping and
// re-asserts it, without knowing *why* anything wants the hold.
import { DEV_MODE } from '../env';
import type { CatalogMessage, ClientAction, FlashMessage, ServerMessage, SessionCompleteMessage, StateMessage } from '../wire';

export interface WsClientHandlers {
  onCatalog(msg: CatalogMessage): void;
  onState(msg: StateMessage): void;
  onFlash(msg: FlashMessage): void;
  // Phase P2 (docs/plan/P2-design.md §3.1, §8's GO-3 task; docs/ui-spec.md
  // §9.6): routed here, straight off the wire's `sessionComplete` message
  // — main.ts's own onSessionComplete(msg.session) is the one place that
  // knows what to do with it (open the summary card). This module stays
  // wire-shape-only, same as every other handler here.
  onSessionComplete(msg: SessionCompleteMessage): void;
  // `stale` is docs/plan/BUGS-RESILIENCE.md R9's honest half: after
  // STALE_AFTER_ATTEMPTS consecutive failed connects, this client stops
  // implying that waiting will fix it and says the runtime is not there.
  // It never stops retrying — with the runtime's sticky port, a
  // supervisor's restart usually comes back on THIS port and the page
  // reconnects on its own — but a page whose runtime really has moved or
  // stopped has to say so instead of showing RECONNECTING... forever.
  onConnecting(reconnecting: boolean, stale: boolean): void;
}

// STALE_AFTER_ATTEMPTS is how many consecutive failed connects it takes
// before the overlay stops saying RECONNECTING... (R9). With the 500 ms
// -> 8 s capped backoff below, six failures is ~23 s of a dead socket —
// long enough that an ordinary server blip, a laptop lid, or a
// supervisor's `RestartSec=5` restart has passed without ever showing
// the harsher message, and short enough that a genuinely dead window
// does not lie for minutes.
const STALE_AFTER_ATTEMPTS = 6;

let handlers: WsClientHandlers | null = null;
let ws: WebSocket | null = null;
let attempt = 0;
// consecutiveFailures counts connects that never opened, and is reset by
// `open`. Deliberately NOT `attempt`, which counts connects for the
// CONNECTING/RECONNECTING distinction and must keep counting across a
// successful connection for that wording to stay right.
let consecutiveFailures = 0;
let reconnectDelay = 500;
let reconnectTimer: ReturnType<typeof setTimeout> | undefined;

// B2 loop guard: true once we've re-sent STORE_OPEN in response to a
// `state` snapshot showing storeOpen:false while a hold is still desired,
// until the server confirms the gate is open again (or the hold is
// released). Without this, EVERY subsequent `state` frame that still
// shows storeOpen:false (e.g. while our re-assertion is in flight, or a
// stalled ws.send that never reaches the server) would trigger another
// STORE_OPEN send, once per ~1s tick, forever.
let storeOpenHoldDesired = false;
let storeReassertSent = false;

export function setStoreOpenHoldDesired(desired: boolean): void {
  storeOpenHoldDesired = desired;
  if (!desired) storeReassertSent = false;
}

export function sendAction(action: ClientAction): void {
  if (DEV_MODE) {
    // No backend to talk to in dev mode. Log so a human/orchestrator can
    // see intent; drive actual state changes via window.devApply instead.
    console.log('[dev mode] would send:', action);
    return;
  }
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(action));
  }
}

function handleServerMessage(msg: ServerMessage | null | undefined): void {
  if (!msg || !msg.type || !handlers) return;
  switch (msg.type) {
    case 'catalog':
      handlers.onCatalog(msg);
      break;
    case 'state':
      handlers.onState(msg);
      // B2 work-gate: the server is the source of truth for storeOpen
      // (docs/ui-spec.md §5.3's earning gate), but the server's flag is
      // now scoped per-connection (a refcounted set of connIDs holding
      // it open, not one global bool). If a hold is still desired but a
      // state snapshot says storeOpen is false — most likely because our
      // OWN hold hasn't landed yet (e.g. this snapshot arrived from a
      // fresh reconnect's initial send, before our re-asserted
      // STORE_OPEN below was applied) — re-assert it ONCE
      // (storeReassertSent guards against re-sending on every subsequent
      // tick that still shows storeOpen:false) so a held-open gate can
      // never silently drift apart from the server's flag, without
      // looping.
      if (storeOpenHoldDesired) {
        if (msg.storeOpen === false) {
          if (!storeReassertSent) {
            storeReassertSent = true;
            sendAction({ action: 'STORE_OPEN' });
          }
        } else {
          storeReassertSent = false; // gate confirmed open again
        }
      }
      break;
    case 'flash':
      handlers.onFlash(msg);
      break;
    case 'sessionComplete':
      handlers.onSessionComplete(msg);
      break;
    default:
      break;
  }
}

function connect(): void {
  if (!handlers) return;
  handlers.onConnecting(attempt > 0, consecutiveFailures >= STALE_AFTER_ATTEMPTS);
  attempt++;
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  let sock: WebSocket;
  try {
    sock = new WebSocket(proto + '//' + location.host + '/ws');
  } catch (e) {
    scheduleReconnect();
    return;
  }
  ws = sock;
  sock.addEventListener('open', function () {
    reconnectDelay = 500;
    consecutiveFailures = 0;
    // B2: a reconnect (server restart/blip, or this tab's own network
    // hiccup) gets a brand-new connID server-side — the OLD connID's
    // store-open hold was already released on disconnect
    // (handlers.go's defer), so if a hold is still desired locally, it
    // must be re-asserted under the new connection or the work/Dev Cash
    // gate silently comes back "closed" despite the caller still wanting
    // it held.
    if (storeOpenHoldDesired) {
      sendAction({ action: 'STORE_OPEN' });
    }
  });
  sock.addEventListener('message', function (ev: MessageEvent) {
    try { handleServerMessage(JSON.parse(ev.data) as ServerMessage); }
    catch (err) { console.error('bad ws message', err); }
  });
  sock.addEventListener('close', function () { scheduleReconnect(); });
  sock.addEventListener('error', function () { /* 'close' follows */ });
}
function scheduleReconnect(): void {
  consecutiveFailures++;
  handlers?.onConnecting(true, consecutiveFailures >= STALE_AFTER_ATTEMPTS);
  clearTimeout(reconnectTimer);
  reconnectTimer = setTimeout(connect, reconnectDelay);
  reconnectDelay = Math.min(reconnectDelay * 2, 8000);
}

export function connectWsClient(h: WsClientHandlers): void {
  handlers = h;
  connect();
}
