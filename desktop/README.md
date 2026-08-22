# `desktop/` — the dexel Tauri v2 shell

A native window around dexel's existing Go server. This directory is
**packaging only**; it contains no game logic, no economy, no privacy rules.
Those all live in `app/` and are entirely unchanged by it.

See [ADR 0015](../docs/adr/0015-tauri-desktop-shell.md) for the decision and
[`docs/plan/F3-design.md`](../docs/plan/F3-design.md) for the full design.
For which way to *run* dexel (browser, app, installer), see
[`docs/plan/RUN-MODES.md`](../docs/plan/RUN-MODES.md).

---

## Status: AUTHORED, NOT YET BUILT

**This code has never been compiled.** It was written on a machine with no
Rust toolchain and no webkit2gtk, so:

| | |
|---|---|
| Every Tauri API and config key used | **doc-verified** against the current official v2 docs (sources listed below) |
| `scripts/build-sidecar.sh` + the `DEXEL_LISTENING` handshake | **actually tested** (see [What *is* verified](#what-is-verified)) |
| `cargo build` / `cargo tauri build` / `cargo tauri dev` | **never run** |
| The app window opening on the game | **never seen** |

The first real build is the gate. Do not treat this as working software until
someone has run it on a machine with Rust — either a dev machine, or the
`desktop-linux`/`mac` CI runners once they exist (see
[`.github/workflows/desktop.yml`](../.github/workflows/desktop.yml), where both
bundle jobs are currently dormant by design).

---

## Layout

```
desktop/
  dist/index.html            placeholder `build.frontendDist` — never displayed;
                             the window opens on the sidecar's URL instead
  src-tauri/
    Cargo.toml               declares its OWN empty [workspace] (see below)
    build.rs                 tauri_build::build()
    tauri.conf.json          window, externalBin, icons (no bundle.resources —
                             the sidecar is self-contained, see EMBED-1 below)
    capabilities/default.json  minimal ACL; grants the loopback page NOTHING
    icons/                   PLACEHOLDER icon set (see Icons)
    binaries/                sidecar build artifacts — gitignored
    src/main.rs              thin entry point
    src/lib.rs               the whole lifecycle: spawn -> handshake -> window -> kill
```

### Why `src-tauri/Cargo.toml` declares an empty `[workspace]`

The repository root `Cargo.toml` is a Cargo workspace (`activity/`,
`companion/`, `tools/shotcap/` — the legacy Rust/Bevy track frozen by ADR
0011). A crate nested under a workspace root that is not a member makes cargo
refuse to build with *"current package believes it's in a workspace when it's
not"*. The empty `[workspace]` table makes this crate its own workspace root,
which also keeps its lockfile, feature resolution and `target/` directory
independent of that frozen track.

---

## How to build

### Prerequisites

1. **Rust** (>= 1.77.2 — `tauri-plugin-shell`'s MSRV), via
   [rustup](https://rustup.rs).
2. **Go** 1.27+ — the sidecar is the Go server from `app/`.
3. **Node** 24+ — only to rebuild the frontend bundle, and only if you have
   changed `app/frontend/src/*.ts` (the compiled bundle is committed).
4. **Platform webview deps**, per
   <https://v2.tauri.app/start/prerequisites/>:

   ```bash
   # Debian / Ubuntu
   sudo apt install libwebkit2gtk-4.1-dev build-essential curl wget file \
     libxdo-dev libssl-dev libayatana-appindicator3-dev librsvg2-dev

   # Arch
   sudo pacman -S --needed webkit2gtk-4.1 base-devel curl wget file openssl \
     appmenu-gtk-module libappindicator-gtk3 librsvg xdotool

   # Fedora
   sudo dnf install webkit2gtk4.1-devel openssl-devel curl wget file \
     libappindicator-gtk3-devel librsvg2-devel libxdo-devel
   sudo dnf group install "c-development"
   ```

   macOS: `xcode-select --install`. Windows: the MSVC C++ build tools plus
   WebView2 (shipped with Windows 11 / modern Windows 10).

5. **The Tauri CLI**:

   ```bash
   cargo install tauri-cli --version "^2.0.0" --locked
   ```

### Build, in order

**Step 1 — build the sidecar. This is not optional.** `bundle.externalBin`
points at `binaries/dexel-server`, and Tauri resolves that to
`binaries/dexel-server-<target triple>`. If the file is not there the build
fails (or, worse, bundles no server), so run this first and after every
change to `app/`:

```bash
# from the repository root
scripts/build-sidecar.sh              # just this host's triple
scripts/build-sidecar.sh --all        # every cross target
scripts/build-sidecar.sh --list       # show the triple -> GOOS/GOARCH table
```

**Step 2 — run or bundle:**

```bash
cd desktop
cargo tauri dev                       # dev: debug build, live window
cargo tauri build                     # release bundle for this host OS
cargo tauri build --bundles appimage,deb   # Linux, skipping rpm
cargo tauri build --bundles app,dmg        # macOS
```

`tauri.conf.json` sets `bundle.targets: "all"`, so plain `cargo tauri build`
attempts every format the host supports — including `rpm` on Linux, which
needs `rpmbuild`. Pass `--bundles` to narrow it, as CI does.

> `cargo tauri build` will also rebuild the frontend? **No.** There is no
> `beforeBuildCommand`: dexel's frontend bundle is committed
> (`app/public/js/dexel.js`) and is compiled into the sidecar binary by
> `go:embed` when `scripts/build-sidecar.sh` runs. If you changed the
> TypeScript, run `npm run build` in `app/frontend/` first **and then rebuild
> the sidecar** — otherwise the bundle you edited is not in the binary the
> bundler picks up. CI asserts the committed bundle has not drifted.

---

## How the shell works

All of it is in [`src-tauri/src/lib.rs`](src-tauri/src/lib.rs).

Since **EMBED-1** (`docs/plan/ROADMAP.md`) the sidecar is a *self-contained*
binary: `app/embed.go` compiles the frontend (`app/public/`) and the sprites
(`app/assets/`) into it with `go:embed`. So this shell hands it an address and
nothing else. What used to be step 1 — resolve the Tauri resource directory,
then pass `-public <res>/public` and `DEXEL_ASSETS_DIR=<res>/assets` — is
gone, along with the `bundle.resources` map that staged those trees and the
`resolve_roots()` fallback that guessed when staging did not happen. Those two
Go flags still exist, but only as explicit dev overrides for iterating on the
frontend/art without a rebuild; a packaged app never sets them.

```
setup()
  |
  1. app.shell().sidecar("dexel-server")
  |      .arg("-addr").arg("127.0.0.1:0")     loopback only, ephemeral port
  |      .spawn()  ->  (Receiver<CommandEvent>, CommandChild)
  |
  2. reader thread: drain stdout/stderr into the log forever; on the line
  |      "DEXEL_LISTENING http://127.0.0.1:<port>"  send the URL back
  |      (bounded wait: HANDSHAKE_TIMEOUT = 20s, then fail loudly)
  |
  3. WebviewWindowBuilder::new(app, "main", WebviewUrl::External(url))
  |      .title("dexel").inner_size(660,460).min_inner_size(660,460)
  |      .always_on_top(true).decorations(true).center().build()
  |
  v
run(|handle, event|)
     RunEvent::ExitRequested | Exit  ->  SidecarGuard::shutdown()
        unix : SIGTERM by pid, wait <=3s for the reader thread to observe
               termination, then escalate to CommandChild::kill()
        other: CommandChild::kill() straight away
```

Four things about that are load-bearing:

- **The shell, not the JS, needs the port** — it cannot create the webview
  until it knows the URL. So the port arrives on stdout, the one channel Rust
  already owns from `spawn()`.
- **The reader thread outlives the handshake.** It is the only reader of the
  child's pipes; abandoning them would eventually block the Go process on a
  full pipe buffer.
- **SIGTERM before SIGKILL.** `CommandChild::kill()` maps to
  `std::process::Child::kill()`, i.e. SIGKILL, which would skip main.go's
  `signal.Notify(SIGINT, SIGTERM)` handler and therefore its **final save**.
  Hence the `libc::kill(pid, SIGTERM)` first step on unix. On Windows the
  hard kill is accepted: autosave already bounds loss to ~30s.
- **`shutdown()` is idempotent** (the child is `Option::take`n), because
  `ExitRequested` and `Exit` both fire on a normal quit.

### Security posture

The webview loads `http://127.0.0.1:<port>` — the server's *own* origin. So
the frontend's `location.host`-derived WebSocket URL is already right, the
`Origin` header equals the request `Host`, and the server's existing
same-origin check accepts it with **no `-insecure-origin`, no wildcard
origin, and no change to `app/`**. The `-allow-origin` flag main.go grew for
this work is insurance for a future `tauri://` variant and is *not used*.

`capabilities/default.json` has **no `remote` field**, so that loopback page
is granted nothing at all through Tauri's ACL. It does not need to be — it
talks to the Go backend over plain HTTP/WebSocket and never uses Tauri IPC.

---

## Icons

`src-tauri/icons/` holds **placeholders**: a mint `d` with a cream cursor on a
dark plate, drawn as a 16x16 pixel-art master and nearest-neighbour upscaled,
using colours from `docs/art-direction.md`. They exist only so the bundlers
have files of the right names and sizes, and so this pipeline is not blocked
on the art task.

The real icon pass is **F3-design.md §8 T4, owned by game-artist**: generate
the set procedurally from the dexel identity via `tools/gen_assets.py`, per
ADR 0004 (every asset reproducible from its generator). When that lands,
these files are replaced in place — `bundle.icon` in `tauri.conf.json`
already lists the five filenames Tauri wants
(`32x32.png`, `128x128.png`, `128x128@2x.png`, `icon.icns`, `icon.ico`).

---

## What *is* verified

Tested for real on the authoring machine (Go 1.27, no Rust):

- `scripts/build-sidecar.sh --all` produces correctly triple-named binaries
  for `x86_64-unknown-linux-gnu`, `aarch64-unknown-linux-gnu`,
  `x86_64-pc-windows-msvc`, `aarch64-pc-windows-msvc`, and *loudly skips* the
  two darwin targets (they need cgo for the real macOS provider, so they are
  built on the Mac that builds the macOS bundle).
- The linux sidecar, launched exactly as `lib.rs` launches it — `-addr
  127.0.0.1:0` and no other argument, from an **empty directory** with no
  `public/` or `assets/` beside it — prints
  `DEXEL_LISTENING http://127.0.0.1:<port>` as its first stdout line, binds
  `127.0.0.1` only, serves `GET /` as 200 with `/api/health` reporting
  `publicOk: true` and `"source": "embedded"`, serves its sprites from the
  embedded copy, and on **SIGTERM** logs
  `shutting down: saving state...`, writes `state.json`, and exits 0 — which
  is precisely why the shell sends SIGTERM rather than SIGKILL.
- `tauri.conf.json` and `capabilities/default.json` parse as JSON;
  `desktop.yml` parses as YAML and every `run:` block parses as bash.

Doc sources every API/config key was checked against:

- <https://v2.tauri.app/develop/sidecar/> — `bundle.externalBin`, the
  `<name>-<target-triple>` rule, `app.shell().sidecar()`,
  `(Receiver<CommandEvent>, CommandChild)`, `CommandEvent::Stdout(Vec<u8>)`,
  the `shell:allow-execute` capability shape.
- <https://v2.tauri.app/reference/config/> — the config key names and nesting.
- <https://v2.tauri.app/security/capabilities/> — capability file format and
  auto-discovery from `capabilities/`.
- <https://v2.tauri.app/plugin/logging/> — default targets are exactly
  `[Stdout, LogDir { file_name: None }]`.
- <https://v2.tauri.app/start/prerequisites/> — the dependency lists above.
- <https://v2.tauri.app/reference/cli/> — `--bundles`, and
  `cargo install tauri-cli --version "^2.0.0" --locked`.
- docs.rs (tauri 2.11.x): `WebviewWindowBuilder`, `WebviewUrl`, `RunEvent`,
  `tauri_plugin_shell::process::{Command, CommandChild}`,
  `tauri_plugin_log::Builder`.

## What is NOT verified — the first build's checklist

Nothing below has been observed. In rough order of "most likely to be the
thing that is wrong":

1. **It compiles at all.** Type inference, trait bounds, and whether
   `CommandChild` is `Send + Sync` enough for `app.manage()`.
2. **`always_on_top` + a native frame** on each platform.
3. **The quit gate.** After closing the window, `pgrep -f dexel-server` must
   come back empty, and the save must be newer than the session start (proving
   SIGTERM won, not SIGKILL).
4. **The macOS `.icns`** is written by Pillow's pure-Python ICNS encoder, not
   `iconutil`. It reads back correctly, but no macOS bundler has consumed it.
5. **Whether the `shell:allow-execute` capability is needed at all.** The
   official docs state the ACL scope applies to frontend JS calls, not
   Rust-side ones; the entry is kept because the sidecar guide shows it and it
   costs nothing.
