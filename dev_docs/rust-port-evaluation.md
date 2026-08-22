# dexel — Rust vs Go: parallel-implementation evaluation and plan

Status: evaluation + plan, 2026-08-22 · Answers "can we build the Rust version,
which will win, and what's the plan" · Grounded in ADR 0011 (the deliberate
Rust→Go pivot), ADR 0015 (which rejected a Rust *rewrite* once, for reasons
that do not apply to a parallel build), ADR 0014/0016 (persistence and
integrity), ADR 0018 + `dev_docs/production-runtime/` (CLI/runtime/release),
the frozen Rust legacy in `companion/`+`activity/`, and measurements taken on
this box today — including an actual Rust probe binary built and run.

**Scope note.** The owner has ruled out a migration: the Rust app may use its
own fresh save format and its own key. There is no byte-compatibility problem
to solve, and this document does not design one. The Rust app is a **second
implementation living in this repo alongside the Go one**, built to parity so
the two can be compared and one chosen. **Production is not blocked on it: the
Go app keeps shipping the whole time.**

---

## 0. Verdict — which will win, and why

**Can we build a Rust version to parity? Yes.** Nothing in this app is
Go-specific enough to block it. Every mechanism has a real Rust equivalent,
this repo has already written Rust versions of the two hardest platform pieces
(`activity/src/global_input.rs`, `active_app.rs` — 2,330 lines of working, once-green
Rust), and dropping the migration requirement removes what would otherwise
have been the single hardest technical risk (§3.3). A parallel build is
engineering, not research.

**Is there a plan? Yes — §5.** Seven phases (P0–P6) standing up `app-rs/` as
its own Cargo workspace beside `app/`, sharing the TypeScript frontend and
sprite assets *as the same bytes*, speaking the identical WebSocket wire
contract so **the same browser client works against either server unmodified**.
§6 is the scorecard the two get judged by. §7 names cheaper ways to get most of
the same information.

**Which will win?** My honest prediction, stated up front and argued in §4:

> **Go wins the scorecard as weighted in §6 — but the experiment is worth
> running anyway, because it is cheap to abort, it produces three artifacts of
> standalone value even if Rust loses, and it is the only way to test the one
> thing Go structurally cannot do.**

The prediction rests on measurements, not taste:

| Criterion | Measured today | Likely winner |
|---|---|---|
| Shipped binary | Go 12.72 MB (`-s -w`); Rust probe **2.59 MB** | **Rust, 4.9x** |
| Idle RSS | Go **10.9–16.6 MiB**, PSS 9.9 MiB | Rust, by ~6–10 MiB — inside a ~100 MB budget the webview dominates |
| Privacy invariant | Go: reflection, test-failing, **non-recursive** | **Rust — build-failing and recursive (§2.6)** |
| Dependency surface | Go 27 modules / 35 non-stdlib pkgs; Rust probe **95 crates** | Go |
| Inner dev loop | Go cold 8.70 s / incremental **0.36 s** / 257 tests in 1.5 s; Rust probe cold 25.57 s / incremental **0.67 s** | Go, by ~2x — much closer than expected |
| **Cross-compile** | Go **4/4** non-mac targets, one env var each. Rust reached **2/4**; `windows-gnu` fails on **two independent** toolchain blockers even with a zig cross-compiler (§3.4) | **Go, decisively** |
| **Local verifiability** | This box can build and screenshot the Go product. It has no `webkit2gtk`, so the Rust *server* is verifiable but the Tauri shell is not (§3.5) | **Go** |
| **Time to parity** | 20,262 lines of Go (10,008 prod + 10,254 test, 257 tests) to match; estimated **18–29 agent-days** to full parity, **5–9 to the decision checkpoint** (§5.8) | Go — it is already there, and moving |

**The two findings that decide it.**

1. **Cross-compile is a measured regression, not a worry.** Go emits four of
   five release targets from the one Linux runner this project has, with one
   environment variable each. The Rust probe — with `rusqlite`'s bundled
   SQLite — could not cross-compile to `aarch64-linux` without a C
   cross-compiler, and could not reach `x86_64-pc-windows-gnu` *at all*, even
   with a working zig cross-compiler, because `windows-sys` needs
   `x86_64-w64-mingw32-dlltool` at the rustc level and zig's linker cannot
   satisfy mingw's import libraries. Verbatim errors in §3.4. This is fixable
   with new CI toolchains; it is not fixable with a decision.

2. **Parity is defined against a target that moved 48 commits on the day this
   was written.** The Go app took its **sixth** save-schema version in about a
   day and a half of shipping, with a seventh already specified. A parallel
   implementation chasing `main` never converges — so **§5's first hard rule is
   that parity is pinned to a frozen tag, not to `main`.** Get that wrong and
   the comparison never completes, regardless of which language is better.

**Where I think Rust genuinely wins, and it surprised me.** Not size and not
memory — those are small absolute numbers on metrics nobody is complaining
about. It is the **privacy invariant** (§2.6). The Go content-free tests are
the mechanism enforcing this product's one non-negotiable boundary, and they
fail the *test*, not the build, and they **do not recurse** — coverage of
nested types exists only because a human remembered to add a line. Rust's
exhaustive destructuring makes the same guarantee **build-failing**, and a
recursive `serde_json` key walk closes the nesting gap. That is a strictly
better guarantee than the incumbent has. It is also, notably, a technique that
can be back-ported into Go today for ~150 lines — which is exactly the kind of
finding that makes the experiment worth running even when it loses.

**The one thing only Rust can test.** An in-process Tauri app — webview and
server in one process, no sidecar, no port, no handshake. §2.3 argues the prize
is much smaller than it looks (~150 lines, and PR-9 collects most of it in Go),
but it is genuinely unavailable to the Go app, and if the desktop app ever
becomes the product this is the experiment you'd wish you had run.

**What happens to the loser:** exactly what happened to the Bevy track under
ADR 0011 — frozen in-tree, compiling and green at its final commit, excluded
from the product path, not deleted. This repo already has that convention and
should reuse it in both directions (§8).

---

## 1. What the Rust app has to match

Measured on this tree, 2026-08-22.

### 1.1 Line inventory

| | Files | LOC |
|---|---:|---:|
| Go production code | 42 | **10,008** |
| Go test code | 33 | **10,254** |
| **Go total — the parity target** | **75** | **20,262** |
| TypeScript frontend (`app/frontend/src/`) | 19 | 3,316 — **shared, not ported** |
| Rust — Tauri shell (`desktop/src-tauri/`) | 3 | 344 (never compiled) |
| Rust — frozen Bevy legacy (`companion/` 5,600 + `activity/` 2,330 + `shotcap` 84) | 8 | 8,014 |
| **Rust, handwritten, total** | **11** | **8,358** |

The test-to-production ratio is **1.02 : 1**, and that is the most important
number here: parity is not a 10,008-line job, it is a 20,262-line job, because
the 10,254 lines of tests are where this product's invariants actually live.

For scale, the Go app alone is **1.2× the entire handwritten Rust in this
repo** — including the whole frozen Bevy game — and carries 10,254 test lines
against `companion/`'s single 271-line smoke test.

### 1.2 Module inventory — the work-breakdown units

| Package | Prod LOC | Test LOC | Tests | What it is | Parity difficulty |
|---|---:|---:|---:|---|---|
| `internal/store` | 2,164 | **3,825** | **89** | SQLite single signed row, integrity, chained session log, config split | medium (was *high* before the migration was dropped — §3.3) |
| `app` (package main) | 2,844 | 1,540 | 37 | select loop + 4 tickers, CLI verbs, argv-shape dispatch, WS hub, handlers, embed, build-tagged spawn | medium |
| `internal/game` | 2,671 | 2,799 | **75** | sessions, sprints, stats, catalog, coins, identity, history, streaks — the invariant-dense core | **high** |
| `internal/activity` | 837 | 285 | 9 | provider trait + honesty model; darwin (cgo/Cocoa), linux (raw evdev), fake | medium; darwin needs a Mac |
| `internal/lifecycle` | 689 | 918 | 28 | `runtime.json` discovery, OS advisory lock, rotation-lite log | low |
| `internal/paths` | 331 | 458 | 11 | per-OS state/log/bin/cache dirs, `DEXEL_HOME` | low |
| `internal/engine` | 313 | 429 | 8 | ADR 0005/0010 economy + honesty gating | low — small, pure, **start here** |
| `internal/assets` | 176 | 0 | 0 | embedded-vs-disk resolution | low |
| **Total** | **10,008** | **10,254** | **257** | | |

`internal/store` carries **89 of 257 tests and 3,825 of 10,254 test lines** —
37% of the suite guards persistence and integrity.

### 1.3 What is SHARED, not ported — and why this makes the comparison honest

This is the structural reason a parallel implementation is a fair experiment
rather than two different products:

| Artifact | Shared? | Mechanism |
|---|---|---|
| `app/frontend/src/*.ts` (3,316 LOC) + `app/public/js/dexel.js` bundle | **shared, byte-identical** | both servers embed and serve the same committed bundle |
| `app/public/` (index.html, `nes.min.css`, `game.css`, font) — 636,385 B | **shared, byte-identical** | Go: `go:embed all:public`. Rust: `rust-embed` with `folder = "$CARGO_MANIFEST_DIR/../app/public"` |
| `app/assets/` (84 sprite PNGs) — 42,589 B | **shared, byte-identical** | same |
| The WebSocket wire contract (`StateMessage` 18 fields, `CatalogMessage`, the action verbs) | **shared spec** | extracted in P0 as golden JSON captured from the Go server; both sides tested against it |
| The HTTP surface (`/`, `/assets/`, `/api/health`, `/ws`) | **shared spec** | same |
| `docs/ui-spec.md`, ADR 0005 economy numbers, ADR 0010 honesty rules, ADR 0002/0009 privacy rules | **shared spec** | prose + tests on both sides |
| The save file, its key, its schema numbering | **NOT shared** | fresh format, fresh key, own `state.db` path (§3.3) |

**A real Rust advantage falls out of this for free:** `go:embed` can only reach
files inside its own module, which is why `assets/` had to move to
`app/assets/` under EMBED-1. `rust-embed` and `include_dir!` take a relative
path, so `app-rs/` can embed `../app/public` and `../app/assets` **without
copying, moving, or duplicating a single byte.** The shared-frontend property
is therefore enforced by construction, and CI can assert byte-identity on every
served path (§6.2).

### 1.4 Dependency surface

| | Direct | Transitive |
|---|---:|---:|
| Go (`app/go.mod`) | **3** (`golang.org/x/sys`, `modernc.org/sqlite`, `nhooyr.io/websocket`) | **11 in `go.mod`, 27 in the module graph**; 234 packages linked, **35 non-stdlib** |
| Rust probe, measured (§2.1) | 8 | **95 crates** in `Cargo.lock`, 79 in the build tree |
| Rust, frozen Bevy track | 9 | 566 crates |

Go is a 3-dependency program: HTTP, JSON, HMAC-SHA256, embedded filesystems,
atomic writes, process spawning, signals, tar/gzip/TLS are all stdlib. A Rust
port lands at 95 crates — the low end of expectations, and nowhere near the
Bevy track's 566, but still ~3.5× the module count and a larger
license-audit surface for a repo that maintains `THIRD-PARTY-LICENSES.md`.

### 1.5 Concurrency surface — smaller than you'd guess

Non-test code contains **one** `go func` and **four** mutexes. The runtime is a
single `for { select { ... } }` over four tickers (1 s state, 350 ms terminal,
2500 ms ticker, 30 s autosave) plus an `actions` channel; `game.Game` does no
locking at all because, per `ARCHITECTURE.md`, *"the select loop **is** the
lock."*

Two consequences. The translation is easy — one owning task, `tokio::select!`
or plain threads, an `mpsc`. And **Rust's fearless-concurrency advantage buys
almost nothing here**, because the design already eliminated shared mutable
state by construction rather than by type system. Any Rust implementation must
preserve that single-owner invariant rather than "improving" it into
`Arc<Mutex<Game>>`.

---

## 2. Where Rust should win the scorecard

### 2.1 Binary size — measured, 4.9× — on a metric nobody is complaining about

The Go binary, built on this box from HEAD with the exact release flags from
`scripts/build-release.sh` (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build
-trimpath -ldflags "-X main.version=..."`):

| Artifact | bytes | vs release |
|---|---:|---:|
| **release linux/amd64, as shipped** | **18,633,290** | — |
| plain `go build ./app`, no flags | 18,671,187 | +37,897 |
| release + `-ldflags "-s -w"` | **12,718,240** | **−31.7%** |
| release then `strip(1)` | 12,718,168 | −31.7% |
| release, `gzip -9` | 10,419,978 | −44.1% |
| linux/arm64 · windows/amd64 · windows/arm64 | 17,721,874 · 19,028,992 · 17,821,696 | |
| darwin/arm64 at `CGO_ENABLED=0` | **build fails** | see below |

Where it goes, measured by rebuilding with the embed trees emptied to 1-byte
stubs:

| | bytes | share |
|---|---:|---:|
| code + Go runtime | 17,949,586 | **96.3%** |
| embedded assets (90 files) | 683,840 | 3.7% |

Assets are not the size problem — every implementation carries that ~0.68 MB
floor. Of the 17.95 MB of code, `.gopclntab` + `.go.type` + `.go.func` =
**5,200,962 bytes (27.9% of the binary)** is Go runtime reflection and unwind
metadata, which is genuinely the part Rust deletes outright.

**And a Rust probe was actually built and run, not estimated.** A crate with
the dependency surface `app-rs/` would plausibly use — `tiny_http` +
`tungstenite` + `rusqlite`(bundled) + `serde`/`serde_json` + `hmac` + `sha2` +
`rust-embed` + `dirs` — with a main that genuinely exercises all of them (a
real SQLite table plus `PRAGMA user_version`, a real HMAC, a real TCP request
served, a real WS handshake with an echoed frame) and a **680,305-byte**
embedded payload matched to the real app's 678,974 within 0.2%:

| Rust probe build | bytes | vs Go |
|---|---:|---:|
| `opt-level="z"` + `lto` + `codegen-units=1` + `panic="abort"` + `strip`, **static musl** | **2,587,952** | 7.2× smaller than 18.6 MB |
| same profile, `aarch64-unknown-linux-gnu` | 2,500,384 | |
| default `cargo --release` (already strips debuginfo) | 3,540,688 | 5.3× |
| `strip="none"`, symbol table kept | 14,851,880 | |
| `strip="none"`, full DWARF | 30,340,424 | |
| **code only** (payload subtracted) | **1,907,647** | vs Go's ~16,846,989 — **8.8×** |
| hello-world floor, no deps, default release | 365,376 | |

Crates: **95** in `Cargo.lock`, 79 in the build tree.

**The fair comparison is shipping-config to shipping-config: Go with `-s -w` at
12,718,240 bytes against Rust at 2,587,952 — 4.9×.** (Cargo's release default
already strips debuginfo, so comparing against Go's *unstripped* 18.6 MB
overstates the win; the apples-to-apples for that figure is Rust's 14,851,880
with the symbol table kept.)

So Rust wins ~10 MB off the shipped binary. Note two things about that win:
there is **no stated binary-size budget anywhere in this repo** (the only size
figure is an illustrative `"size": 12841984` in a manifest example), and the
incumbent's first 6 MB is available for free without any Rust:

1. **The release script does not pass `-s -w`** — 5,915,050 bytes (31.7%) left
   on the table, one line of shell.
2. **`app/public/js/dexel.js.map` is 218,017 bytes — 31.9% of the entire
   embedded payload** — a pure debug artifact shipped to every end user. It can
   stay committed for CI's bundle-drift check while being excluded from the
   embed.

Together: ~6.13 MB (33%) off the shipped Go binary in two lines.

For the record, the only Rust binary this project has ever actually shipped —
`target/release/companion` (Bevy, release, *stripped*, *dynamically linked*) —
is **101,150,376 bytes, 5.43× the Go server.** That is not a fair comparison
for a server (Bevy is a renderer) but it is worth stating, because it is the
measured counterexample to "Rust binaries are small."

**Bonus finding, worth fixing regardless of any of this.**
`scripts/build-sidecar.sh` lines 36–41 justify its host gate by claiming a
`CGO_ENABLED=0` darwin build *"would compile but ship a **blind** provider,
which would be a dishonest binary."* Verified false: it does not compile —
`./provider_select_darwin.go:12:18: undefined: activity.NewDarwinProvider`,
because `provider_darwin.go` is `//go:build darwin` but uses `import "C"`, so
`CGO_ENABLED=0` drops the file while its caller survives. The real behaviour is
a hard build failure, which is *safer* than the comment claims — but the
comment is the stated reason for the gate, so it should say what happens.

### 2.2 Memory / idle footprint — measured, and the win is small

Measured: the release binary run with the fake provider (`-provider fake -addr
127.0.0.1:0`), sampled from `/proc/<pid>/status` and `smaps_rollup`.

| Metric | idle (just bound) | after 200 HTTP requests |
|---|---:|---:|
| **VmRSS** | **16,956 kB (16.6 MiB)** | **16,956 kB (16.6 MiB)** |
| VmHWM (peak) | 16,956 kB | 16,956 kB |
| **PSS** | — | **10,172 kB (9.9 MiB)** |
| VmSize (reserved virtual) | 1,273,200 kB (1.21 GiB) | unchanged |
| Threads | 13 | 13 |
| CPU after 17 s + 200 req | — | `00:00:00` |

A second sample read 11,176 kB RSS / 8 threads, so the honest range is
**10.9–16.6 MiB**, varying with how many `GOMAXPROCS` worker threads have spun
up. **RSS did not move at all across 200 requests**, including 50 fetches of
the 54 KB JS bundle and 50 of a PNG — embedded `io/fs` reads serve straight out
of the already-resident binary mapping.

Against the only stated budget in the repo, which is not about the server at
all: ADR 0011's revisit trigger is *"webview memory footprint breaking the
'cheap to leave open all day' promise (>~100MB)"*. That is spent on the
**webview** — WKWebView / WebView2 / WebKitGTK — which is the OS's and is
identical either way.

A Rust equivalent would plausibly idle at **5–10 MiB**, so the saving is
**~6–11 MiB out of a ~100 MB budget: 6–11%**, on a metric not near its limit
and whose dominant term the Rust app does not touch. (Estimate, not
measurement — P1 measures it for real with the same method.)

The one genuinely useful thing Rust fixes here is cosmetic: Go reserves
**1.21 GiB of virtual address space** for heap arenas. Irrelevant to real
footprint, alarming in any `ps` or Activity Monitor glance, and the likeliest
thing a user would complain about. That is worth a line in the README either
way.

### 2.3 The in-process Tauri app — the only thing Go structurally cannot do

This deserves the most serious weight, because `desktop/` is already Rust and
the story is clean: one process embeds the webview and the server. No sidecar,
no ephemeral port, no `DEXEL_LISTENING` stdout handshake, no reader thread, no
`SidecarGuard`, no 20-second `HANDSHAKE_TIMEOUT`, no `libc` dependency for a
graceful SIGTERM, and — the non-obvious one — no second executable nested
inside the bundle to sign and notarize separately, which macOS requires.

Three findings shrink the prize considerably.

**(a) The architecture has already decided the window must not own the
runtime.** ADR 0018 and PR-9 (`ARCHITECTURE.md` Decision 17) are an explicit
*inversion*: today the shell spawns the server and kills it on window close;
after PR-9 the shell *attaches* to a runtime it does not own. The exit
criterion, verbatim: *"closing the window leaves the runtime running (`dexel
status` still reports it) — that is the single assertion this whole step exists
to make true."* An in-process Rust server has two options. Re-couple the
lifetimes — reintroducing the exact bug PR-9 deletes, and breaking the owner's
stated intent ("start dexel, forget the terminal, come back to it"). Or keep
them decoupled — in which case you still need `dexel runtime`, `dexel start`,
`runtime.json`, the discovery round-trip and the single-instance lock, which is
the *entire mechanism*, and "unification" saved you the handshake alone.

**(b) The prize is roughly 150 lines.** Everything in-process deletes is
enumerated line-by-line in `ARCHITECTURE.md`'s PR-9 table, out of a **344-line**
file — and PR-9 deletes most of it anyway, in Go, for free, because a shell
that attaches has no child to guard.

**(c) Nobody has run it once.** `desktop/README.md`, verbatim: *"**This code
has never been compiled.**"* / *"`cargo build` / `cargo tauri build` / `cargo
tauri dev` — **never run**"* / *"The app window opening on the game — **never
seen**."* Confirmed: no `desktop/src-tauri/Cargo.lock`, no `target/`. Both
bundle jobs in `.github/workflows/desktop.yml` are dormant behind
`vars.DESKTOP_LINUX_RUNNER` and `vars.MAC_RUNNER`.

**Verdict for the scorecard: this is a genuine Rust-only capability and it
belongs in §6 as a tie-breaker, not as a headline.** It is worth ~150 lines and
one timeout, on a desktop app that has not had its first build. If desktop
primacy is the goal, the highest-value move is *building PR-9*, and the
experiment worth running in `app-rs/` is the in-process shell as a **spike** —
a day, to learn whether it feels better — not as a reason to reach parity on
20,262 lines first.

### 2.4 No-GC determinism — irrelevant here, and worth saying plainly

**This product has no use for it.** The tightest loop is a 50 ms poll of
`CGEventSourceSecondsSinceLastEventType`; state broadcasts are 1 Hz; the save
path runs every 30 s and writes one SQLite row. There is no frame budget, no
audio callback, no allocation storm, no perceptible p99. Any GC pause Go might
produce is invisible behind a 1-second tick — and measured CPU time after 17
seconds and 200 requests was `00:00:00`.

This was a live argument under ADR 0001, when the product was a Bevy game at
60 fps. ADR 0011 moved rendering into the OS webview. The argument left with
it. It should not appear on the scorecard.

### 2.5 Ecosystem fits — itemised, honestly

| Concern | Go today | Rust | Call |
|---|---|---|---|
| SQLite | `modernc.org/sqlite` — pure Go, **which is why `CGO_ENABLED=0` cross-compiles work** | `rusqlite` — mature, fast, real C SQLite | **Better library, worse build input.** §3.4; the port's biggest infrastructure regression. |
| macOS input | ~50 lines of cgo/Objective-C, quarantined in one file | `core-graphics` / `objc2` — pure-Rust FFI, no C compiler, type-safe | **Rust mildly better**, but the macOS SDK is still needed to link, so *"needs a macOS host"* survives. |
| Linux input | pure Go, reads `/dev/input/event*` by hand, no cgo, no library | `evdev` crate — **and this repo already wrote it** (`activity/src/global_input.rs`, ADR 0003) | **Wash.** The Go file's own comment says it *"mirrors the Rust evdev provider"* — this code has already been ported once, the other way. |
| Windows input | **absent** — provider is `HonestyBlind` | same absence | Wash. PR-10 calls it *"the biggest honest gap in the support matrix"*. Language-independent. |
| HTTP + WS | stdlib + `nhooyr.io/websocket` | `tiny_http`+`tungstenite` (measured) or `axum`+`tokio-tungstenite` | **Wash, slight edge Go** — adds a runtime where Go declared nothing. |
| Single-instance lock | `x/sys` flock / LockFileEx | `fd-lock`, `nix`, `windows-sys` | Wash. |
| Detached daemon | *"Go cannot `fork()` safely"* → re-exec a child | real double-fork; `pre_exec` + `setsid` | **Rust better**, modestly. |
| Windows console flash at autostart | needs a second `dexelw.exe` built `-H=windowsgui` (a named, deferred hack) | `#![windows_subsystem = "windows"]` | **Rust better** — deletes the hack. |
| Self-update | stdlib TLS, tar, gzip, zip | `reqwest`+`rustls`, `flate2`, `tar`, `zip` | **Go better** — four crates plus a TLS stack for what is stdlib. |
| Version stamping | `-ldflags "-X main.version=$VERSION"` at link time | no link-time patching; `build.rs` + `env!`/`vergen` | **Go better** — version becomes a compile input, so every release build recompiles. |
| Embedded assets | `go:embed`, stdlib, zero deps, **module-local only** | `rust-embed` / `include_dir!`, **relative paths allowed** | **Rust better here specifically** — it can embed `../app/public` with no copying (§1.3). |

### 2.6 The privacy invariant — where Rust is decisively, structurally better

This is the strongest genuinely-technical argument for Rust in this document,
and it cuts against the intuition that a second implementation must weaken the
guarantees.

The content-free tests (`app/internal/{activity,store,game}/content_free_test.go`)
enforce the product's non-negotiable privacy boundary. Mechanically they are
`reflect.TypeOf(T{})` field enumeration against a hardcoded `map[string]string`
of field-name → type-string, with three rules: an exact `NumField() !=
len(allowed)` fatal, a per-field allow-list lookup, and a forbidden-substring
scan (`title`, `text`, `content`, `keycode`, `clipboard`, `url`, `document`,
`message`, `body`, `keyname`, `char`, …). They guard **22 types**: `Snapshot`
(5 fields), `StateMessage` (18), `SaveData` (13), and 19 nested types. Two
value-level companions (`identity_wire_test.go`, `sessions_wire_test.go`)
marshal a real snapshot and grep the bytes for a leaked name.

They are genuinely deny-by-default — adding, removing, renaming or retyping any
field on any guarded type fails immediately. They have two structural
weaknesses:

1. **They fail the test, not the build.** `go build` and `go vet` stay green.
2. **They do not recurse.** Coverage exists only because someone remembered to
   add a `checkExact(t, reflect.TypeOf(X{}), allowed)` line per nested type. A
   struct introduced two levels down with no matching call is uncaught. (The
   pinned type-strings — `"game.ConfigView"`, `"*store.ActiveSessionSave"` —
   make the hand-enumeration *mostly* sound, since you cannot retarget a pinned
   field to an unpinned type without failing. "Mostly" is doing work.) The
   value-level tests share the flaw: they inspect only **top-level** JSON keys
   via `map[string]json.RawMessage`, so a nested `name` key evades half the
   check.

Rust has no runtime reflection, which sounds like a problem and isn't. Two
mechanisms together are strictly stronger:

- **Exhaustive destructuring with no `..`:** `let SaveData { schema, dev_cash,
  /* …every field… */ } = d;` in a test. Add a field and it **fails to
  compile** (`pattern does not mention field`). That upgrades weakness (1) from
  a test failure to a build failure — which is precisely what the Go comments
  say they want.
- **A recursive `serde_json::Value` key walk** against a deny-by-default
  allow-list plus the same forbidden-substring list. Because it walks the
  *actual serialized wire and save bytes*, it fixes weakness (2) for free:
  nested objects are covered whether anyone remembered them or not.

**Verdict: Rust wins this outright, and it is a HIGH-weight scorecard item
because privacy here is non-negotiable rather than nice-to-have.** Porting the
guard is real work (22 types, 6 test functions, ~800 LOC) with a better result.

**And note this does not require Rust to win anything.** Nothing stops the Go
tests from gaining a recursive `json.Marshal`-and-walk-keys pass today, in ~150
lines. If the Rust experiment produces only this one insight and is then
frozen, it will have paid for a meaningful chunk of itself.

---

## 3. Where Rust loses, and what the experiment risks

### 3.1 The raw work: 20,262 lines to match, ~22,000–28,000 lines to write

Rust for this kind of code typically runs **1.2–1.5×** the Go line count —
explicit error enums instead of `error`, `Result` plumbing, trait impls Go gets
from structural typing. So roughly **12,000–15,000 lines of production Rust and
10,000–13,000 of tests**. Dropping the migration removes the one file that
would have been permanently unpleasant (an `encoding/json` emulator), so this
is now honest translation work rather than translation-plus-archaeology.

### 3.2 The test suite — 257 tests, and they *are* the specification

Measured: **257 `func Test*`**, 0 fuzz, 0 benchmarks, 33 files, **10,254 LOC**,
only **23 `t.Run` subtests**, 101 uses of `t.TempDir()`, 3 files using
`httptest`, 2 using goroutines, 0 using `exec.Command`.

That shape is good news. It is overwhelmingly flat, one-assertion-per-function,
filesystem-and-pure-logic testing — **not** table-driven, which is the pattern
that usually needs restructuring. `t.TempDir()` → `tempfile::tempdir()`,
`httptest` → a real bind on `127.0.0.1:0`. There is no mocking framework, no
golden-file harness, no fixture DSL to reproduce. Mechanically translatable, at
volume.

What is not mechanical:

- **The content-free structural tests** (§2.6) — a real redesign, better result.
  ~800 LOC, 22 types.
- **The ~30 tamper-matrix tests** in `internal/store`: MAC mismatch, row count,
  `PRAGMA user_version` disagreement, future schema, corrupt file, broken
  session chain — each with its own quarantine suffix (`.corrupt`, `.future`,
  `.invalid`). These must be reproduced as *behaviour*, though no longer as
  *bytes* (§3.3).
- **The honesty tests** in `internal/engine`: a blind provider must never
  produce `OnBreak`, and must freeze the idle clock rather than guess. This is
  the product's other non-negotiable and it is easy to reimplement subtly wrong.

### 3.3 Persistence and integrity — no migration, and what that removes

**The owner has ruled out a migration, and that deletes the hardest technical
risk in the original evaluation.** For the record, since it explains why this
section is now short: the Go MAC preimage is
`"dexel-save-integrity-v1" ‖ 0x00 ‖ json.Marshal(SaveData with Mac zeroed)`,
with field order fixed by Go struct declaration order. Reproducing those bytes
in `serde_json` would have required permanently emulating `encoding/json`'s
quirks — integral floats rendering as `0` not `0.0`, nil-vs-empty slices as
`null` not `[]`, six `omitempty`/`omitzero` fields and no others, byte-wise map
key ordering, and HTML escaping of `<`/`>`/`&`. All reproducible; none pleasant;
and no golden vectors exist anywhere in the repo to verify against (the only
64-hex literal in `app/` is the key itself). **None of that is needed now.**

What `app-rs/` should do instead:

- **Its own everything.** Own state path (`<StateDir>/state-rs.db`, or
  `$DEXEL_HOME` pointed elsewhere — the two apps must never share a file while
  both are runnable, or a comparison session corrupts a real one). Own 32-byte
  key. Own domain tags — `dexel-rs-save-integrity-v1`, `dexel-rs-session-log-v1`.
  Own schema numbering starting at 1. A canonical serializer designed for
  clarity, not compatibility.
- **Keep the *design*, which is good and is the part worth comparing.** One
  signed row in a `STRICT` table; `PRAGMA user_version` as the container version
  with no second `meta` table to drift; `PRAGMA quick_check` as the corruption
  gate; `journal_mode = DELETE` so there is no `-wal`/`-shm` at rest to
  complicate quarantine; `synchronous = FULL`; immediate-lock transactions;
  atomic tmp+fsync+rename for the unsigned config; quarantine-by-rename that
  never deletes and never overwrites; the session log chained on the previous
  row's MAC and anchored by a `sessionLogHead` field that the state MAC itself
  covers; and the load-bearing sentinel that a *tampered* save must never
  collapse into *no save*.
- **Keep the config split.** Free text (the dexel's name, session names) lives
  in an unsigned `config.json`; the signed save holds only ids, timestamps, hex
  digests and closed-set enums. That is what makes §2.6's allow-list true.
- **One thing worth deciding early**, because it interacts with §3.4: whether to
  use `rusqlite` (bundled C SQLite — better library, breaks single-toolchain
  cross-compilation) or a pure-Rust store (`redb`, `sled` — restores
  cross-compilation, discards ADR 0016's whole SQLite-native tamper design).
  **Recommendation: `rusqlite`.** ADR 0016's design is a real asset and
  comparing a different persistence design against Go's would muddy the
  scorecard. Take the cross-compile hit and record it as a measured cost.

### 3.4 Cross-compile — measured, and the sharpest finding in this document

Today, from the **one** self-hosted Linux runner this repo has (`jwdlab-runner`,
label `darkmirror`; `desktop-linux` and `mac` are dormant, and the repo is
private so GitHub-hosted mac/Windows runners are not free):

| Target | Go today |
|---|---|
| `linux/amd64` | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build` — zero extra toolchain |
| `linux/arm64` | same, one env var |
| `windows/amd64` | same |
| `windows/arm64` | same |
| `darwin/arm64` | needs a macOS host (cgo Cocoa provider); the script refuses off-darwin with an explicit error |

Four of five with one environment variable each. `docs/plan/ROADMAP.md` marks
this load-bearing, in capitals: DB-1's driver choice was *"**CRITICAL**: pure-Go
driver (modernc.org/sqlite) so `CGO_ENABLED=0` cross-compiles keep working in
the release matrix."* The pure-Go SQLite exists for this reason and no other.

**This was tested on the Rust side, not assumed.** The §2.1 probe crate was
pointed at each target. Verbatim:

| Target | naive `cargo build --target …` | with a `zig cc` cross-compiler shim |
|---|---|---|
| `x86_64-unknown-linux-gnu` | works (host) | — |
| `x86_64-unknown-linux-musl`, static | — | **SUCCEEDS** — 6.94 s, 2,587,952 B, runs. Needed `-C link-self-contained=no`, else `ld.lld: error: duplicate symbol: _start_c` |
| `aarch64-unknown-linux-gnu` | `error occurred in cc-rs: failed to find tool "aarch64-linux-gnu-gcc"` | **SUCCEEDS** — 19.50 s, 2,500,384 B |
| `x86_64-pc-windows-gnu` | `failed to find tool "x86_64-w64-mingw32-gcc"` **and**, independently of SQLite, `error: error calling dlltool 'x86_64-w64-mingw32-dlltool'` → `could not compile 'windows-sys'` | **STILL FAILS**: `error: unable to find dynamic system library 'windows.0.48.5'` / `'msvcrt'` |
| `aarch64-pc-windows-msvc` | needs MSVC libs (`cargo-xwin`) or a Windows host | — |
| `aarch64-apple-darwin` | macOS SDK to link — same blocker as Go, no worse | — |

Read the Windows row carefully. **It has two independent toolchain blockers,
not one:** `libsqlite3-sys` needs a mingw C compiler, and `windows-sys` needs
`x86_64-w64-mingw32-dlltool` at the *rustc* level. Even with a working zig
cross-C-compiler in place the final link fails, because zig's linker cannot
satisfy the mingw import libraries the `windows-gnu` target requests.

**Score: Go 4/4 non-mac targets with one env var each. Rust 2/4, with a
hand-built zig shim and a linker workaround, and nothing for Windows.**
`cross`, `cargo-xwin` and `cargo-zigbuild` can all close this — it is solvable
— but it converts a **solved** problem into a **maintained** one, on a project
with exactly one runner. This is the scorecard's highest-weight Rust loss.

Two adjacent details for the plan:

- **A second, smaller break.** `ARCHITECTURE.md` Decision 19 pins the update
  manifest's platform keys: *"Keys are `<GOOS>-<GOARCH>`, exactly the strings
  `runtime.GOOS` and `runtime.GOARCH` produce. **The updater does zero name
  mapping, so a mapping table cannot drift.**"* Rust's
  `std::env::consts::{OS, ARCH}` yield `linux` / `x86_64`, not `linux` /
  `amd64`. `app-rs/` must emit the Go-shaped strings from a single `const`, or
  reintroduce exactly the drift that decision forbade. `scripts/build-sidecar.sh`
  additionally keys its six triples by Rust triple with a GOOS/GOARCH mapping
  alongside, and `desktop.yml` asserts those filenames — so today three views
  of the same thing agree by construction, and would become three that can
  disagree.
- **Build-cache cost: roughly a wash.** The leftover Bevy track is 70.3 GiB on
  this box, which is tempting to cite and would be dishonest — that is a
  renderer. The measured probe's cache is **166 MB** for one target and profile
  (plus a 113 MB C-object cache) against Go's **150 MB**. Three Rust targets
  across several profiles reached 1.3 GB. Parity for single-target work; a
  few-× penalty for the release matrix.

### 3.5 Local verifiability — asymmetric, and it matters more than it sounds

This repo's central discipline is that **no visual or UX change is trusted
until it is rendered in the real running product and judged by eye** — because,
in the repo's own words, *"isolated mockups have lied to us twice."* ADR 0011
chose the current stack substantially *for* this: *"**Verifiability decides
it.** This development box is Linux; the user is on macOS. A Bevy UI's macOS
behavior is invisible from here… An HTML UI is pixel-identical here and
there."*

The good news for the experiment: **because `app-rs/` serves the same HTML
frontend, that whole property survives.** The screenshot-and-judge loop works
identically against either server, which is exactly what makes a fair
comparison possible at all.

The bad news is narrower but real. `MIGRATION_PLAN.md`: *"**PR-9 (desktop
inversion) cannot be compiled on this box** — no Rust, no webkit2gtk."* Cargo
is in fact present (1.97.1) but `webkit2gtk` is not, so **the in-process Tauri
spike in §2.3 — the one thing only Rust can test — cannot be verified here.**
It needs the `desktop-linux` runner or a Mac. That is a prerequisite for the
most interesting experiment, and it is owner time rather than agent time.

### 3.6 Velocity — the experiment's real risk is the chase, not the cost

**The inner loop, measured on this box.** I expected this to be a rout and it
is not, so the honest reading is the one that matters:

| | Go (the real app) | Rust (the §2.1 probe) |
|---|---:|---:|
| cold build, one target, empty cache | **8.70 s** / 54 s CPU / 816 MB peak | **25.57 s** / 70 s CPU / 955 MB peak (~20 s is `sqlite3.c`, once per C-object cache) |
| clean build, warm C-object cache | — | 5.10 s |
| **incremental rebuild** | **0.36 s** | **0.67–0.72 s** |
| `go vet ./...` | 3.00 s | — |
| full suite, cold / warm | 11.19 s / **1.50–1.85 s** — 257 tests, PASS | n/a |
| full suite with `-race` | 18.11 s, PASS | — |
| build cache, one target | **150 MB** | **166 MB** (+113 MB C cache) |

**~2× slower incremental, ~3× slower cold, caches at parity.** A real
regression on a loop the fleet runs dozens of times a day, but a modest one —
and *not* the argument against Rust. Anyone citing the 70.3 GiB of leftover
Bevy cache as evidence about a server implementation is citing the wrong
program.

**The outer loop is where the risk lives.** This repo is four days old:

| Date | Track | commits | LOC added (`*.go`+`*.rs`) |
|---|---|---:|---:|
| 2026-08-19 | Rust/Bevy | 14 | +1,024 |
| 2026-08-20 | Rust/Bevy | 16 | +5,087 |
| 2026-08-21 | pivot → Go | 31 | +11,189 |
| 2026-08-22 | Go | 48 | +15,310 |

Rust era: 8,358 LOC in 2 days (~4,200/day). Go era: 26,499 in 2 days
(~13,250/day). A ~3.2× ratio — **and the caveats are large enough to state
first:** the two eras are not a controlled experiment. By 2026-08-21 the design
was settled (the PDF, the economy calibration, the honesty rules, the art all
carried over per ADR 0011) and the Go app started from the owner's existing
prototype, not from nothing. The Rust era also carried opencode-fleet friction
that Claude subagent orchestration later replaced. Discount hard — call it 2×
— and the conclusion does not move.

**Because production is not blocked, the 18–29 agent-days is no longer an
opportunity cost against shipping — which is a genuine improvement in this
proposal over a rewrite.** What replaces it is the classic second-system risk,
and here it is quantified: the Go app landed **48 commits and its sixth save
schema** in the last measured day, with a seventh specified. A parallel
implementation chasing `main` is chasing something moving faster than it can
advance, permanently, and "parity" never arrives.

**This is the single most important design decision in §5, and it is not about
Rust at all: pin parity to a frozen tag.** Choose a released version, freeze it
as `PARITY-BASELINE`, and measure `app-rs/` against *that* while `main` moves
on. Rebaseline deliberately, once, at a named checkpoint — never continuously.

### 3.7 The two-language tax — reduced, not removed

| | Today | With `app-rs/` winning |
|---|---|---|
| Backend | Go, 10,008 LOC | Rust, ~12,000–15,000 LOC |
| Frontend | TypeScript, 3,316 LOC + esbuild + npm | **TypeScript, unchanged** |
| Desktop shell | Rust, 344 LOC | folded into `app-rs/` |
| **Languages** | **3** | **2** |

Winning removes a **third** language that is **344 lines** of packaging glue
with no game logic, no economy and no privacy rules in it (`desktop/README.md`:
*"This directory is packaging only"*). It does not remove the two-language tax,
because the frontend stays TypeScript in every scenario — and must, since ADR
0011's verifiability argument depends on the UI being HTML.

**And during the experiment the tax goes up, not down: three languages plus a
second full backend to keep green.** That is the honest cost of running the
comparison, and it is why §7's cheaper paths deserve a real look before
committing to full parity.

One legitimate side-benefit if Rust wins: one toolchain, one lockfile, one
`target/`, one `cargo test` covering both the backend and the desktop shell.

---

## 4. Which will win the comparison — argued

**Prediction: Go wins the scorecard as weighted in §6. Rust wins two of the
three HIGH-weight criteria it can win, loses the two that are hardest to fix,
and cannot win the one that is not a criterion at all — already being finished.**

**Why Rust loses.** The two HIGH-weight losses are both measured, both
specific, and neither is a matter of preference. Cross-compilation goes from
four targets for one environment variable each to two targets with a hand-built
zig shim and nothing at all for Windows, on a project with one runner and a
release matrix that already treats "four from one box" as critical
infrastructure (§3.4). And parity is a 20,262-line target that added 15,310
lines and a schema version on the last measured day (§3.6) — so unless parity
is pinned to a frozen tag, the comparison structurally cannot finish.

**Why Rust's wins are real but light.** A 4.9× smaller binary (12.72 MB →
2.59 MB) on a metric with no stated budget, distributed once by `curl | sh`,
where the incumbent's first 6 MB is free with two lines of build config. A
6–11 MiB lighter idle footprint inside a ~100 MB budget whose dominant term is
a webview neither implementation controls. Both genuinely better; neither worth
much on its own.

**The one Rust win I would weight heavily: the privacy invariant (§2.6).**
This product's non-negotiable boundary is currently enforced by tests that fail
the *test* rather than the build and that **do not recurse into nested types**.
Rust's exhaustive destructuring makes it build-failing, and a recursive
`serde_json` key walk closes the nesting gap. That is not a stylistic
preference; it is a stronger guarantee on the thing the project says it will
never compromise. If the scorecard were weighted purely by "which
implementation is harder to break accidentally," Rust would win it.

**And the wrinkle that keeps it from deciding the comparison:** the same
technique can be back-ported into Go in about 150 lines. A guarantee available
to both implementations cannot distinguish them.

**Where I disagree with the framing the experiment starts from.** The intuition
is that Rust's trump card is the in-process Tauri app. It isn't (§2.3). ADR 0018
deliberately decoupled window lifetime from runtime lifetime; an in-process
server either re-couples them — reintroducing exactly the bug PR-9 exists to
delete — or keeps the whole `dexel runtime` / `runtime.json` / lock discovery
layer, in which case unification saved a handshake and ~150 lines. On top of
which the desktop app **has never been compiled once**, so there is no measured
friction to remove. If desktop primacy is the real goal, the highest-value
action is not `app-rs/` at all — it is registering a runner and building PR-9.

**Why run the experiment anyway.** Three reasons, and I think they hold even
given the prediction:

1. **It is cheap to abort, if you build it in the order §5 specifies.** P0–P2
   is ~4–6 agent-days and ends with the *real frontend animating against the
   Rust server*. That is a genuine, visible, judgeable result, and it is the
   natural off-ramp: if it feels bad there, you have spent a week and learned
   something real. Everything expensive is downstream of that checkpoint.
2. **Three artifacts have standalone value even if Rust is frozen the day
   after P2.** (a) The extracted wire contract and golden `StateMessage`
   captures from P0 — which the Go app **does not have today**, and which are
   the only thing standing between a refactor and a silently changed wire
   format. (b) The recursive privacy-guard technique, back-portable to Go. (c)
   `-s -w` plus dropping the sourcemap from the embed: 6.13 MB off the shipped
   Go binary, found while measuring for this document.
3. **It is the only way to answer the desktop question honestly.** A one-day
   in-process spike in `app-rs/` (P6b) tells you whether the unification
   actually feels better. No amount of reasoning substitutes for that, and Go
   cannot run the experiment.

**The condition under which I would change the prediction.** If the
cross-compile gap closes cleanly — `cargo-zigbuild` or `cross` producing all
four non-mac targets from the one Linux runner, reproducibly, in CI — then
Rust's two HIGH-weight losses become one, and the privacy-invariant win starts
to look decisive. **That is a two-day experiment and it should be run at P0,
before anything else, because it is the cheapest possible way to learn whether
the whole comparison is winnable.** §5 puts it there.

---

## 5. THE PLAN — a parallel Rust implementation in this repo

### 5.0 Naming and layout

**Recommendation: `app-rs/`.**

Rationale: it mirrors `app/` exactly, sorts adjacent to it in any listing, and
makes the parallel-implementation intent legible at a glance — which matters
for a directory that will exist for weeks alongside the shipping product.
`rust/` was considered and rejected: this repo already contains three Rust
trees (`activity/`, `companion/`, `tools/shotcap/`) plus `desktop/src-tauri`, so
a directory called `rust/` would be the least specific name available.

```
app-rs/
  Cargo.toml            # its OWN [workspace] — see below
  crates/
    dexel-store/        # SQLite + integrity + config + paths
    dexel-engine/       # ADR 0005/0010 economy + honesty gating
    dexel-game/         # sessions, sprints, stats, catalog, coins, identity
    dexel-activity/     # provider trait + honesty; linux evdev, fake, (darwin later)
    dexel-bin/          # the binary: CLI dispatch, runtime loop, HTTP+WS, embed
  tests/                # cross-crate parity + wire-contract tests
```

**`app-rs/Cargo.toml` must declare its own `[workspace]`**, exactly as
`desktop/src-tauri/Cargo.toml` already does and for the same documented reason:
the repository root is a workspace whose members are the frozen Bevy track. An
own-workspace declaration keeps `app-rs/`'s lockfile, feature resolution and
`target/` fully independent of a 566-crate tree and its 70 GiB of artifacts.

**Hard rule, stated once and enforced by CI:** `app-rs/` never writes to the
Go app's state path. Use `state-rs.db` under the same `StateDir`, or require
`DEXEL_HOME` to differ. Two runnable implementations sharing one save file will
eventually eat a real one.

### 5.1 The rule that makes the comparison finishable

**Parity is measured against a frozen tag, never against `main`.**

Tag the current release `PARITY-BASELINE` (or reuse `v1.0.0`). `app-rs/` targets
*that* feature set and *that* wire contract. `main` keeps shipping — PR-4
through PR-8, the analytics track, new modals, schema 7 — and `app-rs/` does
**not** chase any of it. Rebaselining happens once, deliberately, at the §6.6
decision checkpoint, and is announced.

Without this rule §3.6's numbers guarantee the comparison never completes. With
it, "parity" is a fixed target that can actually be hit and scored.

*How live this problem is, illustrated:* while this document was being written,
the working tree gained `app/lifecycle_handlers.go` and
`app/internal/game/pause.go` — PR-4 and PR-5 landing in flight — plus edits to
`engine.go`, `game.go`, `history.go`, `session.go`, `store.go` and
`docs/ui-spec.md`. Every "PLANNED" marker in this file was accurate when
written and some are already stale. That is not a criticism of the pace; it is
the exact reason parity must be pinned to a tag.

### P0 — Feasibility probes and the shared contract (2–3 agent-days)

Two things, in this order, because the first can cancel the project for two
days' spend.

**P0a — the cross-compile probe, first, before any Rust is written.** Take the
§2.1 probe crate and try to produce **all four non-mac release targets from
this Linux box, in CI, reproducibly**: `x86_64-unknown-linux-gnu`,
`aarch64-unknown-linux-gnu`, `x86_64-pc-windows-*`, `aarch64-pc-windows-*`.
Try `cargo-zigbuild`, `cross`, and `cargo-xwin`. Record verbatim what works and
what does not.

> **Exit / kill criterion:** if all four targets build from one Linux runner,
> Rust's largest measured disadvantage is gone and §4's prediction weakens
> materially — proceed with confidence. If Windows cannot be reached without a
> Windows runner, **that is a scorecard result, recorded now rather than
> discovered at P6**, and the experiment continues knowing it starts one HIGH
> criterion down.

**P0b — extract the shared contract.** This writes Go, not Rust, and is
valuable whether or not `app-rs/` ever exists.

- `dev_docs/parity/CONTRACT.md`: the WS `StateMessage` (18 fields, camelCase),
  `CatalogMessage`, the client action verbs; the HTTP surface (`/`,
  `/assets/`, `/api/health`, `/ws`) and the same-origin rule; the
  `DEXEL_LISTENING` handshake line; `runtime.json` schema 1; `dexel status
  --json`; the honesty rules (blind provider ⇒ never `OnBreak`, idle clock
  freezes) and the privacy rules (counts and durations only; free text lives in
  `config.json`) as testable statements.
- `dev_docs/parity/golden/*.json`: real `StateMessage` and `CatalogMessage`
  captures emitted by a **new Go test** driving the fake provider through a
  fixed script — the sequence, not just one frame. Note that byte-identity is
  *not* required on the wire (a browser reads `0` and `0.0` identically); the
  contract is **the exact key set, the types, and the value sequence**.
- `scripts/parity-check.sh`: the harness both servers get run through. Boot
  each with the same `-fake-script`, collect the WS frames, diff field-by-field.

**Exit:** the golden captures are committed, a Go test regenerates them and
fails on drift, and `parity-check.sh` passes with the Go server on both sides
of the diff (proving the harness before there is anything to compare).

### P1 — `app-rs/` serves the same frontend, byte-identically (1–2 agent-days)

- Scaffold the workspace. `dexel-bin` serves `/`, `/assets/`, `/api/health`.
- Embed via `rust-embed` with `folder = "$CARGO_MANIFEST_DIR/../app/public"`
  and `../app/assets` — **no copying, no duplication** (§1.3). Keep `-public`
  and `DEXEL_ASSETS_DIR` as dev-only overrides with no implicit cwd probe, so
  "which tree is serving" stays an operator decision, as it is in Go.
- Loopback-only bind, ephemeral port support (`-addr 127.0.0.1:0`), the
  `DEXEL_LISTENING <url>` line as the first stdout output.

**Exit:** a CI job asserts the Rust and Go servers return **byte-identical
bodies** for every served path (`/`, `/js/dexel.js`, `/css/*`, the font, all 84
PNGs); `/api/health` returns the same JSON shape; the real browser client loads
the game shell from the Rust binary with no frontend change whatsoever. Measure
and record binary size and idle RSS here — first real scorecard data.

### P2 — the game visibly runs: engine + WS + fake provider (2–3 agent-days)

**This is the decision checkpoint, and it is deliberately early.**

- Port `internal/engine` (313 LOC, pure, no I/O) — the shakedown for crate
  layout and test conventions. Port its 8 tests first.
- The runtime loop: one owning task, four tickers (1 s / 350 ms / 2500 ms /
  30 s), an `actions` channel. **Preserve the single-owner invariant** (§1.5) —
  no `Arc<Mutex<Game>>`.
- The WS hub with the same-origin check, and enough of `game` to emit a
  well-formed `StateMessage` (sprint progress, dev cash, activity line,
  terminal/ticker lines).
- The fake provider and its script parser (`type:20s,idle:40s,mouse:15s`).

**Exit — the milestone that actually informs the decision:** the **unmodified**
real frontend animates against the Rust server driven by `-fake-script`, and a
screenshot of it, judged by eye against the Go build's, is indistinguishable.
`parity-check.sh` agrees on the emitted `StateMessage` key set. Then **stop and
score** (§6.6) before committing to P3+.

### P3 — game core parity (4–6 agent-days)

Port `internal/game`: sessions, sprints, stats and daily rollover, catalog,
coins, identity, 30-day history, streaks. **Tests before code** — all 75 of
them.

Rebuild the privacy guard per §2.6: exhaustive destructuring with no `..`, plus
a recursive `serde_json::Value` key walk against the allow-lists, covering all
22 guarded types and the forbidden-substring list. Port the meta-test too
(`TestStateMessageRejectsObservedContentFields`) — it exercises the *rules*
against synthetic names, so it cannot be satisfied by loosening the production
allow-list. Port both value-level privacy proofs, and fix their top-level-keys-
only gap.

**Exit:** game test parity; adding a field to `StateMessage` or `Snapshot`
**fails to compile**, demonstrated; the same fake-script produces the same
`StateMessage` sequence as the Go baseline via `parity-check.sh`; ADR 0005
economy numbers reproduce exactly.

### P4 — persistence and integrity, fresh format (3–4 agent-days)

Port `internal/store` + `internal/paths` per §3.3 — own key, own domain tags,
own schema 1, own db path, `rusqlite`. Keep the design: one signed row in a
`STRICT` table, `PRAGMA user_version`, `quick_check`, `journal_mode=DELETE`,
immediate transactions, quarantine-by-rename that never deletes, the chained
session log anchored by a MAC-covered head, the unsigned `config.json` for free
text, and the tampered-is-not-absent sentinel.

Port the store suite first — **89 tests / 3,825 lines, 37% of the whole suite.**

**Exit:** the full tamper matrix passes with the same quarantine suffixes
(`.corrupt`, `.future`, `.invalid`) and the same refuse-to-collapse behaviour;
30 s autosave round-trips; a killed process loses at most the documented ~30 s.

### P5 — real activity providers (2–4 agent-days, partly gated on hardware)

- **Linux evdev first**, because it is the one testable on this box — and start
  from `activity/src/global_input.rs`, which already exists and was green.
  Counts only, no keycodes retained, no app identity under Wayland.
- **Windows stays honestly blind** (`HonestyBlind`), exactly as in Go. Do not
  "improve" this into a guess.
- **macOS via `core-graphics`/`objc2`** — permissionless
  `CGEventSourceSecondsSinceLastEventType` on a 50 ms ticker, never a
  CGEventTap (ADR 0010), plus `NSWorkspace` frontmost-app *localized name only*
  (ADR 0009 — never a window title, document or URL). **Blocked without a Mac.**

**Exit:** the Linux provider reports the same counts as Go on the same physical
input; the engine's honesty gating is verified — a blind provider never
produces `OnBreak` and freezes the idle clock rather than guessing.

### P6 — CLI, lifecycle, release, and the one Rust-only experiment (3–5 agent-days)

**P6a — parity.** `classify` as a pure function over argv and the argv-*shape*
dispatch table (bare / known word / leading flag / unknown word → exit 2), the
subcommand map that makes "documented but unwired" structurally impossible,
`runtime.json` (0600, atomic tmp+fsync+rename), the advisory lock
(`fd-lock`/`LockFileEx`), rotation-lite logging (8 MiB, exactly two files),
detached spawn, the graceful-stop asymmetry (signal on Unix, endpoint on
Windows). Version stamping moves to `build.rs` + `env!` (§2.5). Emit
Go-shaped `<GOOS>-<GOARCH>` strings from one `const` (§3.4).

**P6b — the in-process Tauri spike. One day, timeboxed, and the most
interesting single experiment in the plan.** Fold a Tauri window into
`dexel-bin` — webview and server in one process, no sidecar, no handshake, no
`SidecarGuard` — **while keeping the runtime's lifetime decoupled from the
window's** (§2.3: `dexel runtime` / `dexel start` / `runtime.json` stay).
Requires the `desktop-linux` runner or a Mac (§3.5).

**Exit:** every CLI verb behaves as the Go binary does, asserted by the ported
`cli_test.go` and `cmd_lifecycle` tests; the release matrix produces whatever
P0a proved reachable; and P6b has a written verdict on whether in-process
actually feels better, with a screenshot.

### 5.8 Estimate

| Phase | Agent-days | Blocked on |
|---|---:|---|
| P0a cross-compile probe | 1–2 | — |
| P0b shared contract + golden captures | 1–2 | — |
| P1 serve the same frontend | 1–2 | — |
| **↳ first scorecard data** | | |
| P2 engine + WS + fake provider — **the game runs** | 2–3 | — |
| **↳ DECISION CHECKPOINT (§6.6)** | | |
| P3 game core parity | 4–6 | — |
| P4 persistence + integrity | 3–4 | — |
| P5 providers | 2–4 | **a Mac** for darwin |
| P6a CLI + lifecycle + release | 3–5 | P0a's outcome |
| P6b in-process Tauri spike | 1 | **webkit2gtk host or a Mac** |
| **Total to full parity** | **18–29** | |
| **Total to the decision checkpoint** | **5–9** | |

Basis: ~22,000–28,000 lines of Rust to author *and verify against an existing
behavioural spec*, at roughly 1,000–1,500 verified LOC/agent-day — below the
Rust era's observed ~4,200 LOC/day of greenfield authoring, because a parallel
implementation's cost is dominated by *matching* behaviour, not producing it.

**The number that matters is 5–9, not 18–29.** Everything expensive sits behind
a checkpoint that ends with the real game visibly running on Rust. Budget the
first number; decide before spending the second.

---

## 6. THE COMPARISON SCORECARD

How the two implementations get judged. Baselines are measured today, so the
Go column is already filled in — which is the point: the scorecard is not a
retrospective rationalisation, it is a pre-registered set of criteria with the
incumbent's numbers already on the record.

### 6.1 Gates — pass/fail, not scored

A gate failure means `app-rs/` is not a candidate, regardless of how well it
scores elsewhere. These are the product's non-negotiables.

| # | Gate | How it is checked |
|---|---|---|
| G1 | **Feature parity** against `PARITY-BASELINE` | The §6.5 checklist, every row green |
| G2 | **Test parity** — all 257 Go tests have a named Rust twin or a **written waiver** in `dev_docs/parity/TEST-PARITY.md` giving the test name and the reason | count + review; silence is a dropped invariant, a waiver is a decision |
| G3 | **Privacy boundary** — no raw content, no titles, no keycodes, no URLs anywhere in the wire or the save; free text confined to unsigned config | §2.6's guard, and it must **fail the build** on a new field, demonstrated |
| G4 | **Honesty** — a blind provider never reports `OnBreak`; the idle clock freezes rather than guessing; `STORE_OPEN` freezes earning | ported engine honesty tests |
| G5 | **Same frontend, byte-identical** | CI diff of every served path against the Go server (§P1) |
| G6 | **Wire contract identical** — the unmodified browser client works against either server | `scripts/parity-check.sh` field-by-field over a fixed fake-script run |
| G7 | **No shared state file** — the two implementations cannot corrupt each other | CI assertion on the resolved db path |
| G8 | **Data safety** — quarantine never deletes or overwrites; a tampered save never reads as "no save" | ported tamper matrix, all ~30 cases |

### 6.2 Scored criteria, with weights and measured baselines

| Criterion | Weight | Go baseline (measured today) | How to measure `app-rs/` | Prediction |
|---|---|---|---|---|
| **Cross-compile matrix** | **HIGH** | **4/4 non-mac targets, one env var each**; darwin needs a Mac | same CI job, same one Linux runner, count targets produced | **Go** — Rust reached 2/4; Windows blocked twice (§3.4). P0a can overturn this. |
| **Local verifiability** | **HIGH** | full loop: build → run with fake provider → headless screenshot → judge | same loop; plus can the desktop shell be built here? | **Go** — the Rust *server* is equally verifiable (shared HTML), the Tauri shell is not (§3.5) |
| **Privacy-invariant strength** | **HIGH** | reflection; **test**-failing; **non-recursive**; 22 types hand-enumerated | does adding a field fail the *build*? does the guard recurse? | **Rust** (§2.6) |
| **Time to parity** | **HIGH** | already there, and moving (+15,310 LOC on the last measured day) | actual agent-days per phase vs §5.8's estimate | **Go** |
| Shipped binary size | MED | **12,718,240 B** (`-s -w`); 18,633,290 as shipped today | same release profile, `stat -c%s`, minus the 683,840 B payload | **Rust, 4.9×** (probe: 2,587,952 B) |
| Idle RSS / PSS | MED | **10.9–16.6 MiB RSS**, PSS 9.9 MiB, flat across 200 requests | identical method: `/proc/<pid>/status` + `smaps_rollup`, same fake script, same 200 requests | Rust, by ~6–11 MiB |
| Inner dev loop | MED | cold **8.70 s**, incremental **0.36 s**, 257 tests in **1.50–1.85 s**, `-race` 18.11 s | same, on `app-rs/` | **Go, ~2×** — closer than expected (§3.6) |
| Startup to serving | MED | not yet measured — **add to both**; PR-3's criterion is `dexel start` returning in <2 s | time from `exec` to the `DEXEL_LISTENING` line, 10 runs, median | likely Rust, marginally |
| Dependency / supply-chain surface | MED | 3 direct, 11 in `go.mod`, 27 in the graph, 35 non-stdlib packages linked | `grep -c '^\[\[package\]\]' Cargo.lock` | **Go** — probe measured 95 crates |
| Release-pipeline blast radius | MED | one script, one `-ldflags`, `<GOOS>-<GOARCH>` keys with zero mapping | how many pipeline surfaces change (§3.4's manifest keys, `build.rs` version stamping, six sidecar triples) | Go |
| Code volume for the same behaviour | LOW | 10,008 prod + 10,254 test = 20,262 | same, on `app-rs/` at G1 | Go, ~1.2–1.5× |
| Reserved virtual memory (cosmetic) | LOW | **1.21 GiB** — alarming in `ps`, irrelevant in fact | `VmSize` | Rust |
| **In-process desktop app** | **tie-break** | **structurally impossible** | P6b's spike: does it exist, and does it feel better? | Rust by default — the question is whether it matters (§2.3) |
| No-GC determinism | **not scored** | irrelevant at 1 Hz ticks and `00:00:00` CPU (§2.4) | — | — |

### 6.3 Feature-parity checklist (G1) — the concrete rows

Filled from `docs/plan/ROADMAP.md`'s shipped set and the wire contract. Each
row is checked by `parity-check.sh`, a ported test, or a screenshot.

- **Earning + economy:** keystroke earning, mouse-active earning, the
  anti-mashing clamp (ADR 0005), focus-session earning, app-switch earning
  (ADR 0012), sprint progress and completion, XP and level, `coinsToday`
  breakdown by source.
- **Honesty:** provider honesty reported; blind provider freezes the idle clock
  and never claims a break (ADR 0010); `STORE_OPEN` freezes earning.
- **Identity + config:** the dexel's name, stored in unsigned config, never in
  the signed save (ADR 0014).
- **Store:** catalog with slots/tiers, buy item, buy tint, equip with tint,
  tier-0 defaults granted, owned-items and owned-tints sets.
- **Sessions:** start/stop, active session view, end reasons
  (`user|idle|maxDuration`), completed count, this-week count, longest,
  session names in unsigned config, the chained session log.
- **Analytics:** today and lifetime `StatCounters` (7 fields), daily rollover,
  30-day history, current and longest streak, longest focus block.
- **Wire + UI surface:** `StateMessage` all 18 fields, `CatalogMessage`, every
  client action verb, the terminal lines, the ticker lines, onboarding flag.
- **HTTP:** `/`, `/assets/`, `/api/health` (including `source`/`publicSource`/
  `assetsSource`/`version`/`commit`), `/ws` with the same-origin rule.
- **CLI:** `start`, `stop`, `restart`, `status`, `open`, `logs`, `serve`,
  `runtime`, `version`, `help`; argv-shape dispatch; unknown word → usage on
  stderr, exit 2.
- **Runtime:** `runtime.json` discovery by asking not trusting a pid,
  single-instance OS lock, detached spawn, log rotation-lite, 30 s autosave,
  graceful save on SIGTERM.
- **Packaging:** single self-contained binary, no files needed beside it.

### 6.4 What "winning" means

Stated in advance so the decision is not argued after the fact.

**`app-rs/` wins and becomes the product only if all three hold:**

1. **Every gate G1–G8 passes.** No partial credit; these are the
   non-negotiables.
2. **It loses no HIGH-weight criterion.** In practice that means **P0a must have
   succeeded** — all four non-mac targets from the one Linux runner — because
   otherwise the release matrix regresses on the day of the switch, and no
   amount of binary-size win compensates for a platform the project can no
   longer ship.
3. **It wins at least two HIGH-weight criteria outright.** Privacy-invariant
   strength is the realistic one; the second has to come from cross-compile
   (i.e. P0a not merely succeeding but doing so more cleanly than Go's matrix,
   which is a high bar) or from local verifiability (which requires the desktop
   runners to exist, at which point PR-9 has probably already shipped in Go).

**Go stays the product if any of those fail** — which, per §4, is the predicted
outcome.

**A third outcome, and the most likely useful one: partial adoption.** If
`app-rs/` demonstrably wins the privacy guard and the in-process desktop spike
but loses cross-compile, the correct move is neither "switch" nor "delete" — it
is **harvest**: back-port the recursive privacy guard to Go (~150 lines), keep
`app-rs/` frozen as the reference for the in-process shell, and revisit when
the cross-compile story changes. The scorecard should record this explicitly as
a permitted result rather than treating the comparison as binary.

### 6.5 What happens to the loser

**Frozen in-tree, compiling and green at its final commit, excluded from the
product path, not deleted.** This is not an invention: it is exactly what ADR
0011 did to the Rust/Bevy track — *"kept in-tree, compiling and green as of its
final commit, excluded from the product path. Not deleted — it is the reference
implementation of the mechanics and may return if the web stack disappoints."*
The convention exists and should apply symmetrically.

Concretely, whichever loses gets: a `README.md` at its root stating the date,
the scorecard result, and the revisit triggers; a CI job that keeps it
compiling and its tests green (cheap, and it is what made the Bevy track
available as a real option rather than a corpse); and removal from the release
matrix and from any documentation that tells a user how to run it.

**And the losing side keeps shipping until the winner is ready.** If `app-rs/`
wins, Go remains the released product through one full release cycle after the
switch, with both implementations able to build, so a rollback is real rather
than aspirational. Production is never blocked on the comparison — that is the
premise of the whole exercise.

### 6.6 The decision checkpoint

**Score at the end of P2, not at the end of P6.** At that point the cost is
5–9 agent-days and the evidence available is:

- P0a's verbatim cross-compile result — the single highest-weight criterion,
  answered before most of the work.
- Measured binary size and idle RSS from a real `app-rs/` binary (P1).
- The real frontend animating against the Rust server, screenshotted and judged
  by eye (P2) — the only evidence that speaks to how it *feels*.
- Measured build and test times for the actual crate, not a probe (P2).
- Actual agent-days spent against §5.8's estimate — the calibration for whether
  18–29 is credible.

Three permitted outcomes at the checkpoint: **continue to P3** (the estimate
held and P0a succeeded); **harvest and freeze** (§6.4's third outcome — take the
insights, stop the build); or **stop** (the estimate did not hold, or P2 felt
worse). Naming all three in advance is what keeps a checkpoint from becoming a
formality.

A second checkpoint at the end of P4 covers the case where persistence turns
out harder than expected — historically the likeliest place for an estimate to
break, since it is 37% of the test suite.

---

## 7. Cheaper ways to get most of the same information

Ranked. Each is worth considering **before** committing to §5, and the first
three are worth doing regardless of what happens with `app-rs/`.

**C1 — Extract the wire contract and golden captures now, in Go (P0b alone).**
1–2 agent-days. The Go app has **no** golden wire captures today — nothing
stands between a refactor and a silently changed `StateMessage`. This is the
single highest-value artifact in the whole plan and it does not need Rust to
exist. Do it whether or not the comparison happens.

**C2 — Harvest §2's free wins in Go.** ~1 agent-day total. Add `-s -w` to
`scripts/build-release.sh` and drop `dexel.js.map` from the embed: **6.13 MB /
33% off the shipped binary**, capturing most of Rust's headline size advantage
without Rust. Add the recursive `json.Marshal`-and-walk-keys pass to the
content-free tests: closes the real weakness in the privacy guard (§2.6), ~150
lines. Fix `build-sidecar.sh`'s incorrect darwin comment (§2.1).

**C3 — Run P0a as a standalone two-day question.** "Can we produce all four
non-mac targets from this one Linux runner in Rust?" is the highest-weight
criterion on the scorecard and it is answerable without writing any of the
application. If the answer is no, §4's prediction is close to settled and the
remaining phases buy considerably less.

**C4 — Register the two dormant runners.** `desktop-linux` and `mac` jobs
already exist, are correct, and are gated behind `vars.DESKTOP_LINUX_RUNNER` /
`vars.MAC_RUNNER`. ADR 0015 already recommends the owner's own Mac as a
self-hosted runner at zero cost. This unblocks darwin releases, macOS bundles,
PR-9, P5's darwin provider and P6b's spike simultaneously. **Owner time, not
agent time — and it is a prerequisite for the most interesting parts of both
plans.**

**C5 — Build PR-9 in Go instead.** ~1 agent-day plus C4. The desktop shell
stops owning the server and attaches to a runtime it discovers. This captures
the entire *user-visible* half of the in-process prize — no orphaned process,
closing the window doesn't kill the runtime — in one Rust file **that is
already written**. If desktop primacy is the actual motivation behind wanting
Rust, this is the direct route.

**C6 — Stop at P2 by design.** Rather than treating P3–P6 as the plan and P2 as
a checkpoint, treat **P0–P2 as the whole experiment**: a 5–9 agent-day spike
that ends with the real game running on Rust, screenshotted, with measured size,
memory, and build times, and a written verdict. Then decide whether parity is
worth pursuing with real numbers instead of estimates. This is the version of
§5 I would actually recommend committing to.

---

## 8. Decision record and revisit triggers

**Recommended decision, stated plainly:**

1. **Do C1, C2 and C3 now** (~4 agent-days). They are cheap, they are useful
   regardless, and C3 answers the scorecard's heaviest criterion before any
   application code exists.
2. **Do C4** (owner time). It unblocks more than anything else on this page.
3. **Then run P0–P2 as a bounded spike** (§7's C6), score it against §6, and
   decide at the checkpoint with measurements instead of predictions.
4. **Keep shipping Go throughout.** PR-4 → PR-7 → PR-8 is the path to the first
   public release and nothing here touches it.

**Revisit the whole question if any of these become true:**

- **P0a succeeds cleanly** — all four non-mac targets from one Linux runner,
  reproducibly, in CI. This is the one finding that would most change §4.
- **The memory promise breaks** — the desktop app exceeds ~100 MB resident
  (ADR 0011's own trigger) **and** profiling shows the *server*, not the
  webview, is the dominant term.
- **A capture gap the Go/cgo path cannot close** (ADR 0011's second trigger).
  Note it is currently untested where it matters: the Windows provider is blind
  in both languages.
- **PR-9 ships and the sidecar model proves genuinely fragile in the field** —
  orphaned runtimes, nested-executable notarization pain, handshake failures on
  a real machine. That is the version of the desktop argument that would be
  evidence rather than intuition, and it requires C5 first.
- **The baseline stops moving.** Feature velocity drops and the save schema
  stabilises. A parallel implementation against a stable baseline is a far more
  reasonable proposition than one against a tree that added 15,310 lines in a
  day.
- **The frontend leaves HTML.** ADR 0011's verifiability argument inverts, and
  the question reopens — including whether `companion/` should be unfrozen
  rather than `app-rs/` written.

---

## Appendix — measurements and provenance

All figures taken on this box, 2026-08-22, on `main` at `7a78704`, from a
pristine `git archive HEAD` export (the live tree was being concurrently
modified by other agents mid-evaluation).

| Measurement | Value | How |
|---|---|---|
| Go production LOC | 10,008 (42 files) | `find app -name '*.go' -not -name '*_test.go' \| xargs wc -l` |
| Go test LOC | 10,254 (33 files) | `find app -name '*_test.go' \| xargs wc -l` |
| Go test functions | 257 (`Fuzz` 0, `Benchmark` 0, `Example` 0) | `grep -rhoE '^func Test[A-Za-z0-9_]*' --include='*_test.go' app/ \| wc -l` |
| `t.Run` subtests / `t.TempDir()` | 23 / 101 | grep |
| `go func` / mutexes in prod code | 1 / 4 | grep, excluding `_test.go` |
| Go deps: direct / `go.mod` / graph | 3 / 11 / 27 | `app/go.mod`, `go list -m all` |
| Go packages linked / non-stdlib | 234 / 35 | `go list -deps .` |
| **Go release binary, linux/amd64** | **18,633,290 B** | release flags, `CGO_ENABLED=0` |
| Go binary + `-s -w` | **12,718,240 B** (−31.7%) | same, extra ldflags |
| Go binary, `gzip -9` | 10,419,978 B | `gzip -9 -c \| wc -c` |
| Embedded payload, in-binary | 683,840 B (3.67%) | rebuild with embed trees emptied; diff |
| `dexel.js.map` share of embed | 218,017 B (31.9%) | `du -sb app/public/js` |
| Go runtime metadata in binary | 5,200,962 B (27.9%) | `size -A` (`.gopclntab`+`.go.type`+`.go.func`) |
| **Go idle RSS (fake provider)** | **10.9–16.6 MiB**; PSS 9.9 MiB; flat across 200 req | `/proc/<pid>/status`, `smaps_rollup` |
| Go reserved virtual | 1.21 GiB | `VmSize` |
| Go build: cold / warm / vet | 8.70 s / 0.36 s / 3.00 s | wiped `GOCACHE`, `/usr/bin/time` |
| Go tests: cold / warm / `-race` | 11.19 s / 1.50–1.85 s / 18.11 s — all PASS | `go test -count=1 ./...` |
| Go build cache | 150 MB cold; 1.12 GB after 4 cross-builds + `-race` | `du -sb $GOCACHE` |
| `darwin/arm64` at `CGO_ENABLED=0` | **build failure** (`undefined: activity.NewDarwinProvider`) | `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build` |
| **Rust probe** (`tiny_http`+`tungstenite`+`rusqlite`bundled+`serde`+`hmac`+`sha2`+`rust-embed`+`dirs`; 680,305 B payload; every dep exercised at runtime) | | built in the scratchpad, not the repo |
| ↳ opt-z+lto+cgu1+panic=abort+strip, static musl | **2,587,952 B** | `--target x86_64-unknown-linux-musl` |
| ↳ default `cargo --release` | 3,540,688 B | |
| ↳ `strip="none"` / +full DWARF | 14,851,880 / 30,340,424 B | |
| ↳ code only (payload subtracted) | 1,907,647 B | |
| ↳ crates | **95** in `Cargo.lock`, 79 in the build tree | `grep -c '^\[\[package\]\]'` |
| ↳ build: cold / clean-warm-C / incremental | 25.57 s / 5.10 s / **0.67–0.72 s** | `/usr/bin/time` |
| ↳ build cache | 166 MB + 113 MB C-object cache | `du -sb` |
| ↳ hello-world floor (no deps) | 365,376 B release; 310,672 B opt-z | |
| ↳ cross `aarch64-unknown-linux-gnu` | naive **fails** (`failed to find tool "aarch64-linux-gnu-gcc"`); zig cc **succeeds**, 19.50 s | |
| ↳ cross `x86_64-pc-windows-gnu` | naive **fails** (mingw gcc **and** `dlltool` for `windows-sys`); zig cc **still fails** (`unable to find dynamic system library 'windows.0.48.5'` / `'msvcrt'`) | |
| Frozen Bevy binary (release, stripped, dynamic) | 101,150,376 B — **5.43× the Go binary** | `stat -c%s target/release/companion` |
| Leftover Bevy build cache | 70.3 GiB (55 + 8.8 + 6.9) | `du -sb target target-m3-review tools/shotcap/target` |
| Root `Cargo.lock` crates | 566 | `grep -c '^\[\[package\]\]' Cargo.lock` |
| Rust handwritten LOC / tests | 8,358 (11 files) / 89 `#[test]` | `wc -l`, grep |
| Tauri shell LOC / build state | 344 / **never built** (no `Cargo.lock`, no `target/`) | `wc -l`; `ls desktop/src-tauri` |
| Frontend TS LOC | 3,316 (19 files) | `find app/frontend/src -name '*.ts' \| xargs wc -l` |
| Commits by day (Aug 19–22) | 14 / 16 / 31 / 48 | `git log --date=short --pretty=%ad \| sort \| uniq -c` |
| LOC/day (`*.go`+`*.rs`) | +1,024 / +5,087 / +11,189 / +15,310 | `git log --numstat` per day |
| `CurrentSchema` | 6 (7 specified in PR-5) | `app/internal/store/store.go:326` |
| Toolchains | cargo/rustc 1.97.1; go 1.27.0 at `/home/darkmirror/go-toolchain` (not on `PATH`); **no webkit2gtk**; no `upx`; no `gcc` (a zig-backed `cc` shim only, so local `go test -race` needs `CC=cc`) | — |

Labelled as estimates, not measurements: `app-rs/`'s idle RSS (§2.2), its
production and test LOC, and every agent-day figure (§3.1, §5.8). P1 and P2
replace the first two with real numbers; the checkpoint in §6.6 replaces the
third.

One housekeeping note from the measurement pass: local `go test -race` needs an
explicit `CC=cc` on this box, because there is no `gcc`. CI is unaffected —
`build.yml` sets `CGO_ENABLED: "1"` and installs `build-essential` if `cc` is
missing.
