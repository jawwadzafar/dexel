# Art direction — dev-companion v0.2

Reference: *Little Writer Desktop Companion* (Steam tags: Pixel Graphics, 2D,
Cozy, Cute, Minimalist, Retro, 1990's). We are NOT copying its assets; we are
matching the register: a tiny cozy room you glance at while working.

## Non-negotiables

1. **True pixel art.** Author every sprite at small native size (16-96px),
   render with `ImageSampler::nearest`, and scale up by an integer factor
   only. No anti-aliasing, no gradients, no sub-pixel placement — a blurry
   or half-pixel sprite is the single fastest way to lose the look.
2. **Warm, not clinical.** The current build is cold blue-grey (#1e2230) and
   reads as a debug scene. Cozy means warm wood, cream light, a lamp glow.
3. **A character must exist.** There is currently no developer on screen at
   all. The character is the emotional core — you are watching *them* work.
4. **Compact window.** A desktop companion sits beside your work, it does not
   fill the screen. 640x400 logical, integer-scaled.

## Palette (use these exact hex values — do not improvise)

| Role | Hex | Notes |
|---|---|---|
| wall_dark | `#2f2a3d` | upper wall, dusk purple |
| wall_light | `#3d3550` | lit band behind the desk |
| floor | `#5b4433` | warm wood floor |
| floor_light | `#6d5240` | floorboard highlight |
| desk | `#8b5e3c` | desk surface |
| desk_dark | `#6b452b` | desk edge/legs, shading |
| metal | `#2b2b33` | monitor bezel, PC case |
| screen | `#7fd4c1` | glowing monitor teal |
| screen_dim | `#4a8f83` | monitor when idle |
| skin | `#e8b48c` | character skin |
| hair | `#3a2a20` | character hair |
| shirt | `#4a7fa8` | character shirt (denim blue) |
| plant | `#4e8b4f` | leaves |
| pot | `#a45c3a` | terracotta |
| gold | `#e8c46a` | coins, level accents, highlights |
| lamp | `#ffd98a` | lamp glow, warm light pooling |
| cream | `#f2e0c9` | text, paper, bright accents |
| shadow | `#241f2e` | occlusion, under-desk |

## Required sprites

Native sizes are authored pixel sizes, before integer upscale.

| File | Size | Content |
|---|---|---|
| `room_bg.png` | 320x200 | wall + floor + skirting, lamp light pooling on the wall, a window with night sky + a few stars |
| `desk.png` | 120x48 | desk surface + legs, front edge shading |
| `monitor_on.png` | 40x36 | bezel + glowing teal screen with 3-4 faint code lines |
| `monitor_off.png` | 40x36 | same bezel, dim screen |
| `chair.png` | 28x36 | simple office chair, back visible behind character |
| `dev_idle.png` | 24x32 | developer seated, hands resting, eyes open |
| `dev_type_a.png` | 24x32 | typing frame A — hands slightly left/down |
| `dev_type_b.png` | 24x32 | typing frame B — hands slightly right/up (2-frame loop) |
| `dev_coffee.png` | 24x32 | holding a mug up, on break |
| `dev_sleep.png` | 24x32 | slumped, eyes closed, small `z` |
| `plant.png` | 24x32 | potted plant (the desk upgrade) |
| `mug.png` | 10x10 | coffee mug on desk |
| `books.png` | 32x24 | small stack of books / shelf |
| `lamp.png` | 20x28 | desk lamp, warm bulb |
| `rug.png` | 96x32 | oval rug on the floor |

### Upgrade tracks (docs/upgrade-design.md)

Appended rather than merged into the table above so the original 15 rows
stay untouched. Every sprite below still only paints the 18-colour palette
above, follows the same no-anti-aliasing/no-resize/no-dither rules, and (for
anything that rests on the desk or floor) ends in a 1px contact shadow wider
than the object, per `tools/gen_assets.py`'s lighting-pass convention.

| File | Size | Content |
|---|---|---|
| `keyboard_t1.png` | 20x8 | basic grey keyboard, 2 rows of key highlights |
| `keyboard_t2.png` | 20x8 | mechanical, darker body, 3 per-key RGB accents (`screen`/`gold`/`plant`) |
| `mouse_t1.png` | 12x8 | dark pad + small mouse on it (pad+mouse combined — see the naming note below) |
| `mouse_t2.png` | 8x6 | sleeker standalone mouse, one `screen` accent pixel |
| `monitor_dual.png` | 40x36 | tier-0 monitor's bezel language, twice: a main panel + a second, smaller panel beside it |
| `monitor_ultra.png` | 56x36 | one wide ultrawide panel, same bezel language (56x36, not the design doc's 56x30 — see the naming note below) |
| `chair_t1.png` | 28x36 | tier-0 chair silhouette + a headrest block |
| `chair_t2.png` | 28x38 | taller back, 2 `pot` accent stripes (gaming-chair energy) |
| `duck.png` | 8x8 | rubber duck: `gold` body, `pot` beak, `shadow` eye |
| `poster.png` | 24x30 | dark frame, `cream` paper, a taped-on `screen` accent + ragged text-line rows (no legible words) |
| `shelf.png` | 40x22 | wooden wall shelf, 4 book spines + one `gold` trophy |
| `cat_a.png` / `cat_b.png` | 20x12 each | sleeping curled cat, `hair` body, frames differ ONLY in tail pixels (flick) |
| `cat.png` | 20x12 | the `pet` track's shipped static sprite — pinned identical to `cat_a.png`/frame A until the tail-flick animation is wired up |

**Naming/sizing notes for the mechanics side:**

- The design doc writes the mouse tier as "mouse_t1 8x6+pad 12x8". Reconciled
  as ONE sprite: `mouse_t1.png` is the pad+mouse combo at 12x8, and
  `mouse_t2.png` is the standalone sleeker mouse at 8x6.
- `monitor_ultra.png` is 56x36, not the design doc's 56x30. Sprites are
  centred on their `Transform` in `scene.rs`, and the `monitor` upgrade slot
  renders at the tier-0 monitor's exact centre, in front of it
  (`Z_MONITOR_UPGRADE` over `Z_MONITOR`) — a shorter overlay would leave the
  base `monitor_on`/`monitor_off` sprite's top and bottom rows visible around
  it. Matching the tier-0 height keeps both tiers' footprints exactly
  superimposed (verified pixel-for-pixel against `monitor_on.png`); only the
  width grows, which is the part that should read as "ultrawide".
- `cat.png` is a 13th file alongside `cat_a.png`/`cat_b.png`: the mechanics
  side's `pet` slot currently loads one static sprite rather than the
  two-frame loop, so `cat.png` is generated pinned to frame A. `cat_a.png`/
  `cat_b.png` stay generated too, ready for when the animation is wired up.

## Character rules

- Readable at 24x32: big head, simple 2-pixel eyes, no mouth detail.
- Seen from the side/three-quarter at the desk, facing right toward the
  monitor. Never front-facing — a side view reads as "working".
- The two typing frames must differ ONLY in arm/hand pixels so the loop reads
  as typing, not as the whole body jittering.

## Layering (back to front)

`room_bg` -> `rug` -> `books` -> `chair` -> `dev_*` -> `desk` -> `monitor_*`
-> `lamp` -> `mug` -> `plant`

The character sits behind the desk edge but in front of the chair, so the desk
occludes their lower body — that is what makes them look seated *at* it.

**CORRECTION (this list previously had `desk` before `dev_*`, which
contradicted the sentence above and shipped that way):** the desk must be
drawn AFTER — i.e. in front of — the character, or nothing occludes the
character's legs and they read as standing in front of the desk with their
torso floating. Everything that rests ON the desk surface (`monitor_*`,
`lamp`, `mug`, `plant`) must then come after the desk in turn. If you ever
find the prose and the layer list disagreeing again, the prose wins: it
describes the effect, the list is just an encoding of it.


## Why PNG and not SVG

Asked more than once, so recording the reasoning.

**The source format is not PNG — it is `tools/gen_assets.py`.** The PNGs in
`assets/` are build artifacts, like object files. Regenerate them with
`python3 tools/gen_assets.py`.

SVG would be the wrong tool here, for three concrete reasons:

1. **Bevy cannot load SVG natively** (upstream issue #1139 is still open). The
   community `bevy_svg` crate parses with `usvg` and *tessellates into meshes*
   via Lyon — a vector pipeline, added dependency, and rasterisation step for
   no benefit.
2. **Vector rasterisation anti-aliases.** Hard nearest-neighbour edges ARE the
   pixel-art look; a vector renderer works against it by design.
3. **Pixel art is inherently raster.** Expressing a 320x200 room as vectors
   means either ~64,000 one-pixel `<rect>` elements, or smooth vector shapes —
   which is a different art style, not this one.

**Recolouring is already better than SVG could manage.** The palette is one
dict at the top of the generator: change a hex value, re-run, and all 15
sprites update coherently, with the palette assertion proving nothing drifted.
The SVG equivalent is hand-editing fill attributes across 15 files and hoping
they stay consistent.

For **runtime** recolouring (day/night themes, user-selectable palettes) the
right mechanism is Bevy's per-sprite colour tint or a palette-swap shader —
not a different asset format.
