# Game changelog

All notable changes to **how the game behaves** are recorded here. The format
is [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Two deliberate narrowings from the standard format:

- **This logs game behaviour, not commits.** An internal refactor, a new test,
  or a comment fix does not belong here. An economy constant, a threshold, a
  sprint target, a catalog price, a mood rule, what a modal does, what is
  persisted, or what a counter counts — all of those do.
- **Every entry names the page it changed.** If a change did not require a page
  edit, it probably was not a behaviour change; if it was and the page was not
  edited, the entry is not finished. See
  [`README.md`](README.md#how-to-keep-this-true).

Versions track the release the change shipped in. Dexel's version is injected
at build time (`-X main.version=…`), defaulting to `dev`, and this repo has no
tags at all — every tag and GitHub release was deleted on 2026-08-23 because
v1.0.0/v1.1.0 had been cut far too early. **The working version is 0.1.0**
(`desktop/src-tauri/tauri.conf.json`, `desktop/src-tauri/Cargo.toml`) and
nothing is shipped yet, so `[Unreleased]` below is anchored to the commits it
describes rather than to a version number a build could not report.

---

## [Unreleased]

### Added

- **`docs/game/` — a living design document for the shipped game.** Nine pages
  (index, activity signal, economy, progression, moods, sessions, persistence,
  surfaces, backlog) plus this log, derived from the Go and TypeScript source
  on `feat/dexel-0.3.0`, not from the existing design docs. Written against
  `18ef1db` and updated to `12accf0` when the app-type work landed mid-write.
  The directory's purpose, its boundaries against `docs/adr/`, `docs/plan/` and
  `docs/ui-spec.md`, and the rule that keeps it from rotting are all in
  [`README.md`](README.md).

  This is documentation only. **No game behaviour changed.**

### Changed

- **App identity works at all.** Previously `Snapshot.ActiveApp` froze at the
  first sample for the life of the process, in EVERY mode: `NSWorkspace`'s
  running-app cache refreshes from notifications delivered on the main run
  loop, and a Go server never runs one. Foreground runs froze on whatever app
  launched them (so "In the terminal" looked correct by coincidence); the
  login-time daemon froze on nil and reported no app forever, which is why the
  app-switch counter read 0 all day. Now read fresh per call from
  `CGWindowListCopyWindowInfo` (owner name only, never the window title), with
  a new `AppIdentityAvailable` bit so "cannot see apps from here" is
  distinguishable from "nothing is frontmost". Pages: `activity-signal.md`,
  `moods.md`. ADR 0019.
- **The window is a view, not the owner of the game.** The desktop shell used
  to spawn its own private server and SIGTERM it on window close — so closing
  the window silently stopped all activity capture, and that server was
  invisible to `dexel status`. It now attaches to the detached runtime
  (starting one only if none exists) and terminates nothing. Page:
  `surfaces.md`.
- **The character no longer flickers.** The scene subtree was rebuilt on every
  render, and a fresh `<img>` paints nothing until its bitmap decodes — worse,
  an undecoded CSS `mask-image` masks its fill away entirely, so the character
  flashed pure WHITE. Over one 60s cycle the old build produced 17 distinct
  character images where only 8 poses exist. The scene is now built once and
  mutated, with every sprite pre-decoded. Page: `surfaces.md`.
- **The window scales proportionally instead of sitting 1:1 in dead space.**
  The whole fixed-pixel layout scales as one unit, aspect preserved, centred,
  letterboxed in the scene's own colour, preferring a crisp scale factor.
  Page: `surfaces.md`.
- **Dexel never claims you were typing in Dexel itself,** and its own window is
  transparent to app-switch counting — that counter was measuring how often
  you glanced at your companion. Page: `moods.md`.
- **The activity line no longer offers a work verb for non-coding apps**
  (commit `12accf0`, landed while this directory was being written and folded
  into it). `game.ActivityLine`'s two substring predicates
  (`isTerminalApp`/`isBrowserApp`) are replaced by `activity.AppTypeOf`, an
  explicit 88-entry table over ten classes, and phrasings are drawn from
  per-type pools tiered by what the signals license. Only `coding` and
  `terminal` have a non-empty `work` tier, which makes **"Coding in Brave"
  unrepresentable rather than merely unreachable**. Phrasing varies but is a
  deterministic hash of `(seed, app id, floor(now / 45 s))`, so it cannot
  flicker at the 1 Hz broadcast rate.
  Documented in [`moods.md`](moods.md) §3; `activity-signal.md` §2 updated for
  the table's new size and the `AppTypeSelf` short-circuit.

### Fixed

- **A v1.0.0 save is no longer destroyed by upgrading.** The JSON side of the
  save MAC verifies against a re-serialization of the parsed struct, so a field
  added later without `omitempty` changed the preimage and every existing
  `state.json` failed its own MAC — the player was silently reset to a fresh
  economy with their save quarantined. Page: `persistence.md`.
- **The runtime no longer erases `config.json`'s `autostart` field** on every
  SET_NAME and SESSION_START. Page: `persistence.md`.

### Notes — where the code disagreed with an existing doc

Recorded here rather than silently corrected, so the next reader of the older
document is not misled by it. Each was verified against the source.

- **`docs/ui-spec.md` §10.1** gives the mouse pose hold as "~1.4 s". The code
  holds it for 8 ticks on a 200 ms timer — **1.6 s** — and the constant's own
  comment says so. *([`surfaces.md`](surfaces.md) §2)*
- **`docs/ui-spec.md` §3.2** specifies a terminal line pushed "every 0.35s",
  and the constant cites that section. The 350 ms branch mutates the buffer but
  does not broadcast, and the client only receives `screenLines` on the 1 s
  state frame — so about three lines advance at once, once a second.
  *([`surfaces.md`](surfaces.md) §6, [`BACKLOG.md`](BACKLOG.md) §4.5)*
- **`app/internal/game/catalog.go`** states twice that `buddy_bot` is a
  two-frame blink and that "the frontend derives frame B by the same `_a`/`_b`
  convention". It does not — `renderSlotSprite` sets frame A and nothing else,
  and `buddy_bot_b.png` is unreferenced. *([`BACKLOG.md`](BACKLOG.md) §4.2)*
- **`app/internal/store/config.go`** states the `autostart` field "is written
  ONLY by `dexel autostart enable`/`disable` — nothing else in this codebase
  may set it". `persistConfig` in `main.go` rewrites `config.json` from a fresh
  literal on every `SET_NAME` and `SESSION_START`, resetting the field.
  *([`persistence.md`](persistence.md) §6, [`BACKLOG.md`](BACKLOG.md) §4.3)*
- **`ADR 0005` and the calibration comment at the top of `engine.go`** state
  "real typing ~21 min per 50-work sprint". That figure predates the A2
  focus-session bonus, which is folded into `WorkUnits` in the same function.
  The mouse-only companion figure (~42 min) still reproduces exactly. The ADR
  is not to be edited — that is the ADR rule — but the current figures now live
  in [`economy.md`](economy.md) §7.
- **`app/internal/game/game.go`'s `StatsView` doc comment** says "Deliberately
  just these two buckets — a rolling multi-day history is Phase A3's job, not
  this one", on a struct that has carried `History` and `Streak` since A3
  landed. The `statsDateFormat` comment nearby likewise still says "Phase A1
  keeps no multi-day history". *(Noted on [`sessions.md`](sessions.md) §3 by
  documenting the behaviour that actually exists.)*
- **A cluster of persistence comments** still describe the pre-SQLite world:
  `SaveData`'s, `ConfigData`'s and `content_free_test.go`'s doc comments all
  give the on-disk path as `~/.config/dexel/state.json`, and
  `writeFileAtomically`'s says it is shared with `Save`, which no longer calls
  it. *([`BACKLOG.md`](BACKLOG.md) §4.7)*
- **`docs/upgrade-design.md`** describes a JSON save shape with
  `sprint.unitsDone`. The persisted field is still named `unitsDone`, but the
  *wire* field is `sprint.progress`, and persistence is SQLite `state.db` rather
  than `state.json`. *([`persistence.md`](persistence.md) §2, §5)*
- **`docs/upgrade-design.md`'s pricing table** lists the cheapest purchase
  (`bev_thermos`, 40) as reachable "inside the first sprint". A fresh game
  starts with 0 Dev Cash and the first sprint pays 25, so it is affordable
  after the second payout. *([`progression.md`](progression.md) §4)*
- **`docs/plan/ROADMAP.md`'s Phase A1 bullet** lists "active minutes, idle
  minutes"; the counters are `activeSeconds` / `idleSeconds`. Its A2 bullet
  lists a copy/paste chord count, which the Status line beneath it correctly
  records as deferred behind a permission fork (ADR 0012). *(No page change
  needed; noted for readers of that bullet.)*

Things that read like disagreements and are **not**, verified:

- `MaxRecentRate = 15.0` sitting above the maximum achievable weighted rate of
  12.5 is **deliberate** — it is a backstop against a fabricated signal, and
  `TestMaxRecentRateIsABackstopNotASilentCap` asserts exactly that inequality.
  *([`economy.md`](economy.md) §3)*
- `tauri.conf.json`'s `"version": "0.1.0"` against a `0.3.0` branch name is
  intentional; commit `f3df435`'s own subject records the reset.
- The `sessions_ended_at` index having no reader is labelled as such in its own
  comment, for a future feature.

### Observed but not landed

At the time of writing, the working tree carried an **uncommitted** rename of
the Tauri sidecar (`Dexel Runtime` → `Dexel`) and of the desktop shell's main
binary (`dexel` → `dexel-desktop`). [`surfaces.md`](surfaces.md) §8 documents
the committed names and carries a banner about the change. When it lands, that
subsection and this log both need updating — the macOS Login Items pane names a
background item after the executable actually exec'd, so this rename changes a
user-visible string.

---

## Template for the next entry

```markdown
## [x.y.z] — YYYY-MM-DD

### Changed
- **<what a player would notice>.** <the constant or rule, with its old and new
  value.> Updated `<page>.md` §<n>.

### Added
### Removed
### Fixed
```
