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
  if (state.activeState === 'coding') return devFrameIndex === 0 ? 'type_a' : 'type_b';
  return FRAME_FOR_STATE[state.activeState] || 'idle';
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
  const frame = currentDevFrame();
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
    style.style.top = '0';
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
// requestAnimationFrame, per art-direction.md "Visual states".
setInterval(function () {
  const state = store.getState();
  if (!state) return;
  if (state.activeState === 'coding') {
    devFrameIndex = devFrameIndex === 0 ? 1 : 0;
    if (sceneBuilt) renderDev();
  }
}, 200);
