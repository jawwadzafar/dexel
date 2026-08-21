# 0013 — Analytics A3 over time: 30-day rolling history, server-side streaks, CSS pixel charts

Status: accepted (2026-08-22, A3 design pass) · Extends ADR 0012, honours ADR 0002/0009/0010

## Context

Phase A3 (ROADMAP.md "Phase A3 (v1.3) — Analytics over time") asks for
daily/weekly history, streaks, pixel-styled bar charts, and a couple of honest
"workflow insights" — all derived only from counts, with no content anywhere in
the stored history. It builds on A2 (ADR 0012), which established the per-day
`StatCounters` (keystrokes, mouse-active/active/idle seconds, sprints,
focusSessions, appSwitches) and today's earned-coin split, persisted at
schema 3, finalized on a local-date rollover (`rolloverStatsIfNewDay`).

The design pass had to settle five decisions the ROADMAP leaves open: how much
history to retain and in what shape; how a streak is defined and computed
correctly across day boundaries (the correctness-critical part); which insights
the retained data can *honestly* support; what chart technology survives the
game's fixed 640×400, 1× pixel stack; and where each computation lives so it is
testable and non-drifting.

Two candidate insights from the ROADMAP forced explicit privacy/scope calls:
- **"busiest hour"** would require a per-hour activity profile (24 buckets/day)
  — a materially more sensitive artifact than a daily count, because it
  reconstructs the user's daily schedule/sleep window from counts alone.
- **"longest focus block"** is *not derivable* from A2 data (A2 keeps only a
  count of focus sessions, never a run length).

## Decision

**1. History data model — a 30-day rolling window, sparse storage.**
Persist one day-bucket per day that actually ran: the seven `StatCounters` +
that day's total `coinsEarned` (+ Fork B's `longestFocusBlockSeconds`). Retain
the last **30 local days** (`HistoryRetentionDays = 30`) — a month is a
meaningful trend/weekly-aggregate horizon and costs ~2 KB. Days the process
never ran produce **no bucket** (an honest gap, never a fabricated zero). The
day is finalized at the existing `rolloverStatsIfNewDay` point, before the
reset. **Save schema bumps 3 → 4, additively** (a schema-3 file has no
`history`/`streak` keys → empty history + zero streak; **no backfill** — we
never had per-day data and inventing days would fabricate a fake streak). The
`ErrFutureSchema` guard is preserved.

**2. Streaks — server-side (Go), from persisted state, not the window.**
An **active day** = a local calendar date with `activeSeconds ≥
ActiveDayMinSeconds` (default **300s** of real `MoodCoding`). Streaks are
computed in `internal/game` and sent as `stats.streak.{current,longest}`; the
client only renders them. Persist `current`, `longest` (running max, never
decreases) and `lastActiveDate`, updated incrementally at finalize — so a
streak **longer than the 30-day window stays correct** after buckets are
pruned. "Today in progress" is folded in at read time (a run alive through
yesterday still shows before today's first keystroke; today adds 1 once it
crosses the threshold, with a no-double-count guard). Adjacency is calendar-
date arithmetic (`AddDate(0,0,-1)`) on the **same local-date basis the rollover
already uses**, so DST and month/year boundaries cannot corrupt it.

**3. Insights — daily granularity only; honest-or-dropped.**
Ship **"busiest day"** (max `activeSeconds` over the window — derivable from
daily buckets, zero new data). **DROP "busiest hour"** (Fork A): hourly
retention is a schedule-profiling privacy cost for marginal value. **Include
"longest focus block"** (Fork B) as a per-day content-free **duration** fed by
the engine exposing its existing sustained-typing run length — the one optional
engine touch in A3; if declined, the second insight falls back to "best focus
day" (max focus-session count) and A3 has **zero** engine changes. Insights are
pure `max`-reductions over the server-sent history array, so they are derived
**client-side**; only streaks (which depend on out-of-window persisted state)
are Go.

**4. Chart tech — CSS block bars, not canvas.**
Bars are integer-height `<div>`s filled with palette CSS vars; the 30-day
strip is 30 lit/unlit square cells. Canvas fills/strokes/text anti-alias at 1×
low-res — the A2 gate already proved fine detail turns to mush at this size/DPI
(the `→` glyph). CSS blocks land on exact pixels, theme for free, and fit the
"feature updates its own DOM" model. Labels stay ASCII (`M T W …`, `->`).

**5. UI surface — a new [H] HISTORY modal.**
A separate `<dialog id="history">` (560×312, [H] key), not a tab in Activity:
the Activity modal is already packed to the pixel in the fixed 640×400
viewport, and charts need horizontal room. It mirrors the [A]/[S] modal pattern
(read-only, gates nothing) via a new `features/history-modal.ts` — render/* and
state/store.ts are untouched.

**6. Wire — `stats.history` (dense) + `stats.streak`.**
The server sends a **dense**, date-complete, zero-filled array of the last 30
local dates ending with today-live (`DayStat` per day, incl. an `isActive` bool
set by the Go threshold so client and server share one "active day"
definition), plus the computed `StreakView`. Storage stays sparse; date math
and zero-fill live in testable Go. camelCase throughout, both new `Stats`
fields optional so a stale server degrades to "no history".

## Consequences

- **Privacy holds and is provable.** Base A3 changes no provider and no
  `Snapshot`; every new field is a count, a duration, a bool, or a calendar
  date (the same class as the existing `StatsSave.Date`). Fork B adds only a
  duration to `TickResult`. Structural allow-list tests in `game` and `store`
  are extended; "busiest hour" is deliberately absent so no schedule profile is
  ever stored.
- **Migration is trivial and safe.** Schema 3 → 4 is additive; old saves load
  with empty history + zero streak, balances/A1/A2 counters intact; future-
  schema refusal still fires. Restore order gains
  `RestoreHistory`/`RestoreStreak` **before** `RestoreStats` (extending A2's
  ordering rule) so the load-time finalize sees restored state.
- **Streak correctness is a Go test surface**, not a client concern: the tricky
  cases (gaps, today-in-progress, death, `> window`, calendar boundaries) are
  unit-tested via the existing clock seam. The client cannot drift the "active
  day" definition because the server sends `isActive`.
- **The chart cannot regress the pixel aesthetic**: no canvas, no anti-aliased
  glyphs; every bar/cell is integer-px and palette-driven.
- **Extensibility**: adding a new metric to the chart is a client change over
  the already-sent `DayStat`; the history model reuses `StatCounters`, so a
  future counter added to A1/A2 flows into history for free.
- Superseded/updated by nothing yet; the deferred copy/paste opt-in (ADR 0012
  Fork A) and hourly granularity (this ADR, Fork A) remain named, not built.
