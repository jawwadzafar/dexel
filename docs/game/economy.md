# The economy — how activity becomes work

Every number on this page was read out of `app/internal/engine/engine.go`,
`app/internal/game/game.go`, `app/internal/game/coins.go` and
`app/internal/game/sprint.go`. The reasoning behind the calibration is
[ADR 0005](../adr/0005-anti-mashing-economy.md); the reasoning behind the A2
additions is [ADR 0012](../adr/0012-a2-content-free-signal-set-and-permission-fork.md).
This page is the *mechanics*, not the argument.

---

## 1. Every constant, with its real value

All of these live in one `const` block in `app/internal/engine/engine.go`,
deliberately — "all pricing is single-sourced".

| Constant | Value | What it does |
| --- | --- | --- |
| `KeystrokeWeight` | `1.0` | weight of one counted keystroke in the per-tick rate |
| `MouseWeight` | `0.25` | weight applied to the mouse-active signal |
| `MaxRecentRate` | `15.0` | ceiling on the weighted rate — a **backstop**, see §3 |
| `WorkPerUnitRate` | `0.008` | work units per unit of weighted rate |
| `FocusSessionSeconds` | `120.0` | how long a sustained-typing run must reach to pay out |
| `FocusGapToleranceSeconds` | `3.0` | the longest typing gap a run survives |
| `FocusSessionBonusWork` | `2.0` | flat work bonus per completed focus session |
| `AppSwitchWork` | `0.0` | work per counted app switch — **zero today**, see §5 |
| `AppSwitchDailyCap` | `40` | counted app switches per local day |
| `CodingRecencyWindow` | `10 * time.Second` | keystroke recency that means `coding` |
| `OnBreakIdleThreshold` | `30 * time.Second` | global idleness that means `onBreak` |

Two derived values, computed rather than hand-written so they cannot drift:

| Derived | Value | From |
| --- | --- | --- |
| `AntiMashSampleInterval` | `100 ms` | re-export of `activity.MouseSampleInterval` |
| `MouseSustainedRate` | `10.0` | `1.0 / AntiMashSampleInterval.Seconds()` |

Elsewhere:

| Constant | Value | File |
| --- | --- | --- |
| `activity.MouseSampleInterval` | `100 * time.Millisecond` | `app/internal/activity/provider.go` |
| `activity.MaxAppIDLen` | `32` | `app/internal/activity/sanitize.go` |
| `tickInterval` | `1 * time.Second` | `app/main.go` |
| `game.HistoryRetentionDays` | `30` | `game/history.go` |
| `game.ActiveDayMinSeconds` | `300` | `game/history.go` |

---

## 2. The formula

`engine.Engine.Tick()` runs once per second and computes, in this order:

```
weightedRate  = KeystrokeDelta × KeystrokeWeight            // 1.0 each
              + (MouseActive ? MouseSustainedRate × MouseWeight : 0)   // 10.0 × 0.25 = 2.5

weightedRate  = min(weightedRate, MaxRecentRate)            // 15.0

WorkUnits     = weightedRate × WorkPerUnitRate              // × 0.008
              + FocusSessionBonusWork × FocusSessionsCompleted   // + 2.0 per session
              + AppSwitchWork        × AppSwitches               // + 0.0 today
```

Which reduces to three prices a player can actually reason about:

| Signal | Work earned |
| --- | --- |
| one counted keystroke | **0.008** |
| one second with any mouse activity | **0.020** (`2.5 × 0.008`) |
| one completed focus session | **2.000** — the same as **250 keystrokes** |

Mouse activity is a *flag*, not a count: a second in which you moved the
mouse once earns exactly the same as a second in which you moved it
constantly. Keystrokes are a count, but a coalesced one (§3).

### `KeystrokeDelta` and the first tick

`KeystrokeDelta` is `Snapshot.KeystrokeCount` minus the previous tick's
value. On the first tick after start — or after
`engine.Engine.Reset()`, i.e. after a resume — the delta is forced to zero,
because there is no baseline to diff against. Without that guard a provider
that starts with a nonzero counter, or a restart, would hand out work it never
earned. `TestFirstTickNeverAwardsFreeWork` pins it.

---

## 3. The anti-mash clamp — where it actually lives

The important thing about the anti-mash property is that **most of it is not
in the economy at all**. It is enforced upstream, in the provider, by
coalescing (see [`activity-signal.md`](activity-signal.md) §1):

- at most **one counted keystroke per 100 ms** → `KeystrokeDelta` ≤ 10 per
  second,
- the mouse-active flag's maximum honest sustained rate is one per 100 ms,
  which is exactly where `MouseSustainedRate = 10.0` comes from.

So the highest weighted rate a real, honestly-coalesced provider can ever hand
the engine is:

```
10.0 (keystrokes) × 1.0  +  10.0 (mouse) × 0.25  =  12.5
```

`MaxRecentRate` is `15.0` — **above** that, on purpose. It is not the thing
limiting fast honest typing; it is a backstop against a fabricated or scripted
signal claiming a rate no coalesced provider could produce.
`TestMaxRecentRateIsABackstopNotASilentCap` asserts exactly this inequality
and fails if the ceiling ever regresses to sit at or below the real achievable
rate. This is easy to misread, so, plainly: **in normal play the clamp never
fires.**

The clamp does matter for the coin split, though. Because keystrokes and mouse
compete for the *same* clamped ceiling, `signalWork` in `game/coins.go` splits
the rate-derived work proportionally to their pre-clamp shares rather than
computing each independently — that is what keeps
`keyWork + mouseWork + focusWork + switchWork == r.WorkUnits`.

### The invariant, and where it stops holding

ADR 0005's headline property is "mouse-only earns strictly less than typing",
and it is unit-tested: `TestMouseOnlyEarnsLessThanTyping` asserts typing beats
mouse-only by at least 1.5×, and `TestStrategyComparisonA2` asserts the full
ordering `deepFocus > steadyTypist > mouseOnly > appSwitchMasher == idle`.

Both tests model the typist at **6 keystrokes per second**. That is the
assumption the invariant rests on, and it is worth stating explicitly because
the arithmetic inverts below it: a mouse-active second is worth 2.5
keystrokes, so **any typing rate below 2.5 counted keystrokes per second earns
less per second than simply having the mouse in motion.** Whether real usage
sits above or below that line is a measurement question, not a code question —
see [`BACKLOG.md`](BACKLOG.md) §3, which records the one real measurement we
have.

---

## 4. The focus-session bonus

The only signal added by phase A2 that actually earns anything.

A **sustained-typing run** is tracked in the engine by `focusRunActive` /
`focusRunStart`:

- a run **starts** on any tick with `KeystrokeDelta > 0`;
- it **restarts** (start time reset to now) if the gap since the last counted
  keystroke exceeded `FocusGapToleranceSeconds`;
- it **breaks** on a tick with no keystrokes when that same gap is exceeded;
- when the run's length reaches `FocusSessionSeconds`, this tick reports
  `FocusSessionsCompleted = 1`, `WorkUnits` gains `FocusSessionBonusWork`, and
  `focusRunStart` is reset to now — so a genuinely continuous run pays again
  every 120 seconds.

Mouse activity never sets `KeystrokeDelta`, so **mouse can never start, extend
or complete a run.** The mouse-below-typing invariant holds here by
construction rather than by a runtime check.

`FocusRunSeconds` on `TickResult` reports the current run's length every tick.
It has **no economy effect** — it exists so the game layer can track a per-day
and per-session "longest focus block".

### What the thresholds mean in practice

Ticks are one second apart, so `FocusGapToleranceSeconds = 3.0` means the run
breaks on the fourth consecutive second with no counted keystroke. To complete
one focus session you must therefore land a keystroke in at least one of every
three consecutive seconds, **without exception, for two full minutes**.

That is a strict requirement, and it is the subject of a recorded open
question: see [`BACKLOG.md`](BACKLOG.md) §3.

---

## 5. App-switch work — present, and worth nothing

`AppSwitches` is derived in the engine by diffing this tick's sanitised
`ActiveApp` against the previous tick's. It is `0` or `1`, always `0` on Linux
(which never reports an app), and never counts a switch into or out of Dexel's
own window.

`AppSwitchWork = 0.0`. **A counted app switch earns literally nothing.** This
is ADR 0012's "Fork B1": the switch is tracked and displayed, but does not
earn, so the economy stays identical across platforms even though only macOS
can see apps. `TestStrategyComparisonA2` asserts an app-switch masher earns
exactly `0` and exactly as much as doing nothing at all. Flipping to "Fork B2"
(macOS-first capped earning) is documented in the code as a one-constant
change to `0.1`.

`AppSwitchDailyCap = 40` is enforced in `game.Game.recordStats`, not in the
engine — the engine deliberately reports the switch uncapped and the
daily-aggregation layer decides. Once today's counter reaches 40, a further
switch that same local day is dropped entirely: not counted in today, not in
lifetime, and not folded into the work accumulator. **Today, with
`AppSwitchWork = 0.0`, that cap governs only a displayed counter.**

---

## 6. What caps exist, and what does not

This is worth stating flatly because it is easy to assume more structure than
exists. Searching the non-test source for a cap turns up exactly one:

| | Exists? |
| --- | --- |
| A daily cap on counted app switches (`AppSwitchDailyCap = 40`) | **yes** |
| A daily cap on keystroke work | **no** |
| A daily cap on mouse work | **no** |
| A daily cap on focus-session bonuses | **no** |
| A daily cap on Dev Cash or XP earned | **no** |
| A per-tick ceiling on the weighted rate (`MaxRecentRate = 15.0`) | yes, but it sits above the achievable maximum — §3 |

The anti-mash guarantee is therefore **entirely** a rate property (coalescing
plus weighting), not a budget property. There is no daily allowance and
nothing that runs out. `ActiveDayMinSeconds = 300` and
`HistoryRetentionDays = 30` are thresholds and window lengths, not caps.

The only other thing that stops the economy is the store gate (§8) and pause
(see [`surfaces.md`](surfaces.md)).

---

## 7. Work → Dev Cash → XP → levels

Work units do not convert to Dev Cash continuously. They accumulate as sprint
progress, and **completing a sprint is the only event that pays**
([ADR 0008](../adr/0008-upgrade-tracks.md)). Full details on
[`progression.md`](progression.md); the economically relevant summary:

- The six-sprint rotation totals **530 work units → 305 Dev Cash + 460 XP**.
- That is an average of **0.5755 Dev Cash and 0.8679 XP per work unit**.
- Overshoot carries into the next sprint, so no work is lost at a boundary.
- Level *n* is reached at `50·(n−1)·n` XP.

### What that costs in wall-clock time

The figures below are computed from the constants above, for a **50-unit first
sprint**, at the 6 keystrokes/second the engine's own tests model. They assume
continuous activity, which nobody sustains — read them as a scale, not a
prediction.

| Activity | Work / s | 50 units takes |
| --- | --- | --- |
| typing only, focus bonus never firing | 0.048 | ≈ 17 min 22 s |
| typing only, focus bonus firing every 120 s | 0.0647 | ≈ 12 min 53 s |
| typing + mouse active, no bonus | 0.068 | ≈ 12 min 15 s |
| typing + mouse active, bonus firing | 0.0847 | ≈ 9 min 51 s |
| **mouse activity only, continuous** | 0.020 | ≈ **41 min 40 s** |

The mouse-only figure reproduces ADR 0005's "~42 min per 50-work project"
exactly. Its companion figure, "real typing: ~21 min", corresponds to about
5.2 keystrokes/second with **no** focus bonus — which is consistent with the
5–10 keys/s range the ADR cites, but predates the A2 bonus and does not
include it. Neither ADR 0005 nor the calibration comment at the top of
`engine.go` has been restated since `FocusSessionBonusWork` was added; the
comment still says "real typing ~21 min per 50-work sprint" while the file it
sits in now also grants a 2.0-unit bonus. Treat the table above as the current
answer.

Emptying the whole store (6,370 Dev Cash — see [`progression.md`](progression.md))
is ≈ 11,069 work units: **36 to 64 hours of continuous typing**, depending on
which of the four typing rows you live on. Mouse activity alone would take
≈ 154 hours.

---

## 8. When the economy does not run

Three states, with three different rationales.

### The store gate — shopping must not count as work

While **any** connected client holds the store modal open, `game.Game.Tick`
returns early: `Mood`, `ActiveApp`, `Progress`, `DevCash` and the per-signal
work accumulators are all untouched, and the last honest mood is **held**. The
reasoning (`docs/ui-spec.md` §5.3) is the ADR 0010 rule applied to shopping:
the game cannot know whether a keystroke was aimed at the store, so it must
not claim work for it.

Two mechanical details:

- The gate is **refcounted by connection id** (`openStoreConns`), not a global
  bool, so one client's close or disconnect can never release a gate another
  client is still holding, and a client that reconnects mid-session leaves no
  stale entry.
- The server **still calls `engine.Engine.Tick()` every second** while the
  store is open, so the engine's own keystroke baseline keeps advancing.
  Otherwise a shopping burst of keystrokes would retroactively count as work
  the instant the store closed.

The analytics counters deliberately do **not** freeze here — see
[`sessions.md`](sessions.md) §2.

### Pause — Dexel stops observing

Pause is not a gate inside `Tick`; the engine is simply never ticked and the
provider is stopped. Only `PausedSeconds` advances. See
[`surfaces.md`](surfaces.md).

### A blind provider

A provider with `HonestyBlind` still produces `Snapshot`s, but a blind
provider reports no keystrokes and no mouse activity, so it earns nothing —
and the mood rules refuse to let it claim `onBreak`. See
[`moods.md`](moods.md).

---

## 9. Coin attribution — an accounting split, not a second payout

`stats.coinsToday` on the wire shows Dev Cash broken down by signal
(`keystrokes` / `mouse` / `focusSessions` / `appSwitches`). It is worth being
precise about what this is, because it looks like four income streams and is
not one.

There is exactly one coin source: a sprint payout. On every tick that actually
advances progress, `accrueWork` folds that tick's per-signal work shares into
four in-memory float accumulators. When a sprint completes, `awardCoins` takes
the integer Dev Cash that sprint just paid and splits **that same integer**
across the four accumulators in proportion to the work each contributed, then
resets them.

- The split uses the **largest-remainder method** so the four integers always
  sum to exactly the Dev Cash paid — coin conservation, unit-tested by
  `TestCoinBreakdownSumsToEarnedDevCash`, with a belt-and-braces
  exact-conservation adjustment after the float arithmetic.
- The four work accumulators are gated by the same `StoreOpen` check as
  progress itself. A tick that cannot advance progress must not inflate the
  accumulators either, or the next payout would attribute coins to work that
  never earned any (`TestWorkAccrualGatedByStoreOpenLikeProgress`).
- The accumulators **never cross the wire and never reach disk**. Only the
  resulting integer breakdown does.
- There is a `today` coin breakdown and no lifetime one, by design.

Because `AppSwitchWork` is `0.0`, the `appSwitches` coin bucket is always `0`
in practice today.
