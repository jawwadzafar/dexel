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
// CLICK-TO-PREVIEW (owner refinement 2026-08-27). A card is a clickable
// THUMBNAIL (art + name + price, no inline button). Clicking an unlocked
// card opens the PREVIEW overlay (#store-preview) — a larger view of the art
// + name + flavor + price — and THERE lives the one action: BUY (spends
// cash, then equips), EQUIP (re-wear an owned item), or ✓ EQUIPPED (the
// already-worn no-op). BUY/EQUIP send a single item-only BUY_AND_EQUIP
// action; the server buys the item if not yet owned and equips it atomically
// (app/actions.go -> game.BuyAndEquip), so the client never chains BUY then
// EQUIP across the 1Hz broadcast and can never leave a half-applied
// purchase. The client renders state FROM the server broadcast
// (render-from-server-state); it never asserts ownership the server did not
// send. LEVEL-GATING is preserved: an unowned item whose catalog minLevel
// exceeds the player's level is shown as a "?" MYSTERY (with a "LV n" hint),
// not buyable at any price, and clicking it opens no preview.
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
  scrollThumb: document.querySelector('#store-scroll .thumb-bar') as HTMLElement,
  // Preview (owner refinement 2026-08-27): the item detail overlay.
  preview: byId<HTMLDivElement>('store-preview'),
  back: byId<HTMLButtonElement>('store-back'),
  previewArt: byId<HTMLDivElement>('store-preview-art'),
  previewName: byId<HTMLDivElement>('store-preview-name'),
  previewFlavor: byId<HTMLDivElement>('store-preview-flavor'),
  previewPrice: byId<HTMLDivElement>('store-preview-price'),
  previewAction: byId<HTMLButtonElement>('store-preview-action')
};

interface StoreUI {
  initialized: boolean;
  activeSlot: string;   // which tab is showing
  cardIndex: number;    // keyboard selection over the active tab's card list
  previewItem: CatalogItem | null; // the item the preview overlay is showing, or null (grid)
}

const storeUI: StoreUI = {
  initialized: false,
  activeSlot: '',
  cardIndex: 0,
  previewItem: null
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

  // Clicking an UNLOCKED card opens the preview overlay; a locked ("?")
  // card is a no-op (guarded inside openPreview via computeCardState).
  card.addEventListener('click', function () { onCardClick(item); });
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

    // "?" mystery overlay: present only while locked (owner refinement
    // 2026-08-27 — a locked item hides its real art/name/price behind a big
    // "?" so the player has something to come back for). CSS hides the
    // thumb/name/price of a .locked card, so the "?" is all that shows.
    const hasMystery = !!card.querySelector('.mystery');
    if (cs.kind === 'locked' && !hasMystery) card.appendChild(buildMystery(cs.minLevel));
    if (cs.kind !== 'locked' && hasMystery) card.querySelector('.mystery')!.remove();
    const lv = card.querySelector('.mystery .lv');
    if (cs.kind === 'locked' && lv) lv.textContent = 'LV ' + cs.minLevel;
  });
}

// The "?" mystery placeholder for a level-locked card: a big pixel question
// mark and a small "LV n" hint telling the player when to come back. No
// sprite, no emoji — a plain glyph in the game's pixel font (CSS in game.css).
function buildMystery(minLevel: number): HTMLDivElement {
  const wrap = document.createElement('div');
  wrap.className = 'mystery';
  const q = document.createElement('span');
  q.className = 'q';
  q.textContent = '?';
  const lv = document.createElement('span');
  lv.className = 'lv';
  lv.textContent = 'LV ' + minLevel;
  wrap.appendChild(q);
  wrap.appendChild(lv);
  return wrap;
}

// ---------------------------------------------------------------------
// Card click -> PREVIEW. A locked ("?") card opens nothing; an unlocked
// card opens the detail overlay where the one action (BUY / EQUIP /
// ✓ EQUIPPED) lives (owner refinement 2026-08-27).
// ---------------------------------------------------------------------
function onCardClick(item: CatalogItem): void {
  const cs = computeCardState(item);
  if (cs.kind === 'locked') return; // "?" — no preview, no-op
  openPreview(item);
}

// ---------------------------------------------------------------------
// Preview overlay: a larger view of the item (art + name + flavor + price)
// and THE one action. BUY spends cash then equips (BUY_AND_EQUIP, atomic on
// the server); EQUIP re-wears an owned item; ✓ EQUIPPED is the already-worn
// no-op. Back / Esc returns to the grid.
// ---------------------------------------------------------------------
function openPreview(item: CatalogItem): void {
  storeUI.previewItem = item;
  // Fill the (state-independent) art, name and flavor once; the price and
  // action button track state via refreshPreview().
  el.previewArt.replaceChildren(buildThumb(itemSlot(item), item));
  el.previewName.textContent = item.name;
  el.previewFlavor.textContent = item.flavor || '';
  showPreview(true);
  refreshPreview();
}

function closePreview(): void {
  storeUI.previewItem = null;
  showPreview(false);
}

// Toggle the tabs+grid+scroll vs the preview overlay.
function showPreview(on: boolean): void {
  el.preview.hidden = !on;
  el.tabs.hidden = on;
  el.grid.hidden = on;
  el.scrollTrack.hidden = on;
}

// The active tab's CatalogSlot for an item — the preview needs it to render
// the (possibly tinted) art the same way the grid card does.
function itemSlot(item: CatalogItem): CatalogSlot {
  const catalog = store.getCatalog();
  const slot = catalog && catalog.slots.filter(function (s) { return s.id === item.slot; })[0];
  return slot || { id: item.slot, name: item.slot };
}

// Keep the preview's price + action button truthful to the live server
// state (called on open, after any local action, and on the 1Hz refresh).
function refreshPreview(): void {
  const item = storeUI.previewItem;
  if (!item) return;
  const cs = computeCardState(item);
  el.previewPrice.textContent = cs.priceText;
  const btn = el.previewAction;
  btn.classList.remove('is-equipped', 'is-cant-afford');
  switch (cs.kind) {
    case 'equipped':
      btn.textContent = '✓ EQUIPPED';
      btn.classList.add('is-equipped');
      break;
    case 'equip':
      btn.textContent = 'EQUIP';
      break;
    case 'buy':
      btn.textContent = 'BUY';
      break;
    case 'cant-afford':
      btn.textContent = 'NEED ' + fmtInt(item.price);
      btn.classList.add('is-cant-afford');
      break;
    default:
      btn.textContent = '';
      break;
  }
}

function runPreviewAction(): void {
  const item = storeUI.previewItem;
  if (!item) return;
  const cs = computeCardState(item);
  switch (cs.kind) {
    case 'buy':
    case 'equip':
      // ONE action. The server buys the item if needed and equips it
      // atomically — no client-side BUY-then-EQUIP chaining. The preview
      // and the underlying grid card both flip to ✓ EQUIPPED on the
      // confirming broadcast (refreshIfOpen).
      sendAction({ action: 'BUY_AND_EQUIP', slot: item.slot, itemId: item.id });
      break;
    case 'cant-afford':
      flashInsufficientFunds();
      break;
    case 'equipped': // already worn — harmless no-op
    default:
      break;
  }
}
el.back.addEventListener('click', closePreview);
el.previewAction.addEventListener('click', runPreviewAction);

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
  closePreview(); // always open on the grid, never a stale preview
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
  closePreview(); // reset so the next open starts on the grid
  sendAction({ action: 'STORE_CLOSE' });
  setStoreOpenHoldDesired(false); // next open starts the B2 reassert guard fresh
});
// Esc backs out of the preview FIRST, then (a second Esc) closes the store.
// The native <dialog> fires 'cancel' before it would close on Esc; when the
// preview is open we swallow that first Esc and just return to the grid.
el.store.addEventListener('cancel', function (e: Event) {
  if (storeUI.previewItem) {
    e.preventDefault();
    closePreview();
  }
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
  refreshPreview();
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
function openSelectedCardPreview(): void {
  const sel = cards[storeUI.cardIndex];
  if (sel) onCardClick(sel.item);
}

export function handleKeydown(e: KeyboardEvent): void {
  // In the preview: Enter runs the action; Esc backs out (native 'cancel',
  // handled above); S / Tab still close the whole store.
  if (storeUI.previewItem) {
    switch (e.key) {
      case 'Enter': runPreviewAction(); break;
      case 's': case 'S': close(); break;
      case 'Tab': close(); break;
      default: break; // Esc: native <dialog> 'cancel' backs out to the grid
    }
    return;
  }
  switch (e.key) {
    case 'ArrowUp': e.preventDefault(); moveSelection(-CARDS_PER_ROW); break;
    case 'ArrowDown': e.preventDefault(); moveSelection(CARDS_PER_ROW); break;
    case 'ArrowLeft': e.preventDefault(); moveSelection(-1); break;
    case 'ArrowRight': e.preventDefault(); moveSelection(1); break;
    case '[': e.preventDefault(); cycleTab(-1); break; // previous tab
    case ']': e.preventDefault(); cycleTab(1); break;  // next tab
    case 'Enter': openSelectedCardPreview(); break;    // open the preview
    case 's': case 'S': close(); break;
    case 'Tab': close(); break; // do not preventDefault: leave native focus cycling alone
    default: break; // Esc: native <dialog> behaviour, not intercepted
  }
}
