//! The pixel-art room: real sprites, integer-upscaled, replacing the M1
//! placeholder `Node` rects (docs/art-direction.md).
//!
//! [`ScenePlugin`] owns everything *world-space* about the scene — the
//! camera's integer upscale, the back-to-front sprite stack, and the
//! per-frame swaps that make the room react to game state:
//!
//! * the character's frame follows [`Mood`] (typing loop / idle / coffee),
//! * the monitor is lit while `Mood::Coding` and dim otherwise,
//! * every [`crate::UPGRADE_TRACKS`] track has an anchored slot whose
//!   `Visibility` [`upgrade_render_system`] derives from `OwnedUpgrades`
//!   each frame (docs/upgrade-design.md "Scene contract") — the v0.2 plant
//!   prop is now just `desk_decor` tier 1, rendered through this same path.
//!
//! It deliberately owns *nothing* about the HUD: the bottom bar stays in
//! `lib.rs` as `bevy_ui` `Node`s, which are laid out from the window's
//! logical size and are unaffected by the camera projection this plugin
//! installs (`bevy_ui`'s layout reads the render target's scale factor and
//! physical size, never the camera's `Projection`) — so the HUD keeps
//! rendering at 1:1 on top of a 2x-upscaled room.

// ---------------------------------------------------------------------------
// WIRING REQUIRED (lib.rs) — WIRED: lib.rs declares `pub mod scene;` and adds `ScenePlugin` in both `run()` and `build_app_with_seed()`.
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
const SPRITE_MUG: &str = "mug.png"; // 10x10
const SPRITE_BOOKS: &str = "books.png"; // 32x24
const SPRITE_LAMP: &str = "lamp.png"; // 20x28
const SPRITE_RUG: &str = "rug.png"; // 96x32

// `dev_sleep.png` (24x32) is in the manifest but is deliberately NOT loaded:
// [`Mood`] has exactly three variants (`Idle` / `Coding` / `OnBreak`) and
// none of them means "asleep". Loading a handle no system ever reads would
// be a dead field under `-D warnings` and, worse, would imply a fourth mood
// exists. When a `Mood::Sleeping` lands, add it to [`CharacterFrames`].
//
// The v0.3 upgrade sprites (`keyboard_t1.png`, `plant.png`, `cat.png`, …)
// are NOT listed here: `crate::UPGRADE_TRACKS` is their one source of truth
// (docs/upgrade-design.md "Data-driven from one table"), and
// `spawn_upgrade_slots` loads straight from that table's `sprite` field —
// duplicating the filenames as consts here would be exactly the drift the
// table exists to prevent.

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
/// `desk_decor` tier 1's footprint (docs/upgrade-design.md art manifest:
/// "plant.png … 24x32" — the v0.2 plant, unchanged).
const PLANT_SIZE: Vec2 = Vec2::new(24.0, 32.0);

// v0.3 upgrade-slot footprints (docs/upgrade-design.md art manifest). Each
// track's tiers share ONE footprint here even where the manifest's later
// tiers differ slightly in size (e.g. `chair_t2` is 2px taller than
// `chair_t1`) — the anchor is derived once per track/layer, not once per
// tier, so a later tier centred on it may sit a pixel or two off the exact
// contact line until the real art lands and the constant is revisited. This
// is the same tolerance the shipped `monitor_on`/`monitor_off` swap already
// relies on (both exactly `MONITOR_SIZE`), just extended to tiers that are
// not authored at matching sizes.
/// `keyboard_t1.png`/`keyboard_t2.png` — both 20x8 per the manifest, so no
/// cross-tier approximation applies here.
const KEYBOARD_SIZE: Vec2 = Vec2::new(20.0, 8.0);
/// `mouse_t1.png` ("mouse + pad", the larger footprint) — `mouse_t2.png`
/// (8x6, no pad) is smaller and centres within this same slot.
const MOUSE_SIZE: Vec2 = Vec2::new(12.0, 8.0);
/// `duck.png` — the `desk_decor` tier-2 addition that joins the plant
/// rather than replacing it (see `UpgradeTrackKind::Accumulate`).
const DUCK_SIZE: Vec2 = Vec2::new(8.0, 8.0);
/// `poster.png` (tier 1's footprint; `shelf.png` at 40x22 centres on the
/// same anchor).
const WALL_SIZE: Vec2 = Vec2::new(24.0, 30.0);
/// `cat.png`.
const CAT_SIZE: Vec2 = Vec2::new(20.0, 12.0);

// --- Per-TIER sprite sizes for upgrade tracks whose tiers ship different
// PNG dimensions. One size const per track silently mispositioned four
// tier-2 sprites (review finding: a 1-4px error in a game whose whole
// contact-shadow mechanism is a deliberate 1px correction). A test parses
// each tier PNG's IHDR and asserts it matches the size the anchor math
// assumes, so art and geometry cannot drift apart again.
const MOUSE_T2_SIZE: Vec2 = Vec2::new(8.0, 6.0);
const CHAIR_T2_SIZE: Vec2 = Vec2::new(28.0, 38.0);
const SHELF_SIZE: Vec2 = Vec2::new(40.0, 22.0);
const MONITOR_ULTRA_SIZE: Vec2 = Vec2::new(56.0, 36.0);

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
/// Measured from the background rather than guessed: rows 128-132 of
/// `room_bg.png` are the skirting board (wall trim), row 133 is its contact
/// shadow, and row 134 is the first floorboard row — so the boards start at
/// `100 - 134 = -34`. The previous `-30` sat *inside* the skirting, which is
/// why floor props looked pasted onto the wall trim rather than standing on
/// the boards.
///
/// Everything on the floor is positioned from this line, so re-drawing the
/// background with a higher or lower horizon stays a one-constant fix here
/// instead of a hunt through fourteen `Transform`s.
const FLOOR_LINE_Y: f32 = -34.0;

// ---------------------------------------------------------------------------
// Layer order (docs/art-direction.md § "Layering")
// ---------------------------------------------------------------------------
//
//   room_bg -> rug -> books -> chair -> dev_* -> desk -> monitor_* -> lamp
//           -> mug -> plant
//
// Sprites at equal z draw in an unspecified order in Bevy's 2D pipeline, so
// the layering is enforced by giving every layer its own integer z rather
// than by spawn order. One unit apart is plenty: the 2D orthographic
// projection spans z -1000..1000.
//
// The load-bearing pair is `dev_* -> desk`: the desk draws IN FRONT of the
// character, which is the only reason the character's lower body is hidden and
// they read as seated *at* the desk rather than standing behind it. This file
// used to have it the other way round (`desk` at 4, `dev` at 5) and the result
// was exactly what the manifest's prose warns about — a blue torso painted
// over the tabletop, with the arms in front of a desk their waist was behind.
// Everything that rests ON the desk (`monitor_*`, `lamp`, `mug`, `plant`) has
// to come after the desk in turn, or the tabletop paints over its own props.

const Z_ROOM_BG: f32 = 0.0;
const Z_RUG: f32 = 1.0;
const Z_BOOKS: f32 = 2.0;
const Z_CHAIR: f32 = 3.0;
const Z_DEV: f32 = 4.0;
const Z_DESK: f32 = 5.0;
const Z_MONITOR: f32 = 6.0;
const Z_LAMP: f32 = 7.0;
const Z_MUG: f32 = 8.0;
const Z_PLANT: f32 = 9.0;

// v0.3 upgrade-slot layers. `keyboard`/`mouse`/`duck` are small flat items on
// the desk, same class as the mug — grouped near it. `wall` sits on the
// back wall, behind the chair (like the books it's positioned beside).
// `chair`/`monitor` upgrade overlays sit just in front of the base sprite
// they visually replace, so buying a tier reads as "the same object,
// upgraded" rather than a second object appearing beside it. `pet` sits at
// the rug's near edge — the frontmost surface in the room — so it is drawn
// frontmost of all.
const Z_WALL: f32 = 2.5; // beside the books, behind the chair
const Z_CHAIR_UPGRADE: f32 = 3.5; // over the base chair, still behind the desk/dev
const Z_MONITOR_UPGRADE: f32 = 6.5; // over the base monitor
const Z_KEYBOARD: f32 = 8.1;
const Z_MOUSE: f32 = 8.2;
const Z_DUCK: f32 = 9.1; // beside the plant, not replacing it
const Z_CAT: f32 = 10.0;

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
// Nothing here is hand-tuned; each constant names the surface it came from and
// why its x is where it is. Every value stays a whole pixel — a half-pixel
// placement re-samples the texture and defeats the nearest-neighbour sampler
// (non-negotiable #1) — and that falls out of the arithmetic on its own,
// because every sprite height in the manifest is even.
//
// Two surfaces matter and everything hangs off them:
//
//   * [`FLOOR_LINE_Y`], the back edge of the floor plane — the chair, the book
//     stack and the rug are placed against it;
//   * [`DESK_TOP_Y`], the tabletop's front edge — the monitor, lamp, mug and
//     the plant upgrade are placed against it, and it is the line that hides
//     the character's lower body.
//
// The room is a flat side-on elevation, so "further from the viewer" is
// "higher up the image". That is why the desk's feet land *below*
// [`FLOOR_LINE_Y`]: the desk stands in front of the chair (it occludes it —
// see the layer order), so its floor contact is nearer the viewer and
// therefore lower down. Depth ordering and vertical placement have to agree or
// the room stops reading as a room.
//
// Horizontally the composition is one group off-centre to the right — chair
// behind the character, desk in front, monitor to the character's right — with
// the window (x -120..-43 in `room_bg.png`) and the book stack carrying the
// left third so the room does not look like furniture pushed into one corner.

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

/// Rows of `lamp.png` that are light spill rather than lamp.
///
/// The bottom row (row 27) is the warm pool the bulb throws on whatever the
/// lamp stands on — the art deliberately moved it out of `desk.png` so it
/// follows the lamp. Spill has to be painted *onto* the tabletop, so the lamp
/// is sunk by exactly this much: one row of overlap, which the layer order
/// resolves in the lamp's favour (`Z_LAMP > Z_DESK`).
const LAMP_SPILL_ROWS: f32 = 1.0;

/// Rows of contact shadow baked into the bottom of every OTHER on-desk
/// sprite (monitor, mug, plant, and the v0.3 keyboard/mouse/duck slots) —
/// the same one-row convention [`LAMP_SPILL_ROWS`] already applies to the
/// lamp's light spill, just for a contact shadow instead of a light pool.
/// The lighting pass that baked real shading into these sprites made the
/// gap visible for the first time: without this sink, that bottom row
/// renders one pixel ABOVE the tabletop instead of ON it.
const DESK_CONTACT_SHADOW_ROWS: f32 = 1.0;

/// The desk's surface: the tabletop's front edge (`desk.png` row 0), which is
/// both the line every object *on* the desk rests on and the line that
/// occludes the character's lower body (`Z_DESK > Z_DEV`).
///
/// Derived rather than chosen. The character sits on the chair — bottom edge on
/// the seat, which is [`CHAIR_SEAT_ABOVE_FLOOR`] above the floor — and types
/// with their hands [`DEV_HANDS_ABOVE_BOTTOM`] above that bottom edge, so the
/// tabletop has to be exactly there. Floor -> seat -> hands -> tabletop is the
/// whole chain; change any link and the desk follows.
const DESK_TOP_Y: f32 = FLOOR_LINE_Y + CHAIR_SEAT_ABOVE_FLOOR + DEV_HANDS_ABOVE_BOTTOM;

/// Desk centre.
///
/// y: `desk.png` is a flat elevation whose top row *is* the tabletop's front
/// edge, opaque across all 120 px down to row 9, with the two legs below — so
/// the centre is exactly half a sprite below [`DESK_TOP_Y`]. That also puts its
/// feet 25 px in front of (below) the chair's wheels, the depth offset the
/// layer order already claims.
///
/// x: off-centre right by 24 px. Two reasons, both about what is *behind* the
/// group: it puts the desk's left edge at -36, clear of the window's right
/// frame at -43, so the whole desk group reads against bare wall instead of
/// against window panes; and it leaves the left third to the window and the
/// book stack rather than dead-centring the furniture.
const DESK_POS: Vec2 = Vec2::new(24.0, DESK_TOP_Y - DESK_SIZE.y / 2.0);

/// Character centre.
///
/// y: their bottom edge sits [`DEV_HANDS_ABOVE_BOTTOM`] below the tabletop,
/// which is what lands the hands *on* it. The desk then draws over rows 24-31
/// — everything from the waist down — which is the single cue that reads as
/// "seated at the desk" (docs/art-direction.md § Layering).
///
/// x: just left of the desk's middle, so the character faces right into the
/// monitor with the mug in the gap between them, and the chair's backrest has
/// room to show on their left.
const DEV_POS: Vec2 = Vec2::new(-6.0, DESK_TOP_Y - DEV_HANDS_ABOVE_BOTTOM + DEV_SIZE.y / 2.0);

/// Chair centre — wheels on the floor line, and only 2 px left of the
/// character.
///
/// `chair.png` is drawn front-on (symmetric backrest over a centred column and
/// a three-wheel base), so its column has to sit *under* the character or they
/// read as sitting beside their chair rather than on it — an earlier 6 px
/// offset was already enough for a reviewer to call them "not aligned with the
/// chair". 2 px is the most that can be given up: it is what lets the backrest
/// (which reaches `FLOOR_LINE_Y + 35`, i.e. 12 px clear of the tabletop) show
/// past the character's left shoulder instead of vanishing behind their torso.
/// The seat, visible in the gap between the desk's legs, is exactly where the
/// character's bottom edge is: they sit *on* it.
const CHAIR_POS: Vec2 = Vec2::new(DEV_POS.x - 2.0, FLOOR_LINE_Y + CHAIR_SIZE.y / 2.0);

/// Monitor centre — bezel and stand standing on the tabletop, to the
/// character's right (they face right, toward it), far enough right that the
/// mug fits between them and near enough that the character is clearly working
/// at *this* screen. Sunk by [`DESK_CONTACT_SHADOW_ROWS`] so its contact
/// shadow lands on the tabletop rather than floating above it. The v0.3
/// `monitor` upgrade slot (dual/ultrawide) shares this same centre, drawn
/// just in front of it (`Z_MONITOR_UPGRADE > Z_MONITOR`).
const MONITOR_POS: Vec2 = Vec2::new(
    36.0,
    DESK_TOP_Y - DESK_CONTACT_SHADOW_ROWS + MONITOR_SIZE.y / 2.0,
);

/// Desk lamp centre — at the desk's right end, sunk [`LAMP_SPILL_ROWS`] so its
/// bottom row of spill lands on the tabletop instead of hovering one pixel
/// above it.
///
/// WHY the right end and not the left, where it used to be: on the left it sat
/// exactly on top of the chair (x -40..-20 against the chair's -40..-12) and,
/// drawing later, hid it completely — the chair was in the scene and invisible.
/// On the right it lights the empty end of the desk and balances the plant at
/// the other end.
const LAMP_POS: Vec2 = Vec2::new(72.0, DESK_TOP_Y - LAMP_SPILL_ROWS + LAMP_SIZE.y / 2.0);

/// Mug centre — on the tabletop in the gap between the character's hands
/// (whose pixels reach x = +4) and the monitor's left edge (x = +16). The
/// painted mug is narrower than its 10 px canvas, so it clears both by a
/// pixel or two: close enough to read as *their* mug, touching neither.
/// Sunk by [`DESK_CONTACT_SHADOW_ROWS`], same reason as the monitor.
const MUG_POS: Vec2 = Vec2::new(
    10.0,
    DESK_TOP_Y - DESK_CONTACT_SHADOW_ROWS + MUG_SIZE.y / 2.0,
);

/// Book stack centre — standing on the floor line against the wall below the
/// window, filling the otherwise empty left third of the room.
const BOOKS_POS: Vec2 = Vec2::new(-104.0, FLOOR_LINE_Y + BOOKS_SIZE.y / 2.0);

/// Rug centre.
///
/// A rug *lies on* the floor rather than standing on it, so it is its far (top)
/// edge that meets [`FLOOR_LINE_Y`] and the sprite runs toward the viewer from
/// there — the one prop whose half-height is subtracted rather than added.
/// Centred on the desk so the desk's legs visibly cross it (`Z_DESK > Z_RUG`),
/// which is what grounds the desk on the floor instead of leaving it standing
/// on a bare band of boards.
const RUG_POS: Vec2 = Vec2::new(DESK_POS.x, FLOOR_LINE_Y - RUG_SIZE.y / 2.0);

/// Plant centre — `desk_decor` tier 1, standing on the tabletop at the
/// desk's left end, immediately beside the character.
///
/// WHY here: it used to sit on the floor to the right of the desk, which was
/// both wrong (the manifest calls it a *desk* decoration) and invisible in
/// practice — green-and-terracotta against brown floorboards with its lower
/// half behind the HUD bar. The desk's left end is the one stretch of tabletop
/// with 24 px of clear width whose background is bare wall (the window stops at
/// x -43), so the plant is silhouetted at the character's eye level: the
/// moment it is bought, a new object unmistakably appears on the desk next
/// to them. Sunk by [`DESK_CONTACT_SHADOW_ROWS`], same reason as the
/// monitor/mug.
const PLANT_POS: Vec2 = Vec2::new(
    -24.0,
    DESK_TOP_Y - DESK_CONTACT_SHADOW_ROWS + PLANT_SIZE.y / 2.0,
);

/// Keyboard centre (`keyboard` track) — on the tabletop, directly in front
/// of the character, matching their own x so it reads as *their* keyboard
/// rather than a prop set beside them. Sunk like every other on-desk item.
const KEYBOARD_POS: Vec2 = Vec2::new(
    DEV_POS.x,
    DESK_TOP_Y - DESK_CONTACT_SHADOW_ROWS + KEYBOARD_SIZE.y / 2.0,
);

/// Mouse centre (`mouse` track) — immediately right of the keyboard
/// (docs/upgrade-design.md: "mouse right of the keyboard"), same sunk
/// tabletop line. The desk is small and already carries the mug at this
/// same x band — a player who owns both a keyboard and the pre-existing mug
/// will see them close together; there is no free stretch of tabletop left
/// once every upgrade is bought, and rebalancing the whole desk's item
/// layout is out of scope here (no new art, no repositioning existing
/// shipped props).
const MOUSE_POS: Vec2 = Vec2::new(
    KEYBOARD_POS.x + KEYBOARD_SIZE.x / 2.0 + MOUSE_SIZE.x / 2.0,
    DESK_TOP_Y - DESK_CONTACT_SHADOW_ROWS + MOUSE_SIZE.y / 2.0,
);

/// Rubber duck centre (`desk_decor` tier 2) — beside the plant, not
/// replacing it (`UpgradeTrackKind::Accumulate`): docs/upgrade-design.md
/// "plant + rubber duck".
const DUCK_POS: Vec2 = Vec2::new(
    PLANT_POS.x + PLANT_SIZE.x / 2.0 + DUCK_SIZE.x / 2.0,
    DESK_TOP_Y - DESK_CONTACT_SHADOW_ROWS + DUCK_SIZE.y / 2.0,
);

/// Wall-mounted decoration centre (`wall` track: poster, then shelf).
///
/// Composition note: the room's left half is otherwise a large bare wall
/// with only the book stack on it, while everything else in the scene
/// crowds the right third around the desk. This slot is placed in the gap
/// left-aligned directly above the floor book stack — the classic reading
/// corner — in the wall band between the books' top edge and the window's
/// bottom frame. The previous anchor tried the ~21 px gap between the
/// window's right frame and the chair, but the 40 px shelf cannot fit a
/// 21 px gap: on screen it overlapped both the window frame and the plant
/// (screenshot-verified), which is exactly the crowding this slot was meant
/// to relieve. x left-aligns the shelf with the 32 px book stack
/// (`BOOKS_POS.x + (WALL_SIZE.x - BOOKS_SIZE.x) / 2`); y centres the shelf
/// in the bare band, accepting that the taller poster (30 px) dips a couple
/// of pixels behind the books' tops — depth, not a defect.
const WALL_POS: Vec2 = Vec2::new(BOOKS_POS.x + (WALL_SIZE.x - BOOKS_SIZE.x) / 2.0, 2.0);

/// Sleeping cat centre (`pet` track) — lying ON the rug's visible
/// foreground, bottom row resting on the rug's own horizontal midline
/// (`RUG_POS.y`), to the right of the desk-leg/chair cluster. The previous
/// anchor put it at the floor line at x -20, which is geometrically "on the
/// rug's back edge" but visually BEHIND the chair and desk skirt — only a
/// gold rim peeked out (screenshot-verified). A purchased pet the buyer
/// cannot see is a refund request, so visibility wins over the floor-line
/// convention here; the rug midline is still a real surface derivation,
/// not a hand-tuned magic number.
const CAT_POS: Vec2 = Vec2::new(RUG_POS.x + 20.0, RUG_POS.y + CAT_SIZE.y / 2.0);

/// Tier-2 mouse: same x centre as the pad it replaces, bottom row on the
/// desk contact line via its OWN 6px height (the shared MOUSE_SIZE put its
/// bottom 1px above the line, losing the sink the contact shadow needs).
const MOUSE_T2_POS: Vec2 = Vec2::new(
    MOUSE_POS.x,
    DESK_TOP_Y - DESK_CONTACT_SHADOW_ROWS + MOUSE_T2_SIZE.y / 2.0,
);

/// Tier-2 chair: 2px taller than the base chair, so its centre derives from
/// its OWN height or it sinks 1px through the floor.
const CHAIR_T2_POS: Vec2 = Vec2::new(CHAIR_POS.x, FLOOR_LINE_Y + CHAIR_T2_SIZE.y / 2.0);

/// The shelf (wall tier 2): left-aligned with the book stack like the
/// poster, but via its OWN 40px width, and bottom-aligned with the poster's
/// bottom so swapping tiers doesn't jump the decoration around the wall.
const SHELF_POS: Vec2 = Vec2::new(
    BOOKS_POS.x - BOOKS_SIZE.x / 2.0 + SHELF_SIZE.x / 2.0,
    (WALL_POS.y - WALL_SIZE.y / 2.0) + SHELF_SIZE.y / 2.0,
);

/// The ultrawide (monitor tier 2): 16px wider than the base monitor it
/// covers. Centring it on MONITOR_POS overlapped the lamp by 2px, so its x
/// is derived from the constraint that actually matters: right edge 2px
/// clear of the lamp's left edge, via its OWN width. (Full coverage of the
/// base monitor's footprint is asserted in the tests.)
const MONITOR_ULTRA_POS: Vec2 = Vec2::new(
    LAMP_POS.x - LAMP_SIZE.x / 2.0 - 2.0 - MONITOR_ULTRA_SIZE.x / 2.0,
    MONITOR_POS.y,
);

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

/// Marker + identity on one `(track, tier)` anchor spawned by
/// `spawn_upgrade_slots` — one entity per row of `crate::UPGRADE_TRACKS`
/// (docs/upgrade-design.md "Scene contract"). Never despawned; only its
/// `Visibility` is toggled by [`upgrade_render_system`], the same
/// stable-identity pattern the rest of this file uses for state-driven
/// props (the old `desk_upgrade_system`'s plant prop, `character_anim_
/// system`'s sprite, `monitor_power_system`'s sprite).
#[derive(Component)]
struct UpgradeSlot {
    /// [`crate::UpgradeTrack::id`] — used to look the track back up in
    /// `crate::UPGRADE_TRACKS` and to query `OwnedUpgrades`.
    track_id: &'static str,
    /// 1-based tier this entity represents (`tiers[tier - 1]` in the
    /// table); there is no tier-0 entity because tier 0 has no sprite.
    tier: u8,
}

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

/// Renders the room as real pixel-art sprites and keeps them in sync with
/// game state (character frame, monitor power, and every v0.3 upgrade
/// slot's owned-tier visibility).
///
/// Requires the app to have been built with
/// `ImagePlugin::default_nearest()` — see the `WIRING REQUIRED` block at the
/// top of this module.
pub struct ScenePlugin;

impl Plugin for ScenePlugin {
    fn build(&self, app: &mut App) {
        app.init_resource::<TypingAnimation>()
            .add_systems(Startup, (spawn_room, spawn_upgrade_slots))
            // PostStartup, not Startup: the camera system needs to know
            // whether lib.rs's `setup_scene` already spawned a `Camera2d`,
            // and two systems in the same schedule have no guaranteed order
            // across plugins. Everything in `Startup` has run by the time
            // `PostStartup` begins.
            .add_systems(PostStartup, setup_pixel_camera)
            .add_systems(Update, upgrade_render_system)
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

/// Spawns the whole room, one sprite per manifest entry.
///
/// Spawn order is only for readability — the draw order is the `Z_*`
/// constants, because sprites at equal z draw in an unspecified order.
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
}

/// The anchor position + z-layer for one `(track_id, tier_index)` slot
/// (docs/upgrade-design.md "Scene contract": "one anchor position + z … a
/// sprite-per-tier lookup"). `tier_index` is 0-based (`0` = tier 1) — needed
/// only by `desk_decor`, whose two tiers sit at different positions because
/// tier 2 (the duck) *joins* tier 1 (the plant) rather than replacing it
/// (`UpgradeTrackKind::Accumulate`); every other track's tiers share one
/// position because only one is ever visible at a time (`Replace`).
///
/// Panics on an unknown `track_id` — this is only ever called from
/// `spawn_upgrade_slots`, which iterates `crate::UPGRADE_TRACKS` itself, so
/// an unmatched id here means this function's `match` fell out of sync with
/// the table, a programming error to catch at once rather than silently
/// mis-placing a sprite.
fn upgrade_slot_anchor(track_id: &str, tier_index: usize) -> (Vec2, f32) {
    match (track_id, tier_index) {
        ("keyboard", _) => (KEYBOARD_POS, Z_KEYBOARD),
        ("mouse", 0) => (MOUSE_POS, Z_MOUSE),
        ("mouse", _) => (MOUSE_T2_POS, Z_MOUSE),
        ("monitor", 0) => (MONITOR_POS, Z_MONITOR_UPGRADE),
        ("monitor", _) => (MONITOR_ULTRA_POS, Z_MONITOR_UPGRADE),
        ("chair", 0) => (CHAIR_POS, Z_CHAIR_UPGRADE),
        ("chair", _) => (CHAIR_T2_POS, Z_CHAIR_UPGRADE),
        ("desk_decor", 0) => (PLANT_POS, Z_PLANT),
        ("desk_decor", _) => (DUCK_POS, Z_DUCK),
        ("wall", 0) => (WALL_POS, Z_WALL),
        ("wall", _) => (SHELF_POS, Z_WALL),
        ("pet", _) => (CAT_POS, Z_CAT),
        (other, _) => unreachable!("upgrade_slot_anchor: unknown track id {other:?}"),
    }
}

/// Spawns one entity per `(track, tier)` row of `crate::UPGRADE_TRACKS` —
/// docs/upgrade-design.md "Scene contract". Every slot starts
/// `Visibility::Hidden`; [`upgrade_render_system`] corrects that on the very
/// next frame from whatever `OwnedUpgrades` says (including a restored save
/// that already owns tiers, the same "derive from persisted state, don't
/// special-case a relaunch" rule the old `desk_upgrade_system` used).
///
/// Loading a sprite whose PNG does not exist yet (true for all of these at
/// the time this system was written — the art lands separately) logs a
/// Bevy asset error and renders nothing; it does not panic, so the game
/// stays runnable while the art catches up (same rule `spawn_room` already
/// documents for the original manifest).
fn spawn_upgrade_slots(mut commands: Commands, assets: Res<AssetServer>) {
    for track in crate::UPGRADE_TRACKS {
        for (i, tier) in track.tiers.iter().enumerate() {
            let (pos, z) = upgrade_slot_anchor(track.id, i);
            commands.spawn((
                Sprite::from_image(assets.load(tier.sprite)),
                Transform::from_xyz(pos.x, pos.y, z),
                Visibility::Hidden,
                UpgradeSlot {
                    track_id: track.id,
                    tier: (i + 1) as u8,
                },
            ));
        }
    }
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

/// `OwnedUpgrades` → every upgrade slot's `Visibility` (docs/upgrade-
/// design.md "Scene contract"): a `Replace` track shows only its highest
/// owned tier's slot; an `Accumulate` track (`desk_decor`) shows every slot
/// up to and including the owned tier. Owning tier 0 (nothing bought)
/// hides every slot for that track, same as the old `desk_upgrade_system`'s
/// below-threshold state.
///
/// Reads `crate::OwnedUpgrades`/`crate::UPGRADE_TRACKS`/
/// `crate::UpgradeTrackKind` directly (all private to the crate root, all
/// visible here the same way `Developer`/`Mood` already are — see this
/// module's imports note) rather than duplicating any of it in `scene.rs`,
/// per docs/upgrade-design.md's "one table" principle.
fn upgrade_render_system(
    owned: Res<crate::OwnedUpgrades>,
    mut slots: Query<(&UpgradeSlot, &mut Visibility)>,
) {
    for (slot, mut visibility) in &mut slots {
        let Some(track) = crate::UPGRADE_TRACKS.iter().find(|t| t.id == slot.track_id) else {
            continue; // unreachable in practice — slots are spawned FROM this table
        };
        let owned_tier = owned.tier_of(slot.track_id);
        let visible = match track.kind {
            crate::UpgradeTrackKind::Replace => owned_tier == slot.tier,
            crate::UpgradeTrackKind::Accumulate => owned_tier >= slot.tier,
        };
        *visibility = if visible {
            Visibility::Visible
        } else {
            Visibility::Hidden
        };
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
            Z_ROOM_BG, Z_RUG, Z_BOOKS, Z_CHAIR, Z_DEV, Z_DESK, Z_MONITOR, Z_LAMP, Z_MUG, Z_PLANT,
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
        // Not just *any* straddle: the tabletop has to cut the sprite below
        // the hands, or the desk edge saws through the character's arms.
        assert!(
            DESK_TOP_Y < feet + DEV_SIZE.y / 2.0,
            "the desk must not hide the character's upper body"
        );
        // …and the occlusion only happens at all because the desk draws over
        // the character. With these two swapped the geometry is unchanged and
        // the render is wrong: the torso paints over the tabletop.
        const {
            assert!(
                Z_DESK > Z_DEV,
                "the desk must draw in front of the character to occlude it"
            )
        };
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
        // Standing on the tabletop, sunk by DESK_CONTACT_SHADOW_ROWS so each
        // sprite's own contact-shadow row lands ON the wood rather than
        // floating one pixel above it (the v0.3 lighting-pass fix, applied
        // to every on-desk sprite including the new upgrade slots).
        for (name, pos, size) in [
            ("monitor", MONITOR_POS, MONITOR_SIZE),
            ("mug", MUG_POS, MUG_SIZE),
            ("plant", PLANT_POS, PLANT_SIZE),
            ("keyboard", KEYBOARD_POS, KEYBOARD_SIZE),
            ("mouse", MOUSE_POS, MOUSE_SIZE),
            ("duck", DUCK_POS, DUCK_SIZE),
        ] {
            assert_eq!(
                pos.y - size.y / 2.0,
                DESK_TOP_Y - DESK_CONTACT_SHADOW_ROWS,
                "{name} must rest on the tabletop (sunk by its contact-shadow row), \
                 not hover above it"
            );
        }
        // The lamp is the one exception, and only by the height of the light
        // spill baked into its bottom row: spill has to land *on* the tabletop,
        // so the lamp's own base — one row up — is what rests on the surface.
        assert_eq!(
            LAMP_POS.y - LAMP_SIZE.y / 2.0 + LAMP_SPILL_ROWS,
            DESK_TOP_Y,
            "the lamp's base must rest on the tabletop, with only its spill row \
             overlapping"
        );
        // Every one of them has to draw over the desk, or the tabletop paints
        // across the props standing on it.
        const {
            assert!(
                Z_MONITOR > Z_DESK
                    && Z_LAMP > Z_DESK
                    && Z_MUG > Z_DESK
                    && Z_PLANT > Z_DESK
                    && Z_KEYBOARD > Z_DESK
                    && Z_MOUSE > Z_DESK
                    && Z_DUCK > Z_DESK
                    && Z_MONITOR_UPGRADE > Z_DESK,
                "props resting on the desk must draw in front of it"
            )
        };

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

        // The cat lies ON the rug's visible foreground: its bottom row rests
        // on the rug's own horizontal midline (RUG_POS.y). The floor-line
        // convention put it behind the chair/desk cluster where a purchased
        // pet was invisible (see CAT_POS's doc comment) — this asserts the
        // surface derivation that replaced it, still a derivation and not a
        // hand-tuned number.
        assert_eq!(
            CAT_POS.y - CAT_SIZE.y / 2.0,
            RUG_POS.y,
            "the cat must rest on the rug's midline"
        );
        // And it must sit clear of the chair on the rug's open right side.
        const {
            assert!(
                CAT_POS.x - CAT_SIZE.x / 2.0 > CHAIR_POS.x + CHAIR_SIZE.x / 2.0,
                "the cat must be clear of the chair, on the rug's visible side"
            )
        };

        // The wall decoration (poster/shelf) hangs left-aligned above the
        // book stack, fully inside the bare band between the books' top and
        // the window's bottom frame (per WALL_POS's own doc comment).
        assert_eq!(
            WALL_POS.x - WALL_SIZE.x / 2.0,
            BOOKS_POS.x - BOOKS_SIZE.x / 2.0,
            "the shelf must left-align with the book stack below it"
        );
        // The taller poster deliberately dips a couple of pixels behind the
        // books' tops (depth, per WALL_POS's doc comment), so "clear above
        // the books" is NOT asserted. What must hold is that the decoration
        // hangs on the wall — well above the floor — rather than standing
        // on it like furniture.
        const {
            assert!(
                WALL_POS.y - WALL_SIZE.y / 2.0 > FLOOR_LINE_Y + 8.0,
                "the wall decoration must hang on the wall, not stand on the floor"
            )
        };

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
            ("keyboard", KEYBOARD_POS, KEYBOARD_SIZE),
            ("mouse", MOUSE_POS, MOUSE_SIZE),
            ("duck", DUCK_POS, DUCK_SIZE),
            ("wall", WALL_POS, WALL_SIZE),
            ("cat", CAT_POS, CAT_SIZE),
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
            ("keyboard", KEYBOARD_POS),
            ("mouse", MOUSE_POS),
            ("duck", DUCK_POS),
            ("wall", WALL_POS),
            ("cat", CAT_POS),
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
            ("keyboard", KEYBOARD_POS, KEYBOARD_SIZE),
            ("mouse", MOUSE_POS, MOUSE_SIZE),
            ("duck", DUCK_POS, DUCK_SIZE),
            ("wall", WALL_POS, WALL_SIZE),
            ("cat", CAT_POS, CAT_SIZE),
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

    // ------------------------------------------------------------------
    // v0.3 — upgrade slot visibility (docs/upgrade-design.md "Scene
    // contract"), app-driven through the *shipping* `upgrade_render_system`.
    //
    // These replace the M5 desk-upgrade tests that used to live in lib.rs
    // (`m5_desk_upgrade_shows_plant_when_wallet_crosses_threshold` /
    // `..._restored_wallet_above_threshold_is_visible_on_launch`): same
    // intent — a visible change driven by persisted state, proven by
    // stepping the app and asserting `Visibility` — new mechanism (owned
    // tiers in `OwnedUpgrades`, not a coin threshold on `Wallet`).
    // ------------------------------------------------------------------

    use std::collections::BTreeMap;

    /// A bare `UpgradeSlot` with no real sprite — these tests exercise only
    /// `upgrade_render_system`'s `Visibility` logic, not asset loading, so
    /// `MinimalPlugins` (no `AssetServer`/render pipeline) is enough.
    fn spawn_bare_slot(commands: &mut Commands, track_id: &'static str, tier: u8) {
        commands.spawn((Visibility::Hidden, UpgradeSlot { track_id, tier }));
    }

    /// The `(track_id, tier)` slot's current `Visibility`, or `None` if no
    /// such slot was spawned (a fixture bug, never an expected test
    /// outcome). `World::query` takes `&mut World` in Bevy 0.19.
    fn slot_visibility(world: &mut World, track_id: &str, tier: u8) -> Option<Visibility> {
        let mut q = world.query::<(&UpgradeSlot, &Visibility)>();
        q.iter(world)
            .find(|(slot, _)| slot.track_id == track_id && slot.tier == tier)
            .map(|(_, v)| *v)
    }

    #[test]
    fn keyboard_replace_shows_only_the_highest_owned_tier() {
        let mut app = App::new();
        app.add_plugins(MinimalPlugins);
        app.insert_resource(crate::OwnedUpgrades::default());
        app.add_systems(Startup, |mut commands: Commands| {
            spawn_bare_slot(&mut commands, "keyboard", 1);
            spawn_bare_slot(&mut commands, "keyboard", 2);
        });
        app.add_systems(Update, upgrade_render_system);

        // Tier 0 (nothing bought): both hidden.
        app.update();
        assert_eq!(
            slot_visibility(app.world_mut(), "keyboard", 1),
            Some(Visibility::Hidden)
        );
        assert_eq!(
            slot_visibility(app.world_mut(), "keyboard", 2),
            Some(Visibility::Hidden)
        );

        // Buy tier 1: only tier 1 shows.
        app.world_mut()
            .resource_mut::<crate::OwnedUpgrades>()
            .0
            .insert("keyboard".to_string(), 1);
        app.update();
        assert_eq!(
            slot_visibility(app.world_mut(), "keyboard", 1),
            Some(Visibility::Visible),
            "owning tier 1 must show the basic keyboard"
        );
        assert_eq!(
            slot_visibility(app.world_mut(), "keyboard", 2),
            Some(Visibility::Hidden)
        );

        // Buy tier 2: tier 1 HIDES (Replace, not stacking) and tier 2 shows.
        app.world_mut()
            .resource_mut::<crate::OwnedUpgrades>()
            .0
            .insert("keyboard".to_string(), 2);
        app.update();
        assert_eq!(
            slot_visibility(app.world_mut(), "keyboard", 1),
            Some(Visibility::Hidden),
            "Replace: buying tier 2 must hide tier 1's sprite, not stack it"
        );
        assert_eq!(
            slot_visibility(app.world_mut(), "keyboard", 2),
            Some(Visibility::Visible)
        );
    }

    #[test]
    fn desk_decor_accumulate_shows_every_owned_tier_at_once() {
        // Accumulate (desk_decor only): the duck joins the plant, it does
        // not replace it — buying tier 2 must leave tier 1 visible too.
        let mut app = App::new();
        app.add_plugins(MinimalPlugins);
        app.insert_resource(crate::OwnedUpgrades::default());
        app.add_systems(Startup, |mut commands: Commands| {
            spawn_bare_slot(&mut commands, "desk_decor", 1);
            spawn_bare_slot(&mut commands, "desk_decor", 2);
        });
        app.add_systems(Update, upgrade_render_system);

        app.world_mut()
            .resource_mut::<crate::OwnedUpgrades>()
            .0
            .insert("desk_decor".to_string(), 2);
        app.update();

        assert_eq!(
            slot_visibility(app.world_mut(), "desk_decor", 1),
            Some(Visibility::Visible),
            "Accumulate: owning tier 2 must still show tier 1 (the plant)"
        );
        assert_eq!(
            slot_visibility(app.world_mut(), "desk_decor", 2),
            Some(Visibility::Visible),
            "Accumulate: owning tier 2 must show tier 2 (the duck) too"
        );
    }

    #[test]
    fn upgrade_render_system_restored_ownership_is_visible_on_launch() {
        // Non-wallet-derived consequence, ported from the old M5 test: a
        // restored save whose `OwnedUpgrades` already owns a tier must show
        // it from the very first frame, with no separate "just unlocked"
        // transition needed.
        let mut app = App::new();
        app.add_plugins(MinimalPlugins);
        let mut owned = BTreeMap::new();
        owned.insert("pet".to_string(), 1u8);
        app.insert_resource(crate::OwnedUpgrades(owned));
        app.add_systems(Startup, |mut commands: Commands| {
            spawn_bare_slot(&mut commands, "pet", 1);
        });
        app.add_systems(Update, upgrade_render_system);
        app.update();

        assert_eq!(
            slot_visibility(app.world_mut(), "pet", 1),
            Some(Visibility::Visible),
            "a restored OwnedUpgrades that already owns tier 1 must show it on launch"
        );
    }

    /// Review finding: four tier-2 sprites shipped different dimensions than
    /// the one-per-track size const, so their anchors were 1-4px off — in a
    /// game whose contact-shadow mechanism is a deliberate 1px correction.
    /// This parses each tier PNG's actual IHDR and asserts it matches the
    /// size the anchor math assumes, so art and geometry cannot drift apart
    /// silently. (It also closes the tautology gap in the rests-on-surface
    /// test: that test checks derivations against constants; this one checks
    /// the constants against the shipped pixels.)
    #[test]
    fn every_tier_sprite_png_matches_the_size_its_anchor_assumes() {
        fn png_size(name: &str) -> (u32, u32) {
            let path = concat!(env!("CARGO_MANIFEST_DIR"), "/../assets/");
            let bytes = std::fs::read(format!("{path}{name}"))
                .unwrap_or_else(|e| panic!("missing sprite {name}: {e}"));
            // PNG layout: 8-byte signature, then the IHDR chunk whose first
            // eight data bytes are big-endian width and height.
            let be = |b: &[u8]| u32::from_be_bytes([b[0], b[1], b[2], b[3]]);
            (be(&bytes[16..20]), be(&bytes[20..24]))
        }
        for (sprite, size) in [
            ("keyboard_t1.png", KEYBOARD_SIZE),
            ("keyboard_t2.png", KEYBOARD_SIZE),
            ("mouse_t1.png", MOUSE_SIZE),
            ("mouse_t2.png", MOUSE_T2_SIZE),
            ("monitor_dual.png", MONITOR_SIZE),
            ("monitor_ultra.png", MONITOR_ULTRA_SIZE),
            ("chair_t1.png", CHAIR_SIZE),
            ("chair_t2.png", CHAIR_T2_SIZE),
            ("plant.png", PLANT_SIZE),
            ("duck.png", DUCK_SIZE),
            ("poster.png", WALL_SIZE),
            ("shelf.png", SHELF_SIZE),
            ("cat.png", CAT_SIZE),
        ] {
            let (w, h) = png_size(sprite);
            assert_eq!(
                (w as f32, h as f32),
                (size.x, size.y),
                "{sprite}: shipped PNG dimensions disagree with the size const \
                 its anchor is derived from"
            );
        }
    }

    /// Review finding: `upgrade_slot_anchor`'s `unreachable!` is reachable by
    /// DATA — adding a track row without a match arm panics at Startup with a
    /// fully green suite. Loop the real table through it so that failure mode
    /// becomes a test failure instead of a launch crash.
    #[test]
    fn every_upgrade_track_row_has_an_anchor() {
        for track in crate::UPGRADE_TRACKS {
            for (tier_index, _tier) in track.tiers.iter().enumerate() {
                let (pos, z) = upgrade_slot_anchor(track.id, tier_index);
                assert!(pos.x.is_finite() && pos.y.is_finite() && z.is_finite());
            }
        }
    }

    /// Tier-2 geometry that the render depends on, asserted:
    #[test]
    fn tier_two_sprites_sit_where_their_own_dimensions_say() {
        // mouse t2: bottom on the desk contact line via its OWN height
        assert_eq!(
            MOUSE_T2_POS.y - MOUSE_T2_SIZE.y / 2.0,
            DESK_TOP_Y - DESK_CONTACT_SHADOW_ROWS
        );
        // chair t2: stands on the floor line via its OWN height
        assert_eq!(CHAIR_T2_POS.y - CHAIR_T2_SIZE.y / 2.0, FLOOR_LINE_Y);
        // shelf: left-aligned with the books, bottom-aligned with the poster
        assert_eq!(
            SHELF_POS.x - SHELF_SIZE.x / 2.0,
            BOOKS_POS.x - BOOKS_SIZE.x / 2.0
        );
        assert_eq!(
            SHELF_POS.y - SHELF_SIZE.y / 2.0,
            WALL_POS.y - WALL_SIZE.y / 2.0
        );
        // ultrawide: fully covers the base monitor AND clears the lamp
        const {
            assert!(
                MONITOR_ULTRA_POS.x - MONITOR_ULTRA_SIZE.x / 2.0
                    <= MONITOR_POS.x - MONITOR_SIZE.x / 2.0,
                "ultrawide must cover the base monitor's left edge"
            )
        };
        const {
            assert!(
                MONITOR_ULTRA_POS.x + MONITOR_ULTRA_SIZE.x / 2.0
                    >= MONITOR_POS.x + MONITOR_SIZE.x / 2.0,
                "ultrawide must cover the base monitor's right edge"
            )
        };
        const {
            assert!(
                MONITOR_ULTRA_POS.x + MONITOR_ULTRA_SIZE.x / 2.0 < LAMP_POS.x - LAMP_SIZE.x / 2.0,
                "ultrawide must clear the lamp"
            )
        };
    }
}
