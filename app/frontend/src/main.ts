// dev-companion frontend (F1 TypeScript port) — mechanical, behaviour-identical
// port of the former hand-written app/public/js/game.js into TypeScript,
// compiled+bundled+minified by esbuild (see app/frontend/README / package.json).
// Built against docs/ui-spec.md (DOM/WS contract), docs/art-direction.md
// (scene geometry, tint mechanism, sprite manifest), docs/upgrade-design.md
// (catalog content), and ADR 0009/0010 (honesty rules). The WS wire contract
// itself is typed in ./wire.ts.
//
// ASSET URL PREFIX: "/assets/<file>" — docs/ui-spec.md and
// docs/art-direction.md never name an explicit HTTP prefix for the sprite
// PNGs (only that "the Go server will serve them"), so this frontend uses
// "/assets/<file>". app/main.go's mux serves this route for real (a
// registerAssetsRoute() call that locates the repository's assets/
// directory via internal/assets.Locate() and mounts
// http.FileServer(http.Dir(...)) on it) — no symlink, no dev-only stopgap.

import type {
  ActiveState,
  CatalogItem,
  CatalogMessage,
  CatalogSlot,
  CatalogTint,
  Equipped,
  FlashMessage,
  ServerMessage,
  StateMessage,
  Stats,
  StatBlock,
  ClientAction
} from './wire';

const ASSET_PREFIX = '/assets/';
function assetUrl(file: string | null | undefined): string | null {
  return file ? ASSET_PREFIX + file : null;
}

const DEV_MODE = new URLSearchParams(location.search).get('dev') === '1';

// ---------------------------------------------------------------------
// Fixed geometry (docs/art-direction.md "Element placement table")
// ---------------------------------------------------------------------
interface Rect {
  left: number;
  top: number;
  w: number;
  h: number;
}

const SLOT_RECT: Record<string, Rect> = {
  wall: { left: 24, top: 16, w: 40, h: 44 },
  plant: { left: 244, top: 32, w: 40, h: 44 },
  buddy: { left: 288, top: 46, w: 28, h: 30 },
  beverage: { left: 56, top: 90, w: 20, h: 24 },
  keyboard: { left: 112, top: 90, w: 96, h: 24 },
  mouse: { left: 224, top: 90, w: 44, h: 24 }
};
const DEV_RECT: Rect = { left: 116, top: 92, w: 88, h: 104 };
const CHAIR_RECT: Record<string, Rect> = {
  chair_basic: { w: 136, h: 84, left: 92, top: 116 },
  chair_racer: { w: 140, h: 88, left: 90, top: 112 },
  chair_exec: { w: 144, h: 100, left: 88, top: 100 },
  chair_antigrav: { w: 128, h: 72, left: 96, top: 128 }
};

interface SceneryItem extends Rect {
  file: string;
  z: number;
  id?: string;
}

const SCENERY: SceneryItem[] = [
  { file: 'room_back.png', left: 0, top: 0, w: 320, h: 200, z: 1 },
  { file: 'desk_back.png', left: 0, top: 74, w: 320, h: 58, z: 3 },
  { file: 'monitor.png', left: 94, top: 20, w: 132, h: 64, z: 4, id: 'sprite-monitor' }
];
const SLOT_Z: Record<string, number> = { wall: 2, plant: 5, buddy: 6, beverage: 7, keyboard: 8, mouse: 9 };
const CHAIR_Z_FORM = 10, CHAIR_Z_DETAIL = 11;
const DEV_Z_FORM = 12, DEV_Z_STYLE = 13, DEV_Z_BASE = 14;

const MOOD_COLOR: Record<ActiveState, string> = { coding: 'var(--plant)', idle: 'var(--screen)', onBreak: 'var(--pot)' };
const FRAME_FOR_STATE: Record<'idle' | 'onBreak', string> = { idle: 'idle', onBreak: 'sleep' }; // 'coding' alternates type_a/type_b

// ---------------------------------------------------------------------
// Dev-mode hardcoded catalog + state (docs/upgrade-design.md values).
// Only used behind ?dev=1 — never loaded in normal operation.
// ---------------------------------------------------------------------
const DEV_CATALOG: CatalogMessage = {
  type: 'catalog', v: 1,
  slots: [
    { id: 'hoodie', name: 'Hoodie', tintable: true },
    { id: 'chair', name: 'Chair', tintable: true },
    { id: 'keyboard', name: 'Keyboard', tintable: false },
    { id: 'mouse', name: 'Mouse', tintable: false },
    { id: 'beverage', name: 'Beverage', tintable: false },
    { id: 'plant', name: 'Plant', tintable: false },
    { id: 'wall', name: 'Wall', tintable: false },
    { id: 'buddy', name: 'Buddy', tintable: false }
  ],
  tints: [
    { id: 'slate', name: 'Classic Black', hex: '#2b2b33', price: 40 },
    { id: 'cobalt', name: 'Cobalt Blue', hex: '#4a7fa8', price: 40 },
    { id: 'forest', name: 'Forest Green', hex: '#4e8b4f', price: 40 },
    { id: 'ember', name: 'Cyberpunk Orange', hex: '#a45c3a', price: 40 },
    { id: 'neon', name: 'Neon Pink', hex: '#e86aa4', price: 40 },
    { id: 'indigo', name: 'Midnight Indigo', hex: '#6a5aa0', price: 40 }
  ],
  items: [
    { id: 'hoodie_classic', slot: 'hoodie', name: 'Classic Pullover', price: 0, sprite: 'hoodie_classic.png', detail: null, thumb: null, thumbForm: 'thumb_hoodie_classic_form.png', thumbDetail: 'thumb_hoodie_classic_detail.png', defaultTint: 'indigo', flavor: 'Drawstrings, one pocket, no opinions.' },
    { id: 'hoodie_zip', slot: 'hoodie', name: 'Zip-Up', price: 120, sprite: 'hoodie_zip.png', detail: null, thumb: null, thumbForm: 'thumb_hoodie_zip_form.png', thumbDetail: 'thumb_hoodie_zip_detail.png', defaultTint: 'slate', flavor: 'For when the office is exactly two degrees off.' },
    { id: 'hoodie_tech', slot: 'hoodie', name: 'Techwear', price: 300, sprite: 'hoodie_tech.png', detail: null, thumb: null, thumbForm: 'thumb_hoodie_tech_form.png', thumbDetail: 'thumb_hoodie_tech_detail.png', defaultTint: 'forest', flavor: 'Straps that hold nothing. Reflective, though.' },
    { id: 'hoodie_cloak', slot: 'hoodie', name: 'Night Cloak', price: 500, sprite: 'hoodie_cloak.png', detail: null, thumb: null, thumbForm: 'thumb_hoodie_cloak_form.png', thumbDetail: 'thumb_hoodie_cloak_detail.png', defaultTint: 'neon', flavor: 'Ships at 3am or not at all.' },

    { id: 'chair_basic', slot: 'chair', name: 'Basic Office', price: 0, sprite: 'chair_basic_form.png', detail: 'chair_basic_detail.png', thumb: null, thumbForm: 'thumb_chair_basic_form.png', thumbDetail: 'thumb_chair_basic_detail.png', defaultTint: 'slate', flavor: 'Adjusts in one axis. That axis is "no".' },
    { id: 'chair_racer', slot: 'chair', name: 'Racer', price: 100, sprite: 'chair_racer_form.png', detail: 'chair_racer_detail.png', thumb: null, thumbForm: 'thumb_chair_racer_form.png', thumbDetail: 'thumb_chair_racer_detail.png', defaultTint: 'ember', flavor: 'Bolstered wings. Zero laps completed.' },
    { id: 'chair_exec', slot: 'chair', name: 'Executive Leather', price: 300, sprite: 'chair_exec_form.png', detail: 'chair_exec_detail.png', thumb: null, thumbForm: 'thumb_chair_exec_form.png', thumbDetail: 'thumb_chair_exec_detail.png', defaultTint: 'ember', flavor: 'Tufted. Reclines further than the deadline.' },
    { id: 'chair_antigrav', slot: 'chair', name: 'Anti-Gravity', price: 500, sprite: 'chair_antigrav_form.png', detail: 'chair_antigrav_detail.png', thumb: null, thumbForm: 'thumb_chair_antigrav_form.png', thumbDetail: 'thumb_chair_antigrav_detail.png', defaultTint: 'cobalt', flavor: 'Floats. Physics pending review.' },

    { id: 'kb_membrane', slot: 'keyboard', name: 'Stock Membrane', price: 0, sprite: 'kb_membrane.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Came with the machine. Still here.' },
    { id: 'kb_mech', slot: 'keyboard', name: 'Mechanical', price: 60, sprite: 'kb_mech.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Audible from the next room. Intentionally.' },
    { id: 'kb_split', slot: 'keyboard', name: 'Split Ergo', price: 180, sprite: 'kb_split.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Two halves, one wrist, endless smugness.' },
    { id: 'kb_neon', slot: 'keyboard', name: 'Neon 60%', price: 300, sprite: 'kb_neon.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Fewer keys, more colours, same bugs.' },

    { id: 'mouse_stock', slot: 'mouse', name: 'Stock Mouse', price: 0, sprite: 'mouse_stock.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Two buttons and a wheel. It works.' },
    { id: 'mouse_gaming', slot: 'mouse', name: 'Gaming Mouse', price: 50, sprite: 'mouse_gaming.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Seven buttons. Two are bound.' },
    { id: 'mouse_trackball', slot: 'mouse', name: 'Trackball', price: 150, sprite: 'mouse_trackball.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'The wrist thanks you. The cursor does not.' },
    { id: 'mouse_vertical', slot: 'mouse', name: 'Vertical Ergo', price: 220, sprite: 'mouse_vertical.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Held like a handshake with your desk.' },

    { id: 'bev_mug', slot: 'beverage', name: 'Chipped Mug', price: 0, sprite: 'bev_mug.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'The chip is load-bearing.' },
    { id: 'bev_thermos', slot: 'beverage', name: 'Thermos', price: 40, sprite: 'bev_thermos.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Still hot at 4pm. Suspiciously.' },
    { id: 'bev_teacup', slot: 'beverage', name: 'Tea & Saucer', price: 90, sprite: 'bev_teacup.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'A saucer. On a developer’s desk.' },
    { id: 'bev_energy', slot: 'beverage', name: 'Energy Can', price: 140, sprite: 'bev_energy.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Tastes like a changelog.' },

    { id: 'plant_none', slot: 'plant', name: 'Bare Desk', price: 0, sprite: null, detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Minimalism, or forgetfulness.' },
    { id: 'plant_succulent', slot: 'plant', name: 'Succulent', price: 50, sprite: 'plant_succulent.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Survives neglect. Relatable.' },
    { id: 'plant_monstera', slot: 'plant', name: 'Monstera', price: 140, sprite: 'plant_monstera.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Big leaves. Bigger commitment.' },
    { id: 'plant_bonsai', slot: 'plant', name: 'Bonsai', price: 260, sprite: 'plant_bonsai.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Pruned more carefully than the git history.' },

    { id: 'wall_bare', slot: 'wall', name: 'Bare Wall', price: 0, sprite: null, detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Ready for anything.' },
    { id: 'wall_poster', slot: 'wall', name: '"Works On My Machine"', price: 80, sprite: 'wall_poster.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'The oldest defence.' },
    { id: 'wall_shelf', slot: 'wall', name: 'Shelf: Books & Trophy', price: 200, sprite: 'wall_shelf.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Four books, one trophy, zero pages read.' },
    { id: 'wall_neon', slot: 'wall', name: 'Neon Sign', price: 380, sprite: 'wall_neon.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Casts a glow on every late commit.' },

    { id: 'buddy_none', slot: 'buddy', name: 'No Buddy', price: 0, sprite: null, detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Solo run.' },
    { id: 'buddy_duck', slot: 'buddy', name: 'Rubber Duck', price: 60, sprite: 'buddy_duck.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Best listener on the team.' },
    { id: 'buddy_bot', slot: 'buddy', name: 'Desk Bot', price: 250, sprite: 'buddy_bot_a.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Blinks. Judges. Blinks again.' },
    { id: 'buddy_cat', slot: 'buddy', name: 'Sleeping Cat', price: 300, sprite: 'buddy_cat.png', detail: null, thumb: null, thumbForm: null, thumbDetail: null, defaultTint: null, flavor: 'Has opinions about the keyboard. Asleep.' }
  ]
};

const DEV_STATE: StateMessage = {
  type: 'state', v: 1,
  activeState: 'coding',
  activityLine: 'Coding in VS Code',
  devCash: 2150,
  level: 5,
  xp: 1240,
  storeOpen: false,
  sprint: { index: 1, name: 'Refactor Auth Engine', progress: 34, target: 75, unitLabel: 'units' },
  screenLines: [
    '   Compiling companion v0.2',
    'resolved 118 deps in 0.9s',
    'func handleRequest(ctx) error {',
    '  if err != nil { return err }',
    'warning: unused import \'fmt\'',
    '$ cargo build --release',
    '[ 62%] building target...',
    'note: recompile with -v',
    '-> ok  lexer         0.6s',
    'test result: ok. 41 passed',
    '-> ok  parser        1.4s'
  ],
  tickerLines: ['Running unit 42...', 'Resolving dependencies...', 'Compiling...'],
  equipped: {
    hoodie: { itemId: 'hoodie_zip', tintId: 'cobalt' },
    chair: { itemId: 'chair_racer', tintId: 'ember' },
    keyboard: { itemId: 'kb_mech', tintId: null },
    mouse: { itemId: 'mouse_gaming', tintId: null },
    beverage: { itemId: 'bev_thermos', tintId: null },
    plant: { itemId: 'plant_none', tintId: null },
    wall: { itemId: 'wall_poster', tintId: null },
    buddy: { itemId: 'buddy_duck', tintId: null }
  },
  ownedItems: [
    'hoodie_classic', 'hoodie_zip', 'chair_basic', 'chair_racer',
    'kb_membrane', 'kb_mech', 'mouse_stock', 'mouse_gaming',
    'bev_mug', 'bev_thermos', 'plant_none', 'wall_bare', 'wall_poster',
    'buddy_none', 'buddy_duck'
  ],
  ownedTints: ['hoodie_zip:cobalt', 'chair_racer:ember'],
  stats: {
    today: { keystrokes: 842, mouseActiveSeconds: 96, activeSeconds: 610, idleSeconds: 340, sprintsCompleted: 1 },
    lifetime: { keystrokes: 58120, mouseActiveSeconds: 7400, activeSeconds: 42300, idleSeconds: 19800, sprintsCompleted: 37 }
  }
};

// ---------------------------------------------------------------------
// Module state
// ---------------------------------------------------------------------
let catalog: CatalogMessage | null = null;
let state: StateMessage | null = null;
let catalogBySlot: Record<string, CatalogItem[]> = {}; // slot id -> [items] in catalog order
let itemsById: Record<string, CatalogItem> = {};
let tintsById: Record<string, CatalogTint> = {};

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

// ---------------------------------------------------------------------
// DOM refs
// ---------------------------------------------------------------------
function byId<T extends HTMLElement = HTMLElement>(id: string): T {
  return document.getElementById(id) as T;
}

const el = {
  scene: byId<HTMLDivElement>('scene-sprites'),
  terminal: byId<HTMLDivElement>('terminal'),
  connOverlay: byId<HTMLDivElement>('conn-overlay'),
  assetsErrorOverlay: byId<HTMLDivElement>('assets-error-overlay'),
  moodDot: byId('mood-dot'),
  hudLevel: byId('hud-level'),
  hudCash: byId('hud-cash').querySelector('.value') as HTMLElement,
  storeOpenBtn: byId<HTMLButtonElement>('store-open'),
  activityOpenBtn: byId<HTMLButtonElement>('activity-open'),
  activity: byId<HTMLDialogElement>('activity'),
  activityClose: byId<HTMLButtonElement>('activity-close'),
  statTodayKeystrokes: byId('stat-today-keystrokes'),
  statTodayMouse: byId('stat-today-mouse'),
  statTodayActive: byId('stat-today-active'),
  statTodayIdle: byId('stat-today-idle'),
  statTodaySprints: byId('stat-today-sprints'),
  statLifeKeystrokes: byId('stat-life-keystrokes'),
  statLifeMouse: byId('stat-life-mouse'),
  statLifeActive: byId('stat-life-active'),
  statLifeIdle: byId('stat-life-idle'),
  statLifeSprints: byId('stat-life-sprints'),
  sprintName: byId('sprint-name').querySelector('.value') as HTMLElement,
  sprintBar: byId<HTMLProgressElement>('sprint-bar'),
  sprintUnits: byId('sprint-units'),
  statusDot: byId('status-dot'),
  statusLine: byId('status-line'),
  ticker: byId<HTMLUListElement>('ticker'),
  scrim: byId<HTMLDivElement>('scrim'),
  flash: byId<HTMLDivElement>('flash'),
  store: byId<HTMLDialogElement>('store'),
  storeClose: byId<HTMLButtonElement>('store-close'),
  storeCash: byId('store-cash').querySelector('.value') as HTMLElement,
  storeCashBox: byId('store-cash'),
  catList: document.querySelector('#store-cats .cat-list') as HTMLElement,
  grid: byId<HTMLDivElement>('store-grid'),
  scrollTrack: byId<HTMLDivElement>('store-scroll'),
  scrollThumb: document.querySelector('#store-scroll .thumb-bar') as HTMLElement,
  previewViewport: byId<HTMLDivElement>('store-preview-viewport'),
  previewName: byId('store-preview-name'),
  previewState: byId('store-preview-state'),
  previewColor: byId('store-preview-color')
};

// ---------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------
function clamp(n: number, lo: number, hi: number): number { return Math.max(lo, Math.min(hi, n)); }
// Wire numbers arrive as float64; every on-screen numeric render must be an
// integer (see ui-spec.md's own examples: "4,200 / 5,000", "LV 5", "34 / 75").
function fmtInt(n: number | undefined): string { return String(Math.floor(Number(n) || 0)); }
// Renders a whole-seconds duration count from state.stats (Analytics
// track Phase A1) as "Xm Ys" (or just "Ys" under a minute) — the wire
// sends raw seconds (see docs handed to the overseer for ui-spec.md's
// patch), never a pre-formatted string; all formatting happens here.
function fmtDuration(totalSeconds: number | undefined): string {
  const s = Math.max(0, Math.floor(Number(totalSeconds) || 0));
  const m = Math.floor(s / 60);
  const rem = s % 60;
  if (m <= 0) return rem + 's';
  return m + 'm ' + rem + 's';
}
function truncate(str: string | null | undefined, maxLen: number): string {
  str = str || '';
  if (str.length <= maxLen) return str;
  return str.slice(0, Math.max(0, maxLen - 1)) + '…';
}
// Falls back to white (never black) for an unknown/missing tint id: the
// tint mechanism paints .tint-fill with this colour then multiply-blends
// the form sprite over it (game.css ".tint-fill"/".tint-shade"), so white
// is the multiplicative identity — it leaves the form sprite's own
// (undyed/neutral) colours on screen instead of crushing them to black.
// A save/state referencing a tint id this client's catalog doesn't know
// about (stale client vs. newer server, or a bad devApply payload) must
// degrade to "untinted", never to a black silhouette.
function tintHexFor(tintId: string | null | undefined): string {
  const t = tintId ? tintsById[tintId] : undefined;
  return t ? t.hex : '#ffffff';
}
// "swatch chip = tint * 0xd4/0xff" (art-direction.md, step-4 base fabric)
function swatchColor(hex: string): string {
  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);
  const f = 0xd4 / 0xff;
  const rr = Math.round(r * f), gg = Math.round(g * f), bb = Math.round(b * f);
  return 'rgb(' + rr + ',' + gg + ',' + bb + ')';
}
function isTintOwned(item: CatalogItem | undefined, tintId: string | null): boolean {
  if (!item) return false;
  if (tintId === item.defaultTint) return true;
  return (state!.ownedTints || []).indexOf(item.id + ':' + tintId) !== -1;
}
function freeDefaultItem(slotId: string): CatalogItem | undefined {
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
function equippedItemFor(slotId: string): CatalogItem | undefined {
  const eq = state!.equipped && state!.equipped[slotId];
  const item = eq && itemsById[eq.itemId];
  if (!item && eq && eq.itemId) {
    console.warn('[dev-companion] unknown item id "' + eq.itemId + '" equipped in slot "' + slotId + '" — rendering the slot default instead');
  }
  return item || freeDefaultItem(slotId);
}
function selectedTintFor(item: CatalogItem | undefined): string | null {
  if (!item) return null;
  if (storeUI.selectedTintByItem.hasOwnProperty(item.id)) {
    return storeUI.selectedTintByItem[item.id];
  }
  const eq = state!.equipped[item.slot];
  if (eq && eq.itemId === item.id && eq.tintId) return eq.tintId;
  return item.defaultTint;
}

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

// ---------------------------------------------------------------------
// Tint mechanism (mask + multiply), art-direction.md "The CSS tint
// mechanism". Returns a DOM node ready to be positioned by the caller.
// ---------------------------------------------------------------------
function buildTintLayer(formFile: string | null | undefined, tintHex: string): HTMLDivElement {
  const wrap = document.createElement('div');
  wrap.className = 'tintable';
  wrap.style.position = 'absolute';
  wrap.style.setProperty('--tint', tintHex);
  wrap.style.setProperty('--form', 'url(' + assetUrl(formFile) + ')');
  const fill = document.createElement('div');
  fill.className = 'tint-fill';
  const shade = document.createElement('img');
  shade.className = 'tint-shade';
  shade.alt = '';
  shade.src = assetUrl(formFile) || '';
  wrap.appendChild(fill);
  wrap.appendChild(shade);
  return wrap;
}
function positionEl<T extends HTMLElement>(node: T, rect: Rect): T {
  node.style.left = rect.left + 'px';
  node.style.top = rect.top + 'px';
  node.style.width = rect.w + 'px';
  node.style.height = rect.h + 'px';
  return node;
}
function plainImg(file: string | null | undefined, rect: Rect, cls?: string): HTMLImageElement {
  const img = document.createElement('img');
  img.className = 'layer sprite' + (cls ? ' ' + cls : '');
  img.alt = '';
  img.src = assetUrl(file) || '';
  img.style.position = 'absolute';
  positionEl(img, rect);
  return img;
}

// ---------------------------------------------------------------------
// Scene rendering (#scene-sprites), art-direction.md layer order 1..14.
// ---------------------------------------------------------------------
let sceneBuilt = false;
const sceneNodes: Record<string, HTMLElement> = {}; // slot -> container node (for slots we clear+refill)
let devFrameIndex = 0; // toggles 0/1 for type_a/type_b while coding

function buildSceneSkeleton(): void {
  el.scene.innerHTML = '';
  SCENERY.forEach(function (s) {
    const img = plainImg(s.file, s);
    img.style.zIndex = String(s.z);
    if (s.id) img.id = s.id;
    // room_back.png is internal/assets' own "does assets/ even exist"
    // sentinel (see locate.go) — reuse it here as the frontend's sentinel
    // too: if this one 404s, every sprite in the scene is 404ing, and the
    // player would otherwise just see a blank room with no explanation.
    if (s.file === 'room_back.png') img.addEventListener('error', handleSpriteSentinelError);
    el.scene.appendChild(img);
  });
  ['wall', 'plant', 'buddy', 'beverage', 'keyboard', 'mouse'].forEach(function (slot) {
    const holder = document.createElement('div');
    holder.style.position = 'absolute';
    holder.style.zIndex = String(SLOT_Z[slot]);
    positionEl(holder, SLOT_RECT[slot]);
    el.scene.appendChild(holder);
    sceneNodes[slot] = holder;
  });
  const chairHolder = document.createElement('div');
  chairHolder.style.position = 'absolute';
  el.scene.appendChild(chairHolder);
  sceneNodes.chair = chairHolder;

  const devHolder = document.createElement('div');
  devHolder.style.position = 'absolute';
  positionEl(devHolder, DEV_RECT);
  el.scene.appendChild(devHolder);
  sceneNodes.dev = devHolder;

  sceneBuilt = true;
}

function currentDevFrame(): string {
  if (!state) return 'idle';
  if (state.activeState === 'coding') return devFrameIndex === 0 ? 'type_a' : 'type_b';
  return FRAME_FOR_STATE[state.activeState] || 'idle';
}

function renderSlotSprite(slotId: string): void {
  const holder = sceneNodes[slotId];
  holder.innerHTML = '';
  const item = equippedItemFor(slotId);
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
  const holder = sceneNodes.chair;
  holder.innerHTML = '';
  const eq = state!.equipped.chair;
  const item = equippedItemFor('chair');
  if (!item) return; // no chair item at all, not even a free default — nothing to draw
  const rect = CHAIR_RECT[item.id] || CHAIR_RECT.chair_basic;
  positionEl(holder, rect);
  const tint = buildTintLayer(item.sprite, tintHexFor((eq && eq.tintId) || item.defaultTint));
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
  const eq = state!.equipped.hoodie;
  const item = equippedItemFor('hoodie');
  const tintHex = tintHexFor((eq && eq.tintId) || (item && item.defaultTint));

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

function renderScene(): void {
  if (!state || !catalog) return;
  if (!sceneBuilt) buildSceneSkeleton();
  ['wall', 'plant', 'buddy', 'beverage', 'keyboard', 'mouse'].forEach(renderSlotSprite);
  renderChair();
  renderDev();
  const monitor = document.getElementById('sprite-monitor');
  if (monitor) monitor.classList.toggle('monitor-onbreak', state.activeState === 'onBreak');
}

// ---------------------------------------------------------------------
// dev frame animation (5fps while coding) — a fixed-interval timer, not
// requestAnimationFrame, per art-direction.md "Visual states".
// ---------------------------------------------------------------------
setInterval(function () {
  if (!state) return;
  if (state.activeState === 'coding') {
    devFrameIndex = devFrameIndex === 0 ? 1 : 0;
    if (sceneBuilt) renderDev();
  }
}, 200);

// idle cursor blink (0.5s), applied to the last terminal line.
let cursorOn = true;
setInterval(function () {
  cursorOn = !cursorOn;
  const cursor = el.terminal.querySelector('.cursor');
  if (cursor) cursor.classList.toggle('off', !cursorOn || !state || state.activeState !== 'idle');
}, 500);

// ---------------------------------------------------------------------
// Terminal (#terminal), ui-spec.md §3.2 / art-direction.md "screen region"
// ---------------------------------------------------------------------
function renderTerminal(): void {
  el.terminal.innerHTML = '';
  if (!state) return;
  const lines = (state.screenLines || []).slice(0, 11);
  while (lines.length < 11) lines.unshift('');
  const onBreak = state.activeState === 'onBreak';
  lines.forEach(function (text, idx) {
    const isLast = idx === lines.length - 1;
    const div = document.createElement('div');
    div.className = 'line';
    const recentCount = 2;
    const isRecent = !onBreak && (idx >= lines.length - recentCount);
    if (isRecent) div.classList.add('recent');
    const shown = isLast && onBreak ? '-- idle --' : truncate(text, 30);
    div.textContent = shown;
    if (isLast && state!.activeState === 'idle') {
      const cursor = document.createElement('span');
      cursor.className = 'cursor' + (cursorOn ? '' : ' off');
      div.appendChild(cursor);
    }
    el.terminal.appendChild(div);
  });
}

// ---------------------------------------------------------------------
// Titlebar / sprint panel / status panel
// ---------------------------------------------------------------------
function renderChrome(): void {
  if (!state) return;
  const moodColor = MOOD_COLOR[state.activeState] || MOOD_COLOR.idle;
  el.moodDot.style.background = moodColor;
  el.statusDot.style.background = moodColor;
  el.hudLevel.textContent = 'LV ' + fmtInt(state.level);
  el.hudCash.textContent = fmtInt(state.devCash);

  el.sprintName.textContent = truncate(state.sprint.name, 28);
  el.sprintBar.max = state.sprint.target;
  el.sprintBar.value = state.sprint.progress;
  // unitLabel falls back to "units" — a state missing that field (a
  // stale server, or a devApply payload that replaced sprint wholesale
  // without it) must never render the literal string "undefined".
  el.sprintUnits.textContent = fmtInt(state.sprint.progress) + ' / ' + fmtInt(state.sprint.target) + ' ' + (state.sprint.unitLabel || 'units');

  el.statusLine.textContent = truncate(state.activityLine, 34);

  const lis = el.ticker.querySelectorAll('li');
  for (let i = 0; i < lis.length; i++) {
    const raw = (state.tickerLines || [])[i] || '';
    lis[i].textContent = raw ? truncate('> ' + raw, 36) : '';
  }
}

// ---------------------------------------------------------------------
// Activity modal (Analytics track Phase A1, docs/plan/ROADMAP.md) — a
// read-only rendering of state.stats.{today,lifetime}, straight off the
// server's `state` broadcast like everything else in this file (no
// client-side derivation: the server is the sole source of truth).
// ---------------------------------------------------------------------
function renderActivity(): void {
  if (!state) return;
  const stats: Partial<Stats> = state.stats || {};
  const today: Partial<StatBlock> = stats.today || {};
  const life: Partial<StatBlock> = stats.lifetime || {};
  el.statTodayKeystrokes.textContent = fmtInt(today?.keystrokes);
  el.statTodayMouse.textContent = fmtDuration(today?.mouseActiveSeconds);
  el.statTodayActive.textContent = fmtDuration(today?.activeSeconds);
  el.statTodayIdle.textContent = fmtDuration(today?.idleSeconds);
  el.statTodaySprints.textContent = fmtInt(today?.sprintsCompleted);
  el.statLifeKeystrokes.textContent = fmtInt(life?.keystrokes);
  el.statLifeMouse.textContent = fmtDuration(life?.mouseActiveSeconds);
  el.statLifeActive.textContent = fmtDuration(life?.activeSeconds);
  el.statLifeIdle.textContent = fmtDuration(life?.idleSeconds);
  el.statLifeSprints.textContent = fmtInt(life?.sprintsCompleted);
}

function renderAll(): void {
  if (!state) return;
  renderChrome();
  renderTerminal();
  renderScene();
  if (el.store.open) {
    updateStoreCash();
    refreshGridStates();
    updatePreview();
  }
  if (el.activity.open) renderActivity();
}

// ---------------------------------------------------------------------
// Flash toast (server "flash" messages) — 1.5s, positioned over
// #store-cash while the modal is open, else over #sprint-name.
// ---------------------------------------------------------------------
let flashTimer: ReturnType<typeof setTimeout> | undefined;
function showFlash(msg: FlashMessage): void {
  clearTimeout(flashTimer);
  el.flash.textContent = msg.text || '';
  el.flash.className = 'visible kind-' + (msg.kind || 'equip');
  if (el.store.open) {
    el.flash.style.left = '384px';
    el.flash.style.top = '64px';
    el.flash.style.width = '200px';
    el.flash.style.textAlign = 'right';
  } else {
    el.flash.style.left = '18px';
    el.flash.style.top = '332px';
    el.flash.style.width = '288px';
    el.flash.style.textAlign = 'left';
  }
  flashTimer = setTimeout(function () {
    el.flash.classList.remove('visible');
  }, 1500);
}

// Client-side instant "you can't afford this" feedback (ui-spec.md §4.4):
// flash #store-cash to var(--pot) for 400ms and back. No text change.
function flashInsufficientFunds(): void {
  el.storeCashBox.classList.add('flash-insufficient');
  setTimeout(function () { el.storeCashBox.classList.remove('flash-insufficient'); }, 400);
}

// ---------------------------------------------------------------------
// Store: categories
// ---------------------------------------------------------------------
function buildCats(): void {
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
    const eq = state && state.equipped[slot.id];
    const freeItem = freeDefaultItem(slot.id);
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
  idx = clamp(idx, 0, catalog!.slots.length - 1);
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
// Store: item grid / cards
// ---------------------------------------------------------------------
function currentSlot(): CatalogSlot { return catalog!.slots[storeUI.catIndex]; }
function currentItems(): CatalogItem[] { return catalogBySlot[currentSlot().id] || []; }
function currentItem(): CatalogItem | undefined { return currentItems()[storeUI.cardIndex]; }

interface CardAction {
  kind: 'buy' | 'insufficient' | 'buytint' | 'none' | 'equip';
  label: string;
}

// ui-spec.md §4.3 — the one action button, fixed precedence.
function computeCardAction(slot: CatalogSlot, item: CatalogItem, tintId: string | null): CardAction {
  const owned = (state!.ownedItems || []).indexOf(item.id) !== -1;
  if (!owned) {
    if (state!.devCash >= item.price) return { kind: 'buy', label: 'BUY ' + item.price };
    return { kind: 'insufficient', label: 'NEED ' + item.price };
  }
  if (slot.tintable) {
    if (!isTintOwned(item, tintId)) {
      const price = (tintsById[tintId as string] || {}).price || 40;
      if (state!.devCash >= price) return { kind: 'buytint', label: 'BUY COLOUR ' + price };
      return { kind: 'insufficient', label: 'NEED ' + price };
    }
  }
  const eq = state!.equipped[slot.id];
  const sameEquip = !!eq && eq.itemId === item.id && (!slot.tintable || eq.tintId === tintId);
  if (sameEquip) return { kind: 'none', label: '✓ EQUIPPED' };
  return { kind: 'equip', label: 'EQUIP' };
}

function priceStateText(slot: CatalogSlot, item: CatalogItem, tintId: string | null): string {
  const owned = (state!.ownedItems || []).indexOf(item.id) !== -1;
  if (!owned) return item.price + ' ◆';
  const eq = state!.equipped[slot.id];
  const sameEquip = !!eq && eq.itemId === item.id && (!slot.tintable || eq.tintId === tintId);
  if (sameEquip) return 'OWNED · EQUIPPED';
  return 'OWNED';
}

function buildGrid(): void {
  el.grid.innerHTML = '';
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
      catalog!.tints.forEach(function (tint) {
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
// item.thumbDetail (see internal/game/catalog.go and this reconciliation
// pass's proposed docs/ui-spec.md §6.1 wording). Falls back to the
// thumb_<id>_form.png / thumb_<id>_detail.png naming convention only if
// an older backend build hasn't sent those fields yet, so this frontend
// degrades gracefully rather than breaking outright against a stale
// catalog message.
function buildThumb(slot: CatalogSlot, item: CatalogItem): HTMLElement {
  if (slot.tintable) {
    if (!item.sprite) { return document.createElement('span'); }
    const tintId = selectedTintFor(item);
    const formFile = item.thumbForm || ('thumb_' + item.id + '_form.png');
    const detailFile = item.thumbDetail || ('thumb_' + item.id + '_detail.png');
    const wrap = document.createElement('div');
    wrap.style.position = 'absolute';
    wrap.style.inset = '0';
    const tint = buildTintLayer(formFile, tintHexFor(tintId));
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

function refreshGridStates(): void {
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
        chip.classList.toggle('unowned', !isTintOwned(item, tid));
      });
      // thumbnail tint follows the selected swatch
      const tintWrap = card.querySelector('.tintable') as HTMLElement | null;
      if (tintWrap) {
        tintWrap.style.setProperty('--tint', tintHexFor(tintId));
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
// Store: preview pane (ui-spec.md §4.2)
// ---------------------------------------------------------------------
function updatePreview(): void {
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
  el.previewColor.textContent = slot.tintable ? truncate((tintsById[tintId as string] || {}).name || '', 24) : '';
}

// hoodie/chair composed mini-scene at 1x, centred in the 152x152 viewport
// (ui-spec.md §4.2): the previewed slot uses the selected item+tint, every
// other one of {hoodie, chair} uses whatever is currently equipped.
function renderComposedPreview(previewSlotId: string, previewItem: CatalogItem, previewTintId: string | null): void {
  const hoodieEq = state!.equipped.hoodie;
  const chairEq = state!.equipped.chair;
  const hoodieItem = previewSlotId === 'hoodie' ? previewItem : itemsById[hoodieEq.itemId];
  const hoodieTint = previewSlotId === 'hoodie' ? previewTintId : (hoodieEq.tintId || hoodieItem.defaultTint);
  const chairItem = previewSlotId === 'chair' ? previewItem : itemsById[chairEq.itemId];
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
  const chairTintLayer = buildTintLayer(chairItem.sprite, tintHexFor(chairTint));
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
  const formLayer = buildTintLayer('dev_form_' + frame + '.png', tintHexFor(hoodieTint));
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
// Store open/close (ui-spec.md §4.5, §5.3 — STORE_OPEN/STORE_CLOSE)
// ---------------------------------------------------------------------
function ensureStoreDefaults(): void {
  if (storeUI.initialized || !catalog || !state) return;
  const hoodieIdx = catalog.slots.findIndex(function (s) { return s.id === 'hoodie'; });
  storeUI.catIndex = hoodieIdx === -1 ? 0 : hoodieIdx;
  const items = catalogBySlot.hoodie || [];
  const eq = state.equipped.hoodie;
  let idx = 0;
  for (let i = 0; i < items.length; i++) if (eq && items[i].id === eq.itemId) { idx = i; break; }
  storeUI.cardIndex = idx;
  storeUI.focus = 'cards';
  storeUI.initialized = true;
}

function openStore(): void {
  if (el.store.open) return;
  ensureStoreDefaults();
  buildCats();
  buildGrid();
  updatePreview();
  updateStoreCash();
  el.store.showModal();
  el.scrim.classList.add('visible');
  sendAction({ action: 'STORE_OPEN' });
  updateScrollThumb();
}
function closeStore(): void {
  if (!el.store.open) return;
  el.store.close(); // fires 'close' below regardless of trigger (X / S / Tab / Esc)
}
el.store.addEventListener('close', function () {
  el.scrim.classList.remove('visible');
  sendAction({ action: 'STORE_CLOSE' });
  storeReassertSent = false; // next open starts the B2 reassert guard fresh
});
el.storeOpenBtn.addEventListener('click', openStore);
el.storeClose.addEventListener('click', closeStore);

function updateStoreCash(): void {
  if (!state) return;
  el.storeCash.textContent = fmtInt(state.devCash);
}

// ---------------------------------------------------------------------
// Activity modal open/close (Analytics track Phase A1). Deliberately
// sends NO open/close action to the server, unlike STORE_OPEN/CLOSE:
// this modal is read-only and gates nothing (no earning to freeze), and
// the counts it displays come entirely from the server's own per-tick
// sampling of the real, global activity provider — never from anything
// this page does. Opening it via the [A] key IS a real keystroke on the
// user's system and gets honestly counted like any other keypress
// (exactly as pressing [S] to open the store already does today); this
// page never simulates or double-counts a keystroke on top of that by
// rendering the dialog, so there is no separate inflation risk to gate
// against.
// ---------------------------------------------------------------------
function openActivity(): void {
  if (el.activity.open) return;
  renderActivity();
  el.activity.showModal();
  el.scrim.classList.add('visible');
}
function closeActivity(): void {
  if (!el.activity.open) return;
  el.activity.close();
}
el.activity.addEventListener('close', function () {
  el.scrim.classList.remove('visible');
});
el.activityOpenBtn.addEventListener('click', openActivity);
el.activityClose.addEventListener('click', closeActivity);

// ---------------------------------------------------------------------
// Input — keyboard (ui-spec.md §5.2)
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
  const tint = catalog!.tints[idx];
  if (!tint) return;
  selectTint(item, tint.id);
}
function cycleSwatch(dir: number): void {
  const slot = currentSlot(), item = currentItem();
  if (!slot.tintable || !item) return;
  const tints = catalog!.tints;
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

document.addEventListener('keydown', function (e: KeyboardEvent) {
  if (el.store.open) {
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
      case 's': case 'S': closeStore(); break;
      case 'Tab': closeStore(); break; // do not preventDefault: leave native focus cycling alone
      default: break; // Esc: native <dialog> behaviour, not intercepted
    }
  } else if (el.activity.open) {
    switch (e.key) {
      case 'a': case 'A': closeActivity(); break;
      default: break; // Esc: native <dialog> behaviour, not intercepted
    }
  } else {
    if (e.key === 's' || e.key === 'S') { e.preventDefault(); openStore(); }
    else if (e.key === 'Tab') { e.preventDefault(); openStore(); }
    else if (e.key === 'a' || e.key === 'A') { e.preventDefault(); openActivity(); }
  }
});

// ---------------------------------------------------------------------
// Connection status overlay
// ---------------------------------------------------------------------
let attempt = 0;
function showConnOverlay(reconnecting: boolean): void {
  (el.connOverlay.querySelector('span') as HTMLElement).textContent = reconnecting ? 'RECONNECTING...' : 'CONNECTING...';
  el.connOverlay.classList.add('visible');
}
function hideConnOverlay(): void { el.connOverlay.classList.remove('visible'); }

// ---------------------------------------------------------------------
// Assets-missing banner — the frontend half of never leaving a blank,
// unexplained scene when the server couldn't find assets/ (backend half:
// internal/assets.LocateVerbose + GET /api/health, main.go).
// ---------------------------------------------------------------------
let assetsErrorShown = false;
function showAssetsErrorBanner(detail?: string): void {
  if (assetsErrorShown) return; // one banner is enough; don't refetch per failed sprite
  assetsErrorShown = true;
  let msg = 'ASSETS NOT FOUND — the server could not locate the assets/ directory. ' +
    'Run from the repo, or set DEVCOMPANION_ASSETS_DIR.';
  if (detail) msg += ' (' + detail + ')';
  (el.assetsErrorOverlay.querySelector('span') as HTMLElement).textContent = msg;
  el.assetsErrorOverlay.classList.add('visible');
}
function handleSpriteSentinelError(): void {
  if (DEV_MODE) return; // no backend / no /api/health to ask in dev mode
  fetch('/api/health').then(function (r) { return r.json(); }).then(function (h) {
    showAssetsErrorBanner('server assetsDir: ' + (h && h.assetsDir ? h.assetsDir : 'null'));
  }).catch(function () {
    showAssetsErrorBanner('/api/health unreachable');
  });
}

// ---------------------------------------------------------------------
// WebSocket (ui-spec.md §6) — camelCase wire contract, backoff retry.
// ---------------------------------------------------------------------
let ws: WebSocket | null = null;
let reconnectDelay = 500;
let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
// B2 loop guard: true once we've re-sent STORE_OPEN in response to a
// `state` snapshot showing storeOpen:false while our modal is still
// open, until the server confirms the gate is open again (or we close
// the modal ourselves). Without this, EVERY subsequent `state` frame
// that still shows storeOpen:false (e.g. while our re-assertion is in
// flight, or a stalled ws.send that never reaches the server) would
// trigger another STORE_OPEN send, once per ~1s tick, forever.
let storeReassertSent = false;

function sendAction(action: ClientAction): void {
  if (DEV_MODE) {
    // No backend to talk to in dev mode. Log so a human/orchestrator can
    // see intent; drive actual state changes via window.devApply instead.
    console.log('[dev mode] would send:', action);
    return;
  }
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(action));
  }
}

function handleServerMessage(msg: ServerMessage | null | undefined): void {
  if (!msg || !msg.type) return;
  switch (msg.type) {
    case 'catalog':
      catalog = msg;
      indexCatalog();
      if (el.store.open) buildCats();
      break;
    case 'state':
      state = msg;
      hideConnOverlay();
      renderAll();
      // B2 work-gate: the server is the source of truth for storeOpen
      // (docs/ui-spec.md §5.3's earning gate), but the server's flag is
      // now scoped per-connection (a refcounted set of connIDs holding
      // it open, not one global bool). If our own store modal is still
      // showing but a state snapshot says storeOpen is false — most
      // likely because our OWN hold hasn't landed yet (e.g. this
      // snapshot arrived from a fresh reconnect's initial send, before
      // our re-asserted STORE_OPEN below was applied) — re-assert it
      // ONCE (storeReassertSent guards against re-sending on every
      // subsequent tick that still shows storeOpen:false) so the modal
      // being open and the server's gate can never silently drift
      // apart, without looping.
      if (el.store.open) {
        if (msg.storeOpen === false) {
          if (!storeReassertSent) {
            storeReassertSent = true;
            sendAction({ action: 'STORE_OPEN' });
          }
        } else {
          storeReassertSent = false; // gate confirmed open again
        }
      }
      break;
    case 'flash':
      showFlash(msg);
      break;
    default:
      break;
  }
}

function connectWS(): void {
  showConnOverlay(attempt > 0);
  attempt++;
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  let sock: WebSocket;
  try {
    sock = new WebSocket(proto + '//' + location.host + '/ws');
  } catch (e) {
    scheduleReconnect();
    return;
  }
  ws = sock;
  sock.addEventListener('open', function () {
    reconnectDelay = 500;
    // B2: a reconnect (server restart/blip, or this tab's own network
    // hiccup) gets a brand-new connID server-side — the OLD connID's
    // store-open hold was already released on disconnect
    // (handlers.go's defer), so if our store modal is still open
    // locally, we must re-open it under the new connection or the
    // work/Dev Cash gate silently comes back "closed" despite the
    // modal still being up.
    if (el.store.open) {
      sendAction({ action: 'STORE_OPEN' });
    }
  });
  sock.addEventListener('message', function (ev: MessageEvent) {
    try { handleServerMessage(JSON.parse(ev.data) as ServerMessage); }
    catch (err) { console.error('bad ws message', err); }
  });
  sock.addEventListener('close', function () { scheduleReconnect(); });
  sock.addEventListener('error', function () { /* 'close' follows */ });
}
function scheduleReconnect(): void {
  showConnOverlay(true);
  clearTimeout(reconnectTimer);
  reconnectTimer = setTimeout(connectWS, reconnectDelay);
  reconnectDelay = Math.min(reconnectDelay * 2, 8000);
}

// ---------------------------------------------------------------------
// Boot
// ---------------------------------------------------------------------
declare global {
  interface Window {
    devApply?: (partialState: Partial<StateMessage> & { equipped?: Partial<Equipped> }) => void;
    devCatalog?: CatalogMessage;
  }
}

if (DEV_MODE) {
  document.title = 'dev companion [DEV MODE]';
  catalog = DEV_CATALOG;
  indexCatalog();
  state = DEV_STATE;
  hideConnOverlay();
  renderAll();

  // Verification hook: window.devApply(stateJson) merges into the current
  // state and re-renders, so the orchestrator can drive specific states
  // for a screenshot without a running backend. Only defined in dev mode.
  //
  // Validates partialState.equipped.*.itemId/tintId against the loaded
  // catalog before merging: an unknown item id gets dropped (console.warn
  // says which one and for which slot) so it can't overwrite a
  // previously-valid slot with a dangling reference, and an unknown tint
  // id is cleared to null rather than left pointing at nothing. Render
  // time (equippedItemFor / tintHexFor) already degrades gracefully
  // either way — this is defense in depth plus a paper trail for whoever
  // is driving the harness.
  window.devApply = function (partialState) {
    const incoming = partialState || {};
    if (incoming.equipped) {
      Object.keys(incoming.equipped).forEach(function (slotId) {
        const eq = incoming.equipped![slotId];
        if (!eq) return;
        if (eq.itemId && !itemsById[eq.itemId]) {
          console.warn('[devApply] dropping unknown item id "' + eq.itemId + '" for slot "' + slotId + '" (not in loaded catalog)');
          delete incoming.equipped![slotId];
          return;
        }
        if (eq.tintId && !tintsById[eq.tintId]) {
          console.warn('[devApply] clearing unknown tint id "' + eq.tintId + '" for slot "' + slotId + '" (not in loaded catalog)');
          eq.tintId = null;
        }
      });
    }
    state = Object.assign({}, state, incoming) as StateMessage;
    renderAll();
  };
  window.devCatalog = DEV_CATALOG;
} else {
  connectWS();
}
