// FEATURE/LOGIC layer — the store modal (ui-spec.md §4). Owns every DOM
// node under #store, its own UI-selection state (storeUI), and is the
// only module that sends BUY_ITEM/BUY_TINT/EQUIP_ITEM/STORE_OPEN/
// STORE_CLOSE. Reads the central store (../state/store) for catalog/state
// and reuses the render layer's generic sprite/tint primitives
// (../render/tint, ../render/scene's currentDevFrame) to compose its
// preview pane — it never reaches into the scene compositor's own DOM.
import { byId } from '../dom';
import * as store from '../state/store';
import { sendAction, setStoreOpenHoldDesired } from '../state/ws-client';
import { assetUrl } from '../assets';
import { clamp, fmtInt, truncate } from '../format';
import { CHAIR_RECT, DEV_RECT, SLOT_RECT } from '../geometry';
import { buildTintLayer, plainImg, positionEl, swatchColor } from '../render/tint';
import { currentDevFrame } from '../render/scene';
import { flashInsufficientFunds } from '../render/flash';
import type { CatalogItem, CatalogSlot } from '../wire';

const el = {
  store: byId<HTMLDialogElement>('store'),
  storeOpenBtn: byId<HTMLButtonElement>('store-open'),
  storeClose: byId<HTMLButtonElement>('store-close'),
  scrim: byId<HTMLDivElement>('scrim'),
  storeCash: byId('store-cash').querySelector('.value') as HTMLElement,
  catList: document.querySelector('#store-cats .cat-list') as HTMLElement,
  grid: byId<HTMLDivElement>('store-grid'),
  scrollTrack: byId<HTMLDivElement>('store-scroll'),
  scrollThumb: document.querySelector('#store-scroll .thumb-bar') as HTMLElement,
  previewViewport: byId<HTMLDivElement>('store-preview-viewport'),
  previewName: byId('store-preview-name'),
  previewState: byId('store-preview-state'),
  previewColor: byId('store-preview-color')
};

interface StoreUI {
  initialized: boolean;
  catIndex: number;
  cardIndex: number;
  focus: 'cats' | 'cards';
  selectedTintByItem: Record<string, string>;
}

const storeUI: StoreUI = {
  initialized: false,
  catIndex: 0,
  cardIndex: 0,
  focus: 'cards', // 'cats' | 'cards'
  selectedTintByItem: {}
};

export function isOpen(): boolean { return el.store.open; }

function selectedTintFor(item: CatalogItem | undefined): string | null {
  if (!item) return null;
  if (storeUI.selectedTintByItem.hasOwnProperty(item.id)) {
    return storeUI.selectedTintByItem[item.id];
  }
  const state = store.getState()!;
  const eq = state.equipped[item.slot];
  if (eq && eq.itemId === item.id && eq.tintId) return eq.tintId;
  return item.defaultTint;
}

// ---------------------------------------------------------------------
// Categories
// ---------------------------------------------------------------------
export function buildCats(): void {
  const catalog = store.getCatalog();
  if (!catalog) return;
  el.catList.innerHTML = '';
  catalog.slots.forEach(function (slot, idx) {
    const row = document.createElement('div');
    row.className = 'cat-row';
    row.dataset.index = String(idx);
    const gutter = document.createElement('span');
    gutter.className = 'gutter';
    const label = document.createElement('span');
    label.textContent = slot.name.toUpperCase();
    const check = document.createElement('span');
    check.className = 'check';
    const state = store.getState();
    const eq = state && state.equipped[slot.id];
    const freeItem = store.freeDefaultItem(slot.id);
    if (eq && freeItem && eq.itemId !== freeItem.id) check.textContent = '✓';
    row.appendChild(gutter);
    row.appendChild(label);
    row.appendChild(check);
    row.addEventListener('mouseenter', function () { row.classList.add('hovered'); });
    row.addEventListener('mouseleave', function () { row.classList.remove('hovered'); });
    row.addEventListener('click', function () { selectCategory(idx); });
    el.catList.appendChild(row);
  });
  renderCatSelection();
}
function renderCatSelection(): void {
  const rows = el.catList.querySelectorAll('.cat-row');
  rows.forEach(function (row, idx) {
    const selected = idx === storeUI.catIndex;
    row.classList.toggle('selected', selected);
    (row.querySelector('.gutter') as HTMLElement).textContent = selected ? '>' : '';
  });
}
function selectCategory(idx: number): void {
  const catalog = store.getCatalog()!;
  idx = clamp(idx, 0, catalog.slots.length - 1);
  if (idx === storeUI.catIndex && storeUI.initialized) { renderCatSelection(); return; }
  storeUI.catIndex = idx;
  storeUI.cardIndex = 0;
  storeUI.focus = 'cats';
  renderCatSelection();
  buildGrid();
  el.grid.scrollTop = 0;
  updateScrollThumb();
  updatePreview();
}

// ---------------------------------------------------------------------
// Item grid / cards
// ---------------------------------------------------------------------
function currentSlot(): CatalogSlot { return store.getCatalog()!.slots[storeUI.catIndex]; }
function currentItems(): CatalogItem[] { return store.getCatalogBySlot(currentSlot().id); }
function currentItem(): CatalogItem | undefined { return currentItems()[storeUI.cardIndex]; }

interface CardAction {
  kind: 'buy' | 'insufficient' | 'buytint' | 'none' | 'equip';
  label: string;
}

// ui-spec.md §4.3 — the one action button, fixed precedence.
function computeCardAction(slot: CatalogSlot, item: CatalogItem, tintId: string | null): CardAction {
  const state = store.getState()!;
  const owned = (state.ownedItems || []).indexOf(item.id) !== -1;
  if (!owned) {
    if (state.devCash >= item.price) return { kind: 'buy', label: 'BUY ' + item.price };
    return { kind: 'insufficient', label: 'NEED ' + item.price };
  }
  if (slot.tintable) {
    if (!store.isTintOwned(item, tintId)) {
      const price = (store.getTintById(tintId as string) || { price: 40 }).price || 40;
      if (state.devCash >= price) return { kind: 'buytint', label: 'BUY COLOUR ' + price };
      return { kind: 'insufficient', label: 'NEED ' + price };
    }
  }
  const eq = state.equipped[slot.id];
  const sameEquip = !!eq && eq.itemId === item.id && (!slot.tintable || eq.tintId === tintId);
  if (sameEquip) return { kind: 'none', label: '✓ EQUIPPED' };
  return { kind: 'equip', label: 'EQUIP' };
}

function priceStateText(slot: CatalogSlot, item: CatalogItem, tintId: string | null): string {
  const state = store.getState()!;
  const owned = (state.ownedItems || []).indexOf(item.id) !== -1;
  if (!owned) return item.price + ' ◆';
  const eq = state.equipped[slot.id];
  const sameEquip = !!eq && eq.itemId === item.id && (!slot.tintable || eq.tintId === tintId);
  if (sameEquip) return 'OWNED · EQUIPPED';
  return 'OWNED';
}

export function buildGrid(): void {
  el.grid.innerHTML = '';
  const catalog = store.getCatalog();
  const state = store.getState();
  if (!catalog || !state) return;
  const slot = currentSlot();
  currentItems().forEach(function (item, idx) {
    const card = document.createElement('div');
    card.className = 'card';
    card.dataset.index = String(idx);

    const thumb = document.createElement('div');
    thumb.className = 'thumb';
    thumb.appendChild(buildThumb(slot, item));
    card.appendChild(thumb);

    const name = document.createElement('div');
    name.className = 'name';
    name.textContent = truncate(item.name, 21);
    card.appendChild(name);

    const priceState = document.createElement('div');
    priceState.className = 'price-state';
    card.appendChild(priceState);

    if (slot.tintable) {
      const swatches = document.createElement('div');
      swatches.className = 'swatches';
      catalog.tints.forEach(function (tint) {
        const chip = document.createElement('div');
        chip.className = 'swatch';
        chip.style.background = swatchColor(tint.hex);
        chip.dataset.tint = tint.id;
        chip.addEventListener('click', function (ev) {
          ev.stopPropagation();
          selectCard(idx);
          selectTint(item, tint.id);
        });
        swatches.appendChild(chip);
      });
      card.appendChild(swatches);
    }

    const action = document.createElement('button');
    action.className = 'nes-btn action';
    action.addEventListener('click', function (ev) {
      ev.stopPropagation();
      selectCard(idx);
      runCardAction(slot, item);
    });
    card.appendChild(action);

    card.addEventListener('mouseenter', function () { card.classList.add('hovered'); });
    card.addEventListener('mouseleave', function () { card.classList.remove('hovered'); });
    card.addEventListener('click', function () { selectCard(idx); });

    el.grid.appendChild(card);
  });
  refreshGridStates();
  updateScrollThumb();
}

// Tintable slots (hoodie, chair) get TWO thumbnail files — the store
// card's thumbnail runs the live tint recipe as the player clicks
// swatches (art-direction.md's tintable-thumbnail rule) — which the
// catalog now carries as explicit wire fields, item.thumbForm /
// item.thumbDetail (see internal/game/catalog.go). Falls back to the
// thumb_<id>_form.png / thumb_<id>_detail.png naming convention only if
// an older backend build hasn't sent those fields yet.
function buildThumb(slot: CatalogSlot, item: CatalogItem): HTMLElement {
  if (slot.tintable) {
    if (!item.sprite) { return document.createElement('span'); }
    const tintId = selectedTintFor(item);
    const formFile = item.thumbForm || ('thumb_' + item.id + '_form.png');
    const detailFile = item.thumbDetail || ('thumb_' + item.id + '_detail.png');
    const wrap = document.createElement('div');
    wrap.style.position = 'absolute';
    wrap.style.inset = '0';
    const tint = buildTintLayer(formFile, store.tintHexFor(tintId));
    tint.style.left = '0'; tint.style.top = '0'; tint.style.width = '100%'; tint.style.height = '100%';
    wrap.appendChild(tint);
    const detail = document.createElement('img');
    detail.alt = '';
    detail.src = assetUrl(detailFile) || '';
    detail.style.position = 'absolute';
    detail.style.inset = '0';
    detail.style.width = '100%';
    detail.style.height = '100%';
    wrap.appendChild(detail);
    return wrap;
  }
  if (!item.sprite) { return document.createElement('span'); }
  const img = document.createElement('img');
  img.alt = '';
  img.src = assetUrl(item.thumb || ('thumb_' + item.id + '.png')) || '';
  return img;
}

export function refreshGridStates(): void {
  const catalog = store.getCatalog();
  const state = store.getState();
  if (!catalog || !state) return;
  const slot = currentSlot();
  const items = currentItems();
  const cards = el.grid.querySelectorAll('.card');
  cards.forEach(function (card, idx) {
    const item = items[idx];
    if (!item) return;
    const tintId = selectedTintFor(item);
    card.classList.toggle('selected', idx === storeUI.cardIndex);
    const priceState = card.querySelector('.price-state');
    if (priceState) priceState.textContent = priceStateText(slot, item, tintId);
    if (slot.tintable) {
      const chips = card.querySelectorAll('.swatch');
      chips.forEach(function (chip) {
        const tid = (chip as HTMLElement).dataset.tint || '';
        chip.classList.toggle('selected', tid === tintId);
        chip.classList.toggle('unowned', !store.isTintOwned(item, tid));
      });
      // thumbnail tint follows the selected swatch
      const tintWrap = card.querySelector('.tintable') as HTMLElement | null;
      if (tintWrap) {
        tintWrap.style.setProperty('--tint', store.tintHexFor(tintId));
      }
    }
    const action = computeCardAction(slot, item, tintId);
    const btn = card.querySelector('.action');
    if (btn) {
      btn.textContent = action.label;
      btn.classList.toggle('is-disabled', action.kind === 'insufficient' || action.kind === 'none');
    }
  });
}

function selectCard(idx: number): void {
  const items = currentItems();
  idx = clamp(idx, 0, Math.max(0, items.length - 1));
  storeUI.cardIndex = idx;
  storeUI.focus = 'cards';
  refreshGridStates();
  const card = el.grid.querySelectorAll('.card')[idx];
  if (card && (card as HTMLElement).scrollIntoView) (card as HTMLElement).scrollIntoView({ block: 'nearest' });
  updatePreview();
}

function selectTint(item: CatalogItem, tintId: string): void {
  storeUI.selectedTintByItem[item.id] = tintId;
  refreshGridStates();
  updatePreview();
}

function runCardAction(slot: CatalogSlot, item: CatalogItem): void {
  const tintId = selectedTintFor(item);
  const action = computeCardAction(slot, item, tintId);
  switch (action.kind) {
    case 'buy':
      sendAction({ action: 'BUY_ITEM', itemId: item.id });
      break;
    case 'buytint':
      sendAction({ action: 'BUY_TINT', itemId: item.id, tintId: tintId as string });
      break;
    case 'equip':
      sendAction({ action: 'EQUIP_ITEM', slot: slot.id, itemId: item.id, tintId: slot.tintable ? tintId : null });
      break;
    case 'insufficient':
      flashInsufficientFunds();
      break;
    default:
      break; // already equipped: nothing
  }
}

function updateScrollThumb(): void {
  const trackH = 212;
  const sh = el.grid.scrollHeight, ch = el.grid.clientHeight, st = el.grid.scrollTop;
  if (sh <= ch) {
    el.scrollThumb.style.height = trackH + 'px';
    el.scrollThumb.style.top = '0px';
    return;
  }
  const top = (st / sh) * trackH;
  const h = Math.max(8, (ch / sh) * trackH);
  el.scrollThumb.style.top = top + 'px';
  el.scrollThumb.style.height = h + 'px';
}
el.grid.addEventListener('scroll', updateScrollThumb);

// ---------------------------------------------------------------------
// Preview pane (ui-spec.md §4.2)
// ---------------------------------------------------------------------
function updatePreview(): void {
  const catalog = store.getCatalog();
  const state = store.getState();
  if (!catalog || !state) return;
  const slot = currentSlot();
  const item = currentItem();
  el.previewViewport.innerHTML = '';
  if (!item) return;
  const tintId = selectedTintFor(item);

  if (slot.id === 'hoodie' || slot.id === 'chair') {
    renderComposedPreview(slot.id, item, tintId);
  } else if (!item.sprite) {
    const nothing = document.createElement('div');
    nothing.className = 'nothing';
    nothing.textContent = 'NOTHING';
    el.previewViewport.appendChild(nothing);
  } else {
    const rect = SLOT_RECT[slot.id];
    const scale = Math.max(1, Math.min(3, Math.floor(Math.min(152 / rect.w, 152 / rect.h))));
    const img = document.createElement('img');
    img.className = 'scene';
    img.alt = '';
    img.src = assetUrl(item.sprite) || '';
    img.style.width = (rect.w * scale) + 'px';
    img.style.height = (rect.h * scale) + 'px';
    img.style.left = Math.round((152 - rect.w * scale) / 2) + 'px';
    img.style.top = Math.round((152 - rect.h * scale) / 2) + 'px';
    el.previewViewport.appendChild(img);
  }

  el.previewName.textContent = truncate(item.name, 20);
  const owned = (state.ownedItems || []).indexOf(item.id) !== -1;
  const eq = state.equipped[slot.id];
  const sameEquip = !!eq && eq.itemId === item.id && (!slot.tintable || eq.tintId === tintId);
  el.previewState.textContent = !owned ? (item.price + ' ◆') : (sameEquip ? 'EQUIPPED' : 'OWNED');
  el.previewColor.textContent = slot.tintable ? truncate((store.getTintById(tintId as string) || { name: '' }).name || '', 24) : '';
}

// hoodie/chair composed mini-scene at 1x, centred in the 152x152 viewport
// (ui-spec.md §4.2): the previewed slot uses the selected item+tint, every
// other one of {hoodie, chair} uses whatever is currently equipped.
function renderComposedPreview(previewSlotId: string, previewItem: CatalogItem, previewTintId: string | null): void {
  const state = store.getState()!;
  const hoodieEq = state.equipped.hoodie;
  const chairEq = state.equipped.chair;
  const hoodieItem = previewSlotId === 'hoodie' ? previewItem : store.getItemById(hoodieEq.itemId)!;
  const hoodieTint = previewSlotId === 'hoodie' ? previewTintId : (hoodieEq.tintId || hoodieItem.defaultTint);
  const chairItem = previewSlotId === 'chair' ? previewItem : store.getItemById(chairEq.itemId)!;
  const chairTint = previewSlotId === 'chair' ? previewTintId : (chairEq.tintId || chairItem.defaultTint);

  const chairRect = CHAIR_RECT[chairItem.id] || CHAIR_RECT.chair_basic;
  const devRect = DEV_RECT;
  const bboxLeft = Math.min(devRect.left, chairRect.left);
  const bboxTop = Math.min(devRect.top, chairRect.top);
  const bboxRight = Math.max(devRect.left + devRect.w, chairRect.left + chairRect.w);
  const bboxBottom = Math.max(devRect.top + devRect.h, chairRect.top + chairRect.h);
  const bboxW = bboxRight - bboxLeft, bboxH = bboxBottom - bboxTop;
  const originLeft = Math.round((152 - bboxW) / 2);
  const originTop = Math.round((152 - bboxH) / 2);

  const root = document.createElement('div');
  root.style.position = 'absolute';
  root.style.left = originLeft + 'px';
  root.style.top = originTop + 'px';
  root.style.width = bboxW + 'px';
  root.style.height = bboxH + 'px';
  el.previewViewport.appendChild(root);

  const chairHolder = document.createElement('div');
  chairHolder.style.position = 'absolute';
  positionEl(chairHolder, { left: chairRect.left - bboxLeft, top: chairRect.top - bboxTop, w: chairRect.w, h: chairRect.h });
  const chairTintLayer = buildTintLayer(chairItem.sprite, store.tintHexFor(chairTint));
  positionEl(chairTintLayer, { left: 0, top: 0, w: chairRect.w, h: chairRect.h });
  chairTintLayer.style.zIndex = '1';
  chairHolder.appendChild(chairTintLayer);
  if (chairItem.detail) {
    const chairDetail = plainImg(chairItem.detail, { left: 0, top: 0, w: chairRect.w, h: chairRect.h });
    chairDetail.style.zIndex = '2';
    chairHolder.appendChild(chairDetail);
  }
  root.appendChild(chairHolder);

  const devHolder = document.createElement('div');
  devHolder.style.position = 'absolute';
  positionEl(devHolder, { left: devRect.left - bboxLeft, top: devRect.top - bboxTop, w: devRect.w, h: devRect.h });
  const frame = currentDevFrame();
  const formLayer = buildTintLayer('dev_form_' + frame + '.png', store.tintHexFor(hoodieTint));
  positionEl(formLayer, { left: 0, top: 0, w: devRect.w, h: devRect.h });
  formLayer.style.zIndex = '3';
  devHolder.appendChild(formLayer);
  if (hoodieItem) {
    const styleImg = plainImg(hoodieItem.sprite, { left: 0, top: 0, w: devRect.w, h: devRect.h });
    styleImg.style.zIndex = '4';
    devHolder.appendChild(styleImg);
  }
  const baseImg = plainImg('dev_base_' + frame + '.png', { left: 0, top: 0, w: devRect.w, h: devRect.h });
  baseImg.style.zIndex = '5';
  devHolder.appendChild(baseImg);
  root.appendChild(devHolder);
}

// ---------------------------------------------------------------------
// Open/close (ui-spec.md §4.5, §5.3 — STORE_OPEN/STORE_CLOSE)
// ---------------------------------------------------------------------
function ensureStoreDefaults(): void {
  const catalog = store.getCatalog();
  const state = store.getState();
  if (storeUI.initialized || !catalog || !state) return;
  const hoodieIdx = catalog.slots.findIndex(function (s) { return s.id === 'hoodie'; });
  storeUI.catIndex = hoodieIdx === -1 ? 0 : hoodieIdx;
  const items = store.getCatalogBySlot('hoodie');
  const eq = state.equipped.hoodie;
  let idx = 0;
  for (let i = 0; i < items.length; i++) if (eq && items[i].id === eq.itemId) { idx = i; break; }
  storeUI.cardIndex = idx;
  storeUI.focus = 'cards';
  storeUI.initialized = true;
}

export function open(): void {
  if (el.store.open) return;
  ensureStoreDefaults();
  buildCats();
  buildGrid();
  updatePreview();
  updateStoreCash();
  el.store.showModal();
  el.scrim.classList.add('visible');
  sendAction({ action: 'STORE_OPEN' });
  setStoreOpenHoldDesired(true);
  updateScrollThumb();
}
export function close(): void {
  if (!el.store.open) return;
  el.store.close(); // fires 'close' below regardless of trigger (X / S / Tab / Esc)
}
el.store.addEventListener('close', function () {
  el.scrim.classList.remove('visible');
  sendAction({ action: 'STORE_CLOSE' });
  setStoreOpenHoldDesired(false); // next open starts the B2 reassert guard fresh
});
el.storeOpenBtn.addEventListener('click', open);
el.storeClose.addEventListener('click', close);

function updateStoreCash(): void {
  const state = store.getState();
  if (!state) return;
  el.storeCash.textContent = fmtInt(state.devCash);
}

// ---------------------------------------------------------------------
// Refresh-if-open — called by main.ts's renderAll() after any state
// change, so the store layer never has to know this modal exists.
// ---------------------------------------------------------------------
export function refreshIfOpen(): void {
  if (!el.store.open) return;
  updateStoreCash();
  refreshGridStates();
  updatePreview();
}
export function onCatalogChanged(): void {
  if (el.store.open) buildCats();
}

// ---------------------------------------------------------------------
// Keyboard (ui-spec.md §5.2)
// ---------------------------------------------------------------------
function moveSelection(delta: number): void {
  if (storeUI.focus === 'cats') {
    selectCategory(storeUI.catIndex + delta);
  } else {
    selectCard(storeUI.cardIndex + delta);
  }
}
function selectSwatchByIndex(idx: number): void {
  const slot = currentSlot(), item = currentItem();
  if (!slot.tintable || !item) return;
  const tint = store.getCatalog()!.tints[idx];
  if (!tint) return;
  selectTint(item, tint.id);
}
function cycleSwatch(dir: number): void {
  const slot = currentSlot(), item = currentItem();
  if (!slot.tintable || !item) return;
  const tints = store.getCatalog()!.tints;
  const cur = selectedTintFor(item);
  let idx = tints.findIndex(function (t) { return t.id === cur; });
  idx = (idx + dir + tints.length) % tints.length;
  selectTint(item, tints[idx].id);
}
function runSelectedCardAction(): void {
  const slot = currentSlot(), item = currentItem();
  if (!item) return;
  runCardAction(slot, item);
}

export function handleKeydown(e: KeyboardEvent): void {
  switch (e.key) {
    case 'ArrowUp': e.preventDefault(); moveSelection(-1); break;
    case 'ArrowDown': e.preventDefault(); moveSelection(1); break;
    case 'ArrowLeft': e.preventDefault(); storeUI.focus = 'cats'; renderCatSelection(); break;
    case 'ArrowRight': e.preventDefault(); storeUI.focus = 'cards'; refreshGridStates(); break;
    case '1': case '2': case '3': case '4': case '5': case '6':
      selectSwatchByIndex(Number(e.key) - 1); break;
    case '[': cycleSwatch(-1); break;
    case ']': cycleSwatch(1); break;
    case 'Enter': runSelectedCardAction(); break;
    case 's': case 'S': close(); break;
    case 'Tab': close(); break; // do not preventDefault: leave native focus cycling alone
    default: break; // Esc: native <dialog> behaviour, not intercepted
  }
}
