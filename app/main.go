// Command app is the dev-companion server (ADR 0011): a Go backend that
// samples real activity, runs the ADR 0005/0010 economy+honesty engine,
// and serves the live game state over HTTP/WebSocket to the NES.css
// frontend in ./public, per docs/ui-spec.md's wire contract.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jawwadzafar/dev-companion/app/internal/activity"
	"github.com/jawwadzafar/dev-companion/app/internal/assets"
	"github.com/jawwadzafar/dev-companion/app/internal/engine"
	"github.com/jawwadzafar/dev-companion/app/internal/game"
	"github.com/jawwadzafar/dev-companion/app/internal/store"
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
	addr := flag.String("addr", ":8080", "listen address")
	publicDir := flag.String("public", "./public", "static frontend directory (owned by the frontend agent)")
	providerKind := flag.String("provider", "auto", `activity provider: "auto" (native for this OS) or "fake"`)
	fakeScript := flag.String("fake-script", "", "explicit fake-provider script (e.g. type:20s,idle:40s,mouse:15s); overrides DEVCOMPANION_FAKE_SCRIPT; implies -provider=fake")
	flag.Parse()

	ensurePublicDirExists(*publicDir)

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

	eng := engine.New(provider)
	hub := newHub()
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
	registerAssetsRoute(mux)
	mux.HandleFunc("/ws", hub.handleWS(actions, catalog))

	httpSrv := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		log.Printf("dev-companion listening on http://localhost%s (serving %s)", *addr, *publicDir)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
			mutated, flash := applyAction(g, req.msg)
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
// STORE_OPEN/STORE_CLOSE mutate state but produce no flash.
func applyAction(g *game.Game, msg actionMessage) (mutated bool, flash *flashMessage) {
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
		g.OpenStore()
		return true, nil

	case "STORE_CLOSE":
		g.CloseStore()
		return true, nil

	default:
		return false, errFlash(fmt.Errorf("unknown action %q", msg.Action))
	}
}

// selectProvider builds the activity.Provider for this run. -fake-script
// (or DEVCOMPANION_FAKE_SCRIPT) always wins so a scripted demo/test run is
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
		return activity.NewFakeProviderFromEnv(), "fake (DEVCOMPANION_FAKE_SCRIPT or built-in demo)"
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
// silently fail to preserve. internal/assets.Locate() finds the real
// directory (env override, then upward from the executable, then upward
// from cwd — see that package's doc comment for the full lookup order and
// why both of the latter two are needed).
//
// If assets/ cannot be found, the server still starts (matching the
// activity-provider failure mode elsewhere in main: never fatal for a
// missing *optional* signal source) — sprite requests simply 404, the same
// degraded-but-alive behaviour the DOM/CSS already tolerate per game.js's
// own comments.
func registerAssetsRoute(mux *http.ServeMux) {
	dir, err := assets.Locate()
	if err != nil {
		log.Printf("assets route not registered: %v (sprite requests will 404)", err)
		return
	}
	log.Printf("serving /assets/ from %s", dir)
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(dir))))
}

// ensurePublicDirExists creates an empty ./public (with a .gitkeep) if it
// doesn't exist yet. app/public/ belongs to the frontend agent — this
// backend only needs the directory to exist so http.FileServer doesn't
// 404 the whole server at startup; it never writes an index.html or any
// asset into it.
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
