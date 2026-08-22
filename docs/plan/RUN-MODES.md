# How to run dexel — the run-mode matrix

dexel is **one program with two front doors**. The game itself is always the
same Go server (`app/`) serving the same HTML/NES.css frontend over loopback;
what changes is whether you look at it through your browser or through a
native window.

This document is the honest status of each way in. Nothing here is aspirational
without saying so.

---

## The matrix at a glance

| | Mode | What you get | Needs | Status |
|---|---|---|---|---|
| **A** | **Browser (dev)** | `go run .`, open a tab | Go 1.27+ | **Works today** |
| **B** | **App (dev)** | `cargo tauri dev` — a native window | Go + Rust + webview deps | **Authored, never built** |
| **C** | **Installer** | `.AppImage` / `.deb` / `.dmg` / `.msi` | nothing (that's the point) | **Not shipped** — needs CI runners |
| **D** | **Build from source** | either of the above, from a clean clone | see below | A: works · B: unbuilt |

Modes B and C are the same code (`desktop/`, ADR 0015); C is just B packaged.
Neither has ever been compiled — see [Why B and C are not
verified](#why-b-and-c-are-not-verified).

---

## Mode A — Browser (works today)

The zero-extra-dependency path, and the only one that is proven.

```bash
git clone git@github.com:jawwadzafar/dexel.git
cd dexel/app
go run .
```

Open **<http://localhost:8080>**.

- **Needs:** [Go 1.27+](https://go.dev/dl/). **No Node, no npm, no Rust.** The
  compiled frontend bundle (`app/public/js/dexel.js`) is committed, so this
  always serves a working game.
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

## Mode B — App, dev (authored, never built)

A real native window instead of a browser tab. Same game, same server; the
window is just a frame around it.

```bash
# 1. build the Go server into the Tauri sidecar slot  — NOT optional
scripts/build-sidecar.sh

# 2. run the shell
cd desktop
cargo tauri dev
```

- **Needs:** everything mode A needs, **plus** Rust >= 1.77.2, the Tauri CLI
  (`cargo install tauri-cli --version "^2.0.0" --locked`), and your platform's
  webview development packages (`libwebkit2gtk-4.1-dev` and friends on Linux;
  Xcode Command Line Tools on macOS; MSVC build tools + WebView2 on Windows).
  The full lists are in [`desktop/README.md`](../../desktop/README.md).
- **What actually happens:** the shell spawns the *same* Go binary as a
  sidecar with `-addr 127.0.0.1:0`, reads its stdout until the
  `DEXEL_LISTENING http://127.0.0.1:<port>` line, and opens the window on that
  URL. Closing the window SIGTERMs the server so it saves on the way out.
- **Why step 1 is not optional:** Tauri resolves `bundle.externalBin` to
  `binaries/dexel-server-<target triple>`. If the script has not run, there is
  no server to spawn.
- **You do not get a different game.** Same economy, same save file, same
  privacy boundary, same loopback-only posture. Packaging changed; nothing else
  did.

**Status: authored, pending a build environment.** The Rust was written against
the current official Tauri v2 docs and every config file validates, but
`cargo` has never run on it.

---

## Mode C — Installer (not shipped)

The end goal: a double-clickable app. No terminal, no browser, no Go
toolchain.

| Platform | Format | Blocked on |
|---|---|---|
| Linux x86_64 | `.AppImage`, `.deb` | a runner labelled `desktop-linux` (needs `webkit2gtk-4.1-dev`) |
| macOS arm64 / universal | `.app`, `.dmg` | a runner labelled `mac` |
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
writing code. The recommended next step
([F3-design.md](F3-design.md) FORK 1) is to register the owner's own Mac —
macOS is the primary platform per ADR 0010 and the hardware already exists.

Note when it does ship: the first bundles will be **unsigned and
unnotarized**. Gatekeeper and SmartScreen will warn; the apps still run.
Signing needs paid certificates the owner must supply and is tracked as a
follow-up ([F3-design.md](F3-design.md) §6).

---

## Mode D — Build from source (OSS)

Both paths, from a clean clone.

### D1 — Go only (browser mode). Works today.

```bash
cd app
go build -o dexel .          # Windows: go build -o dexel.exe .
./dexel                      # then open http://localhost:8080
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
- That closing the window leaves no orphaned `dexel-server` process.

Those four are the first build's gate. Until someone has seen them,
[`desktop/README.md`](../../desktop/README.md) is the authority on what is
real and what is merely written down.

---

## Which mode should I use?

- **Just want to play / develop the game?** Mode A. It works, it is one
  command, and it is what the whole repo is tested against.
- **Working on the desktop shell itself?** Mode B, on a machine with Rust.
- **Want to hand dexel to someone who does not have Go?** Mode C — which
  means registering a runner first.

## See also

- [ADR 0015 — Tauri desktop shell](../adr/0015-tauri-desktop-shell.md) — the
  decision and its alternatives.
- [F3-design.md](F3-design.md) — the full design, build matrix and phasing.
- [`desktop/README.md`](../../desktop/README.md) — build instructions and the
  verified/unverified split.
- [`.github/workflows/desktop.yml`](../../.github/workflows/desktop.yml) — the
  pipeline and how to activate the dormant jobs.
