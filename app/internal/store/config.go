// config.go implements SEC-1's config/state split
// (docs/plan/SEC-1-design.md §1, docs/adr/0014-save-integrity-hmac-and-config-split.md):
// user-authored, hand-editable config lives in its own file, separate
// from the protected, HMAC'd SaveData at state.json.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jawwadzafar/dexel/app/internal/paths"
)

// ConfigData is the on-disk shape at ~/.config/dexel/config.json — a
// second, INDEPENDENT file from state.json's SaveData (SEC-1 design §1.2:
// two files, not one mixed file). Name is the dexel's name, freely set by
// the user, and is deliberately the ONLY free text anywhere in dexel's
// persistence (design §1.1). It is NOT a field on SaveData, and it is NOT
// on content_free_test.go's SaveData allow-list: that allow-list exists
// to prove state.json — the protected, HMAC'd file — carries no free
// text, and config.json is, by design, the one place free text
// legitimately lives instead. config.json is never MAC'd, is never
// touched by Load/Save's tamper path, and never influences the protected
// economy in any way — a corrupt or tampered state.json cannot take the
// user's name down with it, and a malformed config.json can never block
// the economy (design §1.2 point 3).
//
// Room for future cosmetic prefs (theme, always-on-top, sound…) on this
// same struct later — additive, the same pattern as SaveData's own
// schema bumps.
//
// SessionNames (P2, docs/plan/P2-design.md §2.7, ADR 0017 Decision 2) is
// the second piece of free text this file legitimately carries: an
// optional per-session project name, keyed by the session's INTEGER id
// (a decimal string, since JSON object keys must be strings — the id
// itself, never any text the user typed, is what the signed `sessions`
// log references). This is exactly the boundary DB-1-design.md §2.4
// warns is "most at risk of being crossed by accident": a *timestamped
// series* of project names is data about the work, the same artifact
// class ADR 0013 refused for hourly buckets, so it belongs here — in the
// unsigned, user-editable file — and NOT as a column on the MAC'd
// `sessions` table. config.json can desync from the log (a deleted file,
// a hand edit, an id reused after a discarded short session); that is
// accepted and honest (§2.7): a logged session simply renders unnamed,
// and the protected counts are never affected.
// Autostart (PR-6, docs/production-runtime/MIGRATION_PLAN.md §PR-6,
// PLATFORM_NOTES.md §3) is the first of the "future cosmetic prefs"
// this struct's doc comment above anticipated: which login-autostart
// mechanism `dexel autostart enable` last installed —
// "launchd" | "systemd-user" | "xdg-autostart" | "windows-run", or ""
// for none. It is written ONLY by `dexel autostart enable`/`disable`
// (app/cmd_autostart.go) — nothing else in this codebase may set it,
// which is the whole point of ADR-level explicit consent for autostart
// — and it is advisory, never authoritative: `dexel autostart status`
// always asks the OS directly (app/internal/autostart.Query), because
// this field can legitimately drift from reality (a hand-deleted unit
// file, a hand-edited config.json, a platform switch), the same
// "config.json can desync and that is accepted and honest" posture
// SessionNames' doc comment above already describes for its own field.
//
// AlwaysOnTop and ShowAwayTime (SET-1, docs/ui-spec.md §11) are the
// "future cosmetic prefs" this struct's doc comment anticipated, now
// real, and they are the FIRST fields here a running dexel writes on a
// user's say-so through the UI rather than through a CLI verb (SET_PREF,
// §6.2). Both are plain bools and both default to FALSE — which is the
// Go zero value, so an absent field, a fresh config.json and a
// hand-deleted key all mean the same honest thing and no defaulting code
// is needed anywhere:
//
//   - AlwaysOnTop: whether the desktop shell pins its window above other
//     windows (desktop/src-tauri/src/lib.rs reads it via
//     `dexel status --json`'s `prefs` block). It replaces a hardcoded
//     always_on_top(true) in that shell — ADR 0007 argued "a companion
//     the editor buries never gets seen", which is true, but forcing it
//     on every user was the wrong way to act on it, so the behaviour is
//     kept and the DEFAULT is inverted to off. A web-only user may still
//     set it; it simply persists and nothing consumes it, which is
//     harmless rather than a lie (nothing on screen claims otherwise).
//   - ShowAwayTime: whether the Activity modal DISPLAYS the away-time
//     rows. It is a PRESENTATION preference and nothing else: away time
//     is recorded exactly as before whatever this says (ADR 0010/0013 are
//     untouched, StatCounters.IdleSeconds still accrues every second and
//     still crosses the wire), because a counter that silently stopped
//     counting when hidden would make every derived total quietly wrong
//     and would be the dishonesty this project refuses. Hiding is the
//     client's rendering decision, driven by this field arriving over the
//     wire in ConfigView.
//
// Neither is protected state: like Name, they are the user's to hand-edit
// (the file is unsigned by design), and neither can influence the economy.
type ConfigData struct {
	Name         string            `json:"name"`
	SessionNames map[string]string `json:"sessionNames"`
	Autostart    string            `json:"autostart"`
	AlwaysOnTop  bool              `json:"alwaysOnTop"`
	ShowAwayTime bool              `json:"showAwayTime"`
}

// ConfigPath returns <StateDir>/config.json — the same directory as
// DefaultPath's state.db (paths.StateDir(), see its DefaultPath's doc
// comment for why this is byte-identical to the old hardcoded
// ~/.config/dexel/config.json on Linux), but a second, independent file.
func ConfigPath() (string, error) {
	dir, err := paths.StateDir()
	if err != nil {
		return "", fmt.Errorf("resolve state dir: %w", err)
	}
	return filepath.Join(dir, "config.json"), nil
}

// LoadConfig reads ConfigData at path. Unlike Load (state.json),
// config.json is deliberately hand-editable and unsigned, so both a
// missing file and a malformed one degrade to the zero value rather than
// an error or a quarantine — "a missing/malformed config.json yields
// defaults and never blocks startup" (SEC-1 design §5). A read failure
// other than "does not exist" (e.g. a permission error) is still
// surfaced as an error, the same way Load treats an unreadable
// state.json.
func LoadConfig(path string) (ConfigData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ConfigData{}, nil
		}
		return ConfigData{}, fmt.Errorf("read config file: %w", err)
	}
	var cfg ConfigData
	if err := json.Unmarshal(data, &cfg); err != nil {
		// config.json is deliberately hand-editable; a syntax mistake
		// there must never prevent the game from starting or need the
		// .corrupt/.invalid quarantine dance state.json uses — it simply
		// degrades to defaults.
		return ConfigData{}, nil
	}
	return cfg, nil
}

// SaveConfig writes cfg to path atomically, using the exact same
// tmp-write + fsync + rename + dir-fsync recipe Save uses for
// state.json (SEC-1 design §1.2: "a second, identical, already-written
// atomic write"). Unlike Save, SaveConfig never computes or writes a MAC:
// config.json is deliberately unsigned and user-editable by design.
func SaveConfig(path string, cfg ConfigData) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return writeFileAtomically(path, data)
}
