// Command app is the dexel server (ADR 0011): a Go backend that
// samples real activity, runs the ADR 0005/0010 economy+honesty engine,
// and serves the live game state over HTTP/WebSocket to the NES.css
// frontend, per docs/ui-spec.md's wire contract.
//
// Since EMBED-1 (docs/plan/ROADMAP.md) both static trees it serves — the
// frontend (app/public) and the sprites (app/assets) — are compiled into
// this binary by embed.go, so the shipped product is ONE file with nothing
// to locate at runtime. -public and $DEXEL_ASSETS_DIR remain as explicit
// DEV overrides that serve those trees off disk instead.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/engine"
	"github.com/jawwadzafar/dexel/app/internal/game"
	"github.com/jawwadzafar/dexel/app/internal/lifecycle"
	"github.com/jawwadzafar/dexel/app/internal/paths"
	"github.com/jawwadzafar/dexel/app/internal/store"
)

// Cadences from docs/ui-spec.md: the 1Hz state tick/autosave gate is the
// original brief's; the ticker/terminal cadences are §3.1/§3.2's.
const (
	tickInterval     = 1 * time.Second
	autosaveInterval = 30 * time.Second
	terminalInterval = 350 * time.Millisecond
	tickerInterval   = 2500 * time.Millisecond
)

// suspendGapThreshold is how much wall-clock time a single 1 Hz tick may
// cover that the MONOTONIC clock did not, before the runtime treats the
// tick as landing on the far side of a machine suspend (or a large clock
// step) and resets the engine's cross-tick memory
// (docs/plan/BUGS-RESILIENCE.md R3/R4).
//
// Why the two clocks disagree at all: one time.Now() carries both a wall
// reading and a monotonic reading, and on Linux CLOCK_MONOTONIC does not
// advance while the machine is suspended (that is CLOCK_BOOTTIME). Go's
// tickers are armed on the monotonic clock, so they do not fire during a
// suspend either — the loop simply resumes. Nothing accrues for the gap,
// which is correct; what is NOT correct is that the engine's recency
// state is then compared against a monotonic clock that skipped the hole:
// a sustained-typing focus run survives an arbitrarily long sleep and
// pays FocusSessionBonusWork for it (R3), and mood() keeps claiming
// MoodCoding for up to CodingRecencyWindow of awake time on the strength
// of a keystroke that is hours old in wall time (R4) — verbatim the two
// failures Engine.Reset's own doc comment describes for the pause seam,
// with nobody calling Reset on resume-from-suspend.
//
// 60s is chosen to be unreachable in normal operation while still tiny
// against any real sleep: a tick covers 1s of wall time, and even a
// runtime descheduled by heavy swap or a stop-the-world pause would have
// to lose a full minute of wall clock WITHOUT the monotonic clock
// advancing to reach it — and if it somehow did, clearing stale recency
// state is the honest response anyway. It also fires on a large NTP/RTC
// step, which is the correct response there too.
const suspendGapThreshold = 60 * time.Second

// suspendGapExceeded is R3/R4's detector, extracted as a pure function so
// it can be tested without a real suspend (which no test can stage —
// there is no way to fake a monotonic reading in Go).
//
// wallElapsed is the wall-clock time between two consecutive ticks
// (measured on time.Now() values with their monotonic readings STRIPPED,
// so Sub cannot silently use the monotonic clock); monoElapsed is the
// same interval measured on the monotonic clock (two unstripped
// time.Now() values). In normal operation both are ~tickInterval and the
// gap is ~0. Across a suspend, wallElapsed is the whole sleep while
// monoElapsed is a fraction of a second, and the difference IS the sleep.
//
// Returns the gap and whether it exceeds threshold. A negative gap
// (wall stepped backwards) never trips the detector: the engine's own
// state is entirely monotonic, so a backward wall step cannot make it
// stale — the day-bucket layer is where a backward step matters (R7).
func suspendGapExceeded(wallElapsed, monoElapsed, threshold time.Duration) (time.Duration, bool) {
	gap := wallElapsed - monoElapsed
	return gap, gap > threshold
}

// serveMode is WHICH of the three entry points is running today's server
// body (docs/production-runtime/ARCHITECTURE.md Decision 3 and FORK
// A). `serve` and `runtime` "are the same code path; the two names exist
// so a log line, a launchd plist, and a human's terminal each say the
// thing they mean" — and modeLegacy is the third name for that same path:
// the pre-PR-3 `dexel -addr ...` shape, which must keep behaving
// EXACTLY as it did before a dispatcher existed.
//
// The mode changes exactly three things and nothing else:
//
//	              default -addr      runtime.lock + runtime.json   extra log line
//	modeLegacy    127.0.0.1:8080     no                            no
//	modeServe     127.0.0.1:8080     no                            yes ("no lock")
//	modeRuntime   127.0.0.1:0        YES                           yes
//
// modeLegacy differs from modeServe ONLY in that it prints nothing extra.
// That is deliberate and it is the compatibility rule of this whole PR:
// `.github/workflows/desktop.yml`'s sidecar assertion, the Tauri sidecar,
// and every README invocation all take the legacy shape, so the legacy
// shape emits byte-for-byte what it emitted before — not one added line,
// on stdout or stderr. ARCHITECTURE.md Decision 7's "it logs one line
// saying so" is attached to `dexel serve`, the explicitly-named developer
// verb, which is where it can do no harm.
type serveMode int

const (
	modeLegacy serveMode = iota
	modeServe
	modeRuntime
)

// defaultAddr: `runtime` defaults to an OS-assigned ephemeral port
// because a background runtime nobody typed a port for should never fight
// another process for 8080; `serve` (and legacy) keep today's
// 127.0.0.1:8080 so `go run . serve` behaves exactly as `go run .` did
// before (ARCHITECTURE.md Decision 3).
func (m serveMode) defaultAddr() string {
	if m == modeRuntime {
		return "127.0.0.1:0"
	}
	return "127.0.0.1:8080"
}

// flagSetName is what a flag error message calls this invocation. Legacy
// keeps os.Args[0], which is what the pre-PR-3 global flag.CommandLine
// used, so a bad flag in the legacy shape reads as it always did.
func (m serveMode) flagSetName() string {
	switch m {
	case modeServe:
		return "dexel serve"
	case modeRuntime:
		return "dexel runtime"
	default:
		return os.Args[0]
	}
}

// serveFlags is the server's flag set, unchanged in every name, type,
// default and help string from the pre-PR-3 global flag.CommandLine — the
// ONE exception being -addr's default, which now comes from
// serveMode.defaultAddr() (ARCHITECTURE.md Decision 3: `runtime` defaults
// to an ephemeral port, `serve` and the legacy shape keep 127.0.0.1:8080).
//
// Split out of runServe purely so that default IS testable
// (TestServeFlagSetDefaultsPerMode). It was not, in the first cut of this
// PR, and the mode-dependent default was silently left as a hardcoded
// 127.0.0.1:8080 — a bug that made every detached runtime fight for port
// 8080 and that no unit test could see, because the only way to observe
// it was to start a real server.
type serveFlags struct {
	addr           *string
	publicDir      *string
	providerKind   *string
	fakeScript     *string
	insecureOrigin *bool
	allowOrigins   originList
}

// newServeFlagSet builds the flag set for one runServe invocation.
func newServeFlagSet(mode serveMode) (*flag.FlagSet, *serveFlags) {
	fs := flag.NewFlagSet(mode.flagSetName(), flag.ExitOnError)
	f := &serveFlags{}
	f.addr = fs.String("addr", mode.defaultAddr(), "listen address (loopback by default — binding beyond 127.0.0.1/localhost exposes the activity monitor and save to your LAN/tailnet); a port of 0 (e.g. 127.0.0.1:0) binds an OS-assigned free port, reported via the DEXEL_LISTENING stdout handshake — see ADR 0015")
	f.publicDir = fs.String("public", "", "DEV OVERRIDE: serve the frontend from this directory on disk instead of the copy embedded in this binary (EMBED-1) — point it at app/public to iterate on the frontend without rebuilding Go; empty (the default) always serves the embedded copy")
	f.providerKind = fs.String("provider", "auto", `activity provider: "auto" (native for this OS) or "fake"`)
	f.fakeScript = fs.String("fake-script", "", "explicit fake-provider script (e.g. type:20s,idle:40s,mouse:15s); overrides DEXEL_FAKE_SCRIPT; implies -provider=fake")
	f.insecureOrigin = fs.Bool("insecure-origin", false, "accept WebSocket connections from ANY Origin, skipping same-origin verification entirely — for embedded webviews only (e.g. a file:// or app:// frontend whose Origin header, if any, will never match a loopback host pattern); never enable this if -addr binds beyond 127.0.0.1/localhost")
	fs.Var(&f.allowOrigins, "allow-origin", "extra literal WebSocket Origin(s) to accept, beyond the loopback host(s) derived from -addr's resolved port (repeatable, and/or comma-separated within one occurrence); each value is a bare host[:port] (e.g. \"tauri.localhost\") or a full origin URL from which the host is extracted (e.g. \"tauri://localhost\", \"http://tauri.localhost\") — never a wildcard; insurance for a future embedded webview whose Origin header won't match a loopback pattern (ADR 0015/docs/plan/F3-design.md §2,§8); not needed, and unused in Phase 1, when the webview loads this server's own loopback URL")
	return fs, f
}

// runServe is today's main() body, unchanged in its logic
// (MIGRATION_PLAN.md §PR-3: "today's body becomes runServe(args []string)
// WITH ITS LOGIC UNCHANGED"). What is new is only the frame around it: a
// per-invocation flag.FlagSet instead of the process-global
// flag.CommandLine, and — in modeRuntime only — PLATFORM_NOTES.md §5's
// lock and ARCHITECTURE.md Decision 6's discovery file bracketing the
// existing net.Listen.
//
// It exits the process on failure via log.Fatalf, exactly as the old
// main() did; that is this repo's stated fail-loudly posture (ADR 0010),
// and cli.go's dispatcher treats a plain return as success.
func runServe(mode serveMode, args []string) {
	fs, f := newServeFlagSet(mode)
	// flag.ExitOnError makes Parse's only failure mode a process exit
	// (with flag's own usage output), matching what the global
	// flag.Parse() did before this became a FlagSet.
	_ = fs.Parse(args)
	// Rebound to the exact local names the pre-PR-3 body used, so
	// everything below this line is untouched code rather than a
	// mechanical rename spread over 900 lines.
	addr, publicDir, providerKind, fakeScript, insecureOrigin := f.addr, f.publicDir, f.providerKind, f.fakeScript, f.insecureOrigin
	allowOrigins := f.allowOrigins

	// PLATFORM_NOTES.md §5's single-instance layers, in the order that
	// document pins, because each catches what the previous cannot:
	//   1. the OS lock on <StateDir>/runtime.lock  (here)
	//   2. the explicit net.Listen below, with its existing log.Fatalf
	//   3. runtime.json + a live round-trip, used by the CLI BEFORE it
	//      ever spawns us (app/internal/lifecycle's Discover)
	//
	// Only modeRuntime takes layers 1 and 3. `dexel serve` and the legacy
	// flag shape skip them by design (ARCHITECTURE.md Decision 7: "so a
	// developer can run a scratch instance next to a real one exactly as
	// they can today"), which is also what keeps every existing CI
	// invocation from suddenly contending with a developer's real
	// runtime.
	var stateDir string
	if mode == modeRuntime {
		var err error
		stateDir, err = paths.StateDir()
		if err != nil {
			log.Fatalf("resolve state dir: %v", err)
		}
		lock, err := lifecycle.AcquireLock(lifecycle.LockPath(stateDir))
		if err != nil {
			var busy *lifecycle.ErrLockedError
			if errors.As(err, &busy) {
				// Name the pid, per PLATFORM_NOTES.md §5 layer 1
				// ("Failure message names the pid from runtime.json").
				// The pid is read from the file purely to make the
				// message useful — nothing here TRUSTS it.
				if r, readErr := lifecycle.ReadRuntime(stateDir); readErr == nil {
					log.Fatalf("dexel is already running (pid %d) at %s — refusing to start a second runtime in %s", r.Pid, r.URL, stateDir)
				}
				log.Fatalf("dexel is already running (another process holds %s) — refusing to start a second runtime in %s", busy.Path, stateDir)
			}
			log.Fatalf("acquire runtime lock: %v", err)
		}
		// The OS drops this lock on process death anyway (including
		// SIGKILL); releasing it explicitly on a clean return is
		// tidiness, not the mechanism.
		defer func() { _ = lock.Release() }()

		// R6 (docs/plan/BUGS-RESILIENCE.md): THE RUNTIME ROTATES ITS OWN
		// LOG, because the systemd unit and the launchd agent exec
		// `runtime` and `dexel start`'s one-shot rotation is therefore
		// never on their path at all.
		//
		// Deliberately AFTER the lock, and this is the whole reason the
		// call is inside this block rather than above it: a SECOND
		// `dexel runtime` on this state dir dies at the lock above, and
		// a process that is about to die must not take ownership of the
		// live runtime's log file — it could rotate it out from under
		// the running process, which would then be appending to a
		// renamed inode nobody can see. Nothing is lost by waiting: the
		// lock-failure message still goes wherever THIS mode's stderr
		// points, which on the two paths that own the log file is that
		// very file.
		closeLog := attachRuntimeLog()
		defer closeLog()
	} else if mode == modeServe {
		log.Print("serve: foreground dev mode — not taking runtime.lock and not writing runtime.json, so this instance is invisible to `dexel status`/`stop` and can run alongside a real runtime (ARCHITECTURE.md Decision 7)")
	}

	// Bind explicitly (ADR 0015 / docs/plan/F3-design.md §1,§8 T2) rather
	// than letting http.Server.ListenAndServe do it implicitly, so that
	// (a) -addr's port can be 0 and the OS-assigned real port is knowable
	// before anything else needs it (wsOriginPatterns below, the stdout
	// handshake, the human log line), and (b) a port-already-in-use error
	// fails loudly right here instead of after the rest of startup runs.
	ln, err := listenRuntime(mode, stateDir, *addr)
	if err != nil {
		log.Fatalf("%v", err)
	}
	actualAddr := ln.Addr().String()

	// R9's other half: record the port we actually got, whether it came
	// from the sticky record or from the OS, so the NEXT runtime in this
	// state directory can try to land on it again. Written after the bind
	// succeeded — the record is "the port a runtime here really answered
	// on", never "the port one hoped for".
	if mode == modeRuntime {
		recordStickyPort(stateDir, ln)
	}

	// ARCHITECTURE.md Decision 6: runtime.json is written atomically
	// "immediately after net.Listen succeeds" — i.e. right here, before
	// anything else in startup can fail — because it is the ONLY way the
	// CLI that spawned us (whose stdout we are not, we are a log file)
	// can learn which port we landed on. The token is minted per start
	// and never leaves this file (Decision 8).
	//
	// runtimeInfo is kept around (PR-4, MIGRATION_PLAN.md §PR-4) purely so
	// the lifecycle handlers registered below can be built from the SAME
	// token/pid/port this file was just written with, rather than a
	// second computation of them.
	var runtimeInfo lifecycle.Runtime
	if mode == modeRuntime {
		rt, err := writeRuntimeFile(stateDir, actualAddr)
		if err != nil {
			log.Fatalf("%v", err)
		}
		runtimeInfo = rt
		// Removed on CLEAN exit only (the sigCh/control branch's return
		// runs this defer). A crash or a log.Fatalf leaves the file
		// behind on purpose: staleness is not this process's problem to
		// solve, it is resolved by the next round-trip that fails to
		// reach us (Decision 6), which is the only test that cannot be
		// fooled by a recycled pid.
		defer func() {
			if err := lifecycle.RemoveRuntime(stateDir); err != nil {
				log.Printf("%v", err)
			}
		}()
		log.Printf("discovery: %s (0600)", lifecycle.RuntimePath(stateDir))
	}

	// Stdout handshake (ADR 0015 / docs/plan/F3-design.md §1,§8 T2): print
	// exactly one stable, machine-readable line to STDOUT as soon as the
	// listener is bound, so a parent process (the future Tauri shell, T1)
	// can learn the real port from an ephemeral `-addr 127.0.0.1:0` bind
	// without guessing a port or polling for one. Deliberately independent
	// of the human-readable "dexel listening on..." log line below (which
	// goes through the log package, i.e. stderr by default): the parent
	// parses stdout, a person reads the log.
	fmt.Println(handshakeLine(actualAddr))

	// EMBED-1: decide which copy of each static tree this run serves —
	// the embedded default, or an explicitly requested disk override —
	// before anything is mounted, so the startup log states it once and
	// /api/health can report it for the life of the process.
	publicFS, publicSource, publicOk := resolvePublicSource(*publicDir)
	assetsFS, assetsDir, assetsSource := resolveAssetsSource()

	log.Printf("%s starting", versionLine())

	// PR-5 (MIGRATION_PLAN.md §PR-5, ARCHITECTURE.md Decision 13/16): the
	// provider is SELECTED here but deliberately not STARTED yet. Whether
	// it may observe anything at all depends on SaveData.Paused, which is
	// not known until loadOrImport below has run — and a paused dexel must
	// never observe, not even for the few milliseconds it would take to
	// start a provider and stop it again. See startProvider below.
	provider, providerDesc := selectProvider(*providerKind, *fakeScript)
	log.Printf("activity provider: %s", providerDesc)

	g := game.New()

	savePath, err := store.DefaultPath()
	if err != nil {
		log.Fatalf("resolve save path: %v", err)
	}

	// SEC-1 (docs/plan/SEC-1-design.md §7 GO-2, ADR 0014): config.json is
	// loaded on a path fully independent of state.json/state.db below —
	// it is never blocked by, and never blocks, a tampered or
	// future-schema state load.
	//
	// Phase P1 (docs/plan/PRODUCT-EVOLUTION.md §5) is what finally reads
	// the dexel's own name for something: SEC-1 deliberately shipped it
	// with "no wire change in this stage", and P1 puts it on
	// StateMessage.Config — still the user-authored, unsigned side of ADR
	// 0014's split, still never a SaveData field.
	//
	// This load moves AHEAD of loadOrImport below (it used to run after)
	// because Phase P2 (docs/plan/P2-design.md §8's GO-3 task, extending
	// store.Apply's own doc comment) adds a real ordering requirement on
	// top of that independence: RestoreSessionNames must run before
	// store.Apply's RestoreStats, the same A3 rule RestoreHistory/
	// RestoreStreak already follow — so the config (which carries
	// sessionNames) has to be in hand before loadOrImport ever calls
	// store.Apply.
	cfgPath, cfg := loadOrInitConfig()
	g.RestoreConfigName(cfg.Name)
	g.RestoreSessionNames(store.SessionNamesFromConfig(cfg))
	// SET-1 (docs/ui-spec.md §11): the two user preferences ride the same
	// config.json load as the name, so a restart comes back with the
	// settings the user chose — and, since both default to false, a fresh
	// install comes back with away time private and the window unpinned
	// without any defaulting code here.
	g.RestorePrefs(cfg.AlwaysOnTop, cfg.ShowAwayTime)
	if g.ConfigName() != "" {
		log.Printf("dexel name: %q", g.ConfigName())
	}

	saveExisted := loadOrImport(g, savePath)

	// THE first-launch decision, made exactly once, here, by the server
	// (docs/ui-spec.md §7): show onboarding only when this really is a
	// fresh install — no save of any kind existed when we booted AND
	// config.json carries no name. Either half being false makes this a
	// RETURNING user, who must never see the intro:
	//   - an existing state.db/state.json/legacy save (saveExisted) means
	//     someone has played, even if they never named the dexel;
	//   - a named config.json means someone has already answered the one
	//     question onboarding asks, even on a machine whose save was
	//     wiped.
	// loadOrImport is deliberately conservative about saveExisted (a
	// tampered, future-schema or unreadable save all count as "existed"),
	// because the failure we care about is nagging a returning user, not
	// missing an intro.
	g.SetOnboarding(!saveExisted && g.ConfigName() == "")
	if g.Onboarding() {
		log.Print("first launch: no save and no name — onboarding")
	}

	// startProvider is the ONE place observation begins — at boot (just
	// below) and again on resume (the action loop's RESUME branch) — so
	// "starting the provider" means the same thing, and logs the same
	// way, in both.
	startProvider := func() {
		if err := provider.Start(); err != nil {
			// Not fatal: every provider degrades to a blind, zero-signal
			// state rather than crashing (ADR 0010's honesty rules then
			// simply never claim onBreak from it).
			log.Printf("activity provider start warning: %v", err)
		}
	}

	// PR-5 / FORK D: a dexel that was paused when it last exited comes
	// back PAUSED, and says so — "a paused-and-forgotten dexel must be
	// obvious, never mute" (ARCHITECTURE.md Decision 16). The provider is
	// simply never started, so this process observes nothing at all until
	// somebody resumes it.
	if g.Paused() {
		log.Printf("PAUSED: this dexel was paused when it last exited — the activity provider (%s) has NOT been started and nothing is being observed. Resume with `dexel resume`, or from the UI.", providerDesc)
	} else {
		startProvider()
	}

	eng := engine.New(provider)
	extraOrigins, err := wsExtraOriginPatterns(allowOrigins)
	if err != nil {
		log.Fatalf("%v", err)
	}
	hub := newHub(append(wsOriginPatterns(actualAddr), extraOrigins...), *insecureOrigin)
	// Seed the cache a new connection's initial `state` snapshot reads
	// from — done here, before the HTTP server (and therefore before any
	// per-connection goroutine) starts, so the very first client to
	// connect gets the real loaded/imported state rather than a blank
	// zero value (see Hub.setInitialState's doc comment).
	hub.setInitialState(g.State())
	catalog := game.NewCatalogMessage()
	actions := make(chan actionRequest)
	// controlCh is PR-4's "new control channel" (MIGRATION_PLAN.md §PR-4):
	// POST /api/lifecycle/stop pushes onto it and the single-owner select
	// loop below reads it, running the EXACT SAME shutdown sequence as
	// `case <-sigCh:` (via the shared shutdown closure) — one shutdown
	// path, not two to keep in sync. Buffered by 1 so the handler can
	// signal and return without waiting for the loop to be ready to
	// receive, and so a hypothetical second /stop landing before the
	// first is processed never blocks a handler goroutine.
	controlCh := make(chan struct{}, 1)

	mux := http.NewServeMux()
	// INTERACTION-HARDENING (docs/plan/ROADMAP.md): both static mounts are
	// FILES ONLY — see filesOnlyFS. "/" is the one mount whose own root
	// serves an index.html; every other directory URL under either mount
	// is a 404.
	mux.Handle("/", filesOnlyFS(publicFS, true))
	// "/assets/<file>" is the URL prefix the frontend's assetUrl() builds
	// against (app/frontend/src/assets.ts); StripPrefix turns it back into
	// the bare filename the tree — embedded or on disk — is keyed by.
	mux.Handle("/assets/", http.StripPrefix("/assets/", filesOnlyFS(assetsFS, false)))
	mux.HandleFunc("/api/health", healthHandler(assetsDir, publicOk, version, buildVersion(), publicSource, assetsSource))
	mux.HandleFunc("/ws", hub.handleWS(actions, catalog))

	// PR-4 (MIGRATION_PLAN.md §PR-4, ARCHITECTURE.md §3 Decision 5/8): the
	// lifecycle control plane exists only for a detached runtime that
	// actually wrote a runtime.json and minted a token. `dexel serve` and
	// the legacy foreground shape have neither, so per the plan these
	// routes are simply ABSENT there — a request to them 404s off the
	// bare mux like any other undefined path, rather than existing in
	// some half-authenticated form with nothing to check a token
	// against.
	if mode == modeRuntime {
		lifecycleLogPath, logPathErr := paths.LogFile()
		if logPathErr != nil {
			// Not fatal: the status endpoint's logPath field is a
			// convenience for a human/CLI reading it, not something
			// anything here depends on to function.
			log.Printf("resolve log path for /api/lifecycle/status: %v", logPathErr)
		}
		lifecycleStartedAt, parseErr := time.Parse(time.RFC3339, runtimeInfo.StartedAt)
		if parseErr != nil {
			lifecycleStartedAt = time.Now()
		}
		// PR-5 (MIGRATION_PLAN.md §PR-5): `paused` is a LIVE fact, unlike
		// every other field of this response, so it is read through the
		// hub's cached last-broadcast state rather than from g — which
		// only the select loop below may touch (see Hub.getLastState).
		// The cache is seeded before the HTTP server starts, so it is
		// already correct for a runtime that booted paused.
		livePaused := func() bool { return hub.getLastState().Paused }
		mux.Handle("GET /api/lifecycle/status", requireLifecycleToken(runtimeInfo.Token,
			lifecycleStatusHandler(runtimeInfo, savePath, lifecycleLogPath, lifecycleStartedAt, livePaused)))
		mux.Handle("POST /api/lifecycle/stop", requireLifecycleToken(runtimeInfo.Token,
			lifecycleStopHandler(controlCh)))
		// PR-5's two verbs. They ride the EXISTING actions channel
		// (ARCHITECTURE.md Decision 5: "pause/resume funnel through the
		// existing actions channel... so the single-owner invariant on
		// game.Game is untouched"), which is also why the WebSocket UI
		// gets a pause button for free with no second code path — unlike
		// `stop`, which is deliberately NOT a WS action and reaches the
		// runtime only through controlCh.
		mux.Handle("POST /api/lifecycle/pause", requireLifecycleToken(runtimeInfo.Token,
			lifecyclePauseHandler(actions, livePaused, true)))
		mux.Handle("POST /api/lifecycle/resume", requireLifecycleToken(runtimeInfo.Token,
			lifecyclePauseHandler(actions, livePaused, false)))
	}

	httpSrv := &http.Server{Handler: mux}
	go func() {
		log.Printf("dexel listening on http://%s (frontend: %s, assets: %s)", actualAddr, publicSource, assetsSource)
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	tickTicker := time.NewTicker(tickInterval)
	defer tickTicker.Stop()
	// prevTick/prevTickWall are the same instant read twice: with its
	// monotonic reading (prevTick) and with that reading stripped
	// (prevTickWall). Comparing the two deltas at the next tick is how the
	// loop notices a machine suspend — see suspendGapThreshold (R3/R4).
	prevTick := time.Now()
	prevTickWall := prevTick.Round(0)
	terminalTicker := time.NewTicker(terminalInterval)
	defer terminalTicker.Stop()
	tickerTicker := time.NewTicker(tickerInterval)
	defer tickerTicker.Stop()
	saveTicker := time.NewTicker(autosaveInterval)
	defer saveTicker.Stop()

	persist := func() {
		if err := store.Save(savePath, store.Snapshot(g)); err != nil {
			log.Printf("save failed: %v", err)
		}
	}

	// appendQueue carries finished sessions between the moment the game
	// hands one over and the moment the disk accepts it (B-3). Owned by
	// this loop like every other piece of I/O state.
	appendQueue := &sessionAppendQueue{}

	// shutdown is the ONE graceful-exit sequence (PR-4, MIGRATION_PLAN.md
	// §PR-4: "stop reuses the existing case <-sigCh: body verbatim — one
	// shutdown path"). Both `case <-sigCh:` (a real SIGINT/SIGTERM) and
	// `case <-controlCh:` (POST /api/lifecycle/stop) call this and only
	// this, so there is exactly one place that decides what "shutting
	// down" means.
	shutdown := func() {
		log.Println("shutting down: saving state...")
		persist()
		_ = provider.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = httpSrv.Shutdown(ctx)
		cancel()
	}

	// persistConfig writes EVERY half of config.json this process owns
	// through to disk IMMEDIATELY: the dexel's own name (Phase P1, after
	// SET_NAME), the P2 per-session project-name map
	// (docs/plan/P2-design.md §2.7, after SESSION_START) and SET-1's user
	// preferences (docs/ui-spec.md §11, after SET_PREF)
	// — deliberately not on the 30s autosave timer
	// the protected save uses, and deliberately as ONE write for the one
	// shared file, so writing either half never clobbers the other.
	// Naming something (a dexel, or a session) is a one-shot
	// first-minutes moment; if the process died in the next 30 seconds
	// the user would be asked again (or the name would silently vanish),
	// which is the one thing Phase P1's "returning users never see it"
	// criterion — and P2's "a declared intention that silently fails to
	// survive a crash is worse than no name at all" — both forbid.
	//
	// This is the ONLY place any of them is written, and it writes
	// game.Game.ConfigName()/SessionNames()/AlwaysOnTop()/ShowAwayTime()
	// — the values NormalizeName/NormalizeSessionName already sanitised,
	// and the bools game.Game.SetPref already checked against its
	// key allow-list — never a raw client payload. cfgPath == "" means the home directory could not be
	// resolved at boot (already logged there); the game still runs, the
	// name(s) just cannot outlive the process.
	//
	// READ-MODIFY-WRITE, not construct-and-overwrite. config.json also
	// carries `autostart` — the mechanism `dexel autostart enable` last
	// installed — whose own doc comment in store/config.go says it is
	// "written ONLY by `dexel autostart enable`/`disable` ... nothing else
	// in this codebase may set it". Building a fresh ConfigData here broke
	// exactly that: SaveConfig marshals the whole struct, so the zero-value
	// Autostart was written over the real one on every SET_NAME and every
	// SESSION_START, silently erasing which mechanism was installed. Load
	// first, overwrite only the two halves this function owns, and the
	// promise in the comment two lines above ("writing either half never
	// clobbers the other") becomes true of the third field as well.
	//
	// A load failure is NOT swallowed into a default: continuing with a
	// zero ConfigData would reintroduce the clobber under a different name,
	// which is the trade docs/upgrade-design.md's "log once, start fresh"
	// posture explicitly does not extend to a file we are about to
	// overwrite. (LoadConfig already degrades a hand-edited syntax error to
	// defaults on its own — that path is its call to make, not ours.)
	persistConfig := func() error {
		if cfgPath == "" {
			return errors.New("no config path (home directory unresolved at startup)")
		}
		return writeConfigThrough(cfgPath, g.ConfigName(), store.SessionNamesToConfig(g.SessionNames()), configPrefs{
			AlwaysOnTop:  g.AlwaysOnTop(),
			ShowAwayTime: g.ShowAwayTime(),
		})
	}

	// Single-owner loop: every mutation of g happens on this goroutine
	// only — game.Game does no locking of its own, so this loop IS the
	// lock. Every WS action funnels in over `actions`; the 1s engine tick,
	// the cosmetic ticker/terminal scroll, and autosave all live here too.
	for {
		select {
		case <-tickTicker.C:
			// Suspend/clock-jump detection runs FIRST, before the pause
			// gate and before eng.Tick(), so the tick that lands on the
			// far side of a sleep is already working from cleared engine
			// state (docs/plan/BUGS-RESILIENCE.md R3/R4). The bookkeeping
			// is updated on EVERY tick including paused ones, so a long
			// pause — during which ticks keep firing at 1 Hz — can never
			// look like a gap.
			tickNow := time.Now()
			if gap, jumped := suspendGapExceeded(tickNow.Round(0).Sub(prevTickWall), tickNow.Sub(prevTick), suspendGapThreshold); jumped {
				// One line per event, not per tick: the detector fires on
				// the single tick that crosses the hole.
				log.Printf("resumed after ~%s of suspend or clock jump: resetting engine recency state", gap.Round(time.Second))
				// The same call the RESUME action makes, for the same
				// reason (Engine.Reset's doc comment): a keystroke seen
				// before the hole is not "coding right now", and a focus
				// run with a hole in the middle is not sustained typing.
				// Clearing `initialized` also makes the first tick back
				// contribute exactly zero work.
				eng.Reset()
			}
			prevTick, prevTickWall = tickNow, tickNow.Round(0)

			// PR-5 (ARCHITECTURE.md Decisions 13/14): while paused there
			// is NO sampling. eng.Tick() is not called, so no mood is
			// computed, no work unit is produced, no focus run is
			// tracked and no analytics counter moves — and the provider
			// feeding it was stopped when the pause was applied, so
			// there is nothing to sample even if something asked. The
			// only thing that happens is the paused second being
			// credited to its own bucket (g.TickPaused), and the state
			// still being broadcast: "the UI stays live and shows
			// PAUSED — a frozen window would be indistinguishable from
			// a crash". The 30s autosave, the terminal/ticker scroll and
			// the HTTP server all keep running on their own branches
			// below, untouched, for the same reason.
			if g.Paused() {
				g.TickPaused()
				hub.broadcastState(g.State())
				continue
			}
			prevCash := g.DevCash
			prevSprintName := g.State().Sprint.Name
			r := eng.Tick()
			completed := g.Tick(r)
			hub.broadcastState(g.State())
			if completed {
				reward := g.DevCash - prevCash
				hub.broadcastFlash(flashMessage{
					Type: "flash", Kind: "sprint",
					Text: fmt.Sprintf("%s complete! +%d Dev Cash", prevSprintName, reward),
				})
			}
			// P2 (docs/plan/P2-design.md §2.2's pending-record seam):
			// g.Tick above runs checkSessionAutoEnd at its own top, which
			// may have just queued a completed session (idle or
			// maxDuration). Popped and celebrated HERE, immediately after
			// Tick, exactly as GO-1's session.go doc comment says. No
			// fallback flash on a discard: an auto-ended session is
			// always far longer than SessionMinDurationSeconds in
			// practice (the idle/cap constants are hours; the min is a
			// minute), so this branch is defensive only, not
			// user-facing — nothing here can be a mashed start/stop.
			popEndedSession(g, hub, savePath, appendQueue)

		case <-terminalTicker.C:
			g.AdvanceTerminal()

		case <-tickerTicker.C:
			g.RotateTicker()

		case <-saveTicker.C:
			persist()

		case req := <-actions:
			mutated, flash := applyAction(g, req.msg, req.connID)
			// Phase P1/P2 write-through. Kept HERE rather than inside
			// applyAction for the same reason the protected save is: this
			// loop owns all I/O, applyAction stays a pure state
			// transition over *game.Game (which is what lets
			// store_gate_test.go drive it with no filesystem at all). A
			// failed write is surfaced honestly — the in-memory name(s)
			// stand so the session still works, but the success toast is
			// replaced by an error flash, because a warm "hello"/"session
			// started" for a name that silently will not survive a
			// restart is a lie.
			if mutated && (req.msg.Action == actionSetName || req.msg.Action == actionSessionStart || req.msg.Action == actionSetPref) {
				if err := persistConfig(); err != nil {
					log.Printf("save config failed: %v", err)
					errText := "could not save name"
					switch req.msg.Action {
					case actionSessionStart:
						errText = "could not save session name"
					case actionSetPref:
						// SET-1: a setting that silently will not survive
						// a restart is the same lie a lost name is, so
						// SET_PREF joins the immediate write-through and
						// the same honest failure. The in-memory value
						// still stands, so the session behaves as asked —
						// the toast is what stops us pretending it was
						// saved. (This is also SET_PREF's ONLY flash: a
						// successful one is answered by `state` alone.)
						errText = "could not save settings"
					}
					flash = &flashMessage{Type: "flash", Kind: "error", Text: errText}
				}
			}
			// PR-5's pause/resume side effects (MIGRATION_PLAN.md §PR-5,
			// ARCHITECTURE.md Decisions 13/16). Kept HERE for the same
			// reason the config write-through above is: applyAction stays
			// a pure state transition over *game.Game, and this loop owns
			// every piece of I/O and every non-game resource — the
			// activity provider, the engine, the save file. `mutated`
			// gates them, so a second `dexel pause` while already paused
			// is a genuine no-op rather than a redundant provider
			// stop/save.
			if mutated && (req.msg.Action == actionPause || req.msg.Action == actionResume) {
				if req.msg.Action == actionPause {
					// Decision 13: dexel stops OBSERVING, it does not
					// sample-and-discard. Stopping the provider is the
					// strongest honest reading of "pause tracking", and
					// it is what makes "nothing accrued" structurally
					// true rather than a promise.
					if err := provider.Stop(); err != nil {
						log.Printf("activity provider stop warning: %v", err)
					}
					log.Print("PAUSED: tracking is off — the activity provider is stopped and nothing is being observed or counted (paused time is recorded as its own stat, never as idle)")
				} else {
					// Decision 16: Reset FIRST, then restart the
					// provider. Without the reset, resume would inherit a
					// stale lastKeystrokeAt (and briefly claim `coding`
					// for typing from before the pause) and a stale focus
					// run (and pay a focus bonus for a "sustained" run
					// with the whole pause missing from its middle).
					eng.Reset()
					startProvider()
					log.Print("RESUMED: tracking is on — the activity provider is running again and the engine started from a clean slate (no pre-pause keystroke or focus run carried over)")
				}
				// "On resume the runtime writes an immediate save, so a
				// crash right after resume cannot come back paused"
				// (Decision 16) — and symmetrically for pause, so a
				// crash right after pausing cannot come back tracking.
				persist()
			}
			if mutated {
				hub.broadcastState(g.State())
			}
			if flash != nil {
				hub.broadcastFlash(*flash)
			}
			// P2's pending-record seam, the applyAction half: a
			// SESSION_STOP may have just queued a completed session (or
			// discarded a too-short one — Fork P2-E). Popped and
			// celebrated HERE, immediately after applyAction, exactly as
			// GO-1's session.go doc comment says; every OTHER action is a
			// guaranteed no-op pop (only StartSession/StopSession/
			// checkSessionAutoEnd ever populate the pending slot).
			popped := popEndedSession(g, hub, savePath, appendQueue)
			if mutated && req.msg.Action == actionSessionStop && !popped {
				// Fork P2-E: a session under SessionMinDurationSeconds is
				// discarded outright — no log row, no counter, and
				// "never a scold" (docs/plan/P2-design.md §2.2 step 3).
				hub.broadcastFlash(flashMessage{Type: "flash", Kind: "session", Text: "Session ended — too short to keep."})
			}
			close(req.done)

		case <-sigCh:
			shutdown()
			return

		case <-controlCh:
			// POST /api/lifecycle/stop's trigger (PR-4). Deliberately the
			// SAME shutdown() as a real signal — see its doc comment —
			// so a web page can never reach this by any path other than
			// the token-gated handler that fed controlCh.
			shutdown()
			return
		}
	}
}

// ---------------------------------------------------------------------
// R6 / R9 — what a SUPERVISED runtime has to do for itself
// (docs/plan/BUGS-RESILIENCE.md).
//
// Both fixes exist because the two supervised autostart mechanisms —
// the systemd user unit and the launchd agent — exec `dexel runtime`
// directly, never `dexel start`. Everything the CLI's `start` path does
// on the way through (rotate the log; nothing at all about the port) is
// therefore absent on exactly the paths a real user's dexel runs on, and
// a crash-restart by the supervisor lands in a runtime that has to be
// self-sufficient.
// ---------------------------------------------------------------------

// attachRuntimeLog points the `log` package at a self-rotating writer on
// <StateDir>/logs/runtime.log, and returns the closer for it.
//
// # Why the runtime, and not `dexel start`
//
// lifecycle.RotateLog runs once, in `dexel start`, before the child is
// spawned. The systemd unit and the launchd plist both exec `runtime`,
// so on those paths — the PRIMARY autostart paths on Linux and macOS —
// it was never called at all, and a runtime that stays up for weeks had
// nothing that would ever shrink its log (R6).
//
// # Why it tees, and when it must not
//
// The three supervision modes leave stderr pointing at three different
// things, and this function's whole subtlety is telling them apart:
//
//	`dexel start` (and XDG autostart / the Windows Run key, which run
//	`start`): the CLI opened the log file and handed it to us as fd 1
//	AND fd 2. Our stderr already IS the log file, so teeing would write
//	every line into it twice.
//
//	launchd: StandardOutPath/StandardErrorPath name the same log file,
//	so launchd opened it and handed it to us. Same case as above.
//
//	systemd: StandardOutput/StandardError=journal, so stderr is a
//	journald socket and the log FILE has nobody writing to it at all.
//	Here we must tee — the journal keeps working (`journalctl --user -u
//	dexel`) and `dexel logs` finally has content to show, which the
//	unit's own comment always claimed and until now was not true of.
//
//	a terminal (`dexel runtime` run by hand): stderr is the tty. Tee, so
//	the developer sees the lines AND the file records them.
//
// os.SameFile is the test, on the descriptor rather than on the path, so
// it answers the real question ("is this the same inode?") and not a
// re-derived guess about which mechanism started us.
//
// Nothing here is fatal. A log we cannot open leaves logging exactly
// where it was — on the inherited stderr — and says so there.
func attachRuntimeLog() func() {
	path, err := paths.LogFile()
	if err != nil {
		log.Printf("resolve log path: %v — this runtime's log cannot be rotated in-process", err)
		return func() {}
	}
	w, err := lifecycle.NewRotatingWriter(path, lifecycle.RotationThreshold(os.Getenv))
	if err != nil {
		log.Printf("%v — logging stays on the inherited stderr and nothing will rotate it", err)
		return func() {}
	}
	if w.SameFileAs(os.Stderr) {
		// Our stderr is this very file: take it over outright.
		log.SetOutput(w)
	} else {
		log.SetOutput(io.MultiWriter(os.Stderr, w))
	}
	log.Printf("log: %s, rotated in-process at %d bytes (keeping one %s.1)", path, lifecycle.RotationThreshold(os.Getenv), lifecycle.LogFileName)
	return func() { _ = w.Close() }
}

// listenRuntime binds the listener, preferring the port this state
// directory's last runtime used (R9).
//
// # The bug this fixes
//
// A runtime binds an ephemeral port by design (serveMode.defaultAddr).
// Both supervisors restart it after a crash — systemd `Restart=on-failure`,
// launchd `KeepAlive{SuccessfulExit:false}` — and the restarted runtime
// used to land on a DIFFERENT port. Every already-open page then retried
// its dead `location.host` forever and the Tauri shell, which resolves
// the URL once at startup, never re-resolved: a healthy runtime, and
// every window a grey box.
//
// # The semantics, stated exactly
//
//   - modeRuntime only, and only when -addr's port is 0. An explicit
//     -addr is never second-guessed (lifecycle.StickyAddr enforces both
//     halves).
//   - The record is advisory. If the port is not available the bind
//     falls back to the requested address, i.e. to an OS-assigned port,
//     and the record is then overwritten with what we actually got. A
//     sticky port can never keep the runtime from starting, and can
//     never be taken from whoever holds it.
//   - The record survives a clean exit, unlike runtime.json, so `dexel
//     restart` and a bookmarked browser tab benefit too — not only the
//     crash-restart case.
//   - Two runtimes cannot fight over it: runtime.lock (taken above) is
//     what makes one-runtime-per-state-dir true, and it is taken before
//     we get here.
func listenRuntime(mode serveMode, stateDir, addr string) (net.Listener, error) {
	if mode == modeRuntime {
		sticky, err := lifecycle.ReadStickyPort(stateDir)
		if err != nil {
			// A corrupt record is said out loud and then ignored; it is
			// a hint about a port, not state anything depends on.
			log.Printf("sticky port: %v — taking an OS-assigned port", err)
		} else if want, ok := lifecycle.StickyAddr(addr, sticky); ok {
			stickyLn, stickyErr := net.Listen("tcp", want)
			if stickyErr == nil {
				log.Printf("sticky port: rebound %s, the port the last runtime here used — pages and windows opened against it reconnect on their own", want)
				return stickyLn, nil
			}
			log.Printf("sticky port: %s is unavailable (%v) — taking an OS-assigned port instead; already-open pages will need `dexel open`", want, stickyErr)
		}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// The exact message the pre-R9 inline net.Listen produced, kept
		// verbatim: the legacy foreground shape's output is a
		// compatibility contract (see serveMode's doc comment).
		return nil, fmt.Errorf("listen on %s: %v", addr, err)
	}
	return ln, nil
}

// recordStickyPort persists the port ln actually bound, for the next
// runtime in this state directory to try. Failure is logged and survived:
// the worst case is that the next restart takes a fresh ephemeral port,
// which is precisely today's behaviour.
func recordStickyPort(stateDir string, ln net.Listener) {
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		log.Printf("sticky port: listener address %s is not TCP — not recording a port", ln.Addr())
		return
	}
	if err := lifecycle.WriteStickyPort(stateDir, tcp.Port); err != nil {
		log.Printf("sticky port: %v — the next restart will take a fresh OS-assigned port", err)
	}
}

// filesOnlyFS wraps a static tree so it serves FILES and nothing else —
// INTERACTION-HARDENING (docs/plan/ROADMAP.md): "The server must NOT serve
// directory listings (/assets/ currently lists all files via
// http.FileServer). Files only, no index pages, both embedded and
// disk-override modes."
//
// # What was wrong
//
// http.FileServer's documented behaviour for a directory URL is to serve
// that directory's index.html if one exists and otherwise to render a
// generated HTML listing of every entry. Neither static tree dexel serves
// has an index.html below its own root, so `GET /assets/` returned a 200
// with a browsable index of every sprite in the product, and `GET /js/`,
// `/css/`, `/fonts/` did the same for the frontend. That is a real, if
// small, information leak (it enumerates files a client was never told
// about, including ones a future build might not mean to publish), and it
// is a UX lie: a directory is not a page of this application.
//
// # The rule this enforces
//
// One mount root serves an index page — "/" — and every other directory
// URL under either mount is a 404. Concretely, for a request whose path
// has already had its mount prefix stripped:
//
//	""  or "/"          -> rootIndex ? the tree's index.html : 404
//	anything ending "/" -> 404          (an explicit directory request)
//	names a directory   -> 404          (bare "/js", which FileServer
//	                                     would 301 to "/js/" and list)
//	names a file        -> the file, served by http.FileServer
//
// The last line is the point of wrapping rather than reimplementing:
// http.FileServer/http.ServeContent own conditional requests, Range,
// Last-Modified/ETag and content sniffing, and all of that keeps working
// unchanged for every real file (js, css, fonts, PNGs). Only the
// directory cases are intercepted, so nothing that used to load stops
// loading — see TestStaticTreesServeFilesOnly, which asserts both halves
// (404s AND 200s) against BOTH the embedded and the disk-override tree.
//
// rootIndex is true only for the frontend mount, where "/" IS the
// application. The /assets/ mount passes false: its root is a sprite
// directory, and a request for it is a request for nothing.
//
// The check is deliberately on the fs.FS, not on the URL string, because
// which names are directories is a property of the tree — and the two
// trees this serves (embed.FS via fs.Sub, os.DirFS) must behave
// identically, which is exactly what a stat on the fs.FS gives.
func filesOnlyFS(fsys fs.FS, rootIndex bool) http.Handler {
	files := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// http.StripPrefix leaves "/assets/" as "" and "/assets/x.png" as
		// "x.png", so the leading slash is restored before cleaning to
		// keep one code path for both mounts.
		upath := r.URL.Path
		if !strings.HasPrefix(upath, "/") {
			upath = "/" + upath
		}
		// path.Clean resolves any ".." before it is ever handed to the
		// tree; it also collapses "//" and trailing dots. The trailing
		// slash is checked on the RAW path because Clean removes it.
		cleaned := path.Clean(upath)

		if cleaned == "/" {
			if !rootIndex {
				http.NotFound(w, r)
				return
			}
			// Delegate: FileServer serves this root's index.html with
			// full conditional-request handling, exactly as before.
			files.ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(upath, "/") {
			http.NotFound(w, r)
			return
		}
		info, err := fs.Stat(fsys, strings.TrimPrefix(cleaned, "/"))
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}
