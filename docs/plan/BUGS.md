# dexel — bug / polish log (evidence-based)

Filed 2026-08-22 from user review of the live screenshots. Each fix must be
verified in the REAL running game (render → look → iterate), not in isolation.

## UI track (frontend: index.html / game.css / features/menu.ts)

- **BUG-1 — Menu needs a one-line title.** The hamburger dropdown currently
  lists only the buttons. Add a single title line (e.g. "MENU") at the top,
  then Store / Activity / History (and room for future sections). Keep it one
  line.
- **BUG-2 — Top-left should be coin + level ONLY.** Remove the "dexel" wordmark
  and the mood dot from the title bar. Show the coin/Dev Cash FIRST, then the
  level. Nothing else top-left.
- **BUG-3 — Bottom-panel text is vertically clipped.** e.g. "SPRINT" in the
  sprint panel is cut off at the bottom — the text row height/line-height is
  ~8px and glyphs are clipped. Increase to ~10px (and audit the other panel/
  terminal/status text for the same clipping) so no text is cut. Verify in the
  real render that full glyphs/descenders show.

## Art track (tools/gen_assets.py / assets / geometry.ts) — FIXED 2026-08-22

**Resolved:** root cause was the head sitting above the hard-region line so no
chair could rise over it — fixed by LOWERING the head (not raising chairs).
Hood dome dropped ~11px; arms pulled outward with a 4px gap each side (hood +
2 arms now read as 3 forms); every chair back painted up to the hard-region
top with always-visible detail markers (mesh/stitch/tufting/glow) so the
chair-top reads above the head at any tint and stays distinct from the hoodie.
Overseer-gated across basic/exec/antigrav.

Evidence: in the current hero/scene render the character still doesn't clearly
read as sitting IN the chair, and several elements blend together.

- **BUG-4 — Hoodie and chair don't visually distinguish.** The character's
  hoodie and the chair behind it are too similar in tone/shape; they merge.
  They need clearly different value/shape so the eye separates person from seat.
- **BUG-5 — Character still doesn't read as ON the chair.** Key direction from
  the user: from behind, the TOP of the chair (backrest top) should be seen
  FIRST — i.e. the chair back rises above the head and reads as the topmost/
  backmost element, with the person's head sitting BELOW the chair-back's top
  edge. Right now the person reads as in front of / on top of a low shape.
- **BUG-6 — Hand + hood merge into one blob.** The arms/hands reaching to the
  keyboard blend into the hood/head so it reads as a single blob. Separate them
  — a gap, an outline, or distinct shading between the hood, the shoulders, and
  the arms/hands.
- **BUG-7 — Behind-view hooded-figure reference.** Study how a person actually
  looks from behind wearing a hood while seated (hood drape, shoulder line,
  where the chair shows around them) and apply it so the silhouette is legible.

Fix approach: BUG-5/6/7 are one coherent redraw of the behind-view seated
figure + chair relationship (dev sprite + chair sprites + the CHAIR_RECT/DEV_RECT
geometry), gated by real in-game renders judged by eye until it clearly reads as
"a hooded person seated in a chair, seen from behind."

## Follow-ups found during fixes

- **BUG-8 — Activity modal footer/last row clipped (pre-existing).** `#activity`
  is declared `height: 396px`, but the browser caps a native `dialog:modal` at
  `max-height: calc(100% - 38px)` = 362px at the game's 400px viewport, so the
  modal's footer and last "coins earned today" row get scroll-clipped. Predates
  the BUG-1/2/3 fixes (Store/History modals stay under the cap). Fix: shrink the
  Activity modal to fit ≤362px (tighten row heights / overall height) or
  restructure so nothing is cut. UI/CSS follow-up.
