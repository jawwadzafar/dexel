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
	publicDir := flag.String("public", "", "DEV OVERRIDE: serve the frontend from this directory on disk instead of the copy embedded in this binary (EMBED-1) — point it at app/public to iterate on the frontend without rebuilding Go; empty (the default) always serves the embedded copy")
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

	// EMBED-1: decide which copy of each static tree this run serves —
	// the embedded default, or an explicitly requested disk override —
	// before anything is mounted, so the startup log states it once and
	// /api/health can report it for the life of the process.
	publicFS, publicSource, publicOk := resolvePublicSource(*publicDir)
	assetsFS, assetsDir, assetsSource := resolveAssetsSource()

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
	saveExisted := loadOrImport(g, savePath)

	// SEC-1 (docs/plan/SEC-1-design.md §7 GO-2, ADR 0014): config.json is
	// loaded on a path fully independent of state.json/state.db above —
	// it is never blocked by, and never blocks, a tampered or
	// future-schema state load.
	//
	// Phase P1 (docs/plan/PRODUCT-EVOLUTION.md §5) is what finally reads
	// this name for something: SEC-1 deliberately shipped it with "no
	// wire change in this stage", and P1 puts it on StateMessage.Config —
	// still the user-authored, unsigned side of ADR 0014's split, still
	// never a SaveData field.
	cfgPath, cfg := loadOrInitConfig()
	g.RestoreConfigName(cfg.Name)
	if g.ConfigName() != "" {
		log.Printf("dexel name: %q", g.ConfigName())
	}

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
	mux.Handle("/", http.FileServer(http.FS(publicFS)))
	// "/assets/<file>" is the URL prefix the frontend's assetUrl() builds
	// against (app/frontend/src/assets.ts); StripPrefix turns it back into
	// the bare filename the tree — embedded or on disk — is keyed by.
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assetsFS))))
	mux.HandleFunc("/api/health", healthHandler(assetsDir, publicOk, buildVersion(), publicSource, assetsSource))
	mux.HandleFunc("/ws", hub.handleWS(actions, catalog))

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

	// persistConfig writes the dexel's name through to config.json
	// IMMEDIATELY after SET_NAME — deliberately not on the 30s autosave
	// timer the protected save uses. Naming your dexel is a one-shot
	// first-minutes moment; if the process died in the next 30 seconds
	// the user would be asked again, which is the one thing Phase P1's
	// "returning users never see it" criterion forbids.
	//
	// This is the ONLY place a name is written, and it writes
	// game.Game.ConfigName() — the value NormalizeName already
	// sanitised — never the raw client payload. cfgPath == "" means the
	// home directory could not be resolved at boot (already logged
	// there); the game still runs, the name just cannot outlive the
	// process.
	persistConfig := func() error {
		if cfgPath == "" {
			return errors.New("no config path (home directory unresolved at startup)")
		}
		return store.SaveConfig(cfgPath, store.ConfigData{Name: g.ConfigName()})
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
			// Phase P1 write-through. Kept HERE rather than inside
			// applyAction for the same reason the protected save is: this
			// loop owns all I/O, applyAction stays a pure state
			// transition over *game.Game (which is what lets
			// store_gate_test.go drive it with no filesystem at all). A
			// failed write is surfaced honestly — the in-memory name
			// stands so the session still works, but the success toast is
			// replaced by an error flash, because a warm "hello" for a
			// name that silently will not survive a restart is a lie.
			if mutated && req.msg.Action == actionSetName {
				if err := persistConfig(); err != nil {
					log.Printf("save config failed: %v", err)
					flash = &flashMessage{Type: "flash", Kind: "error", Text: "could not save name"}
				}
			}
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

// actionSetName is SET_NAME's wire literal (docs/ui-spec.md §6.2), named
// once so applyAction and main's write-through loop cannot drift apart on
// a typo — the loop has to recognise the same action applyAction handled
// in order to know a config write is owed.
const actionSetName = "SET_NAME"

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
// trees are in play, and whether the frontend is really there) plus a build
// identifier, so a bug report or an automated check can distinguish "the
// server is fine, the browser lost the socket" from "the server itself never
// found its own files".
//
// Source/PublicSource/AssetsSource are EMBED-1 additions; every field that
// existed before it kept its name and meaning.
type healthResponse struct {
	AssetsDir *string `json:"assetsDir"` // the disk directory /assets/ is served from; null when it is served from the embedded copy
	PublicOk  bool    `json:"publicOk"`  // true iff the serving frontend tree holds index.html
	Version   string  `json:"version"`
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
func healthHandler(assetsDir *string, publicOk bool, version, publicSource, assetsSource string) http.HandlerFunc {
	body, err := json.Marshal(healthResponse{
		AssetsDir:    assetsDir,
		PublicOk:     publicOk,
		Version:      version,
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
		store.Apply(g, data)
		log.Printf("loaded save from %s (dev_cash=%d)", savePath, g.DevCash)
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
