// RENDER layer — titlebar / sprint panel / status panel / ticker. Given
// the current state, updates the DOM it owns; no business logic.
import { byId } from '../dom';
import * as store from '../state/store';
import { fmtInt, truncate } from '../format';
import { MOOD_COLOR } from '../geometry';

const hudLevel = byId('hud-level');
const hudCash = byId('hud-cash').querySelector('.value') as HTMLElement;
const sprintName = byId('sprint-name').querySelector('.value') as HTMLElement;
const sprintBar = byId<HTMLProgressElement>('sprint-bar');
const sprintUnits = byId('sprint-units');
const statusDot = byId('status-dot');
const statusLine = byId('status-line');
const ticker = byId<HTMLUListElement>('ticker');
// Phase P1 name echo (docs/ui-spec.md §7.4). Two places, both here
// because this module owns the titlebar and the status panel:
//   - #status-name, the always-visible one, in the empty strip below the
//     ticker;
//   - #menu-panel-title, the hamburger panel's heading, which reads
//     "MENU" until the dexel has a name and the dexel's name after that.
// Deliberately NOT the titlebar's top-left cluster: that stays
// coin-then-level and nothing else, by owner directive.
const statusName = byId('status-name');
const menuPanelTitle = byId('menu-panel-title');
// Phase P2 (docs/ui-spec.md §9.5) — the always-visible session indicator.
const sessionPill = byId('session-pill');
const sessionPillText = byId('session-pill-text');

export function renderChrome(): void {
  const state = store.getState();
  if (!state) return;
  // The title bar no longer shows a mood dot (BUG-2) — mood is still
  // conveyed via #status-dot in the bottom status panel.
  const moodColor = MOOD_COLOR[state.activeState] || MOOD_COLOR.idle;
  statusDot.style.background = moodColor;
  hudLevel.textContent = 'LV ' + fmtInt(state.level);
  hudCash.textContent = fmtInt(state.devCash);

  sprintName.textContent = truncate(state.sprint.name, 28);
  sprintBar.max = state.sprint.target;
  sprintBar.value = state.sprint.progress;
  // unitLabel falls back to "units" — a state missing that field (a
  // stale server, or a devApply payload that replaced sprint wholesale
  // without it) must never render the literal string "undefined".
  sprintUnits.textContent = fmtInt(state.sprint.progress) + ' / ' + fmtInt(state.sprint.target) + ' ' + (state.sprint.unitLabel || 'units');

  statusLine.textContent = truncate(state.activityLine, 34);

  // The name is USER text, so it is rendered as typed — never upper-cased
  // to match the surrounding labels, and never assembled into a sentence
  // (ui-spec.md §3's "zero client-side assembly" rule; the one composed
  // string, the welcome toast, is composed by the SERVER). Empty until
  // named, which renders as nothing at all rather than a placeholder.
  const dexelName = (state.config && state.config.name) || '';
  statusName.textContent = truncate(dexelName, 24);
  menuPanelTitle.textContent = dexelName ? truncate(dexelName, 16) : 'MENU';

  const lis = ticker.querySelectorAll('li');
  for (let i = 0; i < lis.length; i++) {
    const raw = (state.tickerLines || [])[i] || '';
    lis[i].textContent = raw ? truncate('> ' + raw, 36) : '';
  }

  // Phase P2 (docs/ui-spec.md §9.5): the pill is EMPTY, and therefore
  // invisible, whenever no session is active — a pre-P2 server that
  // omits `sessions` entirely degrades the same way (state.sessions is
  // optional on the wire). The name is truncated to 16 chars with '…'
  // (the pill's own budget, independent of #status-name's 24); the clock
  // is server-computed elapsedSeconds, never derived client-side.
  const active = state.sessions && state.sessions.active;
  if (active) {
    const name = active.name ? truncate(active.name, 16) + ' ' : '';
    sessionPillText.textContent = name + fmtClock(active.elapsedSeconds);
    sessionPill.classList.add('visible');
  } else {
    sessionPillText.textContent = '';
    sessionPill.classList.remove('visible');
  }
}

// fmtClock renders a whole-seconds duration as "H:MM:SS" for the
// titlebar pill (docs/ui-spec.md §9.5) — deliberately NOT format.ts's
// fmtDuration ("Xm Ys" prose, used by the Sessions modal's own longer-form
// numbers): a live titlebar clock reads better as a clock face. Hours are
// unpadded (a session can run up to the 16h cap); minutes/seconds are
// always two digits.
function fmtClock(totalSeconds: number | undefined): string {
  const s = Math.max(0, Math.floor(Number(totalSeconds) || 0));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const pad = (n: number) => String(n).padStart(2, '0');
  return h + ':' + pad(m) + ':' + pad(sec);
}
