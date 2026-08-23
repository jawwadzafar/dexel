# F3 — Tauri desktop shell (design)

**Status:** design only (no implementation). Realizes ROADMAP F3.
**Decision record:** ADR 0015. **Scope owned by this doc:** the packaging of
dexel as a native desktop app. Backend behaviour, economy, privacy and the
frontend are unchanged except for the two small, explicitly-scoped server
changes in §2 and §8.

Goal (owner): ship dexel as a native desktop app — no browser, no terminal —
for macOS, Windows and Linux on x86_64 **and** arm64, with least effort.
Wrap the EXISTING Go backend + web frontend; do **not** rewrite in Rust
(ADR 0011 deliberately pivoted away from Rust).

---

## OWNER-DECISION FORKS (read first)

Two forks need an owner call. Each has a recommended default the rest of this
plan assumes; say the word to change either.

### FORK 1 — Which platforms first, and how do we get runners?

The blunt constraint: CI is **one self-hosted Linux x64 runner**
(`jwdlab-runner`, labels `self-hosted, Linux, X64, darkmirror`) and the repo
is **private**, so GitHub-hosted macOS/Windows runners are **not free**.
Tauri bundling must run on the target OS. So a Mac `.dmg` and a Windows
`.msi` cannot be produced on the current runner, full stop — even though the
Go sidecar for those OSes cross-compiles here fine. And note the tension:
the product is **mac-first** (ADR 0010) and the owner is on macOS, yet mac is
exactly the bundle we *cannot* build today.

**Recommended default:**
1. Ship the **Linux x86_64** desktop bundle **now** on the existing runner
   (proves the whole architecture end-to-end).
2. Register the **owner's own Mac** as a self-hosted runner (labels
   `self-hosted, macOS, ARM64`) to unblock the **macOS universal `.dmg`** —
   this is the primary user platform and costs nothing extra (the hardware
   already exists). The release workflow already contains a `mac`-label gate
   concept (release.yml) to slot this into.
3. Defer **Windows** until a Windows machine is available as a runner.
4. Do **not** pay for GitHub-hosted runners unless/until the repo goes public
   or the owner accepts CI minutes cost.

Alternative if the owner wants mac/Windows in CI immediately without
providing machines: make the repo public (free GitHub-hosted `macos-14`
[arm64] + `windows-latest` runners) or buy CI minutes. Not recommended over
using the Mac already on the desk.

### FORK 2 — How does the webview get the frontend?

- **(Recommended) Point the webview at the running local server**
  (`WebviewUrl::External("http://127.0.0.1:<port>")`). Least effort; the
  frontend already builds every URL from `location.host`, so it "just works";
  the WS Origin equals the server Host, so the loopback origin check accepts
  it **with zero code change and no `-insecure-origin`**.
- **(Alternative) Bundle the frontend as `tauri://` assets.** Requires (a)
  extending the WS origin allow-list to the Tauri origin *and* (b) changing
  the frontend so its WS/asset URLs target the sidecar's loopback host
  instead of `location.host`. More moving parts, more risk, no benefit here.

**Recommended default: the first.** This plan is written for it; the origin
allow-list flag in §2/§8 is built anyway as tight insurance and to keep the
alternative open.

---

## 1. Integration architecture

```
 ┌──────────────────────────────────────────────────────────┐
 │  desktop/  (NEW Tauri v2 app — the native window)          │
 │                                                            │
 │   src-tauri/ (Rust shell)                                  │
 │     1. spawn sidecar: dexel-server-$TARGET_TRIPLE          │
 │          args: -addr 127.0.0.1:0                           │
 │                -public   <resourceDir>/public             │
 │          env : DEXEL_ASSETS_DIR=<resourceDir>/assets      │
 │     2. read sidecar stdout until:                          │
 │          "DEXEL_LISTENING http://127.0.0.1:<port>"        │
 │     3. open window -> WebviewUrl::External(that URL)       │
 │     4. keep CommandChild; kill it on app exit             │
 │                                                            │
 │   webview  ── HTTP + WS on 127.0.0.1:<port> ──►  sidecar   │
 └──────────────────────────────────────────────────────────┘
                 (sidecar = the UNCHANGED Go binary from app/)
```

**Why sidecar (vs the alternatives).**
- *Rewrite in Rust*: rejected — contradicts ADR 0011, discards the working
  engine/economy/honesty/privacy code, huge effort, no user gain.
- *Embed as WASM*: not viable — the server binds a real loopback socket,
  reads global input via cgo, and writes a save file; none survive a browser
  WASM sandbox.
- *Sidecar*: the binary we already build, run verbatim, wrapped in a window.
  Least effort, and the path the ROADMAP names.

**Sidecar lifecycle (no orphaned Go process).**
- *Spawn:* in the Tauri `setup` hook via the shell plugin
  (`app.shell().sidecar("dexel-server")…spawn()`), returning an event
  stream + a `CommandChild`.
- *Readiness:* consume `CommandEvent::Stdout` lines until the
  `DEXEL_LISTENING` line; parse the URL; then build the window. (Also
  forward sidecar stdout/stderr to the Tauri log so a packaged crash is
  diagnosable — mirrors the loud-failure ethos already in main.go.)
- *Terminate:* **SUPERSEDED by ARCHITECTURE.md Decision 17 (implemented
  2026-08-23).** This section described the shell owning the server and
  killing it on window close. It no longer does: the shell attaches to the
  detached runtime (`status --json`, `start` if absent) and terminates
  nothing, because closing a window must never stop activity capture. The
  `CommandChild`, the SIGTERM/SIGKILL escalation and the `libc` dependency
  are gone. Everything else in this section (spawn mechanics, readiness,
  forwarding output to the Tauri log) still describes the code, now applied
  to short-lived CLI calls rather than a long-lived child.

**Port strategy — ephemeral loopback.** Launch with `-addr 127.0.0.1:0`.
The OS assigns a free port, so a stray port-8080 process or a second
instance never collides. The chosen port is discovered by the shell from the
handshake line, not guessed.

**How the webview learns the port — stdout handshake.** The *Rust shell*,
not the JS, needs the port (it must create the webview on that URL). stdout
is the channel it already owns from `spawn()`. Chosen over: a fixed port
(collisions), a Tauri command the JS calls (JS can't help — the shell needs
the port first), or health-polling a guessed port (racy). The frontend never
learns or cares about the port: it loads from the URL and uses
`location.host` from there on.

---

## 2. Security — the loopback-only posture is preserved

Non-negotiables from ADR 0011 / the B1 review (ORCHESTRATION-LOG): **no
wildcard bind, real Origin checks, loopback only.** F3 keeps all three:

- **Bind:** sidecar still binds `127.0.0.1` only (`-addr 127.0.0.1:0`). Never
  `0.0.0.0`, never a LAN/tailnet address.
- **Origin:** because the webview loads `http://127.0.0.1:<port>` (FORK 2
  recommended), the browser sends `Origin: http://127.0.0.1:<port>`, whose
  host equals the request Host — the same-origin case
  `nhooyr.io/websocket` accepts unconditionally. **No change to the origin
  check, no `-insecure-origin`, no wildcard.** The existing
  `wsOriginPatterns` (127.0.0.1:port + localhost:port) continues to cover the
  127.0.0.1↔localhost alias; it must now be derived from the **resolved**
  port, since the requested port is `0` (see §8, Go change).
- **`-insecure-origin` is not used.** It accepts *any* Origin and is the
  wrong tool; it stays a documented last resort for genuinely origin-less
  embedded webviews only.
- **Tight allow-list flag (insurance, not used in Phase 1).** Add
  `-allow-origin <origin>` (repeatable) that appends *specific literal*
  origins to the accept list — e.g. `tauri.localhost` — never `*`. This is
  the correct, tight lever if the alternative in FORK 2 is ever adopted, or
  if a platform's webview surprises us with a non-loopback Origin. Verified
  Tauri v2 origins for that case: `tauri://localhost` (macOS/Linux) and
  `http://tauri.localhost` (Windows), configurable — but irrelevant under the
  recommended FORK 2 path.

Net: the desktop app is *more* contained than the browser build — the server
is reachable only for as long as the app is open, and only over loopback.

---

## 3. Asset bundling

> **Superseded by EMBED-1** (`docs/plan/ROADMAP.md`): this section describes
> the original no-`go:embed` design. The implementation instead embeds the
> frontend and sprites (moved to `app/assets/`) directly into the Go binary
> via `app/embed.go`, so there is no `bundle.resources` map, no resource
> directory to resolve, and the sidecar is launched with no `-public`/
> `DEXEL_ASSETS_DIR` at all in a packaged build. Those two flags remain as
> dev overrides only. See `desktop/README.md`'s "Asset bundling" section
> for the current story; the rest of this section is left as the historical
> record of the design that was superseded.

The sidecar's working directory inside a packaged `.app`/`.msi`/AppImage is
not the repo, so locate.go's upward-walk cannot find `assets/`, and `-public`
would resolve `./public` to the wrong place. Both are solved **without
embedding**, using escape hatches that already exist:

- Declare the built frontend and the sprites as Tauri **`bundle.resources`**
  in `tauri.conf.json` (paths relative to `src-tauri/`, e.g.
  `../../app/public` and `../../assets`).
- At runtime the Rust shell resolves the resource directory
  (`app.path().resource_dir()`) and launches the sidecar with:
  - `-public <resourceDir>/public` (frontend), and
  - env `DEXEL_ASSETS_DIR=<resourceDir>/assets` (sprites).
- `app/internal/assets/locate.go` already treats `DEXEL_ASSETS_DIR` as a
  verbatim, no-search override whose doc comment literally names "a Wails
  bundle's Resources dir" as its reason to exist. `/api/health` already
  surfaces the resolved assets dir + `publicOk`, so a mis-bundled resource is
  diagnosable, not silent.

No `go:embed` is introduced: it would be a larger Go change for no gain over
the flag+env path the code was already built to support. **Prerequisite:**
the frontend bundle must be freshly built (`app/frontend` → `npm run build`)
before the Go resources are packaged, exactly as CI's drift check enforces
today.

---

## 4. Build matrix & the honest constraint

**The Go sidecar cross-compiles trivially.** One script builds every target
with `GOOS`/`GOARCH`, `CGO_ENABLED=0` for the blind Linux/Windows providers
(honest per ADR 0010). The **one** exception: the macOS native provider uses
cgo (ADR 0011's `CGEventSource…` shim), so the darwin sidecar with real
capture must be built **on macOS** — which is exactly where the mac *bundle*
is built too, so no extra burden.

**Tauri bundling is host-OS-bound.** You build Linux bundles on Linux,
Windows on Windows, macOS on macOS. There is no honest "one job builds all
three." Mapped against reality (one private-repo Linux x64 runner):

| Target | Go sidecar | Tauri bundle | Buildable where | Phase |
|---|---|---|---|---|
| Linux x86_64 | `linux/amd64`, CGO off | AppImage + .deb | **current runner now** | **1** |
| Linux arm64 | `linux/arm64`, CGO off (trivial) | AppImage/.deb | needs arm64 host or webkit2gtk:arm64 cross sysroot (hard) | 3 |
| macOS arm64 | `darwin/arm64`, **cgo → on mac** | .app/.dmg | needs a macOS runner | 2 |
| macOS x86_64 | `darwin/amd64`, **cgo → on mac** | (fold into universal) | needs a macOS runner | 2 |
| Windows x86_64 | `windows/amd64`, CGO off | MSI/NSIS (+WebView2) | needs a Windows runner | 2 |
| Windows arm64 | `windows/arm64`, CGO off | MSI/NSIS | needs Windows arm64 tooling | 3 |

Target-triple mapping for the sidecar naming (`dexel-server-$TARGET_TRIPLE`):

| Rust target triple | GOOS/GOARCH |
|---|---|
| `x86_64-unknown-linux-gnu` | linux/amd64 |
| `aarch64-unknown-linux-gnu` | linux/arm64 |
| `aarch64-apple-darwin` | darwin/arm64 |
| `x86_64-apple-darwin` | darwin/amd64 |
| `x86_64-pc-windows-msvc` | windows/amd64 (`.exe`) |
| `aarch64-pc-windows-msvc` | windows/arm64 (`.exe`) |

macOS "universal": build both darwin sidecars and `lipo` them, or ship the
two arch bundles; the universal `.dmg` is the least-friction distributable
once a Mac runner exists.

---

## 5. Window / UX

- **Title:** `dexel`. No application menu bar (the in-page wordmark titlebar
  + hamburger menu stay as the UI chrome).
- **Size:** the root is a fixed 640×400 canvas plus its sprint panel/ticker
  chrome (~660×430 overall). Default **inner size 660×460**; **min size the
  same**, so the fixed-pixel layout is never clipped. `resizable: true`
  (extra space letterboxes around the fixed canvas; the layout does not
  reflow).
- **Always-on-top:** `alwaysOnTop: true` (ADR 0007 — a companion the editor
  buries never gets seen). A runtime toggle can follow later.
- **Decorations:** native window frame in Phase 1 (least effort; gives
  drag/close for free). A frameless "floating companion" look with a custom
  drag region is a Phase-2 polish toward the original vision, not Phase 1.
- **First run:** the window simply opens on the game — sidecar spawns, port
  handshake completes, webview loads. No wizard, no terminal, no browser.
- **Icon:** app icons are required per platform (`.icns` for macOS, `.ico`
  for Windows, a PNG set for Linux + the Tauri icon pipeline). None exist in
  the repo yet (only screenshot PNGs in `docs/images/`). **Asset task for
  game-artist** (see §8) — derive from the dexel wordmark / dev sprite via
  `tools/gen_assets.py` so it stays reproducible per ADR 0004.

---

## 6. Code-signing / notarization — deferred

Required for a *distributable* app but **out of scope for the initial build**:
- **macOS:** Apple Developer ID cert + `codesign` + notarization/stapling.
- **Windows:** Authenticode signing cert.

Both need **paid certificates the owner must provide**; without them users
see Gatekeeper / SmartScreen warnings but the apps run (fine for local/owner
use in Phases 1–2). Flagged as a follow-up; the cert/notarization flow is
**not** designed here. Once certs exist, Tauri's bundler has first-class
config hooks for both — a config+secrets task, not an architecture change.

---

## 7. Repo layout

A new top-level `desktop/` tree; `app/` is not moved or restructured.

```
dev-companion/
  app/                       # UNCHANGED structure (Go backend + frontend)
    main.go                  #   + ephemeral-port & stdout handshake (§8)
    ...
  # assets/ moved to app/assets/ and is embedded into the Go binary
  # (EMBED-1) rather than bundled as a Tauri resource — see §3's note.
  desktop/                   # NEW — the Tauri shell
    src-tauri/
      Cargo.toml
      tauri.conf.json        # window, externalBin, icons (no bundle.resources
                             #   — the sidecar is self-contained, see EMBED-1)
      capabilities/          # shell-plugin sidecar permission (tight)
      icons/                 # .icns/.ico/PNG set (game-artist task)
      binaries/              # dexel-server-$TARGET_TRIPLE (build artifact,
                             #   gitignored; produced by scripts/build-sidecar.sh)
      src/main.rs            # spawn sidecar, handshake, window, kill-on-exit
  scripts/
    build-sidecar.sh         # NEW — cross-compile Go into binaries/
  .github/workflows/
    desktop.yml              # NEW — Linux bundle now; gated mac/win jobs
```

The shell depends only on the *compiled* Go binary via `externalBin`, never
on `app/`'s source, so the two trees stay independently ownable.

---

## 8. Fan-out — implementation tasks by ownership

Exclusive file ownership per the orchestration playbook. No two tasks write
the same file.

**T1 — Tauri scaffold (owner: desktop engineer; owns `desktop/`).**
`Cargo.toml`, `tauri.conf.json` (window §5, no `bundle.resources` — §3's
note; superseded by EMBED-1's self-contained sidecar,
`externalBin: ["binaries/dexel-server"]`, icons), a tight shell-plugin
capability allowing only the sidecar, and `src/main.rs`: spawn sidecar with
the §1 args/env, parse the `DEXEL_LISTENING` line, open the window on that
URL, store the `CommandChild`, kill on exit (SIGTERM on Unix). *(The last two
clauses are superseded by Decision 17 — the shell attaches to the runtime and
kills nothing; see §1's Terminate note.)*

**T2 — Go changes (owner: backend engineer; owns `app/main.go`).** Small and
surgical:
1. Bind explicitly: `net.Listen("tcp", *addr)` then `httpSrv.Serve(ln)`
   (replacing `ListenAndServe`), so `-addr 127.0.0.1:0` yields an ephemeral
   port that is then known.
2. After binding, print one stable line to **stdout**:
   `DEXEL_LISTENING http://<ln.Addr()>`.
3. Derive `wsOriginPatterns` from the **resolved** `ln.Addr()` port, not the
   requested `*addr` (which may be `:0`).
4. Add `-allow-origin <origin>` (repeatable) appending *literal* origins to
   `wsOriginPatterns` — never `*` (insurance per §2; unused in Phase 1).
   Update the flag docs. Keep a unit test that `:0` → a real port is printed
   and that origin patterns reflect the resolved port.

**T3 — Sidecar build script (owner: build engineer; owns
`scripts/build-sidecar.sh`).** Cross-compile `./app` for each target with the
§4 GOOS/GOARCH table, `CGO_ENABLED=0` for linux/windows (cgo on macOS only,
run on the mac), and emit `desktop/src-tauri/binaries/dexel-server-$TRIPLE`
(`.exe` on windows). Gitignore `binaries/`.

**T4 — Icons (owner: game-artist; owns `tools/gen_assets.py` +
`desktop/src-tauri/icons/`).** Generate the platform icon set (.icns/.ico/PNG)
procedurally from the dexel identity, per ADR 0004; wire into Tauri's icon
config.

**T5 — CI (owner: CI engineer; owns `.github/workflows/desktop.yml`).** A
`desktop-linux` job on the `darkmirror` runner: build frontend → build the
linux/amd64 sidecar via T3 → `tauri build` → upload the AppImage/.deb. Add
**gated** `desktop-macos` (label `mac`) and `desktop-windows` (label
`windows`) jobs that no-op with a warning until those runners exist (mirror
release.yml's existing `mac`-gate pattern). Do not pretend one job builds all
OSes.

---

## 9. Phased plan & exit criteria

### Phase 1 — Linux x86_64 desktop app (buildable NOW on the current runner)
Scope: T1, T2, T3 (linux/amd64), T4 (at least a placeholder icon), the
`desktop-linux` CI job (T5).
**Exit criteria:**
- `scripts/build-sidecar.sh` produces
  `desktop/src-tauri/binaries/dexel-server-x86_64-unknown-linux-gnu`.
- `app/` still green: `go vet` + `go test -race` pass; the `:0` ephemeral
  port + `DEXEL_LISTENING` handshake unit-tested; origin patterns reflect the
  resolved port; **no** `-insecure-origin`, **no** wildcard bind.
- `tauri build` on the Linux runner emits an AppImage (and/or .deb).
- **In-game gate (own eyes):** launch the built bundle in this environment;
  the window opens showing the real game (scene + wordmark + seated chair),
  the sidecar is bound on a 127.0.0.1 ephemeral port, the WS connects
  same-origin with zero console errors, and buy→equip works. If a full
  bundle can't run headless here, the fallback gate is: Go sidecar + `tauri
  dev` (debug) launched, window renders the game, screenshot verified.
- **Quit gate:** closing the window terminates the Go sidecar — verified no
  orphaned `dexel-server` process remains (`pgrep` clean after quit).

### Phase 2 — macOS + Windows (needs added runners; FORK 1)
Scope: register the owner's Mac as a `mac` self-hosted runner; enable
`desktop-macos` (universal `.dmg`, cgo darwin sidecar built on the Mac);
register a Windows runner and enable `desktop-windows` (MSI/NSIS + WebView2).
**Exit:** a `.dmg` opens on macOS and a `.msi`/NSIS installs on Windows, each
showing the game with the sidecar killed on quit; unsigned is acceptable
here (Gatekeeper/SmartScreen warnings expected, §6).

### Phase 3 — arm64 breadth + signing
Scope: Linux arm64 bundle (arm64 host or webkit2gtk:arm64 cross sysroot);
Windows arm64; then code-signing/notarization once the owner supplies certs
(§6). **Exit:** arm64 bundles produced; signed/notarized artifacts install
without security warnings.

---

## 10. Non-goals / preserved invariants
- No backend rewrite; no `go:embed`; no frontend change under the
  recommended FORK 2 path.
- Privacy (ADR 0002/0009) and honest mechanics (ADR 0005/0010) are untouched
  — F3 changes packaging, not signals or economy.
- Loopback-only + real origin checks (ADR 0011 / B1) preserved and, in the
  desktop app, tightened (server reachable only while the app is open).
