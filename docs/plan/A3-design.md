# A3 design — analytics over time: history, streaks & honest insights (v1.3)

Design pass for ROADMAP.md "Phase A3 (v1.3) — Analytics over time". This is
the gate: it fixes the per-day history data model + retention, the streak
algorithm (the correctness-critical bit), the workflow insights, the chart
tech, the UI surface, the wire/save shape, and the fan-out. No implementation
code is written here. Companion decision record:
`docs/adr/0013-analytics-over-time-history-streaks-and-charts.md`.

A3 builds directly on A2 (docs/plan/A2-design.md, ADR 0012): the per-day
history is a *rolling window of the counters A1/A2 already accumulate*
(`game.StatCounters` + today's earned coins). It adds **no new signal, no new
`Snapshot` field, no new provider observation** — the base phase adds nothing
to the `activity` or `engine` packages at all (see Fork B for the one optional
exception). That absence is, once again, the privacy evidence.

---

## 0. Forks that need a USER/overseer decision (surface these first)

Both have a recommended default; **A3 ships on the defaults without asking.**

- **Fork A — hourly granularity ("busiest hour" insight).** ROADMAP names
  "busiest hour" as a candidate insight. Delivering it honestly requires
  retaining a **per-hour** activity profile (24 buckets/day), which is a real
  privacy step *down*: a daily count says "you worked 4 hours"; an hourly
  profile says *when* — it reconstructs your working schedule, meal breaks and
  sleep window from counts alone. That is a materially more sensitive artifact
  than anything the tracker stores today, for a marginal insight.
  **Default taken here: DROP "busiest hour". Retain daily granularity only.**
  We ship "busiest **day**" instead (fully derivable from the daily buckets,
  zero new data, zero new privacy cost). Choose Fork A only if the user
  explicitly wants an hourly heatmap and accepts the schedule-profiling cost.

- **Fork B — "longest focus block" insight (a per-day max run duration).**
  ROADMAP names "longest focus block". A2 retains only a *count* of focus
  sessions (each ≥ `engine.FocusSessionSeconds` sustained typing); it does
  **not** retain the length of the longest single run, so this insight is
  **not derivable from A2 data**. It is cheap and honest to add: a single
  content-free **duration** per day (`longestFocusBlockSeconds`), fed by the
  engine exposing its existing sustained-typing run length. Cost: one new
  `uint64` on `engine.TickResult` + a per-day max tracker (the *only* engine
  touch in all of A3).
  **Default taken here: INCLUDE Fork B.** It is the marquee "workflow insight"
  the ROADMAP asks for, it is a duration (passes every structural test), and
  it is a one-field addition. It is spec'd throughout below **in [B] brackets**
  so that declining it removes exactly those bracketed lines and leaves A3
  with **zero engine changes** (the cleanest possible privacy story, matching
  A2's "no new observation"). If declined, the second insight falls back to
  "best focus day" (most focus sessions in a day — derivable from A2 counts).

Everything below is designed so either fork choice is a localized edit.

---

## 1. What the data can honestly support (the hard question, answered)

| Candidate insight | Data needed | Retained today? | Verdict |
|---|---|---|---|
| Daily/weekly totals of every counter | daily buckets of `StatCounters` | **add in A3** (this phase) | **SHIP** |
| Coins earned per day | one `uint64`/day (sum of that day's coin split) | **add in A3** | **SHIP** |
| **Streaks** (consecutive active days) | daily "active?" flag + persisted streak state | **add in A3** | **SHIP** (server-computed, §2) |
| **Busiest day** (most active seconds) | daily `activeSeconds` | yes, from the buckets | **SHIP** (insight 1) |
| **Longest focus block** | per-day max sustained-run duration | **no** (A2 keeps only the count) | **Fork B** — add a content-free duration, or fall back to "best focus day" |
| Busiest **hour** | 24 hourly buckets/day | **no** | **DROP** (Fork A — schedule-profiling privacy cost > value) |

**Design principle carried from A2:** an insight ships only if it is derivable
from data we deliberately retain. We do not fabricate, and we do not widen the
observation surface for a "nice to have". Daily granularity is the honest
resolution; anything finer is a privacy decision, not a UI decision.

---

## 2. Streaks — the correctness core (server-side, Go, fully testable)

**Where it computes: entirely in Go** (`internal/game`). The client renders
`stats.streak.{current,longest}` verbatim and never re-derives them. This is
deliberate: streaks depend on persisted cross-window state (`lastActiveDate`
and a running `current`) that is **not reconstructible from the retained
window** — a streak can outlive the 30-day chart window, and "is the streak
still alive?" needs the exact last-active date. Insights that *are* pure
functions of the sent window (busiest day, etc.) are derived client-side (§5);
streaks are the one thing that must be Go.

### 2.1 Definition of an "active day"

An **active day** is a local calendar date whose `activeSeconds`
≥ `game.ActiveDayMinSeconds` (**default 300s = 5 minutes of `MoodCoding`**).

- `activeSeconds` already counts one second per tick whose mood is
  `engine.MoodCoding` (a real, recent keystroke — ADR 0010), so this bar is
  "5 real minutes of actual coding", not "one stray keypress". Content-free,
  deterministic, tunable in one constant beside the other analytics constants.
- **Local-date basis is identical to the existing rollover**
  (`rolloverStatsIfNewDay`: `now().Local().Format("2006-01-02")`). Streak
  arithmetic operates on *calendar dates*, never on 24-hour deltas, so DST
  transitions and month/year boundaries cannot corrupt it (adjacency = "the
  previous calendar date", computed via `time.Time.AddDate(0,0,-1)`).

### 2.2 Persisted streak state (survives beyond the chart window)

```
current        int     // length of the run ending at lastActiveDate
longest        int     // running max over all time; NEVER decreases
lastActiveDate string  // local "YYYY-MM-DD" of the most recent active day, "" if none
```

Persisting `current`/`lastActiveDate` (rather than recomputing from buckets)
is what makes a streak **longer than the 30-day retention window** correct: the
buckets get pruned, the streak counter does not.

### 2.3 Update at day finalize (§3 — runs when a day ends)

When day `D` (date `dD`, active flag `a`) is finalized:

```
if !a:  return                      // an inactive finished day never extends;
                                    // aliveness is decided at read time (§2.4)
if lastActiveDate == "":            current = 1
else if dD == lastActiveDate.plusDays(1): current += 1     // consecutive
else if dD == lastActiveDate:       /* re-finalize guard: no-op */
else:                               current = 1             // gap → new run
lastActiveDate = dD
if current > longest:               longest = current
```

Gaps are handled implicitly: any day `> 1` calendar day after `lastActiveDate`
starts a fresh run of 1. Missing days (process not running) never call finalize
and never appear in `lastActiveDate`, so the *next* active day sees a > 1 gap
and resets — correct.

### 2.4 Read-time "effective" streak (what the wire sends)

Today is **in progress** and is never finalized while running, so today's
activity is folded in at State-build time without mutating persisted state:

```
today = now().Local() date
todayActive = statsToday.activeSeconds >= ActiveDayMinSeconds
if lastActiveDate == "":                     base = 0
else if lastActiveDate == today:             base = current   // already counts today (defensive)
else if lastActiveDate == today.minusDays(1): base = current  // run alive through yesterday
else:                                         base = 0         // last active ≥2 days ago → dead
effectiveCurrent = base + (todayActive && lastActiveDate != today ? 1 : 0)
effectiveLongest = max(longest, effectiveCurrent)
send streak = { current: effectiveCurrent, longest: effectiveLongest }
```

### 2.5 Edge cases (each gets a Go test — §6)

1. **Consecutive days** → `current` increments by 1 each finalize.
2. **Gap** (a missing/inactive day) → next active day resets `current` to 1;
   `longest` is preserved.
3. **Today in progress**: yesterday active, today not yet → `effectiveCurrent`
   = the run through yesterday (still shown, not reset to 0 at midnight). Today
   crosses the threshold → `effectiveCurrent` = that run + 1, with **no double
   count** (the `lastActiveDate != today` guard).
4. **Streak death**: last active ≥ 2 days ago → `effectiveCurrent` = 0, but
   `longest` still shows the record.
5. **Streak > retention window** (e.g. 40 days): correct, because `current`
   is a persisted counter, not derived from the 30 pruned buckets.
6. **Inactive day** (`activeSeconds` below threshold) → does not extend.
7. **Calendar boundaries**: Dec 31 → Jan 1 and Feb 28 → Mar 1 are adjacent;
   DST spring-forward/fall-back days are still single calendar dates.
8. **Fresh save / migrated schema-3 save**: `lastActiveDate == ""` → streak
   begins today; **no backfill** (see §4 — inventing past days is dishonest).

---

## 3. History data model — the rolling window

### 3.1 Retention window: **30 local days** (`game.HistoryRetentionDays = 30`)

Justification:
- A month is a meaningful trend horizon for a personal workflow tracker and
  gives the weekly view **~4 whole weeks** to aggregate.
- Storage is trivial: one day-bucket ≈ 8 `uint64`s ≈ 64 bytes raw; 30 days is
  ~2 KB raw, ~6–8 KB as indented JSON. No paging, no external file.
- It bounds the save's growth to a constant — the file never grows without
  limit for a long-lived install.
- Streaks are **not** limited by this window (§2.2), so a short retention does
  not cap the headline streak number. 30 days is a display/trend budget, not a
  correctness budget.

### 3.2 Persisted bucket shape (sparse — only days that actually ran)

History stores **only days that had ticks** (a day the process never ran
produces no bucket — an honest gap, not a fabricated zero). The dense,
zero-filled, date-complete view is built for the wire (§5), not stored.

Each finalized day is one `StatCounters` snapshot + the day's coin total
[+ Fork B's max focus-block duration]:

```
DayBucket {
  date        string        // local "YYYY-MM-DD"
  counters    StatCounters  // the 7 A1/A2 counters, verbatim shape (reused)
  coinsEarned uint64        // = sum of that day's CoinBreakdown at finalize
  [B] longestFocusBlockSeconds uint64   // Fork B only
}
```

Reusing `StatCounters` for `counters` means the existing content-free
structural coverage already applies to the seven counters; only the two scalar
additions (`coinsEarned` [, `longestFocusBlockSeconds`]) are new fields to
allow-list. All content-free (counts + durations + a calendar date, exactly
the class `StatsSave.Date` already is).

### 3.3 Finalizing a day at the existing rollover

`rolloverStatsIfNewDay` is the single finalize point. Extend it so that, the
moment the local date changes, the day that just *ended* is finalized **before**
the reset:

```
today := now().Local() date
if statsDate == today { return }
if statsDate != "" {                       // there was a real prior day
    finalizeDay(statsDate, statsToday, coinsToday)   // append bucket + updateStreak (§2.3)
}
statsDate = today
statsToday  = StatCounters{}
coinsToday  = CoinBreakdown{}
[B] resetPerDayFocusBlockMax()

finalizeDay(date, counters, coins):
    coinsEarned := coins.sum()
    history = append(history, DayBucket{date, counters, coinsEarned [, focusBlockMax]})
    if len(history) > HistoryRetentionDays { history = history[len-HistoryRetentionDays:] }
    updateStreak(date, counters.ActiveSeconds >= ActiveDayMinSeconds)   // §2.3
```

This fires from both call sites the rollover already has:
- **in-process midnight crossing** (via `recordStats`) — finalizes the day at
  the first tick after midnight;
- **`RestoreStats` on load** — a save reopened days later finalizes the last
  running day exactly once (see §4 double-finalize proof).

**Same-day reload never double-finalizes** (statsDate == today → early return).
**Multi-day-gap reload** finalizes the single last recorded day and starts
today's bucket empty; the intervening never-ran days remain honest gaps that
break the streak by §2.3's `> 1` gap rule.

---

## 4. Save shape — schema bump 3 → 4 (additive, migration-free)

`internal/store`, mirroring A2's decoupled-declaration pattern (own types, not
imports of the game package's):

```
StatsSave += {
  History []DayBucketSave `json:"history"`   // chronological, oldest→newest, ≤30
  Streak  StreakSave       `json:"streak"`
}
DayBucketSave {
  Date        string           `json:"date"`
  Counters    StatCountersSave `json:"counters"`   // reuse the existing schema-3 shape
  CoinsEarned uint64           `json:"coinsEarned"`
  [B] LongestFocusBlockSeconds uint64 `json:"longestFocusBlockSeconds"`
}
StreakSave {
  Current        int    `json:"current"`
  Longest        int    `json:"longest"`
  LastActiveDate string `json:"lastActiveDate"`   // same content-free class as StatsSave.Date
}
CurrentSchema = 4
```

**Migration 3 → 4 needs no dedicated code** — identical reasoning to the 1→2
and 2→3 bumps documented in `store.go`: a schema-3 file has no `history`/
`streak` keys, `json.Unmarshal` leaves `History` an empty slice and `Streak` a
zero `StreakSave{}`, and `Apply` hands those straight to the game. Result: an
old save loads with **empty history and a zeroed streak** — the correct
"nothing recorded over time yet" state. **We deliberately do not backfill**
history from `lifetime`/`today` — we never had per-day data, so any backfill
would be invented days (dishonest, and would fabricate a fake streak).

**`ErrFutureSchema` is preserved unchanged.** A schema-4 save opened by a
schema-3 build is renamed to `.future` untouched and refused — bumping to 4 is
exactly what makes that guard fire for this change. `store/content_free_test.go`
allow-lists are extended to match (§6).

**Restore ordering** (extends A2's `RestoreCoinsToday`-before-`RestoreStats`
rule): `Apply` must call `RestoreHistory` and `RestoreStreak` **before**
`RestoreStats`, because `RestoreStats` triggers `rolloverStatsIfNewDay`, whose
new `finalizeDay` appends to the restored history and updates the restored
streak — not empty ones.

---

## 5. Wire contract — new fields (camelCase, A1/A2 convention)

`StatsView` (the `stats` object in a `state` message) gains two fields:

```
StatsView += {
  History []DayStat  `json:"history"`   // DENSE: last 30 local dates ending TODAY, zero-filled, today last
  Streak  StreakView `json:"streak"`    // server-computed (§2)
}
DayStat {
  date               string  `json:"date"`               // local "YYYY-MM-DD"
  keystrokes         uint64  `json:"keystrokes"`
  mouseActiveSeconds uint64  `json:"mouseActiveSeconds"`
  activeSeconds      uint64  `json:"activeSeconds"`
  idleSeconds        uint64  `json:"idleSeconds"`
  sprintsCompleted   uint64  `json:"sprintsCompleted"`
  focusSessions      uint64  `json:"focusSessions"`
  appSwitches        uint64  `json:"appSwitches"`
  coinsEarned        uint64  `json:"coinsEarned"`
  isActive           bool    `json:"isActive"`           // server marks active-day per §2.1 threshold
  [B] longestFocusBlockSeconds uint64 `json:"longestFocusBlockSeconds"`
}
StreakView { current int `json:"current"`; longest int `json:"longest"` }
```

**Why the wire history is DENSE while storage is SPARSE:** date arithmetic and
gap zero-filling belong in testable Go, not in the client. The server builds
`history` as exactly `HistoryRetentionDays` entries, one per calendar date from
`today-(N-1)` to `today` inclusive, ascending. Each entry is the persisted
bucket for that date if one exists, else an all-zero `DayStat` for that date.
The **final** entry is **today, live** (`statsToday` + `coinsToday.sum()`
[+ live focus-block max]) — so today's bar grows through the day. `isActive` is
set by the **same Go threshold** (§2.1) so the client's month-strip coloring
and the streak agree on one definition — the client never re-derives "active".

**Computation location, decided:** streaks → **server** (§2, must-be-Go).
The `history` array → **server** (date math + zero-fill). The two workflow
**insights** → **client**, as pure `max`-reductions over the sent `history`
array (busiest day = max `activeSeconds`; [B] longest focus block = max
`longestFocusBlockSeconds`, else "best focus day" = max `focusSessions`). A
max over already-sent, server-authored data is display derivation of the same
kind as `fmtDuration` — it asserts no state the server didn't send — so it
stays client-side to keep the wire to just `history` + `streak`. The one
error-prone computation (streaks) is the one that is Go-tested.

`StateMessage.Stats` (`stats`) already exists; no top-level wire change.
`wire.ts` mirrors the above with all fields **required** on `DayStat`/
`StreakView` but `history?`/`streak?` **optional on `Stats`** so a stale
(pre-A3) server degrades to "no history" rather than crashing — matching the
existing `coinsToday?`/`stats?` pattern.

---

## 6. UI surface — a new [H] HISTORY modal

### 6.1 Decision: a **new modal**, key **[H]**, NOT a tab in Activity

Rationale (screen real-estate is the deciding factor — modals are fixed-size
pixel boxes):
- The Activity modal is already **360×396 in a 640×400 viewport, packed to the
  pixel** (today + lifetime + coins, footer pinned at `top:376`, "2px clear of
  the footer, no overflow" per its own CSS). There is no room for charts.
- Charts need **horizontal** space (bars across days, a 30-cell month strip);
  Activity is a narrow two-column list. A tab system inside a fixed pixel box
  adds chrome + tab state for no benefit and would force the box wider anyway.
- A separate **[H]** modal mirrors the existing **[A]/[S]** pattern exactly
  (the `add-a-menu-modal` recipe): native `<dialog>`, shared `#scrim`,
  mouse + key + Esc, read-only (gates nothing — no earning to freeze, same as
  Activity). One new titlebar button `[H] HISTORY`.

Sizing: **`#history` at `left:40 top:44 width:560 height:312`** (the store's
wide footprint, centered in 640×400), giving the 7 day-bars and 30 month-cells
room to breathe at integer widths.

Layout (all integer px, Press Start 2P 8px, ASCII-only labels):
- **Streak banner** (top): `CURRENT STREAK: N DAYS   LONGEST: M` — the two
  numbers from `stats.streak`, the current count in `--gold`.
- **7-day bar chart** (primary): the last 7 entries of `history`. One bar per
  day of a primary metric (default: `activeSeconds`), day-initial labels
  (`M T W T F S S`, ASCII). Bars are CSS (§6.2).
- **30-day month strip** (streak/activity heatmap): 30 small square cells, one
  per `history` entry, lit `--screen` when `isActive`, `--shadow` when not,
  today outlined `--gold`. This is the streak made visible.
- **Insights** (bottom, client-derived from `history`): `BUSIEST DAY: <date>`
  and `[B] LONGEST FOCUS: Xm Ys` (else `BEST FOCUS DAY: <date> (K)`).
- Footer: `H / ESC CLOSE`.

### 6.2 Chart tech: **CSS block bars**, not canvas

Decision: **CSS** (styled `<div>`s), justified:
1. **Pixel-perfect, no anti-aliasing.** A bar is a `div` with an integer-px
   `height` and a flat `background: var(--screen)` — every edge lands on a
   pixel. Canvas fills/strokes/text **anti-alias** at 1× low-res; the A2 gate
   already proved fine detail (the `→` glyph) turns to mush at this
   size/DPI — canvas would reintroduce exactly that blur.
2. **Theme + palette for free** via the existing CSS vars (`--screen`,
   `--gold`, `--shadow`).
3. **Fits the render model**: the feature module sets `height`/class on divs;
   no imperative draw loop, no second rendering path, inspectable in devtools.
4. Bar height = `round(value / maxInWindow * chartPx)` in integer px; a zero
   day is a 1px stub (visible "there but empty"), a missing day is a zero
   `DayStat` so it renders identically and honestly.
Labels stay ASCII (`M T W …`, `->`) — no arrow/fancy glyphs (the A2 lesson).

### 6.3 DOM / wire additions and which F2 modules change

Respecting F2 boundaries (features own modals; render/* stays side-effect-free
and is **untouched**; wire.ts is the contract; state/store.ts holds state and
is **unchanged** — the modal reads `store.getState().stats` directly, exactly
as `activity-modal` does):

- **new `features/history-modal.ts`** — owns the `#history` dialog:
  `open/close/isOpen/handleKeydown/renderHistory/refreshIfOpen`, builds the
  streak banner + bars + strip + insights from `store.getState().stats`.
  (The feature builds its own bar/cell DOM, mirroring how `activity-modal`
  builds its rows — no new render module, so render/* is not touched.)
- **`wire.ts`** — add `DayStat`, `StreakView`; extend `Stats` (§5).
- **`features/keybindings.ts`** — route `[H]` → `historyModal.open()`; while
  open, `historyModal.handleKeydown` owns the keyboard (Esc native).
- **`main.ts`** — import history-modal; call `historyModal.refreshIfOpen()` in
  `renderAll()`; wire nothing else.
- **`app/public/index.html`** — add `<dialog id="history">` DOM + the
  `[H] HISTORY` titlebar button.
- **`app/public/css/game.css`** — add `#history` modal + `.history-bar` /
  `.history-cell` / streak-banner / insight styles.

---

## 7. Implementation plan — parallelizable, by EXCLUSIVE file ownership

### Contract seam — PIN these exact names (agents must not drift)

**Wire (`internal/game` + `frontend/src/wire.ts`):**
`StatsView.History []DayStat json:"history"`, `StatsView.Streak StreakView
json:"streak"`. `DayStat{ date, keystrokes, mouseActiveSeconds, activeSeconds,
idleSeconds, sprintsCompleted, focusSessions, appSwitches, coinsEarned,
isActive [, longestFocusBlockSeconds] }`. `StreakView{ current, longest }`.

**Save (`internal/store`):** `StatsSave.History []DayBucketSave
json:"history"`, `StatsSave.Streak StreakSave json:"streak"`. `DayBucketSave{
date, counters (StatCountersSave), coinsEarned [, longestFocusBlockSeconds] }`.
`StreakSave{ current, longest, lastActiveDate }`.

**Constants:** `game.HistoryRetentionDays = 30`, `game.ActiveDayMinSeconds =
300`. **[B] only:** `engine.TickResult.FocusRunSeconds uint64` (current
sustained-typing run length this tick).

**Game API GO-2 depends on:** `Game.HistorySnapshot() []DayBucket`,
`Game.StreakSnapshot() (current, longest int, lastActiveDate string)`,
`Game.RestoreHistory([]DayBucket)`, `Game.RestoreStreak(current, longest int,
lastActiveDate string)` — called **before** `RestoreStats` in `Apply`.

### Tasks (exclusive owners, no shared files)

**[B] Task GO-0 — engine (owns `internal/engine/*`)** *(Fork B only; skip if
declined)*
- Expose `TickResult.FocusRunSeconds` = the length of the current sustained-
  typing run the focus-session tracker already maintains (0 when broken).
- No economy change; keep every A2 strategy/ceiling test green.

**Task GO-1 — game history/streak/wire (owns `internal/game/*`)**
- Add history buckets + `finalizeDay` at `rolloverStatsIfNewDay` (§3.3);
  `coinsEarned = coinsToday.sum()` [+ per-day focus-block max from GO-0].
- Add persisted streak state + `updateStreak` (§2.3) + read-time effective
  streak (§2.4). Add `HistoryRetentionDays`, `ActiveDayMinSeconds`.
- Build the DENSE wire `history` (§5, date-complete zero-fill, today live,
  `isActive` per threshold) + `StreakView`; add both to `StatsView` and
  populate in `State()`.
- Add `HistorySnapshot`/`StreakSnapshot`/`RestoreHistory`/`RestoreStreak`.
- Extend `game/content_free_test.go`: allow-list `StatsView.History`/`Streak`,
  add `checkExact` blocks for `DayStat` and `StreakView` (`date` string handled
  exactly like the existing `StatsSave.Date` allow-list entry).
- **Tests:** every streak edge case §2.5 (drive `SetClockForTest`); dense-wire
  builder (gaps → zero-filled, correct ascending dates ending today, today =
  live, `isActive` matches threshold); retention pruning (len == 30, oldest
  dropped); finalize-on-rollover appends exactly one bucket + updates streak.

**Task GO-2 — persistence (owns `internal/store/*`)**
- `DayBucketSave` + `StreakSave`; `StatsSave.History`/`Streak`; bump
  `CurrentSchema = 4`; extend `Snapshot` (history+streak from GO-1 snapshots)
  and `Apply` (`RestoreHistory`+`RestoreStreak` **before** `RestoreStats`).
- Extend `store/content_free_test.go` allow-lists.
- **Tests:** schema-3 → 4 load (history empty, streak zero, balances/counters
  intact); schema-4 round-trip (history + streak survive); **future-schema
  refusal** (schema-5 → `ErrFutureSchema` + `.future` backup, unchanged);
  retention across a save/reload; **finalize-on-reload exactly once** (save
  from N days ago → last running day finalized into history + streak on Apply;
  same-day reload → no double-finalize).
- Depends on GO-1's game types/API.

**Task TS-1 — frontend (owns `frontend/src/wire.ts`,
`features/history-modal.ts`; `features/keybindings.ts`; `main.ts`; the
`#history` DOM in `app/public/index.html`; `#history`/chart CSS in
`app/public/css/game.css`; and the history fixture in
`frontend/src/dev/dev-fixtures.ts`)**
- Wire types (§5); the `#history` modal + CSS bars/strip (§6.2); `[H]` route;
  `refreshIfOpen` wiring. `tsc --noEmit` clean.
- Pre-stage the visual-gate fixture (§8): a 30-entry `history` + a `streak` on
  `DEV_STATE.stats` with varied values so `?dev=1` renders a populated modal.
- Contract-only dependency on GO-1 field names (this doc) — starts in parallel.

**Task DOC-1 — ADR/spec (owns `docs/`)** — this design + ADR 0013 (done);
update `docs/ui-spec.md` §6.1 with the `history`/`streak` fields and the `[H]`
modal rects; add the ADR 0013 row to `docs/adr/README.md`.

### Waves

`[B] GO-0` → **`GO-1 ‖ TS-1 ‖ DOC-1`** → `GO-2` → **in-game gate (§8)**.
(GO-0 must land before GO-1 consumes `FocusRunSeconds`; if Fork B is declined,
Wave 0 is just `GO-1 ‖ TS-1 ‖ DOC-1`.) No two tasks share a file: `engine/*`
→ GO-0; `game/*` → GO-1; `store/*` → GO-2; `wire.ts`/`features/*`/`main.ts`/
`index.html`/`game.css`/`dev-fixtures.ts` → TS-1; `docs/*` → DOC-1.

---

## 8. Verification exit criteria (from ROADMAP.md A3) + the visual gate

- **History renders**: the `[H]` modal shows a 7-day bar chart + 30-day strip
  from real accumulating buckets; buckets survive restart (schema-4 round-trip).
- **Streaks compute correctly across day boundaries**: the Go tests in §2.5
  pass (consecutive, gap, today-in-progress, death, `> window`, calendar
  boundaries), and the number shown matches.
- **No content anywhere**: `Snapshot`/provider unchanged (base A3; [B] adds
  only a duration to `TickResult`); extended structural tests green in `game`
  and `store`; every new field is a count, a duration, a bool, or a calendar
  date.
- **Migration**: schema-3 save loads with balances + A1/A2 counters intact,
  empty history, zero streak; future-schema refusal still fires.

**In-game visual gate (feature-build-and-verify — no visual change is trusted
until rendered in the REAL running game):**

*Populating multi-day history for the screenshot — two routes, both spec'd:*
1. **Primary (matches how [A]/[S] are gated):** the `?dev=1` harness. TS-1 adds
   a hand-authored 30-day `history` + `streak` to `DEV_STATE.stats`
   (dev-fixtures.ts); headless Chromium loads `?dev=1`, opens `[H]`, screenshots.
   No backend needed.
2. **Real-binary proof:** a test-only seed helper writes a **schema-4**
   `state.json` with N synthetic day-buckets (+ a streak) to the config path;
   run the built Go binary with the **fake provider** against that save so the
   REAL server emits a populated `stats.history`/`stats.streak` over WS, then
   screenshot `[H]`. This proves the Go dense-wire + streak path end-to-end,
   not just the fixture.

*Judge with your own eyes:* bars render crisp (no AA mush), the month strip
lights exactly the active days, the streak number reads correctly, insights are
present, zero console errors — on the Linux dev box (where `appSwitches` is 0
by design, ADR 0009/0012 — shown honestly).
