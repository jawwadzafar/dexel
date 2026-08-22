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
// PERSPECTIVE FIX (3/4 behind, over-the-shoulder). The developer canvas is
// wide because the `mouse` pose reaches the mouse at room x252, and short
// because the whole figure has to read inside room y99..160 (the keyboard
// occupies y90..113 above it, the SPRINT/STATUS HUD panels cover the scene
// below y161). The figure is CENTRED in its canvas (room x159.5 = local
// 95.5), which is also what keeps the derived 40x40 hoodie store thumbnails
// a centred crop of the hood. Sizes match tools/gen_assets.py 1:1.
export const DEV_RECT: Rect = { left: 64, top: 92, w: 192, h: 76 };
// Chairs are bottom-centre anchored (bottom = room row 200, centre x = 160,
// matching DEV_RECT's centre). BEHIND-VIEW COMPOSITING: the chair now draws
// ON TOP of the developer (CHAIR_Z_* > DEV_Z_* below) because from behind a
// seated person the backrest is between the camera and their torso. Every
// chair canvas therefore starts at or below room row 144 — the developer's
// shoulder line, above which only the hood, shoulders and forward-reaching
// arms live — so the plain Z-order swap composites correctly with no
// per-layer masking. Backrests are shoulder-width-plus (half-width 22..28
// against the developer's shoulder half-width 28), so the shoulders peek out
// at the sides of the narrower styles and the hood clears every crown.
// Sizes match the sprite canvases in tools/gen_assets.py 1:1.
export const CHAIR_RECT: Record<string, Rect> = {
  chair_basic: { w: 112, h: 58, left: 104, top: 142 },
  chair_racer: { w: 116, h: 58, left: 102, top: 142 },
  chair_exec: { w: 120, h: 58, left: 100, top: 142 },
  chair_antigrav: { w: 96, h: 58, left: 112, top: 142 }
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
// (hood, shoulders, arms, hands) is below room row 144, and no chair pixel is
// above it (asserted in tools/gen_assets.py: assert_chair_region).
export const DEV_Z_FORM = 10, DEV_Z_STYLE = 11, DEV_Z_BASE = 12;
export const CHAIR_Z_FORM = 13, CHAIR_Z_DETAIL = 14;

export const MOOD_COLOR: Record<ActiveState, string> = { coding: 'var(--plant)', idle: 'var(--screen)', onBreak: 'var(--pot)' };
export const FRAME_FOR_STATE: Record<'idle' | 'onBreak', string> = { idle: 'idle', onBreak: 'sleep' }; // 'coding' alternates type_a/type_b, or 'mouse' while the server reports mouse activity (render/scene.ts)
