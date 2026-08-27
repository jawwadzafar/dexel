#!/usr/bin/env python3
"""Generate every sprite listed in docs/art-direction.md v2 (behind-the-shoulder)
as true pixel art.

v2 is a full rewrite of the sprite *content*: the camera moved from a side-on
profile to behind-and-above the hooded developer, so every silhouette, anchor
and layer changed. None of the 29 v0.2 sprites survive (see "What survives
from v0.2" in the art direction doc) but the *conventions* do: the palette is
a hard constraint enforced by an assertion, nothing is ever resized here
(the frontend/engine does the integer upscale with nearest-neighbour
sampling), and a re-run with no source change rewrites the same bytes.

    python3 tools/gen_assets.py

Output is deterministic: every pixel position is either literal or derived
from integer arithmetic, so a re-run with no source change rewrites the same
bytes. The run ends with a self-check (size, palette/ramp purity, opaque-pixel
count, the chair hard-region constraint, the frame-difference rules, the P3
ambient body-lift ladder, the monitor's exact screen rect) and exits non-zero
if any sprite fails it, so a botched edit here cannot quietly ship a blank,
off-palette, or mis-anchored asset. It also deletes any stale file in
app/assets/ that is not part of the v2 manifest, so a rewrite like this one
cannot leave v0.2 corpses behind.
"""

from __future__ import annotations

import sys
from pathlib import Path

from PIL import Image

REPO = Path(__file__).resolve().parent.parent
# Output lives INSIDE the Go module (app/), not at the repository root:
# app/embed.go compiles these PNGs into the server binary with go:embed, and
# go:embed can only reach files inside the module that declares the directive
# (EMBED-1, docs/plan/ROADMAP.md). Moving this path is the whole reason the
# sprites are no longer at <repo>/assets.
ASSETS = REPO / "app" / "assets"

# The palette from docs/art-direction.md, verbatim (18 colours). Sprites
# reference these by name only - a hex literal anywhere below would be a
# palette violation that the self-check at the bottom would then have to
# catch after the fact.
PALETTE: dict[str, tuple[int, int, int]] = {
    "wall_dark": (0x2F, 0x2A, 0x3D),
    "wall_light": (0x3D, 0x35, 0x50),
    "floor": (0x5B, 0x44, 0x33),
    "floor_light": (0x6D, 0x52, 0x40),
    "desk": (0x8B, 0x5E, 0x3C),
    "desk_dark": (0x6B, 0x45, 0x2B),
    "metal": (0x2B, 0x2B, 0x33),
    "screen": (0x7F, 0xD4, 0xC1),
    "screen_dim": (0x4A, 0x8F, 0x83),
    "skin": (0xE8, 0xB4, 0x8C),
    "hair": (0x3A, 0x2A, 0x20),
    "shirt": (0x4A, 0x7F, 0xA8),
    "plant": (0x4E, 0x8B, 0x4F),
    "pot": (0xA4, 0x5C, 0x3A),
    "gold": (0xE8, 0xC4, 0x6A),
    "lamp": (0xFF, 0xD9, 0x8A),
    "cream": (0xF2, 0xE0, 0xC9),
    "shadow": (0x24, 0x1F, 0x2E),
}
RGB_TO_NAME = {rgb: name for name, rgb in PALETTE.items()}

# The one scoped exception to palette purity (art-direction "Recolourable
# parts"): `*_form.png` files paint ONLY this 5-step grayscale ramp, asserted
# separately from the 18-colour check. The runtime tints (indigo/cobalt/...)
# never appear in a PNG - they live in one place, the item catalog's tint
# table, which is out of this generator's scope by design.
RAMP: dict[str, tuple[int, int, int]] = {
    "ramp1": (0x4D, 0x4D, 0x4D),   # deep fold / underside      - 30% of tint
    "ramp2": (0x7A, 0x7A, 0x7A),   # shadow side                - 48%
    "ramp3": (0xA8, 0xA8, 0xA8),   # mid tone                   - 66%
    "ramp4": (0xD4, 0xD4, 0xD4),   # base fabric                - 83%
    "ramp5": (0xFF, 0xFF, 0xFF),   # specular edge, <5% of px   - 100%
}
RGB_TO_RAMP = {rgb: name for name, rgb in RAMP.items()}

# --------------------------------------------------------------------------
# Ordered (Bayer) dithering - the hero-fidelity pass's one new technique.
# --------------------------------------------------------------------------
#
# Pixel art gets more APPARENT shades than it has actual colours by
# interleaving two adjacent palette/ramp entries in a fixed, repeating
# pattern instead of a flat fill - a checkerboard is the simplest case (50%
# of each), a 4x4 Bayer matrix gives 16 usable densities. This is still
# palette/ramp-pure (every emitted pixel is one of the two named colours,
# nothing new is introduced) and fully deterministic (the matrix is a
# constant, not RNG), so it does not touch either hard rule. Used for: the
# room's light falloff and glow pool, the monitor's bezel bevel and screen
# glow bleed, and the developer's fabric shading/ambient-occlusion - i.e.
# every "flat blob" the hero pass was asked to fix.
_BAYER4 = (
    (0, 8, 2, 10),
    (12, 4, 14, 6),
    (3, 11, 1, 9),
    (15, 7, 13, 5),
)


def bayer_mix(x: int, y: int, ratio: float, lo: str, hi: str) -> str:
    """Ordered-dither pick between two colour NAMES: `hi` for approximately
    `ratio` (0..1) of pixels, `lo` for the rest, in a fixed 4x4 repeating
    pattern - a smooth-reading gradient using only the two colours given."""
    if ratio <= 0.0:
        return lo
    if ratio >= 1.0:
        return hi
    return hi if _BAYER4[y % 4][x % 4] < ratio * 16 else lo


_RAMP_STEPS = ["ramp1", "ramp2", "ramp3", "ramp4", "ramp5"]


def ramp_dither(x: int, y: int, v: float) -> str:
    """`v` is a continuous position on the 5-step ramp (0.0=ramp1 (deepest
    fold) .. 4.0=ramp5 (specular)). Returns one ramp step name, ordered-
    dithered against its neighbour by the fractional part of `v`, so a
    shading gradient reads as smoother than 5 discrete flat bands while
    still only ever emitting the 5 ramp colours the palette exemption
    allows."""
    v = max(0.0, min(4.0, v))
    lo = int(v)
    hi = min(lo + 1, 4)
    frac = v - lo
    return bayer_mix(x, y, frac, _RAMP_STEPS[lo], _RAMP_STEPS[hi])


def radial_dither_glow(s: "Sprite", cx: float, cy: float, rx: float, ry: float,
                        x0: int, y0: int, x1: int, y1: int, colour: str,
                        gain: float = 1.0, gamma: float = 1.0) -> None:
    """Paint `colour` at a density that falls off radially from (cx, cy),
    ordered-dithered rather than as a hard-edged rect - this is how the
    room's glow pool becomes "smooth dithering not a hard blob" while
    staying strictly within the 18-colour palette (it paints only the one
    named colour, at varying density, over whatever is already there)."""
    for y in range(y0, y1 + 1):
        for x in range(x0, x1 + 1):
            dx = (x - cx) / rx
            dy = (y - cy) / ry
            d = (dx * dx + dy * dy) ** 0.5
            t = max(0.0, 1.0 - d)
            if t <= 0.0:
                continue
            t = min(1.0, t * gain)
            if gamma != 1.0:
                t = t ** gamma
            thresh = _BAYER4[y % 4][x % 4]
            if thresh < t * 16:
                s.dot(x, y, colour)


# --------------------------------------------------------------------------
# Small-sprite shading helpers - the REST-OF-MANIFEST fidelity pass's own
# additions, built directly on top of the hero pass's `bayer_mix` so every
# desk item/chair/buddy shares its exact dithering mechanics and its ONE
# stated light direction: a soft key light from the upper-left of each
# sprite's own canvas (matching the room/monitor/developer). Every helper
# below therefore puts its highlight on the top/left and its shadow or AO
# on the bottom/right - never the other way round, so nothing in the scene
# reads as lit from a different angle than the hero sprites.
# --------------------------------------------------------------------------

def hgrad(x: int, y: int, x0: int, x1: int, lo: str, hi: str) -> str:
    """Dithered horizontal gradient across [x0, x1]: `hi` (lit) at x0,
    fading to `lo` (shadow) at x1 - the upper-left key light read as "this
    side is closer to the light" for any panel wider than it is tall."""
    if x1 <= x0:
        return hi
    ratio = max(0.0, min(1.0, 1.0 - (x - x0) / (x1 - x0)))
    return bayer_mix(x, y, ratio, lo, hi)


def vgrad(x: int, y: int, y0: int, y1: int, top: str, bottom: str) -> str:
    """Dithered vertical gradient across [y0, y1]: `top` at y0, `bottom` at
    y1 - used for top-lit/bottom-AO reads on shapes taller than wide."""
    if y1 <= y0:
        return top
    ratio = max(0.0, min(1.0, (y - y0) / (y1 - y0)))
    return bayer_mix(x, y, ratio, top, bottom)


def shade_ellipse(s: "Sprite", cx: int, cy: int, rx: int, ry: int, lo: str,
                   hi: str) -> None:
    """A hard-edged pixel ellipse (see `Sprite.ellipse`), but dithered
    lit-left (`hi`, at x=cx-rx) to shadow-right (`lo`, at x=cx+rx) instead
    of a flat fill - used for plant leaves and pot rims so each rounded
    form gets its own light/dark side under the scene's one key light."""
    for y in range(cy - ry, cy + ry + 1):
        for x in range(cx - rx, cx + rx + 1):
            ddx, ddy = x - cx, y - cy
            if (ddx * ddx) / float(rx * rx) + (ddy * ddy) / float(ry * ry) <= 1.05:
                s.dot(x, y, hgrad(x, y, cx - rx, cx + rx, lo, hi))


def bevel_rect(s: "Sprite", x0: int, y0: int, x1: int, y1: int, base: str,
               light: str, shadow: str, ratio: float = 0.55) -> None:
    """Fill a rect flat `base`, then dither a 1px highlight on its top+left
    edge and a 1px shadow on its bottom+right edge - the cheap, reusable
    "material bevel" every hard-edged desk item (keyboard case, mouse
    shell, gas cylinder, thermos body...) gets so it reads as a beveled
    object under the scene's one key light, not a flat rectangle."""
    s.rect(x0, y0, x1, y1, base)
    for x in range(x0, x1 + 1):
        s.dot(x, y0, bayer_mix(x, y0, ratio, base, light))
    for y in range(y0, y1 + 1):
        s.dot(x0, y, bayer_mix(x0, y, ratio, base, light))
    for x in range(x0, x1 + 1):
        s.dot(x, y1, bayer_mix(x, y1, ratio, base, shadow))
    for y in range(y0, y1 + 1):
        s.dot(x1, y, bayer_mix(x1, y, ratio, base, shadow))


def ao_hline(s: "Sprite", y: int, x0: int, x1: int, base_lookup, shadow: str,
             ratio: float = 0.5) -> None:
    """A dithered ambient-occlusion line: half-density `shadow` dithered
    against whatever `base_lookup(x)` already names at each x - used where
    two forms meet (a seat well, a pot rim, a case lip) so the contact
    line reads as a soft occlusion gradient instead of a hard flat seam."""
    for x in range(x0, x1 + 1):
        s.dot(x, y, bayer_mix(x, y, ratio, base_lookup(x), shadow))


def chair_shade_rect(s: "Sprite", cx: float, half: float, x0: int, y0: int,
                      x1: int, y1: int, extra: float = 0.0, base: float = 3.2,
                      sway: float = 0.7) -> None:
    """Fill a rect on the chair's ramp canvas with a dithered cross-section
    gradient: anchored on `base` (ramp4 by default - the tint-headroom
    anchor the CRITICAL tint lesson requires), lit (brighter) toward
    x < cx and shadowed toward x > cx, matching the room's one upper-left
    key light. `extra` is a flat additional darkening (AO at a seam/taper,
    a shadow-side panel) stacked on top of the cross-section gradient."""
    for y in range(y0, y1 + 1):
        for x in range(x0, x1 + 1):
            rel = max(-1.0, min(1.0, (x - cx) / half)) if half else 0.0
            v = base - sway * rel - extra
            s.dot(x, y, ramp_dither(x, y, v))


def chair_shade_ellipse(s: "Sprite", ecx: int, ecy: int, rx: int, ry: int,
                         cx: float, half: float, extra: float = 0.0,
                         base: float = 3.0, sway: float = 0.8) -> None:
    """Same cross-section gradient as `chair_shade_rect`, but only inside a
    hard-edged pixel ellipse (see `Sprite.ellipse`) - for the antigrav
    pod's rounded tiers."""
    for y in range(ecy - ry, ecy + ry + 1):
        for x in range(ecx - rx, ecx + rx + 1):
            ddx, ddy = x - ecx, y - ecy
            if (ddx * ddx) / float(rx * rx) + (ddy * ddy) / float(ry * ry) <= 1.05:
                rel = max(-1.0, min(1.0, (x - cx) / half)) if half else 0.0
                v = base - sway * rel - extra
                s.dot(x, y, ramp_dither(x, y, v))


def chair_rim_light(s: "Sprite", rim: int = 2, boost: float = 1.6) -> None:
    """THE tint-crush fix, ported from the developer sprite: a dithered
    boost toward ramp5 (undarkened at ANY tint) on the leftmost `rim`
    opaque columns of every row - the true lit silhouette edge. Every
    chair form gets this, called once at the end of the build, so the
    chair still separates from the wall at its DARKEST purchasable tint
    (`slate`, near-black) exactly the way the hero pass's rim light keeps
    the developer's silhouette visible at that same tint. Must run AFTER
    every other fill in the builder (including AO patches), since it is
    the outermost, highest-priority pass."""
    mask = s.mask()
    for y in range(s.h):
        xs = [x for x in range(s.w) if mask[y][x]]
        if not xs:
            continue
        x0 = min(xs)
        for i in range(rim):
            x = x0 + i
            if x >= s.w or not mask[y][x]:
                continue
            cur = s.img.getpixel((x, y))[:3]
            idx = _RAMP_STEPS.index(RGB_TO_RAMP[cur]) if cur in RGB_TO_RAMP else 3
            v = min(4.0, idx + boost * (1 - i / rim))
            s.dot(x, y, ramp_dither(x, y, v))


def chair_back_top_rim(s: "Sprite", x0: int, x1: int, y: int) -> None:
    """A bright top edge along a chair back / wing top. The top row rides high
    on the ramp (near ramp5, undarkened by any tint) and the row below eases
    down, so the seatback's TOP reads as a crisp lit line ABOVE the head
    (BUG-5) and stays legible against the desk/wall at the darkest tint."""
    for x in range(x0, x1 + 1):
        s.dot(x, y, ramp_dither(x, y, 4.0))
        if y + 1 < s.h:
            s.dot(x, y + 1, ramp_dither(x, y + 1, 3.4))


# The 55-file v2 manifest (docs/art-direction.md, "Sprite manifest v2"), used
# both to author and to verify. Keep in sync with the doc; the self-check
# compares against this and the final app/assets/ directory listing must equal
# exactly these files plus their derived thumbnails - nothing else survives.
SPEC: list[tuple[str, int, int]] = [
    # Fixed scenery (3)
    ("room_back.png", 320, 200),
    ("desk_back.png", 320, 58),
    ("monitor.png", 132, 64),
    # SCENE-REACTIONS (docs/plan/ROADMAP.md) - the monitor's click react, on
    # the same 132x64 canvas. Only the HEAD (bezel + glass + chin) slides 1px
    # horizontally; the neck, foot and contact shadow stay planted, so the
    # wobble reads as a knock on a monitor that is still standing on the desk.
    # The shake is horizontal-ONLY and the glass ROWS never move - see
    # assert_monitor_shake for the composition contract that forces this.
    ("monitor_shake_a.png", 132, 64),
    ("monitor_shake_b.png", 132, 64),
    # STORE-2.0 "monitor" colour slot (docs/plan/ROADMAP.md, catalog.go). The
    # runtime tint system is gone from the wire/save/economy, but the frontend
    # still recolours the formerly-tintable parts at RENDER time via CSS tint
    # keyed on a colour token in the item id - and the monitor slot recolours
    # the SAME way. So instead of one PNG per colour we ship ONE ramp-pure
    # bezel FORM, on monitor.png's own 132x64 canvas, that the frontend tints
    # per equipped monitor colour (slate/cobalt/forest/neon) and overlays on
    # the fixed monitor.png so ONLY the bezel recolours - the screen rect (and
    # its terminal geometry) is left transparent and untouched.
    ("monitor_frame.png", 132, 64),
    # SCENE-REACTIONS x STORE-2.0: the tinted bezel's OWN shake frames, so the
    # frame overlay knocks IN SYNC with monitor_shake_a/b underneath it instead
    # of the base sliding out from under a static tinted bezel. Same head-only
    # +-1px horizontal displacement as monitor_shake (build_monitor_frame_shake
    # reuses MONITOR_HEAD_ROWS / MONITOR_SHAKE_DX), so base + bezel move as one
    # rigid monitor - see assert_monitor_frame_shake for the composition guard.
    ("monitor_frame_shake_a.png", 132, 64),
    ("monitor_frame_shake_b.png", 132, 64),
    # Developer (14), all 192x76, identical canvas and anchor. PERSPECTIVE
    # REWRITE: the canvas is wide (the `mouse` pose reaches room x252) and
    # short (the figure has to read inside room y99..160 - keyboard above,
    # HUD panels below), and the figure stays CENTRED in it so the derived
    # 40x40 hoodie thumbnails are a centred crop of the hood. Five frames:
    # idle / type_a / type_b (the two typing frames alternate which hand
    # presses) / mouse (right hand on the mouse) / sleep.
    ("dev_form_idle.png", 192, 76),
    ("dev_form_type_a.png", 192, 76),
    ("dev_form_type_b.png", 192, 76),
    ("dev_form_mouse.png", 192, 76),
    ("dev_form_sleep.png", 192, 76),
    ("dev_base_idle.png", 192, 76),
    ("dev_base_type_a.png", 192, 76),
    ("dev_base_type_b.png", 192, 76),
    ("dev_base_mouse.png", 192, 76),
    ("dev_base_sleep.png", 192, 76),
    # P3 "Character life" (docs/plan/PRODUCT-EVOLUTION.md Phase P3): four
    # ADDITIVE frames on the SAME approved pose - two ambient (breath,
    # stretch) and a two-frame celebration pair (cheer_a/cheer_b). No
    # proportion, silhouette or shading rule changes; see DEV_AMBIENT_FRAMES.
    ("dev_form_breath.png", 192, 76),
    ("dev_form_stretch.png", 192, 76),
    ("dev_form_cheer_a.png", 192, 76),
    ("dev_form_cheer_b.png", 192, 76),
    ("dev_base_breath.png", 192, 76),
    ("dev_base_stretch.png", 192, 76),
    ("dev_base_cheer_a.png", 192, 76),
    ("dev_base_cheer_b.png", 192, 76),
    # SCENE-REACTIONS - the "hey!" react (DEV_REACT_FRAMES): two ADDITIVE
    # frames on the same approved pose. The upper body flinches UP and leans
    # 1px sideways (rigidly, hood and back together, so the hoodie overlays
    # still follow by a single offset) while the RIGHT hand comes off the keys
    # and swings out in a wave-ish acknowledge. The left hand never leaves its
    # key: the beat has to read as an indignant startled turn, not as injury.
    ("dev_form_react_a.png", 192, 76),
    ("dev_form_react_b.png", 192, 76),
    ("dev_base_react_a.png", 192, 76),
    ("dev_base_react_b.png", 192, 76),
    ("hoodie_classic.png", 192, 76),
    ("hoodie_zip.png", 192, 76),
    ("hoodie_tech.png", 192, 76),
    ("hoodie_cloak.png", 192, 76),
    # Chair (8), bottom-centre anchored at room row 200 / x=160. BEHIND-VIEW
    # COMPOSITING: the chair draws ON TOP of the developer, so every canvas
    # starts at (or below) room row 149 - one row above the CHAIR_TOP_ROOM_Y
    # 150 shoulder line, the extra row being the detail layer's silhouette
    # halo - and is shoulder-width-MINUS, never a full-scene slab. SLIMMING
    # PASS: the owner read the figure as fat and the furniture as too wide, so
    # the shoulders came down 92 -> 76px and the chairs came down FURTHER
    # (canvases 116..148 -> 68..88 wide, backrest half-widths 31..39 ->
    # 24..28) until the whole furniture footprint fits INSIDE the figure's own
    # widest span instead of straddling it.
    ("chair_basic_form.png", 80, 51),
    ("chair_basic_detail.png", 80, 51),
    ("chair_racer_form.png", 84, 51),
    ("chair_racer_detail.png", 84, 51),
    ("chair_exec_form.png", 88, 51),
    ("chair_exec_detail.png", 88, 51),
    ("chair_antigrav_form.png", 68, 51),
    ("chair_antigrav_detail.png", 68, 51),
    # Keyboard (4), 96x24 at (112, 90)
    ("kb_membrane.png", 96, 24),
    ("kb_mech.png", 96, 24),
    ("kb_split.png", 96, 24),
    ("kb_neon.png", 96, 24),
    # Mouse (4), 44x24 at (224, 90)
    ("mouse_stock.png", 44, 24),
    ("mouse_gaming.png", 44, 24),
    ("mouse_trackball.png", 44, 24),
    ("mouse_vertical.png", 44, 24),
    # Beverage (4), 20x24 at (56, 90)
    ("bev_mug.png", 20, 24),
    ("bev_thermos.png", 20, 24),
    ("bev_teacup.png", 20, 24),
    ("bev_energy.png", 20, 24),
    # SCENE-REACTIONS - beverage reacts, PER STYLE rather than one shared
    # overlay sprite: the four vessels' rims sit on four different rows (mug
    # 10, thermos 3, teacup 11, can 5), so a single shared burst would either
    # float five rows above the mug or be painted straight through the
    # thermos's lid. Each frame is its own vessel hopped 1px/2px with the
    # contact shadow held on the desk (hop_with_planted_shadow) plus a burst
    # grown from that vessel's own rim - steam for the hot drinks, a fizz
    # sparkle for the can, which is also what keeps the set style-consistent.
    ("bev_mug_react_a.png", 20, 24),
    ("bev_mug_react_b.png", 20, 24),
    ("bev_thermos_react_a.png", 20, 24),
    ("bev_thermos_react_b.png", 20, 24),
    ("bev_teacup_react_a.png", 20, 24),
    ("bev_teacup_react_b.png", 20, 24),
    ("bev_energy_react_a.png", 20, 24),
    ("bev_energy_react_b.png", 20, 24),
    # Plant (3 + empty slot), 40x44 at (244, 32), base row 76
    ("plant_succulent.png", 40, 44),
    ("plant_monstera.png", 40, 44),
    ("plant_bonsai.png", 40, 44),
    # Wall (3 + empty slot), 40x44 at (24, 16)
    ("wall_poster.png", 40, 44),
    ("wall_shelf.png", 40, 44),
    ("wall_neon.png", 40, 44),
    # Buddy (4 + empty slot), 28x30 at (288, 46), base row 76
    ("buddy_duck.png", 28, 30),
    ("buddy_bot_a.png", 28, 30),
    ("buddy_bot_b.png", 28, 30),
    ("buddy_cat.png", 28, 30),
    # SCENE-REACTIONS - one 2-frame react per REAL buddy (buddy_none has no
    # sprite, and buddy_bot_b is the blink frame of buddy_bot, not a fifth
    # companion): the duck and the bot HOP with their contact shadow held on
    # the desk, the bot also flashes its eyes and beeps its antenna, and the
    # cat - which is all head and tail - twitches its ears and flicks its
    # tail instead, then lifts a pixel on the second frame.
    ("buddy_duck_react_a.png", 28, 30),
    ("buddy_duck_react_b.png", 28, 30),
    ("buddy_bot_react_a.png", 28, 30),
    ("buddy_bot_react_b.png", 28, 30),
    ("buddy_cat_react_a.png", 28, 30),
    ("buddy_cat_react_b.png", 28, 30),
]
SPEC_NAMES = {name for name, _, _ in SPEC}
assert len(SPEC) == 78, f"manifest drifted: {len(SPEC)} entries, expected 78"

# `*_form.png` files are palette-purity EXEMPT and ramp-purity CHECKED instead
# (art-direction "Palette-purity exception (the only one)"). Covers both the
# chair naming (`chair_<style>_form.png`) and the developer's
# (`dev_form_<frame>.png`).
FORM_FILES = {name for name in SPEC_NAMES
              if name.endswith("_form.png") or name.startswith("dev_form_")}
# The monitor's tintable bezel is named `monitor_frame.png` (it overlays
# monitor.png, so it reads as the monitor's FRAME, not a garment "form") and
# so matches neither naming pattern above - add it explicitly so it is
# ramp-audited like every other tintable grayscale layer, per the art
# direction's "Palette-purity exception".
FORM_FILES.add("monitor_frame.png")
# The frame's shake frames are the same tintable grayscale bezel, displaced -
# ramp-audited like the rest layer they are copied from.
FORM_FILES.add("monitor_frame_shake_a.png")
FORM_FILES.add("monitor_frame_shake_b.png")

# The two frame-difference rules this generator must prove, not just assert
# by convention (art-direction "Character rules" / buddy manifest note).
# The typing animation now lives in the HANDS (dev_base carries the skin),
# and the mouse pose is a genuinely different arm, so both pairs are checked
# on the layer that actually differs.
TYPING_PAIR = ("dev_base_type_a.png", "dev_base_type_b.png")
TYPING_FABRIC_PAIR = ("dev_form_type_a.png", "dev_form_type_b.png")
MOUSE_PAIR = ("dev_base_type_a.png", "dev_base_mouse.png")
BLINK_PAIR = ("buddy_bot_a.png", "buddy_bot_b.png")

# The four hoodie style overlays, in manifest order - the files that are shared
# by every dev frame and therefore have their own alignment rules.
HOODIE_FILES = tuple(name for name, _w, _h in SPEC
                     if name.startswith("hoodie_"))
# P3: the ambient ladder (idle -> breath -> stretch is a 0/-1/-2px body lift,
# so consecutive rungs MUST differ) and the celebration bounce. Checked on
# dev_form for the ambient pair (the whole garment is what moves) and on
# dev_base for the cheer pair (the hands are what fling out).
AMBIENT_PAIRS = (("dev_form_idle.png", "dev_form_breath.png"),
                 ("dev_form_breath.png", "dev_form_stretch.png"),
                 ("dev_base_idle.png", "dev_base_breath.png"))
CHEER_PAIR = ("dev_base_cheer_a.png", "dev_base_cheer_b.png")
# SCENE-REACTIONS. Every react is checked TWICE over: against its own second
# frame (it must be a real 2-frame beat, not one sprite shown twice) and
# against the REST frame it interrupts (it must be a real departure from the
# sprite already on screen, or the click does nothing visible). Both halves
# are frame diffs, so a future edit that neutralises one of them fails loudly.
REACT_PAIRS = (
    ("dev_form_idle.png", "dev_form_react_a.png"),
    ("dev_form_react_a.png", "dev_form_react_b.png"),
    ("dev_base_idle.png", "dev_base_react_a.png"),
    ("dev_base_react_a.png", "dev_base_react_b.png"),
    ("monitor.png", "monitor_shake_a.png"),
    ("monitor_shake_a.png", "monitor_shake_b.png"),
    ("monitor_frame.png", "monitor_frame_shake_a.png"),
    ("monitor_frame_shake_a.png", "monitor_frame_shake_b.png"),
    ("bev_mug.png", "bev_mug_react_a.png"),
    ("bev_mug_react_a.png", "bev_mug_react_b.png"),
    ("bev_thermos.png", "bev_thermos_react_a.png"),
    ("bev_thermos_react_a.png", "bev_thermos_react_b.png"),
    ("bev_teacup.png", "bev_teacup_react_a.png"),
    ("bev_teacup_react_a.png", "bev_teacup_react_b.png"),
    ("bev_energy.png", "bev_energy_react_a.png"),
    ("bev_energy_react_a.png", "bev_energy_react_b.png"),
    ("buddy_duck.png", "buddy_duck_react_a.png"),
    ("buddy_duck_react_a.png", "buddy_duck_react_b.png"),
    ("buddy_bot_a.png", "buddy_bot_react_a.png"),
    ("buddy_bot_react_a.png", "buddy_bot_react_b.png"),
    ("buddy_cat.png", "buddy_cat_react_a.png"),
    ("buddy_cat_react_a.png", "buddy_cat_react_b.png"),
)
# (react frame, rest frame, contact-shadow row) for every prop react built on
# the hop mechanism - checked by assert_hop_shadow_planted.
HOP_REACTS = tuple(
    (f"bev_{style}_react_{tag}.png", f"bev_{style}.png", 23)
    for style in ("mug", "thermos", "teacup", "energy") for tag in ("a", "b")
) + tuple(
    (f"buddy_{b}_react_{tag}.png", rest, row)
    for b, rest, row in (("duck", "buddy_duck.png", 29),
                         ("bot", "buddy_bot_a.png", 29),
                         ("cat", "buddy_cat.png", 17))
    for tag in ("a", "b")
)


# --------------------------------------------------------------------------
# Sprite: an RGBA canvas that can only be painted in palette (or ramp) colours
# --------------------------------------------------------------------------

class Sprite:
    """An RGBA canvas that can only be painted in palette colours.

    Every drawing call takes a colour *name*, so an off-palette pixel is
    impossible by construction rather than something to audit afterwards.
    Coordinates are inclusive and out-of-range writes are clipped: the poses
    below are hand-authored pixel lists, and a 1px overhang while tuning an
    arm should not abort the whole build.

    `palette` defaults to the 18-colour PALETTE; `*_form.png` builders pass
    `palette=RAMP` so the same drawing calls can only ever emit one of the
    5 grayscale steps - the ramp exemption is enforced at construction time,
    not just at audit time.
    """

    def __init__(self, w: int, h: int, bg: str | None = None,
                 palette: dict[str, tuple[int, int, int]] | None = None) -> None:
        self.w = w
        self.h = h
        self.palette = palette if palette is not None else PALETTE
        self.img = Image.new("RGBA", (w, h), (0, 0, 0, 0))
        self.buf = self.img.load()
        if bg is not None:
            self.rect(0, 0, w - 1, h - 1, bg)

    def dot(self, x: int, y: int, name: str) -> None:
        if 0 <= x < self.w and 0 <= y < self.h:
            self.buf[x, y] = self.palette[name] + (255,)

    def clear(self, x: int, y: int) -> None:
        """Punch a hole back to full transparency - used to carve the
        transparent gap between the developer's two arms/hands where the
        keyboard must show through."""
        if 0 <= x < self.w and 0 <= y < self.h:
            self.buf[x, y] = (0, 0, 0, 0)

    def dots(self, coords, name: str) -> None:
        for x, y in coords:
            self.dot(x, y, name)

    def rect(self, x0: int, y0: int, x1: int, y1: int, name: str) -> None:
        for y in range(y0, y1 + 1):
            for x in range(x0, x1 + 1):
                self.dot(x, y, name)

    def hline(self, y: int, x0: int, x1: int, name: str) -> None:
        self.rect(x0, y, x1, y, name)

    def vline(self, x: int, y0: int, y1: int, name: str) -> None:
        self.rect(x, y0, x, y1, name)

    def outline(self, x0: int, y0: int, x1: int, y1: int, name: str) -> None:
        """A 1px border of identical thickness on all four sides."""
        self.hline(y0, x0, x1, name)
        self.hline(y1, x0, x1, name)
        self.vline(x0, y0, y1, name)
        self.vline(x1, y0, y1, name)

    def line(self, x0: int, y0: int, x1: int, y1: int, name: str) -> None:
        """Bresenham, hard-edged - no PIL `line()`, which anti-aliases."""
        dx = x1 - x0
        dy = y1 - y0
        steps = max(abs(dx), abs(dy))
        if steps == 0:
            self.dot(x0, y0, name)
            return
        for i in range(steps + 1):
            x = x0 + round(dx * i / steps)
            y = y0 + round(dy * i / steps)
            self.dot(x, y, name)

    def ellipse(self, cx: int, cy: int, rx: int, ry: int, name: str) -> None:
        """A hard-edged pixel oval - PIL's ellipse would anti-alias the rim.

        The 1.05 fudge rounds the shape outward slightly so small radii come
        out as blobs rather than diamonds.
        """
        for y in range(cy - ry, cy + ry + 1):
            for x in range(cx - rx, cx + rx + 1):
                ddx, ddy = x - cx, y - cy
                if (ddx * ddx) / float(rx * rx) + (ddy * ddy) / float(ry * ry) <= 1.05:
                    self.dot(x, y, name)

    def mask(self) -> list[list[bool]]:
        """Opaque-pixel footprint as a [y][x] bool grid, for outline strokes
        derived from the actual painted silhouette rather than hand-copied
        coordinates (which drift the moment the silhouette they describe
        changes)."""
        px = self.img.load()
        return [[px[x, y][3] != 0 for x in range(self.w)] for y in range(self.h)]

    def save(self, filename: str) -> Path:
        path = ASSETS / filename
        self.img.save(path)
        return path


def outline_from_mask(s: Sprite, mask: list[list[bool]], name: str = "shadow") -> None:
    """Paint a 1px halo of `name` on every transparent pixel adjacent to a
    `mask`-true pixel. This is how every silhouette outline in this file is
    produced: derived from the shape that was actually painted, so the
    outline cannot go stale relative to a silhouette that later changes
    shape (the developer's per-frame arm offsets, a chair redesign, ...).
    """
    h = len(mask)
    w = len(mask[0]) if h else 0
    for y in range(h):
        for x in range(w):
            if mask[y][x]:
                continue
            near = False
            for dx, dy in ((1, 0), (-1, 0), (0, 1), (0, -1)):
                nx, ny = x + dx, y + dy
                if 0 <= nx < w and 0 <= ny < h and mask[ny][nx]:
                    near = True
                    break
            if near:
                s.dot(x, y, name)


def union_mask(*masks: list[list[bool]]) -> list[list[bool]]:
    h = len(masks[0])
    w = len(masks[0][0])
    return [[any(m[y][x] for m in masks) for x in range(w)] for y in range(h)]


def shifted_copy(src: "Sprite", dx: int, dy: int,
                 rows: set[int] | None = None) -> "Sprite":
    """A rigid, pixel-for-pixel copy of `src` displaced by (dx, dy).

    SCENE-REACTIONS uses this for every react frame that is "the same object,
    moved": re-running a builder with shifted coordinates would re-roll its
    ordered-dither pattern against the new y (a subtly RE-SHADED object, which
    is exactly what the frame-diff rules exist to forbid), whereas copying the
    already-painted pixels moves the object and its shading together.

    `rows` (source rows) limits the displacement to part of the sprite: the
    rest is copied in place. That is how the monitor's head shakes while its
    foot stays planted, and how a buddy hops while its contact shadow stays on
    the desk. Colours round-trip through the sprite's own palette by NAME, so
    a copy can no more introduce an off-palette pixel than a fresh paint can.
    """
    out = Sprite(src.w, src.h, palette=src.palette)
    by_rgb = {rgb + (255,): name for name, rgb in src.palette.items()}
    px = src.img.load()
    for y in range(src.h):
        move = rows is None or y in rows
        for x in range(src.w):
            c = px[x, y]
            if c[3] == 0:
                continue
            name = by_rgb[c]                    # KeyError = off-palette source
            if move:
                out.dot(x + dx, y + dy, name)
            else:
                out.dot(x, y, name)
    return out


def hop_with_planted_shadow(base: "Sprite", dy: int, shadow_row: int,
                            shadow_x0: int, shadow_x1: int,
                            inset: int) -> "Sprite":
    """The shared prop-hop mechanism (beverage + buddy reacts): lift every row
    ABOVE `shadow_row` by `dy`, leave the 1px contact shadow exactly where it
    was, and pull it in `inset` px on each side.

    Holding the shadow row is what makes a 1-2px hop read as leaving the desk
    rather than as the whole sprite sliding: the contact shadow is the only
    thing in these sprites that belongs to the DESK instead of to the object,
    and a tighter shadow is what a thing slightly further from a surface
    casts. It also keeps the "anything resting on a surface ends in a 1px
    shadow row wider than itself" rule true in every react frame - checked by
    assert_hop_shadow_planted.
    """
    out = shifted_copy(base, 0, dy, rows=set(range(0, shadow_row)))
    for x in range(out.w):
        out.clear(x, shadow_row)
    out.hline(shadow_row, shadow_x0 + inset, shadow_x1 - inset, "shadow")
    return out


# --------------------------------------------------------------------------
# Fixed scenery (3): room_back, desk_back, monitor
# --------------------------------------------------------------------------
#
# Coordinate system: room pixels, origin top-left, x right, y down, per
# art-direction "Geometry". room_back and desk_back share the wall/floor line
# at room row 132; desk_back is authored in its OWN local coordinates
# (0..58), which is room row 74..132 once placed.

def build_room_back() -> Sprite:
    """320x200: wall rows 0..132, floor rows 132..200.

    HERO-FIDELITY REWRITE. The v2.0 version painted the glow as two flat,
    hard-edged rects, which is exactly the "flat blob" the fidelity pass
    was asked to fix. v0.2's dithering attempt was rejected for being
    literal RANDOM noise on a wall - a genuine artifact. This is ordered
    (Bayer 4x4) dithering instead: a fixed, repeating interleave of two
    adjacent palette colours, which is the actual pixel-art technique for
    "more shades than colours" and reads as a smooth gradient, not static.
    `radial_dither_glow` (module level) implements it once and is reused for
    every soft-edged glow in this file.

    Stated light source (referenced again in the developer sprite below,
    for one consistent scene): the monitor's glow is the room's only light,
    centred behind the monitor slot (room x 94..226, centre x=160, roughly
    y=45). Everything in the room brightens toward that point and dims with
    distance from it - the wall's overall gradient, the floor patch, and
    (in the developer) the rim light wrapping the silhouette.
    """
    s = Sprite(320, 200, bg="wall_dark")
    # Vertical centre sits mid-monitor (monitor spans room y 20..84), NOT
    # above it - see MONITOR_TOP below for why that matters.
    GLOW_CX, GLOW_CY = 160.0, 55.0
    MONITOR_TOP = 20   # room y where the monitor sprite's own opaque bezel
                        # begins; a hero-pass regression let the glow's
                        # dithered rim extend to y=10, INTO the visible
                        # (row 12+) wall band above the monitor, reading as
                        # a floating cloud of dither dots on the wall (a
                        # real render, not the neutral mockup, caught this -
                        # the exact "stray static particles" defect prior
                        # review passes rejected). Fix: never paint the glow
                        # above the monitor's own top edge at all - the
                        # bounding boxes below start at MONITOR_TOP, and the
                        # radii/gain are tuned so the glow is already at or
                        # near zero by that row (the clip is a hard
                        # guarantee either way).

    # Ceiling shadow band - the top few rows are always hidden behind the
    # title bar (room rows 0..12), but painting it means a shorter title bar
    # in a future revision cannot expose a flat, undecorated wall edge.
    s.rect(0, 0, 319, 9, "shadow")

    # Subtle wall texture: a few faint panel seams away from the glow, so
    # the wall reads as a surface, not a colour field. Kept sparse and off
    # to the sides so nothing competes with the monitor as the focal point.
    for x in (18, 46, 274, 302):
        for y in range(12, 131, 3):          # broken, not a solid line
            s.dot(x, y, "shadow")

    # Glow pool: two nested radii, `wall_light` broad then `lamp` tight at
    # the core. `gain` > 1 on both is deliberate and load-bearing - it is
    # what keeps this a soft-edged BLOB (solid core, a narrow dithered rim,
    # then untouched flat `wall_dark` beyond it) instead of a low-density
    # dither spread thinly across the entire wall, which reads as static,
    # not light (the exact failure the v0.2 "dithered wall" attempt hit).
    # Most of this blob sits BEHIND the monitor sprite (which is opaque
    # over its own 132x64 footprint) and is only actually seen beside it
    # (x<94 or x>226) and, for the smaller/tighter lamp core, not even
    # there - both radii are sized so the visible side-glow fades out
    # before reaching MONITOR_TOP.
    radial_dither_glow(s, GLOW_CX, GLOW_CY, 85, 45, 60, MONITOR_TOP, 260, 131,
                        "wall_light", gain=2.0)
    radial_dither_glow(s, GLOW_CX, GLOW_CY, 40, 22, 105, MONITOR_TOP, 215, 95,
                        "lamp", gain=2.4)

    # Floor.
    s.rect(0, 132, 319, 199, "floor")
    # The desk slab's cast shadow onto the floor - 2 rows, full width, this
    # is the "shadow band" the derivation rules call out by name. Kept flat
    # and exact: it is a functional contact shadow, not a decorative area.
    s.rect(0, 132, 319, 133, "shadow")
    # A skirting kick-highlight: the one row where the (occluded, but still
    # painted per the derivation rules) baseboard would catch light bouncing
    # off the floor, giving the wall/floor seam a "trim", not just a shadow.
    s.hline(134, 0, 319, "floor_light")

    # Warm glow patch on the floor, tying the wall light down to the floor
    # instead of letting it stop dead at the desk edge - dithered, so it
    # fades rather than ending in a hard rectangle.
    radial_dither_glow(s, GLOW_CX, 132.0, 60, 20, 60, 135, 260, 152,
                        "floor_light", gain=2.4)

    # Floorboards: 3 seams, each row band TALLER than the last toward the
    # bottom of the frame (nearer the viewer) - a cheap but effective
    # perspective cue for boards receding toward the desk. Each seam is a
    # groove (`shadow`) then a near-edge highlight (`floor_light`), and each
    # board gets a couple of short dithered grain streaks so the planks read
    # as individual boards, not one texture-less brown field.
    seams = (144, 161, 181)
    bands = [(134, seams[0])] + [(seams[i], seams[i + 1]) for i in range(len(seams) - 1)] + [(seams[-1], 200)]
    for y in seams:
        s.hline(y, 0, 319, "shadow")
        s.hline(y + 1, 0, 319, "floor_light")
    for i, (by0, by1) in enumerate(bands):
        # Plank seams (vertical), staggered per board row so they don't
        # line up into one grid - a deterministic stagger, not randomness.
        step = 34 + i * 6
        offset = (i * 17) % step
        for x in range(offset, 320, step):
            s.vline(x, by0 + 2, by1 - 1, "shadow")
        # Grain: a short dithered streak near the middle of each plank cell.
        gy = (by0 + by1) // 2
        for x in range(offset + 6, 320, step):
            for gx in range(x, min(x + 10, 319)):
                s.dot(gx, gy, bayer_mix(gx, gy, 0.35, "floor", "floor_light"))
    return s


def build_desk_back() -> Sprite:
    """320x58 (room rows 74..132): the desk slab seen from above-behind.

    HERO-FIDELITY MATCH (rest-of-manifest pass). The desk is the stage
    everything else sits on, so it gets the same three moves as the floor
    in `build_room_back`: dithered wood grain instead of flat rows, a
    dithered sheen picking up the monitor's glow (already present, now
    blended at its edges instead of a hard rect), and a genuinely LIT
    front lip - a highlight on the lip's own top edge (it catches the
    room's ambient upper-left key light square-on, being the nearest
    horizontal surface to the viewer) fading into a dithered AO gradient
    immediately above it (the desk surface receding away from that edge)
    and the existing 1px `shadow` hairline below it, which abuts
    room_back's own shadow band to form one unbroken cast-shadow line at
    the wall/floor seam.
    """
    s = Sprite(320, 58, bg="desk")
    GLOW_CX = 160

    # Sheen band: dithered fade in from both sides instead of a hard rect,
    # so the glow bleeding onto the desk reads the same "soft pool" as the
    # wall glow above it, not a pasted rectangle.
    for y in range(0, 8):
        for x in range(90, 231):
            d = abs(x - GLOW_CX) / 70.0
            t = max(0.0, 1.0 - d) * max(0.0, 1.0 - y / 9.0)
            if t > 0.0:
                s.dot(x, y, bayer_mix(x, y, min(1.0, t), "desk", "wall_light"))

    # Wood grain: 4 seams (groove + near-edge highlight, like the floor's
    # plank seams) plus short dithered grain streaks per board so the
    # surface reads as individual boards, not a flat brown field.
    seams = (8, 20, 32, 44)
    bands = [(0, seams[0])] + [(seams[i], seams[i + 1]) for i in range(len(seams) - 1)] + [(seams[-1], 55)]
    for y in seams:
        s.hline(y, 0, 319, "desk_dark")
        for x in range(0, 320):
            s.dot(x, y + 1, bayer_mix(x, y + 1, 0.3, "desk", "desk_dark"))
    for i, (by0, by1) in enumerate(bands):
        step = 40 + i * 5
        offset = (i * 13) % step
        gy = (by0 + by1) // 2
        for x in range(offset, 320, step):
            for gx in range(x, min(x + 14, 319)):
                s.dot(gx, gy, bayer_mix(gx, gy, 0.3, "desk", "desk_dark"))

    # Ambient occlusion rising into the near lip: the surface dims as it
    # approaches its own front edge, a few rows of dithered fade.
    for y in range(50, 55):
        for x in range(0, 320):
            s.dot(x, y, bayer_mix(x, y, 0.15 + (y - 50) * 0.12, "desk", "desk_dark"))

    # Near lip: a lit top edge (row 55, the corner catching the key light
    # square-on) dithering down into flat desk_dark (row 56), then the 1px
    # cast-shadow hairline (row 57) unchanged from the previous version.
    for x in range(0, 320):
        s.dot(x, 55, bayer_mix(x, 55, 0.6, "desk_dark", "floor_light"))
    s.hline(56, 0, 319, "desk_dark")
    s.hline(57, 0, 319, "shadow")          # 1px shadow under the lip
    return s


# The monitor's exact inner screen rect, LOCAL to monitor.png. This is the
# single most load-bearing number in the whole manifest: the frontend draws
# 11 lines of live terminal text into this exact rect (in UI px, x2), and any
# drift here lands text on the bezel. Placed at room (94, 20) this rect is
# room (98, 24) 124x44, matching the placement table exactly.
MONITOR_SCREEN_RECT = (4, 4, 127, 47)   # x0, y0, x1, y1 inclusive -> 124x44


def build_monitor() -> Sprite:
    """132x64: `metal` bezel (4px left/right/top) with a real beveled edge,
    the exact inner screen rect filled flat `shadow` (no text - the
    frontend draws that; UNCHANGED bounds and fill, see below), an 8px chin
    with one `screen` power LED, and a neck+foot with dimension.

    HERO-FIDELITY REWRITE. Same light source as the rest of the scene (see
    `build_room_back`): a soft key light from the upper-left, so the bezel
    gets a highlight on its outer top/left edge and a shadow on its outer
    right edge - a real bevel, not a flat metal frame. The bezel's INNER
    edge (the 1px ring touching the screen well) tells a second, opposite
    story: the screen itself is the room's brightest object, so its glow
    bleeds onto the inner-right bezel lip while the inner-top/left lip
    falls into ambient occlusion where the bezel recesses to meet the
    glass. Both bevels are ordered-dithered (see `bayer_mix`), not flat.
    """
    s = Sprite(132, 64)
    x0, y0, x1, y1 = MONITOR_SCREEN_RECT
    # Bezel block first (top + both sides down to the inner rect's bottom),
    # then the inner rect punches the exact hole the UI text lands in. This
    # order - and ONLY drawing the inner rect as this one rect call, with no
    # further edit to it below - is what keeps the rect exact and
    # reviewable as a single line of code. Everything after this point only
    # ever touches bezel pixels OUTSIDE (x0,y0)-(x1,y1).
    s.rect(0, 0, 131, y1, "metal")
    s.rect(x0, y0, x1, y1, "shadow")

    # Outer bevel, top band (y=0..2, x=4..127): row0 a crisp raised
    # highlight edge, rows 1-2 a dithered fade back toward flat metal.
    s.hline(0, 0, 131, "wall_light")
    for y, ratio in ((1, 0.55), (2, 0.25)):
        for x in range(x0, x1 + 1):
            s.dot(x, y, bayer_mix(x, y, ratio, "metal", "wall_light"))
    # Inner AO ring, top (y=3, the row that actually touches the screen
    # well): the bezel recessing down to the glass falls into shadow.
    for x in range(x0, x1 + 1):
        s.dot(x, 3, bayer_mix(x, 3, 0.55, "metal", "shadow"))

    # Outer bevel, left column (x=0..2): the same upper-left highlight,
    # fading with depth into the bezel and with distance from the top.
    for x, ratio in ((0, 0.7), (1, 0.45), (2, 0.2)):
        for y in range(0, y1 + 1):
            s.dot(x, y, bayer_mix(x, y, max(0.05, ratio * (1 - y / 80)), "metal", "wall_light"))
    # Inner AO ring, left (x=3): same occlusion logic as the top ring.
    for y in range(y0, y1 + 1):
        s.dot(3, y, bayer_mix(3, y, 0.5, "metal", "shadow"))

    # Outer bevel, right column (x=129..131): the shadow side, away from
    # the key light - the bevel's far edge falls dark instead of matching
    # the highlighted left.
    for x, ratio in ((131, 0.7), (130, 0.4), (129, 0.15)):
        for y in range(0, y1 + 1):
            s.dot(x, y, bayer_mix(x, y, ratio, "metal", "shadow"))
    # Inner glow-bleed ring, right (x=128): the screen is the brightest
    # thing in the room, so its light spills onto the inner-right lip.
    for y in range(y0, y1 + 1):
        s.dot(128, y, bayer_mix(128, y, 0.4, "metal", "screen_dim"))

    # Chin: 8px, full width, with the power LED. A thin highlight ridge
    # where the bezel steps down to the chin, and a shadow before the neck.
    s.rect(0, y1 + 1, 131, y1 + 8, "metal")
    for x in range(0, 132):
        s.dot(x, y1 + 1, bayer_mix(x, y1 + 1, 0.35, "metal", "wall_light"))
        s.dot(x, y1 + 8, bayer_mix(x, y1 + 8, 0.4, "metal", "shadow"))
    s.rect(64, y1 + 4, 65, y1 + 5, "screen")

    # Neck + foot + contact shadow, the sprite's last 8 rows. A side bevel
    # on the neck (light left, shadow right, matching the bezel) and a
    # beveled front face on the foot in front of its already-lit top face.
    neck_y0, neck_y1 = y1 + 9, y1 + 12
    foot_y0, foot_y1 = y1 + 13, y1 + 15
    shadow_y = y1 + 16
    s.rect(60, neck_y0, 72, neck_y1, "metal")
    for y in range(neck_y0, neck_y1 + 1):
        s.dot(60, y, bayer_mix(60, y, 0.5, "metal", "wall_light"))
        s.dot(61, y, bayer_mix(61, y, 0.2, "metal", "wall_light"))
        s.dot(72, y, bayer_mix(72, y, 0.5, "metal", "shadow"))
        s.dot(71, y, bayer_mix(71, y, 0.2, "metal", "shadow"))
    s.rect(46, foot_y0, 86, foot_y1, "metal")
    s.hline(foot_y0, 46, 86, "screen_dim")     # foot's lit top face, catches the screen glow
    for x in range(46, 87):
        s.dot(x, foot_y1, bayer_mix(x, foot_y1, 0.45, "metal", "shadow"))
    s.hline(shadow_y, 36, 96, "shadow")        # contact shadow, wider than the foot
    assert shadow_y == s.h - 1, "monitor's contact shadow must be the sprite's last row"
    return s


# SCENE-REACTIONS - "click the monitor -> it shakes", and THE composition
# contract that shape has to respect.
#
# The 11 lines of live terminal text are NOT part of this sprite: they are DOM
# text (#terminal in app/public/css/game.css) positioned in SCENE coordinates
# that know nothing about which monitor PNG is currently shown - left 200px /
# top 48px / 240x88 at the frontend's 2x upscale, i.e. room x100..219,
# y24..67. monitor.png is placed at room (94, 20) (SCENERY in
# app/frontend/src/geometry.ts), so its glass - MONITOR_SCREEN_RECT - lands on
# room x98..221, y24..67 and the text now sits ENTIRELY inside it (a pixel-exact
# vertical fit; 2px of margin at the left and right). The top used to be 52px,
# which dropped the box 2 rows and overhung the chin - once STORE-2.0's tinted
# bezel made that overhang visible it read as overflow, so game.css pins the box
# to the glass. See game.css #terminal for the full note.
#
# Two consequences, both baked into the art rather than left to the wiring:
#   * the shake is HORIZONTAL ONLY. The glass rows never move, so nothing can
#     slide out from under text that does not move with it.
#   * the amplitude is 1px, which is exactly the margin the text has. A 2px
#     shake would put a glyph column on the bezel.
# The alternative - shifting the whole #sprite-monitor element in the DOM -
# would need the terminal element shifted by the same amount in the same
# frame; these frames deliberately do not require that (see the handoff note).
MONITOR_ROOM_ORIGIN = (94, 20)
TERMINAL_ROOM_RECT = (100, 24, 219, 67)
# Only the HEAD moves: bezel + glass + chin (local rows 0..55). The neck, foot
# and contact shadow (rows 56..63) stay planted, which is both what a knocked
# monitor does and what keeps the "contact shadow wider than the object" rule
# true in the react frames.
MONITOR_HEAD_ROWS = frozenset(range(0, MONITOR_SCREEN_RECT[3] + 9))
MONITOR_SHAKE_DX = {"a": -1, "b": 1}


def build_monitor_shake(tag: str) -> Sprite:
    """monitor.png with its head slid MONITOR_SHAKE_DX[tag] px sideways."""
    return shifted_copy(build_monitor(), MONITOR_SHAKE_DX[tag], 0,
                        rows=set(MONITOR_HEAD_ROWS))


def build_monitor_frame_shake(tag: str) -> Sprite:
    """monitor_frame.png (the tinted bezel overlay) with its head slid the SAME
    MONITOR_SHAKE_DX[tag] px sideways as build_monitor_shake, over the SAME
    MONITOR_HEAD_ROWS. That is the whole of BUG-1's frame-sync fix: the frontend
    swaps this bezel-shake in step with monitor_shake, so the base sprite and
    its tinted frame knock as one rigid monitor instead of the frame staying
    put while the base slides. The screen rect is transparent in the rest
    frame, so its shifted copy is transparent too - the tint never lands on the
    glass or the DOM terminal text (assert_monitor_frame_shake)."""
    return shifted_copy(build_monitor_frame(), MONITOR_SHAKE_DX[tag], 0,
                        rows=set(MONITOR_HEAD_ROWS))


def build_monitor_frame() -> Sprite:
    """132x64 RAMP-PURE bezel FORM for the STORE-2.0 'monitor' colour slot
    (docs/plan/ROADMAP.md; catalog.go items monitor_slate/cobalt/forest/neon,
    which differ only by bezel colour).

    The runtime tint system is gone from the wire/save/economy, but the
    frontend still recolours parts at RENDER time via the CSS mask+multiply
    tint recipe (art-direction "The CSS tint mechanism"), keyed on a colour
    token parsed from the item id. The monitor recolours the same way, so -
    exactly like chair_<style>_form / dev_form_* - this ships as ONE grayscale
    ramp form, NOT one PNG per colour. The frontend overlays it, CSS-tinted by
    the equipped monitor's colour, on top of the fixed monitor.png.

    Geometry mirrors build_monitor's METAL casing so the tint lands exactly on
    the bezel/frame the base sprite draws: the 4px bezel border, the 8px chin,
    the neck and the foot. Three regions are left TRANSPARENT so the fixed
    monitor.png shows through them UNCHANGED and never takes the bezel tint:
      * the inner SCREEN rect (MONITOR_SCREEN_RECT) - the load-bearing
        terminal geometry, which must not recolour;
      * the chin's power LED (a fixed `screen` feature of monitor.png); and
      * the contact-shadow row - it belongs to the desk, not the casing.

    One upper-left key light, like every other form: the casing base sits at
    ramp4 (tint HEADROOM per the tint-crush lesson), a dithered ramp5 rim
    rides the outer top+left edge so the bezel stays a legible lit edge even
    at the darkest tint (slate ~= metal ~= the old bezel), and the shadow side
    (outer right) plus the inner AO ring where the bezel recesses to the glass
    fall to ramp2/ramp3.
    """
    s = Sprite(132, 64, palette=RAMP)
    x0, y0, x1, y1 = MONITOR_SCREEN_RECT       # (4, 4, 127, 47)

    # Casing base at ramp4, then punch the exact screen rect back to clear -
    # the same "one rect call defines the hole" discipline as build_monitor,
    # so the transparent screen region stays byte-exact with MONITOR_SCREEN_RECT.
    s.rect(0, 0, 131, y1, "ramp4")
    for y in range(y0, y1 + 1):
        for x in range(x0, x1 + 1):
            s.clear(x, y)
    s.rect(0, y1 + 1, 131, y1 + 8, "ramp4")    # chin
    s.rect(60, y1 + 9, 72, y1 + 12, "ramp4")   # neck
    s.rect(46, y1 + 13, 86, y1 + 15, "ramp4")  # foot

    # Outer bevel, top band (rows 0..2): a dithered ramp5 lit edge fading back
    # to base - the raised top of the bezel under the upper-left key light.
    for x in range(0, 132):
        s.dot(x, 0, bayer_mix(x, 0, 0.7, "ramp4", "ramp5"))
    for y, ratio in ((1, 0.45), (2, 0.2)):
        for x in range(x0, x1 + 1):
            s.dot(x, y, bayer_mix(x, y, ratio, "ramp4", "ramp5"))
    # Inner AO ring, top (row 3, the row touching the glass well).
    for x in range(x0, x1 + 1):
        s.dot(x, 3, bayer_mix(x, 3, 0.55, "ramp4", "ramp2"))

    # Outer bevel, left column (cols 0..2): the same upper-left highlight.
    for x, ratio in ((0, 0.7), (1, 0.4), (2, 0.15)):
        for y in range(0, y1 + 1):
            s.dot(x, y, bayer_mix(x, y, ratio, "ramp4", "ramp5"))
    # Inner AO ring, left (col 3).
    for y in range(y0, y1 + 1):
        s.dot(3, y, bayer_mix(3, y, 0.5, "ramp4", "ramp2"))

    # Outer bevel, right column (cols 129..131): the shadow side, away from
    # the key light - the far edge falls toward ramp2.
    for x, ratio in ((131, 0.7), (130, 0.4), (129, 0.15)):
        for y in range(0, y1 + 1):
            s.dot(x, y, bayer_mix(x, y, ratio, "ramp4", "ramp2"))
    # Inner lip, right (col 128): one ramp step down - the recess to the glass.
    for y in range(y0, y1 + 1):
        s.dot(128, y, bayer_mix(128, y, 0.4, "ramp4", "ramp3"))

    # Chin: a lit ridge where the bezel steps down to it, an AO row before the
    # neck, and the power-LED hole punched clear (so monitor.png's `screen` LED
    # shows through un-tinted).
    for x in range(0, 132):
        s.dot(x, y1 + 1, bayer_mix(x, y1 + 1, 0.35, "ramp4", "ramp5"))
        s.dot(x, y1 + 8, bayer_mix(x, y1 + 8, 0.4, "ramp4", "ramp2"))
    for y in range(y1 + 4, y1 + 6):            # LED rows 52..53
        for x in range(64, 66):                # LED cols 64..65
            s.clear(x, y)

    # Neck bevel (light left, shadow right) + foot (lit top, AO front),
    # matching build_monitor so slate reads exactly like the old casing.
    neck_y0, neck_y1 = y1 + 9, y1 + 12
    foot_y0, foot_y1 = y1 + 13, y1 + 15
    for y in range(neck_y0, neck_y1 + 1):
        s.dot(60, y, bayer_mix(60, y, 0.6, "ramp4", "ramp5"))
        s.dot(72, y, bayer_mix(72, y, 0.6, "ramp4", "ramp2"))
    for x in range(46, 87):
        s.dot(x, foot_y0, bayer_mix(x, foot_y0, 0.35, "ramp4", "ramp5"))
        s.dot(x, foot_y1, bayer_mix(x, foot_y1, 0.45, "ramp4", "ramp2"))
    return s


# --------------------------------------------------------------------------
# Developer (14): dev_form_*, dev_base_*, hoodie_*
# --------------------------------------------------------------------------
#
# PERSPECTIVE REWRITE - 3/4 BEHIND, OVER-THE-SHOULDER.
#
# The previous figure was drawn TOP-DOWN (a bird's-eye head dome with two
# arms rising straight up/away to the keyboard) while the rest of the scene -
# monitor screen facing us, desk seen from above-behind, chair back facing us
# - is drawn from BEHIND the seated developer. The two projections
# contradicted each other, which is why the character never read right no
# matter how it was shaded.
#
# The camera this version is drawn for: BEHIND the developer and clearly
# ABOVE them, looking over their shoulders at the monitor. That single
# camera fixes every silhouette decision:
#
#   HOOD      the BACK of the hood: a rounded crown with a centre-back fold
#             seam, a hint of the hood's own opening rim cresting the top of
#             the head (we are above it), and the fabric draping outward onto
#             the shoulders at the hem.
#   BACK      sloping shoulders CLEARLY wider than the hood (max half-width
#             38 vs the hood's 21), widening from under the hood hem down to
#             the shoulder line and tapering below - the upper back/trapezius
#             we look down onto. Its LOWER half is OCCLUDED by the chair back
#             (the chair now composites ON TOP of the developer, geometry.ts
#             CHAIR_Z_* > DEV_Z_*), which is the whole point: from behind, a
#             chair's backrest is between the camera and the person's torso.
#   ARMS      because the camera is above, we look OVER the shoulders and see
#             the arms reach FORWARD onto the desk. Forward = UP the screen
#             (the keyboard is further away), so each arm leaves the deltoid,
#             bulges out to an elbow at the side, and runs up-and-in as a
#             foreshortened forearm to a HAND flat on the keys. Hands are
#             VISIBLE and they are what animates.
#   HANDS     backs-of-hands seen from above: fingers (split by shadow
#             notches) pointing away onto the keys, a lit knuckle edge on the
#             upper-left, a shadow edge on the lower-right. type_a/type_b
#             alternate which hand presses (2px), so typing is visible motion
#             on the keyboard. The `mouse` frame moves the RIGHT hand off the
#             keyboard and onto the mouse (room x224..267) - a genuinely
#             different, longer reach with its own arm path, not an offset.
#             `sleep` slides both hands forward-and-down off the keys onto
#             the desk lip and tips the hood down.
#
# 192x76 canvas anchored at room (64, 92) [DEV_RECT]. The canvas is wide
# because the mouse reach needs room x252 and the figure must stay CENTRED in
# its own canvas (room x159.5 = local 95.5) - that is what keeps the derived
# 40x40 hoodie thumbnails a centred crop of the hood.
#
# Room mapping: room x = 64 + lx, room y = 92 + ly.
#
# PROPORTION PASS (this revision). The shape was right but the SCALE was
# not: the hands are correctly sized against the keyboard keys they rest on
# (21x10 each then; the hand-narrowing pass below takes them to 17x10) and
# everything else was drawn about 1.45x too small, so the figure read as a
# small person with huge hands, lost against a 320x200
# room. The fix scales the BODY up to the hands rather than shrinking the
# hands: hood 34x25 -> 48x34 px, shoulders 64 -> 92 px wide, arms 7..11 ->
# 11..19 px thick, and every chair up with it.
#
# SLIMMING PASS (this revision). The scale was then right but the BUILD was
# not: the owner read the figure as "FAT". Three things caused that, and all
# three are fixed here without touching anything already approved (the mixed
# behind/elevated camera, the hands on the keys and the mouse pose, the hood
# identity, the hand size):
#   1. the shoulders were 92px across - 4.4 hand-widths, the top of the human
#      range - so they came down to 76px (3.6 hand-widths against the 21px
#      hand of the time, 4.5 against today's 17px one - still human, still
#      1.65x the hood as it then was, which is what keeps the hooded read
#      (the hood-narrowing pass below takes that ratio to 1.81x);
#   2. the torso was a STRAIGHT SLAB at full shoulder width for every row the
#      player could see, so it now tapers 76 -> 66px across those same rows
#      (_SH_HW below) - a waist, which is the single biggest part of the fix;
#   3. the hood's hem drape flared 2px wider than the hood body, widening the
#      figure exactly where head meets shoulders; the flare is now 1px.
# The arms were re-pathed to the narrower shoulders (elbows in by 3px,
# deltoids flush with the new shoulder edge, 11..19 -> 10..16px thick) with
# every WRIST and HAND left exactly where it was - the reach targets are fixed
# world positions (the keys, the mouse) and are not negotiable.
#
# HOOD-NARROWING PASS (this revision). The slimming pass was approved, with
# one residual note: "the head is still a little wider". So the HOOD alone
# comes in - 46 -> 42px at its widest (body 44 -> 40, hem drape half-width 23
# -> 21) - and NOTHING else moves. Hands, arms, shoulders (_SH_HW), the waist
# taper and all four chairs are byte-for-byte the slim pass's; the only
# numbers that change are the hood table, the crown's ellipse span
# (_HOOD_ROUND 13 -> 12, so the dome stays a dome instead of stretching into
# an egg over its unchanged 31 rows) and the marks that are PRINTED on the
# hood - the panel seams, the hem arc, hoodie_cloak's drape folds and
# hoodie_classic's drawstrings/yoke-seam ends - each rescaled by the same
# 20/22 so nothing ends up floating beside the fabric (assert_hoodie_on_fabric
# is what holds that honest for the overlays).
#
# 42 rather than 40 was chosen by rendering 46/42/40 in the real game and
# looking at all three: 42 already reads as a clear narrowing at 1x (2px a
# side is a visible change against what was then a 21px hand), it keeps the
# hood at 2.0 hand-widths of that hand (2.5 of today's 17px one) and
# shoulder:hood at 76:42 = 1.81x, and it leaves the panel seams 5..7px
# inside the silhouette so they still read as SEAMS. At 40 the
# crown starts to pinch and those seams crowd the outline - a strictly worse
# hood for a difference barely visible at 1x.
#
# HAND-NARROWING PASS (this revision; owner: "make hands a bit smaller, like
# 20% less wide"). The slim body and the narrow hood were approved, which left
# the hands as the last oversized part: 21px wide against 76px shoulders is
# 3.6 hand-widths, the LOW end of the human range, so the figure still read
# slightly mitten-handed. Hands are now 17x10 (DEV_HAND_W/H, see "--- HANDS"),
# which puts shoulder:hand at 76:17 = 4.5 - the anthropometric ratio - and
# hood:hand at 42:17 = 2.5. Only the hands moved: shoulders, waist, hood,
# chairs and every arm control point are byte-identical to the two passes
# before this one. Each hand also kept its CENTRE, which is why the keyboard
# contact points and the mouse landing are exactly where they were - the
# hands closed in around them rather than sliding.
#
# Vertical budget (why everything is where it is): the SPRINT/STATUS panels
# cover the scene below room y161 (they are opaque from room y161 to y198,
# and the 4-room-px gap between them at room x158..161 is itself covered by
# the chair, which draws on top), and the keyboard occupies room y90..113
# with its key rows at y95..107. So the whole figure has to read inside room
# y99..160:
#     y100..109  hands on the keys (keyboard rows y90..98 stay visible)
#     y110..140  hood (the back of the head), 31 rows
#     y136..149  shoulders / upper back: out to 76px, then tapering
#     y150+      the chair back, which draws ON TOP from here down
# The developer's near-camera parts (hood, arms, hands) all live ABOVE room
# y150; every chair sprite starts AT y150 (its detail halo at y149). That
# vertical split - asserted by assert_chair_region below - is what makes the
# plain Z-order swap correct with no per-layer masking.
#
# The canvas stays 76 rows tall (room y92..167) DELIBERATELY. Everything
# below room y161 is behind the HUD panels, so a taller canvas would buy the
# figure no visible pixel; it would only push the derived 40x40 hoodie
# thumbnails past derive_thumbnail's 1:1 threshold into a /2 downsample that
# drops every other row/column of the 1px hoodie style marks. The lower
# torso therefore tapers to a rounded end at room y166, five rows behind the
# HUD's top edge, instead of being extended and truncated.
#
# All geometry is defined for the LEFT half / left side only; the right half
# is the exact mirror `DEV_MIRROR - x`, so the two sides cannot drift apart.
# The one exception is the `mouse` frame's right arm, which is a wholly
# different pose and therefore has its own explicit right-side path.

DEV_W, DEV_H = 192, 76
DEV_MIRROR = DEV_W - 1        # 191; mirrored x = DEV_MIRROR - x (centre 95.5)
DEV_CX = DEV_W // 2           # 96; the figure's centreline is local 95/96
DEV_OX, DEV_OY = 64, 92       # room x = DEV_OX + lx, room y = DEV_OY + ly
# P3 "Character life" - AMBIENT + CELEBRATION frames.
#
# The rule these four frames are authored under: the owner-approved pose,
# proportions and silhouette do not change. Each new frame is the SAME figure
# displaced by 1..3px, so the scheduler in render/scene.ts can cross-fade
# between them by plain sprite swap and the figure never morphs:
#
#   breath   the whole upper body (hood + back + the shoulder end of both
#            arms) rises 1px; the HANDS stay planted on the keys. A shoulder
#            rise, which is what a breath looks like from behind.
#   stretch  the same lift, 2px, held longer - sitting up / stretching out of
#            the chair with the hands still on the keyboard.
#   cheer_a/ the celebration beat: body up 1px / 3px (a bounce) with both
#   cheer_b  hands flung OFF the keys and OUT past the keyboard's two ends.
#            Overhead is physically unavailable on this canvas - the hands
#            already sit on the top legal row (DEV_KB_GUARD_ROW), so "arms
#            up" is expressed as arms opened wide plus a vertical bounce.
#
# ARM LIFT, not arm redraw: breath/stretch reuse _ARM_BASE_LEFT and shift only
# its shoulder-side control points (see _ARM_LIFT_WEIGHT), so the wrist stays
# exactly where the keys are and the limb cannot detach at either end.
DEV_AMBIENT_FRAMES = ("breath", "stretch")
DEV_CHEER_FRAMES = ("cheer_a", "cheer_b")
# SCENE-REACTIONS "hey!" - the click react, two more ADDITIVE frames on the
# same pose:
#
#   react_a  the startle. The whole upper body jumps 2px UP and leans 1px
#            AWAY (-x) - a recoil - and the RIGHT hand pops off the keys out
#            past the keyboard's right end.
#   react_b  the acknowledge. The body settles back to 1px up and leans 1px
#            TOWARD the hand (+x), which is the closest this camera can get to
#            "turning to look at you", while that hand swings 5px further out:
#            a wave, not a flail.
#
# Why a rigid whole-body lean instead of tilting the HOOD alone (which is what
# a head-turn would really be): the four hoodie_<style>.png overlays are one
# file per style for every frame, printed on hood AND back, and the frontend
# realigns them by a single rigid offset (FRAME_OVERLAY_DY, now joined by
# FRAME_OVERLAY_DX). Tilt the hood on its own and hoodie_tech's edge piping
# slides off the hood's silhouette while its shoulder strap stays put - the
# one thing assert_hoodie_react_alignment exists to prevent. A 1px lean of the
# whole torso is honest, reads as a flinch, and keeps every printed mark
# pixel-locked to the fabric it is printed on.
DEV_REACT_FRAMES = ("react_a", "react_b")
DEV_FRAMES = ("idle", "type_a", "type_b", "mouse", "sleep") + \
    DEV_AMBIENT_FRAMES + DEV_CHEER_FRAMES + DEV_REACT_FRAMES

# Keyboard guard: the keyboard (room y90..113) is drawn UNDER the developer,
# so the hands legitimately cover its NEAR rows - but its far rows must stay
# visible or the purchasable keyboard disappears behind the figure. No dev
# pixel may sit in local rows 0..6 (room y92..98); the hands start at local
# row 8 so their dev_base outline (row 7) is legal too.
DEV_KB_GUARD_ROW = 7

# --- HOOD (local row -> half-width) ---------------------------------------
# 42x31 (local rows 18..48 = room y110..140). Crown at local
# 18 = room y110, which is the keyboard case's own bottom bevel row: the
# hood's `shadow` halo lands on it, so the head reads as being IN FRONT of
# the keyboard's near edge - correct for a camera behind the developer, and
# the strongest depth cue available in ten pixels. The key rows the hands
# rest on (room y95..107) are never touched.
#
# The dome is generated rather than hand-listed so the crown is a true
# quarter-ellipse (a hand-typed table at this size wobbled): half-width climbs
# 6 -> 20, holds 20 for the body, flares to 21 across the hem where the fabric
# drapes onto the shoulders, then eases back in (the SLIMMING PASS took that
# flare down from 24 to 23: two extra pixels of drape at the exact row where
# head meets shoulders read as width on the whole figure; the HOOD-NARROWING
# PASS then took the whole table in by 2, flare included).
#
# _HOOD_ROUND came down 13 -> 12 with the body half-width, which is the part
# that is easy to get wrong: the hood's 31 rows are FIXED by the vertical
# budget, so narrowing the body without shortening the crown's ellipse span
# would have stretched the dome into an egg. 12/20 holds the same crown
# curvature 13/22 had, and the apex row is unchanged at half-width 6.
#
# 42 wide is 2.5 hand-widths (a hand is DEV_HAND_W = 17px) - still
# unmistakably bigger than either hand, which was half of the original
# "FAT" complaint - and 76:42 = 1.81x against the shoulders, the ratio that
# carries the hooded read. The hem stops at room y140 rather than reaching
# for the chair line: the OTHER half of the complaint was that the shoulders were
# a sliver, and the only way to buy them a readable band in a scene whose
# vertical budget is fixed (keyboard above, HUD below) is to spend rows on the
# upper back instead of on more hood. The split that reads best in the live
# composite is hood 31 rows / bare upper back 11 rows / chair crown 11 rows.
_HOOD_TOP, _HOOD_BOT = 18, 48
_HOOD_BODY_HW = 20
_HOOD_ROUND = 12              # rows the crown takes to reach the body width
_HOOD_HW = {}
for _d in range(_HOOD_ROUND):
    _t = (_HOOD_ROUND - _d) / (_HOOD_ROUND + 0.5)
    _HOOD_HW[_HOOD_TOP + _d] = int(round(_HOOD_BODY_HW * (1.0 - _t * _t) ** 0.5))
for _y in range(_HOOD_TOP + _HOOD_ROUND, 41):
    _HOOD_HW[_y] = _HOOD_BODY_HW
_HOOD_HW.update({41: 20, 42: 21, 43: 21, 44: 21, 45: 21, 46: 20, 47: 19, 48: 17})
assert min(_HOOD_HW) == _HOOD_TOP and max(_HOOD_HW) == _HOOD_BOT
_HOOD_HEM_HW = 21             # widest row; the hem arc below is anchored on it

# --- BACK / SHOULDERS -----------------------------------------------------
# 76px across at the shoulder line (half-width 38, down from 46) - 4.5
# hand-widths against the 17px hand, which is what a real biacromial span is
# against a real hand, and 1.81x the hood's 42: a lean upper body under a
# head, where 4.4 hand-widths of straight SLAB read as a fat one (it was the
# slab that read as fat, not the number - this is a taper now).
# Starts at local row 44 INSIDE the hood
# hem and widens monotonically (no dip -> no notch; the hood-narrowing pass
# makes the first step out of the hem 21 -> 26 instead of 23 -> 26, which is
# still a widening, i.e. still a shoulder and not a notch), steeply enough (26 -> 38
# in six rows) that the widest part of the torso lands ABOVE the backrest
# crown where the camera can actually see it.
#
# Local rows 44..57 (room y136..149) are the band the player sees BARE, and
# this is where the SLIMMING PASS spends its whole budget: rows 44..48 flank
# the hood on both sides, row 49 is the shoulder line (38), and rows 50..57
# TAPER back in to 34 - a visible waist across the nine rows the player
# actually looks at, instead of the full-width slab that was there before.
# From row 58 (room y150 = CHAIR_TOP_ROOM_Y) down the backrest covers the
# middle and the torso holds a constant 33 so the shoulders keep peeking out
# at the sides of every chair (33 against the widest backrest's 28). Below
# row 67 it tapers to a rounded end at local 74 (room y166), behind the HUD.
_BACK_TOP = 44
_BACK_BOT = 74
# The shoulder ramp UP out of the hood hem (rows 44..49) and the waist taper
# DOWN from it (rows 50..67), listed row by row because both halves are what
# the eye reads and neither is a formula: the ramp has to start just wider
# than the hood hem (21) so there is no notch where fabric meets body, and
# the taper has to lose a visible 5px per side across the nine BARE rows
# (49..57) - that is the whole "lean, not slab" fix - then hold 33 through the
# chair band so the shoulders keep peeking out at the sides of every backrest.
_SH_HW = {44: 26, 45: 30, 46: 33, 47: 35, 48: 37, 49: 38, 50: 38,
          51: 37, 52: 37, 53: 36, 54: 36, 55: 35, 56: 34, 57: 34}
_WAIST_HW = 33                # rows 58..67, the band the chair back covers


def _back_hw(y: int) -> int:
    if y in _SH_HW:
        return _SH_HW[y]
    if y <= 67:
        return _WAIST_HW
    return max(15, _WAIST_HW - ((y - 67) * 3) // 2)   # rounded taper into the seat


DOME_DY = {"idle": 0, "type_a": 0, "type_b": 0, "mouse": 0, "sleep": 3,
           "breath": -1, "stretch": -2, "cheer_a": -1, "cheer_b": -3,
           "react_a": -2, "react_b": -1}
BACK_DY = {"idle": 0, "type_a": 0, "type_b": 0, "mouse": 0, "sleep": 2,
           "breath": -1, "stretch": -2, "cheer_a": -1, "cheer_b": -3,
           "react_a": -2, "react_b": -1}
# SIDEWAYS lean, the axis SCENE-REACTIONS adds (every pre-react frame is 0 and
# stays 0). Applied to hood and back TOGETHER, exactly like the vertical lift,
# so the garment moves as one rigid piece; +-1px is the whole budget, which at
# the frontend's 2x upscale is a clearly visible 2px flinch without the
# silhouette ever morphing.
BODY_DX = {f: 0 for f in DEV_FRAMES}
BODY_DX.update({"react_a": -1, "react_b": 1})
assert set(BODY_DX) == set(DEV_FRAMES), "BODY_DX must cover every frame"
assert all(abs(v) <= 1 for v in BODY_DX.values()), "the lean budget is +-1px"

# HOODIE-OVERLAY ALIGNMENT (this is why every P3 frame lifts hood and back by
# the SAME amount, unlike `sleep`, whose 3px/2px split is the documented
# pre-P3 simplification). The four hoodie_<style>.png overlays are authored
# once against the `idle` geometry and are the same single file for every
# frame - the wire carries one sprite filename per catalog item and this
# generator does not fork it per frame. So a frame that MOVES the
# overlay-bearing hood/back has exactly two honest options: ship per-frame
# overlay variants (a new asset per style per frame, and a catalog/wire change
# to select them), or move the ONE overlay by the same rigid offset the fabric
# moved. The P3 frames take the second, which is exact precisely because
# DOME_DY == BACK_DY here: render/scene.ts offsets the hoodie layer's `top` by
# FRAME_OVERLAY_DY[frame] and every drawstring, zip tooth and hem stripe stays
# pixel-locked to the fabric it is printed on. main() prints this table so the
# frontend copy of it can be diffed against the art.
FRAME_OVERLAY_DY = {f: (DOME_DY[f] if DOME_DY[f] == BACK_DY[f] else None)
                    for f in DEV_FRAMES}
# SCENE-REACTIONS: the same contract on the new horizontal axis. render/scene.ts
# must offset the hoodie layer's `left` by this in addition to its `top` -
# every pre-react frame is 0, so an un-updated frontend keeps working for the
# frames it already knows and only the react frames need the new column.
FRAME_OVERLAY_DX = {f: (None if FRAME_OVERLAY_DY[f] is None else BODY_DX[f])
                    for f in DEV_FRAMES}
assert FRAME_OVERLAY_DY["sleep"] is None, "sleep is the ONE non-rigid frame"
assert all(dy is not None for f, dy in FRAME_OVERLAY_DY.items() if f != "sleep"), \
    "every non-sleep frame must lift hood and back rigidly (DOME_DY == BACK_DY)"
# The largest upward displacement any frame applies, i.e. how far the hoodie
# overlay can be shifted up by the frontend - which is why the overlays have
# to keep this many EXTRA rows clear above DEV_KB_GUARD_ROW (assert_dev_lift_headroom).
DEV_MAX_BODY_LIFT = -min(min(DOME_DY.values()), min(BACK_DY.values()))


def _hood_span(y: int, ddy: int, ddx: int = 0):
    hw = _HOOD_HW.get(y - ddy)
    if hw is None:
        return None
    return DEV_CX - hw + ddx, DEV_CX + hw - 1 + ddx


def _back_span(y: int, sdy: int, sdx: int = 0):
    ry = y - sdy
    if ry < _BACK_TOP or ry > _BACK_BOT:
        return None
    hw = _back_hw(ry)
    return DEV_CX - hw + sdx, DEV_CX + hw - 1 + sdx


# --- HANDS ----------------------------------------------------------------
# A hand is DEV_HAND_W x DEV_HAND_H px, and every hand in every frame is that
# same rect moved around - the pose changes where a hand IS, never how big it
# is. Rects are therefore declared as (CENTRE x, top row) and expanded by
# `_hand_rect`, because the centre is the part that carries meaning: it is the
# world point the hand contacts (a key, the mouse), and a width pass has to
# leave every one of those contact points exactly where it was.
#
# HAND-NARROWING PASS (owner: "make hands a bit smaller, like 20% less wide"):
# 21 -> 17px wide, height held at 10. 20% off 21 is 16.8, and of the three
# candidates 17 is both the closest and the one the finger geometry likes:
# _dev_paint_hand splits the width into four finger runs at (w*k)//4, which
# gives 4/3/3/4 at w=17 - symmetric, exactly like the 5/4/4/5 it replaces -
# against a lopsided 4/3/3/3 at 16 and 4/4/3/4 at 18. The notches, the lit
# knuckle edge and the shadow side are all derived from x0/x1/w, so they
# rescale with the width instead of being cropped off the end of it.
# Height stayed 10: this was a WIDTH note, a hand seen from above-behind is
# wider than it is deep, and 17x10 (1.7:1) is still that - the pre-pass 2.1:1
# was the flat, splayed shape the note was actually about.
DEV_HAND_W, DEV_HAND_H = 17, 10


def _hand_rect(cx: int, y0: int):
    """(x0, x1, y0, y1) for a DEV_HAND_W x DEV_HAND_H hand centred on `cx`.
    W is odd, so `cx` is a real pixel column and the rect is exactly centred
    (which is what keeps a mirrored right hand centred on DEV_MIRROR - cx)."""
    x0 = cx - (DEV_HAND_W - 1) // 2
    return x0, x0 + DEV_HAND_W - 1, y0, y0 + DEV_HAND_H - 1


# --- ARMS -----------------------------------------------------------------
# LEFT-arm centreline as control points (x, y, half-thickness) listed from
# the WRIST (top, under the hand on the keyboard) down to the shoulder. The
# path is deliberately STRAIGHT-ish: a near-vertical foreshortened forearm
# from the hand down to an elbow that sits out at the side and LEVEL with the
# shoulder line, then in and down into the deltoid so the limb never ends in
# mid-air. An earlier version put the elbow high and mid-way, which turned
# each arm into a thick flexed loop around the head.
#
# PROPORTION PASS: thickness grew with the torso, from a slim wrist (5.5 ->
# 11px) to a broad deltoid (9.5 -> 19px) - against a 48px hood and 92px
# shoulders that is the ratio a real arm has, and it is what stops the bigger
# body from wearing spaghetti arms. The forearm also got longer (wrist row 20
# to elbow row 44, up from 20->40) so the reach still lands on the same keys
# from a shoulder that is now four rows lower.
#
# SLIMMING PASS: 92px shoulders became 76px, so the elbow came IN with them
# (local x54 -> x61, still just outside the new shoulder edge at x58) and the
# deltoid moved to x69/row 55, flush with that edge instead of buried three
# pixels inside a slab. Thickness came down one notch with it (wrist 5.5 ->
# 4.8, deltoid 9.5 -> 8.0, i.e. 11..19px -> 10..16px), which holds the same
# arm-to-torso ratio the proportion pass established. The WRIST rows are
# UNTOUCHED: those are keyboard/mouse world positions.
#
# HAND-NARROWING PASS: the arm control points are UNCHANGED, and that is a
# result, not an omission. The taper still meets the hand cleanly, with room
# to spare: the wrist stamp paints 9px across the rows the hand covers (10px
# at its widest, on the `mouse` reach), so the forearm END is comfortably
# INSIDE the 17px hand, and it only reaches 17px of its own down at row 45,
# where it is already merging into the deltoid. A narrower hand did not need
# a narrower wrist - it needed the wrist to stop being the wider of the two,
# which it never was.
# The right arm is the exact mirror for every frame but `mouse`.
_ARM_BASE_LEFT = [
    (63, 20, 4.8),    # wrist, just under the hand
    (61, 32, 5.6),    # forearm - near VERTICAL, only leaning out a little
    (61, 43, 6.4),    # elbow, just OUTSIDE the shoulder, above the shoulder line
    (69, 55, 8.0),    # deltoid, flush with the shoulder edge
]
_HAND_BASE_LEFT = _hand_rect(64, 8)     # room x120..136, y100..109 (centre room x128)

# `mouse`: the right hand has left the keyboard for the mouse (room
# x224..267). A longer, flatter reach - its own path, not an offset, because
# the shoulder cannot move and everything between it and the hand must
# lengthen and flatten to get there.
_ARM_MOUSE_RIGHT = [
    (176, 20, 4.8),   # wrist over the mouse
    (167, 30, 5.6),   # forearm, reaching right
    (151, 40, 6.6),   # elbow, swung well out to the side
    (137, 48, 7.3),   # upper arm
    (122, 55, 8.0),   # deltoid (mirror of the left one)
]
_HAND_MOUSE_RIGHT = _hand_rect(178, 8)  # room x234..250, y100..109 (centre room x242)

# P3 `cheer_a`/`cheer_b`: the celebration. Both arms open wide - a straight-ish
# diagonal from the deltoid (which does NOT move sideways; a shoulder cannot)
# out to a hand well past the end of the keyboard, so the pair reads as a V
# rather than the two near-vertical tubes the typing pose is. The deltoid row
# carries the frame's body bounce (-1 / -3), which is what makes the two frames
# a bounce instead of a lateral twitch. Hands stay on local rows 8..17: the
# keyboard far-row guard (DEV_KB_GUARD_ROW) forbids going any higher, so the
# celebration's energy is spent on width plus the bounce.
_ARM_CHEER_LEFT = {
    "cheer_a": [(48, 20, 4.8), (54, 31, 5.5), (61, 42, 6.4), (69, 53, 8.0)],
    "cheer_b": [(42, 20, 4.8), (50, 31, 5.5), (59, 42, 6.4), (69, 51, 8.0)],
}
_HAND_CHEER_LEFT = {"cheer_a": _hand_rect(48, 8),   # room x104..120
                    "cheer_b": _hand_rect(42, 8)}   # room x98..114

# SCENE-REACTIONS `react_a`/`react_b`: the "hey!" wave. ONLY the right arm has
# its own path (the left one is the base arm carrying the body's lift/lean, so
# the left hand stays planted on its key and anchors the whole beat). The hand
# leaves the keyboard SIDEWAYS, not upward: rows 8..17 is already the top legal
# band (DEV_KB_GUARD_ROW), so - exactly as for the cheer pair - "off the keys"
# has to be spent on width. It lands on the bare desk strip between the
# keyboard (ends room x207) and the mouse slot (starts room x224), the same
# strip the sleep frame's `z` uses, so the wave never covers a purchasable item.
# The deltoid row/column carries the frame's own bounce and lean, which is what
# makes the pair a flinch instead of a detached arm swing.
_ARM_REACT_RIGHT = {
    "react_a": [(142, 20, 4.8), (138, 30, 5.6), (134, 41, 6.6), (121, 53, 8.0)],
    "react_b": [(147, 20, 4.8), (143, 30, 5.6), (138, 41, 6.6), (123, 54, 8.0)],
}
_HAND_REACT_RIGHT = {"react_a": _hand_rect(141, 8),   # room x197..213
                     "react_b": _hand_rect(146, 8)}   # room x202..218

# `sleep`: the hands have slid forward-and-down OFF the keys onto the desk
# lip, the forearms folded short into a slumped shoulder.
_ARM_SLEEP_LEFT = [
    (58, 34, 4.8),
    (59, 44, 5.6),
    (63, 50, 6.6),
    (69, 56, 8.0),
]
_HAND_SLEEP_LEFT = _hand_rect(62, 22)   # room x118..134, y114..123

# Per-frame (dx, dy) nudge of the HAND end, per side: the two hands alternate
# which one is pressing (a 2px drop - clearly visible at the 5fps the
# frontend animates at), so typing is motion ON THE KEYBOARD rather than a
# body wobble. idle rests both hands level. The overlay-bearing hood/back
# geometry never moves between typing frames, so the hoodie style overlays
# (authored once against the idle geometry) stay pixel-aligned.
FRAME_HAND_OFFSET = {
    "idle":   {"L": (0, 0), "R": (0, 0)},
    "type_a": {"L": (0, 0), "R": (0, 2)},
    "type_b": {"L": (0, 2), "R": (0, 0)},
    "mouse":  {"L": (0, 1), "R": (0, 0)},
    "sleep":  {"L": (0, 0), "R": (0, 0)},
    # P3 ambient: the hands do NOT move - a breath is a shoulder rise, and a
    # hand that drifted off its key would break the one thing the typing
    # animation established (motion belongs ON the keyboard). The cheer frames
    # carry their own hand rects (_HAND_CHEER_LEFT) instead of an offset.
    "breath": {"L": (0, 0), "R": (0, 0)},
    "stretch": {"L": (0, 0), "R": (0, 0)},
    "cheer_a": {"L": (0, 0), "R": (0, 0)},
    "cheer_b": {"L": (0, 0), "R": (0, 0)},
    # SCENE-REACTIONS: the left hand stays exactly on its key (no offset) and
    # the right hand carries its own rect (_HAND_REACT_RIGHT), so neither side
    # reads this table - the entries exist so every DEV_FRAMES key is present.
    "react_a": {"L": (0, 0), "R": (0, 0)},
    "react_b": {"L": (0, 0), "R": (0, 0)},
}

# How much of a frame's body lift each arm control point takes, from the WRIST
# (index 0, pinned to the keys) down to the DELTOID (index 3, which rises with
# the shoulders). The graded middle is what keeps the forearm a smooth tube
# instead of kinking at the elbow: at -1px the arm bends only at the elbow and
# below, at -2px the forearm eases in by 1px on the way.
_ARM_LIFT_WEIGHT = (0.0, 0.4, 1.0, 1.0)


def _arm_points(frame: str, side: str):
    """Control points for one arm in `frame`, already mirrored for the right
    side. For typing/idle only the wrist end moves, so the shoulder join
    never detaches; `sleep` and the `mouse` right arm use dedicated paths."""
    if frame == "sleep":
        pts = [list(p) for p in _ARM_SLEEP_LEFT]
        if side == "R":
            pts = [[DEV_MIRROR - p[0], p[1], p[2]] for p in pts]
        return pts
    if frame == "mouse" and side == "R":
        return [list(p) for p in _ARM_MOUSE_RIGHT]
    if frame in DEV_CHEER_FRAMES:
        pts = [list(p) for p in _ARM_CHEER_LEFT[frame]]
        if side == "R":
            pts = [[DEV_MIRROR - p[0], p[1], p[2]] for p in pts]
        return pts
    if frame in DEV_REACT_FRAMES:
        if side == "R":
            return [list(p) for p in _ARM_REACT_RIGHT[frame]]
        # Left arm: the base limb, with the shoulder end carrying the frame's
        # lift AND lean on the same graded weights the ambient frames use - so
        # the wrist stays on its key while the deltoid moves with the body.
        pts = [list(p) for p in _ARM_BASE_LEFT]
        for i, w in enumerate(_ARM_LIFT_WEIGHT):
            pts[i][0] += int(round(BODY_DX[frame] * w))
            pts[i][1] += int(round(BACK_DY[frame] * w))
        return pts
    pts = [list(p) for p in _ARM_BASE_LEFT]
    ox, oy = FRAME_HAND_OFFSET[frame][side]
    for i in (0, 1):
        pts[i][0] += ox
        pts[i][1] += oy
    if frame in DEV_AMBIENT_FRAMES:
        lift = BACK_DY[frame]
        for i, w in enumerate(_ARM_LIFT_WEIGHT):
            pts[i][1] += int(round(lift * w))
    if side == "R":
        pts = [[DEV_MIRROR - p[0], p[1], p[2]] for p in pts]
    return pts


def _stamp_disc(grid, cx: float, cy: float, r: float) -> None:
    r2 = r * r + r * 0.6                       # small outward fudge, rounder tube
    for yy in range(int(cy - r) - 1, int(cy + r) + 2):
        for xx in range(int(cx - r) - 1, int(cx + r) + 2):
            if 0 <= xx < DEV_W and 0 <= yy < DEV_H:
                dx, dy = xx - cx, yy - cy
                if dx * dx + dy * dy <= r2:
                    grid[yy][xx] = True


def _arm_grid(frame: str):
    """Bool [y][x] footprint of BOTH arms for `frame`, rasterised by stamping
    tapered discs along each side's control-point path."""
    grid = [[False] * DEV_W for _ in range(DEV_H)]
    for side in ("L", "R"):
        pts = _arm_points(frame, side)
        for (x0, y0, r0), (x1, y1, r1) in zip(pts, pts[1:]):
            seg = int(max(abs(x1 - x0), abs(y1 - y0))) * 2 + 1
            for i in range(seg + 1):
                t = i / seg
                _stamp_disc(grid, x0 + (x1 - x0) * t, y0 + (y1 - y0) * t,
                            r0 + (r1 - r0) * t)
    return grid


def _dev_hand_rects(frame: str):
    """Left/right skin hand rects (dev_base only) for `frame`."""
    if frame == "sleep":
        x0, x1, y0, y1 = _HAND_SLEEP_LEFT
        return (x0, x1, y0, y1), (DEV_MIRROR - x1, DEV_MIRROR - x0, y0, y1)
    if frame in DEV_CHEER_FRAMES:
        x0, x1, y0, y1 = _HAND_CHEER_LEFT[frame]
        return (x0, x1, y0, y1), (DEV_MIRROR - x1, DEV_MIRROR - x0, y0, y1)
    if frame in DEV_REACT_FRAMES:
        # Left hand planted on its key, right hand out on the bare desk strip.
        return _HAND_BASE_LEFT, _HAND_REACT_RIGHT[frame]
    x0, x1, y0, y1 = _HAND_BASE_LEFT
    lox, loy = FRAME_HAND_OFFSET[frame]["L"]
    left = (x0 + lox, x1 + lox, y0 + loy, y1 + loy)
    if frame == "mouse":
        return left, _HAND_MOUSE_RIGHT
    rox, roy = FRAME_HAND_OFFSET[frame]["R"]
    right = (DEV_MIRROR - (x1 + rox), DEV_MIRROR - (x0 + rox), y0 + roy, y1 + roy)
    return left, right


def _dev_fabric_grid(frame: str):
    """Fabric footprint (hood + back + arms) - exactly what dev_form fills."""
    ddy, sdy = DOME_DY[frame], BACK_DY[frame]
    bdx = BODY_DX[frame]
    grid = _arm_grid(frame)
    for y in range(DEV_H):
        for span in (_hood_span(y, ddy, bdx), _back_span(y, sdy, bdx)):
            if span:
                x0, x1 = span
                for x in range(max(0, x0), min(DEV_W - 1, x1) + 1):
                    grid[y][x] = True
    return grid


def _dev_full_mask(frame: str):
    """Fabric + skin hands - the silhouette the dev_base outline is drawn
    around (so the halo wraps the whole figure, hands included)."""
    grid = _dev_fabric_grid(frame)
    for x0, x1, y0, y1 in _dev_hand_rects(frame):
        for y in range(max(0, y0), min(DEV_H - 1, y1) + 1):
            for x in range(max(0, x0), min(DEV_W - 1, x1) + 1):
                grid[y][x] = True
    return grid


# --------------------------------------------------------------------------
# dev_form shading (the tintable grayscale ramp)
# --------------------------------------------------------------------------
#
# ONE light direction: a soft key from the upper-left plus a rim where the
# monitor's glow wraps the lit (left) silhouette edge - so every gradient
# reads brighter toward small x and darker toward large x, and the outermost
# lit column is boosted toward ramp5 (undarkened at ANY tint) so the figure
# still separates from the wall at the darkest purchasable hoodie.
#
# TINT-CRUSH DISCIPLINE (art-direction ramp table): the base cross-section is
# ANCHORED on ramp4 (v=3.0) and only sways +/-0.8, landing in ramp2..ramp5 -
# so multiplied against the DEFAULT indigo tint (#6a5aa0) it stays a visibly
# graduated mid-value garment, never a crushed void. ramp1 is reserved for
# the small, localised accents (nape AO, fold seam, arm-cast shadow) that
# stack a bounded extra subtraction on top of that anchor - never a whole
# region's dominant tone.


def _arm_run(arms, x: int, y: int):
    x0 = x
    while x0 - 1 >= 0 and arms[y][x0 - 1]:
        x0 -= 1
    x1 = x
    while x1 + 1 < DEV_W and arms[y][x1 + 1]:
        x1 += 1
    return x0, x1


def _arm_shadow(arms, x: int, y: int) -> bool:
    """True if a body pixel sits just lower-right of an arm - the side the
    upper-left key light throws the arm's cast shadow onto, which is what
    peels each arm visually off the hood/back it passes in front of."""
    for dx, dy in ((-1, 0), (-2, 0), (-1, -1), (0, -1)):
        nx, ny = x + dx, y + dy
        if 0 <= nx < DEV_W and 0 <= ny < DEV_H and arms[ny][nx]:
            return True
    return False


def _arm_v(arms, x: int, y: int) -> float:
    x0, x1 = _arm_run(arms, x, y)
    c = (x0 + x1) / 2.0
    half = max(1.0, (x1 - x0) / 2.0)
    return 3.0 - 0.9 * ((x - c) / half)         # rounded cylinder, lit-left


def _hood_v(x: int, y: int, ddy: int, span, arms, ddx: int = 0) -> float:
    x0, x1 = span
    hw = max(1.0, (x1 - x0 + 1) / 2.0)
    # `xr` is x back in the UNLEANED frame of reference: a leaning body carries
    # its own shading with it (centre fold, crown highlight), so the lean can
    # never re-light the garment - it only moves it.
    xr = x - ddx
    rel = (xr - DEV_CX) / hw
    ry = y - ddy
    v = 3.0 - 0.8 * rel
    if ry <= _HOOD_TOP + 9:
        v += 0.3                                 # crown catches the key light
    if abs(xr - DEV_CX) <= 1 and ry >= _HOOD_TOP + 8:
        v -= 0.5                                 # soft centre-back fold seam
    if rel < -0.4 and x - x0 <= 3:
        v += 1.2 * (1 - (x - x0) / 4.0)          # rim light on the lit edge
    if ry >= _HOOD_BOT - 5:
        v -= 0.9 * min(1.0, (ry - (_HOOD_BOT - 6)) / 6.0)   # nape AO, hood->back
    if _arm_shadow(arms, x, y):
        v -= 0.8
    return v


def _back_v(x: int, y: int, sdy: int, span, arms, sdx: int = 0) -> float:
    x0, x1 = span
    half = max(1.0, (x1 - x0) / 2.0)
    xr = x - sdx                                 # see _hood_v: lean, not relight
    rel = (xr - DEV_CX) / half
    ry = y - sdy
    v = 3.0 - 0.8 * rel
    if ry <= _BACK_TOP + 2:
        v -= 0.7 * (0.3 + max(0.0, (_BACK_TOP + 2 - ry) / 3.0))   # hood-drape/neck AO
    if abs(xr - DEV_CX) <= 1 and ry >= _BACK_TOP + 3:
        v -= 0.35                                # centre-back seam
    for bx in (DEV_CX - 16, DEV_CX + 15):        # shoulder-blade patches
        if abs(xr - bx) <= 8 and _BACK_TOP + 7 <= ry <= _BACK_TOP + 20:
            d = ((xr - bx) ** 2 + (ry - (_BACK_TOP + 13)) ** 2) ** 0.5
            if d <= 9:
                v -= 0.4 * (1 - d / 9.0)
    if x - x0 <= 3:
        v += 0.7 * (1 - (x - x0) / 4.0)          # rim light on the lit edge
    if _arm_shadow(arms, x, y):
        v -= 0.7
    return v


def build_dev_form(frame: str) -> Sprite:
    s = Sprite(DEV_W, DEV_H, palette=RAMP)
    ddy, sdy = DOME_DY[frame], BACK_DY[frame]
    bdx = BODY_DX[frame]
    arms = _arm_grid(frame)
    hood = {}
    back = {}
    for y in range(DEV_H):
        hs = _hood_span(y, ddy, bdx)
        if hs:
            hood[y] = hs
        bs = _back_span(y, sdy, bdx)
        if bs:
            back[y] = bs
    for y in range(DEV_H):
        for x in range(DEV_W):
            if arms[y][x]:
                v = _arm_v(arms, x, y)
            elif y in hood and hood[y][0] <= x <= hood[y][1]:
                v = _hood_v(x, y, ddy, hood[y], arms, bdx)
            elif y in back and back[y][0] <= x <= back[y][1]:
                v = _back_v(x, y, sdy, back[y], arms, bdx)
            else:
                continue
            s.dot(x, y, ramp_dither(x, y, v))
    chair_rim_light(s, rim=2, boost=1.3)         # lit-edge rim (shared helper)
    return s


# --------------------------------------------------------------------------
# dev_base (skin hands + dark outline + hood detail + sleep cue)
# --------------------------------------------------------------------------

# The hood's two construction lines, LEFT half only (the right half is
# mirrored about DEV_CX - 0.5, like every other piece of dev geometry). Both
# are rescaled every time the hood's width is (the HOOD-NARROWING PASS took
# the seam in by the same 20/22 the body half-width moved, which keeps it
# 5..7px inside the silhouette - close enough to read as a panel seam,
# far enough not to read as a second outline). The hem is GENERATED as a
# quarter-ellipse arc off _HOOD_HEM_HW rather than hand-listed: it has to sit
# exactly one pixel inside a silhouette that curves, and a typed list of 24
# points went stale the moment the hood profile moved.
_HOOD_SEAM_L = ((-11, 23), (-12, 25), (-12, 27), (-13, 29), (-13, 31),
                (-14, 33), (-14, 35), (-15, 37), (-15, 39), (-15, 41),
                (-16, 43))
_HEM_SIDE_ROW, _HEM_CENTRE_ROW = 43, 48     # hem is highest at the sides
_HOOD_HEM_L = tuple(
    (-dx, _HEM_SIDE_ROW + int(round((_HEM_CENTRE_ROW - _HEM_SIDE_ROW)
                                    * (1.0 - (dx / _HOOD_HEM_HW) ** 2) ** 0.5)))
    for dx in range(_HOOD_HEM_HW, 0, -1))


def _dev_hood_detail(s: Sprite, frame: str) -> None:
    """The hood's own construction lines, in dev_base so EVERY hoodie style
    gets them and they follow the frame's hood offset:

      * two symmetric panel seams running from the crown down the sides;
      * the hood HEM - an arc where the fabric drapes onto the shoulders,
        which is what separates head from body.

    Both are deliberately OFF-CENTRE and vertical-ish. Earlier versions put a
    small horizontal crescent (plus a peek of `hair`) near the crown apex to
    suggest looking into the hood, and at this size a horizontal dark mark
    near the top of a dome reads as a MOUTH, not as a hood opening. The
    centre column is also reserved: hoodie_zip's teeth run up it, and
    dev_base draws ABOVE the style overlay, so anything painted there would
    erase the zip."""
    ddy, ddx = DOME_DY[frame], BODY_DX[frame]
    for mx, my in _HOOD_SEAM_L + _HOOD_HEM_L:
        s.dot(DEV_CX + mx + ddx, my + ddy, "shadow")
        s.dot(DEV_CX - 1 - mx + ddx, my + ddy, "shadow")


def _dev_paint_hand(s: Sprite, x0: int, x1: int, y0: int, y1: int) -> None:
    """The back of a hand seen from above-behind, resting on the keys: four
    finger runs pointing AWAY from us (top rows, split by thin `shadow`
    notches), a solid back-of-hand below, a `cream` lit knuckle edge on the
    upper-left and a `shadow` edge down the lower-right - the same one
    upper-left key light as every other sprite in the scene.

    Every mark is DERIVED from the rect, which is what let the hand-narrowing
    pass rescale a hand instead of cropping one: the three notches sit at
    (w*k)//4, so at DEV_HAND_W = 17 the four runs are 4/3/3/4 px (the widest
    two outboard, like the 5/4/4/5 of the 21px hand - an index and a little
    finger are the wide ones), the shadow side rides x1 and the lit knuckle
    edge rides x0."""
    s.rect(x0, y0, x1, y1, "skin")
    w = x1 - x0 + 1
    for k in (1, 2, 3):
        gx = x0 + (w * k) // 4
        for gy in range(y0, min(y0 + 4, y1) + 1):
            s.dot(gx, gy, "shadow")
    s.vline(x1, y0 + 4, y1, "shadow")            # shadow side
    for gy in range(y0 + 4, min(y0 + 6, y1) + 1):
        s.dot(x0, gy, "cream")                   # lit knuckle edge


def build_dev_base(frame: str) -> Sprite:
    s = Sprite(DEV_W, DEV_H)
    outline_from_mask(s, _dev_full_mask(frame), "shadow")
    _dev_hood_detail(s, frame)
    for x0, x1, y0, y1 in _dev_hand_rects(frame):
        _dev_paint_hand(s, x0, x1, y0, y1)
    if frame == "sleep":
        # A `z` floating up and to the right of the slumped head: two bars
        # and a real diagonal (not a symmetric bar-dot-bar, which reads as an
        # "I" at this size). Moved out to local x146..158 = room x210..222
        # with the proportion pass: that is the bare strip of desk between the
        # keyboard (ends room x207) and the mouse (starts x224), so the cue no
        # longer sits on top of a purchasable keyboard's keys.
        s.hline(9, 146, 158, "cream")
        s.line(157, 10, 147, 17, "cream")
        s.hline(18, 146, 158, "cream")
    return s


# --------------------------------------------------------------------------
# Hoodie style overlays (4): frame-independent, hood + upper back only
# --------------------------------------------------------------------------
#
# Per art-direction "Character rules": overlays may paint only the hood and
# back panel, which are static across the four non-sleep frames, so they are
# authored once against the `idle` geometry (hood rows 18..48, x75..116 at its
# widest; back rows 44..74, x58..133 at the shoulders; centre seam x95..96)
# and never reference the per-frame hand offsets. The sleep frame's 2-3px drop
# of that geometry is a deliberate, documented simplification.
#
# The CHAIR draws on top of the developer from room y150 (local row 58) down,
# so every identifying mark is kept at local row <= 57 - i.e. on the hood and
# the exposed upper back, the part of the garment the player can actually
# see. Every mark below is rescaled whenever the garment is: a mark authored
# for a 34px hood reads as a scratch on a 48px one, and a mark authored for a
# 46px hood floats off the edge of a 42px one. assert_hoodie_on_fabric is the
# check that catches the second case.

def build_hoodie_classic() -> Sprite:
    """Two drawstrings hanging out from under the hood hem down the
    centre-back, each ending in a small knot, plus the yoke seam across the
    upper back - the classic pullover read from behind. (The hood hem arc
    itself is shared: dev_base draws it for every style.)"""
    s = Sprite(DEV_W, DEV_H)
    # The cords hang OUT OF THE HEM and down the exposed upper back, which is
    # both where a drawstring actually is and the only place there is now room
    # for one: run up the hood instead (as they were before the shoulders grew
    # a readable band) they crossed the hem arc and the pair read as a cross.
    s.vline(DEV_CX - 5, 48, 55, "shadow")
    s.rect(DEV_CX - 6, 56, DEV_CX - 4, 57, "shadow")    # knot
    s.vline(DEV_CX + 4, 48, 53, "shadow")
    s.rect(DEV_CX + 3, 54, DEV_CX + 5, 55, "shadow")    # knot
    # The yoke seam picks up where the hood hem arc stops and runs OUT to the
    # shoulder tips, so hem + seam read as one shoulder yoke. Placed inboard
    # of the hem it just made a second horizontal bar under the first.
    s.hline(49, DEV_CX - 33, DEV_CX - 18, "shadow")     # yoke seam, left
    s.hline(49, DEV_CX + 17, DEV_CX + 32, "shadow")     # yoke seam, right
    return s


def build_hoodie_zip() -> Sprite:
    """Metal zip teeth up the hood's centre-back seam, a cream pull tab at
    the hem."""
    s = Sprite(DEV_W, DEV_H)
    for y in range(23, 50, 2):
        s.dot(DEV_CX - 1, y, "metal")
        s.dot(DEV_CX, y, "metal")
    s.rect(DEV_CX - 2, 47, DEV_CX + 1, 51, "cream")     # pull tab
    return s


def build_hoodie_tech() -> Sprite:
    """Screen-cyan reflective piping down the hood's lit edge, a desk_dark
    strap across the shoulders with a metal buckle, and one reflective stripe
    on the upper back. The piping follows the SILHOUETTE rather than crossing
    the crown: a bright horizontal line near the top of a dome reads as a
    visor, which is not what a hoodie is."""
    s = Sprite(DEV_W, DEV_H)
    for y in range(22, 43):                             # reflective edge piping
        hw = _HOOD_HW[y]
        s.dot(DEV_CX - hw, y, "screen")
        s.dot(DEV_CX - hw + 1, y, "screen") if y % 3 == 0 else None
    s.line(DEV_CX - 30, 53, DEV_CX + 29, 57, "desk_dark")
    s.line(DEV_CX - 30, 54, DEV_CX + 29, 58, "desk_dark")
    s.rect(DEV_CX - 6, 53, DEV_CX + 5, 57, "metal")     # buckle
    # Row 50, on the bare upper back: at the hood hem (row 48) dev_base's own
    # hem arc draws OVER the overlay and chopped this stripe into two tabs.
    s.hline(50, DEV_CX - 27, DEV_CX + 26, "screen")     # reflective stripe
    return s


def build_hoodie_cloak() -> Sprite:
    """Gold hem trim across the upper back and a draped fold pattern down the
    hood - the long-hemmed 'cloak' silhouette."""
    s = Sprite(DEV_W, DEV_H)
    # The folds hang DOWN the hood, inside its silhouette (five lines fanned
    # out directly below the middle of a dome read as whiskers), and the gold
    # trim crosses the now-visible upper back below the hem.
    for x in (DEV_CX - 14, DEV_CX + 13):
        s.vline(x, 30, 47, "shadow")                    # long drape folds
    for x in (DEV_CX - 7, DEV_CX + 6):
        s.vline(x, 36, 47, "shadow")                    # short drape folds
    s.hline(51, DEV_CX - 29, DEV_CX + 28, "gold")       # gold hem trim
    for x in (DEV_CX - 25, DEV_CX - 12, DEV_CX + 11, DEV_CX + 24):
        s.vline(x, 52, 57, "shadow")                    # skirt folds below it
    return s


# --------------------------------------------------------------------------
# Chair (8): 4 styles x form/detail, bottom-centre anchored at room (160,200)
# --------------------------------------------------------------------------
#
# BEHIND-VIEW COMPOSITING (the structural half of the perspective fix). From
# behind a seated person, the chair's BACKREST is between the camera and
# their torso. So the chair now composites ON TOP of the developer
# (geometry.ts: CHAIR_Z_FORM/DETAIL 13/14 > DEV_Z_FORM/STYLE/BASE 10/11/12),
# the exact opposite of the old top-down order, and the developer sprite is
# authored so that only what genuinely rises above the backrest - the back of
# the hood, the shoulders/upper back, and the arms reaching forward - lives
# above room row 150.
#
# TWO HARD REGIONS, both asserted (assert_chair_region):
#
#   1. The KEYBOARD (room x[112,208], y[90,113]) draws on a LOWER z-layer
#      than the chair, so no chair pixel may fall in x[112,208] above room
#      y116 or the chair would paint over a purchasable item.
#   2. No chair pixel above room y150 (CHAIR_TOP_ROOM_Y) at all. That is the
#      developer's shoulder line: everything of the figure that is NEARER the
#      camera than the chair (hood, arms, hands) is above it, everything the
#      chair should occlude (mid/lower back) is below it. Honouring one
#      horizontal line is what lets a plain Z-order swap composite correctly
#      with no per-layer masking, no split behind/front chair layers, and no
#      coupling between the frame-independent chair sprites and the
#      per-frame developer geometry.
#
# What the player actually SEES of the chair, and therefore where all four
# styles have to earn their identity: the SPRINT/STATUS panels cover the
# scene below room y161, so the visible chair is the band room y150..160 -
# the backrest crown + its top band, and the armrests at the sides. The seat
# pan, gas cylinder and star base below that are still drawn (they show in
# the store's composed preview, which renders the full sprite) but they carry
# no weight in the main scene. Per style, in the visible band:
#
#   basic     narrowest back (hw 25) + an aluminium frame rail around it and
#             a sparse mesh weave -> an ergonomic mesh task chair.
#   racer     wide back (hw 27) with raised, cream-stitched side bolsters and
#             a seam cutting off a headrest band -> a gaming bucket seat.
#   exec      broadest back (hw 28), gold button tufting in a diamond grid,
#             the widest and thickest armrest pads -> padded executive.
#   antigrav  a strongly domed shell (hw 13 at the crown, 24 at the body), no
#             armrests, no base, screen/lamp glow -> a floating pod.
#
# SLIMMING PASS: the owner's other complaint was that the chairs were too
# wide, and the fix is NOT proportional - as the shoulders slim, the furniture
# has to slim MORE or the backrest swallows them. Backrest half-widths
# 31..39 -> 24..28 (0.63..0.74 of the new shoulder hw 38, down from
# 0.67..0.85 of the old 46) and the armrest pads came in with them, which is
# the measurement that actually settles it: the whole furniture footprint is
# now 78/82/86px (65px for the pod) against a figure whose own widest span is
# 90px, so the chair sits INSIDE the person instead of straddling them. It
# was 105/110/119px against 100px before - literally wider than its occupant,
# which is what "too wide" was describing. Per-style identity is unchanged
# (mesh rail, stitched bolsters, gold tufting, glowing pod), and the hood -
# 31 rows tall, ending at room y140 - still clearly clears every crown.
#
# Everything is authored in ROOM coordinates and converted to the sprite's
# LOCAL frame via _chair_axes: local x = room x - (160 - w//2), local y =
# room y - (200 - h). Dev landmarks used for proportioning: shoulders room
# x114..205, hood hem room y140, shoulder line room y150.

CHAIR_TOP_ROOM_Y = 150        # no chair pixel may sit above this room row


def chair_forbidden_zone(w: int, h: int) -> tuple[int, int, int] | None:
    """Return (y_below, x_lo, x_hi) - the local (y, x-range) that must stay
    fully transparent so the chair never paints over the keyboard - or None
    if the style is short enough that its whole canvas already sits below
    room row 116."""
    y_below = h - 84
    if y_below <= 0:
        return None                # h <= 84: the canvas starts below room 116
    return y_below, w // 2 - 48, w // 2 + 48


def assert_chair_region(name: str, s: Sprite) -> str:
    """Both chair hard regions (see the section comment): the keyboard band,
    and the room y150 shoulder line above which the chair may not paint
    because the developer is composited BELOW it there.

    ONE row of tolerance for a `_detail` layer: outline_from_mask gives every
    chair its own 1px `shadow` silhouette halo, so the detail layer legally
    reaches room y149 - a single dark line landing on the developer's
    shoulders, which is precisely the contact edge that separates the chair
    back from the body in front of it. The tinted `_form` body itself gets no
    tolerance at all."""
    notes = []
    mask = s.mask()
    zone = chair_forbidden_zone(s.w, s.h)
    if zone is None:
        notes.append("keyboard band n/a (canvas starts below room row 116)")
    else:
        y_below, x_lo, x_hi = zone
        x_lo, x_hi = max(0, x_lo), min(s.w - 1, x_hi)
        bad = [(x, y) for y in range(0, y_below) for x in range(x_lo, x_hi + 1)
               if mask[y][x]]
        if bad:
            raise AssertionError(
                f"{name}: {len(bad)} pixel(s) violate the keyboard band "
                f"(local y<{y_below}, x in [{x_lo},{x_hi}]); first={bad[0]}")
        notes.append(f"keyboard band clear (local y<{y_below})")

    oy = 200 - s.h
    limit = CHAIR_TOP_ROOM_Y - (1 if name.endswith("_detail.png") else 0)
    top_local = limit - oy                       # first LEGAL local row
    bad = [(x, y) for y in range(0, max(0, min(top_local, s.h)))
           for x in range(s.w) if mask[y][x]]
    if bad:
        raise AssertionError(
            f"{name}: {len(bad)} pixel(s) rise above the developer's shoulder "
            f"line (room y<{limit}, local y<{top_local}); "
            f"first={bad[0]} (room {(bad[0][0] + 160 - s.w // 2, bad[0][1] + oy)})")
    notes.append(f"nothing above room y{limit}")
    return f"{name}: " + "; ".join(notes) + " - OK"


def _chair_axes(w: int, h: int) -> tuple[int, int, float, float]:
    """Return (ox, oy, cx, half): the room->local origin (local x = room x -
    ox, local y = room y - oy), the local x of the dev centre (room x=160),
    and the half-span used by the dithered cross-section shaders."""
    ox = 160 - w // 2
    oy = 200 - h
    return ox, oy, float(160 - ox), float(w // 2) or 1.0


def _shade_room_rect(s: "Sprite", ox: int, oy: int, cx: float, half: float,
                     rx0: int, ry0: int, rx1: int, ry1: int,
                     extra: float = 0.0) -> None:
    """chair_shade_rect, but taking ROOM coordinates (converted to local)."""
    chair_shade_rect(s, cx, half, rx0 - ox, ry0 - oy, rx1 - ox, ry1 - oy, extra=extra)


def _chair_back_panel(s: "Sprite", ox: int, oy: int, cx: float, half: float,
                      top: int, bottom: int, hw_body: int, hw_top: int,
                      round_rows: int, extra: float = 0.0,
                      curve: bool = False) -> None:
    """A rounded-top upholstered backrest, centred on the developer's own
    centreline (room x159.5, so the panel is 2*hw px wide - NOT centred on
    x160, which would leave the whole chair 1px right of the figure). The top
    `round_rows` rows narrow from hw_body to hw_top so the seatback CROWN is a
    soft curve (a chair back), never a flat slab edge (a wall). Same
    ramp4-anchored dithered cross-section + one upper-left key light as every
    chair fill, so it tints with headroom."""
    for ry in range(top, bottom + 1):
        hw = _chair_panel_hw(ry - top, hw_body, hw_top, round_rows, curve)
        for rx in range(160 - hw, 160 + hw):
            lx, ly = rx - ox, ry - oy
            rel = max(-1.0, min(1.0, (lx - cx) / half))
            s.dot(lx, ly, ramp_dither(lx, ly, 3.3 - 0.7 * rel - extra))


def _chair_panel_hw(d: int, hw_body: int, hw_top: int, round_rows: int,
                    curve: bool = False) -> int:
    """The exact half-width _chair_back_panel paints at offset `d` from its
    top row - shared so a detail layer can trace the crown it actually drew
    instead of a hand-copied approximation that goes stale. `curve` swaps the
    linear ramp (a straight 45-degree chamfer) for a quarter-ellipse, i.e. a
    genuinely DOMED top - what the antigrav pod shell needs."""
    if round_rows and d < round_rows:
        f = d / float(round_rows)
        if curve:
            f = (1.0 - (1.0 - f) ** 2) ** 0.5
        return hw_top + int(round((hw_body - hw_top) * f))
    return hw_body


def _chair_crown_outline(top: int, hw_body: int, hw_top: int,
                         round_rows: int, side_bot: int, curve: bool = False):
    """ROOM-coordinate pixels along a backrest's crown and both side edges:
    the whole top row, the rounded shoulders of the crown, then the two
    vertical side edges down to `side_bot`.

    This is THE legibility fix for dark tints. The chair's `_form` is
    multiplied by the tint, and the default chair tint (`slate`, #2b2b33) is
    near-black, so NO ramp step survives it as a readable edge - the crown,
    the one line that says "there is a chair back behind them", disappears.
    Every chair therefore strokes this outline in its own PALETTE (untinted)
    detail colour: aluminium for the mesh chair, cream piping for the racer,
    gold for the executive, glow for the pod. Identity and legibility from
    the same stroke."""
    pts = []
    prev = None
    for d in range(max(round_rows, 1)):
        ry = top + d
        hw = _chair_panel_hw(d, hw_body, hw_top, round_rows, curve)
        if prev is None:
            for rx in range(160 - hw, 160 + hw):
                pts.append((rx, ry))
        else:
            for rx in range(160 - hw, 160 - prev + 1):
                pts.append((rx, ry))
            for rx in range(159 + prev, 160 + hw):
                pts.append((rx, ry))
        prev = hw
    for ry in range(top + max(round_rows, 1), side_bot + 1):
        pts.append((160 - hw_body, ry))
        pts.append((159 + hw_body, ry))
    return pts


def _stroke_outline(s: "Sprite", ox: int, oy: int, pts, colour: str,
                    dither: str | None = None, ratio: float = 0.55) -> None:
    for rx, ry in pts:
        lx, ly = rx - ox, ry - oy
        s.dot(lx, ly, colour if dither is None
              else bayer_mix(lx, ly, ratio, dither, colour))


def _chair_crown_rim(s: "Sprite", ox: int, oy: int, hw_top: int, top: int) -> None:
    """Bright lit edge along the backrest crown - the chair's single most
    load-bearing line, since it is the silhouette the player reads as "there
    is a chair back behind them" and it must stay legible against the desk at
    the darkest tint."""
    chair_back_top_rim(s, (160 - hw_top) - ox, (159 + hw_top) - ox, top - oy)


def _chair_armrests(s: "Sprite", ox: int, oy: int, cx: float, half: float,
                    pad_x0: int, pad_x1: int, pad_y0: int, pad_y1: int,
                    post_inset: int, post_bot: int) -> None:
    """A pad each side at elbow level plus a thin support post down to the
    seat, mirrored about room x159.5 (the developer's centreline). With the
    chair's seat and base hidden behind the HUD panels, these pads are half
    of what makes the thing read as a chair rather than a slab, so they reach
    a little outside the shoulders - and their INNER end overlaps the backrest's
    side edge, so each pad reads as growing out of the chair instead of
    floating beside it as a separate rectangle.

    The pad's top row is a hard AO seam and the row under it a highlight: at
    the proportion pass's bigger scale the pad and the backrest above it are
    the same tinted panel for eleven rows, and without that 2px lip the whole
    thing read as one widening slab (a lectern) rather than a back with arms."""
    for (a0, a1) in ((pad_x0, pad_x1), (319 - pad_x1, 319 - pad_x0)):
        _shade_room_rect(s, ox, oy, cx, half, a0, pad_y0, a1, pad_y1)
        _shade_room_rect(s, ox, oy, cx, half, a0, pad_y0, a1, pad_y0, extra=1.0)
        _shade_room_rect(s, ox, oy, cx, half, a0, pad_y0 + 1, a1, pad_y0 + 1,
                         extra=-0.5)
        p0, p1 = a0 + post_inset, a1 - post_inset
        _shade_room_rect(s, ox, oy, cx, half, p0, pad_y1 + 1, p1, post_bot, extra=0.3)


def _chair_seat(s: "Sprite", ox: int, oy: int, cx: float, half: float,
                sx0: int, sx1: int, sy0: int, sy1: int) -> None:
    """The seat pan tucked under the backrest. One dithered AO seam along its
    top so it reads as a seat under a back, not one flat panel."""
    _shade_room_rect(s, ox, oy, cx, half, sx0, sy0, sx1, sy1)
    _shade_room_rect(s, ox, oy, cx, half, sx0, sy0, sx1, sy0, extra=0.9)


def _chair_lumbar_seam(s: "Sprite", ox: int, oy: int, cx: float, half: float,
                       hw: int, y: int) -> None:
    """A 2px dithered AO seam across the backrest. Cheap and load-bearing: it
    is what stops a 17-row-tall backrest band reading as a flat wooden box and
    starts it reading as an upholstered panel with a lumbar section."""
    _shade_room_rect(s, ox, oy, cx, half, 162 - hw, y, 157 + hw, y + 1, extra=0.9)


def _chair_star_base(s: "Sprite", cx: int, hub_y: int, foot_y: int, spread: int) -> None:
    """A 5-star caster base: a hub under the gas cylinder, 5 splayed spokes to
    caster feet, and a floor contact shadow."""
    feet = [cx - spread, cx - spread // 2, cx, cx + spread // 2, cx + spread]
    s.rect(cx - 3, hub_y - 1, cx + 3, hub_y + 1, "metal")            # central hub
    for fx in feet:
        s.line(cx, hub_y, fx, foot_y - 1, "metal")                  # thin splayed spoke
        s.rect(fx - 2, foot_y - 1, fx + 2, foot_y, "metal")         # caster foot
        s.hline(foot_y + 1, fx - 2, fx + 2, "shadow")               # wheel/ground
    s.hline(foot_y + 2, feet[0] - 3, feet[-1] + 3, "shadow")        # floor contact shadow


# --- basic: ergonomic mesh task chair -------------------------------------

def build_chair_basic_form() -> Sprite:
    """Ergonomic mesh task chair, behind-view: the narrowest of the four
    backs (only a little wider than the shoulders, so they peek out at both
    sides), a seat pan tucked beneath, slim armrests, over a gas cylinder +
    star base in the detail layer."""
    w, h = 80, 51
    s = Sprite(w, h, palette=RAMP)
    ox, oy, cx, half = _chair_axes(w, h)
    _chair_back_panel(s, ox, oy, cx, half, top=150, bottom=182,
                      hw_body=25, hw_top=22, round_rows=6)
    _chair_crown_rim(s, ox, oy, 23, 150)
    _chair_lumbar_seam(s, ox, oy, cx, half, 25, 159)
    _chair_armrests(s, ox, oy, cx, half, 122, 139, 155, 161,
                    post_inset=6, post_bot=174)
    _chair_seat(s, ox, oy, cx, half, 124, 195, 174, 186)
    # Sparse mesh punctures across the back panel, one ramp step darker.
    for ry in range(156, 182, 5):
        for rx in range(139, 182, 6):
            lx, ly = rx - ox, ry - oy
            rel = max(-1.0, min(1.0, (lx - cx) / half))
            s.dot(lx, ly, ramp_dither(lx, ly, 3.0 - 0.8 * rel - 0.7))
    chair_rim_light(s)
    return s


def build_chair_basic_detail() -> Sprite:
    w, h = 80, 51
    s = Sprite(w, h)
    ox, oy = 160 - w // 2, 200 - h
    bevel_rect(s, 156 - ox, 185 - oy, 164 - ox, 193 - oy, "metal", "wall_light", "shadow")
    _chair_star_base(s, 160 - ox, 190 - oy, 197 - oy, 34)
    # The slate default tint crushes the tinted mesh near-black, so the
    # detail layer carries the light aluminium FRAME around the backrest
    # (crown rail + both side rails) plus a sparse weave: this is what keeps
    # the basic chair reading as a MESH office chair at its darkest tint, and
    # it sits squarely in the visible band (room y150..160).
    # `wall_light` alone is #3d3550 - all but invisible against the slate
    # default tint, which is #2b2b33. The frame therefore reads as CREAM
    # dithered into wall_light: brushed aluminium that survives every tint.
    _stroke_outline(s, ox, oy, _chair_crown_outline(150, 25, 22, 6, 180),
                    "cream", dither="wall_light", ratio=0.5)
    for ry in range(157, 180, 5):
        for rx in range(140, 181, 7):
            s.dot(rx - ox, ry - oy,
                  bayer_mix(rx - ox, ry - oy, 0.5, "wall_light", "cream"))
    fab = build_chair_basic_form()
    outline_from_mask(s, union_mask(fab.mask(), s.mask()), "shadow")
    return s


# --- racer: gaming bucket chair -------------------------------------------

def build_chair_racer_form() -> Sprite:
    """Gaming bucket seat, behind-view: a wide shaped back with the shoulder
    bolsters INTEGRATED as raised side ridges (not detached towers), a seam
    cutting off the headrest band at the top, blocky armrests."""
    w, h = 84, 51
    s = Sprite(w, h, palette=RAMP)
    ox, oy, cx, half = _chair_axes(w, h)
    _chair_back_panel(s, ox, oy, cx, half, top=150, bottom=184,
                      hw_body=27, hw_top=24, round_rows=6)
    # Bolster ridges: brighten the two side bands so the edges read as a
    # bucket seat's raised bolsters cradling the torso.
    for ry in range(157, 185):
        for (b0, b1) in ((133, 140), (179, 186)):
            for rx in range(b0, b1 + 1):
                lx, ly = rx - ox, ry - oy
                rel = max(-1.0, min(1.0, (lx - cx) / half))
                s.dot(lx, ly, ramp_dither(lx, ly, min(4.0, 3.0 - 0.8 * rel + 0.7)))
    # Headrest seam: an AO band across the back, the racing-seat tell that
    # the top section is a separate padded headrest.
    _chair_lumbar_seam(s, ox, oy, cx, half, 27, 158)
    _chair_crown_rim(s, ox, oy, 25, 150)
    _chair_armrests(s, ox, oy, cx, half, 120, 138, 155, 161,
                    post_inset=6, post_bot=176)
    _chair_seat(s, ox, oy, cx, half, 122, 197, 176, 188)
    chair_rim_light(s)
    return s


def build_chair_racer_detail() -> Sprite:
    w, h = 84, 51
    s = Sprite(w, h)
    ox, oy = 160 - w // 2, 200 - h
    bevel_rect(s, 156 - ox, 186 - oy, 164 - ox, 194 - oy, "metal", "wall_light", "shadow")
    _chair_star_base(s, 160 - ox, 191 - oy, 197 - oy, 36)
    # Double stitching up each bolster (racing-seat tell), inside the band
    # the HUD panels leave visible.
    _stroke_outline(s, ox, oy, _chair_crown_outline(150, 27, 24, 6, 182),
                    "cream", dither="shadow", ratio=0.6)   # contrast piping
    for rx in (134, 139, 180, 185):
        for ry in range(157, 181, 3):
            s.dot(rx - ox, ry - oy, "cream")               # bolster stitching
    fab = build_chair_racer_form()
    outline_from_mask(s, union_mask(fab.mask(), s.mask()), "shadow")
    return s


# --- exec: padded executive chair -----------------------------------------

def build_chair_exec_form() -> Sprite:
    """Executive chair, behind-view: the broadest and most softly rounded
    padded back of the four, the widest and thickest armrest pads, a broad
    seat, over a heavier gas cylinder + base."""
    w, h = 88, 51
    s = Sprite(w, h, palette=RAMP)
    ox, oy, cx, half = _chair_axes(w, h)
    _chair_back_panel(s, ox, oy, cx, half, top=150, bottom=186,
                      hw_body=28, hw_top=25, round_rows=6)
    _chair_crown_rim(s, ox, oy, 26, 150)
    _chair_lumbar_seam(s, ox, oy, cx, half, 28, 159)
    _chair_armrests(s, ox, oy, cx, half, 118, 139, 155, 162,
                    post_inset=8, post_bot=178)
    _chair_seat(s, ox, oy, cx, half, 120, 199, 178, 190)
    chair_rim_light(s)
    return s


def build_chair_exec_detail() -> Sprite:
    w, h = 88, 51
    s = Sprite(w, h)
    ox, oy = 160 - w // 2, 200 - h
    # Button tufting on the padded back: a diamond grid of gold buttons,
    # placed in the band the HUD panels leave visible (room y150..160).
    _stroke_outline(s, ox, oy, _chair_crown_outline(150, 28, 25, 6, 184),
                    "gold", dither="shadow", ratio=0.5)     # gold hide piping
    for (bx, by) in ((139, 155), (149, 155), (160, 155), (170, 155), (180, 155),
                     (134, 160), (144, 160), (155, 160), (165, 160), (175, 160),
                     (185, 160)):
        s.dot(bx - ox, by - oy, "gold")                     # button tufting
    bevel_rect(s, 155 - ox, 188 - oy, 165 - ox, 194 - oy, "metal", "wall_light", "shadow")
    _chair_star_base(s, 160 - ox, 191 - oy, 197 - oy, 38)
    fab = build_chair_exec_form()
    outline_from_mask(s, union_mask(fab.mask(), s.mask()), "shadow")
    return s


# --- antigrav: floating pod chair -----------------------------------------

def build_chair_antigrav_form() -> Sprite:
    """Anti-gravity pod, behind-view: a strongly domed shell (half-width 13
    at the crown widening to 26) cradling the torso with a rounded seat pod
    below - unmistakably a seat the developer sits IN, not a slab behind
    them. No legs or base (it floats); the levitation glow is in the detail
    layer."""
    w, h = 68, 51
    s = Sprite(w, h, palette=RAMP)
    ox, oy, cx, half = _chair_axes(w, h)
    _chair_back_panel(s, ox, oy, cx, half, top=150, bottom=184,
                      hw_body=24, hw_top=13, round_rows=13, curve=True)
    _chair_crown_rim(s, ox, oy, 13, 150)
    chair_shade_ellipse(s, 160 - ox, 183 - oy, 31, 9, cx, half, extra=0.3)
    chair_rim_light(s)
    return s


def build_chair_antigrav_detail() -> Sprite:
    w, h = 68, 51
    s = Sprite(w, h)
    ox, oy = 160 - w // 2, 200 - h
    # Levitation glow ring beneath the pod (where a base would be) + drifting
    # float motes + a floor-facing glow shadow: the 'floating' tell.
    s.outline(135 - ox, 193 - oy, 184 - ox, 196 - oy, "screen")
    for rx in range(136, 184):
        s.dot(rx - ox, 194 - oy, bayer_mix(rx - ox, 194 - oy, 0.4, "screen", "lamp"))
    s.dots([(130 - ox, 196 - oy), (160 - ox, 198 - oy), (189 - ox, 196 - oy)], "lamp")
    s.hline(198 - oy, 141 - ox, 178 - ox, "shadow")
    # Glow along the visible shell crown so the pod reads as energised - and
    # this is the part the HUD panels never cover.
    # The pod's whole visible edge is energised, plus an energy seam and two
    # float motes INSIDE the band the HUD panels leave visible - the glow ring
    # under the pod is real but sits below room y160 where nothing sees it.
    _stroke_outline(s, ox, oy,
                    _chair_crown_outline(150, 24, 13, 13, 182, curve=True),
                    "screen", dither="shadow", ratio=0.65)
    for rx in range(142, 178, 2):
        s.dot(rx - ox, 159 - oy, bayer_mix(rx - ox, 159 - oy, 0.5, "shadow", "screen"))
    s.dots([(133 - ox, 154 - oy), (186 - ox, 156 - oy),
            (131 - ox, 160 - oy), (188 - ox, 158 - oy)], "lamp")
    fab = build_chair_antigrav_form()
    outline_from_mask(s, union_mask(fab.mask(), s.mask()), "shadow")
    return s


# --------------------------------------------------------------------------
# Keyboard (4): 96x24 at (112, 90); desk-surface baseline = room row 114
# --------------------------------------------------------------------------

def build_kb_membrane() -> Sprite:
    """HERO-FIDELITY MATCH: the case is a real bevel (dithered highlight top
    edge, dithered AO where the case meets the desk) instead of one flat
    metal rect plus a single flat highlight line, and each printed key
    dimple gets a paired highlight so it reads as a shallow dish, not a
    dot."""
    s = Sprite(96, 24)
    bevel_rect(s, 2, 2, 93, 18, "metal", "wall_light", "shadow", ratio=0.6)
    for ky in (5, 9, 13, 17):
        for kx in range(5, 91, 4):
            s.dot(kx, ky, "desk_dark")           # dimple shadow
            s.dot(kx + 1, ky, "wall_light")      # dimple's lit far edge
    bevel_rect(s, 2, 19, 93, 20, "desk_dark", "floor_light", "shadow", ratio=0.5)
    s.hline(23, 0, 95, "shadow")
    return s


def build_kb_mech() -> Sprite:
    """HERO-FIDELITY MATCH: each keycap is its own small bevel (light
    top/left, dark bottom/right) instead of a flat metal square, so the
    row reads as individual raised caps over the dark plate, not a strip
    of dots."""
    s = Sprite(96, 24)
    s.rect(2, 1, 93, 19, "shadow")          # visible plate, darker than the caps
    for x in range(2, 94):
        s.dot(x, 1, bayer_mix(x, 1, 0.4, "shadow", "wall_light"))   # plate's own top AO/light seam
    for ky in (4, 9, 14):
        for kx in range(5, 91, 4):
            bevel_rect(s, kx, ky, kx + 2, ky + 2, "metal", "wall_light", "shadow", ratio=0.65)
            s.dot(kx + 1, ky + 1, "cream")  # legend pixel
    s.hline(23, 0, 95, "shadow")
    return s


def build_kb_split() -> Sprite:
    """HERO-FIDELITY MATCH: each of the 4 case blocks is beveled instead of
    flat metal, and the key-legend dots get a paired shadow pixel so the
    keywell reads with real depth."""
    s = Sprite(96, 24)
    bevel_rect(s, 2, 4, 20, 19, "metal", "wall_light", "shadow")    # left outer, lower
    bevel_rect(s, 21, 2, 42, 17, "metal", "wall_light", "shadow")   # left inner, tented 2px higher
    bevel_rect(s, 53, 2, 74, 17, "metal", "wall_light", "shadow")   # right inner
    bevel_rect(s, 75, 4, 93, 19, "metal", "wall_light", "shadow")   # right outer
    for ky in (7, 11):
        for kx in (5, 9, 13, 17, 24, 28, 32, 36, 57, 61, 65, 69, 78, 82, 86, 90):
            s.dot(kx, ky, "wall_light")
            s.dot(kx, ky + 1, "shadow")     # paired shadow, gives the legend a lip
    s.hline(23, 0, 95, "shadow")
    return s


def build_kb_neon() -> Sprite:
    s = Sprite(96, 24)
    bevel_rect(s, 12, 4, 83, 16, "metal", "wall_light", "shadow")   # 60% footprint (72px), centred
    third = (83 - 12 + 1) // 3
    s.hline(17, 12, 12 + third - 1, "screen")
    s.hline(17, 12 + third, 12 + 2 * third - 1, "gold")
    s.hline(17, 12 + 2 * third, 83, "plant")
    # Underglow bleed: the RGB strip's own light spills one row up onto the
    # case, dithered rather than a hard second stripe.
    for x in range(12, 12 + third):
        s.dot(x, 15, bayer_mix(x, 15, 0.35, "metal", "screen"))
    for x in range(12 + third, 12 + 2 * third):
        s.dot(x, 15, bayer_mix(x, 15, 0.35, "metal", "gold"))
    for x in range(12 + 2 * third, 84):
        s.dot(x, 15, bayer_mix(x, 15, 0.35, "metal", "plant"))
    s.hline(23, 8, 87, "shadow")
    return s


# --------------------------------------------------------------------------
# Mouse (4): 44x24 at (224, 90)
# --------------------------------------------------------------------------

def build_mouse_stock() -> Sprite:
    """HERO-FIDELITY MATCH: the shell is a real bevel (upper-left key light
    highlight, lower-right shadow) instead of a flat metal blob, matching
    every other beveled case in the scene."""
    s = Sprite(44, 24)
    s.rect(2, 10, 41, 21, "shadow")           # mat/pad shadow the shell sits on
    bevel_rect(s, 15, 4, 28, 16, "metal", "wall_light", "shadow", ratio=0.6)
    s.vline(21, 5, 15, "shadow")              # 2-button seam
    s.hline(23, 0, 43, "shadow")
    return s


def build_mouse_gaming() -> Sprite:
    s = Sprite(44, 24)
    s.rect(2, 9, 41, 21, "shadow")
    bevel_rect(s, 13, 3, 30, 17, "metal", "wall_light", "shadow", ratio=0.6)
    s.vline(21, 3, 6, "shadow")               # scroll notch
    s.dot(21, 9, "screen")                    # accent
    for x, y in ((20, 8), (22, 10)):
        s.dot(x, y, bayer_mix(x, y, 0.5, "screen", "screen_dim"))   # accent glow bleed
    s.hline(23, 0, 43, "shadow")
    return s


def build_mouse_trackball() -> Sprite:
    s = Sprite(44, 24)
    s.rect(2, 10, 41, 21, "shadow")
    bevel_rect(s, 10, 5, 33, 16, "metal", "wall_light", "shadow", ratio=0.6)
    s.ellipse(21, 11, 3, 3, "gold")
    s.dot(19, 10, "cream")                    # ball's specular glint, upper-left
    s.hline(23, 0, 43, "shadow")
    return s


def build_mouse_vertical() -> Sprite:
    """HERO-FIDELITY MATCH: the ergonomic body reads with a lit ridge along
    its upper-left face and a dithered shadow along its lower-right face
    instead of one flat metal silhouette."""
    s = Sprite(44, 24)
    s.rect(2, 12, 41, 21, "shadow")
    for i, y in enumerate(range(4, 17)):
        inset = i // 3
        x0, x1 = 17 + inset, 26 - inset
        s.hline(y, x0, x1, "metal")
        s.dot(x0, y, bayer_mix(x0, y, 0.6, "metal", "wall_light"))   # lit left ridge
        s.dot(x1, y, bayer_mix(x1, y, 0.6, "metal", "shadow"))       # shadow right ridge
    s.hline(23, 0, 43, "shadow")
    return s


# --------------------------------------------------------------------------
# Beverage (4): 20x24 at (56, 90)
# --------------------------------------------------------------------------

def build_bev_mug() -> Sprite:
    """HERO-FIDELITY MATCH: the ceramic body is a dithered highlight+shadow
    gradient (lit toward the left, matching the room's key light) instead
    of a flat cream fill, so it reads as a rounded mug, not a beige tile."""
    s = Sprite(20, 24)
    for y in range(10, 22):
        for x in range(4, 16):
            s.dot(x, y, hgrad(x, y, 4, 15, "pot", "cream"))
    s.hline(14, 4, 15, "pot")                 # coffee line at the rim
    s.dot(4, 10, "shadow")                    # chip
    s.dots([(6, 6), (9, 4)], "cream")         # steam
    s.dots([(16, 12), (16, 13), (16, 14)], "cream")   # handle
    s.dot(16, 13, "pot")
    s.hline(23, 2, 17, "shadow")
    return s


def build_bev_thermos() -> Sprite:
    """HERO-FIDELITY MATCH: the steel body is a real bevel instead of a
    flat metal rect plus one manual highlight line."""
    s = Sprite(20, 24)
    bevel_rect(s, 6, 6, 13, 21, "metal", "wall_light", "shadow", ratio=0.6)
    bevel_rect(s, 6, 3, 13, 5, "desk_dark", "floor_light", "shadow", ratio=0.6)
    s.hline(23, 3, 16, "shadow")
    return s


def build_bev_teacup() -> Sprite:
    """HERO-FIDELITY MATCH: the cup body gets the same lit-left/dark-right
    ceramic gradient as the mug; the saucer keeps a flat top but gains a
    dithered AO rim where it meets the desk."""
    s = Sprite(20, 24)
    s.rect(2, 19, 17, 21, "cream")
    for y in range(11, 20):
        for x in range(6, 15):
            s.dot(x, y, hgrad(x, y, 6, 14, "pot", "cream"))
    s.hline(11, 6, 14, "pot")
    s.dot(15, 13, "cream")                    # handle
    for x in range(2, 18):
        s.dot(x, 21, bayer_mix(x, 21, 0.4, "cream", "shadow"))   # saucer's own AO rim
    s.hline(23, 0, 19, "shadow")
    return s


def build_bev_energy() -> Sprite:
    """HERO-FIDELITY MATCH: `screen_dim` already gave this can a shadow
    side; adding a dithered `cream` glint on the lit side completes the
    bevel so the can reads as a metallic cylinder."""
    s = Sprite(20, 24)
    s.rect(6, 5, 13, 21, "screen")
    s.vline(13, 5, 21, "screen_dim")          # shadow side
    for y in range(5, 22):
        s.dot(6, y, bayer_mix(6, y, 0.45, "screen", "cream"))   # lit side glint
    s.dots([(9, 5), (10, 5)], "gold")
    s.hline(23, 3, 16, "shadow")
    return s


# SCENE-REACTIONS - "click the beverage -> steam/sip".
#
# Mechanism: per-style FRAMES, not one shared overlay sprite. The four vessels
# have four different rim heights (mug 10, thermos 3, teacup 11, can 5) on a
# 20x24 canvas, so a single shared burst would float five rows above the mug's
# rim or be painted straight through the thermos's lid - and a shared overlay
# would also owe the frontend a new layer, z-index and placement rect. A frame
# swap on the slot that already exists needs none of that.
#
# Each react frame is the SAME vessel, hopped (1px then 2px) with its contact
# shadow held on the desk and pulled in a pixel on the second rung
# (hop_with_planted_shadow), plus a burst grown from that vessel's OWN rim:
# steam wisps for the three hot drinks, fizz bubbles plus a `gold` sparkle for
# the energy can - which is what keeps the four reads style-consistent instead
# of putting a plume of steam over a cold can. The thermos, whose lid is 3
# rows from the top of the canvas, vents SIDEWAYS at the lid seam instead of
# upward: there is no headroom, and pressure escaping a sealed flask is the
# right read for it anyway.


def _wisp(x: int, y0: int, n: int, phase: int = 0, colour: str = "cream"):
    """A wavy dotted steam wisp rising from (x, y0): `n` dots, one every other
    row, alternating 1px sideways so it curls instead of ruling a straight
    line (a straight dotted line reads as a crack, not as steam)."""
    return [(x + (1 if (i + phase) % 2 else 0), y0 - 2 * i, colour)
            for i in range(n)]


# style -> rest builder, (contact shadow row, x0, x1), and the burst point
# list for each of the two rungs, in FINAL canvas coordinates.
BEV_REACT: dict[str, dict] = {
    "mug": {
        "rest": build_bev_mug, "shadow": (23, 2, 17),
        "burst": (_wisp(5, 8, 3) + _wisp(11, 7, 2, phase=1),
                  _wisp(4, 7, 4) + _wisp(12, 6, 3, phase=1)),
    },
    "thermos": {
        "rest": build_bev_thermos, "shadow": (23, 3, 16),
        "burst": ([(5, 3, "cream"), (4, 1, "cream"),
                   (14, 3, "cream"), (15, 1, "cream")],
                  [(5, 2, "cream"), (3, 1, "cream"), (2, 0, "cream"),
                   (14, 2, "cream"), (16, 1, "cream"), (17, 0, "cream")]),
    },
    "teacup": {
        "rest": build_bev_teacup, "shadow": (23, 0, 19),
        "burst": (_wisp(7, 9, 3) + _wisp(12, 8, 2, phase=1),
                  _wisp(6, 8, 4) + _wisp(13, 7, 3, phase=1)),
    },
    "energy": {
        "rest": build_bev_energy, "shadow": (23, 3, 16),
        "burst": ([(7, 3, "cream"), (11, 3, "cream"), (9, 1, "gold")],
                  # Rung two pushes the two side bubbles OUT past the can's
                  # own columns (6..13): sat on its shoulders they merged into
                  # the silhouette's top edge instead of reading as fizz.
                  [(5, 2, "cream"), (14, 2, "cream"), (9, 0, "cream"),
                   (11, 1, "gold")]),
    },
}


def build_bev_react(style: str, stage: int) -> Sprite:
    """`stage` 0/1 = the react's first/second frame: hop 1px/2px, shadow
    pulled in 0px/1px per side, burst small/opened-out."""
    spec = BEV_REACT[style]
    row, sx0, sx1 = spec["shadow"]
    s = hop_with_planted_shadow(spec["rest"](), -(stage + 1), row, sx0, sx1,
                                inset=stage)
    for x, y, colour in spec["burst"][stage]:
        s.dot(x, y, colour)
    return s


# --------------------------------------------------------------------------
# Plant (3): 40x44 at (244, 32), base row 76 (local row 44)
# --------------------------------------------------------------------------

def build_plant_succulent() -> Sprite:
    """Only the lower 24 rows (20..43) are painted; the tall empty slot
    above is intentional - a small plant in a tall slot.

    HERO-FIDELITY MATCH: each rosette lobe gets its own light/dark side
    (`shade_ellipse`) instead of a flat green blob, and the terracotta
    pot gets a beveled rim so it reads as a glazed ceramic pot.
    """
    s = Sprite(40, 44)
    bevel_rect(s, 14, 32, 25, 41, "pot", "lamp", "desk_dark", ratio=0.45)
    s.hline(32, 14, 25, "gold")
    for cx, cy in ((16, 24), (22, 22), (19, 27)):
        shade_ellipse(s, cx, cy, 4, 4, "wall_dark", "plant")
    s.hline(42, 10, 29, "shadow")
    return s


def build_plant_monstera() -> Sprite:
    """HERO-FIDELITY MATCH: each leaf gets a lit/shadow split instead of a
    flat green fill, and the pot is beveled like every other ceramic/metal
    vessel in the manifest."""
    s = Sprite(40, 44)
    bevel_rect(s, 12, 38, 27, 43, "pot", "lamp", "desk_dark", ratio=0.45)
    s.vline(19, 20, 38, "desk_dark")
    s.vline(23, 22, 38, "desk_dark")
    leaves = ((14, 10, 8, 6), (26, 8, 8, 6), (20, 20, 10, 7), (12, 26, 7, 5), (28, 26, 7, 5))
    for cx, cy, rx, ry in leaves:
        shade_ellipse(s, cx, cy, rx, ry, "wall_dark", "plant")
        s.vline(cx, cy - ry, cy + ry, "desk_dark")   # the monstera split
    s.hline(43, 8, 31, "shadow")
    return s


def build_plant_bonsai() -> Sprite:
    """HERO-FIDELITY MATCH: canopy clumps shaded lit/dark, tray beveled."""
    s = Sprite(40, 44)
    bevel_rect(s, 4, 38, 35, 42, "pot", "lamp", "desk_dark", ratio=0.4)   # wide shallow tray
    s.hline(38, 4, 35, "gold")
    s.vline(18, 24, 38, "desk_dark")        # gnarled trunk
    s.vline(19, 20, 25, "desk_dark")
    s.vline(22, 18, 22, "desk_dark")
    for cx, cy, rx, ry in ((16, 14, 9, 4), (24, 12, 8, 4)):
        shade_ellipse(s, cx, cy, rx, ry, "wall_dark", "plant")
    s.hline(43, 2, 37, "shadow")
    return s


# --------------------------------------------------------------------------
# Wall (3): 40x44 at (24, 16) - wall-mounted, no floor contact shadow
# --------------------------------------------------------------------------

def build_wall_poster() -> Sprite:
    """HERO-FIDELITY MATCH: the frame is beveled and the paper gets a soft
    dithered AO fade at its edges (curling slightly away from the wall)
    instead of a flat cream card."""
    s = Sprite(40, 44)
    bevel_rect(s, 2, 2, 37, 41, "shadow", "wall_light", "wall_dark", ratio=0.5)
    s.rect(5, 5, 34, 38, "cream")
    for y in range(5, 39):
        for x in (5, 34):
            s.dot(x, y, bayer_mix(x, y, 0.4, "cream", "floor_light"))   # paper edge AO
    for y, x1 in ((10, 28), (13, 25), (16, 30), (19, 22), (22, 27), (25, 20),
                  (28, 26), (31, 24)):
        s.hline(y, 8, x1, "desk_dark")
    return s


def build_wall_shelf() -> Sprite:
    """HERO-FIDELITY MATCH: each volume's highlight/shadow pair now sits
    lit-left/shadow-right (matching the room's one key light, where the
    old version had it backwards), and the board itself is beveled."""
    s = Sprite(40, 44)
    board_top = 30
    volumes = ((3, 14, 8, "shirt"), (10, 12, 15, "plant"),
               (17, 16, 22, "pot"), (24, 13, 27, "metal"))
    for x0, y0, x1, cover in volumes:
        s.rect(x0, y0, x1, board_top - 1, cover)
        s.rect(x0, y0 + 1, x0 + 1, board_top - 2, "wall_light")   # lit left edge
        s.vline(x1, y0, board_top - 2, "shadow")                  # shadow right edge
    s.rect(31, 20, 34, 22, "gold")           # trophy cup
    s.vline(32, 23, 25, "gold")
    s.rect(31, 26, 34, board_top - 1, "gold")
    s.dot(31, 20, "cream")                   # trophy's own glint
    bevel_rect(s, 0, board_top + 1, 39, board_top + 2, "desk", "wall_light", "desk_dark", ratio=0.5)
    s.hline(board_top, 0, 39, "gold")
    s.hline(43, 0, 39, "shadow")
    return s


def build_wall_neon() -> Sprite:
    """HERO-FIDELITY MATCH: the outline-only tube gets the same dithered
    bloom the room's own glow pool uses (`radial_dither_glow`) instead of
    being a hard, unfilled ring - a neon sign should visibly bleed light,
    the exact "soft dithering not a flat blob" standard the hero pass set."""
    s = Sprite(40, 44)
    radial_dither_glow(s, 19.5, 21.0, 16, 20, 4, 2, 35, 41, "lamp", gain=1.6)
    s.outline(10, 6, 29, 37, "lamp")          # bloom halo, one step out
    radial_dither_glow(s, 19.5, 21.0, 10, 14, 8, 4, 31, 39, "screen", gain=1.8)
    s.outline(12, 8, 27, 35, "screen")        # the tube glyph itself
    s.vline(19, 12, 31, "screen")
    s.vline(20, 12, 31, "screen")
    return s


# --------------------------------------------------------------------------
# Buddy (4): 28x30 at (288, 46), base row 76 (local row 30)
# --------------------------------------------------------------------------

def build_buddy_duck() -> Sprite:
    """HERO-FIDELITY MATCH: the rubber body reads with a lit-left/shadow-
    right dithered gradient instead of a flat gold fill."""
    s = Sprite(28, 30)
    for y in range(15, 18):
        for x in range(9, 19):
            s.dot(x, y, hgrad(x, y, 9, 18, "pot", "gold"))          # head
    for y in range(17, 28):
        for x in range(7, 21):
            s.dot(x, y, hgrad(x, y, 7, 20, "pot", "gold"))          # body
    s.hline(26, 8, 19, "pot")
    s.dot(20, 17, "pot")                      # beak
    s.dot(21, 17, "lamp")                     # beak's own lit tip
    s.dot(17, 16, "shadow")                   # eye
    s.hline(29, 5, 22, "shadow")
    return s


def _build_buddy_bot(eyes_open: bool) -> Sprite:
    """HERO-FIDELITY MATCH: the chassis is a real bevel (lit upper-left,
    shadow lower-right) instead of a flat metal block."""
    s = Sprite(28, 30)
    s.vline(13, 4, 9, "metal")                # antenna
    s.dot(13, 9, "metal")
    bevel_rect(s, 6, 10, 21, 26, "metal", "wall_light", "shadow", ratio=0.55)
    eye_colour = "screen" if eyes_open else "metal"
    s.dots([(10, 14), (17, 14)], eye_colour)
    s.dot(13, 4, "gold")                      # antenna bead
    s.hline(29, 4, 23, "shadow")
    return s


def build_buddy_bot_a() -> Sprite:
    return _build_buddy_bot(eyes_open=True)


def build_buddy_bot_b() -> Sprite:
    return _build_buddy_bot(eyes_open=False)


_CAT_HEAD_ROWS = ((10, 10, 17), (11, 8, 19), (12, 6, 21), (13, 5, 22),
                  (14, 5, 22), (15, 6, 21), (16, 8, 19))
_CAT_TAIL_REST = ((20, 13), (21, 14), (22, 15), (21, 16))
_CAT_EARS_REST = ((9, 9), (16, 9))


def _build_buddy_cat(dy: int = 0, tail=_CAT_TAIL_REST, tip=(22, 15),
                     ears=_CAT_EARS_REST, shadow_inset: int = 0) -> Sprite:
    """HERO-FIDELITY MATCH: the fur reads as a lit-left/shadow-right
    dithered gradient (`hair` toward the dark/shadow side, `desk` - the
    nearest warm-brown palette entry - toward the lit side) instead of a
    flat single-tone silhouette.

    Parameterised for SCENE-REACTIONS: `dy` lifts the cat off the desk while
    its contact shadow (row 17) stays put, and the ears/tail are passed in so
    a react frame can twitch them without redrawing the head. The gradient is
    always evaluated at the UNSHIFTED y and plotted at y + dy, so a hop moves
    the fur's dither pattern with it instead of re-rolling it (the default
    arguments reproduce the original sprite byte for byte)."""
    s = Sprite(28, 30)
    for y, x0, x1 in _CAT_HEAD_ROWS:
        for x in range(x0, x1 + 1):
            s.dot(x, y + dy, hgrad(x, y, x0, x1, "hair", "desk"))
    for ex, ey in ears:
        s.dot(ex, ey + dy, "pot")             # ear tips
    s.dots([(x, y + dy) for x, y in tail], "hair")
    s.dot(tail[0][0], tail[0][1] + dy, "desk")     # tail's own lit root
    s.dot(tip[0], tip[1] + dy, "cream")            # tail tip
    s.hline(17, 4 + shadow_inset, 23 - shadow_inset, "shadow")
    return s


def build_buddy_cat() -> Sprite:
    return _build_buddy_cat()


# --------------------------------------------------------------------------
# SCENE-REACTIONS - one 2-frame react per real buddy
# --------------------------------------------------------------------------
#
# Same "small displacement, contact shadow stays on the desk" mechanism the
# beverages use (hop_with_planted_shadow), because it is the cue that reads at
# 1x: 1-2px of lift plus a shadow that tightens says "it jumped", where the
# same 1-2px applied to the shadow as well would just be a slide.
#
#   duck  a plain hop - a rubber duck's whole character is that it bobs.
#   bot   a hop plus its own two electrical tells: the eyes flash from
#         `screen` to `lamp` (the palette's brightest warm) and the antenna
#         bead throws a pair of `lamp` beep sparks that fly further out on the
#         second frame. The blink pair (buddy_bot_a/b) is untouched.
#   cat   ears and tail, not a hop, on the first frame - a cat that is all
#         head and tail reacts by twitching them - then a 1px lift with the
#         ears splayed wider and the tail flicked higher on the second.


def build_buddy_duck_react(stage: int) -> Sprite:
    return hop_with_planted_shadow(build_buddy_duck(), -(stage + 1),
                                   29, 5, 22, inset=stage)


def build_buddy_bot_react(stage: int) -> Sprite:
    dy = -(stage + 1)
    s = hop_with_planted_shadow(_build_buddy_bot(eyes_open=True), dy,
                                29, 4, 23, inset=stage)
    s.dots([(10, 14 + dy), (17, 14 + dy)], "lamp")        # eyes flash
    spark = 2 + stage                                     # beep, thrown wider
    s.dots([(13 - spark, 3 + dy - stage), (13 + spark, 3 + dy - stage)], "lamp")
    return s


_CAT_TAIL_FLICK = ({"tail": ((20, 13), (21, 12), (22, 11), (21, 10)),
                    "tip": (22, 11), "ears": ((9, 8), (16, 8)), "dy": 0},
                   {"tail": ((20, 13), (21, 12), (23, 11), (22, 9)),
                    "tip": (23, 11), "ears": ((8, 7), (17, 7)), "dy": -1})


def build_buddy_cat_react(stage: int) -> Sprite:
    spec = _CAT_TAIL_FLICK[stage]
    return _build_buddy_cat(dy=spec["dy"], tail=spec["tail"], tip=spec["tip"],
                            ears=spec["ears"], shadow_inset=stage)


# --------------------------------------------------------------------------
# COIN - dexel's own HUD currency (COIN)
# --------------------------------------------------------------------------
#
# NOT a scene sprite. The coin lives in the top-left HUD (index.html
# #hud-cash) where it replaces the NES.css cash glyph, and on the store's
# "TOTAL DEV CASH" line. game.css draws it in a 16x16 CSS-px box at 1x
# (`.nes-icon.coin.is-small { transform: scale(1) }`, "a 16x16 `.nes-icon`,
# twice the height of the text beside it"), so it is AUTHORED at 16x16 native
# - displayed 1:1, or a clean nearest-neighbour 2x on a retina panel. It is a
# UI icon that floats in the bar, so - unlike every scene prop - it carries NO
# contact shadow (nothing-resting-on-a-surface, so rule 5 does not apply).
#
# One key light, upper-left, like everything else in the scene: a lit `lamp`
# rim on the upper-left arc, a `pot` (ember) shadow rim on the lower-right, a
# flat `gold` body, a `cream` specular glint high-left, and a `pot` "$" stamped
# through the centre so the icon reads as CURRENCY at 16px, not just a disc.
# The warm depth ramp is entirely in-palette: pot < gold < lamp < cream.

COIN_SIZE = 16
_COIN_CX = _COIN_CY = 7.5          # centre of the 16px canvas (even width)
_COIN_R = 7.0                      # radius -> a 14px disc, 1px clear margin

# The HUD/store coin manifest. Kept separate from the 75-entry scene SPEC
# because the coin is a UI icon, not a behind-the-shoulder scene sprite: it
# has no anchor in the 320x200 room, no contact shadow, and no store
# thumbnail. main() builds, palette-checks and accounts for these exactly the
# way it does the scene manifest.
COIN_SPEC: list[tuple[str, int, int]] = [
    ("coin.png", COIN_SIZE, COIN_SIZE),
    ("coin_react_a.png", COIN_SIZE, COIN_SIZE),
    ("coin_react_b.png", COIN_SIZE, COIN_SIZE),
]
COIN_NAMES = {name for name, _, _ in COIN_SPEC}

# The "$" glyph: a 5x7 bitmap, a vertical bar through an S. Stamped in `pot`
# (the coin's darkest warm) centred on the disc so the icon reads as money.
_COIN_DOLLAR = (
    "..X..",
    ".XXX.",
    "X.X..",
    ".XXX.",
    "..X.X",
    ".XXX.",
    "..X..",
)


def _coin_inside(x: int, y: int) -> bool:
    """True inside the centred 14px disc."""
    dx, dy = x - _COIN_CX, y - _COIN_CY
    return dx * dx + dy * dy <= _COIN_R * _COIN_R


def build_coin() -> Sprite:
    """The rest coin: a cozy gold disc with an ember-shadowed lower-right rim,
    a lit upper-left rim, a cream glint and a stamped "$"."""
    s = Sprite(COIN_SIZE, COIN_SIZE)
    inside = [[_coin_inside(x, y) for x in range(COIN_SIZE)]
              for y in range(COIN_SIZE)]
    # 1. flat gold body
    for y in range(COIN_SIZE):
        for x in range(COIN_SIZE):
            if inside[y][x]:
                s.dot(x, y, "gold")
    # 2. rim ring, coloured by the one upper-left light: a rim pixel is an
    #    inside pixel touching the outside. lamp on the upper-left arc, pot
    #    (ember depth) on the lower-right arc, gold on the two sides - the
    #    classic lit/shadow crescent that reads a flat fill as a raised disc.
    for y in range(COIN_SIZE):
        for x in range(COIN_SIZE):
            if not inside[y][x]:
                continue
            edge = any(not (0 <= x + dx < COIN_SIZE and 0 <= y + dy < COIN_SIZE
                            and inside[y + dy][x + dx])
                       for dx, dy in ((1, 0), (-1, 0), (0, 1), (0, -1)))
            if not edge:
                continue
            proj = (x - _COIN_CX) + (y - _COIN_CY)
            if proj <= -1.0:
                s.dot(x, y, "lamp")
            elif proj >= 1.0:
                s.dot(x, y, "pot")
    # 3. "$" stamp in pot, centred (5x7 glyph -> x 5..9, y 4..10)
    for gy, row in enumerate(_COIN_DOLLAR):
        for gx, ch in enumerate(row):
            if ch == "X":
                s.dot(5 + gx, 4 + gy, "pot")
    # 4. specular glint, high-left, cream (kept off the "$", <5% of the disc)
    s.dots([(4, 4), (5, 4), (4, 5)], "cream")
    return s


def build_coin_react_a() -> Sprite:
    """Frame A of the click flip: the coin turned edge-on - a narrow vertical
    ellipse, mid-spin. Lit `lamp` on the left face, `pot` shadow on the right,
    a `cream` catch-light down the lit edge. No "$": you cannot read the face
    of a coin seen on its edge, and its absence is what sells the rotation."""
    s = Sprite(COIN_SIZE, COIN_SIZE)
    rx, ry = 2.5, 7.0
    for y in range(COIN_SIZE):
        row_inside = []
        for x in range(COIN_SIZE):
            dx, dy = x - _COIN_CX, y - _COIN_CY
            if (dx * dx) / (rx * rx) + (dy * dy) / (ry * ry) <= 1.0:
                row_inside.append(x)
                if dx <= -1.3:
                    s.dot(x, y, "lamp")
                elif dx >= 1.0:
                    s.dot(x, y, "pot")
                else:
                    s.dot(x, y, "gold")
        # cream catch-light on the lit (left) edge of the middle rows
        if row_inside and 4 <= y <= 11:
            s.dot(min(row_inside), y, "cream")
    return s


def build_coin_react_b() -> Sprite:
    """Frame B: the coin lands back face-up with a `cream`/`lamp` sparkle
    thrown off its upper-right rim - the satisfying end of the flip. Same
    canvas and anchor as the rest coin, so the wiring swaps `src` in place."""
    s = build_coin()
    s.dots([(13, 2), (12, 2), (14, 2), (13, 1), (13, 3)], "cream")   # 4-point spark
    s.dot(14, 1, "lamp")                                             # twinkle tip
    return s


COIN_BUILDERS = {
    "coin.png": build_coin,
    "coin_react_a.png": build_coin_react_a,
    "coin_react_b.png": build_coin_react_b,
}
assert set(COIN_BUILDERS) == COIN_NAMES, "COIN_BUILDERS and COIN_SPEC drifted apart"

# The coin's opaque-footprint band (a ~14px disc, area ~= pi*7^2 ~= 154 px),
# and the react-pair frame diffs. Filled in / asserted by the coin self-checks.
COIN_FOOTPRINT_MIN = 130
COIN_FOOTPRINT_MAX = 175
COIN_REACT_PAIRS = (
    ("coin.png", "coin_react_a.png"),        # rest -> edge-on: a big change
    ("coin_react_a.png", "coin_react_b.png"),  # edge-on -> face+sparkle
    ("coin.png", "coin_react_b.png"),        # rest differs from the landed frame
)


# --------------------------------------------------------------------------
# Builders table + thumbnail catalog
# --------------------------------------------------------------------------

BUILDERS = {
    "room_back.png": build_room_back,
    "desk_back.png": build_desk_back,
    "monitor.png": build_monitor,
    "monitor_shake_a.png": lambda: build_monitor_shake("a"),
    "monitor_shake_b.png": lambda: build_monitor_shake("b"),
    "monitor_frame.png": build_monitor_frame,
    "monitor_frame_shake_a.png": lambda: build_monitor_frame_shake("a"),
    "monitor_frame_shake_b.png": lambda: build_monitor_frame_shake("b"),
    "dev_form_idle.png": lambda: build_dev_form("idle"),
    "dev_form_type_a.png": lambda: build_dev_form("type_a"),
    "dev_form_type_b.png": lambda: build_dev_form("type_b"),
    "dev_form_mouse.png": lambda: build_dev_form("mouse"),
    "dev_form_sleep.png": lambda: build_dev_form("sleep"),
    "dev_base_idle.png": lambda: build_dev_base("idle"),
    "dev_base_type_a.png": lambda: build_dev_base("type_a"),
    "dev_base_type_b.png": lambda: build_dev_base("type_b"),
    "dev_base_mouse.png": lambda: build_dev_base("mouse"),
    "dev_base_sleep.png": lambda: build_dev_base("sleep"),
    "dev_form_breath.png": lambda: build_dev_form("breath"),
    "dev_form_stretch.png": lambda: build_dev_form("stretch"),
    "dev_form_cheer_a.png": lambda: build_dev_form("cheer_a"),
    "dev_form_cheer_b.png": lambda: build_dev_form("cheer_b"),
    "dev_base_breath.png": lambda: build_dev_base("breath"),
    "dev_base_stretch.png": lambda: build_dev_base("stretch"),
    "dev_base_cheer_a.png": lambda: build_dev_base("cheer_a"),
    "dev_base_cheer_b.png": lambda: build_dev_base("cheer_b"),
    "dev_form_react_a.png": lambda: build_dev_form("react_a"),
    "dev_form_react_b.png": lambda: build_dev_form("react_b"),
    "dev_base_react_a.png": lambda: build_dev_base("react_a"),
    "dev_base_react_b.png": lambda: build_dev_base("react_b"),
    "hoodie_classic.png": build_hoodie_classic,
    "hoodie_zip.png": build_hoodie_zip,
    "hoodie_tech.png": build_hoodie_tech,
    "hoodie_cloak.png": build_hoodie_cloak,
    "chair_basic_form.png": build_chair_basic_form,
    "chair_basic_detail.png": build_chair_basic_detail,
    "chair_racer_form.png": build_chair_racer_form,
    "chair_racer_detail.png": build_chair_racer_detail,
    "chair_exec_form.png": build_chair_exec_form,
    "chair_exec_detail.png": build_chair_exec_detail,
    "chair_antigrav_form.png": build_chair_antigrav_form,
    "chair_antigrav_detail.png": build_chair_antigrav_detail,
    "kb_membrane.png": build_kb_membrane,
    "kb_mech.png": build_kb_mech,
    "kb_split.png": build_kb_split,
    "kb_neon.png": build_kb_neon,
    "mouse_stock.png": build_mouse_stock,
    "mouse_gaming.png": build_mouse_gaming,
    "mouse_trackball.png": build_mouse_trackball,
    "mouse_vertical.png": build_mouse_vertical,
    "bev_mug.png": build_bev_mug,
    "bev_thermos.png": build_bev_thermos,
    "bev_teacup.png": build_bev_teacup,
    "bev_energy.png": build_bev_energy,
    "bev_mug_react_a.png": lambda: build_bev_react("mug", 0),
    "bev_mug_react_b.png": lambda: build_bev_react("mug", 1),
    "bev_thermos_react_a.png": lambda: build_bev_react("thermos", 0),
    "bev_thermos_react_b.png": lambda: build_bev_react("thermos", 1),
    "bev_teacup_react_a.png": lambda: build_bev_react("teacup", 0),
    "bev_teacup_react_b.png": lambda: build_bev_react("teacup", 1),
    "bev_energy_react_a.png": lambda: build_bev_react("energy", 0),
    "bev_energy_react_b.png": lambda: build_bev_react("energy", 1),
    "plant_succulent.png": build_plant_succulent,
    "plant_monstera.png": build_plant_monstera,
    "plant_bonsai.png": build_plant_bonsai,
    "wall_poster.png": build_wall_poster,
    "wall_shelf.png": build_wall_shelf,
    "wall_neon.png": build_wall_neon,
    "buddy_duck.png": build_buddy_duck,
    "buddy_bot_a.png": build_buddy_bot_a,
    "buddy_bot_b.png": build_buddy_bot_b,
    "buddy_cat.png": build_buddy_cat,
    "buddy_duck_react_a.png": lambda: build_buddy_duck_react(0),
    "buddy_duck_react_b.png": lambda: build_buddy_duck_react(1),
    "buddy_bot_react_a.png": lambda: build_buddy_bot_react(0),
    "buddy_bot_react_b.png": lambda: build_buddy_bot_react(1),
    "buddy_cat_react_a.png": lambda: build_buddy_cat_react(0),
    "buddy_cat_react_b.png": lambda: build_buddy_cat_react(1),
}
assert set(BUILDERS) == SPEC_NAMES, "BUILDERS and SPEC have drifted apart"

CHAIR_STYLES = ("basic", "racer", "exec", "antigrav")

# Store thumbnails (art-direction "Store thumbnails (derived, not
# authored)"). The doc names the mechanism (derive 40x40 from the real
# sprite, nearest-neighbour, ÷1/÷2/÷4 then centre-crop; tintable items need
# BOTH a _form and a _detail thumb) but does not enumerate the catalog's
# item ids - that list lives in the (out of scope, Go-side) item catalog.
# This generator's own catalog is reconstructed here from the manifest
# groups themselves: one thumbnail per distinct purchasable sprite. Two
# judgment calls, spelled out because they are not literally in the doc:
#   * `buddy_bot` is ONE catalog item (bot_a is its sprite); bot_b is the
#     blink animation frame, not a second purchasable companion.
#   * the hoodie's tintable "form" thumbnail is derived from
#     dev_form_idle.png (the shared, frame-independent fabric layer every
#     hoodie style tints), not from a per-style form file - there isn't one,
#     by design (art-direction "hoodie styles share one silhouette").
NONTINT_ITEMS: dict[str, str] = {
    "kb_membrane": "kb_membrane.png", "kb_mech": "kb_mech.png",
    "kb_split": "kb_split.png", "kb_neon": "kb_neon.png",
    "mouse_stock": "mouse_stock.png", "mouse_gaming": "mouse_gaming.png",
    "mouse_trackball": "mouse_trackball.png", "mouse_vertical": "mouse_vertical.png",
    "bev_mug": "bev_mug.png", "bev_thermos": "bev_thermos.png",
    "bev_teacup": "bev_teacup.png", "bev_energy": "bev_energy.png",
    "plant_succulent": "plant_succulent.png", "plant_monstera": "plant_monstera.png",
    "plant_bonsai": "plant_bonsai.png",
    "wall_poster": "wall_poster.png", "wall_shelf": "wall_shelf.png",
    "wall_neon": "wall_neon.png",
    "buddy_duck": "buddy_duck.png", "buddy_bot": "buddy_bot_a.png",
    "buddy_cat": "buddy_cat.png",
}
TINT_ITEMS: dict[str, tuple[str, str]] = {
    "hoodie_classic": ("dev_form_idle.png", "hoodie_classic.png"),
    "hoodie_zip": ("dev_form_idle.png", "hoodie_zip.png"),
    "hoodie_tech": ("dev_form_idle.png", "hoodie_tech.png"),
    "hoodie_cloak": ("dev_form_idle.png", "hoodie_cloak.png"),
    "chair_basic": ("chair_basic_form.png", "chair_basic_detail.png"),
    "chair_racer": ("chair_racer_form.png", "chair_racer_detail.png"),
    "chair_exec": ("chair_exec_form.png", "chair_exec_detail.png"),
    "chair_antigrav": ("chair_antigrav_form.png", "chair_antigrav_detail.png"),
}
# STORE-2.0 "monitor" colour slot: unlike the hoodie/chair (which are a
# form+detail pair), the monitor ships ONE tintable bezel form overlaid on the
# fixed monitor.png, so its store card needs a single `_form` thumbnail and NO
# `_detail`. A ÷2 centre-crop of monitor_frame.png would be unrecognisable (its
# screen interior is transparent), so the thumb is authored directly at 40x40
# via THUMB_BUILDERS below, same as the chair thumbs.
FORM_ONLY_ITEMS: dict[str, str] = {"monitor": "monitor_frame.png"}


def derive_thumbnail(img: Image.Image) -> Image.Image:
    """40x40, integer nearest-neighbour downsample (÷1/÷2/÷4 only, and only
    when it divides evenly) then centre-crop; a sprite smaller than 40px in
    either dimension is centred on a transparent 40x40 canvas instead (the
    doc's "centre-crop" does not define what to do when there is nothing to
    crop - this generator's choice, noted in the handoff).
    """
    w, h = img.size
    d = 1
    for cand in (4, 2, 1):
        if w % cand == 0 and h % cand == 0 and w // cand >= 40 and h // cand >= 40:
            d = cand
            break
    if w // d >= 40 and h // d >= 40:
        scaled = img.resize((w // d, h // d), Image.NEAREST) if d > 1 else img
        sw, sh = scaled.size
        x0, y0 = (sw - 40) // 2, (sh - 40) // 2
        return scaled.crop((x0, y0, x0 + 40, y0 + 40))
    canvas = Image.new("RGBA", (40, 40), (0, 0, 0, 0))
    canvas.paste(img, ((40 - w) // 2, (40 - h) // 2), img)
    return canvas


# Authored-override escape hatch (art-direction "Store thumbnails"): if a
# derived thumbnail fails the vision self-check at 40x40, the fix is a
# hand-authored replacement at the same filename rather than a smarter crop.
#
# The vision self-check on this run's derived thumbnails found exactly the
# casualty the doc predicts by name: "the chair sprites at ÷4 are the likely
# casualties". At the ÷2 downsample this generator actually uses (÷4 would
# undershoot 40px on every chair), a centre-crop of chair_basic/_racer/_exec
# lands in the middle of a wide, flat backrest panel and shows neither the
# wings nor the base - it reads as an unidentifiable rectangle, tinted or
# not (checked as the real store card would show it: tinted form + detail
# composited together). chair_antigrav now joins them: after the behind-view
# seat redesign its full sprite grew tall enough (136x100) that the ÷2
# centre-crop lands in the flat interior of the back shell and shows only an
# unidentifiable rounded rectangle - so it too is authored directly at 40x40.
# Every hoodie style (whose _form thumb is a recognisable garment silhouette
# on its own) still survives the crop and is NOT overridden.
#
# These 8 overrides are still procedurally built - by the small, purpose-
# built icon functions below, each its own miniature chair drawn directly at
# 40x40 rather than derived - so they stay deterministic, diffable, and
# palette/ramp-pure like every other file in this generator, without
# resurrecting the "N pre-coloured PNGs" pattern the doc forbids for tinted
# parts: each is still a form/detail pair, tintable by the same CSS recipe.
THUMB_OVERRIDES: set[str] = {
    "thumb_chair_basic_form.png", "thumb_chair_basic_detail.png",
    "thumb_chair_racer_form.png", "thumb_chair_racer_detail.png",
    "thumb_chair_exec_form.png", "thumb_chair_exec_detail.png",
    "thumb_chair_antigrav_form.png", "thumb_chair_antigrav_detail.png",
    # STORE-2.0 monitor slot: a single tintable `_form` icon (a screen on a
    # stand), authored directly at 40x40 because monitor_frame.png's screen
    # interior is transparent and a centre-crop would show nothing.
    "thumb_monitor_form.png",
}


def _thumb_chair_generic_form(seat_w: int) -> Sprite:
    """Shared shape for the overridden chair thumbnails: a rounded-top
    backrest, small armrest pads, a seat pan and a gas cylinder - the same
    furniture the full sprites now read as, drawn whole inside a 40x40 icon
    (an icon has no keyboard/hard-region to keep clear of)."""
    s = Sprite(40, 40, palette=RAMP)
    half = seat_w // 2
    s.rect(20 - half, 8, 20 + half, 24, "ramp4")             # back body
    s.rect(20 - half + 2, 4, 20 + half - 2, 8, "ramp4")      # rounded crown (narrower)
    s.rect(20 - half, 9, 20 + half, 10, "ramp3")             # crown shade line
    s.rect(20 - half - 3, 15, 20 - half - 1, 20, "ramp3")    # left armrest pad
    s.rect(20 + half + 1, 15, 20 + half + 3, 20, "ramp3")    # right armrest pad
    s.rect(20 - half + 1, 25, 20 + half - 1, 29, "ramp3")    # seat pan
    s.rect(18, 30, 22, 34, "ramp2")                          # gas cylinder
    return s


def build_thumb_chair_basic_form() -> Sprite:
    s = _thumb_chair_generic_form(13)
    for my in range(12, 24, 3):                     # sparse mesh punctures
        for mx in range(11, 30, 4):
            s.dot(mx, my, "ramp3")
    return s


def build_thumb_chair_basic_detail() -> Sprite:
    s = Sprite(40, 40)
    s.hline(34, 10, 30, "metal")
    s.hline(35, 8, 32, "shadow")
    fab = build_thumb_chair_basic_form()
    outline_from_mask(s, union_mask(fab.mask(), s.mask()), "shadow")
    return s


def build_thumb_chair_racer_form() -> Sprite:
    s = _thumb_chair_generic_form(15)
    s.rect(20 - 7, 8, 20 - 4, 22, "ramp5")          # integrated bolster ridges
    s.rect(20 + 4, 8, 20 + 7, 22, "ramp5")
    return s


def build_thumb_chair_racer_detail() -> Sprite:
    s = Sprite(40, 40)
    s.vline(15, 6, 20, "cream")                     # double stitching
    s.vline(25, 6, 20, "cream")
    s.hline(34, 10, 30, "metal")
    s.hline(35, 8, 32, "shadow")
    fab = build_thumb_chair_racer_form()
    outline_from_mask(s, union_mask(fab.mask(), s.mask()), "shadow")
    return s


def build_thumb_chair_exec_form() -> Sprite:
    return _thumb_chair_generic_form(17)            # broad padded executive back


def build_thumb_chair_exec_detail() -> Sprite:
    s = Sprite(40, 40)
    for gx, gy in ((14, 12), (26, 12), (14, 18), (26, 18)):
        s.dot(gx, gy, "gold")                       # button tufting
    s.hline(34, 8, 32, "metal")
    s.hline(35, 6, 34, "shadow")
    fab = build_thumb_chair_exec_form()
    outline_from_mask(s, union_mask(fab.mask(), s.mask()), "shadow")
    return s


def build_thumb_chair_antigrav_form() -> Sprite:
    """Rounded floating-pod icon: a rounded cradle back with two rounded
    wings, over a rounded seat pod - the behind-view pod the full sprite
    shows, drawn directly at 40x40 so the store card reads as a pod, not the
    featureless interior a centre-crop of the tall full sprite would give."""
    s = Sprite(40, 40, palette=RAMP)
    s.ellipse(20, 15, 12, 11, "ramp4")              # rounded back shell
    s.ellipse(7, 13, 4, 6, "ramp4")                 # left cradle wing
    s.ellipse(33, 13, 4, 6, "ramp4")                # right cradle wing
    s.ellipse(20, 8, 10, 4, "ramp3")                # top shading on the shell
    s.ellipse(20, 27, 14, 6, "ramp3")               # rounded seat pod
    return s


def build_thumb_chair_antigrav_detail() -> Sprite:
    s = Sprite(40, 40)
    s.outline(9, 33, 31, 35, "screen")              # levitation glow ring
    for x in range(10, 31):
        s.dot(x, 34, bayer_mix(x, 34, 0.4, "screen", "lamp"))
    fab = build_thumb_chair_antigrav_form()
    outline_from_mask(s, union_mask(fab.mask(), s.mask()), "shadow")
    return s


def build_thumb_monitor_form() -> Sprite:
    """40x40 RAMP-PURE store-card icon for the monitor colour slot: a screen on
    a stand, its BEZEL in the ramp so the store card's CSS tint colours it per
    selected monitor colour (the same recipe the chair thumbs use). The screen
    glass sits at ramp1 (a dark, off screen) so the bezel reads as a frame
    AROUND it; one upper-left key light (dithered ramp5 rim on the top+left,
    ramp2 on the shadowed right+bottom) keeps the frame legible at the darkest
    tint. Ramp-pure, so there is no `shadow`-palette contact row (a form file
    may only paint the ramp) - a store icon rests on nothing anyway."""
    s = Sprite(40, 40, palette=RAMP)
    bx0, by0, bx1, by1 = 6, 6, 33, 27           # bezel outer rect (28x22)
    sx0, sy0, sx1, sy1 = 9, 9, 30, 24           # screen glass
    s.rect(bx0, by0, bx1, by1, "ramp4")         # bezel casing
    s.rect(sx0, sy0, sx1, sy1, "ramp1")         # dark screen glass
    for i in range(4):                          # glass reflection streak (upper-left)
        s.dot(sx0 + 1 + i, sy0 + 1 + i, "ramp3")
    # Bezel bevel: lit top+left, shadowed right+bottom.
    for x in range(bx0, bx1 + 1):
        s.dot(x, by0, bayer_mix(x, by0, 0.7, "ramp4", "ramp5"))
        s.dot(x, by1, bayer_mix(x, by1, 0.5, "ramp4", "ramp2"))
    for y in range(by0, by1 + 1):
        s.dot(bx0, y, bayer_mix(bx0, y, 0.7, "ramp4", "ramp5"))
        s.dot(bx1, y, bayer_mix(bx1, y, 0.5, "ramp4", "ramp2"))
    # Stand: neck + foot, mid ramp so the screen stays the focal mass.
    s.rect(18, by1 + 1, 21, 32, "ramp3")        # neck
    for y in range(by1 + 1, 33):
        s.dot(18, y, "ramp4")                   # lit left edge of the neck
        s.dot(21, y, "ramp2")                   # shadowed right edge
    s.rect(12, 33, 27, 34, "ramp3")             # foot base
    for x in range(12, 28):
        s.dot(x, 33, bayer_mix(x, 33, 0.4, "ramp3", "ramp4"))
        s.dot(x, 34, bayer_mix(x, 34, 0.4, "ramp3", "ramp2"))
    return s


THUMB_BUILDERS = {
    "thumb_monitor_form.png": build_thumb_monitor_form,
    "thumb_chair_basic_form.png": build_thumb_chair_basic_form,
    "thumb_chair_basic_detail.png": build_thumb_chair_basic_detail,
    "thumb_chair_racer_form.png": build_thumb_chair_racer_form,
    "thumb_chair_racer_detail.png": build_thumb_chair_racer_detail,
    "thumb_chair_exec_form.png": build_thumb_chair_exec_form,
    "thumb_chair_exec_detail.png": build_thumb_chair_exec_detail,
    "thumb_chair_antigrav_form.png": build_thumb_chair_antigrav_form,
    "thumb_chair_antigrav_detail.png": build_thumb_chair_antigrav_detail,
}
assert THUMB_OVERRIDES == set(THUMB_BUILDERS), "override set and its builders drifted apart"


def thumbnail_plan() -> dict[str, list[str]]:
    """Map every thumbnail filename this run must produce to the source
    file(s) it is derived from, so main() can both write them and account
    for every file that should exist in assets/ afterwards."""
    plan: dict[str, list[str]] = {}
    for item_id, src in NONTINT_ITEMS.items():
        plan[f"thumb_{item_id}.png"] = [src]
    for item_id, (form_src, detail_src) in TINT_ITEMS.items():
        plan[f"thumb_{item_id}_form.png"] = [form_src]
        plan[f"thumb_{item_id}_detail.png"] = [detail_src]
    for item_id, form_src in FORM_ONLY_ITEMS.items():
        # `_form` only - no `_detail` pair (see FORM_ONLY_ITEMS). The source is
        # recorded for provenance; the thumb is a THUMB_OVERRIDES builder, so
        # main() builds it directly rather than deriving from this source.
        plan[f"thumb_{item_id}_form.png"] = [form_src]
    return plan


# --------------------------------------------------------------------------
# Self-check
# --------------------------------------------------------------------------

def check(filename: str, want_w: int, want_h: int) -> tuple[bool, str]:
    """Verify one written PNG: size, ramp/palette purity, partial alpha."""
    img = Image.open(ASSETS / filename).convert("RGBA")
    w, h = img.size
    problems = []
    if (w, h) != (want_w, want_h):
        problems.append(f"size {w}x{h} != {want_w}x{want_h}")

    # Covers both manifest form files and the two overridden chair thumbnail
    # form icons (thumb_chair_<style>_form.png), which are also built with
    # palette=RAMP and must be ramp-audited, not palette-audited.
    is_form = filename in FORM_FILES or (filename.startswith("thumb_") and
                                          filename.endswith("_form.png"))
    valid = RGB_TO_RAMP if is_form else RGB_TO_NAME
    label = "off-ramp" if is_form else "off-palette"

    opaque = 0
    partial = 0
    offlist: dict[tuple[int, int, int], int] = {}
    for _count, colour in img.getcolors(maxcolors=1 << 20):
        r, g, b, a = colour
        if a == 0:
            continue
        if a != 255:
            partial += _count
        opaque += _count
        if (r, g, b) not in valid:
            offlist[(r, g, b)] = offlist.get((r, g, b), 0) + _count
    if partial:
        problems.append(f"{partial}px with partial alpha")
    if offlist:
        listed = ", ".join(f"#{r:02x}{g:02x}{b:02x} x{n}"
                           for (r, g, b), n in sorted(offlist.items()))
        problems.append(f"{label}: {listed}")
    if opaque < 4:
        problems.append(f"only {opaque} opaque px - looks blank")
    if filename == "room_back.png" and opaque != w * h:
        problems.append("background must be fully opaque")

    kind = "ramp " if is_form else "pal  "
    detail = f"{w}x{h}".ljust(9) + f"{opaque:>6} opaque  {kind}"
    if problems:
        return False, detail + "FAIL: " + "; ".join(problems)
    return True, detail + "ok"


def assert_dev_region(name: str, s: Sprite) -> str:
    """Developer keyboard guard (art-direction "Derivation rules"). The
    keyboard (room y90..113) is drawn UNDER the developer, and in this
    3/4-behind camera the hands genuinely rest ON the keys, so they DO cover
    its near rows - that is the correct composition, not a bug. What must be
    protected is the keyboard's FAR rows, or a purchasable item vanishes
    behind the figure: every developer layer (dev_form_*, dev_base_*, and the
    hoodie overlays, all on the 192x76 dev canvas) must leave local rows
    0..DEV_KB_GUARD_ROW-1 (room y92..98) fully transparent."""
    px = s.img.load()
    bad = [(x, y) for y in range(0, DEV_KB_GUARD_ROW) for x in range(s.w)
           if px[x, y][3] != 0]
    if bad:
        raise AssertionError(
            f"{name}: {len(bad)} dev pixel(s) intrude into the keyboard's far-row "
            f"guard (local rows 0..{DEV_KB_GUARD_ROW - 1}); first={bad[0]}")
    return (f"{name}: keyboard far-row guard clear "
            f"(local rows 0..{DEV_KB_GUARD_ROW - 1} = room y92..{92 + DEV_KB_GUARD_ROW - 1})")


def assert_dev_lift_headroom(name: str, s: Sprite) -> str:
    """P3 overlay-lift guard. render/scene.ts shifts the hoodie style overlay
    UP by FRAME_OVERLAY_DY[frame] (0..DEV_MAX_BODY_LIFT px) so its marks stay
    printed on the fabric that moved. A shifted overlay would therefore push
    any mark within DEV_MAX_BODY_LIFT rows of the keyboard far-row guard INTO
    that guard - so the overlays must keep those rows clear too, not just the
    guard's own rows. (dev_form/dev_base need no such margin: they are never
    offset by the frontend, the lift is baked into the frame.)"""
    limit = DEV_KB_GUARD_ROW + DEV_MAX_BODY_LIFT
    px = s.img.load()
    bad = [(x, y) for y in range(0, limit) for x in range(s.w)
           if px[x, y][3] != 0]
    if bad:
        raise AssertionError(
            f"{name}: {len(bad)} px in local rows 0..{limit - 1}; a hoodie overlay "
            f"shifted up {DEV_MAX_BODY_LIFT}px by the P3 scheduler would intrude "
            f"into the keyboard far-row guard; first={bad[0]}")
    return (f"{name}: {DEV_MAX_BODY_LIFT}px lift headroom clear "
            f"(local rows 0..{limit - 1})")


def assert_hoodie_on_fabric(name: str, s: Sprite) -> str:
    """SLIMMING-PASS guard. A hoodie style overlay is PRINTED ON the garment,
    so every one of its marks has to land on a fabric pixel of the frame it
    was authored against (`idle`). Nothing enforced that before, because the
    92px shoulder slab was wider than any mark anyone would place; the moment
    the shoulders came in to 76px, a yoke seam or a hem stripe that was not
    rescaled with them became a line floating in mid-air beside the figure.
    This is the check that says the garment still fits the body."""
    fabric = _dev_fabric_grid("idle")
    px = s.img.load()
    bad = [(x, y) for y in range(DEV_H) for x in range(DEV_W)
           if px[x, y][3] != 0 and not fabric[y][x]]
    if bad:
        raise AssertionError(
            f"{name}: {len(bad)} style mark px land OFF the idle-frame garment "
            f"(they would float beside the figure); first={bad[0]} "
            f"(room {(DEV_OX + bad[0][0], DEV_OY + bad[0][1])})")
    return f"{name}: every style mark printed on the garment"


# The band where the chair's BACKREST and the developer's shoulders are both
# visible: from the chair line down to just above the row the armrest pads
# start on (an armrest legitimately reaches outside the sitter - a shoulder
# blade does not).
PEEK_ROOM_ROWS = range(CHAIR_TOP_ROOM_Y, CHAIR_TOP_ROOM_Y + 4)


def assert_shoulder_peek(style: str, form: Sprite, detail: Sprite) -> str:
    """SLIMMING-PASS guard, and the one that makes "the chairs must slim MORE
    than the shoulders" a checked rule instead of a good intention: across the
    rows where backrest and shoulders are both on screen, the DEVELOPER has to
    be wider than the CHAIR on both sides. Fail it and the backrest has eaten
    the figure, which is the exact regression a future width tweak would
    introduce silently - the hooded read depends on shoulders > backrest."""
    dev = Image.open(ASSETS / "dev_form_idle.png").convert("RGBA").load()
    ox = 160 - form.w // 2
    oy = 200 - form.h
    fp, dp = form.img.load(), detail.img.load()
    worst = None
    for ry in PEEK_ROOM_ROWS:
        cxs = [x for x in range(form.w)
               if fp[x, ry - oy][3] != 0 or dp[x, ry - oy][3] != 0]
        dxs = [x for x in range(DEV_W) if dev[x, ry - DEV_OY][3] != 0]
        if not cxs or not dxs:
            raise AssertionError(f"chair_{style}: nothing to compare at room y{ry}")
        left = (ox + min(cxs)) - (DEV_OX + min(dxs))
        right = (DEV_OX + max(dxs)) - (ox + max(cxs))
        if left < 1 or right < 1:
            raise AssertionError(
                f"chair_{style}: at room y{ry} the backrest is as wide as or wider "
                f"than the developer (peek L{left}px / R{right}px) - the shoulders "
                f"no longer show at the sides of the chair")
        if worst is None or min(left, right) < worst[1]:
            worst = (ry, min(left, right))
    return (f"chair_{style}: shoulders peek past the backrest on every row of "
            f"room y{PEEK_ROOM_ROWS[0]}..{PEEK_ROOM_ROWS[-1]} "
            f"(tightest {worst[1]}px at y{worst[0]})")


# The two desk rects the hands have to actually reach, in ROOM coordinates
# (from geometry.ts SLOT_RECT): the keyboard and the mouse.
KB_ROOM_RECT = (112, 90, 207, 113)
MOUSE_ROOM_RECT = (224, 90, 267, 113)


def _skin_pixels(s: Sprite):
    """Room-coordinate list of every `skin` pixel in a dev_base layer - i.e.
    where the hands actually are."""
    px = s.img.load()
    want = PALETTE["skin"] + (255,)
    return [(DEV_OX + x, DEV_OY + y) for y in range(s.h) for x in range(s.w)
            if px[x, y] == want]


def assert_dev_hands(frame: str, s: Sprite) -> str:
    """POSITIVE silhouette check for the new composition (the old one only
    forbade things). The whole point of the 3/4-behind camera is that the
    hands are VISIBLE on the desk, so prove it from the pixels: every frame
    has exactly two hands, both above the chair line, and

      idle/type_a/type_b  both hands sit inside the KEYBOARD rect
      mouse               the left hand is on the keyboard and the right hand
                          is on the MOUSE rect
      sleep               both hands have slid off the keys (below the
                          keyboard's key rows) and are still on the desk
      breath/stretch      unchanged from idle - both hands still on the keys
                          (P3: a breath moves the SHOULDERS, not the hands)
      cheer_a/cheer_b     both hands flung OUT past the keyboard's two ends,
                          and the right one stops short of the mouse

    Plus, in EVERY frame, the two SIZE facts a pose must not change: each
    hand's skin bounding box is exactly DEV_HAND_W x DEV_HAND_H, and the pair
    is separated by at least a full hand width of empty space. The size check
    is what the hand-narrowing pass added - it is the difference between
    "the hands are 17px wide" being true and being merely intended, and it
    fails loudly if a future pose ever clips a hand against a canvas edge or
    lets one drift on top of the other.
    """
    pts = _skin_pixels(s)
    if not pts:
        raise AssertionError(f"dev_base_{frame}.png: no skin pixels - the hands vanished")
    lo = [p for p in pts if p[0] < 160]
    hi = [p for p in pts if p[0] >= 160]
    if not lo or not hi:
        raise AssertionError(
            f"dev_base_{frame}.png: expected one hand each side of room x160, "
            f"got {len(lo)} left / {len(hi)} right skin px")
    below = [p for p in pts if p[1] >= CHAIR_TOP_ROOM_Y]
    if below:
        raise AssertionError(
            f"dev_base_{frame}.png: {len(below)} hand px at/below the chair line "
            f"(room y{CHAIR_TOP_ROOM_Y}) would be occluded by the chair; first={below[0]}")

    # SIZE + SEPARATION, checked from the pixels in every frame (a pose moves
    # a hand; it must never resize or merge one).
    for label, side in (("left", lo), ("right", hi)):
        xs = [x for x, _y in side]
        ys = [y for _x, y in side]
        bw = max(xs) - min(xs) + 1
        bh = max(ys) - min(ys) + 1
        if (bw, bh) != (DEV_HAND_W, DEV_HAND_H):
            raise AssertionError(
                f"dev_base_{frame}.png: the {label} hand's skin bbox is {bw}x{bh}, "
                f"not {DEV_HAND_W}x{DEV_HAND_H} (room x{min(xs)}..{max(xs)}, "
                f"y{min(ys)}..{max(ys)}) - a pose must move a hand, not resize it")
    gap = min(x for x, _y in hi) - max(x for x, _y in lo) - 1
    if gap < DEV_HAND_W:
        raise AssertionError(
            f"dev_base_{frame}.png: only {gap}px between the two hands "
            f"(want >= one hand width, {DEV_HAND_W}px) - they stop reading as two")

    def inside(rect, ps):
        x0, y0, x1, y1 = rect
        return sum(1 for (x, y) in ps if x0 <= x <= x1 and y0 <= y <= y1)

    if frame in DEV_CHEER_FRAMES:
        # P3 celebration: prove from the pixels that the hands really did open
        # wide - the left one reaches past the keyboard's LEFT edge and the
        # right one past its RIGHT edge - and that the right hand still stops
        # short of the mouse slot, which it would otherwise sit on top of.
        kb_x0, _, kb_x1, _ = KB_ROOM_RECT
        lmin = min(x for x, _y in lo)
        rmax = max(x for x, _y in hi)
        if lmin >= kb_x0 or rmax <= kb_x1:
            raise AssertionError(
                f"dev_base_{frame}.png: hands did not open past the keyboard "
                f"(left reaches room x{lmin}, needs < {kb_x0}; right reaches "
                f"room x{rmax}, needs > {kb_x1})")
        if rmax >= MOUSE_ROOM_RECT[0]:
            raise AssertionError(
                f"dev_base_{frame}.png: the right hand reaches room x{rmax}, "
                f"onto the mouse slot (starts x{MOUSE_ROOM_RECT[0]})")
        return (f"dev_base_{frame}.png: hands flung out to room x{lmin} / x{rmax}, "
                f"past both keyboard ends (x{kb_x0}..{kb_x1}), clear of the mouse; "
                f"both hands {DEV_HAND_W}x{DEV_HAND_H}, {gap}px apart")

    if frame in DEV_REACT_FRAMES:
        # SCENE-REACTIONS: prove the wave from the pixels. The LEFT hand must
        # still be fully on the keyboard (it is the anchor that makes the beat
        # read as a flinch rather than as the whole figure sliding), and the
        # right one must have left the keys - past the keyboard's right end -
        # while still stopping short of the mouse slot it would otherwise sit
        # on top of.
        kb_x0, _, kb_x1, _ = KB_ROOM_RECT
        if inside(KB_ROOM_RECT, lo) != len(lo):
            raise AssertionError(
                f"dev_base_{frame}.png: the left hand left the keyboard "
                f"({inside(KB_ROOM_RECT, lo)}/{len(lo)}px inside {KB_ROOM_RECT}) - "
                f"the react must keep one hand planted")
        rmax = max(x for x, _y in hi)
        if rmax <= kb_x1:
            raise AssertionError(
                f"dev_base_{frame}.png: the right hand only reaches room x{rmax}, "
                f"still on the keyboard (ends x{kb_x1}) - the wave must come OFF "
                f"the keys")
        if rmax >= MOUSE_ROOM_RECT[0]:
            raise AssertionError(
                f"dev_base_{frame}.png: the right hand reaches room x{rmax}, onto "
                f"the mouse slot (starts x{MOUSE_ROOM_RECT[0]})")
        return (f"dev_base_{frame}.png: left hand planted on the keys, right hand "
                f"waved out to room x{rmax} (past the keyboard's x{kb_x1}, clear of "
                f"the mouse); both hands {DEV_HAND_W}x{DEV_HAND_H}, {gap}px apart")

    if frame == "sleep":
        if inside(KB_ROOM_RECT, pts) == len(pts):
            raise AssertionError(
                "dev_base_sleep.png: the hands are still fully on the keyboard - "
                "sleep must slide them off the keys")
        detail = f"both hands slid off the keys (lowest row {max(p[1] for p in pts)})"
    elif frame == "mouse":
        onkb = inside(KB_ROOM_RECT, lo)
        onmouse = inside(MOUSE_ROOM_RECT, hi)
        if onkb == 0 or onmouse == 0:
            raise AssertionError(
                f"dev_base_mouse.png: left hand on keyboard={onkb}px, right hand on "
                f"mouse={onmouse}px - the mouse pose must put ONE hand on the mouse")
        detail = f"left hand on the keyboard ({onkb}px), right hand on the mouse ({onmouse}px)"
    else:
        onkb = inside(KB_ROOM_RECT, pts)
        if onkb < len(pts) * 0.8:
            raise AssertionError(
                f"dev_base_{frame}.png: only {onkb}/{len(pts)} hand px land on the "
                f"keyboard rect {KB_ROOM_RECT}")
        detail = f"both hands on the keys ({onkb}/{len(pts)}px inside the keyboard rect)"
    return (f"dev_base_{frame}.png: {detail}; "
            f"both hands {DEV_HAND_W}x{DEV_HAND_H}, {gap}px apart")


def check_monitor_screen_rect() -> str:
    img = Image.open(ASSETS / "monitor.png").convert("RGBA")
    x0, y0, x1, y1 = MONITOR_SCREEN_RECT
    px = img.load()
    shadow = PALETTE["shadow"] + (255,)
    bad = [(x, y) for y in range(y0, y1 + 1) for x in range(x0, x1 + 1)
           if px[x, y] != shadow]
    w, h = x1 - x0 + 1, y1 - y0 + 1
    if bad:
        raise AssertionError(f"monitor.png screen rect has {len(bad)} wrong px; first={bad[0]}")
    return (f"monitor.png inner screen rect local ({x0},{y0})-({x1},{y1}) = {w}x{h}, "
            f"room (98,24) 124x44 once placed at (94,20) - OK, all flat shadow")


def check_frame_diff(name_a: str, name_b: str, expect_max_px: int | None = None) -> str:
    a = Image.open(ASSETS / name_a).convert("RGBA")
    b = Image.open(ASSETS / name_b).convert("RGBA")
    if a.size != b.size:
        raise AssertionError(f"{name_a} and {name_b} differ in size")
    w, h = a.size
    pa, pb = a.load(), b.load()
    xs, ys, n = [], [], 0
    for y in range(h):
        for x in range(w):
            if pa[x, y] != pb[x, y]:
                xs.append(x)
                ys.append(y)
                n += 1
    if n == 0:
        raise AssertionError(f"{name_a} and {name_b} are byte-identical - not a real 2-frame pair")
    bbox = (min(xs), min(ys), max(xs), max(ys))
    if expect_max_px is not None and n != expect_max_px:
        raise AssertionError(
            f"{name_a} vs {name_b}: expected exactly {expect_max_px} differing px, got {n} "
            f"(bbox {bbox})")
    return f"{name_a} vs {name_b}: {n} differing px, bbox {bbox}"


def _form_mass(name: str) -> tuple[int, float]:
    """Opaque-pixel count and vertical centroid of a written dev layer."""
    img = Image.open(ASSETS / name).convert("RGBA")
    px = img.load()
    n, ysum = 0, 0
    for y in range(img.size[1]):
        for x in range(img.size[0]):
            if px[x, y][3] != 0:
                n += 1
                ysum += y
    return n, ysum / n


def check_body_lift_ladder() -> str:
    """P3 proof-of-motion, the half a frame-diff cannot prove. A frame diff
    says "these two PNGs are not identical"; this says the ambient frames are
    the SAME FIGURE, RAISED - which is the whole authoring promise ("keep the
    silhouette/proportions identical, 1-2px movements only"):

      * the vertical centroid must climb monotonically idle -> breath ->
        stretch (the body is rising, one rung at a time), and
      * the opaque mass must stay within 4% of the idle frame's at every rung
        (nothing was added, removed, or redrawn - it moved).

    A regression that redrew the pose, or that moved a hand instead of the
    shoulders, fails one of the two."""
    rungs = ["idle"] + list(DEV_AMBIENT_FRAMES)
    out = []
    base_n, prev_cy = None, None
    for frame in rungs:
        n, cy = _form_mass(f"dev_form_{frame}.png")
        if base_n is None:
            base_n = n
        drift = abs(n - base_n) / base_n
        if drift > 0.04:
            raise AssertionError(
                f"dev_form_{frame}.png: opaque mass {n}px is {drift:.1%} off "
                f"dev_form_idle.png's {base_n}px - the figure was REDRAWN, not lifted")
        if prev_cy is not None and cy >= prev_cy - 0.3:
            raise AssertionError(
                f"dev_form_{frame}.png: centroid y {cy:.2f} did not rise clearly "
                f"above the previous rung's {prev_cy:.2f} - no visible lift")
        out.append(f"{frame} {n}px cy={cy:.2f}")
        prev_cy = cy
    return "ambient lift ladder: " + " -> ".join(out) + " (mass within 4%, centroid rising)"


def assert_monitor_shake(tag: str, s: Sprite, rest: Sprite) -> str:
    """SCENE-REACTIONS composition guard - the one that keeps the shake from
    breaking the terminal's text positioning contract (see the constants above
    build_monitor_shake). Three things, all checked from the pixels:

      * the glass is still the exact flat-`shadow` rect, at MONITOR_SCREEN_RECT
        shifted by the frame's dx and NOT by a single row;
      * the DOM terminal's text box still lands inside that glass; and
      * the neck, foot and contact shadow are byte-identical to monitor.png -
        the head shook, the monitor did not walk across the desk.
    """
    dx = MONITOR_SHAKE_DX[tag]
    x0, y0, x1, y1 = MONITOR_SCREEN_RECT
    px, rp = s.img.load(), rest.img.load()
    shadow = PALETTE["shadow"] + (255,)
    bad = [(x, y) for y in range(y0, y1 + 1) for x in range(x0 + dx, x1 + dx + 1)
           if px[x, y] != shadow]
    if bad:
        raise AssertionError(
            f"monitor_shake_{tag}.png: {len(bad)} px of the shifted screen rect "
            f"are not flat shadow; first={bad[0]}")
    # A vertical shake would move the glass rows out from under 11 lines of DOM
    # text that do not move with them, so prove the displacement is purely
    # horizontal: every head row of the shake frame must be that SAME row of
    # monitor.png, shifted by dx (the one column that falls off the leading
    # edge excepted). This also proves the head moved rigidly - no row of the
    # bezel slid further than another.
    moved = [(x, y) for y in MONITOR_HEAD_ROWS
             for x in range(max(0, dx), min(s.w, s.w + dx))
             if px[x, y] != rp[x - dx, y]]
    if moved:
        raise AssertionError(
            f"monitor_shake_{tag}.png: {len(moved)} head px are not monitor.png's "
            f"own row shifted {dx:+d}px - the shake is not a rigid HORIZONTAL "
            f"displacement, so the glass rows can drift out from under the DOM "
            f"terminal; first={moved[0]}")
    ox, oy = MONITOR_ROOM_ORIGIN
    gl, gr = ox + x0 + dx, ox + x1 + dx
    tl, tt, tr, tb = TERMINAL_ROOM_RECT
    if tl < gl or tr > gr:
        raise AssertionError(
            f"monitor_shake_{tag}.png: glass spans room x{gl}..{gr} but the terminal "
            f"text box spans x{tl}..{tr} - the shake pushed text onto the bezel")
    planted = [(x, y) for y in range(s.h) if y not in MONITOR_HEAD_ROWS
               for x in range(s.w) if px[x, y] != rp[x, y]]
    if planted:
        raise AssertionError(
            f"monitor_shake_{tag}.png: {len(planted)} px of the neck/foot/contact "
            f"shadow moved; first={planted[0]} - only the head may shake")
    return (f"monitor_shake_{tag}.png: head {dx:+d}px, glass room x{gl}..{gr} "
            f"y{oy + y0}..{oy + y1} (rows unmoved), terminal text x{tl}..{tr} inside "
            f"it, foot planted")


def assert_monitor_frame_shake(tag: str, s: Sprite, rest: Sprite) -> str:
    """BUG-1 guard: the tinted bezel's shake must be the SAME rigid horizontal
    knock as monitor_shake, so base + frame move as one. Three things, all from
    the pixels:

      * the head shifted rigidly by exactly MONITOR_SHAKE_DX[tag] (every head
        px is monitor_frame.png's own row shifted dx) - matching the base;
      * the shifted screen rect stays fully TRANSPARENT, so the tint never
        lands on the glass the DOM terminal text sits over; and
      * the neck, foot and contact shadow are byte-identical to
        monitor_frame.png - only the head knocks, exactly as the base does.
    """
    dx = MONITOR_SHAKE_DX[tag]
    x0, y0, x1, y1 = MONITOR_SCREEN_RECT
    px, rp = s.img.load(), rest.img.load()
    # The head is a rigid horizontal copy of the rest bezel, one leading column
    # excepted (it falls off the canvas edge) - identical proof to the base's.
    moved = [(x, y) for y in MONITOR_HEAD_ROWS
             for x in range(max(0, dx), min(s.w, s.w + dx))
             if px[x, y] != rp[x - dx, y]]
    if moved:
        raise AssertionError(
            f"monitor_frame_shake_{tag}.png: {len(moved)} head px are not "
            f"monitor_frame.png's own row shifted {dx:+d}px - base and bezel "
            f"would desync; first={moved[0]}")
    # The screen interior (shifted by dx) must stay transparent so the bezel
    # tint never bleeds over the terminal glass.
    tinted = [(x, y) for y in range(y0, y1 + 1)
              for x in range(x0 + dx, x1 + dx + 1)
              if 0 <= x < s.w and px[x, y][3] != 0]
    if tinted:
        raise AssertionError(
            f"monitor_frame_shake_{tag}.png: {len(tinted)} px of the shifted "
            f"screen rect are opaque - the bezel tint reaches the glass; "
            f"first={tinted[0]}")
    planted = [(x, y) for y in range(s.h) if y not in MONITOR_HEAD_ROWS
               for x in range(s.w) if px[x, y] != rp[x, y]]
    if planted:
        raise AssertionError(
            f"monitor_frame_shake_{tag}.png: {len(planted)} px of the "
            f"neck/foot/contact shadow moved; first={planted[0]} - only the "
            f"head may shake")
    return (f"monitor_frame_shake_{tag}.png: bezel head {dx:+d}px in sync with "
            f"monitor_shake_{tag}, glass rect transparent, foot planted")


def assert_hop_shadow_planted(react: str, rest: str, shadow_row: int) -> str:
    """SCENE-REACTIONS prop-hop guard: a react frame may lift the object, but
    the contact shadow belongs to the DESK, so it must stay on the row it was
    on, never grow, and the object must actually have risen. Fail any of the
    three and the "hop" is either a slide (shadow moved with it) or a
    floating sprite (shadow gone)."""
    ra = Image.open(ASSETS / react).convert("RGBA")
    rb = Image.open(ASSETS / rest).convert("RGBA")
    pa, pb = ra.load(), rb.load()
    w, h = ra.size

    def rows(px):
        return [y for y in range(h) if any(px[x, y][3] != 0 for x in range(w))]

    def span(px, y):
        xs = [x for x in range(w) if px[x, y][3] != 0]
        return (min(xs), max(xs)) if xs else None

    ya, yb = rows(pa), rows(pb)
    if ya[-1] != yb[-1] != shadow_row:
        raise AssertionError(
            f"{react}: bottom-most opaque row is {ya[-1]} (rest {yb[-1]}, contact "
            f"shadow {shadow_row}) - the shadow must stay on the desk")
    sa, sb = span(pa, shadow_row), span(pb, shadow_row)
    if sa is None or sa[0] < sb[0] or sa[1] > sb[1]:
        raise AssertionError(
            f"{react}: contact shadow spans {sa} against the rest frame's {sb} - a "
            f"hopping object casts the SAME or a tighter shadow, never a wider one")
    if ya[0] > yb[0]:
        raise AssertionError(
            f"{react}: topmost opaque row {ya[0]} is BELOW the rest frame's {yb[0]} - "
            f"the react must lift/extend the object, not sink it")
    return (f"{react}: top row {yb[0]}->{ya[0]}, contact shadow held on row "
            f"{shadow_row} ({sb[0]}..{sb[1]} -> {sa[0]}..{sa[1]})")


def assert_bev_burst_accounting(style: str, stage: int) -> str:
    """SCENE-REACTIONS beverage guard, and the cheapest possible proof that no
    steam dot was painted ON TOP OF the vessel (steam in front of a mug reads
    as a bug) and none fell off the canvas: the react frame's opaque mass must
    equal the rest frame's, minus the two px the tightened contact shadow gives
    up, plus EXACTLY one px per burst point."""
    tag = "ab"[stage]
    rest = Image.open(ASSETS / f"bev_{style}.png").convert("RGBA")
    react = Image.open(ASSETS / f"bev_{style}_react_{tag}.png").convert("RGBA")

    def mass(img):
        px = img.load()
        return sum(1 for y in range(img.size[1]) for x in range(img.size[0])
                   if px[x, y][3] != 0)

    burst = BEV_REACT[style]["burst"][stage]
    want = mass(rest) - 2 * stage + len(burst)
    got = mass(react)
    if got != want:
        raise AssertionError(
            f"bev_{style}_react_{tag}.png: {got} opaque px, expected {want} "
            f"({mass(rest)} rest - {2 * stage} shadow inset + {len(burst)} burst) - a "
            f"burst dot landed on the vessel or off the canvas")
    px = react.load()
    for x, y, _c in burst:
        if px[x, y][3] == 0:
            raise AssertionError(
                f"bev_{style}_react_{tag}.png: burst px ({x},{y}) is transparent")
    return (f"bev_{style}_react_{tag}.png: {len(burst)}px burst clear of the vessel, "
            f"mass {mass(rest)} -> {got}")


def assert_hoodie_react_alignment(name: str, s: Sprite, frame: str) -> str:
    """SCENE-REACTIONS overlay guard. The react frames move the garment on BOTH
    axes, and the frontend realigns the one hoodie overlay by the matching
    rigid offset (FRAME_OVERLAY_DX/DY). Prove the result: every style mark,
    once shifted, still lands on THAT frame's fabric. This is what makes the
    "lean the whole torso instead of tilting the hood" decision checkable
    rather than a comment - tilt the hood alone and this fails."""
    dx, dy = FRAME_OVERLAY_DX[frame], FRAME_OVERLAY_DY[frame]
    fabric = _dev_fabric_grid(frame)
    px = s.img.load()
    bad = []
    for y in range(DEV_H):
        for x in range(DEV_W):
            if px[x, y][3] == 0:
                continue
            nx, ny = x + dx, y + dy
            if not (0 <= nx < DEV_W and 0 <= ny < DEV_H and fabric[ny][nx]):
                bad.append((x, y))
    if bad:
        raise AssertionError(
            f"{name}: {len(bad)} style mark px land OFF the {frame} garment once "
            f"offset by ({dx:+d},{dy:+d}); first={bad[0]}")
    return f"{name}: every style mark still on the fabric at ({dx:+d},{dy:+d})"


def check_react_displacement() -> str:
    """SCENE-REACTIONS proof-of-motion for the developer, the half a frame diff
    cannot give: the react frames must be the SAME FIGURE displaced, so the
    vertical centroid has to rise above idle's (the body flinches UP) while the
    opaque mass stays close to it. The bound is looser than the ambient
    ladder's 4% because one arm genuinely changes pose here (it reaches further
    than a forearm on the keys, so a few dozen px of limb are new) - what it
    still rules out is a redrawn or resized figure."""
    base_n, base_cy = _form_mass("dev_form_idle.png")
    out = []
    for frame in DEV_REACT_FRAMES:
        n, cy = _form_mass(f"dev_form_{frame}.png")
        drift = abs(n - base_n) / base_n
        if drift > 0.12:
            raise AssertionError(
                f"dev_form_{frame}.png: opaque mass {n}px is {drift:.1%} off "
                f"dev_form_idle.png's {base_n}px - the figure was REDRAWN, not moved")
        if cy >= base_cy - 0.3:
            raise AssertionError(
                f"dev_form_{frame}.png: centroid y {cy:.2f} is not clearly above "
                f"dev_form_idle.png's {base_cy:.2f} - no visible flinch")
        out.append(f"{frame} {n}px ({drift:+.1%}) cy={cy:.2f}")
    return (f"react displacement: idle {base_n}px cy={base_cy:.2f} -> "
            + " , ".join(out) + " (mass within 12%, centroid risen)")


def cleanup_stale(expected: set[str]) -> list[str]:
    """Delete any file in app/assets/ that is not part of this run's expected
    output (the SPEC manifest plus its derived thumbnails). This is what
    guarantees app/assets/ contains EXACTLY the v2 files after a run - the v0.2
    corpses (room_bg.png, dev_idle.png, chair.png, ...) do not survive a
    rewrite by omission, they are actively removed."""
    removed = []
    for path in sorted(ASSETS.glob("*.png")):
        if path.name not in expected:
            path.unlink()
            removed.append(path.name)
    return removed


def assert_coin_round(name: str, s: Sprite) -> str:
    """The rest coin must read as a DISC, not a square: every 2x2 corner block
    of the 16x16 canvas is transparent, and the opaque footprint sits in the
    band a ~14px disc occupies. A regression that squared it off, shrank it to
    a dot, or grew it to a blob fails one of the two."""
    px = s.img.load()
    for cx0, cy0 in ((0, 0), (COIN_SIZE - 2, 0),
                     (0, COIN_SIZE - 2), (COIN_SIZE - 2, COIN_SIZE - 2)):
        for y in range(cy0, cy0 + 2):
            for x in range(cx0, cx0 + 2):
                if px[x, y][3] != 0:
                    raise AssertionError(
                        f"{name}: corner pixel ({x},{y}) is opaque - the coin is "
                        "not round")
    opaque = sum(1 for y in range(s.h) for x in range(s.w) if px[x, y][3] != 0)
    if not COIN_FOOTPRINT_MIN <= opaque <= COIN_FOOTPRINT_MAX:
        raise AssertionError(
            f"{name}: {opaque} opaque px outside the disc band "
            f"{COIN_FOOTPRINT_MIN}..{COIN_FOOTPRINT_MAX}")
    return f"{name}: round ({opaque} opaque px, all four corners clear)"


def assert_coin_glint(name: str, s: Sprite) -> str:
    """The coin's highlight is load-bearing (it is what makes gold read as a
    metal disc under the one upper-left light, not a flat sticker): at least
    one `cream` specular pixel must sit in the upper-left quadrant."""
    px = s.img.load()
    cream = PALETTE["cream"] + (255,)
    n = sum(1 for y in range(0, COIN_SIZE // 2) for x in range(0, COIN_SIZE // 2)
            if px[x, y] == cream)
    if n < 1:
        raise AssertionError(
            f"{name}: no cream glint pixel in the upper-left quadrant - the "
            "coin's key highlight is missing")
    return f"{name}: cream glint present ({n}px, upper-left quadrant)"


def main() -> int:
    ASSETS.mkdir(parents=True, exist_ok=True)

    built: dict[str, Sprite] = {}
    for filename, _w, _h in SPEC:
        sprite = BUILDERS[filename]()
        sprite.save(filename)
        built[filename] = sprite
    for filename, _w, _h in COIN_SPEC:
        sprite = COIN_BUILDERS[filename]()
        sprite.save(filename)
        built[filename] = sprite

    ok = True
    print(f"{'file':<28}{'size':<9}{'opaque':>8}  {'kind':<5}status")
    for filename, want_w, want_h in SPEC + COIN_SPEC:
        passed, detail = check(filename, want_w, want_h)
        ok = ok and passed
        print(f"{filename:<28}{detail}")

    print(f"\n{len(SPEC)} manifest sprites + {len(COIN_SPEC)} coin (HUD currency) "
          f"written to {ASSETS}")

    print("\n-- chair hard-region assertion --")
    for style in CHAIR_STYLES:
        for layer in ("form", "detail"):
            name = f"chair_{style}_{layer}.png"
            try:
                print(" ", assert_chair_region(name, built[name]))
            except AssertionError as exc:
                ok = False
                print("  FAIL:", exc)

    print("\n-- developer keyboard far-row guard --")
    for frame in DEV_FRAMES:
        for layer in ("form", "base"):
            name = f"dev_{layer}_{frame}.png"
            try:
                print(" ", assert_dev_region(name, built[name]))
            except AssertionError as exc:
                ok = False
                print("  FAIL:", exc)
    for name in HOODIE_FILES:
        try:
            print(" ", assert_dev_region(name, built[name]))
        except AssertionError as exc:
            ok = False
            print("  FAIL:", exc)

    print("\n-- hoodie style marks printed on the garment --")
    for name in HOODIE_FILES:
        try:
            print(" ", assert_hoodie_on_fabric(name, built[name]))
        except AssertionError as exc:
            ok = False
            print("  FAIL:", exc)

    print("\n-- shoulders peek past every backrest (slimming-pass invariant) --")
    for style in CHAIR_STYLES:
        try:
            print(" ", assert_shoulder_peek(style, built[f"chair_{style}_form.png"],
                                            built[f"chair_{style}_detail.png"]))
        except AssertionError as exc:
            ok = False
            print("  FAIL:", exc)

    print("\n-- hoodie overlay lift headroom (P3 scheduler offsets it up to "
          f"{DEV_MAX_BODY_LIFT}px) --")
    for name in HOODIE_FILES:
        try:
            print(" ", assert_dev_lift_headroom(name, built[name]))
        except AssertionError as exc:
            ok = False
            print("  FAIL:", exc)
    print("  FRAME_OVERLAY_DY (render/scene.ts must carry this same table):")
    print("   ", ", ".join(f"{f}:{dy if dy is not None else 'n/a'}"
                           for f, dy in FRAME_OVERLAY_DY.items()))

    print("\n-- developer hand placement (behind-view silhouette check) --")
    for frame in DEV_FRAMES:
        try:
            print(" ", assert_dev_hands(frame, built[f"dev_base_{frame}.png"]))
        except AssertionError as exc:
            ok = False
            print("  FAIL:", exc)

    print("\n-- monitor screen rect --")
    try:
        print(" ", check_monitor_screen_rect())
    except AssertionError as exc:
        ok = False
        print("  FAIL:", exc)

    print("\n-- frame-difference assertions --")
    for pair in (TYPING_PAIR, MOUSE_PAIR):
        try:
            print(" ", check_frame_diff(*pair))
        except AssertionError as exc:
            ok = False
            print("  FAIL:", exc)
    try:
        print(" ", check_frame_diff(*TYPING_FABRIC_PAIR))
    except AssertionError as exc:
        ok = False
        print("  FAIL:", exc)
    try:
        print(" ", check_frame_diff(*BLINK_PAIR, expect_max_px=2))
    except AssertionError as exc:
        ok = False
        print("  FAIL:", exc)
    for pair in AMBIENT_PAIRS + (CHEER_PAIR,):
        try:
            print(" ", check_frame_diff(*pair))
        except AssertionError as exc:
            ok = False
            print("  FAIL:", exc)
    try:
        print(" ", check_body_lift_ladder())
    except AssertionError as exc:
        ok = False
        print("  FAIL:", exc)

    print("\n-- SCENE-REACTIONS: rest-vs-react and react-pair frame diffs --")
    for pair in REACT_PAIRS:
        try:
            print(" ", check_frame_diff(*pair))
        except AssertionError as exc:
            ok = False
            print("  FAIL:", exc)

    print("\n-- SCENE-REACTIONS: the developer's flinch is a displacement --")
    try:
        print(" ", check_react_displacement())
    except AssertionError as exc:
        ok = False
        print("  FAIL:", exc)
    print("  FRAME_OVERLAY_DX (render/scene.ts needs this second column):")
    print("   ", ", ".join(f"{f}:{dx if dx is not None else 'n/a'}"
                           for f, dx in FRAME_OVERLAY_DX.items()))

    print("\n-- SCENE-REACTIONS: hoodie overlays still fit the react frames --")
    for frame in DEV_REACT_FRAMES:
        for name in HOODIE_FILES:
            try:
                print(" ", assert_hoodie_react_alignment(name, built[name], frame))
            except AssertionError as exc:
                ok = False
                print("  FAIL:", exc)

    print("\n-- SCENE-REACTIONS: monitor shake keeps the terminal contract --")
    for tag in ("a", "b"):
        try:
            print(" ", assert_monitor_shake(tag, built[f"monitor_shake_{tag}.png"],
                                            built["monitor.png"]))
        except AssertionError as exc:
            ok = False
            print("  FAIL:", exc)

    print("\n-- SCENE-REACTIONS: tinted bezel shake stays in sync with the base --")
    for tag in ("a", "b"):
        try:
            print(" ", assert_monitor_frame_shake(
                tag, built[f"monitor_frame_shake_{tag}.png"],
                built["monitor_frame.png"]))
        except AssertionError as exc:
            ok = False
            print("  FAIL:", exc)

    print("\n-- SCENE-REACTIONS: hopping props keep their contact shadow --")
    for react, rest, row in HOP_REACTS:
        try:
            print(" ", assert_hop_shadow_planted(react, rest, row))
        except AssertionError as exc:
            ok = False
            print("  FAIL:", exc)

    print("\n-- SCENE-REACTIONS: beverage burst accounting --")
    for style in BEV_REACT:
        for stage in (0, 1):
            try:
                print(" ", assert_bev_burst_accounting(style, stage))
            except AssertionError as exc:
                ok = False
                print("  FAIL:", exc)

    print("\n-- COIN (HUD currency): round footprint + glint --")
    try:
        print(" ", assert_coin_round("coin.png", built["coin.png"]))
    except AssertionError as exc:
        ok = False
        print("  FAIL:", exc)
    try:
        print(" ", assert_coin_glint("coin.png", built["coin.png"]))
    except AssertionError as exc:
        ok = False
        print("  FAIL:", exc)

    print("\n-- COIN: click-react frame diffs (rest vs react, react pair) --")
    for pair in COIN_REACT_PAIRS:
        try:
            print(" ", check_frame_diff(*pair))
        except AssertionError as exc:
            ok = False
            print("  FAIL:", exc)

    print("\n-- derived store thumbnails --")
    plan = thumbnail_plan()
    thumb_names = set()
    derived_count = 0
    override_ok = True
    for thumb_name, sources in sorted(plan.items()):
        thumb_names.add(thumb_name)
        if thumb_name in THUMB_OVERRIDES:
            thumb_sprite = THUMB_BUILDERS[thumb_name]()
            thumb_sprite.save(thumb_name)
            passed, detail = check(thumb_name, 40, 40)
            override_ok = override_ok and passed
            print(f"  {thumb_name:<32} <- authored override builder      {detail}")
            continue
        src_img = Image.open(ASSETS / sources[0]).convert("RGBA")
        thumb = derive_thumbnail(src_img)
        thumb.save(ASSETS / thumb_name)
        derived_count += 1
        print(f"  {thumb_name:<32} <- {sources[0]:<28} {thumb.size[0]}x{thumb.size[1]}")
    ok = ok and override_ok
    print(f"{derived_count} thumbnails derived, {len(THUMB_OVERRIDES)} authored override(s) "
          "(chair_basic/racer/exec/antigrav form+detail - see THUMB_OVERRIDES comment)")

    expected = SPEC_NAMES | COIN_NAMES | thumb_names
    removed = cleanup_stale(expected)
    print("\n-- stale file cleanup --")
    if removed:
        for name in removed:
            print(f"  removed {name}")
    else:
        print("  nothing stale found")

    on_disk = {p.name for p in ASSETS.glob("*.png")}
    if on_disk != expected:
        ok = False
        missing = expected - on_disk
        extra = on_disk - expected
        if missing:
            print("MISSING:", sorted(missing))
        if extra:
            print("UNEXPECTED EXTRA:", sorted(extra))
    else:
        print(f"\napp/assets/ contains exactly {len(expected)} files "
              f"({len(SPEC)} manifest + {len(COIN_NAMES)} coin + "
              f"{len(thumb_names)} thumbnails)")

    if not ok:
        print("\nSELF-CHECK FAILED", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
