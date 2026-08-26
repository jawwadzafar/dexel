# Dexel — bug / polish log (evidence-based)

Filed 2026-08-22 from user review of the live screenshots. Each fix must be
verified in the REAL running game (render → look → iterate), not in isolation.

## UI track (frontend: index.html / game.css / features/menu.ts)

- **BUG-1 — Menu needs a one-line title.** The hamburger dropdown currently
  lists only the buttons. Add a single title line (e.g. "MENU") at the top,
  then Store / Activity / History (and room for future sections). Keep it one
  line.
- **BUG-2 — Top-left should be coin + level ONLY.** Remove the "dexel" wordmark
  and the mood dot from the title bar. Show the coin/Dev Cash FIRST, then the
  level. Nothing else top-left.
- **BUG-3 — Bottom-panel text is vertically clipped.** e.g. "SPRINT" in the
  sprint panel is cut off at the bottom — the text row height/line-height is
  ~8px and glyphs are clipped. Increase to ~10px (and audit the other panel/
  terminal/status text for the same clipping) so no text is cut. Verify in the
  real render that full glyphs/descenders show.

## Art track (tools/gen_assets.py / assets / geometry.ts) — FIXED 2026-08-22

**Resolved:** root cause was the head sitting above the hard-region line so no
chair could rise over it — fixed by LOWERING the head (not raising chairs).
Hood dome dropped ~11px; arms pulled outward with a 4px gap each side (hood +
2 arms now read as 3 forms); every chair back painted up to the hard-region
top with always-visible detail markers (mesh/stitch/tufting/glow) so the
chair-top reads above the head at any tint and stays distinct from the hoodie.
Overseer-gated across basic/exec/antigrav.

Evidence: in the current hero/scene render the character still doesn't clearly
read as sitting IN the chair, and several elements blend together.

- **BUG-4 — Hoodie and chair don't visually distinguish.** The character's
  hoodie and the chair behind it are too similar in tone/shape; they merge.
  They need clearly different value/shape so the eye separates person from seat.
- **BUG-5 — Character still doesn't read as ON the chair.** Key direction from
  the user: from behind, the TOP of the chair (backrest top) should be seen
  FIRST — i.e. the chair back rises above the head and reads as the topmost/
  backmost element, with the person's head sitting BELOW the chair-back's top
  edge. Right now the person reads as in front of / on top of a low shape.
- **BUG-6 — Hand + hood merge into one blob.** The arms/hands reaching to the
  keyboard blend into the hood/head so it reads as a single blob. Separate them
  — a gap, an outline, or distinct shading between the hood, the shoulders, and
  the arms/hands.
- **BUG-7 — Behind-view hooded-figure reference.** Study how a person actually
  looks from behind wearing a hood while seated (hood drape, shoulder line,
  where the chair shows around them) and apply it so the silhouette is legible.

Fix approach: BUG-5/6/7 are one coherent redraw of the behind-view seated
figure + chair relationship (dev sprite + chair sprites + the CHAIR_RECT/DEV_RECT
geometry), gated by real in-game renders judged by eye until it clearly reads as
"a hooded person seated in a chair, seen from behind."

## Follow-ups found during fixes

- **BUG-8 — Activity modal footer/last row clipped (pre-existing). FIXED 2026-08-22.** `#activity`
  is declared `height: 396px`, but the browser caps a native `dialog:modal` at
  `max-height: calc(100% - 38px)` = 362px at the game's 400px viewport, so the
  modal's footer and last "coins earned today" row get scroll-clipped. Predates
  the BUG-1/2/3 fixes (Store/History modals stay under the cap). Fix: shrink the
  Activity modal to fit ≤362px (tighten row heights / overall height) or
  restructure so nothing is cut. UI/CSS follow-up.

## From the 2026-08-22 adversarial review (docs/plan/REVIEW-2026-08-22.md)

- **B-1..B-3 (BLOCKING)** — schema<5 unsigned-save mint; legacy-Rust-import
  mint; failed AppendSession → id-skip → false-tamper wipe. Fix wave queued
  behind PR-5 (same files).
- **SF-2 (CI never ran)** — every GitHub Actions run in this repo's history is
  `startup_failure` at 0s with path "BuildFailed" (runner online, Actions
  enabled, workflows schema-valid per actionlint). Remaining documented cause:
  ACCOUNT-LEVEL billing/Actions state on the private repo — **OWNER ACTION:
  check github.com/settings/billing (Actions payment/spending limit) and the
  repo's Settings→Actions page for a banner.** All verification so far has
  been our local gates (real, but CI must come alive before production tag).
- SF-1/3/4/5/6/7 + 10 NITs — queued into the post-PR-5 fix wave.

- **BUG-9 — `dexel stop` escalates to SIGKILL consistently. FIXED 2026-08-25.**
  (Observed in every installer gate: "did not exit within 5s… escalating".)

  **Root cause: `os.File.Fd()`.** Measured, not guessed — a SIGQUIT dump of a
  hung runtime put goroutine 1 in `LinuxProvider.Stop` → `wg.Wait`, and all 12
  reader goroutines in a bare blocking `read(2)` syscall (`internal/poll.FD.Read`
  with no netpoller wait). The reason they were blocking rather than parked in
  the poller is one line in the hotplug rewrite's `evdevScanner.Open`:
  `syscall.Fstat(int(f.Fd()), &st)`. `os.File.Fd()` puts the descriptor **back
  into blocking mode** and takes it off Go's netpoller for good, and closing
  such an fd CANNOT interrupt an in-flight read — internal/poll defers the real
  `close(2)` until the read returns. So `Stop()` waited for the next key or
  mouse event on **every** device; on an idle gate machine (no page open, nobody
  typing) that never came, and `dexel stop` spent 5s on the lifecycle endpoint,
  5s more on SIGTERM, then hard-killed. Isolated probe of the two variants:
  `OpenFile` + `Fd()` → 0 of 12 readers returned within 2s of `Close()`;
  without `Fd()` → 12 of 12 returned in ~6ms each.

  Everything else on the path was innocent, with numbers: `persist` (SQLite)
  **1ms**, `httpSrv.Shutdown` **0s** even with three live WebSocket connections
  and a real browser page open (nhooyr.io/websocket hijacks the connection, and
  `http.Server.Shutdown` by design neither closes nor waits for hijacked
  connections).

  **Fix.** (a) `evdevScanner.Open` now opens each node with
  `syscall.Open(..., O_RDONLY|O_NONBLOCK|O_CLOEXEC)`, fstats the **raw** fd
  (never through `Fd()`), and wraps it with `os.NewFile` — which registers the
  pollable evdev fd with the netpoller, so `Close()` interrupts a blocked read
  in milliseconds. (b) `Stop()` bounds its wait anyway (`stopWaitTimeout`,
  500ms) and says so out loud if it ever has to abandon a reader, so no future
  kernel quirk can make shutdown hang again; each start generation gets its own
  WaitGroup so an abandoned wait can never collide with a later `Start()`.
  (c) `main.go`'s shutdown closure now logs a per-phase timing breakdown (the
  bug was invisible for a day because the log said "shutting down" and then
  nothing), and `http.Server.Shutdown`'s budget dropped from 5s — the CLI's
  *entire* patience — to 1s (`httpShutdownGrace`); the save still goes first and
  unbounded, because losing progress is the only unacceptable outcome.
  The CLI's 5s `stopGrace` is unchanged and is now generous headroom rather than
  a coin flip (`shutdown_budget_test.go` asserts that relationship).

  **Before → after** (same box, real Linux provider, 12 input devices):
  `dexel stop` 10.12s / 10.12s / 10.13s / 10.12s / 10.12s with a hard kill every
  time → **0.109s / 0.109s / 0.108s / 0.109s / 0.109s**, and 0 escalations in 10
  consecutive stop cycles with a real browser page (live WebSocket) open, save
  file mtime+hash advancing on every one. In-runtime breakdown after the fix:
  `persist=1ms provider.Stop=83-109ms http.Shutdown=0s total=84-110ms`
  (provider.Stop is 12 sequential poller-closes at ~7ms each — the whole
  graceful path is now ~1% of the CLI's grace).

  Same fix cures a bug nobody had filed: `dexel pause` calls `provider.Stop()`
  from the single-owner loop, so pausing an idle machine used to freeze the
  entire game (ticks, WS broadcasts, autosave) until the next keystroke. Pause
  now returns in ~0.1s.

  Regression tests (`app/internal/activity/provider_linux_shutdown_test.go`,
  `app/shutdown_budget_test.go`): the real scanner's fds must be non-blocking
  and interruptible (a FIFO stands in for an evdev node, so it runs anywhere);
  `Stop()` on real `/dev/input` must return promptly (skipped where no node is
  readable); `Stop()` must stay bounded even against a reader that never
  returns; and non-blocking fds must still DELIVER events (counted keystrokes),
  since an interruptible fd that stops counting would be a worse bug. Both
  fd-level tests were confirmed to FAIL against the old `Fd()` code.

- **BUG-10 — input directed AT dexel counts as the user's work activity.**
  The global provider (evdev/system hooks) counts ALL input; clicking/typing in
  dexel's OWN window therefore accrues activity and animates the mouse hand —
  i.e. fiddling with the companion reads as "working," the same self-referential
  lie ADR 0019 rejects for the activity line. Fix (honest framing = "dexel does
  not count itself"): the shell/page already knows its own focus (Tauri window
  focus + document focus); send a self-focused signal over WS, and while dexel's
  own window is focused, SUPPRESS accrual (treat like a scoped auto-pause — that
  time counts in nothing, per ADR 0010), and don't drive the mouse-hand frame
  from dexel-directed input. Cross-platform (doesn't need global ActiveApp, which
  Wayland can't give). Medium effort — touches the engine honesty core + a WS
  focus message + the frontend; sequence it carefully (well-tested honesty code).
  Owner flagged it as OK-to-defer if big; it's medium, so queued after the store
  batch.

- **BUG-11 — hamburger menu items look unaligned/disharmonious.** Each menu
  button has a different-length label + an inline key hint, so nothing lines up
  and it reads as ragged. Fix (standard menu harmony, like a command palette):
  in the monospace pixel font, LEFT-align the labels and RIGHT-align the [KEY]
  hints into a single column (flex justify-between), consistent item width (full
  menu width), consistent vertical padding/row height, and a consistent hover/
  selected treatment. All the [S][A][H][W][G][P] hints then stack in a clean
  right column. Frontend: features/menu.ts + the menu DOM in index.html +
  game.css menu section. Small. Sequence with the other game.css/index.html work
  (coin/store/store-size) to avoid file collisions.

- **BUG-12 — right-click context menu is allowed in the app.** The webview
  shows the browser context menu (Linux: back/forward/stop/reload; macOS:
  reload) — lets anyone navigate/reload the app. Fix: suppress `contextmenu`
  (preventDefault) globally, alongside the existing dragstart/selectstart
  hardening in `render/interaction.ts`. Keep it on real text inputs
  (name/session fields) so right-click paste still works there — same
  input-exception pattern as the drag/select hardening. Tiny (one capturing
  listener). Sequence with the next frontend pass (rebuilds the same bundle as
  the running store redesign — can't parallelize on dexel.js).
