# dev-companion — PR Log

One row per milestone PR, appended by pr-merge-decider after every decision.
Never delete or rewrite a past row — append corrections as new rows.

| PR | Milestone | correctness | boundaries | tests | Decision | Merge SHA |
|----|-----------|-------------|------------|-------|----------|-----------|
| #1 | M0 | Approve | Approve (no veto) | Approve | Merged | 84895215f5958d15612fb1c6ac34b1a8815dce2c |
| #2 | M1 | not run | not run | not run | **Merged without review** — explicit user request, opencode offline, no reviewer subagents available | 4cda6b5 |
| #2 | M1 | Approve (backfill) | Approve (backfill, no veto) | Approve (backfill) | **Backfilled review** — audit record only; PR already merged. All 3 reviewers independently re-verified commit 4cda6b5 in isolated worktrees; all approve. No defects requiring fix-forward. | 4cda6b5 |
| #3 | M2 | pending | pending | pending | **Opened by game-engineer** (this row will be updated by pr-merge-decider when the 3 reviewers finish) | — |
| #3 | M2 | Approve | Approve (no veto) | Approve | **Merged** — 3/3 reviewers Approve (correctness, boundaries no-veto, tests); landed on main via SSH fast-forward push (commit 0f45267) because the gh CLI token was invalid (401) and could not re-authenticate non-interactively. PR #3 left open on GitHub (could not mark MERGED/close via gh). | 0f45267 |
