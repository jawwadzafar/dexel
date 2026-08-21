// Typed mirror of docs/ui-spec.md §6 (WebSocket state contract) and the Go
// StateMessage/CatalogMessage types under app/internal/game. camelCase
// throughout, per ui-spec.md §6.2's explicit design call — the whole payload
// is consumed by JS/TS, so one casing convention across the wire and the DOM
// removes a whole class of typo.
//
// This module is types only (no runtime code): it exists so main.ts's
// message handling is checked by `tsc --noEmit` against the real shape of
// what the server sends, instead of trusting `any`.

export type ActiveState = 'coding' | 'idle' | 'onBreak';

// ---------------------------------------------------------------------
// catalog (sent once on connect, static thereafter)
// ---------------------------------------------------------------------
export interface CatalogSlot {
  id: string;
  name: string;
  tintable: boolean;
}

export interface CatalogTint {
  id: string;
  name: string;
  hex: string;
  price: number;
}

export interface CatalogItem {
  id: string;
  slot: string;
  name: string;
  price: number;
  sprite: string | null;
  detail: string | null;
  // Thumbnail fields — ui-spec.md §6.1 "Thumbnail fields": exactly one of
  // `thumb` or the `thumbForm`/`thumbDetail` pair is non-null, decided by
  // the item's slot (tintable vs not), never guessed by the frontend.
  thumb: string | null;
  thumbForm: string | null;
  thumbDetail: string | null;
  defaultTint: string | null;
  flavor: string;
}

export interface CatalogMessage {
  type: 'catalog';
  v: number;
  slots: CatalogSlot[];
  tints: CatalogTint[];
  items: CatalogItem[];
}

// ---------------------------------------------------------------------
// state (on connect, every 1s, and immediately after any mutation)
// ---------------------------------------------------------------------
export interface EquippedEntry {
  itemId: string;
  tintId: string | null;
}

// Keyed by slot id (hoodie, chair, keyboard, mouse, beverage, plant, wall,
// buddy) — ui-spec.md: "equipped has an entry for every slot, always."
export type Equipped = Record<string, EquippedEntry>;

export interface SprintInfo {
  index: number;
  name: string;
  progress: number;
  target: number;
  // Falls back to "units" client-side if a stale server omits it — see
  // main.ts renderChrome().
  unitLabel?: string;
}

// Analytics track Phase A1 (docs/plan/ROADMAP.md) — counts and durations
// only, never content, per ADR 0002/0009. Seconds are whole seconds; the
// frontend formats them (fmtDuration), never the server.
//
// Phase A2 (docs/plan/A2-design.md §6) adds focusSessions/appSwitches.
// Both optional so a stale (pre-A2) server degrades to 0 rather than
// failing type-checking or crashing at runtime, matching the existing
// `stats?` pattern on StateMessage below.
export interface StatBlock {
  keystrokes: number;
  mouseActiveSeconds: number;
  activeSeconds: number;
  idleSeconds: number;
  sprintsCompleted: number;
  focusSessions?: number;
  appSwitches?: number;
}

// Phase A2 (A2-design.md §6/§5) — coins (DevCash) attributed today, split
// proportionally across the signal that earned them at sprint-payout time.
// All whole coin counts; content-free (uint64 on the wire side).
export interface CoinBreakdown {
  keystrokes: number;
  mouse: number;
  focusSessions: number;
  appSwitches: number;
}

export interface Stats {
  today: StatBlock;
  lifetime: StatBlock;
  // Optional — same stale-server degradation as StatBlock's new fields
  // above; absent means "no coins attributed yet" (render as 0s).
  coinsToday?: CoinBreakdown;
}

export interface StateMessage {
  type: 'state';
  v: number;
  activeState: ActiveState;
  activityLine: string;
  devCash: number;
  level: number;
  xp: number;
  storeOpen: boolean;
  sprint: SprintInfo;
  screenLines: string[]; // always exactly 11, oldest first
  tickerLines: string[]; // always exactly 3, newest first
  equipped: Equipped;
  ownedItems: string[];
  ownedTints: string[]; // "<itemId>:<tintId>"
  // Optional so a stale server (pre-A1) degrades gracefully rather than
  // failing type-checking or crashing at runtime — main.ts's renderActivity
  // treats a missing stats block as all-zero.
  stats?: Stats;
}

export interface FlashMessage {
  type: 'flash';
  kind?: string;
  text?: string;
}

export type ServerMessage = CatalogMessage | StateMessage | FlashMessage;

// ---------------------------------------------------------------------
// client -> server (ui-spec.md §6.2)
// ---------------------------------------------------------------------
export type ClientAction =
  | { action: 'BUY_ITEM'; itemId: string }
  | { action: 'BUY_TINT'; itemId: string; tintId: string }
  | { action: 'EQUIP_ITEM'; slot: string; itemId: string; tintId: string | null }
  | { action: 'STORE_OPEN' }
  | { action: 'STORE_CLOSE' };
