# UI spec — dev-companion v2 (HTML / NES.css frontend)

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
      <span id="title-text">dev companion</span>
      <span id="mood-dot"></span>
      <span id="hud-level">LV 1</span>
      <span id="hud-cash"><i class="nes-icon coin is-small"></i> 0</span>
      <button id="store-open" class="nes-btn">[S] STORE</button>
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

    <div id="scrim"></div>            <!-- shown only while the store is open -->
    <dialog id="store" class="nes-dialog is-dark"> … §4 … </dialog>
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
  | dev companion  ●                    LV 5  ◆ 2150  [ [S] STORE ]|
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

| Element | Rect (x, y, w, h) | Content |
|---|---|---|
| `#titlebar` | 0, 0, 640, 24 | `background: var(--metal)`; 2px bottom border `var(--wall-light)` |
| `#title-text` | 8, 8, 104, 8 | `dev companion`, 8px, `var(--cream)` (13 chars) |
| `#mood-dot` | 120, 8, 8, 8 | solid square, colour per the `activeState` table in docs/art-direction.md ("Visual states"). `border-radius: 0` — an 8px circle is mush |
| `#hud-level` | 456, 8, 40, 8 | `LV 5`, 8px, `var(--cream)`, right-aligned |
| `#hud-cash` | 500, 8, 44, 8 | coin icon + `2150`, 8px, `var(--gold)`, right-aligned |
| `#store-open` | 552, 4, 80, 16 | `nes-btn`, label `[S] STORE`, 8px, padding 0 4px |

Dev Cash lives here, next to the button that spends it. **[DESIGN CALL]** the
mockups only show the balance *inside* the store modal, but a currency you
cannot see is a currency you forget you have; the decluttering the user asked
for removed three header *panels*, not the score. One 8px text run next to the
button that spends it is one glanceable pair, and it keeps both bottom panels
single-purpose.

**[DESIGN CALL]** the button label is `[S] STORE`, not `STORE`. ADR 0010
requires the store be permanently discoverable *and* the shortcut be
discoverable; the bracketed key does both jobs in one element, which is how
the shipped Bevy build already presented it.

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

* The card list auto-scrolls to keep the selection fully visible.
* `Tab` is bound to *open* on the main screen but must **not** be captured
  inside the dialog beyond closing it — leave native focus cycling alone.
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
    "coinsToday": {"keystrokes": 6, "mouse": 2, "focusSessions": 4, "appSwitches": 0}
  }
}
```

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
* The Activity modal (`features/activity-modal.ts`) is a read-only render of
  `stats`: under the existing TODAY/LIFETIME keystroke/mouse/active/idle/
  sprints rows it adds a **Focus sessions** row (`today.focusSessions` /
  `lifetime.focusSessions`) and an **App switches** row
  (`today.appSwitches` / `lifetime.appSwitches`), plus a **"Coins earned
  today"** block reading `stats.coinsToday` as four
  `label → count` lines (Keystrokes, Mouse, Focus sessions, App switches).
  All five/four values render with `fmtInt`; nothing is formatted
  server-side.

**`flash`** — a transient toast for a discrete event.

```json
{"type": "flash", "kind": "purchase", "text": "Racer chair!"}
```

`kind` ∈ `purchase | equip | sprint | error`. Rendered for **1.5 s** as an 8px
line, `var(--gold)` for `purchase`/`sprint`, `var(--cream)` for `equip`,
`var(--pot)` for `error`. Position: over `#store-cash` while the modal is open,
otherwise over `#sprint-name`. Only one flash at a time; a new one replaces the
old.

### 6.2 Client → server

```json
{"action": "BUY_ITEM",   "itemId": "chair_racer"}
{"action": "BUY_TINT",   "itemId": "chair_racer", "tintId": "neon"}
{"action": "EQUIP_ITEM", "slot": "chair", "itemId": "chair_racer", "tintId": "neon"}
{"action": "STORE_OPEN"}
{"action": "STORE_CLOSE"}
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
* **[DESIGN CALL] camelCase field names throughout** (`itemId`, not the PDF
  blueprint's `item_id`). The whole payload is consumed by JavaScript; one
  casing convention across the wire and the DOM removes a whole class of typo.

## 7. Deferred, and why

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
