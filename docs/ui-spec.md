# UI spec — dexel v2 (HTML / NES.css frontend)

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

* **Window: 640 x 400 logical px, fixed.** Not responsive. No media queries,
  no flex reflow that could move a pixel. The layout below is absolute
  positioning inside a 640x400 root, and that is deliberate: a desktop
  companion at a fixed integer scale is the entire premise (art-direction
  non-negotiable #7).
* **Every length is an integer px.** No `%`, no `em`, no `rem`, no `vh` in the
  chrome. Fractional layout at this scale produces the half-pixel blur the art
  direction bans.
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
        <div id="menu-panel-title">MENU</div>  <!-- becomes the name, §7.4 -->
        <button id="store-open"    class="nes-btn menu-item">[S] STORE</button>
        <button id="activity-open" class="nes-btn menu-item">[A] ACTIVITY</button>
        <button id="history-open"  class="nes-btn menu-item">[H] HISTORY</button>
        <!-- P2: #sessions-open joins here, §9.1 -->
      </div>
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
      <div id="status-name"></div>    <!-- §7.4 the dexel's name; empty until named -->
    </div>

    <div id="scrim"></div>            <!-- shown while a modal is open -->
    <dialog id="store" class="nes-dialog is-dark"> … §4 … </dialog>
    <dialog id="activity" class="nes-dialog is-dark"> … §6.1 … </dialog>
    <dialog id="history" class="nes-dialog is-dark"> … §6.1 … </dialog>
    <!-- P2: <dialog id="sessions"> joins here, §9.2 -->
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
| `#menu-panel-title` | inside `#menu-panel`, 128 x 16 | static `MENU`, replaced by the dexel's name once set (§7.4), 8px `var(--screen-dim)`, bottom rule separating it from the items |
| `.menu-item` (×N) | inside `#menu-panel`, 128 x 20 each, 4px gap | one `nes-btn` per launcher, in menu order: `#store-open` (`[S] STORE`), `#activity-open` (`[A] ACTIVITY`), `#history-open` (`[H] HISTORY`), and (P2, §9.1) `#sessions-open` (`[W] SESSIONS`) |

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
* The three rows below it are the character's own chatter. They are dimmer, are
  prefixed `>`, and are separated by the rule. **Never merge the two zones,
  never let a ticker line borrow a word from the real one.**

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

**[DESIGN CALL] the scrim is deliberately light, and clicking it does NOT
close the modal.** The whole point of equipping from the store is watching the
scene change; the PDF calls for the main companion screen to update the instant
an item is equipped. A dark scrim hides the payoff, and a click-to-close scrim
punishes you for clicking on the thing you just bought to look at it. Close is
`X`, `Esc`, or `S`/`Tab`.

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
| `#scrim` | **nothing** (see §4.5) |
| anywhere in the scene | nothing. The scene is not interactive in v2 |

### 5.2 Keyboard

Main screen:

| Key | Action |
|---|---|
| `S` | open the store |
| `Tab` | open the store (`preventDefault()` so focus does not move) |
| `W` | open the Sessions modal (P2, §9.1 — "work session"; `S`/`Tab`/`A`/`H`/`M` are taken) |

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
* Window-level shortcuts (always-on-top toggle, quit) belong to the Wails
  shell, not to this document.

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
      "appSwitches": 12
    },
    "lifetime": {
      "keystrokes": 88420,
      "mouseActiveSeconds": 15810,
      "activeSeconds": 61200,
      "idleSeconds": 5100,
      "sprintsCompleted": 41,
      "focusSessions": 57,
      "appSwitches": 205
    },
    "coinsToday": {"keystrokes": 6, "mouse": 2, "focusSessions": 4, "appSwitches": 0},
    "history": [
      {"date": "2024-06-01", "keystrokes": 3980, "mouseActiveSeconds": 740, "activeSeconds": 2900, "idleSeconds": 300, "sprintsCompleted": 2, "focusSessions": 3, "appSwitches": 9, "coinsEarned": 41, "isActive": true, "longestFocusBlockSeconds": 1380},
      "... 28 more DayStat entries, dense and ascending ...",
      {"date": "2024-06-30", "keystrokes": 4210, "mouseActiveSeconds": 812, "activeSeconds": 3040, "idleSeconds": 260, "sprintsCompleted": 3, "focusSessions": 4, "appSwitches": 12, "coinsEarned": 12, "isActive": true, "longestFocusBlockSeconds": 900}
    ],
    "streak": {"current": 6, "longest": 14}
  },
  "config": {"name": "Pixel"},
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
      "longestFocusBlockSeconds": 720
    },
    "summary": {"completed": 27, "thisWeek": 4, "longestSessionSeconds": 9840},
    "recent": [
      {"id": 27, "name": "", "startedAt": "2024-06-29T09:00:00Z", "endedAt": "2024-06-29T10:24:00Z", "durationSeconds": 5040, "keystrokes": 4182, "mouseActiveSeconds": 610, "activeSeconds": 4700, "idleSeconds": 200, "sprintsCompleted": 2, "focusSessions": 3, "appSwitches": 5, "coinsEarned": 18, "longestFocusBlockSeconds": 840, "endReason": "user"},
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
  `appSwitches` — a counted foreground-app change; tracked on **macOS only**
  and always `0` on Linux, shown as-is with no special-casing (no focus
  detection there, ADR 0009). Seconds are whole seconds; the frontend
  formats them (`fmtDuration`/`fmtInt`), never the server. `stats` itself
  stays optional so a pre-A1 server degrades to an all-zero block.
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
  isActive, longestFocusBlockSeconds }` — the same seven A1/A2 counters as
  `stats.today`/`stats.lifetime`, plus `coinsEarned` (that day's total, the
  same sum `coinsToday`'s four fields add to) and `isActive` (a **bool set by
  the server**, never re-derived client-side: `activeSeconds >=
  game.ActiveDayMinSeconds`, default **300s** — the client and the streak
  banner must agree on one "active day" definition, so the client only
  renders this flag). `longestFocusBlockSeconds` is Fork B of A3-design.md §0
  (shipped by default) — the day's longest single sustained-typing run.
  camelCase throughout; `history` stays optional so a stale (pre-A3) server
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
  `stats`: under the existing TODAY/LIFETIME keystroke/mouse/active/idle/
  sprints rows it adds a **Focus sessions** row (`today.focusSessions` /
  `lifetime.focusSessions`) and an **App switches** row
  (`today.appSwitches` / `lifetime.appSwitches`), plus a **"Coins earned
  today"** block reading `stats.coinsToday` as four
  `label → count` lines (Keystrokes, Mouse, Focus sessions, App switches).
  All five/four values render with `fmtInt`; nothing is formatted
  server-side.
* `config` — Phase P1 (Identity & first minutes,
  `docs/plan/PRODUCT-EVOLUTION.md` §5). Exactly one field today,
  `config.name`: the dexel's name, **user-authored**, `""` when unset. The
  server always sends the block; it is optional client-side
  (`wire.ts: config?`) only so a pre-P1 server degrades to "unnamed".

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
    **server-computed**; the client never derives live time.
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
* `onboarding` — Phase P1. `true` **only** in the genuine first-launch state,
  decided **once, by the server, at boot**, as *(no save of any kind existed)*
  `&&` *(`config.name` is empty)*. Both halves matter: an existing
  `state.db`/`state.json`/legacy Rust save means somebody has played here even
  if they never named the dexel, and a named `config.json` means the one
  question onboarding asks is already answered even on a machine whose save was
  wiped. A tampered, future-schema or unreadable save all count as "a save
  existed" — the failure worth avoiding is nagging a returning user, not
  missing an intro. `SET_NAME` clears it for good; nothing sets it again for
  the life of the process, and **no client action can ever set it**. Optional
  client-side, defaulting to false. See §7.
* The **`[H] HISTORY`** modal (`features/history-modal.ts`, A3, ADR 0013) is
  a second read-only render, opened the same way `[A]`/`[S]` are — native
  `<dialog>`, its own titlebar button, the `H` key, `Esc` to close — and
  gates nothing (no earning to freeze, same as Activity). Rect: `#history`
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
{"action": "SESSION_START", "name": "auth refactor"}   // name optional
{"action": "SESSION_STOP"}
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
* `SET_NAME` (Phase P1, §7) sets the dexel's name. `name` is **raw user
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
  rename with the same action and no new wire.
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
* **[DESIGN CALL] camelCase field names throughout** (`itemId`, not the PDF
  blueprint's `item_id`). The whole payload is consumed by JavaScript; one
  casing convention across the wire and the DOM removes a whole class of typo.

## 7. The onboarding modal

Phase P1 — Identity & first minutes (`docs/plan/PRODUCT-EVOLUTION.md` §5,
§2.9). Shown **once, ever**, on a genuine fresh install: name your dexel,
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
> of garments, and this modal has no such context. Showing the dexel
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
> server never sent, which §6.1's whole contract forbids. Naming the dexel
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

Two places, both rendered by `render/chrome.ts` (which already owns the
titlebar and the status panel) from `state.config.name`, as typed — never
upper-cased to match the surrounding labels, and never assembled into a
sentence (§3's zero-client-side-assembly rule; the one composed string, the
welcome toast, is composed by the server).

| Where | id | Behaviour |
|---|---|---|
| Status panel, bottom-right | `#status-name` | `left:6 top:55 288x10`, `var(--gold)`, right-aligned, truncated to 24. Sits in the 12px of previously-**empty** space below `#ticker` (the panel's padding box is 66px tall; the ticker ends at 54). Empty — and therefore invisible — until named. |
| Hamburger panel heading | `#menu-panel-title` | The static text `MENU` becomes the dexel's name once set, truncated to 16, falling back to `MENU` when unset. |

> **[DESIGN CALL] / FLAGGED FOR THE OWNER: not the titlebar cluster.**
> P1's exit criterion says the name is "echoed in the HUD/titlebar", but the
> standing owner directive is that the top-left titlebar cluster is
> **coin-then-level only** — nothing added to it, and nothing left of or
> before the coin (§2.1, BUG-2). Both echoes above therefore stay out of
> that cluster: one takes dead space in the status panel (always visible,
> no layout moved), the other replaces a static label. If the owner wants
> the name in the titlebar proper, `#hud-level` ends at x120 and
> `#menu-open` starts at x600, so there is room for a name box at roughly
> `left:128 top:8 w:340 h:8` — one CSS rect plus one line in
> `renderChrome`, and it would *not* disturb the coin/level cluster itself.

### 7.5 Verified in the real running game

Per `feature-build-and-verify`'s gate, against the built binary with the fake
provider and temp `$HOME`s: the modal appears on a fresh install; typing
`sasha mh` into the name field opens **no** modal (§5.2's guard) while `[S]`
still opens the store the moment nothing is focused; a locked swatch cannot
be selected; confirming closes the modal, shows the `welcome` flash and
echoes the name in both places; `config.json` holds the name and `state.db`
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

## 9. The Sessions modal

Phase P2 — Sessions & the session-complete moment
(`docs/plan/PRODUCT-EVOLUTION.md` §3 Bet 1 / §5 Phase P2, ADR 0017,
`docs/plan/P2-design.md`). A session is a user-declared work interval:
start it (optionally named), work, stop it, and dexel hands back a cozy
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
therefore invisible, when no session is active** — the same idiom
`#status-name` already uses. Owned by `render/chrome.ts` (a render-layer
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
