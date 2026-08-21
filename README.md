# dev-companion

A cozy pixel-art desktop companion whose workday runs on *your* real typing.

![dev companion running with a live sprint, terminal, and status line](docs/images/hero.png)

## What is this

`dev-companion` is a small pixel-art developer character who lives at a desk
on your screen. Real typing — in **any** app on your machine, not just the
game window — advances their current sprint. Finishing a sprint pays out
Dev Cash, which you spend in a store on hoodies, chairs, keyboards, mice,
plants, and desk decor for their corner of the screen. Items are bought
once and equipped any time ("own many, equip one" per slot).

There are no meters, no hunger, no decay, and nothing to feed or lose. It's
pure idle: leave it running, keep working, watch the desk fill in.

It ships today as a small local web app: a Go backend samples your real
input and serves an HTML/[NES.css](https://nostalgic-css.github.io/NES.css/)
frontend over a WebSocket at `http://127.0.0.1:8080`.

## Features

- **Sprints -> Dev Cash -> store.** A visible sprint bar advances from real
  typing; completed sprints pay Dev Cash you spend on cosmetics that
  actually render on the character and desk.
- **Honest moods.** `Coding` only shows after a recent keystroke — mouse
  motion alone never triggers it. `On break` only comes from genuine global
  idleness; if the platform can't see globally, the idle clock freezes
  instead of guessing.
- **Activity analytics ([A]).** Today's and lifetime keystrokes, mouse-active
  seconds, active/idle time, sprints, focus sessions, and app switches, plus
  a per-signal Dev Cash breakdown (how many coins each signal actually
  earned today).
- **30-day history ([H]).** A CSS bar chart of the last 7 days, a 30-day
  streak strip, current/longest streak, and two honest insights — busiest
  day and longest focus block — derived only from counts.
- **Content-free by construction.** See [Privacy](#privacy) below.

## Screenshots

| Store | History |
|---|---|
| ![Store modal: categories, catalog grid, buy/equip, live preview](docs/images/store.png) | ![History modal: 7-day bar chart, 30-day streak strip, insights](docs/images/history.png) |

## Quick start

Requires [Go 1.27](https://go.dev/dl/) or newer.

```bash
git clone git@github.com:jawwadzafar/dev-companion.git
cd dev-companion/app
go run .
```

Open **http://localhost:8080**. That's it — the compiled frontend bundle is
committed, so no Node/npm is required just to run the game.

By default the server binds to `127.0.0.1:8080` (loopback only). Binding
`-addr` beyond `127.0.0.1`/`localhost` exposes the activity monitor and your
save file to your LAN or tailnet — the flag's own help text warns about
this; leave it at the default unless you specifically intend that.

### macOS

- **No permission prompt.** The activity provider polls
  `CGEventSourceSecondsSinceLastEventType` — a system timestamp delta, not a
  keystroke tap — so there is no Accessibility dialog to click through.

### Linux

Same commands. The activity provider reads raw `/dev/input/event*` nodes,
which needs your user in the `input` group:

```bash
sudo usermod -aG input "$USER"    # then log out and back in
```

Without that group membership the server still runs; it just can't see
global input, so activity only counts while the browser tab itself has
focus, and the honesty rules above freeze rather than guess at idle time.

### Troubleshooting

- **`ASSETS NOT FOUND` banner in the scene:** the server couldn't locate the
  repo's `assets/` directory (the sprite PNGs). This happens if the binary
  is run from somewhere other than a full checkout. Set
  `DEVCOMPANION_ASSETS_DIR=/path/to/dev-companion/assets`, or run from
  `app/` inside the checkout as shown above.
- **Check what the server actually found:** `GET /api/health` returns the
  resolved `assetsDir`, whether `public/index.html` was found, and the
  build version.

## Controls

| Key / control | Action |
|---|---|
| `S` or the `[S] STORE` title-bar button | Open the store |
| `A` or the `[A] ACTIVITY` title-bar button | Open the activity log |
| `H` or the `[H] HISTORY` title-bar button | Open the 30-day history |
| `Esc` | Close whichever modal is open |

## How it works / Architecture

```
activity provider (darwin / linux)
        |  counts + booleans only
        v
engine (anti-mashing economy, honest moods)
        |
        v
game (sprints, store, save/load)
        |
        v
WebSocket state broadcast (~1 Hz)
        |
        v
HTML/CSS/TS frontend (app/public/)
```

The current, actively developed stack is Go + TypeScript:

```
app/
  main.go, handlers.go, hub.go        # HTTP + WebSocket server, single-owner game loop
  provider_select_{darwin,linux,other}.go
  internal/
    activity/    # per-OS activity providers; the privacy boundary lives here
    engine/      # economy + honesty rules (moods, anti-mashing, coin pricing)
    game/        # sprints, catalog, store gate, save/load state shape
    store/       # save file read/write, schema migrations
    assets/      # locates the repo's assets/ directory at runtime
  frontend/      # TypeScript source, built with esbuild
    src/
      wire.ts            # typed mirror of the WS contract
      state/              # typed store + WS client
      render/             # scene/terminal/chrome/overlays/flash (no actions sent)
      features/           # store-modal, activity-modal, history-modal, keybindings
      dev/                 # ?dev=1 harness (hardcoded catalog + state, no backend needed)
  public/         # what the Go server actually serves (index.html, css, js, fonts)
```

Every sprite in `assets/` is generated by committed code
(`tools/gen_assets.py`) from a fixed 18-colour palette rather than hand-drawn
binaries, so any sprite is a reviewable diff:

```bash
python3 tools/gen_assets.py   # regenerates all sprite/thumbnail PNGs
```

Full specs live in `docs/` (`docs/ui-spec.md`, `docs/art-direction.md`,
`docs/upgrade-design.md`); the decisions behind the design, and why, live in
[`docs/adr/`](docs/adr/README.md).

### Legacy: the Rust/Bevy implementation

`companion/` and `activity/` at the repo root (the Rust crates — not
`app/internal/activity/`), plus `Cargo.toml`/`Cargo.lock` and `target/`, are
the **original** implementation of this game in Rust + Bevy. It is frozen
legacy: kept in the tree and buildable (CI still builds it), but it is not
the product and receives no new feature work. The project pivoted to the Go
+ HTML/NES.css stack in `app/` for verifiability (a headless browser on any
machine sees exactly what a user sees) and portability. See
[ADR 0011](docs/adr/0011-engine-pivot-to-pdf-native-stack.md) for the full
reasoning. If you're new here: **`app/` is the thing you run.**

## Privacy

This is the project's defining constraint, not a footnote:

- **Counts and durations only, never content.** The activity layer records
  *that* a key was pressed and *that* the mouse moved — never which key,
  never any typed text, never clipboard contents.
- **App identity only, never window titles.** The foreground-app signal is
  an app name (e.g. "Visual Studio Code") — never a window title, document
  name, or URL ([ADR 0009](docs/adr/0009-app-identity-not-titles.md)).
- **Enforced structurally, not just promised.** A reflect-based allow-list
  test fails the build if anyone ever adds a field to the activity
  `Snapshot`/state/save types that isn't on an explicit allow-list, or whose
  name even smells like content (`title`, `text`, `clipboard`, `keycode`,
  ...). See [ADR 0002](docs/adr/0002-activity-isolation-and-privacy.md) and
  [ADR 0009](docs/adr/0009-app-identity-not-titles.md).
- **Copy/paste detection was deliberately dropped on macOS.** Distinguishing
  a copy/paste chord from any other keystroke would require the
  `CGEventTap` API, which requires the Accessibility permission this
  project's whole design exists to avoid. It's deferred behind a future,
  explicit, opt-in permission phase rather than shipped by default. See
  [ADR 0012](docs/adr/0012-a2-content-free-signal-set-and-permission-fork.md).
- **100% local.** Everything runs on your machine talking to itself over
  `localhost`; nothing phones out.

## Configuration

### Flags

| Flag | Default | Description |
|---|---|---|
| `-addr` | `127.0.0.1:8080` | Listen address. Binding beyond `127.0.0.1`/`localhost` exposes the activity monitor and save to your LAN/tailnet. |
| `-public` | `./public` | Static frontend directory. |
| `-provider` | `auto` | Activity provider: `auto` (native provider for this OS) or `fake`. |
| `-fake-script` | `""` | Explicit fake-provider script, e.g. `type:20s,idle:40s,mouse:15s`. Overrides `DEVCOMPANION_FAKE_SCRIPT` and implies `-provider=fake`. |
| `-insecure-origin` | `false` | Accept WebSocket connections from **any** Origin, skipping same-origin verification. For embedded webviews only (e.g. a `file://`/`app://` frontend); never combine with an `-addr` bound beyond loopback. |

### Environment variables

| Variable | Purpose |
|---|---|
| `DEVCOMPANION_FAKE_SCRIPT` | Same script format as `-fake-script`, used when `-provider=fake` and no `-fake-script` flag is given. |
| `DEVCOMPANION_ASSETS_DIR` | Overrides where the server looks for the repo's `assets/` directory. |

### Health endpoint

`GET /api/health` returns the resolved `assetsDir` (or `null`), whether
`public/index.html` was found (`publicOk`), and the build `version` — useful
for diagnosing "it looks broken" reports without guessing.

### Save file

State persists to `~/.config/devcompanion/state.json`: Dev Cash, XP, current
sprint progress, owned/equipped items and tints, and the activity
counters/history described above. The save schema is versioned; migrations
are additive and non-destructive, and a save from a *future* schema version
is refused outright rather than silently downgraded.

## Development

```bash
cd app
go test ./...
go vet ./...
go run .
```

**Frontend build** (only needed when changing the frontend — the compiled
bundle is committed so `go run .` alone always serves a working game):

```bash
cd app/frontend
npm ci
npm run build       # bundles + minifies src/main.ts -> ../public/js/game.js
npm run typecheck   # tsc --noEmit, strict
```

See [`app/frontend/README.md`](app/frontend/README.md) for the layer
breakdown (state/render/features) and why the built bundle is committed
alongside its TypeScript source.

**Scripted activity, no real input needed:**

```bash
go run . -fake-script "type:20s,idle:40s,mouse:15s"
```

**Frontend dev harness:** `http://localhost:8080/?dev=1` loads a hardcoded
catalog + state client-side, for iterating on the store/history/activity
modals without a live backend connection.

**Project layout:** see [How it works / Architecture](#how-it-works--architecture)
above. Design specs live in `docs/` (`docs/ui-spec.md`,
`docs/art-direction.md`, `docs/upgrade-design.md`); the reasoning behind
each major decision is recorded as an ADR in
[`docs/adr/`](docs/adr/README.md).

**CI:** `.github/workflows/build.yml` runs three jobs on push/PR — the
legacy Rust build, `go vet` + `go test -race` for `app/`, and a frontend job
that runs `npm run build` fresh and diffs it against the committed bundle so
the two can never silently drift. This repository is private, so the
workflow runs on a self-hosted runner and its badge/checks won't render for
non-collaborators.

## Roadmap

Tracked in [`docs/plan/ROADMAP.md`](docs/plan/ROADMAP.md). Current state:

- **Analytics track (A1/A2/A3): complete.** Activity log foundation, priced
  content-free signals with a per-signal coin breakdown, and 30-day
  history/streaks are all shipped.
- **Frontend architecture (F1 build+TypeScript, F2 modular layers): done.**
  F3 — wrapping the same frontend + Go backend in a native desktop shell via
  [Tauri](https://tauri.app/) — is planned but not scheduled.
- **Art track: parked** at its current procedural-pixel-art fidelity by
  deliberate decision, in favor of shipping features; see the roadmap for
  the reasoning and the routes considered for a future revisit.

## License

No `LICENSE` file exists in this repository yet, so **there is currently no
license for the code in `app/`** (the active Go/TypeScript product). The
root `Cargo.toml` declares `MIT OR Apache-2.0` as SPDX metadata for the
frozen legacy Rust crates, but no corresponding `LICENSE-MIT`/`LICENSE-APACHE`
text is checked in either. Treat this repository as **all rights reserved**
until a `LICENSE` file is added.
