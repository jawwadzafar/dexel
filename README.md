# dexel

A cozy pixel-art desktop companion whose workday runs on *your* real typing.

![dexel running with a live sprint, terminal, and status line](docs/images/hero.png)

## What is this

`dexel` is a small pixel-art developer character who lives at a desk
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

Requires [Go 1.27](https://go.dev/dl/) or newer. Node/npm are **not**
needed to run the game — only if you're changing the frontend (see
[Building from source](#building-from-source) below).

```bash
git clone git@github.com:jawwadzafar/dexel.git
cd dexel/app
go build -o dexel .
./dexel
```

`./dexel` with no arguments starts dexel's background runtime if one isn't
already running, then opens the game in your browser — no port to
remember, no terminal to leave open. Run it again any time to reopen the
window. `./dexel status` reports whether it's running (pid, url, uptime);
`./dexel stop` shuts it down cleanly, saving on the way out — closing the
browser tab does **not** stop it.

State (`state.db`, `config.json`) and the runtime's own bookkeeping
(`runtime.json`, `runtime.lock`, logs) live under `~/.config/dexel`
(`$DEXEL_HOME` if set, or the platform default on macOS/Windows);
`./dexel status` prints the exact paths.

Iterating on the Go source, or want the classic foreground server on a
fixed port? See [Development](#development) below for `go run . serve`.

Binding `-addr` beyond `127.0.0.1`/`localhost` (relevant to `serve`/legacy
flag-style invocations) exposes the activity monitor and your save file to
your LAN or tailnet — the flag's own help text warns about this; leave it
at the default unless you specifically intend that.

macOS and Linux both run the exact commands above; see
[Building from source](#building-from-source) for platform setup (Linux
needs one group membership), what to expect on Windows, building a
standalone binary, and cross-compiling.

### Troubleshooting

- **`ASSETS NOT FOUND` banner in the scene:** the binary embeds its own
  sprite PNGs and frontend, so this shouldn't happen on a normal `go run .`
  / `go build`. It means either `-public` or `DEXEL_ASSETS_DIR` was set to a
  directory that doesn't actually hold the app's assets, or the binary was
  built without its embedded copy. Drop the override to fall back to the
  embedded assets, or point it at a real `app/public` / `app/assets`
  checkout.
- **Check what the server actually found:** `GET /api/health` returns the
  resolved `assetsDir` (`null` means "serving the embedded copy"), whether
  `public/index.html` was found, and the build version.

## Building from source

### Prerequisites

| Need | Requirement |
|---|---|
| Build or run the server | [Go 1.27](https://go.dev/dl/) or newer |
| Change the frontend | Node + npm (see [`app/frontend/README.md`](app/frontend/README.md)) |

The compiled frontend bundle (`app/public/js/dexel.js`) is committed to the
repo, and the whole `app/public/` + `app/assets/` tree is embedded into the
Go binary at build time (`go build`/`go run .` alone always produce a
working game with zero Node/npm involved). Node is only needed if you edit
`app/frontend/src/*.ts` and want to rebuild that bundle — see
[Development](#development) below.

### Build a binary

From `app/`:

```bash
cd app
go build -o dexel .        # Windows: go build -o dexel.exe .
./dexel                    # Windows: dexel.exe
```

`./dexel` starts the background runtime and opens your browser (see
[Quick start](#quick-start)). For the foreground developer server instead
— fixed `127.0.0.1:8080`, no background process, no `runtime.json`/lock —
run `./dexel serve` (or `go run . serve` straight from source); both
accept the same flags, documented in [Configuration](#configuration).

### Platform support for activity capture

The server **runs on every platform Go supports**; what differs is
whether it can see your real typing/mouse activity *globally*, which is
what advances sprints and drives the honest mood/idle logic:

| Platform | Global activity capture | Setup |
|---|---|---|
| macOS | Yes — permissionless | None. Polls `CGEventSourceSecondsSinceLastEventType` (a system timestamp delta, not a keystroke tap), so there's no Accessibility dialog to click through. |
| Linux | Yes — needs one group membership | Add your user to the `input` group (below); it reads raw `/dev/input/event*` nodes. |
| Windows (and any other OS) | **No native provider yet** | None available. See below. |

**Linux:** grant access to the raw input devices, then log out and back
in:

```bash
sudo usermod -aG input "$USER"    # then log out and back in
```

Without that group membership the server still runs; it just can't see
global input, so activity only counts while the browser tab itself has
focus, and the honesty rules above freeze rather than guess at idle time.

**Windows — be aware of this before you rely on it:** dexel builds and
runs on Windows and the web UI works fully, but there is currently **no
native global activity provider** for Windows. It falls back to a
permanently-blind, zero-signal provider by deliberate design (ADR 0010's
honesty rules would rather freeze than fabricate activity — see
[ADR 0010](docs/adr/0010-mac-first-honest-mechanics.md) and
[ADR 0011](docs/adr/0011-engine-pivot-to-pdf-native-stack.md)). Concretely:
sprints will **not** advance from real global typing on Windows yet — this
is stated plainly here so nobody mistakes it for a bug. A native Windows
provider is future work, not yet started.

### Cross-compiling

Go can cross-compile a binary for another OS/architecture from any host,
e.g. building a Windows binary from macOS or Linux:

```bash
GOOS=windows GOARCH=amd64 go build -o dexel.exe .
GOOS=darwin  GOARCH=arm64 go build -o dexel .
GOOS=linux   GOARCH=amd64 go build -o dexel .
```

The caveat: activity capture is OS-native code (see the table above), so a
cross-built binary only ever gets the provider its *target* OS has — a
Windows binary cross-built from a Mac still has no native activity
provider, because none exists yet for Windows.

### Packaged desktop app (planned, not shipped)

A native desktop build via [Tauri](https://tauri.app/) — targeting macOS,
Windows, and Linux, x86_64 + arm64 — is planned so users won't need a
terminal or a browser tab at all. It's tracked as F3 in
[`docs/plan/ROADMAP.md`](docs/plan/ROADMAP.md) and is **not shipped yet**;
today, running from source (`go run .`) or a `go build` binary as above is
the only way to run dexel.

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
  embed.go                            # go:embed of public/ and assets/ into the binary
  provider_select_{darwin,linux,other}.go
  internal/
    activity/    # per-OS activity providers; the privacy boundary lives here
    engine/      # economy + honesty rules (moods, anti-mashing, coin pricing)
    game/        # sprints, catalog, store gate, save/load state shape
    store/       # save file read/write, schema migrations (SQLite state.db)
    assets/      # resolves embedded-vs-disk (DEXEL_ASSETS_DIR) at runtime
  frontend/      # TypeScript source, built with esbuild
    src/
      wire.ts            # typed mirror of the WS contract
      state/              # typed store + WS client
      render/             # scene/terminal/chrome/overlays/flash (no actions sent)
      features/           # store-modal, activity-modal, history-modal, keybindings
      dev/                 # ?dev=1 harness (hardcoded catalog + state, no backend needed)
  public/         # frontend the Go server embeds and serves (index.html, css, js, fonts)
  assets/         # sprite PNGs the Go server embeds and serves at /assets/
```

Every sprite in `app/assets/` is generated by committed code
(`tools/gen_assets.py`) from a fixed 18-colour palette rather than hand-drawn
binaries, so any sprite is a reviewable diff:

```bash
python3 tools/gen_assets.py   # regenerates all sprite/thumbnail PNGs into app/assets/
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
| `-public` | `""` (embedded) | DEV override: serve the frontend from this directory on disk instead of the copy embedded in the binary. Empty (the default) always serves the embedded copy. |
| `-provider` | `auto` | Activity provider: `auto` (native provider for this OS) or `fake`. |
| `-fake-script` | `""` | Explicit fake-provider script, e.g. `type:20s,idle:40s,mouse:15s`. Overrides `DEXEL_FAKE_SCRIPT` and implies `-provider=fake`. |
| `-insecure-origin` | `false` | Accept WebSocket connections from **any** Origin, skipping same-origin verification. For embedded webviews only (e.g. a `file://`/`app://` frontend); never combine with an `-addr` bound beyond loopback. |

### Environment variables

| Variable | Purpose |
|---|---|
| `DEXEL_FAKE_SCRIPT` | Same script format as `-fake-script`, used when `-provider=fake` and no `-fake-script` flag is given. |
| `DEXEL_ASSETS_DIR` | DEV override: serve `/assets/` from this directory on disk instead of the copy embedded in the binary. Unset (the default) always serves the embedded copy. |

### Health endpoint

`GET /api/health` returns the resolved `assetsDir` (or `null`), whether
`public/index.html` was found (`publicOk`), and the build `version` — useful
for diagnosing "it looks broken" reports without guessing.

### Save file

State persists to `~/.config/dexel/state.db` (SQLite): Dev Cash, XP, current
sprint progress, owned/equipped items and tints, and the activity
counters/history described above. A separate, user-editable
`~/.config/dexel/config.json` holds settings (currently just a display
name) that deliberately live outside the protected save.

The save schema is versioned; migrations are additive and non-destructive.
Integrity is checked on every load: a save that fails its check is never
loaded, never deleted, and never silently downgraded — the original file is
renamed aside (`state.db.invalid` for a tamper/integrity failure,
`state.db.corrupt` for a file SQLite itself can't open, `state.db.future`
for a schema newer than this build understands) and the game starts fresh
from that point on, leaving the quarantined file recoverable at its own
path.

## Development

```bash
cd app
go test ./...
go vet ./...
go run . serve
```

`go run . serve` is the classic foreground dev loop: `127.0.0.1:8080`,
output straight to your terminal, no background process, no
`runtime.json`/lock file. Bare `go run .` now behaves like the installed
CLI — it starts the background runtime and opens a browser — so use
`serve` while iterating on the Go source. Any invocation that starts with
a flag (`go run . -addr 127.0.0.1:0 -provider fake`, etc.) is unaffected
and still runs the legacy foreground server exactly as before.

**Frontend build** (only needed when changing the frontend — the compiled
bundle is committed so `go run . serve` alone always serves a working game):

```bash
cd app/frontend
npm ci
npm run build       # bundles + minifies src/main.ts -> ../public/js/dexel.js
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

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

dexel is licensed under the [Apache License 2.0](LICENSE). Code is
Copyright 2026 Jawwad Zafar.

Third-party components bundled with or used to build dexel (fonts,
CSS frameworks, and build/runtime dependencies) are listed with their own
licenses in [`THIRD-PARTY-LICENSES.md`](THIRD-PARTY-LICENSES.md); see also
[`NOTICE`](NOTICE).
