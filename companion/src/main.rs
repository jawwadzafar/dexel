//! dev-companion — companion desktop game binary.
//!
//! M1: static scene + HUD skeleton. Spawns the Developer entity, a
//! placeholder desk, and a HUD (progress bar, coin/xp/level text, mood
//! label) all with hardcoded values. No systems update anything yet — this
//! milestone only proves the layout exists and renders.
//!
//! See docs/implementation-plan.md §3.2 (ECS design) and §4/M1.

use bevy::prelude::*;

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

fn main() {
    App::new()
        .add_plugins(DefaultPlugins)
        .add_systems(Startup, setup_scene)
        .run();
}

/// Spawns the static M1 scene: the Developer entity, a placeholder desk
/// made of colored `Node` rects, and a HUD bar at the bottom with all four
/// elements (progress bar, coins, xp/level, mood label) using hardcoded
/// values. Nothing here is updated by any system yet.
fn setup_scene(mut commands: Commands) {
    // A UI camera is required to render UI nodes.
    commands.spawn(Camera2d);

    spawn_desk(&mut commands);

    spawn_hud(&mut commands);
}

/// Spawns the Developer entity and its placeholder desk.
///
/// The Developer is a root entity carrying the `Developer` component; the
/// desk is a set of placeholder colored `Node` rects parented under it so
/// later milestones can move/hide the whole scene as one unit.
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
/// Contains all four M1 elements, all hardcoded:
/// - progress bar (a fill node marked `ProgressBarFill` at a fixed 0% width)
/// - coin count text
/// - xp / level text
/// - mood label (marked `MoodLabel`, hardcoded "Idle")
fn spawn_hud(commands: &mut Commands) {
    commands
        .spawn((
            Node {
                width: Val::Percent(100.0),
                height: px(64.0),
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
        });
}
