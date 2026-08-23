# Upgrade system design — Dexel v2 (own-many / equip-one)

The product loop in one line, unchanged: **real activity earns Dev Cash; Dev
Cash buys visible customisation of the character, the desk and the room; every
purchase permanently changes what you see.**

What changes in v2 is the *shape* of the content. v0.3 modelled upgrades as
seven **tracks with linear tiers** — buying tier 2 replaced tier 1 forever, and
progression was a ladder you could only climb. The user's design conversation
locked a different model: **eight slots, many owned items per slot, one
equipped at a time, plus a colour per item.** Owning and wearing are separate.
That single change turns the shop from a ladder into a wardrobe, and it is what
makes "lots of options in future" a content problem rather than a systems one.

Engine context: **ADR 0011**. The shipping implementation is Go + HTML/NES.css.
The catalog lives in Go as a static table; the store UI is specified in
`docs/ui-spec.md`; the sprites are specified in `docs/art-direction.md`. This
document owns the model, the content, the economy, and persistence.

## Principles

1. **Every purchase is visible.** No stat-only items, no "+5% efficiency". If
   it does not change pixels on screen it does not ship. (Carried forward from
   ADR 0008 and still the most important rule here.)
2. **Buy, don't unlock.** Nothing appears at a threshold. Dev Cash is spent, the
   balance drops, and the item is owned forever. Spending is what makes earning
   mean anything.
3. **Own many, equip one.** Every slot holds one equipped item. Buying never
   destroys or replaces what you already own — you can always go back to the
   chipped mug. This is the v2 change and it is load-bearing: a wardrobe
   rewards breadth, a ladder only rewards spending.
4. **Style, then colour.** An item is a *shape*; a colour is a cheap variation
   on a shape you already own. Colours are runtime tints on grayscale-authored
   sprite regions, so N colours cost zero extra sprites (see
   `docs/art-direction.md`, "Recolourable parts").
5. **Data-driven from one table.** Slots, items, prices, sprite filenames and
   tints all live in one static Go catalog, broadcast verbatim to the frontend
   as the `catalog` message. The store renders whatever the table says; the
   scene renders whatever `equipped` says. Adding content never touches a
   system, a template, or a CSS file.
6. **The player's real work is never fiction, and the character's work is
   never presented as the player's.** See "Sprint naming" — this is a privacy
   and honesty rule, not a copy rule.

## The model

```
Slot        one per scene position, always has exactly one equipped item
 └─ Item    a style: a shape, a price, a sprite, a default tint
     └─ Tint  a colour for that item; the default is free, the rest cost

ownedItems  set of item ids
ownedTints  set of "<itemId>:<tintId>" pairs (the default tint is implicit)
equipped    slot id -> { itemId, tintId }        — every slot, always
```

Three invariants the backend must enforce and a test must assert:

* **Every slot has a free tier-0 item**, so `equipped` never contains a null
  and the scene never has an undefined layer. For Plant, Wall and Buddy the
  tier-0 item is an explicit *nothing* item (`plant_none`, `wall_bare`,
  `buddy_none`) with no sprite — the slot element is simply hidden. Hoodie has
  no *nothing* item, because a hoodless companion is a different character.
* **You cannot equip what you do not own**, item or tint. Equipping is not a
  back door around buying.
* **Buying is idempotent-safe**: buying an already-owned item is rejected with
  an error flash, never charged twice.

**[DESIGN CALL] the Monitor is not a slot.** v0.3 had a `monitor` track with
dual and ultrawide tiers. The behind-the-shoulder view makes the monitor's
inner rect load-bearing UI geometry — 11 lines of live terminal at an exact
pixel rect (`docs/art-direction.md`) — and every variant would move it. A
monitor upgrade would silently break the one element the whole composition
points at. Existing monitor purchases are **refunded** at migration (below).

## Slots and items

Eight slots, four items each, thirty-two items, of which eight are free
defaults. Prices are in Dev Cash.

### Hoodie — the customisation canvas the back view exists for

| Item id | Name | Price | Default tint | Flavour |
|---|---|---|---|---|
| `hoodie_classic` | Classic Pullover | **0** | `indigo` | Drawstrings, one pocket, no opinions. |
| `hoodie_zip` | Zip-Up | 120 | `slate` | For when the office is exactly two degrees off. |
| `hoodie_tech` | Techwear | 300 | `forest` | Straps that hold nothing. Reflective, though. |
| `hoodie_cloak` | Night Cloak | 500 | `neon` | Ships at 3am or not at all. |

### Chair — the PDF's worked example for style + colour

| Item id | Name | Price | Default tint | Flavour |
|---|---|---|---|---|
| `chair_basic` | Basic Office | **0** | `slate` | Adjusts in one axis. That axis is "no". |
| `chair_racer` | Racer | 100 | `ember` | Bolstered wings. Zero laps completed. |
| `chair_exec` | Executive Leather | 300 | `ember` | Tufted. Reclines further than the deadline. |
| `chair_antigrav` | Anti-Gravity | 500 | `cobalt` | Floats. Physics pending review. |

### Keyboard

| Item id | Name | Price | Flavour |
|---|---|---|---|
| `kb_membrane` | Stock Membrane | **0** | Came with the machine. Still here. |
| `kb_mech` | Mechanical | 60 | Audible from the next room. Intentionally. |
| `kb_split` | Split Ergo | 180 | Two halves, one wrist, endless smugness. |
| `kb_neon` | Neon 60% | 300 | Fewer keys, more colours, same bugs. |

### Mouse

| Item id | Name | Price | Flavour |
|---|---|---|---|
| `mouse_stock` | Stock Mouse | **0** | Two buttons and a wheel. It works. |
| `mouse_gaming` | Gaming Mouse | 50 | Seven buttons. Two are bound. |
| `mouse_trackball` | Trackball | 150 | The wrist thanks you. The cursor does not. |
| `mouse_vertical` | Vertical Ergo | 220 | Held like a handshake with your desk. |

### Beverage

| Item id | Name | Price | Flavour |
|---|---|---|---|
| `bev_mug` | Chipped Mug | **0** | The chip is load-bearing. |
| `bev_thermos` | Thermos | 40 | Still hot at 4pm. Suspiciously. |
| `bev_teacup` | Tea & Saucer | 90 | A saucer. On a developer's desk. |
| `bev_energy` | Energy Can | 140 | Tastes like a changelog. |

### Plant

| Item id | Name | Price | Flavour |
|---|---|---|---|
| `plant_none` | Bare Desk | **0** | Minimalism, or forgetfulness. |
| `plant_succulent` | Succulent | 50 | Survives neglect. Relatable. |
| `plant_monstera` | Monstera | 140 | Big leaves. Bigger commitment. |
| `plant_bonsai` | Bonsai | 260 | Pruned more carefully than the git history. |

### Wall

| Item id | Name | Price | Flavour |
|---|---|---|---|
| `wall_bare` | Bare Wall | **0** | Ready for anything. |
| `wall_poster` | "Works On My Machine" | 80 | The oldest defence. |
| `wall_shelf` | Shelf: Books & Trophy | 200 | Four books, one trophy, zero pages read. |
| `wall_neon` | Neon Sign | 380 | Casts a glow on every late commit. |

### Buddy

| Item id | Name | Price | Flavour |
|---|---|---|---|
| `buddy_none` | No Buddy | **0** | Solo run. |
| `buddy_duck` | Rubber Duck | 60 | Best listener on the team. |
| `buddy_bot` | Desk Bot | 250 | Blinks. Judges. Blinks again. |
| `buddy_cat` | Sleeping Cat | 300 | Has opinions about the keyboard. Asleep. |

### Tints

Six colours, shared by every tintable item (Hoodie and Chair in v2). Hexes and
the grayscale-authoring convention are in `docs/art-direction.md`.

| Tint id | Name | Hex | Price |
|---|---|---|---|
| `slate` | Classic Black | `#2b2b33` | 40 |
| `cobalt` | Cobalt Blue | `#4a7fa8` | 40 |
| `forest` | Forest Green | `#4e8b4f` | 40 |
| `ember` | Cyberpunk Orange | `#a45c3a` | 40 |
| `neon` | Neon Pink | `#e86aa4` | 40 |
| `indigo` | Midnight Indigo | `#6a5aa0` | 40 |

**Each item ships with one of the six free** — its `defaultTint`, granted the
moment the item is owned (and, for the tier-0 items, from the very first
launch). The other five cost 40 each, **per (item, tint) pair**.

**[DESIGN CALL] colours are purchasable, not free-on-ownership.** The source
mockup shows a row of colour swatches *and* a `Buy for N Credits` button on the
same card without saying which the button buys, and the prose says only that
"once a style is owned, a sub-menu allows applying" a colour. Making the
default free means every purchase is immediately wearable and nobody is stuck
owning an item they cannot equip; making the extras cost 40 gives the long tail
somewhere cheap to go between big-ticket items.

**[DESIGN CALL] only Hoodie and Chair are tintable in v2.** They are the
PDF's two worked examples and the two largest pixel areas where a recolour
actually reads at 2x. The mechanism is general: any slot opts in later by
authoring a `*_form.png` and setting `tintable: true` in the catalog — no code
change.

**[DESIGN CALL] four items per slot; the mockup's fifth chair is documented,
not shipped.** The final mockup grid shows five chairs (adding "Aero-Style" at
the same price as Racer); the prose lists four. Four ships. Adding Aero-Style
is exactly one catalog row plus `chair_aero_form.png` /
`chair_aero_detail.png` — and it is the worked example for "how do I add
content", proving the grid scrolls and the table is the only thing you touch.

## Economy

| | |
|---|---|
| Currency name | **Dev Cash** (symbol: a `gold` coin glyph) |
| Earned by | completing a sprint |
| Per sprint | 25–80 Dev Cash, **~51 average** across the six-sprint rotation |
| A sprint | ≈ 20 minutes of real typing (ADR 0005's anti-mashing calibration) |
| Cheapest purchase | `bev_thermos`, 40 — **inside the first sprint** |
| Whole board, items | 4,770 |
| Whole board, tints (8 tintable items x 5 extra x 40) | 1,600 |
| Whole board, total | **6,370** ≈ 125 sprints ≈ **~42 hours of typing** |

First purchase in the first session, full completion measured in weeks of real
work. Long-tail progression with no grind wall.

**[DESIGN CALL] the PDF's price *ratios* are kept; the absolute numbers are
divided by ten.** The design conversation priced the chairs at 0 / 1,000 /
3,000 / 5,000 against a Go prototype that paid 50 per sprint — 100 sprints for
one chair. The shipped economy pays ~51 per sprint, so the same ratios at ÷10
(0 / 100 / 300 / 500) land the identical *shape* of progression on a scale a
human will actually reach. The ratio is documented precisely so a future
rebalance is one multiplier, not a re-read of this table. (Those ÷10 chair
numbers also happen to match v0.3's shipped chair costs of 100 and 300, which
makes migration land cleanly.)

**Renaming only.** "Coins" becomes "Dev Cash" everywhere in the UI. The
underlying integer, the XP curve (`50·(n−1)·n`) and the level display are
unchanged — this is a label change, not a balance change.

## Sprints

v0.3 called them "projects". v2 calls them **sprints**, per the design
conversation. The rotation is a static list; the game never generates text.

| # | Sprint name | Units | Dev Cash | XP |
|---|---|---|---|---|
| 0 | Fix Bug #404 | 50 | 25 | 40 |
| 1 | Refactor Auth Engine | 75 | 40 | 60 |
| 2 | Add CI Cache | 100 | 60 | 90 |
| 3 | Write the API Docs | 130 | 80 | 120 |
| 4 | Build a Robot | 100 | 60 | 90 |
| 5 | Tame the Flaky Test | 75 | 40 | 60 |

Completion rolls to `(index + 1) % 6` and carries any overshoot into the next
sprint, so no work is ever lost at a boundary. The four `(units, cash, xp)`
tuples from v0.3 are preserved exactly; the two new sprints reuse existing
tuples, so the rename adds content without touching the calibration.

The unit label displayed is `units` — `34 / 75 units`. **[DESIGN CALL]** the
mockup shows `100 / 1000 work units`; 1000 is a mockup number, not a balance
decision, and the real totals are tuned against ADR 0005's anti-mashing
economy. Keep the real numbers, keep the mockup's word.

### Sprint naming and ADR 0009 — the honesty rule

ADR 0009 removed generated fiction from the HUD because the HUD's activity
line was claiming to describe the user's work. That decision stands, and v2
does not weaken it. The reconciliation is **spatial and typographic**:

* **The sprint name is the character's project.** It lives in the bottom-**left**
  panel, prefixed with the literal word `SPRINT:`, and it is one of the six
  static strings above. It describes what the little pixel developer is
  working on. It is game flavour and it is fine.
* **The activity line is the user's reality.** It lives in the bottom-**right**
  panel, top row, comes from ADR 0009's app-identity mapping
  (`"Coding in VS Code"`, `"In the terminal"`, `"Working..."`), and never
  contains a sprint name.
* The two are in different panels, at different weights, with a labelled
  prefix on one and a mood dot on the other. **They must never be merged into
  one line, and no sprint name may be phrased so it could be read as a
  description of the user's activity.** "Fix Bug #404" is unmistakably the
  character's; "Working on your PR" would not be, and is banned.
* Sprint titles come from the static list only. No generation, no interpolation
  of anything from the user's machine — same rule as the ticker and the
  terminal (`docs/ui-spec.md` §3).

## Scene contract

Each slot maps to exactly one absolutely positioned element in
`#scene-sprites`, at the pixel offset and layer index given in
`docs/art-direction.md` ("Element placement table" and "Layer order").
Rendering is a pure function of `state.equipped`:

```
for slot, {itemId, tintId} in state.equipped:
    item = catalog.items[itemId]
    if item.sprite == null:  hide the slot element;  continue
    set the slot element's sprite to item.sprite
    if catalog.slots[slot].tintable:
        apply the mask+multiply tint recipe with catalog.tints[tintId].hex
        overlay item.detail
```

No slot has special-case code. The developer is the one composite that is not
a slot — it is the hoodie slot's `form` + the hoodie style overlay + the
frame-driven `base` layer, all three keyed off `activeState` for the frame and
off the hoodie slot for the style and tint.

## Persistence

### The Go store

| | |
|---|---|
| Path | `~/.config/dexel/state.json` |
| Write | atomically: write `state.json.tmp`, `fsync`, `rename` |
| Cadence | on every mutation, plus a 30 s safety flush |
| On malformed/unreadable | log once, start fresh. **Never** panic, never delete the bad file — rename it to `state.json.corrupt` so a user can send it in |

```json
{
  "schema": 1,
  "devCash": 2150,
  "xp": 1240,
  "sprint": { "index": 3, "unitsDone": 34.0 },
  "ownedItems": ["hoodie_classic", "hoodie_zip", "chair_basic", "chair_racer"],
  "ownedTints": ["hoodie_zip:cobalt", "chair_racer:ember"],
  "equipped": {
    "hoodie": {"itemId": "hoodie_zip", "tintId": "cobalt"},
    "chair":  {"itemId": "chair_racer", "tintId": "ember"}
  },
  "importedFromRust": true,
  "importedAt": "2026-08-21T18:45:00Z"
}
```

* `level` is **not** stored — it is derived from `xp` on load, so a
  hand-edited file self-corrects (the same rule the Rust save already applied).
* Arrays are written **sorted**, and maps are marshalled with sorted keys, so
  the file is byte-stable across saves and diffable.
* **Load validation, never trust the file:** an unknown `itemId` or `tintId` is
  dropped; a `sprint.index` out of range is clamped; an `equipped` entry naming
  an unowned or unknown item falls back to that slot's tier-0 item; a missing
  slot is filled with its tier-0 item; `unitsDone` is clamped to
  `[0, target]`. Every one of these is a log line, not an error dialog, and
  certainly not a crash.

**[DESIGN CALL] JSON, not sqlite.** The PDF blueprint and the Go prototype both
reached for sqlite. This document is one small object with one writer and no
queries; sqlite's actual advantages — concurrent writers, indexed queries,
schema migrations — are all unused, and it costs a Cgo dependency (or a pure-Go
driver) and makes the state file un-inspectable by a user filing a bug. If a
real need appears (per-day activity history for charts is the plausible one),
add sqlite for *that* table and leave this document in JSON.

### One-time import from the Rust save

The frozen Rust/Bevy build (ADR 0011) wrote:

```
macOS   ~/Library/Application Support/dev-companion/save.json
Linux   ~/.local/share/dev-companion/save.json
```

```json
{ "wallet": 340, "xp": 1240, "level": 5,
  "current_project": { "index": 2, "work_done": 41.0 },
  "upgrades": { "chair": 2, "keyboard": 1, "monitor": 1, "wall": 2 } }
```

**Import rule.** On startup, **if and only if** `~/.config/dexel/state.json`
does not exist, look for the Rust save. If it parses, import it, set
`importedFromRust: true`, and write the new state file. Then never look again.

* The Rust save is opened **read-only** and is never modified, moved, or
  deleted — the legacy build must stay launchable, per ADR 0011.
* If the Go state file already exists, the import is skipped entirely. **No
  merging**, ever: a merge of two economies has no correct answer and would
  silently double-grant on every launch.
* If the Rust save is missing or malformed, start fresh and log one line. A
  failed import is not an error the user needs to see.

**Direct carry-over:** `wallet -> devCash`, `xp -> xp`,
`current_project.index -> sprint.index` (clamped to 0..5 — the old list had 4
entries, the new one has 6, and indices 0..3 keep the same four names in the
same order), `current_project.work_done -> sprint.unitsDone`.

**[DESIGN CALL] the upgrade-tier mapping.** For each old track, grant the
**N cheapest paid items** of the mapped new slot, where N is the old owned
tier; refund in full any old track with no new slot. This is chosen over
name-matching because the old model was cumulative — owning tier 2 implied
owning tier 1 — so granting the two cheapest paid items in the slot preserves
"you paid for two things, you own two things", and any flavour mismatch between
an old tier and a new item is invisible because the player owns both and picks.

| Old track / tier | Paid | Grants | Value | Net |
|---|---|---|---|---|
| `keyboard` 1 | 30 | `kb_mech` | 60 | player gains |
| `keyboard` 2 | 150 | `kb_mech`, `kb_split` | 240 | player gains |
| `mouse` 1 | 20 | `mouse_gaming` | 50 | player gains |
| `mouse` 2 | 110 | `mouse_gaming`, `mouse_trackball` | 200 | player gains |
| `chair` 1 | 100 | `chair_racer` | 100 | even |
| `chair` 2 | 400 | `chair_racer`, `chair_exec` | 400 | even |
| `desk_decor` 1 | 50 | `plant_succulent` | 50 | even |
| `desk_decor` 2 | 180 | `plant_succulent`, `buddy_duck` | 110 | **refund 70** |
| `wall` 1 | 80 | `wall_poster` | 80 | even |
| `wall` 2 | 280 | `wall_poster`, `wall_shelf` | 280 | even |
| `pet` 1 | 250 | `buddy_cat` | 300 | player gains |
| `monitor` 1 | 150 | — (no such slot) | — | **refund 150** |
| `monitor` 2 | 550 | — | — | **refund 550** |

* `desk_decor` was the one `Accumulate` track (it rendered plant *and* duck at
  once). It fans out across **two** new slots, `plant` and `buddy`, which is
  the same two objects on screen; its tier-2 grant is worth less than was paid,
  so the difference is refunded.
* Refunds are added to `devCash`. **Nobody loses currency in this migration** —
  that was ADR 0008's promise for the last migration and it holds for this one.
* Every player is additionally granted the eight free tier-0 items and each
  owned item's `defaultTint`, exactly as a fresh install would be.
* **Equip rule after import:** for each slot, equip the **most expensive owned
  item**, with its default tint. The old build rendered only the highest owned
  tier, so this reproduces what the player was last looking at. Slots with
  nothing bought keep their tier-0 item.
* An unknown track id in `upgrades` (a hand-edited save) is ignored; a tier
  above a track's known max is clamped down.

Worked example — the save above (`wallet 340`, `chair: 2, keyboard: 1,
monitor: 1, wall: 2`):

```
devCash    340 + 150 (monitor refund)                          = 490
ownedItems 8 free defaults
           + chair_racer, chair_exec        (chair 2)
           + kb_mech                        (keyboard 1)
           + wall_poster, wall_shelf        (wall 2)
equipped   hoodie   hoodie_classic / indigo   (nothing bought)
           chair    chair_exec / ember        (most expensive owned)
           keyboard kb_mech
           mouse    mouse_stock               (nothing bought)
           beverage bev_mug                   (nothing bought)
           plant    plant_none                (nothing bought)
           wall     wall_shelf                (most expensive owned)
           buddy    buddy_none                (nothing bought)
xp         1240  -> level derived = 5
sprint     index 2, unitsDone 41.0
```

**Test this with fixtures, not by hand.** The import is a pure function
`(rustSave) -> goState`; it deserves a table test with one row per old-track
tier plus the malformed and missing cases, and an assertion that
`devCash_after >= devCash_before` for every input.

## Naming note

The product name is still a working title. Nothing in this design is
developer-specific except sprite flavour and the sprint list; slots, items,
tints, prices and persistence are theme-neutral by construction, so a rename or
a re-skin touches the catalog and nothing else.
