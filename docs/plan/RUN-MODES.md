# How to run Dexel — the run-mode matrix

Dexel is **one program with two front doors**. The game itself is always the
same Go server (`app/`) serving the same HTML/NES.css frontend over loopback;
what changes is whether you look at it through your browser or through a
native window. Orthogonal to that is *who manages the process*: a terminal
you keep open (modes A/B below), or the CLI's own detached background
runtime — mode P, the primary way Dexel is meant to run day to day. See
[ADR 0018](../adr/0018-dexel-cli-and-background-runtime.md) for the CLI's
design.

This document is the honest status of each way in. Nothing here is aspirational
without saying so.

---

## The matrix at a glance

| | Mode | What you get | Needs | Status |
|---|---|---|---|---|
| **P** | **CLI-managed (production)** | `dexel` / `dexel start` + `open` — a background runtime, browser or app window | a built/installed `dexel` binary | **Works today — the primary way to run Dexel** |
| **A** | **Browser (dev)** | `go run . serve`, open a tab | Go 1.27+ | **Works today** |
| **B** | **App** | `Dexel.app` / `cargo tauri dev` — a native window that ATTACHES to the runtime | Go + Rust + webview deps to build | **Works on macOS arm64** (built 2026-08-23); Linux/Windows unbuilt |
| **C** | **Installer** | `.AppImage` / `.deb` / `.dmg` / `.msi` | nothing (that's the point) | **Unsigned Linux bundles (.deb, AppImage) ship on the release; signed macOS/Windows installers need runners. |
| **D** | **Build from source** | either of the above, from a clean clone | see below | A: works · B: unbuilt |

Modes B and C are the same code (`desktop/`, ADR 0015); C is just B packaged.
B is now compiled and running on macOS arm64; the other platforms' bundles are
still blocked on runners. Mode P wraps mode A's own binary — it is not a
different server, just a different way of starting and stopping it.

**Mode B does not own a server.** Since ARCHITECTURE.md Decision 17 the window
attaches to mode P's runtime (starting one if there is none) and terminates
nothing when it closes. So B and P are not alternatives: B is a view onto P.

---

## Mode P — CLI-managed (works today, the primary production mode)

The same Go binary as mode A, run as a detached background process instead
of a foreground terminal session. This is how Dexel is meant to be used day
to day: build or install it once, then forget the terminal — `dexel open`
(or the button on the desktop app, once mode B/C ship) whenever you want the
window back.

```bash
cd app
go build -o dexel .
./dexel          # bare = start-if-needed, then open
```

- **`dexel` / `dexel open`** — ensure the background runtime is running
  (spawning a detached child of itself if not), then show the UI:
  `dexel-desktop` if installed, else the default browser.
- **`dexel start` / `dexel stop` / `dexel restart`** — explicit lifecycle
  control. `stop` saves on the way out; a second `start` refuses with the
  already-running pid instead of spawning a duplicate.
- **`dexel status [--json]`** — pid, url, version, uptime, and the exact
  state/log paths. It never trusts a pid on its own: it round-trips an HTTP
  call to the runtime it found before believing it is actually alive.
- **`dexel logs [-f] [-n N]`** — the runtime's own log, since a detached
  process has no terminal to print to.
- **Closing the browser tab or app window does not stop the runtime** — only
  `dexel stop` does. The game keeps ticking and autosaving with zero clients
  connected; that was already true of mode A's server, this mode just gives
  it a correct owner instead of a terminal that has to stay open.
- Any invocation that starts with a flag (`dexel -addr 127.0.0.1:0 -provider
  fake`, and the bundled `"Dexel Runtime" -addr ...`) is untouched: it is byte-for-byte today's
  foreground server, so every existing sidecar/CI invocation keeps meaning
  exactly what it meant before this mode existed.
- State (`state.db`, `config.json`) and the runtime's own bookkeeping
  (`runtime.json`, `runtime.lock`, `logs/`) live under `~/.config/dexel`
  (`$DEXEL_HOME` if set, or the platform default on macOS/Windows);
  `dexel status` prints the exact paths.

**Status: works today.** See
[ADR 0018](../adr/0018-dexel-cli-and-background-runtime.md) for the design,
and [`docs/production-runtime/ARCHITECTURE.md`](../production-runtime/ARCHITECTURE.md)
for the full decision record this mode was built from.

---

## Mode A — Browser, dev (works today)

The zero-extra-dependency path for iterating on the Go source, and the mode
every screenshot, test and visual verification in this repo was produced
with.

```bash
git clone git@github.com:jawwadzafar/dexel.git
cd dexel/app
go run . serve
```

Open **<http://localhost:8080>**.

- **Needs:** [Go 1.27+](https://go.dev/dl/). **No Node, no npm, no Rust.** The
  compiled frontend bundle (`app/public/js/dexel.js`) is committed, so this
  always serves a working game.
- **`serve`, not bare:** bare `go run .` (no arguments) now does what mode P
  does — starts the background runtime and opens a browser — so the
  foreground dev server needs its explicit name. Any invocation starting
  with a flag, e.g. `go run . -addr 127.0.0.1:0 -provider fake`, is
  unaffected either way and runs exactly as it always has.
- **Binds:** `127.0.0.1:8080`, loopback only. Moving `-addr` beyond
  `127.0.0.1`/`localhost` exposes your activity monitor and save file to your
  LAN or tailnet; the flag's help text says so. Leave it alone unless you mean
  it.
- **Two instances?** Use `-addr 127.0.0.1:0` for an OS-assigned free port. The
  server prints `DEXEL_LISTENING http://127.0.0.1:<port>` on stdout so you
  know where it landed. (This is the same handshake the desktop shell parses —
  see mode B.)
- **Activity capture** differs by platform (macOS is permissionless, Linux
  needs an `input` group membership, Windows is currently blind). The main
  [README](../../README.md) has that table; it is identical in every mode
  below, because every mode runs this same binary.

**Status: works today.** This is the mode every screenshot, test and visual
verification in this repo was produced with.

---

## Mode B — App (works on macOS arm64)

A real native window instead of a browser tab. Same game, same server; the
window is just a frame around it — and since Decision 17, a frame that owns
nothing.

```bash
# 1. build the Go server into the Tauri sidecar slot  — NOT optional
scripts/build-sidecar.sh

# 2a. run the shell in dev
cd desktop && cargo tauri dev

# 2b. or build the app bundle and launch it
cd desktop && cargo tauri build --bundles app
open src-tauri/target/release/bundle/macos/Dexel.app
```

- **Needs:** everything mode A needs, **plus** Rust >= 1.77.2, the Tauri CLI
  (`cargo install tauri-cli --version "^2.0.0" --locked`), and your platform's
  webview development packages (`libwebkit2gtk-4.1-dev` and friends on Linux;
  Xcode Command Line Tools on macOS; MSVC build tools + WebView2 on Windows).
  The full lists are in [`desktop/README.md`](../../desktop/README.md).
- **What actually happens:** the shell resolves a `dexel` binary (`PATH` ->
  `~/.local/bin/dexel` -> the bundled copy), runs `status --json`, and opens
  the window on the runtime's URL. If no runtime is running it runs `start`
  first — the same detached, lock-taking runtime mode P starts — and then
  attaches.
- **Closing the window stops NOTHING.** The runtime keeps observing activity,
  keeps ticking and keeps autosaving; `dexel stop` is the only thing that
  stops it. This is the whole point: a companion that measures your workday
  cannot depend on a window nobody keeps open. Verified end to end on macOS —
  see [`desktop/README.md`](../../desktop/README.md).
- **Do not run an old build alongside a runtime.** Before Decision 17 the
  shell spawned a *private* server that took no `runtime.lock`; with a
  runtime also running, two processes held two in-memory economies over one
  `state.db`.
- **Why step 1 is not optional:** Tauri resolves `bundle.externalBin` to
  `binaries/Dexel Runtime-<target triple>`. If the script has not run, there is
  no server to spawn.
- **You do not get a different game.** Same economy, same save file, same
  privacy boundary, same loopback-only posture. Packaging changed; nothing else
  did.

**Status: built and verified on macOS arm64** (2026-08-23) — release build
clean, `cargo fmt`/`clippy -D warnings` clean, window seen, attach/start/
survive-close verified. Unsigned. Linux and Windows still need their own build
hosts (mode C).

---

## Mode C — Installer (not shipped)

The end goal: a double-clickable app. No terminal, no browser, no Go
toolchain.

| Platform | Format | Blocked on |
|---|---|---|
| Linux x86_64 | `.AppImage`, `.deb` | a runner labelled `desktop-linux` (needs `webkit2gtk-4.1-dev`) |
| macOS arm64 / universal | `.app`, `.dmg` | `.app` BUILDS LOCALLY today (`cargo tauri build --bundles app`); CI + `.dmg` + signing still need a runner labelled `mac` |
| Windows x86_64 | `.msi` / NSIS | any Windows machine at all |
| Linux arm64, Windows arm64 | — | Phase 3 (arm64 host or a cross sysroot) |

**Why this is not just "run the workflow":** Tauri bundling is host-OS-bound.
You build Linux installers on Linux, macOS on macOS, Windows on Windows. The
Go sidecar cross-compiles from anywhere — the *installer* does not. This repo
has one self-hosted Linux runner (`jwdlab-runner`, label `darkmirror`) which
does **not** have the webview dev packages, and the repo is private so
GitHub-hosted macOS/Windows runners are not free.

So [`.github/workflows/desktop.yml`](../../.github/workflows/desktop.yml) has
three jobs with honestly different status:

- **`sidecar` — active.** Runs on every push. Cross-compiles the Go server for
  all four non-darwin triples, asserts the `externalBin` filenames are exactly
  what Tauri looks for, and verifies the `DEXEL_LISTENING` handshake plus the
  SIGTERM-saves-and-exits behaviour on the real linux binary. This part works.
- **`desktop-linux` — dormant.** Skipped until a `desktop-linux` runner exists
  **and** the repo variable `DESKTOP_LINUX_RUNNER=true` is set.
- **`desktop-macos` — dormant.** Skipped until a `mac` runner exists **and**
  `MAC_RUNNER=true` is set.

The gate is a repository variable *and* a runner label on purpose: a job whose
`runs-on` names a label no runner has does not fail, it queues forever. The
variable makes "dormant" mean cleanly skipped.

**Status: not shipped.** Unblocking it is a matter of registering runners, not
writing code. The recommended next step is to register the owner's own Mac —
macOS is the primary platform per ADR 0010 and the hardware already exists.

Note when it does ship: the first bundles will be **unsigned and
unnotarized**. Gatekeeper and SmartScreen will warn; the apps still run.
Signing needs paid certificates the owner must supply and is tracked as a
follow-up.

---

## Mode D — Build from source (OSS)

Both paths, from a clean clone.

### D1 — Go only (modes A and P). Works today.

```bash
cd app
go build -o dexel .          # Windows: go build -o dexel.exe .
./dexel                      # mode P: starts in the background, opens a browser itself
./dexel serve                # or, mode A's foreground dev server; then open http://localhost:8080
```

One dependency: Go. Node is needed **only** if you edit
`app/frontend/src/*.ts` and want to rebuild the committed bundle — see
[`app/frontend/README.md`](../../app/frontend/README.md). CI asserts the
committed bundle matches a fresh build, so it can never silently drift.

Cross-compiling the server alone is trivial (`GOOS`/`GOARCH`,
`CGO_ENABLED=0`) for Linux and Windows. The macOS build wants cgo for its real
activity provider, so build that one on a Mac; a `CGO_ENABLED=0` darwin build
compiles but ships a *blind* provider. `scripts/build-sidecar.sh --list`
prints the whole triple table and refuses the darwin targets off-Mac for
exactly this reason.

### D2 — Go + Rust (app mode). Authored, unbuilt.

```bash
# frontend, only if you changed the TypeScript
cd app/frontend && npm ci && npm run build && cd ../..

scripts/build-sidecar.sh     # the Go server -> the sidecar slot
cd desktop
cargo tauri build            # or: cargo tauri build --bundles appimage,deb
```

Prereqs are mode B's. Full detail, including the per-distro package lists, is
in [`desktop/README.md`](../../desktop/README.md).

---

## Why B and C are not verified

The machine that authored `desktop/` had **no Rust toolchain and no
webkit2gtk**, so nothing in it has been compiled or run. That is a deliberate,
stated limitation, not an oversight:

**Tested for real:**

- `scripts/build-sidecar.sh --all` produces correctly triple-named binaries
  for all four non-darwin targets and loudly skips the two darwin ones.
- The linux sidecar, launched exactly as the shell launches it, prints
  `DEXEL_LISTENING http://127.0.0.1:<port>` as its first stdout line, binds
  `127.0.0.1` only, serves `GET /` as `200` with `publicOk: true`, honours
  `DEXEL_ASSETS_DIR`, and on SIGTERM saves state and exits 0.
- Every JSON and YAML file validates; every `run:` block in `desktop.yml`
  parses as bash; the two `sidecar`-job assertion steps were executed
  verbatim and pass.

**Not tested (needs a machine with Rust):**

- That the Rust compiles.
- That the packaged app actually launches the self-contained sidecar
  (EMBED-1 — no `bundle.resources` to stage any more) and the window loads
  its embedded frontend/assets end to end.
- That the window opens on the game, always-on-top, at the right size.
- ~~That closing the window leaves no orphaned server process.~~ **Inverted.**
  The runtime MUST survive the window closing (ARCHITECTURE.md Decision 17);
  an empty `pgrep` after quitting would now be the bug, not the pass.

Those four were the first build's gate; it has been run (see mode B's status). Until someone has seen them,
[`desktop/README.md`](../../desktop/README.md) is the authority on what is
real and what is merely written down.

---

## Which mode should I use?

- **Just want to use Dexel day to day?** Mode P — build (or install) once,
  then `dexel start`/`open`/`stop` from a terminal you don't have to keep
  open.
- **Developing the Go source or the frontend?** Mode A. It works, it is one
  command, and it is what the whole repo is tested against.
- **Working on the desktop shell itself?** Mode B, on a machine with Rust.
- **Want to hand Dexel to someone who does not have Go?** Mode C — which
  means registering a runner first.

## See also

- [ADR 0018 — Dexel CLI and background runtime](../adr/0018-dexel-cli-and-background-runtime.md) —
  mode P's design: the argv dispatch, the detached runtime, discovery and
  single-instance locking.
- [ADR 0015 — Tauri desktop shell](../adr/0015-tauri-desktop-shell.md) — the
  decision and its alternatives for modes B/C.
- [`docs/production-runtime/ARCHITECTURE.md`](../production-runtime/ARCHITECTURE.md) —
  the full production-runtime design mode P and ADR 0018 were built from.
- [`desktop/README.md`](../../desktop/README.md) — build instructions and the
  verified/unverified split.
- [`.github/workflows/desktop.yml`](../../.github/workflows/desktop.yml) — the
  pipeline and how to activate the dormant jobs.
