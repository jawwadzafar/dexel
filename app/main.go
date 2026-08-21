// Command app is the dexel server (ADR 0011): a Go backend that
// samples real activity, runs the ADR 0005/0010 economy+honesty engine,
// and serves the live game state over HTTP/WebSocket to the NES.css
// frontend in ./public, per docs/ui-spec.md's wire contract.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/activity"
	"github.com/jawwadzafar/dexel/app/internal/assets"
	"github.com/jawwadzafar/dexel/app/internal/engine"
	"github.com/jawwadzafar/dexel/app/internal/game"
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

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address (loopback by default — binding beyond 127.0.0.1/localhost exposes the activity monitor and save to your LAN/tailnet); a port of 0 (e.g. 127.0.0.1:0) binds an OS-assigned free port, reported via the DEXEL_LISTENING stdout handshake — see ADR 0015")
	publicDir := flag.String("public", "./public", "static frontend directory (owned by the frontend agent)")
	providerKind := flag.String("provider", "auto", `activity provider: "auto" (native for this OS) or "fake"`)
	fakeScript := flag.String("fake-script", "", "explicit fake-provider script (e.g. type:20s,idle:40s,mouse:15s); overrides DEXEL_FAKE_SCRIPT; implies -provider=fake")
	insecureOrigin := flag.Bool("insecure-origin", false, "accept WebSocket connections from ANY Origin, skipping same-origin verification entirely — for embedded webviews only (e.g. a file:// or app:// frontend whose Origin header, if any, will never match a loopback host pattern); never enable this if -addr binds beyond 127.0.0.1/localhost")
	var allowOrigins originList
	flag.Var(&allowOrigins, "allow-origin", "extra literal WebSocket Origin(s) to accept, beyond the loopback host(s) derived from -addr's resolved port (repeatable, and/or comma-separated within one occurrence); each value is a bare host[:port] (e.g. \"tauri.localhost\") or a full origin URL from which the host is extracted (e.g. \"tauri://localhost\", \"http://tauri.localhost\") — never a wildcard; insurance for a future embedded webview whose Origin header won't match a loopback pattern (ADR 0015/docs/plan/F3-design.md §2,§8); not needed, and unused in Phase 1, when the webview loads this server's own loopback URL")
	flag.Parse()

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

	// Stdout handshake (ADR 0015 / docs/plan/F3-design.md §1,§8 T2): print
	// exactly one stable, machine-readable line to STDOUT as soon as the
	// listener is bound, so a parent process (the future Tauri shell, T1)
	// can learn the real port from an ephemeral `-addr 127.0.0.1:0` bind
	// without guessing a port or polling for one. Deliberately independent
	// of the human-readable "dexel listening on..." log line below (which
	// goes through the log package, i.e. stderr by default): the parent
	// parses stdout, a person reads the log.
	fmt.Println(handshakeLine(actualAddr))

	ensurePublicDirExists(*publicDir)
	publicOk := publicIndexExists(*publicDir)
	if !publicOk {
		log.Printf("WARNING: %s has no index.html — the frontend will not load; check -public or the directory this process was launched from", *publicDir)
	}

	provider, providerDesc := selectProvider(*providerKind, *fakeScript)
	if err := provider.Start(); err != nil {
		// Not fatal: every provider degrades to a blind, zero-signal state
		// rather than crashing (ADR 0010's honesty rules then simply never
		// claim onBreak from it).
		log.Printf("activity provider start warning: %v", err)
	}
	log.Printf("activity provider: %s", providerDesc)

	g := game.New()

	savePath, err := store.DefaultPath()
	if err != nil {
		log.Fatalf("resolve save path: %v", err)
	}
	loadOrImport(g, savePath)

	// SEC-1 (docs/plan/SEC-1-design.md §7 GO-2, ADR 0014): config.json is
	// loaded on a path fully independent of state.json above — it is
	// never blocked by, and never blocks, a tampered or future-schema
	// state load. The loaded name is only held here for a future
	// Settings-modal UI (design §7: "no wire change in this stage" — it
	// is never added to SaveData, StateMessage, or any other wire
	// contract).
	if dexelName := loadOrInitConfig(); dexelName != "" {
		log.Printf("dexel name: %q", dexelName)
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

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(*publicDir)))
	assetsDir := registerAssetsRoute(mux, *publicDir)
	mux.HandleFunc("/api/health", healthHandler(assetsDir, publicOk, buildVersion()))
	mux.HandleFunc("/ws", hub.handleWS(actions, catalog))

	httpSrv := &http.Server{Handler: mux}
	go func() {
		log.Printf("dexel listening on http://%s (serving %s)", actualAddr, *publicDir)
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

	// Single-owner loop: every mutation of g happens on this goroutine
	// only — game.Game does no locking of its own, so this loop IS the
	// lock. Every WS action funnels in over `actions`; the 1s engine tick,
	// the cosmetic ticker/terminal scroll, and autosave all live here too.
	for {
		select {
		case <-tickTicker.C:
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

		case <-terminalTicker.C:
			g.AdvanceTerminal()

		case <-tickerTicker.C:
			g.RotateTicker()

		case <-saveTicker.C:
			persist()

		case req := <-actions:
			mutated, flash := applyAction(g, req.msg, req.connID)
			if mutated {
				hub.broadcastState(g.State())
			}
			if flash != nil {
				hub.broadcastFlash(*flash)
			}
			close(req.done)

		case <-sigCh:
			log.Println("shutting down: saving state...")
			persist()
			_ = provider.Stop()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = httpSrv.Shutdown(ctx)
			cancel()
			return
		}
	}
}

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

	case "STORE_OPEN":
		g.OpenStore(connID)
		return true, nil

	case "STORE_CLOSE":
		g.CloseStore(connID)
		return true, nil

	default:
		return false, errFlash(fmt.Errorf("unknown action %q", msg.Action))
	}
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

// registerAssetsRoute serves the repository's assets/ directory (the art
// agent's sprite/thumbnail PNGs) at "/assets/<file>", the URL prefix
// game.js's assetUrl() already builds against. This is a real route, not
// the app/public/assets -> ../../assets symlink an earlier pass used as a
// stopgap: a symlink checked into git breaks the moment this binary is
// built and run from anywhere that is not this exact checkout, and a repo
// symlink is also just one more thing a fresh clone or a zip download can
// silently fail to preserve. internal/assets.LocateVerbose() finds the real
// directory (env override, then upward from the executable, then upward
// from cwd, then the assets/ directory implied by wherever publicDir itself
// actually resolved — see that package's doc comment for the full lookup
// order and internal/assets.LocateVerbose's doc comment for why the
// public-derived candidate exists: it unifies this route's resolution with
// "/"'s, so the two static trees can no longer silently disagree about
// where the checkout root is).
//
// Every attempt LocateVerbose made is logged, in order, so a broken lookup
// (most commonly: $DEXEL_ASSETS_DIR set to a stale or wrong path,
// which is trusted verbatim with no existence check) is diagnosable from
// the log alone instead of just "sprite requests will 404".
//
// If assets/ cannot be found, the server still starts (matching the
// activity-provider failure mode elsewhere in main: never fatal for a
// missing *optional* signal source) — sprite requests simply 404, the same
// degraded-but-alive behaviour the DOM/CSS already tolerate per game.js's
// own comments. The frontend's own defence against that silent 404 is a
// visible in-scene banner (game.js's room_back.png <img> error handler),
// fed by this function's result via /api/health.
//
// Returns the located directory (nil if none was found) for healthHandler.
func registerAssetsRoute(mux *http.ServeMux, publicDir string) *string {
	derived := deriveAssetsCandidateFromPublic(publicDir)
	dir, attempts, err := assets.LocateVerbose(derived)
	for _, a := range attempts {
		log.Printf("assets lookup: %s", a.Strategy)
	}
	if err != nil {
		log.Printf("assets route not registered: %v (sprite requests will 404)", err)
		return nil
	}
	log.Printf("serving /assets/ from %s", dir)
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(dir))))
	return &dir
}

// deriveAssetsCandidateFromPublic returns the assets/ directory implied by
// wherever publicDir actually resolves to on disk. publicDir is normally
// ".../app/public" (the -public default, "./public", resolved relative to
// cwd with no upward walk of its own — unlike internal/assets.Locate, so a
// cwd that isn't exactly app/ silently gets an empty directory from
// ensurePublicDirExists instead of the real frontend); its grandparent is
// then the repo root, and "<repo root>/assets" is where the art agent's
// sprites really live. This gives LocateVerbose one more thing to try
// beyond the executable/cwd upward walks: whatever base directory main.go
// is ALREADY trusting for the frontend, so public/ and assets/ are resolved
// from one consistent base rather than two lookups that can disagree.
// Returns "" if publicDir itself doesn't resolve to a real directory (no
// point deriving a candidate from a base that's already broken).
func deriveAssetsCandidateFromPublic(publicDir string) string {
	abs, err := filepath.Abs(publicDir)
	if err != nil {
		return ""
	}
	if info, statErr := os.Stat(abs); statErr != nil || !info.IsDir() {
		return ""
	}
	return filepath.Join(filepath.Dir(filepath.Dir(abs)), "assets")
}

// ensurePublicDirExists creates an empty ./public (with a .gitkeep) if it
// doesn't exist yet. app/public/ belongs to the frontend agent — this
// backend only needs the directory to exist so http.FileServer doesn't
// 404 the whole server at startup; it never writes an index.html or any
// asset into it.
//
// Creating this placeholder is itself a silent-failure hazard: run from the
// wrong cwd with a relative -public (the default), this quietly manufactures
// an empty directory that makes "/" 200 with a bare file listing instead of
// erroring — see publicIndexExists, which main() uses right after this call
// to detect exactly that case and warn loudly instead of leaving it silent.
func ensurePublicDirExists(dir string) {
	if _, err := os.Stat(dir); err == nil {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("could not create %s: %v", dir, err)
		return
	}
	keep := dir + string(os.PathSeparator) + ".gitkeep"
	if _, err := os.Stat(keep); os.IsNotExist(err) {
		_ = os.WriteFile(keep, nil, 0o644)
	}
}

// publicIndexExists reports whether dir actually holds the frontend's
// index.html, as opposed to being an empty (possibly just-created by
// ensurePublicDirExists) directory that http.FileServer would otherwise
// serve as a silent-but-200 bare file listing.
func publicIndexExists(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil && !info.IsDir()
}

// healthResponse is GET /api/health's body: a small, stable, machine-
// readable summary of the two things known to silently misbehave (the
// assets/ and public/ lookups) plus a build identifier, so a bug report or
// an automated check can distinguish "the server is fine, the browser lost
// the socket" from "the server itself never found its own files".
type healthResponse struct {
	AssetsDir *string `json:"assetsDir"` // null if assets.LocateVerbose found nothing
	PublicOk  bool    `json:"publicOk"`  // true iff <publicDir>/index.html exists
	Version   string  `json:"version"`
}

// healthHandler serves the fixed healthResponse computed once at startup
// (assetsDir/publicOk never change for the life of the process) as JSON.
func healthHandler(assetsDir *string, publicOk bool, version string) http.HandlerFunc {
	body, err := json.Marshal(healthResponse{AssetsDir: assetsDir, PublicOk: publicOk, Version: version})
	if err != nil {
		log.Fatalf("marshal health response: %v", err) // unreachable: healthResponse always marshals
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
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
func loadOrImport(g *game.Game, savePath string) {
	data, ok, err := store.Load(savePath)
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
			log.Printf("save integrity check failed; starting a fresh economy — your file was preserved at %s.invalid", savePath)
			return
		}
		if errors.Is(err, store.ErrFutureSchema) {
			log.Printf("save schema is newer than this build supports; starting fresh — your file was preserved at %s.future: %v", savePath, err)
			return
		}
		log.Printf("load save failed (starting fresh): %v", err)
	}
	if ok {
		store.Apply(g, data)
		log.Printf("loaded save from %s (dev_cash=%d)", savePath, g.DevCash)
		return
	}

	legacyPath, err := store.LegacyPath()
	if err != nil {
		log.Printf("resolve legacy save path: %v", err)
		return
	}
	legacy, err := store.LoadLegacy(legacyPath)
	if err != nil {
		log.Printf("read legacy save failed (starting fresh): %v", err)
		return
	}
	if legacy == nil {
		log.Println("no save and no legacy save found: starting fresh")
		return
	}

	imported := store.ImportLegacy(legacy, g.Catalog())
	store.Apply(g, imported)
	log.Printf("imported legacy save from %s (dev_cash=%d, owned=%v)", legacyPath, imported.DevCash, imported.OwnedItems)
	if err := store.Save(savePath, imported); err != nil {
		log.Printf("failed to persist imported legacy save: %v", err)
	}
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
// Returns the loaded (or default) name; callers only log it for now — no
// wire change in this stage (SEC-1 design §7: the name is deliberately
// NOT added to SaveData, StateMessage, or any other wire contract).
func loadOrInitConfig() string {
	cfgPath, err := store.ConfigPath()
	if err != nil {
		log.Printf("resolve config path: %v", err)
		return ""
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
	return cfg.Name
}
