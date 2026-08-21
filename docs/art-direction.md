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
