# The Tauri shell's first Linux build

**Date:** 2026-08-24 · **Host:** Linux x86_64 (Ubuntu, GNOME 4x Wayland
session), rustc 1.98.0, tauri-cli 2.11.4, tauri 2.11.5, webkit2gtk 4.1
(2.52.3), GTK 3.24.52 · **Commit under test:** `328df4c` (a docs-only commit,
`793de49`, landed during the run and changed nothing under `desktop/`, `app/`
or `scripts/`, so these results hold unchanged at that commit too)

`desktop/` had been compiled and bundled once before, on macOS arm64
(2026-08-23). This was its **first build on Linux**, its first `.deb`/`.rpm`/
`.AppImage`, and the first time the window was opened and driven on a real
Linux desktop.

---

## Headline: the scaffold had zero compile errors

This run was scoped expecting to fix compile errors in a shell that had been
"authored blind". There were none. `cargo build` succeeded on the **first
attempt**, and the quality gates were clean without a single edit:

| gate | result |
|---|---|
| `cargo build` | clean, 50.96s (223 crates) |
| `cargo tauri build` (release + all bundles) | clean, 56.50s + bundling |
| `cargo fmt --check` | clean |
| `cargo clippy --all-targets -- -D warnings` | clean |
| `cargo test` | 3 passed, 0 failed |

The reason is worth recording so the next platform bring-up is scoped
correctly: **the blind-authoring bugs had already been paid for on macOS.**
The scaffold reached Linux having been compiled, bundled, run and then
substantially redesigned (the shell was rewritten from "spawn a private
server and SIGTERM it on window close" to "attach to the real detached
runtime"). Linux inherited that debugged code. The lesson is that the second
platform is cheap *when the first platform's build was real*; there was no
second round of scaffold bugs to find.

The only compiler output at all was one benign warning, from the linker rather
than from our code:

```
warning: linker stderr: ignoring deprecated linker optimization setting '1'
```

## What actually broke: the native Wayland backend segfaults

The one real defect, and it is not in Dexel's code.

Launched in a GNOME Wayland session (so GDK defaults to the `wayland`
backend), the shell dies immediately:

```
Segmentation fault (core dumped)
```

The log shows it got through every line of our own logic first — it resolved
the driver, started the runtime, and obtained the URL:

```
[INFO] driving /home/darkmirror/.local/bin/dexel
[INFO] no runtime running; starting the detached runtime via `start`
[INFO] started the dexel runtime at http://127.0.0.1:43641
[INFO] it keeps running after this window closes — `dexel stop` is what stops it
<segfault>
```

So it crashes inside window creation, in `tao`/GTK, below anything we wrote.
Forcing the X11 backend fixes it completely, and Xwayland is always present in
a GNOME Wayland session:

```bash
export GDK_BACKEND=x11
```

Things tried that did **not** help, recorded so nobody re-runs them:

- `WEBKIT_DISABLE_DMABUF_RENDERER=1` — the usual fix for webkit2gtk crashes on
  Linux. Still segfaults.
- `WEBKIT_DISABLE_COMPOSITING_MODE=1` — no effect on the crash.

**Not fixed in code, deliberately.** The fault is upstream (`tao` 0.35.3 /
webkit2gtk 2.52.3 under this compositor), and the honest options are a
documented env var or shipping a launcher that sets `GDK_BACKEND=x11` for
everyone — which would also opt macOS-style native-Wayland users out of
Wayland forever, on the evidence of one machine. It is written up in
`desktop/README.md` instead. A `.desktop` `Exec=` override is the obvious fix
if a second Linux machine reproduces it.

The `GDK_BACKEND=x11` path has one prerequisite that produces a *different*,
misleading failure if missed — without a usable `XAUTHORITY` the app exits
with `Authorization required, but no authorization protocol specified` and
`Failed to initialize GTK`, which reads like a broken GTK install. In this
session the right value was mutter's Xwayland auth file
(`/run/user/1000/.mutter-Xwaylandauth.*`).

## The environment trap that cost the most time

`~/.local/bin/pkg-config` on this machine is an unrelated Python script, and
it sits earlier on `PATH` than `/usr/bin`. With it in the way, the build fails
insisting `webkit2gtk-4.1` is absent while `/usr/bin/pkg-config --modversion
webkit2gtk-4.1` happily prints `2.52.3`. The `pkg-config` crate reads an
explicit override, which is the least invasive fix — it does not require
touching the developer's own shim:

```bash
export PKG_CONFIG=/usr/bin/pkg-config
```

## A documentation bug, found and fixed

Both `desktop/README.md` and two doc comments in `src-tauri/src/lib.rs` stated
that `dexel status --json` **exits 1** when no runtime is running, and built a
paragraph of reasoning on top of it. Measured:

```
$ DEXEL_HOME=/tmp/empty dexel status --json >/dev/null; echo $?
0
```

`cmdStatus` in `app/cmd_lifecycle.go` is explicit that this is intended: *"It
exits 0 whether or not a runtime is running ... making 'not running' an error
exit would force every such caller to treat a perfectly normal answer as a
failure."*

The claim was wrong and cost nothing, because the code never read the exit
code — it reads the `running` field, which is correct under either contract.
Fixed in both files rather than left, since the whole purpose of those
comments is to stop a future reader from "handling" the exit code.

## Bundles

`cargo tauri build` produced all three Linux formats with no configuration
changes (`bundle.targets: "all"`; `rpmbuild` was present).

| artifact | size |
|---|---|
| `target/release/dexel-desktop` (the shell) | 9.3 MB (9,698,776 B) |
| `bundle/deb/Dexel_0.1.0_amd64.deb` | 8.6 MB |
| `bundle/rpm/Dexel-0.1.0-1.x86_64.rpm` | 8.6 MB |
| `bundle/appimage/Dexel_0.1.0_amd64.AppImage` | 84 MB |

The AppImage is ~10x the `.deb` because it vendors GTK/WebKit and their
dependency closure via `linuxdeploy`, where the `.deb` declares them
(`Depends: libwebkit2gtk-4.1-0, libgtk-3-0`). AppImage bundling downloads
`linuxdeploy` and its GTK/GStreamer plugins on first run, so that build needs
network access.

### The case-collision invariant, now proven on a second platform

`mod bundle_layout`'s reason for existing is that two executables landing in
one flat directory can silently overwrite each other. The `.deb` puts both in
`/usr/bin`, and both survived at full size:

```
-rwxr-xr-x  12603552  usr/bin/Dexel          <- the Go daemon
-rwxr-xr-x   9698776  usr/bin/dexel-desktop  <- this shell
```

Linux ext4 is case-sensitive so they could not have collided here regardless,
but this confirms the names Tauri actually emits on the `.deb` path match what
the test asserts — and the `.deb` copies the main binary *first* and
`externalBin` *second*, the opposite of macOS, so it is the ordering that
would have destroyed the daemon rather than the shell.

## Lifecycle verification

Run twice: once on the debug build, once on the packaged AppImage. Both used a
throwaway `DEXEL_HOME` (see *Isolation* below).

| check | result |
|---|---|
| Fresh start, no runtime | `start` detaches a real runtime; URL obtained; window opens |
| Runtime count while the window is open | exactly **one** |
| Runtime's parent | the session manager, **not** `dexel-desktop` — genuinely detached |
| Second launch while running | `attaching to the running dexel runtime ... (this window owns nothing)`; no second server |
| Window geometry | `660x460`, titled "Dexel", centred, always-on-top, native frame |
| Window close | shell exits; window gone; **runtime still alive** — the contract |
| Close log line | `window closed; the dexel runtime keeps running (use 'dexel stop' to stop it)` |
| `dexel stop` | `stopping dexel (pid ...) via the lifecycle endpoint`; runtime logs `shutting down: saving state...` |
| After stop | no `dexel-desktop`/`Dexel`/`dexel-server` process, port released |

Note the contract verified here is the **current** one — *the window is a
VIEW* — not the older "SIGTERM the sidecar on window close" design. Closing
the window terminating the runtime would now be the bug.

### A dev-only process-name collision worth knowing

`cargo build` names the debug binary after the Cargo package, so it is
`target/debug/dexel` — the same `comm` as the Go runtime. During development
`pgrep -x dexel` therefore cannot tell the shell from the runtime, and a
`pkill -x dexel` kills both. The bundle is unaffected: `mainBinaryName` makes
it `dexel-desktop`, and `pgrep -x dexel-desktop` vs `pgrep -x dexel` separate
them cleanly. Use `ps -o args` rather than `comm` when checking a dev build.

(Related trap hit during this run: `pgrep -f 'dexel runtime'` counts *itself*,
reporting 2 runtimes when there was 1. Exact-name matching only.)

## Evidence that the window really renders the game

Three independent lines, because the first capture route lied.

**1. The window exists, with the right geometry.** From the X server's own
tree — note `WM_CLASS` is `dexel-desktop` for the packaged build, confirming
`mainBinaryName` took effect:

```
0x800114 "Dexel": ("mutter-x11-frames")     710x547+605+282   <- frame
   0xc00003 "Dexel": ("dexel-desktop")      660x460+25+62     <- our window
```

**2. The webview really loaded it.** Seven keep-alive TCP connections from
`WebKitNetworkPr` to the runtime's port, stable across repeated sampling —
the document, stylesheet, script, sprite assets and the state WebSocket:

```
ESTAB 127.0.0.1:35858  127.0.0.1:40905  users:(("WebKitNetworkPr",pid=2322237,fd=20))
... (7 total)
```

**3. The window's own pixels.** `XGetImage` on the window returns a flat
`#f6f5f4` — the compositor redirects the window, so its contents live in an
offscreen pixmap rather than in the window drawable. That flat fill is a
capture artifact and **not** a blank window; mistaking it for one would have
been the wrong conclusion. `XCompositeNameWindowPixmap` gets the real pixmap,
and reading *that* yields the finished scene (57 distinct colours): the desk,
the monitor with scrolling build output, the companion sprite mid-keystroke,
the `SPRINT: Fix Bug #404` bar, the status ticker and the open menu overlay
(`[S] STORE`, `[A] ACTIVITY`, `[H] HISTORY`, `[W] SESSIONS`, `[P] PAUSE`).

Two capture routes that did **not** work here, for the next person:
`org.gnome.Shell.Screenshot` over D-Bus returns `AccessDenied` for an
unsandboxed caller on this GNOME, and `XGetImage` on the root window fails
`BadMatch` under Xwayland (there is no composited X root to read).

**Interaction, verified for real.** A `Return` keypress synthesised through
**XTEST** (not `XSendEvent`, which webviews ignore) into the focused window
dismissed the first-launch onboarding modal. The proof that it round-tripped
rather than merely repainting: a *separate* browser client, connected
afterwards to the same URL, saw the post-onboarding state with the companion
named — so the keystroke reached the webview, ran the frontend's handler, and
was committed on the server, which is the sole source of truth.

The developer was at the desktop while this ran and interacted with the test
window himself (naming a companion `jwd` and opening the menu around 15:03) —
independent confirmation that the window was visible and usable, and the
reason the captured scene shows a named companion rather than the onboarding
modal.

## Isolation: the developer's real save was never opened

Every process launched here ran with `DEXEL_HOME` pointed at a scratch
directory (`DEXEL_HOME` overrides `StateDir` wholesale on every platform, per
`app/internal/paths`), so each test got its own `state.db`, `runtime.json` and
`runtime.lock`. Three scratch homes were used across the runs; each shows its
own `state.db`, which is where the writes went.

Confirmed afterwards against `~/.config/dexel`:

- `config.json`, `state.json`, `state.json.imported` — **byte-identical**
  (SHA-256 unchanged from the pre-run snapshot).
- `state.db`, `logs/runtime.log`, `runtime.json` — changed, and attributable
  to *the developer's own runtime*: pid 2331995, started 14:47:12 with no
  `DEXEL_HOME`, holding `~/.config/dexel/runtime.lock` open, its log recording
  its own start/stop cycles at 11:34, 14:28 and 14:47. Its economy value
  (`dev_cash=65`) is unchanged across all of them.
- That runtime was left running, untouched, along with two unrelated
  pre-existing `dexel-release` processes from `/tmp/dexel-eval`.

## Reproducing this

```bash
# 0. toolchains
source ~/.cargo/env
export PATH="$HOME/go-toolchain/go/bin:$PATH"
cargo install tauri-cli --version "^2.0.0" --locked   # -> tauri-cli 2.11.4

# 1. the sidecar FIRST — bundle.externalBin resolves binaries/Dexel-<triple>
bash scripts/build-sidecar.sh
#    -> desktop/src-tauri/binaries/Dexel-x86_64-unknown-linux-gnu (12.6 MB)

# 2. work around the pkg-config shim (see above) for every cargo command
export PKG_CONFIG=/usr/bin/pkg-config

# 3. compile and gate
cd desktop/src-tauri
cargo build
cargo fmt --check
cargo clippy --all-targets -- -D warnings
cargo test

# 4. bundle: deb + rpm + AppImage
cargo tauri build

# 5. run it — X11, throwaway state, real desktop
export DISPLAY=:0 GDK_BACKEND=x11
export XAUTHORITY=/run/user/1000/.mutter-Xwaylandauth.*   # mutter's Xwayland auth
export XDG_RUNTIME_DIR=/run/user/1000
export DEXEL_HOME=/tmp/dexel-scratch                     # NEVER the real save
./target/release/bundle/appimage/Dexel_0.1.0_amd64.AppImage

# 6. tear down (exact names — never pgrep -f)
DEXEL_HOME=/tmp/dexel-scratch dexel stop
pgrep -x dexel-desktop; pgrep -x dexel; pgrep -x Dexel
```

`scripts/build-sidecar.sh` needed no changes: it already emits the
`Dexel-<triple>` name Tauri expects, and it cleaned up the four stale
`dexel-server-*` artifacts from the retired base name on its own.

## Still not done

- **Windows** — never built.
- **Signing / notarization** — nothing is signed on any platform.
- **Native Wayland** — see above; Linux runs under Xwayland for now, and this
  is the one finding that deserves a second machine's opinion before it is
  treated as settled.
- **CI** — these bundles were built by hand on a developer machine;
  `.github/workflows/desktop.yml` has still never produced a Linux bundle
  (`docs/plan/RUN-MODES.md` mode C).
