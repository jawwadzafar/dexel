//! dev-companion — companion desktop game binary.
//!
//! M1: static scene + HUD skeleton. M2: activity wiring — a
//! `FocusedWindowProvider` is fed by Bevy input event readers (forwarded in,
//! never read directly by a game system) and `activity_bridge_system` drains
//! it each frame into an `ActivityMeter` resource via `decay_and_accumulate`.
//! The M2 **temporary** `[debug]` raw-event counter was removed in M5 polish
//! (plan §4/M2/§4-M5): a raw count contradicts the anti-mashing principle if
//! a user saw it as the real metric.
//!
//! M3: first playable loop — `project_progress_system` /
//! `project_completion_system` / `xp_level_system` / `idle_detection_system` /
//! `mood_render_system` / `hud_render_system` make the HUD reflect real
//! resources: typing fills the progress bar, completed projects award
//! coins/xp from a static rotation list, the level rises at pure
//! `level_for_xp` thresholds, and idling past `IDLE_THRESHOLD` flips the mood
//! to `OnBreak` (see docs/implementation-plan.md §3.2/§4-M3).
//!
//! M5: v0.1 polish — the M2 debug counter is gone; a coin-threshold desk
//! upgrade (a plant prop) makes the world visibly change; an idle/break
//! personality line ("Maybe we should take a break?") appears when the mood
//! flips to `OnBreak` and clears when the developer returns to coding.
//! See docs/implementation-plan.md §3.1/§3.2 and §4/M2/M3/M5.

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
use serde::{Deserialize, Serialize};
use std::fs;
use std::path::PathBuf;
use std::time::Duration;

// ---------------------------------------------------------------------------
// Components / mood
// ---------------------------------------------------------------------------

/// The one developer character. Holds the character's current mood, which
/// M3's `idle_detection_system` drives from real activity (plan §3.2).
#[derive(Component)]
struct Developer {
    mood: Mood,
}

/// The developer's mood (plan §3.2). M3 drives all three variants:
/// `Idle` at launch, `Coding` on fresh activity, `OnBreak` after
/// [`IDLE_THRESHOLD`] seconds without input.
#[derive(Clone, Copy, PartialEq, Eq, Debug, Default)]
enum Mood {
    #[default]
    Idle,
    Coding,
    OnBreak,
}

impl Mood {
    /// The display name rendered by `mood_render_system`.
    fn label(self) -> &'static str {
        match self {
            Mood::Idle => "Idle",
            Mood::Coding => "Coding",
            Mood::OnBreak => "OnBreak",
        }
    }

    /// The mood-label color: gray idling, green coding, warm orange break.
    fn color(self) -> Color {
        match self {
            Mood::Idle => Color::srgb(0.45, 0.75, 0.95),
            Mood::Coding => Color::srgb(0.35, 0.85, 0.50),
            Mood::OnBreak => Color::srgb(0.95, 0.62, 0.35),
        }
    }
}

/// Marker on the HUD text that mirrors `Developer.mood` (plan §3.2).
#[derive(Component)]
struct MoodLabel;

/// Marker on the progress-bar fill node, whose width mirrors the current
/// project's completion (plan §3.2).
#[derive(Component)]
struct ProgressBarFill;

/// Marker on the HUD text showing the coin count (updated by
/// `hud_render_system` in M3).
#[derive(Component)]
struct CoinCount;

/// Marker on the HUD text showing `Lv N · X XP` (updated by
/// `hud_render_system` in M3).
#[derive(Component)]
struct XpLevel;

/// Marker on the HUD text showing the current project name (updated by
/// `hud_render_system` in M3).
#[derive(Component)]
struct ProjectName;

/// Marker on the placeholder **desk-upgrade prop** the M5 coin-threshold
/// upgrade spawns (plan §4/M5: "a coin threshold unlocks a second placeholder
/// prop"). `desk_upgrade_system` inserts/removes this component (rather than
/// despawning/re-spawning the entity) so the entity's identity is stable and
/// the prop simply appears/disappears as the wallet crosses the threshold.
#[derive(Component)]
struct DeskUpgradeProp;

/// Marker on the HUD **mood line** text (plan §4/M5 "one or two idle/mood
/// text lines … for personality, no dialogue system"). `mood_render_system`
/// writes the mood word to `MoodLabel` and the personality line (only on
/// `OnBreak`, else empty) to this node.
#[derive(Component)]
struct MoodLine;

// ---------------------------------------------------------------------------
// Resources
// ---------------------------------------------------------------------------

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
/// `idle_timer` is consumed by M3: `activity_bridge_system` resets it on
/// fresh activity (see [`non_focus_event`]), and `idle_detection_system`
/// reads its completion to flip the developer's mood to `OnBreak` after
/// [`IDLE_THRESHOLD`] seconds.
#[derive(Resource)]
struct ActivityMeter {
    recent_rate: f32,
    idle_timer: Timer,
}

impl Default for ActivityMeter {
    fn default() -> Self {
        Self {
            recent_rate: 0.0,
            idle_timer: Timer::from_seconds(IDLE_THRESHOLD, TimerMode::Repeating),
        }
    }
}

/// How long without any activity before the mood flips from
/// `Coding`/`Idle` to `OnBreak` (plan §3.2: "60s, make it a named const").
/// Named const so the M3 integration test and the milestone log can share
/// the same tuning.
pub const IDLE_THRESHOLD: f32 = 60.0;

/// How often the autosave fires (plan §3.4: "Write on a `SaveTimer` (e.g.
/// every 30s)"). Named const so the M4 test and the milestone log share the
/// same tuning.
pub const SAVE_INTERVAL: f32 = 30.0;

/// The wallet value at which the desk plant upgrade unlocks (plan §4/M5:
/// "a coin threshold unlocks a second placeholder prop"). Named const so the
/// upgrade system and its unit test share the same tuning.
///
/// Tuning: the first two projects award 25 and 40 coins (65 total), so the
/// plant appears partway through the *third* project ("Add CI cache") on a
/// fresh run — the "world visibly changes" hook lands a couple of minutes in,
/// not immediately, and not before the player has seen the base desk.
pub const DESK_UPGRADE_COST: u64 = 50;

/// The developer's coin purse (plan §3.2). Awarded on project completion by
/// `xp_level_system`.
#[derive(Resource, Debug, Default, Clone, Copy, PartialEq, Eq)]
struct Wallet(u64);

/// The developer's experience and level (plan §3.2). Level is always
/// `level_for_xp(xp)`, maintained by `xp_level_system`.
#[derive(Resource, Debug, Clone, Copy, PartialEq, Eq)]
struct PlayerXp {
    level: u32,
    xp: u32,
}

impl Default for PlayerXp {
    fn default() -> Self {
        Self { level: 1, xp: 0 }
    }
}

/// The project currently being worked on (plan §3.2).
///
/// `work_done` is a plain `f32` accumulator; `total_work > 0.0` always
/// holds (see [`project_at`]), so the completion rule in
/// `project_completion_system` is well-defined. The overshoot past
/// `total_work` is carried into the next project by that system, so
/// `work_done` can temporarily exceed `total_work` within one frame
/// (the HUD clamps the bar width to 100 % in that case).
#[derive(Resource, Debug, Clone)]
struct CurrentProject {
    name: String,
    total_work: f32,
    work_done: f32,
    reward_coins: u64,
    reward_xp: u32,
}

/// The static rotation of projects the companion rolls through (plan
/// §4/M3: "static list of 3-5 hardcoded projects to roll through … no
/// content generation"). Index-rolls after every completion so the same
/// project never immediately reappears.
/// The project to load from the static list at `index` — a plain `fn` (no
/// Bevy, no `String` allocation in const context) so the const list stays
/// buildable and the M3 rotation test can call it directly. Panics on an
/// out-of-range index, which cannot happen through
/// `next_project_index` (always `% len`).
fn project_at(index: usize) -> CurrentProject {
    match index {
        0 => CurrentProject {
            name: "Fix login flow".to_string(),
            total_work: 50.0,
            work_done: 0.0,
            reward_coins: 25,
            reward_xp: 40,
        },
        1 => CurrentProject {
            name: "Refactor config loader".to_string(),
            total_work: 75.0,
            work_done: 0.0,
            reward_coins: 40,
            reward_xp: 60,
        },
        2 => CurrentProject {
            name: "Add CI cache".to_string(),
            total_work: 100.0,
            work_done: 0.0,
            reward_coins: 60,
            reward_xp: 90,
        },
        3 => CurrentProject {
            name: "Write API docs".to_string(),
            total_work: 130.0,
            work_done: 0.0,
            reward_coins: 80,
            reward_xp: 120,
        },
        _ => panic!("project_at: index {index} out of range 0..4"),
    }
}

/// How many projects are in the static rotation list (plan §4/M3: a
/// hardcoded list of 3-5; this is 4).
const PROJECT_LIST_LEN: usize = 4;

// ---------------------------------------------------------------------------
// Events (plan §3.2 — Bevy 0.19 `#[derive(Message)]`, the 0.19 rename of
// `#[derive(Event)]`)
// ---------------------------------------------------------------------------

/// Fired by `project_completion_system` when `work_done >= total_work`;
/// consumed by `xp_level_system` to award coins/xp and (possibly) level up
/// (plan §3.2).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Message)]
struct ProjectCompleted {
    coins: u64,
    xp: u32,
}

/// Fired by `xp_level_system` when `level_for_xp` returns a level strictly
/// above the one stored in `PlayerXp` (plan §3.2).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Message)]
struct LevelUp {
    new_level: u32,
}

// ---------------------------------------------------------------------------
// `*` functions — plain `fn`, no Bevy types (plan §3.2 rule), unit-tested
// directly in the `#[cfg(test)] mod tests` at the bottom of this file.
// ---------------------------------------------------------------------------

/// How much work `recent_rate` should add to the current project this
/// `fixed_dt` (in seconds) (plan §3.2, `project_progress_system`'s
/// `*progress_delta`):
///
/// ```text
/// progress_delta(rate, fixed_dt) = rate * MIN_WORK_PER_EVENT * fixed_dt
/// ```
///
/// with `MIN_WORK_PER_EVENT = 0.05` (tuning const).
///
/// **Anti-mashing rule** (plan §1, made concrete and testable): the input
/// `rate` is a *decaying* events/second average that the `activity` crate
/// already clamps to `activity::MAX_RECENT_RATE` (120 ev/s) in
/// `decay_and_accumulate`. Because this function is **linear** in `rate`
/// (work is added per wall-clock second at a fixed rate proportional to
/// events/s, not per event), a sustained max-rate mash converges to
/// `MAX_RECENT_RATE * MIN_WORK_PER_EVENT` work/s — the same throughput as
/// a steady real-typing session at 120 ev/s would produce within one
/// session (M2's `decay_and_accumulate_sustained_max_rate_mash_does_not_
/// runaway` test proves the rate itself converges; this function's linearity
/// is what carries that bound through to work). Per-frame clamping inside
/// `decay_and_accumulate` plus this linear mapping means no input pattern
/// can produce more work/second than the ceiling. `fixed_dt` is a plain
/// `f32` (seconds) rather than a `Duration` so the unit test can pass a
/// fractional step without a unit-conversion helper.
#[must_use]
pub fn progress_delta(recent_rate: f32, fixed_dt: f32) -> f32 {
    recent_rate * MIN_WORK_PER_EVENT * fixed_dt
}

/// Work added per input event per second. Tuning const shared by
/// `progress_delta` and its unit tests. `MIN` (not `MAX`): it is the
/// constant coefficient — the bound on total work/s comes from
/// `activity::MAX_RECENT_RATE`, not this value.
///
/// At the 120 ev/s anti-mash ceiling this gives 6.0 work/s — project #1
/// (`total_work = 50`) completes in ≈ 8.3 s of continuous typing at the
/// ceiling, which is a readable demo pace for M3's exit criterion without
/// making the first project feel trivially short.
pub const MIN_WORK_PER_EVENT: f32 = 0.05;

/// The level the developer is at for `xp` total experience (plan §3.2,
/// `xp_level_system`'s `*level_for_xp`). Pure and unit-testable with the
/// **classic quadratic threshold** (each level n requires
/// `50·(n−1)·n` cumulative XP, a well-known progression curve):
///
/// ```text
/// level 1:  0   XP     (the floor — `xp = 0` ⇒ level 1, never 0)
/// level 2: 100  XP
/// level 3: 300  XP
/// level 4: 600  XP
/// level 5: 1000 XP
/// level n: 50·(n−1)·n  XP   for n ≥ 1
/// ```
///
/// The cumulative threshold is a quadratic in `n`, so the largest `n`
/// with `50·(n−1)·n ≤ xp` is
///
/// ```text
/// n  =  floor(  (1 + sqrt(1 + 4·(xp/50)))  /  2  )
/// ```
///
/// which is well-defined (monotone non-decreasing in `xp`) and
/// `u32`-safe: at `xp = u32::MAX` the discriminant is ~9.4e13 and the
/// result is ~3.4e6, well below `u32::MAX`.
///
/// The 50·(n−1)·n curve was chosen so a session of M3's play pace —
/// ~29 ev/s ≈ 1.45 work/s, projects 50–130 work each — produces enough
/// XP per session to cross at least one level boundary (the first
/// project alone grants 40 XP; two projects grant 100 XP, which is
/// exactly the level-2 threshold), while the threshold gap widens
/// quadratically so later levels are earned less frequently.
#[must_use]
pub fn level_for_xp(xp: u32) -> u32 {
    // Solve 50(n−1)n ≤ xp for the largest integer n ≥ 1.
    //
    // 50n² − 50n − xp ≤ 0  ⇒  n ≤ (1 + sqrt(1 + 4·(xp/50))) / 2
    //
    // Compute `xp / 50` as f64 (exact for all u32 inputs since both are
    // integers representable in f64 exactly up to 2^53) and solve the
    // quadratic in f64 (discriminant up to ~9.4e13 for u32::MAX, well
    // within f64's 15-16 significant-digit range).
    let k = 50.0f64; // XP multiplier per (n−1)·n
    let xp_f = f64::from(xp);
    let disc = 1.0 + 4.0 * (xp_f / k);
    let n = (1.0 + disc.sqrt()) / 2.0;
    (n as u32).max(1)
}

/// The project to roll to after the one currently at `old_index` completed
/// (plan §4/M3: "rolls the next project from the static list").
///
/// Pure (no Bevy, no index-panic surface) so a unit test can check the
/// rotation without constructing an `App` or a `Res<CurrentProject>`:
/// `(old_index + 1) % len`, wrapped for an empty list.
#[must_use]
pub fn next_project_index(old_index: usize, len: usize) -> usize {
    if len == 0 { 0 } else { (old_index + 1) % len }
}

// ---------------------------------------------------------------------------
// Systems — input forwarding (M2, unchanged)
// ---------------------------------------------------------------------------

/// Spawns the scene (Developer entity, desk, HUD) — M1 layout, M3 adds the
/// `CoinCount` / `XpLevel` / `ProjectName` markers on the three text nodes
/// the real `hud_render_system` writes, and inserts every M3 resource /
/// event (see `main()`).
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

// ---------------------------------------------------------------------------
// Systems — M2 activity bridge (unchanged except `has_activity` is read by
// M3's `idle_detection_system`)
// ---------------------------------------------------------------------------

/// Drains the provider once per frame, updates `ActivityMeter.recent_rate`
/// via `decay_and_accumulate`, and handles the idle timer (plan §3.2 /
/// §4/M2): fresh activity **resets** `idle_timer` (the idle clock starts
/// over); no activity merely ticks the already-running timer, so
/// `idle_timer.finished()` in [`idle_detection_system`] fires exactly
/// [`IDLE_THRESHOLD`] seconds after the *last* activity.
///
/// The M2 session event counter is no longer accumulated here — it drove
/// only the temporary `[debug]` HUD counter, which M5 removed (plan
/// §4/M5).
fn activity_bridge_system(
    mut source: ResMut<ActivitySource>,
    mut meter: ResMut<ActivityMeter>,
    times: Res<Time>,
) {
    let dt = times.delta();
    let events = source.0.poll();
    meter.recent_rate = decay_and_accumulate(meter.recent_rate, &events, dt);
    let fresh = non_focus_event(&events);
    if fresh {
        meter.idle_timer.reset();
    } else {
        meter.idle_timer.tick(dt);
    }
}

// ---------------------------------------------------------------------------
// Systems — M3
// ---------------------------------------------------------------------------

/// true if any non-focus event arrived on this frame — a *filter* so a
/// lone focus flip (a common OS artifact with no keyboard/mouse) does not
/// count as "fresh activity" for the idle mood. Used by
/// `activity_bridge_system` (timer reset) and (transitively) by
/// [`idle_detection_system`].
#[must_use]
fn non_focus_event(events: &[ActivityEvent]) -> bool {
    events
        .iter()
        .any(|e| !matches!(e, ActivityEvent::FocusChanged(_)))
}

/// `Developer.mood` ↔ idle (plan §3.2):
///
/// * `idle_timer.finished()` (i.e. it has been [`IDLE_THRESHOLD`] seconds
///   since the bridge last reset it on fresh activity) sets mood to
///   `OnBreak`;
/// * fresh activity this frame (the bridge reset the timer — detectable
///   because the finished/elapsed state collapsed back to non-finished)
///   sets mood back to `Coding` **immediately**, not one tick later.
///
/// Chained after `activity_bridge_system` (see `main()`) so the timer
/// state this system reads is from *this* frame.
fn idle_detection_system(meter: Res<ActivityMeter>, mut developer: Query<&mut Developer>) {
    let Ok(mut dev) = developer.single_mut() else {
        return;
    };
    // Fresh-activity path: the bridge's `reset()` left the timer at
    // elapsed 0 / not-finished, which (given the last observed state) is
    // itself the "activity just happened" signal — the previous frame
    // either ticked it (elapsed >= frame dt, never < 1 ms for realistic
    // frame rates) or had it finished, so a non-finished near-zero state
    // means a reset happened *this* frame.
    let fresh_activity =
        !meter.idle_timer.is_finished() && meter.idle_timer.elapsed() < Duration::from_millis(1);
    if fresh_activity && dev.mood != Mood::Coding {
        dev.mood = Mood::Coding;
    } else if !fresh_activity && meter.idle_timer.is_finished() && dev.mood != Mood::OnBreak {
        dev.mood = Mood::OnBreak;
    }
}

/// Advances `CurrentProject.work_done` this fixed step while the developer
/// is `Coding` and a project exists (plan §3.2, `FixedUpdate` schedule):
/// `work_done += progress_delta(recent_rate, fixed_dt)`.
///
/// Reads the fixed step's timestep from `Time<Fixed>` (each `FixedUpdate`
/// run is exactly one timestep — the `run_fixed_main_schedule` loop that
/// drives `FixedUpdate` advances `Time<Fixed>` by one `timestep()` per pass
/// before running, and discards leftover overstep on exit — so
/// `timestep().as_secs_f32()` is the exact `fixed_dt` for this tick and
/// total work per second equals `progress_delta(recent_rate, 1.0)`
/// regardless of frame rate).
fn project_progress_system(
    mut project: ResMut<CurrentProject>,
    meter: Res<ActivityMeter>,
    developer: Query<&Developer>,
    fixed_time: Res<Time<Fixed>>,
) {
    let Ok(dev) = developer.single() else {
        return;
    };
    if dev.mood != Mood::Coding {
        return;
    }
    let fixed_dt = fixed_time.timestep().as_secs_f32();
    if fixed_dt == 0.0 {
        return; // nothing elapsed this fixed tick; do not add 0.0
    }
    let delta = progress_delta(meter.recent_rate, fixed_dt);
    project.work_done += delta;
    // `work_done` is carried across completions by
    // `project_completion_system` on the same frame (Update runs after
    // FixedUpdate) — we do not clamp here so a mid-tick overshoot is not
    // lost: the roll carries it into the next project.
}

/// When `work_done >= total_work`: fire `ProjectCompleted` and roll the
/// next project from the static list (plan §3.2). The overshoot past
/// `total_work` is *carried* into the next project's `work_done` — a
/// single big frame that finishes a 50-work project by 3 work units starts
/// the 75-work next project at 3, not 0, so no work is silently dropped
/// at the boundary.
fn project_completion_system(
    mut project: ResMut<CurrentProject>,
    mut next_index: ResMut<NextProjectIndex>,
    mut completed_writer: MessageWriter<ProjectCompleted>,
) {
    if project.work_done < project.total_work {
        return;
    }
    let carry = project.work_done - project.total_work;
    let rewards = (project.reward_coins, project.reward_xp);
    let finished_name = project.name.clone();
    completed_writer.write(ProjectCompleted {
        coins: rewards.0,
        xp: rewards.1,
    });
    // Roll the next project (pure function — unit-tested directly).
    let next = next_project_index(next_index.0, PROJECT_LIST_LEN);
    let mut rolled = project_at(next);
    // The carried work belongs to the *next* project, and cannot overshoot
    // its total in practice (carry < total_work of the just-finished
    // project; the static list's totals are ordered ascending) — clamp
    // defensively so the invariant "work_done <= total_work" holds after
    // the roll no matter how a big frame oversteps.
    rolled.work_done = carry.min(rolled.total_work);
    next_index.0 = next;
    *project = rolled;
    info!(
        "project '{}' complete — awarded {} coins, {} XP; started '{}'",
        finished_name, rewards.0, rewards.1, project.name
    );
}

/// On `ProjectCompleted`: add coins to `Wallet` and xp to `PlayerXp`;
/// recompute the level with `level_for_xp` and fire `LevelUp` if it
/// increased (plan §3.2).
fn xp_level_system(
    mut project_completed: MessageReader<ProjectCompleted>,
    mut level_up_writer: MessageWriter<LevelUp>,
    mut wallet: ResMut<Wallet>,
    mut xp: ResMut<PlayerXp>,
) {
    for event in project_completed.read() {
        wallet.0 += event.coins;
        xp.xp = xp.xp.saturating_add(event.xp);
        let new_level = level_for_xp(xp.xp);
        if new_level > xp.level {
            info!("level up: {} → {}", xp.level, new_level);
            level_up_writer.write(LevelUp { new_level });
        }
        xp.level = new_level;
    }
}

/// The personality line shown for a mood (plan §4/M5: "one or two idle/mood
/// text lines (e.g. 'Maybe we should take a break?' on entering OnBreak) for
/// personality, no dialogue system"). `None` = no line (the node renders an
/// empty string).
fn mood_line(mood: Mood) -> Option<&'static str> {
    match mood {
        Mood::OnBreak => Some("Maybe we should take a break?"),
        // `Coding` / `Idle` get no line — the mood label itself already
        // states them, and a per-frame line there would be noise.
        Mood::Idle | Mood::Coding => None,
    }
}

/// `Developer.mood` → `MoodLabel` text + color (plan §3.2), plus the
/// personality line on the `MoodLine` node (plan §4/M5) — set on `OnBreak`,
/// cleared for the other moods.
///
/// The two `&mut Text` queries (the mood label and the mood line) are merged
/// into one `ParamSet` because Bevy 0.19's query-state validator does not
/// track `With<_>` disjointness *across separate params* — two
/// `Query<&mut Text, With<_>>` params panic with B0001 at runtime even though
/// the `MoodLabel` and `MoodLine` markers are on disjoint entities (the same
/// fix M4 applied to `hud_render_system`'s three HUD text queries). A
/// `ParamSet` proves disjointness within itself.
#[allow(
    clippy::type_complexity,
    reason = "ParamSet of 2 disjoint mood text queries — factoring into type aliases loses the elided lifetimes the ParamSet impl needs"
)]
fn mood_render_system(
    developer: Query<&Developer>,
    mut texts: ParamSet<(
        Query<(&mut Text, &mut TextColor), With<MoodLabel>>,
        Query<&mut Text, With<MoodLine>>,
    )>,
) {
    let Ok(dev) = developer.single() else {
        return;
    };
    for (mut text, mut color) in &mut texts.p0() {
        *text = Text::new(format!("Mood: {}", dev.mood.label()));
        *color = TextColor(dev.mood.color());
    }
    for mut line in &mut texts.p1() {
        *line = Text::new(mood_line(dev.mood).unwrap_or_default());
    }
}

/// Real `hud_render_system` (plan §3.2) — replaces M1's hardcoded
/// progress-bar width / coin / xp-level text with values read from
/// resources each frame:
///
/// * `ProgressBarFill` width = `work_done / total_work * 100 %` (clamped
///   to `0..=100` for the render node);
/// * `Coins: N` from `Wallet`;
/// * `Lv n · x XP` from `PlayerXp`;
/// * project name from `CurrentProject` (shows which static-list project is
///   in progress).
///
/// The M2 `[debug]` row was removed in M5 (plan §4/M5); the three HUD text
/// queries in [`hud_render_system`], merged into one
/// `ParamSet` so Bevy 0.19's query-state validation can prove they're
/// disjoint (three separate `Query<&mut Text, With<_>>` params trigger a
/// B0001 panic at runtime — the validator doesn't track `With<_>`
/// disjointness across separate params, only within a single `ParamSet`).
/// (Bevy 0.19's `ParamSet` is a tuple alias, not a derive macro — the
/// plan's §3.2 era used the older `#[derive(ParamSet)]` API.)
#[allow(
    clippy::type_complexity,
    reason = "ParamSet of 3 disjoint HUD text queries — factoring into type aliases loses the elided lifetimes the ParamSet impl needs"
)]
fn hud_render_system(
    project: Res<CurrentProject>,
    wallet: Res<Wallet>,
    xp: Res<PlayerXp>,
    mut fill: Query<&mut Node, With<ProgressBarFill>>,
    mut texts: ParamSet<(
        Query<&mut Text, With<CoinCount>>,
        Query<&mut Text, With<XpLevel>>,
        Query<&mut Text, With<ProjectName>>,
    )>,
) {
    let pct = (project.work_done / project.total_work * 100.0).clamp(0.0, 100.0);
    for mut node in &mut fill {
        node.width = Val::Percent(pct);
    }
    for mut text in &mut texts.p0() {
        *text = Text::new(format!("Coins: {}", wallet.0));
    }
    for mut text in &mut texts.p1() {
        *text = Text::new(format!("Lv {} · {} XP", xp.level, xp.xp));
    }
    for mut text in &mut texts.p2() {
        *text = Text::new(format!("Project: {}", project.name));
    }
}

/// The M5 desk upgrade (plan §4/M5: "a coin threshold unlocks a second
/// placeholder prop … proves the 'world visibly changes' hook without real
/// art").
///
/// While `Wallet >= [`DESK_UPGRADE_COST`]` the placeholder plant prop on the
/// desk carries `Visibility::Visible` and is rendered; below the threshold
/// it carries `Visibility::Hidden` and Bevy's render pipeline skips it (the
/// "hidden" state). The prop entity is spawned once by [`spawn_desk`] and
/// kept alive; this system only swaps the `Visibility` *component*
/// (visible ↔ hidden), so the entity's identity is stable and there is no
/// spawn/despawn churn.
///
/// (Bevy 0.19 note: `Node` — the `bevy_ui` layout component — has no
/// `visibility` field, and `EntityCommands` has no `despawn_component`;
/// toggling the `Visibility` *component* via `Command` is the clean,
/// chainable way to show/hide. Visibility is a standard Bevy concept the
/// plan §3.3 already relies on for the placeholder `Node` UI, so this adds
/// no new architecture.)
///
/// The upgrade is **non-persistent**: it is derived from the persisted
/// wallet every frame, so a relaunch whose wallet already meets the
/// threshold re-unlocks it automatically. This avoids extending the plan
/// §3.4 `SaveData` shape (the plan's M5 text offers "keep it non-persistent
/// if simpler") — see the M5 milestone-log entry.
///
/// Logs one `info!` per unlock (a `Local<bool>` edge trigger, the same
/// pattern M2's removed counter log used with `Local<usize>`) so a headless
/// smoke run leaves an audit trail — the window's pixels aren't visible from
/// an agent session. A wallet that already meets the threshold at startup
/// (a restored save) unlocks silently: the plant is simply there, which is
/// the correct visible behavior for a returning player.
fn desk_upgrade_system(
    mut commands: Commands,
    wallet: Res<Wallet>,
    mut prop: Query<Entity, With<DeskUpgradeProp>>,
    mut ever_unlocked: Local<bool>,
) {
    let unlocked = wallet.0 >= DESK_UPGRADE_COST;
    for entity in &mut prop {
        // The prop entity is always spawned (by `setup_scene`); this query
        // is empty only in a test fixture that didn't build the desk. Swap
        // the `Visibility` component to show/hide it. (Commands are applied
        // at frame end, so the change takes effect on the *next* frame —
        // deterministic, and exactly what the app-driven test asserts.)
        commands
            .entity(entity)
            .remove::<Visibility>()
            .insert(if unlocked {
                Visibility::Visible
            } else {
                Visibility::Hidden
            });
    }
    if unlocked && !*ever_unlocked {
        *ever_unlocked = true;
        info!(
            "desk upgrade unlocked: the desk plant appeared (wallet {} ≥ {} coins)",
            wallet.0, DESK_UPGRADE_COST
        );
    }
    if !unlocked && *ever_unlocked {
        *ever_unlocked = false;
    }
}

// ---------------------------------------------------------------------------
// Scene spawners (M1 layout, M3 markers added)
// ---------------------------------------------------------------------------

/// Spawns the Developer entity and its placeholder desk (unchanged from M1).
fn spawn_desk(commands: &mut Commands) {
    commands
        .spawn((
            // The developer character, mood starts Idle (M3's
            // idle_detection_system drives it after that).
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
                    // The M5 desk-upgrade prop: a placeholder "plant" that is
                    // hidden until the wallet crosses [`DESK_UPGRADE_COST`]
                    // (plan §4/M5 "a coin threshold unlocks a second
                    // placeholder prop"). Marked `DeskUpgradeProp` so
                    // `desk_upgrade_system` can toggle its `Visibility`
                    // component; it spawns `Visibility::Hidden` (the "hidden"
                    // state — Bevy's render pipeline skips hidden entities,
                    // so it draws nothing).
                    desk.spawn((
                        Node {
                            width: px(60.0),
                            height: px(80.0),
                            ..default()
                        },
                        BackgroundColor(Color::srgb(0.25, 0.55, 0.30)),
                        DeskUpgradeProp,
                        Visibility::Hidden,
                    ));
                });
        });
}

/// Spawns the HUD bar pinned to the bottom of the window.
///
/// M1's four elements (progress bar, coins, xp/level, mood) plus M3's
/// `CoinCount` / `XpLevel` / `ProjectName` components on the three text
/// nodes the real `hud_render_system` updates, and M5's `MoodLine` component
/// on a dedicated mood-personality row (plan §4/M5). The temporary M2
/// `[debug]` counter row was removed in M5 (plan §4/M5), so the bar shrank
/// back from 104 px to 88 px.
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
            // --- Progress bar (hardcoded 0% for M1; `hud_render_system`
            //     drives it from `CurrentProject` in M3) ---
            hud.spawn((
                Node {
                    width: Val::Percent(100.0),
                    height: px(14.0),
                    ..default()
                },
                BackgroundColor(Color::srgb(0.20, 0.22, 0.26)),
            ))
            .with_children(|bar| {
                // Starts at 0% width (M1); `hud_render_system` (M3) updates
                // it from the project's `work_done / total_work` each frame.
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

            // --- Metrics row: coins, xp/level, mood (markers added in M3) ---
            hud.spawn((
                Node {
                    width: Val::Percent(100.0),
                    justify_content: JustifyContent::SpaceBetween,
                    ..default()
                },
                BackgroundColor(Color::NONE),
            ))
            .with_children(|row| {
                // Coin count (hardcoded 0 for M1; `CoinCount` — M3).
                row.spawn((
                    Text::new("Coins: 0"),
                    TextFont::from_font_size(px(14.0)),
                    TextColor(Color::srgb(0.95, 0.80, 0.30)),
                    CoinCount,
                ));
                // Xp / level (hardcoded Lv 1, 0 XP for M1; `XpLevel` — M3).
                row.spawn((
                    Text::new("Lv 1 · 0 XP"),
                    TextFont::from_font_size(px(14.0)),
                    TextColor(Color::srgb(0.9, 0.9, 0.9)),
                    XpLevel,
                ));
                // Mood label. Marked `MoodLabel` so `mood_render_system`
                // (M3) can update exactly this text; hardcoded "Idle" for
                // M1.
                row.spawn((
                    Text::new("Mood: Idle"),
                    TextFont::from_font_size(px(14.0)),
                    TextColor(Color::srgb(0.45, 0.75, 0.95)),
                    MoodLabel,
                ));
            });

            // --- Project name row (M3: shows which static-list project is
            //     in progress, updated by the real `hud_render_system`) ---
            hud.spawn((
                Text::new("Project: Fix login flow"),
                TextFont::from_font_size(px(14.0)),
                TextColor(Color::srgb(0.75, 0.85, 0.95)),
                ProjectName,
            ));

            // --- Mood line row (M5: a personality line on OnBreak —
            //     "Maybe we should take a break?" — cleared otherwise;
            //     updated by `mood_render_system`, plan §4/M5) ---
            hud.spawn((
                Text::new(""),
                TextFont::from_font_size(px(12.0)),
                TextColor(Color::srgb(0.70, 0.65, 0.50)),
                MoodLine,
            ));
        });
}

// ---------------------------------------------------------------------------
// App wiring
// ---------------------------------------------------------------------------

/// The index into `DEFAULT_PROJECTS` of the currently-loaded project, so
/// `project_completion_system` can roll to the *next* one (plan §4/M3
/// "rolls the next project from the static list").
#[derive(Resource, Debug, Default)]
struct NextProjectIndex(usize);

// ---------------------------------------------------------------------------
// M4 — persistence (plan §3.4)
// ---------------------------------------------------------------------------

/// The developer's coins, experience, and the in-progress project at the
/// moment of saving — the shape `save_system` serializes and
/// `load_or_init_save` restores (plan §3.4).
///
/// `serde` field names are lowercase (matching the plan's literal struct
/// definition) so the on-disk JSON is exactly what the plan specifies:
/// `{"wallet": …, "xp": …, "level": …, "current_project": …}`.
///
/// Privacy invariant (plan §1/§3.1): the save contains **no user content** —
/// project names come from the hardcoded static list (never from input), and
/// only the project *index* and work counters are persisted.
#[derive(Serialize, Deserialize, Debug, Clone, PartialEq)]
struct SaveData {
    /// The developer's wallet (mirrors the `Wallet` resource).
    wallet: u64,
    /// Cumulative experience (mirrors `PlayerXp.xp`).
    xp: u32,
    /// Derived level (mirrors `PlayerXp.level`; `load_or_init_save`
    /// recomputes it with `level_for_xp` on restore, so a hand-edited
    /// inconsistent file self-corrects).
    level: u32,
    /// The in-progress project, if any. `None` only ever happens on a fresh
    /// install (there is always a current project once the game has started;
    /// a malformed save is treated as fresh, see [`load_save_file`]).
    current_project: Option<CurrentProjectSave>,
}

/// The slice of a [`CurrentProject`] that survives a quit/relaunch
/// (plan §3.4: "current_project: Option<CurrentProjectSave>" — "partway
/// through a project" = *which* static-list project + how much work is done).
///
/// **Design choice (noted per plan boundaries):** the save stores the
/// static-list `index` rather than the project's `name`, because M3's
/// `CurrentProject` is always built by `project_at(index)` — the name and
/// the rewards (`total_work`, `reward_coins`, `reward_xp`) are therefore
/// recoverable without duplicating data that could drift from the list.
/// `work_done` is a plain `f32` so a mid-project quit resumes exactly where
/// the bar was.
#[derive(Serialize, Deserialize, Debug, Clone, PartialEq)]
struct CurrentProjectSave {
    /// Index into the static project list (0..=3); re-validated against
    /// `PROJECT_LIST_LEN` on load (an out-of-range value is clamped, never
    /// panicking — a corrupted save must not crash the app).
    index: usize,
    /// Work completed on this project so far.
    work_done: f32,
}

/// The save-file interval timer (plan §3.2's `SaveTimer(Timer)` resource,
/// §3.4's "autosave on a SaveTimer").
#[derive(Resource)]
struct SaveTimer(Timer);

impl SaveTimer {
    fn new() -> Self {
        Self(Timer::from_seconds(SAVE_INTERVAL, TimerMode::Repeating))
    }
}

/// The on-disk save location (plan §3.4, verbatim):
/// `dirs::data_dir().unwrap().join("dev-companion").join("save.json")`.
///
/// In tests this is overridden with [`set_save_path`] so nothing is ever
/// written to the real data dir; in the game it is the one path the save
/// systems use.
fn save_path() -> PathBuf {
    dirs::data_dir()
        .unwrap()
        .join("dev-companion")
        .join("save.json")
}

/// Test seam: redirect all save I/O to `path` (or back to the real data
/// dir with `None`). Only the `#[cfg(test)]` unit tests in this file call
/// this — the game code never does, so in a build with tests excluded the
/// function is dead code. Annotated the same way M1 handled the unused
/// `Mood` variants (the seam lives here so the tests can point save I/O at
/// a temp dir; specific one-line allow, not a blanket crate-level allow).
///
/// A **lazy process-global** `Mutex<Option<PathBuf>>` (deliberately *not*
/// a thread-local, and not a plain `static` — `Mutex::new` is not
/// const-constructible in Rust 2024): `cargo test` runs each test on its
/// own *spawned* thread, so a thread-local override set on the main thread
/// would be invisible to the test body (the first M4 run hit exactly that).
/// The four path-sensitive tests additionally serialize on
/// [`save_path_guard`], so the override is never contended.
#[allow(
    dead_code,
    reason = "test seam — only the #[cfg(test)] module below calls this"
)]
fn set_save_path(path: Option<PathBuf>) {
    *SAVE_PATH_OVERRIDE
        .lock()
        .expect("save-path override mutex poisoned") = path;
}

use std::sync::LazyLock;

static SAVE_PATH_OVERRIDE: LazyLock<std::sync::Mutex<Option<PathBuf>>> =
    LazyLock::new(|| std::sync::Mutex::new(None));

/// The path save I/O actually uses (override if set, else the real data
/// dir). A `const fn` can't call `dirs`, so this is a plain `fn` that every
/// save call site goes through.
fn effective_save_path() -> PathBuf {
    SAVE_PATH_OVERRIDE
        .lock()
        .expect("save-path override mutex poisoned")
        .clone()
        .unwrap_or_else(save_path)
}

/// Read + parse the save file at `path`, if present.
///
/// * No file → `Ok(None)` (fresh install; `load_or_init_save` will start
///   from defaults).
/// * Malformed JSON / wrong shape / unknown fields → `Err` (logged by the
///   caller, and treated as a fresh start — a corrupted save must not crash
///   the app or wipe a good wallet; the *old* file is left untouched, so a
///   later successful autosave is what overwrites it).
fn load_save_file(path: &std::path::Path) -> Result<Option<SaveData>, String> {
    let raw = match fs::read_to_string(path) {
        Ok(raw) => raw,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(e) => return Err(format!("could not read save file {path:?}: {e}")),
    };
    match serde_json::from_str::<SaveData>(&raw) {
        Ok(data) => Ok(Some(data)),
        Err(e) => Err(format!("save file {path:?} is malformed: {e}")),
    }
}

/// Serialize `data` to `path`, creating parent directories as needed.
///
/// A failed write is logged (by the caller) and returns `Err`; the game
/// keeps running on its in-memory state and the next autosave retry will
/// re-attempt the write.
fn write_save_file(path: &std::path::Path, data: &SaveData) -> Result<(), String> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)
            .map_err(|e| format!("could not create save dir {parent:?}: {e}"))?;
    }
    let json = serde_json::to_string_pretty(data)
        .map_err(|e| format!("could not serialize save data: {e}"))?;
    fs::write(path, json).map_err(|e| format!("could not write save file {path:?}: {e}"))
}

/// Extract a [`SaveData`] from the live game resources (plan §3.4, what
/// `save_system` serializes each interval).
fn save_data_from_resources(
    wallet: &Wallet,
    xp: &PlayerXp,
    project: &CurrentProject,
    index: &NextProjectIndex,
) -> SaveData {
    SaveData {
        wallet: wallet.0,
        xp: xp.xp,
        level: xp.level,
        current_project: Some(CurrentProjectSave {
            index: index.0,
            work_done: project.work_done,
        }),
    }
}

/// `load_or_init_save` (plan §3.2 Startup row, §4/M4): at startup read the
/// save file if present and populate the M3 resources from it; otherwise
/// leave the app-construction defaults in place (fresh install).
///
/// Runs in `Startup` **after** `setup_scene` (see `main()`'s
/// `.chain()`), so it overwrites whatever `project_at(0)` etc. inserted at
/// construction. On a successful load the wallet / xp / level / project are
/// restored, and `PlayerXp.level` is recomputed with `level_for_xp` so a
/// hand-edited (inconsistent) save self-corrects instead of showing a
/// wrong level.
fn load_or_init_save(
    mut wallet: ResMut<Wallet>,
    mut xp: ResMut<PlayerXp>,
    mut project: ResMut<CurrentProject>,
    mut index: ResMut<NextProjectIndex>,
) {
    let path = effective_save_path();
    match load_save_file(&path) {
        Ok(Some(data)) => {
            wallet.0 = data.wallet;
            xp.xp = data.xp;
            // Derive the level from the xp (the stored level is authoritative
            // only insofar as it equals level_for_xp(xp); recompute so an
            // edited file can't show a level the xp doesn't justify).
            xp.level = level_for_xp(data.xp);
            if let Some(cp) = data.current_project {
                // Clamp a corrupted index into range instead of panicking
                // (the save is user-visible file data; never trust it).
                let idx = cp.index.min(PROJECT_LIST_LEN - 1);
                let mut rolled = project_at(idx);
                rolled.work_done = cp.work_done.clamp(0.0, rolled.total_work);
                index.0 = idx;
                *project = rolled;
            }
            info!(
                "restored save: wallet {} · level {} · {} XP · project '{}' ({:.1}/{:.1} work)",
                wallet.0, xp.level, xp.xp, project.name, project.work_done, project.total_work
            );
        }
        Ok(None) => {
            info!(
                "no save at {path:?} — starting fresh (wallet {} · level {} · {} XP)",
                wallet.0, xp.level, xp.xp
            );
        }
        Err(e) => {
            // Corrupted/unreadable save: log it loudly and start fresh,
            // keeping the in-memory defaults. The file is left in place so
            // the next autosave is what overwrites it (no silent data loss
            // without a successful write first).
            warn!("{e} — starting with a fresh state (the file is left in place)");
        }
    }
}

/// `save_system` (plan §3.2 Update row, §3.4): when [`SaveTimer`] fires (every
/// [`SAVE_INTERVAL`] seconds) serialize the current resources to the save
/// file. Gated on `AppState::Playing` per the plan's table (the v0.1 app is
/// always `Playing` once `Startup` finishes, so the gate is a no-op in
/// practice — but it matches the plan's stated shape and stays correct if a
/// later state is added).
fn save_system(
    state: Res<State<AppState>>,
    mut save_timer: ResMut<SaveTimer>,
    times: Res<Time>,
    wallet: Res<Wallet>,
    xp: Res<PlayerXp>,
    project: Res<CurrentProject>,
    index: Res<NextProjectIndex>,
) {
    let dt = times.delta();
    save_timer.0.tick(dt);
    // `just_finished()` (not `is_finished()`): Bevy 0.19's `Timer::tick`
    // recomputes the `finished` flag *after* advancing the stopwatch, so a
    // repeating timer whose elapsed already meets its duration (e.g. a
    // large `dt` that lands elapsed ≥ duration) stays `finished = false`
    // until the next tick — with a variable (test-driven) `dt` the flag
    // could then be observed as never firing. `just_finished()` is true
    // exactly on the tick(s) that crossed the interval, which is the
    // "fire when the 30 s interval elapses" semantic the plan wants.
    if !save_timer.0.just_finished() || !(state.get() == &AppState::Playing) {
        return;
    }
    let data = save_data_from_resources(&wallet, &xp, &project, &index);
    let path = effective_save_path();
    match write_save_file(&path, &data) {
        Ok(()) => info!(
            "autosave: wrote {path:?} (wallet {} · level {} · {} XP · project '{}' {:.1}/{:.1})",
            wallet.0, xp.level, xp.xp, project.name, project.work_done, project.total_work
        ),
        Err(e) => warn!("autosave failed: {e} — will retry on the next interval"),
    }
}

/// `AppState` (plan §3.2): `Loading` until `load_or_init_save` finishes, then
/// `Playing`. The save system gates on `Playing`; no system currently
/// branches on state, so `Loading` is the startup-only default.
#[derive(States, Clone, Eq, PartialEq, Hash, Debug, Default)]
enum AppState {
    #[default]
    Loading,
    Playing,
}

/// Transitions `AppState → Playing` once (after `load_or_init_save` has
/// run, per the plan's Startup row: "… transitions `AppState → Playing`").
///
/// Bevy 0.19: state changes are *queued* through the `NextState<S>`
/// resource (the plan's `state.set(...)` example is 0.18 API). Normally
/// `StatesPlugin` inserts the `StateTransition` schedule into
/// `MainScheduleOrder` (after `PreUpdate`) and it applies on the next
/// frame — but that wiring is only consulted when the main schedule is
/// *built* (the first `app.update()`), and the M4 test fixtures call
/// `app.update()` (for `Startup`) *before* the plugin is installed, so
/// the transition would silently never apply and `save_system`'s
/// `Playing` gate would block forever. Running the `StateTransition`
/// schedule synchronously here is exactly what that schedule would do —
/// just earlier — so the transition applies during `Startup`, in both
/// the real app and the fixtures, with no `MainScheduleOrder`
/// dependency.
fn enter_playing(world: &mut World) {
    world
        .resource_mut::<NextState<AppState>>()
        .set(AppState::Playing);
    world.run_schedule(bevy::state::state::StateTransition);
}

/// Build and run the real game app (the exact app `main` constructs).
///
/// Exposed as a library entry point so the visual probe and any future
/// test harness can drive the real scene from outside the binary without
/// duplicating the plugin/resource/system wiring. The binary's `main` is
/// a one-line call into this function.
pub fn run() {
    App::new()
        .add_plugins(DefaultPlugins)
        // M2 activity resources — inserted during construction, so they
        // exist before any Update frame runs.
        .init_resource::<ActivitySource>()
        .init_resource::<ActivityMeter>()
        // M3 progression resources — same rule: present before the first
        // FixedUpdate/Update. `CurrentProject` starts at the first static
        // entry; `NextProjectIndex` mirrors it.
        .insert_resource(Wallet(0))
        .insert_resource(PlayerXp::default())
        .insert_resource(project_at(0))
        .init_resource::<NextProjectIndex>()
        // M4 persistence: the autosave interval timer (plan §3.2/§3.4).
        .insert_resource(SaveTimer::new())
        // M3 messages (Bevy 0.19: `add_message` per type — there is no
        // `add_messages` plural; `MessageReader` / `MessageWriter` need
        // each type registered).
        .add_message::<ProjectCompleted>()
        .add_message::<LevelUp>()
        // AppState (plan §3.2): Loading → Playing after the save is loaded.
        .init_state::<AppState>()
        .add_systems(
            Startup,
            // The save is loaded *after* the scene exists (setup_scene is a
            // no-op on resources, so order is safe either way — the chain is
            // explicit per the M2 bug notes), and Playing begins immediately
            // after the load so the first Update frame's save_system gate
            // passes.
            (setup_scene, load_or_init_save, enter_playing).chain(),
        )
        .add_systems(
            FixedUpdate,
            // Deterministic per-step work advance (plan §3.2). Runs on the
            // `Time<Fixed>` clock — `project_progress_system` reads its
            // delta so a frame's overstep does not drop work.
            project_progress_system,
        )
        .add_systems(
            Update,
            // All Update systems in ONE explicit chain; intra-chain order is
            // guaranteed so the data flow holds within a frame (Bevy 0.19
            // tuple systems run in unspecified order without `.chain()` —
            // see M2's bug notes). Order:
            //
            //   1. forward_focus_events    (input → provider, plan §3.1)
            //   2. forward_keyboard_events (input → provider)
            //   3. forward_mouse_events    (input → provider)
            //   4. activity_bridge_system  (drain → rate, reset/tick timer)
            //   5. idle_detection_system   (mood ↔ idle, M3)
            //   6. desk_upgrade_system     (M5: toggle the plant prop's
            //                               visibility at the wallet threshold)
            //   7. project_progress_system is FixedUpdate (below)
            //   8. project_completion_system (work_done ≥ total → award, roll)
            //   9. xp_level_system              (coins/xp in, level out)
            //  10. mood_render_system      (mood → label text/color + mood line)
            //  11. hud_render_system       (resources → bar/text, M3 real HUD)
            //  12. save_system (M4) — autosave every SAVE_INTERVAL seconds
            //      when AppState::Playing (plan §3.2/§3.4).
            //
            //   (The M2 `debug_counter_hud_system` sat here and was removed in
            //   M5 — plan §4/M5.)
            (
                // Forward Bevy input events into the provider — the ONLY
                // place Bevy input events are read; no game system reads
                // them directly (plan §3.1).
                forward_focus_events,
                forward_keyboard_events,
                forward_mouse_events,
                // Drain the provider, update the meter, reset/tick the
                // idle timer.
                activity_bridge_system,
                // M3: flip the mood to Coding on fresh activity, to
                // OnBreak after IDLE_THRESHOLD seconds of silence.
                idle_detection_system,
                // M5: the desk plant upgrade — show the placeholder prop
                // once the wallet crosses the coin threshold (plan §4/M5).
                // Toggles the prop's `Visibility` component via a `Command`
                // (applied at frame end → visible next frame). Reads only the
                // `Wallet` resource, which `xp_level_system` (later in the
                // chain) updates, so an upgrade earned this frame shows next
                // frame — the deterministic behavior the app-driven test
                // asserts.
                desk_upgrade_system,
                // M3: complete the project when the bar is full and roll
                // the next one from the static list (plan §3.2).
                // Runs after project_progress_system (FixedUpdate precedes
                // Update), so a frame that oversteps completes in the same
                // frame the work landed.
                project_completion_system,
                // M3: award the fired ProjectCompleted (coins/xp) and fire
                // LevelUp if level_for_xp crossed a threshold.
                xp_level_system,
                // M3: mood → label text + color (plan §3.2).
                mood_render_system,
                // M3: the real HUD — progress-bar fill width, coin /
                // xp-level / project-name text from resources (plan §3.2).
                hud_render_system,
                // M4: tick the SaveTimer and autosave when it fires, gated
                // on AppState::Playing (plan §3.2/§3.4).
                save_system,
            )
                .chain(),
        )
        .run();
}

/// Build the exact app [`main`] constructs (same plugins, resources,
/// messages, state, and system chains), then seed the progression state
/// before the first frame so a visual capture shows a populated HUD (wallet,
/// XP/level, and partial project progress) and the M5 desk-plant upgrade
/// (the wallet seed is expected to be ≥ [`DESK_UPGRADE_COST`]).
///
/// The save path is redirected to a temp dir so the probe never reads or
/// writes the user's real save file.

#[cfg(test)]
mod tests {
    use super::*;
    use activity::MAX_RECENT_RATE;
    use bevy_input::ButtonState;
    use bevy_input::keyboard::{Key, KeyCode, KeyboardInput};

    // ------------------------------------------------------------------
    // progress_delta — the anti-mashing clamp (plan §4/M3)
    // ------------------------------------------------------------------

    #[test]
    fn progress_delta_scales_linearly_with_rate_and_dt() {
        // At a typical real-typing rate (10 events/s) over 100 ms the work
        // added is 10 * 0.05 * 0.1 = 0.05 — small, and proportional to the
        // input (not clipped to a per-keystroke step that would let a mash
        // outrun steady typing).
        let dt = 0.1f32; // seconds
        assert!((progress_delta(1.0, dt) - MIN_WORK_PER_EVENT * 0.1).abs() < 1e-5);
        assert!((progress_delta(10.0, dt) - MIN_WORK_PER_EVENT * 1.0).abs() < 1e-5);
        assert!(progress_delta(10.0, dt) > progress_delta(1.0, dt));
    }

    #[test]
    fn progress_delta_is_zero_when_rate_is_zero_or_dt_is_zero() {
        // No activity → no work, regardless of dt; no time elapsed → no
        // work, regardless of rate. Both guard the "no free work" rule.
        assert_eq!(progress_delta(0.0, 1.0), 0.0);
        assert_eq!(progress_delta(MAX_RECENT_RATE, 0.0), 0.0);
        assert_eq!(progress_delta(0.0, 0.0), 0.0);
    }

    #[test]
    fn progress_delta_at_the_anti_mash_ceiling_bounded_work_per_session() {
        // The concrete anti-mashing rule, end-to-end: a *sustained*
        // max-rate mash (120 events/frame at 120 frames/s — the same input
        // pattern as M2's rate test) converges the rate to
        // MAX_RECENT_RATE; `progress_delta` is linear in rate, so the work
        // per simulated second is bounded by MAX_RECENT_RATE *
        // MIN_WORK_PER_EVENT = 6.0 work/s. Simulate 60 s of that mash
        // through BOTH functions and assert the total work is within a
        // small tolerance of that bound — i.e. no input pattern can make
        // the progress bar fill faster than a steady 120 ev/s session.
        let frame = Duration::from_micros(1_000_000u64 / 120);
        let frame_secs = frame.as_secs_f32();
        let mut rate = 0.0f32;
        let mut work = 0.0f32;
        for _ in 0..(120 * 60) {
            // One keystroke per frame (120 events/s input, same shape as
            // M2's runaway test).
            let events = [ActivityEvent::Keystroke];
            rate = decay_and_accumulate(rate, &events, frame);
            work += progress_delta(rate, frame_secs);
        }
        // Work over 60 s of sustained max-rate input must be finite and
        // close to the bound: 6.0 work/s * 60 s = 360 work total. The rate
        // takes ~30 s (≈ 44 half-lives of 0.69 s … ~14 half-lives to 6%)
        // to settle, so the early work is *less* than the bound; assert
        // finite, non-negative, and not above the bound (the "no runaway"
        // direction — a runaway would be hundreds of work/s).
        assert!(work.is_finite(), "work ran away: {work}");
        let bound_per_session = MAX_RECENT_RATE * MIN_WORK_PER_EVENT * 60.0;
        assert!(
            work <= bound_per_session + 1e-3,
            "60 s of max-rate mash produced {work} work, above the {bound_per_session} \
             bound (anti-mashing violated)"
        );
        // And it must have *done* substantial work — a clamp that kills
        // all progress would also pass the `<=` check; the lower bound
        // keeps the test honest about "clamped, not disabled".
        assert!(
            work > bound_per_session * 0.5,
            "60 s of max-rate mash should still produce substantial work \
             (clamped, not zero): {work}"
        );
    }

    #[test]
    fn progress_delta_typical_typing_is_small_per_frame() {
        // A readability guard: at a normal ~5 events/s typing rate over a
        // typical 16 ms frame the work added is ≈ 0.004 — the bar moves
        // slowly and visibly, which is the point (plan §1: "the character
        // visibly codes while the real user works").
        let per_frame = progress_delta(5.0, 0.016);
        assert!(
            (0.0 < per_frame) && (per_frame < 0.01),
            "~5 ev/s at 16 ms should add a small positive amount of work, got {per_frame}"
        );
    }

    // ------------------------------------------------------------------
    // level_for_xp — monotone, pure threshold math
    // ------------------------------------------------------------------

    #[test]
    fn level_for_xp_floor_is_one() {
        // `xp = 0` must map to level 1 (never 0), and the level must not
        // drop below 1 for any input.
        assert_eq!(level_for_xp(0), 1);
        assert_eq!(level_for_xp(1), 1);
        assert_eq!(level_for_xp(99), 1);
        assert!(level_for_xp(u32::MAX) >= 1);
    }

    #[test]
    fn level_for_xp_thresholds_are_correct() {
        // level n requires 50 * (n-1) * n total XP:
        //   level 2: 100  level 3: 300  level 4: 600  level 5: 1000
        assert_eq!(level_for_xp(99), 1);
        assert_eq!(level_for_xp(100), 2);
        assert_eq!(level_for_xp(299), 2);
        assert_eq!(level_for_xp(300), 3);
        assert_eq!(level_for_xp(599), 3);
        assert_eq!(level_for_xp(600), 4);
        assert_eq!(level_for_xp(999), 4);
        assert_eq!(level_for_xp(1000), 5);
    }

    #[test]
    fn level_for_xp_is_monotone_non_decreasing() {
        // Walking the whole u32 range from 0..=2000 (enough to pass
        // through levels 1..6+) the level must never decrease — a defect
        // here would let level-ups *revert* on a later project.
        let mut last = 0u32;
        for xp in 0..=2000u32 {
            let lvl = level_for_xp(xp);
            assert!(lvl >= last, "level dropped at xp {xp}: {last} → {lvl}");
            last = lvl;
        }
    }

    #[test]
    fn level_for_xp_is_idempotent_on_recompute() {
        // `xp_level_system` recomputes `level_for_xp(xp)` every time a
        // project completes; if xp did not cross a threshold the level
        // must be unchanged (no spurious `LevelUp` fires).
        assert_eq!(level_for_xp(150), level_for_xp(150));
        assert_eq!(level_for_xp(100), level_for_xp(299)); // both level 2
        assert_ne!(level_for_xp(300), level_for_xp(299)); // crossing the threshold changes it
    }

    // ------------------------------------------------------------------
    // next_project_index — the static-list rotation (used by
    // project_completion_system)
    // ------------------------------------------------------------------

    #[test]
    fn next_project_index_advances_and_wraps() {
        assert_eq!(next_project_index(0, 4), 1);
        assert_eq!(next_project_index(1, 4), 2);
        assert_eq!(next_project_index(2, 4), 3);
        assert_eq!(next_project_index(3, 4), 0); // wraps
        assert_eq!(next_project_index(0, 1), 0); // single-project list wraps to itself
        assert_eq!(next_project_index(0, 0), 0); // degenerate: no panic
    }

    // ------------------------------------------------------------------
    // has_activity — the mood-reset filter (idle timer resets on real
    // input, *not* on a lone focus flip — a common OS artifact)
    // ------------------------------------------------------------------

    #[test]
    fn non_focus_event_ignores_lone_focus_flips() {
        assert!(!non_focus_event(&[]));
        assert!(!non_focus_event(&[ActivityEvent::FocusChanged(true)]));
        assert!(!non_focus_event(&[ActivityEvent::FocusChanged(false)]));
        assert!(non_focus_event(&[ActivityEvent::Keystroke]));
        assert!(non_focus_event(&[ActivityEvent::MouseMoved]));
        assert!(non_focus_event(&[
            ActivityEvent::FocusChanged(true),
            ActivityEvent::Keystroke
        ]));
    }

    // ------------------------------------------------------------------
    // M5 — personality line (plan §4/M5: "one or two idle/mood text lines
    // for personality, no dialogue system")
    // ------------------------------------------------------------------

    #[test]
    fn mood_line_shows_a_break_line_only_on_on_break() {
        // The plan's own example: "Maybe we should take a break?" on
        // entering OnBreak. The other moods get no line (empty string).
        assert_eq!(
            mood_line(Mood::OnBreak),
            Some("Maybe we should take a break?"),
            "OnBreak must carry the personality line"
        );
        assert_eq!(mood_line(Mood::Idle), None, "Idle gets no line");
        assert_eq!(mood_line(Mood::Coding), None, "Coding gets no line");
    }

    // ------------------------------------------------------------------
    // Integration-style: project roll + award through the M3 systems,
    // driven by a MinimalPlugins App (no window, no render) — the same
    // pattern M2 used. Exercises `project_progress_system`,
    // `project_completion_system`, and `xp_level_system` together across
    // enough frames that the first project completes.
    // ------------------------------------------------------------------

    /// Per-frame dt for the Update schedule (bridge / idle detection).
    /// The fixed step is set via `Time<Fixed>` directly so the harness
    /// controls it deterministically (same rationale as M2's `FrameDt`).
    #[derive(Resource, Default, Clone, Copy)]
    struct FrameDt(pub Duration);

    /// The shared M3 (progress + activity + idle) test fixture. `pub(crate)`
    /// so the M4 app-driven persistence test in this same module can reuse
    /// the *exact same* systems, resources, and `Time<Fixed>` setup as the
    /// M3 idle test (the "quit, relaunch, values restore" half of M4's exit
    /// criterion is proven by driving these systems, not a second fixture).
    pub(crate) fn build_m3_app() -> App {
        let mut app = App::new();
        app.add_plugins(MinimalPlugins);
        app.insert_resource(Time::<Fixed>::from_hz(32.0));

        app.add_message::<ProjectCompleted>();
        app.add_message::<LevelUp>();
        app.add_message::<KeyboardInput>();

        app.insert_resource(FrameDt(Duration::from_millis(16)))
            .init_resource::<ActivitySource>()
            .init_resource::<ActivityMeter>()
            .insert_resource(Wallet(0))
            .insert_resource(PlayerXp::default())
            .insert_resource(project_at(0))
            .init_resource::<NextProjectIndex>()
            .add_systems(Startup, |mut commands: Commands| {
                commands.spawn(Developer { mood: Mood::Idle });
            });

        // The same input → bridge → idle chain as main.rs, with `dt` from
        // `FrameDt` for a deterministic per-frame duration (the wall clock
        // in a headless test is microseconds, which would break the
        // 60 s idle threshold and distort `decay_and_accumulate`).
        app.add_systems(
            Update,
            (
                |mut source: ResMut<ActivitySource>, mut kb: MessageReader<KeyboardInput>| {
                    for _ in kb.read() {
                        source.0.record([ActivityEvent::Keystroke]);
                    }
                },
                |mut source: ResMut<ActivitySource>,
                 mut meter: ResMut<ActivityMeter>,
                 dt: Res<FrameDt>| {
                    let events = source.0.poll();
                    meter.recent_rate = decay_and_accumulate(meter.recent_rate, &events, dt.0);
                    let fresh = non_focus_event(&events);
                    if fresh {
                        meter.idle_timer.reset();
                    } else {
                        meter.idle_timer.tick(dt.0);
                    }
                },
                idle_detection_system,
            )
                .chain(),
        );

        // The shipping `project_progress_system` in FixedUpdate, driven by
        // the `Time<Fixed>` overstep (which `step_m3` feeds by advancing
        // `Time<Virtual>` by the simulated frame dt). Registered exactly
        // ONCE here (M4 bug fix: an earlier version of this fixture also
        // registered it near the top of the function — a duplicate that
        // silently no-op'd while `Time<Fixed>` never advanced under
        // MinimalPlugins' wall-clock time, but would double the progress
        // the moment the harness (or the shipped app) feeds the fixed
        // clock deterministically).
        app.add_systems(FixedUpdate, project_progress_system);

        // The completion + award chain.
        app.add_systems(Update, (project_completion_system, xp_level_system).chain());

        app
    }

    fn write_key(world: &mut World) {
        world.write_message(KeyboardInput {
            key_code: KeyCode::KeyA,
            logical_key: Key::Character("a".into()),
            state: ButtonState::Pressed,
            text: Some("a".into()),
            repeat: false,
            window: Entity::PLACEHOLDER,
        });
    }

    /// Advance one simulated frame: write `keys` keystrokes, set the
    /// frame-dt, **advance the simulated clocks by `dt`**, and run the app.
    ///
    /// Bevy 0.19: `Time<Virtual>` (installed by `MinimalPlugins`) drives the
    /// generic `Time` resource, which (a) `save_system` reads for its
    /// `delta()` and (b) `run_fixed_main_schedule` consumes as
    /// `Time<Fixed>`'s overstep — so advancing it here is what makes both
    /// the 32 Hz `project_progress_system` and the 30 s `SaveTimer` tick
    /// deterministically, independent of the wall clock (the same rationale
    /// as M2's `FrameDt` for the activity bridge, extended to the clocks).
    fn step_m3(app: &mut App, dt: Duration, keys: u32) {
        for _ in 0..keys {
            write_key(app.world_mut());
        }
        let world = app.world_mut();
        *world.resource_mut::<FrameDt>() = FrameDt(dt);
        world.resource_mut::<Time<Virtual>>().advance_by(dt);
        app.update();
    }

    /// The single `Developer`'s mood, or `None` if no entity was spawned
    /// (read through `&mut World` so the call sites in the tests, which
    /// hold the app as `&mut App`, need no extra mutability dance around
    /// the immutable `app.world()` accessor).
    fn dev_mood(app: &mut App) -> Option<Mood> {
        world_mood(app.world_mut())
    }

    fn world_mood(world: &mut World) -> Option<Mood> {
        let mut q = world.query::<&Developer>();
        q.iter(world).next().map(|d| d.mood)
    }

    /// Simulate `seconds` of continuous typing at ~1 keystroke / 34 ms
    // NOTE: a Bevy-driven app integration test for the M3 loop is NOT in
    // M3's required scope (plan §4/M3 mandates unit tests for
    // `progress_delta` and `level_for_xp` — both present above — and the
    // `cargo test --workspace` green criterion, which the pure tests
    // plus the M2 `m2_smoke.rs` integration suite satisfy). The
    // remaining M3 behaviors (project roll on completion, level-up on
    // XP threshold, idle → OnBreak flip) are exercised by the `cargo run
    // -p companion` smoke test in `docs/milestone-log.md` for M3, which
    // is the same pattern M2 used for its non-headless-observable exit
    // criterion: the pure math is unit-tested, the app-wiring is verified
    // by the smoke run + code review, and an app-driven test would
    // require the full Bevy fixture (Time<Virtual> + Time<Fixed> +
    // FixedUpdate scheduling + 30–60 s of simulated frames) to be
    // deterministic in headless CI. See the M3 milestone-log entry for
    // the full command sequence + actual output.

    #[test]
    fn m3_idle_flips_mood_to_on_break_after_threshold() {
        let mut app = build_m3_app();
        let dt = Duration::from_millis(100);
        // Get into Coding with some activity.
        for _ in 0..10 {
            step_m3(&mut app, dt, 1);
        }
        assert_eq!(dev_mood(&mut app).unwrap(), Mood::Coding);

        // Now: no input for IDLE_THRESHOLD + 2 s of frames.
        let mut idle_ms = 0u64;
        while idle_ms < (IDLE_THRESHOLD * 1000.0 + 2000.0) as u64 {
            step_m3(&mut app, dt, 0);
            idle_ms += 100;
        }
        assert_eq!(
            dev_mood(&mut app).unwrap(),
            Mood::OnBreak,
            "mood should be OnBreak after {} ms of silence (threshold {}s)",
            idle_ms,
            IDLE_THRESHOLD
        );

        // One keystroke resets to Coding *immediately* (no second-tick
        // delay) and restarts the idle timer.
        step_m3(&mut app, dt, 1);
        assert_eq!(
            dev_mood(&mut app).unwrap(),
            Mood::Coding,
            "fresh activity must flip OnBreak → Coding"
        );
        let timer = app.world().resource::<ActivityMeter>().idle_timer.clone();
        assert_eq!(
            timer.elapsed(),
            Duration::ZERO,
            "the idle timer must be reset by fresh activity"
        );

        // And the flip back requires a *full* threshold of silence again —
        // one frame short must not flip it.
        let mut idle_ms = 0u64;
        while idle_ms < (IDLE_THRESHOLD * 1000.0) as u64 - 100 {
            step_m3(&mut app, dt, 0);
            idle_ms += 100;
        }
        assert_eq!(
            dev_mood(&mut app).unwrap(),
            Mood::Coding,
            "no flip-back one frame before the full threshold"
        );
        step_m3(&mut app, dt, 0);
        assert_eq!(
            dev_mood(&mut app).unwrap(),
            Mood::OnBreak,
            "flips back at the full threshold of silence"
        );
    }

    // ------------------------------------------------------------------
    // M4 — persistence (plan §3.4 / §4/M4)
    // ------------------------------------------------------------------

    #[test]
    fn save_data_round_trip_serialize_deserialize_asserts_equality() {
        // Plan §4/M4 exit criterion: "Add a round-trip unit test
        // (serialize a SaveData, deserialize, assert equality) independent
        // of the file system." — pure serde_json, no fs involved.
        let original = SaveData {
            wallet: 1_000_000,
            xp: 599,
            level: 3,
            current_project: Some(CurrentProjectSave {
                index: 2,
                work_done: 37.5,
            }),
        };
        let json = serde_json::to_string(&original).expect("serialization of a valid SaveData");
        let back: SaveData =
            serde_json::from_str(&json).expect("deserialization of the round-tripped JSON");
        assert_eq!(back, original);

        // The `None` arm (a save written before any project exists) must
        // round-trip too.
        let fresh = SaveData {
            wallet: 0,
            xp: 0,
            level: 1,
            current_project: None,
        };
        let json_fresh = serde_json::to_string(&fresh).expect("fresh SaveData serialization");
        let back_fresh: SaveData =
            serde_json::from_str(&json_fresh).expect("fresh SaveData deserialization");
        assert_eq!(back_fresh, fresh);
    }

    /// Focused check of Bevy 0.19's `Timer::tick` + `just_finished` at the
    /// exact cadence the app-driven tests use (one 31.25 ms tick per frame,
    /// 30 s interval): the interval must register `just_finished` exactly
    /// once, at the first tick where elapsed crosses 30 s.
    #[test]
    fn timer_just_fires_at_the_interval_under_test_cadence() {
        let mut t = Timer::from_seconds(SAVE_INTERVAL, TimerMode::Repeating);
        let step = Duration::from_millis(31250 / 1000.0 as u64); // 31.25 ms
        let mut fired = 0usize;
        let mut elapsed = Duration::ZERO;
        for _ in 0..1100 {
            t.tick(step);
            elapsed += step;
            if t.just_finished() {
                fired += 1;
                assert!(
                    elapsed >= Duration::from_secs(30),
                    "fires only at/after 30 s"
                );
            }
        }
        assert_eq!(
            fired, 1,
            "the 30 s interval must fire exactly once in 34.4 s"
        );
    }

    /// A `SaveData` written to a **real file** (in a temp dir, never the
    /// user's data dir) and read back must be equal — the on-disk half of
    /// the round trip, still independent of any game state.
    #[test]
    fn save_file_round_trip_write_and_read() {
        let dir = std::env::temp_dir().join(format!(
            "companion-m4-save-{}-{}",
            std::process::id(),
            unique_suffix()
        ));
        let path = dir.join("save.json");
        let original = SaveData {
            wallet: 77,
            xp: 1234,
            level: 5,
            current_project: Some(CurrentProjectSave {
                index: 3,
                work_done: 0.0,
            }),
        };
        write_save_file(&path, &original).expect("write_save_file to a temp dir");
        let loaded = load_save_file(&path).expect("load_save_file of a just-written file");
        assert_eq!(loaded, Some(original));
        let _ = fs::remove_dir_all(&dir);
    }

    /// A missing file is a *fresh install*, not an error; a malformed file
    /// is an error the caller treats as fresh (it must never crash).
    #[test]
    fn save_file_missing_is_fresh_and_malformed_is_an_error() {
        let dir = std::env::temp_dir().join(format!(
            "companion-m4-save-{}-{}",
            std::process::id(),
            unique_suffix()
        ));
        fs::create_dir_all(&dir).expect("temp dir");
        let missing = dir.join("save.json");
        assert_eq!(load_save_file(&missing), Ok(None));

        let bad = dir.join("bad.json");
        fs::write(&bad, "{ not valid json !!!").expect("write bad json");
        assert!(
            load_save_file(&bad).is_err(),
            "malformed JSON must be an Err"
        );

        let wrong_shape = dir.join("wrong.json");
        fs::write(&wrong_shape, r#"{"wallet": 5}"#).expect("write wrong-shape json");
        assert!(
            load_save_file(&wrong_shape).is_err(),
            "a SaveData missing required fields must be an Err (deny unknown/missing fields)"
        );
        let _ = fs::remove_dir_all(&dir);
    }

    /// `save_data_from_resources` captures exactly what the exit criterion
    /// names: wallet, xp, level, and the in-progress project (index + work
    /// done).
    #[test]
    fn save_data_from_resources_captures_wallet_xp_level_and_project() {
        let wallet = Wallet(42);
        let xp = PlayerXp { level: 2, xp: 100 };
        let mut project = project_at(1);
        project.work_done = 20.25;
        let index = NextProjectIndex(1);

        let data = save_data_from_resources(&wallet, &xp, &project, &index);
        assert_eq!(
            data,
            SaveData {
                wallet: 42,
                xp: 100,
                level: 2,
                current_project: Some(CurrentProjectSave {
                    index: 1,
                    work_done: 20.25
                }),
            }
        );
    }

    /// App-driven: `load_or_init_save` **must not destroy** the in-memory
    /// resources when the save file is absent — the fresh-install path
    /// keeps whatever `main()` inserted at construction (wallet 0, level 1,
    /// project #1). Run on a MinimalPlugins app (no window, no render) so it
    /// works headless; the path override points at an empty temp dir so no
    /// real file is touched.
    #[test]
    fn load_or_init_save_without_a_file_keeps_fresh_state() {
        let dir = std::env::temp_dir().join(format!(
            "companion-m4-load-{}-{}",
            std::process::id(),
            unique_suffix()
        ));
        fs::create_dir_all(&dir).expect("temp dir");
        let _guard = save_path_guard(); // serialize the path-sensitive tests
        set_save_path(Some(dir.join("save.json")));

        let mut app = App::new();
        app.add_plugins(MinimalPlugins);
        app.insert_resource(Wallet(0));
        app.insert_resource(PlayerXp::default());
        app.insert_resource(project_at(0));
        app.init_resource::<NextProjectIndex>();
        app.add_systems(Update, load_or_init_save);
        app.update();

        assert_eq!(app.world().resource::<Wallet>().0, 0);
        assert_eq!(app.world().resource::<PlayerXp>(), &PlayerXp::default());
        assert_eq!(
            app.world().resource::<CurrentProject>().name,
            "Fix login flow"
        );
        assert_eq!(app.world().resource::<CurrentProject>().work_done, 0.0);

        set_save_path(None);
        let _ = fs::remove_dir_all(&dir);
    }

    /// App-driven: `load_or_init_save` **restores** wallet / xp / level /
    /// project from an existing save file — the "relaunch" half of M4's
    /// exit criterion, driven through the real system on a real `App`
    /// (MinimalPlugins, headless-safe; `FrameDt`/no window per the M2/M3
    /// pattern).
    #[test]
    fn load_or_init_save_restores_a_partway_project() {
        let _guard = save_path_guard(); // serialize the path-sensitive tests
        let dir = std::env::temp_dir().join(format!(
            "companion-m4-load-{}-{}",
            std::process::id(),
            unique_suffix()
        ));
        fs::create_dir_all(&dir).expect("temp dir");
        let path = dir.join("save.json");

        // "Get partway through a project": 30/100 work on project #2
        // ("Add CI cache"), wallet 65 (one project #1 completed), xp 40,
        // level still 1 (40 < 100).
        let save = SaveData {
            wallet: 65,
            xp: 40,
            level: 1,
            current_project: Some(CurrentProjectSave {
                index: 2,
                work_done: 30.0,
            }),
        };
        write_save_file(&path, &save).expect("seed the save file");
        set_save_path(Some(path));

        let mut app = App::new();
        app.add_plugins(MinimalPlugins);
        // Fresh-install defaults at construction (what main() inserts).
        app.insert_resource(Wallet(0));
        app.insert_resource(PlayerXp::default());
        app.insert_resource(project_at(0));
        app.init_resource::<NextProjectIndex>();
        app.add_systems(Update, load_or_init_save);
        app.update();

        let world = app.world();
        assert_eq!(world.resource::<Wallet>().0, 65, "wallet must restore");
        let xp = world.resource::<PlayerXp>();
        assert_eq!(xp.xp, 40, "xp must restore");
        assert_eq!(xp.level, 1, "level must be level_for_xp(40) = 1");
        let project = world.resource::<CurrentProject>();
        assert_eq!(
            project.name, "Add CI cache",
            "the in-progress project must restore"
        );
        assert_eq!(project.total_work, 100.0);
        assert_eq!(
            project.work_done, 30.0,
            "partway progress (work_done) must restore exactly"
        );
        assert_eq!(
            world.resource::<NextProjectIndex>().0,
            2,
            "the static-list index must restore so the next roll continues correctly"
        );

        set_save_path(None);
        let _ = fs::remove_dir_all(&dir);
    }

    /// A unique-ish temp-dir suffix so parallel tests don't collide. A
    /// process-global counter is sufficient (each test in this binary gets
    /// a distinct value; `std::thread::current().id().as_u64()` is unstable
    /// and was the previous attempt — see M4 bug notes).
    fn unique_suffix() -> u64 {
        static COUNTER: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
        COUNTER.fetch_add(1, std::sync::atomic::Ordering::Relaxed)
    }

    /// The path-sensitive M4 tests (the ones that call [`set_save_path`])
    /// must not interleave: the override is process-global, so two such
    /// tests running in parallel would each clobber the other's save path
    /// mid-run (`cargo test` runs tests on parallel threads by default).
    /// `cargo test --workspace` is the verification command, so serializing
    /// *these* few fast tests is the right fix (a per-test global would
    /// need a larger redesign for no benefit in v0.1).
    fn save_path_guard() -> std::sync::MutexGuard<'static, ()> {
        static GUARD: std::sync::Mutex<()> = std::sync::Mutex::new(());
        // Tolerate poisoning: if a *previous* holder panicked, its finally-style
        // cleanup (set_save_path(None)) may not have run, but the override is
        // per-test and every test resets it at the top of its body — recovering
        // from a poisoned lock is safe here and keeps one flaky panic from
        // cascading into every other path-sensitive test.
        GUARD
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
    }

    /// Drive time deterministically for the app-driven M4 tests:
    /// `TimeUpdateStrategy::FixedTimesteps(1)` makes `time_system` (First)
    /// advance `Time<Real>` by exactly one `Time<Fixed>` timestep per
    /// `app.update()`, and `update_virtual_time` then copies that delta
    /// into `Time<Virtual>` and the generic `Time` — the exact resource
    /// `save_system` reads. `step_m3`'s `Time<Virtual>::advance_by(dt)`
    /// additionally feeds the `Time<Fixed>` overstep so
    /// `project_progress_system` gets its 32 Hz ticks. Both advances are
    /// additive, so `save_system` sees the 32 ms `Time` delta it needs
    /// while the progress system sees the simulated frame dt.
    fn drive_fixed_timesteps(app: &mut App) {
        app.world_mut()
            .insert_resource(bevy_time::TimeUpdateStrategy::FixedTimesteps(1));
    }

    /// App-driven: `save_system` (the **shipping** system) fires when
    /// `SaveTimer` elapses and writes the current resources to the (temp)
    /// save file — the "quit" half of the exit criterion. The app runs the
    /// full M3 loop (via the shared `build_m3_app` fixture) so the values
    /// being saved are the ones the loop itself produced: ~10 keystrokes of
    /// typing fill `ActivityMeter` and `project_progress_system` does the
    /// real work on the project.
    #[test]
    fn save_system_writes_the_live_state_when_the_timer_fires() {
        let _guard = save_path_guard(); // serialize the path-sensitive tests
        let dir = std::env::temp_dir().join(format!(
            "companion-m4-save-{}-{}",
            std::process::id(),
            unique_suffix()
        ));
        fs::create_dir_all(&dir).expect("temp dir");
        let path = dir.join("save.json");
        set_save_path(Some(path.clone()));

        // The shared M3 fixture: the real project_progress / completion /
        // xp-level / bridge / idle systems + a 32 Hz Time<Fixed>.
        // `StatesPlugin` must be installed *before the first `app.update()`*
        // (it rewrites `MainScheduleOrder` to insert the `StateTransition`
        // schedule after `PreUpdate` — a schedule that is only consulted
        // when the main schedule is *built*, which happens on the first
        // update). `build_m3_app` runs no updates, so adding the plugin
        // here is still before that build.
        let mut app = build_m3_app();
        app.add_plugins(bevy::state::app::StatesPlugin);
        app.init_state::<AppState>();
        drive_fixed_timesteps(&mut app);
        app.insert_resource(SaveTimer::new());
        app.add_systems(Update, save_system);
        // Transition to Playing (the M4 fixture has no `main()`-style
        // Startup chain; `enter_playing` is added directly).
        app.add_systems(Startup, enter_playing);
        app.update();

        // Drive ~4.5 s of typing at ~30 fps (30 ms frames): at the ~10 ev/s
        // input rate that produces, the rate settles ≈ 10 ⇒ ≈ 0.5 work/s ⇒
        // project #1 (50 work) is ≈ 2.2/50 done — partway, and no
        // completion has fired yet (wallet/xp stay at the fresh defaults).
        let frame = Duration::from_millis(30);
        for i in 0..135u32 {
            // One keystroke every 3rd frame ≈ 10 ev/s of real typing.
            step_m3(&mut app, frame, if i.is_multiple_of(3) { 1 } else { 0 });
        }

        // Let the 30 s autosave timer elapse: each app.update() advances
        // `Time` by one 32 ms fixed timestep, so 30 s = 937.5 frames — run
        // 950 idle frames. The save must fire at least once.
        for _ in 0..950 {
            step_m3(&mut app, Duration::from_millis(1), 0);
        }

        assert!(path.exists(), "save_system must have written the save file");
        let loaded = load_save_file(&path).expect("the written file must parse");
        let loaded = loaded.expect("the written file must hold a SaveData");

        // wallet/xp: project #1 (total 50) is ~41/50 done at ~10 ev/s
        // (6 work/s ⇒ ~7.5 s to finish; 1.5 s of typing ≈ 9 work), so no
        // completion has fired yet — wallet and xp must be the fresh
        // defaults. (If a completion *had* fired, the assertions below on
        // work_done/index would still prove the in-progress restore.)
        let world = app.world();
        assert_eq!(loaded.wallet, world.resource::<Wallet>().0);
        assert_eq!(loaded.xp, world.resource::<PlayerXp>().xp);
        assert_eq!(loaded.level, world.resource::<PlayerXp>().level);

        let cp = loaded
            .current_project
            .expect("a current project is always saved");
        assert_eq!(cp.index, 0, "project #1 is still in progress");
        assert!(
            (cp.work_done > 0.0 && cp.work_done < 50.0),
            "partway through project #1: 0 < work_done < 50, got {}",
            cp.work_done
        );

        set_save_path(None);
        let _ = fs::remove_dir_all(&dir);
    }

    /// **The M4 exit criterion, app-driven:** "get partway through a
    /// project, quit, relaunch — wallet, xp, level, and in-progress project
    /// all restore."
    ///
    /// Session 1: the full M3 loop runs (typing → progress → a project
    /// completes and is awarded, and the *next* project is partway through)
    /// until the 30 s autosave fires. Then the app is *dropped* (quit).
    /// Session 2: a brand-new app is built the way `main()` builds it and
    /// `load_or_init_save` runs — the wallet / xp / level / in-progress
    /// project from session 1 must all be back. This is the mechanical
    /// proof of the exit criterion in a headless environment (same pattern
    /// as M2/M3's `FrameDt`-driven app tests).
    #[test]
    fn m4_quit_and_relaunch_restores_wallet_xp_level_and_in_progress_project() {
        let _guard = save_path_guard(); // serialize the path-sensitive tests
        let dir = std::env::temp_dir().join(format!(
            "companion-m4-e2e-{}-{}",
            std::process::id(),
            unique_suffix()
        ));
        fs::create_dir_all(&dir).expect("temp dir");
        let path = dir.join("save.json");
        set_save_path(Some(path.clone()));

        // ---- Session 1: get partway through a project, then quit. ----
        {
            // `StatesPlugin` before the first update (see the other M4
            // test for why — the schedule order is frozen at first update).
            let mut app = build_m3_app();
            app.add_plugins(bevy::state::app::StatesPlugin);
            app.init_state::<AppState>();
            drive_fixed_timesteps(&mut app);
            app.insert_resource(SaveTimer::new());
            app.add_systems(Update, save_system);
            app.add_systems(Startup, enter_playing);
            app.update();

            // ~4.5 s of typing at ~30 fps, one keystroke every 3rd frame
            // (≈ 10 ev/s real-typing pace): at the settled ≈ 10 ev/s rate
            // that's ≈ 0.5 work/s, so project #1 (50 work) is ≈ 2.2/50
            // done — "partway through a project", exactly what the exit
            // criterion says. (No completion fires in this window — wallet
            // and xp stay at the fresh defaults, and the save captures the
            // in-progress project; `load_or_init_save_restores_a_partway_
            // project` covers the completed-project + award restore path
            // with a hand-written save.)
            let frame = Duration::from_millis(30);
            for i in 0..135u32 {
                step_m3(&mut app, frame, if i.is_multiple_of(3) { 1 } else { 0 });
            }
            // Let the 30 s autosave fire: each app.update() advances `Time`
            // by one 32 ms fixed timestep, so 30 s ≈ 938 frames — run 950
            // idle frames.
            for _ in 0..950 {
                step_m3(&mut app, Duration::from_millis(1), 0);
            }

            // Capture session 1's end state *before* quitting, to compare
            // against the restored state (the save is written from these
            // resources, so they must match exactly — see the assertion
            // comments below for why the tail is safe to ignore).
            let wallet = app.world().resource::<Wallet>().0;
            let xp = app.world().resource::<PlayerXp>();
            let project = app.world().resource::<CurrentProject>().clone();
            let index = app.world().resource::<NextProjectIndex>().0;

            // The session must actually have made progress (otherwise the
            // "partway" claim is vacuous) and the save must exist.
            assert!(
                project.work_done > 0.0,
                "session 1 must get partway through a project (work_done {})",
                project.work_done
            );
            assert!(
                path.exists(),
                "the 30 s autosave must have written the save file"
            );

            let expected_wallet = wallet;
            let expected_xp = xp.xp;
            let expected_level = xp.level;
            let expected_index = index;
            let expected_work_done = project.work_done;
            let expected_name = project.name.clone();
            // --- quit: drop the app (all in-memory state gone). ---
            drop(app);

            // ---- Session 2: relaunch — a fresh app, load_or_init_save. ----
            let mut app2 = App::new();
            app2.add_plugins(MinimalPlugins);
            // Exactly what main() inserts at construction (fresh defaults):
            app2.insert_resource(Wallet(0));
            app2.insert_resource(PlayerXp::default());
            app2.insert_resource(project_at(0));
            app2.init_resource::<NextProjectIndex>();
            app2.add_systems(Update, load_or_init_save);
            app2.update(); // runs load_or_init_save on the real save file

            let world = app2.world();
            let w = world.resource::<Wallet>().0;
            let x = world.resource::<PlayerXp>();
            let p = world.resource::<CurrentProject>();
            let i = world.resource::<NextProjectIndex>().0;

            // wallet / xp / level / index: exact (no project completion
            // fires in the ~4.5 s tail after the 30 s save mark).
            assert_eq!(
                (w, x.xp, x.level, i),
                (expected_wallet, expected_xp, expected_level, expected_index),
                "relaunch must restore wallet/xp/level/index exactly \
                 (restored ({w}, {}, {}, {}), session 1 end ({}, {}, {}, {}))",
                x.xp,
                x.level,
                i,
                expected_wallet,
                expected_xp,
                expected_level,
                expected_index
            );
            // work_done: the save is a mid-session snapshot (at the 30 s
            // mark); the ~4.5 s tail after it added a small amount of work
            // (the rate has decayed but isn't exactly zero). Assert the
            // restored value is in (0, session-end].
            assert!(
                p.work_done > 0.0 && p.work_done <= expected_work_done,
                "restored work_done {} must be in (0, {}] (the save was a \
                 mid-session snapshot at the 30 s mark; the tail added a \
                 small amount of work)",
                p.work_done,
                expected_work_done
            );
            assert_eq!(
                p.name, expected_name,
                "project name must match via the saved index"
            );
        }

        set_save_path(None);
        let _ = fs::remove_dir_all(&dir);
    }

    // ------------------------------------------------------------------
    // M4 coverage gaps flagged by the tests reviewer (optional, non-
    // blocking — picked up in M5).
    // ------------------------------------------------------------------

    /// App-driven: the `load_or_init_save` **`Err` arm** (a corrupted /
    /// unreadable save on disk) must keep the in-memory fresh state intact —
    /// the wallet / level / project must be untouched, and the app must not
    /// crash. This exercises the `Err(e) => warn!(…)` branch of
    /// `load_or_init_save` (main.rs ~line 1079), which no prior test reached
    /// (the malformed-JSON `Err` was covered only at the `load_save_file`
    /// level, not through the system).
    #[test]
    fn load_or_init_save_malformed_save_keeps_fresh_state_without_crashing() {
        let _guard = save_path_guard(); // serialize the path-sensitive tests
        let dir = std::env::temp_dir().join(format!(
            "companion-m5-malformed-{}-{}",
            std::process::id(),
            unique_suffix()
        ));
        fs::create_dir_all(&dir).expect("temp dir");
        let path = dir.join("save.json");
        // A genuinely corrupt file (not valid JSON) → `load_save_file`
        // returns `Err`, so `load_or_init_save` takes its `Err` arm.
        fs::write(&path, "{ this is not valid json !!!").expect("write corrupt save");
        set_save_path(Some(path));

        let mut app = App::new();
        app.add_plugins(MinimalPlugins);
        // The in-memory fresh defaults main() inserts at construction.
        app.insert_resource(Wallet(0));
        app.insert_resource(PlayerXp::default());
        app.insert_resource(project_at(0));
        app.init_resource::<NextProjectIndex>();
        app.add_systems(Update, load_or_init_save);
        // Must NOT panic on the Err arm — that is the whole point.
        app.update();

        // Fresh state preserved (the Err arm keeps the in-memory defaults).
        assert_eq!(
            app.world().resource::<Wallet>().0,
            0,
            "a corrupted save must not clobber the wallet"
        );
        assert_eq!(
            app.world().resource::<PlayerXp>(),
            &PlayerXp::default(),
            "a corrupted save must not clobber xp/level"
        );
        assert_eq!(
            app.world().resource::<CurrentProject>().name,
            "Fix login flow",
            "a corrupted save must not clobber the in-progress project"
        );
        assert_eq!(
            app.world().resource::<CurrentProject>().work_done,
            0.0,
            "a corrupted save must not clobber work_done"
        );

        set_save_path(None);
        let _ = fs::remove_dir_all(&dir);
    }

    /// App-driven: a **corrupted (out-of-range) project index** in a
    /// *structurally valid* save file must be clamped into range, never
    /// panic — the `cp.index.min(PROJECT_LIST_LEN - 1)` clamp in
    /// `load_or_init_save` (main.rs ~line 1062). The M4 tests covered
    /// `current_project: None` and a valid `Some`, but not an
    /// out-of-range `index`; this exercises the clamp directly through the
    /// system.
    #[test]
    fn load_or_init_save_clamps_an_out_of_range_project_index() {
        let _guard = save_path_guard(); // serialize the path-sensitive tests
        let dir = std::env::temp_dir().join(format!(
            "companion-m5-clamp-{}-{}",
            std::process::id(),
            unique_suffix()
        ));
        fs::create_dir_all(&dir).expect("temp dir");
        let path = dir.join("save.json");
        // A structurally valid SaveData whose index is far out of range
        // (the static list has 4 projects, indices 0..=3).
        let save = SaveData {
            wallet: 10,
            xp: 0,
            level: 1,
            current_project: Some(CurrentProjectSave {
                index: 999,
                work_done: 0.0,
            }),
        };
        write_save_file(&path, &save).expect("seed the (corrupt-index) save");
        set_save_path(Some(path));

        let mut app = App::new();
        app.add_plugins(MinimalPlugins);
        app.insert_resource(Wallet(0));
        app.insert_resource(PlayerXp::default());
        app.insert_resource(project_at(0));
        app.init_resource::<NextProjectIndex>();
        app.add_systems(Update, load_or_init_save);
        // Must NOT panic on the out-of-range index — the clamp handles it.
        app.update();

        let world = app.world();
        assert_eq!(
            world.resource::<NextProjectIndex>().0,
            PROJECT_LIST_LEN - 1,
            "an out-of-range index must clamp to the last project (index \
             {}), not panic",
            PROJECT_LIST_LEN - 1
        );
        assert_eq!(
            world.resource::<CurrentProject>().name,
            "Write API docs",
            "the clamped index must land on the last static-list project"
        );
        assert_eq!(
            world.resource::<CurrentProject>().work_done,
            0.0,
            "a 0.0 work_done clamps to itself (it is already in range)"
        );

        set_save_path(None);
        let _ = fs::remove_dir_all(&dir);
    }

    // ------------------------------------------------------------------
    // M5 — desk upgrade (plan §4/M5: "a coin threshold unlocks a second
    // placeholder prop"). App-driven through the *shipping*
    // `desk_upgrade_system`: the plant prop starts hidden, becomes visible
    // the frame the wallet crosses [`DESK_UPGRADE_COST`], and (with a
    // restored wallet above the threshold) is visible from the start —
    // proving the upgrade is derived from the persisted wallet.
    // ------------------------------------------------------------------

    /// The M5 desk prop, as `setup_scene` spawns it in the real app
    /// (hidden by default via `Visibility::Hidden`; `desk_upgrade_system`
    /// swaps it to `Visibility::Visible` when the wallet crosses the
    /// threshold).
    fn spawn_desk_prop(commands: &mut Commands) {
        commands.spawn((
            Node {
                width: px(60.0),
                height: px(80.0),
                ..default()
            },
            BackgroundColor(Color::srgb(0.25, 0.55, 0.30)),
            DeskUpgradeProp,
            Visibility::Hidden,
        ));
    }

    /// True if the desk prop is currently `Visibility::Visible` (i.e. the
    /// upgrade is "visible"). `World::query_filtered` takes `&mut World` in
    /// Bevy 0.19.
    fn prop_is_visible(world: &mut World) -> bool {
        let mut q = world.query_filtered::<&Visibility, With<DeskUpgradeProp>>();
        q.iter(world)
            .next()
            .copied()
            .map(|v| v == Visibility::Visible)
            .unwrap_or(false)
    }

    #[test]
    fn m5_desk_upgrade_shows_plant_when_wallet_crosses_threshold() {
        let mut app = App::new();
        app.add_plugins(MinimalPlugins);
        app.insert_resource(Wallet(0));
        app.add_systems(Startup, |mut commands: Commands| {
            spawn_desk_prop(&mut commands)
        });
        app.add_systems(Update, desk_upgrade_system);

        // Below the threshold: hidden (Visibility::Hidden).
        app.update();
        assert!(
            !prop_is_visible(app.world_mut()),
            "wallet 0 < {DESK_UPGRADE_COST}: the plant must be hidden"
        );

        // Cross the threshold (exactly at DESK_UPGRADE_COST): visible.
        *app.world_mut().resource_mut::<Wallet>() = Wallet(DESK_UPGRADE_COST);
        app.update();
        assert!(
            prop_is_visible(app.world_mut()),
            "wallet == {DESK_UPGRADE_COST}: the plant must be visible (the \
             coin threshold unlocks the prop)"
        );

        // Above the threshold: still visible.
        *app.world_mut().resource_mut::<Wallet>() = Wallet(DESK_UPGRADE_COST + 5);
        app.update();
        assert!(
            prop_is_visible(app.world_mut()),
            "wallet above the threshold: the plant stays visible"
        );
    }

    #[test]
    fn m5_desk_upgrade_restored_wallet_above_threshold_is_visible_on_launch() {
        // Non-persistence consequence: the upgrade is *derived* from the
        // persisted wallet, so a relaunch whose wallet already meets the
        // threshold shows the plant from the first frame (no separate
        // "bought" flag to persist — the plan §4/M5 "keep it non-persistent
        // if simpler" choice).
        let mut app = App::new();
        app.add_plugins(MinimalPlugins);
        // A restored save's wallet, above the threshold.
        app.insert_resource(Wallet(120));
        app.add_systems(Startup, |mut commands: Commands| {
            spawn_desk_prop(&mut commands)
        });
        app.add_systems(Update, desk_upgrade_system);
        app.update();

        assert!(
            prop_is_visible(app.world_mut()),
            "a restored wallet of 120 ≥ {DESK_UPGRADE_COST} must show the \
             plant on launch (derived, not separately persisted)"
        );
    }
}
