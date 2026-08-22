package store

import (
	"os"
	"path/filepath"
	"reflect"
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
	// ConfigData now carries a map (SessionNames, P2 §2.7), so it is no
	// longer comparable with == / != — reflect.DeepEqual is the
	// replacement, and treats two nil maps (the case here) as equal.
	if !reflect.DeepEqual(got, want) {
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
	if !reflect.DeepEqual(cfg, ConfigData{}) {
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
	if !reflect.DeepEqual(cfg, ConfigData{}) {
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

// TestConfigSessionNamesRoundTrip is P2's basic exit criterion for the
// second free-text field config.json carries (docs/plan/P2-design.md
// §2.7, ADR 0017 Decision 2): SaveConfig then LoadConfig returns the same
// id -> name map, byte-for-byte, alongside the pre-existing Name field.
func TestConfigSessionNamesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	want := ConfigData{
		Name:         "Widget",
		SessionNames: map[string]string{"1": "auth refactor", "2": "release cut"},
	}
	if err := SaveConfig(path, want); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadConfig = %+v, want %+v", got, want)
	}
}

// TestConfigSessionNamesSurviveAUserHandEditAndSessionNamesFromConfigDropsGarbageKeys
// is §2.7's "user-edit tolerance" contract from two angles: LoadConfig
// itself never blocks on or filters a hand-edited sessionNames map (that
// is not its job — it degrades a whole malformed FILE to defaults, but a
// well-formed JSON object with a garbage key is not malformed JSON), and
// SessionNamesFromConfig — the function that actually feeds
// Game.RestoreSessionNames — is the layer that skips a non-decimal key
// rather than blocking startup, mirroring LoadConfig's own "malformed
// degrades, never blocks" contract one level down.
func TestConfigSessionNamesSurviveAUserHandEditAndSessionNamesFromConfigDropsGarbageKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"name":"Widget","sessionNames":{"1":"ok","not-a-number":"garbage","2":"also ok"}}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig on a hand-edited sessionNames map should not error: %v", err)
	}
	if len(cfg.SessionNames) != 3 {
		t.Fatalf("cfg.SessionNames = %v, want all 3 raw keys to survive LoadConfig itself (filtering is SessionNamesFromConfig's job, not LoadConfig's)", cfg.SessionNames)
	}

	got := SessionNamesFromConfig(cfg)
	want := map[int]string{1: "ok", 2: "also ok"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SessionNamesFromConfig = %v, want %v (a non-decimal key must be skipped, never block startup)", got, want)
	}
}

// TestSessionNamesToConfigAndSessionNamesFromConfigRoundTrip proves the
// exported conversion pair GO-3's main.go uses on both ends of
// config.json's sessionNames map (SaveConfig's input, RestoreSessionNames'
// input) is a true inverse pair for well-formed ids.
func TestSessionNamesToConfigAndSessionNamesFromConfigRoundTrip(t *testing.T) {
	in := map[int]string{1: "auth refactor", 42: "release cut"}
	cfg := ConfigData{SessionNames: SessionNamesToConfig(in)}
	got := SessionNamesFromConfig(cfg)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("round trip = %v, want %v", got, in)
	}
}
