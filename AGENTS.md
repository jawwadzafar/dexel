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
longer exist. See ADR 0020.)


## Changelog

Harness changes are recorded in `docs/HARNESS-CHANGELOG.md` — append a row
there rather than editing this file.
