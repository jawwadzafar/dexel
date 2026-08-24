# Backlog — PROPOSED, nothing here is built

> **Every entry on this page is a proposal or an open question.** None of it is
> implemented, none of it is scheduled, and none of it has been decided.
> If you are looking for how Dexel works, every other page in this directory
> is the built behaviour; this one is not.

Scope, to keep this from becoming a second roadmap:
[`docs/plan/PRODUCT-EVOLUTION.md`](../plan/PRODUCT-EVOLUTION.md) owns the
product roadmap (phases P1–P6, and the directions that were explicitly
rejected). [`docs/plan/BUGS.md`](../plan/BUGS.md) owns bugs. **This page holds
open questions about mechanics that already exist** — the things a reader of
the other pages in this directory will bump into and ask "why is it like that?"

Each entry carries a status:

- **PROPOSED** — an idea with a shape, no decision.
- **OPEN QUESTION** — a real tension in shipped behaviour, needing a call.
- **FOUND IN CODE** — something verified to be unfinished, unreachable, or
  contradicting its own documentation.

---

## 1. Per-app-type animation — PROPOSED

**Raised by the owner.** When the user is in a browser, or a chat app, or
anything that is not an editor, the character and the scene should animate
*differently* than when coding — and "we could have monitor renderer also and
it all should be random".

### What exists to build on

- **The classification already exists**, as of commit `12accf0`.
  `activity.AppTypeOf` maps a sanitised app id to one of ten classes —
  `coding`, `terminal`, `browser`, `comms`, `design`, `media`, `notes`, `files`,
  `unknown`, `self` — from an explicit 88-entry table with no substring
  guessing. See [`moods.md`](moods.md) §3. This is a large part of what this
  proposal would otherwise have had to build, and it means the design question
  is now "what varies per class", not "how do we get a class".
- The class is currently consumed **only** by the activity line, which never
  touches work units. ADR 0010 explicitly defers per-app-class *economy*
  weighting. **Any animation work must stay on the same side of that line.**
- The activity line's own solution to this exact problem is the template to
  copy: a **tiered pool** where the tier a phrasing sits in encodes how much it
  claims, and the tier that would claim too much is simply *empty* for the
  classes that cannot support it. The same shape applies directly to poses —
  a "heads-down typing" pose belongs in a tier only `coding`/`terminal` can
  reach, and every other class gets postures that assert presence only.
- The scene already has one scenery element that reacts to state:
  `#sprite-monitor` gets `.monitor-onbreak` (a `brightness(0.55)` filter) while
  `onBreak`. That is the entire "monitor renderer" today — one static
  `monitor.png` with the fake terminal text drawn over its glass area.
- The nine-frame system, the 200 ms timer, the "swap only when the frame
  changed" discipline and the ambient scheduler are all in place. Adding poses
  is cheap; adding a *second* animation system would not be.

### The honesty constraint, which decides most of the design

**The scene must not imply an activity Dexel cannot observe.** This is the same
rule that produced the mood table, and it bites here in a specific way:

Dexel knows *which application is frontmost* and *that a key went down
somewhere in the last N seconds*. It does **not** know that those two facts are
about each other. That is exactly why "Coding in dexel" was a bug and why the
mouse pose is driven by a real rise in `mouseActiveSeconds` rather than by a
periodic beat.

So the safe shape is: **a pose may reflect posture, not purpose.** "Leaning
back, one hand on the mouse, reading" is a posture consistent with the observed
signals (browser frontmost, low keystroke rate, mouse active). "Reading
documentation" is a purpose, and Dexel cannot know it. The same test applies to
the monitor: showing a *browser-shaped chrome* on the glass is a claim about
what is on the user's screen, which Dexel has never been allowed to see. Showing
a *different abstract pattern* is not.

The randomness the owner asked for is a help here, not a risk: a randomly
chosen variation cannot be read as a specific claim precisely because it is not
tied to anything. The existing ambient scheduler already establishes that
pattern — jittered bands, "the timers here are a display loop and mean
nothing".

### What could vary

| Layer | Today | Could vary by app type |
| --- | --- | --- |
| character pose | 9 frames, driven by mood + mouse recency | a "reading/browsing" posture set; a "meeting" posture (hands off keys, facing forward) |
| the monitor glass | one static sprite, dimmed while `onBreak` | an abstract pattern per class — dense text-ish for editor, wide blocks for browser, a call-shaped grid for chat — plus randomised variants within each |
| the terminal pool | one pool, partitioned by mood | pools partitioned by mood **and** class (still compile-time constants, still fiction) |
| the ticker pool | same | same |
| ambient beat rate | one band | a slower band for "reading" postures |

### Open questions

1. **Where does the class cross the wire?** `activity.AppTypeOf` lives
   server-side, and `StateMessage` carries no app type today — the class is
   consumed entirely inside `ActivityLine` and never leaves the server. The
   frontend would need either a new `StateMessage` field (which has to be
   justified on the content-free allow-list, though a closed ten-value enum is
   an easy case) or would have to re-derive the class from `activityLine`'s
   text, which the project's standing rule ("the client never re-derives what
   the server could have sent") argues against.
2. **What happens where there is no class?** Linux never reports an app, and
   macOS can report `AppIdentityAvailable: false`. Those are *different* from
   "an app we do not recognise", and the three cases probably want three
   answers. Whatever is chosen, "not measured" must not render as a confident
   pose.
3. **How much art?** Each new pose is three layers (`dev_form_*`,
   `dev_base_*`, and alignment with the one hoodie overlay via
   `FRAME_OVERLAY_DY`) and must be the approved pose displaced, not redrawn —
   `tools/gen_assets.py` enforces that with a centroid/mass ladder check and a
   hand-position assertion. A monitor variant set is cheaper: one sprite each,
   no rig, no tint. **The art track is currently PARKED at its present fidelity
   by an explicit owner decision** ([ROADMAP.md](../plan/ROADMAP.md)) — this
   proposal needs that parking to be lifted, or to be scoped to the monitor
   only, which needs no character art at all.
4. **Is the monitor a store slot?** There is history here: v0.3 had a `monitor`
   upgrade track, and `docs/upgrade-design.md`'s migration table **refunds** it
   ("`monitor` 1 | 150 | — (no such slot) | — | **refund 150**") because v2
   dropped the slot. Re-introducing a monitor as a *cosmetic slot* and
   introducing it as an *app-type-driven renderer* are different features that
   would compete for the same 132×64 pixels.

### The cheapest honest first slice

Monitor-glass variation only. No new character art, no new wire field if the
frontend classifies from the `activeApp` it already receives, no economy change,
and it is the layer where "abstract pattern, randomly chosen" is most obviously
not a claim about the user's screen. It would also make the existing
`.monitor-onbreak` dimming look deliberate rather than lonely.

---

## 2. Naming: "Sprint", "units", "LV" — OPEN QUESTION

**Raised by the owner**, in their words: *"'Sprint' and title is also very
generic we should do something else for this and why it's unit and what's
level"*.

This section lays out the problem and the options. **It deliberately does not
pick.** This is a product decision.

### The actual problem, stated precisely

Three labels, three different failures:

**"Sprint" is a real word from the user's real job, used for a fiction.** The
player is a developer. "Sprint" already means something specific to them — a
two-week planning increment with tickets and a review. Dexel's sprint is a
progress bar that fills at 0.008 units per keystroke and pays 25 coins. The
collision makes the fiction read as a claim about their actual work, which is
the one thing every other part of this product is careful not to do. Note that
`sprint.go` already carries a *rule* about this at the item level ("a sprint
name must never be phrased so it could be read as a description of the user's
real activity") — the rule protects the individual names, but the category word
itself was never held to it.

**"Unit" names a quantity with no referent.** As [`progression.md`](progression.md)
§1 spells out, one unit is 125 keystrokes, or 50 seconds of mouse activity, or
half a focus session. It is an internal float. "34 / 75 units" tells a player
nothing about what 75 is, how far away it is, or what they did to earn 34.
`docs/upgrade-design.md` records that the word came from the original mockup
("keep the real numbers, keep the mockup's word") — so it was inherited, never
chosen.

**"LV 3" is an RPG convention imported without its mechanics.** In the genre it
comes from, a level gates content or grants power. Here it does neither: it is a
pure derived display of XP, and XP itself comes from the same event as Dev Cash
and buys nothing. So the HUD carries two numbers (`Dev Cash`, `LV`) that rise
together from one event, where one of them is spendable and the other is inert.
A player reasonably reads `LV` as "progress toward something" and there is
nothing at the other end.

### Options for the sprint/task container

| Option | Reads as | Trade-off |
| --- | --- | --- |
| keep **Sprint** | familiar, zero migration | the collision above; a reviewer already flagged it as generic |
| **Task** / **Ticket** | still workplace vocabulary | same collision, weaker flavour |
| **Job** / **Contract** / **Gig** | the Dexel has its *own* freelance work, separate from yours | needs the sprint names to sound like commissions, not your backlog; strongest separation of fiction from reality |
| **Quest** | unambiguously game | may read as childish for a developer tool; also collides with the P5 "Journeys" phase already planned |
| **Build** / **Run** / **Cycle** | on-theme, short, fits a HUD | "Build" already means something specific in a dev tool; "Cycle" is vague |
| **Chapter** / **Log entry** | fits the P6 scrapbook direction | implies narrative content that does not exist |

A structural observation, offered without a recommendation: the fiction is
*stronger* the further the vocabulary gets from the user's real job, because the
whole design rests on the player never confusing Dexel's claims with claims
about themselves. "Sprint" is the closest of any of these to their real work.

### Options for the unit

| Option | Reads as | Trade-off |
| --- | --- | --- |
| keep **units** | honest about being abstract | says nothing; the current complaint |
| **%** (a percentage of the current sprint) | immediately legible, no new concept | loses the sense of a shared currency across sprints; "77%" of a thing you did not choose |
| **lines** (of the fictional code) | fits the terminal fiction perfectly | *claims* a line count, and the numbers would be nonsense (a 50-unit sprint is 6,250 keystrokes) — arguably an ADR 0010 problem |
| **commits** / **steps** / **tokens** | game-y, no real-world referent | another word with no referent — trades one abstraction for another |
| **keystrokes**, priced 1:1 | maximally honest and self-explaining | forces a ×125 rescale of every target, and prices mouse and focus work *in keystrokes*, which is its own small lie |
| show **time-to-finish** instead of a count | the number a player actually wants | requires estimating from a recent rate, i.e. a prediction — and a wrong prediction is exactly the kind of confident claim this project avoids |

### Options for the level

| Option | Trade-off |
| --- | --- |
| keep **LV n** and give it something to do | the honest fix, but it is a *feature*, not a rename — and ADR 0008's "bought not unlocked" rule means it must not gate store items |
| rename to something inert-sounding — **Day 14**, **Streak**, **Tenure**, **Rank** | matches what it is (a tenure display) without promising power |
| **remove it from the HUD** and keep XP only in the Activity modal | frees the title bar; loses the one at-a-glance "you have been at this a while" signal |
| merge it with the streak, which is already computed and already means "you kept showing up" | the streak is arguably the number `LV` is pretending to be |

### The thing worth deciding first

All three labels are downstream of one unanswered question: **is the fiction
about the *Dexel's* workday, or a gamified view of *yours*?**

- If it is the Dexel's own job, the vocabulary should be *its* ("contracts",
  "commissions") and the abstraction of "units" stops being a problem, because
  the player is not supposed to map it onto their own output.
- If it is a view of yours, the labels should be measurable things you did
  (keystrokes, focus blocks, active hours) and "units" has to go.

Today's build is halfway between the two, which is why all three words feel
placeholder at once. Renaming without answering this would just relabel the
ambiguity.

---

## 3. Focus-session thresholds — OPEN QUESTION

The focus-session bonus is the only earning signal phase A2 added
([`economy.md`](economy.md) §4). It appears not to fire in real use.

### The knobs

| Constant | Value | What it controls |
| --- | --- | --- |
| `engine.FocusSessionSeconds` | `120.0` | how long a run must reach |
| `engine.FocusGapToleranceSeconds` | `3.0` | the longest typing gap a run survives |
| `engine.FocusSessionBonusWork` | `2.0` | what a completed run pays |

Because ticks are one second apart, `3.0` means the run breaks on the **fourth**
consecutive second with no counted keystroke. Completing one session therefore
requires landing a keystroke in at least one of every three consecutive seconds,
without a single exception, for two full minutes.

### The measurement

Recorded from the owner's own machine, one day: **2,289 keystrokes, 999 active
seconds, 0 focus sessions.** Not one, all day.

Meanwhile the mood machine called that same activity `coding` for 999 seconds,
using `CodingRecencyWindow = 10 * time.Second`. **The two subsystems are
answering the same question — "is this person typing right now?" — with a 10 s
window and a 3 s window, and they disagree about 999 seconds of the day.**

### What the bonus is worth, and what it delivers

Verified arithmetic against the constants:

| | |
| --- | --- |
| Bonus per completed session | 2.0 work units |
| Keystroke work in 120 s at 6 keys/s (the rate the engine's own tests model) | 120 × 6 × 0.008 = 5.76 units |
| **Bonus as a share of the typing it rides on, at 6 keys/s** | **+34.7%** |
| Keystroke work in 120 s at the measured 2.29 counted keys/active-second | 120 × 2.29 × 0.008 = 2.20 units |
| Bonus as a share, at the measured rate | +91% |
| **Delivered, on the measured day** | **0** |

So: a signal budgeted at roughly a third of the keystroke earnings it sits on
top of, worth proportionally *more* at realistic typing rates than at the rate
it was calibrated against, paying nothing.

Note also that the engine's own strategy test *proves* the bonus works — its
`deepFocus` fixture types 6 keys every single second for 300 ticks and the test
`t.Fatalf`s if that completes zero sessions. It is a correct test of the
mechanism. It is not a model of a human, and nothing in the suite is.

### The knobs, and what each would cost

**Do nothing.** Defensible: the bonus is meant to reward genuinely unbroken
typing, and the honest answer may be that this user does not do that. The cost
is that a shipped, priced, documented and displayed earning signal reads as
broken to the one person who can see its counter sitting at zero — and the
Activity modal shows that zero.

**Raise `FocusGapToleranceSeconds`.** The most targeted change: it is the
constant that actually breaks the runs, and raising it to roughly the mood
machine's 10 s would make the two subsystems agree about what "typing right now"
means. Risk: a longer tolerance makes "sustained" weaker, and at some value a
run stops meaning anything. Worth noting the current value's asymmetry — a 3 s
gap tolerance with a 120 s target means the *tolerance*, not the target, is what
you fail on.

**Lower `FocusSessionSeconds`.** Also effective, and cheap. But 120 s is the
number that makes the block feel like *focus* rather than a lucky streak; halving
it would fire far more often and mean less. Note that a completed session resets
the run's clock rather than the run itself, so a shorter target compounds: a
genuinely continuous typist would earn the bonus twice as often *and* keep it.

**Reprice `FocusSessionBonusWork`.** Orthogonal, and only worth touching *after*
one of the two above, because repricing a signal that fires zero times cannot
change anything. If the thresholds loosen, this is the dial that keeps total
earning where ADR 0005 calibrated it.

**Change the run tracker's shape.** Rather than a hard gap that resets the
clock, the run could tolerate a budget of quiet seconds within the window
("110 of the last 120 seconds had a keystroke"), or use the same recency window
the mood machine uses. This is the only option that fixes the *disagreement*
rather than moving the threshold, and it is the only one that is a code change
rather than a constant change.

### What any change here must not break

- `TestStrategyComparisonA2`'s ordering: `deepFocus > steadyTypist > mouseOnly
  > appSwitchMasher == idle`. In particular `steadyTypist` (30 s on, 10 s off)
  must still *never* complete a session — so `FocusGapToleranceSeconds` cannot
  reach 10 s without that fixture becoming a focus-completing strategy.
  **That test constrains the most obvious fix**, and it is worth deciding
  whether the fixture or the constant is the thing that should change.
- Mouse must remain unable to start, extend or complete a run. That holds by
  construction today (mouse never sets `KeystrokeDelta`) and any rewrite of the
  tracker must preserve it structurally, not with a runtime check.
- ADR 0005's total-earning calibration.

### The same question, one layer up

The measurement raises a second issue that is not about focus sessions at all.
2,289 keystrokes over 999 active seconds is **2.29 counted keystrokes per active
second**. A mouse-active second is worth the equivalent of 2.5 keystrokes. So on
the measured day, **a second with the mouse in motion was worth slightly more
than a second of that user's real typing** — while ADR 0005's headline property,
and two unit tests, assert that typing beats mouse. Those tests are not wrong;
they model 6 keys/s. But the invariant they protect is a per-second one, and it
holds only above 2.5 counted keystrokes per second.

Whether that matters depends on what `MouseWeight` is *for*. It is a real open
question and it belongs beside this one, because both are the same discovery:
**the economy was calibrated against a modelled typist, and never re-checked
against a measured one.** One day of data from one machine is not a basis for
retuning — but it is a basis for measuring properly before anyone touches a
constant.

---

## 4. Found in the code — FOUND IN CODE

Verified while writing this directory. Each is a small, specific gap between
what the code does and what something in the repo says it does. None is a
crisis; all are cheap to fix and each one costs a future reader time.

### 4.1 The `[P]` pause shortcut does not exist

The menu button is labelled `[P] PAUSE` / `[P] RESUME`, in the same bracketed
style as `[S] STORE`, `[A] ACTIVITY`, `[H] HISTORY` and `[W] SESSIONS` — all
four of which *are* bound. `keybindings.ts` has no `p` case. Pause is reachable
only by clicking that button or from the CLI. Either bind the key or drop the
bracket.

### 4.2 The Desk Bot does not blink

`app/assets/buddy_bot_b.png` exists. `catalog.go` says, twice, that `buddy_bot`
is "a 2-frame blink animation" and that "the frontend derives frame B by the
same `_a`/`_b` convention it already uses for `dev_form_type_a/b`". The frontend
does not: `renderSlotSprite` sets `item.sprite` (frame A) and nothing else, and
there is no `_a`/`_b` handling for any slot. So a 250-Dev-Cash item whose
flavour text is "Blinks. Judges. Blinks again." never blinks, and one shipped
asset is unreferenced. Either implement the convention the comment describes, or
correct the comment and the flavour text.

### 4.3 `persistConfig` resets the `autostart` field

Every `SET_NAME` and every `SESSION_START` rewrites `config.json` from a fresh
literal with no `Autostart`, so that field is reset to `""`. This contradicts
`config.go`'s own "written ONLY by `dexel autostart enable`/`disable`" and
`main.go`'s own adjacent claim that the write never clobbers the other half.
`cmd_autostart.go` does the correct load-modify-save and has a test for it;
`persistConfig` has neither. Impact is bounded — the field is advisory and
`status` asks the OS — but the "your config disagrees with the OS" advisory will
fire spuriously after any rename. Details in
[`persistence.md`](persistence.md) §6.

### 4.4 Today's longest focus block is lost on restart

`statsFocusBlockMax` has no `Restore` counterpart and no field in the persisted
save, so it resets to 0 on a restart while every other "today" counter survives.
It reaches disk only when the day finalises. The A3 comments are explicit
wherever a gap *is* intentional and this one is silent, which suggests an
oversight — *but that reading is inference, not verified intent.* See
[`sessions.md`](sessions.md) §3.

### 4.5 The 350 ms terminal cadence is not observable

`terminalInterval = 350 * time.Millisecond` advances the terminal buffer but the
branch does not broadcast, and the client only receives `screenLines` on the 1 s
state frame. So roughly three lines advance at once, once a second, instead of
scrolling at 350 ms. `docs/ui-spec.md` §3.2 specifies "push a line every 0.35s"
and the constant's comment cites that section. No test covers it and no comment
acknowledges the gap. This may be an accepted consequence of the 1 Hz contract
rather than a bug — but if it is, the constant deserves a comment saying so.
*(Inference from reading the loop; not verified against a running build.)*

### 4.6 Two things exist with no reader

- `paths.CacheDir()` has no caller anywhere in the tree. Its doc describes
  update downloads for a `dexel update` command that does not exist.
- The `sessions_ended_at` index has no query that uses it — the only read is
  `ORDER BY id ASC`. This one is honestly labelled in its own comment as being
  for a future feature, so it is a note rather than a finding.

### 4.7 Stale comments in the persistence layer

A cluster, all the same shape — the file describes the pre-SQLite world:

- `SaveData`'s doc comment, `ConfigData`'s, and `content_free_test.go`'s all
  say the on-disk path is `~/.config/dexel/state.json`. Both the directory
  (macOS/Windows) and the filename (`state.db`) are now wrong, and the *same
  files* get it right elsewhere.
- `writeFileAtomically`'s comment says it is "Shared by `Save` (state.json) and
  `SaveConfig`". `Save` uses a SQLite transaction and does not call it;
  `SaveConfig` is the only caller.
- `loadJSON`'s corrupt branch discards its rename error (`_ = os.Rename`) while
  the message it returns asserts "(moved to %s)". The `.future` and `.invalid`
  branches both check. Same shape at the journal-sibling rename in
  `quarantine`, where the sibling can silently stay behind.

### 4.8 Stale comments elsewhere

- `hub.go` says `Name` is "SET_NAME's **only** payload"; `SESSION_START` uses
  it too. The same comment block lists the flash kinds and omits `welcome`,
  which the server really sends.
- `assets.ts` describes a server-side `registerAssetsRoute()` /
  `internal/assets.Locate()` mechanism that was replaced by an embedded FS.
  `overlays.ts`'s assets-missing banner still advises "Run from the repo", which
  post-embedding is no longer meaningful — the only way to see that banner is an
  explicit bad `DEXEL_ASSETS_DIR`.
- `autostart.go`'s header calls the launchd implementation "unverified on
  hardware"; `launchd_darwin.go` carries a dated per-claim table saying it is
  partly verified.
- The Tauri shell's comment says `status --json` "EXITS 1" when nothing is
  running. It exits 0 either way, and its own doc comment argues at length for
  that. The Rust code reads the `running` field and is harmlessly tolerant, but
  the comment is wrong.

### 4.9 Two spec/code mismatches in the animation

- `docs/ui-spec.md` §10.1 gives the mouse pose hold as "~1.4 s". The code is
  `MOUSE_HOLD_TICKS = 8` on a 200 ms timer, i.e. **1.6 s**, and the constant's
  own comment says 1.6 s.
- §10.2 lists sleep as suppressing celebration, which `onCelebrate` does
  correctly refuse to start while `onBreak`. But a celebration already *in
  progress* keeps playing if the mood flips to `onBreak` mid-beat, because
  `sceneDevFrame` checks `celebrateFrame` first. A 1.4 s window, almost
  certainly harmless, and arguably the nicer behaviour — but the precedence
  table implies otherwise.

### 4.10 The economy calibration comment predates the bonus it sits above

`engine.go`'s calibration comment states the outcome as "real typing ~21 min per
50-work sprint", and ADR 0005 says the same. The A2 focus bonus was later added
to the same file, in the same function, and changes that figure for a
continuously typing user (see [`economy.md`](economy.md) §7). The numbers were
never restated. ADR 0005 itself should not be edited — that is the ADR rule —
but the comment in `engine.go` can be, and the current figures now live in
[`economy.md`](economy.md).
