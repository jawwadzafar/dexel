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
type ConfigData struct {
	Name string `json:"name"`
}

// ConfigPath returns ~/.config/dexel/config.json — the same directory as
// DefaultPath's state.json, but a second, independent file.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "dexel", "config.json"), nil
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
