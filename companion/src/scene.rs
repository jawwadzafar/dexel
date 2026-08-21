//! The pixel-art room: real sprites, integer-upscaled, replacing the M1
//! placeholder `Node` rects (docs/art-direction.md).
//!
//! [`ScenePlugin`] owns everything *world-space* about the scene — the
//! camera's integer upscale, the back-to-front sprite stack, and the three
//! per-frame swaps that make the room react to game state:
//!
//! * the character's frame follows [`Mood`] (typing loop / idle / coffee),
//! * the monitor is lit while `Mood::Coding` and dim otherwise,
//! * the plant appears at exactly the [`DESK_UPGRADE_COST`] wallet threshold
//!   `desk_upgrade_system` already uses.
//!
//! It deliberately owns *nothing* about the HUD: the bottom bar stays in
//! `lib.rs` as `bevy_ui` `Node`s, which are laid out from the window's
//! logical size and are unaffected by the camera projection this plugin
//! installs (`bevy_ui`'s layout reads the render target's scale factor and
//! physical size, never the camera's `Projection`) — so the HUD keeps
//! rendering at 1:1 on top of a 2x-upscaled room.

// ---------------------------------------------------------------------------
// WIRING REQUIRED (lib.rs) — this module is not yet referenced anywhere.
// ---------------------------------------------------------------------------
//
// 1. Declare the module, next to the other top-level items in lib.rs:
//
//        pub mod scene;
//
//    (`pub` rather than private: `ScenePlugin` is then part of the crate's
//    public surface, so a `mod scene;` that nothing constructs cannot trip
//    `dead_code` under `-D warnings`.)
//
// 2. In BOTH `run()` and `build_app_with_seed()` — they must stay identical,
//    the second exists so the screenshot probe drives the *real* scene —
//    change the single `DefaultPlugins` line to:
//
//        .add_plugins(DefaultPlugins.set(ImagePlugin::default_nearest()))
//
//    WHY: `ImagePlugin::default_nearest()` swaps the default image sampler
//    from linear to `ImageSamplerDescriptor::nearest()`. This is an
//    app-level setting on the plugin group, so it cannot be done from inside
//    `ScenePlugin` — and it is not cosmetic: art-direction non-negotiable #1
//    is "no anti-aliasing". With the default linear sampler every 24x32
//    character drawn at 2x is bilinearly filtered into mush, which is the
//    single fastest way to lose the pixel-art look. A per-image override via
//    `ImageLoaderSettings` would have to be repeated at all fourteen load
//    sites and silently regress the moment someone adds a fifteenth.
//
// 3. Add the plugin itself in both functions:
//
//        .add_plugins(scene::ScenePlugin)
//
// 4. `setup_scene` in lib.rs spawns a bare `commands.spawn(Camera2d)`.
//    Deleting that line is preferred, because then this plugin owns the one
//    camera outright. It is NOT required: [`setup_pixel_camera`] runs in
//    `PostStartup` (after every `Startup` system, so the ordering is
//    defined) and *adopts* an existing `Camera2d` by inserting the
//    pixel-perfect `Projection` onto it, spawning its own only when no 2D
//    camera exists. WHY adopt instead of always spawning: two `Camera2d`s
//    targeting one window render in an unspecified order and Bevy warns
//    about the ambiguity — and the UI needs a camera, so the plugin cannot
//    simply assume it is alone.
//
// 5. Asset root. Every path below is relative to Bevy's assets directory,
//    which `AssetPlugin` defaults to `"assets"` resolved against the
//    process's working directory. The sprites live in `<repo>/assets/`, so
//    `cargo run` from the workspace root already works. Only if the binary
//    must be launched from elsewhere (an installed bundle, a `cd companion`
//    invocation) does lib.rs need an explicit root, set on the same plugin
//    group:
//
//        .add_plugins(
//            DefaultPlugins
//                .set(ImagePlugin::default_nearest())
//                .set(AssetPlugin {
//                    file_path: "assets".to_string(), // or an absolute path
//                    ..default()
//                }),
//        )
//
//    Note `.set(AssetPlugin { .. })` replaces the whole plugin, so the
//    remaining fields must come from `..default()`.
//
// 6. Window size (optional). Nothing in lib.rs configures `WindowPlugin`, so
//    the window opens at Bevy's default 1280x720 and the 320x200 room would
//    sit 2x-upscaled in the middle of a too-large window. The cleanest fix
//    is in lib.rs, where the plugin group is built:
//
//        .set(WindowPlugin {
//            primary_window: Some(Window {
//                resolution: WindowResolution::new(640, 400),
//                ..default()
//            }),
//            ..default()
//        })
//
//    Until that lands, [`setup_pixel_camera`] resizes the primary window to
//    [`ROOM_W`] x [`ROOM_H`] x [`PIXEL_SCALE`] itself so the plugin honours
//    its own contract (art-direction non-negotiable #4, "640x400 logical,
//    integer-scaled") without editing lib.rs. Doing it in `WindowPlugin`
//    avoids the one-frame resize flicker.

use bevy::prelude::*;

// `Developer` / `Mood` / `Wallet` are private to the crate root; a child
// module still sees them, so the scene reads the *real* mood enum and the
// *real* wallet rather than shadowing them with look-alike copies that could
// drift out of sync with the progression code.
use crate::{Developer, Mood};

// ---------------------------------------------------------------------------
// Sprite manifest (docs/art-direction.md § "Required sprites")
// ---------------------------------------------------------------------------
//
// Paths are relative to the Bevy assets root. The comment on each line is
// the authored native pixel size from the manifest — it is what the layout
// constants further down are computed against, so if the art changes size
// the arithmetic here has to be revisited, not just the file.

const SPRITE_ROOM_BG: &str = "room_bg.png"; // 320x200
const SPRITE_DESK: &str = "desk.png"; // 120x48
const SPRITE_MONITOR_ON: &str = "monitor_on.png"; // 40x36
const SPRITE_MONITOR_OFF: &str = "monitor_off.png"; // 40x36
const SPRITE_CHAIR: &str = "chair.png"; // 28x36
const SPRITE_DEV_IDLE: &str = "dev_idle.png"; // 24x32
const SPRITE_DEV_TYPE_A: &str = "dev_type_a.png"; // 24x32
const SPRITE_DEV_TYPE_B: &str = "dev_type_b.png"; // 24x32
const SPRITE_DEV_COFFEE: &str = "dev_coffee.png"; // 24x32
const SPRITE_PLANT: &str = "plant.png"; // 24x32
const SPRITE_MUG: &str = "mug.png"; // 10x10
const SPRITE_BOOKS: &str = "books.png"; // 32x24
const SPRITE_LAMP: &str = "lamp.png"; // 20x28
const SPRITE_RUG: &str = "rug.png"; // 96x32

// `dev_sleep.png` (24x32) is in the manifest but is deliberately NOT loaded:
// [`Mood`] has exactly three variants (`Idle` / `Coding` / `OnBreak`) and
// none of them means "asleep". Loading a handle no system ever reads would
// be a dead field under `-D warnings` and, worse, would imply a fourth mood
// exists. When a `Mood::Sleeping` lands, add it to [`CharacterFrames`].

// ---------------------------------------------------------------------------
// Authored sprite sizes (docs/art-direction.md § "Required sprites")
// ---------------------------------------------------------------------------
//
// The same numbers as the comments above, but named — because every constant
// in the Composition section is *derived* from them. A sprite is centred on
// its `Transform`, so "resting on a surface at y = s" is
// `centre_y = s + height / 2`, and that arithmetic is only checkable (by a
// reader or by a test) if the height is a symbol rather than a magic `/ 2.0`.
// Re-authoring a sprite at a new size then moves the prop instead of quietly
// leaving it hovering.

const DESK_SIZE: Vec2 = Vec2::new(120.0, 48.0);
const DEV_SIZE: Vec2 = Vec2::new(24.0, 32.0);
const CHAIR_SIZE: Vec2 = Vec2::new(28.0, 36.0);
const MONITOR_SIZE: Vec2 = Vec2::new(40.0, 36.0);
const LAMP_SIZE: Vec2 = Vec2::new(20.0, 28.0);
const MUG_SIZE: Vec2 = Vec2::new(10.0, 10.0);
const BOOKS_SIZE: Vec2 = Vec2::new(32.0, 24.0);
const RUG_SIZE: Vec2 = Vec2::new(96.0, 32.0);
const PLANT_SIZE: Vec2 = Vec2::new(24.0, 32.0);

// ---------------------------------------------------------------------------
// Room geometry
// ---------------------------------------------------------------------------

/// Authored room width in pixels — `room_bg.png` is 320x200 and defines the
/// world-space coordinate system every layout constant below is expressed in
/// (x in `-160..160`, y in `-100..100`, y up, origin at the room's centre).
const ROOM_W: f32 = 320.0;
/// Authored room height in pixels (see [`ROOM_W`]).
const ROOM_H: f32 = 200.0;

/// The one and only upscale factor. Art-direction non-negotiable #1 allows
/// integer factors only, so this must stay a whole number: at 2 the 320x200
/// room fills a 640x400 window exactly, one texel per 2x2 block of screen
/// pixels, with no resampling.
const PIXEL_SCALE: f32 = 2.0;

/// World-space y of the floor plane's *back* edge in `room_bg.png` — the line
/// anything standing at the back of the room puts its bottom edge on.
///
/// Measured from the background rather than guessed: rows 128-135 of
/// `room_bg.png` are the skirting board (wall trim) and row 136 is the first
/// floorboard row, so the boards start at `100 - 136 = -36`. The previous
/// `-30` sat *inside* the skirting, which is why floor props looked pasted
/// onto the wall trim rather than standing on the boards.
///
/// Everything on the floor is positioned from this line, so re-drawing the
/// background with a higher or lower horizon stays a one-constant fix here
/// instead of a hunt through fourteen `Transform`s.
const FLOOR_LINE_Y: f32 = -36.0;

// ---------------------------------------------------------------------------
// Layer order (docs/art-direction.md § "Layering")
// ---------------------------------------------------------------------------
//
//   room_bg -> rug -> books -> chair -> desk -> dev_* -> monitor_* -> lamp
//           -> mug -> plant
//
// Sprites at equal z draw in an unspecified order in Bevy's 2D pipeline, so
// the layering is enforced by giving every layer its own integer z rather
// than by spawn order. One unit apart is plenty: the 2D orthographic
// projection spans z -1000..1000.

const Z_ROOM_BG: f32 = 0.0;
const Z_RUG: f32 = 1.0;
const Z_BOOKS: f32 = 2.0;
const Z_CHAIR: f32 = 3.0;
const Z_DESK: f32 = 4.0;
const Z_DEV: f32 = 5.0;
const Z_MONITOR: f32 = 6.0;
const Z_LAMP: f32 = 7.0;
const Z_MUG: f32 = 8.0;
const Z_PLANT: f32 = 9.0;

// ---------------------------------------------------------------------------
// Composition
// ---------------------------------------------------------------------------
//
// Sprites are centred on their `Transform`, so every constant below is a
// CENTRE — and the only way to stop a prop floating is to derive that centre
// from the surface it rests on instead of nudging it until it looks close:
//
//     centre_y = surface_y + own_height / 2
//
// Nothing here is hand-tuned; each constant names the surface it came from.
// Every value stays a whole pixel — a half-pixel placement re-samples the
// texture and defeats the nearest-neighbour sampler (non-negotiable #1) — and
// that falls out of the arithmetic on its own because all the sprite heights
// are even.
//
// Two surfaces matter and everything hangs off them:
//
//   * [`FLOOR_LINE_Y`], the back edge of the floor plane — the chair, the book
//     stack and the rug are placed against it;
//   * [`DESK_TOP_Y`], the desk's surface — the monitor, lamp, mug and the
//     plant upgrade are placed against it.
//
// The room is a flat elevation, so "further from the viewer" is "higher up the
// image". That is why the desk's feet land *below* [`FLOOR_LINE_Y`]: the desk
// stands in front of the chair (it occludes it — see the layer order), so its
// floor contact is nearer the viewer and therefore lower down. Depth ordering
// and vertical placement have to agree or the room stops reading as a room.
//
// The composition is off-centre to the right, which is what makes the
// character read as *seated at* the desk: chair behind, desk in front, monitor
// to the character's right.

/// How far the chair's seat is above the chair's own floor contact.
///
/// `chair.png` draws the seat slab at rows 21-25 of its 36, so the seat's top
/// is 15 px above the sprite's bottom edge (the wheels).
const CHAIR_SEAT_ABOVE_FLOOR: f32 = 15.0;

/// How far the character's typing hands are above the character's own bottom
/// edge.
///
/// The hands occupy rows 19-24 of `dev_type_*.png`'s 32, so the lowest hand
/// pixel is 8 px above the sprite's bottom edge. This is the number that
/// decides whether the character types *on* the desk or in mid-air.
const DEV_HANDS_ABOVE_BOTTOM: f32 = 8.0;

/// The desk's surface: the line every object *on* the desk rests on, and the
/// line that occludes the character's lower body.
///
/// Derived rather than chosen. The character sits on the chair — bottom edge
/// on the seat, which is [`CHAIR_SEAT_ABOVE_FLOOR`] above the floor — and
/// types with their hands [`DEV_HANDS_ABOVE_BOTTOM`] above that bottom edge,
/// so the surface has to be exactly there. Floor -> seat -> hands -> desk top
/// is the whole chain; change any link and the desk follows.
const DESK_TOP_Y: f32 = FLOOR_LINE_Y + CHAIR_SEAT_ABOVE_FLOOR + DEV_HANDS_ABOVE_BOTTOM;

/// Desk centre.
///
/// y: `desk.png` draws its surface slab flush with the sprite's top edge
/// (rows 0-9) and its two legs below it, so the centre is exactly half a
/// sprite below [`DESK_TOP_Y`]. That puts the feet 25 px in front of (below)
/// the chair's wheels — the depth offset the layer order already claims.
///
/// x: off-centre right, so that the lamp standing at the desk's right end
/// lands under the warm light `room_bg.png` already paints on the wall and
/// pools on the floor (centred on x = +73). The empty left third of the room
/// is carried by the window and the book stack instead.
const DESK_POS: Vec2 = Vec2::new(24.0, DESK_TOP_Y - DESK_SIZE.y / 2.0);

/// Character centre.
///
/// y: their bottom edge sits [`DEV_HANDS_ABOVE_BOTTOM`] below the desk
/// surface, which is what lands the hands *on* it; the desk slab then hides
/// rows 24-31 — everything from the waist down — which is the single cue that
/// reads as "seated at the desk" (docs/art-direction.md § Layering).
///
/// x: just left of the desk's middle, so the character's right side faces the
/// monitor with the mug in the gap between them.
const DEV_POS: Vec2 = Vec2::new(-6.0, DESK_TOP_Y - DEV_HANDS_ABOVE_BOTTOM + DEV_SIZE.y / 2.0);

/// Chair centre — wheels on the floor line, 6 px left of the character so the
/// backrest (which reaches `FLOOR_LINE_Y + 35`, i.e. 12 px clear of the desk
/// surface) shows past their shoulder instead of hiding behind their torso.
/// Its seat, visible in the gap between the desk's legs, is exactly where the
/// character's bottom edge is: they sit *on* it.
const CHAIR_POS: Vec2 = Vec2::new(DEV_POS.x - 6.0, FLOOR_LINE_Y + CHAIR_SIZE.y / 2.0);

/// Monitor centre — bezel standing on the desk surface, to the character's
/// right (they face right, toward it).
const MONITOR_POS: Vec2 = Vec2::new(36.0, DESK_TOP_Y + MONITOR_SIZE.y / 2.0);

/// Desk lamp centre — base on the desk surface at its right end, directly
/// under the glow `room_bg.png` paints on the wall (x 20..136, centred on
/// x = +73).
///
/// WHY the right end and not the left, where it used to be: a lamp that is not
/// standing under its own light pool is the loudest possible "this scene was
/// assembled rather than composed", and it was also sitting on top of the
/// chair, hiding it completely.
const LAMP_POS: Vec2 = Vec2::new(72.0, DESK_TOP_Y + LAMP_SIZE.y / 2.0);

/// Mug centre — on the desk surface in the gap between the character's hands
/// (which reach x = +3) and the monitor's left edge (x = +16).
const MUG_POS: Vec2 = Vec2::new(9.0, DESK_TOP_Y + MUG_SIZE.y / 2.0);

/// Book stack centre — standing on the floor line against the wall below the
/// window, filling the otherwise empty left third of the room.
const BOOKS_POS: Vec2 = Vec2::new(-104.0, FLOOR_LINE_Y + BOOKS_SIZE.y / 2.0);

/// Rug centre.
///
/// A rug *lies on* the floor rather than standing on it, so it is its far
/// (top) edge that meets [`FLOOR_LINE_Y`] and the sprite extends from there
/// toward the viewer — the one prop whose height is subtracted rather than
/// added. Centred on the desk so the desk's legs visibly cross it
/// (`Z_DESK > Z_RUG`), which is what grounds the desk on the floor.
const RUG_POS: Vec2 = Vec2::new(DESK_POS.x, FLOOR_LINE_Y - RUG_SIZE.y / 2.0);

/// Plant centre — the desk upgrade, standing on the desk surface at its left
/// end, immediately beside the character.
///
/// WHY here: it used to sit on the floor to the right of the desk, which was
/// both wrong (the manifest calls it the *desk* upgrade) and invisible in
/// practice — green-and-terracotta against the brown floor boards, with its
/// lower half behind the HUD bar. The desk's left end is the only stretch of
/// surface with 24 px of clear width whose background is bare wall, so the
/// plant is silhouetted against the wall at the character's eye level: the
/// frame the wallet crosses [`crate::DESK_UPGRADE_COST`], a new object
/// unmistakably appears on the desk.
const PLANT_POS: Vec2 = Vec2::new(-24.0, DESK_TOP_Y + PLANT_SIZE.y / 2.0);

/// Frames per second of the two-frame typing loop. The manifest's brief is
/// "4-6 times/sec"; 5 sits in the middle and, being the reciprocal of a
/// round 0.2 s, keeps the timer duration exact in binary floating point.
const TYPING_FPS: f32 = 5.0;

// ---------------------------------------------------------------------------
// Components
// ---------------------------------------------------------------------------

/// Marker on the character sprite whose image [`character_anim_system`] swaps
/// from `Developer.mood`.
///
/// Note this is a *separate entity* from the `Developer` the rest of the game
/// queries: lib.rs puts that component on the desk's UI root and several
/// systems call `.single()` on it, so spawning a second `Developer` here
/// would break them. The scene only ever *reads* the mood.
#[derive(Component)]
struct CharacterSprite;

/// Marker on the monitor sprite, swapped between the lit and dim images by
/// [`monitor_power_system`].
#[derive(Component)]
struct MonitorSprite;

/// Marker on the plant sprite — kept for the scene's own layout tests.
/// Visibility is driven by lib.rs's `desk_upgrade_system` (the plant also
/// carries `crate::DeskUpgradeProp`): one owner, so the sprite can never
/// disagree with the HUD about whether the upgrade is affordable.
///
/// Previously shown and hidden by
/// [`plant_visibility_system`] at the same wallet threshold `lib.rs`'s
/// `desk_upgrade_system` uses for the placeholder prop.
#[derive(Component)]
struct PlantSprite;

// ---------------------------------------------------------------------------
// Resources
// ---------------------------------------------------------------------------

/// The four character frames, loaded once at startup and swapped per mood.
///
/// Held as handles rather than re-requesting paths from the `AssetServer`
/// every frame: `load` on an already-loaded path is cheap but not free, and
/// keeping the handles alive is also what stops the asset server from
/// unloading a frame the moment the mood stops using it.
#[derive(Resource)]
struct CharacterFrames {
    idle: Handle<Image>,
    type_a: Handle<Image>,
    type_b: Handle<Image>,
    coffee: Handle<Image>,
}

/// The monitor's lit and dim images (same bezel, different screen).
#[derive(Resource)]
struct MonitorFrames {
    on: Handle<Image>,
    off: Handle<Image>,
}

/// The typing loop's cadence.
///
/// A repeating [`Timer`] rather than a per-frame flip, because the animation
/// must run at [`TYPING_FPS`] on a 30 Hz laptop and on a 144 Hz monitor
/// alike — a frame-rate-tied toggle would strobe on the latter.
#[derive(Resource)]
struct TypingAnimation {
    timer: Timer,
    /// Which of the two typing frames is currently shown; flipped on every
    /// timer completion.
    on_frame_b: bool,
}

impl Default for TypingAnimation {
    fn default() -> Self {
        Self {
            timer: Timer::from_seconds(1.0 / TYPING_FPS, TimerMode::Repeating),
            on_frame_b: false,
        }
    }
}

// ---------------------------------------------------------------------------
// Plugin
// ---------------------------------------------------------------------------

/// Renders the room as real pixel-art sprites and keeps three of them in
/// sync with game state (character frame, monitor power, plant unlock).
///
/// Requires the app to have been built with
/// `ImagePlugin::default_nearest()` — see the `WIRING REQUIRED` block at the
/// top of this module.
pub struct ScenePlugin;

impl Plugin for ScenePlugin {
    fn build(&self, app: &mut App) {
        app.init_resource::<TypingAnimation>()
            .add_systems(Startup, spawn_room)
            // PostStartup, not Startup: the camera system needs to know
            // whether lib.rs's `setup_scene` already spawned a `Camera2d`,
            // and two systems in the same schedule have no guaranteed order
            // across plugins. Everything in `Startup` has run by the time
            // `PostStartup` begins.
            .add_systems(PostStartup, setup_pixel_camera)
            .add_systems(
                Update,
                // One explicit chain, matching lib.rs's convention: these
                // three are independent (they touch disjoint entities), but
                // an unordered tuple would leave the schedule free to
                // reshuffle them between builds, which makes a rendering
                // bug harder to reproduce than it needs to be.
                (character_anim_system, monitor_power_system).chain(),
            );
    }
}

// ---------------------------------------------------------------------------
// Systems — setup
// ---------------------------------------------------------------------------

/// Installs the pixel-perfect orthographic projection and sizes the window
/// to an exact integer multiple of the room.
///
/// The projection uses `ScalingMode::WindowSize` with
/// `scale = 1 / PIXEL_SCALE` rather than
/// `ScalingMode::Fixed { width: 320, height: 200 }`. Both give a 2x room in
/// a 640x400 window, but `Fixed` *stretches* the 320x200 area to whatever
/// the viewport becomes, so the first window resize produces a fractional
/// scale and blurred, unevenly-sized pixels. With `WindowSize` the factor
/// stays pinned at exactly [`PIXEL_SCALE`] and a resize reveals or crops
/// room around the edges instead — crispness is the non-negotiable, the
/// exact framing is not.
///
/// (Caveat worth knowing: `WindowSize` works in the viewport's *logical*
/// pixels, so on a HiDPI display the on-screen factor is
/// `PIXEL_SCALE * window_scale_factor`. That stays integral for the usual
/// integral scale factors; a fractional desktop scale (1.5x) is the one case
/// that reintroduces resampling, and the fix belongs in `WindowPlugin` as a
/// `scale_factor_override`, not here.)
fn setup_pixel_camera(
    mut commands: Commands,
    existing: Query<Entity, With<Camera2d>>,
    mut windows: Query<&mut Window>,
) {
    let projection = Projection::Orthographic(OrthographicProjection {
        scale: 1.0 / PIXEL_SCALE,
        ..OrthographicProjection::default_2d()
    });

    // Adopt the camera lib.rs already spawned for the UI if there is one
    // (see WIRING REQUIRED #4) — a second camera on the same window renders
    // in an ambiguous order.
    if let Some(camera) = existing.iter().next() {
        commands.entity(camera).insert(projection);
    } else {
        // Transform at the origin: the room is authored centred on (0, 0),
        // so the default camera position already centres the composition.
        commands.spawn((Camera2d, projection, Transform::default()));
    }

    // Only until lib.rs configures `WindowPlugin` (WIRING REQUIRED #6).
    // Skipped when the size already matches so a user's manual resize isn't
    // fought over every launch.
    let want = Vec2::new(ROOM_W * PIXEL_SCALE, ROOM_H * PIXEL_SCALE);
    for mut window in &mut windows {
        if window.resolution.size() != want {
            window.resolution.set(want.x, want.y);
        }
    }
}

/// Spawns the whole room, back to front, one sprite per manifest entry.
///
/// Every image is requested through the `AssetServer` here — `load` is
/// non-blocking and returns a handle immediately, so a sprite whose PNG does
/// not exist yet simply renders nothing and logs an asset error rather than
/// panicking. That is deliberate: the art is generated separately from the
/// code, and a missing file must not take the game down.
fn spawn_room(mut commands: Commands, assets: Res<AssetServer>) {
    // Static props: loaded, spawned and forgotten — nothing swaps them
    // later, so the sprite component is the only handle owner needed.
    commands.spawn(prop(&assets, SPRITE_ROOM_BG, Vec2::ZERO, Z_ROOM_BG));
    commands.spawn(prop(&assets, SPRITE_RUG, RUG_POS, Z_RUG));
    commands.spawn(prop(&assets, SPRITE_BOOKS, BOOKS_POS, Z_BOOKS));
    commands.spawn(prop(&assets, SPRITE_CHAIR, CHAIR_POS, Z_CHAIR));
    commands.spawn(prop(&assets, SPRITE_DESK, DESK_POS, Z_DESK));
    commands.spawn(prop(&assets, SPRITE_LAMP, LAMP_POS, Z_LAMP));
    commands.spawn(prop(&assets, SPRITE_MUG, MUG_POS, Z_MUG));

    // Animated / state-driven sprites: each gets a marker so its Update
    // system can find it, and the frames it swaps between go in a resource.
    let frames = CharacterFrames {
        idle: assets.load(SPRITE_DEV_IDLE),
        type_a: assets.load(SPRITE_DEV_TYPE_A),
        type_b: assets.load(SPRITE_DEV_TYPE_B),
        coffee: assets.load(SPRITE_DEV_COFFEE),
    };
    // Start on the idle frame: `Developer.mood` starts `Mood::Idle` (see
    // `spawn_desk` in lib.rs), so this matches the game state on frame one
    // instead of flickering to it on frame two.
    commands.spawn((
        Sprite::from_image(frames.idle.clone()),
        Transform::from_xyz(DEV_POS.x, DEV_POS.y, Z_DEV),
        CharacterSprite,
    ));
    commands.insert_resource(frames);

    let monitor = MonitorFrames {
        on: assets.load(SPRITE_MONITOR_ON),
        off: assets.load(SPRITE_MONITOR_OFF),
    };
    commands.spawn((
        Sprite::from_image(monitor.off.clone()),
        Transform::from_xyz(MONITOR_POS.x, MONITOR_POS.y, Z_MONITOR),
        MonitorSprite,
    ));
    commands.insert_resource(monitor);

    // The plant is spawned once and toggled by `Visibility`, never
    // despawned — the same choice (and the same reasoning about stable
    // entity identity) as lib.rs's `desk_upgrade_system`. It starts hidden
    // because a wallet below the threshold is the common case on frame one;
    // `plant_visibility_system` corrects it immediately for a restored save
    // that already qualifies.
    commands.spawn((
        Sprite::from_image(assets.load(SPRITE_PLANT)),
        Transform::from_xyz(PLANT_POS.x, PLANT_POS.y, Z_PLANT),
        Visibility::Visible, // TEMP-VERIFY
        PlantSprite,
        crate::DeskUpgradeProp,
    ));
}

/// A plain, never-changing sprite at a room-space position and layer.
///
/// Factored out because the seven static props differ *only* in path,
/// position and z, and spelling out `Sprite`/`Transform` seven times invites
/// exactly the kind of copy-paste z-value mistake the layer constants exist
/// to prevent.
///
/// `path` is `&'static str` because `AssetServer::load` needs an
/// `AssetPath<'static>`; every caller passes one of the manifest consts, so
/// this costs nothing and avoids an allocation per prop.
fn prop(assets: &AssetServer, path: &'static str, pos: Vec2, z: f32) -> (Sprite, Transform) {
    (
        Sprite::from_image(assets.load(path)),
        Transform::from_xyz(pos.x, pos.y, z),
    )
}

// ---------------------------------------------------------------------------
// Systems — per-frame state → sprite
// ---------------------------------------------------------------------------

/// `Developer.mood` → the character's sprite image.
///
/// * [`Mood::Coding`] alternates `dev_type_a` / `dev_type_b` at
///   [`TYPING_FPS`], driven by the [`TypingAnimation`] timer so the cadence
///   is independent of the frame rate;
/// * [`Mood::Idle`] holds `dev_idle`;
/// * [`Mood::OnBreak`] holds `dev_coffee`.
///
/// The timer is ticked unconditionally — including while idle — so the loop
/// has no phase memory to accumulate: returning to `Coding` picks up the
/// running cadence rather than restarting mid-keystroke. Assignment to
/// `sprite.image` is guarded by an equality check because writing a `&mut`
/// component every frame marks it changed, and change detection is what
/// downstream extraction uses to decide what work to redo.
fn character_anim_system(
    time: Res<Time>,
    mut anim: ResMut<TypingAnimation>,
    frames: Res<CharacterFrames>,
    developer: Query<&Developer>,
    mut sprite: Query<&mut Sprite, With<CharacterSprite>>,
) {
    // Tick first: the phase must advance even on the frames where the mood
    // is not `Coding`.
    if anim.timer.tick(time.delta()).just_finished() {
        anim.on_frame_b = !anim.on_frame_b;
    }

    // No developer (a fixture that never built the desk) means nothing to
    // mirror — leave the sprite as it is rather than guessing a mood.
    let Ok(dev) = developer.single() else {
        return;
    };

    let wanted = match dev.mood {
        Mood::Coding => {
            if anim.on_frame_b {
                &frames.type_b
            } else {
                &frames.type_a
            }
        }
        Mood::Idle => &frames.idle,
        Mood::OnBreak => &frames.coffee,
    };

    for mut sprite in &mut sprite {
        if sprite.image != *wanted {
            sprite.image = wanted.clone();
        }
    }
}

/// `Developer.mood` → the monitor's sprite image: `monitor_on` (glowing
/// teal) while [`Mood::Coding`], `monitor_off` (dim screen, same bezel)
/// otherwise.
///
/// A screen that goes dark the moment typing stops is the cheapest possible
/// signal that the room is watching real activity, and it costs one image
/// swap — no shader, no light, nothing in the render graph.
fn monitor_power_system(
    frames: Res<MonitorFrames>,
    developer: Query<&Developer>,
    mut sprite: Query<&mut Sprite, With<MonitorSprite>>,
) {
    let Ok(dev) = developer.single() else {
        return;
    };
    let wanted = if dev.mood == Mood::Coding {
        &frames.on
    } else {
        &frames.off
    };
    for mut sprite in &mut sprite {
        // Same change-detection guard as `character_anim_system`.
        if sprite.image != *wanted {
            sprite.image = wanted.clone();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The manifest's layering order must be reflected in strictly
    /// increasing z values — the composition is only correct because of it
    /// (the desk has to occlude the character's legs, the plant has to sit
    /// in front of everything), and a reordered constant would be a silent
    /// visual regression no compiler check catches.
    #[test]
    fn layer_z_values_follow_the_manifest_order() {
        let order = [
            Z_ROOM_BG, Z_RUG, Z_BOOKS, Z_CHAIR, Z_DESK, Z_DEV, Z_MONITOR, Z_LAMP, Z_MUG, Z_PLANT,
        ];
        for pair in order.windows(2) {
            assert!(
                pair[0] < pair[1],
                "layer z values must strictly increase back-to-front, got {pair:?}"
            );
        }
    }

    /// The character must straddle the desk's surface line: feet below it
    /// (occluded, i.e. seated *at* the desk) and head above it (visible).
    #[test]
    fn the_desk_occludes_the_characters_lower_body() {
        let feet = DEV_POS.y - DEV_SIZE.y / 2.0;
        let head = DEV_POS.y + DEV_SIZE.y / 2.0;
        assert!(feet < DESK_TOP_Y, "the desk must hide the character's feet");
        assert!(
            head > DESK_TOP_Y,
            "the character's head must clear the desk"
        );
        // Not just *any* straddle: the surface has to cut the sprite below the
        // hands, or the desk edge saws through the character's arms.
        assert!(
            DESK_TOP_Y < feet + DEV_SIZE.y / 2.0,
            "the desk must not hide the character's upper body"
        );
    }

    /// The bug class this whole section exists to prevent: a prop whose centre
    /// was nudged until it "looked about right" and ends up hovering a few
    /// pixels off the surface it is supposed to be sitting on. A sprite is
    /// centred on its `Transform`, so resting on a surface at `y = s` is
    /// exactly `centre.y - height / 2 == s` — an equality, not a tolerance,
    /// and exact in binary floating point because every sprite height here is
    /// an even whole number.
    #[test]
    fn every_prop_rests_on_the_surface_it_belongs_to() {
        // Standing on the desk surface.
        for (name, pos, size) in [
            ("monitor", MONITOR_POS, MONITOR_SIZE),
            ("lamp", LAMP_POS, LAMP_SIZE),
            ("mug", MUG_POS, MUG_SIZE),
            ("plant", PLANT_POS, PLANT_SIZE),
        ] {
            assert_eq!(
                pos.y - size.y / 2.0,
                DESK_TOP_Y,
                "{name} must rest on the desk surface, not hover above it"
            );
        }

        // Standing on the floor plane's back edge.
        for (name, pos, size) in [
            ("chair", CHAIR_POS, CHAIR_SIZE),
            ("books", BOOKS_POS, BOOKS_SIZE),
        ] {
            assert_eq!(
                pos.y - size.y / 2.0,
                FLOOR_LINE_Y,
                "{name} must stand on the floor line"
            );
        }

        // The rug *lies on* the floor: its far edge is the floor line and the
        // sprite runs toward the viewer from there, so it is the TOP edge that
        // has to match — sitting it "on" the line like a standing prop would
        // paste it up the wall over the skirting.
        assert_eq!(
            RUG_POS.y + RUG_SIZE.y / 2.0,
            FLOOR_LINE_Y,
            "the rug must meet the floor line with its far edge, not cross it"
        );

        // The desk offers a surface, so both of its edges are load-bearing.
        assert_eq!(
            DESK_POS.y + DESK_SIZE.y / 2.0,
            DESK_TOP_Y,
            "DESK_TOP_Y must be the desk sprite's actual top edge"
        );
        // Its feet are in front of (below) the chair's, because it occludes
        // the chair: in a flat elevation, nearer is lower.
        let desk_feet = DESK_POS.y - DESK_SIZE.y / 2.0;
        assert!(
            desk_feet < FLOOR_LINE_Y,
            "the desk stands forward of the floor's back edge"
        );
        // `const` blocks: both sides are compile-time constants, so this is a
        // static assertion and clippy rightly refuses to see it as a runtime
        // check.
        const {
            assert!(
                Z_DESK > Z_CHAIR,
                "the desk's lower floor contact only reads as depth if it also \
                 draws in front of the chair"
            )
        };

        // Floor -> seat -> hands -> desk top: the chain that makes the
        // character look like they are working at the desk rather than posed
        // near it.
        let seat_top = CHAIR_POS.y - CHAIR_SIZE.y / 2.0 + CHAIR_SEAT_ABOVE_FLOOR;
        let dev_bottom = DEV_POS.y - DEV_SIZE.y / 2.0;
        assert_eq!(
            dev_bottom, seat_top,
            "the character must sit on the chair's seat"
        );
        assert_eq!(
            dev_bottom + DEV_HANDS_ABOVE_BOTTOM,
            DESK_TOP_Y,
            "the character's hands must land on the desk surface"
        );
    }

    /// Framing: lib.rs pins a HUD bar to the bottom of the window, which eats
    /// the bottom rows of the room. A prop placed down there is a prop nobody
    /// ever sees — half of why the old floor-level plant was invisible even
    /// when the upgrade was unlocked — so the readable props must all clear it.
    #[test]
    fn the_readable_props_clear_the_hud_bar() {
        // The HUD is `bevy_ui` and its 88 logical pixels are a private layout
        // detail of lib.rs, so the number is repeated here rather than shared.
        // At PIXEL_SCALE that hides the bottom 44 rows of the room.
        const HUD_H_LOGICAL: f32 = 88.0;
        const HUD_TOP_Y: f32 = -ROOM_H / 2.0 + HUD_H_LOGICAL / PIXEL_SCALE;

        for (name, pos, size) in [
            ("dev", DEV_POS, DEV_SIZE),
            ("chair", CHAIR_POS, CHAIR_SIZE),
            ("monitor", MONITOR_POS, MONITOR_SIZE),
            ("lamp", LAMP_POS, LAMP_SIZE),
            ("mug", MUG_POS, MUG_SIZE),
            ("books", BOOKS_POS, BOOKS_SIZE),
            ("plant", PLANT_POS, PLANT_SIZE),
        ] {
            assert!(
                pos.y - size.y / 2.0 >= HUD_TOP_Y,
                "{name} must be fully above the HUD bar to be readable"
            );
        }
        const {
            assert!(
                DESK_TOP_Y > HUD_TOP_Y,
                "the desk surface must be above the HUD bar"
            )
        };
        // Deliberately not asserted for the rug or the desk's legs: both run
        // *under* the HUD by design, because the floor plane continues behind
        // it. Their tops are what matter and those are checked above.
    }

    /// Art-direction non-negotiable #1: integer upscale only, and whole-pixel
    /// placement (a half-pixel offset resamples the texture and undoes the
    /// nearest-neighbour sampler).
    #[test]
    fn the_composition_is_pixel_aligned() {
        assert_eq!(PIXEL_SCALE, PIXEL_SCALE.trunc(), "upscale must be integral");
        for (name, y) in [("floor line", FLOOR_LINE_Y), ("desk surface", DESK_TOP_Y)] {
            assert_eq!(y, y.trunc(), "the {name} must fall on a whole pixel");
        }
        for (name, pos) in [
            ("desk", DESK_POS),
            ("dev", DEV_POS),
            ("chair", CHAIR_POS),
            ("monitor", MONITOR_POS),
            ("lamp", LAMP_POS),
            ("mug", MUG_POS),
            ("books", BOOKS_POS),
            ("rug", RUG_POS),
            ("plant", PLANT_POS),
        ] {
            assert_eq!(pos.x, pos.x.trunc(), "{name} x must be a whole pixel");
            assert_eq!(pos.y, pos.y.trunc(), "{name} y must be a whole pixel");
        }
    }

    /// Every prop must sit inside the authored 320x200 background, or it
    /// hangs off the room into the window's letterbox.
    #[test]
    fn every_prop_is_inside_the_room() {
        for (name, pos, size) in [
            ("desk", DESK_POS, DESK_SIZE),
            ("dev", DEV_POS, DEV_SIZE),
            ("chair", CHAIR_POS, CHAIR_SIZE),
            ("monitor", MONITOR_POS, MONITOR_SIZE),
            ("lamp", LAMP_POS, LAMP_SIZE),
            ("mug", MUG_POS, MUG_SIZE),
            ("books", BOOKS_POS, BOOKS_SIZE),
            ("rug", RUG_POS, RUG_SIZE),
            ("plant", PLANT_POS, PLANT_SIZE),
        ] {
            let half = size / 2.0;
            assert!(
                pos.x - half.x >= -ROOM_W / 2.0 && pos.x + half.x <= ROOM_W / 2.0,
                "{name} overflows the room horizontally"
            );
            assert!(
                pos.y - half.y >= -ROOM_H / 2.0 && pos.y + half.y <= ROOM_H / 2.0,
                "{name} overflows the room vertically"
            );
        }
    }

    /// The typing loop must land in the manifest's 4-6 fps window: slower
    /// reads as a stutter, faster as a vibration.
    #[test]
    fn typing_cadence_is_within_the_art_direction_window() {
        assert!((4.0..=6.0).contains(&TYPING_FPS));
        let anim = TypingAnimation::default();
        assert_eq!(anim.timer.duration().as_secs_f32(), 1.0 / TYPING_FPS);
        assert!(anim.timer.mode() == TimerMode::Repeating);
    }
}
