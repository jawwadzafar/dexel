# Upgrade system design — v0.3

The product loop the user asked for, in one line: **real activity earns coins;
coins buy visible pixel-art upgrades to the character, desk, and room; every
upgrade permanently changes what you see.** This is the Little Writer loop
with a developer skin, and the desk-plant threshold in v0.2 was a one-item
prototype of it. This doc scales it to a real system without painting us into
a corner.

## Principles

1. **Every upgrade is visible.** No stat-only purchases. If it doesn't change
   pixels on screen it doesn't ship. (The "world visibly changes" hook is the
   product.)
2. **Buy, don't unlock.** v0.2's plant appears automatically at a wallet
   threshold. Real ownership needs a *choice*: coins are spent, the balance
   drops, the item stays owned forever (persisted). Spending is what makes
   earning meaningful.
3. **Tracks, not a flat list.** Each track is one slot in the scene with
   tiered sprites. This is what keeps "lots of options in future" cheap: a
   new tier is one sprite + one table row, a new track is one slot in the
   scene + N sprites.
4. **Data-driven from one table.** All tracks/tiers/costs/sprite names live in
   a single Rust table (`UPGRADE_TRACKS`). The scene renders whatever the
   table says; the shop lists whatever the table says; the save stores only
   `(track_id, owned_tier)`. Adding content never touches systems.

## Tracks and tiers (initial set)

| Track (slot) | Tier 0 (start) | Tier 1 | Tier 2 | Costs |
|---|---|---|---|---|
| `keyboard` | none visible | basic keyboard | mechanical w/ RGB pixels | 30, 120 |
| `mouse` | none visible | mouse + pad | gaming mouse (accent colors) | 20, 90 |
| `monitor` | current single | dual monitors | ultrawide | 150, 400 |
| `chair` | current chair | ergonomic (headrest) | gaming chair (accent) | 100, 300 |
| `desk_decor` | none | plant (v0.2's) | plant + rubber duck | 50, 130 |
| `wall` | bare wall | poster ("it works on my machine") | shelf w/ books + trophy | 80, 200 |
| `pet` | none | sleeping cat on the rug | — (future: more pets) | 250 |

Costs tuned against the rebalanced economy (~25-90 coins/project, a project ≈
20 min of real typing): first purchase within the first session or two, the
full board is many hours — long-tail progression without grind walls.

## Interaction: how buying works

A shop the size of a HUD row. `Tab` (or click a HUD button later) toggles a
**shop strip** above the HUD: horizontally listed track icons with
`name — next tier cost`, arrow keys/1-9 to select, `Enter` to buy if
affordable. No modal window, no pause — the game never interrupts work; the
strip is glanceable and dismissable. Buying: wallet -= cost, owned tier += 1,
sprite swaps immediately, one-line HUD flash ("Mechanical keyboard!").

## Persistence

`SaveData` gains `upgrades: BTreeMap<String, u8>` (track id -> owned tier),
default empty. BTreeMap for deterministic serialization. Missing field on old
saves = empty map (serde default), so v0.2 saves load cleanly. The v0.2
plant-at-50-coins rule is REPLACED by `desk_decor` tier 1 as a purchase; the
migration maps "old save with wallet ever >= 50" to nothing — the plant
becomes buyable, which is strictly more agency, and nobody loses coins.

## Scene contract

Each track = one anchor position + z in `scene.rs` + a sprite-per-tier lookup.
`upgrade_render_system` reads the owned map and swaps sprite handles; tier 0
with no sprite = `Visibility::Hidden` on the slot entity. Anchors derive from
surfaces like everything else (keyboard ON desk in front of dev, mouse right
of keyboard, poster ON wall above books, cat ON rug).

## Art manifest additions (gen_assets.py)

All native sizes, palette-only, same rules as ever:
keyboard_t1 20x8, keyboard_t2 20x8 (+2 accent px), mouse_t1 8x6+pad 12x8,
mouse_t2 8x6, monitor_dual 40x36 (second smaller panel), monitor_ultra 56x30,
chair_t1 28x36 (headrest), chair_t2 28x38 (accent stripes), duck 8x8,
poster 24x30, shelf 40x22, cat 20x12 (2 frames, tail flick).

## Naming note

The user may rename the product later ("developer companion" is a working
title). Nothing in this design is developer-specific except sprite flavor —
tracks/tiers/shop are theme-neutral by construction.
