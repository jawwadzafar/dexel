---
name: rust-bevy-game-architecture
description: |
  Methodology for architecting the dev-companion Rust + Bevy desktop game: ECS component/system/resource design, the ActivityProvider abstraction boundary, save/load persistence, and milestone sequencing down to the smallest playable vertical slice. Use when writing or revising docs/implementation-plan.md, deciding how a new signal (git, VS Code, AI agent) should enter the ECS, or scoping a new milestone.
x-fleetsmith-origin: human
---

# Rust + Bevy game architecture for dev-companion

## Non-negotiable boundaries

1. **Activity monitoring is a separate crate from the game.** The game
   (Bevy) crate never reads OS input or editor/git state directly — it
   only consumes `ActivityEvent`s from an `ActivityProvider` trait object
   defined in a plain-Rust `activity` crate with zero Bevy dependency.
   This is what lets VS Code / Git / AI-agent signals get added later as
   new event producers without touching any game system.
2. **Never collect or persist raw input content.** Counts and durations
   only (keystrokes-this-tick, idle duration, focus duration). No key
   identity, no file contents, no clipboard, no window titles beyond
   "focused vs not."
3. **Reward meaningful sessions, not raw activity volume.** Any function
   mapping activity to progress must clamp/smooth its input (e.g. a
   diminishing-returns curve or a rate cap) so key-mashing plateaus
   quickly. State this as a testable pure function, not a comment.

## Default shape (deviate only with a stated reason)

- Cargo workspace, two crates: `activity` (lib, no Bevy dep) and
  `companion` (bin, Bevy app). This is a compiler-enforced boundary, not
  a convention someone can accidentally violate.
- Bevy `States<AppState>` for `Loading` / `Playing`; character mood
  (`Idle` / `Coding` / `OnBreak`) is a component field, not a global
  state, since it belongs to the developer entity.
- Progression math (activity → progress delta, XP thresholds, level
  curve) lives in plain functions outside any Bevy system so they are
  unit-testable without spinning up an `App`.
- Persistence is `serde` + JSON to the OS data dir (`dirs` crate). JSON
  until there's a concrete reason to change — it's human-diffable while
  debugging save format issues.

## Milestone sequencing rule

Order milestones so the game is playable (even if ugly) as early as
possible, and every milestone after that only adds one new axis:
rendering skeleton → activity wiring → the actual game loop →
persistence → polish. Never bundle "wire up the input source" and "build
the reward loop" into the same milestone — if the reward math is wrong,
you need to know without also doubting the input plumbing.

Every milestone's exit criterion must be a command someone can run and
get a pass/fail answer from — "the character looks nice" is not a
milestone exit criterion, "cargo test --workspace passes and the
progress bar visibly moves after 10s of typing" is.
