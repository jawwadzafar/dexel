# Sessions, counters, history, and streaks

Everything Dexel counts about your day, and the one thing you can declare.
Sources: `app/internal/game/session.go`, `app/internal/game/game.go`
(`recordStats`, `rolloverStatsIfNewDay`), `app/internal/game/history.go`.
Decisions: [ADR 0013](../adr/0013-analytics-over-time-history-streaks-and-charts.md),
[ADR 0017](../adr/0017-sessions.md).

---

## 1. Sessions

A **session** is the user saying "I sat down to work". It is a *lens* over
tracking that already happens — **never a second economy**. The invariant that
governs every method in `session.go`, stated once in its package comment:

> No code path in `Tick` may behave differently — for Dev Cash, XP, progress,
> mood, active app, or any daily/lifetime counter — because a session happens
> to be open.

That is made true by construction rather than by a clamp. Every number a
session reports is `watermark − baseline`: two reads of the same monotonic
lifetime counter. "Session ⊆ global" and "no double counting" are therefore
not properties that have to be maintained; they are arithmetic.
`TestSessionDoesNotAffectTheEconomyAtAll` pins it.

Two numbers have no monotonic lifetime counter to subtract from — `coinsEarned`
and `longestFocusBlockSeconds` — and those two are per-session accumulators
updated at their single source-of-truth call sites (`awardCoins` and
`recordStats`). The design is careful never to have both an accumulator *and*
a delta for the same number.

### The constants

All in `app/internal/game/session.go`:

| Constant | Value | Meaning |
| --- | --- | --- |
| `SessionMinDurationSeconds` | `60` | shorter than this and the session is **discarded entirely** |
| `SessionIdleTimeoutSeconds` | `7200` (2 h) | no observed input for this long → auto-end |
| `SessionMaxDurationSeconds` | `57600` (16 h) | hard cap, applies to every provider |
| `MaxSessionNameLen` | `32` runes | project names are wider than the pet's 24 |
| `SessionsWireWindow` | `10` | finished sessions carried in `sessions.recent` |
| `SessionsWeekDays` | `7` | the "this week" window, local dates including today |

### Start and stop

- **Exactly one session at a time.** `SESSION_START` while one is open is
  rejected with `ErrSessionAlreadyActive`; `SESSION_STOP` with none open is
  rejected with `ErrSessionNotActive`. Neither rejection changes anything.
- **A name is optional.** `NormalizeSessionName` drops control characters,
  trims, and truncates to 32 runes — and an empty result is **legal**.
  "Unnamed" is a first-class session state, unlike the pet's name.
- **A session under 60 seconds produces nothing at all**: no log row, no
  counter, no message, no scold. Mashing start/stop is structurally
  inert. This applies uniformly, including to an auto-ended session.
- **Both end paths share one function** (`finishSession`), so a user stop and
  an automatic end are celebrated identically.

### The two auto-ends, and the honesty gate between them

`checkSessionAutoEnd` runs at the very *top* of `Tick`, against the
session's **pre-existing** last-activity watermark. That ordering is what makes
a session that went stale while the process was closed end backdated to the
last activity actually seen, rather than blamed on the tick that noticed. It is
the reopen-after-a-long-close self-heal.

Rule order, and it matters:

1. **Idle (2 h)** — checked first, and **only when
   `r.SeesGlobalInput()`**. A blind provider can never know "idle" and must
   not claim it; the idle auto-end refuses it for exactly the same reason the
   mood machine refuses `onBreak` from a blind provider.
2. **The 16 h cap** — checked second, and applies to *every* provider, honest
   or blind. It is the only bound a blind provider has.

A session both stale enough to idle-end and old enough to have hit the cap
ends via the idle rule, which produces the shorter and more honest of the two
records. The cap's end timestamp is `lastActivityAt`, unless continued real
activity pushed that past the cap boundary — a hard cap must never be exceeded
by the record it produces.

### What a session reports

`sessions.active` (null when none) and each entry of `sessions.recent` carry
the same shape: `keystrokes`, `mouseActiveSeconds`, `activeSeconds`,
`idleSeconds`, `sprintsCompleted`, `focusSessions`, `appSwitches`,
`pausedSeconds`, `coinsEarned`, `longestFocusBlockSeconds`, plus timestamps
and — for a finished one — `endReason` (`user` | `idle` | `maxDuration`).

`pausedSeconds` is in that delta set because **a session survives a pause**.
Without it, a two-hour session with a 90-minute pause in the middle would look
like 90 minutes of unexplained idle.

`sessions.summary` is derived from the verified log itself rather than from a
second protected counter that could drift from it:

- `completed` — the log's length,
- `thisWeek` — sessions whose local end-date falls in the last 7 local dates
  including today,
- `longestSessionSeconds` — the max across the **whole** log, never bounded by
  the 10-entry wire window.

### Names live outside the protected save

No type in `session.go` has a `Name` field. A project name is user-authored
config: it lives in `config.json`'s `sessionNames` map keyed by session id, and
is joined onto the wire view by id at broadcast time. The
content-free allow-list for the session save types adds `"name"` to its
forbidden-substring list specifically to keep it that way. See
[`persistence.md`](persistence.md) §5.

A consequence, accepted rather than fixed: a discarded short session leaves its
id to be reused by the next start, so starting a session unnamed can clear a
name a previous discarded attempt at that id had set. That doubles as the
user's natural "clear that name" path.

---

## 2. The daily and lifetime counters

`recordStats` runs on **every** tick, and this is the one place the analytics
layer deliberately diverges from the economy layer.

### What is counted

`StatCounters` is eight `uint64`s, and every one is a count of events or
elapsed seconds:

| Field | Counted when |
| --- | --- |
| `Keystrokes` | `+= r.KeystrokeDelta` — the same anti-mash-coalesced count the economy uses |
| `MouseActiveSeconds` | `+1` for every tick with `r.MouseActive` |
| `ActiveSeconds` | `+1` for every tick whose mood was `coding` |
| `IdleSeconds` | `+1` for every tick whose mood was **not** `coding` |
| `SprintsCompleted` | `+1` per sprint rollover |
| `FocusSessions` | `+= r.FocusSessionsCompleted` |
| `AppSwitches` | `+1` per switch, **subject to `AppSwitchDailyCap = 40`** |
| `PausedSeconds` | `+1` per second spent paused (from `TickPaused`, never from `Tick`) |

Two of these are easy to misread:

- **`ActiveSeconds` is a mood measure, not a typing measure.** The mood is
  `coding` for a full 10 seconds after each keystroke, so a minute containing
  six well-spaced keystrokes can score close to 60 active seconds. It is
  "seconds during which you had recently typed", not "seconds you spent
  typing".
- **`AppSwitches` is always `0` on Linux**, which never reports an app. Shown
  honestly, with no special-casing.

`PausedSeconds` is its own third bucket and must never be folded into
`IdleSeconds`: idle means "observed, and doing nothing", paused means "not
observed". Together the three partition every second the runtime was up,
exactly once — `ActiveSeconds + IdleSeconds + PausedSeconds == uptime seconds`,
unit-tested in `stats_test.go`.

### Why analytics do not freeze while shopping

`recordStats` runs **before** the store gate in `Tick`, so the counters keep
accumulating while the store modal is open even though Dev Cash, progress and
the mood are all frozen. The stated reason: these are a passive tally of the
same already-honest signal the engine hands over every call, and shopping is a
few seconds *inside* a tracked session. There is no honesty reason to freeze
them — unlike the economy, which cannot know whether a keystroke was aimed at
the store, so must not claim work for it.

It also reads `r` directly rather than `g.Mood`, which `Tick` leaves stale
while the store is open. That is what keeps it honest instead of freezing on a
frozen mood.

Pause is the opposite case: there is no `TickResult` at all, because the engine
is never ticked, so pause cannot be — and is not — implemented as an early
return inside `Tick`. `TickPaused` credits the second to `PausedSeconds` and
nothing else.

### The day boundary

`rolloverStatsIfNewDay` compares `now().Local()` formatted as `"2006-01-02"`
against the date `statsToday` represents. When they differ:

1. the day that just **ended** is finalised into history and the streak, then
2. `statsToday`, `coinsToday` and `statsFocusBlockMax` reset to zero.

`statsLifetime` is never touched.

It fires from two call sites, which between them cover every case: from
`recordStats`, so an in-process midnight crossing rolls over on the very next
tick; and from `RestoreStats` on load, so a save reopened days later starts
today's bucket at zero immediately rather than resurrecting a stale "today".
`TickPaused` calls it too — a pause spanning midnight must still finalise the
day that ended.

A multi-day-gap reload finalises **only** the single last-running day. The
intervening days the process never ran produce no bucket at all: an honest gap,
never a fabricated zero.

---

## 3. The 30-day history

`HistoryRetentionDays = 30`. Storage and wire deliberately have different
shapes:

- **Persisted: sparse.** One `DayBucket` per local day that actually finalised,
  oldest first, pruned to at most 30. A day the process never ran has no
  bucket.
- **On the wire: dense.** `stats.history` always has exactly 30 entries, one
  per calendar date from today−29 to today inclusive, ascending, zero-filled
  for any date with no bucket. Built fresh on every `State()` call.

The final entry is always **today, live** — built from `statsToday` /
`coinsToday` / `statsFocusBlockMax` rather than from a finalised bucket, so it
grows through the day.

Each `DayStat` carries the eight counters plus `coinsEarned`,
`longestFocusBlockSeconds`, and `isActive`. `isActive` is computed
**server-side** by the same threshold the streak algorithm uses, so the client
can never drift from "what counts as active".

Calendar arithmetic uses `time.Time.AddDate` field math on date-only values,
never a 24-hour duration delta — which is what makes DST transitions and
month/year boundaries impossible to corrupt.

### One counter does not survive a restart

`statsFocusBlockMax` — today's longest focus block so far — has no `Restore`
counterpart and no field in the persisted save. Every other "today" counter
survives a restart; this one resets to 0. It reaches disk only once the day
finalises into a `DayBucket`, and it does reach the session record via the
active session's own accumulator.

Nothing in the code acknowledges this, and the A3 comments are explicit
wherever a gap *is* intentional, so **this looks like an oversight rather than
a design decision — but that reading is inference, not verified intent.**

---

## 4. Streaks

`ActiveDayMinSeconds = 300`. A local calendar date counts as **active** when
its `ActiveSeconds` reaches 300 — five minutes of `coding` mood, not one stray
keypress. (Per §2, that means five minutes of "having recently typed", which is
a lower bar than five minutes of typing.)

Three persisted values: `streakCurrent`, `streakLongest`,
`streakLastActiveDate`. They are updated at **one** place only — `finalizeDay`,
for a day that has already ended:

- an inactive finished day never extends the streak;
- an active day one calendar date after `lastActiveDate` extends the run;
- the same date finalising twice is a no-op (`finalizeDay` is idempotent for
  its date);
- any larger gap starts a fresh run of 1;
- `streakLongest` never decreases.

**Today is folded in at read time, without mutating anything.**
`effectiveStreak` decides the number the wire carries: the run is shown as
alive if `lastActiveDate` is today or yesterday, dead if it is two or more days
ago, and today's `+1` is added once today crosses 300 active seconds — with a
no-double-count guard for the case where `lastActiveDate` already is today.

A streak is **not** bounded by `HistoryRetentionDays`: it depends on
`lastActiveDate`, which is persisted cross-window state. That is precisely why
the client renders `stats.streak.current` and `.longest` verbatim and never
re-derives them from the 30-day window it was sent — it could not.

---

## 5. What resets, what persists, at a glance

| | Resets at local midnight | Survives a restart | Survives a pause |
| --- | --- | --- | --- |
| `stats.today.*` | **yes** | yes (unless the date is stale) | yes |
| `stats.lifetime.*` | no | yes | yes |
| `stats.coinsToday` | **yes** | yes | yes |
| today's longest focus block | **yes** | **no** — §3 | yes |
| `stats.history` (30 days) | rolls forward | yes | yes |
| `stats.streak` | recomputed at read | yes | yes |
| the active session | no — a session may cross midnight | yes (`SaveData.Session`) | **yes** — pause is "stop watching me", not "abandon my intention" |
| the session log | no | yes (chained-MAC rows) | yes |
| Dev Cash / XP / sprint progress | no | yes | yes, frozen |
| mood / active app | — | **no** — recomputed from the first real tick | cleared on pause |
| the per-signal work accumulators | on each sprint payout | **no** — never persisted | frozen |
| the cosmetic ticker / terminal buffers | no | **no** — rebuilt blank | keep scrolling |
