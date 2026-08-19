---
name: pr-merge-decision
description: |
  Methodology for pr-merge-decider: how to synthesize three independent reviewer verdicts on a milestone PR into one decision, merge on approval, and consolidate change requests otherwise. Use whenever deciding the fate of a dev-companion milestone PR after all three reviewers have reported.
x-fleetsmith-origin: human
---

# Merge decision

## Before deciding

Confirm all three handoffs exist for this PR:
`_fleet/local/handoffs/*-pr-reviewer-correctness-to-pr-merge-decider.md`,
`*-pr-reviewer-boundaries-to-pr-merge-decider.md`,
`*-pr-reviewer-tests-to-pr-merge-decider.md`. If any is missing, do not
decide — that reviewer hasn't finished; wait or flag it as blocked in
`docs/pr-log.md` rather than deciding on two-of-three by default.

## Decision rule

1. If `pr-reviewer-boundaries` reported a veto (a stated architecture
   boundary violation): **Request changes**, full stop, regardless of
   the other two verdicts. This is the one lens with veto power because
   its three checks (activity isolation, no raw content, anti-mashing
   clamp) are the plan's non-negotiables, not a quality preference.
2. Otherwise: **Approve and merge** if at least 2 of the 3 reviewers
   approved; **Request changes** if 2 or more requested changes.

## On approval

1. `gh pr review <n> --approve --body "..."` — this is the one formal
   GitHub review posted for this PR; summarize what each of the three
   reviewers verified.
2. `gh pr merge <n> --squash --delete-branch` — do not wait for a human
   to merge. This project runs with standing authorization to commit,
   push, open PRs, review, and merge without per-action confirmation;
   that authorization lives here and in `docs/RUN_PROMPT.md`, not in
   any single conversation turn.
3. Append a row to `docs/pr-log.md`: PR number, milestone, each
   reviewer's individual verdict, final decision, merge commit SHA
   (`git rev-parse HEAD` on `main` after the merge, or the SHA `gh pr
   merge` reports).

## On request-changes

1. `gh pr review <n> --request-changes --body "..."` consolidating
   every reviewer's required fix into one list — game-engineer reads
   only this, not each reviewer's individual handoff, so nothing you
   omit here reaches them.
2. Do not merge. Append a row to `docs/pr-log.md` recording the
   decision and the consolidated fix list.
3. Leave the PR open on its existing branch — `game-engineer` fixes and
   re-hands-off to the reviewers on the same branch, not a new PR.
