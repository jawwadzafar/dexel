# P2 design — Sessions & the session-complete moment (v1.5)

**Status:** design-only decision document (Opus, 2026-08-22). No code. No commit.
**Realises:** `docs/plan/PRODUCT-EVOLUTION.md` §3 Bet 1 / §5 Phase P2 — *the
keystone of the whole thesis*.
**Companion decision record:** `docs/adr/0017-sessions.md`.
**Extends:** `docs/plan/DB-1-design.md` §2.4 (the hybrid's named second half),
`docs/plan/A3-design.md` (the counters a session is a lens over),
`docs/plan/SEC-1-design.md` / ADR 0014 (the config/state split).

This is the gate. It fixes the session model, the name-storage boundary, the
complete-moment, the reward, the schema and chained-MAC design, the wire and UI
surface, the test matrix, and the fan-out. An implementing agent should not have
to re-derive any of it.

> **The one-sentence product intent, from the thesis:** the user supplies the
> **intention**, Dexel supplies the companionship and the **honest reflection**,
> and together they build a **memory**. A session is the container for the first
> verb. Everything below exists to make that container feel warm without ever
> making it a quota.

---

## 0. Forks that need an OWNER decision (surfaced first)

Five. All have a recommended default; **P2 ships on the defaults without
asking.**

- **Fork P2-A — where does the optional project name live?**
  *(a)* in the MAC'd `sessions` row, documented as ADR 0014's "user-authored
  config category"; *(b)* an integer id in the log, names in `config.json`
  keyed by that id; *(c)* no per-session names in v1.
  **Default taken: (b).** Justified at length in §2.7 and ADR 0017 Decision 2.
  The short form: a *timestamped series of project names* is a work journal —
  materially more sensitive than a pet's name, and the exact artifact ADR 0013
  refused when it dropped hourly buckets. Nothing is priced on a name, so
  signing it buys no anti-cheat (the log is chained either way) while costing
  the user the right to edit or delete their own words that ADR 0014 explicitly
  granted. Choose (a) only if the owner wants project names to be
  tamper-evident and is willing to spend the "the protected save holds no free
  text" sentence to get it; choose (c) only if the owner wants zero free-text
  growth anywhere and will accept an anonymous container.

- **Fork P2-B — which schema number does P2 take, 5→6 or 6→7?**
  `dev_docs/production-runtime/MIGRATION_PLAN.md` sequencing constraint 3: only
  ONE schema-bumping task may be in flight, and PR-5 (pause) also wants 5→6.
  **Default taken: P2 claims 5 → 6.** P2 is the keystone of the
  FULL-EXECUTION MANDATE and is starting now; PR-5 has not started. Whoever runs
  PR-5 later takes **6 → 7** and adds `pausedSeconds` to the session delta set
  (§2.6). This must be written into the migration plan as part of DOC-1 (§8).
  Choose otherwise only if PR-5 is already in flight — in which case P2 takes
  6→7 and nothing else in this document changes.

- **Fork P2-C — what does a completed session reward?**
  **Default taken: nothing economic.** A counter, a "sessions this week" number,
  a longest-session personal best, and the celebration. **No coins, no XP, no
  sprint progress** — ADR 0005's calibration and ADR 0008's single-coin-source
  rule are not reopened, and there is no rebalancing risk to carry into P3/P4.
  Choose otherwise only if the owner explicitly wants session payouts, which
  means re-running ADR 0005's strategy-comparison tests against a second coin
  source.

- **Fork P2-D — when does a session end by itself?**
  **Default taken:** it **survives app restarts** (persisted in the signed
  snapshot); it **auto-ends after 2 h of continuous idle**, with the recorded end
  **backdated to the last observed activity**, and only when the provider can
  actually see global input; and there is a **16 h hard cap**. Full justification
  against ADR 0010 in §2.5. Choose otherwise only to retune the two constants —
  the shape is the honest one.

- **Fork P2-E — are very short sessions recorded?**
  **Default taken: no.** A session stopped under 60 s is **discarded** — not
  logged, not counted, never scolded ("that one was too short to keep"). This is
  what makes the anti-mash guarantee structural rather than rhetorical: mashing
  start/stop produces literally nothing, not even a log row. Choose otherwise
  (log everything) only if the owner prefers a complete record and is content
  that "sessions this week" can be inflated by button-mashing.

**Deliberately NOT forks** (decided here so nobody reopens them): the action
names (§6.2), the delta-of-lifetime accounting model (§2.3), the payload-BLOB
log row and its chained MAC (§5.2/§5.3), the fact that the Sessions modal gates
nothing (§2.4), and the decision that the A3 History modal gains nothing in P2
(§3.4).

---

## 1. What P2 is — and the four things it must not touch

**P2 adds no observation.** Everything a session reports already crosses the
`engine → game` boundary every tick: `KeystrokeDelta`, `MouseActive`,
`FocusSessionsCompleted`, `FocusRunSeconds`, `AppSwitches`, `Mood`. `Game.recordStats`
already sums all of them into `today` and `lifetime`. So P2 is **a lens over
tracking that already happens**, and its privacy evidence is an *absence*:

| Must not change | Proof |
|---|---|
| `activity.Snapshot` | `activity/content_free_test.go` — `NumField() == 5`, byte-identical |
| The anti-mash economy (ADR 0005) | `engine/engine_test.go`'s strategy-comparison + ceiling tests untouched and green |
| The single coin source (ADR 0008) | a completed session leaves `DevCash`/`XP`/`Progress` unchanged (§7) |
| Honest moods (ADR 0010) | `Engine.mood` untouched; sessions never influence `activeState` |

The **only** engine change in all of P2 is one *method* on an existing type —
`TickResult.SeesGlobalInput() bool`, returning `r.Honesty == activity.HonestyGlobal`
— so `TickResult` gains **no field**, no data crosses the boundary that did not
before, and `game` does not need to import `activity` to ask the one honesty
question the auto-end rule depends on (§2.5).

And the **lens rule**, stated once because it is the constraint most likely to be
violated by accident: *no code path in `Engine.Tick` or `Game.Tick` may behave
differently because a session is open.* A user who never opens the Sessions modal
must get a bit-identical game. §7 asserts this with a test that runs the same
tick sequence with and without a session and compares the entire economy.

---

## 2. Session mechanics

### 2.1 The model in one paragraph

A session is a **user-declared interval** with an optional name. Starting it
records a *baseline* copy of the lifetime counters; while it runs, its numbers
are always `watermark − baseline`; stopping it turns that delta into one
immutable record appended to a MAC-chained log, and shows the user a summary
card. It grants nothing economic, it constrains nothing, and abandoning it costs
nothing.

### 2.2 Lifecycle — start and stop

**Start** (`SESSION_START`, optional `name`):
1. Reject if a session is already active → `flash{kind:"error"}`, no state
   change. Exactly one session at a time; the single-owner action loop in
   `main.go` serializes concurrent clients for free.
2. `id = len(sessionLog) + 1` — the ordinal the record *will* have. No separate
   monotonic counter is needed, and a discarded short session simply leaves its
   id to be reused.
3. `name` is normalized server-side (`game.NormalizeSessionName`, §2.7). An empty
   result is **legal** — unnamed is a first-class state, unlike a Dexel's name.
4. `baseline = lifetime StatCounters` (a struct copy — see §2.3), `startedAt =
   now`, `lastActivityAt = now`, `watermark = baseline`, `coinsEarned = 0`,
   `focusBlockMax = 0`.
5. `main.go` writes `config.json` **immediately** (not on the 30 s autosave) — the
   same reasoning P1's `persistConfig` records for `SET_NAME`: a declared
   intention that silently fails to survive a crash is worse than no name at all.
   A write failure is surfaced as an error flash; the session still starts.
6. Broadcast `state` + `flash{kind:"session", text:"Session started."}` (text
   composed **server-side**, per ui-spec §6.1 — the frontend never assembles a
   sentence).

**Stop** (`SESSION_STOP`):
1. Reject if no session is active → error flash, no state change.
2. Advance the watermark to the current lifetime counters (so a stop mid-keystroke
   captures everything), set `endedAt = now`, `endReason = "user"`.
3. Compute the record (§2.3). If `durationSeconds < SessionMinDurationSeconds`
   → **discard**: clear the active session, no log row, no counter, and a warm
   flash (`"Session ended — too short to keep."`). Never a scold.
4. Otherwise queue the record as *pending*, clear the active session.

**The pending-record seam.** Both a user stop and an automatic end produce a
record, and both must be persisted and celebrated identically, so they share one
path: `Game` holds at most one pending record, and `main.go` pops it with
`g.TakeEndedSession() (SessionRecord, bool)` immediately after `g.Tick(...)` and
immediately after `applyAction(...)`. On a pop, `main.go` calls
`store.AppendSession` **at once** (a one-shot moment, same rule as the config
write-through), then broadcasts `state`, then the `sessionComplete` message
(§3.1). `Game.Tick`'s existing `(completed bool)` signature is unchanged.

### 2.3 What a session captures — delta of lifetime, and nothing new

The seven counters come from a **struct-wide subtraction**, not a
re-implementation of `recordStats`:

```
session.<counter> = watermark.<counter> − baseline.<counter>
```

over `game.StatCounters` — `Keystrokes`, `MouseActiveSeconds`, `ActiveSeconds`,
`IdleSeconds`, `SprintsCompleted`, `FocusSessions`, `AppSwitches` (+
`PausedSeconds` once PR-5 lands). Consequences, all of them the point:

- **`session ⊆ global` and no double-counting are true by construction**, not by
  a runtime clamp, because every session number is literally a difference of two
  reads of the same monotonic counter.
- **Restart survival is free.** `lifetime` is persisted; the baseline is
  persisted in the snapshot; the subtraction is stable across any number of
  reboots.
- **Midnight is a non-event.** `today` resets at `rolloverStatsIfNewDay`;
  `lifetime` never does. A session spanning midnight needs no special case — and
  a day rollover explicitly does **not** end a session (§2.4).
- **A new counter added to `StatCounters` in a future phase joins the session
  automatically**, with no per-field maintenance — the same property ADR 0014
  prizes in its whole-struct MAC preimage.

Two numbers have **no** monotonic lifetime counter to subtract from, so they are
per-session accumulators held on the active-session block:

| Number | Why an accumulator | Update site |
|---|---|---|
| `coinsEarned` | `DevCash` is *spendable* (it decreases on a purchase) and `coinsToday` resets daily, so neither is a monotonic "earned" counter | `Game.awardCoins` — the single sprint payout, already the only coin source |
| `longestFocusBlockSeconds` | a **max**, not a sum; `statsFocusBlockMax` is a per-*day* max that resets at midnight | `Game.recordStats`, from the `FocusRunSeconds` the engine already emits |

**The rule, stated once so it cannot drift:** *delta where a lifetime counter
exists; an accumulator only where one does not; never both for the same number.*

**Duration.** `durationSeconds = endedAt − startedAt`, in whole seconds. Because
ticks only happen while the process is up, `activeSeconds + idleSeconds ≤
durationSeconds` always holds; a session with a long app-closed gap shows a real
span with honestly small counts, and the auto-end rules (§2.5) bound how weird
that can get. `observedSeconds` is deliberately **not** a wire field: it is a
pure reduction over two fields already sent, i.e. the client-derivable class
A3-design.md §5 already permits (`the client asserts no state the server did not
send`).

**No new observation, restated as a checklist for the reviewer:** `Snapshot`
unchanged; `TickResult` gains no field; no provider file is touched; every
session number is a difference or a max of numbers already flowing through
`recordStats`.

### 2.4 Interaction with the existing gates

| Gate / event | Behaviour | Why |
|---|---|---|
| **`STORE_OPEN`** | Session counters keep advancing; session coins provably do not | Sessions follow the **analytics** rule, not the economy rule. `recordStats` already runs unconditionally while shopping — `game.go`'s own comment: shopping is "a few seconds *inside* a tracked session", so counting the seconds is honest, while `Progress`/`DevCash` stay frozen per ui-spec §5.3 |
| **The Sessions modal itself** | Gates **nothing** — no `SESSION_MODAL_OPEN`, no `ws-client` hold | It is not shopping. Same as the Activity/History/onboarding modals, which ui-spec §7 records as gating nothing. The residual crumb of work from typing a name is quantified in §9 |
| **Pause (PR-5)** | The session **survives**; counters freeze because the provider is stopped and `eng.Tick()` is not called; `pausedSeconds` joins the delta set | Pause is "stop watching me", not "abandon my intention". Auto-ending on pause would make a user's declared container a casualty of a privacy action. The idle auto-end cannot fire while paused (no ticks) — it fires on the **first tick after resume**, backdated, which is the self-healing behaviour we want |
| **Resume (PR-5's `Engine.Reset()`)** | No effect on the session | Reset clears engine-local recency state; lifetime counters (the session's substrate) are untouched |
| **Day rollover** | Never ends a session | Sessions are not days |
| **Process exit / restart** | Never ends a session | §2.5 |
| **Client disconnect** | Never ends a session | Unlike `STORE_OPEN` (which *must* be released on disconnect or progression wedges), a session is a persistent user intention, not a per-connection hold. It is deliberately **not** keyed by `connID` |
| **Tamper reset** | The session and the whole log are lost with the economy; `config.json`'s project names survive | ADR 0014/0016's policy, unchanged (§5.5) |

### 2.5 Auto-end rules, justified against ADR 0010

The governing sentence is ADR 0010's: *the game cannot know, so it must not
claim.* Applied to a container rather than a mood:

**(1) Closing the app does NOT end the session.** The in-progress session lives
in the signed snapshot and resumes on the next boot. The runtime already tracks
independently of any window (`ORCHESTRATION-LOG`: "closing the window never
stopped tracking"), and PRODUCT-EVOLUTION requires that "abandoning a session
loses nothing and is never scolded". Ending on exit would either fabricate an end
time or throw the container away.

**(2) Idle auto-end after `SessionIdleTimeoutSeconds` (2 h), with the end
backdated to `lastActivityAt`.** Evaluated at the top of `Game.Tick`, before
anything else, so it fires on the very first tick after a load. `lastActivityAt`
advances on any tick with `KeystrokeDelta > 0 || MouseActive` — *real observed
input*, not the keystroke-recency-derived mood — and the **watermark advances at
the same moment**, so the record's counters are taken as of the last time Dexel
actually saw the user. This is what keeps every number on the card mutually
consistent: without the watermark, a backdated end would report two hours of
`idleSeconds` inside a fifteen-minute duration.

Backdating *is* the honest choice: Dexel knows when it last saw input; it does
**not** know the user was still "in session" through the silence, so it declines
to claim it. It also makes the reopen-after-a-long-close case self-heal — reopen
the app after a ten-hour night with a session open, and the first tick ends it at
last night's last keystroke instead of inventing a ten-hour session.

**(3) The idle rule fires only when the provider sees global input.** Guarded by
`r.SeesGlobalInput()`. With a blind provider (Windows today, per
`PLATFORM_NOTES`) "idle" is unknowable, and ending a session because we cannot
see would be precisely the ADR 0010 lie the mood rules already forbid ("on break
because you minimized me"). Blind providers rely on rule (4) instead.

**(4) A hard cap at `SessionMaxDurationSeconds` (16 h).** `endReason =
"maxDuration"`; the end is backdated to `lastActivityAt` when that is known,
otherwise `startedAt + cap`. This is the only bound a blind provider has, and it
guarantees no session can ever claim a multi-day container.

**(5) Every automatic end is labelled.** `endReason ∈ {user, idle, maxDuration}`
— a closed three-value set, the same shape as `activeState`'s three wire strings.
The card and the future scrapbook say "Dexel closed this one for you" rather than
pretending the user did.

### 2.6 Pinned constants (`app/internal/game/session.go`)

| Name | Value | Rationale |
|---|---|---|
| `SessionMinDurationSeconds` | `60` | Below this a session is discarded (Fork P2-E). Makes the anti-mash guarantee structural |
| `SessionIdleTimeoutSeconds` | `7200` (2 h) | Survives a lunch, a long meeting, a commute; short enough that a forgotten session cannot absorb an evening. A one-line retune |
| `SessionMaxDurationSeconds` | `57600` (16 h) | A ceiling no honest workday reaches; the only bound a blind provider has |
| `MaxSessionNameLen` | `32` (runes) | Wider than `MaxNameLen 24` because a project name is not a pet name; still fits the modal's input and the titlebar pill's 16-char truncation |
| `SessionsWireWindow` | `10` | How many finished sessions the wire carries (§6.1). Storage is unbounded (§5.6) |
| `SessionsWeekDays` | `7` | The "sessions this week" window, in local dates including today |

---

## 2.7 The name-storage decision (Fork P2-A), in full

**Decision: (b) — an integer id in the log; the name in `config.json` keyed by
that id.**

```
state.db  sessions row  →  payload.id = 42        (an integer; no text)
config.json               →  "sessionNames": { "42": "auth refactor" }
```

`ConfigData` gains `SessionNames map[string]string json:"sessionNames"` (JSON
object keys must be strings; the id is decimal). `game` holds
`sessionNames map[int]string`, seeded at boot by `main.go` from
`store.LoadConfig` and written back through `store.SaveConfig` after
`SESSION_START` — **exactly** the P1 pattern for the Dexel's name, including the
"game stays pure, the server owns the I/O" split.

Why not **(a) the name in the MAC'd log row**, even though ADR 0014 already
established a "user-authored config category" that P1 used to allow-list a string
onto the wire:

1. **A project name is closer to work content than a pet name is.** ADR 0014's
   category argument is that the Dexel's name is "data the user deliberately
   writes about their own pet, not surveillance of their work". A *timestamped
   series* of project names is data about the work: it answers *what you were
   doing, and when*. That is the same artifact ADR 0013 refused when it dropped
   hourly buckets — "a daily count says you worked 4 hours; an hourly profile
   says *when*". Here: a session says you worked 90 minutes; a named,
   timestamped session says **what on**. The one file whose structural test
   exists to prove it holds no free text is the wrong home for it.
2. **Nothing is priced on a name, so nothing needs it protected.** MAC coverage
   buys anti-cheat, and a name has no economic value. Signing it would instead
   cost the user the right ADR 0014 deliberately granted them: to edit or delete
   their own words with a text editor. Under (b) a user purges what they were
   working on by editing one plain JSON file and the honest counts survive;
   under (a) they would have to tamper a MAC'd row and take an economy reset
   with it.
3. **The cheating argument is a wash.** The log is MAC-chained either way, so an
   edit is detected either way. There is no integrity gain on the table to trade
   privacy and editability for.
4. **DB-1 already wrote the boundary down on purpose.** DB-1-design §2.4: *"P2's
   optional project name is CONFIG, not a log column… This is the boundary most
   at risk of being crossed by accident; it is written down here on purpose."*
   Crossing it in the very phase it was written for would be the accident.

Why not **(c) no names in v1**: the name *is* the Intention the thesis asks the
user to supply, and PRODUCT-EVOLUTION names it twice inside P2's scope
("an optional project name", "the optional project name is user-typed CONFIG").
An anonymous container is a timer.

**Validation and normalization.** `game.NormalizeSessionName(raw) string` drops
every control character, trims, and truncates to `MaxSessionNameLen` runes —
reusing the exact core `NormalizeName` already implements (extract the shared
body as `normalizeUserText(raw string, maxRunes int) string`; `NormalizeName`
keeps its `ErrEmptyName` behaviour, `NormalizeSessionName` returns `""` for
empty, which is legal). `RestoreSessionNames` runs every value from a
hand-edited `config.json` through the same function, matching
`RestoreConfigName`'s contract that a malformed config degrades rather than
blocks.

**Accepted desync, stated honestly.** `config.json` can lose or contradict the
log — a deleted file, a hand edit, or an id reused after a discarded short
session. Consequences: a logged session renders **unnamed**, and the counts are
never affected. `SESSION_START` overwrites its id's entry (or deletes the key
when the new session is unnamed), which doubles as the user's natural "clear that
name" path. This is the *right* failure mode: the counts are the honest record,
the name is the user's annotation of it.

**The proof is a test, not a promise.** `TestSessionNameNeverReachesTheProtectedSave`
marshals both `store.Snapshot(g)` and the appended row's payload and asserts the
literal name string appears in neither, plus that no key in either shape contains
`"name"` — the direct analogue of P1's `TestSetNameNeverReachesTheProtectedSave`.

---

## 3. The session-complete moment

This is why P2 is the keystone: it is the first time Dexel says *"here's what we
did together."* PRODUCT-EVOLUTION's guard is explicit — "a cozy card…, not a
stats readout" — and ui-spec §0's is too: **no animation longer than 400 ms, no
easing, no colour transitions. Retro UI snaps.** So the beat is short, composed
server-side, and made of parts that already exist.

### 3.1 The event

A new WS message type, sent to every connection immediately after the `state`
broadcast that cleared the session:

```json
{ "type": "sessionComplete", "v": 1, "session": { …one SessionView… } }
```

**Why a dedicated message rather than reusing `flash`.** ui-spec §6.2's rule is
"no dedicated ack — every successful action is answered by a state broadcast plus
a flash". A flash alone would force the client to *infer* which entry of
`sessions.recent` just ended, and inference is exactly what the contract forbids
("the client never asserts state the server didn't send"). One explicit message
carrying the exact record keeps the server the sole source of truth, and gives
P3/P4 a single clean event to hook. `handleWS`/`hub.go` gain one broadcast method
mirroring `broadcastFlash`.

Alongside it, the ordinary gold flash: `flash{kind:"session", text:"…"}`, text
composed server-side (e.g. `"auth refactor — 1h 24m together."`, or
`"Session complete — 1h 24m together."` when unnamed). `kind-session` joins
`kind-purchase`/`kind-sprint`/`kind-welcome` in the gold CSS group.

### 3.2 The card

Rendered as a **panel inside the Sessions modal, which auto-opens on
`sessionComplete`** — not a fifth `<dialog>`. It reuses the modal we already need,
and a modal is the expected response to a button the user just pressed. Content,
in this order (largest first, cozy phrasing, no percentages, no scores, no
"efficiency"):

```
        SESSION COMPLETE
        auth refactor                      ← omitted entirely when unnamed
        1h 24m                             ← duration, large
        ------------------------------------
        TYPED        4,182 keys
        FOCUS        3 blocks   BEST 14m
        SPRINTS      2 finished
        COINS        +18 earned during it
        ------------------------------------
        SESSION 27  ·  4 THIS WEEK
                              [ NICE ]
```

- Every number comes from the message's `session` object verbatim; the client
  only formats (`fmtDuration`, `fmtInt` — the existing utils).
- `COINS` is honest about its verb: coins were **earned during** the session by
  the ordinary sprint payout. The card must not imply the session paid them.
- No target, no comparison, no "you were X% focused", no red anything. A zero
  row renders as a zero, not as a failure.
- `[ NICE ]` closes the modal back to the live view. `Esc` does the same (native
  `<dialog>`, never intercepted).
- A discarded short session (§2.2) produces **no card** — only the warm flash.

### 3.3 The celebration beat, and the P3 hook

Two steps, both inside the 400 ms budget: the gold `#flash` toast, and the card
appearing. That is deliberately all P2 ships — the repo has **no** celebration
primitive today (`render/overlays.ts` is two overlays; the "sprint complete"
celebration is a single gold toast and zero client-side code).

The hook for P3 (Character life) is a single client-side seam: `main.ts`'s
`onSessionComplete(msg)` handler calls, in order, `showFlash(...)`,
`sessionsModal.showSummary(msg.session)`, and a **no-op-in-P2**
`scene.onCelebrate('session')`. P3 fills in that third call with the celebrate
animation frames and needs **no** backend, wire or privacy change — exactly what
PRODUCT-EVOLUTION promises for P3 ("no backend, no wire, no privacy change").
P4 hooks the same server-side seam: moment evaluation goes where `main.go` pops
the pending record.

### 3.4 Where completed sessions go (the minimal v1 surface)

- **The append log** → the Sessions modal's *recent* list: the last
  `SessionsWireWindow` (10) records, newest first, one line each (date · name ·
  duration · keys · focus blocks). Clicking one re-shows its card, which is the
  cheapest possible seed for P6.
- **The A3 History `[H]` modal gains nothing.** Decided, not deferred by
  accident: `[H]` is per-**day** analytics, a sessions row there would duplicate
  the Sessions modal for no gain, and **P6 is explicitly the phase where sessions,
  moments and days merge into a scrapbook**. This saves TS-1 real work and keeps
  the two modals single-purpose.
- **P6 (scrapbook)** reads over the same log with no new table and no new
  observation, exactly as DB-1-design §2.4 anticipated.

---

## 4. Rewards (Fork P2-C)

**v1 grants nothing economic.** No coins, no XP, no sprint progress, ever, from
starting or ending a session. ADR 0005's calibration and ADR 0008's
"sprint payout is the only coin source" are not reopened, which is precisely why
P2 is safe to land ahead of P3/P4 — there is no rebalance to carry forward.

What the user gets:

| Reward | Source | Notes |
|---|---|---|
| The moment | §3 | The actual payoff |
| `completed` — lifetime sessions | `len(sessionLog)` | Derived from the **verified** log, so it needs no second protected counter: the chain head in the signed snapshot already protects everything derived from it |
| `thisWeek` | log filtered to the last `SessionsWeekDays` local dates | Server-computed; the client renders it verbatim (the A3 streak precedent) |
| `longestSessionSeconds` | `max(durationSeconds)` over the log | A cozy personal best, never a target |

**Anti-mash, structurally.** Start/stop grants **nothing at all**, and a session
under 60 s is discarded (Fork P2-E) — so mashing the buttons cannot even inflate
a count, let alone a balance. §7 asserts this with a thousand start/stop pairs.

**Pressure guards (PRODUCT-EVOLUTION's rejections, applied).** "Sessions this
week" is rendered as a warm fact: **no target, no goal, no 0-of-N, no
comparison, no colour change when it is low, and never a decrease framed as a
loss.** There are no session quotas, no streak of sessions, and no notification
of any kind. If a future phase wants to celebrate consistency, that is P4's
Moments layer on "firsts, journeys and consistency" — not a number on this card
that starts feeling like a debt.

**The P4 hook, named not built.** Moments will hang "first session", "first named
session", "ten sessions" off exactly this data and grant **earn-only cosmetics**
— which is how a session's reward becomes visible in the scene (ADR 0008) without
ever entering the priced economy. The earned-collectible set goes in the snapshot
payload (MAC-protected for free); only the *log* of when a moment fired needs a
table, and that table copies §5.3's chain verbatim.

---

## 5. Persistence

DB-1 built half of ADR 0016's hybrid and **named** the other half for exactly this
phase. P2 builds it, unchanged in shape from what DB-1-design §2.4 specified.

### 5.1 The split: snapshot vs log

| Data | Home | Why |
|---|---|---|
| The **in-progress** session | two additive fields on `SaveData` (the signed snapshot row) | Single, mutable, rewritten on every 30 s autosave — the snapshot row's exact shape. MAC-protected for free by ADR 0014's whole-struct-minus-the-tag preimage |
| **Finished** sessions | a new `sessions` append table | Append-mostly, unbounded, read as ranges. Cramming them into the snapshot would mean re-serializing and re-MACing the whole history on every session end — "the thing that actually gets slow" (DB-1 §2.2) |
| Project **names** | `config.json` | §2.7 |

`SaveData` additions (both `omitzero`/`omitempty`, both additive):

```go
Session        *ActiveSessionSave `json:"session,omitzero"`
SessionLogHead string             `json:"sessionLogHead,omitempty"`

type ActiveSessionSave struct {
    ID                       int              `json:"id"`
    StartedAt                string           `json:"startedAt"`      // RFC3339
    LastActivityAt           string           `json:"lastActivityAt"` // RFC3339
    Baseline                 StatCountersSave `json:"baseline"`
    Watermark                StatCountersSave `json:"watermark"`
    CoinsEarned              uint64           `json:"coinsEarned"`
    LongestFocusBlockSeconds uint64           `json:"longestFocusBlockSeconds"`
}
```

Reusing `StatCountersSave` for both baseline and watermark is deliberate: the
existing structural content-free coverage on that type applies for free (the same
trick `DayBucketSave` used), and a counter added to it in a future phase joins
the session with no edit here.

### 5.2 The log table

```sql
PRAGMA user_version = 6;

CREATE TABLE IF NOT EXISTS sessions (
  id       INTEGER PRIMARY KEY,   -- 1-based ordinal; ALSO inside the signed payload
  ended_at TEXT    NOT NULL,      -- denormalized mirror, for range reads
  payload  BLOB    NOT NULL,      -- canonical compact JSON of SessionSave
  mac      TEXT    NOT NULL       -- hex chained HMAC-SHA256 (§5.3)
) STRICT;

CREATE INDEX IF NOT EXISTS sessions_ended_at ON sessions(ended_at);
```

Four deliberate choices, each an application of a reason ADR 0016 already
recorded:

- **`payload` is a BLOB of the whole record, not a column per counter.** This is
  the same argument that rejected normalizing the economy snapshot: the MAC
  covers *the whole struct minus the tag*, so **every future session field is
  protected automatically**, instead of needing a line in a hand-built row
  serializer whose omission would be a **silent anti-cheat hole rather than a
  test failure**. `BLOB` (not `TEXT`) so the bytes round-trip with zero encoding
  interpretation — the MAC is verified against the bytes as stored.
- **`ended_at` is a mirror, not an authority** — the identical role
  `state.schema` plays. It exists so "sessions this week" and P6's range reads
  do not have to parse every payload. `payload.endedAt` is the signed truth; a
  disagreement is a tamper signal.
- **`STRICT`**, for the same reason the `state` table is strict: the engine
  rejects `UPDATE sessions SET id='lots'` instead of leaving the loader to
  wonder.
- **`id INTEGER PRIMARY KEY`** is the ordinal, and it is *inside* the payload
  too, so renumbering is detected.

```go
type SessionSave struct {
    ID                       int              `json:"id"`
    StartedAt                string           `json:"startedAt"`
    EndedAt                  string           `json:"endedAt"`
    DurationSeconds          uint64           `json:"durationSeconds"`
    Counters                 StatCountersSave `json:"counters"`
    CoinsEarned              uint64           `json:"coinsEarned"`
    LongestFocusBlockSeconds uint64           `json:"longestFocusBlockSeconds"`
    EndReason                string           `json:"endReason"` // user|idle|maxDuration
}
```

### 5.3 The chain

```
logDomain = "dexel-session-log-v1"          // distinct from macDomain
row_mac_0     = ""                          // genesis
row_mac_i     = hex(HMAC-SHA256(key, logDomain ‖ 0x00 ‖ row_mac_{i-1} ‖ 0x00 ‖ payload_i))
SessionLogHead = row_mac_n                  // inside the SIGNED snapshot payload
```

Same key, same `integrity.go` primitives (`computeMACBytes` generalized to take a
domain, or a sibling `computeLogMACBytes`), same `hmac.Equal` constant-time
compare. A **distinct domain tag** is not decoration: it is what stops a state
payload from ever being replayed as a log row, or vice versa — the stated purpose
of domain separation in SEC-1 §2.2.

Why a chain and not independent per-row MACs, restated from DB-1 §2.4: appending
costs **one** HMAC rather than re-signing the whole history, and because the head
lives inside the signed payload, **deleting, truncating, reordering or renumbering
rows is detectable** — which independent per-row tags would not be.

No separate row-count field is needed: truncating the log changes the replayed
head, which no longer matches the signed one. The lifetime session count is
therefore *derived* from the verified log (§4) rather than duplicated as a second
protected counter that could drift.

### 5.4 Verification — appended to DB-1's exact gate order

DB-1 §3.2's order is unchanged: corrupt → future → row-count → snapshot MAC →
unmarshal → schema cross-check. Chain verification is **step 7**, strictly after
the snapshot MAC verifies, because the head must be trusted before it can be
used as an anchor:

7. Read `SELECT id, ended_at, payload, mac FROM sessions ORDER BY id ASC`.
   - table missing **and** `SessionLogHead == ""` → **the honest empty log, OK**.
   - table missing **and** head `!= ""` → `ErrTampered`.
   - zero rows and head `!= ""`, or rows present and head `== ""` → `ErrTampered`.
8. Replay: `prev := ""`; for each row *i* (1-based):
   `row.id == i`; `computeLogMAC(prev, row.payload) == row.mac`;
   `payload.id == row.id`; `payload.endedAt == row.ended_at`. Any mismatch →
   `ErrTampered`. Then `prev = row.mac`.
9. `prev == SessionLogHead` → else `ErrTampered`.

Every failure goes through the existing `failClosed` path: quarantine to
`.invalid` (renamed, never deleted, journal sibling moved with it), `ErrTampered`
returned, economy resets from `game.New()`, the legacy Rust import stays
unreachable, `config.json` untouched.

**Verification cannot be skipped by construction.** The full loader becomes
`LoadAll(path) (SaveData, []SessionSave, bool, error)`; today's
`Load(path) (SaveData, bool, error)` becomes a thin wrapper that calls `LoadAll`
and **discards** the log — so every existing call site and test keeps working
*and* still exercises the chain check. A two-function API where the convenient
one skips integrity is exactly the silent hole ADR 0016 warns about.

Cost: O(n) HMACs at boot. At ~150 bytes and one HMAC per row, a decade of ten
sessions a day is a few tens of milliseconds, once. Measured, not assumed, in
the handoff.

### 5.5 The write path — ONE transaction

`store.AppendSession(path string, d SaveData, s SessionSave) (newHead string, err error)`:

```sql
BEGIN IMMEDIATE;
  INSERT INTO sessions (id, ended_at, payload, mac) VALUES (?, ?, ?, ?);
  INSERT INTO state (id, schema, payload, mac) VALUES (1, ?, ?, ?)
    ON CONFLICT(id) DO UPDATE SET schema=excluded.schema,
                                  payload=excluded.payload,
                                  mac=excluded.mac;
  PRAGMA user_version = 6;
COMMIT;
```

Order inside the call: compute `payload_s`; `rowMac = chain(d.SessionLogHead,
payload_s)`; set `d.SessionLogHead = rowMac` **and** `d.Session = nil`; sign
`d`; write both rows in the one transaction; return `rowMac`.

**This must be one transaction or the design is broken.** A crash between the two
writes would leave a log row past the signed head — i.e. a *false* tamper report
that resets an innocent user's economy on the next boot. `writeStateRow` is
refactored to a `writeStateRowTx(tx, …)` so both writers share it.

`main.go` then hands the new head back with `g.SetSessionLogHead(newHead)`, and
`game` treats it as **an opaque integrity token it never interprets** — the same
relationship `ImportedFromRust`/`ImportedAt` already have (set only by the store,
carried forward by every subsequent `Snapshot`). `game` never computes a MAC and
never sees the key. Ordinary `Save` is unchanged: it rewrites the snapshot row
carrying whatever head `SaveData` holds, so a cheater who deletes the last log
row while the app runs is caught on the next boot.

ADR 0016's deferred **long-lived `*store.DB` handle stays deferred**: session
writes are a handful a day, and open-write-close with `synchronous=FULL` is
already the shipped pattern. And a hard rule for the implementer: **the 1 s state
broadcast never touches SQLite.** The verified log is held in memory (`game`,
seeded at boot by `Apply`, appended on each end) and `State()` reads only that.

### 5.6 Schema bump, migration, retention

- **`CurrentSchema` 5 → 6** (Fork P2-B), additive in exactly the way 1→2, 2→3,
  3→4 were: a schema-5 payload has neither new key, `json.Unmarshal` leaves
  `Session == nil` and `SessionLogHead == ""`, and no `sessions` table exists —
  which §5.4's rule accepts as the honest empty log. **No dedicated migration
  code beyond the bump.** Nothing is backfilled: we never had sessions before, so
  inventing past ones would be fabrication, not migration.
- **`ErrFutureSchema` preserved unchanged** — a schema-7 save is still renamed
  `.future` and refused. A test named after the bump (mirroring
  `TestFutureSchema6RefusalStillFiresAfterTheSchema5Bump`) proves it.
- **PR-5 must take 6 → 7** and add `pausedSeconds` to §2.3's delta set. Written
  into `dev_docs/production-runtime/MIGRATION_PLAN.md` by DOC-1.
- **Retention: unbounded rows, a windowed UI.** A row is ~150 bytes; ten a day
  for a decade is a few megabytes, and the rows *are* the memory P6 is built on,
  so pruning them would delete the product feature. The wire carries the last
  `SessionsWireWindow` (10); "this week" and the personal best are computed
  server-side over the whole verified log. If a cap is ever wanted, it belongs in
  P6 with a user-visible "forget sessions older than…", never as a silent prune.

---

## 6. Wire + UI

### 6.1 `StateMessage` additions (camelCase, additive, optional client-side)

One nested block, always sent (the P1 `config` precedent: the server always sends
the block, it may be empty), typed `sessions?` in `wire.ts` so a stale frontend
degrades to "no sessions" rather than breaking:

```go
type SessionsView struct {
    Active  *ActiveSessionView `json:"active"`            // null when none
    Summary SessionsSummary    `json:"summary"`
    Recent  []SessionView      `json:"recent"`            // newest first, ≤ 10
}

type ActiveSessionView struct {
    ID                       int    `json:"id"`
    Name                     string `json:"name"`            // "" when unnamed
    StartedAt                string `json:"startedAt"`       // RFC3339
    ElapsedSeconds           uint64 `json:"elapsedSeconds"`  // SERVER-computed
    Keystrokes               uint64 `json:"keystrokes"`
    MouseActiveSeconds       uint64 `json:"mouseActiveSeconds"`
    ActiveSeconds            uint64 `json:"activeSeconds"`
    IdleSeconds              uint64 `json:"idleSeconds"`
    SprintsCompleted         uint64 `json:"sprintsCompleted"`
    FocusSessions            uint64 `json:"focusSessions"`
    AppSwitches              uint64 `json:"appSwitches"`
    CoinsEarned              uint64 `json:"coinsEarned"`
    LongestFocusBlockSeconds uint64 `json:"longestFocusBlockSeconds"`
}

type SessionView struct {          // one finished session
    ID                       int    `json:"id"`
    Name                     string `json:"name"`
    StartedAt                string `json:"startedAt"`
    EndedAt                  string `json:"endedAt"`
    DurationSeconds          uint64 `json:"durationSeconds"`
    Keystrokes               uint64 `json:"keystrokes"`
    MouseActiveSeconds       uint64 `json:"mouseActiveSeconds"`
    ActiveSeconds            uint64 `json:"activeSeconds"`
    IdleSeconds              uint64 `json:"idleSeconds"`
    SprintsCompleted         uint64 `json:"sprintsCompleted"`
    FocusSessions            uint64 `json:"focusSessions"`
    AppSwitches              uint64 `json:"appSwitches"`
    CoinsEarned              uint64 `json:"coinsEarned"`
    LongestFocusBlockSeconds uint64 `json:"longestFocusBlockSeconds"`
    EndReason                string `json:"endReason"`
}

type SessionsSummary struct {
    Completed             uint64 `json:"completed"`
    ThisWeek              int    `json:"thisWeek"`
    LongestSessionSeconds uint64 `json:"longestSessionSeconds"`
}
```

The counters are **flattened**, deliberately mirroring `DayStat`'s shape so one
rule covers both: *a session view carries the same seven counters a day does.*
`ElapsedSeconds` is server-computed (the client never derives live time). Every
field is a count, a duration, an ISO timestamp, an integer id, or a closed-set
enum — **except `Name`**, the one user-authored string, allow-listed on ADR
0014's category citation exactly as P1's `Config.Name` was.

New message type, sent right after the clearing `state`:

```json
{ "type": "sessionComplete", "v": 1, "session": { …SessionView… } }
```

### 6.2 Actions (`docs/ui-spec.md` §6.2 gains two rows)

```json
{"action": "SESSION_START", "name": "auth refactor"}   // name optional
{"action": "SESSION_STOP"}
```

Names **pinned**: `SESSION_START` / `SESSION_STOP`. PRODUCT-EVOLUTION §2.1 wrote
`SESSION_START`/`SESSION_END`; the imperative pair matches the UI's Start/Stop
buttons and the `STORE_OPEN`/`STORE_CLOSE` verb-pair precedent, so **STOP wins
for the action** while the *data* keeps `endedAt`/`endReason` (a session has an
end; the user performs a stop). Recorded here so nobody re-derives it.

Both obey §6.2's contract: no dedicated ack, success answered by a `state`
broadcast + a `flash`, failure by `flash{kind:"error"}` and **no state change**.
Server-side validation is the only validation (`NormalizeSessionName`; the
input's `maxlength` is a courtesy). `SESSION_RENAME` is deferred, named.

`ClientAction` in `wire.ts` is a **closed discriminated union** — both actions
must be added there or `tsc --noEmit` fails.

### 6.3 The Sessions modal

- **Menu entry:** `<button id="sessions-open" class="nes-btn menu-item">[W] SESSIONS</button>`
  after `#history-open` in `#menu-panel`. `features/menu.ts` needs **zero
  changes** — its own comment already says *"a future entry (Sessions, Goals,
  …) is just one more `.menu-item` button in index.html"* — and `game.css`'s
  panel rule targets the class, not an id list, so the panel auto-grows.
- **Keybinding `[W]`** ("work session"). `S`/`Tab`/`A`/`H`/`M` are taken; `W` is
  free. One new tier in `features/keybindings.ts`, placed with the other modal
  tiers. The bare-letter hazard is **already solved**: `isTextEntryTarget`
  returns early for any keydown aimed at an `input`, so typing a project name
  containing `w`, `s`, `a`, `h` or `m` cannot open a modal — and the Sessions
  modal, owning an input, additionally claims the keyboard-ownership tier
  (export `handleKeydown` even if it only handles `W`, the onboarding precedent:
  *"presence is the point"*).
- **`<dialog id="sessions" class="nes-dialog is-dark">`** after `#history` in
  `index.html`. Geometry: **480 × 300 at left 80, top 50** — under BUG-8's ~346px
  UA `max-height` cap, centred by the recorded `(400 − h) / 2` convention. Ids
  `#sessions-title` / `#sessions-close` / `#sessions-help` (`W / ESC CLOSE`);
  repeated content uses `.sessions-*` classes and the shared `.label`/`.value`
  (+`.value.gold`); a `game.css` section banner after the history/onboarding
  blocks, with the line-by-line **internal layout budget** comment the
  `#history` block models. ASCII only (`->`, not `→` — the A2 lesson), and
  `parseLocalDate`-style parsing for any date string.
- **Three views in one dialog**, switched by which one the server's state makes
  true — never by client-held mode:
  1. **Idle** (`sessions.active == null`): the name input + `START SESSION`, then
     the recent list, then the summary line.
  2. **Live** (`sessions.active != null`): the name (or "unnamed session"),
     elapsed, and the effort-so-far counters, updating on each 1 s `state`; the
     one action button reads `END SESSION`, or `CANCEL SESSION` while
     `elapsedSeconds < 60` (the label flips on a **server-sent** number — the
     store modal's one-action-button precedence idea, applied).
  3. **Summary card** (§3.2), shown by `showSummary(session)` on
     `sessionComplete` and dismissed by `[ NICE ]`/`Esc`.
- **Module surface**, matching the existing feature modules exactly:
  `isOpen()`, `renderSessions()`, `refreshIfOpen()`, `open()`, `close()`,
  `handleKeydown(e)`, plus `showSummary(s: SessionView)`. State is **pulled**
  from `state/store.ts` (`store.getState()`), never pushed. DOM is authored in
  `index.html` and grabbed once into a module-private `el = { … byId(…) }`; only
  the repeated recent-list rows are built with `createElement` +
  `replaceChildren` (the history-modal precedent). `render` before `showModal`,
  always; cleanup hangs on the dialog's own `'close'` event, not on each trigger.
  The name input uses the onboarding pattern: `maxlength="32"`,
  `autocomplete="off"`, `spellcheck="false"`, focus on open in the idle view,
  and `Enter` bound **on the input** (the global router deliberately never sees
  keys aimed at an input).
- **The modal gates nothing** — no open/close action, no `ws-client` hold.
  `setStoreOpenHoldDesired` is store-specific by name and stays untouched.
- **`main.ts` grows exactly three lines**: the import, `sessionsModal.refreshIfOpen()`
  inside `renderAll()`, and an `onSessionComplete(msg)` handler in
  `connectWsClient`'s handler object (which also needs one field on
  `WsClientHandlers` and one `case` in `ws-client.ts`'s message switch).

### 6.4 The always-visible indicator

A session the user cannot see from the main screen is a feature they forget is
running. `#session-pill` in `#titlebar` at **left 132, top 8, width 240,
height 8**, 8px font — the titlebar is empty between `#hud-level` (ends 120) and
`#menu-open` (starts 600), so nothing moves. Content: an 8px solid gold square
(`border-radius: 0` — the `#mood-dot` lesson) + the name truncated to 16 chars
with `…` + `H:MM:SS`. **Empty, and therefore invisible, when no session is
active** — the same idiom `#status-name` already uses. Owned by
`render/chrome.ts` (a render-layer module: reads state, sends nothing).

### 6.5 `?dev=1` fixtures

Three additions, following the P1 precedent:
1. `DEV_STATE.sessions` — an active session mid-flight with self-consistent
   numbers (its counters ≤ `DEV_STATE.stats.today`, a non-round elapsed like
   `4931`, a name that exercises truncation) plus 10 `recent` entries spanning
   two weeks with a mix of `endReason` values, at least one unnamed, and one
   whose `coinsEarned` is 0.
2. `DEV_STATE_NO_SESSION: Partial<StateMessage>` — the idle view.
3. `window.devSessionComplete(entry?)` in `dev-tools.ts` (with its `declare
   global` entry) to fire a synthetic `sessionComplete` locally, so the summary
   card can be visually gated without waiting 60 s. Documented as a one-liner
   comment, like `window.devApply(window.devStateOnboarding)`.

### 6.6 `docs/ui-spec.md`

Sessions gets a **real numbered section** (`§9`, after §8 Deferred): the rects,
the three views, the two actions, the `sessionComplete` message, `[W]`, and the
gates-nothing note. While in there, DOC-1 should also fix the **stale** §1 DOM
contract and §2.1 titlebar table (they still list `#store-open` in the titlebar
and omit `#menu-open`/`#menu-panel`/`#activity`/`#history`) — the
`add-a-menu-modal` skill's step 1 is "spec it in `docs/ui-spec.md` first, not in
code", and that only works if the spec is true.

---

## 7. Tests + gates

### 7.1 Engine (`internal/engine`)

- `SeesGlobalInput()` returns true only for `HonestyGlobal`.
- Every existing A2/A3 test and the ADR 0005 strategy-comparison/ceiling tests
  green and **unmodified**. `TickResult` field count asserted unchanged.

### 7.2 Game (`internal/game`) — the session accounting invariants

| Test | Asserts |
|---|---|
| `TestSessionCountsAreSubsetOfLifetime` | for every counter, `session == lifetime_end − lifetime_start`, and `≤ lifetime` at all times |
| `TestSessionDoesNotAffectTheEconomyAtAll` | the **lens rule**: the same 600-tick sequence run with and without a session yields identical `DevCash`, `XP`, `Progress`, `sprintIndex`, and all `today`/`lifetime` counters |
| `TestSessionCompleteGrantsNoCoinsOrXP` | ending a session leaves `DevCash`/`XP`/`Progress` unchanged (ADR 0005/0008 re-proven) |
| `TestStartStopGrantsNothing` | 1,000 start/stop pairs → unchanged economy, no log rows, `completed == 0` |
| `TestShortSessionIsDiscarded` | `< 60 s` → no record, no counter, a non-error flash |
| `TestSessionSurvivesRestartWithoutDoubleCounting` | `Snapshot` mid-session → `Apply` → keep ticking → counters continue from the baseline, never restart at 0, never double |
| `TestSessionSpansMidnight` | `SetClockForTest` across midnight: `today` resets, session counters keep growing, the session does not end |
| `TestSessionAccruesWhileStoreOpen` | counters advance under `StoreOpen`, `coinsEarned` does not |
| `TestIdleAutoEndBackdatesTheEnd` | after 2 h idle: `endReason=="idle"`, `endedAt == lastActivityAt`, and counters equal the watermark delta (so `idleSeconds ≤ durationSeconds`) |
| `TestBlindProviderNeverIdleAutoEnds` | a blind-honesty tick stream never triggers the idle rule; the 16 h cap does |
| `TestMaxDurationCap` | `endReason=="maxDuration"` at exactly the cap |
| `TestReopenAfterLongCloseEndsOnFirstTick` | a loaded stale session ends on the first tick, backdated |
| `TestSecondStartRejected` / `TestStopWithoutSessionRejected` | error flash, no mutation |
| `TestSessionFocusBlockMaxNeverExceedsDayMax` | the max invariant across the days a session spans |
| `TestSessionsThisWeekWindow` | local-date boundaries, including a session that ended exactly 7 days ago |
| `TestSessionNameNormalization` | control chars dropped, trimmed, truncated at 32 runes, empty is legal |
| `content_free_test.go` | `StateMessage` field count bumped with a justification comment; `checkExact` blocks for `SessionsView`, `ActiveSessionView`, `SessionView`, `SessionsSummary`; `TestStateMessageRejectsObservedContentFields` still green (the allow-list did **not** go soft) |

### 7.3 Store (`internal/store`) — the chain tamper matrix

Schema/round-trip: schema-5 → 6 load (no active session, empty head, **missing
`sessions` table is OK**); schema-6 round-trip with an active session and a
3-row log; future-schema-7 still refused to `.future`; a session ended across a
save/reload appears exactly once.

Tamper matrix, each expecting `ErrTampered` + `.invalid` + *original preserved
untouched*, mirroring DB-1 §3.3's table:

| # | Attack | Caught by |
|---|---|---|
| 1 | `UPDATE sessions SET payload=…` (edit a counter, or the name if Fork A had been taken) | row MAC mismatch |
| 2 | `UPDATE sessions SET mac=…` | row MAC mismatch |
| 3 | `DELETE` the **last** row | replayed head ≠ signed head |
| 4 | `DELETE` a **middle** row | id sequence break, then head mismatch |
| 5 | Swap two rows' ids | `payload.id != row.id` |
| 6 | Append a hand-written row | row MAC mismatch |
| 7 | `DELETE FROM sessions` with a non-empty head | zero rows + non-empty head |
| 8 | `DROP TABLE sessions` with a non-empty head | table missing + non-empty head |
| 9 | Edit only the `ended_at` mirror | mirror ≠ `payload.endedAt` |
| 10 | Edit `sessionLogHead` in the snapshot | the **snapshot** MAC fails first |
| 11 | Copy a valid `state` payload into a `sessions` row | domain separation (`logDomain` ≠ `macDomain`) |

Plus: `Load` (the wrapper) verifies the chain identically to `LoadAll` — a test
that a tampered log is caught through *both* entry points, so the convenient
function cannot be the hole. Plus atomicity: a forced failure inside
`AppendSession` leaves **neither** the row nor the new head (no false-tamper
state). Plus `content_free_test.go`: `SaveData`'s field count bumped and
`checkExact` blocks for `SessionSave` and `ActiveSessionSave`.

### 7.4 Server (`app/`, package `main`) — a new `sessions_wire_test.go`

Modelled on `identity_wire_test.go`:
- `TestApplyActionSessionStartStop` — a table over `applyAction` asserting
  `(mutated, flash.Kind)` for: start unnamed, start named, start while active
  (error), stop (session flash), stop with none active (error), a name with
  control characters, a 40-rune name truncated to 32.
- **`TestSessionNameNeverReachesTheProtectedSave`** — the Fork P2-A proof:
  marshal `store.Snapshot(g)` **and** the appended row's payload; assert the
  literal name appears in neither, and that no key in either contains `"name"`.
- `TestSessionShowsOnTheWire` — marshal `g.State()` and assert the exact
  camelCase tags and that the `sessions` block is **always** sent (`"active":null`
  when idle), the way `TestSetNameShowsOnTheWire` pins `"config":{"name":""}`.
- `TestSessionCompleteMessageShape` — the `sessionComplete` envelope's literal
  JSON shape.
- `TestSessionStopPersistsImmediately` — `state.db` mtime advances on stop,
  without waiting for the 30 s autosave.

### 7.5 Frontend

There is **no frontend test runner in this repo**, and P2 does not add one
(that is its own decision, not a P2 side quest). The guards are `tsc --noEmit`
strict, CI's bundle-drift diff against the committed `app/public/js/dexel.js`,
the Go-side wire tests above, and the real-game gate below.

### 7.6 The real-game gate (`feature-build-and-verify` — nothing is trusted until this passes)

Isolated mockups have lied to this project twice. Build the real binary, run it
with the fake provider, and judge with your own eyes:

1. **Live round trip.** `[W]` → type `auth refactor` → START → drive typing for
   ~90 s → the titlebar pill shows the name and a climbing clock; the modal's
   live counters climb → END → **the summary card shows real numbers**, and each
   one equals the corresponding `stats.lifetime` delta captured from the `state`
   messages themselves (assert against the *wire*, never against hand-typed
   expectations).
2. **Nothing is granted.** Mash START/END 20 times: Dev Cash, XP and the sprint
   bar do not move, the recent list stays empty, "sessions this week" stays put.
3. **Restart survival.** Start a session, kill the process, restart within a
   minute: the pill and live counters resume, the elapsed time is continuous, the
   counters did not reset or double.
4. **Backdated auto-end.** Start a session, stop the process, restart 3 h later
   (or with a clock shim): the session is closed on the first tick, `endReason`
   is `idle`, and the recorded duration ends at the last real keystroke — not 3 h
   long.
5. **Names stay out of the protected save.** `sqlite3 state.db 'select
   cast(payload as text) from state; select cast(payload as text) from sessions'`
   → the project name appears in **neither**. `cat config.json` → it is there.
6. **Tamper.** `sqlite3 state.db "UPDATE sessions SET payload = replace(cast(payload as text),'\"keystrokes\":<n>','\"keystrokes\":999999')"`
   → restart shows the reset baseline, one integrity log line, `state.db.invalid`
   exists, `config.json` (the Dexel's name **and** the project names) survives,
   and the freshly written `state.db` re-verifies on a third start.
7. **Delete the last row.** `sqlite3 state.db 'DELETE FROM sessions WHERE id=(SELECT max(id) FROM sessions)'`
   → the integrity line again (**not** "no save", **not** a silently shorter
   list), and no legacy re-grant in the log.
8. **Migration.** A real schema-5 `state.db` opens with no active session, an
   empty recent list, balances intact, and **no** integrity line.
9. **Visual gate.** `?dev=1` + `window.devSessionComplete()` → capture and run
   `scripts/visual-check.py`: a vision model must describe a finished, readable
   summary card — no clipped text (mind BUG-8's 346px cap), no overflow, no
   placeholder rectangles, and the pill legible in the titlebar.
10. **Zero console errors** in every step above.

---

## 8. Fan-out — exclusive file ownership, waves, exit criteria

### Contract seam — PIN these names (agents must not drift)

**Actions:** `SESSION_START{name?}`, `SESSION_STOP`.
**Message:** `{"type":"sessionComplete","v":1,"session":SessionView}`.
**Wire (`internal/game` + `frontend/src/wire.ts`):** `StateMessage.Sessions
SessionsView json:"sessions"`; `SessionsView{active,summary,recent}`;
`ActiveSessionView` / `SessionView` / `SessionsSummary` exactly as §6.1.
**Save (`internal/store`):** `SaveData.Session *ActiveSessionSave
json:"session,omitzero"`, `SaveData.SessionLogHead string
json:"sessionLogHead,omitempty"`; `ActiveSessionSave` / `SessionSave` exactly as
§5.1/§5.2; `CurrentSchema = 6`.
**Config:** `ConfigData.SessionNames map[string]string json:"sessionNames"`.
**DB:** table `sessions(id, ended_at, payload, mac) STRICT` + index
`sessions_ended_at`; `logDomain = "dexel-session-log-v1"`.
**Store API:** `LoadAll(path) (SaveData, []SessionSave, bool, error)`;
`Load` = wrapper; `AppendSession(path, d, s) (newHead string, err error)`.
**Game API:** `Game.StartSession(name string) error`,
`Game.StopSession() error`, `Game.TakeEndedSession() (SessionRecord, bool)`,
`Game.ActiveSession() (SessionRecord, bool)`, `Game.SessionLogHead() string`,
`Game.SetSessionLogHead(string)`, `Game.RestoreSessionLog([]SessionRecord)`,
`Game.SessionLogSnapshot() []SessionRecord`, `Game.RestoreSessionNames(map[int]string)`,
`Game.SessionNames() map[int]string`, `Game.NormalizeSessionName(string) string`.
**Engine:** `func (r TickResult) SeesGlobalInput() bool` — a **method**, no new
field.
**Constants:** §2.6's table.

### Tasks (exclusive owners, no shared files)

**Task GO-0 — engine (owns `app/internal/engine/**`).** Add
`TickResult.SeesGlobalInput()` + its test. No field, no economy change; every
existing engine test green and unmodified. Tiny, but it must land before GO-1
consumes it.

**Task GO-1 — game session core + wire (owns `app/internal/game/**`).** New
`session.go`: the constants, `SessionRecord`, the active-session state, the
delta/watermark model, start/stop/auto-end, the pending-record seam, the log
cache and the derived summary, `NormalizeSessionName` (extracting
`normalizeUserText` out of `identity.go`), the session-name map. Edits to
`game.go`: the auto-end check at the top of `Tick`, the two accumulator hooks in
`recordStats`/`awardCoins`, `SessionsView` on `StateMessage` and its population
in `State()`. Extend `content_free_test.go` (§7.2). All of §7.2's tests.

**Task TS-1 — frontend (owns `app/frontend/src/wire.ts`,
`features/sessions-modal.ts`, `features/keybindings.ts`, `main.ts`,
`state/ws-client.ts`, `render/chrome.ts`, `dev/dev-fixtures.ts`,
`dev/dev-tools.ts`, the `#sessions`/`#sessions-open`/`#session-pill` DOM in
`app/public/index.html`, and the Sessions/pill CSS in
`app/public/css/game.css`).** §6.1's types, the three-view modal, the `[W]`
tier, the pill, the `sessionComplete` handler + the no-op `onCelebrate` seam for
P3, the fixtures. `tsc --noEmit` clean; committed bundle rebuilt.
**Contract-only dependency on GO-1's field names (this document) — starts in
parallel.** Must not touch `features/menu.ts` (zero changes needed) or any
`store-modal`/`history-modal`/`activity-modal` file.

**Task GO-2 — persistence (owns `app/internal/store/**`).** `SessionSave` /
`ActiveSessionSave`; the two `SaveData` fields; `CurrentSchema = 6`; the
`sessions` DDL + index; the chained-MAC helpers in `integrity.go`;
`LoadAll`/`Load`/`AppendSession` and the `writeStateRowTx` refactor; the §5.4
gate steps; `ConfigData.SessionNames`; `Snapshot`/`Apply` carrying all of it
(`RestoreSessionLog`/`RestoreSessionNames` **before** `RestoreStats`, following
the A3 ordering rule). Extend `content_free_test.go`. All of §7.3's tests.
**Depends on GO-1's types/API.**

**Task GO-3 — server wiring (owns `app/main.go`, `app/hub.go`,
`app/handlers.go`, and the new `app/sessions_wire_test.go`).** The two action
cases in `applyAction`; the `actionSessionStart`/`actionSessionStop` literals;
the pending-record pop after both `g.Tick` and `applyAction`; the immediate
`store.AppendSession` + `SetSessionLogHead` write-through; the immediate
`SaveConfig` after `SESSION_START`; `broadcastSessionComplete`; boot-time
`LoadAll` + `RestoreSessionLog`/`RestoreSessionNames`. §7.4's tests.
**Depends on GO-1 and GO-2.**

**Task DOC-1 — docs (owns `docs/**` except the two files this design pass owns,
plus `dev_docs/production-runtime/MIGRATION_PLAN.md`).** `docs/ui-spec.md`: the
new §9 Sessions, the two §6.2 action rows, the `sessionComplete` message in
§6.1, `[W]` in §5.2, **and the stale §1/§2.1 fixes** (§6.6).
`docs/adr/README.md`: the ADR 0017 row. `docs/plan/ROADMAP.md`: P2's status line.
`MIGRATION_PLAN.md`: PR-5 becomes **6 → 7** and gains "add `pausedSeconds` to the
session delta set" (Fork P2-B). Runs in parallel from wave 1.

### Waves

```
Wave 1:  GO-0  →  (GO-1  ‖  TS-1  ‖  DOC-1)
Wave 2:  GO-2                                  (needs GO-1's types)
Wave 3:  GO-3                                  (needs GO-1 + GO-2)
Wave 4:  overseer clean-cache re-verify  →  the real-game gate (§7.6)
```

No two tasks share a file: `engine/*` → GO-0; `game/*` → GO-1; `store/*` → GO-2;
`main.go`/`hub.go`/`handlers.go` → GO-3; `wire.ts`/`features/sessions-modal.ts`/
`keybindings.ts`/`main.ts`/`ws-client.ts`/`chrome.ts`/`dev/*`/`index.html`/
`game.css` → TS-1; `docs/*` + `MIGRATION_PLAN.md` → DOC-1.

**Serialization warning 1 — schema.** No other schema-bumping task (PR-5 above
all) may run concurrently with GO-2, per `MIGRATION_PLAN.md` sequencing
constraint 3.

**Serialization warning 2 — an UNCOMMITTED task is already in the blast radius.**
At the time of writing, the working tree carries in-flight, uncommitted changes
from the production-runtime **PR-1 (paths)** work: a new `app/internal/paths/`
package plus edits to `app/internal/store/config.go`, `app/internal/store/store.go`,
`app/main.go`, `app/main_test.go` and `app/version.go`. Those are **exactly**
GO-2's and GO-3's owned files. PR-1 must be **landed (or reverted) before wave 2
starts** — `MIGRATION_PLAN.md`'s own sequencing constraint 2 already says PR-1
"must not run concurrently with any other task inside `app/internal/store/`".
Wave 1 (GO-0/GO-1/TS-1/DOC-1) touches none of those files and can proceed
regardless.

### Exit criteria (ROADMAP style)

- A user starts an optionally-named session, works, and ends it; **the summary
  card shows real duration / effort / coins-earned-during-it / best focus
  block**, each number equal to the wire's own lifetime deltas.
- **Abandoning loses nothing and is never scolded**: a discarded short session,
  an idle auto-end, and a killed process all leave the economy intact and produce
  no negative message anywhere.
- **No new `Snapshot` field** — `activity/content_free_test.go` byte-identical,
  `NumField() == 5`. **`TickResult` gains no field.**
- **The anti-mash economy is unchanged (ADR 0005 re-proven)** and **the session
  grants nothing** — both by unit test, plus gate step 2.
- **The project name is provably absent** from `state.db` (both the snapshot
  payload and every log row) and present in `config.json` — by unit test and gate
  step 5.
- **Sessions survive restarts**; a stale session auto-ends **backdated** on the
  first tick (gate steps 3–4).
- **The chain detects every row edit, deletion, truncation, reorder and forgery**
  (§7.3's 11-row matrix) with the `.invalid` quarantine, the economy reset, the
  legacy import unreachable, and `config.json` untouched.
- **Schema-5 saves migrate to 6 non-destructively**; a missing `sessions` table
  with an empty head is the honest empty log; **schema 7 is still refused**.
- `go vet ./...`, `go test -race ./...` and `CGO_ENABLED=0 go test ./...` green
  **from a clean cache**; `tsc --noEmit` clean; the committed bundle matches a
  fresh build.
- **Content-free structural coverage exists for every new type**
  (`SessionsView`, `ActiveSessionView`, `SessionView`, `SessionsSummary`,
  `SessionSave`, `ActiveSessionSave`), with both field counts bumped
  deliberately and each new field justified in a comment.
- **The in-game gate (§7.6) passes, judged with the overseer's own eyes**, plus
  the `scripts/visual-check.py` verdict on the card.

---

## 9. Residual risks and explicitly deferred items

- **Project names can desync from the log** (deleted `config.json`, a hand edit,
  an id reused after a discarded short session). Accepted and honest: a session
  renders unnamed, the counts are never affected, and `SESSION_START` overwrites
  or clears its id's entry. Signing the names was rejected (§2.7).
- **Typing a project name earns a few crumbs of work**, because the Sessions
  modal deliberately gates nothing. Bounded: 32 keystrokes × `WorkPerUnitRate
  0.008` ≈ 0.26 work units, about 0.5 % of the smallest (50-unit) sprint — and because the
  baseline is taken *at start*, those keystrokes land **outside** the session, so
  the session's own numbers are clean by construction. A general "freeze earning
  while a text input has focus" rule is a possible follow-up, named not built.
- **A wall-clock duration can exceed observed time** when the process was closed
  mid-session. Honest by design (ticks are the only thing Dexel can count), and
  bounded by the 2 h idle rule and the 16 h cap. `observedSeconds` is
  client-derivable from two fields already sent if it is ever wanted.
- **The chain is verified in full at every boot**, O(n) HMACs. Trivial today;
  measured, not assumed, in the handoff. If it ever matters, the fix is a
  checkpoint MAC every K rows — named, not built.
- **The anti-cheat ceiling is exactly where ADR 0014 left it.** The key is public
  in the source by deliberate choice; a user who reads it can forge a chain just
  as they could forge a row. SQLite is a container, not a security boundary.
- **Deferred, named so they do not creep:** renaming or deleting a completed
  session from the UI; `SESSION_RENAME`; session goals/targets/quotas of any kind;
  session tags or categories; per-session app breakdowns (ADR 0009 territory); a
  sessions row in the A3 History modal (P6 owns the merge); exporting a session
  card (PRODUCT-EVOLUTION's local-only ceiling); multiple concurrent sessions;
  the long-lived `*store.DB` handle (still deferred — a handful of writes a day);
  a frontend test runner.
