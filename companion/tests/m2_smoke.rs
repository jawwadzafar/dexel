//! Integration-level mechanical verification of M2's activity wiring.
//!
//! M2's stated exit criterion is *manual* (type / move the mouse in the
//! focused window → activity is registered within ~1 s, demonstrated in the
//! milestone log via a human-observed smoke run). This test goes one step
//! further: it exercises the **exact same M2 resource + provider + math code
//! that ships in `companion`** — real `KeyboardInput` / `MouseMotion`
//! messages are written into a Bevy app, the M2 forwarding systems drain them
//! into a `FocusedWindowProvider`, and `activity_bridge_system` (the same
//! body as `main.rs`) drains the provider into `ActivityMeter` via
//! `activity::decay_and_accumulate`.
//!
//! **M5 note (plan §4/M5):** M2's temporary `[debug]` raw-event HUD counter
//! — the `SessionEventCount` resource and `debug_counter_hud_system` — was
//! removed in the M5 polish pass because a raw count contradicts the
//! anti-mashing principle (plan §1) if a user saw it as the real metric.
//! These tests previously asserted that counter's text; they now assert the
//! underlying `ActivityMeter.recent_rate` (the *shipped* decaying rate the
//! game actually uses) instead. The wire-up is still proven mechanically:
//! real input messages → provider → bridge → a positive, decaying,
//! capped `recent_rate`.
//!
//! Uses `MinimalPlugins` (pure ECS — no window, no render). Bevy 0.19's
//! `Time` resource is auto-driven by wall clock via `time_system` in the
//! `First` schedule, so the harness threads `dt` in explicitly through a
//! per-frame resource (`FrameDt`) instead of relying on `Res<Time>` — the
//! pure math (decay_and_accumulate) is the same function main.rs calls.

use activity::ActivityProvider;
use bevy::prelude::*;
use bevy_ecs::message::MessageReader;
use bevy_input::ButtonState;
use bevy_input::keyboard::{Key, KeyCode, KeyboardInput};
use bevy_input::mouse::MouseMotion;
use std::time::Duration;

// ---------------------------------------------------------------------------
// M2 types — same shape as the ones in `companion/src/main.rs` (kept local
// to the test binary because integration tests cannot import main's private
// items). M5 removed `SessionEventCount` and `DebugActivityCount` from
// main.rs; they are intentionally absent here too.
// ---------------------------------------------------------------------------

/// Per-frame dt, set by the test harness before each `app.update()`. The
/// bridge reads this (a stand-in for main.rs's `Res<Time>` — see module
/// doc) so the harness controls dt deterministically.
#[derive(Resource, Default)]
struct FrameDt(Duration);

/// The provider resource, wrapping the shipping `FocusedWindowProvider`.
#[derive(Resource, Default)]
struct ActivitySource(activity::FocusedWindowProvider);

/// Same shape as in main.rs. `recent_rate` is a decaying average (events/
/// second), not a raw counter — this is the value the shipped game uses to
/// drive progress, so asserting on it is asserting on shipped behavior.
#[derive(Resource)]
struct ActivityMeter {
    recent_rate: f32,
    #[allow(dead_code)]
    idle_timer: Timer,
}

impl Default for ActivityMeter {
    fn default() -> Self {
        Self {
            recent_rate: 0.0,
            idle_timer: Timer::from_seconds(60.0, TimerMode::Repeating),
        }
    }
}

// ---------------------------------------------------------------------------
// M2 systems — same logic as in main.rs (except reading `FrameDt` instead
// of `Res<Time>` for deterministic dt in tests). M5 removed the debug
// counter HUD system and the `SessionEventCount` parameter from the bridge;
// this bridge matches main.rs's M5 bridge exactly.
// ---------------------------------------------------------------------------

fn forward_keyboard_events(
    mut source: ResMut<ActivitySource>,
    mut keyboard_events: MessageReader<KeyboardInput>,
) {
    for _event in keyboard_events.read() {
        source.0.record([activity::ActivityEvent::Keystroke]);
    }
}

fn forward_mouse_events(
    mut source: ResMut<ActivitySource>,
    mut mouse_motion: MessageReader<MouseMotion>,
) {
    for _event in mouse_motion.read() {
        source.0.record([activity::ActivityEvent::MouseMoved]);
    }
}

/// Drains the provider, updates `ActivityMeter.recent_rate` via the real
/// `activity::decay_and_accumulate` — same body as main.rs (post-M5, with
/// the session counter removed), with `dt` read from `FrameDt`.
fn activity_bridge_system(
    mut source: ResMut<ActivitySource>,
    mut meter: ResMut<ActivityMeter>,
    dt: Res<FrameDt>,
) {
    let events = source.0.poll();
    meter.recent_rate = activity::decay_and_accumulate(meter.recent_rate, &events, dt.0);
    meter.idle_timer.tick(dt.0);
}

// ---------------------------------------------------------------------------
// Test harness
// ---------------------------------------------------------------------------

fn build_app() -> App {
    let mut app = App::new();
    app.add_plugins(MinimalPlugins);

    // 0.19: `add_message::<M>()` registers a `Message` type so that
    // `MessageReader<M>` / `world.write_message::<M>()` can find it.
    app.add_message::<KeyboardInput>();
    app.add_message::<MouseMotion>();

    app.insert_resource(FrameDt(Duration::from_millis(16)))
        .init_resource::<ActivitySource>()
        .init_resource::<ActivityMeter>()
        .add_systems(
            // Bevy 0.19: `(a, b, c).chain()` on the tuple is what makes
            // "forward → bridge" ordering hold within one frame.
            Update,
            (
                forward_keyboard_events,
                forward_mouse_events,
                activity_bridge_system,
            )
                .chain(),
        );
    app
}

/// Write a real `KeyboardInput` "A press" message into the pipeline.
fn key_a(world: &mut World) {
    world.write_message(KeyboardInput {
        key_code: KeyCode::KeyA,
        logical_key: Key::Character("a".into()),
        state: ButtonState::Pressed,
        text: Some("a".into()),
        repeat: false,
        window: Entity::PLACEHOLDER,
    });
}

/// Write a real `MouseMotion` message into the pipeline.
fn mouse(delta: Vec2, world: &mut World) {
    world.write_message(MouseMotion { delta });
}

/// Set `FrameDt` to `delta` and run one full Update frame.
fn step(app: &mut App, delta: Duration) {
    *app.world_mut().resource_mut::<FrameDt>() = FrameDt(delta);
    app.update();
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[test]
fn m2_typing_registers_activity_within_one_frame() {
    // M2 wire-up, mechanical: 5 real keystrokes → the shipping
    // `activity_bridge_system` drains them into `ActivityMeter.recent_rate`,
    // which must lift above 0 within one frame (the M2 exit criterion's
    // "within ~1 second", proven in one frame). This replaces the removed
    // `SessionEventCount` / debug-counter-text assertion with an assertion
    // on the shipped rate.
    let mut app = build_app();

    for _ in 0..5 {
        key_a(app.world_mut());
    }
    step(&mut app, Duration::from_millis(16));

    let rate = app.world().resource::<ActivityMeter>().recent_rate;
    assert!(
        rate > 0.0,
        "5 keystrokes at 16 ms should lift recent_rate (dt > 0), got {rate}"
    );
}

#[test]
fn m2_mouse_motion_registers_activity() {
    // Same wire-up via mouse: 3 real mouse-motion messages lift the shipped
    // `recent_rate` above 0 (replaces the removed raw-count assertion).
    let mut app = build_app();

    for _ in 0..3 {
        mouse(Vec2::new(1.0, 0.0), app.world_mut());
    }
    step(&mut app, Duration::from_millis(16));

    let rate = app.world().resource::<ActivityMeter>().recent_rate;
    assert!(
        rate > 0.0,
        "3 mouse motions at 16 ms should lift recent_rate, got {rate}"
    );
}

#[test]
fn m2_idle_decays_rate_to_nearly_zero() {
    // Burst then silence: a decaying rate must return to ~0 (DECAY_PER_SECOND
    // = 1/s → half-life ≈ 0.69 s → ~14 half-lives over 10 s). Unchanged from
    // M2 — it never referenced the removed debug counter.
    let mut app = build_app();

    for _ in 0..5 {
        key_a(app.world_mut());
    }
    step(&mut app, Duration::from_millis(16));
    let after_burst = app.world().resource::<ActivityMeter>().recent_rate;
    assert!(
        after_burst > 0.0,
        "burst should lift rate > 0, got {after_burst}"
    );

    // 10 s of silence in 100 × 100 ms steps.
    for _ in 0..100 {
        step(&mut app, Duration::from_millis(100));
    }
    let after_silence = app.world().resource::<ActivityMeter>().recent_rate;
    assert!(
        after_silence < 0.1,
        "rate should be ~0 after ~10 s of silence, got {after_silence}"
    );
}

#[test]
fn m2_sustained_max_rate_mash_does_not_runaway() {
    // Anti-mashing: a sustained max-rate mash (≈ 3187 ev/s input) must keep
    // the shipped `recent_rate` finite, at or below `MAX_RECENT_RATE`, and
    // convergent (the last 50 frames within 25% of the final value).
    // Unchanged from M2 — it asserted the rate, not the debug counter.
    let mut app = build_app();
    let mut late = Vec::with_capacity(50);
    for frame in 0..300 {
        // 50 mouse motions + 1 keystroke per 16 ms frame ≈ 3187 ev/s input.
        for _ in 0..50 {
            mouse(Vec2::ZERO, app.world_mut());
        }
        key_a(app.world_mut());
        step(&mut app, Duration::from_millis(16));

        let rate = app.world().resource::<ActivityMeter>().recent_rate;
        assert!(rate.is_finite(), "rate ran away at frame {frame}: {rate}");
        assert!(
            rate <= activity::MAX_RECENT_RATE,
            "rate exceeded MAX_RECENT_RATE at frame {frame}: {rate}"
        );
        if frame >= 250 {
            late.push(rate);
        }
    }
    // The last 50 frames' rates should be stable (converged), not
    // oscillating at the clamp ceiling (plan §1).
    let last = late[late.len() - 1];
    for &r in &late {
        assert!(
            (r - last).abs() <= last * 0.25 + 1.0,
            "late rates should have settled; saw {r}, final {last}"
        );
    }
}
