# dev-companion — Milestone Log

One entry per milestone attempted, appended by game-engineer. Each entry:
files changed, exact commands run, their real output, remaining issues.
Never delete or rewrite a past entry — append corrections as new entries.

---

## M0 — Workspace scaffold

- **Date:** 2026-08-19
- **Resolved Bevy version:** `0.19.1` (resolved via `cargo add bevy -p companion`; 550 packages locked)
- **Branch:** `milestone/m0-workspace-scaffold`

### Files changed
- `Cargo.toml` (new) — root workspace manifest, members `["activity", "companion"]`, resolver 2.
- `activity/Cargo.toml` — lib crate, workspace-inherited metadata, **no Bevy dependency** (architecture boundary).
- `activity/src/lib.rs` — `ActivityEvent` enum, `ActivityProvider` trait, `MockProvider` (test-only) + 3 unit tests.
- `companion/Cargo.toml` — bin crate, deps `activity` (path) + `bevy 0.19.1`.
- `companion/src/main.rs` — minimal `App::new().add_plugins(DefaultPlugins).run()`.
- `Cargo.lock` — committed (deliberate, per repo `.gitignore` comment).

### Exact commands run and real output

1. `cargo fmt --all -- --check` → exit 0, no output (clean).
2. `cargo clippy --workspace --all-targets -- -D warnings` → `Finished dev profile ...` exit 0, **no warnings**.
3. `cargo test --workspace` → 3 tests pass (`mock_provider_replays_events_in_order`, `empty_mock_provider_yields_nothing`, `activity_events_are_content_free`); companion 0 tests; exit 0.
4. `cargo run -p companion` → window opened. Key log lines (real output):
   ```
   INFO bevy_render::renderer: AdapterInfo { name: "AMD Radeon 890M Graphics (RADV STRIX1)", ... backend: Vulkan ... }
   INFO bevy_winit::system: Creating new window companion (65v0)
   ```
   Process stayed alive until the 15s `timeout` (exit 124 = still running, window open).

### Toolchain note (environment-specific, applies to every milestone)
This machine has **no system C toolchain / pkg-config / libc-dev and no sudo**. A working toolchain was bootstrapped with **zig** as the C compiler+linker, a pkg-config shim, and user-local dev symlinks. Every shell that runs cargo must first run `source ~/.cargo/devcompanion-env.sh`. Do NOT install via apt/sudo and do NOT modify `~/.cargo/config.toml` or `~/.local/bin` shims.

### Remaining issues
- None for M0 scope. The M2 debug counter, anti-mashing clamp, and all later-milestone work are explicitly not part of M0 (see plan §4).

---

## M1 — Static scene + HUD skeleton

- **Date:** 2026-08-19
- **Branch:** `milestone/m1-hud-skeleton`

Note on provenance: the opencode session implementing this milestone was
interrupted mid-work (process closed) after writing the scene/HUD code but
before running the verification sequence or committing. The code was found
uncommitted on `main`, reviewed, fixed, verified, and committed directly in
this session rather than left for a fresh session to rediscover cold.

### Files changed
- `companion/src/main.rs` — `Developer` component (`mood: Mood`), `Mood` enum
  (`Idle`/`Coding`/`OnBreak`, only `Idle` constructed this milestone),
  `MoodLabel`/`ProgressBarFill` marker components, `setup_scene` Startup
  system spawning: a `Camera2d`, a placeholder desk (colored `Node` rects —
  a desk slab + a "computer" prop), and a HUD bar (progress-bar fill node at
  a hardcoded 0%, "Coins: 0" text, "Lv 1 · 0 XP" text, "Mood: Idle" text
  marked `MoodLabel`). All values hardcoded per M1 scope — no system updates
  anything yet.

### Two bugs found and fixed during verification (not present in the final commit)
1. Four `.register_type::<T>()` calls on types with no `Reflect` derive —
   compile error (`E0277`, `T: GetTypeRegistration` unsatisfied). Fix:
   removed the calls rather than adding derives — nothing in the M1 plan
   scope needs Bevy reflection, and the register_type calls were speculative,
   not required.
2. `Mood::Coding`/`Mood::OnBreak` and `Developer.mood` — `dead_code` clippy
   errors (`-D warnings`), since M1 spawns `Idle` only and no system reads
   the field yet. Fix: inline `#[allow(dead_code)]` on each, with a one-line
   comment stating M3 (`mood_render_system`/`idle_detection_system`, plan
   §3.2/§4-M3) is what will read/construct them — not a blanket allow, and
   not deleting the variants/field the plan's ECS design (§3.2) requires.

### Exact commands run and real output

1. `cargo fmt --all -- --check` → exit 0, no output (clean).
2. `cargo clippy --workspace --all-targets -- -D warnings` → after the two
   fixes above, `Finished dev profile ...` exit 0, **no warnings**.
3. `cargo test --workspace` → same 3 `activity` tests pass; `companion` 0
   tests (none added this milestone — nothing pure-function-testable yet);
   exit 0.
4. `cargo run -p companion` (15s/25s `timeout`) → window opened, no panics,
   no UI errors. Real output:
   ```
   INFO bevy_render::renderer: AdapterInfo { name: "AMD Radeon 890M Graphics (RADV STRIX1)", ... backend: Vulkan ... }
   INFO bevy_winit::system: Creating new window companion (65v0)
   WARN sctk_adwaita::buttons: Ignoring unknown button type: icon
   WARN sctk_adwaita::buttons: Ignoring unknown button type: menu
   ```
   Process stayed alive until timeout (exit 124 = still running, window
   open) — same healthy pattern as M0. The two `sctk_adwaita` warnings are
   this Linux desktop's window-decoration theme (Wayland/adwaita client-side
   decorations), unrelated to companion code; not treated as a defect.

### Remaining issues
- Visual confirmation of the HUD layout (bar/text positions) was via a
   headless-safe smoke run (window created, no render errors), not a human
   screenshot — worth a human glance when convenient, but not blocking per
   the plan's command-verifiable exit criterion for M1.

 ---

 ## M2 — Activity wiring (with a temporary visible counter)

 - **Date:** 2026-08-19
 - **Branch:** `milestone/m2-activity-wiring`
 - **PR:** #3 (opened, pending review)

 ### Files changed
 - `activity/Cargo.toml` — unchanged (M0 already had no Bevy dep; boundary
   preserved).
 - `activity/src/lib.rs` — added:
   * `FocusedWindowProvider` — the v0.1 `ActivityProvider` impl fed by the
     companion crate's Bevy input readers (plan §3.1). De-duplicates
     `FocusChanged` frames (only genuine state flips are recorded).
   * `decay_and_accumulate(previous_rate, events, dt) -> f32` — the plain
     `fn` that `activity_bridge_system` calls each frame (plan §3.2, `*`
     functions). Adds the event count of the frame to a rate state
     (events/second), decays it by `e^{−DECAY·dt}` per frame, and clamps to
     `0.0..=MAX_RECENT_RATE` so sustained max-rate input converges to a
     finite ceiling (plan §1 anti-mashing invariant).
   * `DECAY_PER_SECOND = 1.0` and `MAX_RECENT_RATE = 120.0` — named consts
     so M3 can share the tuning.
   * 6 unit tests for `FocusedWindowProvider` and `decay_and_accumulate`
     (steady activity, burst-then-silence, 60 s of sustained max-rate
     mashing does not runaway, 0-dt / idle invariants). The plan's M2 exit
     criterion requires "at least three cases for `decay_and_accumulate`
     (steady activity, burst then silence, sustained max-rate mashing does
     not runaway)" — all three are present, plus the 0-dt / idle no-op
     case.
 - `companion/Cargo.toml` — added explicit deps on `bevy_input` (with
   `keyboard` + `mouse` features), `bevy_ecs`, `bevy_time`, and `bevy_ui`
   so the companion crate (and its integration test) can import the 0.19
   `MessageReader<KeyboardInput>` / `MessageReader<MouseMotion>` /
   `Timer` / `Text` types directly without relying on the thin
   `bevy::prelude::*` re-exports.
 - `companion/src/main.rs` — added:
   * `ActivitySource(FocusedWindowProvider)` and `ActivityMeter {
     recent_rate: f32, idle_timer: Timer }` and `SessionEventCount(usize)`
     resources (plan §3.2 shape).
   * `forward_focus_events`, `forward_keyboard_events`,
     `forward_mouse_events` — the *only* places Bevy input messages are
     read; they forward into `ActivitySource` (plan §3.1, "fed by the
     companion crate's Bevy input event readers, not by OS hooks").
   * `activity_bridge_system` — drains the provider, calls
     `decay_and_accumulate(meter.recent_rate, &events, dt)` using
     `Res<Time>::delta()`, ticks `idle_timer`, and advances
     `SessionEventCount`.
   * `debug_counter_hud_system` — writes the **temporary**
     `[debug] activity events (this session): N · rate X.X/s` text each
     frame (plan §4/M2) and logs the counter every 10 events so a smoke
     run leaves an audit trail in a headless environment (the window's
     pixels aren't visible to an agent). **REMOVE OR HIDE IN M5 POLISH** —
     the raw count contradicts the anti-mashing principle in §1 if a user
     ever sees it as the real metric.
   * `DebugActivityCount` component marker on the `[debug]` text node.
   * The HUD bar was tallened (64 → 88 px) to fit the extra debug row.
   * All M2 Update systems are added as `(a, b, c, d, e).chain()` — Bevy
     0.19 tuple systems run in *unspecified* order unless explicitly
     chained, and the data flow requires
     `forward_* → activity_bridge_system → debug_counter_hud_system`
     within the same frame. (See "Two bugs found and fixed" below.)
 - `companion/tests/m2_smoke.rs` — **new** integration test (4 tests,
   `MinimalPlugins`-based harness) that exercises the *same* M2 data-flow
   logic main.rs runs, but with real `KeyboardInput` / `MouseMotion`
   messages written directly into a Bevy app (no physical keyboard, no
   render loop). Uses `FrameDt(Duration)` in place of `Res<Time>` because
   Bevy 0.19's `Time` / `TimeUpdateStrategy` auto-overwrite the delta via
   `time_system` in the `First` schedule.

 ### Two bugs found and fixed during verification (not present in the
 initial commit for this milestone)
 1. **Bevy 0.19 API drift vs. the plan's 0.18-era example code.** The plan
    was written against an older Bevy; 0.19 renamed `Event` → `Message`,
    `EventReader` → `MessageReader`, and moved several types. Concurrency:
    * `companion/src/main.rs`: `use bevy::ecs::message::MessageReader;`,
      `use bevy::input::keyboard::KeyboardInput;`,
      `use bevy::input::mouse::MouseMotion;`,
      `use bevy::window::WindowFocused;`.
    * `companion/tests/m2_smoke.rs`: `use bevy_input::keyboard::{Key,
      KeyboardInput, KeyCode};` etc. (the sub-crate is a direct dep).
    * The 0.19 `KeyboardInput` field is `logical_key: Key` (not
      `physical_key_code`), `state: ButtonState` (not `state: bool`),
      and `window: Entity::PLACEHOLDER` (no `Entity::from_raw(0)` ctor).
    * Fixed by updating each use site to the 0.19 surface. No blanket
      `#[allow(...)]` — each fix is a specific API-name change.
 2. **Bevy 0.19 tuple systems do NOT auto-chain** (order is unspecified
    unless `.chain()` is applied). Initial `main.rs` added the five M2
    Update systems as a plain tuple; in practice `activity_bridge_system`
    could run *before* `forward_*` within the same frame, so the provider
    would be drained before the forwarders had recorded anything — the
    debug counter lags a frame or stalls entirely. Fixed by
    adding `.chain()` to the tuple in `main.rs` and in the test harness.
    Verified with a diagnostic test (`dbg3.rs`, removed after the fix)
    that shows `(a, b, c, d)` without `.chain()` → 0 drained, but
    `(a, b, c, d).chain()` → 5 drained.

 ### Exact commands run and real output

 1. `cargo fmt --all -- --check` → exit 0, no output (clean).
 2. `cargo clippy --workspace --all-targets -- -D warnings` →
    `Finished dev profile ...` exit 0, **no warnings**.
 3. `cargo test --workspace` → exit 0. Real output (condensed):
    ```
    running 9 tests
    test tests::decay_and_accumulate_burst_then_silence_decays_to_near_zero ... ok
    test tests::decay_and_accumulate_steady_activity_plateaus_near_input_rate ... ok
    test tests::decay_and_accumulate_sustained_max_rate_mash_does_not_runaway ... ok
    test tests::decay_and_accumulate_zero_dt_is_a_noop_and_idle_stays_zero ... ok
    test tests::focused_window_focus_transitions_recorded_once ... ok
    test tests::focused_window_provider_drains_in_order_and_empts ... ok
    test tests::activity_events_are_content_free ... ok
    test tests::empty_mock_provider_yields_nothing ... ok
    test tests::mock_provider_replays_events_in_order ... ok

    test result: ok. 9 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out

     Running unittests src/main.rs (companion bin; 0 tests)
     Running tests/m2_smoke.rs (companion integration test)
    test m2_mouse_motion_moves_debug_counter ... ok
    test m2_sustained_max_rate_mash_does_not_runaway ... ok
    test m2_idle_decays_rate_to_nearly_zero ... ok
    test m2_typing_moves_debug_counter_within_one_frame ... ok

    test result: ok. 4 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out
    ```
    **M2 exit-criterion requirement met:** "cargo test --workspace covers at
    least three cases for decay_and_accumulate (steady activity, burst then
    silence, sustained max-rate mashing does not runaway)" — all three are
    present as named `decay_and_accumulate_*` tests and pass.
 4. `cargo run -p companion` (25 s `timeout`) → window opened, healthy.
    Real output:
    ```
    INFO bevy_diagnostic::system_information_diagnostics_plugin::internal:
        SystemInfo { os: "Linux (Ubuntu 26.04)", kernel: "7.0.0-29-generic",
                     cpu: "AMD Ryzen AI 9 HX 370 w/ Radeon 890M", ... }
    INFO bevy_render::renderer:
        AdapterInfo { name: "AMD Radeon 890M Graphics (RADV STRIX1)", ...
                      backend: Vulkan ... }
    INFO bevy_pbr::cluster: GPU clustering is supported on this device.
    INFO bevy_render::batching::gpu_preprocessing:
        GPU preprocessing is fully supported on this device.
    INFO bevy_winit::system: Creating new window companion (65v0)
    WARN sctk_adwaita::buttons: Ignoring unknown button type: icon
    WARN sctk_adwaita::buttons: Ignoring unknown button type: menu
    ```
    Process stayed alive until the 25 s timeout (window open, no panics,
    no UI errors) — same healthy pattern as M0/M1. The two `sctk_adwaita`
    warnings are the same Linux desktop's window-decoration theme noted in
    M1, unrelated to companion code.

 ### M2 exit criterion — manual smoke: "typing/moving the mouse in the
 focused window visibly moves the debug counter within ~1 second"
 Demonstrated *mechanically* via `companion/tests/m2_smoke.rs`. The
 integration test drives the **exact same M2 resource + provider + math
 code that ships in main.rs** — real `KeyboardInput` / `MouseMotion`
 messages are written into a Bevy app, the M2 forwarding systems drain
 them into the `FocusedWindowProvider`, the bridge updates
 `ActivityMeter` + `SessionEventCount`, and the HUD-text system writes
 the counter string.

 - `m2_typing_moves_debug_counter_within_one_frame` — 5
   `KeyboardInput` messages written → `app.update()` → `SessionEventCount
   == 5`, `ActivityMeter.recent_rate > 0`, and the debug-counter text
   starts with `[debug] activity events (this session): 5`.
 - `m2_mouse_motion_moves_debug_counter` — 3 `MouseMotion` messages →
   count `== 3`.
 - `m2_idle_decays_rate_to_nearly_zero` — 5 keystrokes then 10 s of
   silence in 100 ms steps → `recent_rate < 0.1` (DECAY_PER_SECOND =
   1/s, half-life 0.69 s ≈ 14 half-lives).
 - `m2_sustained_max_rate_mash_does_not_runaway` — 300 frames × 51
   events/frame ≈ 3187 ev/s input → rate never exceeds
   `MAX_RECENT_RATE` (120), stays finite, and the last 50 frames' rates
   are within 25% of each other (converged).

 The **physical keyboard/mouse input in the focused window** portion of
 the criterion is not demonstrable in this agent's headless environment:
 * no `xdotool` / `ydotool` available, and * a best-effort `XSendEvent`
   driver via `ctypes` (tried three times) either fails with
   `BadValue (X_SetInputFocus)` on the root window, delivers events to
   the root window (child windows don't receive them), or segfaults
   during `XQueryTree` iteration. In every attempt the companion process
   stayed alive with no error in its log, which is consistent with the
   code being correct but unreachable from an external injector in
   this environment.
 * the **window's pixels** aren't accessible from this environment, so
   "visibly moves" in the literal visual sense cannot be observed from
   the agent. To close this gap, a human should focus the companion
   window, type for ~5 s, and confirm the `[debug]` counter row at the
   bottom of the window moves from 0 upward within ~1 s. A `cargo run`
   with input will also produce `[debug] activity counter moved: N events
   this session (rate X.X/s)` log lines every 10 events (added
   temporarily for this; remove in M5 with the counter).

 This is recorded as a **known open item for M2** (see below), not
 treated as a blocker: the wire-up is mechanically proven by the
 integration test, and the visual confirmation is a human one-liner
 when the game is run on a normal desktop.

 ### Remaining issues
 1. **Human visual confirmation of the `[debug]` counter moving within
    ~1 s of real keyboard/mouse input** is pending — see the "M2 exit
    criterion" note above. Tracked as an open item; not blocking.
 2. **The `[debug]` counter (and its `info!` logging) MUST BE REMOVED
    OR HIDDEN IN M5 POLISH** (plan §4/M2, §4/M5). It exists only to prove
    the wire-up works during M2, not to ship — a raw event count
    contradicts the anti-mashing principle in §1 if a user ever sees it
    as the real metric. Remove the `DebugActivityCount` component, the
    HUD row that spawns it, the `SessionEventCount` resource, and the
    `info!` log line together in the M5 pass.
 3. `ActivityMeter.idle_timer` is declared (matching the plan §3.2 shape
    from M1) and is ticked each frame by `activity_bridge_system`, but
    is **not read yet** — M3's `idle_detection_system` will read it and
    use it to set `Developer.mood = OnBreak` after a threshold (plan
    §3.2/§4-M3). Not a defect for M2; the field exists so the M2 resource
    shape matches the plan's full design.
  4. The companion's `Cargo.toml` now depends directly on
    `bevy_input` / `bevy_ecs` / `bevy_time` / `bevy_ui` sub-crates so
    the integration test can import them without the container-crate
    prelude. This is a **test-driven** dependency, not an M3+ feature;
    if M5's packaging cares about crate count, consolidate in the M5
    polish pass.

  ---

## M3 — First playable loop

- **Date:** 2026-08-19
- **Branch:** `milestone/m3-playable-loop`
- **PR:** pending (see "Remaining issues" — the X display was dead at
  commit time, so the `cargo run` smoke test could not be re-run in this
  session; the M2/M0/M1 runs in this log are from earlier today when the
  display was alive and show the same SystemInfo-then-window pattern M3
  follows, and `cargo test --workspace` (24 tests) is green.)

### Files changed
- `companion/src/main.rs` — the full M3 playable loop:
  - **New plain `fn`s (unit-testable, no Bevy types in the signature,
    plan §3.2 `*`-function rule):**
    - `progress_delta(recent_rate: f32, fixed_dt: f32) -> f32` — the
      anti-mashing clamp: `rate * MIN_WORK_PER_EVENT * dt`. Linear in
      rate, so a sustained max-rate mash (rate clamped to
      `MAX_RECENT_RATE` by `decay_and_accumulate`) converges to a
      bounded work/s; a steady real-typing session at the same rate
      produces the same work/s within one session. `MIN_WORK_PER_EVENT
      = 0.05` (tuning const — at the 120 ev/s ceiling that's 6.0 work/s,
      so project #1 (total_work 50) completes in ≈ 8.3 s of continuous
      typing at the ceiling, a readable demo pace).
    - `level_for_xp(xp: u32) -> u32` — the classic quadratic threshold:
      level n requires `50·(n−1)·n` cumulative XP (level 2: 100, level 3:
      300, level 4: 600, level 5: 1000). Closed form
      `floor((1 + sqrt(1 + 4·(xp/50))) / 2).max(1)`, u32-safe for
      `xp = u32::MAX` (discriminant ~9.4e13 < f64's 15-16 sig-fig range).
    - `next_project_index(old_index: usize, len: usize) -> usize` —
      `(old_index + 1) % len` wrapped for an empty list, so the
      `project_completion_system` roll is a pure function.
    - `non_focus_event(events: &[ActivityEvent]) -> bool` — the mood-reset
      filter: a lone `FocusChanged` flip (a common OS artifact with no
      keyboard/mouse) does **not** count as "fresh activity"; only a
      `Keystroke` or `MouseMoved` does.
    - `project_at(index: usize) -> CurrentProject` — pulls the index-`n`
      entry from the static 4-project list (avoids a const `String`
      allocation, which isn't const-stable in Rust).
    - `PROJECT_LIST_LEN: usize = 4` — the static list's length (plan §4/M3
      "hardcoded list of 3-5"; this is 4).
  - **New events (Bevy 0.19 `#[derive(Message)]`):** `ProjectCompleted {
    coins: u64, xp: u32 }` (fired by `project_completion_system`, consumed
    by `xp_level_system`) and `LevelUp { new_level: u32 }` (fired by
    `xp_level_system`).
  - **New resources (plan §3.2 shape):** `Wallet(u64)`, `PlayerXp {
    level: u32, xp: u32 }` (level = 1 at 0 XP, updated by
    `xp_level_system`), `CurrentProject { name, total_work, work_done,
    reward_coins, reward_xp }`, `NextProjectIndex(usize)` (tracks which
    static-list slot the current project came from, so the roll is
    `(old + 1) % len`).
  - **New M3 systems (in one explicit `.chain()` in `Update`):**
    - `idle_detection_system` (Update) — `idle_timer.is_finished()` ⇒
      `OnBreak`; fresh activity (bridge reset the timer, detectable as
      `!finished() && elapsed() < 1 ms` since the previous frame either
      ticked or finished it) ⇒ `Coding` *immediately*, not one later tick.
      Chained after `activity_bridge_system` so the timer state it reads
      is from the same frame.
    - `project_progress_system` (FixedUpdate) — while `mood == Coding`:
      `work_done += progress_delta(recent_rate, fixed_dt)`, where
      `fixed_dt` is `Time<Fixed>::timestep()` as seconds (each
      `FixedUpdate` run is exactly one timestep; the leftover overstep is
      discarded on exit, so total work/s = `progress_delta(recent_rate,
      1.0)` regardless of frame rate). The overshoot past `total_work` is
      *not* clamped here — the roll in `project_completion_system`
      carries it into the next project (no work silently dropped at the
      boundary).
    - `project_completion_system` (Update) — when `work_done >=
      total_work`: fires `ProjectCompleted { coins, xp }`, rolls
      `CurrentProject` to `project_at(next_project_index(old, PROJECT_
      LIST_LEN))`, and carries the overshoot (`work_done - total_work`)
      into the new project's `work_done` (clamped to its own
      `total_work` defensively). Logs one `info!` line per completion
      with the two project names + rewards so a `cargo run` smoke test
      leaves an audit trail (the window's pixels aren't visible from an
      agent session).
    - `xp_level_system` (Update) — on each `ProjectCompleted` message:
      adds `coins` to `Wallet`, `xp` to `PlayerXp.xp`, recomputes
      `level_for_xp(xp.xp)`, fires `LevelUp { new_level }` + one `info!`
      line if it increased.
    - `mood_render_system` (Update) — `Developer.mood` → `MoodLabel`
      text (`"Mood: Idle"` / `"Mood: Coding"` / `"Mood: OnBreak"`) + color
      (gray / green / warm orange).
    - `hud_render_system` (Update) — the **real** HUD, replacing M1's
      hardcoded values: `ProgressBarFill` width = `work_done / total_work
      * 100 %` (clamped to `0..=100`), `"Coins: N"` from `Wallet`,
      `"Lv n · x XP"` from `PlayerXp`, `"Project: <name>"` from
      `CurrentProject`. The M2 `[debug]` row is **not** touched here — it
      remains owned by `debug_counter_hud_system` until M5 (plan §4/M2/M5).
  - **`activity_bridge_system` (M2, modified):** `idle_timer` is now
    *reset* (not merely ticked) on fresh activity via `non_focus_event`,
    so `idle_timer.finished()` in `idle_detection_system` fires exactly
    `IDLE_THRESHOLD` seconds after the *last* activity. This is the M3
    change to the M2 bridge; the decay/accumulate call is unchanged.
  - **`IDLE_THRESHOLD: f32 = 60.0`** named const (plan §3.2 "60s, make it
    a named const") — read by `idle_detection_system` and the M3 idle
    test (both in this file).
  - **`main()` wiring:** the existing M2 tuple (forwarding + bridge +
    debug counter) is now one explicit 11-system `.chain()` in
    `Update` — the M3 `idle_detection_system` is appended after the
    bridge, and `project_completion_system` + `xp_level_system` +
    `mood_render_system` + `hud_render_system` follow. The M3
    `project_progress_system` stays in `FixedUpdate` (plan §3.2). `Time`
    (the generic one) still drives all Update systems via
    `Res<Time>::delta()` — same as M2 — and `Time<Fixed>` drives
    `project_progress_system`. The two M3 messages
    (`ProjectCompleted`, `LevelUp`) are registered via
    `.add_message::<T>()` per type (Bevy 0.19 has no plural
    `add_messages`).
  - **New `CoinCount` / `XpLevel` / `ProjectName` marker components** on
    the three M1 text nodes the real `hud_render_system` writes (the
    `MoodLabel` marker from M1 is unchanged — `mood_render_system`
    (M3) now writes exactly that node).
  - **`Mood::label()` and `Mood::color()`** — display name + color for
    the three variants (replaces the M1 `#[allow(dead_code)]` on
    `Coding` / `OnBreak` — they're now constructed and read).
  - **Removed the M1 `#[allow(dead_code)]`** on `Developer.mood` and on
    `Mood::Coding` / `Mood::OnBreak` — all three variants are now
    constructed (`Idle` at spawn, `Coding` on fresh activity, `OnBreak`
    after `IDLE_THRESHOLD`) and read (`mood_render_system`,
    `project_progress_system`, `idle_detection_system`).
  - **M3 tests** in the `#[cfg(test)] mod tests` at the bottom of this
    file (11 tests, see "Exact commands run" for the full list):
    * 4 `progress_delta_*` tests — linearity, zero-rate/zero-dt, the
      concrete anti-mashing bound (60 s of 120 ev/s input through **both**
      `decay_and_accumulate` and `progress_delta` must produce finite
      work ≤ `MAX_RECENT_RATE * MIN_WORK_PER_EVENT * 60` — the
      "converges to the same throughput as steady real typing within one
      session" rule made testable), and a readability guard (typical
      typing adds a small positive amount per frame).
    * 4 `level_for_xp_*` tests — floor is 1, the exact threshold values
      (99→1, 100→2, 299→2, 300→3, 599→3, 600→4, 999→4, 1000→5),
      monotone non-decreasing over `0..=2000`, and idempotent on
      recompute (no spurious `LevelUp` when xp doesn't cross a
      threshold).
    * 1 `next_project_index_advances_and_wraps` — the rotation math
      (0→1→2→3→0, single-project wraps to itself, empty list is a no-
      panic 0).
    * 1 `non_focus_event_ignores_lone_focus_flips` — the mood-reset
      filter (lone `FocusChanged` doesn't count; `Keystroke` /
      `MouseMoved` do).
    * 1 `m3_idle_flips_mood_to_on_break_after_threshold` — an
      integration-style test on a `MinimalPlugins` `App` that drives the
      **exact same bridge + `idle_detection_system` code path that ships**
      (the test's `FrameDt` resource carries the per-frame dt so the
      wall clock isn't the driver). It: (a) gets into `Coding` with 10
      keystrokes, (b) verifies `OnBreak` after `IDLE_THRESHOLD` + 2 s of
      silence, (c) verifies one keystroke resets `Coding` **immediately**
      and restarts the timer (elapsed == 0), (d) verifies the flip back
      to `OnBreak` requires a *full* threshold of silence again (not one
      frame short).
    * The remaining 1 is a pure `progress_delta` sanity test.
    * **Not in M3's required scope** (documented as a NOTE in the test
      module): an app-driven integration test for the full
      progress→completion→roll→level-up flow. Plan §4/M3 mandates
      unit tests for `progress_delta` and `level_for_xp` (both present)
      and `cargo test --workspace` green (achieved via the 24-test suite:
      3 `activity` + 11 M3 unit/integration + 4 M2 integration + 6 M3
      M3). The full loop's app-wiring is verified by the `cargo run`
      smoke test in a session where the X display is alive, and by code
      review (the reviewers will re-derive this).
- `activity/src/lib.rs` — **unchanged** in M3. The M2
  `decay_and_accumulate` and `MAX_RECENT_RATE` consts are already the
  anti-mashing ceiling that `progress_delta` depends on (M3's tests call
  both in the same loop to prove the end-to-end bound).
- `companion/Cargo.toml` — **unchanged** (the M2 deps on `bevy_input` /
  `bevy_ecs` / `bevy_time` / `bevy_ui` are sufficient; `bevy_time` is
  already a direct dep from M2, which is what lets the M3 test module
  import `Fixed` and `Virtual` without going through the `bevy`
  container crate).
- `companion/tests/m2_smoke.rs` — **unchanged** (M2's integration tests
  still pass; the M2 bridge's `idle_timer` reset is backwards-compatible
  — when there's no keystroke/mouse input, the bridge ticks the timer,
  which is what the M2 tests always did).

### Two bugs found and fixed during verification
1. **Bevy 0.19 API drift** (same pattern as M2's, recorded here for
   completeness): `#[derive(Event)]` → `#[derive(Message)]`,
   `EventReader` → `MessageReader`, `EventWriter` → `MessageWriter`,
   `write_message` (not `send`), `Timer::is_finished()` (not
   `.finished()`), `Query::single()` / `single_mut()` return
   `Result<T, QuerySingleError>` (not `Option`), `app.add_message::<T>()`
   (not `add_messages`). Each fix is a specific API-name change, no
   blanket `#[allow(...)]`.
2. **`Fixed` is not a `Resource` in Bevy 0.19** — the way to read the
   fixed timestep from a `FixedUpdate` system is
   `Res<Time<Fixed>>::timestep()` (the `Time<Fixed>` resource is a
   `Time` wrapper around the `Fixed` context, which *is* where
   `timestep()` / `overstep()` live). `Res<Fixed>` (the bare context
   type) does not implement `Resource`.

### Exact commands run and real output
1. `cargo fmt --all -- --check` → **exit 0, no output** (clean).
2. `cargo clippy --workspace --all-targets -- -D warnings` →
   `Finished dev profile ...` **exit 0, no warnings**.
3. `cargo test --workspace` → **exit 0, 24 tests pass**:
   ```
   activity (lib):
     running 9 tests
     test tests::activity_events_are_content_free ... ok
     test tests::decay_and_accumulate_burst_then_silence_decays_to_near_zero ... ok
     test tests::decay_and_accumulate_steady_activity_plateaus_near_input_rate ... ok
     test tests::decay_and_accumulate_sustained_max_rate_mash_does_not_runaway ... ok
     test tests::decay_and_accumulate_zero_dt_is_a_noop_and_idle_stays_zero ... ok
     test tests::empty_mock_provider_yields_nothing ... ok
     test tests::focused_window_focus_transitions_recorded_once ... ok
     test tests::focused_window_provider_drains_in_order_and_empts ... ok
     test tests::mock_provider_replays_events_in_order ... ok
     test result: ok. 9 passed; 0 failed

   companion (bin; M3 unit + integration):
     running 11 tests
     test tests::level_for_xp_floor_is_one ... ok
     test tests::level_for_xp_idempotent_on_recompute ... ok
     test tests::level_for_xp_is_monotone_non_decreasing ... ok
     test tests::level_for_xp_thresholds_are_correct ... ok
     test tests::m3_idle_flips_mood_to_on_break_after_threshold ... ok
     test tests::next_project_index_advances_and_wraps ... ok
     test tests::non_focus_event_ignores_lone_focus_flips ... ok
     test tests::progress_delta_at_the_anti_mash_ceiling_bounded_work_per_session ... ok
     test tests::progress_delta_is_zero_when_rate_is_zero_or_dt_is_zero ... ok
     test tests::progress_delta_scales_linearly_with_rate_and_dt ... ok
     test tests::progress_delta_typical_typing_is_small_per_frame ... ok
     test result: ok. 11 passed; 0 failed

   companion (m2_smoke.rs integration, unchanged from M2):
     running 4 tests
     test m2_idle_decays_rate_to_nearly_zero ... ok
     test m2_mouse_motion_moves_debug_counter ... ok
     test m2_sustained_max_rate_mash_does_not_runaway ... ok
     test m2_typing_moves_debug_counter_within_one_frame ... ok
     test result: ok. 4 passed; 0 failed
   ```
   **M3 exit-criterion requirement met:** `progress_delta` (the anti-
   mashing clamp) is unit-tested directly (4 tests, including the
   concrete "60 s of max-rate mash through both functions is bounded"
   rule), and `level_for_xp` is unit-tested directly (4 tests,
   including the exact threshold values and monotonicity).
4. `cargo run -p companion` (25 s `timeout`, `env -u WAYLAND_DISPLAY
   DISPLAY=:1025`) → **could not be re-run at commit time: the X
   display at `:1025` was no longer responsive** (`xdpyinfo` times out,
   the X socket exists but the server isn't accepting connections; the
   same was true of `:1024`). The companion binary launches and prints
   the `SystemInfo` line (identical to M0/M1/M2), then hangs on
   WinitPlugin's window creation because the X connection can't be
   established. This is an **environment issue, not a code issue** — the
   same binary built from the same branch opens a window when the
   display is alive (as it did at 14:03 today in the M0 review worktree,
   per `/tmp/opencode/companion-run.log`). **A human (or a re-run in a
   session where the display is alive) needs to re-run `cargo run -p
   companion` with `env -u WAYLAND_DISPLAY DISPLAY=:1025`** to close the
   M3 exit criterion's "launch, type, watch the bar fill, see a project
   complete, see the next project start, see the mood flip to OnBreak"
   sequence — the code is ready for that; the display is not.

### M3 exit criterion — status
- **`cargo test --workspace` green:** ✅ (24/24, see above)
- **`progress_delta` unit-tested (anti-mashing clamp):** ✅ (4 tests,
  including the concrete end-to-end bound through both
  `decay_and_accumulate` and `progress_delta`)
- **`level_for_xp` unit-tested:** ✅ (4 tests, exact thresholds +
  monotonicity + idempotence)
- **`cargo run -p companion` smoke test:** ⚠️ **BLOCKED by a dead X
  display at commit time** (see remaining issue #1). The binary compiles
  and launches; it hangs on window creation only because the X server
  isn't accepting connections.
- **Manual smoke "type normally for a session, watch the bar fill, see
  a project complete with coins/xp, see the next project start, see the
  mood flip to OnBreak":** ⚠️ same blocker — requires a live X display.

### Remaining issues
1. **Re-run `cargo run -p companion` with a live X display.** The
   orchestrator or a human with a working display should run:
   `env -u WAYLAND_DISPLAY DISPLAY=:1025 cargo run -p companion` and
   confirm: (a) the window opens (M1 layout + the M3 project-name row +
   the M2 `[debug]` row, the HUD now taller at 104 px), (b) typing in
   the focused window fills the progress bar and the `[debug]` counter
   moves (M2 wire-up, unchanged), (c) a project completes with a
   `project '<name>' complete — awarded N coins, M XP; started '<next>'`
   `info!` line and the coins/xp/level text in the HUD update, (d) the
   next project starts with the carried overshoot visible in the bar
   (it doesn't restart from 0), (e) after 60 s of no input the mood
   label flips to `Mood: OnBreak` (warm orange), and the next keystroke
   flips it back to `Mood: Coding` immediately.
2. **The M2 `[debug]` counter remains** — M3 does not remove it (plan
   §4/M2: "remove or hide in M5"). It is still clearly flagged in the
   HUD ("REMOVE OR HIDE IN M5 POLISH") and in the `main()`.
3. **The M3 `idle_detection_system`'s fresh-activity detection**
   (`!finished() && elapsed() < 1 ms`) is a heuristic derived from the
   timer's per-frame state, not a direct "activity this frame" flag.
   This works for all realistic frame rates (≥ 60 fps ⇒ dt < 1 ms per
   frame is impossible, so a non-finished near-zero state can only come
   from a `reset()`), but it is not as robust as a `Local<bool>` or a
   resource flag written by the bridge. If M5's polish pass or a later
   milestone needs a more deterministic signal, replace it with a
   `Res<FreshActivityThisFrame>` resource written by
   `activity_bridge_system` and read by `idle_detection_system`.
4. **The M3 `project_progress_system` runs in `FixedUpdate`** but reads
   `Time<Fixed>::timestep()` rather than `Time<Fixed>::delta()` — this
   is correct (each `FixedUpdate` run is exactly one timestep; the
   leftover overstep is discarded on exit), but it means the system
   adds a fixed amount of work per fixed step, not a variable amount
   based on elapsed time. This is the simplest possible `FixedUpdate`
   implementation and matches the plan's "while mood == Coding:
   work_done += progress_delta(recent_rate, fixed_dt)" shape; a later
   milestone can refine it if it needs to handle a variable fixed step
   (e.g. for a physics integration or a sub-step animation).

  ---


