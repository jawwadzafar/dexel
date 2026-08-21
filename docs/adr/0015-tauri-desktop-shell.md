# 0015 — Tauri desktop shell: Go server as a sidecar, webview on the local server

Status: accepted (2026-08-22) · Realizes ROADMAP F3 · Builds on ADR 0011
(Go + HTML/NES.css) and ADR 0007 (always-on-top)

## Context

F3 (docs/plan/ROADMAP.md) ships dexel as a native desktop app — no browser,
no terminal — for macOS, Windows and Linux on x86_64 and arm64, "with least
effort." ADR 0011 deliberately made the product a **Go backend + HTML/JS/
NES.css frontend** served over loopback, and froze the Rust path as legacy.
So F3 is a *packaging* problem, not a rewrite: wrap the existing binary +
frontend in a native window.

Three integration shapes were considered:

- **Rewrite the backend in Rust** so Tauri hosts it in-process. Rejected:
  directly contradicts ADR 0011; throws away the working Go engine, economy,
  honesty rules, persistence and privacy tests. Enormous effort, zero user-
  visible gain.
- **Embed the Go server as WASM** inside the webview. Not viable: the server
  binds a real loopback TCP socket, reads global input via cgo, and writes a
  save file — none of which a browser WASM sandbox permits. It would stop
  being the same program.
- **Run the compiled Go server as a Tauri sidecar** (`externalBin`), and
  point the webview at the loopback URL the server already serves. This is
  the least-effort path and the one the ROADMAP names. Chosen.

Two facts about the current code decide the finer points:

1. The frontend builds its WebSocket URL from `location.host`
   (`app/frontend/src/state/ws-client.ts`) and its asset URLs relative to
   the page. So if the webview *loads the server's own URL*, every URL is
   already correct and the WS Origin equals the server Host — i.e.
   same-origin, which the existing check accepts with **no change**.
2. The asset lookup already has a packaged-install escape hatch:
   `DEXEL_ASSETS_DIR` (`app/internal/assets/locate.go`, whose doc comment
   literally cites "a Wails bundle's Resources dir"), and `-public` already
   points the frontend at any directory. So bundling needs **no `go:embed`**.

## Decision

**Architecture — sidecar + webview-on-local-server.** A new top-level
`desktop/` Tauri (v2) project holds the Rust shell. On launch the shell
spawns the bundled Go binary (`externalBin` named
`dexel-server-$TARGET_TRIPLE`) as a sidecar, waits for it to report its
address, then creates the app window pointed at that loopback URL
(`WebviewUrl::External`). The webview is a thin frame around the same
frontend the server already serves. `app/` is untouched in structure; the
shell references the compiled binary, not the source tree.

**Port + handshake — ephemeral loopback, stdout handshake.** The sidecar is
launched with `-addr 127.0.0.1:0` (OS-assigned ephemeral port, so two
instances or a busy 8080 never collide) and, once its listener is bound,
prints one stable machine-readable line to stdout:
`DEXEL_LISTENING http://127.0.0.1:<port>`. The Rust shell reads sidecar
stdout, parses that line, and only then opens the window on that URL. This
handshake — rather than a fixed port, a Tauri command, or a health poll — is
chosen because the *Rust side* needs the port before it can create the
webview, and stdout is the one channel it already owns. The WS origin
patterns are derived from the **resolved** port, not the requested `:0`.

**Security — loopback-only posture preserved (ADR 0011 / B1).** The sidecar
still binds `127.0.0.1` only, never a wildcard. Because the webview loads the
server's own origin (`http://127.0.0.1:<port>`), the WS Origin equals the
request Host and the existing same-origin rule accepts it — **no
`-insecure-origin`, no wildcard, no code change to the origin check.** As
tight, defence-in-depth insurance (and to keep the "bundle the frontend as
tauri:// assets" option open) the Go server gains a `-allow-origin <origin>`
flag that appends *specific literal* origins to the accept list (e.g.
`tauri.localhost`), never `*`. Phase 1 does not use it. `-insecure-origin`
remains the wrong tool and is not used.

**Asset bundling — Tauri resources + explicit flags/env.** The built
frontend (`app/public/`) and the sprite PNGs (`assets/`) are declared as
Tauri `bundle.resources`. At runtime the shell resolves the resource
directory and launches the sidecar with `-public <res>/public` and
`DEXEL_ASSETS_DIR=<res>/assets`. This uses the escape hatches that already
exist for exactly this case; no embedding, no reliance on locate.go's
upward walk (which cannot work inside a packaged `.app`/`.msi`).

**Lifecycle — no orphaned Go process.** The shell keeps the sidecar's
`CommandChild` in managed state and kills it on `RunEvent::ExitRequested`/
`Exit` (and on window close). On Unix, prefer a graceful `SIGTERM` so the
server's shutdown handler runs its final save; a hard kill is acceptable
elsewhere because autosave already bounds loss to ~30s.

**Window/UX.** Title "dexel"; default and minimum inner size ~660×460 to fit
the fixed 640×400 root plus its sprint/ticker chrome; `alwaysOnTop: true`
(ADR 0007); native decorations in Phase 1 (the in-page wordmark titlebar
stays); no application menu bar. First run just opens the window on the game.

**Build matrix — phased, because bundling is host-OS-bound.** The Go sidecar
cross-compiles trivially (GOOS/GOARCH; `CGO_ENABLED=0` for the blind Linux/
Windows providers). Tauri *bundling*, however, must run on each target OS
(Linux→AppImage/.deb, Windows→MSI/NSIS+WebView2, macOS→.app/.dmg+WKWebView).
This repo has a **single self-hosted Linux x64 runner** and is **private**
(so GitHub-hosted macOS/Windows runners are not free). Therefore:

- **Phase 1 (now, current runner):** Linux **x86_64** bundle (AppImage +
  .deb). Buildable today.
- **Phase 2 (add runners):** macOS universal `.dmg` once a macOS runner is
  registered — recommended to be the owner's own Mac as a self-hosted runner
  (mac is the primary platform per ADR 0010, at zero added cost); Windows
  x86_64 MSI once a Windows runner exists.
- **Phase 3 (later):** Linux arm64 bundle (needs an arm64 host or a
  webkit2gtk:arm64 cross sysroot — harder than the Go cross-compile),
  Windows arm64.

**Signing/notarization — deferred.** A distributable macOS app needs an
Apple Developer cert + notarization; Windows needs Authenticode. Both need
paid certs the owner must supply, so they are out of scope for the initial
build and tracked as a follow-up. Unsigned bundles are fine for local/owner
use in Phases 1–2.

## Consequences

- The whole "which Tauri origin" question (`tauri://localhost` vs
  `http://tauri.localhost`, which vary by platform) is sidestepped: the page
  origin is the server's loopback origin. The `-allow-origin` flag is the
  tight lever if a future asset-protocol variant is ever adopted.
- No Go rewrite, no `go:embed`, no frontend change. F3 is additive: one new
  `desktop/` tree, a small ephemeral-port + stdout-handshake change in
  `app/main.go`, a sidecar build script, and CI jobs.
- A full "one CI job builds every OS" matrix is not real here; the honest
  unit of delivery is one bundle per registered runner. Phase 1 delivers
  Linux x86_64; mac/Windows are unblocked by adding runners, not code.
- Revisit triggers (inherited from ADR 0011): if the webview's resident
  memory breaks the "cheap to leave open all day" promise, or if the
  sidecar model proves fragile on a target OS.
