# dexel — Harness Changelog

Append-only record of changes to this harness: what changed, where it landed,
and why. The orchestrator writes a row whenever feedback is routed into a
skill, agent, or orchestrator change. Never rewrite history — add a row.

`Origin` is `human` for hand-authored changes and `evolved` for changes
proposed by an automated evolution cycle.

| Date | Change | Target | Origin | Reason |
|------|--------|--------|--------|--------|
| 2026-08-19 | Initial fleet build (fleetsmith) | all | human | - |
| 2026-08-24 | Fleet harness archived (opencode agents/commands, fleet.yaml, opencode.json, .agents/checks, RUN_PROMPT.md, discussion_context.md) to branch `attic/legacy-rust-and-fleet`; handover-gate + run-logger scripts moved from the gitignored `_fleet/local/scripts/` to the tracked `_fleet/shared/scripts/` and `.claude/settings.json` repointed; this changelog moved here from `_fleet/shared/CHANGELOG.md`; the 8 `.claude/agents/*` and the `run-dev-companion` + `visual-verification` skills retargeted from Rust/Bevy at the Go/TS product; `milestone-driven-rust-implementation` and `rust-bevy-game-architecture` skills archived; CLAUDE.md and AGENTS.md rewritten | `.claude/**`, `_fleet/shared/**`, `CLAUDE.md`, `AGENTS.md` | human | the repo-structure audit, phase 4 (owner decisions D-2 commit-the-scripts, D-3 move-the-changelog, D-4 rewrite, D-5 archive-both) |
| 2026-08-24 | Fleet handoff gate retired: the `SubagentStop` hook and its two `Bash(sh _fleet/...)` allow entries removed from `.claude/settings.json`, `_fleet/` deleted (2 tracked scripts; history stays on `attic/legacy-rust-and-fleet`), the write-a-handoff-file protocol replaced with a `## Reporting` section in all 8 `.claude/agents/*`, and the file-based coordination layer retired from the `run-dev-companion`, `orchestration-playbook`, `pr-review-lens`, and `pr-merge-decision` skills. Claude subagents report results through the task system; the durable record is `docs/plan/ORCHESTRATION-LOG.md`. Also dropped the 4 unused `.claude/skills/*/evals/evals.json` scaffolds and rewrote `README.md` to a standard OSS structure | `.claude/**`, `_fleet/**` (deleted), `CLAUDE.md`, `README.md` | human | The gate validated an artifact the current workflow no longer produces — a subagent's reply *is* its handoff, so the hook only blocked on a ceremony file nobody read |
