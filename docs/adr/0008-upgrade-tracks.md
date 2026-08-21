# 0008 — Upgrades as data-driven tracks, bought not unlocked

Status: accepted (v0.3 design; see docs/upgrade-design.md for the full spec)

## Context
The product loop is "real activity earns; earnings buy visible upgrades".
v0.2 had one prototype item (a plant auto-appearing at a wallet threshold).
Auto-unlock removes the choice that makes earning meaningful, and a flat item
list makes future content expensive.

## Decision
- Tracks (one scene slot each) with tiered sprites; all content in one static
  table (`UPGRADE_TRACKS`). New tier = one sprite + one row.
- Purchases spend coins (Tab shop strip; never a modal, never pauses work).
- `SaveData.upgrades: BTreeMap<String, u8>` with serde default, so old saves
  load cleanly. The plant becomes a purchase (strictly more agency).
- Every upgrade must be visible on screen; stat-only purchases are banned.

## Consequences
- "Lots of options in future" is a content problem, not a systems problem.
- Migration note: nobody loses coins; the v0.2 threshold rule is deleted.
