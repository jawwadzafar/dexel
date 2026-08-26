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
  // Minimum player level required to BUY this item (internal/game
  // catalog.go CatalogItem.MinLevel). 0/omitted = LV1 = ungated. Gates
  // purchase only — an owned item is never re-locked. Optional so a stale
  // server that omits it degrades to "ungated", matching the existing
  // optional-field pattern elsewhere on this wire.
  minLevel?: number;
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
  // PR-5 — Pause semantics (docs/production-runtime/MIGRATION_PLAN.md
  // §PR-5). Seconds spent paused (tracking stopped, no ticks, no accrual)
  // — a THIRD bucket alongside activeSeconds/idleSeconds, never folded
  // into idle: `activeSeconds + idleSeconds + pausedSeconds` covers the
  // bucket's whole runtime uptime. Optional so a stale (pre-PR-5) server
  // degrades to 0, matching the existing `focusSessions?`/`appSwitches?`
  // pattern above.
  pausedSeconds?: number;
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
  // PR-5 — Pause semantics (docs/production-runtime/MIGRATION_PLAN.md
  // §PR-5) — that day's total paused seconds, the same third bucket
  // StatBlock gains above. Optional for the same reason
  // `longestFocusBlockSeconds` is: a still-older/degraded server, or a day
  // bucket predating PR-5 landing, may omit it per-entry.
  pausedSeconds?: number;
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
// docs/ui-spec.md §7). The USER-AUTHORED half of Dexel's persistence: the
// Dexel's name, which lives in ~/.config/dexel/config.json, never in the
// protected save (SEC-1 / ADR 0014's config/state split). Empty string
// means "not named yet" — the server always SENDS the block, it just may
// be empty.
export interface ConfigView {
  name: string;
  // SET-1 (docs/ui-spec.md §11) — the two user PREFERENCES, both
  // defaulting to false. Optional here for the same stale-server reason
  // `config` itself is optional on StateMessage: a pre-SET-1 server sends
  // neither, and `!!undefined` is false, which is exactly the right
  // degradation (window unpinned, away time private).
  //
  // `alwaysOnTop` is not consumed by this page at all — the desktop shell
  // reads it out of `dexel status --json` (app/cmd_lifecycle.go). It rides
  // this block so the Settings modal can render the toggle's position from
  // SERVER truth rather than from a value it remembered locally, the same
  // rule every other control in this app follows.
  alwaysOnTop?: boolean;
  // `showAwayTime` gates whether the Activity modal DISPLAYS its away
  // rows. Note what it is not: the away durations themselves
  // (`StatBlock.idleSeconds` and friends) are recorded and SENT either
  // way — hiding them is this client's rendering decision, driven by this
  // field. See docs/ui-spec.md §11.3 for why recording is deliberately
  // untouched.
  showAwayTime?: boolean;
  // `soundEnabled` gates every sound effect this page plays (SOUND-1,
  // docs/ui-spec.md §13). Optional like its siblings, but note that its
  // DEFAULT is the opposite of theirs: the server defaults it ON, so
  // render/audio.ts treats "absent" as on (`!== false`) rather than
  // reading it as `!!undefined`. That is the honest degradation here — a
  // frontend talking to a server too old to send the field is talking to
  // a server that has no mute to respect, and silently muting itself
  // would invent a preference nobody set.
  soundEnabled?: boolean;
}

// Phase P2 — Sessions (docs/plan/P2-design.md §6.1). The counters are
// FLATTENED, deliberately mirroring DayStat's shape so one rule covers
// both: a session view carries the same seven counters a day does. Every
// field is a count, a duration, an ISO timestamp, an integer id, or a
// closed-set enum — except `name`, the one user-authored string,
// allow-listed on ADR 0014's category citation exactly as P1's
// `ConfigView.name` was. Field names are PINNED verbatim by §8's contract
// seam — do not rename or re-shape without re-reading that section.
export interface ActiveSessionView {
  id: number;
  name: string; // "" when unnamed
  startedAt: string; // RFC3339
  elapsedSeconds: number; // SERVER-computed — the client never derives live time
  keystrokes: number;
  mouseActiveSeconds: number;
  activeSeconds: number;
  idleSeconds: number;
  sprintsCompleted: number;
  focusSessions: number;
  appSwitches: number;
  coinsEarned: number;
  longestFocusBlockSeconds: number;
  // PR-5 — Pause semantics (docs/production-runtime/MIGRATION_PLAN.md
  // §PR-5) — joins P2's session delta set (P2-design.md §2.3/§5.6): a
  // running session's counters freeze while paused (no ticks), and this
  // is the accrued paused time for the session so far. Non-optional
  // (unlike the wire-level StatBlock/DayStat fields above) because a
  // PR-5-era server always emits it on every ActiveSessionView/SessionView
  // it sends — there is no pre-PR-5 session-view shape to degrade from.
  pausedSeconds: number;
}

// A session has an end, and the end is one of a closed three-value set
// (P2-design.md §2.5.5) — the same shape as ActiveState above.
export type SessionEndReason = 'user' | 'idle' | 'maxDuration';

export interface SessionView { // one finished session
  id: number;
  name: string;
  startedAt: string;
  endedAt: string;
  durationSeconds: number;
  keystrokes: number;
  mouseActiveSeconds: number;
  activeSeconds: number;
  idleSeconds: number;
  sprintsCompleted: number;
  focusSessions: number;
  appSwitches: number;
  coinsEarned: number;
  longestFocusBlockSeconds: number;
  // PR-5 (docs/production-runtime/MIGRATION_PLAN.md §PR-5) — same
  // field/rationale as ActiveSessionView.pausedSeconds above, carried
  // through to the finished-session record verbatim.
  pausedSeconds: number;
  endReason: SessionEndReason;
}

export interface SessionsSummary {
  completed: number; // lifetime sessions, derived from the verified log
  thisWeek: number; // last SessionsWeekDays local dates, server-computed
  longestSessionSeconds: number; // a cozy personal best, never a target
}

// One nested block, always sent by a P2 server (the P1 `config`
// precedent) — the server always sends the block, it may just be empty
// (active: null, recent: []). Typed optional here (`sessions?` on
// StateMessage below) so a stale, pre-P2 server degrades to "no
// sessions" — a clean empty state — rather than breaking type-checking
// or crashing at runtime.
export interface SessionsView {
  active: ActiveSessionView | null; // null when none
  summary: SessionsSummary;
  recent: SessionView[]; // newest first, <= SessionsWireWindow (10)
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
  // Phase P2 (docs/plan/P2-design.md §6.1) — optional for the same
  // stale-server reason as `stats`/`config` above: a pre-P2 server sends
  // no `sessions` block at all, which the Sessions modal must render as a
  // clean empty state (no active session, no recent list) rather than
  // crashing or fabricating one client-side.
  sessions?: SessionsView;
  // PR-5 — Pause semantics (docs/production-runtime/MIGRATION_PLAN.md
  // §PR-5). TRUE while tracking is stopped (provider.Stop() called,
  // eng.Tick() not invoked, no accrual and no analytics tally). Optional
  // for the same stale-server reason as `onboarding`/`sessions` above: a
  // pre-PR-5 server sends no `paused` field at all, which must degrade to
  // "not paused" rather than crashing. `activeState` does NOT gain a
  // fourth value for this (ADR 0010) — pausedness is conveyed only via
  // this bool, never by inventing a mood string.
  paused?: boolean;
  // ADAPTIVE-STATS — the provider's app-identity CAPABILITY bit
  // (Go: StateMessage.AppIdentityAvailable, itself
  // activity.Snapshot.AppIdentityAvailable): whether THIS host's provider
  // can observe the foreground application at all. FALSE on Linux/Wayland
  // (ADR 0009), true on macOS with a real active-app source. The Activity
  // modal uses it to HIDE its app-derived rows (App switches today/
  // lifetime, and the App-switches coin line) where identity is
  // unobservable AND the value is 0 — so an app-blind platform never shows
  // a misleading frozen "0 app switches" that reads as "you never switched
  // apps", exactly the away-time hiding pattern.
  //
  // Optional for the same stale-server reason as `paused`/`onboarding`
  // above, but note the degradation direction: an ABSENT field means
  // "assume available, show the rows" (`!== false`), which is the
  // pre-existing behaviour — a stale server is never made worse, and the
  // rows are only ever hidden when the server EXPLICITLY says the platform
  // is app-blind.
  appIdentityAvailable?: boolean;
}

export interface FlashMessage {
  type: 'flash';
  kind?: string;
  text?: string;
}

// Phase P2 (docs/plan/P2-design.md §3.1) — a dedicated message, sent to
// every connection immediately after the `state` broadcast that cleared
// the session. Not folded into `flash`: the client must not be made to
// INFER which entry of `sessions.recent` just ended, so the server sends
// the exact record instead ("the client never asserts state the server
// didn't send"). The ordinary gold `flash{kind:"session"}` toast still
// arrives too, as its own separate message, composed server-side.
export interface SessionCompleteMessage {
  type: 'sessionComplete';
  v: number;
  session: SessionView;
}

export type ServerMessage = CatalogMessage | StateMessage | FlashMessage | SessionCompleteMessage;

// ---------------------------------------------------------------------
// client -> server (ui-spec.md §6.2)
// ---------------------------------------------------------------------
export type ClientAction =
  | { action: 'BUY_ITEM'; itemId: string }
  | { action: 'BUY_TINT'; itemId: string; tintId: string }
  | { action: 'EQUIP_ITEM'; slot: string; itemId: string; tintId: string | null }
  // STORE-REDESIGN (docs/plan/ROADMAP.md §STORE-REDESIGN) — the card-grid
  // store's ONE-CLICK action. The server (app/actions.go BUY_AND_EQUIP ->
  // game.BuyAndEquip) buys the item and/or the tint if not already owned
  // and equips it, all as one atomic transaction validated server-side
  // (ownership, funds against the COMBINED cost, level gate, tint
  // ownership) — the client never chains BUY then EQUIP across the 1Hz
  // broadcast. `tintId` is null for a non-tintable slot and the desired
  // tint id for a tintable one (hoodie/chair), mirroring EQUIP_ITEM.
  | { action: 'BUY_AND_EQUIP'; slot: string; itemId: string; tintId: string | null }
  | { action: 'STORE_OPEN' }
  | { action: 'STORE_CLOSE' }
  // Phase P1 (docs/ui-spec.md §6.2/§7). `name` is raw user text; the
  // SERVER trims it, drops control characters, caps it at 24 runes and
  // rejects an empty result (game.NormalizeName) — the client's own
  // trim/maxlength are a courtesy, never the validation.
  | { action: 'SET_NAME'; name: string }
  // SET-1 (docs/ui-spec.md §6.2/§11.4). ONE keyed action for every
  // preference rather than one action per checkbox — the `key` is
  // validated SERVER-side against game.PrefKeys()'s allow-list, so an
  // unknown key is an error flash and no state change. The union of
  // literal keys here is the client-side half of that: a typo is a
  // compile error rather than a round-trip that quietly fails.
  //
  // `value` is a plain boolean because every dexel preference is an
  // on/off choice; a future non-boolean preference is a deliberate wire
  // change (see app/internal/game/prefs.go), not something this type
  // should leave loose now.
  | { action: 'SET_PREF'; key: 'alwaysOnTop' | 'showAwayTime' | 'soundEnabled'; value: boolean }
  // Phase P2 (docs/plan/P2-design.md §6.2). Names PINNED: SESSION_START /
  // SESSION_STOP (the imperative pair matches the UI's Start/Stop buttons
  // and the STORE_OPEN/STORE_CLOSE verb-pair precedent). `name` is raw
  // user text and optional; the SERVER normalizes it
  // (game.NormalizeSessionName: trim, drop control chars, cap at 32
  // runes, empty is legal) — the client's own maxlength is a courtesy,
  // never the validation.
  | { action: 'SESSION_START'; name?: string }
  | { action: 'SESSION_STOP' }
  // PR-5 — Pause semantics (docs/production-runtime/MIGRATION_PLAN.md
  // §PR-5). No payload, same shape as the `STORE_OPEN`/`STORE_CLOSE`
  // no-payload-action precedent above. `PAUSE` calls provider.Stop() and
  // stops tracking (no accrual, no analytics tally, no engine ticks);
  // `RESUME` calls Engine.Reset() + provider.Start() so a stale pre-pause
  // recency state (e.g. a focus-bonus run) never survives across the gap.
  // A running session survives pause — it just stops accruing while
  // paused (its `pausedSeconds` grows instead).
  | { action: 'PAUSE' }
  | { action: 'RESUME' };
