# Sprints, Dev Cash, levels, and the store

What a player is working towards, and what the numbers behind it actually are.
Sources: `app/internal/game/sprint.go`, `app/internal/game/catalog.go`,
`app/internal/game/game.go`. The design origin is
[`docs/upgrade-design.md`](../upgrade-design.md) and
[ADR 0008](../adr/0008-upgrade-tracks.md).

---

## 1. Sprints

A **sprint** is a named progress bar. It has a work-unit target, a Dev Cash
payout and an XP payout, and when you fill it the game rolls to the next one
in a fixed six-entry rotation. That is the whole mechanic — there is no sprint
selection, no difficulty, no deadline, no failure state, and nothing about a
sprint reacts to what you were actually doing.

The rotation, verbatim from `game/sprint.go`:

| # | Name | Target (units) | Dev Cash | XP |
| --- | --- | --- | --- | --- |
| 0 | Fix Bug #404 | 50 | 25 | 40 |
| 1 | Refactor Auth Engine | 75 | 40 | 60 |
| 2 | Add CI Cache | 100 | 60 | 90 |
| 3 | Write the API Docs | 130 | 80 | 120 |
| 4 | Build a Robot | 100 | 60 | 90 |
| 5 | Tame the Flaky Test | 75 | 40 | 60 |
| | **rotation total** | **530** | **305** | **460** |

- Completion rolls to `(index + 1) % 6` and **carries the overshoot forward**,
  so no work is lost at a boundary (`TestTickCarriesOvershootIntoTheNextSprint`).
- The rotation loops forever. There is no end and no prestige.
- Completing a sprint increments `stats.today.sprintsCompleted` and
  `stats.lifetime.sprintsCompleted`, triggers a `flash{kind:"sprint"}`
  broadcast, and is the one and only moment `awardCoins` runs.
- A loaded save's sprint index is clamped into `[0, 6)` and its progress
  clamped into `[0, target]`, so a corrupted or stale save can never overshoot
  or panic (`RestoreSprint`).

### Sprint names are fiction, and that is a rule

The names come from the static list only — no generation, no interpolation,
nothing derived from the machine. `sprint.go`'s own comment states the
constraint: a sprint name "must never be phrased so it could be read as a
description of the user's real activity". "Fix Bug #404" is unmistakably the
game talking. This is the same boundary that keeps the ticker and the terminal
lines fictional while the activity line stays literal — see
[`moods.md`](moods.md).

### What a "unit" mechanically is

This is worth stating plainly because the word carries no meaning on its own.

A **work unit is `0.008` of one counted keystroke's worth of weighted input
rate, summed once per second.** It is a float accumulator with no external
referent — not a line of code, not a minute, not a task. Concretely, one unit
is:

- **125 counted keystrokes**, or
- **50 seconds of mouse activity**, or
- **half of one completed focus session** (which is worth 2.0 units).

`game.UnitLabel = "units"` is a fixed display string, the same word for every
sprint. The wire sends `sprint.progress` (a float), `sprint.target` (a float)
and `sprint.unitLabel` (that string), and the HUD renders e.g. `34 / 75 units`.

Whether "unit" is the right word for that quantity is an open product question
— see [`BACKLOG.md`](BACKLOG.md) §2.

---

## 2. Dev Cash

The single currency. `uint64`, spendable, and it has exactly one source: a
sprint payout ([ADR 0008](../adr/0008-upgrade-tracks.md)). Nothing else mints
it — not a level-up, not a session, not an app switch, not a streak.

Averaged across the rotation:

- **0.5755 Dev Cash per work unit** (305 / 530)
- **50.83 Dev Cash per sprint** (305 / 6)

Dev Cash is spent in the store and never anywhere else. It does not decay, and
nothing takes it away.

`stats.coinsToday` additionally reports today's Dev Cash split by the signal
that earned it. That is an accounting view over the same payout, not a second
income stream — see [`economy.md`](economy.md) §9.

---

## 3. XP and levels

XP is earned from the same sprint payouts, on the same event, and is
**monotonic** — nothing spends it and nothing reduces it.

The level curve is in `game/sprint.go`:

```
thresholdForLevel(n) = 50 · (n − 1) · n      // 0 for n ≤ 1
levelForXP(xp)       = the largest n whose threshold ≤ xp
```

| Level | XP needed | XP from the previous level |
| --- | --- | --- |
| 1 | 0 | — |
| 2 | 100 | 100 |
| 3 | 300 | 200 |
| 4 | 600 | 300 |
| 5 | 1,000 | 400 |
| 6 | 1,500 | 500 |
| *n* | 50·(n−1)·n | 100·(n−1) |

Pinned exactly by `TestLevelForXPMatchesTheCurve`, including the boundaries
(99 → level 1, 100 → level 2, 299 → level 2, 300 → level 3, …).

One full sprint rotation is 460 XP, so a player who completes all six sprints
once ends at **level 3**. Levels arrive quickly early and stretch out
quadratically.

**A level does nothing.** It unlocks nothing, gates nothing, and grants no Dev
Cash. `Level` is a computed field on the wire (`levelForXP(g.XP)`, never
stored) and is displayed as `LV n` in the HUD. It is a pure "you have been
doing this a while" number.

Whether that is enough — and whether `LV` is the right label — is an open
question, [`BACKLOG.md`](BACKLOG.md) §2.

---

## 4. The store: eight slots, thirty-two items

`game/catalog.go` holds the whole catalog as static Go tables. There is no
procedural generation, no drop table, no randomness anywhere in acquisition:
**everything is bought, nothing is unlocked** (ADR 0008).

Eight slots, in the order the frontend renders them:

| Slot | Tintable | Free tier-0 item | Then |
| --- | --- | --- | --- |
| `hoodie` | **yes** | `hoodie_classic` (0) | Zip-Up 120 · Techwear 300 · Night Cloak 500 |
| `chair` | **yes** | `chair_basic` (0) | Racer 100 · Executive Leather 300 · Anti-Gravity 500 |
| `keyboard` | no | `kb_membrane` (0) | Mechanical 60 · Split Ergo 180 · Neon 60% 300 |
| `mouse` | no | `mouse_stock` (0) | Gaming 50 · Trackball 150 · Vertical Ergo 220 |
| `beverage` | no | `bev_mug` (0) | Thermos 40 · Tea & Saucer 90 · Energy Can 140 |
| `plant` | no | `plant_none` (0) | Succulent 50 · Monstera 140 · Bonsai 260 |
| `wall` | no | `wall_bare` (0) | Poster 80 · Shelf 200 · Neon Sign 380 |
| `buddy` | no | `buddy_none` (0) | Rubber Duck 60 · Desk Bot 250 · Sleeping Cat 300 |

Four items per slot, cheapest first, the free one first of all. Three slots
(`plant`, `wall`, `buddy`) use an explicit "nothing" item as their tier-0 —
`plant_none`, `wall_bare`, `buddy_none` — rather than allowing an empty slot,
which is what lets the wire guarantee that `equipped` has an entry for every
slot, always, and never a `null`.

### Board totals

| | Dev Cash |
| --- | --- |
| All 32 items | **4,770** |
| All 40 extra tint purchases (8 tintable items × 5 non-default tints × 40) | **1,600** |
| **The whole board** | **6,370** |

At 50.83 Dev Cash per sprint that is ≈ **125 sprints**, or ≈ 11,069 work units.
See [`economy.md`](economy.md) §7 for what that costs in hours.

The cheapest purchase in the game is `bev_thermos` at 40. A fresh game starts
with 0 Dev Cash and the first sprint pays 25, so it becomes affordable after the
**second** payout (25 + 40 = 65). `docs/upgrade-design.md`'s pricing table says
"inside the first sprint", which the shipped payouts do not support.

---

## 5. Own many, equip one

The ownership model is three rules, and every one of them is enforced
server-side; the client never asserts an outcome.

1. **Buying is permanent and never auto-equips.** `BuyItem` checks the item
   exists, is not already owned, and is affordable, then deducts and marks it
   owned. It does not touch `Equipped`.
2. **Exactly one item per slot is worn.** `EquipItem` replaces whatever
   occupied that slot. Ownership of the others is unaffected —
   `TestEquipOneWinsPerSlot`.
3. **Every slot's tier-0 item is owned and equipped from the first launch,
   and can never be lost.** `GrantTierZeroDefaults` runs on `New()` *and* is
   re-asserted by `store.Apply` after loading a save, so a corrupted or
   hand-edited save cannot un-grant a guaranteed default.

The failure modes are a closed set of sentinel errors — `ErrUnknownItem`,
`ErrUnknownSlot`, `ErrUnknownTint`, `ErrSlotMismatch`, `ErrAlreadyOwned`,
`ErrNotOwned`, `ErrNotTintable`, `ErrInsufficientFunds` — one per rejection
`docs/ui-spec.md` §6.2 enumerates. A rejected action changes nothing at all;
there is no partial write.

---

## 6. Tints

Six shared colours, defined once in `game/catalog.go` and applied per item:

| Id | Name | Hex | Price |
| --- | --- | --- | --- |
| `slate` | Classic Black | `#2b2b33` | 40 |
| `cobalt` | Cobalt Blue | `#4a7fa8` | 40 |
| `forest` | Forest Green | `#4e8b4f` | 40 |
| `ember` | Cyberpunk Orange | `#a45c3a` | 40 |
| `neon` | Neon Pink | `#e86aa4` | 40 |
| `indigo` | Midnight Indigo | `#6a5aa0` | 40 |

The rules:

- **Only tintable slots have tints.** `hoodie` and `chair` are `Tintable:
  true`; the other six are not, and their equipped ref carries `tintId: null`.
- **Ownership is per `(item, tint)` pair**, keyed as `"<itemId>:<tintId>"` in
  `OwnedTints`. Buying Cobalt for the Zip-Up does not give you Cobalt for the
  Night Cloak.
- **Each item's own `DefaultTint` is free the moment the item is owned**, and
  is never written into `OwnedTints` — `IsTintOwned` treats it as implicitly
  owned. The defaults differ per item (`hoodie_classic`/`indigo`,
  `hoodie_zip`/`slate`, `hoodie_tech`/`forest`, `hoodie_cloak`/`neon`,
  `chair_basic`/`slate`, `chair_racer`/`ember`, `chair_exec`/`ember`,
  `chair_antigrav`/`cobalt`), so a fresh install already owns two colours it
  never bought — which is why the whole-board tint cost is 40 purchases and
  not 48.
- **Equipping is not a back door around buying.** `EquipItem` with an unowned
  `tintId` is rejected (`ErrNotOwned`), and a tintable slot with no `tintId`
  at all is rejected too — that slot always wears a colour.
- Tints are a **multiply** over a grayscale form layer in the renderer, which
  is why the art has to be authored on the upper grayscale steps. That is an
  art-pipeline constraint, documented in `docs/art-direction.md`, not a game
  rule.

---

## 7. What is on the wire

For completeness, the progression half of `StateMessage`
(`app/internal/game/game.go`; the full contract is `docs/ui-spec.md` §6.1):

```
devCash      uint64
level        int          // computed from xp, never stored
xp           uint64
sprint       { index, name, progress, target, unitLabel }
equipped     map[slot] { itemId, tintId|null }   // every slot, always
ownedItems   []string     // sorted
ownedTints   []string     // sorted "itemId:tintId" keys
```

The catalog itself is sent once per connection as a separate `catalog`
message carrying `slots`, `tints` and `items`, so prices and flavour text are
never duplicated into the 1 Hz state broadcast.
