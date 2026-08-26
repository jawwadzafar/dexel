# Contributing to Dexel

Thanks for your interest in Dexel — a cozy pixel-art desktop companion whose
workday runs on your real typing. This guide covers how to build it, how to run
it locally, and the boundaries every change is judged against.

> **Status:** the repository is still private. Pull requests are welcome once it
> is public; until then, issues and ideas are the most useful contribution.

Please also read the [Code of Conduct](CODE_OF_CONDUCT.md) — by participating you
agree to uphold it — and, for anything security-sensitive, [SECURITY.md](SECURITY.md).

## The one thing to internalize first: privacy is a hard boundary

Dexel records **counts and durations only, never content.** The activity layer
records *that* a key was pressed and *that* the mouse moved — never which key,
never any typed text, never clipboard contents, never window titles or URLs
(only a mapped application display name). This is not a guideline; it is
[enforced structurally by tests that fail the build](docs/adr/0002-activity-isolation-and-privacy.md).

A reflect-based allow-list test rejects any new field on the activity
`Snapshot`, the WebSocket state, or the save types that isn't on an explicit
allow-list, or whose name even *smells* like content (`title`, `text`,
`clipboard`, `keycode`, …). **If your feature needs raw content, the answer is a
different feature.** See [ADR 0002](docs/adr/0002-activity-isolation-and-privacy.md),
[ADR 0009](docs/adr/0009-app-identity-not-titles.md), and
[ADR 0012](docs/adr/0012-a2-content-free-signal-set-and-permission-fork.md).

## Project layout

`app/` is the thing you run — a Go server + WebSocket + a committed TypeScript
bundle, shipped as one binary (`app/embed.go` compiles `app/public/` and
`app/assets/` in). A Tauri v2 desktop shell lives in `desktop/`.

```
app/
  main.go, handlers.go, hub.go   # HTTP + WebSocket server, single-owner game loop
  cli.go, cmd_*.go               # the dexel CLI + background runtime
  embed.go                       # go:embed of public/ and assets/ into the binary
  internal/
    activity/  # per-OS activity providers; the privacy boundary lives here
    engine/    # economy + honesty rules (moods, anti-mashing, coin pricing)
    game/      # sprints, sessions, catalog, store gate, save/load state shape
    store/     # save file read/write, schema migrations (SQLite state.db)
  frontend/    # TypeScript source, built with esbuild (framework-free)
  public/      # frontend the Go server embeds and serves
  assets/      # sprite PNGs the Go server embeds and serves at /assets/
tools/         # gen_assets.py, gen_sounds.py, gen_icon.py — asset generators
docs/          # see docs/README.md — the four documentation layers
```

Start with [`docs/README.md`](docs/README.md): it indexes the four
documentation layers — `docs/game/` (how the game works today),
`docs/adr/` (why each decision was made), `docs/plan/ROADMAP.md` (what's next),
and the normative specs (`docs/ui-spec.md`, `docs/art-direction.md`,
`docs/upgrade-design.md`).

## Building and running locally

You need **Go 1.27+**. Node/npm are **not** needed to run the game — only to
change the frontend, because the compiled bundle (`app/public/js/dexel.js`) is
committed and embedded at build time.

```bash
cd app
go build -o dexel .        # Windows: go build -o dexel.exe .
go run . serve             # foreground dev server on 127.0.0.1:8080
```

`go run . serve` is the foreground developer loop — output straight to your
terminal, no background runtime or lock file. Bare `go run .` behaves like the
installed CLI (starts the background runtime and opens a browser); use `serve`
while iterating.

**Run with scripted activity — no real input needed.** The fake provider is how
you develop and how the visual gate is exercised:

```bash
go run . serve -fake-script "type:20s,idle:40s,mouse:15s"
```

**Frontend dev harness:** `http://localhost:8080/?dev=1` loads a hardcoded
catalog + state client-side, for iterating on the modals without a live backend.

### Frontend changes

Only needed when changing the frontend. Rebuild the committed bundle and commit
it alongside your TypeScript source:

```bash
cd app/frontend
npm ci
npm run typecheck   # tsc --noEmit, strict
npm run build       # bundles + minifies src/main.ts -> ../public/js/dexel.js
```

See [`app/frontend/README.md`](app/frontend/README.md) for the layer breakdown
(state / render / features) and why the built bundle is committed.

### Art and sound changes

**Assets are generated, never hand-edited.** Every sprite in `app/assets/` and
every sound is produced by committed code so it is a reviewable diff, not an
opaque binary:

```bash
python3 tools/gen_assets.py   # regenerates all sprite/thumbnail PNGs
python3 tools/gen_sounds.py   # regenerates sound assets
```

Change the generator and regenerate — **a hand-edited PNG or WAV is a diff
nobody can review** and will be rejected. Art follows the palette and rules in
[`docs/art-direction.md`](docs/art-direction.md).

## The gates — run all of them before opening a PR

GitHub Actions is blocked at the account level for this repository, so a green
pipeline is **not** something you can lean on. Run the gates locally:

```bash
# from the repo root
(cd app && go vet ./...)
bash scripts/test-race.sh
(cd app/frontend && npm run typecheck && npm run build)
git diff --exit-code -- app/public/js/dexel.js app/public/js/dexel.js.map   # no bundle drift
```

The last command is the **no-bundle-drift** check: the committed
`dexel.js`/`dexel.js.map` must match a fresh `npm run build`, so the shipped
bundle can never silently diverge from its source.

## What a PR is judged on

Beyond the gates passing, three things:

- **The privacy boundary is not negotiable** (see the top of this file). A new
  field on the activity `Snapshot`, the WebSocket state, or the save types must
  pass the structural allow-list test.
- **Honest mechanics** ([ADR 0010](docs/adr/0010-mac-first-honest-mechanics.md)).
  Earning reflects real work; the anti-mash clamp holds; a signal the platform
  cannot actually see **freezes** — it never guesses and never fabricates.
- **Visual/UX changes are verified in the real running game.** No visual change
  is done until it is rendered in the actual binary (build it, run it with the
  fake provider, look at it). Isolated mockups have misled this project before.

## Documentation and code style

- **Behaviour changes update [`docs/game/`](docs/game/README.md)** in the same
  commit, and a decision with lasting consequences gets an ADR in
  [`docs/adr/`](docs/adr/README.md).
- **Go:** idiomatic Go; `go vet` clean; the server is the single source of
  truth for game state — the client never asserts state the server didn't send.
- **TypeScript:** framework-free, `tsc --strict` clean; keep the render layer
  free of action-sending, and mirror the WebSocket contract in `wire.ts`.

## Commit and PR conventions

- Keep commits focused; write a clear, imperative subject line.
- **Do not add a `Co-Authored-By: Claude ...` trailer** — this repository's
  commits carry the maintainer as the sole author.
- Fill out the [pull request template](.github/PULL_REQUEST_TEMPLATE.md) — the
  checklist mirrors the gates and boundaries above.

## Questions

Open an issue using one of the [templates](.github/ISSUE_TEMPLATE/), and see
[`docs/README.md`](docs/README.md) for where each kind of question is answered.
