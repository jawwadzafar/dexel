<!--
Thanks for contributing to Dexel! Please fill this out so a reviewer can trust
your "done" without re-deriving it. See CONTRIBUTING.md for the full details.
-->

## What this changes

<!-- A short description of the change and why. Link any related issue: Closes #123 -->

## How I verified it

<!-- Commands you ran, and — for any visual/UX change — how you saw it in the
     REAL running app (built the binary, ran it with the fake provider, looked). -->

## Checklist

**Gates (run locally — Actions is account-blocked for this repo):**

- [ ] `cd app && go vet ./...` is clean
- [ ] `bash scripts/test-race.sh` passes
- [ ] `cd app/frontend && npm run typecheck && npm run build` succeeds
- [ ] **No bundle drift** — `git diff --exit-code -- app/public/js/dexel.js app/public/js/dexel.js.map` is clean

**Boundaries:**

- [ ] **Privacy invariant respected** — no new field on the activity `Snapshot`,
      the WebSocket state, or the save types carries (or could carry) raw
      content; the structural allow-list test still passes
- [ ] **Honest mechanics** — a signal the platform can't see freezes rather than
      guessing; no fabricated activity
- [ ] Any behaviour change updates `docs/game/`, and lasting decisions get an ADR
- [ ] Art/sound changes go through the generators (`tools/gen_*.py`) — no
      hand-edited PNGs/WAVs
- [ ] Visual/UX changes were verified in the real running app

**Repo policy:**

- [ ] Commits carry the maintainer as sole author — **no `Co-Authored-By: Claude`
      trailer**
- [ ] Docs updated where relevant
