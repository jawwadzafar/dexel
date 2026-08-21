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
// matching DEV_RECT's centre) and now rise well up behind the developer's
// upper body so the seated read is unambiguous: each chair's top reaches the
// shoulder/head band (near or above DEV_RECT.top=92), with raised backrest
// corners / bolsters flanking the head above room row 120 and armrests at the
// sides. Sizes match the sprite canvases in tools/gen_assets.py 1:1.
export const CHAIR_RECT: Record<string, Rect> = {
  chair_basic: { w: 144, h: 108, left: 88, top: 92 },
  chair_racer: { w: 148, h: 112, left: 86, top: 88 },
  chair_exec: { w: 152, h: 118, left: 84, top: 82 },
  chair_antigrav: { w: 136, h: 100, left: 92, top: 100 }
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
