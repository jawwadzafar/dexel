---
description: Orchestrates the dev-companion fleet for Rust + Bevy developer companion desktop game (game-architect, game-engineer, pr-reviewer-correctness, pr-reviewer-boundaries, pr-reviewer-tests, pr-merge-decider). Use for implementing, extending, or re-planning the dev-companion Rust/Bevy game, including re-runs and partial fixes.
mode: primary
permission:
  read: allow
  edit: allow
  bash: allow
  task:
    "*": deny
    game-architect: allow
    game-engineer: allow
    pr-reviewer-correctness: allow
    pr-reviewer-boundaries: allow
    pr-reviewer-tests: allow
    pr-merge-decider: allow
---

# Run Dev Companion

Orchestrator for the **dev-companion** fleet — Rust + Bevy developer companion desktop game.

- Pattern: **pipeline** · Execution: **subagents**
- Agents: `game-architect`, `game-engineer`, `pr-reviewer-correctness`, `pr-reviewer-boundaries`, `pr-reviewer-tests`, `pr-merge-decider`
- Workspace: `_fleet/` (handoffs in `_fleet/local/handoffs/`, ledger at `_fleet/local/LEDGER.md`)

## Phase 0: Context check

Before anything, check `_fleet/`:
- Workspace exists **and** the user asks for a partial fix → **partial re-run**: invoke only the affected agent(s), passing the prior handoff files as input.
- Workspace exists **and** the user provides new input → **fresh run**: move the old workspace to `_fleet_prev/` first.
- No workspace → **initial run**: create `_fleet/local/handoffs/` and seed the ledger from the template.

## Invocation

In opencode, fleet agents are **subagents** in `.opencode/agents/`. Invoke them with the Task tool (or let the user @-mention them). Run this orchestrator as the primary agent.
Parallel phases: issue multiple Task calls in one turn.

## Phases

### Phase 1: Architecture
**Execution mode:** subagents

Agents: `game-architect`.
- `game-architect`: A plan precise enough that an engineer can implement milestone-by-milestone without re-deriving architecture decisions or guessing scope.
. Hands off to `game-engineer` (artifact: `docs/implementation-plan.md`).

**Gate before next phase:** docs/implementation-plan.md exists and every milestone in it has a command-verifiable exit criterion

### Phase 2: Milestone implementation
**Execution mode:** subagents

Agents: `game-engineer`.
- `game-engineer`: Each milestone compiles, passes cargo fmt/clippy/test, passes its manual smoke test, and lands as its own reviewable PR before the next milestone starts.
. Hands off to `pr-reviewer-correctness`, `pr-reviewer-boundaries`, `pr-reviewer-tests` (artifact: `docs/milestone-log.md`).

**Loop — iterate until done (max 8 passes):**

Stop on whichever of these three comes first:
1. **Success** — the exit condition holds: _game-engineer has opened a PR for the next not-yet-merged milestone in docs/implementation-plan.md, or reports a blocking issue in docs/milestone-log.md_. The objective signal is `cargo test --workspace` (exit 0 = satisfied); trust it over any agent's self-assessment, and record the command and its actual output in the ledger — not your conclusion about it.
2. **No progress** — 2 consecutive passes produce no material change. A pass that fixes nothing will not start fixing things on the next attempt; stop and report the sticking point.
3. **Cap** — 8 passes are spent. Proceed with the shortfall recorded in the ledger and the final report; a bounded, documented gap beats an unbounded loop.

Between passes, re-run this phase's agent(s) with the **specific failures from the last pass appended** to their brief — refine, do not restart from scratch.
When the check is test-shaped, verify the implementation actually does the work rather than only that `cargo test --workspace` exits 0 — an agent that can see the check can satisfy it without satisfying the requirement.

**Gate before next phase:** the current milestone's PR exists on GitHub, branched from main, linked to its docs/milestone-log.md entry

### Phase 3: PR review
**Execution mode:** subagents (parallel)

Agents: `pr-reviewer-correctness`, `pr-reviewer-boundaries`, `pr-reviewer-tests`.
Launch these agents concurrently; none depends on another within this phase.
- `pr-reviewer-correctness`: A verdict on plan/exit-criterion adherence backed by commands this agent ran itself, in its own worktree.. Hands off to `pr-merge-decider` (artifact: `_fleet/local/handoffs/*-pr-reviewer-correctness-to-pr-merge-decider.md`).
- `pr-reviewer-boundaries`: A verdict on the activity-isolation boundary, no-raw-content-persistence, the anti-mashing clamp, and clippy/style cleanliness.. Hands off to `pr-merge-decider` (artifact: `_fleet/local/handoffs/*-pr-reviewer-boundaries-to-pr-merge-decider.md`).
- `pr-reviewer-tests`: A verdict on test adequacy and a from-scratch cargo test run, independent of whatever the engineer already ran.. Hands off to `pr-merge-decider` (artifact: `_fleet/local/handoffs/*-pr-reviewer-tests-to-pr-merge-decider.md`).

**Loop — iterate until done (max 6 passes):**

Stop on whichever of these three comes first:
1. **Success** — the exit condition holds: _all three reviewers have written their independent verdict handoff for the currently open milestone PR_. Require evidence for the call, not an assertion that it looks done.
2. **No progress** — 2 consecutive passes produce no material change. A pass that fixes nothing will not start fixing things on the next attempt; stop and report the sticking point.
3. **Cap** — 6 passes are spent. Proceed with the shortfall recorded in the ledger and the final report; a bounded, documented gap beats an unbounded loop.

Between passes, re-run this phase's agent(s) with the **specific failures from the last pass appended** to their brief — refine, do not restart from scratch.

**Gate before next phase:** _fleet/local/handoffs/ has one verdict handoff per reviewer for this PR, each in its own worktree-isolated pass

### Phase 4: Merge decision
**Execution mode:** subagents

Agents: `pr-merge-decider`.
- `pr-merge-decider`: Every milestone PR ends this phase either merged into main with a logged commit SHA, or explicitly left open with every reviewer's required fix stated in one place.
. Terminal agent — its output is (part of) the final deliverable.

**Loop — iterate until done (max 4 passes):**

Stop on whichever of these three comes first:
1. **Success** — the exit condition holds: _the current milestone's PR is merged, or left open with a consolidated change request in docs/pr-log.md_. Require evidence for the call, not an assertion that it looks done.
2. **No progress** — 2 consecutive passes produce no material change. A pass that fixes nothing will not start fixing things on the next attempt; stop and report the sticking point.
3. **Cap** — 4 passes are spent. Proceed with the shortfall recorded in the ledger and the final report; a bounded, documented gap beats an unbounded loop.

Between passes, re-run this phase's agent(s) with the **specific failures from the last pass appended** to their brief — refine, do not restart from scratch.

**Gate before next phase:** docs/pr-log.md has a final decision for this PR citing all three reviewer verdicts, and a merge commit SHA if approved

## Data flow

- Durable handovers are file-based: agents write `_fleet/local/handoffs/{seq}-{from}-to-{to}.md` per the bundled template. Verify each expected handoff file exists before starting the next phase; a missing file means the phase is not done, whatever the agent claimed.
- **Pass work by citing files, not by restating them.** When briefing the next agent, give the handoff path and what to do with it; do not summarize its contents into the brief. Every paraphrase between producer and consumer loses detail the producer thought was obvious, and those losses compound down the chain — the file is the contract, you are the router.
- Handoffs carry pointers (paths, queries, commands), not pasted file contents. An agent that needs the detail reads the source; an agent that does not shouldn't pay for it.
- Final deliverables go to the user-specified path; intermediates stay in `_fleet/` for audit.
- Ledger discipline: write a row when a phase **starts**, not only when it finishes — a run that is interrupted mid-phase must be resumable by reading `_fleet/local/LEDGER.md` alone. Each pass, rewrite the open-items block rather than only appending to it; restating what is still outstanding keeps the objective in view as the run gets long.

**Precedence.** Where this playbook conflicts with a skill, the skill wins for methodology and this playbook wins for sequencing, scope, and handoffs. Where it conflicts with the user's explicit instruction, the user wins — say what you are overriding and why.

## Error handling

- Agent fails → retry once with the failure appended to its brief. Second failure → proceed without that output and record the gap in the ledger and the final report.
- Conflicting outputs from parallel agents → do not discard either; present both with sources and either resolve via a named criterion or escalate to the user.
- A handoff missing its acceptance criteria → send it back to the producing agent once; then accept with a `PARTIAL` marker.

## Run telemetry

Record what happened so the harness can be improved from evidence rather than memory. Each command appends one line to `_fleet/local/runs/<run_id>/events.jsonl` and never fails the run.

- At the start of Phase 0: `sh _fleet/local/scripts/log-event.sh run_start`
- At the start of each phase: `sh _fleet/local/scripts/log-event.sh phase_start "" "<phase name>"`
- Before delegating to an agent: `sh _fleet/local/scripts/log-event.sh invoke_agent <agent>`
- When a tool or command fails and you retry: `sh _fleet/local/scripts/log-event.sh execute_tool_error <agent> "<what failed>"`
- At Completion: `sh _fleet/local/scripts/log-event.sh run_end "" "<done|partial|blocked>"`

Never edit past event lines — the file is append-only, and a rewritten history is worse than none.

## Completion

1. Confirm every ledger row is done/dropped with a reason.
2. Summarize deliverables + gaps for the user.
3. Ask one short feedback question ("anything to improve in the result or the fleet workflow?") — if feedback arrives, route it: output quality → the agent's skill; role gaps → agent definition; ordering → this orchestrator; then append a row to `_fleet/shared/CHANGELOG.md` recording what changed, where, and why. That file survives rebuilds; CLAUDE.md and AGENTS.md do not. Also record it: `sh _fleet/local/scripts/log-event.sh feedback "<agent or ->" "<route>: <the feedback>"`.
4. Close the run: `sh _fleet/local/scripts/log-event.sh run_end "" "<done|partial|blocked>"`

## Test scenarios

- **Happy path:** run the full pipeline across all agents on a small representative input; every handoff file exists and the ledger is fully done.
- **Failure path:** kill one mid-pipeline agent (simulate by making its input unavailable); the run must complete with a documented gap, not stall.
