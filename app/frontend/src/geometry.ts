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
export const DEV_RECT: Rect = { left: 116, top: 92, w: 88, h: 104 };
// Chairs are bottom-centre anchored (bottom = room row 200, centre x = 160,
// matching DEV_RECT's centre). FURNITURE REWRITE: the canvases are now narrow
// and shoulder-proportioned (was 136-152px wide — a near-full-scene slab that
// read as a wall/throne) so each chair sits BEHIND the developer as real
// furniture: the backrest crown peeks a little above the hood, the seat/back
// edges frame the torso, small armrest pads sit at the sides, and the caster
// base splays out below/beside the lower body. The developer (drawn on top,
// z 12-14 > chair z 10-11) stays the dominant silhouette. Sizes match the
// sprite canvases in tools/gen_assets.py 1:1.
export const CHAIR_RECT: Record<string, Rect> = {
  chair_basic: { w: 92, h: 84, left: 114, top: 116 },
  chair_racer: { w: 96, h: 86, left: 112, top: 114 },
  chair_exec: { w: 100, h: 86, left: 110, top: 114 },
  chair_antigrav: { w: 88, h: 82, left: 116, top: 118 }
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
export const CHAIR_Z_FORM = 10, CHAIR_Z_DETAIL = 11;
export const DEV_Z_FORM = 12, DEV_Z_STYLE = 13, DEV_Z_BASE = 14;

export const MOOD_COLOR: Record<ActiveState, string> = { coding: 'var(--plant)', idle: 'var(--screen)', onBreak: 'var(--pot)' };
export const FRAME_FOR_STATE: Record<'idle' | 'onBreak', string> = { idle: 'idle', onBreak: 'sleep' }; // 'coding' alternates type_a/type_b
