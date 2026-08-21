# dexel — agent harness

**Goal:** Rust + Bevy developer companion desktop game

For implementing, extending, or re-planning the dexel Rust/Bevy game, run the fleet orchestrator instead of working solo. Simple questions can be answered directly.

## Invoking the fleet

- **opencode:** run `/run-dev-companion`, or switch to the `run-dev-companion` primary agent (fleet subagents live in `.opencode/agents/`).
- **goose:** `goose run --recipe .goose/recipes/run-dev-companion.yaml`
- **Claude Code:** the `run-dev-companion` skill triggers on domain requests (see CLAUDE.md).

## Coordination

Fleet coordination is file-based under `_fleet/`: handoff documents in `_fleet/local/handoffs/` (template provided) and a task ledger at `_fleet/local/LEDGER.md`. Handoff files are the source of truth between agents — read them before resuming or auditing fleet work, and never delete them mid-run.

## Changelog

Harness changes are recorded in `_fleet/shared/CHANGELOG.md` — append a row there rather than editing this file, which is regenerated on every build.
