// RENDER layer — titlebar / sprint panel / status panel / ticker. Given
// the current state, updates the DOM it owns; no business logic.
import { byId } from '../dom';
import * as store from '../state/store';
import { fmtCount, fmtInt, truncate } from '../format';
import { MOOD_COLOR } from '../geometry';

const hudLevel = byId('hud-level');
const hudCash = byId('hud-cash').querySelector('.value') as HTMLElement;
const sprintName = byId('sprint-name').querySelector('.value') as HTMLElement;
const sprintBar = byId<HTMLProgressElement>('sprint-bar');
const sprintUnits = byId('sprint-units');
const statusDot = byId('status-dot');
const statusLine = byId('status-line');
const ticker = byId<HTMLUListElement>('ticker');
// The dexel's name is no longer echoed as a bare label here. It now lives
// INSIDE the status line as a personal sentence ("Pixel is coding in VS
// Code"), composed on the SERVER and rendered verbatim below as
// state.activityLine (docs/ui-spec.md §7.4). The old always-on
// #status-name strip and the name-swapped #menu-panel-title were redundant
// once the name became meaningful in that sentence, so both were removed:
// #menu-panel-title is static "MENU" again, and #status-name is gone.
// Phase P2 (docs/ui-spec.md §9.5) — the always-visible session indicator.
const sessionPill = byId('session-pill');
const sessionPillText = byId('session-pill-text');
// PR-5 (docs/production-runtime/MIGRATION_PLAN.md §PR-5) — the
// always-visible paused badge, same idiom as #session-pill above.
const pausedBadge = byId('paused-badge');

export function renderChrome(): void {
  const state = store.getState();
  if (!state) return;
  // The title bar no longer shows a mood dot (BUG-2) — mood is still
  // conveyed via #status-dot in the bottom status panel.
  //
  // PR-5: while paused, the dot goes dimmed/muted (var(--screen-dim),
  // the same "dim" half of the existing screen/screen-dim pair the rest
  // of this palette already uses for bright-vs-muted, e.g.
  // .nes-btn.is-disabled) rather than showing whatever mood colour idle
  // would otherwise pick — pausedness is a distinct visual state from
  // idle, even though `activeState` itself stays 'idle' (ADR 0010: no
  // fourth mood string).
  const moodColor = state.paused ? 'var(--screen-dim)' : (MOOD_COLOR[state.activeState] || MOOD_COLOR.idle);
  statusDot.style.background = moodColor;
  hudLevel.textContent = 'LV ' + fmtInt(state.level);
  // DEV CASH is the one HUD balance the owner said "could go to a million"
  // (§14): compact it with fmtCount ("1.2M") so a large balance never
  // overflows the fixed HUD box. Store PRICES and coin deltas elsewhere
  // stay EXACT (fmtInt) — you spend an exact number, so those never compact.
  hudCash.textContent = fmtCount(state.devCash);

  sprintName.textContent = truncate(state.sprint.name, 28);
  sprintBar.max = state.sprint.target;
  sprintBar.value = state.sprint.progress;
  // unitLabel falls back to "units" — a state missing that field (a
  // stale server, or a devApply payload that replaced sprint wholesale
  // without it) must never render the literal string "undefined".
  sprintUnits.textContent = fmtInt(state.sprint.progress) + ' / ' + fmtInt(state.sprint.target) + ' ' + (state.sprint.unitLabel || 'units');

  // PR-5: while paused, the status line is a fixed CLIENT-SIDE string —
  // never the server's `activityLine`, which would otherwise still read
  // as though tracking were live. This is the one place this module
  // composes its own text rather than rendering the server's verbatim,
  // and it is safe specifically because it asserts nothing the server
  // did not say: `state.paused` is the server's own signal, and the
  // string itself carries no observed content.
  statusLine.textContent = state.paused ? 'PAUSED — tracking is off' : truncate(state.activityLine, 34);

  const lis = ticker.querySelectorAll('li');
  for (let i = 0; i < lis.length; i++) {
    const raw = (state.tickerLines || [])[i] || '';
    lis[i].textContent = raw ? truncate('> ' + raw, 36) : '';
  }

  // Phase P2 (docs/ui-spec.md §9.5): the pill is EMPTY, and therefore
  // invisible, whenever no session is active — a pre-P2 server that
  // omits `sessions` entirely degrades the same way (state.sessions is
  // optional on the wire). The name is truncated to 16 chars with '…'
  // (the pill's own 16-char budget); the clock
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

  // PR-5: the badge is visible only while state.paused is true — same
  // .visible-class toggle idiom as #session-pill just above. A session
  // can be active AND paused simultaneously (sessions survive pause,
  // pausedSeconds accrues instead), so this never suppresses the pill.
  pausedBadge.classList.toggle('visible', !!state.paused);
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
