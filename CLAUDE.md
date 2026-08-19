## Harness: dev-companion

**Goal:** Rust + Bevy developer companion desktop game

**Trigger:** For implementing, extending, or re-planning the dev-companion Rust/Bevy game, use the `run-dev-companion` skill. Simple questions can be answered directly.

**Handover gate:** `.claude/settings.json` registers a `SubagentStop` hook running `_fleet/local/scripts/validate-handoff.sh`, which blocks a fleet agent from finishing until its handoff file exists and carries every required section. Note that project-level hooks do not run until this workspace is trusted — until you accept that dialog the gate is silently skipped and the fleet degrades to advisory instructions.

**Changelog:** harness changes are recorded in `_fleet/shared/CHANGELOG.md` — append a row there rather than editing this file, which is regenerated on every build.
