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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/activity"
	"github.com/jawwadzafar/dexel/app/internal/assets"
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
// body (dev_docs/production-runtime/ARCHITECTURE.md Decision 3 and FORK
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
	persistConfig := func() error {
		if cfgPath == "" {
			return errors.New("no config path (home directory unresolved at startup)")
		}
		return store.SaveConfig(cfgPath, store.ConfigData{
			Name:         g.ConfigName(),
			SessionNames: store.SessionNamesToConfig(g.SessionNames()),
		})
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
			popEndedSession(g, hub, savePath)

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
			popped := popEndedSession(g, hub, savePath)
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

// actionSetName is SET_NAME's wire literal (docs/ui-spec.md §6.2), named
// once so applyAction and main's write-through loop cannot drift apart on
// a typo — the loop has to recognise the same action applyAction handled
// in order to know a config write is owed.
const actionSetName = "SET_NAME"

// actionSessionStart/actionSessionStop are SESSION_START/SESSION_STOP's
// wire literals (docs/plan/P2-design.md §6.2, docs/ui-spec.md §9.6),
// PINNED names per §8's contract seam. Named once for the same reason
// actionSetName is: applyAction and main's action-loop (the config
// write-through, the pending-record pop, the discard flash) must
// recognise the exact same literal, never a re-typed copy of it.
const (
	actionSessionStart = "SESSION_START"
	actionSessionStop  = "SESSION_STOP"
)

// actionPause/actionResume are PR-5's wire literals
// (dev_docs/production-runtime/ARCHITECTURE.md §3's `PAUSE`/`RESUME` on the
// existing actions channel). Named once for exactly the reason
// actionSetName and the two session literals are: applyAction handles them,
// and runServe's action loop has to recognise the SAME literal to know that
// a provider stop/start, an engine reset and an immediate save are owed.
//
// They are reachable two ways, both of which land here: POST
// /api/lifecycle/{pause,resume} (token-gated, the CLI's path) and a
// WebSocket action from the UI. That is deliberate — Decision 5's "the
// WebSocket UI gets a pause button for free, with no second code path" —
// and it is safe in a way `stop` is not: pausing is a privacy-positive,
// fully reversible act, whereas "a web page must never be able to kill the
// runtime".
const (
	actionPause  = "PAUSE"
	actionResume = "RESUME"
)

// applyAction runs one client action against g and reports whether state
// changed plus the flash it produced (docs/ui-spec.md §6.2: "no dedicated
// ack... every successful action is answered by an immediate state
// broadcast plus a flash; every failure by a flash of kind error").
// STORE_OPEN/STORE_CLOSE mutate state but produce no flash. connID is the
// sending connection's id (already plumbed through hub.go's
// actionRequest) — STORE_OPEN/CLOSE key their hold on the work gate by it
// (game.Game.OpenStore/CloseStore) so one client's close or disconnect can
// never release a different client's gate.
func applyAction(g *game.Game, msg actionMessage, connID uint64) (mutated bool, flash *flashMessage) {
	errFlash := func(err error) *flashMessage {
		return &flashMessage{Type: "flash", Kind: "error", Text: err.Error()}
	}

	switch msg.Action {
	case "BUY_ITEM":
		if err := g.BuyItem(msg.ItemID); err != nil {
			return false, errFlash(err)
		}
		item, _ := g.ItemByID(msg.ItemID)
		slot, _ := g.SlotByID(item.Slot)
		return true, &flashMessage{Type: "flash", Kind: "purchase", Text: fmt.Sprintf("%s %s!", item.Name, strings.ToLower(slot.Name))}

	case "BUY_TINT":
		if err := g.BuyTint(msg.ItemID, msg.TintID); err != nil {
			return false, errFlash(err)
		}
		item, _ := g.ItemByID(msg.ItemID)
		tint, _ := g.TintByID(msg.TintID)
		slot, _ := g.SlotByID(item.Slot)
		return true, &flashMessage{Type: "flash", Kind: "purchase", Text: fmt.Sprintf("%s %s!", tint.Name, strings.ToLower(slot.Name))}

	case "EQUIP_ITEM":
		var tintPtr *string
		if msg.TintID != "" {
			tintPtr = &msg.TintID
		}
		if err := g.EquipItem(msg.Slot, msg.ItemID, tintPtr); err != nil {
			return false, errFlash(err)
		}
		item, _ := g.ItemByID(msg.ItemID)
		return true, &flashMessage{Type: "flash", Kind: "equip", Text: fmt.Sprintf("Equipped %s!", item.Name)}

	case actionSetName:
		// Phase P1 (docs/ui-spec.md §6.2 SET_NAME). Server-side
		// validation is game.NormalizeName's; an empty/whitespace-only/
		// control-character-only submission is an ordinary error flash
		// with NO state change (never a stored blank, which would
		// re-trigger onboarding on the next boot). A successful set also
		// clears the onboarding flag inside SetConfigName, so the
		// broadcast this returns is what closes the modal client-side —
		// there is no dedicated ack (§6.2).
		name, err := g.SetConfigName(msg.Name)
		if err != nil {
			return false, errFlash(err)
		}
		return true, &flashMessage{Type: "flash", Kind: "welcome", Text: "Hello, " + name + "!"}

	case actionPause, actionResume:
		// PR-5 (ARCHITECTURE.md §6). This is the whole of pause's
		// game-state transition: flip the flag (and, on the way in, park
		// the mood so nothing keeps claiming an observation that has
		// stopped — see game.Game.SetPaused). Everything that makes the
		// pause REAL rather than cosmetic — provider.Stop(), skipping
		// eng.Tick(), Engine.Reset() on the way back, the immediate save
		// — belongs to the caller's loop, which owns those resources.
		//
		// No flash: pause is a state, not an event, and `paused` on the
		// next state broadcast is what the UI renders (Decision 15).
		// STORE_OPEN/STORE_CLOSE below set the same precedent — mutate
		// state, produce no toast.
		//
		// An already-paused PAUSE (or an already-running RESUME) reports
		// mutated=false, which is what makes a repeated `dexel pause`
		// idempotent all the way down: no second provider stop, no
		// second save, no redundant broadcast.
		return g.SetPaused(msg.Action == actionPause), nil

	case "STORE_OPEN":
		g.OpenStore(connID)
		return true, nil

	case "STORE_CLOSE":
		g.CloseStore(connID)
		return true, nil

	case actionSessionStart:
		// Phase P2 (docs/plan/P2-design.md §2.2 "Start", §6.2;
		// docs/ui-spec.md §9.6). Server-side validation only
		// (game.NormalizeSessionName): control characters dropped,
		// trimmed, capped at MaxSessionNameLen runes — and unlike
		// SET_NAME, an EMPTY result is legal ("unnamed" is a first-class
		// session, not a rejection), so StartSession only ever errors
		// when a session is already active (exactly one at a time). The
		// flash text is fixed and composed here, never assembled
		// client-side (ui-spec §6.1's "no client-side assembly" rule).
		// This does not yet WRITE the name to config.json — main's
		// action-loop write-through (mirroring SET_NAME's persistConfig
		// call) does that immediately after this returns, whether or not
		// the name ended up empty.
		if err := g.StartSession(msg.Name); err != nil {
			return false, errFlash(err)
		}
		return true, &flashMessage{Type: "flash", Kind: "session", Text: "Session started."}

	case actionSessionStop:
		// Phase P2 (docs/plan/P2-design.md §2.2 "Stop", §6.2;
		// docs/ui-spec.md §9.6). StopSession's only rejection is "no
		// session is active" — no state change. On success the session
		// is ALWAYS cleared; whether it produced a poppable record (a
		// session under SessionMinDurationSeconds never does — Fork
		// P2-E) is decided entirely inside game.Game. main's action-loop
		// pops it via g.TakeEndedSession() immediately after this
		// returns (the pending-record seam) — which is also where the
		// "too short to keep" flash and the real sessionComplete
		// celebration are decided, so this case deliberately returns no
		// flash of its own.
		if err := g.StopSession(); err != nil {
			return false, errFlash(err)
		}
		return true, nil

	default:
		return false, errFlash(fmt.Errorf("unknown action %q", msg.Action))
	}
}

// popEndedSession implements P2's pending-record seam end to end
// (docs/plan/P2-design.md §2.2, §3.1; ADR 0017 Decision 5): called
// immediately after every g.Tick and every applyAction (main's select
// loop, both call sites), it pops at most one completed-session record
// via g.TakeEndedSession and, if there is one, persists it — via
// store.AppendSession, GO-2's one-transaction API, so the log row and the
// rewritten signed snapshot land together or not at all — and celebrates
// it: a `state` broadcast (the just-appended record is already reflected
// there — g.State()'s Sessions.Recent is built straight from the same
// in-memory sessionLog finishSession appended to, see game/session.go),
// then the dedicated `sessionComplete` message, then the ordinary gold
// `flash{kind:"session"}` toast with server-composed text. This is the
// SAME path whether the record came from a user's SESSION_STOP or an
// automatic idle/maxDuration end (docs/plan/P2-design.md §2.2's "both...
// must be persisted and celebrated identically").
//
// Returns whether a record was actually popped, so the SESSION_STOP call
// site can tell a genuine completion apart from a discarded (< 60s)
// session, for which it owes its own "too short to keep" flash instead.
func popEndedSession(g *game.Game, hub *Hub, savePath string) bool {
	rec, ok := g.TakeEndedSession()
	if !ok {
		return false
	}

	d := store.Snapshot(g)
	newHead, err := store.AppendSession(savePath, d, store.SessionSaveFromRecord(rec))
	if err != nil {
		// The record is already safely sitting in g's in-memory
		// sessionLog (finishSession appended it there before ever
		// setting the pending pointer this function just cleared) — only
		// the DURABLE, chained-MAC copy failed to land. Surfacing this as
		// an honest error flash, rather than fabricating a "complete"
		// celebration for a write that did not actually happen, matches
		// every other write-failure in this file (see persistConfig's
		// callers above): the in-memory state stands, the toast tells the
		// truth about persistence instead.
		log.Printf("append session failed: %v", err)
		hub.broadcastFlash(flashMessage{Type: "flash", Kind: "error", Text: "could not save session"})
		return true
	}
	g.SetSessionLogHead(newHead)

	state := g.State()
	hub.broadcastState(state)

	// state.Sessions.Recent[0] IS this record's own wire view: finishSession
	// (game/session.go) appended rec to g.sessionLog synchronously, before
	// this function ever ran, and nothing else can finish a session
	// between that append and this g.State() call (main's select loop is
	// the single owner of g, and this function runs entirely within one
	// iteration of it) — so this is the exact SessionView the design's
	// `sessionComplete` message and gold flash need, with none of
	// game/session.go's private sessionViewFromRecord duplicated here.
	var view game.SessionView
	if len(state.Sessions.Recent) > 0 {
		view = state.Sessions.Recent[0]
	}
	hub.broadcastSessionComplete(sessionCompleteMessage{Type: "sessionComplete", V: 1, Session: view})
	hub.broadcastFlash(flashMessage{Type: "flash", Kind: "session", Text: sessionCompleteFlashText(view)})
	return true
}

// sessionCompleteFlashText composes the gold toast's text for a completed
// session (docs/plan/P2-design.md §3.1: "e.g. 'auth refactor — 1h 24m
// together.', or 'Session complete — 1h 24m together.' when unnamed") —
// server-side, never assembled by the client (ui-spec §6.1).
func sessionCompleteFlashText(v game.SessionView) string {
	dur := formatSessionDuration(v.DurationSeconds)
	if v.Name != "" {
		return fmt.Sprintf("%s — %s together.", v.Name, dur)
	}
	return fmt.Sprintf("Session complete — %s together.", dur)
}

// formatSessionDuration renders a whole-seconds duration as "XhYm"
// (dropping the hours segment under an hour) for the session-complete
// flash — e.g. 5040s -> "1h 24m", matching docs/plan/P2-design.md §3.1's
// own worked example. Every session this ever runs against is already
// >= game.SessionMinDurationSeconds (60s), since a shorter one is
// discarded before a record is ever produced, so the minutes segment is
// always meaningful even with no hours.
func formatSessionDuration(totalSeconds uint64) string {
	d := time.Duration(totalSeconds) * time.Second
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// wsOriginPatterns derives the WebSocket Origin patterns handleWS should
// accept from -addr's port (B1: the server used to accept literally any
// Origin — InsecureSkipVerify: true — which let any web page a user had
// open in another tab pop a cross-origin WS to this loopback server and
// read activity state or mutate the save). A same-origin browser tab
// always sends an Origin that is either empty (accepted unconditionally
// by nhooyr.io/websocket — see handlers.go) or exactly matches r.Host
// (also accepted unconditionally); these two patterns exist ONLY to cover
// the other loopback hostname the frontend might be addressed by when
// that differs from -addr's own host (127.0.0.1 vs. localhost) — never a
// non-loopback host, regardless of what -addr itself was bound to,
// because this process is meant to be reached over loopback only.
//
// -addr must have a port (flag.String's default does; a malformed
// override is a startup misconfiguration worth failing loudly on rather
// than silently guessing a port that would authorize the wrong Origin).
func wsOriginPatterns(addr string) []string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		log.Fatalf("bad -addr %q: %v (want host:port, e.g. 127.0.0.1:8080)", addr, err)
	}
	return []string{"127.0.0.1:" + port, "localhost:" + port}
}

// handshakeLine formats the one stable, machine-readable line this
// process prints to stdout once its listener is bound (ADR 0015 /
// docs/plan/F3-design.md §1,§8 T2): "DEXEL_LISTENING http://<addr>". A
// future parent process (the Tauri shell's T1) reads this exact prefix
// off the sidecar's stdout to learn which port an ephemeral
// `-addr 127.0.0.1:0` bind actually landed on, before it can open a
// window pointed at this server — so the shape must stay stable; this is
// factored out purely so the shape itself is unit-testable without
// spinning up main()'s whole server.
func handshakeLine(addr string) string {
	return "DEXEL_LISTENING http://" + addr
}

// originList is a repeatable flag.Value for -allow-origin: each
// occurrence (and each comma-separated part within one occurrence)
// appends to the slice, unlike flag.String which would keep only the
// last occurrence and silently drop earlier ones.
type originList []string

func (o *originList) String() string { return strings.Join(*o, ",") }

func (o *originList) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			*o = append(*o, part)
		}
	}
	return nil
}

// wsExtraOriginPatterns converts each -allow-origin flag value into a
// host pattern to append to wsOriginPatterns's result. nhooyr.io/
// websocket's OriginPatterns is matched against the parsed Origin
// header's *host* only (see its accept.go authenticateOrigin), not the
// full origin string, so a bare "tauri://localhost" would never match
// anything if appended verbatim — hence each value here may be given
// either as a bare host[:port] (e.g. "tauri.localhost") or as a full
// origin URL (e.g. "tauri://localhost", "http://tauri.localhost" — the
// two real per-platform Tauri v2 origins docs/plan/F3-design.md §2
// names), from which the host is extracted.
//
// This is deliberately tight, additive insurance (§2: "specific literal
// origins... never *"), not a second -insecure-origin: a wildcard or an
// unparseable/hostless value is a startup misconfiguration worth failing
// loudly on, exactly like wsOriginPatterns's own -addr validation, rather
// than silently degrading the origin check.
func wsExtraOriginPatterns(origins []string) ([]string, error) {
	patterns := make([]string, 0, len(origins))
	for _, o := range origins {
		host := o
		if strings.Contains(o, "://") {
			u, err := url.Parse(o)
			if err != nil {
				return nil, fmt.Errorf("bad -allow-origin %q: %w", o, err)
			}
			host = u.Host
		}
		if host == "" {
			return nil, fmt.Errorf("bad -allow-origin %q: empty host", o)
		}
		if strings.Contains(host, "*") {
			return nil, fmt.Errorf("bad -allow-origin %q: wildcard origins are never allowed", o)
		}
		patterns = append(patterns, host)
	}
	return patterns, nil
}

// selectProvider builds the activity.Provider for this run. -fake-script
// ((or DEXEL_FAKE_SCRIPT)) always wins so a scripted demo/test run is
// never accidentally overridden by a real capture path; otherwise "auto"
// picks this OS's native provider (see provider_select_*.go) and "fake"
// uses the env-driven fake provider.
func selectProvider(kind, fakeScript string) (activity.Provider, string) {
	if fakeScript != "" {
		steps, err := activity.ParseFakeScript(fakeScript)
		if err != nil {
			log.Printf("bad -fake-script %q: %v; falling back to the default demo script", fakeScript, err)
			steps = activity.DefaultFakeScript()
		}
		return activity.NewFakeProvider(steps, activity.HonestyGlobal), "fake (explicit -fake-script)"
	}
	switch kind {
	case "fake":
		return activity.NewFakeProviderFromEnv(), "fake (DEXEL_FAKE_SCRIPT or built-in demo)"
	case "auto":
		return platformProvider(), platformProviderName
	default:
		log.Fatalf(`unknown -provider %q (want "auto" or "fake")`, kind)
		return nil, ""
	}
}

// staticSource labels where one of the two static trees is being served
// from this run. Both values reach the outside world verbatim: they are
// logged at startup and reported by /api/health, so a bug report can say
// "the binary was serving its own embedded copy" or "it was serving my
// working tree" without anyone having to guess from flags.
const (
	sourceEmbedded = "embedded"
	sourceDisk     = "disk"
	// sourceMixed is only ever the AGGREGATE "source" field's value: one
	// tree embedded and the other overridden onto disk, which is a normal
	// dev combination (iterate on sprites, keep the shipped frontend) but
	// never something either individual tree reports about itself.
	sourceMixed = "mixed"
)

// resolvePublicSource decides which frontend tree "/" serves (EMBED-1).
//
// publicDir is -public's value. Empty (the default, and the only thing a
// released binary ever sees) means "the copy embed.go compiled into this
// binary" — that is what makes ./dexel work alone in an empty directory. A
// non-empty value is an explicit DEV override: serve that directory off
// disk, so editing app/public and reloading the page needs no Go rebuild.
//
// The override is deliberately the ONLY way to get disk mode. There is no
// implicit "is there a ./public next to my cwd?" probe, and nothing is ever
// created on disk: the old behaviour (default -public "./public", plus an
// ensurePublicDirExists that manufactured an empty directory when that path
// missed) turned "launched from the wrong cwd" into a silent 200 serving a
// bare file listing. Now the default cannot miss, and a bad override warns
// loudly instead of being papered over.
//
// Returns the tree to serve, its source label, and whether index.html is
// really present (/api/health's publicOk).
func resolvePublicSource(publicDir string) (fsys fs.FS, source string, ok bool) {
	if publicDir == "" {
		embedded := embeddedPublicFS()
		ok = fsFileExists(embedded, "index.html")
		if !ok {
			// Unreachable in a binary built from a tree with a committed
			// bundle; worth saying out loud rather than 404ing mutely.
			log.Print("WARNING: the embedded frontend has no index.html — this binary was built without a frontend bundle")
		}
		log.Print("frontend: serving / from the copy embedded in this binary")
		return embedded, sourceEmbedded, ok
	}

	dir := publicDir
	if abs, err := filepath.Abs(publicDir); err == nil {
		dir = abs
	}
	ok = diskIndexExists(dir)
	log.Printf("frontend: -public %s — serving / from disk (dev override)", dir)
	if !ok {
		log.Printf("WARNING: %s has no index.html — the frontend will not load; check -public, or drop the flag to serve the embedded copy", dir)
	}
	return os.DirFS(dir), sourceDisk, ok
}

// resolveAssetsSource decides which sprite tree "/assets/" serves (EMBED-1),
// the same way resolvePublicSource does for the frontend: embedded by
// default, disk only when $DEXEL_ASSETS_DIR explicitly asks for it.
//
// This replaced a multi-strategy runtime lookup (walk upward from the
// executable, then upward from the cwd, then a candidate derived from
// wherever -public resolved) that existed purely because the sprites lived
// outside the binary and had to be FOUND. Embedded, there is nothing to find
// and nothing to disagree about — which also removes the failure mode that
// lookup was built to diagnose: sprites silently 404ing because the binary
// had been moved away from its checkout.
//
// The override is still trusted verbatim (no search), matching
// assets.EnvOverride's long-standing documented contract, but a value that
// does not look like a real assets directory now warns instead of only
// showing up later as 404s.
//
// Returns the tree to serve, the disk directory for /api/health (nil when
// embedded — there is no path to report), and the source label.
func resolveAssetsSource() (fsys fs.FS, dir *string, source string) {
	override, set := assets.OverrideDir()
	if !set {
		log.Print("assets: serving /assets/ from the copy embedded in this binary")
		return embeddedAssetsFS(), nil, sourceEmbedded
	}

	resolved := override
	if abs, err := filepath.Abs(override); err == nil {
		resolved = abs
	}
	log.Printf("assets: $%s=%q — serving /assets/ from disk (dev override)", assets.EnvOverride, override)
	if !assets.HasSentinel(resolved) {
		log.Printf("WARNING: %s has no %s — sprite requests will 404; unset $%s to serve the embedded copy", resolved, assets.SentinelFile, assets.EnvOverride)
	}
	return os.DirFS(resolved), &resolved, sourceDisk
}

// fsFileExists reports whether name is a regular file in fsys — the io/fs
// equivalent of diskIndexExists, used to sanity-check the embedded tree.
func fsFileExists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

// diskIndexExists reports whether dir actually holds the frontend's
// index.html, as opposed to being an empty or wrong directory that
// http.FileServer would otherwise serve as a silent-but-200 bare listing.
func diskIndexExists(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil && !info.IsDir()
}

// healthResponse is GET /api/health's body: a small, stable, machine-
// readable summary of the things known to silently misbehave (which static
// trees are in play, and whether the frontend is really there) plus two
// build identifiers, so a bug report or an automated check can distinguish
// "the server is fine, the browser lost the socket" from "the server itself
// never found its own files".
//
// Source/PublicSource/AssetsSource are EMBED-1 additions; every field that
// existed before it kept its name and meaning. Commit is PR-2
// (MIGRATION_PLAN.md §PR-2): Version used to BE buildVersion()'s output
// (the git revision, via debug.ReadBuildInfo) — that value now lives in
// Commit, unchanged, and Version becomes the ldflags-stamped semver-or-"dev"
// string (see version.go), because buildVersion() alone cannot report
// anything once a release binary is extracted from its archive with no
// .git directory nearby to have been built "at" in the first place.
type healthResponse struct {
	AssetsDir *string `json:"assetsDir"` // the disk directory /assets/ is served from; null when it is served from the embedded copy
	PublicOk  bool    `json:"publicOk"`  // true iff the serving frontend tree holds index.html
	Version   string  `json:"version"`   // ldflags-stamped semver, or "dev" for a plain `go build`/`go run .` — see version.go
	Commit    string  `json:"commit"`    // buildVersion()'s output: the VCS revision (plus "-dirty"), or "unknown"
	// Source is the aggregate: "embedded" when this binary is serving
	// only itself (the shipped configuration), "disk" when both trees are
	// overridden, "mixed" when one of each.
	Source       string `json:"source"`
	PublicSource string `json:"publicSource"` // "embedded" | "disk"
	AssetsSource string `json:"assetsSource"` // "embedded" | "disk"
}

// healthHandler serves the fixed healthResponse computed once at startup
// (every field is decided during startup and never changes for the life of
// the process) as JSON.
func healthHandler(assetsDir *string, publicOk bool, version, commit, publicSource, assetsSource string) http.HandlerFunc {
	body, err := json.Marshal(healthResponse{
		AssetsDir:    assetsDir,
		PublicOk:     publicOk,
		Version:      version,
		Commit:       commit,
		Source:       aggregateSource(publicSource, assetsSource),
		PublicSource: publicSource,
		AssetsSource: assetsSource,
	})
	if err != nil {
		log.Fatalf("marshal health response: %v", err) // unreachable: healthResponse always marshals
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

// aggregateSource collapses the two per-tree labels into /api/health's
// headline "source": the two agree in both configurations anyone ships or
// debugs end-to-end, and disagree only in a partial dev override.
func aggregateSource(publicSource, assetsSource string) string {
	if publicSource == assetsSource {
		return publicSource
	}
	return sourceMixed
}

// buildVersion returns the VCS revision this binary was built from (plus a
// "-dirty" suffix if the working tree had uncommitted changes at build
// time), via the module build info Go embeds automatically — no ldflags or
// separate VERSION file required. "unknown" if that information isn't
// available (e.g. a binary built with GOFLAGS=-buildvcs=false).
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	rev, dirty := "unknown", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}

// loadOrImport restores g's persisted state. It is the ONLY place the
// legacy-import race is resolved: if state.json already exists, the
// legacy Rust save is never even opened (docs/upgrade-design.md: "if and
// only if state.json does not exist, look for the Rust save... no
// merging, ever"). Otherwise an existing legacy save is imported and
// immediately persisted as the new state.json, so the NEXT run finds
// state.json and this branch never fires again.
//
// Returns whether a save of ANY kind was found — Phase P1's fresh-install
// half of the onboarding decision (docs/ui-spec.md §7). "Any kind"
// deliberately includes the failure modes: a tampered save, a
// future-schema save and an unreadable save all report true, because each
// one proves somebody has played here before, and showing a returning
// user the intro is a worse outcome than a genuinely-fresh install
// missing it. Only "no state.db, no state.json, no legacy Rust save"
// reports false.
func loadOrImport(g *game.Game, savePath string) bool {
	// P2 (docs/plan/P2-design.md §5.4/§8, store.Apply's own doc comment):
	// store.LoadAll instead of the pre-P2 store.Load, so the finished
	// session log — verified by the same chained-MAC gate as everything
	// else in state.db — comes back alongside SaveData rather than being
	// silently discarded. Every failure branch below behaves exactly like
	// store.Load's thin-wrapper equivalent (Load = LoadAll, log
	// discarded), since LoadAll's error/ok shape is otherwise identical.
	data, sessions, ok, err := store.LoadAll(savePath)
	if err != nil {
		// SEC-1 (docs/plan/SEC-1-design.md §4, ADR 0014): ErrTampered and
		// ErrFutureSchema are NOT "no save" — store.Load never returns
		// them alongside ok==true, and they must never be treated like
		// the genuine "no save yet" case (ok==false, err==nil) below,
		// because that case is the ONLY one allowed to fall through to
		// the legacy-import path a few lines down. Legacy import grants
		// items and refunds Dev Cash; if a tampered or future-schema
		// state.json were mistaken for "no save," a hand-edited save
		// could trigger a legacy re-grant — the exact anti-cheat hole
		// SEC-1 closes. Both cases return immediately, before the
		// legacy-import path is even reached, leaving g at game.New()'s
		// fresh defaults; the next autosave writes a valid save.
		if errors.Is(err, store.ErrTampered) {
			// err's own text already names the real quarantined path —
			// state.json.invalid for the legacy-import branch, or
			// state.db.invalid for the SQLite path (db.go's failClosed) —
			// so log it verbatim instead of reconstructing savePath+".invalid",
			// which is wrong whenever savePath is the state.db path but the
			// error actually came from a state.json-shaped quarantine, or
			// vice versa.
			log.Printf("save integrity check failed; starting a fresh economy: %v", err)
			return true // a save existed — it just failed verification
		}
		if errors.Is(err, store.ErrFutureSchema) {
			log.Printf("save schema is newer than this build supports; starting fresh: %v", err)
			return true // a save existed — written by a newer build
		}
		log.Printf("load save failed (starting fresh): %v", err)
		// Fall through: a non-tamper, non-future error is most often an
		// unreadable-but-present file (a permission problem), so treat it
		// as "existed" below rather than offering a returning user the
		// first-launch intro. ok is false here, so nothing is applied.
		return true
	}
	if ok {
		// RestoreSessionLog BEFORE store.Apply — per §8's ordering rule
		// and store.Apply's own doc comment: Apply triggers RestoreStats,
		// and the log must already be in place before that runs, the
		// same A3 rule RestoreHistory/RestoreStreak already follow.
		// RestoreSessionNames already ran above, before this function was
		// even called, for the identical reason.
		recs, err := store.SessionRecordsFromSave(sessions)
		if err != nil {
			// Per SessionRecordsFromSave's own doc comment this should
			// never actually fire against a chain that already verified
			// — degrade to an honest empty log rather than failing the
			// whole boot over it, matching this file's general "a
			// corrupted save degrades field-by-field" stance.
			log.Printf("convert verified session log failed (starting with an empty session log): %v", err)
			recs = nil
		}
		g.RestoreSessionLog(recs)
		store.Apply(g, data)
		log.Printf("loaded save from %s (dev_cash=%d, sessions=%d)", savePath, g.DevCash, len(recs))
		return true
	}

	legacyPath, err := store.LegacyPath()
	if err != nil {
		log.Printf("resolve legacy save path: %v", err)
		return false // no save, and no legacy path to even look at
	}
	legacy, err := store.LoadLegacy(legacyPath)
	if err != nil {
		log.Printf("read legacy save failed (starting fresh): %v", err)
		return true // a legacy save is there, we just could not read it
	}
	if legacy == nil {
		log.Println("no save and no legacy save found: starting fresh")
		return false // the one genuine fresh-install case
	}

	imported := store.ImportLegacy(legacy, g.Catalog())
	// A legacy Rust save predates P2 by construction (state.db did not
	// exist yet — LoadAll's own doc comment: "the import branch always
	// returns a nil session log"), so g's session log is already the
	// honest empty default from game.New() and needs no restore call
	// here. RestoreSessionNames already ran above, before this function
	// was even called.
	store.Apply(g, imported)
	log.Printf("imported legacy save from %s (dev_cash=%d, owned=%v)", legacyPath, imported.DevCash, imported.OwnedItems)
	if err := store.Save(savePath, imported); err != nil {
		log.Printf("failed to persist imported legacy save: %v", err)
	}
	return true
}

// loadOrInitConfig loads ~/.config/dexel/config.json (SEC-1 design §1.2,
// §7 GO-2) on a path fully independent of state.json/loadOrImport: it is
// never blocked by, and never blocks, the protected-save load above,
// whether that load succeeded, found nothing, or hit a tampered/
// future-schema save. store.LoadConfig already degrades a missing or
// malformed config.json to ConfigData{} without an error (config is
// deliberately hand-editable and unsigned), so the only extra step here
// is: if no config.json exists yet, write one with SaveConfig so the user
// has a file to edit at all — that user-editable slot for the dexel's
// name is the entire point of splitting config out of the protected save.
// Returns the resolved config path and the loaded (or default) config.
// The path comes back so main's SET_NAME write-through has exactly the
// file this load read, and never re-derives it; "" means the home
// directory could not be resolved (logged here, and the returned config
// is the zero value, so the game runs unnamed rather than not at all).
//
// Phase P1 note: an EXISTING config.json holding an empty name is not the
// same thing as a returning user — a fresh install writes exactly that
// file on its very first boot (below), so the onboarding decision in
// main() keys off the NAME being empty, never off the file's absence.
func loadOrInitConfig() (string, store.ConfigData) {
	cfgPath, err := store.ConfigPath()
	if err != nil {
		log.Printf("resolve config path: %v", err)
		return "", store.ConfigData{}
	}
	cfg, err := store.LoadConfig(cfgPath)
	if err != nil {
		log.Printf("load config failed (using defaults): %v", err)
	}
	if _, statErr := os.Stat(cfgPath); os.IsNotExist(statErr) {
		if err := store.SaveConfig(cfgPath, cfg); err != nil {
			log.Printf("write default config failed: %v", err)
		} else {
			log.Printf("wrote default config to %s", cfgPath)
		}
	}
	return cfgPath, cfg
}

// writeRuntimeFile builds and writes runtime.json for a modeRuntime run
// (ARCHITECTURE.md Decision 6's exact object). Split out of runServe so
// the port-parsing and URL-normalising rules are testable on their own
// (cli_test.go) rather than only reachable by starting a real server.
//
// Returns the Runtime it wrote (PR-4, MIGRATION_PLAN.md §PR-4): the
// lifecycle handlers registered in runServe need this exact
// token/pid/port/url — the same values just persisted, not a second
// computation of them — to enforce Decision 8's token check and answer
// GET /api/lifecycle/status.
func writeRuntimeFile(stateDir, actualAddr string) (lifecycle.Runtime, error) {
	token, err := lifecycle.NewToken()
	if err != nil {
		return lifecycle.Runtime{}, err
	}
	rt, err := runtimeRecord(actualAddr, os.Getpid(), token, time.Now())
	if err != nil {
		return lifecycle.Runtime{}, err
	}
	if err := lifecycle.WriteRuntime(stateDir, rt); err != nil {
		return lifecycle.Runtime{}, err
	}
	return rt, nil
}

// runtimeRecord is writeRuntimeFile's pure core: the exact object
// ARCHITECTURE.md Decision 6 pins, derived from the RESOLVED listen
// address (so an ephemeral `-addr 127.0.0.1:0` publishes the real port,
// never the literal 0) and this build's own two version answers
// (version.go's ldflags semver, and buildVersion()'s VCS commit — PR-2).
//
// startedAt is RFC3339 in UTC so `dexel status` can subtract it from
// time.Now() without a timezone argument, and so the field means the same
// thing in a log a user pastes from another machine.
func runtimeRecord(actualAddr string, pid int, token string, now time.Time) (lifecycle.Runtime, error) {
	host, portStr, err := net.SplitHostPort(actualAddr)
	if err != nil {
		return lifecycle.Runtime{}, fmt.Errorf("split resolved listen address %q: %w", actualAddr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return lifecycle.Runtime{}, fmt.Errorf("resolved listen address %q has no usable port", actualAddr)
	}
	return lifecycle.Runtime{
		Schema:    lifecycle.RuntimeSchema,
		Pid:       pid,
		Port:      port,
		URL:       "http://" + net.JoinHostPort(reachableHost(host), portStr),
		Version:   version,
		Commit:    buildVersion(),
		StartedAt: now.UTC().Format(time.RFC3339),
		Token:     token,
	}, nil
}

// reachableHost turns a BIND host into a host something can actually
// connect to. A wildcard bind ("", "0.0.0.0", "::") is a valid listen
// address but a meaningless dial address, and runtime.json's url is a
// dial address — it is what `dexel status` probes and what `dexel open`
// hands a browser. Loopback is the honest substitute, and it is the only
// address ARCHITECTURE.md's posture expects the runtime to be reached on
// anyway.
func reachableHost(bindHost string) string {
	switch bindHost {
	case "", "0.0.0.0", "::", "[::]":
		return "127.0.0.1"
	default:
		return bindHost
	}
}
