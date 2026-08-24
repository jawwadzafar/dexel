---
name: pr-merge-decision
description: |
  Methodology for pr-merge-decider: how to synthesize three independent reviewer verdicts on a milestone PR into one decision, merge on approval, and consolidate change requests otherwise. Use whenever deciding the fate of a dev-companion milestone PR after all three reviewers have reported.
x-fleetsmith-origin: human
---

# Merge decision

## Before deciding

Confirm your brief carries all four verdicts for this PR — from
`pr-reviewer-correctness`, `pr-reviewer-boundaries`, `pr-reviewer-tests`,
and `visual-verifier` — and that each names the PR number you were asked
to decide. If any is missing, do not decide: that reviewer hasn't
finished. Say so and let the orchestrator wait, or flag it as blocked in
`docs/plan/ORCHESTRATION-LOG.md`, rather than deciding on a partial set
by default.

## Decision rule

1. If `pr-reviewer-boundaries` reported a veto (a stated architecture
   boundary violation): **Request changes**, full stop, regardless of
   the other verdicts. This is the one lens with veto power because
   its three checks (activity isolation, no raw content, anti-mashing
   clamp) are the plan's non-negotiables, not a quality preference.
2. If `visual-verifier` reported **REFUTED** (a screenshot shows the
   milestone's visual criterion is genuinely not met): **Request
   changes**. A REFUTED visual verdict is a real defect.
3. If `visual-verifier` reported **BLOCKED** (no display, no
   screenshot tool, vision model down): that does **not** block merge —
   it is an environmental gap, not a code defect. Merge if the rule
   below passes, and record the unverified visual criterion explicitly
   in `docs/pr-log.md` so it stays visible as a carried gap rather than
   silently reading as verified.
4. Otherwise: **Approve and merge** if at least 2 of the 3 code
   reviewers (correctness, boundaries, tests) approved; **Request
   changes** if 2 or more requested changes.

## On approval

1. `gh pr review <n> --approve --body "..."` — this is the one formal
   GitHub review posted for this PR; summarize what each of the three
   reviewers verified.
2. `gh pr merge <n> --squash --delete-branch` — do not wait for a human
   to merge. This project runs with standing authorization to commit,
   push, open PRs, review, and merge without per-action confirmation;
   that authorization lives here (and originally in the v0.1 run prompt,
   now archived on branch `attic/legacy-rust-and-fleet`), not in any
   single conversation turn.
3. Append a row to `docs/pr-log.md`: PR number, milestone, each
   reviewer's individual verdict, final decision, merge commit SHA
   (`git rev-parse HEAD` on `main` after the merge, or the SHA `gh pr
   merge` reports).

## On request-changes

1. `gh pr review <n> --request-changes --body "..."` consolidating
   every reviewer's required fix into one list — game-engineer reads
   only this, not each reviewer's individual verdict, so nothing you
   omit here reaches them.
2. Do not merge. Append a row to `docs/pr-log.md` recording the
   decision and the consolidated fix list.
3. Leave the PR open on its existing branch — `game-engineer` fixes and
   re-hands-off to the reviewers on the same branch, not a new PR.
