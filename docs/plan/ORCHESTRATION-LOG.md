# Orchestration log — append-only

One line per orchestration event (agent launched/landed, verdicts, commits,
plan changes). Newest at the bottom. The overseer writes this; the point is
that any future session can reconstruct WHERE THE PROJECT IS from the repo
alone. The master plan lives in `v0.4-behind-view-plan.md`.

| When | Event |
|---|---|
| 2026-08-19 | v0.1 M0-M5 shipped via opencode fleet (PRs #1-#7, see docs/pr-log.md). |
| 2026-08-21 | v0.2+v0.3 shipped via PR #8 (pixel art, global input, shop strip, economy fix, active-app line). Opus review caught the global-path 3:1 mouse exploit pre-merge. |
| 2026-08-21 | macOS field test FAILED mechanics (no capture, dishonest moods, invisible store) → ADR 0010, Go-prototype mined for its proven design. |
| 2026-08-21 | User's design PDF ingested: behind-view + store modal + own/equip. v0.4 plan written (this dir). Standing rule recorded: overseer orchestrates only; subagents implement. |
| 2026-08-21 | W1-A (mechanics green-up, Sonnet) and W1-B (PDF→specs, Opus) launched in parallel with disjoint ownership. |
| 2026-08-21 | **ADR 0011: engine pivot.** The PDF design ships on its native stack (Go + HTML/NES.css) in `app/`; Bevy game frozen as legacy; opencode fleet retired from critical path. W1-B redirected mid-flight to spec for the web frontend. Plan Waves 2-3 rewritten. |
| 2026-08-21 | W1-A landed and committed: Rust legacy freeze, fully green. Verification tooling gap found: no pip/pypi access on this box — headless-browser plan may need npx playwright or an alternative; resolve before W2-β verification. |
| 2026-08-21 | **W1-B DONE** (Opus): three spec contracts committed; store-shopping-earns-cash exploit caught at spec time (STORE_OPEN gate). Wave 2 fully unblocked; W2-β + W2-γ launching. Ownership seam resolved: app/public/ belongs to W2-β, not W2-α. |
| 2026-08-21 | **W2-α DONE** (Go core: providers/engine/game/store/WS, -race clean, caught own data race) and **W2-β DONE** (frontend: full ui-spec DOM, store modal, dev harness; verified in real headless browser, caught 330px nested-coordinate bug). Committed e7504af. |
| 2026-08-21 | **W2-γ DONE** (45-sprite behind-view set + 37 thumbs, all assertions green, 3 visual bugs self-iterated, chair-region constraint caught 3 violations during authoring). Committed 40e712e. |
| 2026-08-21 | **W2-δ launched** (reconciliation): hoodie catalog fields, buddy_bot path, native /assets route replacing the symlink, catalog-vs-disk audit test. Next: overseer composed verification (server+fake provider+playwright), then W3. |
| 2026-08-21 | **W2-δ DONE** (seams reconciled + catalog-vs-disk test; committed b358d68). Design agent patched ui-spec §6 with the thumbForm/thumbDetail contract. |
| 2026-08-21 | **OVERSEER COMPOSED-VERIFICATION (Wave 2 gate): the game RUNS end-to-end** — real server + WS + sprites, screenshot judged by eye against the PDF mockups. Structure matches: behind-view scene, terminal scrolling compile lines, sprint panel, honest activity line, [S] STORE. **Gate FAILED on 3 defects**: (1) dev_form_* sprites are broken art — double-hump, center seam, NO hood dome; reads as two bald figures (the art agent's self-check composited with occluding props and missed it — its re-verification method now mandates a props-free silhouette test); (2) sprint units render the raw float ("38.19600152587…"); (3) [S] STORE button clips/wraps in the title bar. Art redo + frontend polish agents dispatched in parallel. This exact loop — run the real product, look at it — is what ADR 0011 bought us. |
