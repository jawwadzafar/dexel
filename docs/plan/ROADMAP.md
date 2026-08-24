# Dexel roadmap

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
- **Stack**: Go + HTML/NES.css (`app/`), ADR 0011. The Rust/Bevy game is frozen
  legacy and archived on branch `attic/legacy-rust-and-fleet` (ADR 0020) — not
  in this tree. `app-rs/` is a separate, EXPERIMENTAL port of the *current*
  product and stays.
- **Know what already ships before planning the next thing**:
  [`docs/game/`](../game/README.md) is how the game works *today* — the real
  rules and numbers, read out of the Go source and updated in the same commit
  as any behaviour change. This roadmap is what comes next; that directory is
  what exists. [`docs/README.md`](../README.md) indexes both.

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

---

## Night execution pipeline (2026-08-22, autonomous)

User directive: rename done (dexel + Apache-2.0), now "go on into the night and
keep improving" — execute these one by one, gate each in the real game, commit.
Repo stays PRIVATE until the user says otherwise.

- **UI-1 — Hamburger menu + coin top-left.** Replace the separate [A]/[H]/[S]
  launchers with one ☰ menu (top-right) listing all sections; move the coin/Dev
  Cash display top-left; each menu item opens its existing modal. Structured so
  future sections (Sessions/Goals) slot in. Frontend only; modals unchanged.
  Exit: hamburger opens menu, each item opens its box, coin top-left, keyboard
  shortcuts still work, gated in-game. *(in progress)*
- **UI-2 — README screenshots + cross-platform build docs.** Re-capture hero +
  store + history showing the Dexel titlebar, the fixed seated chair, and the new
  hamburger UI; drop into docs/images/. Add build/run instructions for macOS,
  Linux, Windows (build-from-source now; packaged-app once Tauri lands).
- **SEC-1 — Save integrity / anti-cheat.** Design pass (ADR) first: split a
  user-editable CONFIG (e.g. the Dexel's name) from PROTECTED game state; sign
  the economy-critical fields (Dev Cash, owned items, XP, sprint, history) with
  an HMAC/checksum so hand-editing the JSON to mint money is detected/rejected.
  Honest about limits (a fully local single-player save can't be perfectly
  cheat-proof — the goal is to stop casual JSON editing, not a determined
  reverse-engineer). Then implement + gate.
- **F3 — Tauri desktop shell.** Design pass first (ADR): wrap the existing Go
  backend + web frontend in a Tauri window so it runs as a native app, no
  browser. "Least effort" path: Tauri Rust shell spawns the Go binary as a
  sidecar; the webview points at the local server. Targets: macOS, Windows,
  Linux; architectures x86_64 + arm64 (aarch64) wherever the toolchain allows.
  Release CI build matrix. Then implement + gate.
- **PRODUCT-EVOLUTION.md** *(design-only, in progress, Opus)* — a coherent
  phased product vision from the owner's brief (sessions, goals, journeys,
  long-term progression, world expansion, character life, achievements,
  personal-journey/history, onboarding/identity; social = future-only). Respects
  the privacy model + honest mechanics; no AI/LLM/code-reading. Seeds the
  post-infra roadmap for morning review.

---

## FULL-EXECUTION MANDATE (2026-08-22, user)

"Build all the things next — whatever is on the roadmap we should now do 100%
and have all types of pipeline." Standing order: execute the ENTIRE remaining
roadmap autonomously — every PRODUCT-EVOLUTION phase in order (P1 Identity →
P2 Sessions → P3 Character life → P4 Moments+collectibles → P5 Journeys →
P6 Memory scrapbook, + the continuous world-content stream) plus full CI/CD
pipelines (release pipeline modernized for the Go product with a cross-compiled
target matrix; desktop/Tauri pipeline when a mac/windows runner exists). Each
phase: design-checked against PRODUCT-EVOLUTION.md → parallel implementation
waves with exclusive ownership → overseer clean-cache + real-game gate → commit.
Privacy invariant + honest mechanics are non-negotiable throughout. Repo stays
PRIVATE. In-flight: proportion art pass; release-pipeline modernization.

### Phase status (updated 2026-08-22): P1 Identity DONE · P2 Sessions DONE

### Added to the mandate (2026-08-22, user):
- **DB-1 — SQLite persistence.** Move game state from state.json to SQLite,
  carrying the SEC-1 integrity (HMAC) into the DB so tampering is still
  detected/quarantined. config.json STAYS plain JSON (user-editable by design).
  CRITICAL: pure-Go driver (modernc.org/sqlite) so CGO_ENABLED=0 cross-compiles
  keep working in the release matrix. Schema versioning + future-version
  refusal preserved; one-time import from the JSON save.
- **F3-T1 — Tauri scaffold + run modes.** desktop/ Tauri project (sidecar per
  ADR 0015). RUN MODES: dev = browser (go run, as today); app mode = Tauri
  window (same server as sidecar); production = packaged installer; OSS users
  can build from source or just run browser mode. CI job gated on a mac/rust
  runner (cannot be built/gated on this box — authored + doc'd + CI-wired now,
  first real build happens on a runner with Rust+webkit).

### EMBED-1 — one binary + naming (2026-08-22, user)
- `go:embed` public/ + assets/ into the Go binary → the product is ONE file
  (`dexel`), nothing to locate at runtime. Keep `-public`/`DEXEL_ASSETS_DIR`
  disk overrides for frontend dev iteration.
- Rename the bundle `game.js` → `dexel.js` (index.html, build.mjs, CI drift
  paths, release script together).
- Ripple effects: release archives simplify to binary+licenses;
  the Tauri sidecar becomes the same single binary (resource bundling in the
  desktop/ scaffold gets deleted); RUN-MODES stays: dev = browser (disk
  override), app = Tauri, prod = the one binary / installer.
- Frontend stays framework-free TS+esbuild (decision reaffirmed: fixed-DOM
  pixel game, one WS stream — a framework adds runtime+build weight for
  problems we don't have; revisit only if a later phase strains it).
- Exit: `./dexel` alone (empty dir, no public/, no assets/) serves the full
  game; dev override still works; drift check green on the renamed bundle;
  release script ships single-binary archives; Tauri scaffold updated.

### RUST-PARALLEL track (2026-08-22, user)
A parallel Rust implementation of the Dexel backend lives IN THIS REPO
(top-level Rust workspace) built to feature parity with the Go app, so the two
can be COMPARED head-to-head and the better one chosen. Rules:
- NO migration burden: the Rust app uses its own save (nothing shipped broadly
  yet); the Go app keeps shipping — production is NOT blocked on this track.
- The TS frontend + the WS wire contract + public/ are the SHARED spec: the
  Rust app must speak the identical wire contract and serve the identical
  frontend so the same client works against either backend (apples-to-apples).
- Same invariants re-expressed: content-free privacy (serde-level guarantees),
  ADR 0010 honesty, anti-cheat (own SQLite + HMAC, fresh format fine).
- Comparison scorecard: binary size, RSS, startup, feature-parity checklist,
  test parity, build time, cross-compile matrix — documented measurements, a
  declared winner, and the loser archived (not deleted).
Plan + scorecard: dev_docs/rust-port-evaluation.md (in flight).

### Decisions (2026-08-22, user): Rust experimental · Go main · day-1 production
- The RUST-PARALLEL track is EXPERIMENTAL: Go remains the main implementation;
  Rust reports/artifacts live in dev_docs/rust-parallel/ for a later decision.
- PRODUCTION DAY-1 GOAL: a build the owner starts using immediately —
  including the Tauri desktop app. Path: (1) BLOCKING review fixes (B-1/2/3),
  (2) PR-6 autostart, (3) binary slimming (-s -w + drop embedded sourcemap,
  −6.1MB), (4) tag v0.1.0 + build release archives locally and publish the
  GitHub Release via gh (Actions is account-blocked — SF-2 — so the pipeline
  runs by hand until billing is fixed), (5) install to ~/.local/bin on this box
  + autostart, (6) Tauri first-compile + window gate — UNBLOCKED BY OWNER
  installing the webkit dev packages (exact apt command in the session log).

### RUST-PARALLEL track: CONCLUDED (2026-08-24) — Go stays
Experiment stopped at P0 by decision (see app-rs/VERDICT.md): the Tauri
in-process prize became an anti-goal under the background-runtime product
model; remaining gains are cosmetic; the one real win (recursive compile-time
content-free guarantees) is back-ported to the Go tests instead. app-rs/ and
the goldens stay frozen as the record; the goldens double as Go wire-contract
fixtures. Revisit triggers recorded in the verdict file.
