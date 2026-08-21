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
count, the chair hard-region constraint, the two frame-difference rules, the
monitor's exact screen rect) and exits non-zero if any sprite fails it, so a
botched edit here cannot quietly ship a blank, off-palette, or mis-anchored
asset. It also deletes any stale file in assets/ that is not part of the v2
manifest, so a rewrite like this one cannot leave v0.2 corpses behind.
"""

from __future__ import annotations

import sys
from pathlib import Path

from PIL import Image

REPO = Path(__file__).resolve().parent.parent
ASSETS = REPO / "assets"

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
                      x1: int, y1: int, extra: float = 0.0, base: float = 3.0,
                      sway: float = 0.8) -> None:
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


# The 45-file v2 manifest (docs/art-direction.md, "Sprite manifest v2"), used
# both to author and to verify. Keep in sync with the doc; the self-check
# compares against this and the final assets/ directory listing must equal
# exactly these files plus their derived thumbnails - nothing else survives.
SPEC: list[tuple[str, int, int]] = [
    # Fixed scenery (3)
    ("room_back.png", 320, 200),
    ("desk_back.png", 320, 58),
    ("monitor.png", 132, 64),
    # Developer (12), all 88x104, identical canvas and anchor
    ("dev_form_idle.png", 88, 104),
    ("dev_form_type_a.png", 88, 104),
    ("dev_form_type_b.png", 88, 104),
    ("dev_form_sleep.png", 88, 104),
    ("dev_base_idle.png", 88, 104),
    ("dev_base_type_a.png", 88, 104),
    ("dev_base_type_b.png", 88, 104),
    ("dev_base_sleep.png", 88, 104),
    ("hoodie_classic.png", 88, 104),
    ("hoodie_zip.png", 88, 104),
    ("hoodie_tech.png", 88, 104),
    ("hoodie_cloak.png", 88, 104),
    # Chair (8), bottom-centre anchored at room row 200 / x=160
    ("chair_basic_form.png", 144, 108),
    ("chair_basic_detail.png", 144, 108),
    ("chair_racer_form.png", 148, 112),
    ("chair_racer_detail.png", 148, 112),
    ("chair_exec_form.png", 152, 118),
    ("chair_exec_detail.png", 152, 118),
    ("chair_antigrav_form.png", 136, 100),
    ("chair_antigrav_detail.png", 136, 100),
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
]
SPEC_NAMES = {name for name, _, _ in SPEC}
assert len(SPEC) == 45, f"manifest drifted: {len(SPEC)} entries, expected 45"

# `*_form.png` files are palette-purity EXEMPT and ramp-purity CHECKED instead
# (art-direction "Palette-purity exception (the only one)"). Covers both the
# chair naming (`chair_<style>_form.png`) and the developer's
# (`dev_form_<frame>.png`).
FORM_FILES = {name for name in SPEC_NAMES
              if name.endswith("_form.png") or name.startswith("dev_form_")}

# The two frame-difference rules this generator must prove, not just assert
# by convention (art-direction "Character rules" / buddy manifest note).
TYPING_PAIR = ("dev_form_type_a.png", "dev_form_type_b.png")
BLINK_PAIR = ("buddy_bot_a.png", "buddy_bot_b.png")


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


# --------------------------------------------------------------------------
# Developer (12): dev_form_*, dev_base_*, hoodie_*
# --------------------------------------------------------------------------
#
# 88x104, camera behind and slightly above. Anchor is FIXED at room (116, 92)
# for every frame and every hoodie style - frames are same-canvas overlays,
# never re-anchored (art-direction "Developer anchor").
#
# REDESIGNED after a real quality-gate failure: the first version put a
# narrow dome between two similarly-sized, similarly-toned arm/hand columns,
# which read as a double-hump silhouette / "two people back-to-back" rather
# than one hooded figure, and had a hard 1-2px ramp seam down the exact
# centre plus a detached-looking dark arm band at the left edge. The fix
# below is built to survive a literal silhouette test (fill every opaque
# pixel black, it must read as one rounded head on wider shoulders):
#
#   * ONE dome, made unmistakably the largest, roundest shape on the canvas
#     (38px wide at its widest, 28 rows tall) - big enough that the thin
#     (12px) arm columns beside it can never be mistaken for a second head.
#   * Shoulders are a SINGLE step wider than the dome immediately below it -
#     monotonically non-decreasing width the whole way down, so there is no
#     local dip anywhere in the silhouette (a dip is what a "notch" is).
#   * Arms are constant-width rectangles in the SAME base tone as the body
#     (no contrasting dark band), and their lower segment (near the
#     shoulder) never moves between frames, so they always connect - no
#     "detached band" floating apart from the figure.
#   * No seam: shading is one wide (12px), soft, single-step-darker column
#     under the dome/shoulder centre, never a 1-2px line, plus a small
#     rounded highlight/shadow rim on the dome itself.
#
# The keyboard sits ABOVE the dome in room space (keyboard bottom = room row
# 114 = local row 22 at this anchor), which is why the hands - which must
# rest on the keyboard - sit near the TOP of this canvas while the dome (the
# head) sits below them: the hands are not "near the top" out of anatomical
# confusion, they are reaching up and out to either side of the head toward
# a keyboard that is drawn in front of this sprite at render time. What
# changed is not that arrangement (it is load-bearing: art-direction's own
# "dome's apex sits below the keyboard's bottom row") but how visually
# dominant the dome is over the arms that flank it.
#
# All geometry below is expressed as (y, x0, x1) row spans for the LEFT half
# only; the right half is the mirror `87 - x` of each span (canvas width 88,
# symmetric about x=43.5), so left/right can never drift out of sync with
# each other - they are the same numbers, reflected.

DEV_W, DEV_H = 88, 104
DEV_MIRROR = DEV_W - 1   # 87; mirrored x = DEV_MIRROR - x

# Hand (dev_base skin, ~10x8) sits inside a WIDE fabric block (dev_form),
# not a thin column - see below for why. The block is split into an UPPER
# segment (rows 15..22, alongside the hand) that moves per frame - exactly
# the "forearm/sleeve/cuff/hand" pixels the frame-difference rule names -
# and a LOWER segment (rows 23..30) that NEVER moves, so it always overlaps
# the dome's own edge regardless of which frame's offset is applied above.
#
# Three earlier attempts, in order, and why each failed the silhouette
# test:
#   1. Thin column at the canvas edge (x 4..15): barely touched the
#      shoulder below it (a 2px thread, not a join) - read as a detached
#      bar.
#   2. Thin column moved inward (x 14..25), hand 8px tall starting at
#      row 9: connected fine, but the hand rose 14 ROWS above the dome's
#      apex with open space on both sides - two tall, thin, separated
#      peaks either side of a shallow dip. Shortening the hand's rise to
#      "only" 8 rows above the apex (row 15) was still enough of a peak.
#   3. Making the block WIDE (28px, x 12..39) to remove the peak shape
#      instead overshot into swallowing the dome entirely: the dome's own
#      apex (only 8px wide at row 23) is narrower than a 28px block, so
#      the two merge into one flat-topped mass with no rounding visible at
#      all - a castle-turret silhouette, not a head.
#
# The fix keeps the THIN column (10px, reads as "arm" by contrast with the
# 38-60px dome/shoulder, unlike attempt 3) but keeps the hand's rise above
# the dome's apex SMALL - 4 rows (row 19 to row 23), not 8-14 - so there is
# no tall independent peak (attempts 1-2's failure) and no swallowing
# (attempt 3's, since the column stays narrow). The column runs a single
# constant rect from the hand straight down to the shoulder (row 51): it
# is nested inside the shoulder's own x-range the whole way, so the join
# at the bottom is the column's full width, and by row 31 (where the dome
# reaches its constant width) the column's right edge lands exactly on the
# dome's own left edge, so arm and dome are touching (not just adjacent)
# for the entire lower two-thirds of the dome's height. The only place
# arm and dome are NOT touching is rows 23..30, where the dome is still
# narrower than its final width - an intentional, shrinking, expected gap
# (space between a raised arm and the head it is beside), not a floating
# element, because the column is solidly anchored to the shoulder below it
# regardless.
_HAND_BASE = (16, 25, 19, 22)          # x0, x1, y0, y1 - the actual skin cluster
_ARM_UPPER_X = (16, 25)                 # x0, x1 - moves per frame, rows 19..22
_ARM_UPPER_Y = (19, 22)
_ARM_LOWER_X = (16, 25)                 # unshifted, runs all the way to the shoulder
_ARM_LOWER_Y = (23, 50)

# The dome (head): apex at row 23 - one row below the keyboard's bottom edge
# (local row 22), which is the latest it is allowed to start. Half-widths
# widen smoothly (8 rows of rounding) then hold constant for a long,
# unmistakably round cylinder body, so the head reads as one big shape, not
# a pointed cap.
_DOME_HALFWIDTH = [
    (23, 4), (24, 7), (25, 10), (26, 12), (27, 14), (28, 16), (29, 17), (30, 18),
] + [(y, 19) for y in range(31, 51)]        # rows 31..50, constant

# Shoulders: ONE step wider than the dome's own width, starting the row
# right after the dome ends - a single monotonic widening, never a dip, so
# there is no "notch" anywhere in the outline.
_SHOULDER_Y0 = 51
_SHOULDER_X = (14, 73)               # width 60, clearly wider than the dome

FRAME_ARM_OFFSET = {
    "idle": (0, 0),
    "type_a": (-1, 1),
    "type_b": (1, -1),
    "sleep": (10, 10),                # arms fallen off the keys, dropped and inward
}
DOME_DY = {"idle": 0, "type_a": 0, "type_b": 0, "sleep": 3}     # "tipped forward"
SHOULDER_DY = {"idle": 0, "type_a": 0, "type_b": 0, "sleep": 4}


def _dev_rows(frame: str):
    """Yield every (y, x0, x1) span of the developer's fabric silhouette for
    `frame`, LEFT-side spans already expanded to their mirrored right-side
    partner. Shared by the form (ramp-shaded fill), the base (outline +
    hair) and the hoodie style overlay (dome/shoulder rows only) builders,
    so the silhouette is defined exactly once.
    """
    adx, ady = FRAME_ARM_OFFSET[frame]
    ddy = DOME_DY[frame]
    sdy = SHOULDER_DY[frame]

    # Upper arm (moves per frame). Sleep moves the WHOLE arm (see below) so
    # only offset the upper segment here for the other three frames.
    ux0, ux1 = _ARM_UPPER_X
    uy0, uy1 = _ARM_UPPER_Y
    for y in range(uy0, uy1 + 1):
        yield (y + ady, ux0 + adx, ux1 + adx)
        yield (y + ady, DEV_MIRROR - (ux1 + adx), DEV_MIRROR - (ux0 + adx))

    # Lower arm: fixed for idle/type_a/type_b (guarantees the shoulder
    # connection never breaks); sleep drags it down+inward WITH the upper
    # arm, since a slumped pose reads as arms falling away from the body,
    # not as arms staying perfectly attached.
    lx0, lx1 = _ARM_LOWER_X
    ly0, ly1 = _ARM_LOWER_Y
    lo_dx, lo_dy = (adx, ady) if frame == "sleep" else (0, 0)
    for y in range(ly0, ly1 + 1):
        yield (y + lo_dy, lx0 + lo_dx, lx1 + lo_dx)
        yield (y + lo_dy, DEV_MIRROR - (lx1 + lo_dx), DEV_MIRROR - (lx0 + lo_dx))

    for y, hw in _DOME_HALFWIDTH:
        cx = 44
        yield (y + ddy, cx - hw, cx + hw - 1)
    for y in range(_SHOULDER_Y0, DEV_H):
        x0, x1 = _SHOULDER_X
        yield (y + sdy, x0, x1)


def _dev_hand_rects(frame: str):
    """Left/right skin hand rects (dev_base only) for `frame`."""
    adx, ady = FRAME_ARM_OFFSET[frame]
    x0, x1, y0, y1 = _HAND_BASE
    left = (x0 + adx, x1 + adx, y0 + ady, y1 + ady)
    right = (DEV_MIRROR - (x1 + adx), DEV_MIRROR - (x0 + adx), y0 + ady, y1 + ady)
    return left, right


def _dev_fabric_mask(frame: str) -> list[list[bool]]:
    grid = [[False] * DEV_W for _ in range(DEV_H)]
    for y, x0, x1 in _dev_rows(frame):
        if 0 <= y < DEV_H:
            for x in range(max(0, x0), min(DEV_W - 1, x1) + 1):
                grid[y][x] = True
    for rect in _dev_hand_rects(frame):
        x0, x1, y0, y1 = rect
        for y in range(max(0, y0), min(DEV_H - 1, y1) + 1):
            for x in range(max(0, x0), min(DEV_W - 1, x1) + 1):
                grid[y][x] = True
    return grid


# Ramp shading rule for dev_form - HERO-FIDELITY REWRITE.
#
# The v2.0 version used only 2 of the 5 ramp steps (ramp3/ramp4, plus a
# handful of manual ramp5 dots) in one flat rim-and-centre-band pattern.
# This rewrite uses all 5 steps and drives them from one continuous value
# `v` (0.0 = ramp1, deepest fold .. 4.0 = ramp5, specular), ordered-dithered
# between its two nearest steps by `ramp_dither` - the "stepped contours,
# anti-banded with dithering" the fidelity pass asks for, instead of a flat
# pick per region.
#
# ONE stated light direction for the whole sprite (matches the room: the
# monitor's glow sits beyond the character in screen space, so from the
# camera - behind the character - the figure is silhouetted against it,
# rim-lit around the top/left of the dome and shoulder where the glow
# wraps the edge; a softer ambient key from the upper-left of frame gives
# the same side a broader highlight, the opposite/right side falls into
# shadow). This is why every gradient below reads DARKER as x increases
# past the centreline (cx=44) and brightest right at the outer silhouette
# edge on the small-x side.
#
# The arm rectangles stay close to flat ramp4 (see the module docstring
# above on the silhouette failures a contrasting arm tone caused) - only a
# 1px inner seam and a cuff band are added, never a full-arm recolour.
_CENTRE_BAND = (38, 50)   # x0, x1 - kept as the shoulders' soft centre reference


def _dev_ramp_for(x: int, y: int, frame: str, dome_row: dict[int, int]) -> str:
    """Per-pixel ramp value, v2 (post-hero-pass regression fix).

    The first hero pass swung the base cross-section gradient across
    almost the FULL 0..4 ramp range (light side near ramp4, shadow side
    near ramp0), so roughly half the sprite sat at ramp1/ramp2 - fine on
    the neutral mockup, but multiplied against the game's actual DEFAULT
    hoodie tint (`indigo` #6a5aa0, see app/internal/game/catalog.go) that
    crushed to near-black across half the figure: a void, not a shaded
    garment. Root cause + fix, per the art-direction ramp table itself
    ("author the large flat areas at step 4 (ramp4/83%), so a tint has
    HEADROOM for a highlight" - ramp1/ramp2 are for small deliberate deep
    accents, not a whole shadow HALF): the base cross-section gradient
    below now stays anchored on ramp4 (v=3.0) and only sways +/-0.9,
    landing in ramp2..ramp5 - always multiplies to a visibly graduated
    mid-value garment at the default tint. ramp0/ramp1 are reserved for
    the small, explicitly localized accents (neck AO, fold creases,
    shoulder blades) that stack a further, bounded subtraction on top of
    that anchored base - never the dominant tone of a whole region.

    The rim-light bonus (below) is deliberately strong enough to reach
    ramp5 (undarkened tint) right at the lit silhouette edge: that edge is
    what still separates the figure from the wall even at the DARKEST
    purchasable tint (`slate` #2b2b33, a near-black "Classic Black"),
    where internal folds necessarily crush since the tint itself is
    almost black at every ramp step - a solid black hoodie reading mostly
    as silhouette-plus-rim is the correct pixel-art result there, not a
    bug to chase further.
    """
    cx = 44
    ddy = DOME_DY[frame]
    sdy = SHOULDER_DY[frame]

    if y in dome_row:
        hw = dome_row[y]
        ry = y - ddy                                  # unshifted dome row, 23..50
        rel = (x - cx) / hw if hw else 0.0             # -1 (lit rim) .. +1 (shadow rim)
        edge = hw - abs(x - cx)                        # 0 at the silhouette edge

        v = 3.0 - 0.9 * rel                             # anchored on ramp4, sways +/-0.9
        if ry in (33, 41):
            v -= 0.45                                   # two soft fabric fold creases
        if rel < -0.3 and edge <= 3:
            v += (4 - edge) * 0.55                      # rim light wrapping the lit side
        if ry >= 47:
            v -= 1.0 * min(1.0, (ry - 46) / 4)          # neck AO, dome side
        return ramp_dither(x, y, v)

    x0s, x1s = _SHOULDER_X
    if x0s <= x <= x1s:
        sy = y - sdy                                    # unshifted shoulder row
        rel = (x - cx) / ((x1s - x0s) / 2)
        v = 3.0 - 0.8 * rel                              # anchored on ramp4, sways +/-0.8
        if sy <= 54:
            v -= 1.0 * max(0.0, (54 - sy) / 4)          # neck AO, shoulder side
        for bx in (cx - 20, cx + 20):                    # shoulder-blade patches
            if abs(x - bx) <= 7 and 56 <= sy <= 72:
                d = ((x - bx) ** 2 + (sy - 64) ** 2) ** 0.5
                if d <= 8:
                    v -= 0.45 * (1 - d / 8)
        for fx in (cx - 14, cx, cx + 14):                # drape fold lines, back panel
            if abs(x - fx) <= 1 and sy >= 60:
                v -= 0.3
        if x <= x0s + 2:
            v += 0.7                                     # rim light down the lit edge
        v -= 0.3 * (sy - _SHOULDER_Y0) / (DEV_H - _SHOULDER_Y0)   # gentle ambient falloff
        return ramp_dither(x, y, v)

    # Arms (the only remaining fabric pixels once dome/shoulder are ruled
    # out): near-flat ramp4, with a thin inner seam and a cuff fold.
    v = 3.0
    if x in (25, 62):
        v -= 0.5                                         # inner sleeve seam
    if _ARM_UPPER_Y[0] <= y <= _ARM_UPPER_Y[0] + 1:
        v -= 0.35                                        # cuff fold above the hand
    return ramp_dither(x, y, v)


def build_dev_form(frame: str) -> Sprite:
    s = Sprite(DEV_W, DEV_H, palette=RAMP)
    ddy = DOME_DY[frame]
    dome_row = {y + ddy: hw for y, hw in _DOME_HALFWIDTH}
    for y, x0, x1 in _dev_rows(frame):
        for x in range(x0, x1 + 1):
            s.dot(x, y, _dev_ramp_for(x, y, frame, dome_row))
    return s


def _dev_hair_crescent(s: Sprite, frame: str) -> None:
    """A thin `hair` crescent on the dome's own upper edge - the only
    interior detail the character has (art-direction "Character rules": the
    hood is a dome, not a head, no face, no ears). Kept on the dome itself,
    not the shoulders, and small enough (a few px) that it cannot compete
    with the dome for "head" status the way the old arm columns did."""
    ddy = DOME_DY[frame]
    y = 27 + ddy
    for x in range(40, 46):
        s.dot(x, y, "hair")


def _dev_paint_hand(s: Sprite, x0: int, x1: int, y0: int, y1: int) -> None:
    """A `skin` cluster with a finger suggestion: 2 thin `shadow` notches
    across the top (fingertip) row split the ~10px-wide cluster into 3
    finger-width lobes, the only interior detail the 2-`skin`-cluster
    budget (art-direction "Character rules") allows at this size. The
    bottom row(s) stay a solid skin base - the palm resting on the keys."""
    s.rect(x0, y0, x1, y1, "skin")
    w = x1 - x0 + 1
    for i in (1, 2):
        fx = x0 + (w * i) // 3
        s.dot(fx, y0, "shadow")
        if y1 > y0:
            s.dot(fx, y0 + 1, "shadow")


def build_dev_base(frame: str) -> Sprite:
    s = Sprite(DEV_W, DEV_H)
    mask = _dev_fabric_mask(frame)
    outline_from_mask(s, mask, "shadow")
    _dev_hair_crescent(s, frame)
    for x0, x1, y0, y1 in _dev_hand_rects(frame):
        _dev_paint_hand(s, x0, x1, y0, y1)
    if frame == "sleep":
        # A small `z` above the (now lower) dome's right shoulder - the
        # smallest readable sleep cue at this size. A true zigzag (top bar,
        # then a diagonal step down-left, then a bottom bar), not a
        # symmetric bar-dot-bar glyph, which reads as an "I" rather than a
        # "z" at this size.
        s.hline(13, 60, 62, "cream")
        s.dot(62, 14, "cream")
        s.dot(61, 15, "cream")
        s.dot(60, 16, "cream")
        s.hline(17, 60, 62, "cream")
    return s


# --------------------------------------------------------------------------
# Hoodie style overlays (4): frame-independent, back panel + hood only
# --------------------------------------------------------------------------
#
# Per art-direction "Character rules": these may only paint the back panel
# and hood, which are static across all four frames - so they are authored
# once, against the `idle` geometry (dome rows 23..50, shoulder rows 51+),
# and never reference the per-frame arm/hand offset. (The sleep frame's own
# 3-4px drop of that same geometry is therefore not mirrored by the overlay
# - a deliberate, documented v2 simplification; see the handoff report.)
# Coordinates below were re-checked against the redesigned dome (x25..62 at
# its widest, rows 23..50) and shoulder (x14..73, rows 51..103) so every
# mark lands on fabric, not in the transparent margin outside it.

def build_hoodie_classic() -> Sprite:
    """Shadow drawstrings hanging from the hood opening, kangaroo-pocket
    side seams on the lower back."""
    s = Sprite(DEV_W, DEV_H)
    s.vline(40, 25, 37, "shadow")
    s.dot(40, 38, "shadow")
    s.vline(47, 25, 39, "shadow")
    s.dot(47, 40, "shadow")
    s.vline(24, 62, 92, "shadow")
    s.vline(63, 62, 92, "shadow")
    return s


def build_hoodie_zip() -> Sprite:
    """Metal zip teeth up the centre seam of the hood, a cream pull tab."""
    s = Sprite(DEV_W, DEV_H)
    for y in range(26, 96, 2):
        s.dot(43, y, "metal")
        s.dot(44, y, "metal")
    s.rect(42, 90, 45, 92, "cream")
    return s


def build_hoodie_tech() -> Sprite:
    """A desk_dark cross-strap over the shoulders with a metal buckle, one
    screen reflective stripe."""
    s = Sprite(DEV_W, DEV_H)
    s.line(18, 58, 68, 78, "desk_dark")
    s.line(19, 58, 69, 78, "desk_dark")
    s.rect(40, 66, 47, 70, "metal")
    s.hline(58, 22, 65, "screen")
    return s


def build_hoodie_cloak() -> Sprite:
    """Gold hem trim across the shoulders, a draped shadow fold pattern down
    the back panel."""
    s = Sprite(DEV_W, DEV_H)
    s.hline(66, 16, 71, "gold")
    for x in (22, 32, 44, 55, 65):
        s.vline(x, 68, 68 + (x % 3) * 6 + 12, "shadow")
    return s


# --------------------------------------------------------------------------
# Chair (8): 4 styles x form/detail, bottom-centre anchored at room (160,200)
# --------------------------------------------------------------------------
#
# HARD constraint (art-direction "Derivation rules"): above room row 120, a
# chair sprite may only paint pixels at room x < 116 or x > 204 - so the
# keyboard and hands, which sit between those columns, can never be
# occluded by any chair style at any height. Converted to LOCAL sprite
# coordinates (top = 200 - h, so room row 120 is local row h - 80; centred at
# local x = w/2, so room x 116/204 are local w/2 - 44 / w/2 + 44):

def chair_forbidden_zone(w: int, h: int) -> tuple[int, int, int] | None:
    """Return (y_below, x_lo, x_hi) - the local (y, x-range) that must stay
    fully transparent - or None if the style is short enough that its whole
    canvas sits below room row 120 already."""
    y_below = h - 80
    if y_below <= 0:
        return None
    return y_below, w // 2 - 44, w // 2 + 44


def assert_chair_region(name: str, s: Sprite) -> str:
    zone = chair_forbidden_zone(s.w, s.h)
    if zone is None:
        return f"{name}: no restricted rows (top of canvas already below room row 120)"
    y_below, x_lo, x_hi = zone
    mask = s.mask()
    violations = [(x, y) for y in range(0, y_below) for x in range(x_lo, x_hi + 1)
                  if mask[y][x]]
    if violations:
        raise AssertionError(
            f"{name}: {len(violations)} pixel(s) violate the chair hard region "
            f"(local y<{y_below}, x in [{x_lo},{x_hi}]); first={violations[0]}")
    return f"{name}: rows y<{y_below} clear outside x[{x_lo},{x_hi}] - OK"


def _chair_star_base(s: Sprite, cx: int, hub_y: int, foot_y: int, spread: int) -> None:
    """A simplified 5-star caster base: a hub under the gas cylinder and 5
    short spokes radiating to foot blocks."""
    feet_x = [cx - spread, cx - spread // 2, cx, cx + spread // 2, cx + spread]
    for fx in feet_x:
        s.line(cx, hub_y, fx, foot_y, "metal")
        s.rect(fx - 2, foot_y, fx + 2, foot_y + 1, "metal")
    s.hline(foot_y + 2, feet_x[0] - 4, feet_x[-1] + 4, "shadow")   # contact shadow


# --------------------------------------------------------------------------
# Chair (8): *_form (ramp, tintable) + *_detail (palette). BEHIND-VIEW SEAT
# REDESIGN. The prior chairs read as a flat mat around the developer because
# the sprite never rose behind his upper body: the backrest top sat at or
# below the head. Every chair below now has (1) a tall backrest slab directly
# behind the torso, wider than the shoulders so it shows on both sides,
# (2) two raised upper-back corners / headrest bolsters rising ABOVE the head
# in the wing zone (the only place room y<120 may be painted, since the hard
# chair region protects the keyboard across the centre), so the head nestles
# between them and reads as resting against the seatback, and (3) armrests
# protruding at the sides at elbow level. Bottom-centre anchored at room row
# 200 / x=160; each CHAIR_RECT in geometry.ts places the sprite so the
# backrest reaches up to the shoulder/head band (top near or above the dev's
# own DEV_RECT.top=92).
#
# COORDINATE NOTE: local row r -> room y = (200 - h) + r; local x -> room x =
# (160 - w/2) + x. The hard region (assert_chair_region) forbids any pixel at
# local rows < h-80 (room y < 120) inside local x in [w/2-44, w/2+44]
# (room x in [116, 204]) - so above room y120 only the side "wing" columns
# outside that band may be painted. Every builder below keeps its raised
# corners/bolsters strictly outside it; the self-check enforces this.


def _chair_seat_well_ao(s: Sprite, cx: float, half: float, x0: int, y: int,
                         x1: int) -> None:
    """A one-row dithered AO seam where the seat pan meets the backrest, so
    the two forms read as a seat tucked under a back, not one flat panel."""
    for x in range(x0, x1 + 1):
        rel = max(-1.0, min(1.0, (x - cx) / half)) if half else 0.0
        s.dot(x, y, ramp_dither(x, y, 3.0 - 0.8 * rel - 0.8))


def build_chair_basic_form() -> Sprite:
    """Mesh task chair, behind-view. A tall mesh backrest slab behind the
    torso with two raised upper-back corners rising above the head, a seat
    pan tucked beneath it, and armrests protruding at the sides. Dithered
    cross-section gradient anchored on ramp4 (tint headroom - this chair's
    DEFAULT tint is `slate`, near-black, so the anchor + rim light are what
    keep the fresh-player chair a readable mid-value mesh, not a void)."""
    s = Sprite(144, 108, palette=RAMP)
    cx, half = 72.0, 68.0
    # Backrest slab behind the torso+head (rows 30..74 = room y120..164).
    chair_shade_rect(s, cx, half, 14, 32, 130, 74)             # main back slab
    chair_shade_rect(s, cx, half, 20, 30, 124, 31)             # rounded top edge
    # Raised upper-back corners rising ABOVE the head (rows 10..31), kept in
    # the wing zone (local x <=26 / >=118, since room y<120 there). Edges are
    # pulled 1px inside the x<28/x>116 boundary so the detail layer's 1px
    # outline halo (at x27/x117) also stays clear of the hard region.
    chair_shade_rect(s, cx, half, 14, 10, 26, 31)              # left post
    chair_shade_rect(s, cx, half, 118, 10, 130, 31)            # right post
    chair_shade_rect(s, cx, half, 14, 10, 26, 11, extra=0.6)   # post top cap AO
    chair_shade_rect(s, cx, half, 118, 10, 130, 11, extra=0.6)
    # Armrests protruding at elbow level (rows 58..76), sticking out past the
    # slab edges to the sides.
    chair_shade_rect(s, cx, half, 2, 58, 20, 76)               # left armrest
    chair_shade_rect(s, cx, half, 124, 58, 142, 76)            # right armrest
    # Seat pan, tucked under the back (rows 75..88), a touch wider than slab.
    chair_shade_rect(s, cx, half, 10, 75, 134, 88)
    chair_shade_rect(s, cx, half, 110, 79, 134, 88, extra=0.3) # seat well AO, far corner
    _chair_seat_well_ao(s, cx, half, 16, 75, 128)
    # Mesh weave: a sparse grid of small punctures, one ramp step darker.
    for wy in range(35, 72, 5):
        for wx in range(20, 125, 7):
            rel = max(-1.0, min(1.0, (wx - cx) / half))
            s.dot(wx, wy, ramp_dither(wx, wy, 3.0 - 0.8 * rel - 0.7))
    chair_rim_light(s)
    return s


def build_chair_basic_detail() -> Sprite:
    s = Sprite(144, 108)
    bevel_rect(s, 64, 89, 80, 100, "metal", "wall_light", "shadow")   # gas cylinder
    _chair_star_base(s, 72, 100, 104, 54)
    bevel_rect(s, 2, 58, 20, 62, "desk_dark", "wall_light", "shadow")   # armrest caps
    bevel_rect(s, 124, 58, 142, 62, "desk_dark", "wall_light", "shadow")
    fab = build_chair_basic_form()
    outline_from_mask(s, union_mask(fab.mask(), s.mask()), "shadow")
    return s


def build_chair_racer_form() -> Sprite:
    """Gaming chair, behind-view. Two TALL bolstered wings rising well above
    the head (a racing bucket seat's high shoulder bolsters), a waisted
    backrest between/below them, a seat pan, and armrests. Same ramp4-anchored
    dithered gradient + rim light as every chair."""
    s = Sprite(148, 112, palette=RAMP)
    cx, half = 74.0, 70.0
    # Tall bolstered wings (rows 4..40) flanking and rising above the head.
    chair_shade_rect(s, cx, half, 8, 6, 28, 40)               # left bolster
    chair_shade_rect(s, cx, half, 120, 6, 140, 40)            # right bolster
    chair_shade_rect(s, cx, half, 8, 6, 28, 7, extra=0.6)     # bolster top cap AO
    chair_shade_rect(s, cx, half, 120, 6, 140, 7, extra=0.6)
    # Waisted backrest between the bolsters. Its top row is one below the hard
    # region (y_below=32) so the detail layer's outline halo at row 32 lands
    # on the boundary (allowed), not inside it.
    chair_shade_rect(s, cx, half, 24, 33, 124, 80)            # back mass
    chair_shade_rect(s, cx, half, 20, 60, 128, 80)            # lower back, broader
    chair_shade_rect(s, cx, half, 44, 41, 104, 57, extra=0.35)  # waist AO, subtle inset shade
    # Armrests (rows 62..80).
    chair_shade_rect(s, cx, half, 2, 62, 22, 80)
    chair_shade_rect(s, cx, half, 126, 62, 146, 80)
    # Seat pan (rows 80..94).
    chair_shade_rect(s, cx, half, 12, 80, 136, 94)
    chair_shade_rect(s, cx, half, 110, 84, 136, 94, extra=0.3)  # seat well AO
    _chair_seat_well_ao(s, cx, half, 20, 80, 128)
    chair_rim_light(s)
    return s


def build_chair_racer_detail() -> Sprite:
    s = Sprite(148, 112)
    bevel_rect(s, 66, 95, 82, 106, "metal", "wall_light", "shadow")   # gas cylinder
    _chair_star_base(s, 74, 106, 110, 58)
    bevel_rect(s, 2, 62, 22, 66, "desk_dark", "wall_light", "shadow")   # armrest caps
    bevel_rect(s, 126, 62, 146, 66, "desk_dark", "wall_light", "shadow")
    for x0, x1 in ((10, 26), (122, 138)):              # double stitching up the bolsters
        s.vline(x0 + 2, 10, 38, "cream")
        s.vline(x1 - 2, 10, 38, "cream")
    fab = build_chair_racer_form()
    outline_from_mask(s, union_mask(fab.mask(), s.mask()), "shadow")
    return s


def build_chair_exec_form() -> Sprite:
    """Executive leather chair, behind-view. The tallest back of the four:
    very tall tufted headrest wings rising high above the head, a wide tufted
    leather back panel behind the torso, wide padded armrests, and a broad
    seat. Same ramp4-anchored dithered gradient + rim light."""
    s = Sprite(152, 118, palette=RAMP)
    cx, half = 76.0, 72.0
    # Very tall headrest wings (rows 4..44).
    chair_shade_rect(s, cx, half, 8, 6, 30, 44)               # left headrest wing
    chair_shade_rect(s, cx, half, 122, 6, 144, 44)            # right headrest wing
    chair_shade_rect(s, cx, half, 8, 6, 30, 7, extra=0.6)     # wing top cap AO
    chair_shade_rect(s, cx, half, 122, 6, 144, 7, extra=0.6)
    # Wide tufted leather back panel. Top row one below the hard region
    # (y_below=38) so the detail outline halo at row 38 lands on the boundary.
    chair_shade_rect(s, cx, half, 14, 39, 138, 86)            # back panel
    # Wide padded armrests (rows 64..86).
    chair_shade_rect(s, cx, half, 0, 64, 26, 86)
    chair_shade_rect(s, cx, half, 126, 64, 152, 86)
    # Broad seat (rows 86..100).
    chair_shade_rect(s, cx, half, 6, 86, 146, 100)
    chair_shade_rect(s, cx, half, 118, 90, 146, 100, extra=0.3)  # seat well AO
    _chair_seat_well_ao(s, cx, half, 16, 86, 136)
    chair_rim_light(s)
    return s


def build_chair_exec_detail() -> Sprite:
    s = Sprite(152, 118)
    for gy in (48, 60, 72):                            # button tufting on the back panel
        for gx in (40, 62, 90, 112):
            s.dot(gx, gy, "gold")
    bevel_rect(s, 0, 64, 26, 70, "desk_dark", "wall_light", "shadow")     # padded armrest tops
    bevel_rect(s, 126, 64, 152, 70, "desk_dark", "wall_light", "shadow")
    bevel_rect(s, 68, 99, 84, 112, "metal", "wall_light", "shadow")       # heavier gas cylinder
    _chair_star_base(s, 76, 112, 116, 62)
    fab = build_chair_exec_form()
    outline_from_mask(s, union_mask(fab.mask(), s.mask()), "shadow")
    return s


def build_chair_antigrav_form() -> Sprite:
    """Anti-gravity pod, behind-view. Even though it floats, it still cradles
    a seated figure: a rounded shell back rises behind the torso with two
    rounded cradle wings curving up beside the head, over a rounded seat pod.
    Built from ramp4-anchored rounded fills so the silhouette reads as a
    curved pod, not a flat lump. Levitation glow ring lives in the detail."""
    s = Sprite(136, 100, palette=RAMP)
    cx, half = 68.0, 64.0
    # Rounded back shell behind the torso. Its centre must not cross the hard
    # region (y_below=20), so the body top is at row 21 (outline halo at row
    # 20 = the boundary) and the rounded top narrows the top two rows rather
    # than bulging a wide ellipse up into the keyboard band.
    chair_shade_rect(s, cx, half, 10, 23, 126, 70)            # back shell body
    chair_shade_rect(s, cx, half, 22, 21, 114, 22)            # rounded shell top, inset
    # Rounded cradle wings curving up beside the head (rows 5..31), in the
    # wing zone (local x <24 / >112).
    chair_shade_ellipse(s, 13, 18, 9, 13, cx, half)           # left cradle wing
    chair_shade_ellipse(s, 123, 18, 9, 13, cx, half)          # right cradle wing
    # Rounded seat pod (rows 66..94).
    chair_shade_ellipse(s, 68, 80, 60, 14, cx, half, extra=0.3)
    _chair_seat_well_ao(s, cx, half, 18, 71, 118)
    chair_rim_light(s)
    return s


def build_chair_antigrav_detail() -> Sprite:
    s = Sprite(136, 100)
    s.outline(40, 90, 95, 93, "screen")               # glow ring, levitation tell
    for x in range(41, 95):
        s.dot(x, 91, bayer_mix(x, 91, 0.4, "screen", "lamp"))   # dithered glow bleed
    s.dots([(20, 92), (68, 96), (116, 92)], "lamp")   # 3 drifting float motes
    s.hline(97, 34, 101, "shadow")                    # ground-facing glow shadow
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


def build_buddy_cat() -> Sprite:
    """HERO-FIDELITY MATCH: the fur reads as a lit-left/shadow-right
    dithered gradient (`hair` toward the dark/shadow side, `desk` - the
    nearest warm-brown palette entry - toward the lit side) instead of a
    flat single-tone silhouette."""
    s = Sprite(28, 30)
    head_rows = ((10, 10, 17), (11, 8, 19), (12, 6, 21), (13, 5, 22),
                 (14, 5, 22), (15, 6, 21), (16, 8, 19))
    for y, x0, x1 in head_rows:
        for x in range(x0, x1 + 1):
            s.dot(x, y, hgrad(x, y, x0, x1, "hair", "desk"))
    s.dot(9, 9, "pot")                        # ear tips
    s.dot(16, 9, "pot")
    s.dots([(20, 13), (21, 14), (22, 15), (21, 16)], "hair")   # tail
    s.dot(20, 13, "desk")                     # tail's own lit root
    s.dot(22, 15, "cream")                    # tail tip
    s.hline(17, 4, 23, "shadow")
    return s


# --------------------------------------------------------------------------
# Builders table + thumbnail catalog
# --------------------------------------------------------------------------

BUILDERS = {
    "room_back.png": build_room_back,
    "desk_back.png": build_desk_back,
    "monitor.png": build_monitor,
    "dev_form_idle.png": lambda: build_dev_form("idle"),
    "dev_form_type_a.png": lambda: build_dev_form("type_a"),
    "dev_form_type_b.png": lambda: build_dev_form("type_b"),
    "dev_form_sleep.png": lambda: build_dev_form("sleep"),
    "dev_base_idle.png": lambda: build_dev_base("idle"),
    "dev_base_type_a.png": lambda: build_dev_base("type_a"),
    "dev_base_type_b.png": lambda: build_dev_base("type_b"),
    "dev_base_sleep.png": lambda: build_dev_base("sleep"),
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
}


def _thumb_chair_generic_form(seat_w: int) -> Sprite:
    """Shared shape for the 3 overridden chair thumbnails: backrest, seat,
    a cylinder and a base foot-line, all clearly inside a 40x40 icon (unlike
    the full sprites, an icon has no hard chair-region constraint to keep
    clear of, since there is no keyboard drawn at this scale)."""
    s = Sprite(40, 40, palette=RAMP)
    half = seat_w // 2
    # Tall backrest with two raised top corners and a head-gap dip between
    # them - the behind-view seatback the full sprites now show.
    s.rect(20 - half, 8, 20 + half, 24, "ramp4")             # back slab
    s.rect(20 - half, 3, 20 - half + 4, 24, "ramp4")         # raised left corner
    s.rect(20 + half - 4, 3, 20 + half, 24, "ramp4")         # raised right corner
    s.rect(20 - half, 8, 20 + half, 10, "ramp3")             # top-edge shading
    s.rect(20 - half - 3, 16, 20 - half - 1, 22, "ramp3")    # left armrest
    s.rect(20 + half + 1, 16, 20 + half + 3, 22, "ramp3")    # right armrest
    s.rect(20 - half + 2, 25, 20 + half - 2, 29, "ramp3")    # seat
    s.rect(18, 30, 22, 35, "ramp2")                          # cylinder
    return s


def build_thumb_chair_basic_form() -> Sprite:
    return _thumb_chair_generic_form(14)


def build_thumb_chair_basic_detail() -> Sprite:
    s = Sprite(40, 40)
    s.hline(34, 10, 30, "metal")
    s.hline(35, 8, 32, "shadow")
    fab = build_thumb_chair_basic_form()
    outline_from_mask(s, union_mask(fab.mask(), s.mask()), "shadow")
    return s


def build_thumb_chair_racer_form() -> Sprite:
    s = _thumb_chair_generic_form(15)
    s.rect(4, 2, 10, 10, "ramp4")                   # bolstered wing tips
    s.rect(30, 2, 36, 10, "ramp4")
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
    s = _thumb_chair_generic_form(15)
    s.rect(3, 1, 10, 14, "ramp4")                   # tall headrest wings
    s.rect(30, 1, 37, 14, "ramp4")
    return s


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


THUMB_BUILDERS = {
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


def cleanup_stale(expected: set[str]) -> list[str]:
    """Delete any file in assets/ that is not part of this run's expected
    output (the 45-file manifest plus its derived thumbnails). This is what
    guarantees assets/ contains EXACTLY the v2 files after a run - the v0.2
    corpses (room_bg.png, dev_idle.png, chair.png, ...) do not survive a
    rewrite by omission, they are actively removed."""
    removed = []
    for path in sorted(ASSETS.glob("*.png")):
        if path.name not in expected:
            path.unlink()
            removed.append(path.name)
    return removed


def main() -> int:
    ASSETS.mkdir(parents=True, exist_ok=True)

    built: dict[str, Sprite] = {}
    for filename, _w, _h in SPEC:
        sprite = BUILDERS[filename]()
        sprite.save(filename)
        built[filename] = sprite

    ok = True
    print(f"{'file':<28}{'size':<9}{'opaque':>8}  {'kind':<5}status")
    for filename, want_w, want_h in SPEC:
        passed, detail = check(filename, want_w, want_h)
        ok = ok and passed
        print(f"{filename:<28}{detail}")

    print(f"\n{len(SPEC)} manifest sprites written to {ASSETS}")

    print("\n-- chair hard-region assertion --")
    for style in CHAIR_STYLES:
        for layer in ("form", "detail"):
            name = f"chair_{style}_{layer}.png"
            try:
                print(" ", assert_chair_region(name, built[name]))
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
    try:
        print(" ", check_frame_diff(*TYPING_PAIR))
    except AssertionError as exc:
        ok = False
        print("  FAIL:", exc)
    try:
        print(" ", check_frame_diff(*BLINK_PAIR, expect_max_px=2))
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

    expected = SPEC_NAMES | thumb_names
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
        print(f"\nassets/ contains exactly {len(expected)} files "
              f"({len(SPEC)} manifest + {len(thumb_names)} thumbnails)")

    if not ok:
        print("\nSELF-CHECK FAILED", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
