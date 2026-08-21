// FEATURE/LOGIC layer — the history modal (Analytics track Phase A3,
// docs/plan/A3-design.md §6/§7 Task TS-1). A read-only rendering of
// state.stats.{history,streak}, straight off the central store like the
// activity modal — no client-side derivation of anything the server could
// instead have sent. The two exceptions are the two "insights" (busiest
// day, longest focus block), which A3-design.md §5 explicitly assigns to
// the client as pure max-reductions over the already-sent `history` array
// ("a max over already-sent, server-authored data is display derivation of
// the same kind as fmtDuration"); the streak itself is never re-derived,
// only rendered verbatim (§2's "must be Go" reasoning).
//
// Gates nothing (no earning to freeze — same as activity-modal) so, like
// that modal, this one deliberately sends no open/close action to the
// server.
//
// Chart tech is CSS block bars (div height/class), never canvas —
// A3-design.md §6.2: canvas fills/strokes anti-alias at this app's 1x DPI,
// exactly the blur the A2 gate already caught on the "→" glyph. Bars are
// plain divs with an integer-px inline height set here.
import { byId } from '../dom';
import * as store from '../state/store';
import { fmtDuration, fmtInt } from '../format';
import type { DayStat, Stats } from '../wire';

// Keep in sync with A3-design.md §3.1 (game.HistoryRetentionDays) — used
// only to size the empty-state placeholder strip when history is absent;
// the real (dense, server-built) array is rendered at whatever length it
// actually has, never padded/truncated to this number.
const HISTORY_RETENTION_DAYS = 30;
const PRIMARY_CHART_DAYS = 7;
// Keep in sync with game.css's #history-bars height (the bar's containing
// box) — this is the max pixel height a full-scale bar grows to.
const BAR_AREA_HEIGHT_PX = 96;

const el = {
  history: byId<HTMLDialogElement>('history'),
  historyOpenBtn: byId<HTMLButtonElement>('history-open'),
  historyClose: byId<HTMLButtonElement>('history-close'),
  scrim: byId<HTMLDivElement>('scrim'),
  streakCurrent: byId('history-streak-current'),
  streakLongest: byId('history-streak-longest'),
  bars: byId<HTMLDivElement>('history-bars'),
  barLabels: byId<HTMLDivElement>('history-bar-labels'),
  strip: byId<HTMLDivElement>('history-strip'),
  insightBusiest: byId('history-insight-busiest'),
  insightFocus: byId('history-insight-focus')
};

// Local-date-safe: "YYYY-MM-DD" parsed as a local calendar date, never
// via `new Date(str)` (which reads the string as UTC and can shift a day
// in a negative UTC-offset zone) — matches A3-design.md §2.1's own
// "local-date basis" rule for date arithmetic.
function parseLocalDate(dateStr: string): Date | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(dateStr);
  if (!m) return null;
  return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
}

const WEEKDAY_INITIALS = ['S', 'M', 'T', 'W', 'T', 'F', 'S']; // Date#getDay(): 0=Sun..6=Sat
function dayInitial(dateStr: string): string {
  const d = parseLocalDate(dateStr);
  return d ? WEEKDAY_INITIALS[d.getDay()] : '-';
}
// "YYYY-MM-DD" -> "MM/DD", ASCII-only (no fancy glyphs, per the A2 lesson
// this doc's own §6.2 recalls) for the insight lines' compact date.
function shortDate(dateStr: string): string {
  const m = /^\d{4}-(\d{2})-(\d{2})$/.exec(dateStr);
  return m ? m[1] + '/' + m[2] : dateStr;
}

export function isOpen(): boolean { return el.history.open; }

function clearChildren(node: HTMLElement): void {
  node.replaceChildren();
}

// Renders the primary 7-day bar chart (activeSeconds) from the last
// PRIMARY_CHART_DAYS entries of `history` — a zero day is a 1px stub
// ("there but empty", A3-design.md §6.2), a fully-absent history renders
// PRIMARY_CHART_DAYS empty stubs rather than nothing, so the chart frame
// always looks the same shape whether or not data has arrived yet.
function renderBars(history: DayStat[]): void {
  clearChildren(el.bars);
  clearChildren(el.barLabels);
  const days = history.slice(Math.max(0, history.length - PRIMARY_CHART_DAYS));
  const slots = days.length > 0 ? days : new Array<DayStat | null>(PRIMARY_CHART_DAYS).fill(null);
  const max = days.reduce(function (acc, d) { return Math.max(acc, d.activeSeconds); }, 0);
  slots.forEach(function (d) {
    const bar = document.createElement('div');
    bar.className = 'history-bar';
    const value = d ? d.activeSeconds : 0;
    const heightPx = max > 0 ? Math.max(1, Math.round((value / max) * BAR_AREA_HEIGHT_PX)) : 1;
    bar.style.height = heightPx + 'px';
    if (d && d.isActive) bar.classList.add('is-active');
    bar.title = d ? d.date + ': ' + fmtDuration(d.activeSeconds) : 'no data';
    el.bars.append(bar);

    const label = document.createElement('div');
    label.className = 'history-bar-label';
    label.textContent = d ? dayInitial(d.date) : '-';
    el.barLabels.append(label);
  });
}

// Renders the 30-day month strip (streak/activity heatmap, §6.1) — one
// cell per `history` entry, lit `.is-active` when the server marked that
// day active, the last entry (today, per §5's dense-array contract)
// outlined `.is-today`. Absent history renders HISTORY_RETENTION_DAYS
// inactive placeholder cells with no "today" outline (there is no day to
// point at).
function renderStrip(history: DayStat[]): void {
  clearChildren(el.strip);
  const slots = history.length > 0 ? history : new Array<DayStat | null>(HISTORY_RETENTION_DAYS).fill(null);
  slots.forEach(function (d, i) {
    const cell = document.createElement('div');
    cell.className = 'history-cell';
    if (d && d.isActive) cell.classList.add('is-active');
    if (d && i === slots.length - 1) cell.classList.add('is-today');
    cell.title = d ? d.date + (d.isActive ? ' (active)' : ' (inactive)') : 'no data';
    el.strip.append(cell);
  });
}

// The two client-side insights (§5: pure max-reductions over the sent
// `history` array — "display derivation of the same kind as fmtDuration",
// asserts no state the server didn't send). Guards every reduction
// against an empty array so an absent/stale-server history renders
// "NO DATA" instead of throwing or showing a bogus zero date.
function renderInsights(history: DayStat[]): void {
  if (history.length === 0) {
    el.insightBusiest.textContent = 'BUSIEST DAY: NO DATA';
    el.insightFocus.textContent = 'LONGEST FOCUS: NO DATA';
    return;
  }
  const busiest = history.reduce(function (best, d) {
    return d.activeSeconds > best.activeSeconds ? d : best;
  }, history[0]);
  el.insightBusiest.textContent = 'BUSIEST DAY: ' + shortDate(busiest.date) +
    ' (' + fmtDuration(busiest.activeSeconds) + ')';

  // Fork B (A3-design.md §0/§5): use the longest sustained focus block if
  // ANY entry in the window carries it; else fall back to "best focus
  // day" (most focus sessions), which is derivable from A2 data alone.
  const hasFocusBlock = history.some(function (d) { return typeof d.longestFocusBlockSeconds === 'number'; });
  if (hasFocusBlock) {
    const best = history.reduce(function (acc, d) {
      const v = d.longestFocusBlockSeconds || 0;
      return v > (acc.longestFocusBlockSeconds || 0) ? d : acc;
    }, history[0]);
    el.insightFocus.textContent = 'LONGEST FOCUS: ' + fmtDuration(best.longestFocusBlockSeconds);
  } else {
    const best = history.reduce(function (acc, d) {
      return d.focusSessions > acc.focusSessions ? d : acc;
    }, history[0]);
    el.insightFocus.textContent = 'BEST FOCUS DAY: ' + shortDate(best.date) +
      ' (' + fmtInt(best.focusSessions) + ')';
  }
}

export function renderHistory(): void {
  const state = store.getState();
  if (!state) return;
  const stats: Partial<Stats> = state.stats || {};
  const history: DayStat[] = stats.history || [];
  const streak = stats.streak || { current: 0, longest: 0 };

  el.streakCurrent.textContent = fmtInt(streak.current);
  el.streakLongest.textContent = fmtInt(streak.longest);

  renderBars(history);
  renderStrip(history);
  renderInsights(history);
}

export function refreshIfOpen(): void {
  if (el.history.open) renderHistory();
}

export function open(): void {
  if (el.history.open) return;
  renderHistory();
  el.history.showModal();
  el.scrim.classList.add('visible');
}
export function close(): void {
  if (!el.history.open) return;
  el.history.close();
}
el.history.addEventListener('close', function () {
  el.scrim.classList.remove('visible');
});
el.historyOpenBtn.addEventListener('click', open);
el.historyClose.addEventListener('click', close);

export function handleKeydown(e: KeyboardEvent): void {
  switch (e.key) {
    case 'h': case 'H': close(); break;
    default: break; // Esc: native <dialog> behaviour, not intercepted
  }
}
