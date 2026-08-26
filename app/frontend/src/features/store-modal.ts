// FEATURE/LOGIC layer — the store modal (ui-spec.md §4, redesigned per
// docs/plan/ROADMAP.md §STORE-2.0). Owns every DOM node under #store and its
// own UI-selection state (storeUI). It is the only module that sends
// BUY_AND_EQUIP / STORE_OPEN / STORE_CLOSE.
//
// STORE-2.0: the runtime tint SYSTEM is gone and colours are ordinary items.
// The store is now a TAB per slot (Hoodie / Chair / Keyboard / Mouse /
// Beverage / Plant / Wall / Buddy / Monitor); each tab shows that slot's
// colour-item / style cards, so the old single long cross-slot scroll is
// replaced by a short per-slot grid. Each card IS its own preview (the item's
// art thumbnail — tinted from the item id for the colour slots, a flat image
// otherwise), shows the item name and the PRICE in the top-right corner. There
// are NO swatches: each colour is its own card.
//
// ONE CLICK = BUY + EQUIP. Clicking a card sends a single item-only
// BUY_AND_EQUIP action; the server buys the item if not yet owned and equips
// it atomically (app/actions.go -> game.BuyAndEquip), so the client never
// chains BUY then EQUIP across the 1Hz broadcast and can never leave a
// half-applied purchase. The client renders state FROM the server broadcast
// (render-from-server-state); it never asserts ownership the server did not
// send. LEVEL-GATING is preserved: an unowned item whose catalog minLevel
// exceeds the player's level is padlocked ("LV n"), not buyable at any price.
import { byId, spriteImg } from '../dom';
import * as store from '../state/store';
import { sendAction, setStoreOpenHoldDesired } from '../state/ws-client';
import { assetUrl } from '../assets';
import { clamp, fmtInt } from '../format';
import { buildTintLayer } from '../render/tint';
import {
  MONITOR_THUMB_FILE, chairThumbDetailFile, chairThumbFormFile, colourHexForItem,
  hoodieThumbDetailFile, hoodieThumbFormFile, isColourSlot
} from '../colours';
import { flashInsufficientFunds } from '../render/flash';
import { enableClickAwayDismiss } from './modal-dismiss';
import type { CatalogItem, CatalogSlot } from '../wire';

const CARDS_PER_ROW = 3;

const el = {
  store: byId<HTMLDialogElement>('store'),
  storeOpenBtn: byId<HTMLButtonElement>('store-open'),
  storeClose: byId<HTMLButtonElement>('store-close'),
  scrim: byId<HTMLDivElement>('scrim'),
  storeCash: byId('store-cash').querySelector('.value') as HTMLElement,
  tabs: byId<HTMLDivElement>('store-tabs'),
  grid: byId<HTMLDivElement>('store-grid'),
  scrollTrack: byId<HTMLDivElement>('store-scroll'),
  scrollThumb: document.querySelector('#store-scroll .thumb-bar') as HTMLElement
};

interface StoreUI {
  initialized: boolean;
  activeSlot: string;   // which tab is showing
  cardIndex: number;    // keyboard selection over the active tab's card list
}

const storeUI: StoreUI = {
  initialized: false,
  activeSlot: '',
  cardIndex: 0
};

// The active tab's flat card list, in grid order, rebuilt by buildGrid().
// Keyboard nav and refresh-in-place both index into this.
interface CardEntry {
  el: HTMLDivElement;
  slot: CatalogSlot;
  item: CatalogItem;
}
let cards: CardEntry[] = [];
const tabButtons: Record<string, HTMLButtonElement> = {};

export function isOpen(): boolean { return el.store.open; }

// ---------------------------------------------------------------------
// Card state (ui-spec.md §4.3 precedence, re-expressed for one-click,
// item-only). LEVEL-GATING is PRESERVED: an unowned item whose catalog
// minLevel exceeds the player's level is 'locked' — a padlock + "LV n" badge,
// not buyable at any price (mirrors the server's ErrLevelLocked, checked
// before affordability).
// ---------------------------------------------------------------------
type CardKind = 'locked' | 'equipped' | 'equip' | 'buy' | 'cant-afford';
interface CardState {
  kind: CardKind;
  stateText: string; // the bottom band label
  priceText: string; // the top-right corner
  minLevel: number;
}

function computeCardState(item: CatalogItem): CardState {
  const state = store.getState()!;
  const owned = (state.ownedItems || []).indexOf(item.id) !== -1;
  const minLevel = item.minLevel || 0;

  // Level gate wins over everything for an unowned item — non-buyable.
  if (!owned && state.level < minLevel) {
    return { kind: 'locked', stateText: 'LV ' + minLevel, priceText: fmtInt(item.price) + ' ◆', minLevel };
  }

  const priceText = owned ? 'OWNED' : fmtInt(item.price) + ' ◆';

  // Already wearing exactly this item.
  const eq = state.equipped[item.slot];
  if (eq && eq.itemId === item.id) return { kind: 'equipped', stateText: '✓ EQUIPPED', priceText, minLevel };

  const cost = owned ? 0 : item.price;
  // Nothing to buy — just an equip of something already owned.
  if (cost === 0) return { kind: 'equip', stateText: 'EQUIP', priceText, minLevel };
  // A purchase is involved: affordable or not.
  if (cost > state.devCash) return { kind: 'cant-afford', stateText: 'NEED ' + fmtInt(cost), priceText, minLevel };
  return { kind: 'buy', stateText: 'BUY + EQUIP', priceText, minLevel };
}

// ---------------------------------------------------------------------
// Tabs (one per slot) + the active tab's card grid
// ---------------------------------------------------------------------
function buildTabs(): void {
  el.tabs.replaceChildren();
  for (const k of Object.keys(tabButtons)) delete tabButtons[k];
  const catalog = store.getCatalog();
  if (!catalog) return;
  catalog.slots.forEach(function (slot) {
    const btn = document.createElement('button');
    btn.className = 'store-tab';
    btn.type = 'button';
    btn.textContent = slot.name.toUpperCase();
    btn.dataset.slot = slot.id;
    btn.addEventListener('click', function () { setActiveSlot(slot.id); });
    el.tabs.appendChild(btn);
    tabButtons[slot.id] = btn;
  });
}

function setActiveSlot(slotId: string): void {
  if (storeUI.activeSlot === slotId) return;
  storeUI.activeSlot = slotId;
  storeUI.cardIndex = 0;
  buildGrid();
  el.grid.scrollTop = 0;
  updateScrollThumb();
}

function updateTabStates(): void {
  Object.keys(tabButtons).forEach(function (slotId) {
    tabButtons[slotId].classList.toggle('active', slotId === storeUI.activeSlot);
  });
}

export function buildGrid(): void {
  el.grid.innerHTML = '';
  cards = [];
  const catalog = store.getCatalog();
  const state = store.getState();
  if (!catalog || !state) return;
  if (!storeUI.activeSlot && catalog.slots.length) storeUI.activeSlot = catalog.slots[0].id;

  const slot = catalog.slots.filter(function (s) { return s.id === storeUI.activeSlot; })[0];
  if (!slot) return;

  store.getCatalogBySlot(slot.id).forEach(function (item) {
    const card = buildCard(slot, item);
    el.grid.appendChild(card);
    cards.push({ el: card, slot: slot, item: item });
  });

  if (storeUI.cardIndex >= cards.length) storeUI.cardIndex = 0;
  updateTabStates();
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

  const band = document.createElement('div');
  band.className = 'state';
  card.appendChild(band);

  // The whole card is the buy/equip button (one click = buy AND equip).
  card.addEventListener('click', function () { runCardAction(item); });
  card.addEventListener('mouseenter', function () { card.classList.add('hovered'); });
  card.addEventListener('mouseleave', function () { card.classList.remove('hovered'); });
  return card;
}

// A colour-slot card (hoodie, chair, monitor) composes the shared grayscale
// thumbnail form (+ detail, where the style has one) and CSS-tints it with the
// item's own colour — the same recipe the scene uses, so a card matches what
// the Dexel will wear. Every other slot names a real, already-coloured
// thumbnail; a "nothing" item (no thumb) gets an empty box.
function buildThumb(slot: CatalogSlot, item: CatalogItem): HTMLElement {
  if (isColourSlot(slot.id)) return buildTintedThumb(slot.id, item);
  if (!item.thumb) return document.createElement('span');
  const img = spriteImg();
  img.alt = '';
  img.src = assetUrl(item.thumb) || '';
  return img;
}

function buildTintedThumb(slotId: string, item: CatalogItem): HTMLElement {
  let formFile: string;
  let detailFile: string | null = null;
  if (slotId === 'hoodie') { formFile = hoodieThumbFormFile(item.id); detailFile = hoodieThumbDetailFile(item.id); }
  else if (slotId === 'chair') { formFile = chairThumbFormFile(item.id); detailFile = chairThumbDetailFile(item.id); }
  else { formFile = MONITOR_THUMB_FILE; } // monitor: bezel-only, no detail layer

  const wrap = document.createElement('div');
  wrap.style.position = 'absolute';
  wrap.style.inset = '0';
  const tint = buildTintLayer(formFile, colourHexForItem(item.id));
  tint.style.left = '0'; tint.style.top = '0'; tint.style.width = '100%'; tint.style.height = '100%';
  wrap.appendChild(tint);
  if (detailFile) {
    const detail = spriteImg();
    detail.alt = '';
    detail.src = assetUrl(detailFile) || '';
    detail.style.position = 'absolute';
    detail.style.inset = '0';
    detail.style.width = '100%';
    detail.style.height = '100%';
    wrap.appendChild(detail);
  }
  return wrap;
}

// Refresh every card's state text/classes in place from the current server
// state — called on the 1Hz broadcast (refreshIfOpen) and right after any
// local action, so a card flips to "✓ EQUIPPED" and the cash balance drops as
// soon as the server confirms the purchase.
export function refreshCardStates(): void {
  const state = store.getState();
  if (!state) return;
  cards.forEach(function (entry, idx) {
    const { el: card, item } = entry;
    const cs = computeCardState(item);

    card.classList.toggle('equipped', cs.kind === 'equipped');
    card.classList.toggle('cant-afford', cs.kind === 'cant-afford');
    card.classList.toggle('locked', cs.kind === 'locked');
    card.classList.toggle('selected', idx === storeUI.cardIndex);

    const price = card.querySelector('.price');
    if (price) price.textContent = cs.priceText;
    const band = card.querySelector('.state');
    if (band) band.textContent = cs.stateText;

    // Padlock overlay: present only while locked (owner refinement — the
    // lock is a sibling of .thumb so the thumb's dim never touches it).
    const hasLock = !!card.querySelector('.lock');
    if (cs.kind === 'locked' && !hasLock) card.appendChild(buildLock());
    if (cs.kind !== 'locked' && hasLock) card.querySelector('.lock')!.remove();
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
// The one action: buy AND equip (or just equip) in one click. Item-only.
// ---------------------------------------------------------------------
function runCardAction(item: CatalogItem): void {
  const cs = computeCardState(item);
  switch (cs.kind) {
    case 'buy':
    case 'equip':
      // ONE action. The server buys the item if needed and equips it
      // atomically — no client-side BUY-then-EQUIP chaining, no tint.
      sendAction({ action: 'BUY_AND_EQUIP', slot: item.slot, itemId: item.id });
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
  const trackH = 264;
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
  const catalog = store.getCatalog();
  storeUI.activeSlot = catalog && catalog.slots.length ? catalog.slots[0].id : '';
  storeUI.cardIndex = 0;
  storeUI.initialized = true;
}

export function open(): void {
  if (el.store.open) return;
  ensureStoreDefaults();
  buildTabs();
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
  if (el.store.open) { buildTabs(); buildGrid(); }
}

// ---------------------------------------------------------------------
// Keyboard (ui-spec.md §5.2). Arrows move a linear selection over the ACTIVE
// tab's card list; Enter acts on it. Tab-switching is a mouse click (the tab
// bar) — S / Tab / Esc still close the modal.
// ---------------------------------------------------------------------
function moveSelection(delta: number): void {
  if (cards.length === 0) return;
  storeUI.cardIndex = clamp(storeUI.cardIndex + delta, 0, cards.length - 1);
  refreshCardStates();
  const sel = cards[storeUI.cardIndex];
  if (sel && sel.el.scrollIntoView) sel.el.scrollIntoView({ block: 'nearest' });
  updateScrollThumb();
}
function cycleTab(delta: number): void {
  const catalog = store.getCatalog();
  if (!catalog || !catalog.slots.length) return;
  const ids = catalog.slots.map(function (s) { return s.id; });
  const cur = ids.indexOf(storeUI.activeSlot);
  const next = clamp((cur < 0 ? 0 : cur) + delta, 0, ids.length - 1);
  setActiveSlot(ids[next]);
}
function runSelectedCardAction(): void {
  const sel = cards[storeUI.cardIndex];
  if (sel) runCardAction(sel.item);
}

export function handleKeydown(e: KeyboardEvent): void {
  switch (e.key) {
    case 'ArrowUp': e.preventDefault(); moveSelection(-CARDS_PER_ROW); break;
    case 'ArrowDown': e.preventDefault(); moveSelection(CARDS_PER_ROW); break;
    case 'ArrowLeft': e.preventDefault(); moveSelection(-1); break;
    case 'ArrowRight': e.preventDefault(); moveSelection(1); break;
    case '[': e.preventDefault(); cycleTab(-1); break; // previous tab
    case ']': e.preventDefault(); cycleTab(1); break;  // next tab
    case 'Enter': runSelectedCardAction(); break;
    case 's': case 'S': close(); break;
    case 'Tab': close(); break; // do not preventDefault: leave native focus cycling alone
    default: break; // Esc: native <dialog> behaviour, not intercepted
  }
}
