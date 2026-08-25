// RENDER layer — the scene compositor (#scene-sprites), art-direction.md
// layer order 1..14. Pure-ish: reads the current state/catalog off the
// central store and paints the DOM it owns; sends no ClientAction, knows
// nothing about the store/activity modals.
//
// =========================================================================
// BUG-1 — "the character blinks on and off for milliseconds"
// =========================================================================
//
// This module used to DESTROY AND REBUILD its whole subtree on every render:
// `scene.innerHTML = ''` plus a `holder.innerHTML = ''` per slot, then a
// fresh spriteImg() and a fresh `img.src` for every layer.
// renderScene() runs on each ~1Hz state broadcast, and renderDev() runs on
// every 200ms animation tick as well, so that teardown was on the hot path.
//
// A brand-new <img> has NO bitmap to paint until its resource is decoded,
// and Chrome decodes asynchronously even for an image already in cache — the
// first paint after the swap draws nothing for that layer and schedules the
// decode. Worse for the tinted layers: `--form` is a CSS mask-image, and a
// mask whose bitmap is not ready yet masks the fill away completely, so the
// tinted body vanished rather than merely arriving late. Every render
// therefore left a sub-frame hole where the character simply was not there,
// which is the reported blink.
//
// THE FIX, and the two invariants it rests on:
//
//   1. IDENTITY. The subtree is built ONCE (buildSceneSkeleton) and then
//      mutated. One element per layer/slot, alive for the life of the page.
//      Nothing on a render path creates, removes or `innerHTML`-clears an
//      element, and `src` / `--form` are assigned only when the value
//      actually changed (render/tint.ts's setSrc / updateTintLayer). A
//      render where nothing changed writes nothing and repaints nothing.
//
//   2. DECODE-FREE FRAME SWAPS. The nine developer frames are STACKED —
//      nine pre-built form layers and nine pre-built base images, each
//      permanently pointed at its own file — and a frame swap is two style
//      writes (the base stack toggles `display`, the mask-bearing form stack
//      toggles `opacity`, and setFormShown() explains why those differ). No
//      `src` and no mask URL is ever reassigned on the animation path, so no
//      swap can wait on a decode. render/preload.ts additionally fetches and
//      decode()s every sprite the scene can show at startup, so even a
//      frame's FIRST appearance draws from a bitmap the renderer already
//      holds.
//
// The teardown was also doing three jobs implicitly, which are now explicit:
// a slot with no sprite (an `*_none` item) used to stay hidden because the
// holder was left empty and now hides its <img>; a chair with no catalog
// item at all used to draw nothing for the same reason and now hides its
// holder; and "exactly one child per holder" used to be guaranteed by
// clearing first and is now guaranteed by never adding a second child.
//
// Behaviour is otherwise unchanged: same poses, same beat timing, same
// y-offsets, same layer order and occlusion, same tint handling.
import { byId, spriteImg } from '../dom';
import * as store from '../state/store';
import {
  CHAIR_RECT, CHAIR_Z_DETAIL, CHAIR_Z_FORM, DEV_RECT, DEV_Z_BASE, DEV_Z_FORM, DEV_Z_STYLE,
  FRAME_FOR_STATE, HIT_RECT, HIT_Z, SCENERY, SLOT_RECT, SLOT_Z
} from '../geometry';
import { buildTintLayer, plainImg, positionEl, setSrc, updateTintLayer } from './tint';
import { warmCatalogSprites, warmSprites, warmStaticSprites } from './preload';
import { handleSpriteSentinelError } from './overlays';
import type { Rect } from '../geometry';
import type { CatalogMessage } from '../wire';

const scene = byId<HTMLDivElement>('scene-sprites');

const SLOT_IDS = ['wall', 'plant', 'buddy', 'beverage', 'keyboard', 'mouse'];

let sceneBuilt = false;
// Every node below is created exactly once, by buildSceneSkeleton().
const slotImgs: Record<string, HTMLImageElement> = {};   // slot id -> its one sprite <img>
let chairHolder: HTMLDivElement;
let chairForm: HTMLDivElement;                            // buildTintLayer wrapper
let chairDetail: HTMLImageElement;
let devHolder: HTMLDivElement;
const devFormLayers: Record<string, HTMLDivElement> = {}; // frame -> its tinted form layer
const devBaseImgs: Record<string, HTMLImageElement> = {}; // frame -> its base <img>
let devStyleImg: HTMLImageElement;                        // the one hoodie overlay
let monitorImg: HTMLImageElement;                         // SCENERY's monitor, whose src the shake swaps
const hitRegions: Record<string, HTMLDivElement> = {};    // SCENE-REACTIONS click targets
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
//   0. dev react    - SCENE-REACTIONS, added after P3: the player CLICKED the
//                     character, so this outranks even the celebration for
//                     the ~1s it lasts (see "PRECEDENCE, AND WHY A CLICK WINS"
//                     below - it can never be running at the same time as a
//                     celebration anyway)
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
  breath: -1, stretch: -2, cheer_a: -1, cheer_b: -3,
  react_a: -2, react_b: -1
};
// SCENE-REACTIONS: the SAME contract on the horizontal axis, because the
// "hey!" react leans the torso sideways as well as lifting it — hood and back
// together (gen_assets.py's BODY_DX, asserted rigid and capped at +-1px), so
// the one garment overlay follows by one more integer offset instead of the
// art forking into per-frame overlays. Every pre-react frame is 0, which is
// why adding this column changed nothing that was already on screen. Printed
// by `python3 tools/gen_assets.py` as "FRAME_OVERLAY_DX (render/scene.ts
// needs this second column)"; `sleep` is 0 here for the same reason it is 0
// above (it is the one frame whose hood and back do not move rigidly, and its
// slight garment offset is the documented pre-existing simplification).
const FRAME_OVERLAY_DX: Record<string, number> = {
  idle: 0, type_a: 0, type_b: 0, mouse: 0, sleep: 0,
  breath: 0, stretch: 0, cheer_a: 0, cheer_b: 0,
  react_a: -1, react_b: 1
};

// Every developer frame the scene can ever paint — derived from the table
// above rather than written out a second time, so a frame added to the art
// (and therefore to FRAME_OVERLAY_DY) automatically gets its stacked layers
// built and its sprites pre-warmed. A frame missing from here would fall off
// the stacked-layer lookup in renderDev(), so deriving it is not a
// convenience, it is the thing that keeps the two in step.
const DEV_FRAMES = Object.keys(FRAME_OVERLAY_DY);

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
  // SCENE-REACTIONS: a real server event outranks a click that is still
  // playing out (the other direction — a click during a celebration — is
  // refused in queueReact).
  cancelDevReact();
  ambientQueue = [];
  ambientFrame = '';
  celebrateQueue = CELEBRATE_SCRIPT.slice();
  celebrateFrame = celebrateQueue.shift() as string;
  breathIn = ticksUntil(BREATH_BAND_S);
  stretchIn = ticksUntil(STRETCH_BAND_S);
  if (sceneBuilt) renderDev();   // start on the event, not up to 200ms later
}

// =========================================================================
// SCENE-REACTIONS — click the room and the room answers
// (docs/plan/ROADMAP.md "SCENE-REACTIONS", docs/ui-spec.md §12)
// =========================================================================
//
// Four clickable things: the developer, the monitor, the equipped beverage and
// the equipped buddy. Each has its own 2-frame react in the art (commit
// f67baf7) and its own independent little state machine below. Client-side
// only, exactly as the item is scoped: no ClientAction, no wire field, no
// coin, no persistence, nothing the server has to know about or could
// disagree with. A click makes the room feel inhabited and changes nothing
// else.
//
// WHERE THE CLICKS COME FROM, and why there is no coordinate maths. Each item
// gets one invisible absolutely-positioned <div> inside #scene-sprites at its
// room rect (geometry.ts's HIT_RECT / SLOT_RECT) with a `click` listener. The
// scene is drawn at scale(2) inside a #root that is itself translated and
// scaled to fit the window (render/viewport.ts), and a CSS transform is part
// of the box the browser hit-tests — so a click at 3x in a letterboxed window
// maps back through both transforms natively and lands on the same room
// pixel it looks like it landed on. That is why this module reads no
// clientX, calls no getBoundingClientRect, and needs no inverse transform:
// the hit test happens in the scene's own coordinate space because the
// targets LIVE in it. (The same property the modals already rely on — see
// game.css "Window fit: the modal dialogs".)
//
// PRECEDENCE, AND WHY A CLICK WINS. The developer's react is queued at the
// same level as the P3 celebration and checked just above it (sceneDevFrame),
// which means it interrupts the ambient breath/stretch AND outranks the
// typing/mouse/idle pose for its ~1s. That is deliberate: a click that
// visibly does nothing while you happen to be typing reads as broken, and the
// react is brief and returns to whatever the server says you are doing —
// the pose bookkeeping in the tick below keeps running underneath, so typing
// resumes on the very next tick with the cycle it would have had anyway.
// Nothing here CLAIMS activity: the react frames are a startle and a lean,
// they are not the typing pose, and the state-driven pose is what the frame
// falls back to the moment the beat ends.
//
// The two directions of the celebration/react overlap are both closed rather
// than left to chance: a click is REFUSED while a celebration is playing (a
// real server event is not something a click may cut off), and onCelebrate()
// cancels a running react (the event is the more important thing to show). So
// only one of the two is ever on screen.
//
// SLEEP — THE DECIDED CASE. Clicking a SLEEPING dexel does nothing, and it
// says so: while `onBreak` the developer's hit region is hidden entirely, so
// the cursor stays the ordinary arrow and no click is swallowed by a dead
// target. Two reasons this is the honest choice rather than a stir animation:
// the react frames are authored on the AWAKE pose (hands at the keyboard),
// so playing one from the sleep pose would snap the figure upright into a
// hands-on-keys pose and flop it back — slapstick, and momentarily a pose
// that looks like working when the server has said for 30s+ that nobody is
// there; and P3 already suppresses the celebration bounce while asleep for
// exactly the same reason (a sleeping character must not perform). Waking it
// is not on the table either: the mood is the server's to report from real
// keystrokes (ADR 0010), and a client that "woke" the character would be
// asserting a state nobody sent. The other three items DO still react while
// the dexel sleeps — the monitor, the mug and the buddy are their own layers
// and say nothing about whether the human is at the desk.
//
// THE PROPS ARE INDEPENDENT LAYERS. The monitor is SCENERY and the beverage /
// buddy are slot sprites: each is one <img> whose `src` the scheduler swaps,
// which touches neither the developer's frame logic nor any other item's. The
// monitor's react in particular is a SRC SWAP AND NOTHING ELSE — the element
// must never move. tools/gen_assets.py bakes that constraint into the art
// (assert_monitor_shake): the DOM terminal's 11 lines of text sit over the
// glass with only 2px of margin, so the shake is authored as a +-1px
// HORIZONTAL displacement of the monitor's head *inside* the sprite, with the
// neck, foot and contact shadow byte-identical to monitor.png. Moving the
// element instead — a `left` nudge, a transform — would slide the glass out
// from under text that does not move with it.
//
// ANTI-MASH. One react at a time per item (a click during a react is
// dropped, never queued) plus a per-item cooldown after it ends, so holding
// the mouse down and clicking as fast as possible cannot strobe a sprite: the
// worst case is one ~1s beat per ~2s per item. The cooldown is per ITEM, so
// mashing the mug never blocks the buddy. No click is ever "spent" on
// nothing: a refused click leaves the cooldown untouched.
const REACT_KEYS = ['dev', 'monitor', 'beverage', 'buddy'];
// One tick per frame, on the SAME 200ms timer as everything else in this file
// (P3's rule: one interval, and it repaints only when the frame changed).
//
//   dev       400ms of startle (react_a: body 2px up, 1px recoil away, right
//             hand off the keys) then 600ms of the acknowledge lean back
//             toward the viewer (react_b) — the art's own two-beat intent,
//             1.0s total, and not a→b→a: a→b→rest IS the return, because
//             `rest` is whatever pose the server says you are in.
//   monitor   a wobble: -1, +1, -1, +1 px, 800ms.
//   props     a hop and a settle: 1px up, 2px up (held), back to 1px, 800ms.
//             The beverage/buddy react frames are authored as stage 1 and 2 of
//             one hop with the contact shadow planted, so this ordering is the
//             arc the art was drawn for.
const REACT_SCRIPT: Record<string, string[]> = {
  dev: ['react_a', 'react_a', 'react_b', 'react_b', 'react_b'],
  monitor: ['a', 'b', 'a', 'b'],
  beverage: ['a', 'b', 'b', 'a'],
  buddy: ['a', 'b', 'b', 'a']
};
const REACT_COOLDOWN_TICKS = 6;   // 1.2s of quiet after a beat ends
// The prop react sprites, keyed by CATALOG ITEM ID and written out rather
// than derived from item.sprite by string surgery. Two reasons, both real:
// buddy_bot's `sprite` is buddy_bot_a.png (frame A of its blink pair) while
// its react files are buddy_bot_react_a/b.png, so no suffix rule covers the
// set; and an explicit table means an item with NO react art (a future
// beverage, say) is simply absent, which is a case this module already has to
// handle for `buddy_none` — no react, and no hit region either, instead of a
// 404 and a hole where the sprite was. The wire is untouched: it still
// carries exactly one sprite filename per item, and this is the client's own
// presentation table, the same way FRAME_OVERLAY_DY is.
const PROP_REACT_SPRITES: Record<string, [string, string]> = {
  bev_mug: ['bev_mug_react_a.png', 'bev_mug_react_b.png'],
  bev_thermos: ['bev_thermos_react_a.png', 'bev_thermos_react_b.png'],
  bev_teacup: ['bev_teacup_react_a.png', 'bev_teacup_react_b.png'],
  bev_energy: ['bev_energy_react_a.png', 'bev_energy_react_b.png'],
  buddy_duck: ['buddy_duck_react_a.png', 'buddy_duck_react_b.png'],
  buddy_bot: ['buddy_bot_react_a.png', 'buddy_bot_react_b.png'],
  buddy_cat: ['buddy_cat_react_a.png', 'buddy_cat_react_b.png']
};
const MONITOR_REST_SPRITE = 'monitor.png';
const MONITOR_SHAKE_SPRITES: [string, string] = ['monitor_shake_a.png', 'monitor_shake_b.png'];
// Every react bitmap the scene can show, warmed at startup with the rest of
// them (render/preload.ts). A prop react is a `src` swap on a live <img>, and
// an undecoded bitmap paints NOTHING for a frame or two — the first click on
// each item would have blinked the mug/buddy/monitor out of existence before
// hopping it. All 16 files, not just the equipped ones: equipping is a click
// away in the store. (The developer's four react files need no entry here —
// they are stacked layers like every other dev frame and ride
// warmStaticSprites via DEV_FRAMES.)
const REACT_SPRITES: string[] = MONITOR_SHAKE_SPRITES.concat(
  ...Object.keys(PROP_REACT_SPRITES).map(function (id) { return PROP_REACT_SPRITES[id]; })
);

interface ReactState {
  queue: string[];   // frames left in the beat
  frame: string;     // '' = not reacting
  cooldown: number;  // ticks of enforced quiet left after a beat
  painted: string;   // the frame this item's <img> is currently showing
}
const reacts: Record<string, ReactState> = {};
REACT_KEYS.forEach(function (key) {
  reacts[key] = { queue: [], frame: '', cooldown: 0, painted: '' };
});

// The file a prop shows for react frame `tag`, or null when the item equipped
// in that slot has no react art (which is also how `buddy_none` — no item,
// no sprite — answers).
function reactFileFor(key: string, tag: string): string | null {
  if (key === 'monitor') return MONITOR_SHAKE_SPRITES[tag === 'a' ? 0 : 1];
  const item = store.equippedItemFor(key);
  const pair = item ? PROP_REACT_SPRITES[item.id] : undefined;
  if (!pair) return null;
  return pair[tag === 'a' ? 0 : 1];
}

// The sprite a slot must paint RIGHT NOW: its react frame while one is
// playing, otherwise the equipped item's own sprite. Called from
// renderSlotSprite, which is what makes an in-flight react survive the ~1Hz
// state broadcast instead of being reset to the rest sprite mid-hop.
function slotReactSprite(slotId: string): string | null {
  const r = reacts[slotId];
  return r && r.frame ? reactFileFor(slotId, r.frame) : null;
}

// Paints one item's current react frame, and only when it changed.
function paintReact(key: string): void {
  const r = reacts[key];
  if (r.painted === r.frame) return;
  r.painted = r.frame;
  if (key === 'dev') { renderDev(); return; }
  if (key === 'monitor') {
    setSrc(monitorImg, r.frame ? reactFileFor('monitor', r.frame) : MONITOR_REST_SPRITE);
    return;
  }
  renderSlotSprite(key);
}

// A click on one of the four hit regions. Every refusal below is silent by
// design — there is no error state for "the room did not feel like it".
function queueReact(key: string): void {
  const state = store.getState();
  if (!state || !sceneBuilt) return;
  const r = reacts[key];
  if (r.frame || r.queue.length > 0 || r.cooldown > 0) return;   // anti-mash
  if (key === 'dev') {
    if (state.activeState === 'onBreak') return;                 // asleep: see above
    if (celebrateFrame || celebrateQueue.length > 0) return;      // a real event is playing
    ambientQueue = [];                                           // the click outranks the breath
    ambientFrame = '';
  } else if (!reactFileFor(key, 'a')) {
    return;                                                      // nothing there to react
  }
  r.queue = REACT_SCRIPT[key].slice();
  r.frame = r.queue.shift() as string;
  paintReact(key);   // start on the click, not up to 200ms later
}

// One tick of every item's beat. A beat that just ended starts that item's
// cooldown; an item doing nothing burns its cooldown down.
function advanceReacts(): void {
  REACT_KEYS.forEach(function (key) {
    const r = reacts[key];
    if (r.frame) {
      r.frame = r.queue.length > 0 ? r.queue.shift() as string : '';
      if (!r.frame) r.cooldown = REACT_COOLDOWN_TICKS;
      paintReact(key);
    } else if (r.cooldown > 0) {
      r.cooldown -= 1;
    }
  });
}

// Drops a developer react on the floor. Called when the character falls
// asleep mid-beat (the pose the server reports wins the moment it changes)
// and from onCelebrate() (a real event outranks a click). The cooldown is
// left alone: the click was honoured, it just got overtaken.
function cancelDevReact(): void {
  const r = reacts.dev;
  if (!r.frame && r.queue.length === 0) return;
  r.queue = [];
  r.frame = '';
  r.painted = '';
  if (sceneBuilt) renderDev();
}

// One invisible click target. Built once with the rest of the skeleton and
// only ever shown/hidden after that (BUG-1's identity invariant applies to
// these too — and `display: none` is exactly what removes both the click and
// the pointer cursor for an item that is not there).
function buildHitRegion(key: string, rect: Rect): HTMLDivElement {
  const el = document.createElement('div');
  el.className = 'hit-region';
  el.setAttribute('aria-hidden', 'true');   // decoration: it carries no information
  el.style.position = 'absolute';
  el.style.zIndex = String(HIT_Z);
  positionEl(el, rect);
  el.addEventListener('click', function () { queueReact(key); });
  return el;
}

// Which hit regions are live right now. The monitor's is unconditional (it is
// scenery — always there); the developer's disappears while asleep, and the
// two slot regions disappear when the slot is empty or holds an item with no
// react art, so there is never a pointer cursor over something that would not
// answer.
function renderHitRegions(): void {
  const state = store.getState()!;
  setShown(hitRegions.dev, state.activeState !== 'onBreak');
  setShown(hitRegions.beverage, reactFileFor('beverage', 'a') !== null);
  setShown(hitRegions.buddy, reactFileFor('buddy', 'a') !== null);
}

// A bare absolutely-positioned container. Every holder below is one, and
// none of them is ever emptied again once built.
function buildHolder(): HTMLDivElement {
  const holder = document.createElement('div');
  holder.style.position = 'absolute';
  return holder;
}

// One <img> for a slot sprite, sized to its slot rect and pinned to its
// holder's origin. Starts hidden and with no src: the first render decides
// whether the slot has a sprite at all.
function buildSlotImg(slot: string): HTMLImageElement {
  const img = spriteImg();
  img.className = 'layer sprite';
  img.alt = '';
  img.style.position = 'absolute';
  positionEl(img, { left: 0, top: 0, w: SLOT_RECT[slot].w, h: SLOT_RECT[slot].h });
  img.style.display = 'none';
  return img;
}

// Builds the ENTIRE scene subtree, once. Every element the compositor will
// ever need exists after this returns; the render functions below only ever
// change attributes and styles on these nodes.
function buildSceneSkeleton(): void {
  scene.innerHTML = ''; // once, on a subtree that is still empty — never on a render path
  SCENERY.forEach(function (s) {
    const img = plainImg(s.file, s);
    img.style.zIndex = String(s.z);
    if (s.id) img.id = s.id;
    if (s.file === 'room_back.png') img.addEventListener('error', handleSpriteSentinelError);
    // SCENE-REACTIONS: held so the shake is a src swap on a known element
    // (and so renderScene's onBreak dimming toggles the same one) rather than
    // a document lookup per tick.
    if (s.file === 'monitor.png') monitorImg = img;
    scene.appendChild(img);
  });
  SLOT_IDS.forEach(function (slot) {
    const holder = buildHolder();
    holder.style.zIndex = String(SLOT_Z[slot]);
    positionEl(holder, SLOT_RECT[slot]);
    slotImgs[slot] = buildSlotImg(slot);
    holder.appendChild(slotImgs[slot]);
    scene.appendChild(holder);
  });

  // Chair: one tinted form layer + one detail overlay. Both are re-pointed
  // in place when a different chair is equipped (a cold path — a click in
  // the store, not an animation tick), which is why the chair does NOT need
  // the stacked treatment the developer frames get below. The holder's rect
  // is per-chair (CHAIR_RECT) and is set by renderChair().
  chairHolder = buildHolder();
  chairForm = buildTintLayer(null, '#ffffff');
  chairForm.style.zIndex = String(CHAIR_Z_FORM);
  chairDetail = spriteImg();
  chairDetail.className = 'layer sprite';
  chairDetail.alt = '';
  chairDetail.style.position = 'absolute';
  chairDetail.style.left = '0';
  chairDetail.style.top = '0';
  chairDetail.style.zIndex = String(CHAIR_Z_DETAIL);
  chairDetail.style.display = 'none';
  chairHolder.appendChild(chairForm);
  chairHolder.appendChild(chairDetail);
  scene.appendChild(chairHolder);

  // Developer composite. Layer order is unchanged (form 10 < style 11 <
  // base 12), but form and base are now NINE stacked layers each — one per
  // animation frame, every one permanently pointed at its own sprite — with
  // exactly one form and one base showing at a time. That is what makes a
  // frame swap two style writes (see setFormShown / setShown) instead of two
  // image loads plus a mask swap. The three stacks are siblings rather than
  // nine per-frame groups because the single hoodie overlay has to composite
  // BETWEEN form and base, and z-index only orders siblings.
  devHolder = buildHolder();
  positionEl(devHolder, DEV_RECT);
  const devRect = { left: 0, top: 0, w: DEV_RECT.w, h: DEV_RECT.h };
  DEV_FRAMES.forEach(function (frame) {
    const form = buildTintLayer('dev_form_' + frame + '.png', '#ffffff');
    form.style.zIndex = String(DEV_Z_FORM);
    positionEl(form, devRect);
    // OPACITY, not display, for this one stack — see setFormShown().
    form.style.opacity = '0';
    devFormLayers[frame] = form;
    devHolder.appendChild(form);
  });
  devStyleImg = spriteImg();
  devStyleImg.className = 'layer sprite';
  devStyleImg.alt = '';
  devStyleImg.style.position = 'absolute';
  devStyleImg.style.left = '0';
  devStyleImg.style.width = DEV_RECT.w + 'px';
  devStyleImg.style.height = DEV_RECT.h + 'px';
  devStyleImg.style.zIndex = String(DEV_Z_STYLE);
  devStyleImg.style.display = 'none';
  devHolder.appendChild(devStyleImg);
  DEV_FRAMES.forEach(function (frame) {
    const base = spriteImg();
    base.className = 'layer sprite';
    base.alt = '';
    setSrc(base, 'dev_base_' + frame + '.png');
    base.style.position = 'absolute';
    positionEl(base, devRect);
    base.style.zIndex = String(DEV_Z_BASE);
    base.style.display = 'none';
    devBaseImgs[frame] = base;
    devHolder.appendChild(base);
  });
  scene.appendChild(devHolder);

  // Warm every frame's bitmap before the first beat can ask for it. The base
  // stack is display:none until used, and a display:none element is never
  // painted and therefore never decoded, so the stack alone would still pay
  // one decode the first time each pose appeared. (The form stack solves the
  // same problem for its masks a different way — see setFormShown.)
  warmStaticSprites(DEV_FRAMES);
  // SCENE-REACTIONS: and the react bitmaps the props swap to on a click.
  warmSprites(REACT_SPRITES);

  // SCENE-REACTIONS click targets, on top of every sprite layer (HIT_Z) and
  // invisible. Added LAST so they are also last in document order, which is
  // what settles ties between the two that overlap (the developer's region
  // covers the keyboard their hands rest on; neither the keyboard nor the
  // chair is clickable, so there is no ambiguity to resolve beyond that).
  hitRegions.dev = buildHitRegion('dev', HIT_RECT.dev);
  hitRegions.monitor = buildHitRegion('monitor', HIT_RECT.monitor);
  hitRegions.beverage = buildHitRegion('beverage', SLOT_RECT.beverage);
  hitRegions.buddy = buildHitRegion('buddy', SLOT_RECT.buddy);
  REACT_KEYS.forEach(function (key) { scene.appendChild(hitRegions[key]); });

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
  // SCENE-REACTIONS sits at the top: the player clicked the character, and
  // for ~1s that is the most important thing on screen. It can never be set
  // at the same time as celebrateFrame (queueReact / onCelebrate).
  if (reacts.dev.frame) return reacts.dev.frame;
  if (celebrateFrame) return celebrateFrame;
  if (ambientFrame) return ambientFrame;
  return currentDevFrame();
}

// Sets `display` only when it differs, so a slot whose item has not changed
// is left completely untouched (no style write, no invalidation, no repaint).
function setShown(el: HTMLElement, shown: boolean): void {
  const want = shown ? '' : 'none';
  if (el.style.display !== want) el.style.display = want;
}

// The nine stacked developer FORM layers are hidden with opacity, not
// display, and this is the one non-obvious line in the flicker fix.
//
// A form layer's colour comes from `.tint-fill`, whose silhouette is a CSS
// mask-image (the `--form` PNG). `display: none` means the layer is never
// painted, and an unpainted layer's mask is never decoded — so the FIRST time
// a pose appeared, its mask had no bitmap yet, the flat tint was masked away
// entirely, and only the grayscale `.tint-shade` multiplied over nothing
// showed: the character flashed WHITE for one composited frame. That is
// exactly what the screencast of the display-based version caught (3 white
// frames in 126, one per pose's first appearance), and it is what the owner
// was seeing.
//
// `opacity: 0` keeps the layer in the paint tree, so all nine masks are
// decoded up front and every subsequent swap is a compositor-only alpha
// change with nothing left to decode. An alpha-0 layer contributes no pixels,
// so the composite is identical to the display-based version — and the cost
// is bounded: painting happens on invalidation, so nine static 192x76 layers
// are rasterised once and then only re-composited when a swap actually
// changes one.
function setFormShown(el: HTMLElement, shown: boolean): void {
  const want = shown ? '1' : '0';
  if (el.style.opacity !== want) el.style.opacity = want;
}

function renderSlotSprite(slotId: string): void {
  const img = slotImgs[slotId];
  const item = store.equippedItemFor(slotId);
  // *_none item (or unresolved default): the slot stays hidden. The old code
  // got this for free by leaving the emptied holder empty; with a persistent
  // <img> it has to be said out loud. The src is left pointing at whatever
  // it last showed rather than cleared to '' — an empty src is a broken-image
  // request, and the element is not painted anyway.
  if (!item || !item.sprite) { setShown(img, false); return; }
  // SCENE-REACTIONS: while this slot is mid-react it paints the react frame
  // instead of the rest sprite — including on the ~1Hz state broadcast, which
  // would otherwise reset the mug to its resting pose halfway through a hop.
  setSrc(img, slotReactSprite(slotId) || item.sprite);
  setShown(img, true);
}

function renderChair(): void {
  const state = store.getState()!;
  const eq = state.equipped.chair;
  const item = store.equippedItemFor('chair');
  // No chair item at all, not even a free default — nothing to draw. Same
  // note as the slots above: the emptied holder used to express this.
  if (!item) { setShown(chairHolder, false); return; }
  setShown(chairHolder, true);
  // The geometry writes below are unguarded on purpose: a chair change is a
  // cold path, and re-writing an identical `left: 92px` is compared and
  // dropped by the style engine — it invalidates nothing. Only the image and
  // mask assignments need the explicit guards (setSrc / updateTintLayer),
  // because those are the ones that would cost a decode.
  const rect = CHAIR_RECT[item.id] || CHAIR_RECT.chair_basic;
  positionEl(chairHolder, rect);
  positionEl(chairForm, { left: 0, top: 0, w: rect.w, h: rect.h });
  chairDetail.style.width = rect.w + 'px';
  chairDetail.style.height = rect.h + 'px';
  updateTintLayer(chairForm, item.sprite, store.tintHexFor((eq && eq.tintId) || item.defaultTint));
  if (item.detail) {
    setSrc(chairDetail, item.detail);
    setShown(chairDetail, true);
  } else {
    setShown(chairDetail, false);
  }
}

// The tint currently written onto the nine stacked form layers. Tracked so a
// tint change (a store click) writes nine custom properties once, instead of
// nine per animation tick — and so the animation path writes none at all.
let devTintHex = '';

// The developer composite is the one non-generic slot (art-direction.md
// "Scene contract"): dev_form_<frame> (tinted by the hoodie's tint) +
// hoodie_<style> (the equipped hoodie item's own palette-pure file,
// trusted straight off item.sprite — the wire already carries the true
// single-file filename; see internal/game/catalog.go) + dev_base_<frame>
// (frame-driven, always present).
function renderDev(): void {
  const frame = sceneDevFrame();
  const state = store.getState()!;
  const eq = state.equipped.hoodie;
  const item = store.equippedItemFor('hoodie');
  const tintHex = store.tintHexFor((eq && eq.tintId) || (item && item.defaultTint));

  // The hoodie's tint belongs to the FIGURE, not to one pose, so every
  // stacked form layer carries it — including the eight currently hidden
  // ones, which must already be correct at the instant they are shown.
  if (tintHex !== devTintHex) {
    DEV_FRAMES.forEach(function (f) { devFormLayers[f].style.setProperty('--tint', tintHex); });
    devTintHex = tintHex;
  }

  // The garment overlay is ONE file for every pose (see FRAME_OVERLAY_DY),
  // so it is one element whose src changes only when a different hoodie is
  // equipped.
  if (item && item.sprite) {
    setSrc(devStyleImg, item.sprite);
    setShown(devStyleImg, true);
  } else {
    setShown(devStyleImg, false);
  }

  if (frame === renderedDevFrame) return; // nothing to swap — the common tick
  // THE FRAME SWAP. Two show/hide pairs on elements that already hold their
  // decoded bitmaps: no element is created, no `src` is assigned and no mask
  // URL changes, so there is no decode for a paint to wait on and therefore
  // no frame in which the character is absent.
  if (renderedDevFrame) {
    setFormShown(devFormLayers[renderedDevFrame], false);
    setShown(devBaseImgs[renderedDevFrame], false);
  }
  setFormShown(devFormLayers[frame], true);
  setShown(devBaseImgs[frame], true);
  // P3: ride the frame's rigid body lift so the garment's own marks stay
  // printed on the fabric that moved (see FRAME_OVERLAY_DY). A `top` change
  // moves an already-decoded bitmap; it never re-decodes it.
  devStyleImg.style.top = (FRAME_OVERLAY_DY[frame] || 0) + 'px';
  // SCENE-REACTIONS: the react frames also lean the body +-1px sideways, and
  // the garment has to lean with the fabric it is printed on
  // (FRAME_OVERLAY_DX). 0 for every other frame, so this write is a no-op
  // everywhere it was a no-op before.
  devStyleImg.style.left = (FRAME_OVERLAY_DX[frame] || 0) + 'px';
  renderedDevFrame = frame;
}

// The catalog this module has already pre-warmed. The catalog arrives once
// per connection and is static thereafter (ui-spec.md §6), so this is
// identity-compared rather than diffed.
let warmedCatalog: CatalogMessage | null = null;

export function renderScene(): void {
  const state = store.getState();
  const catalog = store.getCatalog();
  if (!state || !catalog) return;
  if (!sceneBuilt) buildSceneSkeleton();
  if (catalog !== warmedCatalog) {
    // Done here rather than at the WS seam so that ?dev=1 (which seeds the
    // store directly, bypassing ws-client.ts) warms the same sprites.
    warmCatalogSprites(catalog);
    warmedCatalog = catalog;
  }
  SLOT_IDS.forEach(renderSlotSprite);
  renderChair();
  renderDev();
  renderHitRegions();
  monitorImg.classList.toggle('monitor-onbreak', state.activeState === 'onBreak');
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

  // SCENE-REACTIONS. The sleep check first: if the character fell asleep
  // mid-react the server's pose wins immediately (and its hit region is
  // already gone). Then one tick of every item's beat — the props repaint
  // themselves inside advanceReacts, the developer's frame is picked up by
  // the sceneDevFrame() check at the bottom of this tick like every other
  // beat.
  if (state.activeState === 'onBreak') cancelDevReact();
  advanceReacts();

  // Pose bookkeeping — byte-for-byte the same decisions as before P3 (the
  // mouse hold still burns one tick per tick and the typing cycle still
  // toggles only while coding); only the early returns are gone, because the
  // ambient scheduler below needs to run on an idle tick too.
  if (state.activeState === 'onBreak') mouseHoldTicks = 0;
  else if (mouseHoldTicks > 0) mouseHoldTicks -= 1;
  else if (state.activeState === 'coding') devFrameIndex = devFrameIndex === 0 ? 1 : 0;

  advanceAmbient(!celebrateFrame && celebrateQueue.length === 0 &&
                 !reacts.dev.frame && reacts.dev.queue.length === 0 &&
                 mouseHoldTicks === 0 && state.activeState === 'idle');

  if (sceneBuilt && sceneDevFrame() !== renderedDevFrame) renderDev();
}, TICK_MS);
