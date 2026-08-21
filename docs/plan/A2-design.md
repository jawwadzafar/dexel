# A2 design — priced, content-free signals & diversified earning (v1.2)

Design pass for ROADMAP.md "Phase A2 (v1.2)". This is the gate: it fixes the
signal set, the pricing, the economy math, the wire/save shape, and the
fan-out. No implementation code is written here. Companion decision record:
`docs/adr/0012-a2-content-free-signal-set-and-permission-fork.md`.

---

## 0. Forks that need a USER/overseer decision (surface these first)

Both have a recommended default; A2 can ship on the defaults without asking.

- **Fork A — copy/paste chord.** It is **impossible permissionlessly on
  macOS** (evidence in §1). Options: (A1) ship A2 without it *[recommended]*;
  (A2) open a dedicated opt-in `CGEventTap`/Accessibility permission phase now.
  Default taken here: **A1 — drop from A2, defer to a future opt-in phase.**
- **Fork B — app-switch earning.** Detectable content-free on **macOS only**
  (Linux has no focus detection, ADR 0009). Options: (B1) track + display it
  but it earns **0** *[recommended]*, keeping the economy identical on both
  platforms; (B2) let it earn macOS-first at a small capped weight, accepting
  platform-asymmetric earning until Linux focus detection lands (A3). Default
  taken here: **B1 — display-only, earning weight 0, spec'd so B2 is a
  one-constant flip.**

Everything below is designed to make either fork choice cheap to switch.

---

## 1. Per-signal verdict table (the hard question, answered)

Evidence is from the real providers (`app/internal/activity/provider_darwin.go`,
`provider_linux.go`, `provider.go`) and the platform APIs.

| Signal | macOS (permissionless) | Linux (/dev/input) | Internal forbidden observation? | Derivable from existing? | Verdict |
|---|---|---|---|---|---|
| **Copy/paste chord** (Cmd/Ctrl+C/V) | **NOT achievable.** `CGEventSourceSecondsSinceLastEventType` counts by event **type** only (keyDown/mouseMoved/dragged/scroll) — no keycode, no modifier flags. A Cmd+C is an ordinary keyDown. Needs a `CGEventTap` = Accessibility permission (ADR 0010 avoids this). | Technically possible: evdev exposes `code`. But requires reading KEY_LEFTCTRL + KEY_C/KEY_V. | **YES** — forces the provider to inspect **keycode identity** (ADR 0002's "no key identity"), even if only a count crosses the boundary. | No. | **DROP** from A2; defer to opt-in permission phase. |
| **App-switch** | **Achievable.** `NSWorkspace.frontmostApplication.localizedName` is already sampled → `SanitizeAppID` (ADR 0009 sanctioned). Count = engine diff of `ActiveApp` across ticks. | **NOT achievable.** `provider_linux.go` deliberately never sets `ActiveApp` (Wayland focus is compositor-specific; ADR 0009 says degrade to none). Reads 0. | No new observation on macOS — app identity already crosses the boundary. | Yes, on macOS, from `Snapshot.ActiveApp`. | **SHIP as tracked/displayed (macOS); earn 0 by default (Fork B).** |
| **Focus-session** (sustained-typing block) | **Achievable.** Pure derivation from the keystroke recency the engine already tracks (`lastKeystrokeAt`, per-tick `keyDelta`). No new API. | **Achievable.** Same derivation from `KeystrokeCount` deltas; evdev already feeds keystroke counts. | **No** — needs only keystroke *timing*, which is already on the boundary. | **Yes** — zero new observation. | **SHIP as the new EARNING signal (both platforms).** The clean win. |

**Key structural finding:** the two shipped signals need **no new `Snapshot`
field and no new provider observation**. Both are derived in the **engine**
from data ADR 0009/0010 already put on `Snapshot` (app identity + keystroke
timing). `activity/content_free_test.go` and the `Snapshot` struct stay
**unchanged** — itself the proof that A2 widened no observation surface.

---

## 2. Final signal set A2 implements

| Signal | Status in A2 | Platforms | Earns? |
|---|---|---|---|
| Keystrokes | existing | both | yes (unchanged, ADR 0005) |
| Mouse-active | existing | both | yes (unchanged, ADR 0005) |
| Sprints completed | existing | both | (is the coin payout) |
| **Focus-session** | **new** | **both** | **yes — work bonus** |
| **App-switch** | **new** | **macOS** (0 on Linux) | **no by default** (Fork B flips it on) |
| Copy/paste chord | **dropped** | — | deferred to opt-in permission phase |

Rationale for calling focus-session "diversified earning" even though it is
one new earner: it rewards a *different axis* — sustained depth, not raw
volume. It is the one new signal that is impossible to fake by mashing
(§4), so it strengthens the anti-mash story rather than straining it.

---

## 3. Coin pricing table (data table, like the upgrade catalog)

Coins (`DevCash`) keep coming from **one source: sprint completion** (ADR
0008) — no second payout path, so nothing double-pays and migration is
trivial. Each signal earns by contributing **work units** into the sprint
(ADR 0005's model), which then pays out. "Coin value" below is derived from
that; the modal shows the *actual attributed* coins (§6), never a nominal.

All constants live beside ADR 0005's in `internal/engine`, tunable in one place.

| Signal | Work weight | Cap / gate | Nominal coin value* | Rationale |
|---|---|---|---|---|
| Keystroke | `KeystrokeWeight = 1.0` → 0.008 work/key | coalesced ≤10/s (100ms window) | ~0.0045 coins/key | Real typing is real work (ADR 0005 baseline, unchanged). |
| Mouse-active | `MouseWeight = 0.25` → 2.5 weighted/s → 0.02 work/s | 1 flag / 100ms | ¼ of typing/s | Scrolling/moving is lighter work (ADR 0005, unchanged). |
| **Focus-session** | `FocusSessionBonusWork = 2.0` per completed session | `FocusSessionSeconds = 120` sustained typing; `FocusGapToleranceSeconds = 3` | ~1.1 coins/session | Depth bonus (~+35% over the keystrokes it rides on); unreachable by mashing or by mouse. Tunable 1.5–2.5. |
| **App-switch** | `AppSwitchWork = 0.0` (default) — set 0.1 for Fork B | `AppSwitchDailyCap = 40`; ≥1s between counted switches (1s tick) | 0 (default) / ≤~2 coins/day (B2) | macOS-only; earning off keeps the economy cross-platform-identical. Any safe weight is marginal — which is *why* earning is deferred. |

*Nominal coin value = work × the sprint table's average payout rate
(~0.564 coins/work across the six sprints). Illustrative only.

---

## 4. Economy math — extending ADR 0005 (anti-mash proof sketch)

Per 1s tick today (`engine.go`):
`weightedRate = keyDelta·1.0 + (mouseActive ? 10·0.25 : 0)`, clamped to
`MaxRecentRate = 15`, `work = weightedRate · 0.008`.
- 6 keys/s typist → work 0.048/s. Mouse-only → 0.02/s (≈2.4× less). Ceiling 0.12/s.

**A2 adds two engine-derived contributions, folded into `WorkUnits`:**

1. **Focus-session bonus.** Maintain a sustained-typing run: a tick with
   `keyDelta>0` extends it; a gap > `FocusGapToleranceSeconds` breaks it. When
   the run first reaches `FocusSessionSeconds`, add `FocusSessionBonusWork`
   and start the next session window.
   - *Anti-mash / mouse<typing:* mouse never sets `keyDelta`, so mouse-only
     **can never** earn a focus bonus — the invariant holds by construction.
     A masher is still capped at ≤10 counted keys/s (100ms coalescing) and
     must spend 120 real wall-clock seconds per session; amortized the bonus
     is 2.0/120 ≈ 0.017 work/s, riding *on top of* genuine typing (0.048/s)
     and only while it is genuinely sustained. The per-second ceiling barely
     moves and only for real deep work.

2. **App-switch (default weight 0).** `work += AppSwitchWork` per counted
   switch, gated by `AppSwitchDailyCap`. At the default 0.0 it is a no-op on
   the economy (pure analytics). Under Fork B (0.1, cap 40): ≤4 work/day ≈
   ≤2 coins/day — strictly below one hour of mouse-only (~72 work/hr) and far
   below typing; a switch-masher cannot out-earn either.

**New strategy-comparison test the implementer MUST add** (extends
`engine_test.go`'s `TestMouseOnlyEarnsLessThanTyping`), over a fixed window
(e.g. 300 ticks), with focus-session ON and app-switch earning at its default 0:

| Strategy | Definition | Expected |
|---|---|---|
| `deepFocus` | 6 keys/s **continuous** (completes focus sessions) | **highest** |
| `steadyTypist` | 6 keys/s in 30s-on/10s-off cycles (never reaches 120s continuous → no focus bonus) | 2nd |
| `mouseOnly` | mouse-active every tick, no keys | 3rd |
| `appSwitchMasher` | app changes every tick | == idle (earns 0 at default weight) |
| `idle` | nothing | 0 |

Assertions: `deepFocus > steadyTypist` (bonus only *adds* to a real typist);
`steadyTypist > mouseOnly` (**ADR 0005 invariant re-proven**);
`appSwitchMasher == 0` at default weight (and, in a Fork-B variant of the
test, `0 < appSwitchMasher < mouseOnly << steadyTypist`); no strategy exceeds
the pre-A2 per-tick ceiling except `deepFocus`, and only by the amortized bonus.
Keep the existing `TestMouseOnlyEarnsLessThanTyping` and ceiling backstop
tests green unchanged.

---

## 5. Provider / Snapshot / wire / save shape changes

### Provider trait & `Snapshot` — NO CHANGE
Neither signal needs a new observation. `activity.Provider`, `Snapshot`, and
`activity/content_free_test.go` are **untouched**. (State this explicitly in
the PR: the absence of a Snapshot change is the privacy evidence.)

### `engine.TickResult` (internal engine→game boundary) — new count fields
- `FocusSessionsCompleted uint64` (0/1 this tick)
- `AppSwitches uint64` (0/1 this tick)
- `WorkUnits` already carries the folded focus bonus (+ app-switch work only
  if Fork B enabled). Engine gains: a sustained-typing run tracker and a
  `lastActiveApp string` to diff.

### Wire — `internal/game`
- `StatCounters` (used by both `today` and `lifetime`) gains:
  `FocusSessions uint64`, `AppSwitches uint64`. Both counts → pass the
  structural test.
- New nested type `CoinBreakdown { Keystrokes, Mouse, FocusSessions, AppSwitches uint64 }`
  (coins attributed **today**), exposed as `StatsView.CoinsToday CoinBreakdown`.
  All `uint64` → content-free.
- `StateMessage` itself: `Stats` field already present; no top-level change.

### `content_free_test.go` (game) — MUST be extended
- `allowedStatCounters` += `FocusSessions:"uint64"`, `AppSwitches:"uint64"`.
- `allowedStatsView` += `CoinsToday:"game.CoinBreakdown"`.
- Add a `checkExact` block for `CoinBreakdown` (all `uint64`, forbidden-substring
  scan). The top-level `StateMessage` allow-list count is unchanged (Stats
  already listed).

### Coin attribution (how "→ N coins today" stays honest)
Coins come only from sprints, so attribute them at payout: the game keeps
in-memory per-signal work accrued **since the last sprint award**
(`workKeys, workMouse, workFocus, workSwitch` floats). On sprint completion,
split `def.DevCash` across those in proportion to their work share and add the
integer coins to `statsToday`'s `CoinBreakdown`; reset the accumulators. Only
the resulting **integer coin counts** are persisted/emitted — no work floats
ever cross the wire or hit disk. This guarantees the modal's per-signal coins
always sum to the DevCash actually earned today.

### Save — schema bump 2 → 3
`internal/store`: `StatCountersSave` += `FocusSessions`, `AppSwitches`
(json `focusSessions`, `appSwitches`); `StatsSave.Today` gains a persisted
`CoinBreakdown` (its own `CoinBreakdownSave`). `CurrentSchema = 3`. A schema-2
file lacks these keys → `json.Unmarshal` leaves them 0 → correct "none yet",
no migration code beyond the bump (same pattern as the 1→2 note in `store.go`).
The `ErrFutureSchema` guard already refuses a newer save and preserves it
untouched — **that rule stays intact**; existing balances/XP/owned/equipped
are read exactly as before. `store/content_free_test.go` allow-lists extended
to match.

---

## 6. Activity-modal UX

Extend the read-only Activity modal (`features/activity-modal.ts`). Under the
existing today/lifetime count rows, add:
- **Focus sessions** — `today.focusSessions` / `lifetime.focusSessions` (count).
- **App switches** — `today.appSwitches` / `lifetime.appSwitches` (count;
  shows 0 honestly on Linux — no special-casing).
- A **"Coins earned today"** breakdown block reading `stats.coinsToday`:
  `Keystrokes 1,240 → 6`, `Mouse → 2`, `Focus sessions 3 → 4`,
  `App switches → 0`, with the total matching today's earned DevCash.

Wire fields the frontend needs (all optional in TS so a stale server degrades
to 0, matching the existing pattern):
- `StatBlock` += `focusSessions?: number`, `appSwitches?: number`.
- new `interface CoinBreakdown { keystrokes; mouse; focusSessions; appSwitches: number }`.
- `Stats` += `coinsToday?: CoinBreakdown`.
The modal formats counts with `fmtInt`; no server-side formatting. Update
`docs/ui-spec.md` §6.1 and the activity-modal DOM ids.

---

## 7. Implementation plan — parallelizable, by exclusive file ownership

Respect the F2 module boundaries: frontend changes touch only
`features/activity-modal.ts`, `wire.ts`, `state/store.ts` (+ the modal's DOM
in `app/public/index.html`), never the render/state-client layers.

**Task GO-1 — engine derivation (owns `internal/engine/*`)**
- Add the sustained-typing run tracker + focus bonus; add `lastActiveApp`
  diff; add `FocusSessionsCompleted`, `AppSwitches` to `TickResult`; fold
  focus bonus (and Fork-B app-switch work behind a 0-default constant) into
  `WorkUnits`. Add the new constants beside ADR 0005's.
- Add the strategy-comparison test (§4). Keep existing ADR 0005 tests green.

**Task GO-2 — game aggregation + wire (owns `internal/game/*`)**
- `StatCounters` += `FocusSessions`, `AppSwitches`; add `CoinBreakdown` +
  `StatsView.CoinsToday`; implement per-signal work accrual + proportional
  coin attribution at sprint completion (§5); wire into `recordStats`/`Tick`.
- Extend `game/content_free_test.go` allow-lists + `CoinBreakdown` block.
  Depends on GO-1's `TickResult` fields.

**Task GO-3 — persistence (owns `internal/store/*`)**
- `StatCountersSave` + `CoinBreakdownSave`; bump `CurrentSchema = 3`; extend
  `statCountersToSave`/`FromSave` and `Snapshot`/`Apply`; extend
  `store/content_free_test.go`. Add a schema-2→3 load round-trip test.
  Depends on GO-2's game types.

**Task TS-1 — frontend (owns `frontend/src/wire.ts`,
`features/activity-modal.ts`, activity-modal DOM in `app/public/index.html`;
touches `state/store.ts` only if a selector is needed)**
- Extend `wire.ts` types (§6); render the two new counts + the coins-today
  breakdown in `activity-modal.ts`; add the DOM ids. `tsc --noEmit` clean.
  Contract-only dependency on GO-2 (field names); can start in parallel off
  this doc.

**Task DOC-1 — ADR/spec (owns `docs/`)** — ADR 0012 (done); update
`docs/ui-spec.md` §6.1 with the new stats/coin fields and the modal rows;
add the ADR row to `docs/adr/README.md`.

Suggested waves: GO-1 ‖ TS-1 ‖ DOC-1 → GO-2 → GO-3. Then the in-game gate
(build Go binary, run with the fake provider, screenshot the modal via
headless Chromium) before A2 is called done — per the feature-build-and-verify
skill; a focus-session/app-switch fake-script step should be added so the
modal shows non-zero new counts on the Linux dev box.

---

## 8. Verification exit criteria (from ROADMAP.md A2)
- Earning is signal-diverse: focus sessions visibly add coins on top of
  typing; strategy test proves the ordering.
- Anti-mash re-proven: `mouseOnly < steadyTypist`, `appSwitchMasher` can't win.
- Per-signal pricing visible in the modal and tuned in one constants block.
- Migration: a schema-2 save loads with balances intact and new counters at 0;
  future-schema refusal still fires.
- No content anywhere: `Snapshot` unchanged; extended structural tests green
  in `activity`, `game`, and `store`.
