# Resilience bug hunt — the long-running runtime vs a sleeping machine

**Date:** 2026-08-25 · **Posture:** independent, adversarial, code-read only.

**Scope:** the family of assumptions that were fine for an hour-long dev run and
are now suspect because the runtime is started by autostart and lives for days —
clock semantics across suspend, tickers after resume, the day rollover,
WS/frontend after resume, discovery, SQLite over long uptimes, the darwin
equivalent of today's field bug, and log/fd/memory growth.

**Deliberately NOT in scope:** today's field bug (GNOME screen-lock
re-enumerated `/dev/input`, the provider held dead fds for 19h, idle accrued
falsely). That fix is owned by another agent and is already in the tree
(`app/internal/activity/provider_linux.go:9-26, 212-238, 337-343, 469-476`).
This document hunts its **siblings**. Two findings (R5, R6b) sit next to that
fix; both are called out as coordination points, and neither is the dead-fd
logic.

## Environment caveat — read this before trusting any number

**No experiment could be run.** Command execution is dead in this environment:
every `Bash` call returns a bare exit code with no stdout. Two subagents pinned
the cause independently — a 2-byte file write returns **`EDQUOT` (disk quota
exceeded)**, so the harness's own output-capture file can be created but never
written. `go build`, `go test`, a probe binary, a `SIGSTOP`/`SIGCONT` run of the
real server and a WebSocket client were all attempted and all impossible.

So **everything below is a code trace with file:line evidence, not a
measurement.** No value here was observed at runtime. Where a claim depends on
kernel or platform behaviour it says so, and §7 gives the exact probes that must
be run. Nothing is taken from a design doc's claim about itself; the two places
where a design doc's recorded conclusion is **contradicted** are called out
explicitly in §1.

---

## 0. The mechanism behind R1-R4

Three facts in combination:

1. **A Go `time.Time` from `time.Now()` carries two clocks** — a wall reading
   and a monotonic reading. `Sub`, `Since`, `Before`, `After` use the
   **monotonic** reading *if both operands have one*, and silently fall back to
   the wall clock if either does not. `Add` preserves it; `time.Parse`,
   `Round(0)` and any marshal round-trip **strip** it.
2. **On Linux, `CLOCK_MONOTONIC` does not advance during suspend** — that is
   `CLOCK_BOOTTIME`. Go's runtime reads `CLOCK_MONOTONIC`. A monotonic delta
   across an 8-hour sleep therefore measures **awake time only**.
3. **Go timers/tickers are armed on the monotonic clock**, so they do not fire
   during suspend at all.

The result is not "everything is broken" — it is worse, because it is
**inconsistent**. Session timestamps that live in memory carry a monotonic
reading; the same timestamps after a save/restore round-trip come back through
`time.Parse` (`app/internal/store/store.go:483-484` writing RFC3339,
`store.go:738-743` parsing it) and carry **none**. So one expression measures
awake time for a live session and wall time for a restored one. The restart path
self-heals; the suspend path does not. That asymmetry is R1/R2. There is no
`.Round(0)` anywhere in `app/` (grepped: zero hits), so this is never
deliberate — it is an accident of which path the value took.

### Inventory: every time comparison in the runtime, and the clock it uses

| Site | Expression | Clock in effect | After an 8h suspend | Verdict |
|---|---|---|---|---|
| `engine/engine.go:254` | `now.Sub(prevKeystrokeAt) > FocusGapToleranceSeconds` | mono | gap reads ~1s, not 8h | **R3** |
| `engine/engine.go:259` | `now.Sub(e.focusRunStart) >= FocusSessionSeconds` | mono | run continues through the hole | **R3** |
| `engine/engine.go:263` | run-break check, same delta | mono | run never breaks | **R3** |
| `engine/engine.go:290` | `FocusRunSeconds` | mono | awake-only length reported | R3 (cosmetic half) |
| `engine/engine.go:317` | `now.Sub(e.lastKeystrokeAt) <= CodingRecencyWindow` | mono | claims `coding` on resume | **R4** |
| `game/session.go:371` | `now.Sub(s.lastActivityAt) >= 2h` (idle auto-end) | mono **live**, wall **restored** | never fires across suspend | **R1** |
| `game/session.go:376-377` | `now.Before(s.startedAt.Add(16h))` (`Add` keeps mono) | same split | 16h cap = 16h **awake** | **R1** |
| `game/session.go:386` | `endAt.After(capBoundary)` | same split | cap ceiling in awake time | R1 |
| `game/session.go:195` | `d := end.Sub(start)` (`durationSecondsBetween`) | same split | duration excludes suspend | **R1/R2** |
| `game/session.go:646` | `ElapsedSeconds: durationSecondsBetween(s.startedAt, g.now())` | same split | pill freezes, jumps on restart | **R2** |
| `game/game.go:826` | `g.now().Local().Format("2006-01-02")` | **wall** (right) | rolls over on first tick back | C1 — but see R7 |
| `game/game.go:747-771` | tick-counted `ActiveSeconds`/`IdleSeconds` | n/a (counts ticks) | contributes zero | C6 |
| `game/pause.go` `TickPaused` | tick-counted `PausedSeconds` | n/a | undercounts a pause spanning sleep | **R8** |
| `activity/provider_linux.go:545` | `IdleSeconds: time.Since(p.lastAnyInput)` | mono | under-reports idle after resume | F1 |
| `activity/provider_linux.go:486,491` | anti-mash coalescing | mono | correct on mono | fine |
| `activity/provider_darwin.go:330,334` | anti-mash coalescing | mono | correct on mono | fine |
| `activity/provider_darwin.go:282-288` | `CGEventSourceSecondsSinceLastEventType` | OS-maintained | undocumented across sleep | **F2** |
| `activity/provider_fake.go:191` | `time.Since(p.startedAt)` | mono | script freezes with the machine | fine (dev only) |
| `lifecycle_handlers.go:155` | `time.Since(startedAt)`, `startedAt` from `time.Parse` | **wall** (right) | counts the sleep, as a human expects | C9 |
| `cmd_lifecycle.go:583-587` | `dexel status` uptime, same parse | **wall** (right) | same | C9 |
| `main.go:400` | fallback `lifecycleStartedAt = time.Now()` | mono | uptime silently becomes awake-time | N1 |
| `main.go:437-443` | four `time.NewTicker`s | mono-armed | stall, then resume; no burst | C6 |
| `cmd_lifecycle.go:257,268,416,421` | CLI poll deadlines | mono | correct — a timeout *wants* mono | fine |
| `game/activity_line.go:237` | phrasing reroll bucket | wall bucket | one extra reroll on resume | fine |

---

## 1. What the project already knew, and where this report contradicts it

Prior art exists and must be read alongside these findings, because **one
recorded conclusion is incomplete in a way that matters.**

`docs/production-runtime/PLATFORM_NOTES.md:847-850`:

```
* **Suspend/resume.** After a laptop sleeps, `time.Ticker` does not fire for the
  missing hours; the 1 Hz loop simply resumes. Nothing accrues for the sleep
  (correct — nobody was typing) and no seconds are counted into any bucket
  (also correct). Worth a test, not a design change.
```

Both sentences are **true** — this report confirms them as C6. But they only
cover what happens *during* the gap. They say nothing about **state that was
carried across the gap and is then compared against a clock that did not
advance**, which is where R1-R4 live. "Not a design change" is therefore the
wrong conclusion, and it is the reason nothing was ever fixed here: the note
closed the question. Also worth recording: the "worth a test" was **never
written** — there is no suspend/resume test of the tick loop anywhere in the
tree, and `CLOCK_MONOTONIC`/`BOOTTIME`/"monotonic clock" appear **nowhere** in
the repo.

`docs/production-runtime/PLATFORM_NOTES.md:841-846` flags the day boundary
("a laptop that changes zone mid-flight hits that code far more often... the
first place to look if a long-running instance reports a weird day"). R7 is that
prediction coming true, with the mechanism identified.

`docs/production-runtime/LINUX-VERIFICATION.md:857-859` already lists
suspend/resume + midnight rollover as **unverified** ("a container that never
sleeps cannot exercise either"). Still unverified — see §7.

`docs/plan/P2-design.md:1160-1163` accepts a related limitation, and this is the
load-bearing sentence for R1:

```
- **A wall-clock duration can exceed observed time** when the process was closed
  mid-session. Honest by design (ticks are the only thing Dexel can count), and
  bounded by the 2 h idle rule and the 16 h cap.
```

The acceptance is explicitly conditional on those two bounds. **R1 shows neither
bound holds across a suspend**, so the accepted limitation is unbounded on a
laptop.

---

## 2. Verdict summary

| Rank | # | Finding |
|---|---|---|
| REAL-BUG | R1 | A session's 2h idle auto-end and 16h hard cap are measured on the **monotonic** clock, so **neither can fire across a suspend**. A session left open over a weekend never ends; the bound P2-design relies on to make "wall duration > observed time" acceptable does not exist on a laptop. |
| REAL-BUG | R2 | `sessions.active.elapsedSeconds` **freezes across a suspend, then jumps by the whole gap at the next restart** — the identical expression measures awake time live and wall time restored. |
| REAL-BUG | R3 | A sustained-typing focus run **survives an arbitrarily long suspend** and pays `FocusSessionBonusWork`. This is verbatim the failure `Engine.Reset`'s own doc comment describes for pause — the guard exists, nothing calls it on resume-from-suspend. |
| REAL-BUG | R4 | For ~10s after every resume the engine claims `activeState: "coding"` (crediting `ActiveSeconds`, scrolling the terminal) on the strength of a keystroke that is 8 hours old in wall time. ADR 0010's forbidden claim, delayed. |
| REAL-BUG | R5 | `statsToday.IdleSeconds` still accrues **while the provider is BLIND** — the analytics half of today's field bug, untouched by the provider-side fix. Lands in `internal/game`, not `internal/activity`. |
| REAL-BUG | R6 | The 8 MiB log rotation **never runs at all** under the two supervised autostart mechanisms (launchd and systemd exec `runtime`, not `start`), and the newly-landed rescan logging is per-event, so one pathological input node writes ~2 lines/s forever into a file nothing will rotate. |
| REAL-BUG | R7 | A **backward local-date step** (flying west; a bad RTC corrected after resume) makes `rolloverStatsIfNewDay` finalize a **duplicate** `DayBucket`; the wire view collapses by date last-write-wins, so the partial bucket **overwrites** the full day — and a long streak resets to 1. |
| REAL-BUG | R8 | The documented, tested invariant `activeSeconds + idleSeconds + pausedSeconds == seconds the runtime was up` is **false across a suspend** by exactly the sleep duration. The test that guards it defines uptime as the tick count, so it cannot fail. |
| REAL-BUG | R9 | `runtime` binds an **ephemeral** port, and both supervisors restart it on crash — so after any restart every open window (browser tab *and* Tauri shell) retries the **dead** port forever and never recovers. The Tauri shell resolves the URL once at startup and never re-resolves. |
| FIELD-TEST | F1 | Whether Linux resume kills the evdev readers (masking the frozen `lastAnyInput`) or a built-in keyboard survives and exposes it. |
| FIELD-TEST | F2 | macOS: `CGEventSourceSecondsSinceLastEventType` across sleep/lock/wake, and whether `CGWindowListCopyWindowInfo` reports the lock screen (fabricated app switches). The dead-handle question itself is **answered and clean** — see F2. |
| FIELD-TEST | F3 | Real-resume ticker behaviour (expected: exactly one tick, no catch-up burst) plus the four clock probes this environment could not run. |
| FIELD-TEST | F4 | That the loopback WS genuinely survives suspend and the pill self-corrects from the next broadcast. |
| CONFIRMED-FINE | C1-C9 | day-rollover attribution, no false accrual during sleep, the frontend's server-authoritative elapsed, discovery vs pid reuse, SQLite durability/growth, memory growth, the darwin dead-handle question, wall-based uptime. |
| NIT | N1-N3 | the uptime fallback clock, the 5s broadcast write on the owner loop, no WS keepalive. |

---

## 3. REAL BUGS

### R1 — A session's idle auto-end and 16h cap cannot fire across a suspend

`app/internal/game/session.go:365-390`, driven from `game.go:627,639`:

```go
now := g.now()                                   // game.go:627 — time.Now(), carries mono
g.checkSessionAutoEnd(r, now)                    // game.go:639
...
if r.SeesGlobalInput() && now.Sub(s.lastActivityAt) >= SessionIdleTimeoutSeconds*time.Second {
        g.finishSession(s.watermark, s.lastActivityAt, endReasonIdle)     // session.go:371
        return
}
capBoundary := s.startedAt.Add(SessionMaxDurationSeconds * time.Second)   // session.go:376
if now.Before(capBoundary) { return }                                    // session.go:377
```

`startedAt`/`lastActivityAt` are stamped from `g.now()` (`session.go:227-232`,
`session.go:404`), so **both operands carry monotonic readings and both
comparisons are monotonic.** A suspend contributes zero.

Concrete outcome — laptop, session left open on a Friday:

- 18:00 last keystroke, lid closed. Monday 09:00 lid opened.
- The monotonic delta across the weekend is the handful of awake seconds around
  lid close, so `now.Sub(s.lastActivityAt)` never approaches 7200 → **no idle
  auto-end.**
- `capBoundary` is `startedAt.Add(16h)` and `Add` preserves the monotonic
  reading, so the "hard cap no honest workday reaches" is a cap on **16 hours of
  awake time** — three or four calendar days for a laptop user.
- When the session eventually ends, the record has `StartedAt` Friday,
  `EndedAt` Monday-or-later, and `DurationSeconds` equal to the awake seconds
  only (`session.go:195`). `EndedAt − StartedAt` and `DurationSeconds` disagree
  by the whole weekend, and `sessions.summary.longestSessionSeconds` /
  `thisWeek` are derived from those values (`session.go`'s `sessionsSummary`).

The design intends the opposite, and says so twice: the idle timeout is defined
as "this many seconds pass with no real observed input" (`session.go:44-55`), and
`checkSessionAutoEnd`'s own doc comment promises a stale session "auto-ends on
the very first tick that notices, backdated honestly". That promise **is** kept
after a process restart — precisely *because* the reloaded timestamps come back
from `time.Parse` (`store.go:738-743`) without a monotonic reading, so the same
comparison silently switches to wall time. It is broken for a suspend, where the
in-memory values still carry one.

Severity, stated honestly: nothing false is *accrued* during the sleep (no ticks
run — C6). The damage is (a) the two bounds P2-design leans on do not exist,
(b) session records whose two timestamps and duration contradict each other, and
(c) an "active session" the user abandoned days ago still holding the pill.

**Fix sketch.** Make session bookkeeping wall-clock **by construction**: strip
the monotonic reading where session timestamps are *stored* — `startedAt:
now.Round(0)` in `StartSession` (`session.go:227-232`) and `s.lastActivityAt =
now.Round(0)` in `advanceSessionActivity` (`session.go:404`). Every downstream
comparison then behaves exactly as it already does for a restored session, the
2h/16h bounds mean what §2.5 says, and the live/restored asymmetry disappears.
`durationSecondsBetween` already floors at 0, which is the guard a backward wall
step needs. Owner: `app/internal/game/session.go`. Exit test: with the existing
`fakeClock` helper (`session_test.go:36-48`; the clock is injected by assigning
the unexported `g.now`), advance 8h **without ticking** and assert the idle
auto-end fires backdated — the test `PLATFORM_NOTES.md` §7 said was worth
writing.

### R2 — The session pill freezes across a suspend, then jumps hours on restart

Same root as R1; this one is on screen. `session.go:645-646`:

```go
StartedAt:      s.startedAt.Format(time.RFC3339),                 // wall part — correct
ElapsedSeconds: durationSecondsBetween(s.startedAt, g.now()),     // monotonic — awake only
```

The wire frame is internally inconsistent once a suspend has happened:
`startedAt` says 09:00 yesterday while `elapsedSeconds` says 1800. The frontend
is not at fault and cannot compensate — it renders the server's number verbatim
and never derives time locally (`frontend/src/render/chrome.ts:88-92`,
`features/sessions-modal.ts:154`, `wire.ts:186`), which is exactly why the
server's number is the only thing that can be wrong.

User-visible sequence: start a session, work an hour, sleep overnight, wake →
pill still reads `1:00:xx`. Then the runtime restarts (an update, a reboot, a
crash-restart) → the reloaded `startedAt` has no monotonic reading, the same
expression switches to wall, and the pill jumps to `14:32:xx` mid-session.
Fixed by R1's fix, which makes both halves wall.

### R3 — A focus run survives an arbitrarily long suspend and pays the bonus

`app/internal/engine/engine.go:250-266`:

```go
gapExceeded := !prevKeystrokeAt.IsZero() && now.Sub(prevKeystrokeAt).Seconds() > FocusGapToleranceSeconds
if !e.focusRunActive || gapExceeded { e.focusRunActive = true; e.focusRunStart = now }
if now.Sub(e.focusRunStart).Seconds() >= FocusSessionSeconds { focusSessionsCompleted = 1; e.focusRunStart = now }
```

Both deltas are monotonic. Across a suspend `now.Sub(prevKeystrokeAt)` is the
**awake** gap only — typically 1-3 seconds, because the normal shape is "type,
close the lid" then "open the lid, type" (and on GNOME the unlock password is
itself real evdev input). With `FocusGapToleranceSeconds = 3.0`, `gapExceeded`
is false, `focusRunActive` stays true, `focusRunStart` is never moved — so a run
holding 118 awake seconds before the lid closed **completes two seconds after
wake**, folding `FocusSessionBonusWork = 2.0` into `WorkUnits`
(`engine.go:259, 279-283`) plus a `FocusSessions` increment in today's stats and
in the open session's delta.

Scale: a sprint target is 50 work units and one keystroke-driven tick is capped
at `MaxRecentRate * WorkPerUnitRate = 0.12`, so 2.0 unearned units ≈ 17 seconds
of genuine hard typing per suspend crossing. Small per event, unbounded over
days, and categorically a false claim rather than a rounding error.

This is a **known** bug on an unwired path. `Engine.Reset`'s doc comment
(`engine.go:335-345`) lists it for the pause seam:

```
//   - focusRunActive/focusRunStart — and pay a FocusSessionBonusWork bonus
//     for a "sustained" typing run with a ten-hour hole in the middle;
```

`Reset()` is called only from the RESUME action branch in `app/main.go`. A system
suspend is the same ten-hour hole with nobody to call it.

**Fix sketch — one detector serves R3, R4 and R8.** Stdlib only, because a
single `time.Now()` carries both clocks:

```go
// in main.go's 1s tick branch, before eng.Tick()
now := time.Now()
mono := now.Sub(prevTick)                  // both carry mono -> awake time
wall := now.Round(0).Sub(prevTickWall)     // mono stripped   -> wall time
if gap := wall - mono; gap > suspendThreshold /* e.g. 5s */ {
        log.Printf("resumed after ~%s of suspend/clock jump: resetting engine recency state", gap)
        eng.Reset()          // R3 + R4
        g.NoteSuspendGap(gap) // R8: credit the gap to its own bucket
}
prevTick, prevTickWall = now, now.Round(0)
```

Owner: `app/main.go` for the detector and the `eng.Reset()` call;
`internal/engine` needs no change (`Reset` already clears exactly the right
four fields). Note the detector also fires on a large NTP step, which is the
correct response there too.

### R4 — `coding` is claimed for ~10 seconds after every resume

`app/internal/engine/engine.go:315-321`:

```go
if !e.lastKeystrokeAt.IsZero() && now.Sub(e.lastKeystrokeAt) <= CodingRecencyWindow {
        return MoodCoding
}
```

Monotonic again. If the last pre-sleep keystroke was within
`CodingRecencyWindow = 10s` of monotonic time, the first tick after resume
reports `coding` with no input at all, and keeps doing so for 10 awake seconds.
Downstream: `statsToday.ActiveSeconds` and the open session's `activeSeconds`
are credited for seconds nobody was there, `AdvanceTerminal` scrolls fake
compile output (`game.go:895-905`), and the activity line asserts "Coding in
<app>". `Reset()`'s doc comment names this one too — *"a keystroke observed ten
hours ago is not 'coding right now'"* (`engine.go:340-342`). Fixed by R3's
detector.

### R5 — `IdleSeconds` still accrues while the provider is BLIND

The surviving **analytics half** of today's field bug, in a file the in-flight
fix does not own. `app/internal/game/game.go:759-771`:

```go
if r.Mood == engine.MoodCoding {
        g.statsToday.ActiveSeconds++
        g.statsLifetime.ActiveSeconds++
} else {
        // Idle or OnBreak both count as "not coding right now" for this tally
        g.statsToday.IdleSeconds++
        g.statsLifetime.IdleSeconds++
}
```

No honesty gate. The provider fix makes a blind provider report
`IdleSeconds: 0` with `HonestyBlind` (`provider_linux.go:533-541`), and the
engine's ADR 0010 gate then refuses `MoodOnBreak` — so the mood is `MoodIdle`,
and this `else` credits one idle second **per tick for the entire blind
stretch**. Replay today's failure against the fixed provider: the 19-hour
dead-fd window would still have added ~19 hours to `statsToday.IdleSeconds`, to
lifetime, and to the open session's `idleSeconds` delta. The claim "you were
idle for 19 hours" survives the fix; only the `onBreak` **mood** claim was
removed.

The same branch is hit after every resume on Linux: if the USB readers die, the
provider is blind for up to `defaultRescanInterval = 15s`
(`provider_linux.go:79-86`) before recovery.

**Fix sketch.** Gate the tally on the honesty bit that is already on
`TickResult`:

```go
switch {
case r.Mood == engine.MoodCoding:      // observed work
        ...
case !r.SeesGlobalInput():             // could not see -> not idleness
        // count nothing (cheap), or g.statsToday.UnobservedSeconds++ (honest)
default:
        g.statsToday.IdleSeconds++
}
```

Cost note, from `session.go`'s own warning on `subtractCounters`: a new
`StatCounters` field is **not** free — it must be added to `subtractCounters`,
`ActiveSessionView`, `SessionView`, `StatCountersSave` and a schema bump, or it
is silently dropped from every session. The count-nothing variant needs none of
that and is already strictly more honest than today. Owner:
`app/internal/game/game.go` (+ `session.go`/`store` only if the new bucket is
chosen). **Requires nothing from `internal/activity`.**

### R6 — Log rotation never runs under the supervised autostart paths

Two halves that multiply.

**(a) The two supervised mechanisms bypass rotation entirely.**
`lifecycle.RotateLog` (`internal/lifecycle/logfile.go:20-52`) has exactly one
production caller: `cmd_lifecycle.go:188-194`, inside `(*cliEnv).start`. But:

- systemd unit: `ExecStart=%s runtime` with `Restart=on-failure`
  (`internal/autostart/systemd_linux.go:64-82`)
- launchd plist: `ProgramArguments = [<exe>, runtime]`, `RunAtLoad`,
  `KeepAlive{SuccessfulExit:false}` (`internal/autostart/autostart.go:192-225`)

Both exec **`runtime`**, not `start` — so on macOS and on systemd Linux (the
primary autostart paths) `RotateLog` is **never called, ever**, not even at
login, not even on a crash-restart. Only the XDG-autostart entry
(`xdg_linux.go:32-41`) and the Windows Run key
(`autostart.go:581-593`) go through `start`. `start` also returns early when a
runtime is already running (`cmd_lifecycle.go:179-182`), so even there the check
only happens when nothing is up.

The accepted limit as written (`PLATFORM_NOTES.md:756-763`) rests on "the
runtime is nearly silent at steady state", and explicitly reasons from
`app/main.go` alone — which is fair: `main.go` logs ~10 startup lines and then
only on failure (`main.go:448` on autosave failure, `:589`, `:614`). It does not
account for the provider.

**(b) The new rescan logging is per-event, not per-transition.** In the case
`defaultMinRescanInterval`'s own doc comment anticipates
(`provider_linux.go:81-86`: "a node that fails its very first read forever...
degrades to one open/read/close per second"), each cycle emits **two** lines —
`deviceDied` (`provider_linux.go:472-475`) and `openMissing`'s "rescan opened N
new input device(s)" (`provider_linux.go:357-362`). At ~2 lines/s and ~130-200
bytes/line that is **~20-35 MB/day**, i.e. past the 8 MiB cap in under half a
day, into a file that — per (a) — nothing will ever rotate. A normal
suspend/lock cycle is cheap by comparison (N death lines + 1 rescan + 1
RECOVERED), but it happens many times a day, forever.

**Fix sketch.** (b) log **state transitions** only (blind→sighted,
sighted→blind, and a per-path "died" line rate-limited to once per N minutes) —
a small change in `internal/activity`, and **this is the coordination point with
the in-flight fix: it is that fix's logging, not its dead-fd logic.** (a) either
have the runtime own and reopen its own log so it can rotate in-process on a slow
timer, or make the systemd/launchd units exec `start` instead of `runtime`, or
amend the accepted-limit paragraph to say the truth (no rotation on the mac and
systemd paths). Note rotation cannot be bolted on from *outside* a running
runtime: the child holds the log as inherited stdout/stderr, so after a rename it
keeps writing to the same inode now called `.1` — the same hazard
`TruncateLog`'s doc comment already documents for unlinking. No fd leak in the
rescan loop: `openMissing` closes every fd that loses its key check
(`provider_linux.go:320-332`) and `deviceDied` closes the dead one.

### R7 — A backward local-date step duplicates a history bucket and resets the streak

Not suspend-specific, but squarely "a changing system", and the case
`PLATFORM_NOTES.md:841-846` predicted. `rolloverStatsIfNewDay`
(`game.go:825-838`) keys purely on string inequality:

```go
today := g.now().Local().Format(statsDateFormat)
if g.statsDate == today { return }
if g.statsDate != "" { g.finalizeDay(g.statsDate, g.statsToday, ...) }
```

If the local date moves **backwards** — flying west across a date boundary, or a
wrong RTC on resume that NTP then corrects — this finalizes the current partial
day, sets `statsDate` to the earlier date, and zeroes today's buckets. When the
clock/TZ moves forward again the same date is finalized a **second** time with
only the post-step counters. `finalizeDay` (`history.go:100-110`) appends
unconditionally, and the wire builder collapses by date with last-write-wins
(`history.go:203-206`):

```go
byDate := make(map[string]DayBucket, len(g.history))
for _, b := range g.history { byDate[b.Date] = b }
```

so the fuller bucket is **overwritten by the partial one** in every `state`
message and in the History modal. Meanwhile `updateStreak`
(`history.go:111-142`) sees a date that is neither `lastActiveDate` nor
`lastActiveDate + 1`, hits `default`, and sets `streakCurrent = 1` — a 40-day
streak gone.

**Fix sketch.** Make `finalizeDay` an upsert-and-merge on date rather than a
blind append (or refuse to finalize a date that already has a bucket, keeping the
larger `ActiveSeconds`), and make `updateStreak` ignore a date **earlier** than
`streakLastActiveDate` instead of treating it as a gap. Owner:
`app/internal/game/history.go`; `history_test.go` already drives dates as
strings, so both tests are cheap.

### R8 — The documented uptime partition is false across a suspend

This is a claim the project states three times and tests once.

`docs/production-runtime/ARCHITECTURE.md:476-492` (Decision 14):

```
* **Invariant, unit-testable:** for any bucket,
  `activeSeconds + idleSeconds + pausedSeconds == seconds the runtime was up
  during that bucket`.
```

Restated at `docs/production-runtime/MIGRATION_PLAN.md:197-202` (PR-5's exit
criterion), at `docs/ui-spec.md:899-904`, and on the struct itself
(`game.go:159-165`).

Every one of those three counters is incremented **per tick**
(`game.go:747-771`, `pause.go`'s `TickPaused`), and ticks do not happen during a
suspend (C6). So for any day containing a sleep, the left side counts awake
seconds and "seconds the runtime was up" — the wall-clock quantity a human, and
`/api/lifecycle/status`'s wall-based `uptimeSeconds` (C9), both mean — is larger
by exactly the sleep. Pause a dexel, close the lid overnight, resume in the
morning: `pausedSeconds` records almost none of it.

The guarding test cannot catch this, by construction. `pause_test.go:11-21`
defines uptime as the tick count:

```go
// It returns the number of seconds the runtime was "up",
// which for a 1Hz tick is simply the tick count: this helper IS the
// definition of uptime the invariant below is asserted against.
```

and `pause_test.go:435-441` asserts the session-level version against
`ElapsedSeconds`, which passes today only because the fake clock advances
exactly one second per tick — no test anywhere drives the clock forward without
ticking. (Note the interaction with R1: while live, `ElapsedSeconds` is *also*
monotonic, so the session-level partition accidentally still holds across a
suspend; it breaks after a restart, when elapsed becomes wall-based and the
counters are still ticks.)

**Fix sketch.** With R3's detector in hand, either (a) credit the measured gap
to its own `suspendedSeconds` bucket via `g.NoteSuspendGap(gap)` and restate the
invariant as `active + idle + paused + suspended == wall uptime` — honest, and
the same StatCounters-field cost R5 notes — or (b) amend all three documents to
say "seconds the runtime was **awake**" and add the missing test that a suspend
gap does not violate it. (b) is free; (a) is what a user reading "pausedSeconds"
would expect. Owner decision, not a silent fix. Owner of the code either way:
`internal/game` + the three docs.

### R9 — After any runtime restart, every open window is permanently dead

The runtime binds an **ephemeral** port by design — `main.go:73-83`:

```go
// defaultAddr: `runtime` defaults to an OS-assigned ephemeral port
// because a background runtime nobody typed a port for should never fight
// another process for 8080
func (m serveMode) defaultAddr() string {
        if m == modeRuntime { return "127.0.0.1:0" }
        return "127.0.0.1:8080"
}
```

And both supervisors restart it: systemd `Restart=on-failure` + `RestartSec=5`
(`systemd_linux.go:64-82`), launchd `KeepAlive{SuccessfulExit:false}`
(`autostart.go:192-225`). So a crash — which is what a multi-day uptime makes
statistically real — produces a healthy new runtime **on a different port**.

Nothing reconnects to it:

- The page's WS client derives its URL from `location.host`
  (`frontend/src/state/ws-client.ts:105-112`) and retries forever with a
  500 ms → 8 s capped backoff (`ws-client.ts:134-139`) against the **dead** port,
  showing `RECONNECTING...` (`render/overlays.ts:11-12`).
- The Tauri shell resolves the URL **once**, at startup, via `dexel status
  --json` (`desktop/src-tauri/src/lib.rs:398-426`, `459-473`) and its run-event
  handler is deliberately empty (`lib.rs:476-486`), so it never re-resolves and
  never navigates the webview.

Result: the companion window is a permanently dead grey box while a perfectly
healthy runtime is up on another port, and the only recovery is for the user to
close and reopen the window. Data is safe; the UI silently is not. This is
distinct from F4 (a plain suspend, where the loopback socket and the port both
survive).

**Fix sketch.** Two independent halves, either of which fixes the common case:
(a) make the port sticky — persist the last bound port (runtime.json already
carries it, `internal/lifecycle/runtimefile.go:77-82`) and try to rebind it at
startup, falling back to ephemeral if taken; owner `app/bootstrap.go`. (b) make
the shell re-resolve: on repeated WS failure (or on window focus), re-run
`status --json` and navigate the webview to the new URL; owner
`desktop/src-tauri/src/lib.rs`. (a) also fixes a bookmarked browser tab, which
(b) cannot.

---

## 4. FIELD-TEST-NEEDED

### F1 — Does Linux resume kill the evdev readers, and does that mask the frozen idle clock?

`provider_linux.go:545` computes `IdleSeconds: time.Since(p.lastAnyInput)` —
monotonic, so after a resume it reports only the awake gap, i.e. it
**under**-reports idle. That is the safe direction (it cannot manufacture an
`onBreak`), but whether it happens at all depends on hardware:

- If USB re-enumeration kills every reader, the provider goes blind and on
  recovery deliberately resets the clock — `p.lastAnyInput = time.Now()`
  (`provider_linux.go:343`) — so the stale value is discarded and this is a
  non-issue. It also means a blind window of up to 15s after every resume, which
  is R5's other trigger.
- A built-in i8042/PS2 keyboard node typically does **not** die across suspend.
  If the surviving set is non-empty there is no recovery reset, and the stale
  `lastAnyInput` is what the engine sees.

**Measure:** `dexel logs -f`, suspend 5 minutes, resume. Record (a) whether
`input device ... died` / `RECOVERED` lines appear, (b) how many of N devices
survive, (c) `activeState` on the first ticks after resume. That one run also
confirms or refutes R4 in the field, and it is the run
`LINUX-VERIFICATION.md:857-859` already lists as never done.

### F2 — macOS: the darwin equivalent of today's bug, plus its own sleep questions

**The dead-handle question is answered, and the answer is clean.** The darwin
idle path holds **no** handle and enumerates **no** devices: every sample is a
fresh, stateless `CGEventSourceSecondsSinceLastEventType(kCGEventSourceStateHIDSystemState, …)`
through the cgo shim (`provider_darwin.go:56-62`, called at `:282-288`). There is
no fd to go stale and no device list to become wrong, so there is **no analogue
of the Linux dead-fd failure** (CONFIRMED-FINE by code read). Its whole state is
`keystrokeCount` plus three `time.Time`s under a mutex, and `Stop()` joins the
poll goroutine cleanly (`:249-263`).

Two darwin-specific things still need hardware:

1. **A fabricated keystroke on wake.** The coalescing rule counts a keystroke
   whenever the OS reports `keyIdle < pollWindow` (50 ms) — `:330-333`. Apple
   does not document how "seconds since last event" behaves across system
   sleep; if a wake presents a near-zero idle, that mints a keystroke, which
   means work units and `MoodCoding`. **Measure:** log `keyIdle`/`mouseIdle` for
   the first 5 seconds after wake with no input, and see whether either reads
   below 0.05.
2. **Lock-screen app switches.** `CGWindowListCopyWindowInfo` will most likely
   report the lock UI as the frontmost layer-0 window, so each lock/unlock cycle
   plausibly counts two `AppSwitches` (`engine.go:225-236`). `AppSwitchWork` is
   0.0 so there is no economy effect today, but the Activity modal's number is a
   claim — the same class of lie the `SelfAppID` transparency rule already fixed
   for dexel's own window.

Also worth capturing in the same session: whether `keystrokeCount` advances while
the screen is locked (it should, and that is fine — only counts cross the
boundary).

### F3 — Real-resume timer behaviour, and the probes that could not run here

Expected from the ticker contract: the channel has capacity 1 and the runtime
re-arms `when` forward rather than accumulating, so a stalled ticker delivers
**one** tick on resume, not N catch-up ticks — which is the mechanical reason the
suspend cannot be replayed as 28,800 seconds of stats (C6). **Unverified here.**
§7 has the probes.

### F4 — WS and frontend after a plain suspend

Code read says this self-heals: the connection is loopback so both ends suspend
together; if it does drop, `close` schedules a capped-backoff reconnect and
re-asserts a desired `STORE_OPEN` hold under the new connID
(`ws-client.ts:113-139`); and no client code accumulates elapsed time locally, so
the pill and modal self-correct from the next `state` frame. **Measure:** suspend
with the window open, resume, confirm the pill resumes within ~1s and the
Sessions modal matches the server. Note that after R1's fix the elapsed value
will *legitimately* jump on resume — this test is also how you confirm the
frontend renders that jump without stuttering. R9 is the case where this does
**not** self-heal.

---

## 5. CONFIRMED-FINE (with the reasoning, not the assertion)

- **C1 — Day rollover across a suspend, including across several midnights.**
  `rolloverStatsIfNewDay` reads the **wall** clock (`game.go:826`) and
  `finalizeDay` attributes the ending day to the **stored** `g.statsDate`
  string, never to "now" (`game.go:830`, `history.go:100-110`). A sleep across
  midnight therefore finalizes yesterday under yesterday's date with only the
  counters actually collected, on the first tick after resume. A multi-midnight
  sleep finalizes only the one day that was running; the intervening days stay
  honest gaps (no fabricated zeros) and break the streak, which is the
  documented intent. The rollover also fires while **paused** (`TickPaused`
  calls it, covered by `pause_test.go:262-301`). The only way to corrupt this is
  a *backward* date step — R7, not this.
- **C2 — Sessions crossing suspend + midnight do not corrupt stats.** Session
  numbers are `watermark − baseline` over the same monotonic lifetime counters
  (`session.go:150-190`), and the rollover only touches the *today* bucket,
  which no session reads. The session's wall span is wrong (R1); its counters
  are not.
- **C3 — The frontend never drifts.** No client-side time accumulation exists:
  `elapsedSeconds` is rendered verbatim (`chrome.ts:88-92`,
  `sessions-modal.ts:154`), the wire type says so (`wire.ts:186`), and the only
  `setInterval`s are animation loops (`render/terminal.ts:11`,
  `render/scene.ts:541`). A stale frame cannot persist — the next broadcast
  overwrites it.
- **C4 — Discovery after resume, and pid reuse over multi-day uptimes.** The
  listener and port survive a suspend (nothing rebinds), and staleness is never
  decided by trusting `runtime.json`: `Probe` does an HTTP round-trip to the
  token-gated status endpoint and requires the answering process to report the
  same pid the file claims (`internal/lifecycle/runtimefile.go:245-290`), so a
  recycled pid cannot be mistaken for a live dexel. `Query` (read-only) is used
  while racing a just-spawned child; only later verbs clean
  (`cmd_lifecycle.go:250-273`).
- **C5 — SQLite over long uptimes: no growth, no fragmentation, and the
  power-loss argument does cover a lid-close.** Each autosave is one transaction
  upserting **one** row plus `PRAGMA user_version`
  (`internal/store/db.go:253-275`), under fixed `journal_mode = DELETE` and
  `synchronous = FULL` (`db.go:192-193`). 30s × days ≈ 2,880 commits/day, all
  replacing the same row, so pages are reused: the file does not grow with
  uptime and there is no WAL/`-shm` to accumulate (deliberate, for the
  quarantine story). No `VACUUM` needed. On durability: a suspend does not
  interrupt a write at all — the process freezes mid-syscall and resumes — and
  a *power loss* during suspend is covered by the rollback journal, which
  `synchronous = FULL` fsynced before the commit, so the next open sees either
  the old row or the new one; `quick_check` plus the MAC gate catch anything
  else, with a torn *first* write specifically classified as corrupt rather than
  as tampering (`db.go:340-355`). The one residual is universal: a disk that
  lies about fsync. `config.json` keeps tmp+fsync+rename+dir-fsync
  (`store.go:898-957`), correct for a file with no journal. The save carries
  **no** `savedAt` field (confirmed: the only `time.Now()` in the package is a
  backup-filename stamp, `db.go:148`), so a loaded save cannot tell how long the
  process was down — worth knowing if any fix wants that.
- **C6 — A suspend cannot inflate anything.** All four tickers
  (`main.go:437-443`) are monotonic-armed and stall for the whole sleep; a Go
  ticker channel holds at most one pending tick and re-arms forward, so resume
  yields one tick, not thousands. Since every stats second is a *counted tick*
  (`game.go:747-771`, `pause.go`), the sleep contributes exactly zero to
  `ActiveSeconds`/`IdleSeconds`/`PausedSeconds` — this is the half
  `PLATFORM_NOTES.md` §7 got right, and it is the flip side of R8's undercount.
  Autosave likewise fires once on resume; nothing is lost, because a suspend is
  not a crash. (Read from the contract, not measured — F3.)
- **C7 — Memory over weeks is a non-issue.** `terminalLines` capped at 11 and
  `tickerLines` at 3 (`game.go:902,915`); `history` capped at 30
  (`history.go:104-106`); the finished-session log is unbounded by design but is
  one small struct per *session* (one or two a day), and `sessionsSummary`'s O(n)
  sweep runs once per `State()` — 1 Hz over a few thousand records is nothing.
  `terminalPushes`/`tickerRotation` are `uint64`. The hub's conn map is bounded
  by real connections and is cleaned on both the read-error and write-failure
  paths (`handlers.go:61-69`, `hub.go:144-149`).
- **C8 — The darwin dead-handle question:** no such failure mode exists; see F2.
- **C9 — `uptimeSeconds` is wall-based, and therefore right.** `startedAt` comes
  from `time.Parse(time.RFC3339, runtimeInfo.StartedAt)` (`main.go:398`), which
  carries **no** monotonic reading, so `time.Since(startedAt)`
  (`lifecycle_handlers.go:155`) falls back to the wall clock and correctly counts
  a suspend as uptime; `dexel status` parses the same string
  (`cmd_lifecycle.go:583-587`). A backward clock is already clamped to 0 and
  tested (`app/cli_test.go:384-388`, `TestUptimeSecondsNeverClaimsWhatItCannotKnow`).
  This is the one place where mono-stripping produces the *desired* semantics —
  which is exactly why it needs a comment before someone "cleans it up".

## 6. NITS

- **N1 —** `main.go:398-401`: if parsing `runtimeInfo.StartedAt` fails,
  `lifecycleStartedAt` falls back to `time.Now()`, which **does** carry a
  monotonic reading — so on that path `uptimeSeconds` silently changes meaning
  to awake-time. `time.Now().Round(0)` makes the fallback agree with the normal
  path.
- **N2 —** `hub.send` uses a 5s write timeout (`hub.go:197-201`) and
  `broadcastState` calls it **synchronously from the single-owner loop**
  (`hub.go:139-150`), so one wedged client can stall the game's tick loop up to
  5 seconds per broadcast before being dropped. Loopback makes this unlikely,
  not impossible; a buffered per-conn writer removes the coupling.
- **N3 —** No WS ping/keepalive and no read deadline (`handlers.go:79-108`,
  `ctx` is just `r.Context()`), so a readable-but-silent connection is only
  reaped when a broadcast write finally fails. Harmless on loopback; relevant if
  the server is ever addressed off-box.

---

## 7. The probes that must still be run

Written for this report; none could execute. All are cheap.

1. **Clock semantics (5 min).** Print `time.Now()` (shows the `m=+…` monotonic
   reading), `t.Round(0)`, and a `time.Parse` round-trip; compare
   `time.Since(t)` with `time.Since(t.Round(0))`. Pins §0 fact 1 in this
   toolchain.
2. **How much suspend this box has accrued (2 min).** `clock_gettime` for
   `CLOCK_MONOTONIC` (1) and `CLOCK_BOOTTIME` (7); `BOOTTIME − MONOTONIC` is the
   machine's total suspended time since boot. A nonzero value **is** the
   quantitative proof of §0 fact 2 on the owner's own hardware.
3. **Ticker after a stall (2 min).** 1s ticker, block the receiver 10s, drain
   non-blocking, count queued ticks. Expected: 1. Settles C6/F3.
4. **Session bounds under a fake clock (15 min).** In `internal/game`, a test
   using the existing `fakeClock` (`session_test.go:36-48`; inject by assigning
   the unexported `g.now` — there is no `SetClockForTest` for this, and
   `activity.HonestyGlobal` is the zero value so `SeesGlobalInput()` is already
   true) that starts a session, ticks 300 times with input, then advances the
   clock **not at all** (monotonic model) versus **+8h** (wall model), printing
   whether `TakeEndedSession` produced a record and with what `EndReason`/
   `DurationSeconds`. Repeat past 16h for the cap. This is the executable form of
   R1 and becomes its regression test.
5. **The real binary, frozen (20 min).** Build; run with `-provider fake` under a
   throwaway `DEXEL_HOME`; subscribe to `/ws`; `kill -STOP`, wait 60s,
   `kill -CONT`. Count `state` frames in the first wall second after `CONT`
   (expected: 1) and record what the single post-resume tick does with the fake
   provider's large keystroke delta — expected clamped by `MaxRecentRate` to
   ≤0.12 work units, and the anti-mash ceiling holding there is the thing to
   confirm. Caveat: `SIGSTOP` freezes the process but **not** the monotonic
   clock, so this models a *time jump*, not a suspend; it answers the
   ticker/burst and catch-up-delta questions and cannot answer the
   frozen-monotonic ones.
6. **The two hardware field tests** — F1 (Linux: suspend 5 min with
   `dexel logs -f`) and F2 (macOS: wake and lock with `keyIdle` logged). These
   are the only way to close R4's blast radius and the darwin questions, and F1
   is already on the record as never done
   (`LINUX-VERIFICATION.md:857-859`).
7. **R9 in 3 minutes, no suspend required.** Start the runtime, open the window,
   `kill -9` the runtime, let the supervisor restart it (or start it again by
   hand), and watch the window: it should keep saying `RECONNECTING...` against
   the old port forever while `dexel status` reports a healthy runtime on a new
   one.
