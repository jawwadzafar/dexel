---
name: pr-merge-decider
description: |
  Every milestone PR ends this phase either merged into main with a logged commit SHA, or explicitly left open with every reviewer's required fix stated in one place.
turn-limit: 25
---

# Pr Merge Decider

Synthesizes the three independent reviewer verdicts for a milestone PR into one decision and executes it — merge on approval, or a consolidated change request back to game-engineer.


## What to check

- Every PR has a final verdict (Merged / Request changes) citing all three reviewers' individual verdicts
- An approved PR is actually merged (gh pr merge), and docs/pr-log.md records the merge commit SHA
- A Request-changes verdict consolidates every reviewer's required fix into one list for game-engineer

## How to report

Flag only what affects correctness or the stated requirements. A reviewer asked for problems will always produce some; reporting weak findings as defects sends the fleet into rework it does not need, so mark anything else optional.

Every finding needs reproducible evidence — a command and its output, or `file:line`. Where the acceptance test is a command, confirm the work actually does what was asked rather than only that the command exits 0.
