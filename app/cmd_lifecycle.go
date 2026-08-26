// cmd_lifecycle.go — the lifecycle verbs: start, stop, restart, status,
// open, logs (docs/production-runtime/ARCHITECTURE.md Decision 3,
// MIGRATION_PLAN.md §PR-3).
//
// The single idea every verb here is built on (ARCHITECTURE.md Decision
// 6): "is a dexel running?" is answered by ASKING one — read
// runtime.json, then round-trip it over HTTP — and never by trusting a
// pid. lifecycle.Discover is that question for every verb that makes a
// DECISION — it deletes any file it cannot confirm, so a stale
// runtime.json can never be mistaken for a live runtime — and
// lifecycle.Query is the read-only variant the readiness poll inside
// `start` uses, because that poll races the child it just spawned and
// must not delete the file that child is in the middle of writing.
//
// PR-4 (MIGRATION_PLAN.md §PR-4, ARCHITECTURE.md Decision 6/8) landed the
// other half of that idea:
//   - the round-trip target is GET /api/lifecycle/status (auth'd, and
//     reporting the answering process's own pid), not GET /api/health
//     (unauthenticated by design, and silent on pid) — Query/Discover's
//     Probe call now requires the returned pid to equal runtime.json's,
//     the last defence against pid reuse Decision 6 promises;
//   - `stop` calls POST /api/lifecycle/stop first (lifecycle.RequestStop)
//     — it drives the exact same shutdown sequence a signal does
//     (app/main.go's shared shutdown() closure) — and falls back to
//     SIGTERM/TerminateProcess (stopViaSignal) only if that endpoint is
//     unreachable or does not act within stopGrace;
//   - `status` reports app/internal/lifecycle.Status.Live, PR-4's
//     GET /api/lifecycle/status body, preferring it over runtime.json's
//     own copy for version/commit (see cmdStatus);
//   - the X-Dexel-Token header PR-3 already sent on every probe is now
//     actually enforced server-side (app/lifecycle_handlers.go).
//
// PR-5 (MIGRATION_PLAN.md §PR-5) adds the last two verbs,
// `pause`/`resume`, on the same endpoint-only footing: they post to
// /api/lifecycle/{pause,resume} and there is deliberately no signal
// fallback, because no signal can express "stop the activity provider"
// (see lifecycle.RequestPause). `status` reports the live `paused` flag
// the same round-trip already returns.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/lifecycle"
	"github.com/jawwadzafar/dexel/app/internal/paths"
	"github.com/jawwadzafar/dexel/app/internal/store"
)

// startTimeout bounds how long `dexel start` waits for the detached child
// to become reachable before giving up and printing its log
// (ARCHITECTURE.md §5: "polls for runtime.json and then for a 200 ...
// up to 10s, and on failure prints the last 20 lines of the log and exits
// non-zero"). A healthy start takes tens of milliseconds; this bound
// exists for the pathological cases (a busy fixed port, an unreadable
// state dir) where failing loudly beats hanging.
const startTimeout = 10 * time.Second

// startPollInterval is how often readiness is re-checked. 25ms keeps
// `dexel start` well inside MIGRATION_PLAN.md §PR-3's "<2s" exit
// criterion — in practice it returns in well under 200ms — without
// spinning a CPU.
const startPollInterval = 25 * time.Millisecond

// stopGrace is how long `dexel stop` waits for a graceful exit before
// escalating (PLATFORM_NOTES.md §2: "Escalate to SIGKILL after a 5s
// grace ... and say so loudly").
//
// It used to be justified as "it matches the runtime's own 5s
// http.Server.Shutdown timeout" — which was the whole problem (BUG-9):
// equal budgets mean the CLI's patience expires at the exact moment the
// runtime's last phase does, so ANY delay ahead of that phase ends in a
// SIGKILL. The runtime's graceful path now bounds itself to well under a
// second in total (see main.go's shutdown closure and
// httpShutdownGrace), so 5s is deliberately generous headroom for a slow
// disk rather than a coin flip, and shutdown_budget_test.go asserts that
// relationship instead of a comment claiming it.
const stopGrace = 5 * time.Second

// killGrace is the short second wait after SIGKILL, purely so `dexel
// stop` can report honestly whether the process actually went away.
const killGrace = 3 * time.Second

// logTailOnFailure is how many log lines a failed `dexel start` prints
// (ARCHITECTURE.md §5's "last 20 lines").
const logTailOnFailure = 20

// desktopAppName is the optional second artifact `dexel open` prefers
// over a browser (ARCHITECTURE.md Decision 17). It is not required to run
// dexel and not required to see the UI — the browser fallback is "the
// shippable path meanwhile" (MIGRATION_PLAN.md sequencing note 6).
const desktopAppName = "dexel-desktop"

// cliEnv is everything a lifecycle verb needs from the outside world,
// resolved exactly once per invocation. Bundling it (rather than calling
// paths.* and os.Executable() ad hoc inside each verb) means a path
// failure is reported once, in one shape, before any command has done
// half its work.
type cliEnv struct {
	stateDir string
	logPath  string
	binDir   string // "" if it could not be resolved; only `open` needs it
	self     string // this executable, re-exec'd as the detached runtime
	goos     string
	out      io.Writer
	errOut   io.Writer
	client   *http.Client
}

// cliEnvOrReport resolves the environment, or reports why it could not
// and returns the exit code to use. Every verb starts with this.
func cliEnvOrReport() (*cliEnv, int) {
	stateDir, err := paths.StateDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dexel: resolve state dir: %v\n", err)
		return nil, 1
	}
	logPath, err := paths.LogFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dexel: resolve log path: %v\n", err)
		return nil, 1
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dexel: locate my own executable: %v\n", err)
		return nil, 1
	}
	// BinDir is only consulted by `open` when looking for the optional
	// desktop app, so failing to resolve it must not break start/stop/
	// status. Empty means "skip that candidate".
	binDir, _ := paths.BinDir()
	return &cliEnv{
		stateDir: stateDir,
		logPath:  logPath,
		binDir:   binDir,
		self:     self,
		goos:     runtime.GOOS,
		out:      os.Stdout,
		errOut:   os.Stderr,
		client:   lifecycle.ProbeClient(lifecycle.DefaultProbeTimeout),
	}, 0
}

// ---------------------------------------------------------------- start

// cmdStart spawns the detached runtime and waits for it to answer
// (ARCHITECTURE.md Decision 12, PLATFORM_NOTES.md §2).
//
// Any extra arguments are forwarded verbatim to the `runtime`
// subcommand, so `dexel start -provider fake` or
// `dexel start -addr 127.0.0.1:9999` work. That is additive to the
// design's `exec.Command(self, "runtime")` and costs nothing: `runtime`
// and `serve` are the same code path with the same flag set, and it is
// what makes an end-to-end test of the detached path possible without a
// real input device.
//
// Already-running is reported and treated as SUCCESS (exit 0), not as an
// error. `start` is the verb an XDG autostart entry and a Windows Run key
// invoke (PLATFORM_NOTES.md §3), and PR-6's `enable`-twice idempotency
// depends on a second start being a harmless no-op rather than a failure
// that a login session would surface as a broken app. The "refuses"
// half of MIGRATION_PLAN.md §PR-3's criterion is about not spawning a
// SECOND PROCESS — which it does not.
func cmdStart(args []string) int {
	env, code := cliEnvOrReport()
	if env == nil {
		return code
	}
	return env.start(args)
}

func (e *cliEnv) start(args []string) int {
	st, err := lifecycle.Discover(e.stateDir, e.client)
	if err != nil {
		fmt.Fprintf(e.errOut, "dexel: %v\n", err)
		return 1
	}
	if st.Running {
		fmt.Fprintf(e.out, "dexel is already running (pid %d) — %s\n", st.Runtime.Pid, st.Runtime.URL)
		return 0
	}
	if st.Cleaned {
		// Say it out loud rather than silently mutating the state dir.
		fmt.Fprintf(e.errOut, "dexel: removed a stale runtime.json (%s)\n", st.Reason)
	}

	// Rotation-lite, at start only (PLATFORM_NOTES.md §4). A log we
	// cannot rotate is a warning, never a reason to refuse to start.
	if rotated, rotErr := lifecycle.RotateLog(e.logPath); rotErr != nil {
		fmt.Fprintf(e.errOut, "dexel: could not rotate %s: %v\n", e.logPath, rotErr)
	} else if rotated {
		fmt.Fprintf(e.errOut, "dexel: rotated %s (over %d MiB) to %s.1\n", e.logPath, lifecycle.MaxLogBytes>>20, e.logPath)
	}

	logFile, err := lifecycle.OpenLog(e.logPath)
	if err != nil {
		fmt.Fprintf(e.errOut, "dexel: %v\n", err)
		return 1
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		_ = logFile.Close()
		fmt.Fprintf(e.errOut, "dexel: open %s: %v\n", os.DevNull, err)
		return 1
	}

	cmd := exec.Command(e.self, append([]string{"runtime"}, args...)...)
	cmd.Stdin = devNull
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = detachAttr()
	startErr := cmd.Start()
	// Both descriptors are dup'd into the child by Start; this process
	// has no further use for them.
	_ = logFile.Close()
	_ = devNull.Close()
	if startErr != nil {
		fmt.Fprintf(e.errOut, "dexel: spawn runtime: %v\n", startErr)
		return 1
	}
	pid := cmd.Process.Pid
	// Release, never Wait (ARCHITECTURE.md §5): waiting would keep this
	// process alive as the child's parent and turn the runtime into a
	// zombie the moment `dexel start` returned. Releasing hands it to
	// init/launchd.
	if relErr := cmd.Process.Release(); relErr != nil {
		fmt.Fprintf(e.errOut, "dexel: release child %d: %v\n", pid, relErr)
	}

	// Readiness is POLLED, not parsed (ARCHITECTURE.md §5): the child's
	// stdout is the log file, not a pipe we hold, so its DEXEL_LISTENING
	// handshake is not available to us. The handshake stays in the binary
	// for `dexel serve` and for desktop.yml's assertion; it is simply no
	// longer the readiness contract.
	ready, err := e.waitReady(startTimeout)
	if err != nil {
		fmt.Fprintf(e.errOut, "dexel: %v\n", err)
		return 1
	}
	if !ready.Running {
		fmt.Fprintf(e.errOut, "dexel: runtime (pid %d) did not become ready within %s\n", pid, startTimeout)
		e.dumpLogTail(logTailOnFailure)
		return 1
	}
	fmt.Fprintf(e.out, "dexel started (pid %d) — %s\n", ready.Runtime.Pid, ready.Runtime.URL)
	return 0
}

// waitReady polls until a live runtime is confirmed or the deadline
// passes. It uses the READ-ONLY lifecycle.Query, never Discover: we are
// racing the child we just spawned, and a cleaning discovery could delete
// the runtime.json that child had just correctly written (see Query's doc
// comment). Cleanup is the job of the next verb that decides nothing is
// starting.
func (e *cliEnv) waitReady(timeout time.Duration) (lifecycle.Status, error) {
	deadline := time.Now().Add(timeout)
	var last lifecycle.Status
	for {
		st, err := lifecycle.Query(e.stateDir, e.client)
		if err != nil {
			return st, err
		}
		last = st
		if st.Running {
			return st, nil
		}
		if time.Now().After(deadline) {
			return last, nil
		}
		time.Sleep(startPollInterval)
	}
}

// dumpLogTail prints the end of the runtime log to stderr — the only
// place a detached child's own words can be found, which is why a failed
// start prints them unprompted instead of telling the user to go looking.
func (e *cliEnv) dumpLogTail(n int) {
	lines, err := lifecycle.TailLines(e.logPath, n)
	if err != nil {
		fmt.Fprintf(e.errOut, "dexel: could not read %s: %v\n", e.logPath, err)
		return
	}
	if len(lines) == 0 {
		fmt.Fprintf(e.errOut, "dexel: %s is empty — the runtime wrote nothing at all\n", e.logPath)
		return
	}
	fmt.Fprintf(e.errOut, "--- last %d line(s) of %s ---\n", len(lines), e.logPath)
	for _, line := range lines {
		fmt.Fprintln(e.errOut, line)
	}
	fmt.Fprintf(e.errOut, "--- end of %s ---\n", e.logPath)
}

// ----------------------------------------------------------------- stop

// cmdStop stops the running runtime, gracefully (PLATFORM_NOTES.md §2).
//
// Not-running is SUCCESS (exit 0): `stop` is the first step of
// `dexel restart`, `dexel update` and `dexel uninstall`, and every one of
// them wants "make sure it is not running", not "fail if it already
// wasn't".
func cmdStop(args []string) int {
	env, code := cliEnvOrReport()
	if env == nil {
		return code
	}
	fs := flag.NewFlagSet("dexel stop", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	return env.stop()
}

func (e *cliEnv) stop() int {
	st, err := lifecycle.Discover(e.stateDir, e.client)
	if err != nil {
		fmt.Fprintf(e.errOut, "dexel: %v\n", err)
		return 1
	}
	if !st.Running {
		if st.Cleaned {
			fmt.Fprintf(e.out, "dexel is not running (removed a stale runtime.json: %s)\n", st.Reason)
		} else {
			fmt.Fprintln(e.out, "dexel is not running")
		}
		return 0
	}

	pid := st.Runtime.Pid

	// Endpoint-primary (MIGRATION_PLAN.md §PR-4: "the CLI calls the
	// endpoints instead of poking signals"; PLATFORM_NOTES.md §2: "the
	// lifecycle endpoint IS the graceful path... fallback is [a
	// signal]"). POST /api/lifecycle/stop drives the exact same
	// persist(); provider.Stop(); httpSrv.Shutdown() sequence a SIGTERM
	// does (app/main.go's shared shutdown() closure) — there is one
	// shutdown path either way; the endpoint is just a different
	// doorbell, and the one that also works on Windows (no SIGTERM
	// there at all — see spawn_windows.go's signalStop).
	//
	// A signal is the fallback for a runtime that cannot or will not
	// answer HTTP: an unreachable/refused connection (a pre-PR-4 binary,
	// a firewalled loopback) falls back immediately; a 202 that the
	// runtime then never acts on (an HTTP server wedged independently of
	// the shutdown path) falls back after stopGrace.
	if reqErr := lifecycle.RequestStop(e.client, st.Runtime); reqErr != nil {
		fmt.Fprintf(e.errOut, "dexel: lifecycle stop endpoint unreachable (%v) — falling back to a signal\n", reqErr)
		return e.stopViaSignal(pid)
	}
	fmt.Fprintf(e.out, "stopping dexel (pid %d) via the lifecycle endpoint...\n", pid)
	if e.waitGone(pid, stopGrace) {
		fmt.Fprintln(e.out, "dexel stopped")
		return e.cleanupAfterStop()
	}
	fmt.Fprintf(e.errOut, "dexel: pid %d did not exit within %s of the lifecycle endpoint accepting the stop request — falling back to a signal\n", pid, stopGrace)
	return e.stopViaSignal(pid)
}

// stopViaSignal is PR-3's original stop mechanism (PLATFORM_NOTES.md §2:
// SIGTERM/TerminateProcess, a stopGrace wait, then escalation to
// SIGKILL). PR-4 makes the lifecycle endpoint the preferred path (see
// stop() above); this is reached only when that endpoint could not be
// reached, or accepted the request but the runtime did not act on it in
// time.
func (e *cliEnv) stopViaSignal(pid int) int {
	p, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintf(e.errOut, "dexel: find process %d: %v\n", pid, err)
		return 1
	}
	if err := signalStop(p); err != nil {
		// The round-trip said it was alive milliseconds ago, so this is
		// either a permission problem or a process that exited in
		// between. Re-checking is cheaper than guessing.
		if !processAlive(pid) {
			fmt.Fprintf(e.out, "dexel (pid %d) had already exited\n", pid)
			return e.cleanupAfterStop()
		}
		fmt.Fprintf(e.errOut, "dexel: signal pid %d: %v\n", pid, err)
		return 1
	}
	fmt.Fprintf(e.out, "stopping dexel (pid %d) via signal...\n", pid)

	if e.waitGone(pid, stopGrace) {
		fmt.Fprintln(e.out, "dexel stopped")
		return e.cleanupAfterStop()
	}

	// Loudly, per PLATFORM_NOTES.md §2. The 30s autosave bounds what a
	// hard kill can cost — the same bound ADR 0015 already accepted — but
	// it is still progress the user may notice missing, so it is never
	// done quietly.
	fmt.Fprintf(e.errOut, "dexel: pid %d did not exit within %s — escalating to a HARD KILL.\n", pid, stopGrace)
	fmt.Fprintf(e.errOut, "dexel: up to the last 30 seconds of progress since the previous autosave may be lost.\n")
	if err := signalKill(p); err != nil {
		fmt.Fprintf(e.errOut, "dexel: kill pid %d: %v\n", pid, err)
		return 1
	}
	if !e.waitGone(pid, killGrace) {
		fmt.Fprintf(e.errOut, "dexel: pid %d is STILL alive after a hard kill — something is very wrong; investigate by hand\n", pid)
		return 1
	}
	fmt.Fprintln(e.out, "dexel killed")
	return e.cleanupAfterStop()
}

// waitGone waits for pid to disappear. This is the one place a pid IS the
// authority, and legitimately so: we signalled THIS pid a moment ago and
// are watching it for a few bounded seconds, so recycling is not a
// realistic concern — whereas "is a dexel running" over unbounded time is
// exactly the question ARCHITECTURE.md Decision 6 forbids answering from a
// pid. The caller re-checks by round-trip afterwards anyway, because the
// runtime removes its own runtime.json on the way out.
func (e *cliEnv) waitGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !processAlive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(startPollInterval)
	}
}

// cleanupAfterStop removes a runtime.json the exiting runtime did not get
// to remove itself. A clean shutdown removes its own file (see
// runServe), so this is normally a no-op; it matters after a hard kill,
// and after a runtime that died of something worse.
func (e *cliEnv) cleanupAfterStop() int {
	if err := lifecycle.RemoveRuntime(e.stateDir); err != nil {
		fmt.Fprintf(e.errOut, "dexel: %v\n", err)
		return 1
	}
	return 0
}

// -------------------------------------------------------------- restart

// cmdRestart is stop, wait for exit, start (ARCHITECTURE.md Decision 3).
// It reuses the two verbs rather than reimplementing either, so the
// grace/escalation policy and the readiness policy exist once.
func cmdRestart(args []string) int {
	env, code := cliEnvOrReport()
	if env == nil {
		return code
	}
	if rc := env.stop(); rc != 0 {
		return rc
	}
	return env.start(args)
}

// --------------------------------------------------------------- status

// statusJSON is `dexel status --json`'s exact output shape. Running is
// the field that carries the answer, which is why the command exits 0
// either way (see cmdStatus).
type statusJSON struct {
	Running   bool   `json:"running"`
	Pid       int    `json:"pid,omitempty"`
	Port      int    `json:"port,omitempty"`
	URL       string `json:"url,omitempty"`
	Version   string `json:"version,omitempty"`
	Commit    string `json:"commit,omitempty"`
	StartedAt string `json:"startedAt,omitempty"`
	Uptime    int64  `json:"uptimeSeconds,omitempty"`
	// Paused (PR-5) is the live pause state, present only when it is
	// TRUE — matching the omitempty style of every other running-only
	// field here, and meaning a consumer reads "absent or false" as "not
	// paused" exactly as it already reads an absent pid as "no pid".
	// ARCHITECTURE.md Decision 16 requires this to be visible from a
	// terminal at all: "a paused-and-forgotten dexel must be obvious,
	// never mute".
	Paused   bool   `json:"paused,omitempty"`
	StateDir string `json:"stateDir"`
	LogPath  string `json:"logPath"`
	Cleaned  bool   `json:"cleanedStaleRuntimeFile,omitempty"`
	Reason   string `json:"reason,omitempty"`
	// Prefs (SET-1, docs/ui-spec.md §11.5) is how a user preference
	// reaches the DESKTOP SHELL. The shell (desktop/src-tauri/src/lib.rs)
	// already runs `status --json` to find the runtime's URL — at launch
	// and again on every window focus — so the preferences it has to act
	// on ride that call rather than needing an IPC channel into a page
	// served from the runtime's own origin (which the shell does not
	// have; see that file's "Why focus, and not a timer or a WS hook").
	//
	// NOT omitempty, and present whether or not a runtime is running: a
	// preference is CONFIG (ADR 0014), it lives in config.json, and it is
	// just as true with nothing running as with a live daemon. A consumer
	// that reads `prefs` therefore never has to branch on `running` to
	// find out what the user asked for.
	Prefs prefsJSON `json:"prefs"`
}

// prefsJSON is `status --json`'s `prefs` block — SET-1's user preferences
// (docs/ui-spec.md §11.5), read STRAIGHT OUT OF config.json rather than
// out of the live runtime.
//
// Reading the file is the right call here, not a shortcut around the
// live-probe path `paused` uses, and the two fields differ in a way that
// decides it: `paused` is a fact about a PROCESS (is it sampling right
// now?), which only that process can answer, so it must come from
// GET /api/lifecycle/status. A preference is a fact about the USER's
// CONFIGURATION, whose authority is config.json itself — the file every
// SET_PREF writes through to immediately (app/main.go's persistConfig),
// and the file ADR 0014 deliberately leaves hand-editable. So reading it
// here is both fresher (a hand edit is seen at once) and honest with no
// runtime at all, and it keeps a user preference out of the token-gated
// lifecycle control plane, which is about controlling the daemon.
type prefsJSON struct {
	AlwaysOnTop  bool `json:"alwaysOnTop"`
	ShowAwayTime bool `json:"showAwayTime"`
	SoundEnabled bool `json:"soundEnabled"`
}

// readPrefs loads the `prefs` block from config.json under stateDir.
//
// It cannot fail into an error: store.LoadConfig already degrades a
// missing or malformed config.json to defaults (both preferences false)
// rather than erroring, which is exactly the answer `status` should give
// — "nothing configured" is a real, correct answer, and a status command
// that refused to print because a hand-edited config file had a stray
// comma would be useless at the one moment it is needed. A genuine read
// failure (a permission problem) is logged nowhere and degrades the same
// way for the same reason: the rest of the status answer is still true.
func readPrefs(stateDir string) prefsJSON {
	cfg, _ := store.LoadConfig(filepath.Join(stateDir, configFileName))
	// SoundEnabledOrDefault, not the raw field: `dexel status` must report
	// what the running game would actually do, and for a config.json that
	// predates SOUND-1 that is the default (on), not the nil pointer.
	return prefsJSON{
		AlwaysOnTop:  cfg.AlwaysOnTop,
		ShowAwayTime: cfg.ShowAwayTime,
		SoundEnabled: cfg.SoundEnabledOrDefault(),
	}
}

// configFileName is config.json's basename, pinned by store.ConfigPath()
// (and asserted equal to it by TestReadPrefsReadsTheSameFileStoreWrites).
// Joined onto cliEnv.stateDir rather than calling store.ConfigPath()
// directly so `status` reads the state dir it already resolved once,
// never a second independent resolution that could drift from it — the
// same rule lifecycleStatusHandler follows for statePath/logPath.
const configFileName = "config.json"

// cmdStatus answers "is a dexel running here" by round-trip
// (ARCHITECTURE.md Decision 6), and cleans up a runtime.json it cannot
// confirm.
//
// It exits 0 whether or not a runtime is running. `status` succeeded at
// answering the question; the ANSWER is the `running` field (--json) or
// the first line of output (text). This is what PR-9's
// `resolve_runtime()` reads to decide whether to call `dexel start`, and
// making "not running" an error exit would force every such caller to
// treat a perfectly normal answer as a failure.
func cmdStatus(args []string) int {
	env, code := cliEnvOrReport()
	if env == nil {
		return code
	}
	fs := flag.NewFlagSet("dexel status", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print machine-readable JSON instead of text")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	st, err := lifecycle.Discover(env.stateDir, env.client)
	if err != nil {
		fmt.Fprintf(env.errOut, "dexel: %v\n", err)
		return 1
	}

	out := statusJSON{
		Running:  st.Running,
		StateDir: env.stateDir,
		LogPath:  env.logPath,
		Cleaned:  st.Cleaned,
		// SET-1: read from config.json, so it is answered identically
		// whether or not a runtime is running (see prefsJSON).
		Prefs: readPrefs(env.stateDir),
	}
	if st.Running {
		out.Pid = st.Runtime.Pid
		out.Port = st.Runtime.Port
		out.URL = st.Runtime.URL
		// Prefer the LIVE answer over the file's copy for version and
		// commit: the file was written by whatever binary started the
		// runtime, and after a `dexel update` the two can legitimately
		// differ until the next restart. What is running is the truth.
		out.Version = firstNonEmpty(st.Live.Version, st.Runtime.Version)
		out.Commit = firstNonEmpty(st.Live.Commit, st.Runtime.Commit)
		out.StartedAt = st.Runtime.StartedAt
		out.Uptime = uptimeSeconds(st.Runtime.StartedAt, time.Now())
		// PR-5: only the LIVE answer can know this — runtime.json is
		// written once at startup and never rewritten, so it has no
		// opinion about a pause that happened later.
		out.Paused = st.Live.Paused
	} else {
		out.Reason = st.Reason
	}

	if *asJSON {
		enc := json.NewEncoder(env.out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(env.errOut, "dexel: encode status: %v\n", err)
			return 1
		}
		return 0
	}

	if st.Running {
		fmt.Fprintln(env.out, bold(env.out, "dexel is running"))
		if out.Paused {
			// Said in the same breath as "running", because the two
			// together are the whole answer: the process is alive AND it
			// is not watching anything. Printing only "running" for a
			// paused dexel would be the mute failure Decision 16 names.
			fmt.Fprintln(env.out, "  tracking  PAUSED — nothing is being observed or counted (`dexel resume` to resume)")
		} else {
			fmt.Fprintln(env.out, "  tracking  on")
		}
		fmt.Fprintf(env.out, "  pid       %d\n", out.Pid)
		fmt.Fprintf(env.out, "  url       %s\n", out.URL)
		fmt.Fprintf(env.out, "  version   %s (commit %s)\n", out.Version, out.Commit)
		if out.Uptime >= 0 && out.StartedAt != "" {
			fmt.Fprintf(env.out, "  uptime    %s (since %s)\n", time.Duration(out.Uptime)*time.Second, out.StartedAt)
		}
	} else {
		fmt.Fprintln(env.out, bold(env.out, "dexel is not running"))
		if st.Cleaned {
			fmt.Fprintf(env.out, "  cleaned   a stale runtime.json (%s)\n", st.Reason)
		}
	}
	fmt.Fprintf(env.out, "  state     %s\n", out.StateDir)
	fmt.Fprintf(env.out, "  log       %s\n", out.LogPath)
	// SET-1: printed for the same reason `paused` is (Decision 16's "a
	// paused-and-forgotten dexel must be obvious, never mute") — a window
	// that will not come to the front, or away rows that are not on
	// screen, should be explicable from a terminal instead of looking like
	// a bug. Printed unconditionally, below the two paths, because these
	// are config facts and do not depend on anything running.
	fmt.Fprintf(env.out, "  on top    %s\n", yesNo(out.Prefs.AlwaysOnTop))
	fmt.Fprintf(env.out, "  away time %s\n", shownHidden(out.Prefs.ShowAwayTime))
	fmt.Fprintf(env.out, "  sound     %s\n", onOff(out.Prefs.SoundEnabled))
	return 0
}

// uptimeSeconds derives uptime from runtime.json's startedAt. Returns -1
// for an unparseable or absent timestamp rather than a plausible-looking
// zero — ADR 0010's rule is that dexel never claims something it cannot
// know, and "started 0 seconds ago" would be exactly such a claim.
func uptimeSeconds(startedAt string, now time.Time) int64 {
	if startedAt == "" {
		return -1
	}
	t, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return -1
	}
	d := now.Sub(t)
	if d < 0 {
		return 0
	}
	return int64(d / time.Second)
}

// yesNo/shownHidden/onOff render the user preferences for the
// human-readable `dexel status` output. Separate wordings on purpose:
// "on top: yes/no" is a property of the window, while away time is either
// "shown" or "hidden" — and calling the hidden case "no" would read as
// "not recorded", which is precisely the thing that is NOT true
// (docs/ui-spec.md §11.3: away time is always recorded, only its display
// is a choice). Sound is the plain case: it is either on or off, and off
// means nothing is recorded, computed or withheld — there is simply no
// sound (SOUND-1, §13).
func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func shownHidden(v bool) string {
	if v {
		return "shown in the activity log"
	}
	return "hidden (still recorded)"
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// ----------------------------------------------------------------- open

// cmdOpen ensures a runtime exists and then shows the UI
// (ARCHITECTURE.md Decision 3 and Decision 17). It is also what a bare
// `dexel` runs (FORK A).
//
// Lookup order for the window, per Decision 17: `dexel-desktop` on PATH,
// then in BinDir, then the platform app location, then the default
// browser. The browser fallback is not a consolation prize — it is the
// path that actually ships today, because `dexel-desktop` is blocked on a
// build host that has never existed (MIGRATION_PLAN.md §PR-9).
func cmdOpen(args []string) int {
	env, code := cliEnvOrReport()
	if env == nil {
		return code
	}
	// start-if-needed, per FORK A's dispatch rule. `start` already
	// reports and succeeds when a runtime is present, so this is one call
	// for both cases.
	if rc := env.start(args); rc != 0 {
		return rc
	}
	st, err := lifecycle.Discover(env.stateDir, env.client)
	if err != nil {
		fmt.Fprintf(env.errOut, "dexel: %v\n", err)
		return 1
	}
	if !st.Running {
		fmt.Fprintln(env.errOut, "dexel: no runtime to open (it stopped between starting and opening)")
		return 1
	}
	return env.openURL(st.Runtime.URL)
}

// openURL launches the desktop app if one is installed, else the default
// browser, on the runtime's own loopback URL.
//
// A desktop app that fails to launch FALLS THROUGH to the browser instead
// of failing the command. `dexel open`'s job is to show the user their
// game; a broken or half-installed window is a reason to use the other
// front door, not a reason to show nothing (RUN-MODES.md: "the browser
// fallback is not a consolation prize").
func (e *cliEnv) openURL(url string) int {
	if app := e.findDesktopApp(); app != "" {
		name, argv := desktopAppLaunchCommand(e.goos, app, url)
		fmt.Fprintf(e.out, "opening %s in %s\n", url, app)
		if err := launchDetached(name, argv); err != nil {
			fmt.Fprintf(e.errOut, "dexel: launch %s: %v\n", app, err)
			fmt.Fprintln(e.errOut, "dexel: falling back to the browser")
		} else {
			return 0
		}
	}
	name, argv := browserOpenCommand(e.goos, url)
	fmt.Fprintf(e.out, "opening %s\n", url)
	if err := launchDetached(name, argv); err != nil {
		fmt.Fprintf(e.errOut, "dexel: %s: %v\n", name, err)
		fmt.Fprintf(e.errOut, "dexel: open this URL yourself: %s\n", url)
		return 1
	}
	return 0
}

// desktopAppLaunchCommand is how to start the desktop app that
// findDesktopApp resolved (ARCHITECTURE.md Decision 17). Pure, for the
// same reason browserOpenCommand below is.
//
// The macOS branch is not a stylistic choice. A `.app` is a DIRECTORY, not
// an executable, so handing its path to exec.Command fails with
// "permission denied" (EACCES) — which is exactly what `dexel open` did
// the moment a dexel.app existed in /Applications, the one path
// desktopAppCandidates points at on darwin. `open -a <bundle>` is how a
// bundle is launched, and it RAISES an already-running instance instead of
// starting a second copy, which is the behaviour `dexel open` wants for a
// window that is meant to be reopened over and over.
//
// No URL is passed to the bundle: the desktop shell resolves the runtime
// itself via `dexel-server status --json` (desktop/src-tauri/src/lib.rs),
// and a lock-holding runtime is unique, so there is nothing to
// disambiguate. A bare executable named dexel-desktop still gets the URL
// as its single argv element, the way it always did.
func desktopAppLaunchCommand(goos, app, url string) (string, []string) {
	if goos == "darwin" && strings.HasSuffix(app, ".app") {
		return "open", []string{"-a", app}
	}
	return app, []string{url}
}

// browserOpenCommand is the per-OS "open this URL in whatever the user
// uses" incantation (ARCHITECTURE.md Decision 17). Pure — goos and url
// in, command and argv out — so every platform's branch is unit-testable
// from one host, the same convention app/internal/paths' stateDirFor
// follows.
//
//	darwin   open <url>
//	windows  rundll32 url.dll,FileProtocolHandler <url>
//	other    xdg-open <url>
//
// `rundll32 url.dll,FileProtocolHandler` rather than `cmd /c start`: the
// latter treats `&` in a URL as a command separator and needs quoting
// rules that differ between cmd and PowerShell, which is a shell-injection
// shape we simply decline to have.
func browserOpenCommand(goos, url string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		return "xdg-open", []string{url}
	}
}

// desktopAppCandidates is the ordered list of FILESYSTEM locations to try
// for the optional desktop app, after a PATH lookup has already been
// tried (ARCHITECTURE.md Decision 17: "`dexel-desktop` → BinDir →
// platform app dir → default browser"). Pure, for the same reason
// browserOpenCommand is.
//
// binDir may be "" when it could not be resolved; that candidate is then
// simply absent rather than becoming a relative path.
func desktopAppCandidates(goos, binDir, localAppData, homeDir string) []string {
	exe := desktopAppName
	if goos == "windows" {
		exe += ".exe"
	}
	var out []string
	if binDir != "" {
		out = append(out, filepath.Join(binDir, exe))
	}
	switch goos {
	case "darwin":
		// The .app bundle itself, opened via `open` — which is what a
		// macOS app is, rather than an executable on a path. Capital D:
		// the bundle is named after tauri.conf.json's productName
		// ("Dexel"), not after the lowercase binary inside it. macOS
		// paths are case-insensitive by default so the old
		// "dexel.app" spelling usually still resolved — this is a
		// correctness statement, not a bug fix.
		out = append(out, "/Applications/Dexel.app")
		// ~/Applications is the no-admin fallback, and it is SECOND for
		// the same reason BinDir is first: /Applications is where a
		// machine-wide install lands, and a user who has both should get
		// the shared one rather than a personal copy that a second
		// account cannot see. install.sh writes here only when
		// /Applications is not writable without sudo — this installer
		// does not sudo — so on most machines this candidate simply does
		// not exist.
		//
		// The reversal already knows about it: cmd_uninstall.go's
		// macAppBundleCandidates has listed both roots since it was
		// written, so an app installed here is removed by
		// `dexel uninstall` with no further change.
		if homeDir != "" {
			out = append(out, filepath.Join(homeDir, "Applications", "Dexel.app"))
		}
	case "windows":
		if localAppData != "" {
			out = append(out, filepath.Join(localAppData, "Programs", "dexel", exe))
		}
	}
	return out
}

// findDesktopApp resolves the desktop app to an actual runnable path, or
// "" when none is installed (the shippable browser-fallback case).
func (e *cliEnv) findDesktopApp() string {
	if p, err := exec.LookPath(desktopAppName); err == nil {
		return p
	}
	// os.UserHomeDir rather than os.Getenv("HOME"): it is the same answer
	// on darwin, and the honest one on the platforms where it is not.
	// An error here is not fatal — it drops one candidate, exactly the
	// way an unresolved BinDir does.
	homeDir, _ := os.UserHomeDir()
	for _, cand := range desktopAppCandidates(e.goos, e.binDir, os.Getenv("LOCALAPPDATA"), homeDir) {
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return ""
}

// launchDetached starts a UI process and immediately lets go of it. It
// uses the SAME detachAttr as the runtime spawn: a browser or window that
// died when the terminal running `dexel open` closed would defeat the
// entire point of a runtime that outlives its window.
func launchDetached(name string, args []string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = detachAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// ----------------------------------------------------------------- logs

// cmdLogs is the interface to the runtime log (PLATFORM_NOTES.md §4:
// "`dexel logs` is the interface; the path is printed by `dexel status`").
func cmdLogs(args []string) int {
	env, code := cliEnvOrReport()
	if env == nil {
		return code
	}
	fs := flag.NewFlagSet("dexel logs", flag.ExitOnError)
	n := fs.Int("n", 40, "print the last N lines")
	follow := fs.Bool("f", false, "follow the log as it grows (Ctrl-C to stop)")
	pathOnly := fs.Bool("path", false, "print the log file's path and exit")
	truncate := fs.Bool("truncate", false, "empty the log file")
	// PLATFORM_NOTES.md §4: "On Linux-with-systemd, also mention
	// `journalctl --user -u dexel` in the command's help, since both
	// exist." Installed BEFORE Parse, or flag would never call it.
	fs.SetOutput(env.errOut)
	fs.Usage = func() {
		fmt.Fprintln(env.errOut, "Usage: dexel logs [-n N] [-f] [--path] [--truncate]")
		fs.PrintDefaults()
		if env.goos == "linux" {
			fmt.Fprintln(env.errOut, "\nIf dexel was started by systemd --user (PR-6's autostart), `journalctl --user -u dexel`\nshows the same output — the unit logs to the journal as well as to this file.")
		}
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *pathOnly {
		fmt.Fprintln(env.out, env.logPath)
		return 0
	}
	if *truncate {
		if err := lifecycle.TruncateLog(env.logPath); err != nil {
			fmt.Fprintf(env.errOut, "dexel: %v\n", err)
			return 1
		}
		fmt.Fprintf(env.out, "emptied %s\n", env.logPath)
		return 0
	}

	lines, err := lifecycle.TailLines(env.logPath, *n)
	if err != nil {
		fmt.Fprintf(env.errOut, "dexel: %v\n", err)
		return 1
	}
	if len(lines) == 0 && !*follow {
		fmt.Fprintf(env.errOut, "dexel: %s is empty or does not exist yet\n", env.logPath)
		return 0
	}
	for _, line := range lines {
		fmt.Fprintln(env.out, line)
	}
	if !*follow {
		return 0
	}
	return env.followLog()
}

// followLog is `dexel logs -f`: poll the file for growth and print what
// appears. Polling rather than inotify/FSEvents/ReadDirectoryChangesW,
// deliberately — three platform implementations and a dependency, for a
// convenience on a log that is nearly silent at steady state. It runs
// until the user interrupts it, which is what `-f` means.
func (e *cliEnv) followLog() int {
	f, err := os.Open(e.logPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Nothing to follow yet is not an error: the runtime may be
			// about to be started in another terminal.
			fmt.Fprintf(e.errOut, "dexel: %s does not exist yet — waiting\n", e.logPath)
		} else {
			fmt.Fprintf(e.errOut, "dexel: %v\n", err)
			return 1
		}
	}
	if f != nil {
		defer func() { _ = f.Close() }()
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			fmt.Fprintf(e.errOut, "dexel: seek %s: %v\n", e.logPath, err)
			return 1
		}
	}
	buf := make([]byte, 32<<10)
	for {
		if f == nil {
			var openErr error
			f, openErr = os.Open(e.logPath)
			if openErr != nil {
				time.Sleep(250 * time.Millisecond)
				continue
			}
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			_, _ = e.out.Write(buf[:n])
			continue
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			fmt.Fprintf(e.errOut, "dexel: read %s: %v\n", e.logPath, readErr)
			return 1
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// --------------------------------------------------------- pause/resume

// cmdPause / cmdResume are ARCHITECTURE.md Decision 3's last two lifecycle
// verbs and PR-5's CLI surface (MIGRATION_PLAN.md §PR-5).
//
// Not-running is an ERROR here, unlike `stop` (where "make sure it is not
// running" is already satisfied). Pause is a state a RUNNING runtime holds
// and persists; there is nothing to pause when nothing is running, and
// silently exiting 0 would let a user believe tracking had been turned off
// when the next `dexel start` would happily start observing. Saying so and
// exiting non-zero is the honest answer.
//
// Both verbs are idempotent server-side (game.Game.SetPaused reports no
// change, so no second provider stop and no second save happen), so a
// repeated `dexel pause` is harmless and still prints the truth.
func cmdPause(args []string) int  { return cmdSetPaused("pause", true, args) }
func cmdResume(args []string) int { return cmdSetPaused("resume", false, args) }

func cmdSetPaused(verb string, pause bool, args []string) int {
	env, code := cliEnvOrReport()
	if env == nil {
		return code
	}
	fs := flag.NewFlagSet("dexel "+verb, flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	return env.setPaused(verb, pause)
}

func (e *cliEnv) setPaused(verb string, pause bool) int {
	st, err := lifecycle.Discover(e.stateDir, e.client)
	if err != nil {
		fmt.Fprintf(e.errOut, "dexel: %v\n", err)
		return 1
	}
	if !st.Running {
		fmt.Fprintf(e.errOut, "dexel: not running — there is nothing to %s (start it with `dexel start`)\n", verb)
		return 1
	}

	paused, err := lifecycle.RequestPause(e.client, st.Runtime, pause)
	if err != nil {
		// No signal fallback exists, by design (lifecycle.RequestPause) —
		// so this is a plain failure, reported as one, never a silent
		// half-success.
		fmt.Fprintf(e.errOut, "dexel: %v\n", err)
		return 1
	}
	// Report what the runtime SAID is in effect, not what was asked for.
	// If the two disagree (they never should) the user learns it here
	// rather than trusting an echo.
	if paused != pause {
		fmt.Fprintf(e.errOut, "dexel: asked to %s, but the runtime reports paused=%v — investigate\n", verb, paused)
		return 1
	}
	if paused {
		fmt.Fprintf(e.out, "dexel PAUSED (pid %d) — tracking is off; nothing is observed or counted while paused. `dexel resume` resumes.\n", st.Runtime.Pid)
	} else {
		fmt.Fprintf(e.out, "dexel RESUMED (pid %d) — tracking is on again, from a clean slate (nothing from before the pause counts).\n", st.Runtime.Pid)
	}
	return 0
}
