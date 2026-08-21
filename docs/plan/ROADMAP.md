# dev-companion roadmap

The overseer executes this autonomously, phase by phase, without asking the
user what to build. The user supplies roadmaps; the overseer plans, delegates
to subagents, gates in-game, commits, and ships versioned releases. Each
phase is independently shippable and must not regress a prior one.

Ground rules that hold across every phase (do not re-decide):
- **Privacy is absolute** (ADR 0002/0009): every signal is a COUNT or a
  DURATION. Never content. "Copy/paste tracking" means detecting that a
  copy/paste *chord happened* (a counter++), NEVER the clipboard, the text,
  the file, or the app's document. A field that could hold content fails the
  structural test and does not ship.
- **Honest mechanics** (ADR 0010): earning reflects real work; anti-mash
  holds; blind sources never fabricate.
- **In-game gate**: no visual/UX change ships without the overseer rendering
  the real product and judging it — isolated mockups have lied to us twice.
- **Stack**: Go + HTML/NES.css (`app/`), ADR 0011. Rust frozen legacy.

---

## Shipped
- **v1.0.0** — core loop: activity -> sprints -> Dev Cash -> store modal ->
  buy/equip -> the character visibly changes. Honest moods, privacy,
  behind-view scene, loud failures. (tag `v1.0.0`)
- **art track — PARKED at current fidelity** (user decision 2026-08-21).
  The desired reference (`/home/darkmirror/transfer/dev_companion_desired_fidelity.png`)
  is an isometric, rich-palette, hand-crafted illustration our
  procedural / 18-colour / behind-view approach structurally cannot reach
  (palette lock, perspective, organic hand/fold/glass detail). Rather than
  chase a ceiling, we keep the current cozy look and ship depth. The
  in-flight dithering rollout is allowed to FINISH (it makes the *current*
  style more consistent — not a chase) and is the last art change until a
  deliberate future art pivot. When we revisit: the honest routes are
  user-generated sprites + an integration pipeline, or CC0 iso packs, or a
  bigger-palette iso generator rebuild (see the parked options in the log).

---

## Analytics track — "your workflow, as a game"

The game doubles as a private, local workflow tracker. Signals are counted,
priced, and paid out as Dev Cash; the player can see their own analytics.
Three phases, each shippable.

### Phase A1 (v1.1) — Activity log foundation
- Backend records per-signal DAILY counters (today + lifetime), persisted
  alongside the save: keystrokes, mouse-active seconds, active minutes,
  idle minutes, sprints completed. (Signals we ALREADY capture — no new
  provider work; this is aggregation + persistence + a read-only view.)
- New "Activity" modal (same modal pattern as the store, opened from the
  title bar / a key): today's counts + lifetime totals, plain NES.css.
- No new earning yet — establishes the data model + the second modal.
- Exit: modal shows real accumulating counts; counters survive restart;
  structural privacy test covers the new stats types.

### Phase A2 (v1.2) — Priced signals & diversified earning
- Add content-free signal detection: copy/paste CHORD count (Cmd/Ctrl+C /
  +V happened — count only, never clipboard), app-switch count,
  focus-session count (a sustained-typing block). All in the provider layer
  behind the same trait, all counts.
- Each signal type has a coin value (a data table, like the upgrade
  catalog); Dev Cash accrues from the weighted mix, EXTENDING ADR 0005 (the
  anti-mash ceiling and the mouse<typing invariant must still hold — add
  the strategy-comparison test for the new mix).
- Activity modal shows per-signal coin contribution ("keystrokes: 1,240 ->
  12 coins today").
- Exit: earning is signal-diverse; anti-mash invariant re-proven; per-signal
  pricing visible and tuned; migration keeps existing balances.
- **Status: DONE.** Ships focus-session as the new earning signal (+ app-switch
  tracked/displayed, earns 0 cross-platform; copy/paste deferred behind a
  permission fork — ADR 0012). Both signals derive from data already on Snapshot
  (keystroke timing + sanitized ActiveApp) — no new provider observation,
  content_free_test intact. Coins single-sourced from sprint payout, split
  proportionally (conservation unit-proven). Schema 2->3, non-destructive.
  Overseer-verified live: focus sessions increment, breakdown renders + conserves,
  real save migrates intact, zero console errors.

### Phase A3 (v1.3) — Analytics over time
- Daily/weekly history (a rolling window persisted), streaks, simple bar
  charts (CSS/canvas, pixel-styled), and a couple of honest "workflow
  insights" derived only from counts (busiest hour, longest focus block).
- Exit: history renders, streaks compute correctly across day boundaries,
  no content anywhere in the stored history.
- **Status: DONE.** 30-day rolling history + server-side streaks + a new [H]
  HISTORY modal with CSS pixel bar charts (busiest-day + longest-focus-block
  insights). Hourly buckets dropped (privacy: would reconstruct a daily
  schedule) — busiest DAY instead; longest-focus-block added as one content-free
  duration (ADR 0013). Schema 3->4, additive, future-schema refusal preserved,
  finalize-on-reload exactly once. All 8 streak edge cases unit-proven
  (incl. cross-year/month + streak outliving the 30-day window). Overseer-verified
  live: crafted schema-4 save renders end-to-end, and the read-time effective
  streak folded today in live (seeded 5 -> showed 6).

---

## Frontend architecture track — "a real build, not a hand-written monolith"

User roadmap (2026-08-21): move off hand-written `game.js` to a proper
system — TypeScript source, split into well-organized files, compiled +
bundled + minified; industry-standard separation (rendering engine / data /
logic); and eventually a desktop app via **Tauri** instead of the local
web server. "See how others do it" — research standard patterns.

**Sequencing decision (overseer):** do the build+TS foundation BEFORE more
feature phases (analytics A2/A3, new menus). Building features on the JS
monolith and converting later is rework; a clean typed foundation makes
every later phase cheaper. So the order is F1 -> F2 -> (resume A2) -> ... ,
with Tauri (F3) explicitly later, after the web version is solid.

### F1 (v1.2-arch) — build pipeline + TypeScript, behaviour-identical
- Introduce a lightweight standard toolchain (esbuild preferred: one dep,
  fast, compiles+bundles+minifies TS with sourcemaps — vs the weight of
  webpack). Source in `app/frontend/src/`, output bundled+minified to
  `app/public/js/` (what the Go server already serves).
- Convert `game.js` to TypeScript **with identical behaviour** — a
  mechanical, gated port, not a redesign. Type the WS wire contract (a TS
  module mirroring ui-spec §6 / the Go StateMessage) so state handling is
  type-checked.
- A `make`/npm script builds; document it; CI builds the frontend too.
- Exit: `go run .` still serves an identical-looking, identical-behaving
  game from the compiled bundle; `tsc --noEmit` clean; the in-game gate
  passes (default + store + activity states unchanged).
- **Status: DONE (fb2761a).** esbuild+TS pipeline; 1:1 gated port; deterministic
  bundle; CI drift check. Overseer-verified in the real game.

### F2 (v1.3-arch) — modular separation of concerns
- Split the ported TS into industry-standard layers: a RENDER layer (scene
  compositor, sprite/tint drawing, the terminal), a DATA/STATE layer (WS
  client + a typed state store), and FEATURE/LOGIC modules (store modal,
  activity modal, input/keybinds), with the typed contract shared. Small,
  framework-free; no React unless a later phase justifies it.
- Light research step first ("how others do it") — a survey of how
  comparable local-web/pixel games structure render/data/logic + build, to
  pick conventions rather than invent them.
- Exit: each layer is independently testable; adding a menu touches only a
  feature module + the contract; no cross-layer reach-through.
- **Status: DONE.** Split into env/dom/format/assets/geometry utils, state/{store,
  ws-client}, render/{tint,scene,terminal,chrome,overlays,flash}, features/{store-modal,
  activity-modal,keybindings}, dev/{fixtures,tools}, thin main.ts. tsc clean, deterministic
  bundle, no cross-layer reach-through. Overseer-verified live: scene, both modals via
  key+click+Esc, buy->equip round-trip (-120 cash), keystrokes tick live, zero console errors.

### F3 (later) — Tauri desktop shell
- Wrap the SAME frontend + Go backend as a native desktop app via Tauri
  (Go backend as a Tauri sidecar, or the webview pointing at the local
  server). Floating/always-on-top like the original vision. Deferred until
  the web version + architecture are solid; planned, not scheduled.

## Menus & content track (enabled by the skills below)
Future menus (settings, achievements, more store categories, themes) reuse
the "add-a-modal" skill + the WS-contract-extension pattern. Added as the
user hands roadmaps; not pre-scoped here.

## Deferred (named so they don't creep)
Wails floating window; meeting/mic detection; the tint-hex contrast fix
(slate ~ wall_dark); auto-update/distribution; any Rust revisit.
