---
name: visual-verification
description: |
  How to verify a milestone's visual exit criterion for dev-companion: launch the game, capture a screenshot, and ask a vision model what is on screen via scripts/visual-check.py. Use whenever a milestone's exit criterion describes something a human must SEE (a progress bar moving, a HUD element appearing, a counter incrementing), or when a milestone log records a visual check as unverified.
x-fleetsmith-origin: human
---

# Visual verification

Every model this fleet runs on is TEXT-ONLY — it cannot look at a
screenshot. `scripts/visual-check.py` bridges that: it sends an image
to a vision-capable model over HTTP and prints the description. This is
why M2 and M3 kept logging their visual criteria as unproven.

## The check

```bash
# 1. Confirm a display exists FIRST. Without it there is nothing to capture.
echo "$DISPLAY"; xdpyinfo >/dev/null 2>&1 && echo "display ok" || echo "NO DISPLAY"

# 2. Launch the game in the background, give it time to draw a frame.
source ~/.cargo/devcompanion-env.sh
cargo run -p companion >/tmp/companion-run.log 2>&1 &
GAME_PID=$!
sleep 15

# 3. Capture. Use whichever tool exists (import/scrot/grim/gnome-screenshot).
import -window root /tmp/companion-shot.png 2>/dev/null \
  || scrot /tmp/companion-shot.png 2>/dev/null \
  || grim /tmp/companion-shot.png 2>/dev/null

# 4. Ask a SPECIFIC question — not "does it look right".
python3 scripts/visual-check.py /tmp/companion-shot.png \
  "Describe every UI element you can see, including any progress bar and any numeric counters. If a progress bar is present, roughly what percentage is filled?"

# 5. Always clean up.
kill $GAME_PID 2>/dev/null
```

## Asking well

Ask the vision model to DESCRIBE, then judge its description yourself.
Never ask a leading question — "is the progress bar filling up?" invites
a yes. "Describe every UI element and any numeric values" gets you
evidence you can actually evaluate. For a before/after criterion (a
counter that should increase), capture two screenshots with activity in
between and compare the two descriptions.

## Verdicts

- **CONFIRMED** — the description independently names the element the
  criterion requires. Quote its words.
- **REFUTED** — the description clearly shows the element missing or
  wrong. Quote it and report which criterion failed.
- **BLOCKED** — no display, no screenshot tool, the window never
  appeared, or every vision model failed. Report the exact command and
  error. **Never** upgrade BLOCKED to CONFIRMED because the unit tests
  pass — mechanical proof and visual proof are different claims, and
  conflating them is how an unverified UI ships looking verified.

## Known environment issue

This machine's X display has been dead (`:1025` socket exists but the
server refuses connections; `xdpyinfo` hangs). While that holds, the
honest outcome is BLOCKED — say so plainly rather than working around
it. `scripts/visual-check.py` itself is verified working and can be
tested independently of any display:

```bash
python3 -c "from PIL import Image, ImageDraw; i=Image.new('RGB',(200,200),(255,230,40)); ImageDraw.Draw(i).ellipse([40,40,160,160],fill=(120,40,200)); i.save('/tmp/t.png')"
python3 scripts/visual-check.py /tmp/t.png "What shape and colors?"
# expect: a purple circle on a yellow background
```
