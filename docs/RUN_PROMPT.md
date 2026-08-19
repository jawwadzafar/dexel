# Standing run brief for the dev-companion fleet

This file exists so invoking the fleet doesn't require re-pasting the full
brief every time — just point the orchestrator at this file:

```
/run-dev-companion read docs/RUN_PROMPT.md and follow it exactly
```

## The brief

Implement `docs/implementation-plan.md`, milestones M0 through M5, all in one
run. The architect phase is already complete — read
`_fleet/local/handoffs/01-game-architect-to-game-engineer.md` and
`_fleet/local/LEDGER.md` (task 1 is done) — skip the Architecture phase.

This whole implement-review-merge cycle is ONE orchestrator phase
("Milestone cycle") with a single loop spanning all six milestones (max 30
passes) — it does not stop and wait after M0 merges. If any single pass
stalls (an agent announces a plan and stops without finishing, which
`game-engineer`'s deepseek model is known to occasionally do), re-invoke that
same phase's agent with exactly what's missing appended, per the
orchestrator's own loop rule — check `docs/milestone-log.md`/`docs/pr-log.md`
for real evidence of progress before assuming a pass is done, never trust an
agent's self-report alone.

For each milestone, in order, run the full cycle before starting the next:

1. **game-engineer** implements the milestone on its own branch
   (`milestone/m<N>-<slug>`), commits as it goes, runs the full command
   sequence from the plan's §5, appends a `docs/milestone-log.md` entry, pushes,
   and opens a PR against `main`.
2. **pr-reviewer-correctness**, **pr-reviewer-boundaries**, and
   **pr-reviewer-tests** each independently review that PR — in their own
   isolated git worktree, never the shared checkout — and each write their
   own verdict handoff to `pr-merge-decider`. They run concurrently and never
   coordinate with each other.
3. **pr-merge-decider** waits for all three verdicts, applies the decision
   rule (a boundaries veto blocks outright; otherwise 2-of-3 approval
   merges), and either merges the PR (`gh pr merge --squash --delete-branch`)
   or posts a consolidated change request back to game-engineer on the same
   branch/PR.
4. Only once the PR is merged does game-engineer branch for the next
   milestone.

Do not stop for confirmation at any point in this cycle — commits, pushes,
opening PRs, independent reviews, and merging on approval all happen without
per-action human confirmation. This project runs under standing
authorization for that (see `docs/pr-log.md`'s and the `pr-merge-decision`
skill's notes on this — it isn't a one-time approval, it's the standing
policy for this repo).

Only stop early if a milestone genuinely cannot be completed as scoped, or a
PR gets stuck in repeated Request-changes cycles — in that case, record the
blocker explicitly in `docs/milestone-log.md` or `docs/pr-log.md` and stop
there rather than skipping ahead. Do not introduce architecture beyond the
plan's §3, and do not let two milestones' branches be open unreviewed at the
same time.

## Repo

`origin` is `git@github.com:jawwadzafar/dev-companion.git` (private). All
commits, branches, and PRs go there under the current git identity
(`git config user.name`/`user.email`) and the `gh` CLI's already-authenticated
account — no credentials need to be supplied by hand.
