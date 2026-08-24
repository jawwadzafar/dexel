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
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
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
	} else if mode == modeServe {
		log.Print("serve: foreground dev mode — not taking runtime.lock and not writing runtime.json, so this instance is invisible to `dexel status`/`stop` and can run alongside a real runtime (ARCHITECTURE.md Decision 7)")
	}

	// Bind explicitly (ADR 0015 / docs/plan/F3-design.md §1,§8 T2) rather
	// than letting http.Server.ListenAndServe do it implicitly, so that
	// (a) -addr's port can be 0 and the OS-assigned real port is knowable
	// before anything else needs it (wsOriginPatterns below, the stdout
	// handshake, the human log line), and (b) a port-already-in-use error
	// fails loudly right here instead of after the rest of startup runs.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen on %s: %v", *addr, err)
	}
	actualAddr := ln.Addr().String()

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
	mux.Handle("/", http.FileServer(http.FS(publicFS)))
	// "/assets/<file>" is the URL prefix the frontend's assetUrl() builds
	// against (app/frontend/src/assets.ts); StripPrefix turns it back into
	// the bare filename the tree — embedded or on disk — is keyed by.
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assetsFS))))
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

	// persistConfig writes BOTH halves of config.json through to disk
	// IMMEDIATELY: the dexel's own name (Phase P1, after SET_NAME) and
	// the P2 per-session project-name map (docs/plan/P2-design.md §2.7,
	// after SESSION_START) — deliberately not on the 30s autosave timer
	// the protected save uses, and deliberately as ONE write for the one
	// shared file, so writing either half never clobbers the other.
	// Naming something (a dexel, or a session) is a one-shot
	// first-minutes moment; if the process died in the next 30 seconds
	// the user would be asked again (or the name would silently vanish),
	// which is the one thing Phase P1's "returning users never see it"
	// criterion — and P2's "a declared intention that silently fails to
	// survive a crash is worse than no name at all" — both forbid.
	//
	// This is the ONLY place either half is written, and it writes
	// game.Game.ConfigName()/SessionNames() — values NormalizeName/
	// NormalizeSessionName already sanitised — never a raw client
	// payload. cfgPath == "" means the home directory could not be
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
		return writeConfigThrough(cfgPath, g.ConfigName(), store.SessionNamesToConfig(g.SessionNames()))
	}

	// Single-owner loop: every mutation of g happens on this goroutine
	// only — game.Game does no locking of its own, so this loop IS the
	// lock. Every WS action funnels in over `actions`; the 1s engine tick,
	// the cosmetic ticker/terminal scroll, and autosave all live here too.
	for {
		select {
		case <-tickTicker.C:
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
			if mutated && (req.msg.Action == actionSetName || req.msg.Action == actionSessionStart) {
				if err := persistConfig(); err != nil {
					log.Printf("save config failed: %v", err)
					errText := "could not save name"
					if req.msg.Action == actionSessionStart {
						errText = "could not save session name"
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
