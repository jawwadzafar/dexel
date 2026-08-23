// RENDER layer — the scene compositor (#scene-sprites), art-direction.md
// layer order 1..14. Pure-ish: reads the current state/catalog off the
// central store and paints the DOM it owns; sends no ClientAction, knows
// nothing about the store/activity modals.
import { byId } from '../dom';
import * as store from '../state/store';
import {
  CHAIR_RECT, CHAIR_Z_DETAIL, CHAIR_Z_FORM, DEV_RECT, DEV_Z_BASE, DEV_Z_FORM, DEV_Z_STYLE,
  FRAME_FOR_STATE, SCENERY, SLOT_RECT, SLOT_Z
} from '../geometry';
import { assetUrl } from '../assets';
import { buildTintLayer, plainImg, positionEl } from './tint';
import { handleSpriteSentinelError } from './overlays';

const scene = byId<HTMLDivElement>('scene-sprites');

let sceneBuilt = false;
const sceneNodes: Record<string, HTMLElement> = {}; // slot -> container node (for slots we clear+refill)
let devFrameIndex = 0; // toggles 0/1 for type_a/type_b while coding
// The `mouse` dev pose (right hand off the keyboard and onto the mouse) is
// SIGNAL-DRIVEN, not a timed flourish: the server already reports mouse
// activity honestly and content-free (stats.today.mouseActiveSeconds ticks up
// once per second in which the activity provider saw mouse motion/scroll/
// drag), so the hand moves to the mouse exactly when the player's really did
// — and never otherwise. Inventing a periodic mouse beat would be the client
// asserting state the server never sent, which this frontend does not do.
let lastMouseSecs = -1;        // -1 = no baseline observed yet
let mouseHoldTicks = 0;        // frame ticks left holding the mouse pose
const MOUSE_HOLD_TICKS = 8;    // ~1.6s at the 200ms tick below

// =========================================================================
// Phase P3 - CHARACTER LIFE (docs/plan/PRODUCT-EVOLUTION.md Phase P3 / §2.6)
// =========================================================================
//
// Two things, and deliberately only two:
//
//   AMBIENT LIFE   Dexel breathes while it sits there, and occasionally
//                  stretches. Pure presentation on top of the pose the
//                  server already reported - it never claims activity.
//   CELEBRATION    a short two-frame bounce fired by onCelebrate(), which
//                  main.ts calls ONLY from a real server event (a kept
//                  session's `sessionComplete`, a sprint's `flash{kind:
//                  "sprint"}`). ADR 0010: nothing here invents an event.
//
// What P3 explicitly is NOT (§2.6's "alive WITHOUT simulation mechanics"):
// there is no meter, no need, no decay, no hunger, nothing that accumulates
// while you are away and nothing that guilt-trips you for it. The ambient
// timers below are a display loop; they hold no state worth persisting and
// mean nothing.
//
// PRECEDENCE, highest first (see sceneDevFrame):
//   1. celebration  - a real event, and it outranks the pose because it is
//                     an event rather than a claim about what you are doing
//   2. sleep        - `onBreak` owns its pose outright, exactly as before;
//                     P3 adds nothing to it, and suppresses the celebration
//                     bounce entirely while Dexel is asleep (below)
//   3. mouse        - the signal-driven pose above
//   4. typing       - the 5fps type_a/type_b cycle above
//   5. ambient      - only in `idle`, i.e. the honest "sitting here, hands
//                     off the keys" mood. Never during coding: that would be
//                     motion competing with the typing animation, and the
//                     typing animation is the one that means something.
//
// Every beat is a SPRITE SWAP on the existing 200ms frame timer - no CSS
// transition, no requestAnimationFrame, no per-frame layout (ui-spec.md's
// no-transitions/400ms spirit, and ADR 0011's all-day cost promise: an idle
// Dexel repaints the 3-image dev composite for ~3 of every 25 ticks and
// nothing at all in between, because the tick only calls renderDev() when
// the frame it would paint actually changed).

// One tick per frame of a beat, at TICK_MS each. Chosen for a read, not for
// smoothness: a breath is a slow single rise-and-settle, the stretch holds
// its top for most of its length (that is what makes it a stretch and not a
// bigger breath), and the celebration is three fast bounces.
const TICK_MS = 200;
const BREATH_SCRIPT = ['breath', 'breath', 'breath'];                       // 600ms
const STRETCH_SCRIPT = ['breath', 'stretch', 'stretch', 'stretch', 'stretch', 'breath']; // 1.2s
const CELEBRATE_SCRIPT = ['cheer_a', 'cheer_b', 'cheer_a', 'cheer_b',
                          'cheer_a', 'cheer_b', 'breath'];                  // 1.4s

// Interval bands, jittered per beat so the loop never reads as a metronome.
// The stretch band is 18-34s rather than a rounder number for one concrete
// reason: `idle` is bounded above by engine.OnBreakIdleThreshold (30s of
// genuine idleness flips the mood to `onBreak`, which owns the sleep pose),
// so a band centred much beyond that would mean the stretch essentially
// never played. The countdowns keep running in every mood and only FIRE
// while eligible, so a stretch that came due mid-keystroke plays out as soon
// as the hands come off the keys - which is also when a person stretches.
const BREATH_BAND_S: [number, number] = [4, 6];
const STRETCH_BAND_S: [number, number] = [18, 34];

// The hoodie style overlay is ONE file per garment, authored against the
// `idle` geometry, and it is what carries the drawstrings/zip/hem marks. The
// P3 frames lift the overlay-bearing hood AND back by the same rigid offset
// (tools/gen_assets.py asserts DOME_DY == BACK_DY for every frame but
// `sleep`), so shifting that one overlay layer by the same offset keeps every
// mark pixel-locked to the fabric it is printed on - no per-frame garment
// assets, no catalog/wire change. THIS TABLE MUST MATCH gen_assets.py's
// DOME_DY/BACK_DY: a `python3 tools/gen_assets.py` run prints it as
// "FRAME_OVERLAY_DY (render/scene.ts must carry this same table)". `sleep`
// is 0 here on purpose - its 3px/2px non-rigid drop predates P3 and its
// slight garment offset is the documented pre-existing simplification.
const FRAME_OVERLAY_DY: Record<string, number> = {
  idle: 0, type_a: 0, type_b: 0, mouse: 0, sleep: 0,
  breath: -1, stretch: -2, cheer_a: -1, cheer_b: -3
};

let ambientQueue: string[] = [];   // frames left in the beat being played
let ambientFrame = '';             // '' = no ambient frame this tick
let celebrateQueue: string[] = [];
let celebrateFrame = '';           // '' = not celebrating
let renderedDevFrame = '';         // the frame renderDev() last painted

function ticksUntil(band: [number, number]): number {
  const secs = band[0] + Math.random() * (band[1] - band[0]);
  return Math.max(1, Math.round((secs * 1000) / TICK_MS));
}
// Ticks until each beat is next due. Seeded with a full jittered interval so
// Dexel does not breathe the instant the page loads.
let breathIn = ticksUntil(BREATH_BAND_S);
let stretchIn = ticksUntil(STRETCH_BAND_S);

// Advances the ambient beat by one tick. `eligible` is precedence 5 above:
// idle mood, no mouse pose held, nothing celebrating.
function advanceAmbient(eligible: boolean): void {
  if (!eligible) {
    ambientQueue = [];
    ambientFrame = '';
  }
  if (breathIn > 0) breathIn -= 1;
  if (stretchIn > 0) stretchIn -= 1;
  if (ambientQueue.length > 0) {
    ambientFrame = ambientQueue.shift() as string;
    return;
  }
  ambientFrame = '';
  if (!eligible) return;
  if (stretchIn === 0) {
    ambientQueue = STRETCH_SCRIPT.slice();
    stretchIn = ticksUntil(STRETCH_BAND_S);
    // Push the breath out too, so a stretch is not immediately chased by the
    // breath that came due underneath it.
    breathIn = ticksUntil(BREATH_BAND_S);
  } else if (breathIn === 0) {
    ambientQueue = BREATH_SCRIPT.slice();
    breathIn = ticksUntil(BREATH_BAND_S);
  }
  if (ambientQueue.length > 0) ambientFrame = ambientQueue.shift() as string;
}

// The Phase P2 seam (docs/plan/P2-design.md §3.3), now real. Called from
// main.ts and ONLY from a server-originated event:
//   'session'  a kept session's `sessionComplete` message
//   'sprint'   a `flash{kind:"sprint"}`, which app/main.go broadcasts only
//              when Game.Tick actually completed a sprint
// Both play the same beat today; `reason` is required anyway so that every
// call site has to name the real event it came from, and so P4's Moments can
// give themselves their own beat without changing this signature.
// Suppressed while `onBreak`: the sleep pose means Dexel is genuinely away
// from the keys (30s+ of real idleness), so an auto-ended session would
// otherwise make a sleeping character cheer at an empty chair.
export function onCelebrate(reason: 'session' | 'sprint'): void {
  const state = store.getState();
  if (!state || state.activeState === 'onBreak') return;
  ambientQueue = [];
  ambientFrame = '';
  celebrateQueue = CELEBRATE_SCRIPT.slice();
  celebrateFrame = celebrateQueue.shift() as string;
  breathIn = ticksUntil(BREATH_BAND_S);
  stretchIn = ticksUntil(STRETCH_BAND_S);
  if (sceneBuilt) renderDev();   // start on the event, not up to 200ms later
}

function buildSceneSkeleton(): void {
  scene.innerHTML = '';
  SCENERY.forEach(function (s) {
    const img = plainImg(s.file, s);
    img.style.zIndex = String(s.z);
    if (s.id) img.id = s.id;
    if (s.file === 'room_back.png') img.addEventListener('error', handleSpriteSentinelError);
    scene.appendChild(img);
  });
  ['wall', 'plant', 'buddy', 'beverage', 'keyboard', 'mouse'].forEach(function (slot) {
    const holder = document.createElement('div');
    holder.style.position = 'absolute';
    holder.style.zIndex = String(SLOT_Z[slot]);
    positionEl(holder, SLOT_RECT[slot]);
    scene.appendChild(holder);
    sceneNodes[slot] = holder;
  });
  const chairHolder = document.createElement('div');
  chairHolder.style.position = 'absolute';
  scene.appendChild(chairHolder);
  sceneNodes.chair = chairHolder;

  const devHolder = document.createElement('div');
  devHolder.style.position = 'absolute';
  positionEl(devHolder, DEV_RECT);
  scene.appendChild(devHolder);
  sceneNodes.dev = devHolder;

  sceneBuilt = true;
}

// Exported for the store feature's composed preview pane, which mirrors
// this same dev-frame animation at a different scale.
export function currentDevFrame(): string {
  const state = store.getState();
  if (!state) return 'idle';
  // `onBreak` owns the sleep pose outright; otherwise reported mouse activity
  // wins over the typing cycle. Note mouse activity ALONE leaves the server in
  // `idle` (mood follows keystrokes), and reading/scrolling with one hand on
  // the mouse is exactly what that is, so the mouse pose is allowed there too.
  if (state.activeState === 'onBreak') return FRAME_FOR_STATE.onBreak;
  if (mouseHoldTicks > 0) return 'mouse';
  if (state.activeState === 'coding') return devFrameIndex === 0 ? 'type_a' : 'type_b';
  return FRAME_FOR_STATE[state.activeState] || 'idle';
}

// The frame the SCENE paints: the state-driven pose above, with the P3
// celebration/ambient beats layered over it. Kept separate from the exported
// currentDevFrame() on purpose - that one is the store modal's composed
// preview doll, which mirrors the honest pose and has no business breathing
// (it also draws its hoodie overlay without the FRAME_OVERLAY_DY offset, so
// handing it a lifted frame would misalign the garment marks it exists to
// show off).
function sceneDevFrame(): string {
  if (!store.getState()) return 'idle';
  if (celebrateFrame) return celebrateFrame;
  if (ambientFrame) return ambientFrame;
  return currentDevFrame();
}

function renderSlotSprite(slotId: string): void {
  const holder = sceneNodes[slotId];
  holder.innerHTML = '';
  const item = store.equippedItemFor(slotId);
  if (!item || !item.sprite) return; // *_none item (or unresolved default): slot stays hidden
  const img = document.createElement('img');
  img.className = 'layer sprite';
  img.alt = '';
  img.src = assetUrl(item.sprite) || '';
  img.style.position = 'absolute';
  img.style.left = '0';
  img.style.top = '0';
  img.style.width = SLOT_RECT[slotId].w + 'px';
  img.style.height = SLOT_RECT[slotId].h + 'px';
  holder.appendChild(img);
}

function renderChair(): void {
  const state = store.getState()!;
  const holder = sceneNodes.chair;
  holder.innerHTML = '';
  const eq = state.equipped.chair;
  const item = store.equippedItemFor('chair');
  if (!item) return; // no chair item at all, not even a free default — nothing to draw
  const rect = CHAIR_RECT[item.id] || CHAIR_RECT.chair_basic;
  positionEl(holder, rect);
  const tint = buildTintLayer(item.sprite, store.tintHexFor((eq && eq.tintId) || item.defaultTint));
  tint.style.zIndex = String(CHAIR_Z_FORM);
  positionEl(tint, { left: 0, top: 0, w: rect.w, h: rect.h });
  holder.appendChild(tint);
  if (item.detail) {
    const detail = document.createElement('img');
    detail.className = 'layer sprite';
    detail.alt = '';
    detail.src = assetUrl(item.detail) || '';
    detail.style.position = 'absolute';
    detail.style.left = '0';
    detail.style.top = '0';
    detail.style.width = rect.w + 'px';
    detail.style.height = rect.h + 'px';
    detail.style.zIndex = String(CHAIR_Z_DETAIL);
    holder.appendChild(detail);
  }
}

// The developer composite is the one non-generic slot (art-direction.md
// "Scene contract"): dev_form_<frame> (tinted by the hoodie's tint) +
// hoodie_<style> (the equipped hoodie item's own palette-pure file,
// trusted straight off item.sprite — the wire already carries the true
// single-file filename; see internal/game/catalog.go) + dev_base_<frame>
// (frame-driven, always present).
function renderDev(): void {
  const holder = sceneNodes.dev;
  holder.innerHTML = '';
  const frame = sceneDevFrame();
  renderedDevFrame = frame;
  const state = store.getState()!;
  const eq = state.equipped.hoodie;
  const item = store.equippedItemFor('hoodie');
  const tintHex = store.tintHexFor((eq && eq.tintId) || (item && item.defaultTint));

  const formLayer = buildTintLayer('dev_form_' + frame + '.png', tintHex);
  formLayer.style.zIndex = String(DEV_Z_FORM);
  positionEl(formLayer, { left: 0, top: 0, w: DEV_RECT.w, h: DEV_RECT.h });
  holder.appendChild(formLayer);

  if (item) {
    const style = document.createElement('img');
    style.className = 'layer sprite';
    style.alt = '';
    style.src = assetUrl(item.sprite) || '';
    style.style.position = 'absolute';
    style.style.left = '0';
    // P3: ride the frame's rigid body lift so the garment's own marks stay
    // printed on the fabric that moved (see FRAME_OVERLAY_DY).
    style.style.top = (FRAME_OVERLAY_DY[frame] || 0) + 'px';
    style.style.width = DEV_RECT.w + 'px';
    style.style.height = DEV_RECT.h + 'px';
    style.style.zIndex = String(DEV_Z_STYLE);
    holder.appendChild(style);
  }

  const base = document.createElement('img');
  base.className = 'layer sprite';
  base.alt = '';
  base.src = assetUrl('dev_base_' + frame + '.png') || '';
  base.style.position = 'absolute';
  base.style.left = '0';
  base.style.top = '0';
  base.style.width = DEV_RECT.w + 'px';
  base.style.height = DEV_RECT.h + 'px';
  base.style.zIndex = String(DEV_Z_BASE);
  holder.appendChild(base);
}

export function renderScene(): void {
  const state = store.getState();
  const catalog = store.getCatalog();
  if (!state || !catalog) return;
  if (!sceneBuilt) buildSceneSkeleton();
  ['wall', 'plant', 'buddy', 'beverage', 'keyboard', 'mouse'].forEach(renderSlotSprite);
  renderChair();
  renderDev();
  const monitor = document.getElementById('sprite-monitor');
  if (monitor) monitor.classList.toggle('monitor-onbreak', state.activeState === 'onBreak');
}

// dev frame animation (5fps while coding) — a fixed-interval timer, not
// requestAnimationFrame, per art-direction.md "Visual states". P3 hangs the
// ambient/celebration scheduler off this same one timer rather than adding a
// second: one 200ms interval that repaints ONLY when the frame it would paint
// changed (renderedDevFrame), which is how an all-day idle Dexel stays cheap.
setInterval(function () {
  const state = store.getState();
  if (!state) return;
  const today = state.stats && state.stats.today;
  const mouseSecs = today ? today.mouseActiveSeconds : 0;
  if (lastMouseSecs >= 0 && mouseSecs > lastMouseSecs) mouseHoldTicks = MOUSE_HOLD_TICKS;
  lastMouseSecs = mouseSecs;

  // Celebration advances first: it outranks every pose, and while it runs the
  // ambient beat is cancelled rather than queued behind it.
  if (celebrateFrame) celebrateFrame = celebrateQueue.length > 0 ? celebrateQueue.shift() as string : '';

  // Pose bookkeeping — byte-for-byte the same decisions as before P3 (the
  // mouse hold still burns one tick per tick and the typing cycle still
  // toggles only while coding); only the early returns are gone, because the
  // ambient scheduler below needs to run on an idle tick too.
  if (state.activeState === 'onBreak') mouseHoldTicks = 0;
  else if (mouseHoldTicks > 0) mouseHoldTicks -= 1;
  else if (state.activeState === 'coding') devFrameIndex = devFrameIndex === 0 ? 1 : 0;

  advanceAmbient(!celebrateFrame && celebrateQueue.length === 0 &&
                 mouseHoldTicks === 0 && state.activeState === 'idle');

  if (sceneBuilt && sceneDevFrame() !== renderedDevFrame) renderDev();
}, TICK_MS);
