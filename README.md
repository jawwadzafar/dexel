<div align="center">

# Dexel

**A cozy pixel-art desktop companion whose workday runs on *your* real typing.**

[![Release](https://img.shields.io/github/v/release/jawwadzafar/dexel?sort=semver&label=release)](https://github.com/jawwadzafar/dexel/releases/latest)
[![Platform](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-8a7fd4)](#platform-support-for-activity-capture)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

![Dexel running with a live sprint, terminal, and status line](docs/images/hero.png)

</div>

## What is this

`dexel` is a small pixel-art developer character who lives at a desk on your
screen. Real typing — in **any** app on your machine, not just the game window
— advances their current sprint. Finishing a sprint pays out Dev Cash, which
you spend in a store on hoodies, chairs, keyboards, mice, beverages, plants,
wall decor, and a desk buddy. Items are bought once and equipped any time
("own many, equip one" per slot). There are no meters, no hunger, no decay,
and nothing to feed or lose — it's pure idle: leave it running, keep working,
watch the desk fill in.

It ships as **one binary**. A Go backend samples your real input and serves an
HTML/[NES.css](https://nostalgic-css.github.io/NES.css/) frontend over a
WebSocket on `127.0.0.1`; the frontend and every sprite are embedded in the
executable, so there is nothing to unpack and nothing to locate at runtime.
Everything stays on your machine — see [Privacy](#privacy).

## Install

One line, and dexel is **installed, in your app grid, and running**. It
resolves the latest release for your platform, verifies its sha256, installs
the `dexel` binary into your user bin directory, registers a launcher so dexel
has an icon in your desktop's app grid (Linux) or Start Menu (Windows), and
then starts the runtime and opens the game. No `sudo`, no elevation, no
autostart.

**Linux and Windows via Git Bash/MSYS2/Cygwin** (amd64 or arm64) — the same
one line everywhere bash exists (macOS runs the same script too, see the note
below on why it isn't installable yet):

```bash
curl -fsSL https://raw.githubusercontent.com/jawwadzafar/dexel/main/install.sh | bash
```

On a real Windows box, running that inside Git Bash (or plain MSYS2, or
Cygwin) does not just print instructions and stop: `install.sh` detects it,
finds `powershell.exe`/`pwsh.exe`, downloads `install.ps1` and hands off to
it automatically, so it finishes the same install `install.ps1` itself would
have done. If no PowerShell can be found on that box, it falls back to
printing the one-liner below instead of guessing. (WSL is unaffected either
way — WSL is Linux, so it just takes the normal Linux path above.)

**Stock PowerShell** (amd64 or arm64), if you'd rather run it directly
instead of through Git Bash:

```powershell
irm https://raw.githubusercontent.com/jawwadzafar/dexel/main/install.ps1 | iex
```

> [!IMPORTANT]
> **These two URLs — and the release badge above — go live the moment this
> repository becomes public.** While it is still private,
> `raw.githubusercontent.com` answers 404 to an anonymous request, so run the
> script from a checkout with a token that can read the repo instead:
>
> ```bash
> GH_TOKEN="$(gh auth token)" bash ./install.sh          # Linux
> $env:GH_TOKEN = (gh auth token); ./install.ps1         # Windows
> ```
>
> The same two files will later be served from
> `https://get.dexel.jwdlab.com/install.sh` and `/install.ps1` — the same
> bytes at a shorter address, not a different installer
> ([release pipeline](docs/production-runtime/RELEASE_PIPELINE.md)).

**macOS is not published yet.** The current release carries no `darwin`
archive, so the installer says so and stops rather than pretending — build
from source with the two commands under [Quick start](#quick-start). Nothing
needs to change in the installer when a macOS build appears: it checks the
release for a `darwin` archive on every run, so the day one is published the
normal install path takes over.

When it does, the installer will place the CLI and start it, and say where the
window comes from: `Dexel.app` is built and signed by `scripts/mac-release.sh`
and ships as a `.dmg`, which is a drag-to-`/Applications` gesture rather than
something a script should do behind your back. `dexel open` finds a bundle
there on its own once it exists, so nothing has to be re-run.

> [!IMPORTANT]
> **On Windows, activity tracking is new in this build and field verification
> is pending.** Dexel now has a real native Windows provider — two low-level
> hooks that count keystrokes and mouse activity globally, no permission
> prompt, no cgo ([ADR 0021](docs/adr/0021-windows-activity-provider.md)) — but
> nobody has yet run it on Windows hardware, because this project has no
> Windows CI runner. Everything decidable without one is tested; the hook
> install itself is not. It also still degrades honestly: if Windows refuses
> the hooks (enterprise policy, a locked desktop) the provider reports itself
> *blind* rather than pretending to see, and the companion will not claim a
> workday it cannot see. See
> [Platform support](#platform-support-for-activity-capture).

### What the installer does, and what it will not do

| | |
|---|---|
| resolves | the latest GitHub release, or `DEXEL_VERSION` if you pin one |
| verifies | sha256 against the release's `sha256sums.txt`, cross-checked against the digest GitHub reports for the same asset. A mismatch is a hard failure and nothing is unpacked |
| installs | the binary: `~/.local/bin/dexel`, or `%LOCALAPPDATA%\dexel\bin\dexel.exe` |
| installs | the launcher icon out of the archive — `~/.local/share/icons/hicolor/128x128/apps/dexel.png` on Linux, `dexel.ico` next to the exe on Windows |
| installs | a launcher entry, so dexel is in your app grid / Start Menu and not only on your PATH: `~/.local/share/applications/dexel.desktop` (`Exec=dexel open`) on Linux, a `Dexel.lnk` in your **per-user** Start Menu on Windows |
| installs | *on Linux, when a desktop session is detected*, the optional GUI shell — the release's AppImage, at `<install-dir>/dexel-desktop.AppImage` with a small `dexel-desktop` shim beside it, which is the name `dexel open` already looks for. It is ~84 MB, its size is announced before it is fetched, and `--no-app` skips it. The `.deb` on the same release is deliberately never used: `dpkg` needs root |
| creates | your state directory and its `logs/` subdirectory, so the first run cannot trip over a missing folder |
| **starts** | dexel, at the end — `dexel open` when there is a desktop session to open into, `dexel start` when there is not. That is the install finished, not a side effect. `--no-start` opts out |
| **never** | uses `sudo`, elevates, or writes outside `$HOME` / `%LOCALAPPDATA%` + `%APPDATA%` + `HKCU` |
| **never** | enables login autostart. Starting the thing you just asked for and making it come back on every login forever are different questions; the second one stays `dexel autostart enable` |
| **never** | installs a binary it could not verify. The AppImage can only be checked against the digest GitHub reports (the Tauri bundles are not in `sha256sums.txt`), so a release that reports no digest for it gets the browser front door instead — one witness is why the GUI shell is optional and the CLI is not |
| PATH | on Linux it *prints* the export line for your shell and lets you paste it — it does not edit your dotfiles. On Windows it appends to your **user** PATH (never the machine PATH), and only if the directory is not already there |

Everything it writes is a plain file under `$HOME` (or `%LOCALAPPDATA%` /
`%APPDATA%`), and every step after the binary is best-effort: a desktop
environment that will not take a launcher does not fail an install of a CLI
that works fine from a terminal.

Re-running is an upgrade in place: a live runtime is stopped first, every
artifact is replaced byte-for-byte, your save data is untouched — and the new
build is started again for you.

<details>
<summary><b>Knobs, exit codes, and dry runs</b></summary>

Both scripts are POSIX-`sh` / PowerShell-5.1 clean, take no arguments they do
not document, and use distinct exit codes so a piped run is diagnosable from
`$?` alone:

| Code | Meaning |
|---|---|
| `3` | unsupported platform |
| `4` | missing tool |
| `5` | no build for this platform in this release |
| `6` | checksum mismatch |
| `7` | network failure |
| `8` | the installed binary failed its own `dexel version` check |

Environment knobs, on either platform: `DEXEL_INSTALL_DIR`, `DEXEL_VERSION`,
`DEXEL_HOME`, `DEXEL_ARCHIVE` (install a `.tar.gz`/`.zip` you already have —
the checksum is still verified), and `GH_TOKEN` / `GITHUB_TOKEN`. A dry run
(`--dry-run` on Linux, `$env:DEXEL_DRY_RUN = '1'` on Windows) resolves,
downloads, and verifies without installing anything — including the AppImage,
which makes it a complete test of the release and every checksum in it.

Turning the new behaviour off, as flags on Linux or environment variables on
either platform:

| Flag (Linux) | Environment | Effect |
|---|---|---|
| `--no-start` | `DEXEL_NO_START=1` | install everything, start nothing |
| `--no-desktop` | `DEXEL_NO_DESKTOP=1` | no icon, no launcher entry, no GUI shell |
| `--no-app` | `DEXEL_NO_APP=1` | keep the icon and the launcher, skip the ~84 MB AppImage |
| `--app` | `DEXEL_APP=1` | fetch the AppImage even with no `$DISPLAY` — e.g. installing over ssh for a desktop you will log into later |

</details>

### Uninstall

One command, on every platform:

```bash
dexel uninstall            # stop, disable autostart, remove every installed file
dexel uninstall --purge    # ...and delete your save data too
```

It is the exact reversal of what the installer did, and it says so as it goes:
it stops the runtime (via the lifecycle endpoint, the same graceful path as
`dexel stop`), disables autostart with **every** mechanism probed — so a login
entry can never outlive the binary it points at — removes the binary, the
optional GUI shell, the launcher entry and the icon, then prints a report of
every path it removed and every path it kept.

**Your save is kept by default.** `state.db`, `config.json` and `logs/` stay
where they are, their paths are printed, and reinstalling later resumes *the
same dexel* — same Dev Cash, same wardrobe, same lifetime counters. Only
`--purge` deletes them, and it asks twice: once for the uninstall, then again
for the data, where the confirmation is typing the literal word `purge`.
`--yes` skips both prompts for a script; without a terminal and without
`--yes`, `uninstall` refuses rather than guessing which answer you meant.
Running it twice is harmless — already-gone paths are reported as
`already absent` and it still exits 0.

Two things it will **not** do, on purpose:

* **It never uses `sudo`.** If you also installed the release's `.deb`
  system-wide, those files are in `/usr/bin` and belong to your package
  manager — `uninstall` detects them, names them, and prints
  `sudo apt remove dexel` instead of failing halfway.
* **It never edits your shell config.** The installer only ever *printed* the
  `PATH` line; if you added it, that line is yours to remove, and `uninstall`
  reminds you which one it was.

On Windows a running `.exe` cannot delete itself, so `uninstall` schedules a
detached helper that removes `dexel.exe` the moment the command exits and
appends one line to the runtime log saying whether it worked. On macOS an
installed `Dexel.app` (dragged in from the `.dmg`) is removed under the same
confirmation.

<details>
<summary>By hand instead, or if you deleted the binary before reading this</summary>

No package manager was involved, so it is just files:

```bash
dexel stop && dexel autostart disable
rm -f ~/.local/bin/dexel
rm -f ~/.local/bin/dexel-desktop ~/.local/bin/dexel-desktop.AppImage  # if the GUI shell was installed
rm -f ~/.local/share/applications/dexel.desktop
rm -f ~/.local/share/icons/hicolor/128x128/apps/dexel.png
rm -rf ~/.config/dexel   # optional: also drops your save
```

On Windows, in PowerShell:

```powershell
dexel stop; dexel autostart disable
Remove-Item "$env:LOCALAPPDATA\dexel\bin\dexel.exe", "$env:LOCALAPPDATA\dexel\bin\dexel.ico"
Remove-Item (Join-Path ([Environment]::GetFolderPath('Programs')) 'Dexel.lnk')
Remove-Item -Recurse "$env:LOCALAPPDATA\dexel"   # optional: also drops your save
```

The installer prints this same list, with your real paths filled in and only
the lines that apply to what it actually installed.

</details>

## Features

- **Sprints -> Dev Cash -> store.** A visible sprint bar advances from real
  typing; completed sprints pay Dev Cash you spend on cosmetics that actually
  render on the character and desk.
- **Honest moods.** `Coding` only shows after a recent keystroke — mouse
  motion alone never triggers it. `On break` only comes from genuine global
  idleness; if the platform can't see globally, the idle clock freezes instead
  of guessing.
- **Work sessions (`W`).** Declare "I sat down to work" and get that stretch
  as its own lens over tracking that already happens. A session is arithmetic
  over the same counters — never a second economy, never double-counted
  ([ADR 0017](docs/adr/0017-sessions.md)).
- **Activity analytics (`A`).** Today's and lifetime keystrokes, mouse-active
  seconds, active/idle time, sprints, focus sessions, and app switches, plus a
  per-signal Dev Cash breakdown (how many coins each signal actually earned
  today).
- **30-day history (`H`).** A bar chart of the last 7 days, a 30-day streak
  strip, current/longest streak, and two honest insights — busiest day and
  longest focus block — derived only from counts.
- **Pause that really pauses.** Pausing stops the provider itself rather than
  gating a counter, so nothing is observed and nothing accrues while paused.
- **Content-free by construction**, enforced by tests that fail the build.
  See [Privacy](#privacy).

## Screenshots

| Store | History |
|---|---|
| ![Store modal: categories, catalog grid, buy/equip, live preview](docs/images/store.png) | ![History modal: 7-day bar chart, 30-day streak strip, insights](docs/images/history.png) |

## Quick start

Building from source — the path to use on macOS, or when you want the tip of
`main` rather than a release. Requires [Go 1.27](https://go.dev/dl/) or newer.
Node/npm are **not** needed to run the game, only to change the frontend.

```bash
git clone git@github.com:jawwadzafar/dexel.git
cd dexel/app
go build -o dexel .        # Windows: go build -o dexel.exe .
./dexel                    # Windows: dexel.exe
```

`./dexel` with no arguments starts Dexel's background runtime if one isn't
already running, then opens the game in your browser — no port to remember, no
terminal to leave open. Closing the browser tab does **not** stop it.

| Command | What it does |
|---|---|
| `dexel` | start the runtime if needed, then open the UI |
| `dexel status` | is a runtime running? pid, port, url, version, paused (`--json`) |
| `dexel stop` / `restart` | shut down cleanly, saving on the way out |
| `dexel pause` / `resume` | stop / start observing activity |
| `dexel logs` | the runtime log (`-n N`, `-f`, `--path`, `--truncate`) |
| `dexel autostart enable\|disable\|status` | the login autostart entry — never enabled implicitly |
| `dexel uninstall` | remove dexel from this machine (`--purge` drops your save too) — see [Uninstall](#uninstall) |
| `dexel serve` | the foreground developer server (see [Development](#development)) |
| `dexel version` / `help` | version, commit, os/arch — or the full command list |

State (`state.db`, `config.json`) and the runtime's own bookkeeping
(`runtime.json`, `runtime.lock`, logs) live under `~/.config/dexel`
(`$DEXEL_HOME` if set, or the platform default on macOS/Windows).
`dexel status` prints the exact paths.

> [!CAUTION]
> Binding `-addr` beyond `127.0.0.1`/`localhost` exposes the activity monitor
> and your save file to your LAN or tailnet. Leave it at the default unless you
> specifically intend that.

### Platform support for activity capture

The server **runs on every platform Go supports**. What differs is whether it
can see your real typing and mouse activity *globally* — which is what advances
sprints and drives the honest mood/idle logic.

| Platform | Global activity capture | Setup |
|---|---|---|
| **macOS** | Yes — permissionless | None. Polls `CGEventSourceSecondsSinceLastEventType` (a system timestamp delta, not a keystroke tap), so there is no Accessibility dialog to click through. |
| **Linux** | Yes — needs one group membership | `sudo usermod -aG input "$USER"`, then log out and back in. It reads raw `/dev/input/event*` nodes. |
| **Windows** | Yes — permissionless, *unverified on hardware* | None. Installs `WH_KEYBOARD_LL` + `WH_MOUSE_LL` low-level hooks, which count events and never identify them. New in this build ([ADR 0021](docs/adr/0021-windows-activity-provider.md)); if Windows refuses the hooks it reports itself blind instead of guessing. |
| **Any other OS** | **No native provider** | None available. A blind, zero-signal provider runs instead. |

Without the Linux `input` group the server still runs; it just can't see global
input, so activity only counts while the browser tab itself has focus, and the
honesty rules freeze rather than guess at idle time.

The Windows provider is the newest of the three and the only one nobody has
run on the platform it targets. Its coalescing rules, its hook-eviction
detection, and the narrowing of a process path down to a bare app name are
pure Go with tests that run on Linux, and two tests parse the Windows source
to prove it never calls `GetWindowTextW` (a window title) or touches the mouse
hook's cursor-position struct. The hook install, the message loop, and
foreground-app detection have never executed. That is a *stated* gap, not an
implied one — [ADR 0021](docs/adr/0021-windows-activity-provider.md) lists what
a first field session should check, and a Windows CI runner is what would
retire the caveat.

<details>
<summary><b>Cross-compiling</b></summary>

Go cross-compiles a binary for another OS/architecture from any host:

```bash
GOOS=windows GOARCH=amd64 go build -o dexel.exe .
GOOS=darwin  GOARCH=arm64 go build -o dexel .
GOOS=linux   GOARCH=amd64 go build -o dexel .
```

The caveat: activity capture is OS-native code (see the table above), so a
cross-built binary only ever gets the provider its *target* OS has. Linux and
Windows both cross-build completely — their providers are plain syscalls with
no cgo, which is exactly why the Windows one was written that way. macOS is the
exception: `provider_darwin.go` is cgo (Cocoa/CoreGraphics), so a
`GOOS=darwin` build needs a macOS host and a clang toolchain rather than just
`go build`.

</details>

<details>
<summary><b>Packaged desktop app (Tauri)</b></summary>

A native desktop shell via [Tauri](https://tauri.app/) lives in `desktop/`. The
v0.1.0 release carries **unsigned Linux bundles** (`.AppImage`, `.deb`); a
macOS `.app`/`.dmg` builds locally today, and signed/notarized installers for
macOS and Windows need CI runners and paid certificates this project does not
have yet. The full matrix is
[`docs/plan/RUN-MODES.md`](docs/plan/RUN-MODES.md).

```bash
scripts/build-sidecar.sh                      # the Go server, for this host
cd desktop && cargo tauri build --bundles app # macOS: needs Rust + Xcode CLT
open src-tauri/target/release/bundle/macos/Dexel.app
```

The window is a **view**: it attaches to Dexel's background runtime, and
closing it does not stop anything — the runtime keeps counting your activity
until `dexel stop`.

</details>

### Troubleshooting

- **`ASSETS NOT FOUND` banner in the scene.** The binary embeds its own sprite
  PNGs and frontend, so this shouldn't happen on a normal `go build`. It means
  `-public` or `DEXEL_ASSETS_DIR` was set to a directory that doesn't hold the
  app's assets. Drop the override to fall back to the embedded copy, or point
  it at a real `app/public` / `app/assets` checkout.
- **Check what the server actually found.** `GET /api/health` returns the
  resolved `assetsDir` (`null` means "serving the embedded copy"), whether
  `public/index.html` was found, and the build version.

## Controls

The title bar carries the coin and level on the left, a session pill and
`PAUSED` badge in the middle, and the ☰ menu on the right. Every menu item has
a keyboard equivalent.

| Key | Action |
|---|---|
| `S` or `Tab` | Open the store |
| `A` | Open the activity log |
| `H` | Open the 30-day history |
| `W` | Open sessions |
| `M` or ☰ | Toggle the menu panel |
| `Esc` | Close the open modal, or the menu |

Two things have no key by design: **pause/resume** is the `[P]` menu item (or
`dexel pause` from the CLI), and the **onboarding** modal opens only on a
genuine first run — never from a button. Typing in a text field never triggers
a shortcut. Full contract: [`docs/ui-spec.md`](docs/ui-spec.md),
[`docs/game/surfaces.md`](docs/game/surfaces.md).

## How it works

```
activity provider (darwin / linux)
        |  counts + booleans only
        v
engine (anti-mashing economy, honest moods)
        |
        v
game (sprints, sessions, store, save/load)
        |
        v
WebSocket state broadcast (~1 Hz)
        |
        v
HTML/CSS/TS frontend (app/public/)
```

Each arrow is a boundary, and the first one is the privacy boundary: only
counts and durations cross it, enforced by tests rather than by convention.

```
app/
  main.go, handlers.go, hub.go        # HTTP + WebSocket server, single-owner game loop
  cli.go, cmd_*.go                    # the dexel CLI + background runtime
  embed.go                            # go:embed of public/ and assets/ into the binary
  provider_select_{darwin,linux,other}.go
  internal/
    activity/    # per-OS activity providers; the privacy boundary lives here
    engine/      # economy + honesty rules (moods, anti-mashing, coin pricing)
    game/        # sprints, sessions, catalog, store gate, save/load state shape
    store/       # save file read/write, schema migrations (SQLite state.db)
    assets/      # resolves embedded-vs-disk (DEXEL_ASSETS_DIR) at runtime
  frontend/      # TypeScript source, built with esbuild
    src/
      wire.ts     # typed mirror of the WS contract
      state/      # typed store + WS client
      render/     # scene/terminal/chrome/overlays/flash (no actions sent)
      features/   # store, activity, history, sessions, onboarding, menu, keybindings
      dev/        # ?dev=1 harness (hardcoded catalog + state, no backend needed)
  public/         # frontend the Go server embeds and serves (index.html, css, js, fonts)
  assets/         # sprite PNGs the Go server embeds and serves at /assets/
```

Every sprite in `app/assets/` is generated by committed code
(`tools/gen_assets.py`) from a fixed 18-colour palette rather than hand-drawn
binaries, so any sprite is a reviewable diff:

```bash
python3 tools/gen_assets.py   # regenerates all sprite/thumbnail PNGs into app/assets/
```

### Documentation

[`docs/README.md`](docs/README.md) is the index. Four layers, four different
questions:

| Layer | Answers |
|---|---|
| [`docs/game/`](docs/game/README.md) | **How the game works today** — the rules and numbers a player is actually subject to, read out of the Go source |
| [`docs/adr/`](docs/adr/README.md) | **Why** each decision was made, and what it gave up (20 records) |
| [`docs/plan/ROADMAP.md`](docs/plan/ROADMAP.md) | **What's next**, in independently shippable phases |
| [`docs/ui-spec.md`](docs/ui-spec.md), [`docs/art-direction.md`](docs/art-direction.md), [`docs/upgrade-design.md`](docs/upgrade-design.md) | The normative specs the frontend, the art, and the economy are checked against |

<details>
<summary><b>Legacy: the Rust/Bevy implementation</b></summary>

The **original** implementation was Rust + Bevy — crates `companion/` and
`activity/` at the repo root, plus `tools/shotcap/`, the root
`Cargo.toml`/`Cargo.lock`, and `scripts/build.sh`. The project pivoted to the
Go + HTML/NES.css stack in `app/` for verifiability (a headless browser on any
machine sees exactly what a user sees) and portability —
[ADR 0011](docs/adr/0011-engine-pivot-to-pdf-native-stack.md).

Those files now live on the branch **`attic/legacy-rust-and-fleet`**, not in
`main`'s working tree
([ADR 0020](docs/adr/0020-archive-the-frozen-rust-track.md)). Nothing consumed
them any more, and two directories named `activity/` in one repo kept
misleading readers. To get the Bevy game back:

```bash
git worktree add ../dexel-legacy attic/legacy-rust-and-fleet
cd ../dexel-legacy && cargo run -p companion
```

If you're new here: **`app/` is the thing you run**, and
`app/internal/activity/` is the only `activity` there is.

</details>

## Privacy

This is the project's defining constraint, not a footnote.

- **Counts and durations only, never content.** The activity layer records
  *that* a key was pressed and *that* the mouse moved — never which key, never
  any typed text, never clipboard contents.
- **App identity only, never window titles.** The foreground-app signal is an
  app name (e.g. "Visual Studio Code") — never a window title, document name,
  or URL ([ADR 0009](docs/adr/0009-app-identity-not-titles.md)).
- **Enforced structurally, not just promised.** A reflect-based allow-list test
  fails the build if anyone ever adds a field to the activity
  `Snapshot`/state/save types that isn't on an explicit allow-list, or whose
  name even *smells* like content (`title`, `text`, `clipboard`, `keycode`, …).
  See [ADR 0002](docs/adr/0002-activity-isolation-and-privacy.md).
- **Nothing on screen is derived from your machine.** The terminal lines and
  status ticker come from compile-time string constants in the backend. The
  only on-screen string that may contain machine-derived text is the activity
  line, and only as a mapped application display name.
- **Copy/paste detection was deliberately dropped on macOS.** Telling a
  copy/paste chord from any other keystroke would need the `CGEventTap` API —
  and therefore the Accessibility permission this project's whole design exists
  to avoid. It is deferred behind a future, explicit, opt-in permission phase
  rather than shipped by default
  ([ADR 0012](docs/adr/0012-a2-content-free-signal-set-and-permission-fork.md)).
- **100% local.** Everything runs on your machine talking to itself over
  loopback. Nothing phones out.

## Configuration

### Flags

Accepted by `dexel serve` and by the flag-style invocations.

| Flag | Default | Description |
|---|---|---|
| `-addr` | `127.0.0.1:8080` | Listen address. A port of `0` binds an OS-assigned free port. Binding beyond `127.0.0.1`/`localhost` exposes the activity monitor and save to your LAN/tailnet. |
| `-provider` | `auto` | Activity provider: `auto` (native for this OS) or `fake`. |
| `-fake-script` | `""` | Explicit fake-provider script, e.g. `type:20s,idle:40s,mouse:15s`. Overrides `DEXEL_FAKE_SCRIPT` and implies `-provider=fake`. |
| `-public` | `""` (embedded) | DEV override: serve the frontend from this directory on disk instead of the embedded copy. |
| `-insecure-origin` | `false` | Accept WebSocket connections from **any** Origin, skipping same-origin verification. For embedded webviews only; never combine with an `-addr` bound beyond loopback. |
| `-allow-origin` | *(none)* | Extra literal WebSocket Origin(s) to accept beyond the loopback hosts, as `host[:port]` or a full origin URL. Repeatable; never a wildcard. |

### Environment variables

| Variable | Purpose |
|---|---|
| `DEXEL_HOME` | Override the state directory (default `~/.config/dexel`, or the platform default on macOS/Windows). |
| `DEXEL_FAKE_SCRIPT` | Same script format as `-fake-script`, used when `-provider=fake` and no flag is given. |
| `DEXEL_ASSETS_DIR` | DEV override: serve `/assets/` from this directory on disk instead of the embedded copy. |

### Save file

State persists to `state.db` (SQLite) in your state directory: Dev Cash, XP,
current sprint progress, owned/equipped items and tints, sessions, and the
activity counters/history described above. A separate, user-editable
`config.json` holds settings (currently just a display name) that deliberately
live outside the protected save.

The schema is versioned and migrations are additive and non-destructive.
Integrity is checked on every load: a save that fails its check is never
loaded, never deleted, and never silently downgraded. The original file is
renamed aside and the game starts fresh from that point on, leaving the
quarantined file recoverable at its own path:

| Renamed to | When |
|---|---|
| `state.db.invalid` | a tamper / integrity failure |
| `state.db.corrupt` | a file SQLite itself can't open |
| `state.db.future` | a schema newer than this build understands |

## Development

```bash
cd app
go vet ./...
go test ./...
go run . serve       # foreground dev server on 127.0.0.1:8080
```

`go run . serve` is the classic foreground loop: output straight to your
terminal, no background process, no `runtime.json`/lock file. Bare `go run .`
behaves like the installed CLI — it starts the background runtime and opens a
browser — so use `serve` while iterating on the Go source. Any invocation that
starts with a flag (`go run . -addr 127.0.0.1:0 -provider fake`) still runs the
foreground server directly.

**Scripted activity, no real input needed:**

```bash
go run . serve -fake-script "type:20s,idle:40s,mouse:15s"
```

**Frontend build** — only needed when changing the frontend. The compiled
bundle (`app/public/js/dexel.js`) is committed, and the whole `app/public/` +
`app/assets/` tree is embedded into the binary at build time, so `go build`
alone always produces a working game with zero Node/npm involved.

```bash
cd app/frontend
npm ci
npm run build       # bundles + minifies src/main.ts -> ../public/js/dexel.js
npm run typecheck   # tsc --noEmit, strict
```

See [`app/frontend/README.md`](app/frontend/README.md) for the layer breakdown
(state/render/features) and why the built bundle is committed alongside its
TypeScript source.

**Frontend dev harness:** `http://localhost:8080/?dev=1` loads a hardcoded
catalog + state client-side, for iterating on the modals without a live backend
connection.

**CI:** [`.github/workflows/build.yml`](.github/workflows/build.yml) runs two
jobs on push/PR — `go vet` plus the raced test suite via `scripts/test-race.sh`,
and a frontend job that runs `npm run build` fresh and diffs it against the
committed bundle so the two can never silently drift. GitHub Actions is
currently blocked at the account level for this repository, so the workflows run
on a self-hosted runner, their checks won't render for non-collaborators, and
the gates below are run locally.

## Roadmap

Tracked in [`docs/plan/ROADMAP.md`](docs/plan/ROADMAP.md). Where things stand:

| Track | State |
|---|---|
| Core loop — activity, sprints, Dev Cash, store, equip | **Shipped** |
| Analytics (A1 activity log, A2 priced signals, A3 history/streaks) | **Shipped** |
| Identity + work sessions (P1, P2) | **Shipped** |
| Persistence — SQLite, HMAC integrity, quarantine | **Shipped** |
| CLI + background runtime, `install.sh` / `install.ps1` | **Shipped** |
| Release pipeline | Workflows written; runs by hand while Actions is account-blocked |
| Frontend architecture (F1 build + TypeScript, F2 modular layers) | **Shipped** |
| F3 — Tauri desktop shell | Scaffold + Linux bundles; signed installers need runners |
| Native Windows activity provider | **Shipped** (ADR 0021) — unverified on hardware; no Windows runner |
| Art fidelity | **Parked** at the current procedural pixel-art look, deliberately, in favour of shipping features |

## Contributing

Pull requests are welcome once this repository is public. Until then, issues
and ideas are the useful contribution.

**Run the gates before you open a PR.** All of them, locally — Actions is blocked
at the account level, so a green pipeline is not available to lean on:

```bash
# from the repo root
(cd app && go vet ./...)
bash scripts/test-race.sh
(cd app/frontend && npm run typecheck && npm run build)
git diff --exit-code -- app/public/js/dexel.js app/public/js/dexel.js.map   # no bundle drift
```

Three things a PR is judged on beyond that:

- **The privacy boundary is not negotiable.** A new field on the activity
  `Snapshot`, the WebSocket state, or the save types must pass the structural
  allow-list test. If your feature needs content, the answer is a different
  feature.
- **Honest mechanics.** A signal the platform cannot actually see freezes; it
  never guesses and never fabricates. See
  [ADR 0010](docs/adr/0010-mac-first-honest-mechanics.md).
- **Art is generated, never hand-edited.** Change
  [`tools/gen_assets.py`](tools/gen_assets.py) and regenerate; a hand-edited
  PNG is a diff nobody can review.

Behaviour changes update [`docs/game/`](docs/game/README.md) in the same
commit, and a decision with lasting consequences gets an ADR in
[`docs/adr/`](docs/adr/README.md).

## License

Dexel is licensed under the **[Apache License 2.0](LICENSE)** — Copyright 2026
Jawwad Zafar. In plain terms, and why that serves you:

- **Free to use, modify, distribute, and sell.** Commercial use is fine. You
  can fork it, ship it inside a paid product, and keep your changes private.
- **Attribution is required.** Redistributions must retain the
  [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE) files and state any changes you
  made — so users can always trace where the code came from.
- **An explicit patent grant** comes with it. Contributors license the patents
  their contributions need, which is the practical difference between Apache
  2.0 and MIT: you get patent peace of mind, not just copyright permission.
- **The name is not part of the grant.** The license covers the code, not the
  *dexel* name, logo, or character as a trademark. Fork freely; ship it under
  your own name.

Third-party components bundled with or used to build Dexel (fonts, CSS
frameworks, and build/runtime dependencies) are listed with their own licenses
in [`THIRD-PARTY-LICENSES.md`](THIRD-PARTY-LICENSES.md).
