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
import { enableClickAwayDismiss } from './modal-dismiss';
import * as store from '../state/store';
import { fmtCount, fmtDuration, fmtInt } from '../format';
import type { CoinBreakdown, StatBlock, Stats } from '../wire';

const el = {
  activity: byId<HTMLDialogElement>('activity'),
  activityOpenBtn: byId<HTMLButtonElement>('activity-open'),
  activityClose: byId<HTMLButtonElement>('activity-close'),
  scrim: byId<HTMLDivElement>('scrim'),
  // TABS (BUG-8): the modal is split into a TODAY tab (today stats + the
  // coins-earned-today breakdown) and a LIFETIME tab, so each set of rows
  // has room to breathe under the browser's ~362px dialog:modal cap instead
  // of ~14 rows crammed into one box that scroll-clipped. The two header
  // buttons and the two panels they toggle.
  tabTodayBtn: byId<HTMLButtonElement>('activity-tab-today-btn'),
  tabLifeBtn: byId<HTMLButtonElement>('activity-tab-lifetime-btn'),
  panelToday: byId<HTMLDivElement>('activity-tab-today'),
  panelLife: byId<HTMLDivElement>('activity-tab-lifetime'),
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
  // ADAPTIVE-STATS: the three APP-DERIVED rows (whole rows, label
  // included) — App switches today/lifetime and the App-switches coin
  // line — hidden where app identity is unobservable, the same
  // whole-row hide the away rows above use.
  rowTodayAppSwitches: byId('activity-row-today-app-switches'),
  rowLifeAppSwitches: byId('activity-row-life-app-switches'),
  rowCoinsAppSwitches: byId('activity-row-coins-app-switches'),
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
  // Left side is a COUNT (keystrokes / focus sessions) -> compact via
  // fmtCount; the coin delta on the right stays EXACT (fmtInt in
  // buildValue), because that is the small number the user reasons about
  // precisely — you don't earn "1.2k" coins, you earn 1240 (§14).
  return buildValue(fmtCount(count) + ' -> ', coins);
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

// TABS: which panel is showing. Remembered across opens WITHIN the session
// (a module var — survives close/reopen while the app runs), and, best
// effort, across restarts via localStorage. localStorage is a convenience,
// never a dependency: every access is wrapped so a private-mode throw or a
// cleared store just falls back to the in-memory default, never breaks the
// modal (the same try/catch discipline the artifact/runtime notes call for).
type TabName = 'today' | 'lifetime';
const TAB_STORAGE_KEY = 'dexel.activity.tab';
let activeTab: TabName = readStoredTab();

function readStoredTab(): TabName {
  try {
    return localStorage.getItem(TAB_STORAGE_KEY) === 'lifetime' ? 'lifetime' : 'today';
  } catch {
    return 'today';
  }
}
function storeTab(tab: TabName): void {
  try { localStorage.setItem(TAB_STORAGE_KEY, tab); } catch { /* best effort */ }
}

// Applies `activeTab` to the DOM: aria-selected on the two headers (the CSS
// styles the selected state off that attribute — one source of truth for
// accessibility and appearance), the `hidden` attribute on the inactive
// panel (so ONLY the active tab's rows are in flow/rendered), and a roving
// tabindex so Tab lands on the selected header and Left/Right move between
// them. `render()` keeps BOTH panels' numbers current every tick regardless,
// so switching is instant with no stale flash.
function applyTab(): void {
  const onToday = activeTab === 'today';
  el.tabTodayBtn.setAttribute('aria-selected', String(onToday));
  el.tabLifeBtn.setAttribute('aria-selected', String(!onToday));
  el.tabTodayBtn.tabIndex = onToday ? 0 : -1;
  el.tabLifeBtn.tabIndex = onToday ? -1 : 0;
  el.panelToday.hidden = !onToday;
  el.panelLife.hidden = onToday;
}

function selectTab(tab: TabName, focusHeader: boolean): void {
  activeTab = tab;
  storeTab(tab);
  applyTab();
  if (focusHeader) (tab === 'today' ? el.tabTodayBtn : el.tabLifeBtn).focus();
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
  // COUNTS compact via fmtCount ("1.2M"); DURATIONS humanize via
  // fmtDuration ("3d 4h"). See docs/ui-spec.md §14 for the classification.
  el.statTodayKeystrokes.textContent = fmtCount(today?.keystrokes);
  el.statTodayMouse.textContent = fmtDuration(today?.mouseActiveSeconds);
  el.statTodayActive.textContent = fmtDuration(today?.activeSeconds);
  el.statTodayAway.textContent = fmtDuration(today?.idleSeconds);
  el.statTodaySprints.textContent = fmtCount(today?.sprintsCompleted);
  el.statTodayFocusSessions.textContent = fmtCount(today?.focusSessions);
  el.statTodayAppSwitches.textContent = fmtCount(today?.appSwitches);
  el.statLifeKeystrokes.textContent = fmtCount(life?.keystrokes);
  el.statLifeMouse.textContent = fmtDuration(life?.mouseActiveSeconds);
  el.statLifeActive.textContent = fmtDuration(life?.activeSeconds);
  el.statLifeAway.textContent = fmtDuration(life?.idleSeconds);
  el.statLifeSprints.textContent = fmtCount(life?.sprintsCompleted);
  el.statLifeFocusSessions.textContent = fmtCount(life?.focusSessions);
  el.statLifeAppSwitches.textContent = fmtCount(life?.appSwitches);
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

  // ADAPTIVE-STATS (docs/ui-spec.md §11.6): the app-derived rows adapt to
  // the CURRENT platform's ability to observe the foreground app. App
  // switches are only countable where app identity is observable — macOS
  // has a real active-app source; Linux/Wayland is honestly app-blind
  // (ADR 0009), so on Linux `appSwitches` is a permanent 0 that would read
  // as the false fact "you never switched apps". The server now says which
  // it is via `appIdentityAvailable`, exactly the content-free capability
  // bit the personalized status line already degrades on.
  //
  // Same DISPLAY-ONLY spirit as the away rows above: recording is
  // untouched (the counts still arrive on every broadcast), this only
  // decides whether a row is on screen. The rule is deliberately narrower
  // than the away one, to honour the one subtlety app switches have that
  // away time does not — HISTORY. `appSwitches` is cumulative, so a user
  // who ran dexel on macOS (real app data) and then moved to Linux still
  // has a real, non-zero LIFETIME total from those macOS days. That value
  // WAS observed and is not a lie, so it must not be erased just because
  // the current platform is app-blind. Hence: hide a row only when the
  // platform is app-blind AND that row's own value is 0 — which hides the
  // misleading current-platform zero (today, and a genuinely-never-observed
  // lifetime) while preserving any real historical total. macOS
  // (`appIdentityAvailable` true) always shows every row, including an
  // honest real 0.
  //
  // Degradation: `!== false` treats an absent field (stale server) as
  // "available, show the rows" — the pre-existing behaviour, never worse.
  const appBlind = state.appIdentityAvailable === false;
  const hideAppRow = function (row: HTMLElement, value: number | undefined): void {
    row.style.display = appBlind && !value ? 'none' : '';
  };
  hideAppRow(el.rowTodayAppSwitches, today?.appSwitches);
  hideAppRow(el.rowLifeAppSwitches, life?.appSwitches);
  hideAppRow(el.rowCoinsAppSwitches, coins?.appSwitches);
}

export function refreshIfOpen(): void {
  if (el.activity.open) renderActivity();
}

export function open(): void {
  if (el.activity.open) return;
  // Restore the remembered tab before showing, so the modal opens on
  // whichever view was last active this session (no flash of the wrong tab).
  applyTab();
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
// A click on a tab HEADER lands inside the dialog's bounding rect, so the
// rect-based click-away helper already treats it as "inside" and does NOT
// dismiss (verified: the headers sit within #activity's box). These handlers
// only switch tabs. focusHeader:false — a mouse user keeps focus where the
// pointer put it; the arrow-key path below focuses so the roving tabindex
// follows the keyboard.
el.tabTodayBtn.addEventListener('click', function () { selectTab('today', false); });
el.tabLifeBtn.addEventListener('click', function () { selectTab('lifetime', false); });
enableClickAwayDismiss(el.activity, close);

export function handleKeydown(e: KeyboardEvent): void {
  switch (e.key) {
    case 'a': case 'A': close(); break;
    // Left/Right switch tabs. The input-focus + modifier guards that gate
    // this handler live upstream in keybindings.ts (isTextEntryTarget /
    // hasModifier run before activityModal.handleKeydown is called), so no
    // guard is repeated here. There are only two tabs, so either arrow just
    // toggles to the other one; focusHeader:true keeps the keyboard focus on
    // the newly selected header (roving tabindex).
    case 'ArrowLeft': e.preventDefault(); selectTab('today', true); break;
    case 'ArrowRight': e.preventDefault(); selectTab('lifetime', true); break;
    default: break; // Esc: native <dialog> behaviour, not intercepted
  }
}
