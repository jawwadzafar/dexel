//! dev-companion — companion desktop game binary.
//!
//! M1: static scene + HUD skeleton. M2: activity wiring — a
//! `FocusedWindowProvider` is fed by Bevy input event readers (forwarded in,
//! never read directly by a game system), `activity_bridge_system` drains it
//! each frame into an `ActivityMeter` resource via `decay_and_accumulate`,
//! and a **temporary** `[debug]` counter on the HUD shows the raw
//! activity-event count this session (to be removed/hidden in M5 polish —
//! see docs/milestone-log.md).
//!
//! See docs/implementation-plan.md §3.1/§3.2 and §4/M2.

use activity::{ActivityEvent, ActivityProvider, FocusedWindowProvider, decay_and_accumulate};
// Bevy 0.19: the event-reading system param is `MessageReader` (the 0.19
// "Messages" API replaced `EventReader`); input events live under
// `bevy::input` (the prelude only re-exports *buttons*/codes, not the
// input/motion event types).
use bevy::ecs::message::MessageReader;
use bevy::input::keyboard::KeyboardInput;
use bevy::input::mouse::MouseMotion;
use bevy::prelude::*;
use bevy::window::WindowFocused;

/// The one developer character. Holds the character's current mood, which
/// later milestones' systems will update (see plan §3.2).
#[derive(Component)]
struct Developer {
    // Unread until M3's mood_render_system reads it (plan §3.2/§4-M3) —
    // spawned now so the component's shape matches the plan from M1.
    #[allow(dead_code)]
    mood: Mood,
}

/// The developer's mood. M1 spawns it at `Idle`; later milestones drive it
/// from real activity via `idle_detection_system` (plan §3.2).
#[derive(Clone, Copy, PartialEq, Eq, Debug, Default)]
// Coding/OnBreak are unconstructed until M3 wires idle_detection_system and
// mood transitions (plan §3.2/§4-M3) — declared now so the type matches the
// plan's full design rather than growing variants milestone-by-milestone.
#[allow(dead_code)]
enum Mood {
    #[default]
    Idle,
    Coding,
    OnBreak,
}

/// Marker on the HUD text that mirrors `Developer.mood` (plan §3.2).
#[derive(Component)]
struct MoodLabel;

/// Marker on the progress-bar fill node, whose width mirrors the current
/// project's completion (plan §3.2).
#[derive(Component)]
struct ProgressBarFill;

/// Marker on the **temporary M2 debug counter** text (plan §4/M2).
///
/// REMOVE OR HIDE IN M5 POLISH: it exists only to prove the activity
/// wire-up works during M2, not to ship. A raw event count contradicts the
/// anti-mashing principle (plan §1) if a user ever sees it as the real
/// metric.
#[derive(Component)]
struct DebugActivityCount;

/// The companion crate's activity source (plan §3.1): `FocusedWindowProvider`,
/// fed by the Bevy input-forwarding systems below. Held as a resource so
/// Update systems can borrow it.
#[derive(Resource, Default)]
struct ActivitySource(FocusedWindowProvider);

/// Activity state shared through ECS (plan §3.2).
///
/// `recent_rate` is a **decaying average in events/second** — not a raw
/// counter — updated per frame by `activity_bridge_system` via
/// `activity::decay_and_accumulate` (a plain `fn`, unit-tested directly in
/// the `activity` crate).
///
/// `idle_timer` is part of the planned shape from M1 (used by M3's
/// `idle_detection_system`, not yet read here).
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

/// Total raw activity events seen this session — drives *only* the temporary
/// `[debug]` HUD counter (plan §4/M2). Not part of any game mechanic.
#[derive(Resource, Default)]
struct SessionEventCount(usize);

fn main() {
    App::new()
        .add_plugins(DefaultPlugins)
        // M2 activity resources — inserted during construction, so they exist
        // before any Update frame runs.
        .init_resource::<ActivitySource>()
        .init_resource::<ActivityMeter>()
        .init_resource::<SessionEventCount>()
        .add_systems(Startup, setup_scene)
        .add_systems(
            Update,
            // Bevy 0.19: tuple systems run in *unspecified* (effectively
            // nondeterministic) order unless chained. `(a, b, c).chain()` is
            // what makes "forward → bridge → HUD text in the same frame"
            // actually hold — without it, the bridge can drain the provider
            // before the forwarders have recorded anything this frame, and
            // the debug counter lags a frame (or stalls entirely).
            (
                // Forward Bevy input events into the provider — the ONLY
                // place Bevy input events are read; no game system reads
                // them directly (plan §3.1).
                forward_focus_events,
                forward_keyboard_events,
                forward_mouse_events,
                // Drain the provider, update the meter, tick the counter.
                activity_bridge_system,
                // Update the temporary [debug] counter text (plan §4/M2,
                // REMOVE OR HIDE IN M5 POLISH).
                debug_counter_hud_system,
            )
                .chain(),
        )
        .run();
}

/// Spawns the M1 scene (Developer entity, desk, HUD) — unchanged in M2.
fn setup_scene(mut commands: Commands) {
    // A UI camera is required to render UI nodes.
    commands.spawn(Camera2d);

    spawn_desk(&mut commands);

    spawn_hud(&mut commands);
}

/// Forward the window's focus state into the provider (plan §3.1).
///
/// Bevy 0.19 exposes one `WindowFocused` message carrying a `focused: bool`
/// (no separate `WindowUnfocused` type), so both directions are recorded
/// here from that single message.
fn forward_focus_events(
    mut source: ResMut<ActivitySource>,
    mut focus_events: MessageReader<WindowFocused>,
) {
    for event in focus_events.read() {
        source
            .0
            .record([ActivityEvent::FocusChanged(event.focused)]);
    }
}

/// Forward keyboard input: one `Keystroke` per `KeyboardInput` event. No key
/// identity is ever recorded (privacy invariant, plan §3.1).
fn forward_keyboard_events(
    mut source: ResMut<ActivitySource>,
    mut keyboard_events: MessageReader<KeyboardInput>,
) {
    for _event in keyboard_events.read() {
        source.0.record([ActivityEvent::Keystroke]);
    }
}

/// Forward mouse motion: one `MouseMoved` per `MouseMotion` event. The
/// `delta` is discarded, not recorded (privacy invariant, plan §3.1).
fn forward_mouse_events(
    mut source: ResMut<ActivitySource>,
    mut mouse_motion: MessageReader<MouseMotion>,
) {
    for _event in mouse_motion.read() {
        source.0.record([ActivityEvent::MouseMoved]);
    }
}

/// Drains the provider once per frame, updates `ActivityMeter.recent_rate`
/// via `decay_and_accumulate`, and advances the session event counter
/// (plan §3.2 / §4/M2).
fn activity_bridge_system(
    mut source: ResMut<ActivitySource>,
    mut meter: ResMut<ActivityMeter>,
    mut session_count: ResMut<SessionEventCount>,
    times: Res<Time>,
) {
    let dt = times.delta();
    let events = source.0.poll();
    session_count.0 += events.len();
    meter.recent_rate = decay_and_accumulate(meter.recent_rate, &events, dt);
    meter.idle_timer.tick(dt);
}

/// Write the temporary `[debug]` counter text each frame (plan §4/M2).
///
/// Also logs the counter value when the session count changes by multiples
/// of 10 — this is the *observable* signal the M2 smoke test uses to prove
/// the wire-up works, since the window's pixels aren't visible from a
/// headless `cargo run`. Remove the `info!` together with the counter in M5.
fn debug_counter_hud_system(
    session_count: Res<SessionEventCount>,
    meter: Res<ActivityMeter>,
    mut last_logged: Local<usize>,
    mut debug_query: Query<&mut Text, With<DebugActivityCount>>,
) {
    for mut text in &mut debug_query {
        *text = Text::new(format!(
            "[debug] activity events (this session): {} · rate {:.1}/s",
            session_count.0, meter.recent_rate
        ));
    }
    // Log every 10 events so a smoke run leaves an unambiguous audit trail
    // of the counter moving (the window's pixels aren't accessible from a
    // headless CI / agent session — the log line is).
    if session_count.0 > *last_logged && session_count.0.is_multiple_of(10) {
        info!(
            "[debug] activity counter moved: {} events this session (rate {:.1}/s)",
            session_count.0, meter.recent_rate
        );
        *last_logged = session_count.0;
    }
}

/// Spawns the Developer entity and its placeholder desk (unchanged from M1).
fn spawn_desk(commands: &mut Commands) {
    commands
        .spawn((
            // The developer character, mood starts Idle.
            Developer { mood: Mood::Idle },
            // The desk's visual root: a full-window container that the desk
            // area fills (the HUD bar is a sibling in the same root).
            Node {
                width: Val::Percent(100.0),
                height: Val::Percent(100.0),
                // Flex column so the desk area and the HUD stack vertically.
                flex_direction: FlexDirection::Column,
                ..default()
            },
        ))
        .with_children(|parent| {
            // Desk area: a placeholder room that fills everything above the
            // HUD. Colored rects stand in for art (plan §3.3).
            parent
                .spawn((
                    Node {
                        width: Val::Percent(100.0),
                        // Flex-grow so the desk takes the remaining height
                        // above the fixed-height HUD bar.
                        flex_grow: 1.0,
                        flex_direction: FlexDirection::Column,
                        align_items: AlignItems::Center,
                        justify_content: JustifyContent::Center,
                        row_gap: px(24.0),
                        ..default()
                    },
                    BackgroundColor(Color::srgb(0.13, 0.15, 0.20)),
                ))
                .with_children(|desk| {
                    // The desk surface: a wide horizontal placeholder slab.
                    desk.spawn((
                        Node {
                            width: px(520.0),
                            height: px(24.0),
                            ..default()
                        },
                        BackgroundColor(Color::srgb(0.45, 0.32, 0.20)),
                    ));
                    // A placeholder "computer" prop next to the developer.
                    desk.spawn((
                        Node {
                            width: px(140.0),
                            height: px(90.0),
                            ..default()
                        },
                        BackgroundColor(Color::srgb(0.20, 0.22, 0.28)),
                    ));
                });
        });
}

/// Spawns the HUD bar pinned to the bottom of the window.
///
/// Contains all four M1 elements (hardcoded) plus the **temporary M2**
/// `[debug]` activity-counter row (component `DebugActivityCount`, removed
/// or hidden in M5 polish). The bar was tallened from 64 to 88 px to
/// accommodate the extra row.
fn spawn_hud(commands: &mut Commands) {
    commands
        .spawn((
            Node {
                width: Val::Percent(100.0),
                height: px(88.0),
                flex_direction: FlexDirection::Column,
                justify_content: JustifyContent::Center,
                row_gap: px(4.0),
                padding: UiRect::axes(px(12.0), px(6.0)),
                ..default()
            },
            BackgroundColor(Color::srgb(0.09, 0.10, 0.13)),
        ))
        .with_children(|hud| {
            // --- Progress bar (fixed 0% fill for M1) ---
            hud.spawn((
                Node {
                    width: Val::Percent(100.0),
                    height: px(14.0),
                    ..default()
                },
                BackgroundColor(Color::srgb(0.20, 0.22, 0.26)),
            ))
            .with_children(|bar| {
                // The fill starts at 0% (width 0) for M1; `hud_render_system`
                // in M3 will update this from the project's progress.
                bar.spawn((
                    Node {
                        width: Val::Percent(0.0),
                        height: Val::Percent(100.0),
                        ..default()
                    },
                    BackgroundColor(Color::srgb(0.10, 0.60, 0.35)),
                    ProgressBarFill,
                ));
            });

            // --- Metrics row: coins, xp/level, mood (all hardcoded) ---
            hud.spawn((
                Node {
                    width: Val::Percent(100.0),
                    justify_content: JustifyContent::SpaceBetween,
                    ..default()
                },
                BackgroundColor(Color::NONE),
            ))
            .with_children(|row| {
                // Coin count (hardcoded 0 for M1).
                row.spawn((
                    Text::new("Coins: 0"),
                    TextFont::from_font_size(px(14.0)),
                    TextColor(Color::srgb(0.95, 0.80, 0.30)),
                ));
                // Xp / level (hardcoded Lv 1, 0 XP for M1).
                row.spawn((
                    Text::new("Lv 1 · 0 XP"),
                    TextFont::from_font_size(px(14.0)),
                    TextColor(Color::srgb(0.9, 0.9, 0.9)),
                ));
                // Mood label. Marked `MoodLabel` so `mood_render_system`
                // (M3) can update exactly this text; hardcoded "Idle" for M1.
                row.spawn((
                    Text::new("Mood: Idle"),
                    TextFont::from_font_size(px(14.0)),
                    TextColor(Color::srgb(0.45, 0.75, 0.95)),
                    MoodLabel,
                ));
            });

            // --- Temporary M2 debug counter (REMOVE OR HIDE IN M5 POLISH) ---
            hud.spawn((
                Text::new("[debug] activity events (this session): 0 · rate 0.0/s"),
                TextFont::from_font_size(px(12.0)),
                TextColor(Color::srgb(0.90, 0.45, 0.45)),
                DebugActivityCount,
            ));
        });
}
