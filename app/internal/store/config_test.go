package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestConfigRoundTrip is config.json's basic exit criterion
// (docs/plan/SEC-1-design.md §8): SaveConfig then LoadConfig returns the
// same Name.
func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	want := ConfigData{Name: "Widget"}
	if err := SaveConfig(path, want); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got != want {
		t.Errorf("LoadConfig = %+v, want %+v", got, want)
	}
}

// TestLoadConfigMissingFileReturnsDefaultsNotError is SEC-1 design §5's
// "a missing/empty file -> defaults, never an error" rule.
func TestLoadConfigMissingFileReturnsDefaultsNotError(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("LoadConfig on a missing file should not error: %v", err)
	}
	if cfg != (ConfigData{}) {
		t.Errorf("cfg = %+v, want the zero value", cfg)
	}
}

// TestLoadConfigMalformedFileReturnsDefaultsNotError proves config.json's
// deliberate hand-editability: unlike state.json's parse failure (which
// quarantines the file to .corrupt and returns an error), a syntax
// mistake in config.json must never block startup — it degrades silently
// to defaults.
func TestLoadConfigMalformedFileReturnsDefaultsNotError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig on a malformed file should not error: %v", err)
	}
	if cfg != (ConfigData{}) {
		t.Errorf("cfg = %+v, want the zero value", cfg)
	}
	// Unlike state.json's Load, a malformed config.json is NOT
	// quarantined — it is simply ignored in favor of defaults.
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("malformed config.json should be left in place untouched, got stat error: %v", statErr)
	}
}

// TestSaveConfigIsAtomicViaDotTmpAndLeavesNoTempFile mirrors
// TestSaveIsAtomicViaDotTmpAndLeavesNoTempFile for config.json: SaveConfig
// must use the same tmp-write + rename recipe and leave no leftover
// ".tmp" file.
func TestSaveConfigIsAtomicViaDotTmpAndLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := SaveConfig(path, ConfigData{Name: "Blip"}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Errorf("dir contents = %v, want exactly [config.json] (no leftover .tmp file)", entries)
	}
}

// TestConfigPathIsASiblingOfStatePathNamedConfigJSON confirms ConfigPath
// and DefaultPath resolve to the same directory with different
// filenames — SEC-1 design §1.2's "two files" in "the same directory as
// DefaultPath's state.json."
func TestConfigPathIsASiblingOfStatePathNamedConfigJSON(t *testing.T) {
	statePath, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	configPath, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	if filepath.Dir(configPath) != filepath.Dir(statePath) {
		t.Errorf("ConfigPath dir = %q, DefaultPath dir = %q, want them equal", filepath.Dir(configPath), filepath.Dir(statePath))
	}
	if filepath.Base(configPath) != "config.json" {
		t.Errorf("ConfigPath basename = %q, want config.json", filepath.Base(configPath))
	}
	if filepath.Base(statePath) != "state.db" {
		t.Errorf("DefaultPath basename = %q, want state.db (DB-1, docs/plan/DB-1-design.md §5)", filepath.Base(statePath))
	}
}

// TestConfigNameEditDoesNotAffectStateIntegrity is SEC-1's explicit
// privacy/integrity-independence exit criterion (docs/plan/SEC-1-design.md
// §8): hand-editing config.json's name must have zero effect on
// state.json's MAC — the two files are independent by construction (a
// different file entirely), not merely "currently untested."
func TestConfigNameEditDoesNotAffectStateIntegrity(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.db")
	configPath := filepath.Join(dir, "config.json")

	if err := SaveConfig(configPath, ConfigData{Name: "Original Name"}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if err := Save(statePath, SaveData{Schema: CurrentSchema, DevCash: 1000}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Freely hand-edit config.json's name, exactly as the design intends
	// a user may.
	if err := SaveConfig(configPath, ConfigData{Name: "A Whole New Name!!"}); err != nil {
		t.Fatalf("SaveConfig (rename): %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Name != "A Whole New Name!!" {
		t.Errorf("cfg.Name = %q, want the freely-edited name", cfg.Name)
	}

	// state.json must still verify cleanly — the name edit touched a
	// completely different file and never entered the MAC preimage.
	d, ok, err := Load(statePath)
	if err != nil {
		t.Fatalf("Load(state.json) after editing config.json's name should not error: %v", err)
	}
	if !ok {
		t.Fatal("Load(state.json) reported no save after editing config.json's name")
	}
	if d.DevCash != 1000 {
		t.Errorf("DevCash = %d, want 1000 (unaffected by the config.json edit)", d.DevCash)
	}
}
