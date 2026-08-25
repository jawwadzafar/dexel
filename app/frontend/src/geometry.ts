// Fixed scene geometry (docs/art-direction.md "Element placement table" /
// layer order 1..14). Pure data — no DOM, no state. Shared by the render
// layer (scene compositor) and the store feature (its item preview pane
// composes the same scene geometry at a different scale).
import type { ActiveState } from './wire';

export interface Rect {
  left: number;
  top: number;
  w: number;
  h: number;
}

export const SLOT_RECT: Record<string, Rect> = {
  wall: { left: 24, top: 16, w: 40, h: 44 },
  plant: { left: 244, top: 32, w: 40, h: 44 },
  buddy: { left: 288, top: 46, w: 28, h: 30 },
  beverage: { left: 56, top: 90, w: 20, h: 24 },
  keyboard: { left: 112, top: 90, w: 96, h: 24 },
  mouse: { left: 224, top: 90, w: 44, h: 24 }
};
// PERSPECTIVE FIX (3/4 behind, over-the-shoulder) + PROPORTION PASS. The
// developer canvas is wide because the `mouse` pose reaches the mouse at room
// x252, and short because the whole figure has to read inside room y99..160
// (the keyboard occupies y90..113 above it, the SPRINT/STATUS HUD panels cover
// the scene below y161). The figure is CENTRED in its canvas (room x159.5 =
// local 95.5), which is also what keeps the derived 40x40 hoodie store
// thumbnails a centred crop of the hood.
//
// The rect is UNCHANGED by the proportion pass, by the SLIMMING pass that
// followed it, and by the HOOD-NARROWING pass after that (the figure inside it
// now reads lean: a 42x31 hood at room y110..140 — 46 -> 42 when the owner
// read the head as still a little wide — 76px-wide shoulders tapering to a
// 66px waist, arms 10..17px thick). It deliberately still stops at room y167 rather than being extended
// down behind the HUD: the panels are opaque from room y161 to y198 and the
// 4px gap between them at room x158..161 is itself covered by the chair, so a
// taller canvas would add no visible pixel — while pushing the derived hoodie
// thumbnails past tools/gen_assets.py derive_thumbnail's 1:1 threshold into a
// ÷2 downsample that drops every other row of the 1px style marks. The lower
// torso instead tapers to a rounded end at room y166, behind the HUD's edge.
// Sizes match tools/gen_assets.py 1:1.
export const DEV_RECT: Rect = { left: 64, top: 92, w: 192, h: 76 };
// Chairs are bottom-centre anchored (bottom = room row 200, centre x = 160,
// matching DEV_RECT's centre). BEHIND-VIEW COMPOSITING: the chair draws ON TOP
// of the developer (CHAIR_Z_* > DEV_Z_* below) because from behind a seated
// person the backrest is between the camera and their torso. Every chair
// canvas therefore starts at room row 149 and paints nothing above room row
// 150 — the developer's shoulder line, above which only the hood, shoulders
// and forward-reaching arms live (row 149 is reserved for the detail layer's
// 1px contact halo) — so the plain Z-order swap composites correctly with no
// per-layer masking. Backrests are shoulder-width-MINUS (half-width 24..28
// against the developer's shoulder half-width 38), so the shoulders peek out
// at the sides of every style and the hood clears every crown. SLIMMING PASS:
// the owner read the figure as fat and the furniture as too wide, so the
// shoulders came down 92 → 76px and the chairs came down FURTHER — canvases
// 116..148 → 68..88 wide, backrest half-widths 31..39 → 24..28, and armrests
// pulled in so the whole furniture footprint (78..86px; 65px for the pod) now
// sits INSIDE the figure's own widest span (90px) instead of outside it, which
// is what read as "too wide".
// Sizes match the sprite canvases in tools/gen_assets.py 1:1.
export const CHAIR_RECT: Record<string, Rect> = {
  chair_basic: { w: 80, h: 51, left: 120, top: 149 },
  chair_racer: { w: 84, h: 51, left: 118, top: 149 },
  chair_exec: { w: 88, h: 51, left: 116, top: 149 },
  chair_antigrav: { w: 68, h: 51, left: 126, top: 149 }
};

export interface SceneryItem extends Rect {
  file: string;
  z: number;
  id?: string;
}

export const SCENERY: SceneryItem[] = [
  { file: 'room_back.png', left: 0, top: 0, w: 320, h: 200, z: 1 },
  { file: 'desk_back.png', left: 0, top: 74, w: 320, h: 58, z: 3 },
  { file: 'monitor.png', left: 94, top: 20, w: 132, h: 64, z: 4, id: 'sprite-monitor' }
];
export const SLOT_Z: Record<string, number> = { wall: 2, plant: 5, buddy: 6, beverage: 7, keyboard: 8, mouse: 9 };
// Layer order 10..14, THE structural half of the perspective fix. The camera
// sits BEHIND the developer, so the chair's backrest is nearer to it than
// their torso: chair form+detail composite ABOVE the three developer layers,
// which is the exact reverse of the old (top-down) order. The developer
// sprite is authored to suit — nothing of the figure that must stay visible
// (hood, shoulders, arms, hands) is below room row 150, and no chair pixel is
// above it (asserted in tools/gen_assets.py: assert_chair_region, whose
// CHAIR_TOP_ROOM_Y is this same 150).
export const DEV_Z_FORM = 10, DEV_Z_STYLE = 11, DEV_Z_BASE = 12;
export const CHAIR_Z_FORM = 13, CHAIR_Z_DETAIL = 14;

// =========================================================================
// SCENE-REACTIONS (docs/plan/ROADMAP.md, docs/ui-spec.md §12) — hit regions
// =========================================================================
//
// The four clickable things in the room, in the SAME room-pixel space as
// every rect above (the scene is a 320x200 surface drawn at 2x inside the
// 640x400 layout, which is itself transformed to fit the window — see
// render/viewport.ts). They are room rects and nothing else, which is what
// lets the render layer express them as ordinary absolutely-positioned
// children of #scene-sprites and let the browser do the hit testing through
// both transforms; no viewport-coordinate maths enters this frontend.
//
// Two of the four ARE existing rects and are deliberately not repeated here:
// the beverage and the buddy are exactly their SLOT_RECT (20x24 and 28x30 —
// small enough that the rect already reads as the object). The other two need
// their own, because the sprite canvas is much bigger than the thing drawn in
// it:
//
//   dev      DEV_RECT is 192x76 at (64,92) and most of it is empty air: the
//            `mouse` pose's arm reaches to room x252 and the `cheer` frames
//            fling out to x223, none of which is where the FIGURE is. So this
//            is the BODY — hood, shoulders, upper arms, torso — and nothing
//            else, bounded by the two things that would otherwise make the
//            hand cursor lie:
//              top    room y110, the hood's own top row. Above it the sprite
//                     is two forearms with a gap between them, and that gap
//                     is where the KEYBOARD (room y90..113) is visible — a
//                     pointer cursor there would be offering a click on the
//                     keyboard, which is not a reactive item.
//              bottom room y148, the last row before the chair. The chair
//                     composites OVER the developer from room y149 down
//                     (CHAIR_Z_* > DEV_Z_*, the behind-view order), so every
//                     pixel below that is chair, not dexel — and further down
//                     still, the SPRINT/STATUS HUD panels are opaque from
//                     room y161 and would eat the click anyway.
//            Horizontally it is the figure's own alpha box (dev_base_idle /
//            dev_form_idle occupy local x54..138 => room x118..202), i.e. the
//            shoulder span. 84x39 room px is 168x78 on screen at the scene's
//            2x, which is a large, easy target.
//   monitor   the monitor sprite's canvas IS its silhouette (every one of
//            rows 0..59 is fully opaque bezel, then the neck and foot), so
//            this is simply SCENERY's monitor rect restated as a hit target.
//            The DOM terminal that sits over the glass is `pointer-events:
//            none` (game.css #scene-text), so clicks on the text land here.
export const HIT_RECT: Record<string, Rect> = {
  dev: { left: 118, top: 110, w: 84, h: 39 },
  monitor: { left: 94, top: 20, w: 132, h: 64 }
};
// Above CHAIR_Z_DETAIL (14), the topmost scene layer: a hit region has to
// win the hit test against every sprite it overlaps (the chair backrest
// draws OVER the developer, and the keyboard sits under their hands), and it
// is invisible, so being on top costs no pixel.
export const HIT_Z = 20;

export const MOOD_COLOR: Record<ActiveState, string> = { coding: 'var(--plant)', idle: 'var(--screen)', onBreak: 'var(--pot)' };
export const FRAME_FOR_STATE: Record<'idle' | 'onBreak', string> = { idle: 'idle', onBreak: 'sleep' }; // 'coding' alternates type_a/type_b, or 'mouse' while the server reports mouse activity (render/scene.ts)
