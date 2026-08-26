// DATA/STATE layer — the central typed state store. Holds the latest
// CatalogMessage/StateMessage the WS client (./ws-client.ts) received, the
// derived catalog indices, and the small set of pure selectors that read
// them (freeDefaultItem/equippedItemFor). Render modules and feature modules
// both read this store; nothing but the WS client (via main.ts's wiring) and
// dev/dev-tools.ts (behind ?dev=1) writes to it — this module never sends a
// ClientAction and never touches the DOM.
//
// STORE-2.0 (docs/plan/ROADMAP.md §STORE-2.0): the tint system is gone from
// the wire, so this store no longer indexes tints or answers tint-ownership
// queries. Colour is now a RENDER concern derived from the item id — see
// ../colours.ts.
import type { CatalogItem, CatalogMessage, StateMessage } from '../wire';

let catalog: CatalogMessage | null = null;
let state: StateMessage | null = null;
let catalogBySlot: Record<string, CatalogItem[]> = {}; // slot id -> [items] in catalog order
let itemsById: Record<string, CatalogItem> = {};

function indexCatalog(): void {
  catalogBySlot = {};
  itemsById = {};
  if (!catalog) return;
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
    console.warn('[dexel] unknown item id "' + eq.itemId + '" equipped in slot "' + slotId + '" — rendering the slot default instead');
  }
  return item || freeDefaultItem(slotId);
}
