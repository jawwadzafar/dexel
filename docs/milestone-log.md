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

