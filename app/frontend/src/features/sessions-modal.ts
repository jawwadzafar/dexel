// FEATURE/LOGIC layer — the Sessions modal (Phase P2, "Sessions & the
// session-complete moment", docs/plan/P2-design.md §6.3). Owns every DOM
// node under #sessions and is the only module that sends
// SESSION_START/SESSION_STOP.
//
// Three views live in one <dialog>, switched by which one the server's
// state makes true — never by client-held mode:
//   - idle    (sessions.active == null): the name input + START, the
//              recent list, the summary line.
//   - live    (sessions.active != null): elapsed + effort-so-far,
//              updating on every 1s `state`; one action button that
//              reads END, or CANCEL while elapsedSeconds < 60 (the label
//              flips on a SERVER-sent number — the store modal's
//              one-action-button precedence idea, applied).
//   - summary (the SESSION COMPLETE card, §3.2): a temporary overlay
//              shown by showSummary(), dismissed by NICE/Esc back to the
//              live game view. `showingSummary` is the one bit of
//              client-held UI state this module keeps — it selects which
//              of the three panes to paint, exactly the way
//              onboarding-modal.ts's `selectedTint` is client-held UI
//              state, never a fact about the world the server didn't
//              also send.
//
// Gates nothing (P2-design.md §2.4/§6.3): no SESSIONS_MODAL_OPEN action,
// no ws-client hold. Same as the Activity/History modals.
//
// State is PULLED from state/store.ts, never pushed — this module has no
// setter of its own. `elapsedSeconds` is never derived locally: the
// server sends it fresh on every ~1s `state` broadcast, and
// refreshIfOpen() (called from main.ts's renderAll()) just re-paints
// whatever the store already holds.
import { byId } from '../dom';
import { enableClickAwayDismiss } from './modal-dismiss';
import * as store from '../state/store';
import { sendAction } from '../state/ws-client';
import { fmtCount, fmtDuration, fmtInt, truncate } from '../format';
import type { ActiveSessionView, SessionsView, SessionView } from '../wire';

const el = {
  dialog: byId<HTMLDialogElement>('sessions'),
  openBtn: byId<HTMLButtonElement>('sessions-open'),
  close: byId<HTMLButtonElement>('sessions-close'),
  scrim: byId<HTMLDivElement>('scrim'),

  idleView: byId<HTMLDivElement>('sessions-idle'),
  nameInput: byId<HTMLInputElement>('sessions-name'),
  startBtn: byId<HTMLButtonElement>('sessions-start'),
  summaryLine: byId<HTMLDivElement>('sessions-summary-line'),
  recentList: byId<HTMLDivElement>('sessions-recent-list'),
  recentEmpty: byId<HTMLDivElement>('sessions-recent-empty'),

  liveView: byId<HTMLDivElement>('sessions-live'),
  liveName: byId('sessions-live-name'),
  liveElapsed: byId('sessions-live-elapsed'),
  liveKeys: byId('sessions-live-keys'),
  liveFocus: byId('sessions-live-focus'),
  liveSprints: byId('sessions-live-sprints'),
  liveCoins: byId('sessions-live-coins'),
  endBtn: byId<HTMLButtonElement>('sessions-end'),

  cardView: byId<HTMLDivElement>('sessions-summary'),
  cardName: byId('sessions-card-name'),
  cardDuration: byId('sessions-card-duration'),
  cardKeys: byId('sessions-card-keys'),
  cardFocus: byId('sessions-card-focus'),
  cardSprints: byId('sessions-card-sprints'),
  cardCoins: byId('sessions-card-coins'),
  cardMeta: byId('sessions-card-meta'),
  niceBtn: byId<HTMLButtonElement>('sessions-nice')
};

// The one bit of client-held UI state (see header note). Cleared on every
// close so a reopened modal always starts from the server's own idle/live
// truth.
let showingSummary = false;

// Below this, the live view's one action button reads CANCEL instead of
// END (P2-design.md §6.3) — mirrors game.SessionMinDurationSeconds (§2.6).
// Duplicated here deliberately: it only decides a BUTTON LABEL, never
// whether the record is kept (that is the server's call on SESSION_STOP).
const SESSION_MIN_DURATION_SECONDS = 60;

export function isOpen(): boolean { return el.dialog.open; }

function showView(name: 'idle' | 'live' | 'summary'): void {
  el.idleView.classList.toggle('active', name === 'idle');
  el.liveView.classList.toggle('active', name === 'live');
  el.cardView.classList.toggle('active', name === 'summary');
}

// ---------------------------------------------------------------------
// formatting — durations go through format.ts's shared fmtDuration (which
// now rolls up hours/days/years, §14), so a session's "1h 24m" and the
// recent list's durations humanize the same way the Activity/History
// modals do. The one local formatter left is fmtBestFocus, which keeps
// this card's deliberate minute-resolution phrasing (§14 / P2 §3.2).
// ---------------------------------------------------------------------
// "BEST 14m" — the personal-best focus block at MINUTE resolution (the
// card's cozy one-line phrasing has no room for seconds), but rolling up
// to "1h 20m" past an hour so a long block never reads as a bare "80m".
// Floors the minute (never rounds up), matching fmtDuration's honesty rule.
function fmtBestFocus(totalSeconds: number | undefined): string {
  const s = Math.max(0, Math.floor(Number(totalSeconds) || 0));
  if (s < 3600) return Math.floor(s / 60) + 'm';
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return m > 0 ? h + 'h ' + m + 'm' : h + 'h';
}
function focusLine(focusSessions: number, longestFocusBlockSeconds: number): string {
  return fmtCount(focusSessions) + ' blocks   BEST ' + fmtBestFocus(longestFocusBlockSeconds);
}
// RFC3339 -> "MM/DD", ASCII-only (the A2/history-modal lesson on glyphs
// this pixel font doesn't have). A real timestamp (unlike history's
// date-only strings) parses correctly with the standard Date
// constructor — no local-date-shift hazard to guard against here.
function shortDateFromIso(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '--/--';
  const mm = String(d.getMonth() + 1).padStart(2, '0');
  const dd = String(d.getDate()).padStart(2, '0');
  return mm + '/' + dd;
}

// ---------------------------------------------------------------------
// idle view
// ---------------------------------------------------------------------
function buildRecentList(recent: SessionView[]): void {
  el.recentList.replaceChildren();
  el.recentList.style.display = recent.length > 0 ? '' : 'none';
  el.recentEmpty.style.display = recent.length > 0 ? 'none' : '';
  recent.forEach(function (s) {
    const row = document.createElement('div');
    row.className = 'sessions-recent-row';
    const name = s.name ? truncate(s.name, 20) : 'unnamed';
    row.textContent = shortDateFromIso(s.endedAt) + '  ' + name + '  ' +
      fmtDuration(s.durationSeconds) + '  ' + fmtCount(s.keystrokes) + ' keys  ' +
      fmtCount(s.focusSessions) + ' blocks';
    row.title = 'Click to re-open this session\'s summary';
    row.addEventListener('click', function () { showSummary(s); });
    el.recentList.appendChild(row);
  });
}

function renderIdleView(sessions: SessionsView | null): void {
  const summary = sessions ? sessions.summary : { completed: 0, thisWeek: 0, longestSessionSeconds: 0 };
  el.summaryLine.textContent = fmtCount(summary.completed) + ' completed  ·  ' +
    fmtCount(summary.thisWeek) + ' this week  ·  longest ' + fmtDuration(summary.longestSessionSeconds);
  buildRecentList(sessions ? sessions.recent : []);
}

// ---------------------------------------------------------------------
// live view
// ---------------------------------------------------------------------
function renderLiveView(a: ActiveSessionView): void {
  el.liveName.textContent = a.name ? truncate(a.name, 40) : 'Unnamed session';
  el.liveElapsed.textContent = fmtDuration(a.elapsedSeconds);
  el.liveKeys.textContent = fmtCount(a.keystrokes);
  el.liveFocus.textContent = focusLine(a.focusSessions, a.longestFocusBlockSeconds);
  el.liveSprints.textContent = fmtCount(a.sprintsCompleted);
  // Coins EARNED stays EXACT (fmtInt): it is a small delta the user reasons
  // about precisely, not a magnitude to compact (§14).
  el.liveCoins.textContent = '+' + fmtInt(a.coinsEarned);
  el.endBtn.textContent = a.elapsedSeconds < SESSION_MIN_DURATION_SECONDS ? 'CANCEL SESSION' : 'END SESSION';
}

// ---------------------------------------------------------------------
// the summary card (§3.2) — every number comes from `s` verbatim; the
// meta line ("SESSION N · M THIS WEEK") reads the CURRENT store summary
// (already refreshed by the `state` broadcast that precedes
// sessionComplete on the wire), falling back to the session's own id/0
// when no summary has arrived yet (e.g. a bare devSessionComplete call).
// ---------------------------------------------------------------------
function renderCard(s: SessionView): void {
  if (s.name) {
    el.cardName.textContent = truncate(s.name, 40);
    el.cardName.classList.remove('hidden');
  } else {
    el.cardName.textContent = '';
    el.cardName.classList.add('hidden');
  }
  el.cardDuration.textContent = fmtDuration(s.durationSeconds);
  el.cardKeys.textContent = fmtCount(s.keystrokes);
  el.cardFocus.textContent = focusLine(s.focusSessions, s.longestFocusBlockSeconds);
  el.cardSprints.textContent = fmtCount(s.sprintsCompleted);
  // Coins EARNED stays EXACT — see renderLiveView (§14).
  el.cardCoins.textContent = '+' + fmtInt(s.coinsEarned) + ' earned during it';

  const state = store.getState();
  const summary = state && state.sessions ? state.sessions.summary : null;
  const completed = summary ? summary.completed : s.id;
  const thisWeek = summary ? summary.thisWeek : 0;
  // "SESSION N" is an ORDINAL identifying THIS session — kept EXACT
  // (fmtInt) so the user can tell exactly which session it was, the
  // count-vs-identifier distinction §14 draws (parallel to money's
  // count-vs-price split). "THIS WEEK" is a small stat -> fmtCount.
  el.cardMeta.textContent = 'SESSION ' + fmtInt(completed) + '  ·  ' + fmtCount(thisWeek) + ' THIS WEEK';
}

// showSummary is also the entry point for clicking a recent-list row —
// "the cheapest possible seed for P6" (§3.4) — and for main.ts's
// sessionComplete handler (real or devSessionComplete). It auto-opens the
// dialog if it is not already open; if it is, it switches the currently
// open dialog straight to the card.
export function showSummary(s: SessionView): void {
  renderCard(s);
  showingSummary = true;
  showView('summary');
  if (!el.dialog.open) {
    el.dialog.showModal();
    el.scrim.classList.add('visible');
  }
}

// ---------------------------------------------------------------------
// the three-view dispatch — driven entirely by the server's own state,
// except for the one client-held `showingSummary` bit (see header note).
// ---------------------------------------------------------------------
export function renderSessions(): void {
  if (showingSummary) return; // the card stays up until NICE/Esc
  const state = store.getState();
  const sessions = (state && state.sessions) || null;
  if (sessions && sessions.active) {
    renderLiveView(sessions.active);
    showView('live');
  } else {
    renderIdleView(sessions);
    showView('idle');
  }
}

export function refreshIfOpen(): void {
  if (el.dialog.open) renderSessions();
}

export function open(): void {
  if (el.dialog.open) return;
  showingSummary = false;
  renderSessions();
  el.dialog.showModal();
  el.scrim.classList.add('visible');
  const state = store.getState();
  const hasActive = !!(state && state.sessions && state.sessions.active);
  if (!hasActive) {
    // Focus the input so the very first keystroke can be the project
    // name — the onboarding modal's same "no click first" reasoning.
    el.nameInput.value = '';
    el.nameInput.focus();
  }
}
export function close(): void {
  if (!el.dialog.open) return;
  el.dialog.close();
}
el.dialog.addEventListener('close', function () {
  el.scrim.classList.remove('visible');
  showingSummary = false;
});
el.openBtn.addEventListener('click', open);
el.close.addEventListener('click', close);
enableClickAwayDismiss(el.dialog, close);
el.niceBtn.addEventListener('click', close);

function submitStart(): void {
  const raw = el.nameInput.value.trim();
  sendAction(raw ? { action: 'SESSION_START', name: raw } : { action: 'SESSION_START' });
}
el.startBtn.addEventListener('click', submitStart);
// Enter in the name field starts the session. Bound HERE, on the input —
// the global router (keybindings.ts) deliberately never sees keys aimed
// at an input.
el.nameInput.addEventListener('keydown', function (e: KeyboardEvent) {
  if (e.key === 'Enter') {
    e.preventDefault();
    submitStart();
  }
});
el.endBtn.addEventListener('click', function () {
  sendAction({ action: 'SESSION_STOP' });
});

// handleKeydown gives this modal the keyboard-ownership tier the other
// modals have (add-a-menu-modal / onboarding-modal's "presence is the
// point"): [W] closes it, same as [H] closes #history and [S] closes
// #store. Esc is deliberately NOT intercepted — native <dialog> closes,
// which the 'close' handler above always resolves cleanly (whichever
// view was showing).
export function handleKeydown(e: KeyboardEvent): void {
  switch (e.key) {
    case 'w': case 'W': close(); break;
    default: break;
  }
}
