## Project: dexel

**Goal:** Dexel — a cozy pixel-art desktop companion whose workday runs on your
real typing. Go + HTML/CSS/TypeScript (ADR 0011). The thing you run is `app/`.

**Stack:** Go server + WebSocket + a committed TypeScript bundle, shipped as ONE
binary (`app/embed.go` compiles `app/public/` and `app/assets/` in). A Tauri v2
shell lives in `desktop/`.

**Where to start:** `docs/README.md` indexes the four documentation layers.
`docs/game/` is how the game works today; `docs/plan/ROADMAP.md` is what's next;
`docs/adr/` is why.

**Source of truth for the work itself:** `docs/plan/ROADMAP.md` is the plan and
`docs/plan/ORCHESTRATION-LOG.md` is its append-only event log. Neither lives in
chat: read both before planning anything, and append to the log as work lands.
If this file and those two disagree, they win.

**How work gets done — standing rule:** the main session **orchestrates only**.
ALL implementation goes through subagents, with exclusive file ownership per
agent (two agents editing one file has already cost this repo real rework), and
every "done" is re-verified by the orchestrator from a clean build/cache before
it is trusted — agents have reported green off a stale cache while the tree did
not actually build. Agent definitions live in `.claude/agents/`; the
`orchestration-playbook` skill is the full method.

**The gate:** no visual/UX change is done until it is rendered in the REAL running
game — build the Go binary, run it with the fake provider, screenshot it, judge it
with your own eyes. Isolated mockups have lied twice. See the
`feature-build-and-verify` skill.

**Commit authorship (owner mandate):** commits carry the repo's main author
ONLY. NEVER add Claude as a co-author — no `Co-Authored-By: Claude ...` trailer,
ever, in any commit made by the main session or any subagent. (A session link
trailer is acceptable; co-authorship is not.)

**Before shipping:** `cd app && go vet ./...`, `bash scripts/test-race.sh`, and
`cd app/frontend && npm run typecheck && npm run build` with no bundle drift.
GitHub Actions is currently blocked at the account level — run the gates locally.

**Legacy:** the Rust/Bevy implementation and the opencode fleet harness are
archived on branch `attic/legacy-rust-and-fleet` (ADR 0011, ADR 0020). Two things
named `activity` used to exist; now only `app/internal/activity/` does.

**Changelog:** harness changes go in `docs/HARNESS-CHANGELOG.md`.
