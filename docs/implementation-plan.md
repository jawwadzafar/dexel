# dev-companion — Implementation Plan v0.1

Author: game-architect · Status: ready for implementation

## 1. Product scope for this plan

A cozy desktop companion: one developer character, one desk, one project
system. The character visibly codes while the real user works at their
computer; the character's work rate is driven by the user's real activity,
never by content. This document scopes **only the MVP vertical slice**.
Explicitly out of scope here (do not build, do not stub the architecture
around them prematurely): VS Code/editor integration, Git/GitHub events, AI
coding-agent activity, career tree, multiple rooms/town, Steam/achievements.
Those are real future work — the architecture below (§3) is shaped so they
plug in later without a rewrite, but nothing about the MVP schedule depends
on them existing.

## 2. Technology decisions

| Decision | Choice | Why |
|---|---|---|
| Language | Rust, current stable toolchain | matches the stated direction; memory-safe native perf for an always-running background app |
| Engine | Bevy, latest stable release pinned exactly in `Cargo.toml` | ECS fits "many small independent behaviors" (mood, progress, animation) better than a scene-graph engine; do not float a `^`/`*` version — pin the exact patch version you land on and bump deliberately |
| UI | Bevy UI (`bevy_ui`) only | no HTML/CSS/Electron/React — keeps the app a single native binary with no webview overhead |
| Persistence | `serde` + `serde_json` to a file under the OS data dir (`dirs` crate) | human-diffable while the save format is still moving; revisit only if a concrete need (size, speed) appears |
| Packaging | `cargo build --release` per OS target | no installer/auto-update/Steam integration in this plan — that's post-MVP |

Run `cargo add bevy` (or check crates.io directly) at M0 start and record the
exact resolved version in the milestone log — do not hardcode a version
number from outside observation of the actual crates.io state at
implementation time.

## 3. Architecture

```
                 OS / window input
                        │
                        ▼
        ┌───────────────────────────────┐
        │   activity crate (no Bevy)     │
        │                                │
        │  trait ActivityProvider {      │
        │    fn poll(&mut self) -> Vec<ActivityEvent>;
        │  }                             │
        │                                │
        │  enum ActivityEvent {          │
        │    Keystroke,                  │
        │    MouseMoved,                 │
        │    FocusChanged(bool),         │
        │  }                             │
        │                                │
        │  struct FocusedWindowProvider  │  ← v0.1 impl (Bevy input events
        │  (fed by the companion crate's │    forwarded in, NOT read directly
        │   input systems, not by OS     │    by any game system)
        │   hooks — see §3.1)            │
        └───────────────┬────────────────┘
                         │  ActivityEvent (count/kind only — never key identity)
                         ▼
        ┌────────────────────────────────┐
        │   companion crate (Bevy app)    │
        │                                 │
        │  activity_bridge_system         │  Bevy Update system: drains the
        │       │                         │  provider, writes Bevy Events
        │       ▼                         │
        │  ActivityMeter (Resource)        │
        │       │                         │
        │       ▼                         │
        │  mood + progress systems         │  ECS, see §3.2
        │       │                         │
        │       ▼                         │
        │  UI / animation systems          │  Bevy UI, see §3.3
        │       │                         │
        │       ▼                         │
        │  save_system (serde_json)        │
        └────────────────────────────────┘
```

### 3.1 The activity boundary, and why v0.1 fakes it deliberately

`activity` is a plain-Rust lib crate with **no Bevy dependency**. It defines:

```rust
pub enum ActivityEvent { Keystroke, MouseMoved, FocusChanged(bool) }

pub trait ActivityProvider {
    /// Drain whatever happened since the last poll. Never returns key
    /// identity, text, or window titles — counts and focus transitions only.
    fn poll(&mut self) -> Vec<ActivityEvent>;
}
```

For **v0.1**, the only implementation is `FocusedWindowProvider`, which is fed
by the `companion` crate's own Bevy `KeyboardInput`/`MouseMotion` event
readers (i.e. activity *while this window is focused*, not global OS
activity). This is intentional, not a shortcut we forget to fix: it lets M1-M4
prove the entire game loop is fun and correct before anyone touches
platform-specific global-hook code, which is the highest-risk, most
platform-fragile part of the whole project. Swapping in a real OS-global
listener later is a one-crate change: implement `ActivityProvider` again in a
new `activity_global` module (behind a Cargo feature, likely on top of a
crate like `rdev` or platform-specific APIs — evaluate at that milestone, not
now) and change one line in `companion`'s setup that chooses which provider to
construct. No game system changes, because no game system ever sees anything
but `ActivityEvent`.

### 3.2 ECS design

**States** (`bevy::state`):
```rust
#[derive(States, Clone, Eq, PartialEq, Hash, Debug, Default)]
enum AppState { #[default] Loading, Playing }
```

**Resources:**
```rust
struct Wallet(u64);
struct PlayerXp { level: u32, xp: u32 }
struct CurrentProject { name: String, total_work: f32, work_done: f32, reward_coins: u64, reward_xp: u32 }
struct ActivityMeter { recent_rate: f32, idle_timer: Timer }   // recent_rate is a decaying average, not a raw counter
struct SaveTimer(Timer)
```

**Components** (on the one developer entity + its children):
```rust
#[derive(Component)] struct Developer { mood: Mood }
enum Mood { Idle, Coding, OnBreak }
#[derive(Component)] struct MoodLabel;      // Bevy UI Text entity mirroring Developer.mood
#[derive(Component)] struct ProgressBarFill; // Bevy UI Node whose width mirrors CurrentProject
```

**Events:**
```rust
#[derive(Event)] struct ProjectCompleted { coins: u64, xp: u32 }
#[derive(Event)] struct LevelUp { new_level: u32 }
```

**Systems** (schedule noted; `*` = extract to a plain, unit-testable function
called by the system, per the architecture skill's rule):

| System | Schedule | Does |
|---|---|---|
| `load_or_init_save` | `Startup` | reads save file if present, populates resources, else defaults; transitions `AppState → Playing` |
| `activity_bridge_system` | `Update` | drains `ActivityProvider::poll`, updates `ActivityMeter.recent_rate` via `*decay_and_accumulate(rate, events, dt)` |
| `idle_detection_system` | `Update` | ticks `ActivityMeter.idle_timer`; past a threshold (60s, make it a named const) sets `Developer.mood = OnBreak`; fresh activity resets it to `Coding` |
| `project_progress_system` | `FixedUpdate` | while `mood == Coding` and a project exists: `work_done += *progress_delta(recent_rate, fixed_dt)` — `progress_delta` must clamp so a sustained max-rate mash converges to the same throughput as steady real typing within one session (this is the anti-mashing rule from §1, made concrete and testable) |
| `project_completion_system` | `Update` | when `work_done >= total_work`: fires `ProjectCompleted`, rolls the next project (v0.1: pick the next from a small static list, no generation) |
| `xp_level_system` | `Update` | on `ProjectCompleted`: adds coins/xp; `*level_for_xp(xp)` pure function decides level, fires `LevelUp` if it changed |
| `mood_render_system` | `Update` | `Developer.mood` → `MoodLabel` text/color |
| `hud_render_system` | `Update` | resources → `ProgressBarFill` width, coin/xp/level text |
| `save_system` | `Update`, gated by `SaveTimer` + `AppState::Playing` | serializes a `SaveData` struct to the save file |

Functions marked `*` live outside any Bevy system signature (plain `fn`, no
`Res`/`Query` params) specifically so M2/M3's unit tests can call them
directly without constructing an `App`.

### 3.3 UI

Bevy UI only: a root `Node` with the desk area (placeholder colored `Node`
rects are fine for v0.1 — no art requirement to reach a playable slice) and a
HUD bar at the bottom (progress bar, coin count, level, mood label). No
`bevy_ui` widget beyond `Text` and `Node`/`BackgroundColor` is needed for this
plan.

### 3.4 Persistence

```rust
#[derive(Serialize, Deserialize)]
struct SaveData { wallet: u64, xp: u32, level: u32, current_project: Option<CurrentProjectSave> }
```
Path: `dirs::data_dir().unwrap().join("dev-companion").join("save.json")`.
Write on a `SaveTimer` (e.g. every 30s) and is acceptable to also add on exit
later — not required for v0.1's exit criterion, which only requires periodic
autosave + load-on-launch.

## 4. Milestones

Each milestone's exit criterion is something you run and get a pass/fail
answer from. Do not start the next milestone until the current one's exit
criterion is demonstrated, not just "compiles."

### M0 — Workspace scaffold
- `cargo new --lib activity`, `cargo new --bin companion`, workspace
  `Cargo.toml` tying them together.
- `activity`: the trait + enum from §3.1, plus a `MockProvider` used only in
  tests.
- `companion`: `App::new().add_plugins(DefaultPlugins).run()` — an empty
  window, nothing else.
- **Exit:** `cargo run -p companion` opens a blank window; `cargo test
  --workspace` passes (testing the mock provider); `cargo fmt --all --
  --check` and `cargo clippy --workspace --all-targets -- -D warnings` are
  clean.

### M1 — Static scene + HUD skeleton
- Spawn the `Developer` entity + placeholder desk `Node`s.
- Spawn the HUD: progress bar at a hardcoded 0%, coin/xp/level text with
  hardcoded values, mood label showing `Idle`.
- No systems update anything yet — this milestone is purely "does the layout
  exist and render."
- **Exit:** window shows the desk area and a HUD with all four elements
  visible (visually confirmed, then screenshot or description recorded in the
  milestone log).

### M2 — Activity wiring (with a temporary visible counter)
- Implement `activity_bridge_system` and `ActivityMeter` (§3.2).
- Add a **temporary** debug counter to the HUD showing raw activity-event
  count this session, explicitly labeled `[debug]` in the UI.
- Unit-test `decay_and_accumulate` directly (no `App` needed).
- **Exit:** typing/moving the mouse in the focused window visibly moves the
  debug counter within ~1 second; `cargo test --workspace` covers at least
  three cases for `decay_and_accumulate` (steady activity, burst then
  silence, sustained max-rate mashing does not runaway).
- **Before moving to M3:** this milestone's debug counter must be flagged in
  the log as "remove or hide in M5 polish" — it exists to prove the wire-up
  works, not to ship, since a raw count contradicts §1's anti-mashing
  principle if a user ever sees it as the real metric.

### M3 — First playable loop
- Implement `project_progress_system`, `project_completion_system`,
  `xp_level_system`, `idle_detection_system`, `mood_render_system`,
  real `hud_render_system` (replacing M1's hardcoded values).
- Static list of 3-5 hardcoded projects to roll through (name + total_work +
  rewards) — no content generation.
- Unit-test `progress_delta` (the anti-mashing clamp) and `level_for_xp`
  directly.
- **Exit:** launch, type normally for a session, watch the progress bar
  actually fill, see a project complete with a coins/xp award, see the next
  project start, and see the mood label flip to `OnBreak` after the idle
  threshold with no input. `cargo test --workspace` green.

### M4 — Persistence
- `SaveData`, `load_or_init_save`, `save_system` (§3.4).
- **Exit:** get partway through a project, quit, relaunch — wallet, xp,
  level, and in-progress project all restore. Add a round-trip unit test
  (serialize a `SaveData`, deserialize, assert equality) independent of the
  file system.

### M5 — v0.1 polish pass
- Remove or hide the M2 debug counter.
- At least one desk upgrade: a coin threshold unlocks a second placeholder
  prop (e.g. a plant `Node`) — proves the "world visibly changes" hook
  without needing real art.
- One or two idle/mood text lines (e.g. "Maybe we should take a break?" on
  entering `OnBreak`) for personality, no dialogue system needed.
- `cargo build --release -p companion` produces a binary; run it once outside
  `cargo run` to confirm the release build actually launches.
- **Exit:** a fresh clone can go from `cargo build --release -p companion` to
  a running, saving, progressing game with zero manual setup beyond that one
  command.

Explicitly not in this plan (raise as a new architecture pass when reached,
not folded into M5): global OS-level activity (replacing
`FocusedWindowProvider`), VS Code/Git/GitHub signals, AI coding-agent
activity, career/room/town progression, packaging/installer/auto-update,
Steam integration.

## 5. Exact commands

```bash
# scaffold (M0)
cargo new --lib activity
cargo new --bin companion
# then hand-write the workspace Cargo.toml at the repo root

# every milestone, in order
cargo fmt --all -- --check
cargo clippy --workspace --all-targets -- -D warnings
cargo test --workspace
cargo run -p companion

# fast iterative dev loop (dev profile / a `fast-dev` feature only — never release)
cargo run -p companion --features dynamic_linking

# debug
RUST_LOG=debug,wgpu=warn RUST_BACKTRACE=1 cargo run -p companion

# release build (M5)
cargo build --release -p companion
```

## 6. Handoff to game-engineer

Implement milestone-by-milestone in the order above. Do not introduce
architecture beyond §3. After each milestone, run the full command sequence
in §5 and append a dated entry to `docs/milestone-log.md`: files changed,
commands run, their actual output, and anything still broken or deferred.
When something fails, find the root cause per the
`milestone-driven-rust-implementation` skill rather than working around it.
If a milestone can't be completed as scoped, say so explicitly in the log
rather than shipping a partial version under the same milestone name.
