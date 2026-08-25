// FEATURE/LOGIC layer — the activity modal (Analytics track Phase A1). A
// read-only rendering of state.stats.{today,lifetime}, straight off the
// central store like everything else (no client-side derivation: the
// server is the sole source of truth). Deliberately sends NO open/close
// action to the server, unlike the store modal: this modal is read-only
// and gates nothing (no earning to freeze), and the counts it displays
// come entirely from the server's own per-tick sampling of the real,
// global activity provider — never from anything this page does. Opening
// it via the [A] key IS a real keystroke on the user's system and gets
// honestly counted like any other keypress (exactly as pressing [S] to
// open the store already does); this page never simulates or
// double-counts a keystroke on top of that by rendering the dialog, so
// there is no separate inflation risk to gate against.
//
// SET-1 (docs/ui-spec.md §11.3) gave this modal its one piece of
// conditional rendering: the two AWAY rows (formerly labelled "IDLE
// TIME") are shown only when the server says `config.showAwayTime`. Read
// the direction of that carefully, because it is the whole point —
// nothing about RECORDING changes. `stats.today.idleSeconds` and
// `stats.lifetime.idleSeconds` still accrue every away second, still
// arrive on every broadcast, and are still written into the rows below
// whether or not the rows are visible. Hiding is a display choice the
// user made, and it is driven by a field the SERVER sends, not by
// anything this module decided on its own. ADR 0010/0013 are untouched.
import { byId } from '../dom';
import * as store from '../state/store';
import { fmtDuration, fmtInt } from '../format';
import type { CoinBreakdown, StatBlock, Stats } from '../wire';

const el = {
  activity: byId<HTMLDialogElement>('activity'),
  activityOpenBtn: byId<HTMLButtonElement>('activity-open'),
  activityClose: byId<HTMLButtonElement>('activity-close'),
  scrim: byId<HTMLDivElement>('scrim'),
  statTodayKeystrokes: byId('stat-today-keystrokes'),
  statTodayMouse: byId('stat-today-mouse'),
  statTodayActive: byId('stat-today-active'),
  statTodayAway: byId('stat-today-away'),
  statTodaySprints: byId('stat-today-sprints'),
  statTodayFocusSessions: byId('stat-today-focus-sessions'),
  statTodayAppSwitches: byId('stat-today-app-switches'),
  // The two AWAY ROWS themselves (not just their values) — SET-1 hides
  // the whole row, label included, so a hidden away time leaves no trace
  // of itself rather than an orphaned "AWAY" with a blank beside it.
  rowTodayAway: byId('activity-row-today-away'),
  rowLifeAway: byId('activity-row-life-away'),
  statLifeKeystrokes: byId('stat-life-keystrokes'),
  statLifeMouse: byId('stat-life-mouse'),
  statLifeActive: byId('stat-life-active'),
  statLifeAway: byId('stat-life-away'),
  statLifeSprints: byId('stat-life-sprints'),
  statLifeFocusSessions: byId('stat-life-focus-sessions'),
  statLifeAppSwitches: byId('stat-life-app-switches'),
  coinsTodayKeystrokes: byId('coins-today-keystrokes'),
  coinsTodayMouse: byId('coins-today-mouse'),
  coinsTodayFocusSessions: byId('coins-today-focus-sessions'),
  coinsTodayAppSwitches: byId('coins-today-app-switches')
};

// "<count> -> <coins>" per A2-design.md §6's UX example ("Keystrokes 1,240
// → 6"). `count` is undefined on a stale (pre-A2) server or before any
// coins are attributed today — fmtInt already degrades that to "0".
//
// §6 writes the separator as a real "→" (U+2192), but rendering the real
// modal (feature-build-and-verify's gate — never trust this in isolation)
// showed that glyph isn't in the pixel font (Press Start 2P has no arrow
// glyphs), so it falls back to a thin system font whose stroke
// anti-aliases down to a near-invisible dot at this row's size/1x DPI —
// reading exactly like the "·" separator this task exists to replace. The
// ASCII "->" below is two glyphs the pixel font actually has, so it stays
// crisp; used in place of U+2192 for that reason.
//
// The coin amount is the point of this row (§6), so it's built as its own
// element with a `coin-amt` class the CSS colors gold, instead of one flat
// string — hence these return a Node to append, not a string to assign.
function countToCoinsNode(count: number | undefined, coins: number | undefined): DocumentFragment {
  return buildValue(fmtInt(count) + ' -> ', coins);
}
// Coin-only variant for signals whose raw count is already shown in the
// today/lifetime sections above (mouse is a duration, not a count; app
// switches is 0 on Linux by design, ADR 0012) — matches §6's "Mouse → 2" /
// "App switches → 0" examples (no leading count).
function coinsOnlyNode(coins: number | undefined): DocumentFragment {
  return buildValue('-> ', coins);
}
function buildValue(prefix: string, coins: number | undefined): DocumentFragment {
  const frag = document.createDocumentFragment();
  frag.append(document.createTextNode(prefix));
  const amt = document.createElement('span');
  amt.className = 'coin-amt';
  amt.textContent = fmtInt(coins);
  frag.append(amt);
  return frag;
}
// Replaces `el`'s children with `node` — the coins-row values are rebuilt
// (not just their textContent) every render so the gold coin-amt span
// stays in sync with whatever count/coins just changed.
function setValue(el: HTMLElement, node: DocumentFragment): void {
  el.replaceChildren(node);
}

export function isOpen(): boolean { return el.activity.open; }

export function renderActivity(): void {
  const state = store.getState();
  if (!state) return;
  const stats: Partial<Stats> = state.stats || {};
  const today: Partial<StatBlock> = stats.today || {};
  const life: Partial<StatBlock> = stats.lifetime || {};
  // Optional on the wire (A2-design.md §6) — absent on a stale server, so
  // default to {} and let fmtInt's own undefined-handling render 0s.
  const coins: Partial<CoinBreakdown> = stats.coinsToday || {};
  el.statTodayKeystrokes.textContent = fmtInt(today?.keystrokes);
  el.statTodayMouse.textContent = fmtDuration(today?.mouseActiveSeconds);
  el.statTodayActive.textContent = fmtDuration(today?.activeSeconds);
  el.statTodayAway.textContent = fmtDuration(today?.idleSeconds);
  el.statTodaySprints.textContent = fmtInt(today?.sprintsCompleted);
  el.statTodayFocusSessions.textContent = fmtInt(today?.focusSessions);
  el.statTodayAppSwitches.textContent = fmtInt(today?.appSwitches);
  el.statLifeKeystrokes.textContent = fmtInt(life?.keystrokes);
  el.statLifeMouse.textContent = fmtDuration(life?.mouseActiveSeconds);
  el.statLifeActive.textContent = fmtDuration(life?.activeSeconds);
  el.statLifeAway.textContent = fmtDuration(life?.idleSeconds);
  el.statLifeSprints.textContent = fmtInt(life?.sprintsCompleted);
  el.statLifeFocusSessions.textContent = fmtInt(life?.focusSessions);
  el.statLifeAppSwitches.textContent = fmtInt(life?.appSwitches);
  setValue(el.coinsTodayKeystrokes, countToCoinsNode(today?.keystrokes, coins?.keystrokes));
  setValue(el.coinsTodayMouse, coinsOnlyNode(coins?.mouse));
  setValue(el.coinsTodayFocusSessions, countToCoinsNode(today?.focusSessions, coins?.focusSessions));
  setValue(el.coinsTodayAppSwitches, coinsOnlyNode(coins?.appSwitches));

  // SET-1 (docs/ui-spec.md §11.3): the away rows appear only when the
  // server says so. Written LAST, and as its own step, so it is obvious
  // that the values above were computed unconditionally — the rows are
  // filled in either way and the only thing this decides is whether they
  // are on screen.
  //
  // `display: none` rather than `visibility: hidden`: the rows sit in
  // normal flow inside .activity-section, so collapsing them closes the
  // gap instead of leaving a blank stripe where a number used to be. The
  // section's own top is fixed in CSS, so the rows below simply move up
  // and the section ends 12px shorter — no overflow, and nothing else in
  // the modal moves.
  const showAway = !!(state.config && state.config.showAwayTime);
  el.rowTodayAway.style.display = showAway ? '' : 'none';
  el.rowLifeAway.style.display = showAway ? '' : 'none';
}

export function refreshIfOpen(): void {
  if (el.activity.open) renderActivity();
}

export function open(): void {
  if (el.activity.open) return;
  renderActivity();
  el.activity.showModal();
  el.scrim.classList.add('visible');
}
export function close(): void {
  if (!el.activity.open) return;
  el.activity.close();
}
el.activity.addEventListener('close', function () {
  el.scrim.classList.remove('visible');
});
el.activityOpenBtn.addEventListener('click', open);
el.activityClose.addEventListener('click', close);

export function handleKeydown(e: KeyboardEvent): void {
  switch (e.key) {
    case 'a': case 'A': close(); break;
    default: break; // Esc: native <dialog> behaviour, not intercepted
  }
}
