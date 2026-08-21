// dev-companion frontend (W2-β) — vanilla JS, no framework, no build step.
// Built against docs/ui-spec.md (DOM/WS contract), docs/art-direction.md
// (scene geometry, tint mechanism, sprite manifest), docs/upgrade-design.md
// (catalog content), and ADR 0009/0010 (honesty rules).
//
// ASSET URL PREFIX: "/assets/<file>" — docs/ui-spec.md and
// docs/art-direction.md never name an explicit HTTP prefix for the sprite
// PNGs (only that "the Go server will serve them"), so this frontend uses
// "/assets/<file>". app/main.go's mux serves this route for real (a
// registerAssetsRoute() call that locates the repository's assets/
// directory via internal/assets.Locate() and mounts
// http.FileServer(http.Dir(...)) on it) — no symlink, no dev-only stopgap.

(function () {
  'use strict';

  var ASSET_PREFIX = '/assets/';
  function assetUrl(file) { return file ? (ASSET_PREFIX + file) : null; }

  var DEV_MODE = new URLSearchParams(location.search).get('dev') === '1';

  // ---------------------------------------------------------------------
  // Fixed geometry (docs/art-direction.md "Element placement table")
  // ---------------------------------------------------------------------
  var SLOT_RECT = {
    wall:     { left: 24,  top: 16, w: 40, h: 44 },
    plant:    { left: 244, top: 32, w: 40, h: 44 },
    buddy:    { left: 288, top: 46, w: 28, h: 30 },
    beverage: { left: 56,  top: 90, w: 20, h: 24 },
    keyboard: { left: 112, top: 90, w: 96, h: 24 },
    mouse:    { left: 224, top: 90, w: 44, h: 24 }
  };
  var DEV_RECT = { left: 116, top: 92, w: 88, h: 104 };
  var CHAIR_RECT = {
    chair_basic:    { w: 136, h: 84,  left: 92, top: 116 },
    chair_racer:    { w: 140, h: 88,  left: 90, top: 112 },
    chair_exec:     { w: 144, h: 100, left: 88, top: 100 },
    chair_antigrav: { w: 128, h: 72,  left: 96, top: 128 }
  };
  var SCENERY = [
    { file: 'room_back.png', left: 0, top: 0, w: 320, h: 200, z: 1 },
    { file: 'desk_back.png', left: 0, top: 74, w: 320, h: 58, z: 3 },
    { file: 'monitor.png', left: 94, top: 20, w: 132, h: 64, z: 4, id: 'sprite-monitor' }
  ];
  var SLOT_Z = { wall: 2, plant: 5, buddy: 6, beverage: 7, keyboard: 8, mouse: 9 };
  var CHAIR_Z_FORM = 10, CHAIR_Z_DETAIL = 11;
  var DEV_Z_FORM = 12, DEV_Z_STYLE = 13, DEV_Z_BASE = 14;

  var MOOD_COLOR = { coding: 'var(--plant)', idle: 'var(--screen)', onBreak: 'var(--pot)' };
  var FRAME_FOR_STATE = { idle: 'idle', onBreak: 'sleep' }; // 'coding' alternates type_a/type_b

  // ---------------------------------------------------------------------
  // Dev-mode hardcoded catalog + state (docs/upgrade-design.md values).
  // Only used behind ?dev=1 — never loaded in normal operation.
  // ---------------------------------------------------------------------
  var DEV_CATALOG = {
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
      { id: 'hoodie_classic', slot: 'hoodie', name: 'Classic Pullover', price: 0, sprite: 'hoodie_classic.png', detail: null, thumbForm: 'thumb_hoodie_classic_form.png', thumbDetail: 'thumb_hoodie_classic_detail.png', defaultTint: 'indigo', flavor: 'Drawstrings, one pocket, no opinions.' },
      { id: 'hoodie_zip', slot: 'hoodie', name: 'Zip-Up', price: 120, sprite: 'hoodie_zip.png', detail: null, thumbForm: 'thumb_hoodie_zip_form.png', thumbDetail: 'thumb_hoodie_zip_detail.png', defaultTint: 'slate', flavor: 'For when the office is exactly two degrees off.' },
      { id: 'hoodie_tech', slot: 'hoodie', name: 'Techwear', price: 300, sprite: 'hoodie_tech.png', detail: null, thumbForm: 'thumb_hoodie_tech_form.png', thumbDetail: 'thumb_hoodie_tech_detail.png', defaultTint: 'forest', flavor: 'Straps that hold nothing. Reflective, though.' },
      { id: 'hoodie_cloak', slot: 'hoodie', name: 'Night Cloak', price: 500, sprite: 'hoodie_cloak.png', detail: null, thumbForm: 'thumb_hoodie_cloak_form.png', thumbDetail: 'thumb_hoodie_cloak_detail.png', defaultTint: 'neon', flavor: 'Ships at 3am or not at all.' },

      { id: 'chair_basic', slot: 'chair', name: 'Basic Office', price: 0, sprite: 'chair_basic_form.png', detail: 'chair_basic_detail.png', thumbForm: 'thumb_chair_basic_form.png', thumbDetail: 'thumb_chair_basic_detail.png', defaultTint: 'slate', flavor: 'Adjusts in one axis. That axis is "no".' },
      { id: 'chair_racer', slot: 'chair', name: 'Racer', price: 100, sprite: 'chair_racer_form.png', detail: 'chair_racer_detail.png', thumbForm: 'thumb_chair_racer_form.png', thumbDetail: 'thumb_chair_racer_detail.png', defaultTint: 'ember', flavor: 'Bolstered wings. Zero laps completed.' },
      { id: 'chair_exec', slot: 'chair', name: 'Executive Leather', price: 300, sprite: 'chair_exec_form.png', detail: 'chair_exec_detail.png', thumbForm: 'thumb_chair_exec_form.png', thumbDetail: 'thumb_chair_exec_detail.png', defaultTint: 'ember', flavor: 'Tufted. Reclines further than the deadline.' },
      { id: 'chair_antigrav', slot: 'chair', name: 'Anti-Gravity', price: 500, sprite: 'chair_antigrav_form.png', detail: 'chair_antigrav_detail.png', thumbForm: 'thumb_chair_antigrav_form.png', thumbDetail: 'thumb_chair_antigrav_detail.png', defaultTint: 'cobalt', flavor: 'Floats. Physics pending review.' },

      { id: 'kb_membrane', slot: 'keyboard', name: 'Stock Membrane', price: 0, sprite: 'kb_membrane.png', detail: null, defaultTint: null, flavor: 'Came with the machine. Still here.' },
      { id: 'kb_mech', slot: 'keyboard', name: 'Mechanical', price: 60, sprite: 'kb_mech.png', detail: null, defaultTint: null, flavor: 'Audible from the next room. Intentionally.' },
      { id: 'kb_split', slot: 'keyboard', name: 'Split Ergo', price: 180, sprite: 'kb_split.png', detail: null, defaultTint: null, flavor: 'Two halves, one wrist, endless smugness.' },
      { id: 'kb_neon', slot: 'keyboard', name: 'Neon 60%', price: 300, sprite: 'kb_neon.png', detail: null, defaultTint: null, flavor: 'Fewer keys, more colours, same bugs.' },

      { id: 'mouse_stock', slot: 'mouse', name: 'Stock Mouse', price: 0, sprite: 'mouse_stock.png', detail: null, defaultTint: null, flavor: 'Two buttons and a wheel. It works.' },
      { id: 'mouse_gaming', slot: 'mouse', name: 'Gaming Mouse', price: 50, sprite: 'mouse_gaming.png', detail: null, defaultTint: null, flavor: 'Seven buttons. Two are bound.' },
      { id: 'mouse_trackball', slot: 'mouse', name: 'Trackball', price: 150, sprite: 'mouse_trackball.png', detail: null, defaultTint: null, flavor: 'The wrist thanks you. The cursor does not.' },
      { id: 'mouse_vertical', slot: 'mouse', name: 'Vertical Ergo', price: 220, sprite: 'mouse_vertical.png', detail: null, defaultTint: null, flavor: 'Held like a handshake with your desk.' },

      { id: 'bev_mug', slot: 'beverage', name: 'Chipped Mug', price: 0, sprite: 'bev_mug.png', detail: null, defaultTint: null, flavor: 'The chip is load-bearing.' },
      { id: 'bev_thermos', slot: 'beverage', name: 'Thermos', price: 40, sprite: 'bev_thermos.png', detail: null, defaultTint: null, flavor: 'Still hot at 4pm. Suspiciously.' },
      { id: 'bev_teacup', slot: 'beverage', name: 'Tea & Saucer', price: 90, sprite: 'bev_teacup.png', detail: null, defaultTint: null, flavor: 'A saucer. On a developer’s desk.' },
      { id: 'bev_energy', slot: 'beverage', name: 'Energy Can', price: 140, sprite: 'bev_energy.png', detail: null, defaultTint: null, flavor: 'Tastes like a changelog.' },

      { id: 'plant_none', slot: 'plant', name: 'Bare Desk', price: 0, sprite: null, detail: null, defaultTint: null, flavor: 'Minimalism, or forgetfulness.' },
      { id: 'plant_succulent', slot: 'plant', name: 'Succulent', price: 50, sprite: 'plant_succulent.png', detail: null, defaultTint: null, flavor: 'Survives neglect. Relatable.' },
      { id: 'plant_monstera', slot: 'plant', name: 'Monstera', price: 140, sprite: 'plant_monstera.png', detail: null, defaultTint: null, flavor: 'Big leaves. Bigger commitment.' },
      { id: 'plant_bonsai', slot: 'plant', name: 'Bonsai', price: 260, sprite: 'plant_bonsai.png', detail: null, defaultTint: null, flavor: 'Pruned more carefully than the git history.' },

      { id: 'wall_bare', slot: 'wall', name: 'Bare Wall', price: 0, sprite: null, detail: null, defaultTint: null, flavor: 'Ready for anything.' },
      { id: 'wall_poster', slot: 'wall', name: '"Works On My Machine"', price: 80, sprite: 'wall_poster.png', detail: null, defaultTint: null, flavor: 'The oldest defence.' },
      { id: 'wall_shelf', slot: 'wall', name: 'Shelf: Books & Trophy', price: 200, sprite: 'wall_shelf.png', detail: null, defaultTint: null, flavor: 'Four books, one trophy, zero pages read.' },
      { id: 'wall_neon', slot: 'wall', name: 'Neon Sign', price: 380, sprite: 'wall_neon.png', detail: null, defaultTint: null, flavor: 'Casts a glow on every late commit.' },

      { id: 'buddy_none', slot: 'buddy', name: 'No Buddy', price: 0, sprite: null, detail: null, defaultTint: null, flavor: 'Solo run.' },
      { id: 'buddy_duck', slot: 'buddy', name: 'Rubber Duck', price: 60, sprite: 'buddy_duck.png', detail: null, defaultTint: null, flavor: 'Best listener on the team.' },
      { id: 'buddy_bot', slot: 'buddy', name: 'Desk Bot', price: 250, sprite: 'buddy_bot_a.png', detail: null, defaultTint: null, flavor: 'Blinks. Judges. Blinks again.' },
      { id: 'buddy_cat', slot: 'buddy', name: 'Sleeping Cat', price: 300, sprite: 'buddy_cat.png', detail: null, defaultTint: null, flavor: 'Has opinions about the keyboard. Asleep.' }
    ]
  };

  var DEV_STATE = {
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
    ownedTints: ['hoodie_zip:cobalt', 'chair_racer:ember']
  };

  // ---------------------------------------------------------------------
  // Module state
  // ---------------------------------------------------------------------
  var catalog = null;
  var state = null;
  var catalogBySlot = {}; // slot id -> [items] in catalog order
  var itemsById = {};
  var tintsById = {};

  var storeUI = {
    initialized: false,
    catIndex: 0,
    cardIndex: 0,
    focus: 'cards', // 'cats' | 'cards'
    selectedTintByItem: {} // itemId -> tintId
  };

  // ---------------------------------------------------------------------
  // DOM refs
  // ---------------------------------------------------------------------
  var el = {
    scene: document.getElementById('scene-sprites'),
    terminal: document.getElementById('terminal'),
    connOverlay: document.getElementById('conn-overlay'),
    moodDot: document.getElementById('mood-dot'),
    hudLevel: document.getElementById('hud-level'),
    hudCash: document.getElementById('hud-cash').querySelector('.value'),
    storeOpenBtn: document.getElementById('store-open'),
    sprintName: document.getElementById('sprint-name').querySelector('.value'),
    sprintBar: document.getElementById('sprint-bar'),
    sprintUnits: document.getElementById('sprint-units'),
    statusDot: document.getElementById('status-dot'),
    statusLine: document.getElementById('status-line'),
    ticker: document.getElementById('ticker'),
    scrim: document.getElementById('scrim'),
    flash: document.getElementById('flash'),
    store: document.getElementById('store'),
    storeClose: document.getElementById('store-close'),
    storeCash: document.getElementById('store-cash').querySelector('.value'),
    storeCashBox: document.getElementById('store-cash'),
    catList: document.querySelector('#store-cats .cat-list'),
    grid: document.getElementById('store-grid'),
    scrollTrack: document.getElementById('store-scroll'),
    scrollThumb: document.querySelector('#store-scroll .thumb-bar'),
    previewViewport: document.getElementById('store-preview-viewport'),
    previewName: document.getElementById('store-preview-name'),
    previewState: document.getElementById('store-preview-state'),
    previewColor: document.getElementById('store-preview-color')
  };

  // ---------------------------------------------------------------------
  // Small helpers
  // ---------------------------------------------------------------------
  function clamp(n, lo, hi) { return Math.max(lo, Math.min(hi, n)); }
  function truncate(str, maxLen) {
    str = str || '';
    if (str.length <= maxLen) return str;
    return str.slice(0, Math.max(0, maxLen - 1)) + '…';
  }
  function tintHexFor(tintId) {
    var t = tintsById[tintId];
    return t ? t.hex : '#000000';
  }
  // "swatch chip = tint * 0xd4/0xff" (art-direction.md, step-4 base fabric)
  function swatchColor(hex) {
    var r = parseInt(hex.slice(1, 3), 16);
    var g = parseInt(hex.slice(3, 5), 16);
    var b = parseInt(hex.slice(5, 7), 16);
    var f = 0xd4 / 0xff;
    var rr = Math.round(r * f), gg = Math.round(g * f), bb = Math.round(b * f);
    return 'rgb(' + rr + ',' + gg + ',' + bb + ')';
  }
  function isTintOwned(item, tintId) {
    if (!item) return false;
    if (tintId === item.defaultTint) return true;
    return (state.ownedTints || []).indexOf(item.id + ':' + tintId) !== -1;
  }
  function freeDefaultItem(slotId) {
    var items = catalogBySlot[slotId] || [];
    for (var i = 0; i < items.length; i++) if (items[i].price === 0) return items[i];
    return items[0];
  }
  function selectedTintFor(item) {
    if (!item) return null;
    if (storeUI.selectedTintByItem.hasOwnProperty(item.id)) {
      return storeUI.selectedTintByItem[item.id];
    }
    var eq = state.equipped[item.slot];
    if (eq && eq.itemId === item.id && eq.tintId) return eq.tintId;
    return item.defaultTint;
  }

  function indexCatalog() {
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
  function buildTintLayer(formFile, tintHex) {
    var wrap = document.createElement('div');
    wrap.className = 'tintable';
    wrap.style.position = 'absolute';
    wrap.style.setProperty('--tint', tintHex);
    wrap.style.setProperty('--form', 'url(' + assetUrl(formFile) + ')');
    var fill = document.createElement('div');
    fill.className = 'tint-fill';
    var shade = document.createElement('img');
    shade.className = 'tint-shade';
    shade.alt = '';
    shade.src = assetUrl(formFile);
    wrap.appendChild(fill);
    wrap.appendChild(shade);
    return wrap;
  }
  function positionEl(node, rect) {
    node.style.left = rect.left + 'px';
    node.style.top = rect.top + 'px';
    node.style.width = rect.w + 'px';
    node.style.height = rect.h + 'px';
    return node;
  }
  function plainImg(file, rect, cls) {
    var img = document.createElement('img');
    img.className = 'layer sprite' + (cls ? ' ' + cls : '');
    img.alt = '';
    img.src = assetUrl(file);
    img.style.position = 'absolute';
    positionEl(img, rect);
    return img;
  }

  // ---------------------------------------------------------------------
  // Scene rendering (#scene-sprites), art-direction.md layer order 1..14.
  // ---------------------------------------------------------------------
  var sceneBuilt = false;
  var sceneNodes = {}; // slot -> container node (for slots we clear+refill)
  var devFrameIndex = 0; // toggles 0/1 for type_a/type_b while coding

  function buildSceneSkeleton() {
    el.scene.innerHTML = '';
    SCENERY.forEach(function (s) {
      var img = plainImg(s.file, s);
      img.style.zIndex = s.z;
      if (s.id) img.id = s.id;
      el.scene.appendChild(img);
    });
    ['wall', 'plant', 'buddy', 'beverage', 'keyboard', 'mouse'].forEach(function (slot) {
      var holder = document.createElement('div');
      holder.style.position = 'absolute';
      holder.style.zIndex = SLOT_Z[slot];
      positionEl(holder, SLOT_RECT[slot]);
      el.scene.appendChild(holder);
      sceneNodes[slot] = holder;
    });
    var chairHolder = document.createElement('div');
    chairHolder.style.position = 'absolute';
    el.scene.appendChild(chairHolder);
    sceneNodes.chair = chairHolder;

    var devHolder = document.createElement('div');
    devHolder.style.position = 'absolute';
    positionEl(devHolder, DEV_RECT);
    el.scene.appendChild(devHolder);
    sceneNodes.dev = devHolder;

    sceneBuilt = true;
  }

  function currentDevFrame() {
    if (!state) return 'idle';
    if (state.activeState === 'coding') return devFrameIndex === 0 ? 'type_a' : 'type_b';
    return FRAME_FOR_STATE[state.activeState] || 'idle';
  }

  function renderSlotSprite(slotId) {
    var holder = sceneNodes[slotId];
    holder.innerHTML = '';
    var eq = state.equipped[slotId];
    var item = eq && itemsById[eq.itemId];
    if (!item || !item.sprite) return; // *_none item: slot stays hidden
    var img = document.createElement('img');
    img.className = 'layer sprite';
    img.alt = '';
    img.src = assetUrl(item.sprite);
    img.style.position = 'absolute';
    img.style.left = '0';
    img.style.top = '0';
    img.style.width = SLOT_RECT[slotId].w + 'px';
    img.style.height = SLOT_RECT[slotId].h + 'px';
    holder.appendChild(img);
  }

  function renderChair() {
    var holder = sceneNodes.chair;
    holder.innerHTML = '';
    var eq = state.equipped.chair;
    var item = eq && itemsById[eq.itemId];
    if (!item) return;
    var rect = CHAIR_RECT[item.id] || CHAIR_RECT.chair_basic;
    positionEl(holder, rect);
    var tint = buildTintLayer(item.sprite, tintHexFor(eq.tintId || item.defaultTint));
    tint.style.zIndex = CHAIR_Z_FORM;
    positionEl(tint, { left: 0, top: 0, w: rect.w, h: rect.h });
    holder.appendChild(tint);
    if (item.detail) {
      var detail = document.createElement('img');
      detail.className = 'layer sprite';
      detail.alt = '';
      detail.src = assetUrl(item.detail);
      detail.style.position = 'absolute';
      detail.style.left = '0';
      detail.style.top = '0';
      detail.style.width = rect.w + 'px';
      detail.style.height = rect.h + 'px';
      detail.style.zIndex = CHAIR_Z_DETAIL;
      holder.appendChild(detail);
    }
  }

  // The developer composite is the one non-generic slot (art-direction.md
  // "Scene contract"): dev_form_<frame> (tinted by the hoodie's tint) +
  // hoodie_<style> (the equipped hoodie item's own palette-pure file,
  // trusted straight off item.sprite — the wire already carries the true
  // single-file filename; see internal/game/catalog.go) + dev_base_<frame>
  // (frame-driven, always present).
  function renderDev() {
    var holder = sceneNodes.dev;
    holder.innerHTML = '';
    var frame = currentDevFrame();
    var eq = state.equipped.hoodie;
    var item = eq && itemsById[eq.itemId];
    var tintHex = tintHexFor((eq && eq.tintId) || (item && item.defaultTint));

    var formLayer = buildTintLayer('dev_form_' + frame + '.png', tintHex);
    formLayer.style.zIndex = DEV_Z_FORM;
    positionEl(formLayer, { left: 0, top: 0, w: DEV_RECT.w, h: DEV_RECT.h });
    holder.appendChild(formLayer);

    if (item) {
      var style = document.createElement('img');
      style.className = 'layer sprite';
      style.alt = '';
      style.src = assetUrl(item.sprite);
      style.style.position = 'absolute';
      style.style.left = '0';
      style.style.top = '0';
      style.style.width = DEV_RECT.w + 'px';
      style.style.height = DEV_RECT.h + 'px';
      style.style.zIndex = DEV_Z_STYLE;
      holder.appendChild(style);
    }

    var base = document.createElement('img');
    base.className = 'layer sprite';
    base.alt = '';
    base.src = assetUrl('dev_base_' + frame + '.png');
    base.style.position = 'absolute';
    base.style.left = '0';
    base.style.top = '0';
    base.style.width = DEV_RECT.w + 'px';
    base.style.height = DEV_RECT.h + 'px';
    base.style.zIndex = DEV_Z_BASE;
    holder.appendChild(base);
  }

  function renderScene() {
    if (!state || !catalog) return;
    if (!sceneBuilt) buildSceneSkeleton();
    ['wall', 'plant', 'buddy', 'beverage', 'keyboard', 'mouse'].forEach(renderSlotSprite);
    renderChair();
    renderDev();
    var monitor = document.getElementById('sprite-monitor');
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
  var cursorOn = true;
  setInterval(function () {
    cursorOn = !cursorOn;
    var cursor = el.terminal.querySelector('.cursor');
    if (cursor) cursor.classList.toggle('off', !cursorOn || !state || state.activeState !== 'idle');
  }, 500);

  // ---------------------------------------------------------------------
  // Terminal (#terminal), ui-spec.md §3.2 / art-direction.md "screen region"
  // ---------------------------------------------------------------------
  function renderTerminal() {
    el.terminal.innerHTML = '';
    if (!state) return;
    var lines = (state.screenLines || []).slice(0, 11);
    while (lines.length < 11) lines.unshift('');
    var onBreak = state.activeState === 'onBreak';
    lines.forEach(function (text, idx) {
      var isLast = idx === lines.length - 1;
      var div = document.createElement('div');
      div.className = 'line';
      var recentCount = 2;
      var isRecent = !onBreak && (idx >= lines.length - recentCount);
      if (isRecent) div.classList.add('recent');
      var shown = isLast && onBreak ? '-- idle --' : truncate(text, 30);
      div.textContent = shown;
      if (isLast && state.activeState === 'idle') {
        var cursor = document.createElement('span');
        cursor.className = 'cursor' + (cursorOn ? '' : ' off');
        div.appendChild(cursor);
      }
      el.terminal.appendChild(div);
    });
  }

  // ---------------------------------------------------------------------
  // Titlebar / sprint panel / status panel
  // ---------------------------------------------------------------------
  function renderChrome() {
    if (!state) return;
    var moodColor = MOOD_COLOR[state.activeState] || MOOD_COLOR.idle;
    el.moodDot.style.background = moodColor;
    el.statusDot.style.background = moodColor;
    el.hudLevel.textContent = 'LV ' + state.level;
    el.hudCash.textContent = String(state.devCash);

    el.sprintName.textContent = truncate(state.sprint.name, 28);
    el.sprintBar.max = state.sprint.target;
    el.sprintBar.value = state.sprint.progress;
    el.sprintUnits.textContent = state.sprint.progress + ' / ' + state.sprint.target + ' ' + state.sprint.unitLabel;

    el.statusLine.textContent = truncate(state.activityLine, 34);

    var lis = el.ticker.querySelectorAll('li');
    for (var i = 0; i < lis.length; i++) {
      var raw = (state.tickerLines || [])[i] || '';
      lis[i].textContent = raw ? truncate('> ' + raw, 36) : '';
    }
  }

  function renderAll() {
    if (!state) return;
    renderChrome();
    renderTerminal();
    renderScene();
    if (el.store.open) {
      updateStoreCash();
      refreshGridStates();
      updatePreview();
    }
  }

  // ---------------------------------------------------------------------
  // Flash toast (server "flash" messages) — 1.5s, positioned over
  // #store-cash while the modal is open, else over #sprint-name.
  // ---------------------------------------------------------------------
  var flashTimer = null;
  function showFlash(msg) {
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
  function flashInsufficientFunds() {
    el.storeCashBox.classList.add('flash-insufficient');
    setTimeout(function () { el.storeCashBox.classList.remove('flash-insufficient'); }, 400);
  }

  // ---------------------------------------------------------------------
  // Store: categories
  // ---------------------------------------------------------------------
  function buildCats() {
    if (!catalog) return;
    el.catList.innerHTML = '';
    catalog.slots.forEach(function (slot, idx) {
      var row = document.createElement('div');
      row.className = 'cat-row';
      row.dataset.index = String(idx);
      var gutter = document.createElement('span');
      gutter.className = 'gutter';
      var label = document.createElement('span');
      label.textContent = slot.name.toUpperCase();
      var check = document.createElement('span');
      check.className = 'check';
      var eq = state && state.equipped[slot.id];
      var freeItem = freeDefaultItem(slot.id);
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
  function renderCatSelection() {
    var rows = el.catList.querySelectorAll('.cat-row');
    rows.forEach(function (row, idx) {
      var selected = idx === storeUI.catIndex;
      row.classList.toggle('selected', selected);
      row.querySelector('.gutter').textContent = selected ? '>' : '';
    });
  }
  function selectCategory(idx) {
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
  // Store: item grid / cards
  // ---------------------------------------------------------------------
  function currentSlot() { return catalog.slots[storeUI.catIndex]; }
  function currentItems() { return catalogBySlot[currentSlot().id] || []; }
  function currentItem() { return currentItems()[storeUI.cardIndex]; }

  // ui-spec.md §4.3 — the one action button, fixed precedence.
  function computeCardAction(slot, item, tintId) {
    var owned = (state.ownedItems || []).indexOf(item.id) !== -1;
    if (!owned) {
      if (state.devCash >= item.price) return { kind: 'buy', label: 'BUY ' + item.price };
      return { kind: 'insufficient', label: 'NEED ' + item.price };
    }
    if (slot.tintable) {
      if (!isTintOwned(item, tintId)) {
        var price = (tintsById[tintId] || {}).price || 40;
        if (state.devCash >= price) return { kind: 'buytint', label: 'BUY COLOUR ' + price };
        return { kind: 'insufficient', label: 'NEED ' + price };
      }
    }
    var eq = state.equipped[slot.id];
    var sameEquip = eq && eq.itemId === item.id && (!slot.tintable || eq.tintId === tintId);
    if (sameEquip) return { kind: 'none', label: '✓ EQUIPPED' };
    return { kind: 'equip', label: 'EQUIP' };
  }

  function priceStateText(slot, item, tintId) {
    var owned = (state.ownedItems || []).indexOf(item.id) !== -1;
    if (!owned) return item.price + ' ◆';
    var eq = state.equipped[slot.id];
    var sameEquip = eq && eq.itemId === item.id && (!slot.tintable || eq.tintId === tintId);
    if (sameEquip) return 'OWNED · EQUIPPED';
    return 'OWNED';
  }

  function buildGrid() {
    el.grid.innerHTML = '';
    if (!catalog || !state) return;
    var slot = currentSlot();
    currentItems().forEach(function (item, idx) {
      var card = document.createElement('div');
      card.className = 'card';
      card.dataset.index = String(idx);

      var thumb = document.createElement('div');
      thumb.className = 'thumb';
      thumb.appendChild(buildThumb(slot, item));
      card.appendChild(thumb);

      var name = document.createElement('div');
      name.className = 'name';
      name.textContent = truncate(item.name, 21);
      card.appendChild(name);

      var priceState = document.createElement('div');
      priceState.className = 'price-state';
      card.appendChild(priceState);

      if (slot.tintable) {
        var swatches = document.createElement('div');
        swatches.className = 'swatches';
        catalog.tints.forEach(function (tint) {
          var chip = document.createElement('div');
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

      var action = document.createElement('button');
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
  function buildThumb(slot, item) {
    if (slot.tintable) {
      if (!item.sprite) { return document.createElement('span'); }
      var tintId = selectedTintFor(item);
      var formFile = item.thumbForm || ('thumb_' + item.id + '_form.png');
      var detailFile = item.thumbDetail || ('thumb_' + item.id + '_detail.png');
      var wrap = document.createElement('div');
      wrap.style.position = 'absolute';
      wrap.style.inset = '0';
      var tint = buildTintLayer(formFile, tintHexFor(tintId));
      tint.style.left = '0'; tint.style.top = '0'; tint.style.width = '100%'; tint.style.height = '100%';
      wrap.appendChild(tint);
      var detail = document.createElement('img');
      detail.alt = '';
      detail.src = assetUrl(detailFile);
      detail.style.position = 'absolute';
      detail.style.inset = '0';
      detail.style.width = '100%';
      detail.style.height = '100%';
      wrap.appendChild(detail);
      return wrap;
    }
    if (!item.sprite) { return document.createElement('span'); }
    var img = document.createElement('img');
    img.alt = '';
    img.src = assetUrl(item.thumb || ('thumb_' + item.id + '.png'));
    return img;
  }

  function refreshGridStates() {
    if (!catalog || !state) return;
    var slot = currentSlot();
    var items = currentItems();
    var cards = el.grid.querySelectorAll('.card');
    cards.forEach(function (card, idx) {
      var item = items[idx];
      if (!item) return;
      var tintId = selectedTintFor(item);
      card.classList.toggle('selected', idx === storeUI.cardIndex);
      var priceState = card.querySelector('.price-state');
      if (priceState) priceState.textContent = priceStateText(slot, item, tintId);
      if (slot.tintable) {
        var chips = card.querySelectorAll('.swatch');
        chips.forEach(function (chip) {
          var tid = chip.dataset.tint;
          chip.classList.toggle('selected', tid === tintId);
          chip.classList.toggle('unowned', !isTintOwned(item, tid));
        });
        // thumbnail tint follows the selected swatch
        var tintWrap = card.querySelector('.tintable');
        if (tintWrap) {
          tintWrap.style.setProperty('--tint', tintHexFor(tintId));
        }
      }
      var action = computeCardAction(slot, item, tintId);
      var btn = card.querySelector('.action');
      if (btn) {
        btn.textContent = action.label;
        btn.classList.toggle('is-disabled', action.kind === 'insufficient' || action.kind === 'none');
      }
    });
  }

  function selectCard(idx) {
    var items = currentItems();
    idx = clamp(idx, 0, Math.max(0, items.length - 1));
    storeUI.cardIndex = idx;
    storeUI.focus = 'cards';
    refreshGridStates();
    var card = el.grid.querySelectorAll('.card')[idx];
    if (card && card.scrollIntoView) card.scrollIntoView({ block: 'nearest' });
    updatePreview();
  }

  function selectTint(item, tintId) {
    storeUI.selectedTintByItem[item.id] = tintId;
    refreshGridStates();
    updatePreview();
  }

  function runCardAction(slot, item) {
    var tintId = selectedTintFor(item);
    var action = computeCardAction(slot, item, tintId);
    switch (action.kind) {
      case 'buy':
        sendAction({ action: 'BUY_ITEM', itemId: item.id });
        break;
      case 'buytint':
        sendAction({ action: 'BUY_TINT', itemId: item.id, tintId: tintId });
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

  function updateScrollThumb() {
    var trackH = 212;
    var sh = el.grid.scrollHeight, ch = el.grid.clientHeight, st = el.grid.scrollTop;
    if (sh <= ch) {
      el.scrollThumb.style.height = trackH + 'px';
      el.scrollThumb.style.top = '0px';
      return;
    }
    var top = (st / sh) * trackH;
    var h = Math.max(8, (ch / sh) * trackH);
    el.scrollThumb.style.top = top + 'px';
    el.scrollThumb.style.height = h + 'px';
  }
  el.grid.addEventListener('scroll', updateScrollThumb);

  // ---------------------------------------------------------------------
  // Store: preview pane (ui-spec.md §4.2)
  // ---------------------------------------------------------------------
  function updatePreview() {
    if (!catalog || !state) return;
    var slot = currentSlot();
    var item = currentItem();
    el.previewViewport.innerHTML = '';
    if (!item) return;
    var tintId = selectedTintFor(item);

    if (slot.id === 'hoodie' || slot.id === 'chair') {
      renderComposedPreview(slot.id, item, tintId);
    } else if (!item.sprite) {
      var nothing = document.createElement('div');
      nothing.className = 'nothing';
      nothing.textContent = 'NOTHING';
      el.previewViewport.appendChild(nothing);
    } else {
      var rect = SLOT_RECT[slot.id];
      var scale = Math.max(1, Math.min(3, Math.floor(Math.min(152 / rect.w, 152 / rect.h))));
      var img = document.createElement('img');
      img.className = 'scene';
      img.alt = '';
      img.src = assetUrl(item.sprite);
      img.style.width = (rect.w * scale) + 'px';
      img.style.height = (rect.h * scale) + 'px';
      img.style.left = Math.round((152 - rect.w * scale) / 2) + 'px';
      img.style.top = Math.round((152 - rect.h * scale) / 2) + 'px';
      el.previewViewport.appendChild(img);
    }

    el.previewName.textContent = truncate(item.name, 20);
    var owned = (state.ownedItems || []).indexOf(item.id) !== -1;
    var eq = state.equipped[slot.id];
    var sameEquip = eq && eq.itemId === item.id && (!slot.tintable || eq.tintId === tintId);
    el.previewState.textContent = !owned ? (item.price + ' ◆') : (sameEquip ? 'EQUIPPED' : 'OWNED');
    el.previewColor.textContent = slot.tintable ? truncate((tintsById[tintId] || {}).name || '', 24) : '';
  }

  // hoodie/chair composed mini-scene at 1x, centred in the 152x152 viewport
  // (ui-spec.md §4.2): the previewed slot uses the selected item+tint, every
  // other one of {hoodie, chair} uses whatever is currently equipped.
  function renderComposedPreview(previewSlotId, previewItem, previewTintId) {
    var hoodieEq = state.equipped.hoodie;
    var chairEq = state.equipped.chair;
    var hoodieItem = previewSlotId === 'hoodie' ? previewItem : itemsById[hoodieEq.itemId];
    var hoodieTint = previewSlotId === 'hoodie' ? previewTintId : (hoodieEq.tintId || hoodieItem.defaultTint);
    var chairItem = previewSlotId === 'chair' ? previewItem : itemsById[chairEq.itemId];
    var chairTint = previewSlotId === 'chair' ? previewTintId : (chairEq.tintId || chairItem.defaultTint);

    var chairRect = CHAIR_RECT[chairItem.id] || CHAIR_RECT.chair_basic;
    var devRect = DEV_RECT;
    var bboxLeft = Math.min(devRect.left, chairRect.left);
    var bboxTop = Math.min(devRect.top, chairRect.top);
    var bboxRight = Math.max(devRect.left + devRect.w, chairRect.left + chairRect.w);
    var bboxBottom = Math.max(devRect.top + devRect.h, chairRect.top + chairRect.h);
    var bboxW = bboxRight - bboxLeft, bboxH = bboxBottom - bboxTop;
    var originLeft = Math.round((152 - bboxW) / 2);
    var originTop = Math.round((152 - bboxH) / 2);

    var root = document.createElement('div');
    root.style.position = 'absolute';
    root.style.left = originLeft + 'px';
    root.style.top = originTop + 'px';
    root.style.width = bboxW + 'px';
    root.style.height = bboxH + 'px';
    el.previewViewport.appendChild(root);

    var chairHolder = document.createElement('div');
    chairHolder.style.position = 'absolute';
    positionEl(chairHolder, { left: chairRect.left - bboxLeft, top: chairRect.top - bboxTop, w: chairRect.w, h: chairRect.h });
    var chairTintLayer = buildTintLayer(chairItem.sprite, tintHexFor(chairTint));
    positionEl(chairTintLayer, { left: 0, top: 0, w: chairRect.w, h: chairRect.h });
    chairTintLayer.style.zIndex = 1;
    chairHolder.appendChild(chairTintLayer);
    if (chairItem.detail) {
      var chairDetail = plainImg(chairItem.detail, { left: 0, top: 0, w: chairRect.w, h: chairRect.h });
      chairDetail.style.zIndex = 2;
      chairHolder.appendChild(chairDetail);
    }
    root.appendChild(chairHolder);

    var devHolder = document.createElement('div');
    devHolder.style.position = 'absolute';
    positionEl(devHolder, { left: devRect.left - bboxLeft, top: devRect.top - bboxTop, w: devRect.w, h: devRect.h });
    var frame = currentDevFrame();
    var formLayer = buildTintLayer('dev_form_' + frame + '.png', tintHexFor(hoodieTint));
    positionEl(formLayer, { left: 0, top: 0, w: devRect.w, h: devRect.h });
    formLayer.style.zIndex = 3;
    devHolder.appendChild(formLayer);
    if (hoodieItem) {
      var styleImg = plainImg(hoodieItem.sprite, { left: 0, top: 0, w: devRect.w, h: devRect.h });
      styleImg.style.zIndex = 4;
      devHolder.appendChild(styleImg);
    }
    var baseImg = plainImg('dev_base_' + frame + '.png', { left: 0, top: 0, w: devRect.w, h: devRect.h });
    baseImg.style.zIndex = 5;
    devHolder.appendChild(baseImg);
    root.appendChild(devHolder);
  }

  // ---------------------------------------------------------------------
  // Store open/close (ui-spec.md §4.5, §5.3 — STORE_OPEN/STORE_CLOSE)
  // ---------------------------------------------------------------------
  function ensureStoreDefaults() {
    if (storeUI.initialized || !catalog || !state) return;
    var hoodieIdx = catalog.slots.findIndex(function (s) { return s.id === 'hoodie'; });
    storeUI.catIndex = hoodieIdx === -1 ? 0 : hoodieIdx;
    var items = catalogBySlot.hoodie || [];
    var eq = state.equipped.hoodie;
    var idx = 0;
    for (var i = 0; i < items.length; i++) if (eq && items[i].id === eq.itemId) { idx = i; break; }
    storeUI.cardIndex = idx;
    storeUI.focus = 'cards';
    storeUI.initialized = true;
  }

  function openStore() {
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
  function closeStore() {
    if (!el.store.open) return;
    el.store.close(); // fires 'close' below regardless of trigger (X / S / Tab / Esc)
  }
  el.store.addEventListener('close', function () {
    el.scrim.classList.remove('visible');
    sendAction({ action: 'STORE_CLOSE' });
  });
  el.storeOpenBtn.addEventListener('click', openStore);
  el.storeClose.addEventListener('click', closeStore);

  function updateStoreCash() {
    if (!state) return;
    el.storeCash.textContent = String(state.devCash);
  }

  // ---------------------------------------------------------------------
  // Input — keyboard (ui-spec.md §5.2)
  // ---------------------------------------------------------------------
  function moveSelection(delta) {
    if (storeUI.focus === 'cats') {
      selectCategory(storeUI.catIndex + delta);
    } else {
      selectCard(storeUI.cardIndex + delta);
    }
  }
  function selectSwatchByIndex(idx) {
    var slot = currentSlot(), item = currentItem();
    if (!slot.tintable || !item) return;
    var tint = catalog.tints[idx];
    if (!tint) return;
    selectTint(item, tint.id);
  }
  function cycleSwatch(dir) {
    var slot = currentSlot(), item = currentItem();
    if (!slot.tintable || !item) return;
    var tints = catalog.tints;
    var cur = selectedTintFor(item);
    var idx = tints.findIndex(function (t) { return t.id === cur; });
    idx = (idx + dir + tints.length) % tints.length;
    selectTint(item, tints[idx].id);
  }
  function runSelectedCardAction() {
    var slot = currentSlot(), item = currentItem();
    if (!item) return;
    runCardAction(slot, item);
  }

  document.addEventListener('keydown', function (e) {
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
    } else {
      if (e.key === 's' || e.key === 'S') { e.preventDefault(); openStore(); }
      else if (e.key === 'Tab') { e.preventDefault(); openStore(); }
    }
  });

  // ---------------------------------------------------------------------
  // Connection status overlay
  // ---------------------------------------------------------------------
  var attempt = 0;
  function showConnOverlay(reconnecting) {
    el.connOverlay.querySelector('span').textContent = reconnecting ? 'RECONNECTING...' : 'CONNECTING...';
    el.connOverlay.classList.add('visible');
  }
  function hideConnOverlay() { el.connOverlay.classList.remove('visible'); }

  // ---------------------------------------------------------------------
  // WebSocket (ui-spec.md §6) — camelCase wire contract, backoff retry.
  // ---------------------------------------------------------------------
  var ws = null;
  var reconnectDelay = 500;
  var reconnectTimer = null;

  function sendAction(action) {
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

  function handleServerMessage(msg) {
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
        break;
      case 'flash':
        showFlash(msg);
        break;
      default:
        break;
    }
  }

  function connectWS() {
    showConnOverlay(attempt > 0);
    attempt++;
    var proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    try {
      ws = new WebSocket(proto + '//' + location.host + '/ws');
    } catch (e) {
      scheduleReconnect();
      return;
    }
    ws.addEventListener('open', function () { reconnectDelay = 500; });
    ws.addEventListener('message', function (ev) {
      try { handleServerMessage(JSON.parse(ev.data)); }
      catch (err) { console.error('bad ws message', err); }
    });
    ws.addEventListener('close', scheduleReconnect);
    ws.addEventListener('error', function () { /* 'close' follows */ });
  }
  function scheduleReconnect() {
    showConnOverlay(true);
    clearTimeout(reconnectTimer);
    reconnectTimer = setTimeout(connectWS, reconnectDelay);
    reconnectDelay = Math.min(reconnectDelay * 2, 8000);
  }

  // ---------------------------------------------------------------------
  // Boot
  // ---------------------------------------------------------------------
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
    window.devApply = function (partialState) {
      state = Object.assign({}, state, partialState || {});
      renderAll();
    };
    window.devCatalog = DEV_CATALOG;
  } else {
    connectWS();
  }
})();
