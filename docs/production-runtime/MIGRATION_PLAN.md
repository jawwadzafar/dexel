# Dexel — migration plan: today's repo → first production release

Companion to `ARCHITECTURE.md`, `PLATFORM_NOTES.md`, `RELEASE_PIPELINE.md`.
ROADMAP style: ordered, each step independently landable, each with an exit
criterion. Every step is tagged:

* **[CODE]** an implementation task, with exclusive file ownership named
* **[INFRA]** an owner action — domains, buckets, secrets, runners
* **[DEFER]** named so it does not creep

Nothing here is started. This is the order, not a status board.

---

## Sequencing constraints (read before reordering)

1. **EMBED-1 must be landed.** Every step assumes one self-contained binary
   (`app/embed.go`, `scripts/build-release.sh`'s single-binary archives). It
   appears done in the tree; confirm before PR-3.
2. **DB-GO-2 / SQLite has landed** (`app/internal/store/db.go`, ADR 0016). PR-1
   edits `store.DefaultPath`/`ConfigPath` — it must not run concurrently with any
   other task inside `app/internal/store/`.
3. **Only ONE schema-bumping task in flight at a time.** `docs/plan/P2-design.md`
   Fork P2-B claims `CurrentSchema` **5 → 6 for P2 (Sessions)**, since P2 started
   first and PR-5 had not. PR-5 now bumps `CurrentSchema` **6 → 7** instead (and
   adds `pausedSeconds` to P2's session delta set, `docs/plan/P2-design.md`
   §2.3/§5.6 — see PR-5 below). The PRODUCT-EVOLUTION phases (P1 Identity, P2
   Sessions, …) each bump the schema too. Two agents bumping to the same next
   number in parallel produces two incompatible schemas. Serialize: PR-5 **or**
   a product phase, never both.
4. **PR-1 through PR-8 are independent of P1+.** The product phases touch
   `game`/frontend features; this track touches `main`, `paths`, CI, and
   `desktop/`. The only overlap is PR-5's wire/save fields — see (3).
5. **PR-0 (infra) can start immediately and in parallel with everything.** It
   gates only PR-8.
6. **PR-9 (desktop inversion) cannot be compiled on the current box** — no Rust,
   no webkit2gtk (`docs/plan/RUN-MODES.md` § "Why B and C are not verified"). It
   is authored and CI-wired, not gated in a real window, exactly as F3-T1 was.
   `dexel open`'s browser fallback (in PR-3) is the shippable path meanwhile.

Minimum shippable set for a first public install:
**PR-0, PR-1, PR-2, PR-3, PR-4, PR-7, PR-8.** PR-5 (pause) and PR-6 (autostart)
are the completion of the owner's product model; PR-9/PR-10 follow.

---

## PR-0 — Hosting exists  **[INFRA]**

Owner: the owner. Everything in `RELEASE_PIPELINE.md` §9 except the deferred
block.

Create the two R2 buckets, attach both custom domains, create the scoped API
token, add three secrets and four variables to the repo, install `rclone` on
`jwdlab-runner`.

**Exit:** `curl -fsSL https://downloads.dexel.jwdlab.com/healthcheck.txt` prints
`ok` for a file the owner uploaded by hand, and
`https://get.dexel.jwdlab.com/` returns 200 for a placeholder. Then delete both
placeholders. `R2_PUBLISH_ENABLED` stays unset until PR-8 is ready.

Blocks: PR-8 only. Start it now.

---

## PR-1 — The storage abstraction  **[CODE]**

Owns: **new** `app/internal/paths/paths.go`, `paths_test.go`,
`paths_darwin.go`, `paths_windows.go`, `paths_other.go`.
Edits: `app/internal/store/store.go` (`DefaultPath` body only),
`app/internal/store/config.go` (`ConfigPath` body only).

* `StateDir/LogDir/BinDir/CacheDir` per `PLATFORM_NOTES.md` §1, build-tagged like
  the existing `provider_select_*.go` trio.
* `DEXEL_HOME` overrides `StateDir`.
* Linux resolves to `$XDG_CONFIG_HOME/dexel` else `~/.config/dexel` — **byte
  identical to what `store.DefaultPath()` returns today** when `XDG_CONFIG_HOME`
  is unset.
* One-time relocation on darwin/windows (`PLATFORM_NOTES.md` §1).

**Exit:**
- `go test ./...` green with no change to any existing store test.
- A test proves Linux + unset `XDG_CONFIG_HOME` ⇒ `~/.config/dexel/state.db`,
  i.e. **existing saves keep loading with zero migration**.
- A test proves `DEXEL_HOME=$(mktemp -d)` fully isolates state, config, logs and
  cache.
- A test proves the relocation moves `state.db` + `config.json` + quarantine
  siblings once, is a no-op the second time, and is a no-op on Linux.

---

## PR-2 — Real version strings  **[CODE]**

Owns: `app/version.go` (new). Edits: `app/main.go` (`healthResponse`,
`healthHandler`), `scripts/build-release.sh` (the `go build` line),
`app/main_test.go` (health assertions).

* `var version = "dev"`, set by `-ldflags "-X main.version=$VERSION"`.
* `/api/health` reports `version` (semver or `dev`) **and** `commit` (today's
  `buildVersion()` output, unchanged).
* `dexel version` — the first subcommand, which forces PR-3's dispatcher to be
  designed rather than retrofitted. If PR-3 lands first, fold this in.

**Exit:** `bash scripts/build-release.sh linux/amd64` with
`DEXEL_RELEASE_VERSION=v9.9.9` produces a binary whose `dexel version` prints
`v9.9.9` and whose `/api/health` shows both fields. A plain `go build` still
prints `dev`.

---

## PR-3 — CLI, detached runtime, discovery, single instance  **[CODE]**

The keystone. Owns: **new** `app/cli.go`, `app/cmd_lifecycle.go`,
`app/spawn_unix.go`, `app/spawn_windows.go`, `app/cli_test.go`, and **new**
`app/internal/lifecycle/{runtimefile.go, lock.go, *_test.go}`.
Edits: `app/main.go` — `main()` becomes the dispatcher, today's body becomes
`runServe(args []string)` **with its logic unchanged**.

* Dispatch per `ARCHITECTURE.md` FORK A: bare → start+open; first arg `-` →
  legacy foreground; known word → subcommand; unknown → usage, exit 2.
* `serve` (foreground, `127.0.0.1:8080` default) and `runtime` (detached target,
  `127.0.0.1:0` default) share one code path.
* `start` / `stop` / `restart` / `status [--json]` / `open` / `logs`.
* `runtime.json` written atomically after bind, removed on clean exit;
  staleness resolved by a live status round-trip, never by pid alone.
* `runtime.lock` via `flock`/`LockFileEx` (`golang.org/x/sys` promoted from
  indirect to direct — no new module).
* Log rotation-lite at start, 8 MiB, one previous file.
* `open`: `dexel-desktop` → `BinDir` → platform app dir → default browser.

**Exit:**
- `dexel start` returns in <2s printing the URL; `pgrep` shows the runtime; the
  terminal can be closed and `dexel status` still reports running.
- `dexel stop` exits it cleanly and a subsequent `dexel status` says not running.
- A second `dexel start` refuses with the running pid; the lock survives a
  `kill -9` of the first (no stale-lock false positive).
- `dexel serve` behaves exactly as today's `go run .` did, and
  `dexel -addr 127.0.0.1:0 -provider fake` still prints `DEXEL_LISTENING` as its
  first stdout line — **`.github/workflows/desktop.yml`'s sidecar assertions pass
  unmodified.**
- `go test -race ./...` green.
- `docs/plan/RUN-MODES.md` and `README.md` § Quick start updated for
  `go run . serve` and a new "installed CLI" mode.

---

## PR-4 — The lifecycle control plane  **[CODE]**

Owns: **new** `app/lifecycle_handlers.go`, `app/lifecycle_handlers_test.go`.
Edits: `app/main.go` (route registration + a `control` channel case in the select
loop), `app/cmd_lifecycle.go` (the CLI calls the endpoints instead of poking
signals), `app/hub.go` (`actionMessage` gains `PAUSE`/`RESUME`).

* `GET /api/lifecycle/status`, `POST /api/lifecycle/{pause,resume,stop}`.
* `X-Dexel-Token` from `runtime.json`, `Content-Type: application/json` required,
  loopback origin check reusing `wsOriginPatterns()`, **no CORS preflight
  answered** for `/api/lifecycle/*`.
* `stop` reuses the existing `case <-sigCh:` body verbatim — one shutdown path.
* `pause`/`resume` ride the existing `actions` channel, so the single-owner
  invariant on `game.Game` is untouched.
* `dexel pause` / `dexel resume` exist here as CLI verbs, even though the
  *semantics* land in PR-5 — at this step they flip a boolean nothing reads yet.

**Exit:**
- A test proves a request without the token gets 401, with a wrong token 401,
  with a `Origin: https://evil.example` 403, and that `OPTIONS` gets no
  permissive preflight.
- `dexel stop` over HTTP saves and exits (assert `state.db` mtime advanced).
- `go test -race ./...` green; every existing `store_gate_test.go` /
  `handlers_test.go` assertion still passes.

---

## PR-5 — Pause semantics  **[CODE, schema bump — serialize against product phases]**

Owns: edits to `app/internal/engine/engine.go` (+`_test.go`),
`app/internal/game/game.go`, `history.go`, `content_free_test.go`,
`stats_test.go`, `app/internal/store/store.go` (`SaveData`, `Snapshot`, `Apply`,
`CurrentSchema`), `content_free_test.go`, `app/frontend/src/wire.ts`,
`render/chrome.ts`, `state/store.ts`, `docs/ui-spec.md`.

Per `ARCHITECTURE.md` §6:
* `provider.Stop()` on pause, `Engine.Reset()` + `provider.Start()` on resume;
  `eng.Tick()` not called while paused.
* No accrual and **no analytics tally** while paused.
* `StatCounters.PausedSeconds` + `DayStat.PausedSeconds`.
* `StateMessage.Paused bool`; `activeState` stays `idle` while paused — no fourth
  mood string.
* `SaveData.Paused bool`; `CurrentSchema` **6 → 7** (P2/Sessions already claimed
  5 → 6 — `docs/plan/P2-design.md` Fork P2-B — so PR-5 takes the next number),
  additive; immediate save on pause and on resume.
* `pausedSeconds` joins P2's session delta set (`docs/plan/P2-design.md`
  §2.3/§5.6): a running session's counters freeze while paused (no ticks), and
  the auto-end idle rule cannot fire while paused — it fires on the **first
  tick after resume**, backdated, once P2 has landed.
* Frontend renders PAUSED and offers resume.

**Exit:**
- Unit test: for any bucket,
  `activeSeconds + idleSeconds + pausedSeconds == the seconds the runtime was
  awake, ticking and observing in that bucket` (amended per
  `docs/plan/BUGS-RESILIENCE.md` R8 — NOT wall-clock uptime: a suspended
  machine takes no ticks, and a blind provider's ticks accrue to no bucket per
  R5, so both fall outside all three counters. See ARCHITECTURE.md Decision 14
  for the full wording.)
- Unit test: across a pause, `devCash`, `xp`, `sprint.unitsDone`, `keystrokes`,
  `mouseActiveSeconds`, `focusSessions`, `appSwitches` are **all** unchanged, and
  `idleSeconds` did **not** absorb the paused seconds.
- Unit test: resume after a long pause grants no work on the first tick and no
  focus bonus from a pre-pause run (`Engine.Reset()` proven).
- Unit test: a schema-6 save loads with `paused=false`; a schema-7 paused save
  round-trips; a schema-8 save is still refused.
- The two structural allow-list tests updated deliberately, with the field count
  bumped and each new field justified in a comment.
- **In-game gate** (`docs/plan/ROADMAP.md` ground rules, the
  `feature-build-and-verify` skill): the real binary, paused live, shows PAUSED,
  the numbers visibly stop, resume restarts them. An isolated mockup does not
  count.

---

## PR-6 — Autostart  **[CODE]**

Owns: **new** `app/cmd_autostart.go`, `app/internal/autostart/{autostart.go,
launchd_darwin.go, systemd_linux.go, xdg_linux.go, windows.go, *_test.go}`.
Edits: `app/internal/store/config.go` (`ConfigData` gains `Autostart string`).

Per `PLATFORM_NOTES.md` §3: launchd user agent on macOS; systemd `--user` with
XDG-autostart fallback on Linux, mechanism detected not assumed; HKCU `Run` key
on Windows. `enable` records the mechanism in `config.json`; `disable` probes
**both** Linux mechanisms regardless of what is recorded.

**Exit:**
- On the Linux runner: `enable` writes the unit and `systemctl --user is-enabled`
  says `enabled`; the runtime is alive after a logout/login cycle (owner-verified
  once, by hand); `disable` leaves no unit, no autostart entry, and
  `config.json` back to `""`.
- Tests run against a fake `HOME` + `DEXEL_HOME` and assert exact file contents
  and idempotency (enable twice = one entry; disable twice = clean).
- macOS and Windows paths are authored + unit-tested for the artifact contents
  they generate, and honestly marked unverified-on-hardware in the same style as
  `desktop/README.md`.

---

## PR-7 — Update and uninstall  **[CODE]**

Owns: **new** `app/cmd_update.go`, `app/cmd_uninstall.go`,
`app/internal/release/{manifest.go, download.go, swap_unix.go, swap_windows.go,
*_test.go}`.

Per `ARCHITECTURE.md` §§8-9: manifest fetch → version compare → artifact lookup
(missing key = exact error) → streamed download with sha256 computed inline →
extract to `<BinDir>/.dexel.new` → execute-and-verify → atomic rename (Windows
rename-dance) → restart if it was running → clear cache. `--check`. Refuse to
downgrade without `--force`. Refuse to self-update a `dev` build without
`--force`. `uninstall` preserves user data; `--purge` requires a typed
confirmation and never touches the legacy Rust save.

**Exit:**
- Table tests against an `httptest` server serving a fixture manifest: happy
  path, hash mismatch (nothing unpacked, non-zero exit), missing platform key
  (exact message), `schema: 2` (refused with "update dexel"), redirect to a
  foreign host (refused), truncated body (refused).
- End-to-end on the runner: install v9.9.8, `dexel update` to v9.9.9 from a local
  fixture bucket, and assert `state.db` is byte-identical across the swap and the
  running version changed.
- ~~`dexel uninstall` removes the binary and leaves
  `state.db`/`config.json`/logs, printing their paths; `--purge --yes` removes
  the state dir and still leaves `~/.local/share/dev-companion/save.json`
  untouched.~~ **SHIPPED** — `app/cmd_uninstall.go` + `app/cmd_uninstall_test.go`,
  wired into `cli.go`'s table. It is the full reversal of what `install.sh` and
  `install.ps1` create, not just the binary: the runtime is stopped
  endpoint-first, autostart is disabled with every mechanism probed, and the
  launcher entry, icon, `dexel-desktop*` shell files, `runtime.json`,
  `runtime.lock` and `cache/` all go, each reported as `removed` or
  `already absent` (a second run is a clean exit 0). Consent is two-stage:
  one `[y/N]`, then the literal word `purge` for the data; `--yes` skips both
  and a non-tty stdin without `--yes` REFUSES rather than assuming an answer.
  A `.deb` install in `/usr/bin` is detected and handed back as
  `sudo apt remove dexel` — never attempted, since this binary does not sudo.
  Verified end-to-end on Linux against three real temp-prefix installs
  (install.sh → autostart enable → runtime running + a headless-Chromium page
  connected → uninstall): state kept with its paths printed, re-run idempotent,
  `--purge --yes` removing the state dir. Windows/macOS branches are
  unit-tested through goos-injected path arithmetic and cross-compiled; the
  Windows self-delete (a detached `powershell -Command Wait-Process; Remove-Item`
  keyed to this pid, since a running `.exe` cannot unlink itself) is
  **FIELD-TEST-NEEDED**.
- ~~`dexel update` is the remaining half of this PR and is still outstanding —
  `update` is deliberately absent from `cli.go`'s table until it does something.~~
  **SHIPPED** — `app/cmd_update.go` + `app/cmd_update_test.go`, wired into
  `cli.go`'s table (and into `cli_test.go`'s wired-and-documented +
  classify assertions). Because R2 is not provisioned yet, it mirrors
  `install.sh`'s trust model rather than the R2/`manifest.json` shape above:
  it resolves the **GitHub "latest release"** for `jawwadzafar/dexel`,
  compares the tag to `main.version`, and — when newer — downloads this
  os/arch's `dexel-<tag>-<os>-<arch>.tar.gz` (`.zip` on Windows), verifies
  it against **two witnesses** (the release's `sha256sums.txt` AND GitHub's
  per-asset `digest`, refusing on either mismatch before anything on disk is
  touched), unpacks the binary, runs it once to prove it works, then does the
  **replace-and-restart dance**: the new bytes are written to a temp file on
  the target's own filesystem, `rename(target → target.old)` then
  `rename(new → target)` (atomic on one fs, mode preserved, rolled back on
  failure), and if a runtime was running it is stopped and restarted on the
  new binary via the same stop→start path `restart` uses. The state dir is
  **never** touched — the save and config survive every upgrade. Flags:
  `--check` (report only), `-y`/`--yes` (non-interactive, mirroring
  `uninstall`'s consent idiom), `--force` (reinstall/downgrade, or update a
  `dev` build). Token via `GH_TOKEN`/`GITHUB_TOKEN` for the private-repo
  path. Exit codes align with `install.sh`'s scheme (0 ok/up-to-date, 5 no
  build for this os/arch, 6 checksum mismatch, 7 feed unreachable, 8 the
  downloaded binary failed its own check). GRACEFUL DEGRADATION is most of
  what runs today and is deliberate: latest == current (the **freeze**:
  v0.1.0) prints "already up to date" and exits 0; a private repo with no
  token / a 404 prints "couldn't reach the release feed (is the repo public
  yet / set GH_TOKEN?)" and exits 7; offline is a clean network error — none
  crash, and a checksum-mismatch injection leaves the binary byte-for-byte
  unchanged (unit-tested). Verified against the **live v0.1.0 release**:
  `dexel update --check` with a token prints "already up to date (v0.1.0)".
  Also added: the conventional `--version`/`-V` alias in `cli.go`'s
  `classify` (routed to the `version` subcommand the same way `-h`/`--help`
  routes to `help`), with `dexel version` kept. The Windows rename-dance and
  self-restart branch is unit-tested through the same OS-injected path
  arithmetic the rest of the CLI uses; the `.old` cleanup on Windows (a
  running `.exe` cannot unlink its own moved-aside image) is best-effort and
  **FIELD-TEST-NEEDED**.

**Recommendation attached to this step:** resolve **FORK C** here. An updater
implies a downgrade path, and today a downgrade quarantines the save and starts
fresh (`app/internal/store/db.go`'s `userVersion > CurrentSchema` branch).
Changing that branch to *refuse to start* is ~10 lines and is the difference
between a clear message and silent data loss.

---

## PR-8 — Installer + R2 publishing  **[CODE + INFRA]**

Needs PR-0, PR-2, PR-7. Owns: **new** `scripts/install.sh`,
`scripts/publish-r2.sh`, `scripts/make-manifest.sh`,
`.github/workflows/deploy-installer.yml`. Edits:
`.github/workflows/release.yml` (two new jobs only — the existing `release` and
`release-macos` jobs untouched apart from PR-2's ldflags).

Per `RELEASE_PIPELINE.md` §§5-6: the `publish` job with the per-object
immutability gate and the append-only `checksums.txt` merge; the `manifest` job
deriving `manifest.json` + `VERSION` **from the bucket listing**; both gated on
`vars.R2_PUBLISH_ENABLED == 'true'`; `deploy-installer.yml` shellchecks,
dry-runs and uploads `install.sh`.

**Exit:**
- A dry-run tag (`v0.0.0-rc1`) publishes to R2; a **re-run of the same tag fails
  the immutability gate with the intended error**, not with an overwrite.
- `curl -fsSL https://downloads.dexel.jwdlab.com/latest/manifest.json | jq .`
  validates against the pinned schema; every `url` answers `HEAD 200`; every
  `sha256` matches `checksums.txt`; no key exists for a platform with no object.
- In a clean container with only `curl`, `tar`, `sha256sum`:
  `curl -fsSL https://get.dexel.jwdlab.com/install.sh | sh` installs, prints the
  next-commands block, and `dexel start && dexel status` works — **with autostart
  not enabled and nothing started by the installer.**
- `install.sh` on an unsupported arch, and on a platform with no artifact, both
  exit with the designed code and message.
- GitHub Releases still carries the same archives and `sha256sums.txt`.

---

## PR-9 — The desktop inversion  **[CODE, cannot be gated here]**

Owns: `desktop/src-tauri/src/lib.rs`, `desktop/src-tauri/Cargo.toml` (drop
`libc`), `desktop/README.md`, `docs/plan/RUN-MODES.md` (mode B's description).

Per `ARCHITECTURE.md` §7: delete `spawn_sidecar`, the stdout reader thread,
`SidecarGuard` and the `ExitRequested|Exit → shutdown()` handler. Add
`resolve_runtime()` — locate a `dexel` (PATH → `BinDir` → bundled
`externalBin`), run `status --json`, `start` if needed, poll to readiness within
the existing 20s bound, build the same always-on-top 660×460 window on the
loopback URL. `externalBin` stays, reinterpreted as the fallback runtime.

**Exit (what can be checked here):** the Rust is authored against current Tauri
v2 docs; `desktop.yml`'s `sidecar` job still green; `desktop/README.md`'s
verified/unverified split updated honestly.
**Exit (needs a build host):** it compiles; the window opens on an
**already-running** runtime; **closing the window leaves the runtime running**
(`dexel status` still reports it) — that is the single assertion this whole step
exists to make true; and re-opening attaches to the same runtime rather than
starting a second.

Write a new **ADR 0017 — background runtime and CLI control plane** as part of
this step (or PR-3, whichever lands first), amending ADR 0015 rather than
replacing it: the window, origin and geometry decisions there all still stand.

---

## PR-10 — Deferred, named  **[DEFER]**

* Code signing + notarization (macOS), Authenticode (Windows) — owner must buy
  certificates (`docs/plan/F3-design.md` §6).
* Artifact signatures (minisign/cosign) + the reserved `signatures` manifest key.
* `dexel-desktop` **distribution**: `.dmg`/`.msi`/`.AppImage` in the manifest as
  a second artifact family. Blocked on runners, not design
  (`docs/plan/RUN-MODES.md` mode C).
* `install.ps1` for Windows.
* Tray / menubar icon with Open · Pause · Quit — the natural home for the three
  operations, and the thing that makes pause discoverable without a terminal.
* Homebrew tap, winget, AUR — downstream consumers of the same manifest.
* Automatic update checks (opt-in only; `--check` already exists as the
  primitive).
* ~~Windows real activity provider~~ — shipped as `provider_select_windows.go`
  (ADR 0021); what is left is *verifying* it on Windows hardware, which needs a
  runner this project does not have.

---

## The critical path, in one line

**PR-0 (owner, now, parallel)** → PR-1 → PR-2 → **PR-3** → PR-4 → PR-7 → **PR-8
= the first `curl | sh` release**. Then PR-5 (pause completes the product model),
PR-6 (autostart), PR-9 (window inversion, when a build host exists).
