---
name: visual-verification
description: |
  How to verify a milestone's visual exit criterion for dev-companion: capture the running game's own framebuffer with the in-process `shotcap` tool, then ask a vision model what is actually on screen via scripts/visual-check.py. Use whenever a milestone's exit criterion describes something a human must SEE (a progress bar moving, a HUD element appearing, a character animating, art looking finished), or when a milestone log records a visual check as unverified.
x-fleetsmith-origin: human
---

# Visual verification

Every model this fleet runs on is TEXT-ONLY — it cannot look at a
screenshot. Two tools bridge that gap. Use them; do not fall back to
asserting "the tests pass so the UI must be fine". A test that checks
"the HUD entity was spawned" passes happily while the HUD is invisible
off-screen — that exact bug shipped through five milestones and three
approving reviewers before a human looked at the window.

## Capture: use in-process capture, NOT an X screenshot

External screenshot tools (`import`, `scrot`, `grim`) have never worked
on this machine — the X display is unauthorized from agent shells and
none of those binaries are installed. Do not spend time on them.

Instead use `tools/shotcap`, which runs the REAL game app under its
normal winit runner, captures its own framebuffer via Bevy's
`Screenshot` component, and exits by itself:

```bash
source ~/.cargo/devcompanion-env.sh
export XAUTHORITY=/home/darkmirror/.Xauthority DISPLAY=:0
mkdir -p /tmp/shots
SHOTCAP_OUT=/tmp/shots SHOTCAP_EXIT_SECS=6 cargo run -p shotcap
ls /tmp/shots/    # shot_2s.png, shot_5s.png
```

It accepts a seeded save so you can capture a specific game state
rather than a fresh one: `DEV_COMPANION_SEED_COINS`,
`DEV_COMPANION_SEED_XP`, `DEV_COMPANION_SEED_WORK_DONE`. Use that to
photograph the exact condition a criterion describes (e.g. seed coins
above the desk-upgrade threshold to prove the upgrade appears).

## First: can YOU see?

If you are a model with native vision (a Claude agent), Read the
screenshot directly and judge it yourself — do not outsource judgment
you can make better firsthand. The pipeline below is the primary path
for text-only models (the opencode fleet) and a second opinion for
everyone else.

## Judge: ask a vision model, and ask it to be critical

```bash
python3 scripts/visual-check.py /tmp/shots/shot_5s.png \
  "Describe exactly what you see: scene contents, any character, art style, palette. Be specific and critical — if it looks unfinished or like placeholder programmer-art rectangles, say so plainly."
```

Ask it to DESCRIBE and let it criticise; then judge its description
yourself. Never ask a leading question — "is the progress bar filling
up?" invites a yes. This exact prompt has correctly called the build
"placeholder programmer art" when that was true, so it is discriminating
enough to trust as a gate.

For a before/after criterion (a counter that should increase), capture
two shots with activity in between and compare the two descriptions.

## Verdicts

- **CONFIRMED** — the description independently names the element the
  criterion requires. Quote its exact words as the evidence.
- **REFUTED** — the description shows the element missing or wrong.
  Quote it and name which criterion failed.
- **BLOCKED** — capture genuinely impossible or every vision model
  down. Report the exact command and error. **Never** upgrade BLOCKED
  to CONFIRMED because unit tests pass; mechanical proof and visual
  proof are different claims.
