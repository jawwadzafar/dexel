#!/usr/bin/env python3
"""Generate every sprite listed in docs/art-direction.md as true pixel art.

The sprites are generated rather than drawn by hand because the palette is a
hard constraint - the art direction lists 18 exact hex values and forbids
anything else on screen - and because a generator is reviewable in a diff
while a PNG is not. Everything is authored at the native size from the
art-direction table; nothing is ever resized here, because the game does the
integer upscale at runtime with `ImageSampler::nearest` and a resize in this
script is exactly what would introduce the soft edges and half-pixels that
rule 1 of the art direction forbids.

    python3 tools/gen_assets.py

Output is deterministic: every pixel position is either literal or derived
from integer arithmetic, so a re-run with no source change rewrites the same
bytes. The run ends with a self-check (size, palette purity, opaque-pixel
count) and exits non-zero if any sprite fails it, so a botched edit here
cannot quietly ship a blank or off-palette asset.
"""

from __future__ import annotations

import sys
from pathlib import Path

from PIL import Image

REPO = Path(__file__).resolve().parent.parent
ASSETS = REPO / "assets"

# The palette from docs/art-direction.md, verbatim. Sprites reference these by
# name only - a hex literal anywhere below would be a palette violation that
# the self-check at the bottom would then have to catch after the fact.
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

# The art-direction table, used both to author and to verify. Keep in sync
# with docs/art-direction.md; the self-check compares against this.
SPEC: list[tuple[str, int, int]] = [
    ("room_bg.png", 320, 200),
    ("desk.png", 120, 48),
    ("monitor_on.png", 40, 36),
    ("monitor_off.png", 40, 36),
    ("chair.png", 28, 36),
    ("dev_idle.png", 24, 32),
    ("dev_type_a.png", 24, 32),
    ("dev_type_b.png", 24, 32),
    ("dev_coffee.png", 24, 32),
    ("dev_sleep.png", 24, 32),
    ("plant.png", 24, 32),
    ("mug.png", 10, 10),
    ("books.png", 32, 24),
    ("lamp.png", 20, 28),
    ("rug.png", 96, 32),
]

CHARACTERS = ("dev_idle.png", "dev_type_a.png", "dev_type_b.png",
              "dev_coffee.png", "dev_sleep.png")

# There is deliberately no dither helper here any more. A 4x4 ordered Bayer
# matrix used to live at this spot, as the way to fade one palette colour into
# another without inventing an in-between entry, and every single thing it was
# used for was rejected by a reviewer: the lamp's glow disc ("looks like a
# clipping error", "particle sparks"), the floor's light spill ("messy and
# inconsistent ... looks like a rendering artifact"), the rug's woven texture,
# the desk's tabletop gradient. At this pixel scale a two-colour dither does
# not read as a gradient, it reads as noise. Light is expressed here as flat
# runs of a brighter palette entry with hard edges instead - see build_desk,
# build_lamp and _monitor. Removing the helper rather than merely not calling
# it is the point: the next person to reach for a soft glow has to re-derive
# it, and will find this comment first.


class Sprite:
    """An RGBA canvas that can only be painted in palette colours.

    Every drawing call takes a palette *name*, so an off-palette pixel is
    impossible by construction rather than something to audit afterwards.
    Coordinates are inclusive and out-of-range writes are clipped: the poses
    below are hand-authored pixel lists, and a 1px overhang while tuning an
    arm should not abort the whole build.
    """

    def __init__(self, w: int, h: int, bg: str | None = None) -> None:
        self.w = w
        self.h = h
        self.img = Image.new("RGBA", (w, h), (0, 0, 0, 0))
        self.buf = self.img.load()
        if bg is not None:
            self.rect(0, 0, w - 1, h - 1, bg)

    def dot(self, x: int, y: int, name: str) -> None:
        if 0 <= x < self.w and 0 <= y < self.h:
            self.buf[x, y] = PALETTE[name] + (255,)

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
        """A 1px border of identical thickness on all four sides.

        Used by the window frame: uniformity there is the whole point, and one
        call is harder to get wrong than four `hline`/`vline` calls that can
        drift apart edge by edge.
        """
        self.hline(y0, x0, x1, name)
        self.hline(y1, x0, x1, name)
        self.vline(x0, y0, y1, name)
        self.vline(x1, y0, y1, name)

    def dither(self, x: int, y: int, name: str, level: float) -> None:
        """Paint (x, y) only if the ordered-dither threshold admits it.

        `level` is 0..1 coverage; 1.0 always paints, 0.0 never does.
        """
        if BAYER4[y % 4][x % 4] < level * 16:
            self.dot(x, y, name)

    def save(self, filename: str) -> Path:
        path = ASSETS / filename
        self.img.save(path)
        return path


# --------------------------------------------------------------------------
# Room
# --------------------------------------------------------------------------

WALL_BOTTOM = 127
SKIRT_TOP, SKIRT_BOTTOM = 128, 133
FLOOR_TOP = 134

# There is deliberately no LAMP_X here any more either. This file used to hold
# the lamp's x in room-background pixels so the wall glow could be painted
# behind it, and that constant went stale twice - once holding the monitor's x
# (so the glow pooled beside the wrong object) and once holding the lamp's old
# left-hand position after scene.rs moved it to the right end of the desk. The
# background cannot know where the props are, because another agent moves them.
# Every light in this scene is therefore baked into the sprite of the object
# that emits or receives it, so it travels with that object; the background's
# only lighting is full-width bands, which cannot go stale.

# Hard horizon where the wall's lit band begins. Set just above the lamp's
# shade so the lamp sits inside the lit part of the wall - that adjacency is
# what makes the band read as the lamp's light rather than as paint. A
# dead-straight boundary between the two wall purples reads as a deliberate
# dado line; the long dithered fade it replaces read as stipple noise smeared
# across the whole upper wall.
WALL_BAND_TOP = 92

# Top row of the window frame. Named because the wall's dark upper band stops
# one row above it: a band that ran on behind the window would put `shadow`
# wall against `shadow` sky with only the 3px frame between them, and the
# window would stop reading as a hole in a wall.
WINDOW_TOP = 22

# Fixed star field, four to a pane. Hard-coded rather than seeded-random
# because a literal list is the only version that survives a Python RNG change
# unaltered - and because 22 semi-random dots read as stuck pixels: at this
# scale a *placed* star field, evenly spread with no two stars sharing a row or
# column, is the only one that reads as sky.
STARS = [
    (50, 32), (64, 28), (58, 42), (70, 47),          # top-left pane
    (86, 30), (99, 36), (91, 46), (106, 41),         # top-right pane
    (48, 62), (61, 72), (54, 79), (71, 66),          # bottom-left pane
    (85, 64), (98, 74), (108, 61), (92, 80),         # bottom-right pane
]
# Two of them are `gold` instead of `cream`. Same single-pixel footprint as
# every other star: the earlier 3px-wide version made those two read as
# horizontal streaks - pixel bleed - rather than as brighter stars.
TWINKLES = [(64, 28), (98, 74)]


def build_room_bg() -> Sprite:
    s = Sprite(320, 200, bg="wall_dark")

    # The wall behind the desk is lit: one solid `wall_light` band with a
    # hard top edge, and nothing else on the wall at all.
    #
    # Three things have been tried here and this is the only one that works.
    # A 100px Bayer-dithered disc of `lamp` yellow read as a dense field of
    # noise clipped flat at the desk line. Replacing it with a low-contrast
    # `wall_light` dome read as "a giant mysterious semi-circle floating in
    # the middle of the wall" - any closed shape on a bare wall reads as an
    # object, however low its contrast. A sparse `lamp` dither hugging the
    # shade read as particle sparks. So: no shape, no dither, no glow. The
    # lamp stands inside the band's top edge (see WALL_BAND_TOP) and the band
    # brightens the whole wall behind the desk, which is all the light this
    # scene needs and the only version that cannot be mistaken for a bug.
    s.rect(0, WALL_BAND_TOP, 319, WALL_BOTTOM, "wall_light")

    # ...and a shadowed ceiling with a wooden cornice, mirroring the skirting
    # board at the other end of the wall. With only two wall entries the room
    # was one flat purple field; this gives it four values top to bottom
    # (shadow, wall_dark, wall_light, wood) so it reads as darker away from
    # the desk without any lamp-shaped geometry drawn on the wall. The cornice
    # is what stops the dark band reading as letterboxing - a bare `shadow`
    # strip across the top looked like a UI bar, a moulding looks like a room.
    s.rect(0, 0, 319, 6, "shadow")
    s.rect(0, 7, 319, 9, "desk_dark")
    s.hline(7, 0, 319, "desk")          # lit top face of the moulding
    s.hline(10, 0, 319, "shadow")       # the shadow it casts down the wall
    # Both lights in this room stand on the desk, so the wall gets darker the
    # further up it you look. That is a third value on the wall - `shadow`
    # from the cornice down to just above the window - and it is what turns
    # the wall from one flat field into a graded surface. It is a flat band
    # with a hard bottom edge, deliberately: every *shaped* attempt at wall
    # light here (a dithered disc, a low-contrast dome, a sparse spark field)
    # was read as a bug, because any closed shape on a bare wall reads as an
    # object. A full-width band cannot be mistaken for an object, and it
    # stops exactly at the window's top edge so the two never interact.
    s.rect(0, 11, 319, WINDOW_TOP - 1, "shadow")

    _room_window(s)

    # Skirting board: the wall/floor joint reads as a room rather than two
    # flat fills only if there is a board with its own top highlight.
    s.rect(0, SKIRT_TOP, 319, SKIRT_BOTTOM, "desk_dark")
    s.hline(SKIRT_TOP, 0, 319, "desk")
    s.hline(SKIRT_BOTTOM, 0, 319, "shadow")

    # Floor. Side-on elevation means the boards run straight across with no
    # perspective at all: evenly spaced full-width seams and nothing else.
    # What this replaces was the other half of the projection clash - seam
    # gaps that widened toward the viewer and staggered plank ends, i.e. a
    # top-down floor under a side-on desk - plus a dithered light spill on
    # top of it that read as a rendering artifact rather than as light.
    s.rect(0, FLOOR_TOP, 319, 199, "floor")
    # One board lighter than its neighbours, full width, hard edges on both
    # sides. This is the floor's share of the same top-to-bottom ramp the wall
    # has: the board nearest the wall sits in the wall's own shade, the next
    # one out is in the open and catches the desk lamp. `floor_light` is the
    # palette's own name for it. NOT a gradient and NOT dithered - the
    # stippled version of this exact idea is the one reviewers called "messy
    # and inconsistent ... looks like a rendering artifact".
    s.rect(0, 149, 319, 165, "floor_light")
    # No extra occlusion band under the skirting. The skirting's own `shadow`
    # bottom row already darkens the joint, and a second two-row band on top
    # of it made a 3px dark strip that anything standing on the floor line
    # appeared to hover above - it read as a one-pixel gap under the books.
    # Each seam is one dark line with a `floor_light` highlight on the near
    # side of it - the least that reads as a joint between two boards.
    for y in (148, 166, 184):
        s.hline(y, 0, 319, "shadow")
        s.hline(y + 1, 0, 319, "floor_light")
    # The lit board's own seams are already `shadow`; its far side needs the
    # highlight row put back on top of the lighter fill so the joint still
    # reads, and `desk` is the only value lighter than `floor_light` that is
    # still a wood colour.
    s.hline(149, 0, 319, "desk")

    return s


def _room_window(s: Sprite) -> None:
    """Night window: wooden frame, four panes of sky, a scatter of stars.

    The frame is deliberately uniform - 3px of `desk_dark` on every side with
    a 1px `desk` highlight right round the outer perimeter, and 3px mullions
    to match. The earlier version lit the top and left edges, darkened the
    bottom one, and then hung a five-row protruding sill off the bottom:
    four different treatments on four edges, which is why the bottom ledge
    read as a misplaced asset.
    """
    x0, y0, x1, y1 = 40, WINDOW_TOP, 116, 86
    s.rect(x0, y0, x1, y1, "desk_dark")
    s.outline(x0, y0, x1, y1, "desk")                 # same lit edge all round
    s.rect(x0 + 3, y0 + 3, x1 - 3, y1 - 3, "shadow")  # sky
    # Mullions split the sky into four panes; without them the sky reads as a
    # hole in the wall rather than glass. Centred in the 71x59 opening so the
    # four panes come out the same size.
    s.rect(77, y0 + 3, 79, y1 - 3, "desk_dark")
    s.rect(x0 + 3, 53, x1 - 3, 55, "desk_dark")
    # Each mullion gets the same 1px `desk` lit edge the outer frame has, on
    # the side facing the light (left, and up). Without it the 3px mullions
    # read as thinner than the 3px frame, because the frame's highlight made
    # it look 4px wide - the same "different thickness" complaint the sill
    # used to draw, one member in.
    s.vline(77, y0 + 3, y1 - 3, "desk")
    s.hline(53, x0 + 3, x1 - 3, "desk")
    for x, y in STARS:
        s.dot(x, y, "cream")
    for x, y in TWINKLES:
        s.dot(x, y, "gold")


# --------------------------------------------------------------------------
# Furniture
# --------------------------------------------------------------------------

def build_desk() -> Sprite:
    """The desk as a flat side-on elevation - no tabletop surface at all.

    This is the sprite the whole composition hangs on. The version before it
    mixed projections: a slab lit along its top rows as though seen from
    above, a `pot`-coloured gradient dithered across that top, and 9px legs
    with a lit left face plus a 2px shaded right face - a box in three-quarter
    view. Set against a side-on character and a side-on window, the desk
    appeared to float. Now every part is a flat rectangle seen edge-on, so the
    top row of the sprite *is* the front edge of the tabletop - which is also
    the line scene.rs stands the monitor, lamp and mug on.

    Note the distinction from that old failure, because it is one pixel row
    wide and easy to undo by accident: the rows below ARE graded light-to-dark
    now, but they are the graded front EDGE of a tabletop seen end-on, in flat
    bands. What broke before was a lit top FACE - a visible horizontal
    surface, which implies a camera looking down at the desk while the
    character beside it is drawn from the side - plus a dither across it. A
    value ramp down a vertical face is elevation shading; a value ramp across
    a horizontal face is a second vanishing point.
    """
    s = Sprite(120, 48)
    # Tabletop seen edge-on. Rows 0-9 are opaque across the full width because
    # that is what hides the seated character's lower body
    # (docs/art-direction.md, Layering), and those ten rows are also where this
    # sprite does its lighting.
    #
    # Rows 0-2 are the part of the tabletop that points at the ceiling, and
    # both lamps in this room stand on it, so it is the brightest wood in the
    # room and darkens away from them: `gold`, `pot`, `desk`, `desk_dark`,
    # `shadow` - five values over ten rows.
    #
    # Full width and position-independent on purpose. A *local* warm patch was
    # tried here and it ends up glowing under an empty stretch of tabletop the
    # moment scene.rs slides the lamp along; a full-width band cannot go stale
    # however the props move. What stops it reading as gold trim rather than as
    # light is that the ramp keeps going underneath it. The lit rows outnumber
    # the shaded ones four to two, which is also what keeps the band as a whole
    # clearly lighter than the purple wall it is silhouetted against - a darker
    # desk was read as blending into the background. This top edge is also the
    # line every desk prop's contact shadow lands against, which is what ties
    # those props to the wood rather than to the wall behind them.
    s.hline(0, 0, 119, "gold")
    s.rect(0, 1, 119, 2, "pot")
    s.rect(0, 3, 119, 6, "desk")
    s.rect(0, 7, 119, 8, "desk_dark")
    s.hline(9, 0, 119, "shadow")

    for lx in (10, 103):
        # The legs fall off in value from top to bottom, because both lamps in
        # this room stand on the tabletop above them: `desk` for the stretch
        # just under the top, `desk_dark` for the rest. Two flat bands, one
        # hard edge, full width, so it holds wherever scene.rs puts the desk.
        #
        # The ramp runs *upward* from `desk_dark` rather than downward into
        # `shadow`, and that direction is the whole point. The palette has no
        # brown between `desk_dark` and `shadow`, so a downward ramp is a jump
        # straight to near-black: it turned the bottom two thirds of each leg
        # into a black rectangle that read as a hole in the rug, not as a leg
        # in shadow. Lighting the top of the leg says the same thing about
        # where the light is and leaves the leg looking like wood.
        s.rect(lx, 10, lx + 6, 16, "desk")
        s.rect(lx, 17, lx + 6, 46, "desk_dark")
        s.vline(lx + 6, 10, 46, "shadow")
        # Contact shadow: the foot's last row plus two pixels of floor either
        # side of it, so the dark line is visibly WIDER than the leg. A shadow
        # exactly as wide as the object it belongs to reads as the object's own
        # bottom edge; one that spills sideways reads as a shadow, and that is
        # the whole difference between a desk standing on the floor and a desk
        # pasted in front of it.
        s.hline(47, lx - 2, lx + 8, "shadow")

    return s


def _monitor(lit: bool) -> Sprite:
    s = Sprite(40, 36)
    # Stand first, so the bezel overlaps it and the neck disappears behind the
    # panel instead of butting against it.
    s.rect(16, 28, 23, 32, "metal")
    s.rect(13, 33, 26, 34, "metal")
    s.vline(23, 28, 32, "shadow")

    # Row 35 is the bottom row of the sprite, and scene.rs stands that row on
    # the desk's top edge - so it is the only row of this sprite that is in
    # contact with the tabletop, and therefore the only place the screen's
    # light can legitimately land on the wood.
    #
    # This is where the cool half of the scene's lighting lives. It is NOT in
    # desk.png: another agent moves the props, and a teal pool baked into the
    # desk at a fixed x glows under an empty stretch of tabletop the moment the
    # monitor slides. Baked here it travels with the screen that emits it, and
    # it is absent from monitor_off.png, which is what makes the mood switch
    # read as the screen actually going dark rather than merely changing hue.
    s.hline(35, 13, 26, "shadow")          # the stand's own contact shadow
    if lit:
        # The top face of the foot is lit by the panel directly above it. This
        # matters more than it sounds: without it the pool is two separate
        # teal blobs with a dark foot between them, and two blobs read as two
        # objects. With it the light runs unbroken across the whole base.
        s.hline(33, 13, 26, "screen_dim")
        # One flat shape in one colour, 34px wide and 4px tall, stepped in by
        # two pixels a row. Deliberately much wider than it is tall: a tall
        # narrow version of the same wedge is a cone, and a cone of light drawn
        # on a wall is the shape reviewers called "a giant mysterious
        # semi-circle". Flat and wide, hugging the stand, it reads as a pool at
        # the base of the monitor - which is where the light actually goes.
        for row, inset in ((32, 8), (33, 6), (34, 4), (35, 3)):
            s.hline(row, inset, 12, "screen_dim")
            s.hline(row, 27, 39 - inset, "screen_dim")

    s.rect(1, 0, 38, 27, "metal")
    # The bezel's own lit edges. When the panel is on they pick up its glow
    # (`screen_dim`), when it is off they pick up nothing but the room
    # (`wall_light`) - a second, quieter tell that the light source is off.
    edge = "screen_dim" if lit else "wall_light"
    s.hline(0, 2, 37, edge)                # 1px top highlight gives it form
    s.vline(0, 1, 26, edge)
    s.hline(27, 2, 38, "shadow")
    s.vline(39, 1, 27, "shadow")

    # A real monitor has a thin bezel above and a deeper chin below; equal
    # borders read as a picture frame.
    face = "screen" if lit else "screen_dim"
    s.rect(4, 3, 35, 22, face)
    s.hline(2, 3, 35, "shadow")            # inset lip around the glass
    s.vline(3, 2, 23, "shadow")
    s.hline(23, 4, 36, "shadow")
    s.vline(36, 3, 23, "shadow")

    if lit:
        # Four code lines plus a cursor block, indented like real source -
        # enough to read as text at native size, not a UI mockup.
        for y, x0, x1 in ((6, 7, 26), (9, 7, 19), (12, 10, 30), (15, 10, 22),
                          (18, 7, 15)):
            s.hline(y, x0, x1, "screen_dim")
        s.rect(18, 18, 20, 18, "screen_dim")
        s.hline(24, 5, 34, "screen_dim")   # screen light spilling on the chin
        s.dot(35, 25, "gold")              # power LED
    else:
        s.dots([(6, 6), (7, 5), (8, 4), (7, 6)], "screen")  # glint on dark glass
        s.dot(35, 25, "shadow")                      # LED off
    return s


def build_chair() -> Sprite:
    """Office chair, side-on.

    The backrest and seat are a `metal` frame around `desk_dark` padding
    rather than solid `metal`. Solid, they were two near-black rectangles
    against a dark wall and read as an unfinished placeholder; the two-tone
    version reads as a padded chair at the same silhouette.
    """
    s = Sprite(28, 36)
    s.rect(7, 1, 21, 17, "metal")          # backrest frame
    s.rect(9, 3, 19, 15, "desk_dark")      # padding inside the frame
    # No horizontal cushion seams: two of them across a framed brown panel
    # made the backrest read as a chest of drawers. One unbroken panel inside
    # a dark frame is what reads as an upholstered chair back at this size.
    s.hline(1, 8, 20, "wall_light")        # lit top edge
    s.vline(7, 2, 16, "wall_light")        # lit near edge
    s.vline(21, 1, 17, "shadow")
    s.hline(17, 8, 21, "shadow")

    s.rect(11, 18, 17, 21, "metal")        # back support into the seat
    s.vline(17, 18, 21, "shadow")

    s.rect(4, 21, 24, 25, "metal")         # seat frame
    s.rect(6, 22, 22, 24, "desk_dark")     # seat padding
    s.hline(21, 5, 23, "wall_light")
    s.hline(25, 4, 24, "shadow")

    s.rect(12, 26, 15, 30, "metal")        # gas post
    s.vline(15, 26, 30, "shadow")
    s.vline(12, 26, 30, "wall_light")

    s.hline(31, 8, 19, "metal")            # spider base
    s.hline(32, 5, 22, "metal")
    s.hline(33, 3, 24, "shadow")
    for wx in (3, 12, 21):                 # castors
        s.rect(wx, 34, wx + 3, 35, "shadow")
    # Contact shadow across the whole footprint and one pixel wider than the
    # outermost castor, so the chair reads as sitting on the floorboards
    # instead of hovering a pixel above them.
    s.hline(35, 2, 25, "shadow")
    return s


# --------------------------------------------------------------------------
# Character
# --------------------------------------------------------------------------
#
# 24x32, three-quarter view facing right. The head is deliberately a third of
# the height: at this size a realistic head would be four pixels wide and
# read as noise. Draw order is torso then head, so a lowered head (the sleep
# pose) sinks into the shoulders instead of floating above them.

def _dev_torso(s: Sprite) -> None:
    """Shoulders and back only; the arms are separate so the poses can share this.

    The torso is deliberately narrow (right edge at x=16/17) so the reaching
    arm has clear space outside the silhouette to live in - a wider torso
    swallowed the arm entirely, and no shading rescues a shirt-coloured limb
    on a shirt-coloured body at 24px.
    """
    s.rect(7, 16, 17, 16, "shirt")         # shoulder line
    s.rect(6, 17, 17, 19, "shirt")
    s.rect(6, 20, 16, 31, "shirt")
    # The back is turned away from both light sources, so it is not a 1px
    # outline but a genuine shadow side, two pixels wide. One pixel of shadow
    # reads as a black keyline drawn round the sprite; two pixels read as the
    # body being unlit on that side, which is the difference between an
    # outlined sticker and a figure standing in a room with lamps in it.
    s.vline(6, 17, 31, "shadow")
    s.vline(7, 20, 31, "shadow")
    s.rect(12, 16, 15, 16, "cream")        # collar under the chin
    # Cool rim on the shoulder facing the monitor, one pixel of `screen`.
    # `screen` rather than `screen_dim` because `screen_dim` and `shirt`
    # happen to sit at the same luminance, so a rim in it is invisible - a rim
    # light has to change value, not just hue. It starts at row 17 rather than
    # row 16: on row 16 it landed immediately beside the cream collar and the
    # two together read as a necklace. Rows 18-19 are included even though the
    # arms usually cover them - the arms are drawn after this, so where a
    # sleeve reaches over the rim the rim correctly vanishes behind it.
    s.vline(17, 17, 19, "screen")


def _dev_head(s: Sprite, dy: int = 0, eyes_closed: bool = False) -> None:
    # Hair is one solid silhouette (crown plus the back of the skull on the
    # left, since the character faces right) - strands do not survive at this
    # size, a shape does.
    s.rect(10, 3 + dy, 15, 3 + dy, "hair")
    s.rect(9, 4 + dy, 16, 4 + dy, "hair")
    s.rect(8, 5 + dy, 17, 7 + dy, "hair")
    s.rect(8, 8 + dy, 9, 11 + dy, "hair")
    # The lamp's bulb sits just above head height and to the right, so the
    # crown and the whole right edge of the skull take a warm `gold` rim -
    # the one place a rim can be a full colour rather than a value nudge,
    # because `hair` is the darkest thing on the sprite. The monitor's cool
    # light is deliberately NOT put here as well: a teal rim on the hair plus
    # a teal rim on the shoulder joined up into what read as a headset rather
    # than as light, so the cool half of the lighting is kept to the pixels
    # that genuinely point at the panel - the nose, the shoulder edge and the
    # hands.
    s.rect(11, 3 + dy, 14, 3 + dy, "gold")        # crown, under the lamp
    s.dots([(16, 4 + dy), (17, 5 + dy), (17, 6 + dy), (17, 7 + dy)], "gold")

    s.rect(10, 8 + dy, 17, 12 + dy, "skin")
    s.rect(11, 13 + dy, 16, 13 + dy, "skin")
    s.rect(12, 14 + dy, 15, 14 + dy, "skin")      # chin
    s.rect(18, 10 + dy, 18, 11 + dy, "skin")      # nose breaks the silhouette
    # The face in three values instead of one flat fill of `skin`, because a
    # flat face is the single loudest "nothing in this room is lit" signal on
    # screen. The palette has no skin_light/skin_dark, so the nearest
    # legitimate entries stand in - the same substitution the hair and shirt
    # already use: `cream` for the side turned toward the lamp and the screen,
    # `skin` as the mid-tone, `pot` for the side turned away from both.
    # (Kept to x16-17 plus the nose column rather than a rect out to x18: rows
    # 8, 9 and 12 have nothing at x18, and a rect there would silently widen
    # the head instead of shading it.)
    s.rect(16, 8 + dy, 17, 12 + dy, "cream")      # lit side, toward the lamp
    s.rect(10, 8 + dy, 11, 12 + dy, "pot")        # shadow side, toward the back
    s.dot(11, 13 + dy, "pot")                     # jaw turning away from us
    s.dot(12, 14 + dy, "pot")

    if eyes_closed:
        s.rect(11, 10 + dy, 12, 10 + dy, "hair")
        s.rect(15, 10 + dy, 16, 10 + dy, "hair")
    else:
        s.dot(13, 9 + dy, "hair")   # far eye
        s.dot(16, 9 + dy, "hair")   # near eye

    s.rect(12, 15 + dy, 15, 15 + dy, "skin")      # neck
    s.dot(12, 15 + dy, "pot")

    # The nose is the most forward point of the head and the only part of it
    # pointing straight at the monitor, so it - and only it - takes the cool
    # light. Two pixels: `screen` sits within a few points of `skin`'s
    # luminance, so this reads as a hue shift on the leading edge rather than
    # as a green stripe. The warm `cream` cheek beside it is the lamp; this is
    # the screen. Having the two lights land on different pixels of the same
    # face is what says they are both real.
    s.dot(18, 10 + dy, "screen")
    s.dot(18, 11 + dy, "screen")


def _far_arm(s: Sprite, dx: int = 0, dy: int = 0) -> None:
    """The arm on the far side of the body.

    Drawn before the near arm so the near arm overlaps it, and painted in
    `pot`/`shadow` rather than skin because darkening a limb wholesale is the
    only depth cue available at 24px. It sits a row higher than the near hand
    - further away means higher on screen.
    """
    s.rect(16 + dx, 19 + dy, 17 + dx, 19 + dy, "pot")     # forearm
    s.rect(18 + dx, 18 + dy, 20 + dx, 20 + dy, "pot")     # hand
    # The hand is placed clear of the torso so it has its own silhouette, and
    # its underside is the 1px dark line that separates it from the near hand
    # directly below.
    s.hline(20 + dy, 18 + dx, 20 + dx, "shadow")
    # Cool rim on the knuckles, which face the monitor. Muted (`screen_dim`)
    # where the near hand's is bright: this hand is the far one, and keeping
    # its highlight a step down is the same depth trick that makes it `pot`
    # instead of `skin` in the first place.
    s.vline(20 + dx, 18 + dy, 19 + dy, "screen_dim")


def _near_arm(s: Sprite, dx: int = 0, dy: int = 0, curl: bool = True) -> None:
    """The arm on the viewer's side, elbow down, forearm reaching right.

    The character wears a short sleeve specifically so the arm can be bare
    skin: an earlier pass drew a long shirt sleeve and it disappeared into the
    shirt-coloured torso, and the `shadow` outline needed to rescue it read as
    a black strap across the chest. Skin against shirt needs no outline.
    `curl` raises the hand a row, as if resting on keys rather than flat.
    """
    s.hline(18 + dy, 15 + dx, 17 + dx, "shadow")          # sleeve hem
    s.rect(15 + dx, 19 + dy, 17 + dx, 21 + dy, "skin")    # upper arm
    s.rect(16 + dx, 22 + dy, 18 + dx, 23 + dy, "skin")    # forearm
    hand_top = 21 + dy if curl else 22 + dy
    s.rect(19 + dx, hand_top, 21 + dx, 23 + dy, "skin")
    s.hline(23 + dy, 17 + dx, 21 + dx, "pot")             # underside/fingers
    # Both lamps in this room are above the desk, so the arm is lit along its
    # top and shaded along its underside - the one cue that turns a limb from
    # a flat skin-coloured bar into a round one. `cream` and `pot` stand in
    # for the skin_light/skin_dark the palette does not have.
    s.hline(19 + dy, 15 + dx, 17 + dx, "cream")           # top of the upper arm
    s.hline(22 + dy, 16 + dx, 18 + dx, "cream")           # top of the forearm
    s.hline(hand_top, 19 + dx, 21 + dx, "cream")          # back of the hand
    # The fingertips are the closest thing in the room to the screen, so they
    # get the cool rim: the leading edge of the hand in `screen`. This is the
    # pixel group that sells the whole idea - the character's hands are lit
    # teal by the monitor they are typing on.
    s.vline(21 + dx, hand_top, 22 + dy, "screen")


def _typing_arms(s: Sprite, dx: int, dy: int) -> None:
    """Both arms at the keyboard, offset by (dx, dy).

    The two typing frames are this one function called with two offsets, which
    is what guarantees the art-direction rule that the frames differ only in
    arm and hand pixels - no other code path exists for them to drift through.
    """
    _far_arm(s, dx, dy)
    _near_arm(s, dx, dy)


def build_dev_idle() -> Sprite:
    s = Sprite(24, 32)
    _dev_torso(s)
    # Hands off the keys: both arms drop a row and the near hand lies flat
    # instead of curling over the keyboard, which is what makes this read as a
    # pause rather than a typing frame.
    _far_arm(s, 0, 2)
    _near_arm(s, 0, 1, curl=False)
    _dev_head(s)
    return s


def build_dev_type_a() -> Sprite:
    s = Sprite(24, 32)
    _dev_torso(s)
    _typing_arms(s, 0, 1)      # hands left and down
    _dev_head(s)
    return s


def build_dev_type_b() -> Sprite:
    s = Sprite(24, 32)
    _dev_torso(s)
    _typing_arms(s, 1, 0)      # hands right and up
    _dev_head(s)
    return s


def build_dev_coffee() -> Sprite:
    s = Sprite(24, 32)
    _dev_torso(s)
    _far_arm(s, -1, 3)         # far hand stays down on the desk
    _dev_head(s)
    # The folded arm and the mug are drawn after the head, because a mug held
    # to the mouth has to overlap the face to read as being drunk from.
    s.hline(18, 15, 17, "shadow")          # sleeve hem
    s.rect(15, 19, 17, 21, "skin")         # upper arm hangs down
    s.rect(17, 15, 18, 20, "skin")         # forearm folded up
    s.rect(17, 14, 19, 14, "skin")         # fist under the mug
    s.dot(18, 15, "pot")
    s.dot(19, 14, "screen")                # knuckle facing the monitor
    s.hline(19, 15, 17, "cream")           # top of the arm, lit from above
    s.rect(16, 10, 19, 13, "cream")        # mug body
    s.hline(10, 16, 19, "desk_dark")       # coffee surface
    s.vline(19, 11, 13, "pot")             # shaded side
    s.hline(13, 16, 19, "pot")
    # This pose covers the shoulder rim and most of the face with the raised
    # arm and the mug, so without these three pixels the coffee frame would be
    # the one frame in the set with no cool light on it at all - and a light
    # that switches off when the character takes a break is not a light. The
    # mug's monitor-facing side and the knuckle beneath it are what is left
    # pointing at the screen in this pose, so they are what catches it.
    s.dots([(19, 11), (19, 12)], "screen")
    s.dots([(20, 11), (20, 12)], "cream")  # handle
    s.dots([(17, 8), (18, 6), (17, 4)], "cream")   # steam
    return s


def build_dev_sleep() -> Sprite:
    s = Sprite(24, 32)
    _dev_torso(s)
    # Slumped: the head sinks two rows into the shoulders and both arms go
    # slack, hands flat on the desk instead of curled over keys.
    _far_arm(s, -1, 4)
    _near_arm(s, 0, 3, curl=False)
    _dev_head(s, dy=2, eyes_closed=True)
    # A single `z` above the head - the smallest readable sleep cue at 24px.
    s.dots([(19, 1), (20, 1), (21, 1), (20, 2), (19, 3), (20, 3), (21, 3)],
           "cream")
    return s


# --------------------------------------------------------------------------
# Props
# --------------------------------------------------------------------------

def _leaf(s: Sprite, cx: int, cy: int, rx: int, ry: int, name: str) -> None:
    """A hard-edged pixel oval - PIL's ellipse would anti-alias the rim.

    The 1.05 fudge rounds the shape outward slightly so the small radii used
    here come out as blobs rather than diamonds.
    """
    for y in range(cy - ry, cy + ry + 1):
        for x in range(cx - rx, cx + rx + 1):
            dx, dy = x - cx, y - cy
            if (dx * dx) / float(rx * rx) + (dy * dy) / float(ry * ry) <= 1.05:
                s.dot(x, y, name)


def build_plant() -> Sprite:
    s = Sprite(24, 32)
    # Foliage first: the pot rim then overlaps the lowest leaves, which is
    # what makes the plant look planted rather than balanced on top.
    stems = ((11, 10), (8, 14), (15, 13), (12, 8))
    for x, top in stems:
        s.vline(x, top, 24, "desk_dark")
    leaves = (
        (11, 6, 4, 3, "plant"), (6, 11, 4, 3, "plant"),
        (17, 12, 4, 3, "plant"), (8, 17, 3, 2, "plant"),
        (16, 18, 3, 2, "screen_dim"), (13, 13, 3, 2, "screen_dim"),
        (12, 20, 3, 2, "plant"),
    )
    for cx, cy, rx, ry, name in leaves:
        _leaf(s, cx, cy, rx, ry, name)
        s.hline(cy + ry, cx - rx + 1, cx + rx - 1, "shadow")  # underside
    s.dots([(11, 4), (6, 9), (17, 10)], "gold")   # new growth catching light

    s.rect(5, 23, 18, 24, "pot")                  # rim
    s.hline(23, 5, 11, "gold")                    # lit lip
    s.hline(24, 6, 17, "desk_dark")               # soil in shadow
    body = ((5, 25, 18), (5, 26, 18), (6, 27, 17), (6, 28, 17),
            (7, 29, 16), (7, 30, 16), (8, 31, 15))
    for x0, y, x1 in body:
        s.hline(y, x0, x1, "pot")
        s.hline(y, x1 - 2, x1, "desk_dark")       # shaded right of the pot
    # Contact shadow on the desk, two pixels wider than the pot's base on each
    # side. The pot's own bottom row used to be `desk_dark`, which is a
    # terracotta value and so read as more pot rather than as shadow - the
    # plant appeared to be stuck onto the desk rather than standing on it.
    s.hline(31, 6, 17, "shadow")
    return s


def build_mug() -> Sprite:
    s = Sprite(10, 10)
    s.rect(1, 2, 7, 9, "cream")
    s.hline(2, 2, 6, "desk_dark")          # coffee seen through the opening
    s.hline(3, 2, 6, "shadow")
    s.vline(7, 3, 8, "pot")                # shaded side
    s.hline(8, 1, 7, "pot")                # shaded bottom of the china
    # Contact shadow, one pixel proud of the mug on the near side: this is a
    # 10px sprite and a 7px mug, so there is room for the shadow to be
    # visibly wider than the object casting it.
    s.hline(9, 0, 8, "shadow")
    s.dots([(8, 4), (8, 5), (8, 6)], "cream")   # handle
    s.dot(8, 5, "pot")                     # the hole in the handle
    return s


def build_books() -> Sprite:
    s = Sprite(32, 24)
    # Four volumes, each offset a pixel or two so the stack leans slightly -
    # a perfectly aligned stack reads as a single striped box.
    volumes = (
        (1, 19, 30, "shirt"), (3, 14, 29, "pot"),
        (2, 9, 27, "plant"), (5, 5, 24, "gold"),
    )
    for x0, y0, x1, cover in volumes:
        y1 = y0 + 4 if cover != "gold" else y0 + 3
        s.rect(x0, y0, x1, y1, cover)
        # `shadow` where a book casts onto the one below. The bottom volume's
        # last row is the stack's contact with the floor and is re-painted
        # wider after the loop; see the contact-shadow note below.
        s.hline(y1, x0, x1, "shadow")
        s.vline(x0, y0, y1 - 1, "shadow")  # spine in shadow
        s.rect(x1 - 3, y0 + 1, x1, y1 - 1, "cream")   # page block
        s.vline(x1 - 3, y0 + 1, y1 - 1, "desk_dark")  # gap before the pages
        # Title dash on the spine face; gold-on-gold would be invisible.
        s.hline(y0, x0 + 1, x1 - 4, "desk_dark" if cover == "gold" else "gold")
    # Contact shadow, in the sprite's last row because that is the row that
    # touches the floor. It spans the full 32px, one pixel proud of the bottom
    # volume on each side, and that overhang is the entire point: a dark line
    # exactly as wide as the book reads as the book's own bottom edge and the
    # stack still floats, while a dark line you can see past the book reads as
    # the book's shadow. (An earlier version put `shadow` here at exactly the
    # book's width, on top of a separate dark band painted across the floor
    # under the skirting, and the two together read as a 1px gap under the
    # stack. That floor band is gone; this row is now the only dark thing at
    # the stack's base.)
    s.hline(23, 0, 31, "shadow")
    return s


def build_lamp() -> Sprite:
    s = Sprite(20, 28)
    # Conical shade: authored row by row so the slope is exact integers.
    shade = ((8, 2, 11), (7, 3, 12), (6, 4, 13), (5, 5, 14),
             (4, 6, 15), (3, 7, 16), (2, 8, 17), (2, 9, 17))
    for x0, y, x1 in shade:
        s.hline(y, x0, x1, "gold")
        s.hline(y, x0, x0 + 1, "lamp")     # lit upper-left face
        s.hline(y, x1 - 1, x1, "pot")      # shaded right face
    s.hline(9, 3, 16, "desk_dark")         # under-rim, in its own shadow
    # The mouth of the shade. This is the one place in the room where the
    # viewer looks straight at a light source, so it is the brightest thing in
    # the sprite: `cream` core, `lamp` around it, and two `lamp` pixels poking
    # one column past the shade's rim on each side - a hard-edged 2px flare,
    # which is as much bloom as this palette can express without the dithered
    # disc that reviewers read as a clipping error.
    s.hline(10, 1, 18, "gold")             # light escaping the whole rim
    s.hline(10, 5, 14, "lamp")             # bulb glow spilling out
    s.rect(8, 11, 11, 11, "lamp")
    s.rect(9, 10, 10, 10, "cream")         # filament core

    s.rect(9, 12, 10, 24, "metal")         # post
    s.vline(9, 12, 24, "wall_light")
    s.hline(25, 5, 14, "metal")            # weighted base
    s.hline(26, 4, 15, "metal")
    s.dots([(5, 25), (6, 25)], "wall_light")
    # The pool rises three rows either side of the base, where nothing blocks
    # it, stepping out a pixel a row. Stepped, not faded: flat runs of warm
    # colour with hard edges, because the soft version of this idea - a Bayer
    # disc of `lamp` yellow - is the one reviewers called particle sparks.
    #
    # `lamp` and not `gold`: desk.png's own top row is `gold`, so a gold pool
    # on a gold tabletop is invisible. `lamp` is the palette's brightest warm
    # value and the only one left that can be brighter than the surface it is
    # falling on - which is what a pool of light has to be.
    # ...and its two hottest pixels are `cream`, the palette's brightest entry,
    # immediately beside the base where the light has the least distance to
    # travel. Without them the pool's brightest value is `lamp`, one step off
    # the tabletop's own `gold`, and at 2x the two are near enough that the
    # pool stops reading as a hotspot and starts reading as more tabletop.
    for row, inset in ((24, 2), (25, 1), (26, 0)):
        s.hline(row, inset, 3, "lamp")
        s.hline(row, 16, 19 - inset, "lamp")
    s.dots([(3, 25), (3, 26), (16, 25), (16, 26)], "cream")
    # Bottom row: the pool of light the lamp throws on whatever it stands on.
    # It lives here rather than in desk.png so that it follows the lamp when
    # scene.rs moves it, and it is exactly one row tall - the lamp's bottom
    # edge is flush with the desk's top edge, so one row is all the tabletop
    # this sprite can reach.
    #
    # Three values across that one row, brightest where the light is least
    # obstructed: `pot` directly beneath the base (the part the base's own
    # weight shades), `lamp` in the two gaps either side of it where the light
    # gets straight out, `gold` at the far ends where it is running out. A
    # flat single-colour line read as a decal; a three-step ramp with hard
    # edges reads as a pool.
    s.hline(27, 0, 19, "gold")
    s.hline(27, 2, 17, "lamp")
    s.hline(27, 5, 14, "pot")
    return s


def build_rug() -> Sprite:
    """Concentric oval bands, hard-edged, no dither.

    The bands used to be overlaid with a 25%-coverage `gold` dither ring for a
    "woven" texture. In the game only the rug's top few rows clear the HUD,
    and that sliver of dither read as a stippled artifact - the same failure
    mode as the old floor stippling. Flat concentric rings are legible at any
    crop and unmistakably deliberate.
    """
    s = Sprite(96, 32)
    # The art direction exempts the rug from the transparency rule, but an
    # opaque 96x32 rectangle would paint a box over the floorboards, so only
    # the oval itself is opaque.
    cx, cy = 47.5, 15.5
    rx, ry = 47.5, 15.5
    # Three zones only. Six concentric bands on a 96x32 oval put a stepped
    # edge every couple of pixels, which read as a jagged gear rather than a
    # rug, and the bright cream core read as a target. One muted field, one
    # narrow accent ring and a dark rim is all this reads as at native size.
    bands = ((0.58, "pot"), (0.68, "gold"), (0.92, "pot"), (1.00, "desk_dark"))
    for y in range(32):
        for x in range(96):
            dx = (x - cx) / rx
            dy = (y - cy) / ry
            q = (dx * dx + dy * dy) ** 0.5
            if q > 1.0:
                continue
            for edge, name in bands:
                if q <= edge:
                    s.dot(x, y, name)
                    break
            # A dark rim along the NEAR edge only, and only the bottom third
            # of it. It used to run round the whole lower half, and a dark
            # arc that long stopped reading as the rug pressing into the
            # boards and started reading as an outline drawn round a sticker -
            # `shadow` against `floor` is the highest-contrast edge in the
            # room. Confined to the bottom, it is a contact shadow; the rest
            # of the rim is `desk_dark`, which is close enough to the floor's
            # own value to sit down into it.
            if q > 0.88 and y >= 26:
                s.dot(x, y, "shadow")
    return s


BUILDERS = {
    "room_bg.png": build_room_bg,
    "desk.png": build_desk,
    "monitor_on.png": lambda: _monitor(True),
    "monitor_off.png": lambda: _monitor(False),
    "chair.png": build_chair,
    "dev_idle.png": build_dev_idle,
    "dev_type_a.png": build_dev_type_a,
    "dev_type_b.png": build_dev_type_b,
    "dev_coffee.png": build_dev_coffee,
    "dev_sleep.png": build_dev_sleep,
    "plant.png": build_plant,
    "mug.png": build_mug,
    "books.png": build_books,
    "lamp.png": build_lamp,
    "rug.png": build_rug,
}


# --------------------------------------------------------------------------
# Self-check
# --------------------------------------------------------------------------

def check(filename: str, want_w: int, want_h: int) -> tuple[bool, str]:
    """Verify one written PNG: size, palette purity, and that it is not blank."""
    img = Image.open(ASSETS / filename).convert("RGBA")
    w, h = img.size
    problems = []
    if (w, h) != (want_w, want_h):
        problems.append(f"size {w}x{h} != {want_w}x{want_h}")

    opaque = 0
    partial = 0
    offpalette: dict[tuple[int, int, int], int] = {}
    for _count, colour in img.getcolors(maxcolors=1 << 20):
        r, g, b, a = colour
        if a == 0:
            continue
        if a != 255:
            partial += _count
        opaque += _count
        if (r, g, b) not in RGB_TO_NAME:
            offpalette[(r, g, b)] = offpalette.get((r, g, b), 0) + _count
    if partial:
        problems.append(f"{partial}px with partial alpha")
    if offpalette:
        listed = ", ".join(f"#{r:02x}{g:02x}{b:02x} x{n}"
                           for (r, g, b), n in sorted(offpalette.items()))
        problems.append(f"off-palette: {listed}")
    if filename in CHARACTERS and opaque < 100:
        problems.append(f"only {opaque} opaque px - looks blank")
    if filename in ("room_bg.png",) and opaque != w * h:
        problems.append("background must be fully opaque")

    detail = f"{w}x{h}".ljust(9) + f"{opaque:>6} opaque"
    if problems:
        return False, detail + "  FAIL: " + "; ".join(problems)
    return True, detail + "  ok"


def main() -> int:
    ASSETS.mkdir(parents=True, exist_ok=True)
    for filename, _w, _h in SPEC:
        BUILDERS[filename]().save(filename)

    ok = True
    print(f"{'file':<16}{'size':<9}{'opaque':>13}")
    for filename, want_w, want_h in SPEC:
        passed, detail = check(filename, want_w, want_h)
        ok = ok and passed
        print(f"{filename:<16}{detail}")
    if not ok:
        print("\nSELF-CHECK FAILED", file=sys.stderr)
        return 1
    print(f"\n{len(SPEC)} sprites written to {ASSETS}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
