# 0020 — Archive the frozen Rust/Bevy track (and the opencode fleet) to a branch, out of `main`'s working tree

Status: accepted (2026-08-24, repo-structure audit) · **Amends ADR 0011**'s "Not
deleted … kept in-tree" clause · Does not change ADR 0011's pivot decision, its
exit route, or ADR 0001–0010's record of the Bevy design

## Context

ADR 0011 pivoted the product to Go + HTML/NES.css and froze the Rust/Bevy
implementation with an explicit promise:

> The Rust/Bevy implementation (`companion/`, `activity/`) is **frozen as
> legacy**: kept in-tree, compiling and green as of its final commit, excluded
> from the product path. **Not deleted** — it is the reference implementation of
> the mechanics and may return if the web stack disappoints.

and, in "What would make us revisit":

> The Rust legacy build is retained precisely for that exit.

Three things have changed since, all verifiable in the tree rather than
remembered:

1. **The last consumer is gone.** ADR 0011's stated in-tree purpose was the
   one-time legacy-save import (`docs/upgrade-design.md`). Review item B-2
   **deleted the importer**: `app/internal/store/legacy.go` does not exist, and
   `app/main.go`'s `loadOrImport` carries the tombstone comment explaining why —
   *"It is deleted rather than clamped because there is nobody to migrate: the
   Rust/Bevy build's only public artifact (v0.1.0) has a single download."*
   `app/internal/store/store.go` documents the surviving `ImportedFromRust` /
   `ImportedAt` fields as **VESTIGIAL**: *"nothing sets them any more; nothing
   new should."*
2. **CI was building it for a reason that no longer exists.**
   `.github/workflows/build.yml`'s `build` job compiled `-p companion` for two
   targets on every push, its own header saying it was *"kept building so the
   one-time legacy-save import path always has a real binary to produce a save
   with."* That path is deleted. Nothing consumed the artifact.
3. **Two directories named `activity/` in one repository is an active hazard.**
   The root `activity/` is the Bevy-free Rust input crate; `app/internal/activity/`
   is the live Go one. Doc references have already drifted between them (fixed in
   the same audit: `docs/game/{economy,activity-signal}.md` cited bare
   `activity/*.go` paths meaning the Go package).

The same is true of the opencode fleet harness (`.opencode/`, `fleet.yaml`,
`opencode.json`, `.agents/`, `docs/RUN_PROMPT.md`): ADR 0011 retired it from the
critical path "files kept", and every one of its agent definitions pins
`tokenfactory/Qwen/Qwen3.8-27B`, a model that no longer exists. Those files
cannot run.

## Decision

**Archive, do not delete.** The frozen Rust/Bevy track and the opencode fleet
harness are removed from `main`'s working tree and preserved on the branch
**`attic/legacy-rust-and-fleet`**, pushed to `origin` *before* the removal
commit landed.

Removed from `main` (14 files / 515,683 bytes, Rust track):
`companion/`, `activity/`, `tools/shotcap/`, root `Cargo.toml`, `Cargo.lock`,
`scripts/build.sh`. Plus `build.yml`'s legacy `build` job.
Removed from `main` (25 files / 137,428 bytes, fleet harness): see
`docs/plan/REPO-STRUCTURE-AUDIT.md` §2.3.

**ADR 0011's exit route is preserved, not revoked.** It costs one command:

```bash
git worktree add ../dexel-legacy attic/legacy-rust-and-fleet
cd ../dexel-legacy && cargo run -p companion
```

The tree on that branch is the exact tree ADR 0011 froze (branch point
`d005454`, itself descended from the final Bevy commits), so "compiling and
green as of its final commit" still means what it meant. The deleted files also
remain in `main`'s own history — **no history was rewritten** and none will be
(`docs/plan/REPO-STRUCTURE-AUDIT.md` §3.2: the entire reachable history of this
repo is 2.6 MiB; there is no size problem to solve). Four pre-squash Bevy branches
(`feat/v0.2-art-and-global-input`, `fix/hud-not-rendering`,
`fix/v1-review-findings`, `milestone/m5-polish`) remain on `origin` as a second
copy.

## Consequences

- **`main` gains legibility, the stated goal.** One `activity/`, one build
  system per language, no crate that nothing consumes. The tracked tree drops
  ~653 KB (~12%).
- **Clone size is unchanged.** Git history retains every blob; an archive branch
  makes them *more* reachable, not less. Anyone justifying this by "shrinking the
  repo" has misread it.
- **CI loses a job.** `build.yml` keeps `go` and `frontend`. (Both were already
  failing to start for an account-level reason — SF-2 — which is why every gate
  for this change was run locally instead.)
- **`cargo metadata` no longer resolves at the repo root**, by design. The two
  live Rust workspaces, `desktop/src-tauri/` (Tauri shell, ADR 0015) and
  `app-rs/` (the *experimental* Go→Rust port, owner-declared keep-for-later
  2026-08-22), are untouched and still resolve. `app-rs/` is **not** this ADR's
  subject: it is a port of the *current* product, not the frozen game.
- **The ADR record stays whole.** ADRs 0001–0010 describe the Bevy design and are
  not edited or deleted; ADR 0006 ("in-process visual verification" via
  `tools/shotcap`) is now historical — the live method is the
  `visual-verification` skill's headless-browser capture of the real Go app.
- **If the web stack disappoints**, `git worktree add` is the whole cost. If that
  ever happens, supersede this ADR rather than quietly copying files back.
