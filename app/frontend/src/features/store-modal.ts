// FEATURE/LOGIC layer — the store modal (ui-spec.md §4, redesigned per
// docs/plan/ROADMAP.md §STORE-REDESIGN). Owns every DOM node under #store
// and its own UI-selection state (storeUI). It is the only module that
// sends BUY_AND_EQUIP / STORE_OPEN / STORE_CLOSE.
//
// STORE-REDESIGN: the old list+preview split is gone. The store is now a
// single scrollable GRID of cards, grouped by slot (a slot header, then
// that slot's item cards). Each card IS its own preview (the item art
// thumbnail), shows the item name and the PRICE in the top-right corner,
// and — on tintable slots (hoodie, chair) — a row of colour swatches.
//
// ONE CLICK = BUY + EQUIP. Clicking a card sends a single BUY_AND_EQUIP
// action; the server buys the item and/or tint if not yet owned and equips
// it atomically (app/actions.go -> game.BuyAndEquip), so the client never
// chains BUY then EQUIP across the 1Hz broadcast and can never leave a
// half-applied purchase. The client renders state FROM the server
// broadcast (render-from-server-state); it never asserts ownership the
// server did not send.
import { byId, spriteImg } from '../dom';
import * as store from '../state/store';
import { sendAction, setStoreOpenHoldDesired } from '../state/ws-client';
import { assetUrl } from '../assets';
import { clamp, fmtInt } from '../format';
import { buildTintLayer, swatchColor } from '../render/tint';
import { flashInsufficientFunds } from '../render/flash';
import { enableClickAwayDismiss } from './modal-dismiss';
import type { CatalogItem, CatalogSlot } from '../wire';

const el = {
  store: byId<HTMLDialogElement>('store'),
  storeOpenBtn: byId<HTMLButtonElement>('store-open'),
  storeClose: byId<HTMLButtonElement>('store-close'),
  scrim: byId<HTMLDivElement>('scrim'),
  storeCash: byId('store-cash').querySelector('.value') as HTMLElement,
  grid: byId<HTMLDivElement>('store-grid'),
  scrollTrack: byId<HTMLDivElement>('store-scroll'),
  scrollThumb: document.querySelector('#store-scroll .thumb-bar') as HTMLElement
};

interface StoreUI {
  initialized: boolean;
  cardIndex: number; // keyboard selection over the FLAT card list
  selectedTintByItem: Record<string, string>;
}

const storeUI: StoreUI = {
  initialized: false,
  cardIndex: 0,
  selectedTintByItem: {}
};

// The flat card list, in grid order (headers excluded), rebuilt by
// buildGrid(). Keyboard nav and refresh-in-place both index into this.
interface CardEntry {
  el: HTMLDivElement;
  slot: CatalogSlot;
  item: CatalogItem;
}
let cards: CardEntry[] = [];

export function isOpen(): boolean { return el.store.open; }

// The tint a tintable card currently shows: an explicit swatch pick wins,
// else the tint this item is equipped with (if it is), else the item's
// free default tint.
function selectedTintFor(item: CatalogItem): string | null {
  if (Object.prototype.hasOwnProperty.call(storeUI.selectedTintByItem, item.id)) {
    return storeUI.selectedTintByItem[item.id];
  }
  const state = store.getState();
  const eq = state && state.equipped[item.slot];
  if (eq && eq.itemId === item.id && eq.tintId) return eq.tintId;
  return item.defaultTint;
}

// ---------------------------------------------------------------------
// Card state (ui-spec.md §4.3 precedence, re-expressed for one-click).
// LEVEL-GATING is PRESERVED: an unowned item whose catalog minLevel
// exceeds the player's level is 'locked' — a padlock + "LV n" badge, not
// buyable at any price (mirrors the server's ErrLevelLocked, checked
// before affordability). `cost` is the COMBINED price this click would
// spend (item price if unowned + tint price if a non-owned tint is
// selected), which is what drives affordability, exactly like the server's
// BuyAndEquip.
// ---------------------------------------------------------------------
type CardKind = 'locked' | 'equipped' | 'equip' | 'buy' | 'cant-afford';
interface CardState {
  kind: CardKind;
  stateText: string; // the bottom band label
  priceText: string; // the top-right corner
  minLevel: number;
}

function computeCardState(slot: CatalogSlot, item: CatalogItem, tintId: string | null): CardState {
  const state = store.getState()!;
  const owned = (state.ownedItems || []).indexOf(item.id) !== -1;
  const minLevel = item.minLevel || 0;

  // Combined cost of the click (what BuyAndEquip would charge).
  let cost = 0;
  if (!owned) cost += item.price;
  let tintOwned = true;
  if (slot.tintable) {
    tintOwned = store.isTintOwned(item, tintId);
    if (!tintOwned) {
      const tint = store.getTintById(tintId as string);
      cost += tint ? tint.price : 0;
    }
  }

  // Level gate wins over everything for an unowned item — non-buyable.
  if (!owned && state.level < minLevel) {
    return { kind: 'locked', stateText: 'LV ' + minLevel, priceText: fmtInt(item.price) + ' ◆', minLevel };
  }

  const priceText = owned ? 'OWNED' : fmtInt(item.price) + ' ◆';

  // Already wearing exactly this item + tint.
  const eq = state.equipped[slot.id];
  const sameEquip = !!eq && eq.itemId === item.id && (!slot.tintable || eq.tintId === tintId);
  if (sameEquip) return { kind: 'equipped', stateText: '✓ EQUIPPED', priceText, minLevel };

  // Nothing to buy — just an equip of something already owned.
  if (cost === 0) return { kind: 'equip', stateText: 'EQUIP', priceText, minLevel };

  // A purchase is involved: affordable or not.
  if (cost > state.devCash) return { kind: 'cant-afford', stateText: 'NEED ' + fmtInt(cost), priceText, minLevel };
  return { kind: 'buy', stateText: 'BUY + EQUIP', priceText, minLevel };
}

// ---------------------------------------------------------------------
// Grid build (grouped by slot: header, then that slot's cards)
// ---------------------------------------------------------------------
export function buildGrid(): void {
  el.grid.innerHTML = '';
  cards = [];
  const catalog = store.getCatalog();
  const state = store.getState();
  if (!catalog || !state) return;

  catalog.slots.forEach(function (slot) {
    const head = document.createElement('div');
    head.className = 'slot-head';
    head.textContent = slot.name.toUpperCase();
    el.grid.appendChild(head);

    store.getCatalogBySlot(slot.id).forEach(function (item) {
      const card = buildCard(slot, item);
      el.grid.appendChild(card);
      cards.push({ el: card, slot: slot, item: item });
    });
  });

  if (storeUI.cardIndex >= cards.length) storeUI.cardIndex = 0;
  refreshCardStates();
  updateScrollThumb();
}

function buildCard(slot: CatalogSlot, item: CatalogItem): HTMLDivElement {
  const card = document.createElement('div');
  card.className = 'card';
  card.dataset.item = item.id;

  const thumb = document.createElement('div');
  thumb.className = 'thumb';
  thumb.appendChild(buildThumb(slot, item));
  card.appendChild(thumb);

  const price = document.createElement('div');
  price.className = 'price';
  card.appendChild(price);

  const name = document.createElement('div');
  name.className = 'name';
  name.textContent = item.name;
  card.appendChild(name);

  if (slot.tintable) {
    const swatches = document.createElement('div');
    swatches.className = 'swatches';
    const catalog = store.getCatalog()!;
    catalog.tints.forEach(function (tint) {
      const chip = document.createElement('div');
      chip.className = 'swatch';
      chip.style.background = swatchColor(tint.hex);
      chip.dataset.tint = tint.id;
      chip.addEventListener('click', function (ev) {
        ev.stopPropagation(); // a swatch is its own target, never a card click
        storeUI.selectedTintByItem[item.id] = tint.id;
        runCardAction(slot, item);
        refreshCardStates();
      });
      swatches.appendChild(chip);
    });
    card.appendChild(swatches);
  }

  const band = document.createElement('div');
  band.className = 'state';
  card.appendChild(band);

  // The whole card is the buy/equip button (one click = buy AND equip).
  card.addEventListener('click', function () { runCardAction(slot, item); });
  card.addEventListener('mouseenter', function () { card.classList.add('hovered'); });
  card.addEventListener('mouseleave', function () { card.classList.remove('hovered'); });
  return card;
}

// Tintable slots (hoodie, chair) get a live-tinted thumbnail (form layer +
// detail layer, the art-direction.md tintable-thumbnail rule); the tint
// follows the selected swatch on refresh. Non-tintable slots get one flat
// thumb image. A "nothing" item (no sprite) gets an empty box.
function buildThumb(slot: CatalogSlot, item: CatalogItem): HTMLElement {
  if (!item.sprite) return document.createElement('span');
  if (slot.tintable) {
    const tintId = selectedTintFor(item);
    const formFile = item.thumbForm || ('thumb_' + item.id + '_form.png');
    const detailFile = item.thumbDetail || ('thumb_' + item.id + '_detail.png');
    const wrap = document.createElement('div');
    wrap.style.position = 'absolute';
    wrap.style.inset = '0';
    const tint = buildTintLayer(formFile, store.tintHexFor(tintId));
    tint.style.left = '0'; tint.style.top = '0'; tint.style.width = '100%'; tint.style.height = '100%';
    wrap.appendChild(tint);
    const detail = spriteImg();
    detail.alt = '';
    detail.src = assetUrl(detailFile) || '';
    detail.style.position = 'absolute';
    detail.style.inset = '0';
    detail.style.width = '100%';
    detail.style.height = '100%';
    wrap.appendChild(detail);
    return wrap;
  }
  const img = spriteImg();
  img.alt = '';
  img.src = assetUrl(item.thumb || ('thumb_' + item.id + '.png')) || '';
  return img;
}

// Refresh every card's state text/classes/swatches in place from the
// current server state — called on the 1Hz broadcast (refreshIfOpen) and
// right after any local action, so a card flips to "✓ EQUIPPED" and the
// cash balance drops as soon as the server confirms the purchase.
export function refreshCardStates(): void {
  const state = store.getState();
  if (!state) return;
  cards.forEach(function (entry, idx) {
    const { el: card, slot, item } = entry;
    const tintId = slot.tintable ? selectedTintFor(item) : null;
    const cs = computeCardState(slot, item, tintId);

    card.classList.toggle('equipped', cs.kind === 'equipped');
    card.classList.toggle('cant-afford', cs.kind === 'cant-afford');
    card.classList.toggle('locked', cs.kind === 'locked');
    card.classList.toggle('selected', idx === storeUI.cardIndex);

    const price = card.querySelector('.price');
    if (price) price.textContent = cs.priceText;
    const band = card.querySelector('.state');
    if (band) band.textContent = cs.stateText;

    // Padlock overlay: present only while locked (owner refinement —
    // the lock is a sibling of .thumb so the thumb's dim never touches it).
    const hasLock = !!card.querySelector('.lock');
    if (cs.kind === 'locked' && !hasLock) card.appendChild(buildLock());
    if (cs.kind !== 'locked' && hasLock) card.querySelector('.lock')!.remove();

    if (slot.tintable) {
      card.querySelectorAll('.swatch').forEach(function (chip) {
        const tid = (chip as HTMLElement).dataset.tint || '';
        chip.classList.toggle('selected', tid === tintId);
        chip.classList.toggle('unowned', !store.isTintOwned(item, tid));
      });
      const tintLayer = card.querySelector('.tintable') as HTMLElement | null;
      if (tintLayer) tintLayer.style.setProperty('--tint', store.tintHexFor(tintId));
    }
  });
}

// The chunky pixel padlock (CSS in game.css draws the blocks): a squared
// shackle arch + a solid body with a punched keyhole. No sprite, no emoji.
function buildLock(): HTMLDivElement {
  const lock = document.createElement('div');
  lock.className = 'lock';
  const shackle = document.createElement('span');
  shackle.className = 'shackle';
  const body = document.createElement('span');
  body.className = 'body';
  lock.appendChild(shackle);
  lock.appendChild(body);
  return lock;
}

// ---------------------------------------------------------------------
// The one action: buy AND equip (or just equip) in one click.
// ---------------------------------------------------------------------
function runCardAction(slot: CatalogSlot, item: CatalogItem): void {
  const tintId = slot.tintable ? selectedTintFor(item) : null;
  const cs = computeCardState(slot, item, tintId);
  switch (cs.kind) {
    case 'buy':
    case 'equip':
      // ONE action. The server buys the item and/or tint if needed and
      // equips it atomically — no client-side BUY-then-EQUIP chaining.
      sendAction({ action: 'BUY_AND_EQUIP', slot: slot.id, itemId: item.id, tintId });
      break;
    case 'cant-afford':
      flashInsufficientFunds();
      break;
    case 'locked':   // not buyable at any price — no-op (badge tells why)
    case 'equipped': // already worn — clicking is a harmless no-op
    default:
      break;
  }
}

// ---------------------------------------------------------------------
// Scroll thumb (native scroll, custom pixel thumb — scrollbar is hidden)
// ---------------------------------------------------------------------
function updateScrollThumb(): void {
  const trackH = 300;
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
// Open/close (ui-spec.md §4.5, §5.3 — STORE_OPEN/STORE_CLOSE)
// ---------------------------------------------------------------------
function ensureStoreDefaults(): void {
  if (storeUI.initialized) return;
  storeUI.cardIndex = 0;
  storeUI.initialized = true;
}

export function open(): void {
  if (el.store.open) return;
  ensureStoreDefaults();
  buildGrid();
  updateStoreCash();
  el.store.showModal();
  el.scrim.classList.add('visible');
  el.grid.scrollTop = 0;
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
enableClickAwayDismiss(el.store, close);

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
  refreshCardStates();
  updateScrollThumb();
}
export function onCatalogChanged(): void {
  if (el.store.open) buildGrid();
}

// ---------------------------------------------------------------------
// Keyboard (ui-spec.md §5.2). Number-key colour picking and the colour-help
// clutter are gone (STORE-REDESIGN — swatches replace them). What remains:
// arrows move a linear selection over the flat card list, Enter acts on it,
// and S / Tab / Esc close.
// ---------------------------------------------------------------------
function moveSelection(delta: number): void {
  if (cards.length === 0) return;
  storeUI.cardIndex = clamp(storeUI.cardIndex + delta, 0, cards.length - 1);
  refreshCardStates();
  const sel = cards[storeUI.cardIndex];
  if (sel && sel.el.scrollIntoView) sel.el.scrollIntoView({ block: 'nearest' });
  updateScrollThumb();
}
function runSelectedCardAction(): void {
  const sel = cards[storeUI.cardIndex];
  if (sel) runCardAction(sel.slot, sel.item);
}

export function handleKeydown(e: KeyboardEvent): void {
  switch (e.key) {
    case 'ArrowUp': e.preventDefault(); moveSelection(-3); break;   // one row up (3/row)
    case 'ArrowDown': e.preventDefault(); moveSelection(3); break;  // one row down
    case 'ArrowLeft': e.preventDefault(); moveSelection(-1); break;
    case 'ArrowRight': e.preventDefault(); moveSelection(1); break;
    case 'Enter': runSelectedCardAction(); break;
    case 's': case 'S': close(); break;
    case 'Tab': close(); break; // do not preventDefault: leave native focus cycling alone
    default: break; // Esc: native <dialog> behaviour, not intercepted
  }
}
