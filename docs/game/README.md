# `docs/game/` — how Dexel actually works, right now

This directory is the **source of truth for the shipped game's mechanics**.
Every number on these pages was read out of the Go source on the branch that
produced them, not copied from another document — the other design docs in
this repo are older than the code in several places, and where they disagree
these pages say so out loud and cite the file.

Nothing here is a plan. Nothing here is a wish. If a page describes a
behaviour, that behaviour is in the code and the page names the file it lives
in. The one exception is [`BACKLOG.md`](BACKLOG.md), which is labelled
`PROPOSED` on every entry and contains nothing that is built.

---

## Why this layer exists at all (and what it must not duplicate)

The repo already had three documentation layers before this one, and each of
them answers a genuinely different question. The failure mode this directory
is designed against is a fourth layer that re-states the other three and then
rots out of sync with all of them.

| Layer | Answers | Immutability | Example |
| --- | --- | --- | --- |
| `docs/adr/` | **Why** we chose this, and what we gave up | Never edited. Superseded by a new ADR | [0005 anti-mashing economy](../adr/0005-anti-mashing-economy.md), [0010 honest mechanics](../adr/0010-mac-first-honest-mechanics.md) |
| `docs/plan/` | **What we are going to build next**, in phases | Rewritten per phase; goes stale by design once shipped | [ROADMAP.md](../plan/ROADMAP.md), [PRODUCT-EVOLUTION.md](../plan/PRODUCT-EVOLUTION.md) |
| `docs/ui-spec.md` | **How the frontend must be built** — DOM ids, pixel geometry, the wire contract, keybindings | Normative spec; the frontend is checked against it | §4 store modal geometry, §6 WebSocket contract |
| **`docs/game/`** (this) | **How the game works today** — the rules and the numbers a player is actually subject to | Updated in the same commit as any behaviour change | "a counted keystroke is worth 0.008 work units" |

Concretely, the boundaries this directory holds:

- **It does not re-argue decisions.** When a rule exists because of an ADR,
  the page states the rule and links the ADR. It never restates the ADR's
  reasoning at length.
- **It does not own pixel geometry, DOM ids, or the wire schema.**
  `docs/ui-spec.md` owns those and is far more precise about them than a
  design page should try to be. [`surfaces.md`](surfaces.md) describes what a
  player sees and can do, and links to `ui-spec.md` for the contract.
- **It does not own the product roadmap.**
  [`docs/plan/PRODUCT-EVOLUTION.md`](../plan/PRODUCT-EVOLUTION.md) owns phases
  P1–P6 and the rejected directions. [`BACKLOG.md`](BACKLOG.md) here is
  narrower on purpose: open questions about *mechanics that already exist*.
- **It does not own bugs.** [`docs/plan/BUGS.md`](../plan/BUGS.md) does.

### The structure, and the practice it comes from

The shape is a **living design document split by system, plus a changelog** —
in game-industry terms, a one-pager (this README) fronting a set of system
pages, with a Keep a Changelog–style log beside it. That combination is what
survives on small teams: the monolithic single-file GDD is the format the
industry moved away from precisely because one giant document is never read
and never updated, while a wiki with no change log gives you a present tense
and no history.

Three properties made it the right pick here rather than the alternatives:

- **A single GDD** would collide head-on with `docs/ui-spec.md`, which is
  already a large normative document. Two overlapping monoliths is worse than
  either.
- **A full wiki** (one page per noun) fragments 5,000 lines of Go across
  thirty stubs and makes "is this still true?" unanswerable.
- **Machinations-style economy diagrams** were considered for the economy
  page and rejected as overkill: Dexel's economy is a single closed-form
  expression per tick with one payout point, so a table of constants and the
  literal formula is both shorter and more checkable than a resource-flow
  diagram. If a second coin source is ever added, revisit this.

The log is [`CHANGELOG.md`](CHANGELOG.md), in
[Keep a Changelog](https://keepachangelog.com/) format, with two deliberate
narrowings: it logs **game behaviour** only (an internal refactor with no
behavioural effect does not belong in it), and every entry names the page it
changed. Versions track the release the change shipped in.

---

## The pages

| Page | What it owns |
| --- | --- |
| [`activity-signal.md`](activity-signal.md) | The signal chain: OS → provider → engine → game → WebSocket → frontend, and the privacy boundary that holds it in place |
| [`economy.md`](economy.md) | Work units, the anti-mash clamp, every economy constant with its real value, the focus-session bonus, app-switch work, and what caps exist (fewer than you would think) |
| [`progression.md`](progression.md) | Sprints, what a "unit" mechanically is, Dev Cash, the XP/level curve, the store, own-many-equip-one, tints |
| [`moods.md`](moods.md) | The three moods, the honesty rules that gate them, and the app-type-tiered activity line |
| [`sessions.md`](sessions.md) | Sessions as a lens, the daily/lifetime counters, the 30-day history, and streaks |
| [`persistence.md`](persistence.md) | `state.db`, the save MAC, quarantine-never-delete, the schema, and the config/state split |
| [`surfaces.md`](surfaces.md) | The window and HUD, the modals, pause, and the run modes — **one subsection is in flight, see its banner** |
| [`BACKLOG.md`](BACKLOG.md) | **PROPOSED, nothing built.** Open mechanical questions, including per-app-type animation, the naming problem, and the focus-session thresholds |
| [`CHANGELOG.md`](CHANGELOG.md) | The log |

---

## Dexel in one page

**Dexel is a pixel-art developer companion whose in-game workday is driven by
your real one.** A tiny figure sits at a desk in a small frameless window
(kept above your other windows only if you ask it to — Settings, default off).
When you type, it types. When you stop, it stops. It works through a rotating
list of fictional sprints, gets paid in Dev Cash, and you spend that on
cosmetics for its desk and its hoodie.

The whole product rests on one rule, stated in
[ADR 0010](../adr/0010-mac-first-honest-mechanics.md) and enforced everywhere:
**Dexel never claims something it cannot observe.** It does not know what you
typed, what file you had open, or what you were working on — and where it
cannot know, it says nothing rather than guessing. "On break because you
minimised me" is the exact lie the honesty rules exist to make impossible.

### The loop, at a glance

```
   your keyboard / mouse
            │  counts and durations only — never content (ADR 0002/0009)
            ▼
   activity.Provider            coalesces to at most 1 counted keystroke
   (CoreGraphics HID timers)    and 1 mouse-active flag per 100 ms
            │
            ▼  Snapshot{KeystrokeCount, MouseActive, IdleSeconds, ActiveApp, …}
   engine.Engine.Tick()         once per second
            │                   → work units  (the economy, ADR 0005)
            │                   → a mood      (the honesty rules, ADR 0010)
            ▼
   game.Game.Tick()             work → sprint progress → Dev Cash + XP
            │                   and a parallel, passive analytics tally
            ▼  StateMessage over WebSocket, once per second
   the frontend                 renders the scene, the HUD, and the modals
```

Everything the player sees is computed on the server and sent whole. The
client never re-derives a number the server could have sent — which is why a
streak, a level, and an activity line are all server-computed strings and
integers on the wire.

### The numbers that matter most

Full derivations, and the exact source location of every constant, are on
[`economy.md`](economy.md) and [`progression.md`](progression.md).

| | Value | Where |
| --- | --- | --- |
| Tick rate | 1 s | `app/main.go` `tickInterval` |
| One counted keystroke | 0.008 work units | `engine.KeystrokeWeight` × `engine.WorkPerUnitRate` |
| One mouse-active second | 0.020 work units | `engine.MouseSustainedRate` × `engine.MouseWeight` × `engine.WorkPerUnitRate` |
| Anti-mash coalescing | ≤ 1 keystroke / 100 ms | `activity.MouseSampleInterval` |
| First sprint | 50 units → 25 Dev Cash + 40 XP | `game/sprint.go` |
| A full sprint rotation | 530 units → 305 Dev Cash + 460 XP | `game/sprint.go` (6 sprints) |
| The whole store | 6,370 Dev Cash | `game/catalog.go` (32 items + 40 tint purchases) |
| Level *n* reached at | 50·(n−1)·n XP | `game/sprint.go` `thresholdForLevel` |

---

## How to keep this true

This directory is only worth having if it cannot silently rot. One rule:

> **A change to game behaviour updates the relevant page and adds a
> `CHANGELOG.md` entry in the same commit as the code.**

"Game behaviour" means anything a player could notice or a number they are
subject to: an economy constant, a threshold, a sprint's target or payout, a
catalog price, a mood rule, what a modal does, what is persisted, what a
counter counts. It does **not** mean an internal refactor, a test, or a
comment — those change the code without changing the game.

In practice:

1. **Find the page that owns the behaviour** (the table above). If none does,
   the change probably belongs in a new page — say so in the commit.
2. **Edit the page, not just the log.** A changelog entry pointing at a page
   that still states the old number is worse than no entry, because it looks
   maintained.
3. **Add the log entry under `## [Unreleased]`,** in the Keep a Changelog
   category that fits (`Changed`, `Added`, `Removed`, `Fixed`), naming the
   page you touched.
4. **Quote the source, not the doc.** Every number on these pages carries the
   constant name and the file it lives in, so the next reader can re-verify
   in one grep instead of trusting the prose. If you add a number without its
   source, you have added something nobody can check.
5. **If you cannot verify it, write "unverified".** That word appears in
   several places in these pages on purpose. A labelled gap is useful; a
   confident guess is a liability, and this project's whole pitch is that it
   does not do that.

### Where a page disagrees with an older doc

Older design docs are not wrong to have existed — they were true when they
were written. When one of them disagrees with the code, the resolution is:

- **these pages follow the code**, and
- the page carries a short note saying which document says otherwise and
  what it says, so the next person to read the older doc is not misled by it.

Every such note found while writing this directory is collected in
[`CHANGELOG.md`](CHANGELOG.md) under the initial entry.
