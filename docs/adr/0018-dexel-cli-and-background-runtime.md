# 0018 — dexel CLI and background runtime: one binary, argv-shape dispatch, ask-don't-trust discovery

Status: accepted (2026-08-22, PR-3) · Realises `dev_docs/production-runtime/ARCHITECTURE.md`
Decisions 1–3, 6, 7, 9, 12 (`dev_docs/production-runtime/MIGRATION_PLAN.md` §PR-3)
· Extends ADR 0015 (the Tauri sidecar this design's endpoint half will
eventually invert) and ADR 0016 (reuses its tmp+fsync+rename write recipe)
· Honours ADR 0010 (never claim something you cannot know — a pid is never
trusted, an unknown command is never silently swallowed)

## Context

Before this change, `dexel` (`app/main.go`) only knew how to run in the
foreground: `go run .` or `./dexel` started an HTTP+WebSocket server on
`127.0.0.1:8080`, printed to the terminal that spawned it, and exited on
SIGINT/SIGTERM after a final save. That server was always tied to a process
someone else owned — a terminal window, or (per ADR 0015) a Tauri sidecar
that killed it the moment its window closed. `docs/plan/RUN-MODES.md`
records this honestly as "mode A."

The owner's product intent — "start dexel, forget the terminal, come back to
it" — needs something that outlives any one window. The load-bearing fact,
verified by reading the tree rather than assumed
(`dev_docs/production-runtime/ARCHITECTURE.md` §1.1): **the runtime already
behaves like a background service**. `main()`'s `for { select { ... } }`
loop is driven by wall-clock `time.Ticker`s, not by connection count;
`hub.broadcastState` iterates an empty client map as a no-op, not a stall.
Closing every browser tab already does not stop tracking or autosaving. What
was missing was never the loop — it was a way to *start* that loop detached
from a terminal, *find* it again later, *stop* it on purpose, and never
run two of them by accident.

The available design (`ARCHITECTURE.md` §§2–5, `MIGRATION_PLAN.md` §PR-3)
answers that with one file (Decision 1: the CLI, the runtime, the web UI and
eventually the updater are the same binary) and a handful of small,
independently-testable mechanisms, and names one genuine owner fork up
front.

## Owner-decision fork — what does a bare `dexel` do? (Fork A)

Today bare `./dexel` / `go run .` starts the foreground server. The owner's
plan says bare `dexel` should mean "start-if-needed, then open" — the
product behaviour, not the developer behaviour. Those two readings conflict
and something has to give.

**Decided (the recommended default): bare `dexel` = start-if-needed + open.**
The foreground developer path gets an explicit name, `dexel serve`, instead.
The dispatch rule that makes this safe is a rule about **argv shape**, not a
word list, and it is what keeps every existing invocation working:

| argv shape | Meaning |
|---|---|
| `dexel` (no arguments) | start-if-needed, then open |
| `dexel <known-subcommand> [args]` | that subcommand |
| `dexel -addr ...` (first argument starts with `-`) | **legacy foreground runtime — byte-identical to before this ADR** |
| `dexel <unknown-word>` | usage on stderr, exit 2 |

The third row is why nothing downstream breaks: `.github/workflows/desktop.yml`'s
sidecar assertion, the Tauri shell's `-addr 127.0.0.1:0`, and every documented
`go run . -fake-script ...` invocation begin with a flag, so all of them keep
meaning exactly what they meant the day before this file existed. The fourth
row is deliberate honesty, not generosity: a typo like `dexel statsu`
silently starting a background runtime would be the exact class of surprise
ADR 0010's posture rejects, so an unknown word is a reported error, never a
guess.

**The accepted cost:** bare `go run .` is the one invocation whose meaning
actually changes. It no longer serves on `127.0.0.1:8080` in the foreground;
it now spawns a detached runtime and opens a browser. Every doc that showed
that bare form as the way to run the game during development — the README's
Quick start and Development sections, and `docs/plan/RUN-MODES.md`'s mode A —
had to be edited to say `go run . serve` instead. That edit is this ADR's
paper trail for Fork A's cost; the alternative (keep bare `dexel` as the
foreground server, require an explicit `dexel open`) was rejected because it
makes the *product* behaviour the one requiring extra typing, which is
backwards from what was asked for.

## Decision

**1. One binary, `package main`, no restructuring.** `dexel` is
simultaneously the CLI (`dexel start`, `stop`, `status`, ...), the runtime
(`dexel runtime`, today's loop unchanged), and the already-embedded web UI.
`app/main.go`'s pre-existing `package main` and its three same-package test
files (`main_test.go`, `handlers_test.go`, `store_gate_test.go`) stay as they
are; new files (`cli.go`, `cmd_lifecycle.go`, `spawn_unix.go`,
`spawn_windows.go`) join `package main` rather than moving the runtime into
`app/internal/runtime`, which would have rewritten three test files for no
product benefit. `main()` shrinks to the ~20-line dispatcher
`os.Exit(dispatch(classify(os.Args[1:])))`; today's `main()` body survives
verbatim as `runServe`.

**2. `classify` is a pure function over argv (`app/cli.go`).** It takes
`os.Args[1:]` and returns a `decision{Kind, Name, Args}` with no filesystem
access, no environment reads, no process spawning — precisely so Fork A's
table above can be unit-tested (`cli_test.go`) without starting a server, a
browser, or a subprocess. `dispatch` then executes exactly one of: run
`cmdOpen` (bare), run `runServe(modeLegacy, ...)` on the full argv (leading
flag), call the matched subcommand's `run` (known word), or print usage to
stderr and return exit code 2 (unknown word). The subcommand table
(`subcommands map[string]subcommand`) pairs each verb's help text with its
implementation in the same map entry, which makes "documented but unwired"
or "wired but undocumented" structurally impossible — `dexel help`'s output
and `classify`'s notion of "known word" read the same data. `pause`,
`resume`, `autostart`, and `update` are deliberately absent from that table
until the PRs that own them land (PR-5/6/7): a listed word that does nothing
is worse than an honestly-unknown one.

**3. `serve` and `runtime` are the same code path under two names, with
different defaults.** `runServe(mode, args)` takes a `serveMode` — `modeLegacy`,
`modeServe`, or `modeRuntime` — that only changes two things: the default
`-addr` (`127.0.0.1:8080` for legacy/serve, so `go run . serve` behaves
exactly as `go run .` did before this ADR; `127.0.0.1:0` for runtime, an
OS-assigned port so a detached instance never fights the foreground default
for :8080 — the first of two bugs this design's own gate caught, see
Consequences) and whether the lock/`runtime.json` layer (Decision 6 below)
is engaged (`serve` skips it by default, so a developer can run a scratch
instance next to a real one exactly as today). `serve` and `runtime` exist
as two names for one reason: a log line, a `launchd`/`systemd` unit, and a
human's terminal should each say the thing they mean.

**4. `dexel start` spawns a detached child of itself, and never waits on
it.** Go cannot safely `fork()`, so there is no self-daemonizing double-fork;
instead `os.Executable()` re-execs the same binary as `dexel runtime`, with
stdin from `/dev/null` and stdout/stderr redirected to a rotation-lite log
file (previous file kept once, current one rotated past 8 MiB — the
`app/internal/lifecycle/logfile.go` half of this design). `cmd.SysProcAttr`
comes from a build-tagged `detachAttr()` in `spawn_unix.go`/`spawn_windows.go`,
mirroring this repo's existing `provider_select_{linux,darwin,other}.go`
pattern. Critically, `cmd.Process.Release()` is called and `cmd.Wait()` never
is: waiting would keep `dexel start` alive as the child's parent and turn
the detached runtime into a zombie the instant the parent exits.

**5. Readiness is polled, not parsed.** A detached child's stdout is the log
file, not a pipe the parent holds, so the `DEXEL_LISTENING` stdout handshake
(still in the binary, still what `dexel serve` and `desktop.yml`'s sidecar
assertion rely on) is not available to `dexel start`. Instead `start` polls
for `runtime.json` and then for a confirmed-alive status, up to a fixed
timeout, and on failure prints the log's own last lines rather than a bare
error — a failure to start should show what the runtime itself said, not
send the user hunting for a log path.

**6. Discovery: `runtime.json`, resolved by asking, never by trusting a
pid.** Immediately after `net.Listen` succeeds, the runtime writes
`runtime.json` (`{schema, pid, port, url, version, commit, startedAt,
token}`, mode `0600`) atomically, using the same tmp+fsync+rename recipe
ADR 0016's `store.SaveConfig` already established — one write primitive,
reused rather than re-invented. A pidfile alone cannot survive pid reuse, so
every verb that needs to know "is a dexel actually running" — `status`,
`start`'s pre-flight check, `stop` — calls `lifecycle.Discover`, which reads
the file, then round-trips an HTTP call to the address inside it, and treats
connection-refused, a timeout, or any answer as confirming liveness; on
failure it **removes the stale file** so a dead runtime can never be
mistaken for a live one on the next check. `lifecycle.Query` is the
read-only sibling used only by `start`'s own readiness poll, because that
poll is racing the child it just spawned and must not delete the
`runtime.json` that child is in the middle of correctly writing — the
second of two bugs this design's gate caught (Consequences).

**7. Single instance is an OS lock, not a file's existence.**
`<StateDir>/runtime.lock` is held for the process lifetime via
`flock(LOCK_EX|LOCK_NB)` on Unix and `LockFileEx(...,
LOCKFILE_FAIL_IMMEDIATELY)` on Windows, both through `golang.org/x/sys`
(already present as an indirect dependency in `app/go.mod`; promoting it to
direct adds no new module to the tree). A second `dexel start` refuses
immediately, naming the running pid, and the OS releases the lock even if
the first process is `kill -9`ed — so there is no stale-lock false-positive
class of bug the way a pidfile-as-mutex would have.

**8. The endpoint-vs-signal split is deliberate, and PR-4 owns the other
half.** This ADR's `stop` sends a real OS signal (SIGTERM on Unix,
`TerminateProcess` on Windows) at the pid `runtime.json` names, because that
signal handler (`case <-sigCh:` in `main.go`) already does the correct
save-then-exit sequence and existed before this ADR. Every liveness check —
`Discover`/`Query` — round-trips `GET /api/health`, which already exists and
is deliberately unauthenticated (the browser reads it too), and already
sends an `X-Dexel-Token` header read from `runtime.json` even though nothing
server-side checks it yet. `ARCHITECTURE.md` Decision 5 specifies the
*next* step this ADR does not take: a dedicated, token-gated
`/api/lifecycle/{status,pause,resume,stop}` surface, with `stop` there
reusing the same signal-handler body over a channel instead of a raw signal,
and `status` gaining `paused`/`providerHonesty` fields `/api/health` was
never meant to carry. Sending the token now, on every probe, means PR-4 only
has to start *enforcing* a contract the CLI already speaks — the CLI side of
that boundary is complete as of this ADR.

## Consequences

- **Fork A's cost is paid, and paid in exactly one place per document.**
  Bare `go run .` no longer runs the foreground server; every place that
  showed that invocation as the way to develop against — README's Quick
  start and Development sections, `docs/plan/RUN-MODES.md`'s mode A — now
  says `go run . serve`. Nothing that already started with a flag needed any
  change: CI's sidecar job, the Tauri shell's `-addr 127.0.0.1:0`, and every
  `-fake-script` example in this repo's own docs are unaffected by
  construction, because the dispatch rule is about argv shape, not a
  rewritten default.
- **Two real bugs were caught by this design's own gate, not by inspection:**
  the detached runtime's default `-addr` was initially hardcoded to
  `127.0.0.1:8080` instead of the mode-specific ephemeral default, which
  would have made a background runtime fight a foreground `serve` for the
  same port; and the readiness poll inside `start` originally called the
  *cleaning* `Discover` instead of the read-only `Query`, which could delete
  the `runtime.json` its own just-spawned child was in the middle of
  writing. Both are fixed as described in Decisions 3 and 6 above, and the
  second is now a regression test (`lifecycle`'s race-condition coverage).
- **Closing a window still cannot stop the runtime**, which is the point:
  the window never owned the runtime's lifetime, so there is no code path
  left for it to accidentally end it. Only `dexel stop` (or, once PR-4 lands,
  its authenticated HTTP twin) exits the process.
- **What this ADR does not decide, on purpose:** `pause`/`resume` semantics
  (`dexel pause`/`resume` exist as CLI verbs once PR-4 lands but flip a
  boolean nothing reads until PR-5); `autostart` (PR-6); self-update (PR-7);
  `uninstall`; a tray/menubar icon; and the Tauri shell actually calling
  `dexel start`/`status` instead of owning a sidecar (PR-9, ADR 0015's
  amendment). Naming them here is what keeps this ADR from quietly growing
  into all of them.
- **Revisit trigger:** if a future platform needs a daemonization mechanism
  this design's `exec`-a-detached-child approach cannot express (a real
  double-fork target, or a platform where `flock`/`LockFileEx` do not behave
  as documented), the single-instance and detach mechanics — not the argv
  dispatch — are what would need to change.
