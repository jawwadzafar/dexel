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
