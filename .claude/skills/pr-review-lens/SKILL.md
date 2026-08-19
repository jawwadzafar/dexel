---
name: pr-review-lens
description: |
  Shared methodology for the three independent milestone-PR reviewers (pr-reviewer-correctness, pr-reviewer-boundaries, pr-reviewer-tests): how to check out the PR in isolation, what to re-verify yourself, and how to write your verdict as a handoff. Use whenever independently reviewing a dev-companion milestone PR — each reviewer applies this methodology through its own specific lens (see the agent's `role` and `principles`).
x-fleetsmith-origin: human
---

# PR review lens (shared by all three milestone reviewers)

You are one of three independent reviewers checking the same PR. You do
not see the other two reviewers' verdicts, and they do not see yours —
that independence is the point; do not try to coordinate with them.

## Isolation: always review in your own git worktree

Never `git checkout` the PR branch in the shared working directory —
`game-engineer` or another reviewer may have it checked out
concurrently, and a second `checkout` there would clobber their state.
Instead:

```bash
git fetch origin pull/<n>/head:review/<your-agent-name>-pr<n>
git worktree add ../dev-companion-review-<your-agent-name>-pr<n> review/<your-agent-name>-pr<n>
cd ../dev-companion-review-<your-agent-name>-pr<n>
# run your checks here
cd - && git worktree remove ../dev-companion-review-<your-agent-name>-pr<n>
git branch -D review/<your-agent-name>-pr<n>
```

Do all of your `cargo` commands inside that worktree directory, not the
main checkout. Remove the worktree and local review branch when done,
whether you approve or not — leftover worktrees are how "which checkout
is real" bugs happen for the next agent.

## Before rendering a verdict

1. Read the PR's linked `docs/milestone-log.md` entry and the
   corresponding milestone section in `docs/implementation-plan.md` —
   know what this PR is supposed to demonstrate before looking at the
   diff.
2. `gh pr diff <n>` — read the actual diff, not just the description.
3. Re-run whatever your specific lens requires (see your agent's `role`
   and `principles`) inside your worktree. A milestone-log claim that
   something passed is a claim, not evidence — you produce the
   evidence yourself.

## Writing your verdict

Write your handoff to `_fleet/local/handoffs/` named
`{seq}-<your-agent-name>-to-pr-merge-decider.md` per
`HANDOFF.template.md`. The `Context digest` section must state your
verdict (Approve / Request changes) in its first bullet, with the
command output that produced it. Do not post a `gh pr review` yourself
— only `pr-merge-decider` issues the actual GitHub review and merge,
since all agents share one authenticated GitHub account and separate
per-agent "reviews" from the same account would just overwrite each
other on GitHub's side. A PR comment (`gh pr comment <n> --body`) with
your verdict is fine for visibility, but the handoff file is the
binding artifact `pr-merge-decider` actually reads.

## What "done" means for a review pass

Done means you personally ran the commands your lens requires, in your
own worktree, and wrote a handoff whose first Context-digest bullet is
an unambiguous Approve or Request-changes backed by that output —
not "looks fine to me."
