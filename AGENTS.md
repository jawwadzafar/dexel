# dexel — agent harness

**Goal:** Dexel — a cozy pixel-art desktop companion whose workday runs on your
real typing. Go + HTML/CSS/TypeScript (ADR 0011). The thing you run is `app/`.
See `CLAUDE.md` for the standing rules and `docs/README.md` for the docs map.

## Invoking the agents

**Claude Code** is the only harness this repo maintains. Implementation work
goes through the subagents defined in `.claude/agents/` — the main session
orchestrates and verifies, it does not implement. The `run-dev-companion` skill
sequences them; `orchestration-playbook` is the method behind it.

(The opencode and goose harnesses that used to be invoked from here are
archived on branch `attic/legacy-rust-and-fleet` — they pinned models that no
longer exist. ADR 0020, `docs/plan/REPO-STRUCTURE-AUDIT.md` §2.3.)

## Coordination

Coordination is file-based under `_fleet/`:

- `_fleet/shared/scripts/` — **tracked**: the `SubagentStop` handover gate
  (`validate-handoff.sh`) registered in `.claude/settings.json`, and the
  append-only run logger (`log-event.sh`).
- `_fleet/local/` — **gitignored, one machine's working state**: handoff
  documents in `_fleet/local/handoffs/` (template included), the task ledger at
  `_fleet/local/LEDGER.md`, and run telemetry under `_fleet/local/runs/`.

Handoff files are the source of truth between agents — read them before
resuming or auditing work, and never delete them mid-run. The durable record of
what actually happened belongs in `docs/plan/ORCHESTRATION-LOG.md`, which is
tracked; `_fleet/local/` is not a substitute for it.

## Changelog

Harness changes are recorded in `docs/HARNESS-CHANGELOG.md` — append a row
there rather than editing this file.
