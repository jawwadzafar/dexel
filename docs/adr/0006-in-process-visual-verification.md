# 0006 — In-process capture + vision-model verification

Status: accepted (v0.1 post-mortem)

## Context
Every model developing this game is text-only. A test asserting "the HUD
entity was spawned" passed while the HUD was invisible off-screen — that bug
survived five milestones and three approving reviewers until a human looked
at the window. External screenshot tools were a dead end on this machine (no
X authorization from agent shells, none installed).

## Decision
Two-part verification for anything visual:
1. `tools/shotcap` — runs the REAL app under its normal winit runner and
   captures its own framebuffer via Bevy's `Screenshot` component, with
   seedable game state (`DEV_COMPANION_SEED_*`).
2. `scripts/visual-check.py` — sends a capture to a vision-capable model.
   Ask it to DESCRIBE and critique; never a leading yes/no question.

## Consequences
- Mechanical proof and visual proof are different claims; a milestone with a
  visual criterion needs both. This is encoded in the fleet's
  `visual-verification` skill and the `visual-verifier` agent.
- The capture tool must reproduce real behaviour: it once silently omitted a
  system (`desk_upgrade_system`), making its screenshots "prove" the wrong
  thing. A verification tool with drift is worse than none — keep both entry
  points sharing configuration (`configured_default_plugins`, same schedule).
- Pixel-region counting on captures gives cheap deterministic assertions
  (e.g. plant absent below threshold: 0 px; present above: 808 px).
