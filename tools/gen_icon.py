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

  * palette purity and BINARY alpha (0 or 255, never a midtone) of every
    master, every delivered PNG and every embedded .ico / .icns frame — the
    mark is transparent pixel art, so a semi-transparent halo pixel is a bug;
  * the opaque footprint of each master is EXACTLY its hooded bust — nothing
    outside the mark is painted, so the icon sits on true transparency and the
    OS (macOS, Windows, GNOME) supplies its own frame;
  * that mark FILLS the canvas — its bounding box is >= 80% of the edge in
    both axes, with at least a 1px margin all round so a plate-compositing
    desktop never clips it;
  * the silhouette (bust, opening, drawstrings) is mirror-symmetric to the
    pixel, while the shading deliberately is not;
  * measured WCAG contrast for the two reads a tile-less, faceless mark lives
    or dies by — the ink5 keyline against black (does the silhouette read on a
    dark Dock?) and the crown highlight against the dark opening (does the
    hood read as a ring, without eyes to anchor it?);
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
THE MARK: dexel, hooded, faceless, on transparency
--------------------------------------------------------------------------

The identity of this product is a character, so the icon is that character
and not a rebus about it. A hooded bust, front on, filling the frame: the
indigo hood's crown and cheeks as a ring of fabric, a dark FACELESS opening
where a face would be, two drawstrings hanging over the chest. No tile behind
it and no eyes in it — the mark stands on TRUE TRANSPARENCY so macOS, Windows
and GNOME each frame it with their own system chrome instead of a dusk
rounded-square baked into the file. (Owner direction, 2026-08: "remove the
glowing eyes. remove background also so that on mac and other places it's just
icon and the OS system.")

Front on, because the game's own camera is behind-the-shoulder (docs/
art-direction.md: "the hood is a **dome**, not a head: no face, no ears") and
a dome from behind reduces, at 16px, to a featureless bump on a trapezoid —
indistinguishable from a hundred other dark app icons. A front-facing hooded
bust keeps every brand-carrying feature: the hood, the indigo, the screen's
cool rim light, the drawstrings.

THE HARD PROBLEM this file solves — a dark indigo hood on a transparent
background is nearly invisible on a dark Dock: the old dusk tile was its
contrast and the old mint eyes were its accent, and both are gone. Three
things carry it instead, none of which assumes a background colour:

  1. A 1px `ink5` KEYLINE around the whole silhouette. `ink5` is the brightest
     indigo the hoodie tint reaches, and against black it is a 3.6:1 read and
     against a #14131a dark Dock a 3.2:1 read — so the outline of the figure
     is legible on the darkest ground it will ever sit on. On a white taskbar
     the dark fabric body is the silhouette instead. This is the same
     "screen glow wrapping the figure" rim the in-game character gets, now
     closed into a full self-contained outline because there is no tile edge
     left to do the job. `check_contrast` measures the black case.
  2. INTERNAL VALUE RANGE, so the form reads by its own shading and not
     against a tile: one key light from the upper left lifts the crown to
     `ink5`/`ink4` while the shoulders and the fabric under the hood's overhang
     fall to `ink2`/`ink3`. A flat indigo blob would be a lozenge; the shaded
     one is unmistakably a domed hood over shoulders.
  3. The OPENING stays a dark, empty `shadow` cavity — faceless, as asked —
     and the `ink5` hem rolled around its inner edge puts the brightest ink in
     the icon directly against the darkest, which is the crispest boundary
     available and the thing that still says "hood, seen front on" at 16px
     with no eyes to anchor it. `check_contrast` measures crown vs opening.

Every colour is derived, not chosen:

  * the hood is the **default hoodie tint composited over the 5-step ramp** —
    `INK[i] = round(0x6a5aa0 * RAMP[i] / 255)`, computed below from
    `gen_assets.RAMP` and the `indigo` tint hex the art direction declares.
    Those five hexes are, to the byte, what the running game multiplies onto
    `dev_form_*.png` for a player who has bought nothing. The icon is not
    "indigo-ish"; it is the same garment.
  * the opening is `shadow`, the palette's occlusion tone, as everywhere else.
  * the drawstrings are `cream` with a `gold` aglet — UI text colour and Dev
    Cash, the two things the product hands you.

Light direction is `gen_assets.py`'s: one key light from the upper-left. The
crown's upper-left arc carries the `ink5` specular, value falls off down and
to the right, and the hood casts a shadow band across the chest. Nothing here
is lit from another angle.

--------------------------------------------------------------------------
FIVE MASTERS, INTEGER UPSCALES ONLY
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
         `ink5` hem around the opening, 2px drawstrings with a `gold` aglet,
         and the full `ink5` keyline.
    M48  the same, one notch down: a smaller drawstring spread.
    M32  half of M64's resolution and half its detail: 1px drawstrings,
         dithering still on, the keyline and hem still on.
    M24  ordered dithering is OFF below 32px (at that scale a Bayer pattern is
         not a gradient, it is four stray pixels of noise): hard-edged bands
         only, no drawstrings, hem and keyline still on.
    M16  the reduction, and the size the mark was designed against first.
         Flat bands, no drawstrings, no hem (with only ~2px of fabric between
         the opening and the keyline, an `ink5` hem inside an `ink5` keyline
         would leave one mid pixel between two highlights and read as a
         mis-registration). The fabric also rides brighter than the other
         masters, because at 16px the ring has to win outright.
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

def rim_of(pts: set[tuple[int, int]]) -> set[tuple[int, int]]:
    """The 1px outermost ring of a point set — every member with at least one
    of its EIGHT neighbours outside it. Derived from the shape actually
    painted, the same way `outline_from_mask` derives sprite outlines, so it
    cannot go stale when a profile changes.

    Eight and not four: on the diagonal staircase of a curved edge a pixel on
    the true edge can still have all four orthogonal neighbours inside the
    shape, so a 4-neighbour test leaves the rim full of holes at every corner.
    """
    return {
        (x, y)
        for (x, y) in pts
        if any((x + dx, y + dy) not in pts
               for dx in (-1, 0, 1) for dy in (-1, 0, 1) if (dx, dy) != (0, 0))
    }


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
# One master, five times: the per-size design spec
# --------------------------------------------------------------------------
#
# Every number a master differs by lives in one of these. The builder below
# reads them and nothing else, so "hand-tuned per size" means editing a table
# rather than forking a drawing — and the five specs sit next to each other
# where the progressive simplification is visible at a glance.

@dataclass(frozen=True)
class Spec:
    edge: int              # canvas (and delivered) size

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

    hem: bool              # 1px `ink5` rolled edge around the opening
    string: tuple[int, int, int, int] | None  # (width, top, bottom, gap apart)
    aglet: bool            # `gold` tip on the last row of each drawstring
    dither: bool           # ordered dithering on the fabric
    base_v: float          # fabric ramp position at the form's mid tone


SPECS: dict[int, Spec] = {
    # ---------------------------------------------------------------- M64 --
    # The hero, and the master every size from 64px up is an integer upscale
    # of. With the dusk tile gone the mark is no longer inset — it FILLS the
    # canvas: the bust's bounding box is 58x57 in the 64x64 frame (91% wide,
    # 89% tall) with a 3px margin every side, so it reads big in a Dock and
    # still keeps a hair of air a plate-compositing desktop can round off.
    #
    # PROPORTION — a hood's MASS is the thing that says "hood", so the crown
    # carries ten rows of fabric above the brow and the opening (24px wide,
    # 26px tall) is one pixel taller than it is wide, a face rather than a
    # porthole. The shoulders flare from 42px to 58px over eleven rows and
    # then run straight down to the hem, so the outline STEPS OUT at the
    # bottom: a ghost has no shoulders, and that step is what stops a viewer
    # resolving the silhouette as one. The drawstrings start on the hem and
    # hang at a 13px spread, where a real hoodie's eyelets are.
    64: Spec(
        edge=64,
        crown=3, dome_c=22, hood_bot=37, sh_bot=48, bust_bot=59,
        a_hood=21.0, a_sh=29.0, n_dome=1.85, flare=0.72, taper=(0, 1, 2),
        cav_top=13, cav_bot=38, a_cav=12.0, n_cav=2.2,
        hem=True, string=(2, 39, 49, 13), aglet=True,
        dither=True, base_v=3.4,
    ),
    # ---------------------------------------------------------------- M48 --
    # Windows Explorer's "medium icons" size, the freedesktop 48px slot, and
    # an .ico frame — the most-seen size on Windows after 16. It gets its own
    # master rather than being M24 doubled, because at 48px there is room for
    # the gold aglet and the dithered shading that M24 cannot hold, and
    # doubling M24 would have thrown both away at the one size where they
    # still register.
    48: Spec(
        edge=48,
        crown=2, dome_c=16, hood_bot=28, sh_bot=36, bust_bot=44,
        a_hood=15.5, a_sh=21.5, n_dome=1.85, flare=0.72, taper=(0, 1, 2),
        cav_top=9, cav_bot=28, a_cav=9.0, n_cav=2.2,
        hem=True, string=(2, 29, 36, 10), aglet=True,
        dither=True, base_v=3.4,
    ),
    # ---------------------------------------------------------------- M32 --
    # Every anchor is roughly M64's halved and rounded to a whole pixel, so
    # the two read as the same drawing; the detail budget halves with it
    # (1px four-row strings).
    32: Spec(
        edge=32,
        crown=2, dome_c=11, hood_bot=19, sh_bot=24, bust_bot=29,
        a_hood=10.5, a_sh=14.0, n_dome=1.85, flare=0.72, taper=(0, 1),
        cav_top=6, cav_bot=19, a_cav=6.0, n_cav=2.2,
        hem=True, string=(1, 20, 24, 6), aglet=True,
        dither=True, base_v=3.5,
    ),
    # ---------------------------------------------------------------- M24 --
    # The tray/tab size on a HiDPI screen and an .ico frame. Ordered
    # dithering is OFF from here down: at this scale a 4x4 Bayer pattern is
    # not a gradient, it is four stray pixels of noise. The drawstrings go
    # too — 1px of `cream` on the chest at 24px is a speck of dirt, not a
    # detail — but the hem and the keyline stay: they are the whole reason the
    # faceless ring still reads without a tile behind it.
    24: Spec(
        edge=24,
        crown=1, dome_c=8, hood_bot=14, sh_bot=18, bust_bot=22,
        a_hood=8.0, a_sh=11.0, n_dome=1.85, flare=0.72, taper=(0, 1),
        cav_top=5, cav_bot=14, a_cav=4.5, n_cav=2.1,
        hem=True, string=None, aglet=False,
        dither=False, base_v=3.55,
    ),
    # ---------------------------------------------------------------- M16 --
    # The tab/tray size, and the size the mark was designed against first.
    # Flat bands, no drawstrings, no hem — with only ~2px of fabric between the
    # opening and the keyline, an `ink5` hem inside an `ink5` keyline leaves
    # one mid pixel between two highlights and reads as a mis-registration, so
    # the keyline alone carries the outer edge and the flat `shadow` opening
    # carries the middle. The fabric rides brighter here too, because at 16px
    # the ring has to win outright and there is no room left for a subtle one.
    16: Spec(
        edge=16,
        crown=1, dome_c=6, hood_bot=10, sh_bot=13, bust_bot=14,
        a_hood=5.5, a_sh=7.0, n_dome=1.85, flare=0.72, taper=(0,),
        cav_top=4, cav_bot=10, a_cav=3.0, n_cav=2.1,
        hem=False, string=None, aglet=False,
        dither=False, base_v=3.9,
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
    widening across the whole lower half instead (a quarter-ellipse to
    `bust_bot`) produced a bell — a cape or a chess pawn, not a person, and a
    silhouette a viewer resolves as "ghost". `flare` tunes the slope: 1.0 is a
    straight diagonal, below 1.0 rounds the deltoid corner outward.

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


def masks(sp: Spec) -> tuple[set, set, set]:
    """(bust, cavity, strings) as point sets.

    Computed before anything is painted, because the fabric needs to know
    where the opening is and the self-checks need each independently of the
    colours that ended up on them. There is no tile and there are no eyes any
    more: the opaque footprint of the icon is exactly `bust`.
    """
    cx = sp.edge / 2.0

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

    return bust, cavity, strings


# --------------------------------------------------------------------------
# Painting
# --------------------------------------------------------------------------

def fabric_v(sp: Spec, x: int, y: int) -> float:
    """The ramp position of one fabric pixel: one key light from the upper
    left, plus the hood's cast shadow on the chest.

    `nx` is signed horizontal position normalised by the shoulder half-width
    and `ny` vertical position through the bust, so the same expression
    produces the same LOOK at every master size instead of needing five sets
    of magic rows. The chest term is the only local one: the hood physically
    overhangs the chest, so the rows just under the opening drop away and
    that dark band is what stops the bust reading as one flat plate — and,
    now that there is no tile, it is half of the internal value range that
    makes the domed form read at all.
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
    """One master, painted back to front on TRANSPARENCY: fabric, the outer
    keyline, the opening, the hem, the drawstrings. Nothing paints outside
    `bust`, so every other pixel stays alpha 0."""
    s = Sprite(sp.edge, sp.edge, palette=ICON_PALETTE)
    bust, cavity, strings = masks(sp)
    cx = sp.edge / 2.0
    fabric = bust - cavity
    cav_rim = rim_of(cavity) if cavity else set()

    # ---- the hood fabric ----------------------------------------------
    # Banded ink shading (one key light, upper left, plus the chest cast
    # shadow): the internal value range that lets the domed form read without
    # a tile behind it.
    for (x, y) in sorted(fabric):
        s.dot(x, y, ink_at(x, y, fabric_v(sp, x, y), sp.dither))

    # ---- the keyline --------------------------------------------------
    # A 1px `ink5` outline around the WHOLE silhouette — the brightest indigo
    # the tint reaches, a 3.6:1 read on black and 3.2:1 on a dark Dock. With
    # the dusk tile gone this closed outline is the only thing holding the
    # figure's edge against a dark ground; on a white taskbar the dark fabric
    # body is the silhouette instead. `check_contrast` measures the black case.
    for (x, y) in rim_of(bust):
        if (x, y) in cavity:
            continue
        s.dot(x, y, "ink5")

    # ---- the opening --------------------------------------------------
    # Flat `shadow`: a dark, empty, FACELESS cavity (owner direction — the
    # mint eyes are gone). Opaque, not transparent, so the opening reads as an
    # occluded interior on a light ground instead of punching a bright hole
    # through the hood; on a dark ground it simply recedes.
    for (x, y) in sorted(cavity):
        s.dot(x, y, "shadow")
    if sp.hem:
        # The rolled front edge of the hood, 1px of `ink5` on the fabric side
        # of the opening. Two jobs: it says "garment with a thickness", and it
        # puts the brightest ink directly against the darkest pixel in the
        # icon, which is the crispest boundary available and the one that
        # makes the ring read as a hood at a glance — the job the eyes used to
        # do, now done by the edge of the opening itself.
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
    fails the build instead of shipping. Binary alpha is load-bearing here —
    the mark is transparent pixel art, so a single midtone edge pixel is a
    halo the OS would composite as fringe, and this is where it dies."""
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
    """Assert the master's opaque footprint is EXACTLY its hooded bust — every
    bust pixel painted, and NOTHING outside it painted, so the icon sits on
    true transparency with no stray tile pixel and no leak past the silhouette.

    This is the assertion that used to read "footprint == tile"; now that the
    tile is gone it is the thing that guarantees the background really is
    empty. Both halves matter: a hole means a bust pixel was never painted, and
    anything outside `bust` is a pixel the OS would frame as part of the mark.
    """
    px = img.convert("RGBA").load()
    opaque = {(x, y) for y in range(sp.edge) for x in range(sp.edge)
              if px[x, y][3] != 0}
    bust, _, _ = masks(sp)
    if opaque - bust:
        raise AssertionError(f"M{sp.edge}: {len(opaque - bust)} px outside the bust, "
                             f"e.g. {sorted(opaque - bust)[:4]}")
    if bust - opaque:
        raise AssertionError(f"M{sp.edge}: {len(bust - opaque)} unpainted bust px, "
                             f"e.g. {sorted(bust - opaque)[:4]}")
    return f"footprint == bust ({len(bust)} px), background fully transparent"


# The mark must OWN the canvas now that no tile pads it, but keep a hair of
# margin so a plate-compositing desktop (GNOME sometimes composites an icon on
# a rounded plate) never clips the silhouette.
FILL_FLOOR = 0.80          # bbox is at least this fraction of the edge, each axis
MARGIN_MIN = 1             # ...and at least this many clear px on every side


def check_fill(img: Image.Image, sp: Spec) -> str:
    """Assert the mark fills the frame (>= FILL_FLOOR in both axes) yet keeps
    at least MARGIN_MIN px of clear transparency on every side. This is the
    'raise' — with the tile's padding gone a free-standing mark should occupy
    more of the canvas — made into a number so a future edit that shrinks the
    mark back into a stamp, or lets it bleed to the very edge, fails."""
    px = img.convert("RGBA").load()
    xs = [x for y in range(sp.edge) for x in range(sp.edge) if px[x, y][3]]
    ys = [y for y in range(sp.edge) for x in range(sp.edge) if px[x, y][3]]
    x0, x1, y0, y1 = min(xs), max(xs), min(ys), max(ys)
    fw, fh = (x1 - x0 + 1) / sp.edge, (y1 - y0 + 1) / sp.edge
    margin = min(x0, y0, sp.edge - 1 - x1, sp.edge - 1 - y1)
    if fw < FILL_FLOOR or fh < FILL_FLOOR:
        raise AssertionError(f"M{sp.edge}: mark fills only {fw:.0%}x{fh:.0%} "
                             f"(floor {FILL_FLOOR:.0%})")
    if margin < MARGIN_MIN:
        raise AssertionError(f"M{sp.edge}: mark leaves only {margin}px margin "
                             f"(min {MARGIN_MIN})")
    return f"fills {fw:.0%}x{fh:.0%} of the frame, {margin}px clear margin"


def check_symmetry(sp: Spec) -> str:
    """Assert every SILHOUETTE is mirror-symmetric to the pixel.

    Not the colours: the shading is directional on purpose (one key light,
    upper left) and asserting symmetric pixels would forbid it. But an
    off-by-one in a span or a drawstring pair that drifted a column apart are
    invisible at 16px and glaring at 512, and this is the assertion that
    catches them.
    """
    bust, cavity, strings = masks(sp)
    for label, pts in (("bust", bust), ("cavity", cavity), ("strings", strings)):
        flip = {(sp.edge - 1 - x, y) for (x, y) in pts}
        if flip != pts:
            raise AssertionError(
                f"M{sp.edge}: {label} is not mirror-symmetric "
                f"({len(pts ^ flip)} px differ)")
    return (f"mirror-symmetric: bust {len(bust)}, opening {len(cavity)}, "
            f"strings {len(strings)}")


def _lum(rgb: tuple[int, int, int]) -> float:
    def ch(v: float) -> float:
        v /= 255.0
        return v / 12.92 if v <= 0.04045 else ((v + 0.055) / 1.055) ** 2.4
    r, g, b = rgb
    return 0.2126 * ch(r) + 0.7152 * ch(g) + 0.0722 * ch(b)


def contrast(a: tuple[int, int, int], b: tuple[int, int, int]) -> float:
    la, lb = _lum(a), _lum(b)
    return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)


# The two reads a tile-less, faceless mark lives or dies by, as measured
# ratios rather than as an opinion. `keyline vs black` is the whole silhouette
# on the darkest Dock it will ever sit on — the make-or-break, since there is
# no tile left to carry it. `crown vs opening` is whether the hood reads as a
# lit ring around a dark hole, the job the mint eyes used to do.
CONTRAST_FLOORS: dict[str, float] = {
    "keyline (ink5) vs black": 2.0,
    "crown (ink5) vs opening (shadow)": 2.0,
}


def check_contrast() -> list[str]:
    got = {
        "keyline (ink5) vs black": contrast(INK["ink5"], (0, 0, 0)),
        "crown (ink5) vs opening (shadow)": contrast(INK["ink5"], PALETTE["shadow"]),
    }
    lines = []
    for k, floor in CONTRAST_FLOORS.items():
        if got[k] < floor:
            raise AssertionError(f"contrast {k} = {got[k]:.2f}:1, floor {floor}:1")
        lines.append(f"{k}: {got[k]:.2f}:1 (floor {floor}:1)")
    # Reported, not floored: the dark-Dock read (#14131a) the sheet is judged
    # against, and the light-taskbar read (the dark fabric body is the
    # silhouette there, so it is measured against white).
    lines.append(f"  (informational) keyline vs #14131a dark Dock: "
                 f"{contrast(INK['ink5'], (0x14, 0x13, 0x1a)):.2f}:1")
    lines.append(f"  (informational) fabric body (ink1) vs white taskbar: "
                 f"{contrast(INK['ink1'], (0xff, 0xff, 0xff)):.2f}:1")
    return lines


def check_not_downscaled(masters: dict[int, Image.Image]) -> str:
    """Assert M16 is a DRAWING, not a shrink of M64.

    The brief this icon was built to calls per-size authoring "what separates
    industry-standard icons from resized ones", so the claim gets an
    assertion: against nearest, box and lanczos reductions of the hero
    master, more than 15% of M16's 256 pixels must differ. The floor was 25%
    while the tile, the mint eyes, the glow pool and the drawstrings all
    diverged the small end hard; a transparent, faceless, tile-less mark is
    simpler and converges more, so a nearest shrink now lands 23% away
    (60 of 256 px) rather than far more. That is still a real, hand-authored
    gap — M16 rides the fabric brighter (base_v 3.9), drops the hem, and
    re-cuts the opening and shoulders — and nearest is the ONLY resize that
    preserves the hard pixel-art edges (box lands 48% away, lanczos 85%), so
    it is the meaningful comparison. The floor is set to fail only if someone
    actually replaces a master with a resize, which would read ~0%.
    """
    small = masters[16].convert("RGBA")
    out = []
    for name, f in (("nearest", Image.NEAREST), ("box", Image.BOX),
                    ("lanczos", Image.LANCZOS)):
        naive = masters[64].convert("RGBA").resize((16, 16), f)
        diff = sum(1 for a, b in zip(pixels(small), pixels(naive)) if a != b)
        frac = diff / 256.0
        if frac <= 0.15:
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
    """The contact sheet this icon is judged on: every delivered size over a
    dark, a light and a mid-grey strip AND a transparency checkerboard —
    because the mark is transparent and faceless now, the dark-ground read is
    make-or-break and the sheet has to make that obvious — the five masters
    magnified so their individual pixels can be inspected, and mock
    dock/taskbar/dash rows. Not a build artifact — written only when `--sheet`
    asks for it."""
    from PIL import ImageDraw, ImageFont

    try:
        font = ImageFont.load_default(13)
        small = ImageFont.load_default(11)
    except TypeError:                       # very old Pillow
        font = small = ImageFont.load_default()

    DARK, LIGHT, MID = (0x14, 0x13, 0x1A), (0xF4, 0xF2, 0xEE), (0x80, 0x80, 0x88)
    sizes = (16, 24, 32, 48, 64, 128, 256, 512)
    W = 1420

    def checker_bg(x0: int, y0: int, w: int, h: int, cell: int = 12) -> None:
        a, b = (0xCF, 0xCF, 0xD6), (0xA6, 0xA6, 0xB0)
        for yy in range(0, h, cell):
            for xx in range(0, w, cell):
                c = a if ((xx // cell + yy // cell) % 2 == 0) else b
                d.rectangle([x0 + xx, y0 + yy,
                             min(x0 + xx + cell - 1, x0 + w - 1),
                             min(y0 + yy + cell - 1, y0 + h - 1)], fill=c)

    sheet = Image.new("RGB", (W, 2200), (0x24, 0x24, 0x2C))
    d = ImageDraw.Draw(sheet)

    def label(x: int, y: int, text: str, fill=(0xF2, 0xE0, 0xC9), f=None) -> None:
        d.text((x, y), text, fill=fill, font=f or small)

    def strip(y: int, bg, title: str, fg, checker=False) -> int:
        H = 300
        if checker:
            checker_bg(0, y, W, H)
        else:
            d.rectangle([0, y, W - 1, y + H], fill=bg)
        label(24, y + 12, title, fg, font)
        x = 24
        for sz in sizes:
            img = render(sz, masters)
            ps = min(sz, 256)
            paste = img if sz <= 256 else img.resize((ps, ps), Image.NEAREST)
            sheet.paste(paste, (x, y + H - 24 - ps), paste)
            label(x, y + H - 20, f"{sz}", fg)
            x += max(ps, 34) + 24
        return y + H

    y = 0
    label(24, y + 8, "dexel app icon - review sheet. Transparent, faceless, no "
                     "tile. Dark-ground legibility is make-or-break: judge the "
                     "top strip first. (512 shown at 256.)", f=font)
    y += 34
    y = strip(y, DARK, "on dark  #14131a  (the make-or-break: a dark Dock)",
              (0xF2, 0xE0, 0xC9))
    y = strip(y, LIGHT, "on light  #f4f2ee", (0x24, 0x1F, 0x2E))
    y = strip(y, MID, "on mid grey  #808088", (0x14, 0x13, 0x1A))
    y = strip(y, None, "on transparency (checkerboard = alpha 0)",
              (0x1A, 0x1A, 0x22), checker=True)

    # ---- pixel-peep: the masters magnified over a checkerboard ----------
    d.rectangle([0, y, W - 1, y + 330], fill=(0x1C, 0x1A, 0x24))
    label(24, y + 12, f"the {len(MASTERS)} masters, magnified over a checkerboard "
                      f"(each drawn at its own size, never resized)", f=font)
    x = 24
    for edge in MASTERS:
        zoom = max(2, 176 // edge)
        checker_bg(x, y + 44, edge * zoom, edge * zoom)
        img = masters[edge].convert("RGBA").resize((edge * zoom, edge * zoom),
                                                   Image.NEAREST)
        sheet.paste(img, (x, y + 44), img)
        label(x, y + 44 + edge * zoom + 6, f"M{edge} at {zoom}x")
        x += edge * zoom + 22
    y += 330

    # ---- mock OS context rows ------------------------------------------
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
            elif col is not None:
                d.rounded_rectangle([x, y + pad, x + sz - 1, y + pad + sz - 1],
                                    radius=max(2, sz // 5), fill=col)
            x += sz + pad + 6
        return y + h + 10

    nb_dark = [(0x3A, 0x6E, 0xA5), (0x8A, 0x8A, 0x92), None, (0xC0, 0x50, 0x40),
               (0x4E, 0x8B, 0x4F), (0xE0, 0xB0, 0x40), (0x60, 0x60, 0x68)]
    nb_light = [(0x2A, 0x5E, 0x95), (0xD0, 0xD0, 0xD8), None, (0xB0, 0x40, 0x30),
                (0x3E, 0x7B, 0x3F), (0xD0, 0xA0, 0x30), (0xA0, 0xA0, 0xA8)]
    y = bar(y, (0x22, 0x24, 0x2E), nb_dark,
            "mock macOS dock (dark)  64px", 64, 10)
    y = bar(y, (0x1B, 0x1B, 0x20), nb_dark,
            "mock Windows taskbar (dark)  24px", 24, 6)
    y = bar(y, (0xE8, 0xE8, 0xEC), nb_light,
            "mock Windows taskbar (light)  24px", 24, 6)
    y = bar(y, (0x2C, 0x2A, 0x36), nb_dark,
            "mock GNOME dash (dark)  48px", 48, 8)

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
            fill = check_fill(img, sp)
            sym = check_symmetry(sp)
        except AssertionError as exc:
            ok, foot, fill, sym = False, f"FAIL: {exc}", "", ""
        print(f"  M{edge:<4} {edge}x{edge}  {len(census)} colours  {foot}")
        print(f"        {fill}")
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
