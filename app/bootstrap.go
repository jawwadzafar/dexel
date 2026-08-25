// bootstrap.go — startup persistence: loading (or starting fresh from)
// the protected save (loadOrImport), loading/initialising config.json
// (loadOrInitConfig) and writing it through (writeConfigThrough,
// persistConfig's pure core), and writing runtime.json for a modeRuntime
// run (writeRuntimeFile, runtimeRecord, reachableHost).
package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/game"
	"github.com/jawwadzafar/dexel/app/internal/lifecycle"
	"github.com/jawwadzafar/dexel/app/internal/store"
)

// loadOrImport restores g's persisted state from state.db (or the
// one-time state.json import, store.LoadAll's own decision tree).
//
// The name is now half a historical artifact: the "import" half was the
// legacy-Rust save import, deleted by B-2 (see the tail of this function
// for why). What remains is load-or-start-fresh, with store.LoadAll
// owning every integrity decision.
//
// Returns whether a save of ANY kind was found — Phase P1's fresh-install
// half of the onboarding decision (docs/ui-spec.md §7). "Any kind"
// deliberately includes the failure modes: a tampered save, a
// future-schema save and an unreadable save all report true, because each
// one proves somebody has played here before, and showing a returning
// user the intro is a worse outcome than a genuinely-fresh install
// missing it. Only "no state.db and no state.json" reports false.
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
		// them alongside ok==true, and they must never be collapsed into
		// the genuine "no save yet" case (ok==false, err==nil) below.
		// Pre-B-2 that mattered because "no save" unlocked the
		// legacy-Rust re-grant; with that path deleted the distinction
		// still decides onboarding (a returning user whose save was
		// tampered with is not a fresh install) and still keeps a
		// failed load from ever being papered over. Both cases return
		// immediately, leaving g at game.New()'s fresh defaults; the
		// next autosave writes a valid save.
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
		// A non-tamper, non-future error is most often an
		// unreadable-but-present file (a permission problem), so treat it
		// as "existed" rather than offering a returning user the
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
		// B-3 (docs/plan/REVIEW-2026-08-22.md): anchor the id sequence on
		// the VERIFIED log's own last row id, taken from `sessions`
		// straight out of store.LoadAll rather than from `recs`. The two
		// normally agree; they disagree exactly in the degraded branch
		// above, where a conversion failure leaves an empty in-memory log
		// while the DB still holds rows — and an id derived from that
		// empty log would collide with a row already on disk. RestoreSessionLog
		// raises the same floor from recs; this raises it again from the
		// authority, and the setter only ever raises.
		if n := len(sessions); n > 0 {
			g.SetSessionLogPersistedID(sessions[n-1].ID)
		}
		store.Apply(g, data)
		log.Printf("loaded save from %s (dev_cash=%d, sessions=%d, last persisted session id=%d)", savePath, g.DevCash, len(recs), g.SessionLogPersistedID())
		return true
	}

	// B-2 (docs/plan/REVIEW-2026-08-22.md): this is where the legacy-Rust
	// import used to live, and it is deliberately gone. store.LoadLegacy
	// read ~/.local/share/dev-companion/save.json (never DEXEL_HOME —
	// SF-7) and store.ImportLegacy took its `wallet` field VERBATIM as
	// devCash, plus every item the upgrade table could grant. Unsigned,
	// unbounded, and repeatable: delete state.db, write a save.json
	// claiming 18446744073709551615, restart, and the economy was minted
	// from nothing, again, as often as you liked. It was also the reason
	// SEC-1 had to argue at length that a tampered save must never be
	// mistaken for "no save".
	//
	// It is deleted rather than clamped because there is nobody to
	// migrate: the Rust/Bevy build's only public artifact (v0.1.0) has a
	// single download, the Go build has never been released at all, and a
	// bounded grant would still be a grant path — more code, more tests,
	// and the same "your economy came from a file you wrote yourself"
	// shape, just with a ceiling. A user who really does have a v0.1.0
	// Rust save now starts fresh, which is what a fresh install of an
	// unreleased product does.
	log.Println("no save found: starting fresh")
	return false // the one genuine fresh-install case

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
		// The default config this writes is the file a user will later
		// hand-edit, so it states SOUND-1's default rather than leaving
		// `"soundEnabled": null` in it. Both mean the same thing to the
		// loader (store.ConfigData.SoundEnabledOrDefault resolves nil to
		// true), but only one of them reads like a setting to a human
		// opening the file — and `null` where a bool belongs reads like
		// damage. This is a FRESH-FILE nicety and nothing more: the nil
		// state still exists, and is still the honest answer, for a
		// config.json written before this field did.
		fresh := cfg.SoundEnabledOrDefault()
		cfg.SoundEnabled = &fresh
		if err := store.SaveConfig(cfgPath, cfg); err != nil {
			log.Printf("write default config failed: %v", err)
		} else {
			log.Printf("wrote default config to %s", cfgPath)
		}
	}
	return cfgPath, cfg
}

// configPrefs is the set of SET-1 user preferences main.go writes through
// to config.json (docs/ui-spec.md §11). A named struct rather than two
// more bool parameters on writeConfigThrough: two adjacent bare bools at
// a call site are indistinguishable to a reader and trivially swappable
// by a future edit, and the whole point of this function is that a write
// of one field must never quietly corrupt another.
type configPrefs struct {
	AlwaysOnTop  bool
	ShowAwayTime bool
	// SoundEnabled (SOUND-1, docs/ui-spec.md §13) is a plain bool here even
	// though the field it writes is a *bool: by the time a value reaches
	// this struct it came from the live game.Game, which always holds a
	// concrete answer. The pointer exists on store.ConfigData only to tell
	// "absent" from "false" when READING a config.json written before this
	// preference existed — a distinction a write never has to make.
	SoundEnabled bool
}

// writeConfigThrough is persistConfig's pure core: the read-modify-write
// that keeps the halves main.go owns from disturbing the rest of
// config.json. Lifted to package scope for the same reason
// browserOpenCommand and paths.binDirFor are — so it is directly testable
// with a temp file instead of only through a running action loop.
//
// READ-MODIFY-WRITE, always. store.SaveConfig marshals the WHOLE struct,
// so anything not re-stated here would be written back as its zero value.
// That is not hypothetical: building a fresh ConfigData here once erased
// `autostart` on every SET_NAME (see persistConfig's own comment in
// main.go). SET-1's two preference fields — and SOUND-1's third — join the
// same discipline: every field this function does not own is loaded and
// left alone, and every field it does own is set from the live game, never
// from a remembered copy.
func writeConfigThrough(cfgPath, name string, sessionNames map[string]string, prefs configPrefs) error {
	cfg, err := store.LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("read config before write-through: %w", err)
	}
	cfg.Name = name
	cfg.SessionNames = sessionNames
	cfg.AlwaysOnTop = prefs.AlwaysOnTop
	cfg.ShowAwayTime = prefs.ShowAwayTime
	// Always a concrete value, never left nil: once dexel has written this
	// file the "never chosen" state is gone for good, so a later boot reads
	// the user's actual choice instead of falling back to the default and
	// silently un-muting someone who muted.
	sound := prefs.SoundEnabled
	cfg.SoundEnabled = &sound
	return store.SaveConfig(cfgPath, cfg)
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
