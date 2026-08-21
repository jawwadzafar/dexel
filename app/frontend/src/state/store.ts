// DATA/STATE layer — the central typed state store. Holds the latest
// CatalogMessage/StateMessage the WS client (./ws-client.ts) received, the
// derived catalog indices, and the small set of pure selectors that read
// them (isTintOwned/freeDefaultItem/equippedItemFor/tintHexFor). Render
// modules and feature modules both read this store; nothing but the WS
// client (via main.ts's wiring) and dev/dev-tools.ts (behind ?dev=1)
// writes to it — this module never sends a ClientAction and never touches
// the DOM.
import type { CatalogItem, CatalogMessage, CatalogTint, StateMessage } from '../wire';

let catalog: CatalogMessage | null = null;
let state: StateMessage | null = null;
let catalogBySlot: Record<string, CatalogItem[]> = {}; // slot id -> [items] in catalog order
let itemsById: Record<string, CatalogItem> = {};
let tintsById: Record<string, CatalogTint> = {};

function indexCatalog(): void {
  catalogBySlot = {};
  itemsById = {};
  tintsById = {};
  if (!catalog) return;
  catalog.tints.forEach(function (t) { tintsById[t.id] = t; });
  catalog.items.forEach(function (it) {
    itemsById[it.id] = it;
    (catalogBySlot[it.slot] = catalogBySlot[it.slot] || []).push(it);
  });
}

export function setCatalog(msg: CatalogMessage): void {
  catalog = msg;
  indexCatalog();
}
export function getCatalog(): CatalogMessage | null { return catalog; }

export function setState(msg: StateMessage): void { state = msg; }
export function getState(): StateMessage | null { return state; }

export function getCatalogBySlot(slotId: string): CatalogItem[] { return catalogBySlot[slotId] || []; }
export function getItemById(id: string): CatalogItem | undefined { return itemsById[id]; }
export function getTintById(id: string): CatalogTint | undefined { return tintsById[id]; }

// Falls back to white (never black) for an unknown/missing tint id: the
// tint mechanism paints .tint-fill with this colour then multiply-blends
// the form sprite over it (game.css ".tint-fill"/".tint-shade"), so white
// is the multiplicative identity — it leaves the form sprite's own
// (undyed/neutral) colours on screen instead of crushing them to black.
// A save/state referencing a tint id this client's catalog doesn't know
// about (stale client vs. newer server, or a bad devApply payload) must
// degrade to "untinted", never to a black silhouette.
export function tintHexFor(tintId: string | null | undefined): string {
  const t = tintId ? tintsById[tintId] : undefined;
  return t ? t.hex : '#ffffff';
}

export function isTintOwned(item: CatalogItem | undefined, tintId: string | null): boolean {
  if (!item) return false;
  if (tintId === item.defaultTint) return true;
  return (state!.ownedTints || []).indexOf(item.id + ':' + tintId) !== -1;
}

export function freeDefaultItem(slotId: string): CatalogItem | undefined {
  const items = catalogBySlot[slotId] || [];
  for (let i = 0; i < items.length; i++) if (items[i].price === 0) return items[i];
  return items[0];
}

// Resolves the item actually equipped in slotId, defensively: an unknown
// itemId (a stale save from a newer client version, or a bad devApply
// payload — either way, an id this client's loaded catalog doesn't have)
// must render the slot's own free default item rather than nothing, so a
// scene never goes missing a piece over one bad field. Only warns when
// eq.itemId was actually present-but-unrecognised — an absent/undefined
// equipped entry is unremarkable (e.g. before the first state arrives).
export function equippedItemFor(slotId: string): CatalogItem | undefined {
  const eq = state!.equipped && state!.equipped[slotId];
  const item = eq && itemsById[eq.itemId];
  if (!item && eq && eq.itemId) {
    console.warn('[dev-companion] unknown item id "' + eq.itemId + '" equipped in slot "' + slotId + '" — rendering the slot default instead');
  }
  return item || freeDefaultItem(slotId);
}
