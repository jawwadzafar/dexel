# dexel — Product Evolution

**Status:** design-only decision document (Opus, 2026-08-22). No code. No commit.
**Audience:** the owner, in the morning, deciding where dexel goes after the
current infra wave lands.
**Scope:** synthesises the owner's 10-direction brief against the *real*
codebase and returns a single coherent product thesis, the strongest bets, the
rejections, and a phased plan tied to the systems each phase would extend.

This document does not re-decide anything already settled. The privacy model
(ADR 0002/0009), honest mechanics (ADR 0010), the engine stack (ADR 0011), and
the anti-mash economy (ADR 0005) are **invariants**, not open questions. Every
proposal below lives inside them or it does not ship.

---

## 0. Where dexel is today (the ground truth this plan builds on)

dexel is a cozy, behind-the-shoulder pixel developer companion. The loop that
shipped at v1.0.0 and has been extended through A1/A2/A3 and F1/F2:

- **Real, content-free activity → work.** The engine samples one `Snapshot`
  per second (`KeystrokeCount`, `MouseActive`, `IdleSeconds`, `ActiveApp`,
  `ActiveAppDisplay` — five fields, and `content_free_test` fails the build if
  a sixth appears). It produces `TickResult.WorkUnits` under the anti-mash
  clamp (`MaxRecentRate 15`, `WorkPerUnitRate 0.008`, typing weight 1.0 vs
  mouse 0.25).
- **Work → sprints → Dev Cash + XP.** Six static sprints rotate; completing one
  pays `DevCash`/`XP` and bumps a level (`levelForXP`, curve `50·(n-1)·n`,
  derived-not-stored). This is the *only* coin source (ADR 0008).
- **Dev Cash → a wardrobe.** Eight slots, own-many/equip-one, data-driven
  catalog, runtime tints. Every purchase visibly changes the scene. Adding
  content is "one catalog row + sprites from `gen_assets.py`", by design.
- **Honest presence.** Moods (`coding`/`idle`/`onBreak`) mean what they say —
  `coding` needs a real recent keystroke; a blind provider can never claim
  `onBreak`. The character sprite already animates: `currentDevFrame()`
  alternates `type_a`/`type_b` at 5 fps while coding, and maps `idle`/`onBreak`
  to `idle`/`sleep` frames.
- **A private analytics spine.** Per-day `StatCounters`, a per-signal
  `CoinBreakdown`, a 30-day dense `History []DayStat`, and a server-computed
  `StreakView{Current,Longest}` — all surfaced through the `StatsView` on the
  `StateMessage` wire contract, all rendered read-only in the Activity `[A]` and
  History `[H]` modals.
- **A clean frontend to extend.** F2 layers: `render/*` (paint only, never send
  an action), `state/*` (typed store + WS client), `features/*` (modals own
  their DOM + are the only senders of their actions), `wire.ts` (the typed
  contract). Adding a surface is a known 7-step recipe.

**Near-term infra already decided (this plan sits on top of it, does not
re-explain it):**

- **UI-1 — hamburger menu + coin top-left.** One ☰ menu replaces the
  `[A]/[H]/[S]` launchers, *explicitly structured so "future sections
  (Sessions/Goals) slot in"*. The information architecture for everything below
  is already being built.
- **SEC-1 — save integrity / anti-cheat.** Splits a **user-editable CONFIG**
  (named example: the dexel's name) from **PROTECTED game state** (Dev Cash,
  owned items, XP, sprint, history), HMAC-signing the protected fields. This is
  the enabling primitive for *identity* (a legitimate place for a user-authored
  name/goal) **and** for *earned collectibles* (a protected grant that can't be
  hand-edited in).
- **F3 — Tauri desktop shell.** Native window, mac/windows/linux, x86_64+arm64.
  The delivery surface; orthogonal to the product features below.

**The honest reading of today's product:** the *mechanics* are excellent and
the *loop closes* — but the loop is **passive and undifferentiated**. Coins
accrue silently in the background; every hour of typing looks like every other
hour; there is no intention going in and no punctuation coming out. dexel keeps
you company but you never *tell it anything* and it never *marks an occasion*.
That gap — not a missing feature — is what the evolution should close.

---

## 1. The product thesis (the through-line)

> **dexel is the coziest witness to your work. You set the intention; dexel
> keeps you company and honestly reflects the effort you actually put in; and
> together you build a memory of the journey.**

Three verbs, in order: **INTENTION → HONEST REFLECTION → MEMORY.**

This thesis is forced by the invariants, not bolted onto them. Honest mechanics
(ADR 0010) mean dexel can *never* know what you did — it sees counts and
durations, never content, never outcomes. So there is exactly one honest way to
add goals, journeys, and meaning: **the user supplies the intention and the
knowledge; dexel supplies the companionship, the honest effort-tracking, and
the celebration.** dexel is a *witness*, not a *tracker*; a *companion*, not a
*coach*. It never grades you, never infers, never nags.

Everything in the brief maps onto the three verbs:

| Verb | Brief directions | What dexel provides |
|---|---|---|
| **Intention** | Sessions (1), Goals (2), Journeys (3), Onboarding/Identity (9) | A container for "I'm setting out to do this" that the user opens and closes |
| **Companionship** | Character life (6), World expansion (5) | A living, growing space that feels present in your day |
| **Memory** | Progression (4), Achievements/Moments (7), Personal journey (8) | Honest celebration and a scrapbook of what you and dexel did together |
| *(future)* | Social (10) | Deferred — a local, user-initiated export at most; never accounts/servers |

**The anti-thesis — what dexel must never become:** a productivity dashboard, a
Pomodoro timer with quotas, a habit app that guilt-trips a broken streak, a
coach that scores your focus, or a surveillance tool that infers what you were
doing. Every rejection in §4 is an application of this one sentence.

---

## 2. Direction-by-direction review

For each: what it **reuses**, what it **costs**, whether it **fits**, and a
verdict. Costs are relative (Small/Medium/Large) against the F2 architecture.

### 1. Sessions — **STRONGEST. Ship first of the product features.**
- **Reuses:** the engine already emits everything a session needs —
  `TickResult.FocusRunSeconds`, `FocusSessionsCompleted`, keystroke delta,
  mood. `StatCounters`/`CoinBreakdown` already tell "coins earned this window".
  UI-1's hamburger is being built with a Sessions slot in mind. No new
  `Snapshot` field — the privacy proof is that `content_free_test`'s
  `NumField()==5` never changes.
- **Cost:** Medium. New `SESSION_START`/`SESSION_END` client actions; session
  state on `Game` + a `session` block on `StatsView`; a schema bump for the
  in-progress session and a lightweight session log (folds naturally beside the
  A3 `History` buckets); `features/session.ts` + a session-complete overlay in
  `render/overlays.ts`.
- **Fit:** this *is* the Intention pillar. It's the container that turns silent
  accrual into "I sat down to work, dexel kept me company, here's what we did."
  The optional project name is user-typed CONFIG (like the SEC-1 name), never an
  observed signal — it lives on the editable side of the SEC-1 split and never
  leaves the machine.
- **Risk & guard:** the failure mode is "Pomodoro dashboard". Guard it: no
  targets, no quotas, no efficiency score, no "you were only 60% focused";
  abandoning a session loses nothing and is never scolded; the summary is a cozy
  card (duration, focus blocks, coins earned this session, a dexel celebrate
  animation), not a stats readout. The game already speaks this language —
  "sprint", not "task".
- **Verdict: SHIP (Phase P2). The keystone of the whole thesis.**

### 2. Goals — **fit is perfect, but do NOT ship as a standalone feature.**
- **Reuses:** the SEC-1 config side (goals are user-authored data, like the
  name — never inferred). The honesty rule is a *gift* here: goals with
  user-controlled milestone completion is the textbook honest mechanic (ADR
  0010) — dexel is structurally incapable of pretending it knows you finished
  something.
- **Cost:** Medium as data; the danger is in the presentation.
- **Fit:** philosophically flawless, but a bare goals-with-checkboxes modal is a
  **todo list**, which the brief explicitly warns against and which weakens the
  product (§4). Goals only become a *dexel* feature when they are made
  game-like — i.e. Journeys (#3).
- **Verdict: SHIP the DATA MODEL only, as the substrate of Journeys. Never ship
  a "Goals" modal that reads as a task manager.**

### 3. Journeys / Quests — **the right expression of Goals. A LATER capstone.**
- **Reuses:** the static-table pattern that already powers sprints and the
  catalog (preset journeys = one more data table); the Moments system (#7) for
  milestone celebration; the world/collectibles pipeline (#5) as the reward a
  milestone grants. A journey milestone marked done → a Moment fires → a
  cosmetic is granted → the scene changes. That chain reuses four existing
  systems.
- **Cost:** Large — it's the integration of several other systems, which is
  exactly why it comes late.
- **Fit:** excellent, *provided the rewards already exist to reward against.*
  Shipping journeys before Moments/Collectibles/Character-life would make
  milestones feel like a checklist with no payoff — the todo-list failure again.
- **Verdict: SHIP LAST of the product phases (P5), once P2–P4 give it teeth.**

### 4. Long-term progression — **partly already built; add sparingly, PULL not PUSH.**
- **Reuses:** XP + levels already exist and are on the wire (`Level`, `XP` on
  `StateMessage`); streaks already exist (`StreakView`). The scaffolding is
  done.
- **Cost:** Small to add badges/unlocks (they ride Moments, #7); Large and
  *wrong* to add pressure systems.
- **Fit:** the brief's own constraint is the whole answer — "without creating
  pressure/guilt or a surveillance vibe." Progression must be **pull**
  (unlocks you discover, cosmetics you're delighted to find) not **push**
  (targets, quotas, loss-framed streaks, nag notifications).
- **Verdict: DO NOT build a standalone progression phase. Fold XP/levels
  (already shipped) and new badges into Moments (#7) and Journeys (#5). Reframe
  the existing streak gently (§P6). Explicitly reject the push mechanics (§4).**

### 5. World expansion — **the cheapest high-value content stream; make it continuous.**
- **Reuses:** the catalog is *built* for this. ADR 0008: "lots of options in
  future is a content problem, not a systems problem." A new item is one row +
  `gen_assets.py` sprites. New tintable slots are `tintable:true` + a `*_form`
  layer, no code.
- **Cost:** Small per item (art time only), within the **parked art fidelity**
  — new items *in the current style* are on-brand and cheap. **Multiple
  scenes / environment overhaul is a different, Large thing:** it collides with
  the parked art ceiling and with the load-bearing monitor geometry (the
  terminal rect the whole composition points at). That is deferred, not near.
- **Fit:** perfect as the **reward currency** for Sessions, Moments, and
  Journeys. It also gains a valuable new *axis*: **earn-only collectibles** —
  cosmetics you cannot buy, only earn from a Moment or a Journey (SEC-1 protects
  the grant). That gives rewards weight without inflating the Dev Cash economy.
- **New idea (grounded): ambient time-of-day room light.** The scene already
  has "one pool of light"; shifting it with the *local clock* (morning / dusk /
  night — content-free, no work signal at all) makes dexel feel present in your
  day for a small render change. High cozy payoff, zero privacy surface.
- **Verdict: run World content as an ALWAYS-ON track alongside every phase, not
  as a phase. Add earn-only collectibles when Moments land (P4). Defer
  multi-scene to a deliberate future art pivot.**

### 6. Character life — **STRONGEST for emotional ROI; the animation hook already exists.**
- **Reuses:** the frame system is *already in the renderer* —
  `currentDevFrame()`, `FRAME_FOR_STATE`, the 5fps coding loop, the
  `dev_form_*`/`dev_base_*`/hoodie composite. Adding life = more frames from
  `gen_assets.py` + a richer scheduler in `render/scene.ts`. No backend, no
  wire, no privacy change.
- **Cost:** Medium (real art work — multiple frames per state — but purely
  presentational and additive to the *current* style, i.e. motion, not a
  fidelity chase; the same category the dithering rollout was allowed under).
- **Fit:** this is the difference between a widget and a companion, and it
  *amplifies every other feature* — a celebrate on a real session-complete, a
  blink/stretch at idle, a mood-reactive pose. It is honest by construction as
  long as the trigger is honest.
- **Risk & guard:** state-based animations must obey ADR 0010 — a "celebrate"
  fires only on a *real* sprint/session/milestone event, a "focused" pose only
  on real keystrokes. Never a fake "working hard" loop while idle. And **no
  simulation/tamagotchi needs** (the brief says "alive *without* simulation
  mechanics"): dexel never gets hungry, never decays, never guilt-trips you for
  being away.
- **Verdict: SHIP (Phase P3). Second product feature, because it makes
  everything after it land harder.**

### 7. Achievements / Moments — **STRONGEST as connective tissue; ship with Sessions.**
- **Reuses:** every trigger already exists — first sprint, first named session,
  named-your-dexel, a gentle consistency streak, first journey milestone. The
  `render/flash.ts` + `render/overlays.ts` layers already do celebration; the
  P3 character animation is the celebration body; earn-only collectibles (#5)
  are the reward.
- **Cost:** Medium — a static Moments table (like sprints/catalog), an
  earned-set on the protected save (SEC-1), a `moments` field on `StatsView`.
- **Fit:** Moments are the **celebration layer** that ties Sessions, Journeys,
  progression, and World into one game instead of a feature list. The brief's
  key instruction is honoured by design: celebrate **firsts, journeys, and
  consistency** — never generic "work X hours" surveillance-flavoured grinds.
- **Verdict: SHIP (Phase P4), tightly coupled to P3's animation and to earn-only
  collectibles.**

### 8. Personal journey / history — **not a new feature; evolve the A3 modal into a scrapbook.**
- **Reuses:** A3 already shipped the History `[H]` modal, `History []DayStat`,
  and `StreakView`. The brief is explicit: build ON it, don't duplicate.
- **Cost:** Small-Medium — `history-modal.ts` gains a "memory/scrapbook" view
  over the Moments (#7), Journeys (#5), and world-ownership data already on the
  wire. No new backend observation.
- **Fit:** this is the home of the Memory pillar — a cozy look-back at moments
  earned, journeys completed, cosmetics acquired, and dexel's growth. It only
  has anything to show *after* Moments/Journeys exist, so it comes late.
- **Verdict: SHIP LATE (Phase P6) as an evolution of the existing modal, and
  reframe the streak here as gentle "consistency", not a loss-framed counter.**

### 9. Onboarding / Identity — **high leverage, cheap, rides SEC-1. The fast-follow.**
- **Reuses:** SEC-1 *already* introduces the user-editable CONFIG with the
  dexel's name as its named example — identity is half-built by infra. Starter
  style = the free tier-0 hoodie + one of the six free tints the wardrobe
  already grants on first launch. A first-launch flow is a `features/*` module +
  the "no save exists" branch the store already handles.
- **Cost:** Small-Medium (one modal + config plumbing that SEC-1 mostly lays).
- **Fit:** the first minutes decide whether someone *bonds* with a companion.
  Naming your dexel and picking its look is the emotional front door, and it
  makes every later feature feel personal ("*your* dexel completed a journey").
- **Risk & guard:** keep it to ~30 seconds — name + one colour + a warm hello.
  Not a wizard. Returning users never see it.
- **Verdict: SHIP EARLY (Phase P1) — cheapest emotional leverage, and it needs
  only SEC-1, which is already in flight.**

### 10. Social / community — **OUT OF SCOPE. Named so it doesn't creep.**
- **Fit:** future only, per the brief. The *only* privacy-safe form is a
  **local, user-initiated export** of a screenshot / achievement card / journey
  summary that the user chooses to share — never an account, never a server,
  never telemetry, never a comparison/leaderboard (leaderboards are a pressure
  mechanic, §4).
- **Verdict: NOT in the phased plan. Recorded in §5 "Out of scope" with its one
  acceptable future shape, so nobody designs a backend for it.**

### Ideas the brief is missing (grounded in what dexel already is)

- **The honesty keystone — "dexel reacts to what you mark."** When the *user*
  marks a goal/journey milestone done, dexel celebrates (P3 animation + a P4
  Moment). This is the single design move that makes Goals honest *and* joyful:
  the user supplies the knowledge (ADR 0010), dexel supplies the joy. It is the
  philosophical center of gravity for directions 2/3/6/7 and should be stated as
  a rule, not just a feature.
- **Earn-only collectibles** (folded into #5/#7 above): a reward axis distinct
  from the bought wardrobe, protected by SEC-1, so Moments and Journeys have
  weight without touching the Dev Cash economy calibration (ADR 0005).
- **Session-framed sprints** (a P2 refinement): the character's sprint is
  ambient today; letting a user session *frame* the on-screen sprint deepens
  Sessions using the existing sprint system — **while strictly preserving ADR
  0009's spatial split** (the sprint name is the character's flavour, bottom-
  left; the activity line is the user's reality, bottom-right; they never merge).
- **Ambient time-of-day light** (folded into #5): reflects *your time*, never
  *your work* — the safest possible way to make dexel feel present.

---

## 3. The strongest bets

Three, in the order they should land, because each makes the next land harder.

### Bet 1 — Sessions + the session-complete moment (P2)
The keystone. It adds the **Intention** the loop is missing and the
**celebration** it never delivers, built almost entirely on signals the engine
*already* emits (`FocusRunSeconds`, focus sessions, keystroke delta) with **no
new privacy surface** (`content_free_test`'s field count is untouched). It turns
"coins appeared while I typed" into "I sat down to work with dexel, and here's
what we did together." Everything else in the thesis hangs off this container.

### Bet 2 — Character life (P3)
The cheapest **emotional** ROI in the whole plan, because the animation
machinery already exists (`currentDevFrame`, frame-per-state, the 5fps coding
loop). Motion turns a HUD widget into a *companion*, and it is a force
multiplier: a session-complete, a milestone, a mood shift all suddenly have a
*body*. Honest by construction — animations react to real events only.

### Bet 3 — Moments + earn-only collectibles (P4)
The **connective tissue**. Moments are what make Sessions, Journeys,
progression, and World feel like one game rather than four features. Grounded in
"firsts, journeys, and consistency" (never "work X hours"), celebrated with the
Bet-2 body, and rewarded with cosmetics that live *outside* the Dev Cash
economy (so no rebalancing risk) but *inside* the scene (so every reward is
visible, per ADR 0008).

*Honourable mention:* **Onboarding/Identity (P1)** is nearly a fourth bet — it
is cheap (rides SEC-1), high-leverage (the emotional front door), and is
sequenced *first* precisely because it makes Bets 1–3 feel personal. It is a
"fast-follow to infra" rather than a headline bet only because it depends on
SEC-1 landing.

---

## 4. What is rejected, and why

Naming these is the point of the document — they are the feature-bloat and
philosophy-erosion risks that a "just do the brief" reading would walk into.

- **Goals as a standalone todo/checklist feature.** REJECTED. A bare
  goals-with-checkboxes modal is a productivity app; the brief warns against it
  and it dilutes a cozy game into task management. Goals ship *only* as the data
  substrate of Journeys (P5), never as their own surface.
- **Push/pressure progression:** loss-framed streaks ("you broke your streak!"),
  daily quotas, XP grind walls, streak-freeze panic mechanics, and *any* nag
  notification. REJECTED wholesale. They are the exact "pressure/guilt/
  surveillance vibe" the brief and the thesis forbid. Progression stays
  discoverable pull.
- **Pomodoro / productivity-dashboard framing of Sessions.** REJECTED. No
  targets, no efficiency scores, no "focus percentage", no shaming an abandoned
  session. Sessions are cozy work-companionship, not a timer with a grade.
- **Character-life *simulation* (tamagotchi needs).** REJECTED. The brief says
  "alive *without* simulation mechanics." dexel never gets hungry, never decays,
  never guilt-trips you for being away. Life = ambient/reactive animation only.
- **Multiple scenes / full environment overhaul — DEFERRED (not rejected).** It
  collides with the parked art fidelity and with the load-bearing monitor
  geometry (the terminal rect the composition is built around). Incremental
  in-style items are cheap and continuous; a new *scene* is a deliberate future
  art pivot, not a near-term bet.
- **Social / community beyond a local user-initiated export.** OUT OF SCOPE now.
  No accounts, no servers, no telemetry, no leaderboards/comparison (comparison
  is a pressure mechanic). The only ever-acceptable shape is exporting a
  screenshot/achievement card the user chose to share.
- **Anything that infers outcomes or content.** PERMANENTLY REJECTED by ADR
  0010/0002/0009: dexel never marks a milestone done itself, never claims you
  "learned"/"finished"/"worked on" anything, never adds a sixth `Snapshot`
  field. The user marks; dexel witnesses.
- **Already-deferred items stay deferred, named not built:** copy/paste
  earning (ADR 0012, needs a consent-gated `CGEventTap`) and hourly buckets
  (ADR 0013, would reconstruct a daily schedule). Neither is revived by this
  plan.

---

## 5. The phased plan

Ordering principle: **infra first (already in flight) → cheap personal front
door → the intention keystone → the body that celebrates it → the celebration
system → the capstone that needs all of the above → the memory that looks back
on it.** World content runs continuously alongside. Version numbers follow the
ROADMAP's `v1.x` cadence and are indicative.

### Foundation (already decided — this plan depends on it)
UI-1 (hamburger IA for new sections), SEC-1 (config/protected split — enables
identity *and* earn-only collectibles), F3 (Tauri shell). No product feature
below should land before UI-1 and SEC-1, which it builds on.

---

### Phase P1 — Identity & first minutes  *(v1.4, Small)*
Name your dexel + pick a starter hoodie/tint on first launch; a warm hello.
Rides the SEC-1 config so the name lives on the *editable* side of the integrity
split (a name is user data, not economy state).
- **Extends:** SEC-1 config (name field); the existing "no save exists" startup
  branch; the catalog's free tier-0 items/tints (already granted on fresh
  install); new `features/onboarding-modal.ts` per the modal recipe; `wire.ts` +
  `StateMessage`/`StatsView` gain a `config`/`name` field (camelCase, optional).
- **Exit criteria:** first launch with no save shows the intro; user sets a name
  + one colour in ~30s; the name persists, survives the SEC-1 integrity check as
  *editable*, and is echoed in the HUD/titlebar; returning users never see it;
  `content_free_test` field count on `Snapshot` unchanged; gated in the real
  running game.

### Phase P2 — Sessions & the session-complete moment  *(v1.5, Medium) — Bet 1*
An intentional, user-started work session with an optional project name; active
time + focus blocks during it feed a cozy session-complete card + reward. Game-
framed, no quotas.
- **Extends:** engine signals already present (`FocusRunSeconds`,
  `FocusSessionsCompleted`, keystroke delta, mood — no `Snapshot` change);
  `Game` gains session state + `SESSION_START`/`SESSION_END` client actions;
  `StatsView` gains a `session` block; save schema **4 → 5** (additive: in-
  progress session + a lightweight session log beside the A3 `History` buckets;
  `ErrFutureSchema` guard preserved); `features/session.ts` + a completion
  overlay in `render/overlays.ts`; UI-1 hamburger gains the Sessions section it
  was built to hold. Optional project name is user-typed CONFIG, never observed.
- **Exit criteria:** user starts a (optionally named) session, works, ends it; a
  summary card shows real duration / focus blocks / coins earned *this session*;
  abandoning loses nothing and never scolds; **no new `Snapshot` field** (the
  privacy proof); anti-mash economy unchanged (ADR 0005 re-proven); schema-4
  saves migrate to 5 non-destructively; gated live.

### Phase P3 — Character life  *(v1.6, Medium) — Bet 2*
Ambient idle animation (blink, stretch), mood-reactive poses, and a **celebrate**
animation fired by *real* events (sprint/session complete, and later milestones).
- **Extends:** `tools/gen_assets.py` adds frames per state (palette-locked,
  deterministic, additive to the *current* style — motion, not a fidelity
  chase); `render/scene.ts` gains a richer animation scheduler keyed off the
  `activeState`/mood it already reads. **No backend, no wire, no privacy change.**
- **Exit criteria:** dexel visibly blinks/stretches at idle and shifts pose with
  mood *honestly* (celebrate fires **only** on a real event, never fabricated,
  never a fake "busy" loop while idle); deterministic sprites; no perceptible
  perf/battery regression (dexel is left open all day — ADR 0011's cost
  promise); overseer visual gate passes with own eyes.

### Phase P4 — Moments & earn-only collectibles  *(v1.7, Medium) — Bet 3*
A Moments system: firsts + gentle consistency + (later) journey milestones fire
a P3 celebration and grant an **earn-only** cosmetic. Add ambient time-of-day
room light as the first "world feels present" touch.
- **Extends:** a static Moments table (like sprints/catalog); an
  earned-collectibles set on the **protected** save (SEC-1 — can't be
  hand-edited in); `StatsView` gains `moments`; catalog gains earn-only items
  (granted, not priced — still visible on screen per ADR 0008); `render/flash` +
  `overlays` for the celebration; the History `[H]` modal begins listing earned
  moments; save schema **5 → 6** (additive). Time-of-day is a `render/scene.ts`
  change off the local clock only.
- **Exit criteria:** each defined Moment fires **exactly once** on its *real*
  trigger, grants its collectible, and celebrates; collectibles cannot be bought
  or hand-edited in (SEC-1 verified); **no "work X hours" grind achievements**;
  time-of-day light shifts with local clock and touches no work signal; gated
  live.

### Phase P5 — Journeys (preset + custom) = Goals made game-like  *(v1.8, Large) — capstone*
User-defined long-term goals with milestones the **user** marks done; preset
journeys from a static table; each milestone celebrates (P4 Moment) and may
grant a collectible.
- **Extends:** a goals/journeys data model on the **user-authored** config side
  (never inferred — the ADR 0010 keystone); a preset table like the catalog;
  `features/journeys.ts`; a `MARK_MILESTONE` client action that **only the user
  can trigger**; `wire.ts` + `StatsView` expose journey progress; reuses P4
  Moments/collectibles for rewards; save schema **6 → 7** (additive). Presented
  as journeys/quests with visual progress, **never a checklist** (§4).
- **Exit criteria:** user creates a custom journey and picks a preset; marking a
  milestone (**user action only** — dexel never auto-completes) celebrates and
  may grant a collectible; **nothing dexel observes ever advances a milestone**
  (the honesty proof — assert it in a test the way anti-mash is asserted); gated
  live.

### Phase P6 — Memory (the scrapbook)  *(v1.9, Small-Medium)*
Evolve the A3 History `[H]` modal into a cozy look-back: completed journeys,
earned moments, collectibles, dexel's growth over time. Reframe the streak
gently as "consistency", never a loss-framed number.
- **Extends:** `features/history-modal.ts` gains a memory/scrapbook view over
  the Moments (P4), Journeys (P5), and world-ownership data **already on the
  wire** — **no new backend observation.** Streak presentation softened
  (celebratory, never guilt).
- **Exit criteria:** the modal shows a warm timeline of moments/journeys/
  cosmetics and dexel's growth; the streak is presented without loss-framing; no
  content stored anywhere (structural test still green); does not duplicate the
  A1/A2 Activity analytics; gated live.

### Always-on — World content stream  *(continuous, Small per item)*
Incremental, in-style catalog additions (new items, new tintable slots, wall/
desk decor, earn-only collectibles) as reward fuel for P4/P5. **Not a phase — a
continuous track**, run alongside every phase, staying within the parked art
fidelity (one catalog row + `gen_assets.py` sprites, per ADR 0008).

---

### Out of scope (named so they do not creep)
- **Multiple scenes / environment overhaul** — a deliberate future art pivot,
  blocked today by parked fidelity + load-bearing monitor geometry.
- **Social / community** — future only; at most a *local, user-initiated*
  screenshot/achievement-card export. No accounts, servers, telemetry,
  leaderboards, or comparison.
- **Copy/paste earning** (ADR 0012) and **hourly buckets** (ADR 0013) — stay
  deferred, named not built.
- **Any outcome/content inference; any pressure mechanic (quotas, loss-framed
  streaks, nags); any tamagotchi-style needs simulation.** Permanently out, by
  thesis and by ADR 0010.

---

## 6. One-paragraph summary for the morning

dexel's mechanics are done and honest; what's missing is *intention going in*
and *occasion coming out*. The evolution is one thesis — **you set the
intention, dexel honestly reflects the effort, and together you build a memory**
— realised in a sequence that spends art and code where the emotional return is
highest and the privacy risk is zero: name your dexel (P1, rides SEC-1), sit
down to a session and get a warm summary (P2, the keystone, built on signals we
already have), give dexel a living body to celebrate with (P3, the animation
hook already exists), turn real firsts and consistency into celebrated moments
with earn-only cosmetics (P4), then — and only then — let users author journeys
they mark themselves (P5) and look back on the whole thing in a scrapbook (P6),
with a steady stream of in-style world content feeding the rewards throughout.
We ship none of the productivity-app traps — no quotas, no guilt streaks, no
todo lists, no inference, no social backend — because the point of dexel is to
be the coziest witness to your work, not a manager of it.
