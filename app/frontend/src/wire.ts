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

// Analytics track Phase A3 (docs/plan/A3-design.md §5) — one dense,
// zero-filled day entry in the 30-day rolling history window. The server
// builds this array date-complete (today-(N-1)..today ascending, gaps
// filled with an all-zero entry for that date) so the client does zero
// date arithmetic — it only ever indexes/reduces over what's sent. All
// counts/durations, plus the one calendar date and the one server-decided
// `isActive` bool (same §2.1 threshold the streak itself uses, so this
// modal's month-strip coloring and the streak agree on one definition).
// `longestFocusBlockSeconds` is Fork B (A3-design.md §0) — included by
// default but optional here since a still-older/degraded server (or a
// day bucket predating Fork B landing) may omit it per-entry.
export interface DayStat {
  date: string;
  keystrokes: number;
  mouseActiveSeconds: number;
  activeSeconds: number;
  idleSeconds: number;
  sprintsCompleted: number;
  focusSessions: number;
  appSwitches: number;
  coinsEarned: number;
  isActive: boolean;
  longestFocusBlockSeconds?: number;
}

// Server-computed (A3-design.md §2) — the client renders this verbatim and
// never re-derives it: a streak depends on persisted cross-window state
// (lastActiveDate) not reconstructible from the retained 30-day window.
export interface StreakView {
  current: number;
  longest: number;
}

export interface Stats {
  today: StatBlock;
  lifetime: StatBlock;
  // Optional — same stale-server degradation as StatBlock's new fields
  // above; absent means "no coins attributed yet" (render as 0s).
  coinsToday?: CoinBreakdown;
  // Phase A3 additions (A3-design.md §5) — both optional so a stale
  // (pre-A3) server degrades to "no history" (the history modal renders
  // cleanly with empty/zeroed data) rather than crashing.
  history?: DayStat[];
  streak?: StreakView;
}

// Phase P1 — Identity & first minutes (docs/plan/PRODUCT-EVOLUTION.md §5,
// docs/ui-spec.md §7). The USER-AUTHORED half of dexel's persistence: the
// dexel's name, which lives in ~/.config/dexel/config.json, never in the
// protected save (SEC-1 / ADR 0014's config/state split). Empty string
// means "not named yet" — the server always SENDS the block, it just may
// be empty.
export interface ConfigView {
  name: string;
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
  // Phase P1 additions. Both optional for the same stale-server reason as
  // `stats` above: a pre-P1 server sends neither, which must degrade to
  // "unnamed, and definitely do not run onboarding" — never to a modal
  // that cannot be answered because the server has no SET_NAME handler.
  config?: ConfigView;
  // TRUE only in the first-launch state (no save existed when the server
  // booted AND config.json carries no name). SERVER-COMPUTED: the client
  // opens the onboarding modal when this is true and closes it when the
  // post-SET_NAME broadcast says false — it never decides this itself and
  // never keeps the modal open against a false here.
  onboarding?: boolean;
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
  | { action: 'STORE_CLOSE' }
  // Phase P1 (docs/ui-spec.md §6.2/§7). `name` is raw user text; the
  // SERVER trims it, drops control characters, caps it at 24 runes and
  // rejects an empty result (game.NormalizeName) — the client's own
  // trim/maxlength are a courtesy, never the validation.
  | { action: 'SET_NAME'; name: string };
