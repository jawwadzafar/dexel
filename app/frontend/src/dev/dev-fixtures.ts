// Dev-mode hardcoded catalog + state (docs/upgrade-design.md values).
// Only used behind ?dev=1 (see ./dev-tools.ts) — never loaded in normal
// operation. Pure data, no runtime logic.
import type { CatalogMessage, StateMessage } from '../wire';

export const DEV_CATALOG: CatalogMessage = {
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

export const DEV_STATE: StateMessage = {
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
