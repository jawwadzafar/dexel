# Dexel — Production Distribution, CLI & Background Runtime (architecture)

Status: **design / decision record**, 2026-08-22. No code was written for this
document. It is the record implementation agents build from; where it says
"decided", do not re-derive.

Scope: how Dexel becomes an installable, developer-native product — a CLI
control plane, a background runtime that outlives every window, and a hosted
release channel — starting from the repo exactly as it stands today.

---

## 0. OWNER-DECISION FORKS (read first)

Five genuine forks. Each has a recommended default; the rest of this document
is written assuming the defaults. Say the word to change any of them.

### FORK A — What does a bare `dexel` (no arguments) do?

Today `./dexel` with no arguments starts a foreground server on
`127.0.0.1:8080` (`app/main.go`'s `flag.String("addr", "127.0.0.1:8080", ...)`).
The owner's plan says bare `dexel` should "start-if-needed + open". Those
conflict.

**Recommended default:** bare `dexel` = **start-if-needed + open** (the product
behaviour wins), and the foreground developer path gets an explicit name:
`dexel serve`. Dispatch rule that keeps every existing invocation working:

| argv shape | Meaning |
|---|---|
| `dexel` | start-if-needed, then open |
| `dexel <known-subcommand> [flags]` | that subcommand |
| `dexel -addr ... ` (first arg starts with `-`) | **legacy foreground runtime** — identical to today |
| `dexel <unknown-word>` | error listing subcommands, exit 2 |

The third row is why nothing in CI breaks: `.github/workflows/desktop.yml`
line 116 launches the binary as `... -addr 127.0.0.1:0 -public ... -provider fake`
and asserts the `DEXEL_LISTENING` handshake; `docs/plan/RUN-MODES.md` mode A is
`go run .` (which *would* change) and mode B's Tauri sidecar passes `-addr
127.0.0.1:0` (which would not). Only bare `go run .` changes meaning, and
`docs/plan/RUN-MODES.md` + `README.md` § Quick start get one edit each to say
`go run . serve`.

*Alternative:* keep bare `dexel` = foreground server and require `dexel open`
explicitly. Rejected: the owner asked for the zero-argument convenience, and a
foreground server is the developer path, not the user path.

### FORK B — Do release artifact filenames keep the version in them?

The owner's sketch says `releases/vX.Y.Z/dexel-<os>-<arch>...`;
`scripts/build-release.sh` already produces `dexel-<version>-<os>-<arch>.tar.gz`.

**Recommended default:** keep the version in the filename **and** put it under
the versioned prefix — `releases/v1.4.0/dexel-v1.4.0-linux-amd64.tar.gz`. A
downloaded archive sitting in `~/Downloads` is then self-describing, and
`build-release.sh` needs no change to its naming. Redundancy in a path is
cheaper than an unidentifiable tarball.

### FORK C — What happens when an older binary meets a newer `state.db`?

Today `store.Load` treats a future schema as a quarantine event: the file is
renamed to `state.db.future` and the game **starts fresh**
(`app/internal/store/db.go`, the `userVersion > CurrentSchema` branch, and
`loadOrImport` in `app/main.go`). That is correct anti-cheat behaviour and was
correct when the only way to run an old binary was to check out an old commit.
With `dexel update` and rollback in the picture it becomes "I downgraded and my
progress vanished".

**Recommended default:** on `ErrFutureSchema`, the **runtime refuses to start**
— exit non-zero with "your save was written by dexel vX.Y.Z; run `dexel update`
or reinstall that version" — and does **not** rename the file. `ErrTampered`
keeps today's quarantine-and-start-fresh behaviour unchanged. This is a small,
contained change to one branch and it is the difference between a clear message
and silent data loss. Flagged because it changes shipped behaviour and touches
SEC-1 / ADR 0014 territory.

### FORK D — Is `paused` persisted across a restart?

**Recommended default: yes.** Pause is a user intent, and `dexel update`
restarts the runtime; a pause that silently evaporated mid-update would be a
lie in the other direction. `SaveData.Paused bool`, schema bump. Mitigation for
"I forgot I paused": the UI renders a persistent PAUSED state, `dexel status`
says `paused`, and the runtime logs one startup line.

### FORK E — Which R2 upload tool, and how many buckets?

**Recommended default: `rclone`, two buckets.** See RELEASE_PIPELINE.md §4.
Named here because it costs the owner a Cloudflare decision and three GitHub
secrets.

---

## 1. Ground truth — what the repo already is

Everything below is verified by reading the tree, not assumed.

**1.1 The Go server IS already the runtime.** `app/main.go`'s `main()` ends in a
single-owner `for { select { ... } }` loop driven by four `time.Ticker`s
(`tickTicker` 1s, `terminalTicker` 350ms, `tickerTicker` 2500ms, `saveTicker`
30s). Nothing in that loop consults the hub's connection count. `eng.Tick()`,
`g.Tick(r)` and `persist()` run on wall-clock cadence; `hub.broadcastState`
iterates `h.snapshot()`, which is an **empty map** when no client is connected
(`app/hub.go`) — a no-op, not a stall.

**Verified conclusion: with zero WebSocket clients the game keeps ticking,
keeps accruing, and keeps autosaving.** Closing the UI already does not stop
tracking. The background runtime the owner wants is not a new component; it is
the component that already exists, currently mis-owned by whoever spawned it.

**1.2 The handshake and ephemeral-port plumbing exist.** `-addr 127.0.0.1:0`
binds via an explicit `net.Listen` before anything else starts, and
`handshakeLine()` prints exactly `DEXEL_LISTENING http://<addr>` to stdout
(`app/main.go`). `GET /api/health` exists and already reports `version`,
`publicOk`, `assetsDir`, `source`, `publicSource`, `assetsSource`.

**1.3 SIGTERM already means "save and exit cleanly."** `signal.Notify(sigCh,
SIGINT, SIGTERM)` and the `case <-sigCh:` branch persist state, stop the
provider, and shut the HTTP server down with a 5s grace. `dexel stop` therefore
has a correct primitive on day one.

**1.4 Persistence is one directory, freshly migrated to SQLite.**
`store.DefaultPath()` → `~/.config/dexel/state.db` and `store.ConfigPath()` →
`~/.config/dexel/config.json` (`app/internal/store/store.go`,
`app/internal/store/config.go`). Both **hardcode `~/.config/dexel` on every
OS** via `os.UserHomeDir()`. `db.go` (DB-1 / ADR 0016) gives us a signed single
row, a strict open-gate, `.corrupt`/`.future`/`.invalid` quarantine, and a
one-time `state.json` → `state.db` import. The codebase already knew per-OS
conventions (`~/Library/Application Support/...` on darwin) from the
legacy-Rust importer — it just never applied them to its own files. (That
importer, `app/internal/store/legacy.go`, was deleted by review item B-2; the
tombstone comment in `app/main.go`'s `loadOrImport` records why.)

**1.5 The Tauri shell currently OWNS the server.** `desktop/src-tauri/src/lib.rs`
spawns the Go binary as a Tauri `externalBin` sidecar with `-addr 127.0.0.1:0`,
parses `DEXEL_LISTENING` off its stdout, builds one always-on-top window on
that URL, and on `RunEvent::ExitRequested | Exit` calls `SidecarGuard::shutdown()`
— SIGTERM, 3s grace, then SIGKILL. **This is the exact ownership that must
invert.** Note it has never been compiled (`desktop/README.md`,
`docs/plan/RUN-MODES.md` § "Why B and C are not verified").

**1.6 The release pipeline already cross-compiles the matrix.**
`scripts/build-release.sh` builds linux/{amd64,arm64} + windows/{amd64,arm64}
at `CGO_ENABLED=0` and darwin/arm64 only on a darwin host (the macOS activity
provider is cgo). `.github/workflows/release.yml` runs it on
`[self-hosted, darkmirror]`, asserts the committed frontend bundle matches a
fresh build, runs `go vet` + `go test -race`, and publishes to GitHub Releases
with `sha256sums.txt`. The `release-macos` job is a deliberate gated no-op.

**1.7 EMBED-1 has landed.** `app/embed.go` embeds `public/` and `assets/`;
`build-release.sh` ships binary + licences only. The product is one file.

**1.8 There is already a precedent for freezing accrual honestly.** The
STORE_OPEN gate: `g.Tick(r)` calls `recordStats(r)` **unconditionally**, then
`if g.StoreOpen() { return false }` before touching `Mood`, `accrueWork`, or
`Progress` (`app/internal/game/game.go` ~L501-540, `docs/ui-spec.md` §5.3). So
"suspend accrual without lying" already has a shape in this codebase. Pause is
a *different* decision about the stats half — see §6.

**1.9 Wire and save shapes are structurally locked.**
`app/internal/game/content_free_test.go` asserts `StateMessage` has *exactly*
the allow-listed fields and fails on a count mismatch;
`app/internal/store/content_free_test.go` does the same for `SaveData`. Any new
field is a deliberate, test-visible act. `CurrentSchema = 5` today.

---

## 2. The settled shape

### Decision 1 — ONE binary, subcommands. The CLI and the runtime are the same file.

`dexel` is a single Go binary built from the existing `app/` module. It is
simultaneously:

* the **CLI** (`dexel start`, `stop`, `status`, ...),
* the **runtime** (`dexel runtime`, the loop that exists today),
* the **web UI** (already embedded by `app/embed.go`),
* the **updater** (`dexel update` replaces this same file).

Rationale, all repo-grounded: post-EMBED-1 there is exactly one artifact; the
runtime loop, the HTTP server, the storage layer and the platform path logic
already live in this module; a second CLI binary would have to duplicate the
storage abstraction or import it anyway. One binary means the version the CLI
reports and the version the runtime runs **cannot disagree**, which is the
single most common failure mode of split CLI/daemon products.

`dexel-desktop` (the Tauri app) is a **second, optional artifact**. It is not
required to run Dexel and it is not required to see the UI (`dexel open` falls
back to the default browser).

### Decision 2 — Keep `package main`; add files. Do not restructure.

`app/main.go` is 669 lines and `app/main_test.go`, `app/handlers_test.go`,
`app/store_gate_test.go` are all `package main` and reach internal helpers
directly. Moving the runtime into `app/internal/runtime` would rewrite three
test files for zero product benefit.

**Decided:** new files in `package main` — `cli.go` (dispatch + usage),
`cmd_lifecycle.go`, `cmd_autostart.go`, `cmd_update.go`, `spawn_unix.go`,
`spawn_windows.go` — and `main()` becomes a ~20-line dispatcher that calls
`runServe(args)` (today's `main()` body, renamed, unchanged) for the runtime
path. Genuinely reusable, non-main logic goes in new `app/internal/*` packages
(`paths`, `lifecycle`, `release`).

### Decision 3 — Command surface

| Command | What it does |
|---|---|
| `dexel` | start-if-needed, then open (FORK A) |
| `dexel start` | spawn the detached runtime; wait for readiness; print the URL |
| `dexel stop` | graceful stop (SIGTERM equivalent, via the lifecycle endpoint); runtime saves and exits |
| `dexel restart` | stop, wait for exit, start |
| `dexel status [--json]` | running? pid, port, url, version, uptime, paused, provider honesty, state path |
| `dexel open` | ensure running, then show the window: `dexel-desktop` if installed, else the default browser |
| `dexel pause` / `dexel resume` | flip the persisted pause state (§6) |
| `dexel autostart enable\|disable\|status` | one abstraction, per-OS mechanism (PLATFORM_NOTES.md §3) |
| `dexel update [--check]` | manifest → download → verify → atomic swap → restart if it was running |
| `dexel uninstall [--purge]` | stop, disable autostart, remove binaries; **preserves user data** unless `--purge` |
| `dexel logs [-f] [-n N] [--path] [--truncate]` | the runtime log |
| `dexel version` | semver + commit + os/arch |
| `dexel serve [flags]` | **foreground** runtime — the developer path; all of today's flags |
| `dexel runtime` | the detached runtime's own entry point (documented, not hidden; `start` execs it) |

`serve` and `runtime` are the same code path; the two names exist so a log line,
a launchd plist, and a human's terminal each say the thing they mean. `runtime`
defaults to `-addr 127.0.0.1:0`; `serve` keeps today's `127.0.0.1:8080` default
so `go run . serve` behaves exactly as `go run .` does now.

### Decision 4 — Three operations, three mechanisms, no ambiguity

| Operation | Mechanism | Runtime process | Tracking | Progression | UI |
|---|---|---|---|---|---|
| **Close UI** | close the window / browser tab | alive | on | on | gone; state keeps ticking |
| **Pause** | `dexel pause`, or a UI control, or `POST /api/lifecycle/pause` | alive | **off** (provider stopped) | **off** | shows PAUSED |
| **Stop** | `dexel stop`, or `POST /api/lifecycle/stop`, or SIGTERM | **exits** after a final save | off | off | UI loses its socket |

Closing the window can never stop the runtime because the window no longer owns
the runtime's lifetime (§7). Nothing in the runtime watches for
"last client disconnected".

---

## 3. The lifecycle control plane

### Decision 5 — No new protocol. HTTP on loopback, which the runtime already speaks.

The runtime already serves an `http.ServeMux` on loopback with an origin-checked
WebSocket and a JSON `/api/health`. A second socket type, a unix-domain socket,
a gRPC surface, or a hand-rolled IPC framing would all be new surface for zero
new capability. **Decided:** `dexel status/pause/resume/stop` are HTTP calls
from the CLI to the running runtime.

New endpoints, all `POST` except the first:

```
GET  /api/lifecycle/status   -> {schema, version, commit, pid, port, url,
                                 startedAt, uptimeSeconds, paused,
                                 providerHonesty, statePath, logPath}
POST /api/lifecycle/pause    -> {paused: true}
POST /api/lifecycle/resume   -> {paused: false}
POST /api/lifecycle/stop     -> 202, then the runtime does exactly what the
                                existing `case <-sigCh:` branch does
```

`stop` is implemented by sending on the **existing** `sigCh`-shaped path: the
handler pushes onto a new `control` channel that the single-owner select loop
reads, and that branch reuses the same `persist(); provider.Stop();
httpSrv.Shutdown()` sequence. No second shutdown path to keep in sync.

`pause`/`resume` funnel through the **existing `actions` channel**
(`app/hub.go`'s `actionRequest`), so the single-owner invariant holds
unchanged — `game.Game` does no locking and the select loop *is* the lock.
Because they are ordinary actions, the WebSocket UI gets a pause button for
free, with no second code path.

`stop` is deliberately **not** a WS action. A web page must never be able to
kill the runtime, and "the window closed" must never mean "stop".

### Decision 6 — Discovery: `runtime.json` in the state dir

Written atomically (the same tmp+fsync+rename recipe `store.SaveConfig` already
uses) immediately after `net.Listen` succeeds, at mode `0600`; removed on clean
exit.

```json
{
  "schema": 1,
  "pid": 48213,
  "port": 51637,
  "url": "http://127.0.0.1:51637",
  "version": "v1.4.0",
  "commit": "7a78704",
  "startedAt": "2026-08-22T09:14:02Z",
  "token": "<32 bytes of crypto/rand, hex>"
}
```

Staleness is resolved by **asking**, never by trusting the file: read it, call
`GET /api/lifecycle/status`, and require the returned `pid` to equal the file's.
Connection refused, a timeout, or a pid mismatch ⇒ stale ⇒ remove the file and
proceed as "not running". A pidfile alone cannot survive pid reuse; a live HTTP
round-trip can.

### Decision 7 — Single instance: an OS lock, not a file's existence

`<StateDir>/runtime.lock` held for the process lifetime — `flock(LOCK_EX|LOCK_NB)`
on unix, `LockFileEx(LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY)` on
Windows, via `golang.org/x/sys` (**already in `app/go.mod` as an indirect
dependency — promoting it to direct adds no module**). The OS releases the lock
even if the process is SIGKILLed, so there is no stale-lock class of bug.

Layered, in order, at runtime startup:
1. acquire `runtime.lock` — fail fast with "dexel is already running (pid N)" if held;
2. `net.Listen` on the requested address — the existing `log.Fatalf` on a busy
   port stays as the belt-and-braces check;
3. write `runtime.json`.

`dexel serve` (the dev foreground path) **skips the lock and the runtime.json
write** by default (`--no-lock` implied), so a developer can run a scratch
instance next to a real one exactly as they can today. It logs one line saying
so.

### Decision 8 — Security: loopback + origin + a capability token

The lifecycle endpoints mutate more than the game does (`stop` ends the
process), so they get one more gate than `/ws`:

* bound to loopback only, inheriting today's posture;
* a required `X-Dexel-Token: <token from runtime.json>` header. Because it is a
  **custom** header, a browser cannot send it on a simple cross-origin request
  — it forces a CORS preflight, and the runtime answers no preflight for
  `/api/lifecycle/*`. That closes the drive-by-CSRF hole that a bare
  `POST /api/lifecycle/stop` would otherwise open to any web page the user has
  in another tab (the same class of hole B1 closed for the WebSocket).
* `Content-Type: application/json` required; `Origin`, if present, must match a
  loopback pattern — reuse `wsOriginPatterns()` verbatim.
* `runtime.json` is `0600`; the token is regenerated on every start and is never
  logged, never sent to a client, and never persisted in `state.db` or
  `config.json`.

`/api/health` stays exactly as it is — unauthenticated, the browser reads it.
It gains two fields (`commit`, and `paused` for the UI's benefit); the
lifecycle status endpoint is the machine surface.

---

## 4. Storage — one abstraction, platform-appropriate locations

### Decision 9 — A new `app/internal/paths` package is the only place that knows a path

```go
package paths

func StateDir() (string, error)   // state.db, config.json, runtime.json, runtime.lock
func LogDir()   (string, error)   // <StateDir>/logs
func BinDir()   (string, error)   // where the installer put (and update replaces) the binary
func CacheDir() (string, error)   // <StateDir>/cache — update downloads, deleted after use
```

`store.DefaultPath()` and `store.ConfigPath()` are rewritten to call
`paths.StateDir()` and keep their signatures, so nothing else in the tree
changes.

| | Linux | macOS | Windows |
|---|---|---|---|
| StateDir | `$XDG_CONFIG_HOME/dexel` else `~/.config/dexel` | `~/Library/Application Support/dexel` | `%LOCALAPPDATA%\dexel` |
| LogDir | `<StateDir>/logs` | `<StateDir>/logs` | `<StateDir>\logs` |
| BinDir | `~/.local/bin` | `~/.local/bin` | `%LOCALAPPDATA%\dexel\bin` |
| CacheDir | `<StateDir>/cache` | `<StateDir>/cache` | `<StateDir>\cache` |

Linux keeps `~/.config/dexel` exactly as the owner asked and as
`store.DefaultPath()` already does — **zero migration for the only platform
with real users today.** `$XDG_CONFIG_HOME` is honoured because the whole point
of the abstraction is not to hardcode.

`DEXEL_HOME` overrides `StateDir` wholesale. This is not a nicety: it lets the
test suite, CI, and a dev instance run without touching the developer's real
save, which today requires threading an explicit path through every call.

**Logs under StateDir on every platform** rather than `~/Library/Logs/dexel` on
macOS: one rule, and `dexel uninstall --purge` then has exactly one directory
to remove. `dexel logs` is the interface anyone should use; the path is printed
by `dexel status`. Accepted deviation from macOS convention, stated on purpose.

**BinDir is `~/.local/bin`, never `/usr/local/bin`:** the installer must work
without `sudo`. `~/.local/bin` is on `PATH` by default on most modern distros
and macOS shells; when it is not, the installer says so and offers to add it
(RELEASE_PIPELINE.md §6 step 8).

### Decision 10 — One-time relocation on macOS and Windows

Because `store.DefaultPath()` currently returns `~/.config/dexel/state.db` on
*every* OS, a macOS or Windows user who ran a from-source build has files in the
Linux location. At startup: if the new `StateDir` has no `state.db` and
`~/.config/dexel/state.db` exists, **move** `state.db`, `config.json` and any
`.invalid`/`.future`/`.corrupt`/`.imported` siblings to the new directory and log
one line. Same shape as `importJSON`'s DB-written-then-rename ordering
(`app/internal/store/db.go`): move only after the destination directory exists,
never copy-and-delete, never merge. Linux is a no-op by construction.

### Decision 11 — What survives update / restart / binary replacement

`state.db`, `config.json`, and `logs/` live in `StateDir`; the binary lives in
`BinDir`. They never overlap, so replacing the binary cannot touch state, and
`dexel uninstall` can remove one without the other. `dexel update` writes only
to `CacheDir` and `BinDir`. Schema migration on the way up is the existing
additive-bump mechanism (`CurrentSchema`, and `Load`'s per-version handling);
on the way *down* it is FORK C.

---

## 5. Daemonization

### Decision 12 — `dexel start` spawns a detached child of itself

Go cannot `fork()` safely (the runtime's threads do not survive it), so there
is no self-daemonizing double-fork. The child is a fresh `exec` of the same
binary:

```
self, _ := os.Executable()
cmd := exec.Command(self, "runtime")
cmd.Stdin  = devNull
cmd.Stdout = logFile      // O_APPEND, after rotation-lite
cmd.Stderr = logFile
cmd.SysProcAttr = detachAttr()   // build-tagged, see PLATFORM_NOTES.md §2
cmd.Start(); cmd.Process.Release()   // never Wait — do not parent a zombie
```

`detachAttr()` lives in `spawn_unix.go` / `spawn_windows.go`, mirroring the
existing `provider_select_{linux,darwin,other}.go` build-tag pattern this repo
already uses.

Readiness is then **polled, not parsed**: a detached child's stdout goes to the
log file, not to us, so the `DEXEL_LISTENING` handshake is unavailable to a
detaching parent. `dexel start` polls for `runtime.json` and then for a 200 from
`/api/lifecycle/status`, up to 10s, and on failure prints the last 20 lines of
the log and exits non-zero. Failing loudly is this repo's stated posture
(`app/main.go`'s `log.Fatalf`s, ADR 0010's honesty rules).

`DEXEL_LISTENING` **stays in the binary**: it is still how `dexel serve` reports
an ephemeral port to a human and to `desktop.yml`'s assertion (line 110). It is
simply no longer the readiness contract.

---

## 6. Pause / resume

### Decision 13 — Pause stops observation at the source

While paused:

* **`provider.Stop()` is called.** Dexel does not observe input while the user
  has said stop. This is the strongest honest reading of "pause tracking", and
  it is cheap: `activity.Provider` already documents `Start`/`Stop` as the
  lifecycle pair, and every provider "degrades to reporting blind zero-signal
  snapshots rather than panicking" (`app/internal/activity/provider.go`).
* `eng.Tick()` is **not called**. No sampling, no mood computation, no work
  units, no focus-run bookkeeping.
* Nothing accrues: `Progress`, `DevCash`, `XP`, sprint completion, and the four
  `workKeys/workMouse/workFocus/workSwitch` accumulators are all untouched.
* **Analytics do not accrue either.** This is where pause deliberately differs
  from the STORE_OPEN gate, which runs `recordStats(r)` unconditionally
  (`game.go` L509 and its comment). STORE_OPEN is a few seconds of shopping
  *inside* a tracked session, so counting the seconds is honest. Pause is the
  user withdrawing consent to be watched; counting those seconds as anything
  observed would be dishonest, and with the provider stopped there is nothing
  honest to count.

Still running while paused: the HTTP server, the WebSocket, the 1s state
broadcast, the 30s autosave, and the cosmetic terminal/ticker scroll. The UI
stays live and shows PAUSED — a frozen window would be indistinguishable from a
crash.

### Decision 14 — Paused time is its own bucket. It is not idle.

ADR 0010's rule is that the game must never claim something it cannot know.
Paused time is known precisely — it just isn't activity.

* `StatCounters` gains **`PausedSeconds uint64`** (`json:"pausedSeconds"`),
  ticked once per second while paused, in both the `today` and `lifetime`
  buckets. One more content-free duration; it goes on
  `app/internal/game/content_free_test.go`'s allow-list explicitly.
* **Invariant, unit-testable:** for any bucket,
  `activeSeconds + idleSeconds + pausedSeconds == seconds the runtime was up
  during that bucket`. Today `activeSeconds + idleSeconds` already partitions
  every tick (`ActiveSeconds` counts `MoodCoding` ticks, `IdleSeconds` counts
  every other tick). Pause carves a third, disjoint slice; it must never be
  added to `IdleSeconds`, and `MouseActiveSeconds`/`Keystrokes`/`FocusSessions`/
  `AppSwitches` must be provably unchanged across a pause.
* `history.DayStat` gains the same field (it mirrors `StatCounters` field for
  field — `app/internal/game/history.go` L56-71), so a day's row can honestly
  show a paused band instead of a suspiciously idle stretch.

### Decision 15 — Wire and UI: a new boolean, not a fourth mood

`docs/ui-spec.md` §6.1 pins `activeState` to exactly three strings — `coding |
idle | onBreak`. **Do not add a fourth.** Instead:

* `StateMessage` gains **`Paused bool`** (`json:"paused"`) — allow-list updated,
  field count bumped in `content_free_test.go`.
* While paused, `activeState` reports **`idle`**. `idle` is already the
  engine's non-claiming fallback (`Engine.mood`'s final `return MoodIdle`);
  `onBreak` would be the ADR 0010 lie ("on break because you paused me"), and
  `coding` is obviously false. `paused: true` is the authoritative signal.
* The frontend renders PAUSED from `paused`, overriding the mood chrome, and
  pins the activity line to a fixed "PAUSED — tracking is off" string chosen
  client-side. No new server-side ticker pool.
* Resume is discoverable from the paused UI itself, not only from the CLI.

### Decision 16 — Persistence and the resume seam

* `SaveData` gains **`Paused bool`** (`json:"paused,omitempty"`);
  `CurrentSchema` 5 → 6, additive in exactly the way the 1→2/2→3/3→4 bumps
  were (a schema-5 file has no key, `json.Unmarshal` leaves `false`, which is
  the correct "not paused" default). `store.Snapshot`/`Apply` carry it.
* `Engine` gains **`Reset()`** — clears `initialized`, `lastKeystrokeCount`,
  `lastKeystrokeAt`, `lastActiveApp`, `focusRunActive`, `focusRunStart` — called
  on **resume**, before the provider restarts. Without it, resume would inherit
  a stale `lastKeystrokeAt` (and briefly claim `coding` for typing that happened
  before the pause) and a stale focus run (and hand out a focus bonus for a
  "sustained" run with a ten-hour hole in it). The engine's existing
  first-tick guard (`wasInitialized`) then does the rest: the tick after resume
  contributes zero work no matter what counter the restarted provider reports.
* On resume the runtime writes an immediate save, so a crash right after resume
  cannot come back paused.
* A paused runtime that starts up logs one line and `dexel status` reports
  `paused: true` — a paused-and-forgotten Dexel must be obvious, never mute.

---

## 7. The desktop inversion

### Decision 17 — `dexel open` owns the window; the runtime owns itself

> **STATUS: IMPLEMENTED 2026-08-23** (macOS arm64). Every row of the table
> below has landed in `desktop/src-tauri/src/lib.rs`, including the
> `PATH → BinDir → bundled` driver lookup, and was verified end to end:
> launch with no runtime starts one and `dexel status` sees it; closing the
> window leaves the runtime observing and autosaving; relaunching attaches to
> the same runtime instead of spawning a second server. One fix outside this
> table was needed to make the window reachable at all: `dexel open` handed
> `/Applications/dexel.app` — a DIRECTORY — to `exec.Command`, which fails
> with "permission denied", so a bundle now launches via `open -a` and a
> failed launch falls through to the browser instead of erroring out.
> The "today" column is kept as the record of what was replaced.

Current flow (`desktop/src-tauri/src/lib.rs`): *shell spawns server → parses
stdout → builds window → kills server on exit.*

New flow: *runtime already exists (or the shell asks the CLI to create it) →
shell attaches a window to it → closing the window attaches nothing and kills
nothing.*

Concretely, in `desktop/src-tauri/src/lib.rs`:

| Today | After |
|---|---|
| `spawn_sidecar()` — `app.shell().sidecar("dexel-server").arg("-addr").arg("127.0.0.1:0").spawn()` | `resolve_runtime()` — run `dexel status --json`; if not running, run `dexel start`; re-query for the URL |
| reader thread parsing `DEXEL_LISTENING` off the child's stdout | **deleted** — the runtime is not our child and has no stdout we own |
| `HANDSHAKE_TIMEOUT` on a channel | the same 20s bound, spent polling `dexel status --json` / `GET /api/health` |
| `SidecarGuard` + `Mutex<Option<CommandChild>>` + SIGTERM/SIGKILL escalation + `libc` dependency | **deleted in full**, along with the `[target.'cfg(unix)'.dependencies] libc` entry |
| `RunEvent::ExitRequested \| Exit → guard.shutdown()` | **deleted.** Closing the window closes the window. |

What survives unchanged: `WebviewUrl::External(loopback url)` (F3-design FORK 2's
recommended path — the page's origin IS the server's origin, so the WS
same-origin check still passes with no `-insecure-origin` and no wildcard);
`always_on_top(true)` (ADR 0007); the 660×460 fixed geometry; `tauri-plugin-log`;
the icon set.

**`bundle.externalBin: ["binaries/dexel-server"]` also survives — reinterpreted.**
It is no longer a sidecar the shell owns; it is the **fallback `dexel` binary**
for someone who installed only the `.dmg` and never ran the install script. The
shell locates a `dexel` to drive in this order: `PATH` → `paths.BinDir()`'s
convention (`~/.local/bin/dexel`) → the bundled copy. It then runs
`<dexel> start` (which detaches) and `<dexel> status --json`. So
`scripts/build-sidecar.sh` and `desktop.yml`'s `sidecar` job keep working
verbatim; only the meaning of the file changes.

> **Update (implemented 2026-08-23).** The rename this paragraph called
> "cosmetic and deferred" was neither. It was done — the bundled artifact is
> now **`Dexel Runtime`** — because macOS names a Login Items entry after the
> executable it launches, so the pane read "dexel-server". And the obvious
> target, `dexel`, is unusable: `Contents/MacOS/dexel` is already the Tauri
> MAIN binary, the volume is case-insensitive, and `tauri-bundler`'s
> `copy_binaries` runs BEFORE `copy_binaries_to_bundle` — so naming the
> daemon `dexel`/`Dexel` would have let the GUI shell silently overwrite it,
> leaving a login item that opens a window instead of starting a runtime.
> `Dexel Runtime` matches the product's own word for it (`dexel runtime`,
> `runtime.json`, `runtime.lock`). It touched `externalBin`, the shell-plugin
> capability scope, `SIDECAR_NAME`, `build-sidecar.sh`, `desktop.yml`'s
> assertion, and autostart's bundle probe (which accepts the legacy name too,
> so an older installed bundle still resolves).

**`dexel open` without the desktop app:** looks for `dexel-desktop` on `PATH`,
then in `BinDir`, then a platform app location (`/Applications/dexel.app`,
`%LOCALAPPDATA%\Programs\dexel`); if none, opens the runtime URL in the default
browser (`xdg-open` / `open` / `rundll32 url.dll,FileProtocolHandler`). This
matters for sequencing: **the browser fallback is shippable today on the one
Linux runner this repo has**, while `dexel-desktop` remains blocked on a build
environment that has never existed (`docs/plan/RUN-MODES.md`). The inversion can
therefore be designed now, shipped in the CLI now, and compiled whenever a
runner appears.

A tray/menubar icon with Open/Pause/Quit is the natural home for these three
operations and is **deferred**, named so it does not creep.

---

## 8. Update

### Decision 18 — Single-binary self-update, atomic rename, state untouched

```
dexel update
 1. GET https://downloads.dexel.jwdlab.com/latest/manifest.json  (HTTPS only;
    redirects to any other host refused)
 2. manifest.version == my version?  -> "already up to date", exit 0
 3. artifacts["<GOOS>-<GOARCH>"] missing? -> exit non-zero naming the platform
 4. download -> <CacheDir>/<artifact>   (streamed; sha256 computed while writing)
 5. sha256 != manifest sha256 -> delete, exit non-zero. Never unpack unverified bytes.
 6. extract the single binary -> <BinDir>/.dexel.new  (same filesystem => rename is atomic)
 7. chmod 0755; run `<BinDir>/.dexel.new version`; refuse if it does not report
    manifest.version  (a tarball that cannot run is not an upgrade)
 8. swap:  unix    -> os.Rename(.dexel.new, dexel)
           windows -> rename dexel.exe -> dexel.exe.old, rename new -> dexel.exe,
                      best-effort delete .old (and on next start, again)
 9. was the runtime running?  -> stop, wait for exit, start   (the new binary)
10. delete <CacheDir> contents; print old -> new version
```

Step 8's Windows path is the standard rename-dance: a running `.exe` cannot be
replaced, but it **can** be renamed, and `dexel update` is itself running from
that file. Deleting `.old` fails while the old process lives, so the delete is
attempted again at the next start; if it still fails, `MoveFileEx(...,
MOVEFILE_DELAY_UNTIL_REBOOT)`.

Step 9 is why state must live outside `BinDir`: the swap is invisible to
`state.db`, and the stop half runs the existing SIGTERM save path, so at most
the last ~1s of progress is at risk (autosave bounds it to 30s even on a hard
kill — the same bound ADR 0015 already accepted).

**`--check`** does steps 1-3 and prints; it is what an autostart-enabled runtime
could call weekly later. **No silent auto-update** is designed here: updating a
running background process without being asked is the same category of surprise
as silent autostart, which the owner explicitly rejected.

**`dexel update` never downgrades.** If `manifest.version` differs and is older,
it says so and requires `--force`, and then FORK C's refusal is what protects
the save.

### Decision 19 — Manifest schema, version 1 (pinned)

```json
{
  "schema": 1,
  "version": "v1.4.0",
  "releasedAt": "2026-08-22T09:14:02Z",
  "artifacts": {
    "linux-amd64": {
      "url": "https://downloads.dexel.jwdlab.com/releases/v1.4.0/dexel-v1.4.0-linux-amd64.tar.gz",
      "sha256": "9f2b…",
      "size": 12841984,
      "binary": "dexel-v1.4.0-linux-amd64/dexel"
    },
    "linux-arm64":   { "…": "…" },
    "darwin-arm64":  { "…": "…" },
    "windows-amd64": { "…": "…" }
  }
}
```

* **Keys are `<GOOS>-<GOARCH>`**, exactly the strings `runtime.GOOS` and
  `runtime.GOARCH` produce. The updater does zero name mapping, so a mapping
  table cannot drift.
* `binary` is the path *inside* the archive, so the extractor never has to guess
  (FORK B keeps the version in that path).
* **A platform with no build has its key OMITTED — never a placeholder.** A
  placeholder is a lie the installer and updater would each have to special-case,
  and a `null` url is a crash waiting for whoever forgets. Omission produces an
  exact error: `no darwin-arm64 build in v1.4.0 yet`. This is the direct answer
  to the gated macOS/Windows runners (`release.yml`'s `release-macos` no-op).
* `signatures` is **reserved**, absent until signing lands. Consumers ignore
  unknown top-level keys (forward compatible); `schema` is bumped only for a
  breaking change, and a consumer seeing `schema > 1` refuses and says "update
  dexel to read this manifest".
* The manifest is **derived from the R2 bucket**, not from one CI job's `dist/`
  — see RELEASE_PIPELINE.md §5. That is what lets a later macOS build add
  `darwin-arm64` to an already-published version without violating
  immutability.

### Decision 20 — Real version strings

`buildVersion()` today returns a VCS revision from `debug.ReadBuildInfo()` —
useful, but not comparable to a manifest. Add `-ldflags "-X main.version=$VERSION"`
in `scripts/build-release.sh`; `main.version` defaults to `"dev"`. `dexel version`
and `/api/health` report **both**: `version` (semver, or `dev`) and `commit`
(today's `buildVersion()` output, kept verbatim). `dexel update` refuses to run
from a `dev` build unless `--force` — a source build is not something an
installer should overwrite.

---

## 9. Uninstall

```
dexel uninstall
  1. dexel stop                    (if running)
  2. dexel autostart disable       (probes BOTH Linux mechanisms; see PLATFORM_NOTES §3)
  3. remove <BinDir>/dexel (+ dexel-desktop if WE installed it)
  4. remove <StateDir>/runtime.json, runtime.lock, cache/
  5. PRINT, do not delete: state.db, config.json, logs/
  6. print the PATH line the installer added, if any, and its file — we do not
     un-edit the user's shell rc automatically
```

`--purge` removes the whole `StateDir` after typing the word `purge`
(`--purge --yes` for scripts). **Never** touches
`~/.local/share/dev-companion/save.json` or
`~/Library/Application Support/dev-companion/save.json` — the legacy Rust save
is not ours. (The importer that read it, `app/internal/store/legacy.go`, was
deleted by review item B-2, and the Rust/Bevy crates themselves are archived on
branch `attic/legacy-rust-and-fleet` — ADR 0011, ADR 0020. The save file still
is not ours to delete.)

Windows cannot delete its own running `.exe`: rename to `dexel.exe.old`,
register `MOVEFILE_DELAY_UNTIL_REBOOT`, and say so.

---

## 10. What this design deliberately does NOT build

The owner asked for the smallest production-ready path. Named rejections, so
nobody adds them back by reflex:

* **No separate daemon binary.** One file (Decision 1).
* **No IPC protocol.** HTTP on loopback already exists (Decision 5).
* **No unix socket / named pipe.** Would need two implementations and a second
  security model for the same capability.
* **No supervisor of our own.** launchd and systemd already supervise; on
  Windows we accept no restart-on-crash in v1.
* **No process-wide platform abstraction layer.** Three build-tagged files
  (`detachAttr`, `autostart`, `lockFile`) plus one `paths` package. The repo's
  existing `provider_select_*.go` pattern is the precedent.
* **No auto-update daemon, no telemetry, no crash reporting, no signing**
  (deferred; certificates are an owner purchase — F3-design §6).
* **No tray/menubar icon** (deferred, named).
* **No packaged manager distribution** (Homebrew tap, winget, AUR, `.deb`) —
  the install script + `dexel update` cover the first release; a tap is a
  follow-on that consumes the same manifest.
* **No change to the privacy or honesty boundaries.** Every new field in this
  design is a count, a duration, a boolean, or a path — the structural
  allow-list tests are the enforcement, and they get updated deliberately, not
  worked around.

---

## 11. Cross-references

* `docs/adr/0015-tauri-desktop-shell.md` — the sidecar decision this document
  inverts. A new ADR (**ADR 0017 — background runtime and CLI control plane**)
  should record §2/§3/§7; ADR 0015 is amended, not deleted, since the window,
  origin and geometry decisions all stand.
* `docs/adr/0016-sqlite-persistence.md`, `docs/plan/DB-1-design.md` — the state
  container this design must not disturb.
* `docs/adr/0010-mac-first-honest-mechanics.md` — the constraint §6 is written
  against.
* `docs/plan/RUN-MODES.md` — gains a mode for the installed CLI; modes A-D stay.
* `docs/plan/F3-design.md` §§1-2, 6-9 — the desktop build matrix and signing
  deferral remain accurate.
