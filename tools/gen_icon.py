#!/usr/bin/env python3
"""Generate dexel's application icon set — every file `bundle.icon` names —
as true pixel art, procedurally.

    python3 tools/gen_icon.py                 # write the icon set + self-check
    python3 tools/gen_icon.py --sheet OUT.png # ...and render a review sheet

Writes into `desktop/src-tauri/icons/`:

    32x32.png          M32, verbatim                 } the five files
    128x128.png        M64 x2                        } tauri.conf.json's
    128x128@2x.png     M64 x4  (= 256px)             } bundle.icon lists,
    icon.icns          10 chunks, 16px..1024px       } and nothing else
    icon.ico           7 frames, 16/24/32/48/64/128/256

    icon.png           M64 x16 (= 1024px) — the source image Tauri's own
                       `tauri icon` command re-derives from, kept in sync
    16x16.png 24x24.png 48x48.png 64x64.png 256x256.png 512x512.png
                       the freedesktop hicolor ladder, for the day a .deb or
                       Flatpak wants `usr/share/icons/hicolor/<N>x<N>/apps/`.
                       (32 and 128 are already above under their Tauri names.)

No `Square*Logo` PNGs: `bundle.icon` does not list any, and Tauri only emits
Windows-Store logo assets for a `windows.appx` target this project does not
build. Generating files nothing consumes is how an icon set rots.

Those PNGs are **build artifacts that happen to be committed** (ADR 0004,
`.claude/skills/pixel-art-authoring`): this file is the source of truth, and a
re-run with no source change rewrites the same bytes — `--sheet` aside, the
whole program is a pure function of the constants below. The run ends with a
self-check that exits non-zero if anything fails, so a botched edit here
cannot quietly ship a blank, off-palette, blurry or illegible icon:

  * palette purity and full-opacity of every master, every delivered PNG and
    every embedded .ico / .icns frame;
  * the opaque footprint of each master is EXACTLY its rounded-square tile;
  * the silhouette (tile, hood, cavity, eyes, drawstrings) is mirror-symmetric
    to the pixel, while the shading deliberately is not;
  * measured WCAG contrast ratios for the three reads the icon lives or dies
    by (hood against tile, eyes against cavity, tile against a black taskbar);
  * every .ico frame and every .icns chunk decodes back to the exact pixels
    of the master it came from — the assertion that catches a silent resample;
  * M16 differs from a naive downscale of M64 by more than a quarter of its
    pixels, i.e. the 16px master really was drawn rather than shrunk.

Pure Pillow, no system tools: `iconutil` is macOS-only (the previous version
of this file called it and therefore could not run on the machine that
builds), so the .icns container is written here, byte for byte, in the exact
shape `iconutil -c icns` produces — ten chunks, no TOC, PNG payloads for the
eight modern slots and RLE-compressed ARGB for `ic04`/`ic05`.

--------------------------------------------------------------------------
THE MARK: dexel, hooded, lit by the screen
--------------------------------------------------------------------------

The identity of this product is a character, so the icon is that character
and not a rebus about it. A hooded bust, front on, centred on a dusk
rounded-square tile: the indigo hood's crown and cheeks as a ring, a dark
opening where a face would be, two mint eyes in it, two drawstrings hanging
over the chest.

Front on, because the game's own camera is behind-the-shoulder (docs/
art-direction.md: "the hood is a **dome**, not a head: no face, no ears") and
a dome from behind reduces, at 16px, to a featureless bump on a trapezoid —
indistinguishable from a hundred other dark app icons. A front-facing
adaptation keeps every brand-carrying feature (the hood, the indigo, the
dusk, the screen light) and adds the one thing a 16px tile can still hold: a
pair of eyes. This is the documented [DESIGN CALL] in docs/art-direction.md
§Icon, and it is why the eyes are `screen` mint rather than any warm colour —
they are not eye colour, they are the monitor reflected in them, the same
"one cool light in a warm frame" rule the room obeys.

Every colour is derived, not chosen:

  * the hood is the **default hoodie tint composited over the 5-step ramp** —
    `INK[i] = round(0x6a5aa0 * RAMP[i] / 255)`, computed below from
    `gen_assets.RAMP` and the `indigo` tint hex the art direction declares.
    Those five hexes are, to the byte, what the running game multiplies onto
    `dev_form_*.png` for a player who has bought nothing. The icon is not
    "indigo-ish"; it is the same garment.
  * the tile is `shadow` with a dithered `wall_dark`/`wall_light` glow pool
    behind the crown — the room's own wall and its lamp bloom.
  * the opening is `shadow` deepening under the brow into `hair`, which is
    the palette entry whose v2 role is literally "hood interior".
  * the eyes are `screen`; the drawstrings are `cream` with a `gold` aglet —
    UI text colour and Dev Cash, the two things the product hands you.

Light direction is `gen_assets.py`'s: one key light from the upper-left. The
crown's upper-left arc carries the `ink5` specular, value falls off down and
to the right, the hood casts a shadow band across the chest, and the tile's
glow sits behind the head. Nothing here is lit from another angle.

**Legibility on light AND dark backgrounds** is handled structurally, not
hoped for. The tile is dark, so on a dark Dock or taskbar its silhouette
comes from a 1px rim one step LIGHTER than its fill, all the way round (never
a bottom-right shadow, which is invisible against black). The motif is
light-on-dark, so on a white taskbar the tile itself is the silhouette. And
the hood's own outer edge is clamped to `ink4`-or-brighter for its full
perimeter — a rim light justified by the screen glow wrapping the figure, and
the reason the bust does not dissolve into the tile at the bottom right where
the shading has gone dark. `check_contrast` measures all three.

--------------------------------------------------------------------------
FOUR MASTERS, INTEGER UPSCALES ONLY
--------------------------------------------------------------------------

Rule 1 of pixel-art authoring is "authored at native size, never resampled"
— so an icon set spanning 16px to 1024px cannot come from one drawing. It
comes from five hand-tuned masters, each authored at its own size, and every
delivered size is an *integer nearest upscale* of one of them:

    16   <- M16 x1        48   <- M48 x1        256  <- M64 x4
    24   <- M24 x1        64   <- M64 x1        512  <- M64 x8
    32   <- M32 x1        128  <- M64 x2        1024 <- M64 x16

Downsampling is never used anywhere in this file, so every size the Windows
.ico and the macOS .icns ask for below 64px is its own drawing: 24 and 48
exist as masters because Explorer wants those two frames and would otherwise
be handed a resample.

Same composition at all five, progressively simplified toward the small end
the way Apple's and Microsoft's own icons are — this per-size reduction is
the whole difference between an icon set and a resized picture:

    M64  the hero. Dithered fabric shading across all five ink steps, a 1px
         `ink5` hem around the opening, 5x5 rounded eyes with a 1px `lamp`
         glint, 2px drawstrings with a `gold` aglet, and the tile's
         two-colour dithered glow pool.
    M48  the same, one notch down: 4x4 eyes, a smaller glow.
    M32  half of M64's resolution and half its detail: 2x3 eyes, no glint,
         1px drawstrings, a one-colour glow, dithering still on.
    M24  ordered dithering is OFF below 32px (at that scale a Bayer pattern
         is not a gradient, it is four stray pixels of noise): hard-edged
         bands only. 2x2 eyes, no glow, no drawstrings.
    M16  the reduction, and the size the mark was designed against first.
         Flat bands, no glow, no drawstrings, no hem, and 2x2 eyes that fill
         the opening either side of a 2px gap — the opposite of the larger
         masters' margin, chosen by rendering both at true size. The fabric
         also rides brighter than the other masters, because at 16px the
         silhouette has to win outright and there is no room left for a
         subtle one.
"""
from __future__ import annotations

import argparse
import hashlib
import io
import struct
import sys
from dataclasses import dataclass
from pathlib import Path

from PIL import Image

# The palette, the Sprite canvas, the ordered-dither helper and the grayscale
# ramp all come from the sprite generator rather than being re-declared here.
# Duplicating hexes into a second file is how a palette drifts;
# `gen_assets.py` is the one place they live (art-direction: "the palette is
# one dict at the top of the generator"). Importing it also means the icon
# literally shares `bayer_mix`'s dithering mechanics with every sprite in the
# game.
sys.path.insert(0, str(Path(__file__).resolve().parent))
from gen_assets import PALETTE, RAMP, Sprite, bayer_mix  # noqa: E402

REPO = Path(__file__).resolve().parent.parent
ICONS = REPO / "desktop" / "src-tauri" / "icons"


# --------------------------------------------------------------------------
# The ink ramp: the default hoodie, exactly as the running game composites it
# --------------------------------------------------------------------------
#
# docs/art-direction.md declares `indigo` #6a5aa0 as the DEFAULT hoodie tint
# and specifies the tint mechanism as `tint * ramp_value / 255` per pixel. So
# the five indigo hexes below are not a design choice made in this file, they
# are that formula applied to `gen_assets.RAMP` — the same five values a
# player with a fresh save is looking at right now. Deriving them (instead of
# typing five hexes) is what guarantees the icon still matches the character
# if the ramp is ever re-cut.
#
# This is the ONE colour extension beyond the 18 palette entries, and it is
# the extension the art direction already sanctions: the tint hexes are
# declared there precisely because "the palette has no bright indigo".
INDIGO = (0x6A, 0x5A, 0xA0)
INK_STEPS = ("ink1", "ink2", "ink3", "ink4", "ink5")
INK: dict[str, tuple[int, int, int]] = {
    f"ink{i}": tuple(round(c * RAMP[f"ramp{i}"][0] / 255) for c in INDIGO)
    for i in range(1, 6)
}
ICON_PALETTE: dict[str, tuple[int, int, int]] = {**PALETTE, **INK}
RGB_TO_ICON_NAME = {rgb: name for name, rgb in ICON_PALETTE.items()}
assert len(RGB_TO_ICON_NAME) == len(ICON_PALETTE), "two icon colours share a hex"


# How sharply the dither seam between two ink steps is squeezed. 1.0 is
# `gen_assets.ramp_dither`'s behaviour — the fractional part used raw, so a
# slow gradient is 50% checkerboard over a wide area. That is right for a
# 192x76 garment seen at 2x and wrong for an icon: at 512px (M64 x8) each
# dithered pixel is an 8x8 block and a broad 50% field stops reading as cloth
# and starts reading as halftone. 3.0 keeps the ordered-dither seam but
# squeezes it into the middle third of each step, so most of the fabric is a
# SOLID band and only the transitions are mixed — flat bands with dithered
# seams, which is what hand-drawn pixel-art shading looks like.
DITHER_SHARPNESS = 3.0


def ink_at(x: int, y: int, v: float, dither: bool) -> str:
    """One of the five ink steps for a continuous ramp position `v`
    (0.0 = ink1, the deepest fold .. 4.0 = ink5, the specular).

    With `dither` on, the fractional part is ordered-dithered against the
    next step the way `gen_assets.ramp_dither` does it, but sharpened by
    `DITHER_SHARPNESS` first. With it off the value is simply rounded —
    because below 32px a 4x4 Bayer pattern does not read as a gradient, it
    reads as dirt.
    """
    v = max(0.0, min(4.0, v))
    lo = int(v)
    hi = min(lo + 1, 4)
    if not dither:
        return INK_STEPS[int(v + 0.5)]
    frac = (v - lo - 0.5) * DITHER_SHARPNESS + 0.5
    return bayer_mix(x, y, max(0.0, min(1.0, frac)), INK_STEPS[lo], INK_STEPS[hi])


# --------------------------------------------------------------------------
# Delivered files
# --------------------------------------------------------------------------
#
# name -> edge in px. `bundle.icon` in desktop/src-tauri/tauri.conf.json lists
# 32x32.png, 128x128.png, 128x128@2x.png, icon.icns and icon.ico; TAURI_ICON
# below is asserted against that file at the end of the run, so a rename there
# fails this generator instead of silently shipping a stale icon.
TAURI_ICON: tuple[str, ...] = (
    "icons/32x32.png",
    "icons/128x128.png",
    "icons/128x128@2x.png",
    "icons/icon.icns",
    "icons/icon.ico",
)

PNG_TARGETS: dict[str, int] = {
    "32x32.png": 32,
    "128x128.png": 128,
    "128x128@2x.png": 256,
    "icon.png": 1024,
    # the freedesktop hicolor ladder
    "16x16.png": 16,
    "24x24.png": 24,
    "48x48.png": 48,
    "64x64.png": 64,
    "256x256.png": 256,
    "512x512.png": 512,
}

# Windows .ico frames. 256 is the largest an ICO directory entry can address;
# 24 and 48 are the two Explorer sizes that are not powers of two and both
# come from M24 (x1 and x2), never from shrinking a bigger master.
ICO_SIZES: tuple[int, ...] = (16, 24, 32, 48, 64, 128, 256)

# The .icns chunk table, in the shape `iconutil -c icns` emits for a full
# 10-entry .iconset: PNG payloads for the eight modern slots, RLE-compressed
# ARGB for the two small legacy ones. ic04/ic05 are what make macOS Finder's
# 16pt list view crisp — without them the OS smooth-downscales ic11 (32px) and
# the pixel art goes soft at exactly the size this icon is judged at most.
ICNS_PNG: dict[bytes, int] = {
    b"ic07": 128, b"ic08": 256, b"ic09": 512, b"ic10": 1024,
    b"ic11": 32, b"ic12": 64, b"ic13": 256, b"ic14": 512,
}
ICNS_ARGB: dict[bytes, int] = {b"ic04": 16, b"ic05": 32}


# --------------------------------------------------------------------------
# Geometry helpers — hard-edged rasterisation, pixel CENTRES
# --------------------------------------------------------------------------

def rounded_rect(x0: int, y0: int, x1: int, y1: int, r: int) -> set[tuple[int, int]]:
    """The point set of a hard-edged rounded square — the icon tile.

    A pixel is kept when its CENTRE, continuous (x+0.5, y+0.5) with the rect
    spanning [x0, x1+1), falls within `r` of the nearest corner circle's
    centre at (x0+r, y0+r) and its three mirrors. No `ImageDraw` anywhere:
    that would anti-alias the curve.

    Rasterising centres rather than indices is what makes the corner
    staircase MONOTONE. `Sprite.ellipse`'s 1.05 outward fudge is right for
    the tiny filled blobs it was written for and wrong here: at r=13 it
    produces a 3px jump, a stall, then another stall, which at 1024px reads
    as visibly chewed corners.
    """
    pts: set[tuple[int, int]] = set()
    lo_x, hi_x = x0 + r, x1 + 1 - r
    lo_y, hi_y = y0 + r, y1 + 1 - r
    for y in range(y0, y1 + 1):
        for x in range(x0, x1 + 1):
            dx = (x + 0.5) - min(max(x + 0.5, lo_x), hi_x)
            dy = (y + 0.5) - min(max(y + 0.5, lo_y), hi_y)
            if dx * dx + dy * dy <= r * r:
                pts.add((x, y))
    return pts


def rim_of(pts: set[tuple[int, int]]) -> set[tuple[int, int]]:
    """The 1px outermost ring of a point set — every member with at least one
    of its EIGHT neighbours outside it. Derived from the shape actually
    painted, the same way `outline_from_mask` derives sprite outlines, so it
    cannot go stale when a radius or a profile changes.

    Eight and not four: on the diagonal staircase of a rounded corner a pixel
    on the true edge can still have all four orthogonal neighbours inside the
    shape, so a 4-neighbour test leaves the rim full of holes at every corner.
    """
    return {
        (x, y)
        for (x, y) in pts
        if any((x + dx, y + dy) not in pts
               for dx in (-1, 0, 1) for dy in (-1, 0, 1) if (dx, dy) != (0, 0))
    }


def dilate(pts: set[tuple[int, int]], n: int) -> set[tuple[int, int]]:
    """`pts` grown by `n` pixels in every direction (Chebyshev). Used to keep
    the tile's glow pool a fixed gap clear of the motif: the glow is there to
    lift the field BEHIND the character, and a lit pixel touching the hood's
    rim would eat the very contrast step the rim exists to provide."""
    out = set(pts)
    for _ in range(n):
        out |= {(x + dx, y + dy) for (x, y) in out
                for dx in (-1, 0, 1) for dy in (-1, 0, 1)}
    return out


def span(cx: float, hw: float, edge: int) -> tuple[int, int] | None:
    """The inclusive pixel span of a row of half-width `hw` about `cx`, as a
    pair mirrored EXACTLY about the canvas centre.

    `x1` is computed as `edge - 1 - x0` rather than from `cx + hw`, which is
    what makes every silhouette in this file bit-for-bit mirror-symmetric
    (`check_symmetry` asserts it) instead of symmetric-to-within-a-rounding-
    mode. Every master has an even edge, so `cx` is a column boundary and
    a mirrored pair of spans is the only symmetric answer available.
    """
    if hw < 0.5:
        return None
    x0 = int(cx - hw + 0.5)
    x1 = edge - 1 - x0
    if x1 < x0:
        return None
    return x0, x1


def superellipse_hw(dy: float, b: float, a: float, n: float) -> float:
    """Half-width of a superellipse |x/a|^n + |y/b|^n = 1 at vertical offset
    `dy` from its centre. n=2 is an ellipse; n<2 is pointier (used for the
    hood crown, which has a peak, not a bald dome); n>2 is boxier."""
    t = abs(dy) / b
    if t >= 1.0:
        return 0.0
    return a * (1.0 - t ** n) ** (1.0 / n)


# --------------------------------------------------------------------------
# One master, four times: the per-size design spec
# --------------------------------------------------------------------------
#
# Every number a master differs by lives in one of these. The builder below
# reads them and nothing else, so "hand-tuned per size" means editing a table
# rather than forking a drawing — and the four specs sit next to each other
# where the progressive simplification is visible at a glance.

@dataclass(frozen=True)
class Spec:
    edge: int              # canvas (and delivered) size
    inset: int             # tile inset from the canvas on all four sides
    radius: int            # tile corner radius (~22% of the tile edge)

    crown: int             # topmost hood row
    dome_c: int            # row where the crown's curve hands over to straight sides
    hood_bot: int          # last row before the shoulders flare
    sh_bot: int            # row where the shoulders reach full width
    bust_bot: int          # bottom row of the motif
    a_hood: float          # hood half-width
    a_sh: float            # shoulder half-width
    n_dome: float          # crown superellipse exponent (<2 = peaked)
    flare: float           # shoulder slope exponent (see `hood_hw`)
    taper: tuple[int, ...] # half-width reduction for the last N rows of the bust

    cav_top: int           # topmost row of the face opening
    cav_bot: int           # bottom row of the face opening
    a_cav: float           # opening half-width
    n_cav: float           # opening superellipse exponent

    eye_w: int
    eye_h: int
    eye_gap: int           # dark pixels between the two eyes (even, for symmetry)
    eye_top: int
    eye_round: bool        # clip the eye's four corners
    glint: int             # `lamp` glint edge in px, 0 = none

    hem: bool              # 1px `ink5` rolled edge around the opening
    lining: int            # `hair` lining depth under the brow, 0 = none
    string: tuple[int, int, int, int] | None  # (width, top, bottom, gap apart)
    aglet: bool            # `gold` tip on the last row of each drawstring
    glow: tuple[float, float, float, float] | None  # (r_dark, gain, r_light, gap)
    dither: bool           # ordered dithering on the fabric and the glow
    base_v: float          # fabric ramp position at the form's mid tone


SPECS: dict[int, Spec] = {
    # ---------------------------------------------------------------- M64 --
    # The hero, and the master every size from 64px up is an integer upscale
    # of. Tile 2..61 (60px) with r=13 = 21.7% of the tile edge, the macOS Big
    # Sur proportion. The motif's bounding box is 46x43 inside that 60x60
    # tile — 77% of the tile wide, 72% tall, 7px of air left and right and
    # 8/9px top and bottom, so the mark is optically centred rather than
    # merely arithmetically centred.
    #
    # PROPORTION PASS — this is the difference between a hoodie and a ghost,
    # and it took three rounds of looking at the review sheet to find.
    # The first version gave the opening a 27px width against a 38px hood: a
    # thin ring around a big round void, which rendered as a spectre, not a
    # garment. Three numbers fixed it. The opening came in to 19px so the
    # crown carries EIGHT rows of fabric above the brow — a hood's mass is
    # the thing that says "hood", and the opening is now one pixel taller
    # than it is wide, which is a face rather than a porthole. The shoulders
    # flare from 34px to 46px over eight rows and then run straight down, so
    # the outline STEPS OUT at the hem: a ghost has no shoulders, and that
    # step is what stops a viewer resolving the silhouette as one. And the
    # drawstrings start ON the hem row and hang at a 12px spread, where a
    # real hoodie's eyelets are — the two rejected alternatives both misread
    # unmistakably, a close narrow pair as fangs and a long wide pair as
    # tusks.
    64: Spec(
        edge=64, inset=2, radius=13,
        crown=10, dome_c=26, hood_bot=38, sh_bot=46, bust_bot=52,
        a_hood=17.0, a_sh=23.0, n_dome=1.85, flare=0.72, taper=(0, 1, 2),
        cav_top=18, cav_bot=38, a_cav=9.5, n_cav=2.2,
        eye_w=5, eye_h=5, eye_gap=4, eye_top=24, eye_round=True, glint=1,
        hem=True, lining=0, string=(2, 39, 45, 12), aglet=True,
        glow=(22.0, 1.0, 11.0, 2.0), dither=True, base_v=3.45,
    ),
    # ---------------------------------------------------------------- M48 --
    # Windows Explorer's "medium icons" size, the freedesktop 48px slot, and
    # an .ico frame — the most-seen size on Windows after 16. It gets its own
    # master rather than being M24 doubled, because at 48px there is room for
    # the glint, the gold aglet and the dithered glow that M24 cannot hold,
    # and doubling M24 would have thrown all three away at the one size where
    # they still register.
    48: Spec(
        edge=48, inset=1, radius=10,
        crown=8, dome_c=20, hood_bot=29, sh_bot=35, bust_bot=39,
        a_hood=12.5, a_sh=17.0, n_dome=1.85, flare=0.72, taper=(0, 1, 2),
        cav_top=13, cav_bot=29, a_cav=7.0, n_cav=2.2,
        eye_w=4, eye_h=4, eye_gap=4, eye_top=18, eye_round=True, glint=1,
        hem=True, lining=0, string=(2, 30, 34, 8), aglet=True,
        glow=(18.0, 0.95, 10.0, 2.0), dither=True, base_v=3.45,
    ),
    # ---------------------------------------------------------------- M32 --
    # Every anchor is M64's halved and then rounded to a whole pixel, so the
    # two read as the same drawing; the detail budget halves with it (no
    # glint, 1px four-row strings, a single-colour glow).
    #
    # The eyes are 2x3 — TALLER than wide, and 4px apart rather than 2. The
    # obvious 3x3-at-2px-apart fills the same opening more completely and was
    # rejected off the sheet: at 32px two 3px-wide blocks that close read as
    # one pair of goggles, and the whole point of the eyes is that they are a
    # pair. Keeping them narrow and separated matches the proportion M48 and
    # M64 get for free from having more pixels.
    32: Spec(
        edge=32, inset=1, radius=7,
        crown=5, dome_c=13, hood_bot=19, sh_bot=23, bust_bot=26,
        a_hood=8.5, a_sh=11.5, n_dome=1.85, flare=0.72, taper=(0, 1),
        cav_top=9, cav_bot=19, a_cav=5.0, n_cav=2.2,
        eye_w=2, eye_h=3, eye_gap=4, eye_top=12, eye_round=False, glint=0,
        hem=True, lining=0, string=(1, 20, 23, 6), aglet=True,
        glow=(12.0, 0.8, 0.0, 1.0), dither=True, base_v=3.5,
    ),
    # ---------------------------------------------------------------- M24 --
    # The tray/tab size on a HiDPI screen and an .ico frame. Ordered
    # dithering is OFF from here down: at this scale a 4x4 Bayer pattern is
    # not a gradient, it is four stray pixels of noise. The drawstrings go
    # too — 1px of `cream` on the chest at 24px is a speck of dirt, not a
    # detail — and the eyes drop to 2x2, the smallest block that still reads
    # as a pair rather than as dust.
    24: Spec(
        edge=24, inset=1, radius=5,
        crown=4, dome_c=10, hood_bot=14, sh_bot=17, bust_bot=20,
        a_hood=6.5, a_sh=8.5, n_dome=1.85, flare=0.72, taper=(0, 1),
        cav_top=8, cav_bot=14, a_cav=4.0, n_cav=2.1,
        eye_w=2, eye_h=2, eye_gap=2, eye_top=10, eye_round=False, glint=0,
        hem=True, lining=0, string=None, aglet=False,
        glow=None, dither=False, base_v=3.55,
    ),
    # ---------------------------------------------------------------- M16 --
    # The tab/tray size. Full-bleed tile (inset 0): at 16px a 1px margin is
    # 12% of the icon's width thrown away, and every tray pads its own slots
    # anyway. Flat bands, no glow, no drawstrings, no hem — with only two
    # pixels of fabric between the tile edge and the opening, an `ink5` ring
    # inside the opening and the `ink5` rim outside the hood would leave one
    # mid pixel between two highlights and read as a mis-registration.
    #
    # The eyes are 2x2 and therefore span the opening's full width either
    # side of a 2px gap, with no `shadow` margin between eye and fabric. That
    # is the deliberate opposite of the choice every larger master makes, and
    # it was made by rendering both at true 16px side by side: the 1x2
    # version keeps its margin and its eyes then read as two specks of dust,
    # while 2x2 reads as a face at a glance. At 16px legibility outranks
    # tidiness. The fabric rides brighter here too, for the same reason —
    # the silhouette has to win outright and there is no room left for a
    # subtle one.
    16: Spec(
        edge=16, inset=0, radius=3,
        crown=2, dome_c=7, hood_bot=10, sh_bot=12, bust_bot=14,
        a_hood=5.0, a_sh=6.5, n_dome=1.85, flare=0.72, taper=(0,),
        cav_top=5, cav_bot=10, a_cav=3.0, n_cav=2.1,
        eye_w=2, eye_h=2, eye_gap=2, eye_top=7, eye_round=False, glint=0,
        hem=False, lining=0, string=None, aglet=False,
        glow=None, dither=False, base_v=3.85,
    ),
}


# --------------------------------------------------------------------------
# Silhouettes
# --------------------------------------------------------------------------

def hood_hw(sp: Spec, y: int) -> float:
    """Half-width of the bust at row `y`: crown curve, straight hood sides,
    then the shoulder flare.

    The shoulders widen over `hood_bot`..`sh_bot` only and then run straight
    down to the bottom edge, which is the shape a shoulder actually has: it
    leaves the neck fast and then the torso goes vertical. Spreading the same
    widening across the whole lower half instead (the first version's
    quarter-ellipse to `bust_bot`) produced a bell — a cape or a chess pawn,
    not a person, and a silhouette a viewer resolves as "ghost". `flare`
    tunes the slope: 1.0 is a straight diagonal, below 1.0 rounds the
    deltoid corner outward.

    The hood's own sides are dead straight between `dome_c` and `hood_bot`:
    that flat vertical run is the "cheek" of the hood and it is the feature
    that survives to 16px.
    """
    if y < sp.crown or y > sp.bust_bot:
        return 0.0
    if y <= sp.dome_c:
        b = sp.dome_c - sp.crown + 1.0
        return superellipse_hw(sp.dome_c - y, b, sp.a_hood, sp.n_dome)
    hw = sp.a_hood
    if y > sp.hood_bot:
        t = min(1.0, (y - sp.hood_bot) / float(sp.sh_bot - sp.hood_bot))
        hw = sp.a_hood + (sp.a_sh - sp.a_hood) * t ** sp.flare
    n = sp.bust_bot - y
    if n < len(sp.taper):
        hw -= sp.taper[len(sp.taper) - 1 - n]
    return hw


def cav_hw(sp: Spec, y: int) -> float:
    """Half-width of the face opening at row `y` — one superellipse, so the
    opening is a closed rounded shape and the hood is a continuous ring
    around it. A ring is the shape that reads at 16px; an opening left open
    at the bottom turns the hood into two disconnected sideburns."""
    c = (sp.cav_top + sp.cav_bot) / 2.0
    b = (sp.cav_bot - sp.cav_top + 1) / 2.0
    if not (sp.cav_top <= y <= sp.cav_bot):
        return 0.0
    return superellipse_hw(y - c, b, sp.a_cav, sp.n_cav)


def masks(sp: Spec) -> tuple[set, set, set, set, set]:
    """(tile, bust, cavity, eyes, strings) as point sets.

    Computed before anything is painted, because the tile's glow needs to
    know where the motif is, the fabric needs to know where the opening is,
    and the self-checks need all five independently of the colours that
    ended up on them.
    """
    cx = sp.edge / 2.0
    tile = rounded_rect(sp.inset, sp.inset,
                        sp.edge - 1 - sp.inset, sp.edge - 1 - sp.inset, sp.radius)

    bust: set[tuple[int, int]] = set()
    for y in range(sp.crown, sp.bust_bot + 1):
        s = span(cx, hood_hw(sp, y), sp.edge)
        if s:
            bust |= {(x, y) for x in range(s[0], s[1] + 1)}

    cavity: set[tuple[int, int]] = set()
    for y in range(sp.cav_top, sp.cav_bot + 1):
        s = span(cx, cav_hw(sp, y), sp.edge)
        if s:
            cavity |= {(x, y) for x in range(s[0], s[1] + 1)}
    cavity &= bust          # an opening is a hole in the garment, by definition

    eyes: set[tuple[int, int]] = set()
    inner = int(cx + sp.eye_gap / 2)          # first fabric-side column right of centre
    for dx in range(sp.eye_w):
        for y in range(sp.eye_top, sp.eye_top + sp.eye_h):
            xr = inner + dx
            eyes.add((xr, y))
            eyes.add((sp.edge - 1 - xr, y))
    if sp.eye_round and sp.eye_w >= 4 and sp.eye_h >= 4:
        x0, x1 = inner, inner + sp.eye_w - 1
        y0, y1 = sp.eye_top, sp.eye_top + sp.eye_h - 1
        for (x, y) in ((x0, y0), (x1, y0), (x0, y1), (x1, y1)):
            eyes.discard((x, y))
            eyes.discard((sp.edge - 1 - x, y))
    eyes &= cavity          # an eye outside the opening is a bug, not a feature

    strings: set[tuple[int, int]] = set()
    if sp.string:
        w, top, bot, gap = sp.string
        inner_s = int(cx + gap / 2)
        for dx in range(w):
            for y in range(top, bot + 1):
                xr = inner_s + dx
                strings.add((xr, y))
                strings.add((sp.edge - 1 - xr, y))
    strings &= bust - cavity

    return tile, bust, cavity, eyes, strings


# --------------------------------------------------------------------------
# Painting
# --------------------------------------------------------------------------

def fabric_v(sp: Spec, x: int, y: int) -> float:
    """The ramp position of one fabric pixel: one key light from the upper
    left, plus the hood's cast shadow on the chest.

    `nx` is signed horizontal position normalised by the shoulder half-width
    and `ny` vertical position through the bust, so the same expression
    produces the same LOOK at every master size instead of needing four sets
    of magic rows. The chest term is the only local one: the hood physically
    overhangs the chest, so the rows just under the opening drop away and
    that dark band is what stops the bust reading as one flat plate.
    """
    cx = sp.edge / 2.0
    nx = (x + 0.5 - cx) / sp.a_sh
    ny = (y - sp.crown) / float(sp.bust_bot - sp.crown)
    v = sp.base_v - 1.2 * nx - 1.4 * (ny - 0.35)
    if y > sp.cav_bot:
        drop = max(0.0, 1.0 - (y - sp.cav_bot - 1) / (2.0 + sp.edge / 16.0))
        v -= 1.15 * drop * max(0.0, 1.0 - abs(nx) * 1.6)
    return v


def paint(sp: Spec) -> Sprite:
    """One master, painted back to front: tile, glow, tile rim, fabric,
    fabric rim light, opening, hem, drawstrings, eyes."""
    s = Sprite(sp.edge, sp.edge, palette=ICON_PALETTE)
    tile, bust, cavity, eyes, strings = masks(sp)
    cx = sp.edge / 2.0
    fabric = bust - cavity

    # ---- tile ---------------------------------------------------------
    for (x, y) in tile:
        s.dot(x, y, "shadow")

    # The room's glow pool, behind the crown: `wall_dark` at a radial density
    # over the `shadow` field, with a tighter `wall_light` core at the larger
    # sizes. Held `gap` px clear of the motif so the halo never touches the
    # hood's rim light — light-dark-light across that boundary is what makes
    # the crown pop instead of dissolving into its own backdrop.
    if sp.glow:
        r_dark, gain, r_light, gap = sp.glow
        keep_clear = dilate(bust, int(gap))
        gy = sp.dome_c
        for (x, y) in sorted(tile - keep_clear):
            d = ((x + 0.5 - cx) ** 2 + (y + 0.5 - gy) ** 2) ** 0.5
            t = max(0.0, 1.0 - d / r_dark)
            if bayer_mix(x, y, min(1.0, t ** 1.3 * gain), "a", "b") == "b":
                s.dot(x, y, "wall_dark")
                if r_light > 0:
                    u = max(0.0, 1.0 - d / r_light)
                    if bayer_mix(x, y, u ** 1.6 * 0.55, "a", "b") == "b":
                        s.dot(x, y, "wall_light")

    # The tile's own edge light, one step above its fill, all the way round —
    # not a bottom-right drop shadow, which would be invisible on the dark
    # taskbars this icon has to survive.
    for (x, y) in rim_of(tile):
        s.dot(x, y, "wall_light")

    # ---- the hood -----------------------------------------------------
    cav_rim = rim_of(cavity) if cavity else set()
    for (x, y) in sorted(fabric):
        s.dot(x, y, ink_at(x, y, fabric_v(sp, x, y), sp.dither))

    # Rim light: the outer 1px of the garment is clamped to `ink4` or
    # brighter, and to `ink5` on the arc facing the key light. The clamp is
    # not decoration — the shading legitimately reaches `ink2` at the bottom
    # right, `ink2` on `shadow` is a 1.7:1 read, and without the clamp the
    # bust's lower corners melt into the tile. `check_contrast` measures it.
    mid_y = (sp.crown + sp.bust_bot) / 2.0
    for (x, y) in rim_of(bust):
        if (x, y) in cavity:
            continue
        lit = (x + 0.5 - cx) / sp.a_sh + (y - mid_y) / (sp.bust_bot - sp.crown) <= -0.1
        base = fabric_v(sp, x, y)
        s.dot(x, y, ink_at(x, y, 4.0 if lit else max(base, 3.0), sp.dither))

    # ---- the opening --------------------------------------------------
    for (x, y) in sorted(cavity):
        s.dot(x, y, "shadow")
    if sp.lining:
        # `hair` is the palette entry whose v2 role IS "hood interior", so a
        # band of it inside the opening is the same depth cue
        # `dev_base_*.png` paints — and being warm, it stops the opening
        # reading as a hole punched clean through to the tile behind.
        #
        # Under the BROW, hugging the top of the opening, taken per column so
        # the band follows the curve. The first version put it along the
        # bottom instead: a warm arc directly under two eyes is a mouth, and
        # the 256px render grinned. At the top it is what it physically is,
        # the lining of the hood lit from below by the screen.
        by_col: dict[int, int] = {}
        for (x, y) in cavity:
            by_col[x] = min(by_col.get(x, sp.edge), y)
        for (x, y) in sorted(cavity):
            if y < by_col[x] + sp.lining:
                s.dot(x, y, "hair")
    if sp.hem:
        # The rolled front edge of the hood, 1px of `ink5` on the fabric side
        # of the opening. Two jobs: it says "garment with a thickness", and it
        # puts the brightest ink directly against the darkest pixel in the
        # icon, which is the crispest boundary available and the one that
        # makes the ring read as a hood at a glance.
        for (x, y) in sorted(cav_rim):
            for dx in (-1, 0, 1):
                for dy in (-1, 0, 1):
                    p = (x + dx, y + dy)
                    if p in fabric:
                        s.dot(p[0], p[1], "ink5")

    # ---- drawstrings --------------------------------------------------
    if strings:
        w, top, bot, _ = sp.string
        for (x, y) in sorted(strings):
            s.dot(x, y, "cream")
        if sp.aglet:
            for (x, y) in sorted(strings):
                if y == bot:
                    s.dot(x, y, "gold")

    # ---- eyes ---------------------------------------------------------
    for (x, y) in sorted(eyes):
        s.dot(x, y, "screen")
    if sp.glint:
        # The same key light as everything else: up and to the left inside
        # each eye — mirrored to each eye's OWN left edge rather than
        # mirrored across the icon, because one light source means both
        # highlights sit on the same side of their eye.
        #
        # Anchored one column in when the eye is corner-clipped: the eye's
        # literal top-left pixel is exactly the pixel `eye_round` removed, so
        # anchoring there made a 1px glint paint nothing at all and vanish
        # without any assertion noticing.
        inner = int(cx + sp.eye_gap / 2)
        off = 1 if sp.eye_round else 0
        painted = 0
        for gx in range(sp.glint):
            for gy in range(sp.glint):
                for q in ((inner + off + gx, sp.eye_top + gy),
                          (sp.edge - 1 - (inner + sp.eye_w - 1 - off - gx),
                           sp.eye_top + gy)):
                    if q in eyes:
                        s.dot(q[0], q[1], "lamp")
                        painted += 1
        assert painted == 2 * sp.glint ** 2, (
            f"M{sp.edge}: {painted} of {2 * sp.glint ** 2} glint px landed "
            f"inside an eye — the glint is being clipped away")
    return s


MASTERS = tuple(sorted(SPECS))


def render(size: int, masters: dict[int, Image.Image]) -> Image.Image:
    """One delivered size, as an integer nearest-neighbour upscale of the
    LARGEST master that divides it exactly — so 48 comes from M24 x2 rather
    than a non-integer shrink of M64, and 128 comes from M64 x2 rather than
    M32 x4 (more real detail whenever the arithmetic allows it). A size no
    master divides is a bug in the tables above, not something to paper over
    with a resample, so it raises."""
    for edge in sorted(masters, reverse=True):
        if size % edge == 0:
            f = size // edge
            return masters[edge].copy() if f == 1 else masters[edge].resize(
                (size, size), Image.NEAREST)
    raise AssertionError(f"no master divides {size}px — masters: {sorted(masters)}")


# --------------------------------------------------------------------------
# Containers: .ico and .icns
# --------------------------------------------------------------------------

def pixels(img: Image.Image) -> list[tuple[int, int, int, int]]:
    """Every pixel of `img` as RGBA tuples. Wrapped because Pillow is midway
    through renaming `getdata` to `get_flattened_data` and a generator whose
    whole job is to be run and trusted should not print eight deprecation
    warnings on the way."""
    rgba = img.convert("RGBA")
    getter = getattr(rgba, "get_flattened_data", None) or rgba.getdata
    return list(getter())


def png_bytes(img: Image.Image) -> bytes:
    """A PNG payload for an .icns chunk. Pillow writes no tIME and no other
    dated chunk, so this is a pure function of the pixels — which is what
    keeps the whole .icns byte-identical between runs."""
    buf = io.BytesIO()
    img.convert("RGBA").save(buf, format="PNG")
    return buf.getvalue()


def rle_encode(data: bytes) -> bytes:
    """Apple's PackBits variant, as used by the icns ARGB/24-bit channels:
    a control byte < 0x80 introduces `n+1` literal bytes, a control byte
    >= 0x80 repeats the next byte `n-125` times (so 0x80 = a run of 3, 0xFF
    = a run of 130). Verified against the chunk layout `iconutil` itself
    produced for this project's previous icon.

    Runs of two are left as literals on purpose: encoding them costs the same
    two bytes and breaking a literal run to do it costs one more.
    """
    out = bytearray()
    i, n = 0, len(data)
    while i < n:
        run = 1
        while i + run < n and run < 130 and data[i + run] == data[i]:
            run += 1
        if run >= 3:
            out.append(0x80 + run - 3)
            out.append(data[i])
            i += run
            continue
        start = i
        while i < n and i - start < 128:
            run = 1
            while i + run < n and run < 3 and data[i + run] == data[i]:
                run += 1
            if run >= 3:
                break
            i += 1
        out.append(i - start - 1)
        out += data[start:i]
    return bytes(out)


def rle_decode(data: bytes, want: int) -> tuple[bytes, int]:
    """Inverse of `rle_encode`, returning (bytes, input consumed). Lives here
    rather than in a test because the self-check uses it to prove the ARGB
    chunks this file writes decode back to the exact pixels of the master —
    an assertion no external tool on this machine can make."""
    out = bytearray()
    i = 0
    while i < len(data) and len(out) < want:
        c = data[i]
        i += 1
        if c & 0x80:
            out += bytes([data[i]]) * (c - 125)
            i += 1
        else:
            out += data[i:i + c + 1]
            i += c + 1
    return bytes(out), i


def argb_chunk(img: Image.Image) -> bytes:
    """An icns `ARGB` payload: the 4-byte magic, then the A, R, G and B
    planes each RLE-compressed separately (the layout `iconutil` emits for
    ic04/ic05, confirmed by decoding one)."""
    rgba = img.convert("RGBA")
    px = pixels(rgba)
    planes = [bytes(p[c] for p in px) for c in (3, 0, 1, 2)]
    return b"ARGB" + b"".join(rle_encode(p) for p in planes)


def build_icns(masters: dict[int, Image.Image]) -> bytes:
    """The .icns container, in `iconutil -c icns` shape: 'icns', the total
    length, then one chunk per representation and no TOC.

    No TOC because Apple's own tool writes none (this project's previous
    icon, produced by `iconutil` on a Mac, has ten chunks and no TOC), which
    is the strongest available evidence that the field is optional. Chunks
    are emitted in sorted tag order so the bytes are stable.
    """
    chunks: list[tuple[bytes, bytes]] = []
    for tag, size in ICNS_PNG.items():
        chunks.append((tag, png_bytes(render(size, masters))))
    for tag, size in ICNS_ARGB.items():
        chunks.append((tag, argb_chunk(render(size, masters))))
    chunks.sort()
    body = b"".join(tag + struct.pack(">I", 8 + len(d)) + d for tag, d in chunks)
    return b"icns" + struct.pack(">I", 8 + len(body)) + body


def parse_icns(blob: bytes) -> dict[bytes, bytes]:
    """Walk an .icns and return {tag: payload}, asserting the declared length
    matches the file and every chunk lies inside it. Pillow's reader silently
    ignores tags it does not know (ic04/ic05 among them), so structural
    validation has to happen here."""
    if blob[:4] != b"icns":
        raise AssertionError("icns: bad magic")
    total = struct.unpack(">I", blob[4:8])[0]
    if total != len(blob):
        raise AssertionError(f"icns: header says {total} bytes, file is {len(blob)}")
    out: dict[bytes, bytes] = {}
    off = 8
    while off < len(blob):
        tag = blob[off:off + 4]
        n = struct.unpack(">I", blob[off + 4:off + 8])[0]
        if n < 8 or off + n > len(blob):
            raise AssertionError(f"icns: chunk {tag!r} length {n} runs off the end")
        out[tag] = blob[off + 8:off + n]
        off += n
    return out


# --------------------------------------------------------------------------
# Self-check
# --------------------------------------------------------------------------

def check_palette(img: Image.Image, label: str) -> dict[str, int]:
    """Assert every pixel is either fully transparent or one of the icon's
    colours at full opacity, and return a name -> count census. Same
    guarantee `gen_assets.py` gives every sprite: art that drifted
    off-palette, or that some resampler quietly anti-aliased on the way out,
    fails the build instead of shipping."""
    rgba = img.convert("RGBA")
    px = rgba.load()
    w, h = rgba.size
    census: dict[str, int] = {}
    for y in range(h):
        for x in range(w):
            r, g, b, a = px[x, y]
            if a == 0:
                continue
            if a != 255:
                raise AssertionError(f"{label}: partial alpha {a} at ({x},{y})")
            name = RGB_TO_ICON_NAME.get((r, g, b))
            if name is None:
                raise AssertionError(
                    f"{label}: off-palette pixel #{r:02x}{g:02x}{b:02x} at ({x},{y})")
            census[name] = census.get(name, 0) + 1
    return census


def check_footprint(img: Image.Image, sp: Spec) -> str:
    """Assert the master's opaque footprint is EXACTLY its tile — nothing
    outside it and no hole inside it.

    Both halves matter. A hole means a tile pixel was never painted. Anything
    outside means an element leaked past the rounded corner, which at icon
    scale does not read as a bold overhang, it reads as a chipped icon. This
    is what lets the bust's shoulders be widened without re-deriving the
    corner arithmetic by hand each time.
    """
    px = img.convert("RGBA").load()
    opaque = {(x, y) for y in range(sp.edge) for x in range(sp.edge)
              if px[x, y][3] != 0}
    tile = rounded_rect(sp.inset, sp.inset,
                        sp.edge - 1 - sp.inset, sp.edge - 1 - sp.inset, sp.radius)
    if opaque - tile:
        raise AssertionError(f"M{sp.edge}: {len(opaque - tile)} px outside the tile, "
                             f"e.g. {sorted(opaque - tile)[:4]}")
    if tile - opaque:
        raise AssertionError(f"M{sp.edge}: {len(tile - opaque)} unpainted tile px, "
                             f"e.g. {sorted(tile - opaque)[:4]}")
    return f"footprint == tile ({len(tile)} px)"


def check_symmetry(sp: Spec) -> str:
    """Assert every SILHOUETTE is mirror-symmetric to the pixel.

    Not the colours: the shading is directional on purpose (one key light,
    upper left) and asserting symmetric pixels would forbid it. But an
    off-by-one in a span, an odd `eye_gap`, or a drawstring pair that drifted
    a column apart are all invisible at 16px and glaring at 512, and this is
    the assertion that catches them.
    """
    tile, bust, cavity, eyes, strings = masks(sp)
    for label, pts in (("tile", tile), ("bust", bust), ("cavity", cavity),
                       ("eyes", eyes), ("strings", strings)):
        flip = {(sp.edge - 1 - x, y) for (x, y) in pts}
        if flip != pts:
            raise AssertionError(
                f"M{sp.edge}: {label} is not mirror-symmetric "
                f"({len(pts ^ flip)} px differ)")
    if sp.eye_gap % 2:
        raise AssertionError(f"M{sp.edge}: eye_gap must be even to stay centred")
    return (f"mirror-symmetric: bust {len(bust)}, opening {len(cavity)}, "
            f"eyes {len(eyes)}, strings {len(strings)}")


def _lum(rgb: tuple[int, int, int]) -> float:
    def ch(v: float) -> float:
        v /= 255.0
        return v / 12.92 if v <= 0.04045 else ((v + 0.055) / 1.055) ** 2.4
    r, g, b = rgb
    return 0.2126 * ch(r) + 0.7152 * ch(g) + 0.0722 * ch(b)


def contrast(a: tuple[int, int, int], b: tuple[int, int, int]) -> float:
    la, lb = _lum(a), _lum(b)
    return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)


# The three reads the icon lives or dies by, as measured ratios rather than
# as an opinion. `hood/tile` is the silhouette (the rim-light clamp exists to
# hold it), `eyes/opening` is the feature that has to survive to 16px, and
# `tile rim/black` is whether the icon has an edge at all on a black taskbar.
CONTRAST_FLOORS: dict[str, float] = {
    "hood rim vs tile": 2.0,
    "eye vs opening": 8.0,
    "tile rim vs black": 1.5,
}


def check_contrast() -> list[str]:
    got = {
        "hood rim vs tile": contrast(INK["ink4"], PALETTE["shadow"]),
        "eye vs opening": contrast(PALETTE["screen"], PALETTE["shadow"]),
        "tile rim vs black": contrast(PALETTE["wall_light"], (0, 0, 0)),
    }
    lines = []
    for k, floor in CONTRAST_FLOORS.items():
        if got[k] < floor:
            raise AssertionError(f"contrast {k} = {got[k]:.2f}:1, floor {floor}:1")
        lines.append(f"{k}: {got[k]:.2f}:1 (floor {floor}:1)")
    return lines


def check_not_downscaled(masters: dict[int, Image.Image]) -> str:
    """Assert M16 is a DRAWING, not a shrink of M64.

    The brief this icon was built to calls per-size authoring "what separates
    industry-standard icons from resized ones", so the claim gets an
    assertion: against nearest, box and lanczos reductions of the hero
    master, more than a quarter of M16's 256 pixels must differ. In practice
    it is far more than that — the small masters drop the glow and the
    drawstrings, and re-cut the eyes — and the floor is set low
    deliberately, to fail only if someone actually replaces a master with a
    resize.
    """
    small = masters[16].convert("RGBA")
    out = []
    for name, f in (("nearest", Image.NEAREST), ("box", Image.BOX),
                    ("lanczos", Image.LANCZOS)):
        naive = masters[64].convert("RGBA").resize((16, 16), f)
        diff = sum(1 for a, b in zip(pixels(small), pixels(naive)) if a != b)
        frac = diff / 256.0
        if frac <= 0.25:
            raise AssertionError(
                f"M16 differs from a {name} downscale of M64 in only "
                f"{frac:.0%} of pixels — it is a resize, not a master")
        out.append(f"{name} {frac:.0%}")
    return "M16 vs naive downscales of M64: " + ", ".join(out)


def sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()[:16]


# --------------------------------------------------------------------------
# Review sheet
# --------------------------------------------------------------------------

def review_sheet(masters: dict[int, Image.Image], out: Path) -> None:
    """The contact sheet this icon was judged on: every delivered size over a
    dark and a light backdrop, the three small masters magnified so their
    individual pixels can be inspected, and mock taskbar/dock rows in both
    themes. Not a build artifact — it is written only when `--sheet` asks for
    it, and it exists so the verdict on this icon is made by looking at it at
    the sizes people will actually see."""
    from PIL import ImageDraw, ImageFont

    try:
        font = ImageFont.load_default(13)
        small = ImageFont.load_default(11)
    except TypeError:                       # very old Pillow
        font = small = ImageFont.load_default()

    DARK, LIGHT, MID = (0x14, 0x13, 0x1A), (0xF4, 0xF2, 0xEE), (0x80, 0x80, 0x88)
    sizes = (16, 24, 32, 48, 64, 128, 256)
    W = 1180
    sheet = Image.new("RGB", (W, 1560), (0x24, 0x24, 0x2C))
    d = ImageDraw.Draw(sheet)

    def label(x: int, y: int, text: str, fill=(0xF2, 0xE0, 0xC9), f=None) -> None:
        d.text((x, y), text, fill=fill, font=f or small)

    def strip(y: int, bg, title: str, fg) -> int:
        d.rectangle([0, y, W - 1, y + 300], fill=bg)
        label(24, y + 12, title, fg, font)
        x = 24
        for sz in sizes:
            img = render(sz, masters)
            sheet.paste(img, (x, y + 300 - 24 - sz), img)
            label(x, y + 300 - 20, f"{sz}", fg)
            x += max(sz, 34) + 26
        return y + 300

    y = 0
    label(24, y + 8, "dexel app icon - review sheet. Every image below is a "
                     "hand-authored master or an integer nearest upscale of one.",
          f=font)
    y += 34
    y = strip(y, DARK, "on dark  #14131a", (0xF2, 0xE0, 0xC9))
    y = strip(y, LIGHT, "on light  #f4f2ee", (0x24, 0x1F, 0x2E))
    y = strip(y, MID, "on mid grey  #808088", (0x14, 0x13, 0x1A))

    # ---- pixel-peep: the small masters magnified ------------------------
    d.rectangle([0, y, W - 1, y + 330], fill=(0x1C, 0x1A, 0x24))
    label(24, y + 12, f"the {len(MASTERS)} masters, magnified (each drawn at its "
                      f"own size, never resized)", f=font)
    x = 24
    for edge in MASTERS:
        zoom = max(2, 192 // edge)
        img = masters[edge].convert("RGBA").resize((edge * zoom, edge * zoom),
                                                   Image.NEAREST)
        sheet.paste(img, (x, y + 44), img)
        label(x, y + 44 + edge * zoom + 6, f"M{edge} at {zoom}x")
        x += edge * zoom + 22
    y += 330

    # ---- fake taskbar / dock -------------------------------------------
    def bar(y: int, bg, neighbours, title: str, sz: int, pad: int) -> int:
        h = sz + pad * 2
        label(24, y + 6, title, f=small)
        y += 24
        d.rectangle([0, y, W - 1, y + h], fill=bg)
        x = 24
        for i, col in enumerate(neighbours):
            if i == 2:
                img = render(sz, masters)
                sheet.paste(img, (x, y + pad), img)
            else:
                d.rounded_rectangle([x, y + pad, x + sz - 1, y + pad + sz - 1],
                                    radius=max(2, sz // 5), fill=col)
            x += sz + pad + 6
        return y + h + 10

    nb_dark = [(0x3A, 0x6E, 0xA5), (0x8A, 0x8A, 0x92), None, (0xC0, 0x50, 0x40),
               (0x4E, 0x8B, 0x4F), (0xE0, 0xB0, 0x40), (0x60, 0x60, 0x68)]
    nb_light = [(0x2A, 0x5E, 0x95), (0xD0, 0xD0, 0xD8), None, (0xB0, 0x40, 0x30),
                (0x3E, 0x7B, 0x3F), (0xD0, 0xA0, 0x30), (0xA0, 0xA0, 0xA8)]
    y = bar(y, (0x1B, 0x1B, 0x20), nb_dark,
            "mock dark taskbar (24px tray/tab size)", 24, 6)
    y = bar(y, (0xE8, 0xE8, 0xEC), nb_light,
            "mock light taskbar (24px tray/tab size)", 24, 6)
    y = bar(y, (0x28, 0x26, 0x32), nb_dark,
            "mock dock (64px)", 64, 10)

    sheet = sheet.crop((0, 0, W, min(y + 8, sheet.height)))
    out.parent.mkdir(parents=True, exist_ok=True)
    sheet.save(out)


# --------------------------------------------------------------------------
# main
# --------------------------------------------------------------------------

def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--sheet", type=Path, default=None,
                    help="also render a review contact sheet to this path")
    args = ap.parse_args()

    if not ICONS.is_dir():
        print(f"missing icon directory {ICONS}", file=sys.stderr)
        return 1

    masters = {edge: paint(SPECS[edge]).img for edge in MASTERS}
    ok = True

    print("-- ink ramp (indigo #%02x%02x%02x x the 5-step ramp) --" % INDIGO)
    print("  " + "  ".join(f"{n}=#{r:02x}{g:02x}{b:02x}"
                           for n, (r, g, b) in INK.items()))

    print("\n-- masters --")
    for edge in MASTERS:
        sp = SPECS[edge]
        img = masters[edge]
        census = check_palette(img, f"M{edge}")
        try:
            foot = check_footprint(img, sp)
            sym = check_symmetry(sp)
        except AssertionError as exc:
            ok, foot, sym = False, f"FAIL: {exc}", ""
        print(f"  M{edge:<4} {edge}x{edge}  {len(census)} colours  {foot}")
        print(f"        {sym}")

    print("\n-- legibility (measured, not asserted by eye) --")
    try:
        for line in check_contrast():
            print(f"  {line}")
    except AssertionError as exc:
        ok = False
        print(f"  FAIL: {exc}")
    try:
        print(f"  {check_not_downscaled(masters)}")
    except AssertionError as exc:
        ok = False
        print(f"  FAIL: {exc}")

    print("\n-- PNG targets --")
    for name, size in PNG_TARGETS.items():
        img = render(size, masters)
        img.save(ICONS / name)
        with Image.open(ICONS / name) as back:
            if back.size != (size, size):
                ok = False
                print(f"  FAIL: {name} is {back.size}, want {size}x{size}")
            check_palette(back, name)
            if pixels(back) != pixels(img):
                ok = False
                print(f"  FAIL: {name} did not round-trip through PNG")
        src = max(e for e in MASTERS if size % e == 0)
        print(f"  {name:<16} {size:>4}x{size:<4} <- M{src} x{size // src:<2} "
              f"sha {sha(ICONS / name)}")

    # ---- .ico ----------------------------------------------------------
    #
    # Pillow's ICO encoder LANCZOS-resamples any size it was not handed
    # directly (IcoImagePlugin._save), which would anti-alias the whole point
    # of this file away. Handing it an exact-size frame for every entry makes
    # it take them verbatim; the base image must be the largest, because it
    # drops any requested size bigger than the base.
    print("\n-- icon.ico --")
    frames = {sz: render(sz, masters) for sz in ICO_SIZES}
    biggest = max(ICO_SIZES)
    frames[biggest].save(
        ICONS / "icon.ico", format="ICO",
        sizes=[(s, s) for s in ICO_SIZES],
        append_images=[frames[s] for s in ICO_SIZES if s != biggest],
    )
    with Image.open(ICONS / "icon.ico") as ico:
        got = sorted(ico.info["sizes"])
        want = sorted((s, s) for s in ICO_SIZES)
        if got != want:
            ok = False
            print(f"  FAIL: holds {got}, want {want}")
        for sz in ICO_SIZES:
            ico.size = (sz, sz)
            ico.load()
            check_palette(ico, f"icon.ico@{sz}")
            if pixels(ico) != pixels(frames[sz]):
                ok = False
                print(f"  FAIL: the {sz}px frame is not the {sz}px master")
    print(f"  {len(ICO_SIZES)} frames {[s for s in ICO_SIZES]} — each one decoded "
          f"back to its own master, so nothing was resampled")
    print(f"  {(ICONS / 'icon.ico').stat().st_size} bytes  sha {sha(ICONS / 'icon.ico')}")

    # ---- .icns ---------------------------------------------------------
    print("\n-- icon.icns --")
    blob = build_icns(masters)
    (ICONS / "icon.icns").write_bytes(blob)
    try:
        chunks = parse_icns(blob)
        want_tags = set(ICNS_PNG) | set(ICNS_ARGB)
        if set(chunks) != want_tags:
            raise AssertionError(f"chunks {sorted(chunks)} != {sorted(want_tags)}")
        for tag, size in sorted(ICNS_PNG.items()):
            with Image.open(io.BytesIO(chunks[tag])) as im:
                if im.size != (size, size):
                    raise AssertionError(f"{tag!r} is {im.size}, want {size}")
                check_palette(im, tag.decode())
                if pixels(im) != pixels(render(size, masters)):
                    raise AssertionError(f"{tag!r} is not the {size}px master")
        for tag, size in sorted(ICNS_ARGB.items()):
            payload = chunks[tag]
            if payload[:4] != b"ARGB":
                raise AssertionError(f"{tag!r} missing the ARGB magic")
            body, off = payload[4:], 0
            planes = []
            for _ in range(4):
                plane, used = rle_decode(body[off:], size * size)
                if len(plane) != size * size:
                    raise AssertionError(f"{tag!r} plane decoded to {len(plane)} bytes")
                planes.append(plane)
                off += used
            if off != len(body):
                raise AssertionError(f"{tag!r} has {len(body) - off} trailing bytes")
            a, r, g, b = planes
            back = Image.frombytes("RGBA", (size, size),
                                   bytes(v for i in range(size * size)
                                         for v in (r[i], g[i], b[i], a[i])))
            ref = render(size, masters).convert("RGBA")
            # RGB under a transparent pixel is not observable; compare it the
            # way a compositor would.
            if [p if p[3] else (0, 0, 0, 0) for p in pixels(back)] != \
               [p if p[3] else (0, 0, 0, 0) for p in pixels(ref)]:
                raise AssertionError(f"{tag!r} did not decode back to the {size}px master")
        with Image.open(ICONS / "icon.icns") as im:
            if im.size != (1024, 1024):
                raise AssertionError(f"Pillow reads the largest slot as {im.size}")
            reps = sorted(im.info["sizes"])
        print(f"  {len(chunks)} chunks {[t.decode() for t in sorted(chunks)]}")
        print(f"  every PNG chunk == its master; ic04/ic05 ARGB decoded back to "
              f"the 16px and 32px masters")
        print(f"  Pillow reads {len(reps)} representations, largest 1024x1024")
        print(f"  {len(blob)} bytes  sha {sha(ICONS / 'icon.icns')}")
    except AssertionError as exc:
        ok = False
        print(f"  FAIL: {exc}")

    # ---- the directory holds exactly the delivered set ------------------
    expected = set(PNG_TARGETS) | {"icon.icns", "icon.ico"}
    on_disk = {p.name for p in ICONS.iterdir() if p.is_file() and not p.name.startswith(".")}
    print("\n-- delivered set --")
    if on_disk != expected:
        ok = False
        if expected - on_disk:
            print("  MISSING:", sorted(expected - on_disk))
        if on_disk - expected:
            print("  UNEXPECTED EXTRA:", sorted(on_disk - expected))
    else:
        print(f"  {ICONS.relative_to(REPO)} holds exactly {len(expected)} files")

    # ---- and tauri.conf.json still asks for the same five ---------------
    conf = (REPO / "desktop" / "src-tauri" / "tauri.conf.json").read_text()
    listed = [p for p in TAURI_ICON if f'"{p}"' in conf]
    if len(listed) != len(TAURI_ICON):
        ok = False
        print("  FAIL: tauri.conf.json no longer lists", 
              sorted(set(TAURI_ICON) - set(listed)))
    else:
        print(f"  tauri.conf.json's bundle.icon lists {len(listed)} files, all present")

    if args.sheet:
        review_sheet(masters, args.sheet)
        print(f"\nreview sheet -> {args.sheet}")

    if not ok:
        print("\nSELF-CHECK FAILED", file=sys.stderr)
        return 1
    print("\nall checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
