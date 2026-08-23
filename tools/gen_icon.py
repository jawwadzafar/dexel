#!/usr/bin/env python3
"""Generate dexel's application icon set — every file `bundle.icon` names —
as true pixel art, procedurally.

    python3 tools/gen_icon.py

Writes into `desktop/src-tauri/icons/`:

    32x32.png          the 32px master, verbatim
    128x128.png        the 64px master, nearest-upscaled 2x
    128x128@2x.png     the 64px master, nearest-upscaled 4x  (= 256px)
    icon.png           the 64px master, nearest-upscaled 16x (= 1024px master)
    icon.icns          built by `iconutil` from a full 10-entry .iconset
    icon.ico           multi-size ICO (16/32/48/64/128/256), every frame
                       supplied at its exact size so Pillow never resamples

Those PNGs are **build artifacts that happen to be committed** (ADR 0004,
`.claude/skills/pixel-art-authoring`): this file is the source of truth, and a
re-run with no source change rewrites the same bytes. The run ends with a
self-check — the delivered file set, exact pixel sizes, palette purity of
every emitted PNG *and of every .ico frame*, the plate-silhouette invariant,
and that the .icns reads back at 1024 — and exits non-zero if anything fails,
so a botched edit here cannot quietly ship a blank, off-palette or leaking
icon.

macOS-only for the `.icns` step: `iconutil` ships with the Xcode command line
tools. Everything else is pure Pillow and runs anywhere.

--------------------------------------------------------------------------
THE MARK
--------------------------------------------------------------------------

A dusk-purple rounded plate, a mint-framed terminal screen, and a gold Dev
Cash coin tucked in front of its lower-right corner. Three shapes, four
palette hues doing the work (`wall_dark` plate, `screen`/`screen_dim` screen,
`cream` caret, `gold` coin), because that is literally what dexel is: *you
type at a screen, and the screen pays you.*

It is a MARK, not a shrunken screenshot. Nothing of the room, the chair or the
hooded character is in here. Two of those were tried: the game's own monitor
sprite (a `metal` bezel around a `shadow` well) vanished on the `wall_dark`
plate, because all three of those palette entries are near-black purples; and
a character in front of a screen collapses at 32px into a dark blob on a dark
plate. What survives reduction is a big bright rectangle and one gold disc.

Light direction is the same as every sprite in `gen_assets.py`: one soft key
light from the upper-left. The plate's gradient runs light at the top-left to
dark at the bottom-right; the plate rim is mint on its lit half; the screen
frame's top and left edges step up from `screen_dim` to `screen`; the coin's
highlight and glint sit up and left of its centre. Nothing here is lit from
another angle.

**Legibility on light AND dark backgrounds** — macOS shows both, and a
dark-only mark disappears in a dark Dock — is handled twice over: the plate
carries a 1px rim that is LIGHTER than its fill all the way round (never a
bottom-right shadow, which would be invisible against dark), and the mark's
own content is bright, so even where the plate merges into a dark ground the
mint frame and the gold coin still read.

--------------------------------------------------------------------------
THREE MASTERS, INTEGER UPSCALES ONLY
--------------------------------------------------------------------------

Rule 2 of pixel-art authoring is "NEAREST only, never resize in the
generator" — so an icon set spanning 16px to 1024px cannot come from one
drawing. It comes from three hand-tuned masters, each authored at its native
size, and every delivered size is an *integer nearest upscale* of one of
them:

    16   <- M16  x1        128  <- M64  x2
    32   <- M32  x1        256  <- M64  x4
    48   <- M16  x3        512  <- M64  x8
    64   <- M64  x1        1024 <- M64  x16

Same composition at all three, progressively simplified toward the small end
the way macOS's own icons are: M64 has a 2px frame, three terminal lines, a
6x6 caret and a coin with an edge ring and a glint; M32 halves all of that;
M16 drops to single-row bars, a 2x3 caret, a 5px coin with neither ring nor
glint, and a plain `wall_light` plate rim (a mint one would sit 1px from the
card's own mint frame and read as a mis-registration). Detail that survives
at 128px is mud at 16px.

Downsampling is never used. `48` is M16 x3 for exactly that reason, not a
shrunk M64.
"""
from __future__ import annotations

import subprocess
import sys
import tempfile
from pathlib import Path

from PIL import Image

# The palette, the Sprite canvas and the ordered-dither helpers all come from
# the sprite generator rather than being re-declared here. Duplicating the 18
# hexes into a second file is how a palette drifts; `gen_assets.py` is the one
# place they live (art-direction "the palette is one dict at the top of the
# generator"). Importing it also means the icon literally shares
# `bayer_mix`'s dithering mechanics with every sprite in the game.
sys.path.insert(0, str(Path(__file__).resolve().parent))
from gen_assets import RGB_TO_NAME, Sprite, bayer_mix  # noqa: E402

REPO = Path(__file__).resolve().parent.parent
ICONS = REPO / "desktop" / "src-tauri" / "icons"

# --------------------------------------------------------------------------
# Delivered files. `bundle.icon` in desktop/src-tauri/tauri.conf.json lists
# the first five; `icon.png` is the 1024 master that Tauri's own `tauri icon`
# command (and any future re-derivation) treats as the source image.
# --------------------------------------------------------------------------
#
# name -> (edge in px, which master it is an integer upscale of)
PNG_TARGETS: dict[str, tuple[int, int]] = {
    "32x32.png": (32, 32),
    "128x128.png": (128, 64),
    "128x128@2x.png": (256, 64),
    "icon.png": (1024, 64),
}

# The ten entries `iconutil` expects in an .iconset directory, in Apple's
# naming scheme. Missing entries are not fatal to iconutil but they are how
# an icon ends up blurry in one particular UI (Finder list view, the
# cmd-tab switcher, Get Info), so all ten are emitted.
ICONSET_ENTRIES: dict[str, int] = {
    "icon_16x16.png": 16,
    "icon_16x16@2x.png": 32,
    "icon_32x32.png": 32,
    "icon_32x32@2x.png": 64,
    "icon_128x128.png": 128,
    "icon_128x128@2x.png": 256,
    "icon_256x256.png": 256,
    "icon_256x256@2x.png": 512,
    "icon_512x512.png": 512,
    "icon_512x512@2x.png": 1024,
}

# Windows .ico frames. 256 is the largest an ICO directory entry can address,
# and 48 is the one Explorer size that is not a power of two — it comes from
# M16 x3, never from shrinking a bigger master.
ICO_SIZES: tuple[int, ...] = (16, 32, 48, 64, 128, 256)


# --------------------------------------------------------------------------
# Shape helpers
# --------------------------------------------------------------------------

def rounded_rect(x0: int, y0: int, x1: int, y1: int, r: int) -> set[tuple[int, int]]:
    """The point set of a hard-edged rounded rectangle (a "squircle" plate).

    Each pixel is kept when its CENTRE — continuous (x+0.5, y+0.5), the rect
    spanning [x0, x1+1) — falls within `r` of the nearest corner circle's
    centre, which sits at continuous (x0+r, y0+r) and its three mirrors. No
    PIL `ImageDraw` anywhere: that would anti-alias the curve.

    Rasterising pixel centres, rather than pixel indices with `Sprite.ellipse`'s
    1.05 outward fudge, is what makes the corner staircase MONOTONE. The fudge
    is right for the tiny filled blobs it was written for and wrong here: at
    r=12 it produced insets 10,7,5,4,3,2,2,1,1,1,0,0 — a 3px jump, then a
    stall, then another stall — which at 1024px read as visibly chewed
    corners. Centres give 9,6,5,4,3,2,1,1,1,0,0,0.
    """
    pts: set[tuple[int, int]] = set()
    lo_x, hi_x = x0 + r, x1 + 1 - r
    lo_y, hi_y = y0 + r, y1 + 1 - r
    for y in range(y0, y1 + 1):
        for x in range(x0, x1 + 1):
            # Clamping the pixel centre into the inner rect makes the straight
            # edges trivially pass and only the four corners curve.
            dx = (x + 0.5) - min(max(x + 0.5, lo_x), hi_x)
            dy = (y + 0.5) - min(max(y + 0.5, lo_y), hi_y)
            if dx * dx + dy * dy <= r * r:
                pts.add((x, y))
    return pts


def rim_of(pts: set[tuple[int, int]]) -> set[tuple[int, int]]:
    """The 1px outermost ring of a point set — every member with at least one
    of its EIGHT neighbours outside it. Derived from the shape actually
    painted, the same way `outline_from_mask` derives sprite outlines, so it
    cannot go stale when the plate's radius changes.

    Eight and not four: on the diagonal staircase of a rounded corner, a
    pixel on the true edge can still have all four orthogonal neighbours
    inside the shape. A 4-neighbour test therefore skips it, and the rim
    comes out with holes at every corner — which is exactly how the first
    pass's plate ended up looking nibbled.
    """
    return {
        (x, y)
        for (x, y) in pts
        if any((x + dx, y + dy) not in pts
               for dx in (-1, 0, 1) for dy in (-1, 0, 1) if (dx, dy) != (0, 0))
    }


def ellipse_points(cx: int, cy: int, r: int) -> list[tuple[int, int]]:
    """The pixels of a hard-edged circle of radius `r` about (cx, cy) — as a
    point list, because the coin is shaded per pixel rather than filled flat.

    Rasterised the same way `rounded_rect` is: pixel CENTRES against a circle
    of radius `r + 0.5`. `Sprite.ellipse`'s index test with its 1.05 fudge is
    wrong at the small end — at r=2 it keeps (0,-2) and (-2,0) but drops
    (-1,-2), so a 5px "circle" comes out a DIAMOND, which is exactly what the
    16px master's coin looked like when blown up to the 48px .ico frame. The
    centre test gives the classic 5px disc (rows 3,5,5,5,3) and stays a clean
    circle at r=9.
    """
    out = []
    limit = (r + 0.5) ** 2
    for y in range(cy - r, cy + r + 1):
        for x in range(cx - r, cx + r + 1):
            dx, dy = x - cx, y - cy
            if dx * dx + dy * dy <= limit:
                out.append((x, y))
    return out


# --------------------------------------------------------------------------
# The three shared elements: plate, screen card, coin
# --------------------------------------------------------------------------

def paint_plate(s: Sprite, x0: int, y0: int, x1: int, y1: int, r: int,
                rim_hi: str, rim_lo: str) -> set[tuple[int, int]]:
    """The rounded dusk plate the whole mark sits on, and its 1px rim.

    The fill is one ordered-dithered DIAGONAL gradient, `wall_light` at the
    top-left corner falling to `wall_dark` at the bottom-right — the scene's
    one key light, and the same two wall colours `build_room_back` shades the
    room with. The two are close enough in value that a Bayer mix between
    them reads as a smooth gradient rather than as dots, which is exactly
    what a background wants and why no third colour joins the plate.

    (The first pass used a radial bloom centred on the screen instead. It
    covered the whole plate at middling density, so the dither pattern read
    as an even halftone — visible grime rather than light. A gradient that
    actually reaches both ends of its two colours does not have that
    problem: the corners are solid.)

    The rim splits on the anti-diagonal, same light: `rim_hi` on the lit
    half, `rim_lo` on the shadowed half. Both are LIGHTER than the fill,
    deliberately — the plate is dark, so on a dark background the silhouette
    has to come from a light edge. A rim that only darkened the bottom-right
    would leave the icon edgeless in a dark Dock.
    """
    pts = rounded_rect(x0, y0, x1, y1, r)
    w = float(x1 - x0) or 1.0
    h = float(y1 - y0) or 1.0
    for (x, y) in pts:
        # 1.0 at the lit top-left corner, 0.0 at the shadowed bottom-right.
        # Weighted toward y: a plate lit more from above than from the side
        # matches how the room's own wall band sits.
        t = 1.0 - (0.62 * (y - y0) / h + 0.38 * (x - x0) / w)
        # Gamma 1.8 narrows the 50%-density band, where a two-colour Bayer mix
        # is at its most obviously checkered, into a thin diagonal and lets
        # most of the plate sit at a near-solid density. Without it the middle
        # of the plate is one large visible checkerboard.
        s.dot(x, y, bayer_mix(x, y, max(0.0, min(1.0, t)) ** 1.8,
                              "wall_dark", "wall_light"))

    mid_x = (x0 + x1) / 2.0
    mid_y = (y0 + y1) / 2.0
    for (x, y) in rim_of(pts):
        s.dot(x, y, rim_hi if (x - mid_x) + (y - mid_y) <= 0 else rim_lo)
    return pts


def paint_card(s: Sprite, x0: int, y0: int, x1: int, y1: int,
               frame: int) -> tuple[int, int, int, int]:
    """The screen: a `shadow` well inside a mint frame `frame` px thick.

    THIS IS THE CHANGE THAT MADE THE ICON READ. The first pass drew the
    game's actual monitor — a `metal` bezel around a `shadow` screen well on
    a `wall_dark` plate. Rendered, all three of those are near-black purples
    (`#2b2b33`, `#241f2e`, `#2f2a3d`), so the monitor had no silhouette at
    all: the icon was a mint bar chart floating on a dark square. An icon
    gets one value structure and it has to be legible at 16px, so the frame
    is now the palette's dim mint (`screen_dim`) — the colour the game uses
    for older terminal lines — with its lit top and left edge stepped up to
    full `screen`. Dark interior, bright edge: a screen, in two colours,
    down to a 10x8 pixel footprint.

    Returns the interior rect the terminal content is allowed to touch.
    """
    s.rect(x0, y0, x1, y1, "screen_dim")
    for i in range(frame):
        s.hline(y0 + i, x0 + i, x1 - i, "screen")
        s.vline(x0 + i, y0 + i, y1 - i, "screen")
    ix0, iy0, ix1, iy1 = x0 + frame, y0 + frame, x1 - frame, y1 - frame
    s.rect(ix0, iy0, ix1, iy1, "shadow")
    return ix0, iy0, ix1, iy1


def paint_bars(s: Sprite, bars, cursor) -> None:
    """The terminal content: bars of decreasing length plus a block cursor.

    Colour follows the art direction's terminal rule verbatim — the newest
    two lines are `screen`, everything older is `screen_dim` — so the icon's
    screen is coloured by the same law as the game's own. The cursor is
    `cream` (the UI text colour) and stands a row proud of its line, the way
    a block caret does in a real terminal; it is the only warm note inside
    the screen and it is what stops the content reading as a bar chart.
    """
    for (y0, y1, x0, x1, colour) in bars:
        s.rect(x0, y0, x1, y1, colour)
    cx0, cy0, cx1, cy1 = cursor
    s.rect(cx0, cy0, cx1, cy1, "cream")


def paint_coin(s: Sprite, cx: int, cy: int, r: int, rim: int, glint: int) -> None:
    """The gold Dev Cash coin, sitting in front of the screen's lower-right
    corner — the half of the product the screen cannot say.

    Structure, outside in: a `shadow` contour one pixel proud of the disc, a
    `pot` edge ring `rim` px thick, a `gold` face, and a `lamp` highlight
    offset up and left inside that face, topped by a flat `cream` glint.
    Every band is SOLID. Dithering is right for a gradient across dozens of
    pixels; on a 19px disc — far more so on the 5px disc of the 16px master —
    it only makes the coin look moth-eaten.

    Two earlier passes got the shading wrong in instructive ways. Banding on
    the DIAGONAL read as a stack of three stripes: a bun, not a disc. Banding
    radially from an off-centre highlight (no edge ring) read as a bread roll,
    because a sphere is not what a coin is — the thing that makes a coin a
    coin is a concentric darker edge with a flat lit face inside it. Hence
    `rim`: a real ring, not a terminator crescent. `rim=0` falls back to the
    plain lit disc, which is all a 5px coin has room for.

    The `shadow` contour matters as much as the gold: the coin overlaps the
    mint frame, and without a dark contact edge the two shapes merge instead
    of reading as one in front of the other.
    """
    for (x, y) in ellipse_points(cx, cy, r + 1):
        s.dot(x, y, "shadow")
    # The highlight sits up and left of centre — the scene's one key light.
    hx, hy = cx - r * 0.32, cy - r * 0.32
    face = max(1, r - rim)
    for (x, y) in ellipse_points(cx, cy, r):
        if rim and ((x - cx) ** 2 + (y - cy) ** 2) > face * face * 1.05:
            s.dot(x, y, "pot")
            continue
        d = (((x - hx) ** 2 + (y - hy) ** 2) ** 0.5) / r
        if d <= 0.44:
            s.dot(x, y, "lamp")
        elif not rim and d >= 1.25:
            # No edge ring to carry the shadow side, so the face carries it.
            s.dot(x, y, "pot")
        else:
            s.dot(x, y, "gold")
    if glint:
        gx, gy = cx - (r + 1) // 2, cy - (r + 1) // 2
        s.rect(gx, gy, gx + glint - 1, gy + glint - 1, "cream")


# --------------------------------------------------------------------------
# The three masters
# --------------------------------------------------------------------------
#
# No monitor stand at any size. The first pass had one (`metal` neck + foot +
# contact shadow); it was invisible at 32px, it was three grey rows fighting
# the coin at 16px, and dropping it is what let the screen card grow. What is
# left is the three shapes the mark actually is: plate, bright-edged screen,
# gold coin — identical composition at all three sizes, so the icon does not
# change identity between the Dock and a Finder list.

def build_m64() -> Sprite:
    """64x64 — the detailed master. Every size from 64px up is this drawing
    nearest-upscaled by 1x/2x/4x/8x/16x, so this is the one that has to hold
    up at 1024px: a 2px frame, three terminal lines with distinct lengths, a
    6x6 caret, and the coin's edge ring and glint.

    The card and coin were both grown once, after looking at the 1024: the
    first layout left the whole lower-left quadrant of the plate empty, which
    at icon scale reads as an off-centre mark rather than as breathing room.
    The card+coin group's bounding box is now centred on the plate to within
    a pixel, with the negative space split between the lower left and the
    upper right instead of pooling in one corner.
    """
    s = Sprite(64, 64)
    paint_plate(s, 4, 4, 59, 59, 12, rim_hi="screen_dim", rim_lo="wall_light")
    interior = paint_card(s, 9, 10, 50, 41, frame=2)
    assert interior == (11, 12, 48, 39), interior
    paint_bars(
        s,
        bars=[
            (15, 18, 14, 43, "screen_dim"),   # oldest line, dim
            (24, 27, 14, 37, "screen"),
            (33, 36, 14, 25, "screen"),       # newest line, the caret follows it
        ],
        cursor=(28, 32, 33, 37),
    )
    paint_coin(s, 46, 46, 9, rim=1, glint=2)
    return s


def build_m32() -> Sprite:
    """32x32 — shipped verbatim as `32x32.png`, and as the .icns 16x16@2x and
    32x32 entries. The same composition at half the resolution, its rects
    derived from M64's as fractions of the plate and then rounded to whole
    pixels: a 1px frame, bars 2px tall, a 3x4 caret and a 1px coin ring."""
    s = Sprite(32, 32)
    paint_plate(s, 2, 2, 29, 29, 6, rim_hi="screen_dim", rim_lo="wall_light")
    interior = paint_card(s, 5, 5, 25, 20, frame=1)
    assert interior == (6, 6, 24, 19), interior
    paint_bars(
        s,
        bars=[
            (8, 9, 8, 22, "screen_dim"),
            (12, 13, 8, 19, "screen"),
            (16, 17, 8, 13, "screen"),
        ],
        cursor=(15, 15, 17, 18),
    )
    paint_coin(s, 23, 23, 4, rim=1, glint=1)
    return s


def build_m16() -> Sprite:
    """16x16 — the reduction, and the size the whole mark was designed
    against first. The bars are single rows, the caret is 2x3, and the coin
    is a 5px disc with no room for an edge ring or a glint.

    The plate rim drops to flat `wall_light` here rather than the mint
    `screen_dim` the bigger masters use: with only 2px between the plate edge
    and the card, a teal plate rim sits directly beside the card's own teal
    frame and the two read as one mis-registered border.
    """
    s = Sprite(16, 16)
    paint_plate(s, 1, 1, 14, 14, 4, rim_hi="wall_light", rim_lo="wall_light")
    interior = paint_card(s, 3, 3, 12, 10, frame=1)
    assert interior == (4, 4, 11, 9), interior
    paint_bars(
        s,
        bars=[
            (5, 5, 5, 10, "screen_dim"),
            (7, 7, 5, 7, "screen"),
        ],
        cursor=(9, 6, 10, 8),
    )
    paint_coin(s, 11, 11, 2, rim=0, glint=0)
    return s


MASTERS = {16: build_m16, 32: build_m32, 64: build_m64}


# --------------------------------------------------------------------------
# Rendering: integer NEAREST upscales only
# --------------------------------------------------------------------------

def render(size: int, masters: dict[int, Image.Image]) -> Image.Image:
    """One delivered size, as an integer nearest-neighbour upscale of the
    largest master that divides it exactly.

    Picking the largest such master is what makes 48 come from M16 (x3)
    rather than from a non-integer shrink of M64, and 128 come from M64 (x2)
    rather than M32 (x4) — more detail whenever the arithmetic allows it. A
    size no master divides is a bug in the tables above, not something to
    paper over with a resample, so it raises.
    """
    for edge in sorted(masters, reverse=True):
        if size % edge == 0:
            factor = size // edge
            if factor == 1:
                return masters[edge].copy()
            return masters[edge].resize((size, size), Image.NEAREST)
    raise AssertionError(f"no master divides {size}px exactly — masters: {sorted(masters)}")


# --------------------------------------------------------------------------
# Self-check
# --------------------------------------------------------------------------

def check_palette(img: Image.Image, label: str) -> int:
    """Assert every opaque pixel is one of the 18 palette colours, and return
    the opaque count. Same guarantee `gen_assets.py`'s self-check gives every
    sprite: an icon that drifted off-palette (or that some resampler
    anti-aliased on the way out) fails the build instead of shipping."""
    rgba = img.convert("RGBA")
    px = rgba.load()
    w, h = rgba.size
    opaque = 0
    for y in range(h):
        for x in range(w):
            r, g, b, a = px[x, y]
            if a == 0:
                continue
            if a != 255:
                raise AssertionError(f"{label}: partial alpha {a} at ({x},{y}) — anti-aliased")
            if (r, g, b) not in RGB_TO_NAME:
                raise AssertionError(
                    f"{label}: off-palette pixel #{r:02x}{g:02x}{b:02x} at ({x},{y})")
            opaque += 1
    return opaque


# The plate each master is drawn on: (x0, y0, x1, y1, radius). Declared here
# rather than only inside the builders so the self-check can assert against it
# independently — a builder that computed its own answer would prove nothing.
PLATES: dict[int, tuple[int, int, int, int, int]] = {
    16: (1, 1, 14, 14, 4),
    32: (2, 2, 29, 29, 6),
    64: (4, 4, 59, 59, 12),
}


def check_silhouette(img: Image.Image, edge: int) -> str:
    """Assert the master's opaque footprint is EXACTLY its plate — nothing
    outside it, and no hole inside it.

    Both halves matter. A hole means some pixel of the plate never got
    painted. Anything outside means an element leaked past the rounded
    corner: the coin sits in the bottom-right corner and its `shadow` contour
    is the thing most likely to poke out, which at icon scale does not read
    as a coin popping off the plate — it reads as a chipped icon. This
    assertion is what lets the coin be nudged around freely without having to
    re-derive the corner arithmetic by hand every time.
    """
    px = img.convert("RGBA").load()
    opaque = {(x, y) for y in range(edge) for x in range(edge) if px[x, y][3] != 0}
    plate = rounded_rect(*PLATES[edge])
    outside = sorted(opaque - plate)
    holes = sorted(plate - opaque)
    if outside:
        raise AssertionError(
            f"M{edge}: {len(outside)} px outside the plate silhouette, e.g. {outside[:4]}")
    if holes:
        raise AssertionError(
            f"M{edge}: {len(holes)} unpainted px inside the plate, e.g. {holes[:4]}")
    return f"silhouette == plate ({len(plate)} px)"


def main() -> int:
    if not ICONS.is_dir():
        print(f"missing icon directory {ICONS}", file=sys.stderr)
        return 1

    masters = {edge: build().img for edge, build in MASTERS.items()}
    ok = True

    print("-- masters --")
    for edge in sorted(masters):
        img = masters[edge]
        opaque = check_palette(img, f"M{edge}")
        try:
            silhouette = check_silhouette(img, edge)
        except AssertionError as exc:
            ok = False
            silhouette = f"FAIL: {exc}"
        print(f"  M{edge:<4} {img.size[0]}x{img.size[1]}  {opaque:>7} opaque px  "
              f"palette-pure  {silhouette}")

    # ---- the plain PNGs -------------------------------------------------
    print("\n-- PNG targets --")
    for name, (size, from_master) in PNG_TARGETS.items():
        img = render(size, masters)
        img.save(ICONS / name)
        factor = size // from_master
        opaque = check_palette(Image.open(ICONS / name), name)
        print(f"  {name:<16} {size:>4}x{size:<4} <- M{from_master} x{factor:<2} "
              f"{opaque:>7} opaque px")
        if render(size, masters).size != (size, size):
            ok = False
            print(f"  FAIL: {name} is not {size}x{size}")

    # ---- .icns via iconutil --------------------------------------------
    #
    # Pillow CAN write ICNS, but its encoder is pure Python and no macOS
    # bundler had ever consumed the result (desktop/README.md's "NOT
    # verified" list, item 4). `iconutil` is the tool Apple's own toolchain
    # uses, so the container is beyond question and only the artwork is ours.
    print("\n-- icon.icns (iconutil) --")
    with tempfile.TemporaryDirectory() as tmp:
        iconset = Path(tmp) / "icon.iconset"
        iconset.mkdir()
        for entry, size in ICONSET_ENTRIES.items():
            render(size, masters).save(iconset / entry)
        icns = ICONS / "icon.icns"
        proc = subprocess.run(
            ["iconutil", "-c", "icns", "-o", str(icns), str(iconset)],
            capture_output=True, text=True,
        )
        if proc.returncode != 0:
            ok = False
            print(f"  FAIL: iconutil exited {proc.returncode}: {proc.stderr.strip()}")
        else:
            print(f"  {len(ICONSET_ENTRIES)} iconset entries "
                  f"({', '.join(str(s) for s in sorted(set(ICONSET_ENTRIES.values())))}px) "
                  f"-> {icns.name} ({icns.stat().st_size} bytes)")
            with Image.open(icns) as read_back:
                # Pillow reports the largest embedded representation, which
                # is the one that has to be the 1024 master.
                if read_back.size != (1024, 1024):
                    ok = False
                    print(f"  FAIL: icon.icns largest entry is {read_back.size}, want 1024x1024")
                else:
                    print("  reads back: largest entry 1024x1024")

    # ---- .ico ----------------------------------------------------------
    #
    # Pillow's ICO encoder LANCZOS-resamples any size it was not handed
    # directly (see IcoImagePlugin._save), which would anti-alias the whole
    # point of this file away. Handing it an exact-size frame for every
    # entry in `sizes` makes it take them verbatim; the base image must be
    # the largest, because it drops any requested size bigger than the base.
    print("\n-- icon.ico (exact-size frames, no resampling) --")
    frames = {size: render(size, masters) for size in ICO_SIZES}
    base = frames[max(ICO_SIZES)]
    base.save(
        ICONS / "icon.ico",
        format="ICO",
        sizes=[(s, s) for s in ICO_SIZES],
        append_images=[frames[s] for s in ICO_SIZES if s != max(ICO_SIZES)],
    )
    with Image.open(ICONS / "icon.ico") as ico:
        got = sorted(ico.info["sizes"])
        want = sorted((s, s) for s in ICO_SIZES)
        if got != want:
            ok = False
            print(f"  FAIL: icon.ico holds {got}, want {want}")
        else:
            print(f"  {len(ICO_SIZES)} frames {got} "
                  f"({(ICONS / 'icon.ico').stat().st_size} bytes)")
        # Every frame, not just the largest, must still be palette-pure —
        # this is the assertion that would catch a silent LANCZOS fallback.
        for size in ICO_SIZES:
            ico.size = (size, size)
            ico.load()
            check_palette(ico, f"icon.ico@{size}")
        print("  every frame palette-pure (so nothing was resampled)")

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
        print(f"  {ICONS.relative_to(REPO)} contains exactly {len(expected)} files: "
              f"{', '.join(sorted(expected))}")

    if not ok:
        print("\nSELF-CHECK FAILED", file=sys.stderr)
        return 1
    print("\nall checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
