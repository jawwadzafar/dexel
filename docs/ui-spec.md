# UI spec — Dexel v2 (HTML / NES.css frontend)

Target stack per **ADR 0011**: Go backend, HTML/JS/NES.css frontend served
over `localhost:8080`, later wrapped by Wails v3 as a floating frameless
always-on-top window. This document is the contract between the backend agent
(state + WebSocket), the frontend agent (DOM + CSS), and the art agent
(sprite geometry, which lives in `docs/art-direction.md`).

**It is written to be implementable without seeing the source mockups.** Every
rect is given in exact pixels. Where the mockups were ambiguous the decision is
marked `[DESIGN CALL]` with the reason.

Companion documents: `docs/art-direction.md` (sprite sizes, scene placement,
layer order, tint mechanism), `docs/upgrade-design.md` (the item catalog,
slots, prices, persistence).

## 0. Ground rules

* **Layout: 640 x 400 logical px, fixed.** Not responsive. No media queries,
  no flex reflow that could move a pixel. The layout below is absolute
  positioning inside a 640x400 root, and that is deliberate: a desktop
  companion at a fixed integer scale is the entire premise (art-direction
  non-negotiable #7). The *window* is resizable and the whole 640x400 unit is
  scaled to fit it — see **§0.1**, which is the only place the window's size
  enters this spec at all.
* **Every length is an integer px.** No `%`, no `em`, no `rem`, no `vh` in the
  chrome. Fractional layout at this scale produces the half-pixel blur the art
  direction bans. (§0.1's letterbox container is the one `%` in the
  stylesheet, and it is outside the layout, not in it.)
* **Font: `Press Start 2P`** (the NES.css companion font), bundled locally —
  **never** from a CDN, because the app must work fully offline (ADR 0001).
  It is an 8x8 cell monospace: at `font-size: 8px` one character is exactly
  8x8 px. Every character-count budget below assumes 8px/char.
  Permitted sizes: **8px and 16px only.** Nothing else stays crisp.
* **Colours: the 18-colour palette in `docs/art-direction.md`, and nothing
  else.** Ship them as CSS custom properties on `:root` (`--wall-dark`,
  `--screen`, `--cream`, …) generated from the same table
  `tools/gen_assets.py` uses, so chrome and sprites can never drift.
* `image-rendering: pixelated` on every `<img>` and sprite background.
* `body { cursor: url(...) }` via NES.css's `.nes-pointer` on `<body>`.
* No animation longer than 400 ms, no easing curves, no transitions on
  colour. Retro UI snaps.

### 0.1 Window fit — scaling the fixed layout to the real window

§0's layout is 640x400 and does not reflow; a window (or a browser tab) is
whatever size it is. The two have to be reconciled somewhere, and they are
reconciled here, and only here — by the page, in `render/viewport.ts`.

**Two halves, and which one is load-bearing where.** The page scales-and-
letterboxes (this section). The native shell additionally *constrains its own
window* so that the letterbox comes out 0px wide — **§0.4**, WINDOW-FIT. In
the shell at rest the game therefore touches all four window edges and none of
the offsets below are ever painted; in a browser tab, during the ~200ms of a
live drag, and in a maximized shell window, the letterbox is exactly what you
see. Neither half is redundant: the shell cannot control a tab, and the page
cannot resize a window it does not own.

**The contract.** The 640x400 layout is scaled **as one unit** to fit the
window: aspect ratio preserved exactly, never stretched on one axis, centred,
with the leftover area letterboxed/pillarboxed in `var(--shadow)` — the same
colour `#root` already paints as its own ground, so the fill reads as the
app's bezel continuing outward rather than as a gap behind the layout. The
layout is never clipped, and no rect in §2 onwards ever moves relative to any
other.

**The scale factor.** `render/viewport.ts` computes it and publishes it as
three custom properties on `:root` — `--ui-scale` (unitless), `--ui-ox` /
`--ui-oy` (integer px) — recomputed on load, on `resize`, and on a
device-pixel-ratio change:

```
exact = min(viewportWidth / 640, viewportHeight / 400)

exact < 1   ->  use exact          (window smaller than the layout: shrink to
                                    fit; clipping would hide controls)
otherwise   ->  cap at MAX_SCALE (3), then snap DOWN to the nearest CRISP
                factor. Always. No tolerance, no non-crisp fallback.
```

A **crisp** factor is one where one art pixel covers a whole number of *device*
pixels: an integer at `devicePixelRatio: 1`, and also 1.5x, 2.5x, … on a
retina display where 1.5 CSS px is exactly 3 device px. At a non-crisp factor
nearest-neighbour scaling gives art pixels *uneven widths* — at 1.5x on a dpr-1
screen, alternating 1 and 2 device px — and the 8px pixel font is rasterised
off its own grid. Both are visibly mushy in a game whose whole look is "every
pixel is where the artist put it".

**Which ladder (WINDOW-POLISH decision).** The ladder is derived from the
display rather than hardcoded, which is the same choice made correctly:

| `devicePixelRatio` | step | ladder |
| --- | --- | --- |
| 1 | 1 | 1x, 2x, 3x |
| 2 | 0.5 | 1x, 1.5x, 2x, 2.5x, 3x |
| 3 | ⅓ | 1x, 1.333x, 1.667x, … |
| fractional (Windows 125%) | 1 | 1x, 2x, 3x — no crisp increment exists |

A hardcoded half-step ladder would be pixel-exact on a retina display and
pixel-*uneven* on the ordinary 1080p monitor most users have. So half steps are
used exactly where they are free. **The cost, stated plainly:** on a dpr-1
display a 960x600 window fits 1.5x exactly and renders at 1x instead — a
smaller crisp picture instead of a larger mushy one.

**The cap.** `MAX_SCALE = 3` is a *product* decision, not a technical one:
nothing breaks at 4x. dexel sits beside your work, and its 8px type stops
reading as cozy somewhere past 3x — on a 4K display an uncapped fit would pick
5x and paint a 3200x2000 developer. Past the cap the extra room becomes
letterbox, which is the intended shape of a too-big window.

**Centring.** `transform-origin: 0 0` plus explicit `Math.round`ed offsets, not
a centre origin. With a centre origin the browser derives the offset itself as
`(vw - 640*s)/2`, which is a half pixel for any odd leftover and puts the whole
layout on a half-pixel grid — every art pixel then straddles two device pixels
and the crisp factor is thrown away at the very last step.

Measured in the real running game with headless Chromium at dpr 1:

| window | exact | applied | offset | letterbox |
| --- | --- | --- | --- | --- |
| 660x460 | 1.03125 | **1x** | (10, 30) | the shell's default until WINDOW-FIT — **this row is the bug §0.4 fixes** |
| 700x500 | 1.09375 | **1x** | (30, 50) | small-window case |
| 960x600 | 1.5 | **1x** | (160, 100) | 1.5x is not device-pixel-exact at dpr 1 |
| 1280x800 | 2.0 | **2x** | (0, 0) | exact fit, no bars |
| 1920x1080 | 2.7 | **2x** | (320, 140) | a wide letterbox, perfectly crisp |

Since WINDOW-FIT the shell only ever hands this rule the sizes in §0.4's second
table, all of which come out at offset (0, 0) — at dpr 1 that is 640x400 (1x),
1280x800 (2x, the row above) and 1920x1200 (3x). Those two extra sizes are
arithmetic rather than measurements from the run above; they are asserted in
Rust instead, by feeding every one of them through a transcription of this
rule (`mod window_sizing` in `desktop/src-tauri/src/lib.rs`).

An earlier revision allowed a non-crisp `exact` whenever snapping cost more
than 1/8 of the size, which is what put a 1920x1080 window at **2.7x** — every
art pixel 2 or 3 device px wide, i.e. exactly the stretch-blur this section
exists to prevent. That tolerance is gone.

**The mechanism.** A single `transform: translate(--ui-ox, --ui-oy)
scale(--ui-scale)` with `transform-origin: 0 0` on `#root`, plus
`image-rendering: pixelated`. A transform and **not** `zoom`: a transform is
applied *after* layout, so every box stays exactly integral in CSS px and the
finished composite is scaled uniformly, whereas `zoom` re-lays-out at the
scaled size and at a fractional factor rounds every box independently — which
lets 8px cells drift a pixel apart and breaks the grid this design is built on.

**Modal dialogs carry the same transform themselves.** A `<dialog>` opened
with `showModal()` is promoted to the **top layer**, and a top-layer element
is *not* affected by an ancestor's transform (verified in this project's own
headless Chromium: a dialog inside a `scale(0.5)` parent rendered at 1:1).
Custom-property inheritance still reaches it, so each of the **six** dialogs
(`#store`, `#activity`, `#history`, `#sessions`, `#settings`, `#onboarding`)
declares its authored position as `--dlg-x` / `--dlg-y` instead of
`left`/`top` and applies
`translate(--ui-ox, --ui-oy) scale(--ui-scale) translate(--dlg-x, --dlg-y)`
— the function order matters: the box is placed inside the layout's
coordinate space *first*, then that space is scaled, then centred. With
`left`/`top` the offset would be applied once by layout and again by the
transform, and the modal would drift away from the scene as the window grew.

> **Fixed in WINDOW-POLISH:** `#settings` was **missing** from that selector
> list. It was added by SET-1 after this rule was written; it declares its own
> `--dlg-x`/`--dlg-y` and its CSS comment says "consumed by the shared
> window-fit transform above", but the id list was never extended — so it got
> neither the transform nor a `left`/`top`, and a `position: fixed` box with
> `margin: 0` and no inset falls back to its *static* position. The Settings
> modal therefore sat at the top-left of the viewport, unscaled, while every
> other modal tracked the scene; visible the moment the window was not exactly
> 1x. This is the tax an id list charges, and it is why the interaction rules in
> §0.2 are written against element selectors instead.

**Hit targets need nothing extra.** A CSS transform is part of the box the
browser hit-tests, so a click at a scaled position maps back through it
natively. Nothing in `app/frontend/src` does *viewport-coordinate* arithmetic
that a transform could invalidate — no `getBoundingClientRect`, no `clientX`.
(The store's scrollbar thumb reads `scrollTop`/`scrollHeight`/`clientHeight`
off `#store-grid`, which are element-local **layout** metrics in unscaled CSS
px; a transform is applied after layout and does not touch them.) Verified
with real CDP-dispatched clicks at 1x, 1.40625x and 2x: the hamburger, every
menu item, a store category row, a card action button, and each modal's X all
hit the element they are drawn on.

### 0.2 Interaction hardening — the surface is a game, not a document

INTERACTION-HARDENING (`docs/plan/ROADMAP.md`). The game surface must behave
like a game surface: **sprites are not draggable, scene text is not selectable,
and clicks are deliberate.** Real text inputs are the one exception and stay
fully editable.

**What was actually wrong.** Every sprite in the scene is an `<img>`, and an
`<img>` is a native drag source *by default*. Dragging one and dropping it back
onto the same window made the page **navigate to `/assets/<file>.png`** — the
running game replaced by a bare PNG, the WebSocket gone, recoverable only by a
reload. Sweeping the mouse across the scene also drew a blue text selection over
the terminal and status lines, which in a pixel-art window reads as a rendering
fault. And `Ctrl/Cmd+A` — the chord a user presses *to select text* — fired the
bare-letter `[A]` shortcut and opened the Activity modal.

**Four mechanisms, because no single one covers it.**

| mechanism | where | what it stops |
| --- | --- | --- |
| `draggable = false` on every `<img>` | `dom.ts`'s `spriteImg()`, the single factory every render module now uses | the element being a drag source at all — the only one of these that is not a hint |
| `-webkit-user-drag: none` on `img` | `game.css` "Interaction hardening" | the drag gesture in Blink/WebKit (every engine dexel ships to) |
| `user-select: none` on `html, body, #root`, restored to `text` on `input, textarea, [contenteditable="true"]` | `game.css` | selection over the scene; the exception is element-selector-based so a modal added later inherits it |
| capturing `dragstart` / `selectstart` cancel, skipped inside text entry | `render/interaction.ts` | anything the three above do not reach (a link a future feature adds, an `<img>` built without `spriteImg`) |

Plus one keyboard rule: `features/keybindings.ts` returns early when
`ctrlKey`/`metaKey`/`altKey` is held. Its own comment already claimed every
shortcut was "a bare letter with no modifier" — nothing checked it, and `e.key`
for `Ctrl+A` is still `"a"`. **Shift is deliberately excluded**: `Shift+S`
produces `"S"`, which the handlers accept on purpose so caps lock does not break
shortcuts.

**What is deliberately NOT done.** No `pointer-events: none`, and nothing
touches `click` / `mousedown` / `pointerdown`. The scene container must keep
receiving clicks cleanly — that is the groundwork SCENE-REACTIONS builds on —
and `pointer-events: none` on sprites would have been the lazy way to stop drags
while making the scene permanently unclickable. Cancelling `mousedown` (the
usual shortcut for "no selection") would also cancel focus and break every
button and input in the app.

`-webkit-touch-callout: none` on `#root` is authored for the WebKit engines
(WebKitGTK on Linux, WKWebView on macOS), where a long press otherwise offers
"Save image" / "Open in new tab" — the same escape hatch, reached differently.
Blink does not implement it, so it does not appear in a Chromium computed style.

### 0.3 Shell mode — the same page in a frameless native window

WINDOW-POLISH. The Tauri shell now builds its window with
**`decorations(false)`**, so the game's own 640x24 `#titlebar` is the whole
title bar: it is the window's **drag region**, and it carries the window's
**close and minimize** buttons.

The same `index.html` is served to a browser tab, which has all of those
already. So the browser page must stay **pixel-identical**, and it is:

| | browser | shell (`?shell=1`) |
| --- | --- | --- |
| `body` class | `nes-pointer` | `nes-pointer shell` |
| `#win-minimize` / `#win-close` | `display: none` | `display: block`, at 564 / 600 |
| `#menu-open` | `left: 600px` | `left: 528px` |
| `data-tauri-drag-region="deep"` on `#titlebar` | present, inert (nothing reads it) | read by the injected handler |

**How the shell is detected: it declares itself.** `desktop/src-tauri/src/
lib.rs` appends `?shell=1` to the loopback URL it loads (`SHELL_QUERY`), and
`app/frontend/src/env.ts`'s `SHELL_MODE` reads it. Deliberately **not**
`typeof window.__TAURI__ !== 'undefined'`: that global is injected into every
document the webview loads and says nothing about whether a native frame exists,
so detecting it would put close/minimize *on top of* a decorated title bar.

**How a remote-origin page can drag and close a native window** is written up
in full in [`desktop/README.md`](../desktop/README.md) § "The frameless shell";
the short version is that tauri injects its IPC and every plugin init script
into *every* document with no local-vs-remote condition, and the only real gate
is the ACL — hence `capabilities/loopback-window-controls.json`.

**Failure is loud.** `features/shell-window.ts` shows the buttons whenever
`?shell=1` is set, even if the Tauri API is unreachable, and a click then reports
the failure in the flash toast and the console. A frameless window with a
silently dead close button is unclosable; a visible complaint is recoverable.

**There is no maximize button, and that is now a decision rather than an
omission** — see §0.4. `?shell=1` changes nothing else about sizing: the page
runs the same §0.1 rule in both modes, and the shell simply never hands it a
size that needs a letterbox.

### 0.4 The shell window is constrained so the game fills it (WINDOW-FIT)

§0.1 makes the page fit any window. This makes the *window* fit the page, in
the one window this product owns. It is implemented entirely in
`desktop/src-tauri/src/lib.rs` (`crisp_sizes` / `nearest_crisp_size` /
`opening_size` / `SnapGuard`); the page is untouched.

**What was wrong.** The window opened at **660x460** and could be dragged to
anything, so the game was letterboxed inside its own frame — a 10px pillarbox
and a 30px letterbox at the default size, and whatever was left over at every
hand-dragged one. The game never touched a window edge. (660x460 dated from
when the sprint/ticker chrome was believed to live *outside* the 640x400 root;
it has been inside it since BUG-2.)

**The rules.**

| | |
| --- | --- |
| inner size range | **min 640x400 (1x)**, **max 1920x1200 (3x)**, via Tauri's `min_inner_size`/`max_inner_size` — the OS enforces it during a drag, so no size outside the range ever has to be corrected |
| opening size | the fixed **1x surface, 640x400**, on every display — 1x is pixel-crisp, and the user resizes up (snapping to 2x/3x) whenever they want. No size is remembered between launches |
| after a resize | when resize events go quiet for **200ms**, the inner size is set to the **nearest crisp size** — Euclidean-nearest in logical pixels, ties to the smaller — bounded by the monitor's work area so a snap can never grow the window off the screen |
| maximize / fullscreen | **not snapped.** `maximizable(false)` removes the gesture on macOS/Windows, `max_inner_size` caps how big a WM maximize can get, and the page's letterbox covers what is left (a keyboard/WM maximize on Linux). A maximized window is the display's shape, which is the one shape an 8:5 game cannot fill |

**The ladder is §0.1's ladder**, derived from the display, because the whole
point is to hand the page a size that is already on it:

| `devicePixelRatio` | window sizes it can have |
| --- | --- |
| 1 | 640x400, 1280x800, 1920x1200 |
| 2 | 640x400, 960x600, 1280x800, 1600x1000, 1920x1200 |
| 3 | 640x400, 1280x800, 1920x1200 |
| fractional (125%) | 640x400, 1280x800, 1920x1200 |

The dpr-3 row is *not* a copy-paste of the dpr-1 row: a 1/3 step would put
1.333x at 853.33 logical px, which is not a size a window can have, and a
window rounded to 853 is a third of a logical pixel short — which §0.1's
`Math.floor` reads as **1x**, making the perfect fit the case that looks
worst. So steps whose
logical size is not whole on both axes are dropped, which on a dpr-3 display
leaves the whole scales.

**Why nearest and not "the largest that fits".** The game's aspect ratio is
fixed and a drag is not, so a drag has to resolve to one rung. Snapping down
to what fits makes a big diagonal drag jump *backwards* (drag to 1200x750 at
dpr 1 and the largest crisp size that fits is 640x400), which reads as the
drag having failed. Nearest means a drag more than halfway to the next rung
lands on it. The honest cost of nearest-in-pixel-space: a *big* one-axis drag
takes the other axis with it — pulling the right edge to 1900 asks for 3x, and
3x is 1920x**1200**. The work-area bound is what stops that leaving the screen.

**Two feedback loops, both guarded** (`SnapGuard`). `set_size` produces a
resize event, which would re-arm the settle timer forever: a reported size that
already *is* the target ends the chain (and, being the definition of success,
clears the retry counter). And a window manager that answers `set_size` with a
different size — a tiling WM, an edge-tiled window — would otherwise be asked
again every 200ms forever: the same unanswered target is asked for **3 times**,
then this shell leaves the window alone and the page's letterbox is what the
user sees. That fallback is not a consolation prize; it is why §0.1 stays.

## 1. DOM contract

Implement exactly these ids. The backend never touches the DOM, but the
verification harness and the spec both address elements by id.

```html
<body class="nes-pointer">
  <div id="root">                    <!-- 640x400, position:relative -->

    <div id="scene-sprites"></div>   <!-- 320x200, scale(2), room px -->
    <div id="scene-text">            <!-- 640x400 overlay, UI px -->
      <div id="terminal"></div>      <!--   11 lines inside the bezel -->
    </div>

    <div id="titlebar">
      <span id="hud-cash"><i class="nes-icon coin is-small"></i> 0</span>
      <span id="hud-level">LV 1</span>
      <button id="menu-open" aria-haspopup="true" aria-expanded="false"
              aria-label="Menu"><!-- three .bar divs, never a glyph --></button>
      <div id="menu-panel">           <!-- dropdown, hidden until .visible -->
        <div id="menu-panel-title">MENU</div>  <!-- always "MENU" (§7.4) -->
        <button id="store-open"    class="nes-btn menu-item">[S] STORE</button>
        <button id="activity-open" class="nes-btn menu-item">[A] ACTIVITY</button>
        <button id="history-open"  class="nes-btn menu-item">[H] HISTORY</button>
        <!-- P2: #sessions-open joins here, §9.1 -->
        <!-- SET-1: #settings-open ([G] SETTINGS) joins here, §11.1 -->
        <!-- PR-5: #pause-toggle joins here, §2.4 -->
      </div>
      <!-- WINDOW-POLISH: the frameless shell's window controls, §0.3.
           Always in the DOM, DISPLAYED only under body.shell. -->
      <button id="win-minimize" class="nes-btn" aria-label="Minimize window">-</button>
      <button id="win-close"    class="nes-btn" aria-label="Close window">X</button>
    </div>

    <div id="panel-sprint" class="pixel-panel">
      <div id="sprint-name">SPRINT: —</div>
      <progress id="sprint-bar" class="nes-progress is-success"
                value="0" max="1"></progress>
      <div id="sprint-units">0 / 0 units</div>
    </div>

    <div id="panel-status" class="pixel-panel">
      <div id="status-real"><span id="status-dot"></span>
           <span id="status-line">Working...</span></div>
      <hr id="status-rule">
      <ul id="ticker"><li></li><li></li><li></li></ul>
    </div>

    <div id="scrim"></div>            <!-- shown while a modal is open -->
    <dialog id="store" class="nes-dialog is-dark"> … §4 … </dialog>
    <dialog id="activity" class="nes-dialog is-dark"> … §6.1 … </dialog>
    <dialog id="history" class="nes-dialog is-dark"> … §6.1 … </dialog>
    <!-- P2: <dialog id="sessions"> joins here, §9.2 -->
    <!-- SET-1: <dialog id="settings"> joins here, §11.2 -->
    <dialog id="onboarding" class="nes-dialog is-dark"> … §7 … </dialog>
  </div>
</body>
```

`.pixel-panel` is our own class, **not** `nes-container`: NES.css containers
carry ~1rem padding and a 4px box-shadow border that, at 640x400, eats the
entire panel. Spec: `background: var(--metal); border: 4px solid
var(--wall-light); box-sizing: border-box; padding: 6px;` — the same chunky
pixel-border *look*, at a size that fits. Use real NES.css classes where they
fit (`nes-btn`, `nes-progress`, `nes-icon`, `nes-dialog`, `nes-text`) and our
own where they do not. Document any NES.css override in one CSS block, not
scattered.

## 2. Main screen layout

The decluttered layout the user chose. **Explicitly banned** (the user's own
word for the rejected version was "too much cluttered"): the three header
widgets `GLOBAL PROJECT OVERVIEW`, `WORKSHOP EFFICIENCY: 85%`,
`AI PARTNER: "…"`; the isometric workshop room; the video-call face grid; the
right-hand always-open store sidebar. If you are adding a widget to the main
screen, the answer is no.

```
 x=0        128       256       384       512       640
  +----------------------------------------------------------------+ y=0
  | dexel          ●                    LV 5  ◆ 2150  [ [S] STORE ]|
  +----------------------------------------------------------------+ 24
  |                                                                |
  |                                                                |
  |            (the behind-the-shoulder scene, 640x400,             |
  |             visible band y 24..320 — see art-direction)         |
  |                                                                |
  |                                                                |
  |                                                                |
  +----------------------------------------------------------------+ 320
  | +--------------------------+  +-----------------------------+   |
  | | SPRINT: Fix Bug #404     |  | ● Coding in VS Code         |   |
  | | [############        ]   |  | ---------------------------- |   |
  | | 34 / 75 units            |  | > Compiling...               |   |
  | +--------------------------+  | > Resolving dependencies...  |   |
  |                               | > Running unit 42...         |   |
  +----------------------------------------------------------------+ 400
```

### 2.1 Title bar — `#titlebar`

**Current reality (BUG-2 titlebar redesign):** the wordmark and mood dot are
gone from the titlebar — mood now shows only via `#status-dot` in the bottom
status panel (§2.3). The top-left cluster is coin-then-level, and nothing
else, ever (the standing owner directive quoted again at §7.4). The three
separate `[S]`/`[A]`/`[H]` launcher buttons that used to sit in the titlebar
were replaced by a single hamburger (`#menu-open`, top-right) that opens a
dropdown (`#menu-panel`) listing every section launcher as one more
`.menu-item` button — so a future entry (P2's Sessions, and whatever comes
after) is zero titlebar layout work.

| Element | Rect (x, y, w, h) | Content |
|---|---|---|
| `#titlebar` | 0, 0, 640, 24 | `background: var(--metal)`; 2px bottom border `var(--wall-light)` |
| `#hud-cash` | 8, 4, 64, 16 | coin icon + `2150`, 8px, `var(--gold)`, left-aligned |
| `#hud-level` | 80, 8, 40, 8 | `LV 5`, 8px, `var(--cream)` |
| `#menu-open` | 600, 4, 32, 16 | hamburger button, `padding: 0`. Icon is three plain 1px-tall `.bar` divs inside `.menu-icon` (16x7), **not** a `☰` glyph — a fancy character renders blurry/inconsistent at this app's 1x DPI in the pixel font, the same lesson A2 already recorded for `→` |
| `#menu-panel` | 496, 26, 136, auto | dropdown opened by `#menu-open`, closed by default (`display:none`, shown via `.visible`); right edge (632) lines up with `#menu-open`'s right edge (632) so it never overflows the 640px titlebar |
| `#win-minimize` | 564, 4, 32, 16 | **shell mode only** (§0.3) — `display: none` in a browser. `line-height: 8px`, not 16: `.nes-btn` is `border-box` with a 4px border, so a 16px-tall button has an **8px content box**, and a 16px line-height put a line box twice its container's height and squashed the glyph into an unreadable smudge at the bottom edge (#store-close solves it the same way). Plain ASCII `-`, per §11.3's missing-glyph lesson |
| `#win-close` | 600, 4, 32, 16 | **shell mode only** — same rules; plain ASCII `X`, the same character `#store-close` uses |

In shell mode `#menu-open` moves to `left: 528px` to make room, so the three
buttons are 528..560 / 564..596 / 600..632 — right-aligned to the same 632px
inner edge `#menu-panel` already uses. `#session-pill` (ends 372) and
`#paused-badge` (ends 476) are clear of all three, so nothing else in the bar
moves.

`#titlebar` itself carries `data-tauri-drag-region="deep"` — the whole bar is
the window's drag handle in shell mode, and the injected handler excludes
`BUTTON`/`INPUT`/`A`/`LABEL` by itself, so all three buttons above stay
clickable with no extra markup. The attribute is inert in a browser.
| `#menu-panel-title` | inside `#menu-panel`, 128 x 16 | static `MENU`, always (a menu is a menu — the name lives in the status line now, §7.4), 8px `var(--screen-dim)`, bottom rule separating it from the items |
| `.menu-item` (×N) | inside `#menu-panel`, 128 x 20 each, 4px gap | one `nes-btn` per launcher, in menu order: `#store-open` (`[S] STORE`), `#activity-open` (`[A] ACTIVITY`), `#history-open` (`[H] HISTORY`), `#sessions-open` (`[W] SESSIONS`, P2, §9.1), `#settings-open` (`[G] SETTINGS`, SET-1, §11.1), and (PR-5, §2.4) `#pause-toggle` — label and action both flip live between `[P] PAUSE` (sends `PAUSE`) and `[P] RESUME` (sends `RESUME`), decided by `state.paused` read fresh from the store at click time, never assumed from the label |
| `#paused-badge` | 380, 8, 96, 8 | (PR-5, §2.4) the always-visible paused indicator — an 8px dim solid square (`#paused-badge-dot`) + `PAUSED`, 8px `var(--screen-dim)`. Empty/hidden (`display:none` / `.visible`, same idiom as `#session-pill`) unless `state.paused` is true. Sits clear of both `#hud-level` (ends 120) and `#session-pill`'s box (132..372, §9.5) — a session can be active *and* paused at the same time, so both must stay visible together |

Dev Cash lives in the titlebar, next to Level. **[DESIGN CALL]** the mockups
only show the balance *inside* the store modal, but a currency you cannot see
is a currency you forget you have; the decluttering the user asked for removed
three header *panels*, not the score. This reasoning outlived BUG-2's move of
the store *button* into the menu — the balance itself never left the titlebar.

**[DESIGN CALL]** every menu item keeps its bracketed shortcut in its label,
e.g. `[S] STORE`, `[A] ACTIVITY`, `[H] HISTORY`, `[W] SESSIONS`. ADR 0010
requires the store (and, by the same reasoning, every section) be permanently
discoverable *and* its shortcut be discoverable; the bracketed key does both
jobs in one element, which is how the shipped Bevy build already presented
it. BUG-2 moved these buttons off the titlebar and into `#menu-panel`; it did
not change this convention.

### 2.2 Sprint panel — `#panel-sprint`

| Element | Rect | Content |
|---|---|---|
| `#panel-sprint` | 8, 322, 308, 74 | `.pixel-panel` (4px border + 6px pad → content box 18, 332, 288, 54) |
| `#sprint-name` | 18, 332, 288, 8 | `SPRINT: ` in `var(--screen-dim)` + the name in `var(--cream)`, 8px. **36 chars max**; truncate with `…` |
| `#sprint-bar` | 18, 348, 288, 16 | `<progress class="nes-progress is-success">`, `value=progress`, `max=target` |
| `#sprint-units` | 18, 372, 288, 8 | `34 / 75 units`, 8px, `var(--screen-dim)` |

The progress bar is the one element that must move visibly while the user
works; it is the product's whole feedback loop. Update it on every `state`
message, with no CSS transition (a transition makes a 1-unit tick look like
lag).

### 2.3 Status panel — `#panel-status`

Two visually distinct zones, and the distinction is a **privacy contract**,
not decoration.

| Element | Rect | Content |
|---|---|---|
| `#panel-status` | 324, 322, 308, 74 | `.pixel-panel` (content box 334, 332, 288, 54) |
| `#status-dot` | 334, 332, 8, 8 | mood dot, same colour as `#mood-dot` |
| `#status-line` | 346, 332, 276, 8 | **REAL**: `state.activityLine`, 8px, `var(--cream)`. 34 chars max |
| `#status-rule` | 334, 344, 288, 1 | 1px `var(--wall-light)` |
| `#ticker li` (3) | 334, 350 / 362 / 374, 288, 8 | **GAME FLAVOUR**: `> ` + a pool line, 8px, `var(--screen-dim)`. 36 chars max incl. prefix |

* The top row is the only place on screen that says anything true about the
  user's machine (`activityLine`, from ADR 0009's app-identity mapping —
  `"Coding in VS Code"`, `"In the terminal"`, `"Working..."`). It is `cream`,
  has no prefix, and carries the mood dot.
* **It is now drawn from a POOL per app type, not one string per state.** The
  examples above are a subset. The server classifies the frontmost app
  (`activity.AppTypeOf`) and only offers phrasings that type licenses: a work
  verb ("Coding in X", "Typing in X") exists ONLY for coding-class apps, and
  the non-coding pools are presence-only ("In X", "Frontmost: X") — which is
  why `"Coding in Brave"` is now unrepresentable rather than merely unlikely.
  The choice is a deterministic hash of (app id, 45s clock bucket), so the
  line re-rolls at most once per 45s while the frontmost app is unchanged;
  a per-tick random pick at 1Hz would read as a broken UI. The 34-char cap on
  this row is enforced SERVER-side now: a candidate that would clip is not
  offered, and if nothing fits, the shortest true rendering wins rather than a
  shorter, less true claim.
* **When the dexel is NAMED, the line becomes a personal sentence** composed
  on the server: `"Pixel is coding in VS Code"`, `"Pixel is in the zone"`,
  `"Pixel is thinking…"`, `"Pixel is on break"` (§7.4). The privacy rule is
  unchanged — the work verb ("coding in {app}") is still reachable only for a
  coding-class app in Coding mood; every other named case is cozy, mood-only
  phrasing that names no app. The name eats into the same 34-char budget, so a
  long name degrades the app-naming form to a short app-less anchor (`"{name}
  is coding"` / `"is here"` / `"is away"`) rather than clipping. An UNNAMED
  dexel keeps exactly the impersonal phrasing above. This is the ONLY place
  the name now appears in the chrome (the old bare `#status-name` strip and
  the name-swapped menu title were removed, §7.4).
* The three rows below it are the character's own chatter. They are dimmer, are
  prefixed `>`, and are separated by the rule. **Never merge the two zones,
  never let a ticker line borrow a word from the real one.**
* **PR-5 override:** while `state.paused` is true, `#status-line` shows a
  fixed client-side string, `PAUSED — tracking is off`, in place of
  `activityLine` — see §2.4.

### 2.4 Paused chrome (PR-5 — Pause semantics)

`docs/production-runtime/MIGRATION_PLAN.md` §PR-5. Three things change,
all driven by the one `state.paused` bool (§6.1) and nothing else —
`activeState` never gains a fourth value for this (ADR 0010; pausedness is
conveyed only via `paused`, never by inventing a mood string):

1. **`#status-line`** (§2.3) shows the fixed client-side string `PAUSED —
   tracking is off` instead of `activityLine`. This is the one place
   `render/chrome.ts` composes its own text rather than rendering the
   server's verbatim — safe specifically because it asserts nothing the
   server did not say (`state.paused` is the server's own signal) and the
   string itself carries no observed content.
2. **`#status-dot`** (§2.3) renders `var(--screen-dim)` — the dim half of
   the same screen/screen-dim pair already used elsewhere for
   bright-vs-muted (e.g. `.nes-btn.is-disabled`) — instead of whatever
   `MOOD_COLOR[activeState]` would otherwise show, so paused reads as a
   visually distinct state from plain idle.
3. **`#paused-badge`** (§2.1) becomes visible in the titlebar, to the
   right of `#hud-level` and clear of `#session-pill`'s box — a session
   survives a pause (§9's `sessions.active`), with its own
   `pausedSeconds` accruing instead of its other counters, so the pill and
   the badge must be readable together, never mutually exclusive.

The menu gains one entry (§2.1's `.menu-item` row): `#pause-toggle`, label
and action both driven live by `state.paused`, read fresh from the store at
the moment of the click (`features/menu.ts`) — never assumed from whatever
label was last painted. No keybinding is bound to it; `[S]`/`[A]`/`[H]`/`[W]`/
`[M]` (§5.2) are unaffected and keep routing exactly as before.

## 3. Status ticker & terminal content rules

Both surfaces are game flavour. Both are subject to the same hard rule, which
is the ADR 0002 privacy boundary expressed in UI terms:

> **No string rendered in `#ticker` or `#terminal` may be derived from
> anything on the user's machine.** No file path, file name, window title, app
> name, URL, clipboard content, hostname, username, or wall-clock time. The
> pools are compile-time `[]string` constants in the backend. The *only*
> string on screen that may contain machine-derived text is `#status-line`,
> and it may contain only a mapped application display name.

Enforce it structurally, the way the Rust build did: the frontend receives
`tickerLines` and `screenLines` as **already-chosen literal strings** and does
zero client-side assembly. A frontend that never formats these fields cannot
leak into them.

### 3.1 Ticker pools, partitioned by `activeState`

| `activeState` | Pool (extend freely; these are the seed sets) |
|---|---|
| `coding` | `Compiling...` · `Resolving dependencies...` · `Analyzing schematic...` · `Running unit 42...` · `Linting the linter...` · `Type-checking module 7...` · `Rebuilding index...` |
| `idle` | `Waiting on input...` · `Cursor blinking...` · `Reading the docs...` · `Watching for changes...` |
| `onBreak` | `Idle timer running...` · `Screen saver engaged...` · `Sipping something warm...` · `Nothing to compile.` |

* One new line every **2.5 s**. Newest line at the **top** (`#ticker li:first-child`),
  the oldest scrolls off the bottom. (Opposite direction to the terminal —
  intentional: a log panel reads newest-first, a terminal reads newest-last.)
* Line selection must be **deterministic**, so a screenshot test reproduces:
  `pool[(sprintIndex*7 + tickCount) % len(pool)]`.
* Switching `activeState` does not clear the panel; the next tick simply draws
  from the new pool.

### 3.2 Terminal (`#terminal`) geometry and behaviour

Full spec in `docs/art-direction.md` ("The screen region"); repeated here as
the UI-side contract because this is `#scene-text`, not a sprite:

| | value |
|---|---|
| rect (UI px) | **(196, 48) 248 x 88** |
| text area (4px inset) | (200, 52) 240 x 84 |
| lines | **11**, `line-height: 8px`, `font-size: 8px` |
| chars per line | **30** at 8px `Press Start 2P` |
| colours | newest 2 lines `var(--screen)`, older `var(--screen-dim)` |
| overflow | `hidden`. No scrollbar, ever |

Newest line at the **bottom**. `coding`: push a line every 0.35 s. `idle`: no
scroll, an 8x8 `var(--screen)` block cursor blinks at 0.5 s after the last
line. `onBreak`: no scroll, all lines `screen_dim`, the monitor **sprite** gets
`filter: brightness(0.55)`, and the last line reads `-- idle --`.

Seed pool for `screenLines` (fake, and obviously fake on inspection). **Every
pool line must be <= 30 characters** — the cap is the font, not a preference,
and a longer line is silently clipped:

```
func handleRequest(ctx) error {
  if err != nil { return err }
-> ok  parser        1.4s
warning: unused import 'fmt'
[ 62%] building target...
test result: ok. 41 passed
resolved 118 deps in 0.9s
   Compiling companion v0.2
```

## 4. The store modal

Opened via the `[S] STORE` button, the `S` key, or `Tab`. Implemented as a
native `<dialog>` opened with `showModal()`, which buys focus trapping and
`Esc`-to-close for free.

```
  x=40                                                          x=600
   +----------------------------------------------------------------+ y=28
   | ▤ DEV STORE & CUSTOMIZATION                              [ X ] | 44
   |                                          TOTAL DEV CASH: ◆ 2150 | 64
   |                                                                | 80
   | +-----------+ +--------------------------------+ +-----------+  | 88
   | | > HOODIE  | | [thumb] Basic Office           | | PREVIEW   |  |
   | |   CHAIR ✓ | |         OWNED · EQUIPPED       | | +-------+ |  |
   | |   KEYBOARD| | ▪▪▪▪▪▪            [ EQUIPPED ] | | |       | |  |
   | |   MOUSE   | +--------------------------------+ | | 152 x | |  |
   | |   BEVERAGE| | [thumb] Racer                  | | | 152   | |  |
   | |   PLANT   | |         100 ◆                  | | |       | |  |
   | |   WALL    | | ▪▪▪▪▪▪            [ BUY 100  ] | | +-------+ |  |
   | |   BUDDY   | +--------------------------------+ | Racer     |  |
   | |           | | [thumb] Executive Leather      | | 300 ◆     |  |
   | | ^v move   | |         300 ◆                  | | Cobalt    |  |
   | | 1-6 colour| | ▪▪▪▪▪▪            [ NEED 300 ] | |           |  |
   | | ESC close | +--------------------------------+ |           |  |
   +----------------------------------------------------------------+ 316
```

### 4.1 Geometry

`#store` is `position: fixed; left: 40px; top: 28px; width: 560px;
height: 288px; margin: 0; padding: 12px; border: 4px solid var(--wall-light);
background: var(--metal);` — giving a content box of **(56, 44) 528 x 256**.

| Element | Rect | Notes |
|---|---|---|
| `#store-title` | 56, 44, 400, 16 | `DEV STORE & CUSTOMIZATION`, 8px, `var(--cream)` (25 chars) |
| `#store-close` | 568, 44, 16, 16 | `nes-btn`, label `X`. Also `nes-icon close is-small` is acceptable |
| `#store-cash` | 384, 64, 200, 8 | `TOTAL DEV CASH: ◆ 2150`, 8px, `var(--gold)`, right-aligned |
| `#store-cats` | 56, 88, 120, 212 | category list |
| `#store-grid` | 184, 88, 232, 212 | scrolling item list (viewport) |
| `#store-scroll` | 418, 88, 6, 212 | drawn scrollbar track |
| `#store-preview` | 424, 88, 160, 212 | preview pane |

**`#store-cats`** — 8 rows, 20px pitch, y 88..248:

| Row | y | Slot | Label |
|---|---|---|---|
| 0 | 88 | `hoodie` | `HOODIE` |
| 1 | 108 | `chair` | `CHAIR` |
| 2 | 128 | `keyboard` | `KEYBOARD` |
| 3 | 148 | `mouse` | `MOUSE` |
| 4 | 168 | `beverage` | `BEVERAGE` |
| 5 | 188 | `plant` | `PLANT` |
| 6 | 208 | `wall` | `WALL` |
| 7 | 228 | `buddy` | `BUDDY` |

Row states: **selected** = `background: var(--wall-light)`, text `var(--cream)`,
a `>` glyph in the 8px gutter at x 56. **hover** = `background:
var(--wall-dark)`. **default** = transparent, text `var(--screen-dim)`. A slot
whose equipped item is not its free default also shows a `✓` in `var(--gold)`
right-aligned at x 160 — so the list doubles as an at-a-glance loadout.

Help block below the list, 8px `var(--screen-dim)`: y 256 `^v MOVE`,
y 268 `1-6 COLOUR`, y 280 `ENTER ACT`, y 292 `ESC CLOSE`.

**`#store-grid`** — a vertical stack of cards, `overflow-y: auto`, native
scrollbar hidden (`scrollbar-width: none`, `::-webkit-scrollbar {display:none}`).

Card: **232 x 64**, 8px vertical gap. Three cards fully visible
(64*3 + 8*2 = 208 ≤ 212). Card internals, relative to the card's origin:

| Part | Rect | Content |
|---|---|---|
| thumbnail | 6, 6, 40, 40 | the item's `thumb` (flat `<img>`), or its `thumbForm`/`thumbDetail` pair under the mask+multiply tint recipe in `docs/art-direction.md`, showing the **selected** tint. Branch on `thumb == null` — see §6.1 |
| name | 52, 6, 174, 8 | item name, 8px `var(--cream)`, **21 chars max** |
| price / state | 52, 20, 174, 8 | `100 ◆` / `OWNED` / `EQUIPPED`, 8px |
| swatch row | 52, 34, 70, 10 | up to 6 chips, 10x10, 2px gap |
| action button | 130, 30, 96, 24 | `nes-btn`, 8px label, one per card |

Card states: **selected** = 2px `var(--screen)` outline. **hover** = 2px
`var(--gold)` outline. Selection and hover are separate; hover never changes
selection.

Swatch chips: `background` = the tint at ramp step 4 (`tint * 0xd4/0xff`,
computed in JS — see the art doc's swatch rule, so the chip matches the
garment). An **unowned** chip carries a 1px `var(--shadow)` diagonal slash. The
**selected** chip carries a 2px `var(--cream)` outline. A slot with no tints
(everything except hoodie and chair) renders no swatch row.

**`#store-scroll`** — **[DESIGN CALL]** the native scrollbar is hidden and a
6x N `var(--wall-light)` thumb is drawn instead, with
`top = scrollTop/scrollHeight * 212` and `height = clientHeight/scrollHeight
* 212`. Platform scrollbar chrome is the one thing guaranteed to look wrong
inside a retro-OS window, and it renders differently in Safari's webview than
in the headless browser we verify with.

**`#store-preview`**:

| Part | Rect | Content |
|---|---|---|
| label | 424, 88, 160, 8 | `PREVIEW`, 8px `var(--screen-dim)` |
| viewport | 428, 104, 152, 152 | 1px `var(--metal)` border, `var(--shadow)` fill |
| name | 424, 264, 160, 8 | full item name, 8px `var(--cream)`, 20 chars |
| state | 424, 276, 160, 8 | `300 ◆` / `OWNED` / `EQUIPPED`, 8px |
| colour | 424, 288, 160, 8 | selected tint's display name, 8px `var(--screen-dim)` |

### 4.2 Preview pane behaviour

* Follows **selection**, instantly, with no button press. There is no
  "commit preview" step; previewing never mutates state.
* **Hoodie and Chair** render a composed mini-scene at **1x**: the developer's
  `dev_form`/`hoodie`/`dev_base` layers plus the chair layers, using the
  currently equipped items for every slot *except* the previewed one, which
  uses the selected item + selected tint. Centre the composite in the 152x152
  viewport (the widest chair is 144px, so 1x fits). This is the only way a
  colour choice is judgeable before buying.
* **Every other slot** renders the item's sprite alone, centred, at the largest
  integer scale ≤ 3 that fits 152x152 (so `bev_mug` 20x24 → 3x, `kb_mech`
  96x24 → 1x).
* The `none` items (`plant_none`, `wall_bare`, `buddy_none`) render the word
  `NOTHING` in `var(--screen-dim)`, centred.

### 4.3 The one action button — precedence

Every card has exactly **one** action button, whose label and behaviour derive
from state in this fixed precedence. Both the click and `Enter` run the same
function.

| # | Condition | Label | Click does |
|---|---|---|---|
| 1 | item not owned, `devCash >= price` | `BUY 100` | `BUY_ITEM` |
| 2 | item not owned, `devCash < price` | `NEED 100` | insufficient-funds feedback (§4.4) |
| 3 | owned, selected tint not owned, affordable | `BUY COLOUR 40` | `BUY_TINT` |
| 4 | owned, selected tint not owned, unaffordable | `NEED 40` | insufficient-funds feedback |
| 5 | owned + tint owned, not currently equipped **or** equipped with a different tint | `EQUIP` | `EQUIP_ITEM` |
| 6 | equipped with exactly this item + tint | `✓ EQUIPPED` | nothing (`is-disabled`) |

**[DESIGN CALL] Buying grants the item with its default tint free; the other
five tints cost 40 each, per (item, tint) pair.** The mockups show colour
swatches *and* a `Buy for N` button on the same card without saying which the
button buys; making the default free means every purchase is immediately
wearable, and making extras cost gives the long tail somewhere to go.

**[DESIGN CALL] There is no per-card `[Preview]` button**, though the mockups
draw one. Selecting the card already previews it, and in a 232px-wide card two
buttons leave no room for a name like "Executive Leather".

### 4.4 Insufficient funds

* Button gets `nes-btn is-disabled`, label `NEED <price>`, and the card is
  **not** dimmed — the art stays fully visible and previewable, because
  showing you what you can't afford yet is the motivation.
* Clicking it (or `Enter` on it) sends nothing. The frontend flashes
  `#store-cash` to `var(--pot)` for 400 ms and back. No sound (the app has no
  audio).
* If the backend rejects a purchase anyway (a race against a concurrent
  client, or a stale balance), it replies with a `flash` of kind `error` and
  broadcasts unchanged state; the frontend renders the flash in
  `#store-cash`'s position for 1.5 s.

### 4.5 Live update behind the modal

`#scrim` covers **only the scene band**: `left:0; top:24px; width:640px;
height:296px; background: rgba(36,31,46,0.45)` (that is `--shadow` at 45%).
The title bar and both bottom panels stay unobscured.

**[DESIGN CALL] the scrim is deliberately light** so you can watch the scene
change while the modal is open; the PDF calls for the main companion screen to
update the instant an item is equipped, and a dark scrim would hide the payoff.
Close is `X`, `Esc`, `S`/`Tab`, **or a click in the empty area outside the
panel** (§5.4).

> **Click-away close is an owner mandate (2026-08) that reverses this
> section's original "clicking outside does NOT close" rule.** With a modal
> open, its `::backdrop` sits in the top layer above the whole window,
> including the frameless desktop titlebar — so the menu / minimise / close
> buttons stopped responding while a modal was up, which is confusing and (in
> the frameless shell) removes the only way to move or close the window.
> Dismissing on an outside click frees the titlebar for the next click. The
> light scrim still does its job: the scene is visible behind the open modal
> exactly as before; only a *click* in that empty area now dismisses instead
> of doing nothing.

On a successful `EQUIP_ITEM`, the backend broadcasts a fresh `state`; the
frontend re-renders `#scene-sprites` from it. The scene must visibly change
while the modal is still open.

## 5. Input

### 5.1 Mouse — this is new, and it is the primary input

The shipped Bevy build had **no clickable UI whatsoever** — the store was a
`Tab`-toggled keyboard-only strip and no element in the app responded to a
click. Every hover state, every button, every scroll region in this document is
net-new interaction surface. Budget for it; do not assume any of it is a port.

The good news: in HTML it is all free. `:hover`, `:active`, `click`, and
`wheel` need no hit-testing code, and at a fixed 640x400 with no page scroll,
DOM rects *are* the numbers in this document.

| Target | Action |
|---|---|
| `#store-open` | open the modal |
| `#store-close` | close the modal |
| `#store-cats` row | select that slot; reset grid selection to index 0 and scroll to top |
| card body | select that card (updates preview) |
| swatch chip | select that tint for that card (updates preview and the thumbnail) |
| card action button | run the §4.3 action |
| `#store-grid` wheel | scroll; update `#store-scroll` |
| `#scrim` | **nothing** — it is a `pointer-events:none` visual dim; the click lands on the modal's `::backdrop` instead (see below) |
| click in the empty area OUTSIDE the panel (the `::backdrop`) | close the modal (click-away, §5.4) |
| the dexel / the monitor / the beverage / the buddy | play that item's reaction (SCENE-REACTIONS, §12) |
| anywhere else in the scene | nothing |

### 5.2 Keyboard

Main screen:

| Key | Action |
|---|---|
| `S` | open the store |
| `Tab` | open the store (`preventDefault()` so focus does not move) |
| `A` | open the activity log |
| `H` | open the history modal |
| `W` | open the Sessions modal (P2, §9.1 — "work session"; `S`/`Tab`/`A`/`H`/`M` are taken) |
| `G` | open the Settings modal (SET-1, §11.1 — `G` for the *gear*; `S`/`A`/`H`/`W`/`M`/`P` and `Tab` were all taken) |
| `M` | toggle the hamburger menu |
| `P` | pause / resume tracking (PR-5, §2.4) |

Store modal open:

| Key | Action |
|---|---|
| `Up` / `Down` | move selection within the focused pane (category list or card list) |
| `Left` | move focus to the category list |
| `Right` | move focus to the card list |
| `1`..`6` | select swatch *n* on the selected card |
| `[` / `]` | cycle the selected swatch back / forward |
| `Enter` | run the selected card's §4.3 action |
| `Esc` | close (native `<dialog>` behaviour — do not intercept) |
| `S` / `Tab` | close |
| click outside the panel | close (click-away, §5.4) |

Onboarding modal open (§7):

| Key | Action |
|---|---|
| any printable key | goes to `#onboarding-name`; **no global shortcut fires** |
| `Enter` | confirm (same as `SAY HELLO`) |
| `Esc` | skip (native `<dialog>` close; see §7.3 for what skipping means) |

Sessions modal open (P2, §9):

| Key | Action |
|---|---|
| any printable key | idle view only: goes to `#sessions-name`; **no global shortcut fires** (the modal owns an input, so it claims the keyboard-ownership tier the same way onboarding does) |
| `Enter` | idle view only, input focused: start the session — same as `START SESSION` |
| `W` / `Esc` | close (idle or live view); on the summary card, closes back to the live view — the same action as `[ NICE ]` |
| click outside the panel | close (click-away, §5.4) |

* The card list auto-scrolls to keep the selection fully visible.
* `Tab` is bound to *open* on the main screen but must **not** be captured
  inside the dialog beyond closing it — leave native focus cycling alone.
* **Bare-letter shortcuts never fire at a focused text field.** Every main
  screen shortcut above is an unmodified letter, which was harmless until §8
  introduced the app's first `<input>`: typing the name "sasha" would
  otherwise fire `[S]`, `[A]`, `[S]`, `[H]`, `[A]` and stack three modals over
  the field being typed into. The global router
  (`features/keybindings.ts`) therefore **returns immediately** for any
  keydown whose `target` is an `input`, `textarea`, `select` or
  `contenteditable` element — checked before any other tier, and written
  against the element KIND, not against §8's specific id, so any future field
  inherits the rule. A modal that owns an input additionally takes the
  keyboard-ownership tier for as long as it is open, so a bare letter cannot
  reach a launcher even when focus sits on one of that modal's buttons.
  `Esc` needs no exception: a native `<dialog>` handles `Esc` itself, above
  this listener.
Settings modal open (SET-1, §11):

| Key | Action |
|---|---|
| any printable key | *while the rename field is focused*: goes to `#settings-name`; **no global shortcut fires** (the field is not focused on open — see §11.2) |
| `Enter` | rename field focused: save the name — same as `SAVE NAME` |
| `Space` / `Enter` | a toggle button focused: flip that preference (native `<button>` activation) |
| `G` / `Esc` | close |
| click outside the panel | close (click-away, §5.4) |

* Window-level shortcuts (quit, minimise) belong to the desktop shell, not
  to this document. **Always-on-top is no longer one of them:** it was a
  hardcoded `always_on_top(true)` in `desktop/src-tauri/src/lib.rs`, and
  SET-1 made it a user preference the *page* owns and the shell obeys
  (§11.4, §11.5). (This bullet also used to say "Wails shell" — the shell
  has been Tauri since ADR 0015.)

### 5.3 Shopping must not count as work — hard requirement

ADR 0010's honesty rules say `coding` requires a real keystroke. The macOS
sampler is **global**: it cannot tell a keystroke aimed at the store modal from
one aimed at your editor. Left alone, keyboard-driven shopping would accrue
work, which is a self-feeding money loop.

Therefore:

* On open, the frontend sends `{"action":"STORE_OPEN"}`; on close,
  `{"action":"STORE_CLOSE"}`.
* While `storeOpen`, the engine **accrues no work and no Dev Cash**, and
  **freezes the idle/AFK clock** — it must not flip to `onBreak` either. It
  holds the last `activeState`. The same reasoning as ADR 0010's
  "an unfocused window freezes the idle clock": the game cannot know, so it
  must not claim.
* The connection dropping while `storeOpen` is true must clear the flag
  server-side, or a crashed tab freezes progression forever.

### 5.4 Click-away dismiss — every browsable modal

A click in the empty area **outside** an open modal's panel closes it. This
applies to all five browsable modals — **Store, Activity, History, Sessions,
Settings** — and is the fix for the frameless titlebar being unreachable while
a modal is up (a `<dialog>` opened with `showModal()` puts its `::backdrop`
in the top layer, above the whole window including the titlebar buttons; see
the owner-mandate note in §4.5). `Esc` still closes every modal natively, and
every in-panel control keeps working — a click *inside* the panel never
dismisses.

**Onboarding is deliberately excluded.** It is the one-time first-run naming
flow, not a browsable modal; a stray click must not silently auto-skip it and
lose a half-typed name. It stays dismissable only by its explicit `[ SKIP ]`
button and by `Esc` (both of which name the dexel with the skip default —
§7.3), never by a click-away.

**Mechanism (`features/modal-dismiss.ts`, wired once, called from each of the
five modal files).** On the dialog's own `click`, close only when the pointer
falls outside the panel's `getBoundingClientRect()` — NOT when
`event.target === dialog`. The panels carry 12px of padding and lay their
children out absolutely, so there are empty gaps *inside* the panel where the
click target is the `<dialog>` itself; a `target === dialog` test would wrongly
dismiss on those. The rect test is also transform-proof: each modal is scaled
by the shared window-fit transform (§0.1, BUG-2), and both
`getBoundingClientRect()` and `clientX/clientY` are post-transform viewport
coordinates, so the test holds identically at 1x, 2x and every fractional
letterbox scale. A `mousedown`-started-inside guard stops a text selection that
begins in a name input and releases outside from reading as a click-away, and a
`detail === 0` guard ignores the synthetic (0,0) click a keyboard-activated
button fires. The opening click never reaches this handler, because it targets
a top-bar / menu button that is not an ancestor of the dialog.

## 6. WebSocket state contract

One endpoint: `ws://localhost:8080/ws`. JSON, one object per message, every
message carries `type`.

### 6.1 Server → client

**`catalog`** — sent once immediately on connect, and again only if the catalog
changes (i.e. never, at runtime). Static content, so it is not part of the 1 Hz
broadcast.

```json
{
  "type": "catalog",
  "v": 1,
  "slots": [
    {"id": "hoodie",   "name": "Hoodie",   "tintable": true},
    {"id": "chair",    "name": "Chair",    "tintable": true},
    {"id": "keyboard", "name": "Keyboard", "tintable": false},
    {"id": "mouse",    "name": "Mouse",    "tintable": false},
    {"id": "beverage", "name": "Beverage", "tintable": false},
    {"id": "plant",    "name": "Plant",    "tintable": false},
    {"id": "wall",     "name": "Wall",     "tintable": false},
    {"id": "buddy",    "name": "Buddy",    "tintable": false}
  ],
  "tints": [
    {"id": "slate",  "name": "Classic Black",     "hex": "#2b2b33", "price": 40},
    {"id": "cobalt", "name": "Cobalt Blue",       "hex": "#4a7fa8", "price": 40},
    {"id": "forest", "name": "Forest Green",      "hex": "#4e8b4f", "price": 40},
    {"id": "ember",  "name": "Cyberpunk Orange",  "hex": "#a45c3a", "price": 40},
    {"id": "neon",   "name": "Neon Pink",         "hex": "#e86aa4", "price": 40},
    {"id": "indigo", "name": "Midnight Indigo",   "hex": "#6a5aa0", "price": 40}
  ],
  "items": [
    {
      "id": "chair_racer",
      "slot": "chair",
      "name": "Racer",
      "price": 100,
      "sprite": "chair_racer_form.png",
      "detail": "chair_racer_detail.png",
      "thumb": null,
      "thumbForm": "thumb_chair_racer_form.png",
      "thumbDetail": "thumb_chair_racer_detail.png",
      "defaultTint": "ember",
      "flavor": "Bolstered wings. Zero laps completed."
    },
    {
      "id": "kb_mech",
      "slot": "keyboard",
      "name": "Mechanical",
      "price": 60,
      "sprite": "kb_mech.png",
      "detail": null,
      "thumb": "thumb_kb_mech.png",
      "thumbForm": null,
      "thumbDetail": null,
      "defaultTint": null,
      "flavor": "Audible from the next room. Intentionally."
    }
  ]
}
```

`items` is ordered: within a slot, cheapest first, the free default first of
all. The frontend renders slots and items **in the order given** and hardcodes
nothing about either. `sprite` is null for the `none` items; `detail` is null
for untinted slots.

**Thumbnail fields.** The two examples above are the only two shapes, and which
one an item uses is decided by its slot, never guessed by the frontend:

* **Non-tintable slot with a real sprite** — `thumb` is
  `"thumb_<itemId>.png"`, and `thumbForm` / `thumbDetail` are `null`.
* **The two tintable slots (`hoodie`, `chair`)** — `thumb` is `null`, and
  `thumbForm` / `thumbDetail` are `"thumb_<itemId>_form.png"` /
  `"thumb_<itemId>_detail.png"`. This is the same two-file tint recipe as the
  full-size sprite (`docs/art-direction.md`, "Recolourable parts"), applied to
  the 40x40 thumbnail — which is what lets a store card **re-tint its own
  thumbnail live** as the player clicks through swatches, instead of showing a
  colour the card is not selling.
* **The three `*_none` items** (`plant_none`, `wall_bare`, `buddy_none`) — all
  three fields are `null`, as is `sprite`.

So the card's thumbnail render is a two-branch decision on `thumb == null`, and
nothing else: one `<img>` for the flat case, the mask+multiply pair for the
tinted case.

**`state`** — on connect, then every **1 s**, and **immediately** after any
mutation (purchase, equip, sprint completion) so the UI never waits up to a
second to reflect a click.

```json
{
  "type": "state",
  "v": 1,
  "activeState": "coding",
  "activityLine": "Coding in VS Code",
  "devCash": 2150,
  "level": 5,
  "xp": 1240,
  "storeOpen": false,
  "paused": false,
  "appIdentityAvailable": true,
  "sprint": {
    "index": 3,
    "name": "Fix Bug #404",
    "progress": 34,
    "target": 75,
    "unitLabel": "units"
  },
  "screenLines": ["...", "...", "...", "...", "...", "...", "...", "...", "...", "...", "..."],
  "tickerLines": ["Compiling...", "Resolving dependencies...", "Running unit 42..."],
  "equipped": {
    "hoodie":   {"itemId": "hoodie_zip",    "tintId": "cobalt"},
    "chair":    {"itemId": "chair_racer",   "tintId": "ember"},
    "keyboard": {"itemId": "kb_mech",       "tintId": null},
    "mouse":    {"itemId": "mouse_gaming",  "tintId": null},
    "beverage": {"itemId": "bev_thermos",   "tintId": null},
    "plant":    {"itemId": "plant_none",    "tintId": null},
    "wall":     {"itemId": "wall_poster",   "tintId": null},
    "buddy":    {"itemId": "buddy_duck",    "tintId": null}
  },
  "ownedItems": ["hoodie_classic", "hoodie_zip", "chair_basic", "chair_racer"],
  "ownedTints": ["hoodie_zip:cobalt", "chair_racer:ember"],
  "stats": {
    "today": {
      "keystrokes": 4210,
      "mouseActiveSeconds": 812,
      "activeSeconds": 3040,
      "idleSeconds": 260,
      "sprintsCompleted": 3,
      "focusSessions": 4,
      "appSwitches": 12,
      "pausedSeconds": 90
    },
    "lifetime": {
      "keystrokes": 88420,
      "mouseActiveSeconds": 15810,
      "activeSeconds": 61200,
      "idleSeconds": 5100,
      "sprintsCompleted": 41,
      "focusSessions": 57,
      "appSwitches": 205,
      "pausedSeconds": 640
    },
    "coinsToday": {"keystrokes": 6, "mouse": 2, "focusSessions": 4, "appSwitches": 0},
    "history": [
      {"date": "2024-06-01", "keystrokes": 3980, "mouseActiveSeconds": 740, "activeSeconds": 2900, "idleSeconds": 300, "sprintsCompleted": 2, "focusSessions": 3, "appSwitches": 9, "coinsEarned": 41, "isActive": true, "longestFocusBlockSeconds": 1380, "pausedSeconds": 0},
      "... 28 more DayStat entries, dense and ascending ...",
      {"date": "2024-06-30", "keystrokes": 4210, "mouseActiveSeconds": 812, "activeSeconds": 3040, "idleSeconds": 260, "sprintsCompleted": 3, "focusSessions": 4, "appSwitches": 12, "coinsEarned": 12, "isActive": true, "longestFocusBlockSeconds": 900, "pausedSeconds": 90}
    ],
    "streak": {"current": 6, "longest": 14}
  },
  "config": {"name": "Pixel", "alwaysOnTop": false, "showAwayTime": false, "soundEnabled": true},
  "sessions": {
    "active": {
      "id": 28,
      "name": "auth refactor",
      "startedAt": "2024-06-30T14:02:00Z",
      "elapsedSeconds": 4931,
      "keystrokes": 2110,
      "mouseActiveSeconds": 340,
      "activeSeconds": 1500,
      "idleSeconds": 90,
      "sprintsCompleted": 1,
      "focusSessions": 2,
      "appSwitches": 3,
      "coinsEarned": 9,
      "longestFocusBlockSeconds": 720,
      "pausedSeconds": 0
    },
    "summary": {"completed": 27, "thisWeek": 4, "longestSessionSeconds": 9840},
    "recent": [
      {"id": 27, "name": "", "startedAt": "2024-06-29T09:00:00Z", "endedAt": "2024-06-29T10:24:00Z", "durationSeconds": 5040, "keystrokes": 4182, "mouseActiveSeconds": 610, "activeSeconds": 4700, "idleSeconds": 200, "sprintsCompleted": 2, "focusSessions": 3, "appSwitches": 5, "coinsEarned": 18, "longestFocusBlockSeconds": 840, "pausedSeconds": 0, "endReason": "user"},
      "... up to 9 more SessionView entries, newest first ..."
    ]
  },
  "onboarding": false
}
```

(`history` is shown abbreviated above — first entry, an elided placeholder,
last entry — to keep this example readable; the wire always sends exactly 30
real `DayStat` objects, never a placeholder string.)

Field notes the implementers must not improvise on:

* `activeState` — `"coding" | "idle" | "onBreak"`. Exactly these three
  strings, lowerCamel. Drives the developer frames, the terminal behaviour and
  both mood dots (§ art-direction "Visual states").
* `activityLine` — the **only** machine-derived string in the payload. ADR 0009
  mapped app identity, never a window title.
* `sprint.progress` / `sprint.target` — the raw work numbers, not a percentage;
  the frontend renders both the bar and the `34 / 75 units` text from them.
* `sprint.index` — the static-list index; the frontend uses it only for
  deterministic ticker selection (§3.1).
* `screenLines` — always exactly **11** strings, oldest first, newest last.
  The backend owns the scroll; the frontend just paints an array. That keeps
  the scroll deterministic and screenshot-reproducible.
* `tickerLines` — always exactly **3** strings, newest first.
* `ownedTints` — `"<itemId>:<tintId>"`. An item's `defaultTint` is implicitly
  owned the moment the item is and need not appear here; the frontend treats
  `tint == item.defaultTint || ownedTints.includes(...)` as owned.
* `equipped` has an entry for **every** slot, always. There is no null slot;
  empty is expressed by the slot's `*_none` item (see
  `docs/upgrade-design.md`).
* `storeOpen` is echoed back so a second client (or a reconnect) can tell
  progression is paused.
* `stats.today` / `stats.lifetime` — Analytics track (Phase A1, extended in
  A2), counts and durations only, never content (ADR 0002/0009). Both are
  the same `StatBlock` shape. Phase A1 fields: `keystrokes`,
  `mouseActiveSeconds`, `activeSeconds`, `idleSeconds`, `sprintsCompleted`.
  Phase A2 adds two more counts to that same shape: `focusSessions` — a
  completed sustained-typing block (ADR 0012, A2-design.md §3) — and
  `appSwitches` — a counted foreground-app change; countable only where the
  platform can observe the foreground app (**macOS** has a real active-app
  source; **Linux/Wayland** is honestly app-blind, ADR 0009, so it is a
  permanent `0` there). The count is still recorded and sent as-is with no
  special-casing; whether the client *displays* it now adapts to the
  platform's capability (ADAPTIVE-STATS, see `appIdentityAvailable` and the
  Activity-modal note below) so an app-blind platform never paints a
  misleading frozen `0` that reads as "you never switched apps". Seconds are
  whole seconds; the frontend
  formats them (`fmtDuration`/`fmtInt`), never the server. `stats` itself
  stays optional so a pre-A1 server degrades to an all-zero block.
  `pausedSeconds` (PR-5, `docs/production-runtime/MIGRATION_PLAN.md`
  §PR-5) adds a **third** time bucket alongside `activeSeconds`/
  `idleSeconds` — time spent with tracking stopped — never folded into
  idle: for any bucket, `activeSeconds + idleSeconds + pausedSeconds`
  equals the seconds that bucket's runtime was **awake, ticking and
  observing** — not its wall-clock uptime (amended per
  `docs/plan/BUGS-RESILIENCE.md` R8: every counter is per-tick, and neither
  a suspended machine nor a blind provider's tick lands in any of the
  three, so a day containing a sleep or a blind stretch sums to less than
  its wall-clock span). Optional client-side for the
  same stale-server reason as the rest of this shape.
* `stats.coinsToday` — optional `CoinBreakdown { keystrokes, mouse,
  focusSessions, appSwitches: number }` (A2, ADR 0012), all whole coin
  counts, content-free. The DevCash actually attributed to each signal
  **today**, split proportionally at each sprint payout (A2-design.md §5) so
  the four numbers always sum to today's earned DevCash. Coins still come
  from exactly one source, sprint completion (ADR 0008) — this is an
  attribution view of that one payout, never a second earning path.
  Optional client-side, matching the rest of `stats`.
* `stats.history` — optional `DayStat[]` (A3, ADR 0013). **Server-built and
  dense**: exactly `HistoryRetentionDays` (**30**) entries, one per local
  calendar date from `today-29` to `today` inclusive, **ascending**, and the
  **final entry is today, live** (it grows through the day as `statsToday`
  does). A date with no recorded activity is zero-filled, never omitted — the
  array is always date-complete, so the client does no gap/date arithmetic.
  Each `DayStat` is `{ date, keystrokes, mouseActiveSeconds, activeSeconds,
  idleSeconds, sprintsCompleted, focusSessions, appSwitches, coinsEarned,
  isActive, longestFocusBlockSeconds, pausedSeconds }` — the same seven
  A1/A2 counters as `stats.today`/`stats.lifetime`, plus `coinsEarned`
  (that day's total, the same sum `coinsToday`'s four fields add to) and
  `isActive` (a **bool set by the server**, never re-derived client-side:
  `activeSeconds >= game.ActiveDayMinSeconds`, default **300s** — the
  client and the streak banner must agree on one "active day" definition,
  so the client only renders this flag). `longestFocusBlockSeconds` is
  Fork B of A3-design.md §0 (shipped by default) — the day's longest
  single sustained-typing run. `pausedSeconds` is PR-5's addition to this
  same shape (see `stats.today`/`stats.lifetime` above) — that day's total
  paused time, optional for the same reason `longestFocusBlockSeconds` is:
  a day bucket predating PR-5 landing may omit it. camelCase throughout;
  `history` stays optional so a stale (pre-A3) server
  degrades to "no history" rather than crashing, matching the existing
  `stats`/`coinsToday` optionality pattern.
* `stats.streak` — optional `StreakView { current, longest }` (A3, ADR 0013).
  **Server-computed only** (`internal/game`, A3-design.md §2) — the client
  renders these two integers verbatim and must never re-derive them from
  `history`, because a streak can outlive the 30-day retention window that
  `history` is limited to. `current` is the length of the active-day run
  ending today (0 once the run is dead — last active day ≥2 calendar days
  ago); `longest` is the running all-time max and never decreases. Optional
  client-side, matching the rest of `stats`.
* The Activity modal (`features/activity-modal.ts`) is a read-only render of
  `stats`, laid out as **two tabs** (BUG-8). The old single box stacked
  today + lifetime + the coins breakdown into ~14 rows and got scroll-clipped
  under the browser's ~362px `dialog:modal` height cap; splitting it gives
  each set room to breathe:
  * **TODAY** tab (`#activity-tab-today`) — today's stats
    (keystroke/mouse/active/idle/sprints, plus a **Focus sessions** row
    `today.focusSessions` and an **App switches** row `today.appSwitches`)
    **and** the **"Coins earned today"** block reading `stats.coinsToday` as
    four `label → count` lines (Keystrokes, Mouse, Focus sessions, App
    switches) — coins belong with today.
  * **LIFETIME** tab (`#activity-tab-lifetime`) — the same seven stat rows
    for `lifetime`.

  Counts render with `fmtCount` ("88.4k"), durations with `fmtDuration`
  ("3d 4h") per §14; coin deltas stay exact via `fmtInt`. Nothing is
  formatted server-side.

  **Tabs (UX).** Two header buttons (`#activity-tab-today-btn`,
  `#activity-tab-lifetime-btn`) with `role=tab` in a `role=tablist` toggle
  the two `role=tabpanel` panels. The selected state is driven by
  `aria-selected` (the CSS styles the gold selected fill/underline off that
  one attribute — one source of truth for accessibility and appearance).
  **Only the active panel is in flow** — the inactive one carries the
  `hidden` attribute — but `renderActivity()` writes **both** panels' values
  every ~1Hz broadcast regardless, so switching is instant with no stale
  flash and the away/adaptive hide rules stay applied per tab. Switch by
  **click** on a header or **Left/Right arrow** (Left → TODAY, Right →
  LIFETIME) via `handleKeydown`; the input-focus + modifier guards upstream
  in `keybindings.ts` (`isTextEntryTarget` / `hasModifier`) still gate it. A
  **roving tabindex** keeps Tab landing on the selected header. The active
  tab is remembered across opens **within the session** (a module var) and,
  best-effort, across restarts via `localStorage` under
  `dexel.activity.tab` — every access is `try/catch`-wrapped so a
  private-mode throw or cleared store just falls back to the TODAY default,
  never breaking the modal. A click on a tab **header** lands inside
  `#activity`'s bounding rect, so the rect-based click-away helper
  (`enableClickAwayDismiss`) treats it as "inside" and does **not** dismiss;
  only a click on the backdrop beyond the panel closes, and Esc still closes
  natively.

  **ADAPTIVE-STATS — the three app-derived rows adapt to platform
  capability.** App switches are only countable where app identity is
  observable (see `appIdentityAvailable` below), so the modal hides an
  app-switch row exactly when the current platform is app-blind **and that
  row's own value is `0`** — this is the same display-only spirit as the
  §11.3 away-rows hide (recording is untouched; the counts still arrive on
  every broadcast), but deliberately narrower in one respect: **history is
  preserved**. `appSwitches` is cumulative, so a user who ran dexel on macOS
  (real app data) and then moved to Linux still has a real, non-zero
  **lifetime** total from those macOS days — that value *was* observed and
  is not a lie, so it is still shown even on the app-blind platform. The net
  effect: on macOS all three rows always show (including an honest real
  `0`); on Linux the TODAY App-switches row and the App-switches coin line
  hide (a misleading current-platform `0`), while a real LIFETIME total
  survives and a genuinely-never-observed lifetime `0` hides too. The three
  rows carry id hooks `#activity-row-today-app-switches`,
  `#activity-row-life-app-switches`, `#activity-row-coins-app-switches` for
  the whole-row (label included) hide, exactly like the away rows.
* `appIdentityAvailable` — ADAPTIVE-STATS. A server-computed `bool`
  capability bit (Go: `StateMessage.AppIdentityAvailable`, sourced verbatim
  from `activity.Snapshot.AppIdentityAvailable` through
  `engine.TickResult`): whether **this host's** provider can observe the
  foreground application at all — `true` on macOS with a real active-app
  source, `false` on Linux/Wayland (ADR 0009). It is content-free by
  construction (a bool **about the provider**, never about the user — the
  same not-a-privacy-concern shape as `paused`) and is on the content-free
  allow-list with that citation. The Activity modal reads it for the hide
  rule above; the personalized `activityLine` already degrades on the same
  underlying capability. Optional client-side (`wire.ts:
  appIdentityAvailable?`); an **absent** field degrades to `!== false` →
  "assume available, show the rows" — the pre-existing behaviour, so a stale
  client is never made worse, and rows are only ever hidden when the server
  *explicitly* reports the platform app-blind.
* `config` — Phase P1 (Identity & first minutes,
  `docs/plan/PRODUCT-EVOLUTION.md` §5), extended by SET-1 (§11).
  `config.name`: the Dexel's name, **user-authored**, `""` when unset. The
  server always sends the block; it is optional client-side
  (`wire.ts: config?`) only so a pre-P1 server degrades to "unnamed".

  SET-1 adds two **preferences**, both `bool` and both defaulting to
  `false` — which is also what an absent field means, so a pre-SET-1
  server degrades to exactly the right thing (an unpinned window, away
  time private):

  * `config.alwaysOnTop` — whether the **desktop shell** pins its window
    above other windows. Nothing on this page consumes it; the shell reads
    it from `dexel status --json` (§11.5). It rides this block anyway so
    the Settings modal can render the toggle's position from *server*
    truth like every other control, never from a value it remembered
    locally.
  * `config.showAwayTime` — whether the Activity modal **displays** its
    two AWAY rows (§11.3). This is a **presentation** switch and nothing
    else: `stats.*.idleSeconds` is recorded exactly as before and is sent
    on every broadcast whatever this says. Hiding is the client's
    rendering decision, driven by this field. A counter that stopped
    counting when hidden would make every total derived from it silently
    wrong, which is the dishonesty ADR 0010/0013 forbid — so recording is
    deliberately untouched.
  * `config.soundEnabled` — whether the page plays its six chiptune sound
    effects (§13). Also purely a **client** instruction: nothing on the
    server makes a sound, branches on this field, or records anything
    differently because of it — a muted dexel earns and counts exactly what
    a noisy one does.

    **This is the one field in this block whose default is `true`**, which
    changes how a client must read it. `alwaysOnTop`/`showAwayTime` are
    typed optional and `!!undefined` is the right degradation for both
    (window unpinned, away time private). For `soundEnabled` the right
    degradation is `!== false`: an absent field means *the user has never
    chosen*, and the honest render of never-chosen is the default. A client
    that read it as `!!undefined` would silently mute itself and paint an
    `OFF` toggle the user never touched. On disk the same distinction is
    carried by a **`*bool`** (`store.ConfigData.SoundEnabled`): absent,
    `true` and `false` are three states, and collapsing them to two is
    exactly what would turn a deliberate mute into an accident.

  All three live in the same `~/.config/dexel/config.json` as the name (ADR
  0014's config side), are written by `SET_PREF` (§6.2) and are freely
  hand-editable.

  This is the **one free-text string anywhere on this wire**, and it is
  legitimate for a reason that must not be generalised: it lives on the
  *editable* side of SEC-1's config/state split (`~/.config/dexel/config.json`,
  unsigned and hand-editable), never in the protected HMAC'd save at
  `state.db`. ADR 0014 states the category distinction outright — "the
  user-authored name is a *different category* from observed activity — data
  the user deliberately writes about their own pet, not surveillance of their
  work". OBSERVED content (a window title, typed text, a path, a keycode) is
  still forbidden here and still fails
  `internal/game/content_free_test.go`'s allow-list, which gained `Config`
  and `Onboarding` entries — and one extra test asserting the guard still
  bites — in the same change.

  Validation is entirely server-side (`game.NormalizeName`): control
  characters dropped, surrounding whitespace trimmed, truncated to **24
  runes** (`game.MaxNameLen`, counted as runes so a multi-byte name is never
  cut mid-character), and an empty result rejected. The client's own
  `maxlength`/trim are a courtesy, never the check.
* `sessions` — Phase P2 (Sessions, `docs/plan/PRODUCT-EVOLUTION.md` §3 Bet 1,
  ADR 0017, `docs/plan/P2-design.md` §6.1). One nested block, **always sent**
  (the same `config` precedent: the server always sends the block, it may be
  empty); typed `sessions?` in `wire.ts` so a stale frontend degrades to "no
  sessions" rather than breaking. `SessionsView { active, summary, recent }`:
  - `active` — `null` when no session is running, otherwise an
    `ActiveSessionView` carrying the seven counters **flattened**, mirroring
    `DayStat`'s shape, computed live as `watermark − baseline` (a delta of
    lifetime, never a re-implementation of `recordStats`), plus
    `coinsEarned` and `longestFocusBlockSeconds` — **per-session
    accumulators**, not deltas, because neither has a monotonic lifetime
    counter to subtract from (P2-design §2.3). `elapsedSeconds` is
    **server-computed**; the client never derives live time. `pausedSeconds`
    (PR-5, `docs/production-runtime/MIGRATION_PLAN.md` §PR-5) joins
    this same delta set: a running session's counters freeze while
    `state.paused` is true (no ticks land), and this is the session's own
    accrued paused time so far — non-optional in `wire.ts` (unlike the
    `stats`/`history` fields above), since every PR-5-era server always
    emits it on every session view it sends.
  - `summary` — `SessionsSummary { completed, thisWeek,
    longestSessionSeconds }`, all server-computed over the **whole verified
    log**, not just the wire window below.
  - `recent` — up to `SessionsWireWindow` (**10**) finished `SessionView`
    entries, newest first. Storage is unbounded; only the wire is windowed.
  - Every field across `ActiveSessionView` / `SessionView` / `SessionsSummary`
    is a count, a duration, an ISO timestamp, an integer id, or a closed
    three-value `endReason` enum (`user | idle | maxDuration`) — **except
    `name`**, the one user-authored string, allow-listed on ADR 0014's
    category citation exactly as `config.name` was (§2.7 of P2-design): a
    session name is CONFIG, never a `state.db` column, and is looked up by
    the server from `config.json`'s `sessionNames` map, keyed by `id`.
* `paused` — PR-5 (`docs/production-runtime/MIGRATION_PLAN.md` §PR-5).
  `true` while tracking is stopped: `provider.Stop()` has been called,
  `eng.Tick()` is not invoked, and there is no accrual and no analytics
  tally for as long as it stays true. **`activeState` does not gain a
  fourth value for this** (ADR 0010) — pausedness is conveyed only via
  this bool, never by inventing a mood string. Optional client-side for
  the same stale-server reason as `onboarding`/`sessions` below: a
  pre-PR-5 server sends no `paused` field at all, which must degrade to
  "not paused". Drives §2.4's PAUSED chrome (the status line, the mood
  dot, the titlebar badge) and the menu's `[P] PAUSE`/`[P] RESUME` entry.
  A running session **survives** a pause — see `sessions.active.pausedSeconds`
  above.
* `onboarding` — Phase P1. `true` **only** in the genuine first-launch state,
  decided **once, by the server, at boot**, as *(no save of any kind existed)*
  `&&` *(`config.name` is empty)*. Both halves matter: an existing
  `state.db`/`state.json`/legacy Rust save means somebody has played here even
  if they never named the Dexel, and a named `config.json` means the one
  question onboarding asks is already answered even on a machine whose save was
  wiped. A tampered, future-schema or unreadable save all count as "a save
  existed" — the failure worth avoiding is nagging a returning user, not
  missing an intro. `SET_NAME` clears it for good; nothing sets it again for
  the life of the process, and **no client action can ever set it**. Optional
  client-side, defaulting to false. See §7.
* The **`[H] HISTORY`** modal (`features/history-modal.ts`, A3, ADR 0013) is
  a second read-only render, opened the same way `[A]`/`[S]` are — native
  `<dialog>`, its own titlebar button, the `H` key, `Esc` or a click-away
  (§5.4) to close — and gates nothing (no earning to freeze, same as
  Activity). Rect: `#history`
  at `left:40 top:44 width:560 height:312` (the store's wide footprint,
  centered in the 640x400 viewport — charts need horizontal room the
  360x396 Activity modal does not have). Contents, top to bottom: a
  **streak banner** (`CURRENT STREAK: N DAYS   LONGEST: M`, both numbers read
  verbatim from `stats.streak`, `N` in `var(--gold)`); a **7-day bar chart**
  of the last 7 `stats.history` entries (default metric `activeSeconds`),
  one CSS bar per day — an integer-`height` `<div>`, flat `background:
  var(--screen)`, no canvas, so no anti-aliasing mush at this size/DPI (the
  A2 `→`-glyph lesson) — with ASCII day-initial labels (`M T W T F S S`); a
  **30-day month strip** of 30 square cells, one per `stats.history` entry,
  lit `var(--screen)` when that entry's `isActive` is true and
  `var(--shadow)` when not, today's cell outlined `var(--gold)` — the streak
  made visible; and two **client-derived insights**, pure `max`-reductions
  over the already-sent `stats.history` array (the client asserts no state
  the server did not send, same class as `fmtDuration`): `BUSIEST DAY:
  <date>` (max `activeSeconds`) and `LONGEST FOCUS: Xm Ys` (max
  `longestFocusBlockSeconds`; falls back to `BEST FOCUS DAY: <date> (K)`, max
  `focusSessions`, if Fork B's field is absent). All values render with
  `fmtInt`/`fmtDuration`; nothing is formatted server-side. `render/*` and
  `state/store.ts` are untouched — the modal reads `store.getState().stats`
  directly, exactly as `activity-modal` does.

**`sessionComplete`** — Phase P2 (§9.6). Sent to every connection immediately
after the `state` broadcast that cleared the session (never in its place):

```json
{ "type": "sessionComplete", "v": 1, "session": { …one SessionView… } }
```

`session` is exactly one `SessionView` — the same shape `sessions.recent`
carries, verbatim. **Why a dedicated message rather than reusing `flash`:**
§6.2's "no dedicated ack" rule still governs the ordinary flash+state
pattern, but a flash alone would force the client to *infer* which
`sessions.recent` entry just ended, and inference is exactly what this
contract forbids — the client never asserts state the server did not send.
One explicit message keeps the server the sole source of truth, and gives a
later phase a single clean event to hook (P2-design §3.1/§3.3). A discarded
short session (< 60 s, P2-design §2.2) produces **no** `sessionComplete` and
**no** card — only the warm flash below.

**`flash`** — a transient toast for a discrete event.

```json
{"type": "flash", "kind": "purchase", "text": "Racer chair!"}
```

`kind` ∈ `purchase | equip | sprint | error | welcome | session`. Rendered
for **1.5 s** as an 8px line, `var(--gold)` for
`purchase`/`sprint`/`welcome`/`session`, `var(--cream)` for `equip`,
`var(--pot)` for `error`. Position: over `#store-cash` while the store modal
is open, otherwise over `#sprint-name`. Only one flash at a time; a new one
replaces the old.

* `welcome` is Phase P1's one-off `Hello, <name>!` on a successful
  `SET_NAME` (§7) — gold, with the celebrations, not the acknowledgements.
  The greeting is composed **server-side**, like every other flash text; the
  frontend never assembles a sentence (§3).
* `session` is Phase P2's flash, sent alongside `SESSION_START` (`"Session
  started."`), a normal `SESSION_STOP` (composed server-side, e.g. `"auth
  refactor — 1h 24m together."`, or `"Session complete — 1h 24m together."`
  when unnamed — paired with `sessionComplete` above), and a discarded short
  session (`"Session ended — too short to keep."` — a warm flash, never a
  scold). Gold, joining `purchase`/`sprint`/`welcome` in that CSS group
  (P2-design §3.1).
* **`#flash` paints an opaque `var(--metal)` fill.** "Over `#sprint-name`"
  above always meant *covering* it, but the toast was transparent, so it
  composited INTO the text underneath and the two interleaved glyph-by-glyph
  into unreadable mush. Found by P1's real-running-game gate on
  `Hello, Pixel!` over `SPRINT: Fix Bug #404`, and confirmed pre-existing —
  a plain `kind: equip` toast (`Equipped Classic Pullover!`) mangles
  identically. Position, size, font and colours are unchanged; only the
  backdrop is now opaque, so "over" means what this section says.

### 6.2 Client → server

```json
{"action": "BUY_ITEM",   "itemId": "chair_racer"}
{"action": "BUY_TINT",   "itemId": "chair_racer", "tintId": "neon"}
{"action": "EQUIP_ITEM", "slot": "chair", "itemId": "chair_racer", "tintId": "neon"}
{"action": "STORE_OPEN"}
{"action": "STORE_CLOSE"}
{"action": "SET_NAME",   "name": "Pixel"}
{"action": "SET_PREF",   "key": "alwaysOnTop", "value": true}
{"action": "SESSION_START", "name": "auth refactor"}   // name optional
{"action": "SESSION_STOP"}
{"action": "PAUSE"}
{"action": "RESUME"}
```

* **No dedicated ack.** Every successful action is answered by an immediate
  `state` broadcast plus a `flash`; every failure by a `flash` of kind `error`
  and no state change. This keeps the frontend a pure function of `state` — a
  render path that never has to reconcile an optimistic local edit with a
  broadcast.
* The server validates everything: unknown `itemId`/`slot`/`tintId`, an item
  whose slot does not match, buying something already owned, equipping
  something not owned, and affordability. All of them are `flash: error`, never
  a panic and never a partial write.
* `EQUIP_ITEM` with a `tintId` the player does not own is rejected — equipping
  is not a back door around `BUY_TINT`.
* `SET_NAME` (Phase P1, §7) sets the Dexel's name. `name` is **raw user
  text** and passes through exactly one door, `game.NormalizeName` (see
  `config` in §6.1 for the rules). A rejected name — empty, whitespace-only,
  control-characters-only, or the `name` key missing entirely — is a
  `flash: error` and **no state change at all**: not the name, not the
  `onboarding` flag. Anything that survives validation is stored, written
  through to `config.json` immediately (never on the 30 s autosave timer —
  naming is a one-shot first-minutes moment and must not be lost to a crash
  30 seconds later), and answered with a full `state` broadcast carrying the
  new `config.name` and `onboarding: false`, plus a `welcome` flash. If that
  config write fails, the in-memory name still stands so the session works,
  but the toast becomes a `flash: error` — a warm hello for a name that will
  silently not survive a restart is a lie.
  `SET_NAME` is not restricted to onboarding: a later Settings surface can
  rename with the same action and no new wire. **SET-1 is that surface**
  (§11.2) and it took that route exactly: the Settings modal's rename field
  sends this same action, with no second rename path and no new validation.
* `SET_PREF` (SET-1, §11.4) sets one user **preference**. `key` is
  validated server-side against `game.PrefKeys()`'s allow-list
  (`alwaysOnTop`, `showAwayTime`, `soundEnabled` today) and `value` is a
  **bool** — every
  dexel preference is an on/off choice, so a malformed value is refused by
  the JSON decode itself rather than re-checked per key. An unknown or
  missing `key` is a `flash: error` with **no state change**, so a client
  can never invent a preference or write an arbitrary field.

  **One keyed action, not one action per preference.** Preferences are the
  part of a UI that grows fastest and matters least individually; a
  per-preference action would add a wire literal, a server case and a
  frontend call per checkbox. A single keyed action with a strict
  server-side allow-list keeps the wire flat without loosening validation.

  **No flash on success** — the `PAUSE`/`STORE_OPEN` precedent: a
  preference is a state, not an event, so success is answered by the
  `state` broadcast alone and the toggle re-rendering from it *is* the
  feedback. Setting a preference to the value it already holds is a
  genuine server-side no-op (no broadcast, no second config write), the
  same idempotence `PAUSE` has. Like `SET_NAME`, a successful `SET_PREF` is
  written through to `config.json` **immediately**, and a failed write
  becomes a `flash: error` while the in-memory value stands — a setting
  that silently will not survive a restart is the same lie a lost name is.
  A future non-boolean preference is a deliberate wire change to make then,
  not a hole to leave open now.
* `SESSION_START` / `SESSION_STOP` (Phase P2, §9.6) — names **pinned**
  exactly as written. PRODUCT-EVOLUTION §2.1 wrote `SESSION_START`/
  `SESSION_END`; the imperative pair became `SESSION_START`/`SESSION_STOP` to
  match the UI's Start/Stop buttons and the `STORE_OPEN`/`STORE_CLOSE`
  verb-pair precedent — **STOP** wins for the action while the *data* keeps
  `endedAt`/`endReason` (a session has an end; the user performs a stop).
  `name` on `SESSION_START` is optional and passes through
  `game.NormalizeSessionName` — the same door `SET_NAME` uses, allow-listing
  the same ADR 0014 category, truncating to `MaxSessionNameLen` (**32**
  runes). Starting while a session is already active, or stopping with none
  active, is `flash: error` and no state change — exactly one session at a
  time. Both actions obey this section's contract in full: no dedicated ack,
  success answered by `state` + `flash` (and, on a non-discarded stop, the
  `sessionComplete` message, §6.1), failure by `flash: error` and no state
  change. `SESSION_RENAME` is deferred, named, not built.
* `PAUSE` / `RESUME` (PR-5, `docs/production-runtime/MIGRATION_PLAN.md`
  §PR-5) — no payload, same shape as the `STORE_OPEN`/`STORE_CLOSE`
  no-payload-action precedent above, and the **same no-flash precedent**
  too: pause is a state, not an event, so success is answered by an
  updated `state` broadcast alone — `state.paused` on the next broadcast
  is what the UI renders, never a toast. `PAUSE` calls `provider.Stop()`:
  no more accrual and no more analytics tally until resumed. `RESUME`
  calls `Engine.Reset()` + `provider.Start()`, so a stale pre-pause
  recency state (e.g. a focus-bonus run in progress) never carries across
  the gap. An already-paused `PAUSE` (or an already-running `RESUME`) is
  a genuine server-side no-op — no second provider stop/start, no
  redundant save — not an error. A running session **survives** a pause
  (§6.1's `sessions.active`) — it just stops accruing everything except
  `pausedSeconds` while paused. §2.4 covers the resulting chrome; §2.1
  covers the menu's `[P] PAUSE`/`[P] RESUME` entry.
* **[DESIGN CALL] camelCase field names throughout** (`itemId`, not the PDF
  blueprint's `item_id`). The whole payload is consumed by JavaScript; one
  casing convention across the wire and the DOM removes a whole class of typo.

## 7. The onboarding modal

Phase P1 — Identity & first minutes (`docs/plan/PRODUCT-EVOLUTION.md` §5,
§2.9). Shown **once, ever**, on a genuine fresh install: name your Dexel,
pick a starter colour, get a warm hello. Built to the §4 store modal's
mechanics (native `<dialog>` + `showModal()`, the shared `#scrim`, one
`'close'` event every dismissal path funnels through) — not a second modal
idiom.

Two things make it unlike every other modal here:

1. **Nothing opens it.** There is no launcher button and no key. It opens
   because `state.onboarding` is `true` and closes because a later `state`
   says `false` (§6.1). The client never decides that flag in either
   direction.
2. **It owns the app's first `<input>`**, which is a real keyboard hazard —
   see §5.2's bare-letter rule, which exists because of this screen.

It **gates nothing** (no `*_OPEN`/`*_CLOSE` action, unlike §5.3's store
gate). The store's gate exists to stop keyboard-driven shopping minting Dev
Cash; this modal can only ever be on screen *before any work has been
accrued at all* — a fresh install at 0 Dev Cash and 0 sprint progress — so
there is nothing to freeze. It also cannot spend: the starter colour is
applied with the existing `EQUIP_ITEM` against a tint the player already
owns, never `BUY_TINT`. No new economy path, no free cash.

### 7.1 Geometry

`#onboarding` at `left:84 top:80 width:472 height:240` — centered in the
640x400 viewport, and deliberately *much* shorter than §4's store: this is a
~30-second moment, not a browsing surface, and its four pieces (a warm line,
a portrait, a name, a colour) should read in one glance. Well under the
browser's 362px `dialog:modal` height cap. `padding: 12px`, `border: 4px
solid var(--wall-light)`, `background: var(--metal)`, so the abs-positioning
origin for children is the 464x232 padding box.

| Element | id | Rect (within the padding box) |
|---|---|---|
| Title (`A DEXEL MOVED IN`, `var(--gold)`) | `#onboarding-title` | 12, 10, 380x16 |
| Warm line 1 | `#onboarding-hello-1` | 12, 32, 440x10 |
| Warm line 2 | `#onboarding-hello-2` | 12, 44, 440x10 |
| Portrait | `#onboarding-preview` | 12, 62, 208x104 |
| Selected colour's name | `#onboarding-tint-name` | 12, 170, 208x10 |
| `WHAT IS ITS NAME?` | `#onboarding-name-label` | 236, 62, 216x10 |
| Name field | `#onboarding-name` | 236, 74, 208x20 |
| `STARTER COLOUR` | `#onboarding-colour-label` | 236, 104, 216x10 |
| Swatch row (6 chips, 16x16, 4px gap) | `#onboarding-swatches` | 236, 118, 216x16 |
| `MORE TO EARN IN THE STORE` | `#onboarding-colour-hint` | 236, 138, 216x10 |
| `SAY HELLO` | `#onboarding-confirm` | 236, 164, 120x24 |
| `SKIP` | `#onboarding-skip` | 372, 164, 64x24 |
| Footer help | `#onboarding-help` | 12, 212, 440x10 |

**The `#scrim` IS used here** — unlike §4.5's deliberate "watch the scene
change live behind you" call for the store. Nothing is applied to the scene
until `SAY HELLO`, so there is nothing live to watch; the colour feedback
lives inside the modal instead.

**The portrait is the real developer sprite stack**, not a thumbnail: the
same three layers (`dev_form_<frame>` tinted, the hoodie style overlay,
`dev_base_<frame>`), the same z-order and the same **2x** scale
`#scene-sprites` renders at, cropped by the box's `overflow: hidden` to
sprite-local `x 44..148, y 5..57` — hands, hood and shoulders, stopping just
above local `y 58` = room row 150, which is exactly where chairs start
painting, so the crop looks complete with no chair drawn. The frame is fixed
to `idle` (the `mouse` pose reaches local `x 190` and would clip; and a
portrait changing pose mid-decision is noise). The box needs
`isolation: isolate`, like any box holding a `.tint-shade`.

> **[DESIGN CALL] the portrait, not the thumbnail.** The first build showed
> the 40x40 tintable hoodie *thumbnail* at 2x here. Rendered in the real
> running game it read as a purple dome, not a garment: a store card gets
> away with that thumbnail because it sits next to the item's name in a list
> of garments, and this modal has no such context. Showing the Dexel
> *wearing* the colour is both legible and the actual point of the screen.

### 7.2 The swatch row, and what a fresh install actually owns

Chips are `.swatch` — the same class §4's store cards use, at 16px instead of
10px, so the `.selected` outline and the `.unowned` diagonal slash come for
free. One chip per catalog tint, **in catalog order**; the frontend hardcodes
nothing about the tint table.

Ownership is read from the server (`state.ownedTints` plus the item's
implicit `defaultTint`). An unowned colour is rendered **shown, slashed and
inert** — not hidden, and never a clickable-looking locked door. Its
`title` names its store price.

> **[DESIGN CALL] / OPEN QUESTION FOR THE OWNER.**
> `PRODUCT-EVOLUTION.md` §2.9 assumes a starter pick from "the six free
> tints the wardrobe already grants on first launch". **The catalog does not
> grant six.** Every entry in `internal/game/catalog.go`'s `catalogTints`
> costs 40, and `GrantTierZeroDefaults` grants tier-0 *items*, not tints —
> so on a real fresh install exactly **one** chip is pickable
> (`indigo`, `hoodie_classic`'s own `defaultTint`) and five are locked.
> Rather than invent a grant (a new economy path, explicitly out of scope
> for P1), this ships honest: all six shown, one pickable, and an explicit
> `MORE TO EARN IN THE STORE` line so the row reads as a wardrobe barely
> started instead of a broken picker. **If the owner wants a real choice at
> first launch, the fix is one catalog decision — make some or all of
> `catalogTints` price 0, or grant a starter set in
> `GrantTierZeroDefaults` — and this modal lights up with zero frontend
> change.**

### 7.3 Confirm, skip, and why skipping sets a name

| Trigger | Effect |
|---|---|
| `SAY HELLO` / `Enter` | `EQUIP_ITEM` for the chosen colour (only if it differs from what is worn), then `SET_NAME` with the typed name |
| `SKIP` button | `SET_NAME` with `"dexel"` |
| `Esc` (native `<dialog>` close) | identical to `SKIP` |

Both dismissal paths converge on the dialog's single `'close'` handler, which
sends the skip if no name was submitted — so any future exit route inherits
the behaviour rather than needing its own wiring.

> **[DESIGN CALL] skipping SETS the name `"dexel"`; it does not leave the
> flag up.** The alternatives are both worse. Leaving `onboarding: true`
> re-opens the modal on the very next 1 Hz broadcast — a nag loop. Letting
> the client suppress it locally forever is the client asserting state the
> server never sent, which §6.1's whole contract forbids. Naming the Dexel
> `"dexel"` is the only option that is both honest and quiet: the user opted
> out of *choosing* a name, not into being asked again — and `SET_NAME`
> already exists for renaming later. `"dexel"` is pinned on both sides
> (`game.DefaultName`, and `SKIP_NAME` in `features/onboarding-modal.ts`)
> with a Go test asserting the literal and that it survives validation.

An empty typed name falls back to `"dexel"` client-side before sending, so
the client never sends something it knows the server would reject (which
would leave `onboarding: true` and re-open the modal). Between sending
`SET_NAME` and seeing the confirming `state`, auto-open is suppressed by a
one-shot flag — the same class of loop guard as `ws-client.ts`'s
`storeReassertSent`, and it suppresses an *action*, never a render.

### 7.4 Where the name is echoed

The name's primary home is the **personal status line** (§2.3): once the dexel
is named, the server composes `#status-line` as `"Pixel is coding in VS Code"`,
`"Pixel is in the zone"`, `"Pixel is thinking…"`, `"Pixel is on break"`. This
is a SERVER-composed string carried on `state.activityLine` and rendered
verbatim by `render/chrome.ts` — the frontend never concatenates the name
(§3's zero-client-side-assembly rule). Seeing your named dexel in a *sentence*
that describes what it is doing is the point; a floating bare label is not.

The name also appears, unchanged, in:

| Where | id | Behaviour |
|---|---|---|
| Settings modal, `CURRENTLY` line | `#settings-name-current` | The server's current name, as typed, truncated to `MaxNameLen` — rendered separately from the draft input so nothing claims a name the server did not send (§11). |
| Titlebar session pill | `#session-pill-text` | The active session's name (a session name, not the dexel's), truncated to 16 with `…` (§9.5). |

**Removed** (this was redundant once the name became meaningful inside the
status-line sentence):

* the always-on bare **`#status-name`** strip at the bottom of the status
  panel — element, its render, and its CSS are gone;
* the name-swapped **`#menu-panel-title`** — the hamburger heading is static
  `MENU` again, because a menu is a menu.

> **[DESIGN CALL] the name is a sentence, not a label.** P1's exit criterion
> said the name is "echoed in the HUD/titlebar" and shipped it as two bare
> labels; the owner found both odd ("name is like name"). The name now earns
> its place by being *part of the true status sentence* instead. The top-left
> titlebar cluster stays **coin-then-level only** (§2.1, BUG-2), untouched by
> this.

### 7.5 Verified in the real running game

Per `feature-build-and-verify`'s gate, against the built binary with the fake
provider and temp `$HOME`s: the modal appears on a fresh install; typing
`sasha mh` into the name field opens **no** modal (§5.2's guard) while `[S]`
still opens the store the moment nothing is focused; a locked swatch cannot
be selected; confirming closes the modal, shows the `welcome` flash and
echoes the name in the personal status line and the Settings `CURRENTLY`
line; `config.json` holds the name and `state.db`
does not contain it anywhere; restarting the same `$HOME` shows no
onboarding; a fresh `$HOME` whose `config.json` was named *before* first run
shows no onboarding; an existing `state.db` with an unnamed `config.json`
shows no onboarding; and `Esc` sets `"dexel"` and never re-opens.

## 8. Deferred, and why

Named explicitly so nobody re-derives them as missing features.

* **The AI agent layer** — Ollama/OpenAI clients, AI sprint summaries, the
  "AI PARTNER" header widget, the AI chat modal, the monitor showing an AI
  dialogue. Deferred indefinitely: it needs window titles and keystroke
  *content* to summarise anything, which ADR 0002 and ADR 0009 forbid at the
  source, and a network LLM breaks ADR 0001's offline-first promise. If it ever
  returns, it returns as a local-only feature with its own ADR.
* **Meeting / mic detection** — the "Meeting Dungeon", noise-cancelling
  headphones state, `IOAudioEngineState`. Deferred. The hook is small when it
  comes: a fourth `activeState` value `"meeting"`, one developer frame pair, and
  one headset overlay sprite.
* **Tamagotchi meters** — Energy, Focus, Hunger, Social Battery, feeding,
  death. **Banned**, by the user's own genre lock: pure idle companion, zero
  maintenance, nothing to keep alive.
* **The three header widgets and the isometric workshop** — banned, per §2.
* **Audio** — the mockups draw a speaker icon at the ticker's edge. There is no
  audio, so there is no icon.
* **Sprint-name ambiguity** — see `docs/upgrade-design.md` §"Sprint naming":
  sprint titles are game flavour, presented as the character's project, and are
  never allowed to imply they describe the user's real work.
* **Resizing, multiple windows, and any responsive behaviour** — out of scope.
  640x400 fixed.
* **A Settings modal** was deferred here and by ADR 0014's own closing note
  ("surfacing the name in the UI (a Settings modal + a WS field) is a
  follow-up feature — SEC-1 only provisions the config slot"). **SET-1 built
  it** — see §11. Still deferred from it, and named so nobody re-derives
  them: a theme picker, an audio section (there is no audio, §8 above), and
  any preference that would need a non-boolean `SET_PREF` value.

## 9. The Sessions modal

Phase P2 — Sessions & the session-complete moment
(`docs/plan/PRODUCT-EVOLUTION.md` §3 Bet 1 / §5 Phase P2, ADR 0017,
`docs/plan/P2-design.md`). A session is a user-declared work interval:
start it (optionally named), work, stop it, and Dexel hands back a cozy
summary card — *"here's what we did together."* It grants nothing economic
and gates nothing: it is a **lens** over tracking that already happens
(P2-design §1), never a second earning path, and never a hold on the
`ws-client` connection the way `STORE_OPEN` is.

### 9.1 Opening it

- **Menu entry:** `#sessions-open` (`nes-btn menu-item`, label
  `[W] SESSIONS`), placed in `#menu-panel` after `#history-open` (§2.1). No
  `features/menu.ts` change is needed — the panel's CSS rule targets the
  `.menu-item` class, not an id list, so it auto-grows around any new entry.
- **Keybinding `[W]`** ("work session") on the main screen (§5.2).
  `S`/`Tab`/`A`/`H`/`M` are already taken; `W` is free.
- Opened the same way `[A]`/`[H]` are: native `<dialog>` + `showModal()`, the
  shared `#scrim`, `Esc` handled natively (never intercepted).

### 9.2 Geometry

`#sessions` is a `nes-dialog is-dark`, `position: fixed; left: 80px; top:
50px; width: 480px; height: 300px;` — under BUG-8's ~346px UA
`dialog::backdrop` `max-height` cap, and centred by the `(400 − h) / 2`
convention `#activity`/`#history` already establish. Ids: `#sessions-title`,
`#sessions-close`, `#sessions-help` (content `W / ESC CLOSE`, the same help-
line idiom `#activity-help`/`#history-help` use). Repeated content uses
`.sessions-*` classes plus the shared `.label`/`.value` (+ `.value.gold`)
idiom already established by `#history-streak`. ASCII only (`->`, never `→`
— the A2 lesson recorded at §3.2); any date string uses `parseLocalDate`-
style parsing, matching `#history`'s own convention.

### 9.3 The three views

Switched by which one the server's state makes true — **never** by a
client-held mode:

1. **Idle** (`sessions.active == null`) — a name input (`#sessions-name`,
   `maxlength="32"`, `autocomplete="off"`, `spellcheck="false"`, focused on
   open, the same shape as `#onboarding-name`) + a `START SESSION` button,
   then the `recent` list (one line each: date · name · duration · keys ·
   focus blocks, newest first, up to the wire's 10), then the summary line
   (`SessionsSummary`, e.g. `SESSION 27 · 4 THIS WEEK`).
2. **Live** (`sessions.active != null`) — the name (or `unnamed session`),
   elapsed, and the effort-so-far counters, redrawn on every 1 s `state`. The
   one action button reads `END SESSION`, or `CANCEL SESSION` while
   `elapsedSeconds < 60` — the label flips on the **server-sent** number,
   the same one-action-button precedence idea §4.3 uses for the store.
3. **Summary card** (§9.4), shown by `showSummary(session)` on
   `sessionComplete` (§6.1) and dismissed by `[ NICE ]` / `Esc`.

**Module surface** matches the existing feature modules exactly: `isOpen()`,
`renderSessions()`, `refreshIfOpen()`, `open()`, `close()`,
`handleKeydown(e)`, plus `showSummary(s: SessionView)`. State is **pulled**
from `state/store.ts` (`store.getState()`), never pushed — the modal reads,
it is never told.

### 9.4 The session-complete card

Rendered as a panel **inside this modal**, which auto-opens on
`sessionComplete` — not a fifth `<dialog>`. It reuses the modal already
needed, and a modal is the expected response to a button the user just
pressed. Content, in this order (largest first, cozy phrasing, no
percentages, no scores, no "efficiency"):

```
        SESSION COMPLETE
        auth refactor                      ← omitted entirely when unnamed
        1h 24m                             ← duration, large
        ------------------------------------
        TYPED        4,182 keys
        FOCUS        3 blocks   BEST 14m
        SPRINTS      2 finished
        COINS        +18 earned during it
        ------------------------------------
        SESSION 27  ·  4 THIS WEEK
                              [ NICE ]
```

- Every number comes from the message's `session` object verbatim; the
  client only formats (`fmtDuration`, `fmtInt` — the existing utils).
- `COINS` is honest about its verb: coins were **earned during** the session
  by the ordinary sprint payout. The card must not imply the session paid
  them.
- No target, no comparison, no "you were X% focused", no red anything. A
  zero row renders as a zero, not as a failure.
- `[ NICE ]` closes the card back to the live view; `Esc` does the same
  (native `<dialog>`, never intercepted).
- A discarded short session (< 60 s, §6.1) produces **no card** — only the
  warm flash.

### 9.5 The always-visible indicator

A session the user cannot see from the main screen is a feature they forget
is running. `#session-pill` sits in `#titlebar` at `left:132 top:8 width:240
height:8`, 8px font — the titlebar is empty between `#hud-level` (ends 120)
and `#menu-open` (starts 600, §2.1), so nothing else moves. Content: an 8px
solid gold square (`border-radius: 0` — the `#mood-dot` lesson) followed by
the name truncated to 16 chars with `…`, then `H:MM:SS`. **Empty, and
therefore invisible, when no session is active** — an "empty text"
idiom. Owned by `render/chrome.ts` (a render-layer
module: reads state, sends nothing).

### 9.6 Actions and messages

Covered in full at §6.1 (`sessionComplete`, the `sessions` state block, the
`session` flash kind) and §6.2 (`SESSION_START`/`SESSION_STOP`). Summarised
here for the modal's own reference:

```json
{"action": "SESSION_START", "name": "auth refactor"}   // name optional
{"action": "SESSION_STOP"}
```
```json
{ "type": "sessionComplete", "v": 1, "session": { …one SessionView… } }
```

Alongside `sessionComplete`, the ordinary gold flash `flash{kind:"session",
text:"…"}`, text composed **server-side** — the frontend never assembles a
sentence (§3).

### 9.7 What the modal deliberately does not do

- **Gates nothing.** No `SESSION_MODAL_OPEN`/`*_CLOSE` action, no
  `ws-client` hold — same as the Activity/History/onboarding modals (§7).
  `setStoreOpenHoldDesired` stays store-specific by name and is untouched.
  Typing a project name earns a few crumbs of ordinary keystroke work while
  the modal is open (bounded at ~0.26 work units per full-length name,
  landing outside the session's own numbers since the baseline is taken at
  start) — named as a residual risk in P2-design §9, not fixed here.
- **Does not appear in the `[H]` History modal.** `[H]` is per-**day**
  analytics; a sessions row there would duplicate this modal for no gain,
  and a later phase is explicitly where sessions, moments and days merge
  into one scrapbook.
- **`SESSION_RENAME`, session goals/targets/quotas, tags/categories, and
  exporting a card** are all deferred, named, not built.

---

## 10. Scene animation — the character's frames (Phase P3, "Character life")

Everything below is **presentation only**. `render/scene.ts` owns it, it sends
no `ClientAction`, it reads nothing but `activeState` and
`stats.today.mouseActiveSeconds` off the state the server already sent, and it
adds **no wire field**. Per PRODUCT-EVOLUTION.md §2.6 Dexel is alive *without
simulation mechanics*: there is no meter, no need, no decay, nothing that
accumulates while you are away and nothing that asks anything of you. The
timers here are a display loop and mean nothing.

### 10.1 The frame set

Nine frames at P3, **eleven** since SCENE-REACTIONS (§12) added `react_a` /
`react_b`, three layers each (`dev_form_<frame>` tinted by the hoodie's
tint + the hoodie style overlay + `dev_base_<frame>`), all
192x76 on `DEV_RECT`, all from `tools/gen_assets.py`:

| frame | what it is | driven by |
| --- | --- | --- |
| `idle` | hands resting on the keys | `activeState: "idle"` |
| `type_a` / `type_b` | alternate which hand presses | `activeState: "coding"`, 5 fps |
| `mouse` | right hand off the keys, on the mouse | a rise in `stats.today.mouseActiveSeconds`, held ~1.4 s |
| `sleep` | hands slid off the keys, hood tipped, `z` floating | `activeState: "onBreak"` |
| `breath` | **P3** whole upper body 1 px higher; hands unmoved | the ambient scheduler |
| `stretch` | **P3** the same lift at 2 px, held | the ambient scheduler |
| `cheer_a` / `cheer_b` | **P3** arms flung up and out in a V, body bouncing 1 px / 3 px | `onCelebrate()` only |
| `react_a` / `react_b` | **§12** startled 2 px lift + 1 px recoil with the right hand off the keys, then the 1 px acknowledge lean back toward the viewer | a click on the dexel |

The four P3 frames are the **same approved pose displaced by 1..3 px** — no
new proportions, no new silhouette. `gen_assets.py` proves it rather than
claiming it: `check_body_lift_ladder()` fails the asset build unless
`idle → breath → stretch` raises the figure's vertical centroid monotonically
*while its opaque mass stays within 4 %* (i.e. the figure moved, it was not
redrawn), and `assert_dev_hands()` proves from the pixels that breath/stretch
leave both hands on the keyboard and that the cheer frames put one hand past
each end of it, clear of the mouse slot.

### 10.2 Precedence — highest wins

0. **the click react** (`react_*`) — SCENE-REACTIONS (§12), added after P3 and
   above everything below it for the ~1 s it lasts: the player touched the
   character, and a click that visibly does nothing reads as broken. It can
   never coincide with a celebration (§12 closes both directions) and it is
   refused outright while `onBreak`.
1. **celebration** (`cheer_*`) — a real event, so it outranks the poses; it is
   not a claim about what you are doing.
2. **sleep** — `onBreak` owns its pose outright, and it also **suppresses the
   celebration**: the sleep pose means 30 s+ of genuine idleness, so an
   auto-ended session would otherwise have a sleeping Dexel cheer at an empty
   chair.
3. **mouse** — the signal-driven pose.
4. **typing** — the 5 fps `type_a`/`type_b` cycle.
5. **ambient** (`breath`/`stretch`) — `activeState: "idle"` only. Never while
   coding: that would be motion competing with the typing animation, and the
   typing animation is the one that means something.

### 10.3 Timing

Every beat is a **sprite swap on the one existing 200 ms frame timer** — no
CSS transition, no easing curve, no `requestAnimationFrame`. §0's "no
animation longer than 400 ms" governs *transitions*, and these are frame
sequences on a fixed-interval timer, exactly like the `type_a`/`type_b` cycle
that already ships. No second interval was added, and the tick touches the dev
composite **only when the frame it would paint changed**, so an idle Dexel is
quiet between beats: measured in the running game, `idle` costs **0.47 frame
swaps/s** against `coding`'s 5.00/s, and `onBreak` stays at exactly 0 (ADR
0011's all-day cost promise). A "swap" is now literally two `display`/`opacity`
writes — see §10.5, which replaced the per-tick rebuild those numbers were
originally measured against.

| beat | sequence | length | cadence |
| --- | --- | --- | --- |
| breath | `breath` x3 | 600 ms | every 4–6 s, jittered per beat |
| stretch | `breath`, `stretch` x4, `breath` | 1.2 s | every 18–34 s, jittered per beat |
| celebrate | `cheer_a`,`cheer_b` x3, then `breath` | 1.4 s | on the event only |

The stretch band stops below 34 s deliberately: `idle` is bounded above by
`engine.OnBreakIdleThreshold` (30 s of real idleness flips the mood to
`onBreak`), so a band centred beyond that would mean the stretch essentially
never played. Both countdowns keep running in every mood and only **fire**
while eligible, so a stretch that came due mid-keystroke plays as soon as the
hands come off the keys — which is when a person stretches anyway.

### 10.4 The two celebration triggers, and why they are honest

`onCelebrate(reason)` is exported by `render/scene.ts` and called from exactly
two places in `main.ts`, both server-originated (ADR 0010 — the client never
invents an event):

* `'session'` — the `sessionComplete` message (§9.6). The server sends it only
  for a session it actually **kept**; a sub-60 s session is discarded and
  produces no such message, so the body language cannot celebrate a session
  that did not count.
* `'sprint'` — `flash{kind:"sprint"}`, which the server broadcasts from one
  place only: the tick loop, when `Game.Tick` reports a sprint completed.
  Deliberately **not** `kind:"session"` — that kind also carries "Session
  started." and the too-short-to-keep notice, so it is not an event.

Nothing else calls it. There is no timed "celebrate occasionally", and no
client-side inference of a milestone.

### 10.5 The compositing contract — build the scene DOM once, then mutate it

This is a hard rule, not a preference, and it exists because breaking it
produced a shipped bug (**BUG-1**: "the character blinks on and off for
milliseconds"). `render/scene.ts` used to `innerHTML = ''` its subtree and
recreate every layer with a fresh `<img>` on every render — and renders happen
on each ~1 Hz state broadcast *and* on every 200 ms animation tick.

**Why that flickers.** A brand-new `<img>` has no bitmap to paint until its
resource is decoded, and Chrome decodes asynchronously even for an image
already in cache. Worse for a tinted layer: `--form` is a CSS `mask-image`,
and a mask whose bitmap is not ready masks the fill away *entirely*, so the
flat tint vanished and only the grayscale `.tint-shade` showed. Captured from
the running game with `Page.startScreencast` (which emits every composited
frame), a 60 s activity cycle of the old build produced **17 distinct
character images where only 8 poses exist**: three frames with the character
completely absent, one rendered pure white, one with the hoodie but no hands.

**The rules.**

1. **Identity.** Every element the scene can show is created once, in
   `buildSceneSkeleton()`, and lives for the page's lifetime — one `<img>` per
   slot, one chair layer pair, one hoodie overlay. No render path creates,
   removes or `innerHTML`-clears anything. `src`, `--form` and `--tint` are
   written **only when the value changed** (`render/tint.ts`'s `setSrc` /
   `updateTintLayer`), so a render where nothing changed writes nothing.
2. **Decode-free frame swaps.** The nine developer frames of §10.1 are
   **stacked**: nine form layers and nine base images, each permanently
   pointed at its own file, exactly one of each shown. A frame swap is two
   style writes, never an image load and never a mask change.
3. **The form stack hides with `opacity: 0`, not `display: none`.** A
   `display: none` layer is never painted and therefore its mask is never
   decoded, so the *first* appearance of each pose still flashed white.
   `opacity: 0` keeps all nine masks in the paint tree and decoded while
   contributing no pixels.
4. **Pre-warm everything.** `render/preload.ts` fetches *and* `decode()`s every
   sprite the scene can show — all nine `dev_form_*`/`dev_base_*` frames plus
   every catalog item's `sprite`/`detail` — once, at startup. 368 KB total off
   localhost, held for the page's lifetime so the decodes are not collected.
5. **What the teardown used to do implicitly, say explicitly.** A slot with no
   sprite (an `*_none` item) stays hidden because its `<img>` is hidden, not
   because a holder was left empty; a chair with no catalog item at all hides
   its holder; "one child per holder" holds because nothing ever appends a
   second one.

**Exit criterion, and how it is checked.** Over a screencast of a full 60 s
activity cycle (typing at 5 fps, idle with breath/stretch, `onBreak` sleep,
the mouse pose), *every* composited frame must show a complete character. The
current build: **261 frames, 7 distinct character images, all of them real
poses, zero absent/white/partial composites**, and a `MutationObserver` on
`#scene-sprites` counts **0 added and 0 removed nodes** across 14 s of
animation (the old build: 132 and 132).

### 10.5 The hoodie overlay rides the lift

The hoodie style overlay (`hoodie_<style>.png`) is **one file per garment**,
authored once against the `idle` geometry, and it is what carries the
drawstrings / zip teeth / hem trim. The P3 frames move the overlay-bearing
hood **and** back by the same rigid offset, so the renderer offsets that one
layer's `top` by the same amount and every mark stays pixel-locked to the
fabric it is printed on — no per-frame garment assets, no catalog or wire
change:

| frame | overlay `top` |
| --- | --- |
| `idle`, `type_a`, `type_b`, `mouse` | `0px` |
| `breath`, `cheer_a` | `-1px` |
| `stretch` | `-2px` |
| `cheer_b` | `-3px` |
| `sleep` | `0px` — its 3 px/2 px non-rigid drop predates P3; the small garment offset is the pre-existing documented simplification |

`gen_assets.py` is the source of that table: it asserts `DOME_DY == BACK_DY`
for every frame but `sleep`, prints the table as *"FRAME_OVERLAY_DY
(render/scene.ts must carry this same table)"*, and asserts the overlays keep
`DEV_MAX_BODY_LIFT` extra rows clear above the keyboard far-row guard so a
lifted overlay can never intrude on the keyboard.

`currentDevFrame()` — the export the store modal's composed preview doll uses
(§4.2) — deliberately returns only the **state-driven** pose. The preview
does not breathe: it draws its overlay without this offset, so handing it a
lifted frame would misalign the very garment marks it exists to show off.

### 10.6 Verified in the real running game

Built and run with the fake provider under a throwaway `$HOME`, watched in a
real browser, and judged from the pixels rather than from the code:

* **Ambient.** Over 80 s and 45 s idle runs (`-fake-script type:2s,idle:26s`),
  `breath` and `stretch` fired repeatedly in `idle` — 13 breaths / 2 stretches
  in the long run — and **never once** during `coding`; the hoodie overlay's
  `top` tracked `0 / -1 / -2` in step with the frame. Diffing the captured
  frames as R/G/B channels shows the whole upper-body silhouette, the hood
  seams, the shoulder line and the drawstrings displaced upward while the two
  hands stay pixel-identical on the keys.
* **Session celebration.** A real 1 m 10 s session started and ended through
  the modal produced `cheer_a → cheer_b → cheer_a → cheer_b → cheer_a →
  cheer_b → breath` in 1.24 s, then the typing cycle resumed unchanged.
* **Sprint celebration.** Left running until "Fix Bug #404" actually completed
  (`flash{kind:"sprint"}`, "+25 Dev Cash", sprint rolling to `0 / 75 units`):
  the same beat played on the same tick as the gold toast.
* **Precedence.** `onBreak` held `sleep` through a `sessionComplete` without
  cheering; a rising `mouseActiveSeconds` beat the ambient scheduler to the
  `mouse` pose; 8 continuous seconds of `coding` produced only
  `type_a`/`type_b`. The store modal's preview doll kept drawing
  `dev_form_idle` + `dev_base_idle` at `top: 0`.
* **Clean.** Zero console errors or warnings in every run; `tsc --noEmit`
  clean; `tools/gen_assets.py` deterministic (a re-run rewrites identical
  bytes and touches none of the pre-P3 sprites) and its self-check green,
  including the new lift ladder and the cheer hand-placement assertions.

## 11. The Settings modal

SET-1 — three owner-requested capabilities in one surface: **rename the
dexel**, **the window's always-on-top preference**, and **away-time
privacy**. ADR 0014 named this modal as the follow-up that would finally
surface the config slot SEC-1 provisioned; this is that follow-up.

SOUND-1 later added a fourth section, **`SOUND`** (§13), through the same
`SET_PREF` wire and the same one-button-per-preference idiom — the point of
the pattern SET-1 established being that the next preference is a row, not a
redesign. The only thing it did not inherit is the *default*: sound is the
first preference here that ships **on**, and every consequence of that is
recorded in §13.4.

The mechanics are the ones every modal here shares (§4, and the
`add-a-menu-modal` skill): a native `<dialog>` opened with `showModal()`,
the shared `#scrim`, mouse **and** keyboard **and** `Esc`, and one `'close'`
event that every dismissal path funnels through. What follows is only what
is specific to this one.

**It gates nothing.** It owns a text input, which is the question
`add-a-menu-modal` §4 asks — and the answer is the one P2 already gave for
the Sessions modal's project-name field (§9.7): the store's gate exists
because keyboard-driven *shopping* is a self-feeding money loop, and there
is no economy path in here at all — no purchase, no equip, no cash.
Typing a name is a real keystroke on the user's real machine and is
honestly counted as one, exactly as pressing `[G]` to get here is. So no
`SETTINGS_OPEN`/`SETTINGS_CLOSE` action is sent, and nothing freezes.

### 11.1 Opening it

- **Menu entry:** `#settings-open` (`nes-btn menu-item`, label
  `[G] SETTINGS`), placed in `#menu-panel` after `#sessions-open` and
  before `#pause-toggle` (§2.1). No CSS was needed for it — the panel is a
  flex column and `#menu-panel .menu-item` targets the class, so a new
  entry really is one button in `index.html`.
- **Key:** `[G]`. `S`, `Tab`, `A`, `H`, `W`, `M` and `P` were already
  taken (§5.2), and `G` is the letter this app's audience already reads as
  the settings *gear*. While the modal is open, `[G]` closes it — the same
  toggle every other modal's own letter performs.
- The modal claims the **keyboard-ownership tier** while open, for the same
  reason onboarding and Sessions do: it owns an input, so a bare letter must
  not reach a launcher even when focus sits on one of its buttons.

### 11.2 Geometry and the four sections

`#settings` is **480 x 344 at left 80, top 28**, centred vertically
((400-344)/2 = 28). `#settings-title` and `#settings-close` follow §9.2's
positions exactly.

**The box grew for SOUND-1**, from the 480x300-at-top-50 it originally
borrowed from `#sessions` (§9.2). A `SOUND` section costs 56px (title, row,
two note lines) and the original budget had 38px spare, so the honest
options were to grow the box or to drop the note line that makes the
setting comprehensible. 344px keeps **18px of clearance** under BUG-8's
~362px browser `dialog:modal` height cap — the same margin `#activity`'s
346px box has, and for the reason that comment gives: stay well under the
cap rather than relying on the cap to save you. A *fifth* section has to
shrink something rather than grow this again.

Everything else lives in one `#settings-body` box (`left: 12, top: 32,
width: 448`) whose children **flow normally** rather than being
individually absolute-positioned — the idiom `.sessions-view` and
`.activity-section` already use. The 8px inter-section gap is a
`margin-top` on `.settings-section-title`, so a section can gain or lose a
note line without a second spacing edit. The vertical budget is 276px of
the 292px available (the full ledger is in `game.css`'s own comment).

| Section | Contents |
|---|---|
| `NAME` | `#settings-name-current` (`CURRENTLY <name>`, the live server-authored name), `#settings-name-label`, `#settings-name` (an `<input maxlength="12">` — see §11.7), `#settings-name-save` (`SAVE NAME`) |
| `WINDOW` | one `.settings-row`: label `ALWAYS ON TOP` + `#settings-ontop`, then one `.settings-note` |
| `SOUND` | one `.settings-row`: label `SOUND EFFECTS` + `#settings-sound`, then two `.settings-note` lines (§13.4) |
| `PRIVACY` | one `.settings-row`: label `SHOW AWAY TIME` + `#settings-away`, then two `.settings-note` lines |

`SOUND` sits between `WINDOW` and `PRIVACY` because it is the same category
as `WINDOW` — how dexel behaves on your machine — while `PRIVACY`, which is
about what dexel shows you about your own work, stays last as the weightiest
section.

**[DESIGN CALL] The current name is shown on its own line, and the input is
a draft.** The input is seeded from `state.config.name` on **open** and
never again — this modal re-renders on every ~1 Hz `state` broadcast, and
re-seeding the field on each one would delete whatever the user was
halfway through typing. So the server's actual name is rendered separately,
above it, and nothing on screen ever claims a name the server did not send.
Renaming leaves the modal **open** (a settings panel is a place you are,
not a question you answered); the `CURRENTLY` line updates from the
broadcast that answers `SET_NAME`.

**[DESIGN CALL] Focus is NOT forced into the input on open**, unlike
onboarding (§7.1) and Sessions (§9.3). Those modals are *about* their one
field; this one is three sections, and grabbing the caret would make `[G]`,
`Esc` and both toggle buttons unreachable from the keyboard until the user
clicked away. The bare-letter guard (§5.2) still applies whenever the field
*is* focused — verified in the real app by typing a name made of nothing
but global shortcut letters and watching no modal stack up.

**Each preference is ONE button whose label and colour are a pure function
of the server's value** — the store's §4.3 one-action-button idea reduced to
its smallest case. `ON` (gold, `aria-pressed="true"`) or `OFF`; no third
state and no "pending" look, because a `SET_PREF` is answered by a full
`state` broadcast and pretending otherwise would be the optimistic local
edit §6.2 forbids. A click sends the **opposite of what the server
currently says**, read fresh at click time — never the opposite of what the
button last painted (the same rule `#pause-toggle` follows, §2.1).

### 11.3 Away-time privacy — recording is untouched

The owner's requirement, verbatim: *"we can record not working but not show
user."* That sentence is a split, and both halves are load-bearing.

**Recording does not change. At all.** `StatCounters.IdleSeconds` accrues
every away second exactly as before, in the today bucket, the lifetime
bucket, every day bucket and every session record; it is persisted; and it
is **still sent on every `state` broadcast** whatever the preference says.
ADR 0010 (the honest mood machine) and ADR 0013 (analytics) are unchanged
by SET-1, and a structural test pins it: `TestShowAwayTimeNeverChangesWhatIsRecorded`
ticks the same fixture with the preference on and off and requires the two
counter sets to be byte-identical. A counter that stopped counting when
hidden would make every total derived from it silently wrong for anyone who
kept the default — the worst possible version of this feature.

**Presentation changes in three ways:**

1. **`IDLE TIME` is now `AWAY`.** Both rows in the Activity modal
   (§4-adjacent, `#activity-today` / `#activity-lifetime`) are relabelled,
   and their value ids are `#stat-today-away` / `#stat-life-away` to match.
   The wire field they render is still `idleSeconds` — the *recording*
   vocabulary is unchanged; only the word shown to a person is.
2. **When `config.showAwayTime` is false, the two AWAY rows are hidden
   entirely** — label included, via `display: none` on
   `#activity-row-today-away` / `#activity-row-life-away`, so a hidden away
   time leaves no trace of itself rather than an orphaned `AWAY` beside a
   blank. `display: none` and not `visibility: hidden`: the rows sit in
   normal flow inside `.activity-section`, whose own `top` is fixed, so
   collapsing them closes the gap and the section simply ends 12px
   shorter — nothing else in the modal moves and nothing overflows. The
   values are computed **unconditionally**; only their visibility is
   conditional.
3. **The History modal is unaffected, and that was audited, not assumed.**
   Every number it shows comes from a signal other than away time: the
   7-day bars and `BUSIEST DAY` read `activeSeconds`, the 30-day strip
   reads the server's own `isActive` flag, `LONGEST FOCUS` reads
   `longestFocusBlockSeconds`/`focusSessions`, and the streak is rendered
   verbatim. `DayStat.idleSeconds` arrives on every entry and is read by
   nothing. A note in `features/history-modal.ts` records the obligation
   for whoever adds an away-derived number there later.

**What deliberately stays:** the sleeping dexel and the `On break` status
line. Those are the character's body language and mood, not a number — the
cozy half of ADR 0010, and the thing the away-time preference is *not*
about. The terminal's `-- idle --` sentinel (§3.2) stays for the same
reason: scene dressing, not a metric.

The modal says all of this in one line, in the owner's own words:

> Away time is always recorded, never judged -
> show it here or keep it private.

Two authored lines rather than one wrapped one (each fits the 448px inner
width at 8px/char), and an ASCII `-` in place of an em dash: Press Start
2P has no dash glyph beyond the hyphen, and a missing glyph falls back to a
thin system font that anti-aliases to near-invisibility at 8px/1x — the
same lesson the A2 `→` glyph taught (§4's coins row).

### 11.4 Actions

Every control reuses or extends the existing wire; none invents a second
path.

| Control | Action | Notes |
|---|---|---|
| `SAVE NAME` / `Enter` in the field | `{"action":"SET_NAME","name":"<raw>"}` | The **existing** P1 action (§6.2), unchanged. Server-side `game.NormalizeName` is still the only door: trim, drop control characters, cap at 24 runes, reject empty. The client trims and sends nothing for an empty result purely so it never fires an action it knows would be rejected. |
| `ALWAYS ON TOP` toggle | `{"action":"SET_PREF","key":"alwaysOnTop","value":<bool>}` | See §6.2 for the full contract: server-side key allow-list, no success flash, immediate `config.json` write-through, honest error flash if that write fails. |
| `SOUND EFFECTS` toggle | `{"action":"SET_PREF","key":"soundEnabled","value":<bool>}` | Same, with one difference that matters: this preference **defaults on**, so the client must send `false` first, not `true`. The button reads its current value through the same `!== false` rule §6.1 describes, so the value it inverts and the value it paints can never disagree about what "absent" means. |

**Web-only users** can set `alwaysOnTop` and nothing consumes it — the
preference simply persists. That is harmless rather than a lie: no text on
this page claims the window moved, and the same `config.json` is read the
moment the desktop shell *is* used.

### 11.5 How `alwaysOnTop` reaches the window

The desktop shell (`desktop/src-tauri/src/lib.rs`) loads its page from the
runtime's own origin, so it has **no IPC channel into the page** — the same
constraint that made window *focus* the trigger for its moved-runtime
re-resolve. It does already run `dexel status --json`, at launch and again
on every focus, so the preference rides that call:

* `dexel status --json` gains a **`prefs`** block — `{"alwaysOnTop": bool,
  "showAwayTime": bool, "soundEnabled": bool}` — read straight out of
  `config.json`. `soundEnabled` is reported through
  `ConfigData.SoundEnabledOrDefault()`, not from the raw field, because
  `status` must say what a running dexel would actually *do*: for a
  `config.json` written before SOUND-1 that is the default (on), not a nil
  pointer rendered as false.
* `prefs` is **not `omitempty`** and is **not conditional on `running`**. A
  preference is *config* (ADR 0014); it is just as true with nothing
  running, so a consumer never has to branch on `running` to find out what
  the user asked for. This is also why it is read from the file rather than
  from the token-gated live probe that answers `paused`: `paused` is a fact
  about a *process* and only that process can answer it, whereas a
  preference's authority is the file itself — the file every `SET_PREF`
  writes through to immediately, and the file ADR 0014 deliberately leaves
  hand-editable. Reading it is therefore both fresher (a hand edit is seen
  at once) and answerable with no runtime at all.
* `dexel status` (text) prints `on top yes|no`,
  `away time shown in the activity log | hidden (still recorded)` and
  `sound on|off`, for the same reason it prints `paused`
  (ARCHITECTURE.md Decision 16: never mute). The away wording is
  deliberately not "yes/no" — "no" would read as *not recorded*, which is
  exactly what is not true. Sound is the plain case and gets the plain
  wording: off means there is simply no sound, with nothing recorded,
  computed or withheld behind it.
* The shell applies it at **window creation** (`WebviewWindowBuilder::always_on_top`),
  so the first frame is already right and there is never a flash of a
  pinned window followed by a correction; and afterwards whenever the value
  changes, calling `set_always_on_top` **only when the value changed**
  from what it last applied. A refused call is logged and not retried
  optimistically — the last-applied value only advances on success.
* **A change is delivered by watching `config.json`, not by waiting for
  focus** (`watch_prefs_file`). `status --json` also carries `stateDir`
  (not `omitempty`, present on both branches), which is the only thing that
  tells the shell where `config.json` lives without re-implementing
  `app/internal/paths`' per-OS rules in Rust; the shell joins
  `config.json` onto it and polls that one file's mtime+length twice a
  second, reading the contents only when one of them moves. The focus
  re-resolve still applies prefs as a backstop, and both paths share ONE
  "last applied" record so they cannot disagree.

  Note the two SHAPES of the same flag, which is the trap here:
  `status --json` nests it (`{"prefs":{"alwaysOnTop":true}}`) while
  `config.json` is flat (`{"alwaysOnTop":true}`). Reading one with the
  other's accessor yields `false` with no error — a watcher that says
  "off" forever — so the shell has a separate parser per shape and a test
  asserting they are not interchangeable.

**Why focus alone was not enough — the "reversed toggle".** Delivery used to
ride the focus re-resolve only, and that produced a bug whose report looked
nothing like its cause: users on Linux said the toggle was *inverted*, and
on macOS that it did *nothing*. Neither was true. A stacking preference can
only be OBSERVED once the window is no longer focused, and focus was exactly
when the new value arrived — so the user always saw the previous setting:

```
toggle ON  -> click away -> not applied yet -> window drops behind   ("reversed")
toggle OFF -> click away -> still applied   -> window stays on top   ("reversed")
```

It was latency, not an inverted boolean, and it was never platform-specific
— the Linux report was reproduced verbatim on macOS with a CGWindowList
z-order dump. Anyone tempted to "fix" a future report like this by negating
a value should read this paragraph first: that would break the platform that
was working.

**The hardcode is gone.** That line was `.always_on_top(true)`, on ADR
0007's reasoning ("a companion the editor buries never gets seen"). The
reasoning is still why the capability exists; what changed is *who
decides*. A window that cannot be put behind anything is an obstruction on
macOS, not a companion, and forcing it on every user was the wrong way to
act on a good idea. So the behaviour is kept, the **default is off**, and
Settings owns the switch.

### 11.7 Why the name field is 12 characters and the server's cap is 24

Two different numbers on purpose, and the gap is the point.

`game.MaxNameLen` is **24 runes** and is the only validation — it is what
`SET_NAME` enforces and what a hand-edited `config.json` is held to (ADR 0014
keeps that file editable, so this cap has to live on the server).

`#settings-name` and `#onboarding-name` carry **`maxlength="12"`**, and their
labels say so (`RENAME YOUR DEXEL (12 MAX)`). This is not cosmetic. The name
is not merely a label: `activity_line.go` composes the whole status line
around it, and that line has a **34-character budget** (§2.3). A phrasing
that does not fit is *dropped from the pool*, not clipped — so the name
directly decides which sentences the server can still say:

| name length | what the line can say (app "VS Code") |
|---|---|
| 6  | `jawwad is coding in VS Code` |
| 11 | `jawwadzafar is coding in VS Code` |
| 24 | `<name> is coding` — every app-naming phrasing is unreachable |

At 24 characters only 10 remain, so **every** `{app}` template is dropped and
the line silently degrades to `{name} is here` / `{name} is coding`. The app
name vanishes with nothing on screen explaining why, which is exactly how
this was reported ("it says just Coding, it forgot what app we are in").

12 is the tightest common case rather than a round number: `{name} is coding
in {app}` needs the name within 12 for `Terminal`, 13 for `VS Code` and
`Ghostty`, 15 for `iTerm`, 17 for `Zed`. Genuinely long app names
(`IntelliJ IDEA`, `VS Code Insiders`) still fall back to `{name} is coding`,
which is correct — there is no honest way to fit both.

The `CURRENTLY` row still truncates at 24, not 12: a longer name arriving from
a hand-edited config is legal and should be shown as far as it fits.

### 11.6 Verified in the real running game

Built and run with the fake provider under a throwaway `$HOME`, driven with
real clicks and real per-character keystrokes, and judged from the pixels
(the `feature-build-and-verify` gate):

* **Opens both ways.** `[G]` and the `[G] SETTINGS` menu entry both open it;
  `[G]`, `Esc` and `X` all close it. `#settings-body`'s measured
  `scrollHeight` was **210px** — exactly the authored budget, no overflow.
  (SOUND-1 re-measured this after adding the fourth section: 276px of the
  292px the taller box provides, still no overflow, at 1x and 2x. §13.6.)
* **Rename round-trips.** Typing a new name and pressing `SAVE NAME`
  changed the personal status line (§2.3, §7.4) and the modal's own
  `CURRENTLY` line together, and `config.json` on disk carried the new
  `name` alongside both preferences.
* **The focus guard holds.** `Washgmps` — every bare global shortcut letter
  — typed character by character into the rename field landed in the field
  intact, with the store, activity, history and sessions dialogs all still
  closed and the menu still hidden.
* **Away rows.** With `showAwayTime` false the Activity modal showed **no**
  AWAY row in either section (computed value present in the DOM, row
  `display: none`); toggling it on made both appear, labelled `AWAY`, with
  the sections tightening cleanly and nothing clipping.
* **Both preferences survive a restart.** Killed and relaunched on the same
  state dir: the modal came back with both toggles `ON` in gold and the
  away rows visible.
* **`status --json` carries `prefs`** both with a live runtime and with
  nothing running.
* **Clean.** Zero page errors or console errors across every run;
  `tsc --noEmit` clean; `cargo check` and `cargo clippy -D warnings` green
  for the shell change.

---

## 12. Scene reactions — click the room and the room answers

SCENE-REACTIONS (`docs/plan/ROADMAP.md`). Four things in the room are
clickable; clicking one plays that thing's short reaction and the room goes
back to what it was doing. That is the whole feature. It is **client-side
only**: `render/scene.ts` owns it, it sends **no `ClientAction`**, adds **no
wire field**, touches no save, pays no coin and unlocks nothing. Reactions are
free, repeatable, and mean nothing — which is exactly why they are safe (§5.3
"shopping must not count as work" applies to fun too: no click here can move a
single number).

### 12.1 The four hit regions

Room pixels (the scene's own 320x200 space, drawn at 2x inside the 640x400
layout). Each is one invisible absolutely-positioned `<div class="hit-region">`
inside `#scene-sprites`, created once with the rest of the scene skeleton
(§10.5) and only ever shown or hidden after that.

| item | rect (room px) | source | shown when |
| --- | --- | --- | --- |
| the dexel | `(118,110) 84x39` | `HIT_RECT.dev` | `activeState != "onBreak"` |
| the monitor | `(94,20) 132x64` | `HIT_RECT.monitor` | always |
| the beverage | `(56,90) 20x24` | `SLOT_RECT.beverage` | the equipped item has react art |
| the buddy | `(288,46) 28x30` | `SLOT_RECT.buddy` | the equipped item has react art (so: never for `buddy_none`) |

The dexel's region is **not** `DEV_RECT`. That canvas is 192x76 and mostly
empty air (the `mouse` pose reaches out to room x252), so the region is the
**body**: from the hood's own top row (y110) down to the last row before the
chair (y148), across the shoulder span (x118..202). Both bounds exist to stop
the hand cursor lying — above y110 the sprite is two forearms with the
**keyboard** visible in the gap between them, and from y149 down the **chair**
composites over the developer (§10.5's behind-view order), with the HUD panels
opaque from y161. The other two regions are their slot rects, which are small
enough to read as the object.

**No coordinate maths anywhere.** A CSS transform is part of the box the
browser hit-tests, so a click at 1x, at a snapped 2x, or at 2x inside a
letterbox maps back through `#root`'s transform *and* the scene's `scale(2)`
natively and lands on the room pixel it looks like it landed on. This
frontend reads no `clientX` and calls no `getBoundingClientRect` (§0.1).

**The affordance** is a hand cursor over those four rects and the app's pixel
arrow everywhere else — one CSS rule (`#scene-sprites .hit-region { cursor:
pointer }`), which is also how the player learns the room is clickable at all
and that the plant, the wall, the keyboard and the mouse are not.

### 12.2 The beats

Sprite swaps on the **same 200 ms timer** as §10.3 — no second interval, no
transition, no `requestAnimationFrame`.

| item | frames | length |
| --- | --- | --- |
| the dexel | `react_a`, `react_a`, `react_b`, `react_b`, `react_b` | 1.0 s |
| the monitor | `monitor_shake_a`, `_b`, `_a`, `_b` | 0.8 s |
| the beverage | `<item>_react_a`, `_b`, `_b`, `_a` | 0.8 s |
| the buddy | `<item>_react_a`, `_b`, `_b`, `_a` | 0.8 s |

There is no `a → b → a → rest` for the character because **rest is not a
frame**: the beat simply ends and §10.2's normal precedence paints whatever
the server says you are doing. The prop beats are a hop and a settle, in the
order the art was drawn for (`react_a`/`_b` are stage 1 and 2 of one hop with
the contact shadow planted on the desk).

The first frame is painted **on the click**, not on the next tick, so the room
answers immediately.

### 12.3 Precedence, and the sleep decision

* The **dexel's** react sits at the top of §10.2 for its ~1 s — it interrupts
  the ambient breath/stretch and outranks the typing/mouse/idle pose. The pose
  bookkeeping keeps running underneath it, so typing resumes on the very next
  tick with the cycle it would have had anyway. Nothing is claimed: the react
  frames are a startle and a lean, not the typing pose.
* **Celebration** and the react can never both be on screen: a click is
  refused while a celebration is playing (a real server event is not something
  a click may cut off), and `onCelebrate()` cancels a running react (the event
  is the more important thing to show).
* **Sleep: clicking a sleeping dexel does nothing, and it says so.** While
  `onBreak` the dexel's hit region is hidden, so the cursor stays the ordinary
  arrow and no click is swallowed by a dead target. The react frames are
  authored on the awake pose (hands at the keyboard), so playing one from the
  sleep pose would snap the figure upright and flop it back — slapstick, and
  momentarily a pose that looks like *working* when the server has said for
  30 s+ that nobody is there. §10.2 already suppresses the celebration while
  asleep for the same reason. Waking it is not on the table either: the mood is
  the server's to report from real keystrokes (ADR 0010). If the character
  falls asleep mid-react the react is dropped on that tick.
* The **monitor, beverage and buddy** react whether the dexel is awake, typing
  or asleep. They are their own layers and say nothing about whether a human is
  at the desk.

### 12.4 Anti-mashing

One react at a time per item — a click during a react is **dropped, never
queued** — plus 1.2 s of enforced quiet after a beat ends. Worst case per item
is therefore one ~1 s beat per ~2 s, no matter how fast the mouse is clicked,
so no sprite can strobe. The cooldown is per **item**: mashing the mug never
blocks the buddy, and a refused click costs nothing (it does not start or
extend a cooldown).

### 12.5 The monitor's hard constraint

The monitor's reaction is a **`src` swap and nothing else** — the element must
never move. The DOM terminal (§2) sits over the glass with 2 px of margin, and
its 11 lines of text do **not** move with the sprite, so nudging `left` or
adding a transform would slide the glass out from under the text. The shake is
therefore authored *inside* the sprite: `tools/gen_assets.py`'s
`assert_monitor_shake` fails the asset build unless each shake frame is
`monitor.png`'s head displaced purely horizontally by exactly ±1 px, with the
neck, foot and contact shadow byte-identical and the terminal's text box still
inside the glass. Verified from the DOM in the real game: across
rest → shake_a → shake_b → rest the monitor's bounding rect, its inline style
and `#terminal`'s rect are all **unchanged**; only the `src` differs.

### 12.6 The hoodie overlay leans too

§10.5's rule gains a second axis. The react frames move the garment-bearing
hood and back **rigidly on both axes** (`gen_assets.py`'s `DOME_DY == BACK_DY`
and its `BODY_DX`, capped at ±1 px), so the one hoodie overlay file is offset
by `FRAME_OVERLAY_DX[frame]` as well as `FRAME_OVERLAY_DY[frame]` and every
drawstring, zip tooth and hem mark stays pixel-locked to the fabric it is
printed on. `assert_hoodie_react_alignment` proves it for all four styles from
the pixels. Measured live: `react_a` → `left: -1px; top: -2px`, `react_b` →
`left: 1px; top: -1px`, every other frame → `left: 0; top: <P3 lift>`.

### 12.7 Verified in the real running game

Built and run with the fake provider under a throwaway `$DEXEL_HOME`, driven
with **real mouse clicks** at mapped screen coordinates and judged from the
pixels (the `feature-build-and-verify` gate):

* **All four react and return.** The dexel (startle → acknowledge → back to
  the typing cycle), the monitor (±1 px head wobble, four frames), all four
  beverages (mug/thermos/teacup/energy — hop plus that vessel's own steam,
  lid vent or fizz burst) and all three buddies (duck hop, bot eye-flash and
  antenna sparks, cat ear-twitch and tail-flick), each returning to its exact
  rest sprite.
* **A click while typing.** State `coding`, frame `type_a` → `react_b` mid-beat
  → `type_a` again ~0.9 s later, with no gap and no missed keystroke display.
* **Mashing.** 14 clicks in ~700 ms produced exactly **one** beat, on the mug
  and on the dexel alike.
* **Scaled and letterboxed.** Real clicks landed on the same regions at
  640x400 (1x), 1280x800 (2x, no bars) and 1400x900 (2x inside a 60x50
  letterbox), with the hand cursor resolving to the hit region at every scale.
* **Nothing else offers a click.** The keyboard, the mouse, the chair back, the
  plant, the wall and an empty buddy slot all keep the ordinary arrow.
* **`buddy_none`.** Sprite hidden, hit region hidden, no cursor change, no
  reaction.
* **Asleep.** The dexel's region gone and clicks inert while the sleep pose
  holds; the monitor and the mug still answering; falling asleep mid-react
  drops the react on that tick and waking up restores the region.
* **Clean.** Zero console errors and zero page errors across every run, in the
  real WS-backed app and under `?dev=1`; `tsc --noEmit` clean and the bundle
  byte-identical across rebuilds.

---

## 13. Sound — six tiny chiptune moments

SOUND-1. dexel makes a noise at six moments and at no others. The moments
are the two completions and the four scene reactions (§12); everything else
— typing, mouse movement, menus, modals, connection changes, purchases — is
**deliberately silent**.

The whole feature is **client-side and additive**: `render/audio.ts` owns
playback, `render/scene.ts` is the only module that calls it, the server
gained exactly one bool (`config.soundEnabled`, §6.1) and nothing else. No
sound is ever *composed* on the client from server text, no sound is queued,
and no sound is played for anything the server did not report.

### 13.1 The six sounds

Generated by `tools/gen_sounds.py` into `app/public/sounds/`, embedded into
the binary by `app/embed.go` and fetched by the page at `/sounds/<file>.wav`.
All mono, 22050 Hz, 16-bit PCM; the whole set is ~106 KB.

| File | Moment | Character | Length | Peak |
|---|---|---|---|---|
| `sprint_complete.wav` | `flash{kind:"sprint"}` | Four **square** notes straight up a C major triad into the octave (C5-E5-G5-C6) — the payday, and the only bright thing here | 0.60s | -12 dBFS |
| `session_complete.wav` | `sessionComplete` | Five **triangle** notes that *resolve* rather than climb (E5-G5-C6, a B5 leaning note, then a held C6) — rounder, calmer, longer | 0.90s | -12 dBFS |
| `react_dexel.wav` | click the developer | A startled "bwip?" — a thin 25%-duty square gliding 400 → 950 Hz, because a rising glide is heard as a question | 0.18s | -15 dBFS |
| `react_monitor.wav` | click the monitor | A soft rattle: a low 190 Hz triangle with ±18% vibrato and matching tremolo at 30 Hz — a wobble, not a hit | 0.30s | -18 dBFS |
| `react_beverage.wav` | click the beverage | A bloop (520 → 300 → 470 Hz triangle: liquid moving) plus four discrete 6ms ticks of fizz | 0.23s | -16 dBFS |
| `react_buddy.wav` | click the buddy | Two quick rising square blips (700→1100, then 950→1400 Hz) — a bird's answer, in the buddy's own two-beat shape | 0.24s | -15 dBFS |

**The two completions are distinguishable in a blind listen**, which is why
they differ on all three axes at once — waveshape, contour and length —
rather than only on pitch. A sprint and a session are different
achievements.

**Levels are the design, not an afterthought.** The two jingles sit at
-12 dBFS and the four reactions between -18 and -15, so idly poking the mug
can never be louder than actually finishing something. `gen_sounds.py` normalises each file to hit its
declared peak exactly and then re-measures it from the written bytes,
failing outside a ±0.05 dB band and outside a hard -24…-9 dBFS envelope: a
future edit cannot quietly ship something twice as loud as this release.

**There is no noise channel.** A real 8-bit chip has one and it is the
fastest way to make a companion sound like an arcade cabinet. Every voice is
a square or a triangle with a pitch slide and a short envelope; even the
beverage's fizz is four discrete ticks rather than a white-noise burst.

**The generator is the source**, exactly as it is for the sprites: the WAVs
are build artefacts of `tools/gen_sounds.py`, not hand-recorded blobs. Its
self-check asserts per file — duration bounds, the declared peak, no DC
offset, a zero first and last sample (no click at either edge), a size
budget, and **byte-identical output across two independent synthesis
passes**, which is what proves there is no RNG, clock or dict-ordering
dependence anywhere in it. A manifest names the exact six files and deletes
anything else in the directory, so no orphan WAV can be embedded into every
binary while no code plays it.

### 13.2 What deliberately makes no sound

* **Typing.** The single most-repeated event in this app. A companion that
  chirps per keystroke is a companion that gets muted on day one — at which
  point the six moments that *were* worth hearing are gone too.
* **Ambient anything.** No loop, no room tone, no music. dexel sits on a
  working developer's desk for eight hours.
* **A UI click on menu/modal opens.** Considered and dropped. Six launchers
  are one keypress away each; a tick on every one of them is exactly the
  "annoying" this feature is trying not to be. If it is ever wanted it
  belongs in `gen_sounds.py`'s manifest with its own self-check, not as a
  special case in the player.
* **Errors, connection loss, purchases, equips.** These already have honest
  *visual* feedback (a flash, the connection overlay, the cash flash). None
  of them is a celebration, and a sound would make several of them feel
  like one.

### 13.3 The autoplay policy — why a locked context is normal, not an error

Browsers refuse to let a page make noise before the user has interacted with
it: an `AudioContext` constructed before any gesture starts **suspended**,
and `resume()` on it rejects until a gesture has happened.

dexel's six moments split across that line, and **not symmetrically**:

* **The four reactions ARE gestures.** They arrive inside a `click` handler
  — precisely when a context may be created and resumed. So the first click
  on the room both unlocks audio and plays its own sound.
* **The two completions are NOT.** A sprint completes because the user was
  typing *in their editor*; dexel's window may not have been clicked since
  the page loaded, or ever. **This is the ordinary case, not an edge case** —
  and it is the one a naive implementation gets wrong by throwing an
  unhandled rejection into the console at the app's happiest moment.

So the contract is: **a locked context produces silence, never an error.**
`play()` on a locked context asks the browser to resume (harmless, and it
succeeds on the occasions a gesture already happened), swallows the
rejection, and returns.

* **A bounded 250ms grace window, and it is not a queue.** Two things can
  make a sound momentarily un-playable and both resolve by themselves within
  a few milliseconds: `resume()` is asynchronous (so a context created
  *inside* the asking gesture can still read `suspended`), and
  `decodeAudioData` is asynchronous (so on the very first gesture the buffer
  is still decoding — that gesture is the earliest moment decoding could have
  started at all). Without a window the **first** sound after a page load is
  silent: click the mug, watch it hop, hear nothing, click again and it
  works. That was measured in the gate, not theorised — a headless click
  lands ~15ms after its own `pointerdown`, nowhere near enough for six WAVs
  to decode.

  So an un-ready sound is re-attempted every 30ms for at most **250ms** and
  then dropped for good. That is bounded well inside the 800-1000ms reaction
  beat and the ~1.4s celebration bounce, so a sound that lands late still
  lands *on its moment*; it **expires**, so a sprint that completed while the
  page had no gesture can never fire twenty minutes later when the user
  finally clicks — the dishonesty this codebase refuses everywhere else (a
  jingle is a claim that something just happened); and the preference is
  re-read on every attempt, so muting inside the window silences it like
  anything else.
* **Nothing is created eagerly.** `initAudio()` (called from `main.ts`)
  installs one capturing `pointerdown` + `keydown` listener on `window` and
  does nothing else — no context, no fetch. Both events count because this
  app is fully keyboard-driven; a user who never touches the mouse must
  still be able to unlock audio. Both listeners are removed the moment
  either fires.
* **Decoding is deferred to that same gesture, then warms all six.**
  `decodeAudioData` needs a context, so nothing can be decoded before one
  exists; warming the whole set at unlock means the two completion
  sounds — which fire on a server event that will not wait for a fetch —
  are already decoded when they arrive.
* **A browser with no WebAudio, or one that refuses the constructor, leaves
  the module permanently and quietly inert.** Sound is a garnish; no part of
  this app may fail because a garnish did. A failed fetch or decode is
  swallowed the same way `render/preload.ts` swallows a failed sprite warm,
  and is retried on a later `play()`.

**One master gain of 0.5**, applied to every voice. The *relative* balance
is already fixed where it belongs — in the generator's per-sound peaks — and
mixing in two places is how two places drift.

### 13.4 The preference

`config.soundEnabled` (§6.1), set by `SET_PREF` (§6.2, §11.4), stored in
`~/.config/dexel/config.json`, surfaced as the `SOUND` section's
`SOUND EFFECTS` toggle (§11.2) under one honest note:

> A few tiny chiptune moments - never music, never typing noise.

* **Default ON.** A companion that ships silent is a companion nobody
  discovers has a voice. The mute is one keypress and two clicks away.
* **`play()` reads the preference from server state on every single call** —
  never cached, never remembered locally. So a mute in a second tab, or a
  hand-edited `config.json` plus a restart, takes effect without the audio
  layer knowing anything happened. Same rule as every other control in this
  app.
* **The default is inverted from its two siblings, and that has real
  cost**, paid in exactly one place: `store.ConfigData.SoundEnabled` is a
  **`*bool`**. `alwaysOnTop`/`showAwayTime` lean on Go's zero value — false
  is what a fresh install, an absent key and a hand-deleted key all mean,
  and they agree, so no defaulting code exists anywhere. A plain bool here
  would make those three cases mean the *opposite* of the default: every
  user with a `config.json` written before this field existed would silently
  come back muted, indistinguishable from a deliberate choice. The pointer
  keeps `nil` (never chosen → default on), `&true` and `&false` distinct,
  and `SoundEnabledOrDefault()` is the single place the default lives — read
  by `main.go` at boot and by `dexel status`, and never re-derived.
  The **write**-through always records a concrete value, so "never chosen"
  is over the first time dexel writes the file — and the default
  `config.json` a fresh install writes states `"soundEnabled": true` rather
  than `null`, because that file exists to be hand-edited and `null` where a
  bool belongs reads like damage. The `nil` state remains the honest answer
  for a `config.json` written before this field did, which is the only place
  it can still occur.
* **Muting changes nothing but the sound.** No counter, no coin, no XP, no
  sprint progress and no recorded duration differs between a muted and an
  unmuted dexel — pinned by
  `TestSoundEnabledChangesNothingButItself`, the sibling of §11.3's
  `TestShowAwayTimeNeverChangesWhatIsRecorded`.

### 13.5 Where the calls are, and what bounds them

Two call sites, both in `render/scene.ts` — the module that already owns
both of these moments:

1. **`queueReact(key)`**, after *every* refusal has returned. So a click the
   room ignores makes no sound, and the anti-mash rules that bound the
   animation (§12.4: one beat at a time per item plus a 1.2s cooldown, worst
   case one beat per ~2s per item) bound the audio **identically**, with no
   second copy of the rule. `REACT_SOUNDS` maps the four `REACT_KEYS` to
   their voices explicitly rather than by string surgery, which is also how
   a future clickable item with no voice says so — by not being in the
   table.
2. **`onCelebrate(reason)`**, *below* the `onBreak` guard. The jingle is
   therefore suppressed by exactly the same condition the visible
   celebration is (§12.3): a sleeping character cheering at an empty chair
   was already refused as dishonest body language, and a jingle playing into
   an empty room while its owner is away is the same lie with the volume up.
   `reason` picks the voice, which is the second thing that parameter now
   earns.

Neither the flash router nor `onSessionComplete` in `main.ts` plays anything
itself: routing both through `onCelebrate` is what keeps "the sound and the
bounce are the same event" true by construction rather than by convention.

### 13.6 Verified in the real running game

Built as the real Go binary and run with the fake provider under throwaway
`$DEXEL_HOME`s, from an **empty directory** (so every asset came from the
copy embedded in the binary), driven with real clicks and real keypresses in
headless Chromium. WebAudio was instrumented from **outside** the product —
the gate wraps the `AudioContext` constructor, `resume()` and
`AudioBufferSourceNode.prototype.start`, so there is no test-only code in the
shipped bundle and what is asserted is the real API call. Because
`start()`'s node exposes `buffer.duration`, the gate can name **which**
sound played, not merely that one did.

* **The binary really carries the sounds.** From an empty directory,
  `/api/health` reported `"publicSource":"embedded"` and all six
  `/sounds/*.wav` returned **200 `audio/wav`** at their exact generated byte
  lengths; `/sounds/` (the directory) returned **404**, keeping
  INTERACTION-HARDENING's no-listings rule.
* **Each reaction plays its own voice.** Clicking the developer, the monitor
  and the mug in turn produced buffer durations `[180, 300, 234]` ms —
  `react_dexel`, `react_monitor`, `react_beverage`, in that order, each on
  the beat of its own first frame.
* **The very first gesture is audible.** On a returning save (no onboarding
  modal), a page whose *first* interaction is a click on the mug played
  `[234]` — the case that was silent before the grace window existed, and
  the reason it exists (§13.3).
* **A completion with no gesture at all is silent and clean.** With the
  autoplay policy simulated by fault injection — `AudioContext.state` forced
  to `suspended` for ever and `resume()` replaced with a rejecting promise,
  because headless Chrome does not enforce the real policy — three
  back-to-back completions produced **0 started sources, 0 console errors and
  0 unhandled rejections**, and 4 seconds later (long after the buffers had
  decoded) still nothing had arrived late. The retry loop terminates.
* **A real sprint completion plays the sprint voice.** No fixture and no
  `evaluate()`: the fake typing provider ground out the whole 50-unit first
  sprint until `app/main.go` broadcast
  `flash{kind:"sprint"}` — observed on screen as
  `"Fix Bug #404 complete! +25 Dev Cash"` — and the page answered with one new
  buffer of **600ms**, `sprint_complete.wav`. (The `[300]` before it is the
  monitor click that unlocked audio at the start of the run, so this is the
  sprint seam being tested and not the autoplay policy.) Zero problems.
* **A real session completion plays the session voice.** A genuine 1m 12s
  session, started and stopped through the Sessions modal against a real
  server, produced the summary card and exactly one new sound: **900ms**,
  `session_complete.wav` — not the sprint jingle.
* **The toggle silences everything, and the animation keeps going.** With
  `SOUND EFFECTS` off, three further reaction clicks left the play counter
  frozen at its previous value while the monitor was still mid-react
  (`monitor_shake_b.png` on screen) — muting is not disabling the feature.
  Turning it back on made the next click audible again.
* **The preference persists both ways.** Muted, then reloaded: the toggle
  came back `OFF`. Muted, then the **process restarted**: still `OFF`, and
  `config.json` carried `"soundEnabled": false` alongside an untouched
  `autostart` and `name`.
* **A pre-SOUND-1 `config.json` boots with sound ON.** A hand-written file
  with `name`/`autostart`/`alwaysOnTop`/`showAwayTime` and **no**
  `soundEnabled` key reported `sound on` from `dexel status`; adding
  `"soundEnabled": false` by hand reported `sound off`; and a running dexel
  writing that file back left `autostart: systemd-user` and the mute both
  intact. `status --json` carried
  `{"alwaysOnTop":…,"showAwayTime":…,"soundEnabled":…}`.
* **The modal fits.** `#settings-body`'s measured `scrollHeight` is **276px**
  against a `clientHeight` of **276px** — the authored budget exactly, no
  overflow — with the dialog measuring 344px and *not* being capped by the UA
  stylesheet. Four section titles in order (`NAME`, `WINDOW`, `SOUND`,
  `PRIVACY`), five note lines, nothing clipped, identical at 1x (640x400) and
  2x (1280x800). Judged from the screenshot: the `SOUND EFFECTS` row reads as
  a peer of the two existing preference rows, with the gold `ON` the only
  gold toggle on screen — which is itself the honest signal that this is the
  one preference that ships enabled.
* **The generator is honest about itself.** `python3 tools/gen_sounds.py`
  passes every self-check (durations, declared peaks to ±0.05 dB, no DC
  offset, zero first/last samples, size budget, two byte-identical synthesis
  passes) and rewrites byte-identical files across separate process runs;
  the manifest holds at exactly six files, 105.7 KB total.
* **Clean.** `go vet`, `gofmt -l`, `bash scripts/test-race.sh` and
  `tsc --noEmit` all green; the esbuild bundle is byte-identical across
  rebuilds. Zero console errors and zero page errors across every run — the
  only console noise in this app is a `/favicon.ico` 404 that predates this
  work (the page declares no icon and the binary embeds none), which the gate
  answers with a 204 so it cannot mask a real error.

## 14. Number & duration formatting — humanized, honest, floored

Every on-screen number in the analytics UI is formatted **client-side** (the
wire sends raw integers and raw whole-seconds durations, never a pre-formatted
string — §6.1). `app/frontend/src/format.ts` owns the rules; each display
site picks a formatter by **classifying the field**, and the classification —
not the widget — decides the formatter:

* **COUNT** — a magnitude that can grow large: keystrokes, mouse clicks, app
  switches, sprints, focus sessions, completed/this-week session tallies, and
  the HUD **DEV CASH** balance. Rendered with **`fmtCount`**.
* **DURATION** — a span of whole seconds: active time, away time, mouse-active
  time, longest-focus block, session/elapsed durations. Rendered with
  **`fmtDuration`**.
* **DAY-COUNT** — a count *of days* that can cross a year: the streak
  (current/longest). Rendered with **`fmtDayCount`**.
* **MONEY (exact)** and **IDENTIFIERS** — store prices, the per-signal **coin
  deltas** a user reasons about exactly ("+14 earned"), and the **"SESSION N"
  ordinal** — stay EXACT via `fmtInt`. You spend an exact number and you refer
  to an exact session, so these never compact. The one money value that *does*
  compact is the HUD DEV CASH balance, which the owner said "could go to a
  million": it is a magnitude to glance at, not a price to match, so a large
  balance compacts rather than overflowing the fixed HUD box.

### `fmtCount(n)` — SI-style compact integers

* `n < 1000` → the exact integer ("0", "88", "842", "999").
* `n >= 1000` → three significant figures with a `k` / `M` / `B` / `T` suffix,
  any trailing `.0`/`.00` trimmed: `1000` → "1k", `1234` → "1.23k", `88426` →
  "88.4k", `120000` → "120k", `1234567` → "1.23M", `12000000` → "12M",
  `1.2e9` → "1.2B".
* The fraction is **floored, never rounded up**, so a value that has not
  actually reached the next unit never claims it: `999999` → "999k" (never
  "1M"), `999999999` → "999M", `1999` → "1.99k". A compact count can
  under-state by less than one displayed step; it can never over-state.
* Negatives keep a leading `-` over the same rule; non-finite/undefined → "0".
* This is why a small count never turns into an ugly "0.8k" — anything below
  1000 is shown exactly.

### `fmtDuration(seconds)` — rolled-up prose, at most two units

Rolls seconds up through **s → m → h → d → y**, with **no months** (a month's
length is variable — deliberately skipped), showing **at most the two
most-significant units** and flooring everything below them:

* `< 60s` → "45s"
* `< 60m` → "3m 30s" (trailing " 0s" dropped → "3m")
* `< 24h` → "2h 15m" (trailing " 0m" dropped → "2h")
* `< 365d` → "3d 4h" (trailing " 0h" dropped → "3d")
* `>= 365d` → "1y 35d" (`1 year = 365 days`; trailing " 0d" dropped → "2y")

The two-unit floor is what turns the raw "1020m 1s" a naïve minutes render
produced into an honest **"17h"**, and "263m 30s" into "4h 23m". Negatives /
undefined clamp to "0s".

**The live session pill keeps its own `fmtClock` (H:MM:SS / M:SS),
deliberately NOT `fmtDuration`** (chrome.ts): a running titlebar clock reads
better as a ticking clock face than as humanized prose, and a ticking value
should not be re-humanized every second. The Sessions modal's live-elapsed
cell *does* use `fmtDuration` — it already displays at minute resolution, so
it visibly changes only once a minute, not every tick.

### `fmtDayCount(days)` / `rollsToYears(days)` — day counts that cross a year

A small day count stays a **bare integer** so the DOM's own " DAYS" unit reads
naturally ("8 DAYS"); once it reaches a year it rolls up to "1y 35d" via the
same 365-day math `fmtDuration` uses, and the caller **drops the " DAYS"
suffix** (`rollsToYears` reports when) because the rolled-up form already
carries its unit — "1y 35d DAYS" would read wrong. Since `longest >= current`
always holds, the unitless small "LONGEST: 12" case only ever appears while
the " DAYS" label is still shown, so nothing is ever left unlabelled.

The **"BUSIEST DAY"** insight is a **date** (`MM/DD`), not a count — its
parenthetical is a DURATION (`fmtDuration`). The Sessions card's **"BEST"**
focus line keeps a cozy minute-resolution local formatter (no seconds on that
one line), but now rolls up past an hour so a long block reads "1h 20m", never
a bare "80m".
