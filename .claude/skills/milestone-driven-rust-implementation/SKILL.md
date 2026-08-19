---
name: milestone-driven-rust-implementation
description: |
  Discipline for implementing a dev-companion milestone from docs/implementation-plan.md: what to run after every milestone, how to debug a Bevy/ECS failure to root cause instead of working around it, and what the handoff log must contain. Use whenever implementing, fixing, or completing a milestone in this repository.
x-fleetsmith-origin: human
---

# Milestone-driven Rust/Bevy implementation

## After every milestone, in this order

1. `cargo fmt --all -- --check` — fix formatting, don't just note it.
2. `cargo clippy --workspace --all-targets -- -D warnings` — a clippy
   warning is a build failure here. Silence a specific lint only with an
   inline `#[allow(...)]` carrying a one-line reason; never a blanket
   crate-level allow.
3. `cargo test --workspace` — including the pure progression-math tests
   and the activity-provider mock tests. All green before moving on.
4. `cargo run -p companion` — manual smoke test against the milestone's
   stated exit criterion in the plan. Actually do it; don't infer it
   compiles therefore it works.
5. Append an entry to `docs/milestone-log.md`: milestone id, files
   changed, exact commands run, their results (paste real output, not a
   summary), and anything still broken or deferred.

## Debugging a Bevy/ECS failure

- A panic with a Bevy-internal frame almost always means a system
  ordering or missing-resource bug, not a Bevy bug — check `.add_systems`
  ordering and whether every `Res`/`ResMut`/`Query` param the panicking
  system asks for was actually inserted before this schedule ran.
- Run with `RUST_LOG=debug,wgpu=warn` and `RUST_BACKTRACE=1` before
  guessing.
- For "system reads stale data" bugs, check the schedule label
  (`Startup` vs `Update` vs `FixedUpdate`) before touching system logic —
  misplaced schedule is a much more common cause than logic errors.
- Enable the `dynamic_linking` Bevy feature in the dev profile only
  (never release) to keep the compile-run-check loop fast enough to
  actually do this iteratively.
- When a query returns nothing unexpectedly, the fix is almost always a
  missing component on the entity or an overly narrow `With<T>`/
  `Without<T>` filter — print `query.iter().count()` before assuming the
  system logic itself is wrong.

## What "done" means for a milestone

A milestone is done when steps 1-4 above all pass AND the plan's stated
exit criterion is demonstrated, not when the code compiles. If a
milestone can't be finished as scoped, say so explicitly in the log
rather than silently shipping a partial version of it under the same
milestone name.

## Git and PR workflow — every milestone gets its own PR

1. Branch from `main`: `git checkout -b milestone/m<N>-<slug>` (e.g.
   `milestone/m0-workspace-scaffold`).
2. Commit as you go, not one giant commit at the end — a commit per
   coherent step (e.g. "scaffold activity crate", "wire DefaultPlugins
   window") makes the PR diff and any later `git bisect` legible.
   Author as the current git identity (`git config user.name/email`) —
   do not override it.
3. After the milestone passes steps 1-4 above and its log entry is
   appended: `git push -u origin milestone/m<N>-<slug>`, then
   `gh pr create --base main --title "M<N>: <milestone name>" --body`
   with a body that links the exact `docs/milestone-log.md` entry and
   states the exit criterion demonstrated.
4. Do not merge your own PR. Hand off to the three reviewers
   (`pr-reviewer-correctness`, `pr-reviewer-boundaries`,
   `pr-reviewer-tests`) and stop — do not start the next milestone's
   branch until `pr-merge-decider` has merged the current PR (check
   `gh pr view <n> --json state`), so two milestones are never in
   flight as unreviewed branches at once.
5. If `pr-merge-decider` comes back with requested changes, fix them on
   the same branch, push, and hand off to the reviewers again — do not
   open a second PR for the same milestone.
