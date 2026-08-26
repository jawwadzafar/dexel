// Dev-mode hardcoded catalog + state (docs/upgrade-design.md values).
// Only used behind ?dev=1 (see ./dev-tools.ts) — never loaded in normal
// operation. Pure data, no runtime logic.
import type { CatalogMessage, DayStat, SessionsView, SessionView, StateMessage, StreakView } from '../wire';

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

// Analytics track Phase A3 (docs/plan/A3-design.md §7 Task TS-1, §8's
// visual gate) — a hand-authored 30-day dense history (oldest first,
// today last, matching the server's own dense-wire contract, §5) so
// `?dev=1` renders a fully populated [H] HISTORY modal without a live
// server. Deliberately varied rather than a flat ramp, so every rendered
// element has something real to show:
//   - a 12-day active run (07-24..08-04) that becomes the LONGEST streak,
//   - a 2-day gap (08-05/08-06) of honest all-zero entries — exactly what
//     the server emits for a day the process never ran (§3.2/§5),
//   - 8 isolated single-active-days (08-07..08-14, alternating with more
//     zero gaps) that never chain into a streak longer than 1 each,
//   - an 8-day active run ending TODAY (08-15..08-22) that becomes the
//     CURRENT streak (8 < the 12-day longest, so "longest" still shows
//     the historical record — exercises A3-design.md §2.4's "current
//     never exceeds longest" invariant),
//   - one clear busiest day (08-18, activeSeconds 14400 — far above every
//     other entry) and a separately-clear longest-focus-block day
//     (08-20, longestFocusBlockSeconds 4800) so the two insight lines
//     resolve to two different dates, and
//   - a final (today) entry whose today/live counters match this
//     fixture's own `stats.today` block above (610s active etc.) — the
//     dense array's last entry IS "today, live" per §5, so it stays
//     consistent with the Activity modal's own numbers instead of
//     contradicting them.
const DEV_HISTORY: DayStat[] = [
  { date: '2026-07-24', keystrokes: 1200, mouseActiveSeconds: 240, activeSeconds: 3200, idleSeconds: 1800, sprintsCompleted: 1, focusSessions: 2, appSwitches: 3, coinsEarned: 38, isActive: true, longestFocusBlockSeconds: 900 },
  { date: '2026-07-25', keystrokes: 1600, mouseActiveSeconds: 300, activeSeconds: 4100, idleSeconds: 2200, sprintsCompleted: 1, focusSessions: 2, appSwitches: 2, coinsEarned: 45, isActive: true, longestFocusBlockSeconds: 1100 },
  { date: '2026-07-26', keystrokes: 950, mouseActiveSeconds: 180, activeSeconds: 2600, idleSeconds: 1500, sprintsCompleted: 0, focusSessions: 1, appSwitches: 1, coinsEarned: 22, isActive: true, longestFocusBlockSeconds: 700 },
  { date: '2026-07-27', keystrokes: 2100, mouseActiveSeconds: 420, activeSeconds: 5200, idleSeconds: 2600, sprintsCompleted: 2, focusSessions: 3, appSwitches: 4, coinsEarned: 58, isActive: true, longestFocusBlockSeconds: 1400 },
  { date: '2026-07-28', keystrokes: 1500, mouseActiveSeconds: 260, activeSeconds: 3900, idleSeconds: 2000, sprintsCompleted: 1, focusSessions: 2, appSwitches: 2, coinsEarned: 40, isActive: true, longestFocusBlockSeconds: 1000 },
  { date: '2026-07-29', keystrokes: 220, mouseActiveSeconds: 60, activeSeconds: 600, idleSeconds: 300, sprintsCompleted: 0, focusSessions: 0, appSwitches: 0, coinsEarned: 5, isActive: true, longestFocusBlockSeconds: 0 },
  { date: '2026-07-30', keystrokes: 1800, mouseActiveSeconds: 360, activeSeconds: 4700, idleSeconds: 2400, sprintsCompleted: 2, focusSessions: 3, appSwitches: 3, coinsEarned: 52, isActive: true, longestFocusBlockSeconds: 1250 },
  { date: '2026-07-31', keystrokes: 1250, mouseActiveSeconds: 220, activeSeconds: 3300, idleSeconds: 1700, sprintsCompleted: 1, focusSessions: 2, appSwitches: 1, coinsEarned: 35, isActive: true, longestFocusBlockSeconds: 850 },
  { date: '2026-08-01', keystrokes: 780, mouseActiveSeconds: 140, activeSeconds: 2100, idleSeconds: 1100, sprintsCompleted: 0, focusSessions: 1, appSwitches: 0, coinsEarned: 18, isActive: true, longestFocusBlockSeconds: 600 },
  { date: '2026-08-02', keystrokes: 2400, mouseActiveSeconds: 480, activeSeconds: 6000, idleSeconds: 3000, sprintsCompleted: 2, focusSessions: 4, appSwitches: 5, coinsEarned: 68, isActive: true, longestFocusBlockSeconds: 1600 },
  { date: '2026-08-03', keystrokes: 1700, mouseActiveSeconds: 310, activeSeconds: 4400, idleSeconds: 2200, sprintsCompleted: 1, focusSessions: 2, appSwitches: 2, coinsEarned: 47, isActive: true, longestFocusBlockSeconds: 1150 },
  { date: '2026-08-04', keystrokes: 1150, mouseActiveSeconds: 200, activeSeconds: 3100, idleSeconds: 1600, sprintsCompleted: 1, focusSessions: 2, appSwitches: 1, coinsEarned: 33, isActive: true, longestFocusBlockSeconds: 800 },
  // Gap: two honest all-zero days (process never ran) — breaks the streak.
  { date: '2026-08-05', keystrokes: 0, mouseActiveSeconds: 0, activeSeconds: 0, idleSeconds: 0, sprintsCompleted: 0, focusSessions: 0, appSwitches: 0, coinsEarned: 0, isActive: false, longestFocusBlockSeconds: 0 },
  { date: '2026-08-06', keystrokes: 0, mouseActiveSeconds: 0, activeSeconds: 0, idleSeconds: 0, sprintsCompleted: 0, focusSessions: 0, appSwitches: 0, coinsEarned: 0, isActive: false, longestFocusBlockSeconds: 0 },
  // Scattered isolated active days, each preceded/followed by a zero gap
  // day — none of these chain into a run longer than 1.
  { date: '2026-08-07', keystrokes: 650, mouseActiveSeconds: 120, activeSeconds: 1800, idleSeconds: 900, sprintsCompleted: 0, focusSessions: 1, appSwitches: 1, coinsEarned: 18, isActive: true, longestFocusBlockSeconds: 500 },
  { date: '2026-08-08', keystrokes: 0, mouseActiveSeconds: 0, activeSeconds: 0, idleSeconds: 0, sprintsCompleted: 0, focusSessions: 0, appSwitches: 0, coinsEarned: 0, isActive: false, longestFocusBlockSeconds: 0 },
  { date: '2026-08-09', keystrokes: 900, mouseActiveSeconds: 160, activeSeconds: 2400, idleSeconds: 1300, sprintsCompleted: 1, focusSessions: 1, appSwitches: 0, coinsEarned: 24, isActive: true, longestFocusBlockSeconds: 650 },
  { date: '2026-08-10', keystrokes: 0, mouseActiveSeconds: 0, activeSeconds: 0, idleSeconds: 0, sprintsCompleted: 0, focusSessions: 0, appSwitches: 0, coinsEarned: 0, isActive: false, longestFocusBlockSeconds: 0 },
  { date: '2026-08-11', keystrokes: 520, mouseActiveSeconds: 90, activeSeconds: 1500, idleSeconds: 800, sprintsCompleted: 0, focusSessions: 1, appSwitches: 1, coinsEarned: 14, isActive: true, longestFocusBlockSeconds: 400 },
  { date: '2026-08-12', keystrokes: 0, mouseActiveSeconds: 0, activeSeconds: 0, idleSeconds: 0, sprintsCompleted: 0, focusSessions: 0, appSwitches: 0, coinsEarned: 0, isActive: false, longestFocusBlockSeconds: 0 },
  { date: '2026-08-13', keystrokes: 1400, mouseActiveSeconds: 260, activeSeconds: 3600, idleSeconds: 1900, sprintsCompleted: 1, focusSessions: 2, appSwitches: 2, coinsEarned: 38, isActive: true, longestFocusBlockSeconds: 950 },
  { date: '2026-08-14', keystrokes: 0, mouseActiveSeconds: 0, activeSeconds: 0, idleSeconds: 0, sprintsCompleted: 0, focusSessions: 0, appSwitches: 0, coinsEarned: 0, isActive: false, longestFocusBlockSeconds: 0 },
  // Current 8-day active run, ending today — the CURRENT streak. 08-18
  // is the clear busiest day (activeSeconds far above every other entry);
  // 08-20 is the clear longest-focus-block day — two different dates, so
  // the modal's two insight lines don't coincidentally read identically.
  { date: '2026-08-15', keystrokes: 1650, mouseActiveSeconds: 300, activeSeconds: 4200, idleSeconds: 2100, sprintsCompleted: 1, focusSessions: 3, appSwitches: 2, coinsEarned: 46, isActive: true, longestFocusBlockSeconds: 1200 },
  { date: '2026-08-16', keystrokes: 1450, mouseActiveSeconds: 280, activeSeconds: 3800, idleSeconds: 1950, sprintsCompleted: 1, focusSessions: 2, appSwitches: 1, coinsEarned: 41, isActive: true, longestFocusBlockSeconds: 1050 },
  { date: '2026-08-17', keystrokes: 2000, mouseActiveSeconds: 380, activeSeconds: 5100, idleSeconds: 2600, sprintsCompleted: 2, focusSessions: 3, appSwitches: 3, coinsEarned: 56, isActive: true, longestFocusBlockSeconds: 1500 },
  { date: '2026-08-18', keystrokes: 5200, mouseActiveSeconds: 1100, activeSeconds: 14400, idleSeconds: 3200, sprintsCompleted: 4, focusSessions: 6, appSwitches: 5, coinsEarned: 142, isActive: true, longestFocusBlockSeconds: 3600 },
  { date: '2026-08-19', keystrokes: 1750, mouseActiveSeconds: 330, activeSeconds: 4600, idleSeconds: 2300, sprintsCompleted: 2, focusSessions: 3, appSwitches: 2, coinsEarned: 50, isActive: true, longestFocusBlockSeconds: 1300 },
  { date: '2026-08-20', keystrokes: 2050, mouseActiveSeconds: 400, activeSeconds: 5400, idleSeconds: 2700, sprintsCompleted: 2, focusSessions: 4, appSwitches: 3, coinsEarned: 59, isActive: true, longestFocusBlockSeconds: 4800 },
  { date: '2026-08-21', keystrokes: 1500, mouseActiveSeconds: 280, activeSeconds: 3900, idleSeconds: 2000, sprintsCompleted: 1, focusSessions: 2, appSwitches: 1, coinsEarned: 42, isActive: true, longestFocusBlockSeconds: 1100 },
  // Today — live, deliberately matching `stats.today` below (§5: the
  // dense array's final entry IS today's running totals, not a separate
  // number) rather than an independently-invented value.
  { date: '2026-08-22', keystrokes: 842, mouseActiveSeconds: 96, activeSeconds: 610, idleSeconds: 340, sprintsCompleted: 1, focusSessions: 2, appSwitches: 1, coinsEarned: 18, isActive: true, longestFocusBlockSeconds: 420 }
];

// current=8 (the run ending today, 08-15..08-22) < longest=12 (the
// 07-24..08-04 run) — exercises the "current never exceeds/overwrites a
// historical longest" case (A3-design.md §2.4) instead of the easier
// case where the two numbers are equal.
const DEV_STREAK: StreakView = { current: 8, longest: 12 };

// Phase P2 (docs/plan/P2-design.md §6.5) — 10 finished sessions spanning
// two weeks (08-09..08-21, "today" being 08-22 per DEV_HISTORY above),
// newest last for readability here but SENT/consumed newest-first (this
// array is reversed below). Mixes all three `endReason` values, one
// unnamed session, and one whose coinsEarned is 0 — per §6.5's checklist.
// ids 17..26 make 26 the lifetime `completed` count, so the in-progress
// DEV_STATE.sessions.active below (id 27 — "the ordinal it WILL have",
// §2.2) lines up honestly with it.
const DEV_SESSIONS_RECENT_OLDEST_FIRST: SessionView[] = [
  { id: 17, name: 'onboarding polish', startedAt: '2026-08-09T13:00:00Z', endedAt: '2026-08-09T14:30:00Z', durationSeconds: 5400, keystrokes: 2200, mouseActiveSeconds: 300, activeSeconds: 4800, idleSeconds: 600, sprintsCompleted: 2, focusSessions: 3, appSwitches: 2, coinsEarned: 45, longestFocusBlockSeconds: 1800, pausedSeconds: 0, endReason: 'user' },
  // The one unnamed session (§6.5) — legal, per §2.2's "unnamed is a
  // first-class state" — and the one whose coinsEarned is 0.
  { id: 18, name: '', startedAt: '2026-08-10T09:00:00Z', endedAt: '2026-08-10T09:30:00Z', durationSeconds: 1800, keystrokes: 600, mouseActiveSeconds: 80, activeSeconds: 1500, idleSeconds: 300, sprintsCompleted: 0, focusSessions: 1, appSwitches: 0, coinsEarned: 0, longestFocusBlockSeconds: 900, pausedSeconds: 0, endReason: 'user' },
  { id: 19, name: 'bug bash', startedAt: '2026-08-11T10:00:00Z', endedAt: '2026-08-11T12:33:20Z', durationSeconds: 9200, keystrokes: 3400, mouseActiveSeconds: 500, activeSeconds: 8000, idleSeconds: 900, sprintsCompleted: 3, focusSessions: 4, appSwitches: 5, coinsEarned: 70, longestFocusBlockSeconds: 2400, pausedSeconds: 0, endReason: 'idle' },
  { id: 20, name: 'docs pass', startedAt: '2026-08-13T15:00:00Z', endedAt: '2026-08-13T16:00:00Z', durationSeconds: 3600, keystrokes: 1500, mouseActiveSeconds: 200, activeSeconds: 3000, idleSeconds: 400, sprintsCompleted: 1, focusSessions: 2, appSwitches: 1, coinsEarned: 30, longestFocusBlockSeconds: 1200, pausedSeconds: 0, endReason: 'user' },
  { id: 21, name: 'perf tuning', startedAt: '2026-08-14T11:00:00Z', endedAt: '2026-08-14T13:00:00Z', durationSeconds: 7200, keystrokes: 2800, mouseActiveSeconds: 420, activeSeconds: 6500, idleSeconds: 500, sprintsCompleted: 2, focusSessions: 3, appSwitches: 2, coinsEarned: 58, longestFocusBlockSeconds: 2100, pausedSeconds: 0, endReason: 'user' },
  // The 16h hard cap (game.SessionMaxDurationSeconds, §2.6) — an honest
  // overnight-watch session with little activity, which is why it is
  // also this fixture's `longestSessionSeconds` below (duration is real
  // wall-clock time, never a proxy for effort).
  { id: 22, name: 'overnight build watch', startedAt: '2026-08-16T02:00:00Z', endedAt: '2026-08-16T18:00:00Z', durationSeconds: 57600, keystrokes: 400, mouseActiveSeconds: 60, activeSeconds: 900, idleSeconds: 200, sprintsCompleted: 0, focusSessions: 0, appSwitches: 0, coinsEarned: 6, longestFocusBlockSeconds: 0, pausedSeconds: 0, endReason: 'maxDuration' },
  { id: 23, name: 'release cut', startedAt: '2026-08-17T09:00:00Z', endedAt: '2026-08-17T10:15:00Z', durationSeconds: 4500, keystrokes: 1800, mouseActiveSeconds: 260, activeSeconds: 3900, idleSeconds: 300, sprintsCompleted: 1, focusSessions: 2, appSwitches: 1, coinsEarned: 34, longestFocusBlockSeconds: 1500, pausedSeconds: 0, endReason: 'user' },
  { id: 24, name: 'auth refactor pt2', startedAt: '2026-08-19T10:00:00Z', endedAt: '2026-08-19T11:42:00Z', durationSeconds: 6120, keystrokes: 3100, mouseActiveSeconds: 340, activeSeconds: 5400, idleSeconds: 500, sprintsCompleted: 2, focusSessions: 3, appSwitches: 3, coinsEarned: 52, longestFocusBlockSeconds: 1320, pausedSeconds: 0, endReason: 'user' },
  { id: 25, name: 'flaky test hunt', startedAt: '2026-08-20T14:00:00Z', endedAt: '2026-08-20T14:45:00Z', durationSeconds: 2700, keystrokes: 1100, mouseActiveSeconds: 160, activeSeconds: 2300, idleSeconds: 300, sprintsCompleted: 1, focusSessions: 1, appSwitches: 1, coinsEarned: 20, longestFocusBlockSeconds: 1100, pausedSeconds: 0, endReason: 'user' },
  { id: 26, name: 'changelog + release notes', startedAt: '2026-08-21T16:00:00Z', endedAt: '2026-08-21T16:25:00Z', durationSeconds: 1500, keystrokes: 500, mouseActiveSeconds: 70, activeSeconds: 1200, idleSeconds: 200, sprintsCompleted: 0, focusSessions: 1, appSwitches: 0, coinsEarned: 9, longestFocusBlockSeconds: 600, pausedSeconds: 0, endReason: 'idle' }
];
// The wire sends `recent` newest-first (§6.1) — reverse the readable
// oldest-first list above once, here, rather than writing it backwards.
const DEV_SESSIONS_RECENT: SessionView[] = DEV_SESSIONS_RECENT_OLDEST_FIRST.slice().reverse();

// Active session mid-flight (§6.5 item 1): a non-round elapsed
// (4931s = 1h 22m 11s), a name long enough to exercise truncation, and
// counters that stay <= DEV_STATE.stats.today below (activeSeconds 480 <=
// 610, idleSeconds 200 <= 340, keystrokes 620 <= 842).
const DEV_ACTIVE_SESSION = {
  id: 27,
  name: 'Refactor Auth Engine And Session Persistence Layer',
  startedAt: '2026-08-22T13:20:00Z',
  elapsedSeconds: 4931,
  keystrokes: 620,
  mouseActiveSeconds: 70,
  activeSeconds: 480,
  idleSeconds: 200,
  sprintsCompleted: 0,
  focusSessions: 2,
  appSwitches: 1,
  coinsEarned: 14,
  longestFocusBlockSeconds: 380,
  // PR-5 (docs/production-runtime/MIGRATION_PLAN.md §PR-5) — this
  // session predates the pause feature in this fixture's own timeline, so
  // an honest 0 rather than an invented paused stretch.
  pausedSeconds: 0
};

const DEV_SESSIONS: SessionsView = {
  active: DEV_ACTIVE_SESSION,
  // completed=26 (this fixture's 10-entry window is a truncated tail of
  // a longer real log, per §5.6's "unbounded rows, a windowed UI").
  // thisWeek=5: of the 10 entries above, those ending 08-16..08-21 fall
  // inside the last SessionsWeekDays(7) local dates including "today"
  // 08-22 (08-16,08-17,08-19,08-20,08-21).
  summary: { completed: 26, thisWeek: 5, longestSessionSeconds: 57600 },
  recent: DEV_SESSIONS_RECENT
};

// Phase P2 (docs/plan/P2-design.md §3.2) — the summary card's own mockup
// numbers verbatim ("auth refactor", "1h 24m", "4,182 keys", "3 blocks
// BEST 14m", "2 finished", "+18 earned"), so a screenshot of
// window.devSessionComplete() matches the design doc's own worked
// example exactly. id 27 lines up with DEV_SESSIONS.active above (the
// ordinal this in-progress session would get if it ended now).
export const DEV_SESSION_COMPLETE_SAMPLE: SessionView = {
  id: 27,
  name: 'auth refactor',
  startedAt: '2026-08-22T12:00:00Z',
  endedAt: '2026-08-22T13:24:00Z',
  durationSeconds: 5040,
  keystrokes: 4182,
  mouseActiveSeconds: 380,
  activeSeconds: 4700,
  idleSeconds: 340,
  sprintsCompleted: 2,
  focusSessions: 3,
  appSwitches: 2,
  coinsEarned: 18,
  longestFocusBlockSeconds: 840,
  pausedSeconds: 0,
  endReason: 'user'
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
    lifetime: { keystrokes: 58120, mouseActiveSeconds: 7400, activeSeconds: 42300, idleSeconds: 19800, sprintsCompleted: 37 },
    history: DEV_HISTORY,
    streak: DEV_STREAK
  },
  // Phase P1 (docs/ui-spec.md §7). The DEFAULT ?dev=1 fixture is a
  // RETURNING, already-named player — the same "seeded, well-populated"
  // spirit as everything else here — so the onboarding modal does NOT
  // ambush every dev-mode screenshot of the main screen. It also means
  // the fixture exercises the personal status line (ui-spec §7.4 —
  // "Pixel is …") and the Settings CURRENTLY line out of the box.
  //
  // To see the modal itself without a backend:
  //   window.devApply({ onboarding: true, config: { name: '' } })
  // and to dismiss it again:
  //   window.devApply({ onboarding: false, config: { name: 'Pixel' } })
  // Note that ?dev=1 has no server, so ws-client.sendAction only logs —
  // SAY HELLO/SKIP will not close the modal in dev mode (the close is
  // driven by the server's next state, by design). Use devApply for that.
  config: { name: 'Pixel' },
  onboarding: false,
  sessions: DEV_SESSIONS
};

// DEV_STATE_ONBOARDING is the fresh-install fixture: unnamed, onboarding
// up, and — importantly — the free tier-0 loadout with only the hoodie's
// own default tint owned, which is what a real first launch actually looks
// like. Not applied by default; hand it to window.devApply to render the
// modal exactly as a new player sees it:
//   window.devApply(window.devStateOnboarding)
export const DEV_STATE_ONBOARDING: Partial<StateMessage> = {
  devCash: 0,
  level: 1,
  xp: 0,
  sprint: { index: 0, name: 'Set Up Environment', progress: 0, target: 40, unitLabel: 'units' },
  equipped: {
    hoodie: { itemId: 'hoodie_classic', tintId: 'indigo' },
    chair: { itemId: 'chair_basic', tintId: 'slate' },
    keyboard: { itemId: 'kb_membrane', tintId: null },
    mouse: { itemId: 'mouse_stock', tintId: null },
    beverage: { itemId: 'bev_mug', tintId: null },
    plant: { itemId: 'plant_none', tintId: null },
    wall: { itemId: 'wall_bare', tintId: null },
    buddy: { itemId: 'buddy_none', tintId: null }
  },
  ownedItems: [
    'hoodie_classic', 'chair_basic', 'kb_membrane', 'mouse_stock',
    'bev_mug', 'plant_none', 'wall_bare', 'buddy_none'
  ],
  ownedTints: [],
  config: { name: '' },
  onboarding: true
};

// DEV_STATE_NO_SESSION (§6.5 item 2) is the idle-view fixture: no active
// session and an empty log — the same "honest empty log" §5.4 accepts
// from a real pre-P2-save migration. Hand it to window.devApply to see
// the Sessions modal's clean empty state without a backend:
//   window.devApply(window.devStateNoSession)
export const DEV_STATE_NO_SESSION: Partial<StateMessage> = {
  sessions: { active: null, summary: { completed: 0, thisWeek: 0, longestSessionSeconds: 0 }, recent: [] }
};

// DEV_STATE_PAUSED — PR-5 (docs/production-runtime/MIGRATION_PLAN.md
// §PR-5) fixture: tracking is stopped. `activeState` stays 'idle' — the
// honest value while no keystroke/mouse ticks are landing — never a
// fourth mood string; pausedness is conveyed only via `paused: true`
// (ADR 0010). `stats.today`/`stats.lifetime` spread the default fixture's
// blocks and add a non-zero `pausedSeconds`, rather than replacing the
// whole `stats` block wholesale, so this fixture's history/streak stay
// intact for a devApply() layered on top of the default DEV_STATE. Hand
// it to window.devApply to see the PAUSED chrome without a backend:
//   window.devApply(window.devStatePaused)
export const DEV_STATE_PAUSED: Partial<StateMessage> = {
  paused: true,
  activeState: 'idle',
  stats: {
    ...DEV_STATE.stats!,
    today: { ...DEV_STATE.stats!.today, pausedSeconds: 420 },
    lifetime: { ...DEV_STATE.stats!.lifetime, pausedSeconds: 9600 }
  }
};
