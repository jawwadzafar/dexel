# Art direction — Dexel v2 (behind-the-shoulder)

**This is a v2 rewrite. The v0.2 side-view composition is superseded.** The
camera has moved: we now sit in a mixed behind + slightly-elevated, 3/4
over-the-shoulder view of the hooded developer, looking past them at their
monitor — close enough behind, and high enough above, that both hands stay
visible on the keyboard, with the right hand reaching over to the mouse
during real mouse activity. Everything downstream of that — every sprite
silhouette, every anchor, the layer order — changes. See
"What survives from v0.2": for the PNGs the answer is *nothing*.

**Engine note (ADR 0011).** The shipping game is Go + HTML/JS/NES.css. This
doc is deliberately **engine-agnostic**: it specifies pixel sizes, pixel
offsets inside a 320x200 scene, and a back-to-front layer order. Those three
things are all any renderer needs, and CSS absolute positioning implements
them directly. Where a rendering *mechanism* is genuinely load-bearing (crisp
upscaling, runtime tinting) the CSS recipe is given explicitly.

Reference register is unchanged: a tiny cozy room you glance at while working
(*Little Writer Desktop Companion*: Pixel Graphics, 2D, Cozy, Minimalist,
Retro). We are not copying its assets, we are matching the register.

## Non-negotiables (the hard rules — unchanged in substance from v0.2)

1. **True pixel art, authored at native size.** Every sprite is drawn at the
   pixel size in the manifest and **never resampled at author time**. It is
   upscaled by an **integer** factor only, with nearest-neighbour sampling. In
   the browser that means `image-rendering: pixelated` on every `<img>` and
   every `background-image` in the scene, and an integer `transform: scale()`.
   No anti-aliasing, no gradients, no sub-pixel placement. A blurry or
   half-pixel sprite is the single fastest way to lose the look.

   **Amendment (window scaling, `docs/ui-spec.md` §0.1).** "Integer only"
   holds for AUTHORING and for the composition inside the scene, which is
   still exactly 2x. It cannot hold for the whole-window scale, because a
   resizable native window is not an integer multiple of 640x400: the honest
   rule is *integer whenever the window can fit one*. §0.1 therefore snaps to
   a crisp factor (an integer, or 1.5x/2.5x on retina where 1.5 CSS px is
   exactly 3 device px) whenever that costs at most 1/8 of the available
   size, and only falls back to a fractional scale when the alternative is a
   large empty margin. Below 1x there is no crisper factor to snap to. This
   is a deliberate trade: slightly uneven art pixels beat both a distorted
   aspect ratio and a window mostly full of dead space.
2. **The generator is the source.** `tools/gen_assets.py` is the source
   format; the PNGs in `app/assets/` are build artifacts, like object files.
   Regenerate with `python3 tools/gen_assets.py`. Never hand-edit a PNG.
   (Why raster PNGs and not SVG: see the last section — the reasoning holds
   for the browser too, for different reasons than it held for Bevy.)
3. **Determinism.** The generator must produce byte-identical PNGs on every
   run — no unseeded RNG, no time or locale inputs. Screenshots are a test
   artifact (ADR 0011 makes headless-browser capture the primary verification
   route); non-deterministic art makes them useless.
4. **Palette purity.** Every pixel of every *palette-authored* PNG is one of
   the 18 hexes below, or fully transparent. The generator asserts this.
   v2 adds exactly one carefully-scoped exception: **grayscale tint layers**
   (see "Recolourable parts").
5. **Contact shadows.** Anything resting on a surface ends in a 1px `shadow`
   row **wider than the object** at its base. Without it, props float.
6. **Warm, not clinical.** Dusk-purple room, warm wood desk, one pool of
   light. The screen is the only cool light in the frame; that is what makes
   it the focal point.
7. **Compact window.** The scene is authored at **320x200** and presented at
   **640x400** (integer 2x). A desktop companion sits beside your work.

## Palette (18 colours — use these exact hex values, do not improvise)

| Role | Hex | Notes (v2 use) |
|---|---|---|
| wall_dark | `#2f2a3d` | upper wall, dusk purple |
| wall_light | `#3d3550` | lit band on the wall behind the monitor |
| floor | `#5b4433` | warm wood floor (the two wedges either side of the chair) |
| floor_light | `#6d5240` | floorboard highlight |
| desk | `#8b5e3c` | desk surface seen from above |
| desk_dark | `#6b452b` | desk near-edge lip, grain, shading |
| metal | `#2b2b33` | monitor bezel, chair frame, mouse body |
| screen | `#7fd4c1` | live terminal text, LEDs, neon accents |
| screen_dim | `#4a8f83` | older terminal lines, dimmed screen |
| skin | `#e8b48c` | the developer's hands (the only skin on screen) |
| hair | `#3a2a20` | hood interior, cat body |
| shirt | `#4a7fa8` | cobalt tint source, denim accents |
| plant | `#4e8b4f` | leaves, forest tint source |
| pot | `#a45c3a` | terracotta, ember tint source |
| gold | `#e8c46a` | Dev Cash coin, trophies, duck, tufting |
| lamp | `#ffd98a` | warm light pooling, glow bloom |
| cream | `#f2e0c9` | UI text, paper, keycap legends |
| shadow | `#241f2e` | occlusion, contact shadows, terminal background |

Note the role shifts, not value shifts: `hair` is now mostly *hood interior*
(the head is inside a hood; almost no hair is visible from behind), and
`skin` shrinks to two 10x8 hand clusters. `shirt`/`plant`/`pot` do double duty
as tint sources (below). The 18 palette hexes are also the CSS palette — the
frontend's chrome (panels, buttons, text) uses these and nothing else.

## Geometry: one coordinate system, two stacked layers

**Every number in this document is a "room pixel": origin at the scene's
top-left, `x` right, `y` down, `x in 0..320`, `y in 0..200`.** That is the
coordinate system you are already in when you author a PNG, and it is the one
the frontend positions in.

The scene renders as **two stacked layers**, both anchored to the same
top-left corner:

```
#scene-sprites   320 x 200,  transform: scale(2); transform-origin: top left;
                 -> children are positioned in ROOM PIXELS, verbatim.
                    left: 94px; top: 20px;   is the monitor. No conversion.

#scene-text      640 x 400,  unscaled overlay on top of #scene-sprites
                 -> children are positioned in UI PIXELS = room px * 2.
                    Holds ONLY live text: the terminal lines and the mood dot.
```

Why the split: a `transform: scale(2)` container would also scale text, so the
terminal's 4-room-px line height would have to be rendered as a 4px font and
then blown up — illegible mush. Text therefore lives unscaled at its natural
8px size in the overlay. Sprites live scaled so their placement table *is* the
CSS.

The identity `ui_px = room_px * 2` must hold exactly. A test should assert
`scene_sprites.scale == 2` and `#scene-text` size `== 640x400`, because the
monitor's screen region is authored in room px but drawn as text in UI px, and
a silent offset there would put the terminal half off the bezel.

### Occluded bands

The window chrome is drawn over the full scene, so two horizontal bands of the
scene are never seen. Compose accordingly:

```
room row   0 ..  12   HIDDEN  (title bar, 24 UI px)
room row  12 .. 160   VISIBLE (148 rows — the whole composition lives here)
room row 160 .. 200   HIDDEN  (bottom panels, 80 UI px)
```

The bottom band doing the occluding is load-bearing, exactly the way the desk
edge was in v0.2: it cuts the character off at the upper back and hides every
chair base, so nobody has to draw a convincing seated lower body.

## Composition: the behind-the-shoulder scene

```
 x=0                              x=160                            x=320
  +-------------------------------------------------------------------+ y=0
  |###################### title bar occludes #########################| 12
  |      +------+                                                     |
  |      | WALL |        +---------------------------+                | 16
  |      | SLOT |        |         MONITOR           |      +----+    | 20
  |      |40x44 |        |   +-------------------+   |      |PLNT|    | 24
  |      +------+        |   |  SCREEN  REGION   |   |      | 40 |    |
  |                      |   |  124 x 44 room px |   |      |  x |+--+| 32
  |    wall_dark         |   |  = 11 text lines  |   |      | 44 ||BD|| 46
  |                      |   +-------------------+   |      |    ||28|| 68
  |                      |   chin + power LED        |      |    ||30||
  |======================|====neck / foot============|======+----++--+| 74  <- desk far edge
  |  desk (from above)   +---------------------------+                |
  |          +----+      +--------------------------+   +----------+  | 90
  |          |BEV |      |        KEYBOARD          |   |MOUSE+PAD |  |
  |          |20x24      |         96 x 24          |   |  44 x 24 |  |
  |          +----+      +--------------------------+   +----------+  | 114
  |                       \  hands (in dev sprite) /                  |
  |                        \                      /                   |
  |  +---------------------|--------------------|-------------------+  | 116  <- chair top (basic)
  |  |    CHAIR wing       |    HOOD  DOME      |    CHAIR wing    |  |
  |  |                     |   (dev 88x104)     |                  |  | 132  <- desk near lip / floor line
  | floor wedge            |     shoulders      |      floor wedge    |
  |                        |                    |                     |
  |###################### bottom panels occlude ######################| 160
  +-------------------------------------------------------------------+ 200
```

The pose that makes this read: **the arms go around the outside of the head.**
The hood dome's apex sits *below* the keyboard's bottom row; the shoulders are
below that; the forearms run from the shoulders out to the sprite's left and
right edges, then up past the dome's flanks, and the hands land on the
keyboard's near two-thirds. The gap between the two arms is transparent, so
you see desk and keycaps through it. This is the single most important
silhouette decision in the doc — get it wrong and the character reads as a
blob pasted over the desk.

### Element placement table (room px, top-left origin)

These `(px, py)` pairs are literally `left`/`top` inside `#scene-sprites`.

| Element | Sprite size | left, top | Occupies x | Occupies y |
|---|---|---|---|---|
| room back (wall+floor) | 320x200 | 0, 0 | 0..320 | 0..200 |
| wall slot | 40x44 | 24, 16 | 24..64 | 16..60 |
| monitor | 132x64 | 94, 20 | 94..226 | 20..84 |
| **screen region (no sprite)** | 124x44 | 98, 24 | 98..222 | 24..68 |
| desk slab | 320x58 | 0, 74 | 0..320 | 74..132 |
| plant slot | 40x44 | 244, 32 | 244..284 | 32..76 |
| buddy slot | 28x30 | 288, 46 | 288..316 | 46..76 |
| beverage slot | 20x24 | 56, 90 | 56..76 | 90..114 |
| keyboard slot | 96x24 | 112, 90 | 112..208 | 90..114 |
| mouse slot | 44x24 | 224, 90 | 224..268 | 90..114 |
| chair slot | per style | x-centred on 160, **bottom edge = row 200** | | |
| developer | 192x76 | 64, 92 | 64..256 | 92..168 |

Chair `left`/`top` derive from the anchor rule below:

| Chair style | Size | left | top |
|---|---|---|---|
| `chair_basic` | 112x58 | 104 | 142 |
| `chair_racer` | 116x58 | 102 | 142 |
| `chair_exec` | 120x58 | 100 | 142 |
| `chair_antigrav` | 96x58 | 112 | 142 |

### Derivation rules (so nothing is a magic number)

* **Desk far edge = room row 74.** Everything that *stands on the desk's back
  edge* (plant, buddy, monitor foot) has its base 2 rows past it, at row 76,
  so its 1px contact shadow lands on desk pixels rather than on the seam.
* **Desk near lip = rows 128..132**, painted `desk_dark`, with a 2-row
  `shadow` band at rows 132..134 on the floor: the slab's cast shadow.
* **Wall/floor line = room row 132**, identical to the desk's near edge. The
  wall band from 74..132 is fully occluded by the desk slab but must still be
  painted in `room_back.png`, so swapping in a narrower desk later cannot
  punch a hole through the room.
* **Desk-surface item baseline = room row 114.** Keyboard, mouse and beverage
  all end there, so their contact shadows form one straight line and they read
  as sitting on the same plane.
* **Chair anchor = bottom-centre**: bottom edge always at room row 200, centre
  column always x=160. So `left = 160 - w/2` and `top = 200 - h`, a taller
  style grows *upward*, and no per-style offset table has to be maintained by
  hand (the table above is derived, not authored).
* **HARD chair constraints (behind-view compositing):** the chair now
  composites ON TOP of the developer (see "Layer order" below), so two
  regions are asserted per chair sprite: (1) no chair pixel in the keyboard's
  column band `x[112,208]` above room row 116, so the chair can never paint
  over a purchasable keyboard; (2) no chair pixel above room row 144 at all —
  the developer's shoulder line. Everything of the figure nearer the camera
  than the chair (hood, arms, hands) stays above that line; everything the
  chair should occlude (mid/lower back) is below it.
* **Developer anchor** is fixed at (64, 92) for all five frames and all
  hoodie layers; the frames are same-canvas overlays, never re-anchored.

### Layer order (back to front)

The frontend implements this as ascending `z-index` on absolutely positioned
elements inside `#scene-sprites`. The *numbers* are arbitrary; the *order* is
the contract.

```
 1  room_back                     (wall + floor)
 2  wall_<item>                   on the wall
 3  desk_back                     the slab
 4  monitor                       stands on the desk's far edge
 5  plant_<item>                  on the desk's far edge
 6  buddy_<item>                  on the desk's far edge
 7  bev_<item>                    on the desk surface
 8  kb_<item>                     on the desk surface
 9  mouse_<item>                  on the desk surface
10  dev_form_<frame>              grayscale hoodie fabric, runtime-tinted
11  hoodie_<style>                palette-pure style overlay, frame-independent
12  dev_base_<frame>              palette-pure: hands, hood interior, outline
13  chair_<style>_form            grayscale, runtime-tinted
14  chair_<style>_detail          palette-pure: frame, wheels, stitching
--- #scene-text overlay, above every sprite ---
15  terminal lines inside the screen region
--- window chrome, above the scene ---
16  title bar / bottom panels / store modal / scrim
```

Two prose statements the list is only an encoding of. **If the prose and the
list ever disagree, the prose wins.**

1. **Desk items sit behind the developer, and the developer's hands sit on top
   of the keyboard.** That is why the dev layers still draw above the desk/
   keyboard layers: the hands are part of the dev sprite, so putting the dev
   above the keyboard puts the hands on the keys for free, with no sprite
   splitting and no separate hand element to keep in sync.
2. **The chair sits in front of the developer, not behind.** From behind a
   seated person, the chair's backrest is nearer the camera than their torso,
   so `chair_form`/`chair_detail` composite above the three dev layers — the
   exact reverse of the old top-down order. Every chair sprite starts at or
   below room row 144 (the developer's shoulder line, per the hard
   constraints above), so the chair occludes the mid/lower back it should
   occlude and nothing above that seam — never the keyboard, never the hood,
   arms or hands.

## The screen region — a spec, not a picture

`monitor.png` paints the bezel, the chin with its `screen` power LED, the neck
and foot, and fills its inner rect flat `shadow`. **It paints no text.** The
frontend draws terminal lines into that rect at runtime, in `#scene-text`.

| | room px | UI px (x2) |
|---|---|---|
| screen region | (98, 24) 124x44 | (196, 48) **248x88** |
| text area | — | (200, 48) **240x88** (4px inset L/R; flush to the glass top/bottom so all 11 lines at 8px fit inside it) |
| line height | 4 | **8** |
| line count | 11 | 11 |
| chars per line | — | **30** (8px `Press Start 2P`; see docs/ui-spec.md, which locks the font) |

* Newest line at the **bottom**, older lines scroll up and out at the top —
  a terminal, not a marquee. `overflow: hidden`, no scrollbar, ever.
* Colour: newest 2 lines `screen` `#7fd4c1`, all older lines `screen_dim`
  `#4a8f83`. Background is the sprite's `shadow` fill; the text container is
  transparent.
* `coding` — push a new line every **0.35 s**.
* `idle` — no scroll; a 1x8 UI px `screen` block cursor blinks at 0.5 s on the
  last line.
* `onBreak` — no scroll, every line `screen_dim`, the monitor **sprite** itself
  dimmed to 55% brightness (`filter: brightness(0.55)`), and the last line
  reads `-- idle --`.
* **Content rule:** lines come from a static, compiled-in pool of fake
  code/log strings. Never a file path, window title, app name, clipboard, or
  any other runtime string derived from the user's machine (ADR 0002). The
  pool ships in the backend as a `[]string` constant and is broadcast to the
  frontend as already-chosen literal lines — no client-side string assembly
  from state fields.
* **Font:** this element wants **monospace** — proportional text in a terminal
  frame is the one thing that will read as fake. Use a pixel bitmap monospace
  webfont. `docs/ui-spec.md` locks this to **`Press Start 2P` at 8px**, an 8x8
  cell, which gives **30 characters** across the 240px text area. Set
  `font-size` and `line-height` to the same integer px, never a fractional
  value, and cap every authored pool line at 30 characters.

## Recolourable parts: grayscale form + palette detail

N colours must not cost N sprites. Two recolourable regions in v2 — **the
hoodie** and **the chair** — the two largest pixel areas where a recolour
actually reads at 2x.

### Authoring convention

A recolourable part ships as two same-canvas, same-anchor PNGs:

* **`*_form.png` — grayscale only**, from this exact 5-step ramp, plus full
  transparency. Nothing else. This is the layer that gets tinted.

  | Step | Value | Meaning | Composites to |
  |---|---|---|---|
  | 1 | `#4d4d4d` | deep fold / underside | 30% of the tint |
  | 2 | `#7a7a7a` | shadow side | 48% |
  | 3 | `#a8a8a8` | mid tone | 66% |
  | 4 | `#d4d4d4` | **base fabric** | 83% |
  | 5 | `#ffffff` | specular edge (use sparingly, <5% of pixels) | 100% |

  Note the ramp is *multiplicative and darkening-only*: step 5 reproduces the
  tint exactly and every other step is darker. Author the large flat areas at
  **step 4**, so a tint has headroom for a highlight.

* **`*_detail.png` — palette-pure**, drawn over the form: the 1px `shadow`
  silhouette outline, plus fixed-colour hardware (chair frame in `metal`,
  wheels, `cream` stitching, `gold` tufting).

> **Palette-purity exception (the only one).** The generator's 18-colour
> assertion runs on palette-authored PNGs. `*_form.png` files are exempt and
> are instead asserted against the 5-step grayscale ramp above. Tint hexes
> live in **one place in the codebase** (the item catalog's tint table), are
> never painted into a PNG, and so cannot drift the generator's palette check.

### The CSS tint mechanism

The form PNG is used **twice**, from one file: once as an alpha **mask** on a
solid-tint fill (giving a flat silhouette in the tint), and once as a
`mix-blend-mode: multiply` overlay on top (re-applying the grayscale shading).
The result is `tint * ramp_value / 255`, per pixel, GPU-composited, with no
interpolation.

```html
<div class="tintable" style="--tint:#6a5aa0; left:64px; top:92px;
                             width:192px; height:76px;">
  <div class="tint-fill" style="--form:url(dev_form_type_a.png)"></div>
  <img class="tint-shade" src="dev_form_type_a.png" alt="">
</div>
```

```css
.tintable   { position:absolute; isolation:isolate; }   /* isolation is REQUIRED:
                 without it the multiply blends against the whole scene */
.tint-fill  { position:absolute; inset:0; background:var(--tint);
              mask-image:var(--form); -webkit-mask-image:var(--form);
              mask-size:100% 100%; mask-mode:alpha; }
.tint-shade { position:absolute; inset:0; mix-blend-mode:multiply;
              image-rendering:pixelated; }
```

Three notes an implementer will otherwise get wrong:

* `isolation: isolate` on the wrapper is **mandatory**. Without it the
  multiply reaches through to the wall and desk behind and darkens them.
* The mask keeps the solid fill inside the silhouette. Skip the mask and you
  get a tinted rectangle.
* A store **swatch chip** must show `tint x 0xd4/0xff` (the step-4 base-fabric
  composite), not the raw tint — otherwise the chip is visibly brighter than
  the garment it promises. Compute it in JS from the same tint hex.

**Fallback if `mix-blend-mode` misbehaves in the macOS WebKit webview:** split
each `*_form.png` into up to five 1-bit alpha masks (one per ramp step) and
give each masked div a JS-computed shade of the tint. Same output, five files
and five divs per part instead of one — only take this route if a real
rendering defect is observed.

### Tint table (6 colours)

Four are lifted straight from the palette; two are declared tint-only because
the palette has no magenta and no bright indigo, and adding a 19th and 20th
palette colour to serve one runtime multiply is a worse trade than declaring
them here.

| Tint id | Store name | Hex | Source |
|---|---|---|---|
| `slate` | Classic Black | `#2b2b33` | palette `metal` |
| `cobalt` | Cobalt Blue | `#4a7fa8` | palette `shirt` |
| `forest` | Forest Green | `#4e8b4f` | palette `plant` |
| `ember` | Cyberpunk Orange | `#a45c3a` | palette `pot` |
| `neon` | Neon Pink | `#e86aa4` | **tint-only** |
| `indigo` | Midnight Indigo | `#6a5aa0` | **tint-only** (default hoodie) |

`indigo` is the default hoodie tint and matches the mockups' hood. Because
`indigo` at ramp step 2 lands close to `wall_light`, the **1px `shadow`
outline in `dev_base_*.png` is mandatory, not decorative** — it is what
guarantees the character's silhouette separates from the wall for *every* tint
in the table, present and future.

## Sprite manifest v2

47 authored files. Native (authored) pixel sizes, before integer upscale.
Every palette-authored file paints only the 18 palette colours; every
`*_form.png` paints only the 5-step ramp; everything resting on a surface ends
in a 1px `shadow` contact row wider than itself.

### Fixed scenery (3)

| File | Size | Content |
|---|---|---|
| `room_back.png` | 320x200 | Wall rows 0..132 (`wall_dark`, with a `wall_light` glow pool centred behind the monitor and a soft `lamp` bloom at its core), floorboards rows 132..200 (`floor`/`floor_light`, 3 board seams), a 2-row `shadow` cast band at rows 132..134 |
| `desk_back.png` | 320x58 | The slab from above-behind: `desk` surface with 4 `desk_dark` grain rows, a `wall_light` sheen band under the monitor's glow, near lip rows 56..58 in `desk_dark`, 1px `shadow` under the lip |
| `monitor.png` | 132x64 | `metal` bezel (4px left/right/top), inner rect (4,4)-(128,48) filled flat `shadow` — **no text**, an 8px chin rows 48..56 with one `screen` power LED, neck+foot rows 56..64 with a `shadow` contact row |

### Developer (14) — all 192x76, identical canvas and anchor

| File | Size | Content |
|---|---|---|
| `dev_form_idle.png` | 192x76 | Grayscale hoodie fabric. Arms up and around the dome, hands resting flat on the keys, no motion |
| `dev_form_type_a.png` | 192x76 | Typing pose A |
| `dev_form_type_b.png` | 192x76 | Typing pose B — **differs from A only in forearm/sleeve/cuff pixels**, so the 2-frame loop reads as typing, not as the whole body jittering |
| `dev_form_mouse.png` | 192x76 | Mouse pose — the right arm leaves the keyboard for a longer reach to the mouse (room x224..267); its own explicit path, not an offset of a typing frame |
| `dev_form_sleep.png` | 192x76 | Slumped: shoulders 4 rows lower, dome tipped 3px forward, arms fallen off the keys |
| `dev_base_idle.png` | 192x76 | Palette-pure: two `skin` hand clusters (~10x8 each), `hair` hood-interior crescent, 1px `shadow` silhouette outline |
| `dev_base_type_a.png` | 192x76 | as above, pose A hands |
| `dev_base_type_b.png` | 192x76 | as above, pose B hands (only the hands differ) |
| `dev_base_mouse.png` | 192x76 | as above, mouse-pose hands (right hand on the mouse) |
| `dev_base_sleep.png` | 192x76 | as above, slumped, plus a 5x5 `cream` `z` glyph above the dome's right shoulder |
| `hoodie_classic.png` | 192x76 | Style overlay, frame-independent (it paints only the **back panel and hood**, which do not move between frames): `shadow` drawstrings, kangaroo-pocket seam |
| `hoodie_zip.png` | 192x76 | `metal` zip teeth up the hood seam, `cream` pull tab |
| `hoodie_tech.png` | 192x76 | `desk_dark` cross-strap, a `metal` buckle, one `screen` reflective stripe across the shoulders |
| `hoodie_cloak.png` | 192x76 | `gold` hem trim, a long draped `shadow` fold pattern down the back panel |

### Chair (8) — bottom-centre anchored at room row 200 / x=160

| File | Size | Content |
|---|---|---|
| `chair_basic_form.png` | 112x58 | Grayscale: low mesh backrest, modest wings, seat sides |
| `chair_basic_detail.png` | 112x58 | `metal` gas cylinder + 5-star base, `shadow` outline |
| `chair_racer_form.png` | 116x58 | Grayscale: high bolstered wings, waisted back |
| `chair_racer_detail.png` | 116x58 | `cream` double stitching, `metal` frame, `shadow` outline |
| `chair_exec_form.png` | 120x58 | Grayscale: tall padded headrest wings, wide leather panels |
| `chair_exec_detail.png` | 120x58 | `gold` button-tufting dots, `desk_dark` armrest tops, `shadow` outline |
| `chair_antigrav_form.png` | 96x58 | Grayscale: wingless floating pod shell |
| `chair_antigrav_detail.png` | 96x58 | `screen` glow ring, 3 drifting `lamp` float pixels, `shadow` outline — **no wheels, no cylinder** |

### Keyboard (4) — 96x24 at (112, 90)

| File | Size | Content |
|---|---|---|
| `kb_membrane.png` | 96x24 | Flat `metal` slab, 4 rows of dull `desk_dark` key highlights |
| `kb_mech.png` | 96x24 | Taller `metal` caps with `cream` legend pixels, visible plate |
| `kb_split.png` | 96x24 | Two halves with an 8px centre gap, each tented 2px |
| `kb_neon.png` | 96x24 | 60% footprint (72px, centred in the canvas) with a `screen`/`gold`/`plant` underglow row |

### Mouse (4) — 44x24 at (224, 90)

| File | Size | Content |
|---|---|---|
| `mouse_stock.png` | 44x24 | `shadow` pad + small 2-button `metal` mouse |
| `mouse_gaming.png` | 44x24 | Pad + shaped body, one `screen` accent pixel, visible scroll notch |
| `mouse_trackball.png` | 44x24 | Pad + wide body with a `gold` ball |
| `mouse_vertical.png` | 44x24 | Pad + upright `metal` wedge seen edge-on |

### Beverage (4) — 20x24 at (56, 90)

| File | Size | Content |
|---|---|---|
| `bev_mug.png` | 20x24 | Chipped `cream` mug, `pot` band, 2 `cream` steam pixels |
| `bev_thermos.png` | 20x24 | Tall `metal` flask, `desk_dark` cap |
| `bev_teacup.png` | 20x24 | `cream` cup on a `cream` saucer, `pot` rim line |
| `bev_energy.png` | 20x24 | `screen`-green can, `gold` pull tab |

### Plant (3 + one empty) — 40x44 at (244, 32), base row 76

| File | Size | Content |
|---|---|---|
| *(plant_none)* | — | No file. The slot element is `display: none` |
| `plant_succulent.png` | 40x44 | Small `plant` rosette in a `pot` cube; only the lower 24 rows are painted |
| `plant_monstera.png` | 40x44 | Tall split `plant` leaves filling the canvas, `pot` planter |
| `plant_bonsai.png` | 40x44 | Gnarled `desk_dark` trunk, flat `plant` canopy, wide shallow `pot` tray |

### Wall (3 + one empty) — 40x44 at (24, 16)

| File | Size | Content |
|---|---|---|
| *(wall_bare)* | — | No file. Slot hidden |
| `wall_poster.png` | 40x44 | `shadow` frame, `cream` paper, ragged `desk_dark` text rows — **no legible words** |
| `wall_shelf.png` | 40x44 | `desk` plank, 4 book spines (`shirt`/`plant`/`pot`/`metal`), one `gold` trophy |
| `wall_neon.png` | 40x44 | A `screen` tube glyph with a `lamp` bloom halo on the wall behind it |

### Buddy (4 + one empty) — 28x30 at (288, 46), base row 76

| File | Size | Content |
|---|---|---|
| *(buddy_none)* | — | No file. Slot hidden |
| `buddy_duck.png` | 28x30 | `gold` body, `pot` beak, `shadow` eye — bottom-centred in the canvas |
| `buddy_bot_a.png` | 28x30 | Boxy `metal` bot, `screen` eye pixels, `gold` antenna bead |
| `buddy_bot_b.png` | 28x30 | **Differs from A only in the 2 `screen` eye pixels** (blink) |
| `buddy_cat.png` | 28x30 | Curled sleeping `hair` cat seen from above-behind, `cream` tail tip |

### HUD currency — COIN (3) — 16x16, NOT a scene sprite

dexel's own currency mark, authored by `tools/gen_assets.py` alongside the
scene sprites but kept in its own `COIN_SPEC` manifest because it is a **UI
icon**, not a behind-the-shoulder scene prop: it has no anchor in the 320x200
room, no store thumbnail, and — because it floats in the HUD bar rather than
resting on a surface — **no contact shadow** (the one manifest-wide rule it is
exempt from, by the same "nothing is resting on anything" logic). It replaces
the NES.css cash glyph in the top-left HUD (`#hud-cash`) and on the store's
`TOTAL DEV CASH` line, both of which render a 16x16 CSS-px box at 1x, so the
coin is authored at **16x16 native** — displayed 1:1, or a clean
nearest-neighbour 2x on a retina panel. One key light, upper-left, like
everything else. The warm depth ramp is entirely in-palette: `pot` < `gold` <
`lamp` < `cream`.

| File | Size | Content |
|---|---|---|
| `coin.png` | 16x16 | A 14px `gold` disc (1px clear margin): lit `lamp` rim on the upper-left arc, `pot` (ember) shadow rim on the lower-right, a `cream` specular glint high-left, and a `pot` "$" stamped through the centre so it reads as currency at 16px |
| `coin_react_a.png` | 16x16 | Click-react frame A: the coin turned **edge-on**, a narrow vertical ellipse mid-flip — lit `lamp` left face, `pot` shadow right, a `cream` catch-light down the lit edge, no "$" |
| `coin_react_b.png` | 16x16 | Click-react frame B: the coin landed back face-up (identical to `coin.png`) with a `cream`/`lamp` 4-point sparkle thrown off its upper-right rim — the satisfying end of the flip. Same canvas/anchor as `coin.png`, so the wiring swaps `src` in place |

The click sound that pairs with this react is `coin.wav` (`tools/gen_sounds.py`):
a two-note B5→E6 square-wave "ching", 0.18s, peak −14 dBFS.

### Store thumbnails (derived, not authored)

The store grid draws each item into a 40x40 box. Rather than 32 more hand
sprites, the generator **derives** `thumb_<item_id>.png` at 40x40 from the
item's real sprite: integer nearest-neighbour downsample (÷1, ÷2 or ÷4 only)
then centre-crop. Nearest downsample of palette pixels stays palette-pure, so
the assertion is unaffected.

If a derived thumb fails the vision self-check (unreadable at 40x40 — the
chair sprites at ÷4 are the likely casualties), the generator prefers a
hand-authored `thumb_<item_id>.png` if one exists. Overrides are the exception,
never the default.

Tint swatch chips in the store are **not sprites** — they are solid
`background-color` divs, coloured per the swatch rule above.

For tintable items (hoodie, chair) the thumbnail must be **two** derived
files, `thumb_<id>_form.png` and `thumb_<id>_detail.png`, so the store card's
thumbnail can use the same CSS tint recipe and show the item in the colour the
card is actually selling.

## What survives from v0.2

**None of the 29 existing PNGs.** Not one. Deleting all of them is the correct
migration, and here is why, per group:

| Existing files | Fate | Why |
|---|---|---|
| `room_bg`, `desk`, `rug` | **deleted** | Side-on camera. New wall/floor line, no window, no visible rug area, and the desk goes from a 120x48 profile slab to a 320x58 surface-from-above |
| `dev_idle/type_a/type_b/coffee/sleep` | **deleted** | Facing right in profile at 24x32. The behind view needs an 88x104 back view with a completely different silhouette and an arms-around-the-head pose |
| `chair`, `chair_t1`, `chair_t2` | **deleted** | Profile chairs. The behind view needs a symmetric back-and-wings silhouette, and chairs become a 4-style tinted slot |
| `monitor_on`, `monitor_off` | **deleted** | 40x36 three-quarter bezel with baked-in code lines. v2 needs a 132x64 front-facing bezel with a *defined empty* inner rect; on/off becomes a runtime dim, not a second file |
| `monitor_dual`, `monitor_ultra` | **deleted** | The monitor is no longer an upgrade slot (see the [DESIGN CALL] below) |
| `keyboard_t1/t2`, `mouse_t1/t2` | **deleted** | 20x8 and 8x6 profile props. The behind view needs 96x24 keyboards with readable keycap rows under the hands, and 44x24 pad+mouse |
| `mug`, `plant`, `books`, `lamp`, `poster`, `shelf`, `duck`, `cat`, `cat_a`, `cat_b` | **deleted** | All re-anchored and re-sized; `lamp` is dropped entirely (its job moves into the painted glow pool in `room_back`), `books` folds into `wall_shelf` |

What *does* survive is everything that matters more than the pixels: the
**generator**, the **18-colour palette**, the **drawing conventions** (1px
contact shadows, no dither, no anti-alias, palette assertion, deterministic
output), and the **per-sprite colour recipes** — `poster` is still "dark frame,
cream paper, ragged text rows"; `duck` is still "gold body, pot beak, shadow
eye"; `keyboard_t2`'s RGB idea becomes `kb_neon`'s underglow row.

## Character rules (v2)

* Readable at 192x76. The hood is a **dome**, not a head: no face, no ears, no
  hair fringe. The only interior detail is a `hair` crescent implying depth.
* **Never rotate, flip or re-anchor a frame.** The five frames (`idle`,
  `type_a`, `type_b`, `mouse`, `sleep`) are overlays on one canvas at one
  anchor, so the frontend swaps a `src` and nothing moves.
* The two typing frames differ **only** in forearm/sleeve/cuff/hand pixels.
  The `mouse` frame's right arm is a genuinely different, longer reach (to
  room x224..267) with its own explicit path, not an offset of a typing frame.
* Two `skin` hand clusters are the entire skin budget. Any more and the
  behind-view silhouette stops reading as a hooded figure.
* The hoodie style overlay may only paint the **back panel and hood**, which
  are static across all five frames. Anything that moves belongs in
  `dev_form_*`/`dev_base_*`.

## Visual states

Keyed off the broadcast `activeState` (see docs/ui-spec.md for the field).

| `activeState` | Developer frames | Screen | Mood dot |
|---|---|---|---|
| `coding` | `type_a`/`type_b` alternating at **5 fps** (200 ms per frame) | new line every 0.35 s | `plant` `#4e8b4f` |
| `idle` | `idle` (hands resting on keys, static) | no scroll, cursor blinks 0.5 s | `screen` `#7fd4c1` |
| `onBreak` | `sleep` (slump + `z`) | dimmed to 55%, all lines `screen_dim`, last line `-- idle --` | `pot` `#a45c3a` |

The AFK threshold that produces `onBreak` is a mechanics number, not an art
number; see ADR 0010 and the current tuning (~30 s).

Frame swapping must be driven by a fixed-interval timer, not by
`requestAnimationFrame` deltas — a 5 fps loop that drifts with the display
refresh reads as a stutter, not as typing.

## The application icon

The in-game art above is one artefact; the **app icon** is another, and it
lives in `desktop/src-tauri/icons/`. Same rules (generator is the source,
determinism, palette purity, integer nearest upscales, one key light from the
upper left), different job: a scene has 320x200 to explain itself in, an icon
has as little as 16x16 and no caption.

Source of truth: **`tools/gen_icon.py`**. Regenerate with

```
python3 tools/gen_icon.py                      # writes + self-checks the set
python3 tools/gen_icon.py --sheet /tmp/s.png   # ...and a review contact sheet
```

Never hand-edit an icon PNG, and never open one in an image editor to "just
fix" a pixel — the next run overwrites it.

### The mark

**dexel, hooded, front on, faceless, on transparency.** A hooded bust that
fills the frame: the indigo hood's crown and cheeks as a ring of fabric, a
dark FACELESS opening where a face would be, two drawstrings hanging from the
hem over the chest. **No tile behind it and no eyes in it** — the mark stands
on true transparency so macOS, Windows and GNOME each frame it with their own
system chrome instead of a dusk rounded-square baked into the file.

The product's identity is a character, so the icon is the character rather
than a rebus about it (a monitor, a coin, a terminal — the mark this one
replaced was all three and said nothing about *whose* desk it is).

* **[DESIGN CALL] No tile, no eyes — the mark is transparent and faceless.**
  Owner direction, 2026-08: "remove the glowing eyes. remove background also
  so that on mac and other places it's just icon and the OS system." The dusk
  rounded-square tile and the two `screen`-mint eyes are both gone. The hood
  opening is now a plain dark `shadow` cavity, faceless by intent. The mark
  was scaled up to fill ~88–91% of the canvas (the tile's padding is no longer
  there to spend), with at least a 1px clear margin all round so a
  plate-compositing desktop can never clip the silhouette.
* **[DESIGN CALL] A full `ink5` keyline carries the silhouette on any ground.**
  A dark indigo hood on transparency is nearly invisible on a dark Dock — the
  tile used to be its contrast and the eyes its accent, and both are gone. So
  the whole outer edge of the bust is a 1px `ink5` outline: `ink5` is the
  brightest indigo the tint reaches, a 3.6:1 read on black and 3.2:1 on a
  #14131a Dock, so the figure's edge is legible on the darkest ground it will
  sit on. On a white taskbar the dark fabric body is the silhouette instead.
  This is the same "screen glow wrapping the figure" rim the in-game character
  gets, now closed into a self-contained keyline because there is no tile edge
  left to do the job. `gen_icon.py`'s `check_contrast` measures the black case.
* **[DESIGN CALL] The form reads by its own shading, not against a tile.** One
  key light from the upper left lifts the crown to `ink5`/`ink4` while the
  shoulders and the fabric under the hood's overhang fall to `ink2`/`ink3` — a
  flat indigo blob would be a lozenge, the shaded one is unmistakably a domed
  hood over shoulders. The `ink5` hem rolled around the opening's inner edge
  puts the brightest ink directly against the darkest, the crispest boundary
  available, and it is what still says "hood, seen front on" at 16px with no
  eyes to anchor it. The opening stays flat `shadow` — no `hair` lining: a warm
  arc along the *bottom* of it reads as a mouth and along the *top* as a
  bowl-cut fringe, both rejected off the review sheet in the previous version.

### Colour: derived, never picked

| Element | Colour | Where it comes from |
|---|---|---|
| hood fabric | `ink1`..`ink5` | `round(indigo * RAMP[i] / 255)` — the **default hoodie tint** `#6a5aa0` composited over the 5-step ramp by the exact formula the CSS tint mechanism uses. To the byte, the garment a player with a fresh save is looking at. |
| keyline (1px, all round) | `ink5` | the brightest step of the same tint — the self-contained outline that holds the silhouette on a dark ground now that there is no tile; see legibility, below |
| the opening | `shadow` | occlusion, as everywhere else — a dark, empty, faceless cavity |
| hem (1px, inside the opening) | `ink5` | the rolled front edge of the hood; brightest ink against the darkest, the boundary that reads the ring as a hood |
| drawstrings | `cream`, `gold` tip | UI text colour, and Dev Cash |
| background | *(none — alpha 0)* | the OS supplies the frame; every pixel outside the bust is fully transparent |

The five ink hexes are **computed in `gen_icon.py` from `gen_assets.RAMP`**,
not typed in, so re-cutting the ramp re-cuts the icon rather than silently
desynchronising it. They are the only colours in the icon outside the 18, and
they are the extension this document already sanctions: the tint table
declares `indigo` precisely because "the palette has no bright indigo".

### Five masters, integer upscales only

An icon set spanning 16px to 1024px cannot come from one drawing, because
rule 1 forbids resampling. It comes from five masters, each authored at its
own size, and every delivered size is an integer nearest upscale of one:

```
16 <- M16 x1     48 <- M48 x1     256  <- M64 x4
24 <- M24 x1     64 <- M64 x1     512  <- M64 x8
32 <- M32 x1    128 <- M64 x2     1024 <- M64 x16
```

Downsampling appears nowhere. 24 and 48 exist as masters because the Windows
`.ico` wants those frames and Explorer would otherwise be handed a resample.

Per-size reduction — **this is the difference between an icon set and a
resized picture**, and each row is a decision made by looking at the review
sheet at true size, not by rule:

| Master | What it drops, and why |
|---|---|
| M64 | nothing: the `ink5` hem around the opening, 2px drawstrings with a `gold` aglet, five-step banded fabric, the full `ink5` keyline |
| M48 | a smaller drawstring spread |
| M32 | 1px drawstrings; dithering, hem and keyline still on |
| M24 | **ordered dithering off** (at 24px a Bayer pattern is not a gradient, it is four stray pixels of noise), no drawstrings; hem and keyline still on |
| M16 | no drawstrings, no hem (an `ink5` hem 1px inside the `ink5` keyline leaves one mid pixel between two highlights and reads as a mis-registration, so the keyline alone carries the outer edge and the flat `shadow` opening carries the middle), brighter fabric — at 16px the ring has to win outright |

### Shading: bands with dithered seams, not a dithered gradient

The fabric uses the ink ramp with **`DITHER_SHARPNESS = 3.0`**, which squeezes
each ordered-dither transition into the middle third of its step. Unsharpened
(the `ramp_dither` behaviour that is correct for a 192x76 garment at 2x) a
slow gradient is a 50% checkerboard over a wide area, and at 512px — where
M64 x8 makes every dithered pixel an 8x8 block — that stops reading as cloth
and starts reading as halftone. Sharpened, most of the hood is a SOLID band
and only the transitions are mixed, which is what hand-drawn pixel-art
shading looks like. This was found by rendering the 512 and looking at it; it
is invisible at 64px and unmissable at 512.

Below 32px ordered dithering is switched off entirely rather than sharpened.

### The legibility contract

A tile-less, faceless mark lives or dies by two reads, and `gen_icon.py`
asserts a measured floor for each rather than an opinion:

| Read | Ratio | Floor | Why it matters |
|---|---|---|---|
| keyline (`ink5`) vs black | 3.58:1 | 2.0:1 | the whole silhouette on the darkest Dock it will sit on — the make-or-break, since there is no tile left to carry it |
| crown (`ink5`) vs opening (`shadow`) | 2.73:1 | 2.0:1 | whether the hood reads as a lit ring around a dark hole, the job the mint eyes used to do |

Reported (not floored) alongside them: the keyline against a #14131a dark Dock
(3.15:1) and the dark fabric body against a white taskbar (`ink1` vs white,
16.66:1 — on a light ground the fabric itself is the silhouette). The mark is
transparent, so it was judged on a review sheet that shows it on dark, light,
mid-grey AND a transparency checkerboard, plus mock macOS-dock, Windows-taskbar
(both themes) and GNOME-dash rows; the dark-ground read is make-or-break and
the sheet leads with it.

### Delivered files

`bundle.icon` in `desktop/src-tauri/tauri.conf.json` lists five, and
`gen_icon.py` asserts that list still matches at the end of every run:
`32x32.png`, `128x128.png`, `128x128@2x.png`, `icon.icns`, `icon.ico`. Also
written: `icon.png` (the 1024 master `tauri icon` re-derives from) and the
freedesktop hicolor ladder as loose `16x16.png` ... `512x512.png` for future
Linux packaging. No `Square*Logo` files — nothing consumes them.

* **`icon.ico`** carries 7 frames (16/24/32/48/64/128/256), each handed to
  Pillow at its exact size, because Pillow's ICO encoder LANCZOS-resamples
  any size it was not given directly.
* **`icon.icns`** is written by `gen_icon.py` itself, byte for byte, in the
  shape `iconutil -c icns` produces: ten chunks, no TOC, PNG payloads for the
  eight modern slots and RLE-compressed ARGB for `ic04`/`ic05`. Not
  `iconutil`, which is macOS-only and therefore cannot run on the machine
  that builds; and not Pillow's ICNS encoder, which omits `ic04`/`ic05` — the
  two chunks that keep Finder's 16pt list view crisp instead of
  smooth-downscaling a 32px slot.
* The self-check decodes **every** `.ico` frame and **every** `.icns` chunk
  back and compares it pixel for pixel against the master it came from. That
  is the assertion that catches a silent resample, and it is the reason the
  containers can be trusted from a Linux build host.

## Design calls

Where the source mockups were ambiguous or self-contradicting, the call and
the one-line reason.

* **[DESIGN CALL] The monitor is not an upgrade slot.** Its inner rect is
  load-bearing UI geometry (11 lines of terminal), and every variant would
  move it; a dual/ultrawide monitor would silently break the one element the
  whole composition points at.
* **[DESIGN CALL] The camera is a mixed behind + slightly-elevated, 3/4
  over-the-shoulder view, not top-down.** The mockups show both; this view is
  the only one that shows the *whole* monitor face and the hood at once while
  keeping both hands visible on the keyboard (and the right hand reaching to
  the mouse during real mouse activity), which is the stated reason for the
  view change.
* **[DESIGN CALL] The desk spans the full 320px width.** The mockups show room
  visible past the desk's ends; a full-width slab removes two edge seams that
  would need per-side shading and buys nothing visually at 2x.
* **[DESIGN CALL] Terminal text uses `screen`/`screen_dim` (teal-green), not a
  new pure green.** The mockups read as "terminal" because of the near-black
  ground and the monospace, not the exact hue; a 19th palette colour for one
  element is a bad trade against the palette lock.
* **[DESIGN CALL] Two tint-only hexes (`neon`, `indigo`) live in the catalog,
  never in a PNG.** The palette has no magenta and no bright indigo; scoping
  them to the runtime tint table keeps the generator's palette assertion
  intact.
* **[DESIGN CALL] Hoodie *styles* share one silhouette; only the back panel
  differs.** Otherwise every style needs its own 4 form frames *and* its own 4
  base frames — 32 sprites instead of 12, for a difference nobody sees behind
  a static back panel.
* **[DESIGN CALL] Tinting is mask + multiply from ONE grayscale PNG, not N
  pre-coloured PNGs and not a flat mask.** N pre-coloured PNGs is the thing
  the brief explicitly forbids; a flat mask alone throws away all shading and
  makes the hoodie a silhouette.
* **[DESIGN CALL] No coffee-sip animation in v2.** The beverage sits at
  x 56..76 and the developer's reach starts at x 116; a plausible sip needs a
  fifth frame *plus* a hide-the-mug rule. The slump already reads
  unmistakably. `dev_form_sip`/`dev_base_sip` + "hide `bev_*` while sipping"
  is the documented v2.1 addition.
* **[DESIGN CALL] Store thumbnails are generator-derived, with an authored
  override escape hatch.** 32 hand-authored 40x40 thumbs is a lot of art for a
  40x40 box; deriving them costs nothing and the override path covers the few
  that fail the vision check.
* **[DESIGN CALL] No speaker/mute icon in the ticker panel.** The mockups draw
  one, but the game has no audio; an icon for a feature that does not exist is
  the kind of thing the user called clutter.

## Why raster PNGs and not SVG (asked more than once, still no)

**The source format is not PNG — it is `tools/gen_assets.py`.** The PNGs in
`app/assets/` are build artifacts. Regenerate with `python3 tools/gen_assets.py`.

On the browser stack SVG is *loadable*, so the old "Bevy can't parse it"
argument retires — but the other two reasons are the real ones and they are
unchanged:

1. **Vector rasterisation anti-aliases.** Hard nearest-neighbour edges ARE the
   pixel-art look; a vector renderer works against it by design, and
   `shape-rendering: crispEdges` fights it back only approximately, at
   non-integer scales especially.
2. **Pixel art is inherently raster.** Expressing a 320x200 room as vectors
   means either ~64,000 one-pixel `<rect>` elements, or smooth vector shapes —
   a different art style, not this one.

Recolouring is already better than SVG could manage: the palette is one dict
at the top of the generator, and the palette assertion proves nothing drifted.
For **runtime** recolouring the right mechanism is the mask+multiply CSS
recipe above, which is exactly what the `*_form.png` convention formalises.
