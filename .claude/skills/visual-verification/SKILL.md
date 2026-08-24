---
name: visual-verification
description: |
  How to verify a visual exit criterion for dexel: render the REAL running app (build the Go binary, run it with the fake provider, screenshot the page with headless Chromium per the feature-build-and-verify recipe), then judge the image — yourself if you have vision, and/or via scripts/visual-check.py's vision model. Use whenever an exit criterion describes something a human must SEE (a progress bar moving, a HUD element appearing, a character animating, art looking finished), or when a log records a visual check as unverified.
x-fleetsmith-origin: human
---

# Visual verification

Do not fall back to asserting "the tests pass so the UI must be fine". A
test that checks "the element was created" passes happily while the
element is invisible off-screen — that exact bug shipped through five
milestones and three approving reviewers before a human looked at the
window. Mechanical proof and visual proof are different claims.

## Capture: render the REAL app, in a headless browser

The product is a Go server serving an HTML page, so the capture path is
the one in the `feature-build-and-verify` skill (§ THE GATE) — that
skill owns the recipe; this one owns the judging. In short:

```bash
export PATH=/home/darkmirror/go-toolchain/go/bin:$PATH
cd app && go build -o /tmp/dc-verify . && cd ..

# The fake provider is a deterministic scripted timeline (pure function of
# elapsed time since Start), so a screenshot reproduces on any box and
# needs no input-monitoring permission.
DEVCOMPANION_FAKE_SCRIPT="type:20s,idle:40s,mouse:15s" \
  /tmp/dc-verify -provider fake -addr 127.0.0.1:8099 &

curl -s http://127.0.0.1:8099/api/health   # publicOk:true before you shoot

# 640x400 is the shipping resolution (art-direction non-negotiable #7).
npx --yes playwright screenshot --viewport-size=640,400 \
  --wait-for-timeout=3000 http://127.0.0.1:8099/ /tmp/dc-shot.png
kill %1
```

Two notes that save time. External X screenshot tools (`import`,
`scrot`, `grim`) have never worked from agent shells on this machine —
do not spend time on them; the headless browser is the supported path.
And to photograph a specific game state rather than a fresh one, seed a
save file or drive the app through the WS/HTTP surface — never fake the
screenshot.

(The old capture path was `cargo run -p shotcap`, an in-process Bevy
framebuffer grab. That crate is archived with the frozen Rust game on
branch `attic/legacy-rust-and-fleet` — ADR 0011, ADR 0020. There is
nothing to run it against any more.)

## First: can YOU see?

If you are a model with native vision (a Claude agent), Read the
screenshot directly and judge it yourself — do not outsource judgment
you can make better firsthand. The pipeline below is a second opinion
for you, and the primary path for a text-only model.

## Judge: ask a vision model, and ask it to be critical

```bash
python3 scripts/visual-check.py /tmp/dc-shot.png \
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
