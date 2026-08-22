package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/game"
)

// writeSignedJSONFixture builds SaveData d, signs it exactly the way the
// pre-DB-1 Save always did (computeMAC over the whole struct), and
// writes it to jsonPath as indented JSON — a faithful stand-in for a
// real pre-DB-1 state.json on disk, without needing the old file-based
// Save to still exist.
func writeSignedJSONFixture(t *testing.T, jsonPath string, d SaveData) SaveData {
	t.Helper()
	d.Mac = computeMAC(d)
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return d
}

// richFixture returns a non-trivial SaveData exercising every field
// group DB-1's migration must carry across intact.
func richFixture() SaveData {
	neon := "neon"
	return SaveData{
		Schema:     CurrentSchema,
		DevCash:    12345,
		XP:         6789,
		Sprint:     SprintSave{Index: 3, UnitsDone: 41.5},
		OwnedItems: []string{"chair_basic", "chair_racer"},
		OwnedTints: []string{"chair_racer:neon"},
		Equipped: map[string]EquippedSave{
			"chair": {ItemID: "chair_racer", TintID: &neon},
		},
		Stats: StatsSave{
			Date:       "2026-06-15",
			Today:      StatCountersSave{Keystrokes: 42, SprintsCompleted: 1},
			Lifetime:   StatCountersSave{Keystrokes: 9000, SprintsCompleted: 40},
			CoinsToday: CoinBreakdownSave{Keystrokes: 6},
			History: []DayBucketSave{
				{Date: "2026-06-14", Counters: StatCountersSave{Keystrokes: 100}, CoinsEarned: 12},
			},
			Streak: StreakSave{Current: 2, Longest: 5, LastActiveDate: "2026-06-14"},
		},
	}
}

// TestOneTimeImportFromStateJSONKeepsBalancesAndRenamesToImported is
// DB-1's central migration exit criterion (design §4.3/§6): a real,
// signed schema-5 state.json with a rich state migrates into state.db
// with every field intact, and the JSON is renamed (never deleted) to
// state.json.imported.
func TestOneTimeImportFromStateJSONKeepsBalancesAndRenamesToImported(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	jsonPath := filepath.Join(dir, "state.json")
	want := writeSignedJSONFixture(t, jsonPath, richFixture())

	d, ok, err := Load(dbPath)
	if err != nil {
		t.Fatalf("Load (import): %v", err)
	}
	if !ok {
		t.Fatal("Load reported no save for a valid state.json fixture")
	}
	if d.DevCash != want.DevCash || d.XP != want.XP {
		t.Errorf("devCash/xp = (%d,%d), want (%d,%d)", d.DevCash, d.XP, want.DevCash, want.XP)
	}
	if d.Sprint != want.Sprint {
		t.Errorf("sprint = %+v, want %+v", d.Sprint, want.Sprint)
	}
	if !reflect.DeepEqual(d.OwnedItems, want.OwnedItems) {
		t.Errorf("ownedItems = %v, want %v", d.OwnedItems, want.OwnedItems)
	}
	if !reflect.DeepEqual(d.OwnedTints, want.OwnedTints) {
		t.Errorf("ownedTints = %v, want %v", d.OwnedTints, want.OwnedTints)
	}
	if !reflect.DeepEqual(d.Equipped, want.Equipped) {
		t.Errorf("equipped = %+v, want %+v", d.Equipped, want.Equipped)
	}
	if !reflect.DeepEqual(d.Stats, want.Stats) {
		t.Errorf("stats = %+v, want %+v", d.Stats, want.Stats)
	}

	if _, statErr := os.Stat(dbPath); statErr != nil {
		t.Errorf("expected %s to exist after import: %v", dbPath, statErr)
	}
	if _, statErr := os.Stat(jsonPath); !os.IsNotExist(statErr) {
		t.Error("state.json should have been renamed away by the import, not left in place")
	}
	if _, statErr := os.Stat(jsonPath + ".imported"); statErr != nil {
		t.Errorf("expected %s.imported to exist: %v", jsonPath, statErr)
	}

	// A round trip through Apply confirms the imported values are really
	// usable, not just structurally present.
	g := game.New()
	Apply(g, d)
	if g.DevCash != want.DevCash {
		t.Errorf("after Apply: DevCash = %d, want %d", g.DevCash, want.DevCash)
	}
	if !g.OwnedItems["chair_racer"] {
		t.Error("after Apply: chair_racer should be owned")
	}
}

// TestImportedJSONMacCarriesOverWithoutRecomputation is design §3.1's
// carry-across claim proven directly against the stored row: for a
// schema-5 source, the DB's mac column must equal the JSON file's OWN
// tag byte-for-byte, not merely "a tag that happens to verify."
func TestImportedJSONMacCarriesOverWithoutRecomputation(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	jsonPath := filepath.Join(dir, "state.json")
	want := writeSignedJSONFixture(t, jsonPath, richFixture())

	d, ok, err := Load(dbPath)
	if err != nil || !ok {
		t.Fatalf("Load (import): ok=%v err=%v", ok, err)
	}
	if d.Mac != want.Mac {
		t.Errorf("imported d.Mac = %q, want the JSON file's own tag %q, carried across verbatim", d.Mac, want.Mac)
	}

	_, _, storedMac := rawReadStateRow(t, dbPath)
	if storedMac != want.Mac {
		t.Errorf("stored mac column = %q, want the JSON file's own tag %q", storedMac, want.Mac)
	}
}

// TestImportIsOneTimeAndTheDBWinsAfterwards is design §4.3's "the DB
// branch never consults jsonPath again" property: once state.db exists,
// a freshly-recreated state.json (even a legitimate-looking one) is
// simply ignored — Load never re-imports and never touches it.
func TestImportIsOneTimeAndTheDBWinsAfterwards(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	jsonPath := filepath.Join(dir, "state.json")
	original := writeSignedJSONFixture(t, jsonPath, richFixture())

	if _, ok, err := Load(dbPath); err != nil || !ok {
		t.Fatalf("first Load (import): ok=%v err=%v", ok, err)
	}

	// Recreate state.json with a DIFFERENT balance, exactly as a user
	// might if they dropped an old backup back into place.
	recreated := richFixture()
	recreated.DevCash = 999
	recreatedRaw := writeSignedJSONFixture(t, jsonPath, recreated)

	d, ok, err := Load(dbPath)
	if err != nil || !ok {
		t.Fatalf("second Load: ok=%v err=%v", ok, err)
	}
	if d.DevCash != original.DevCash {
		t.Errorf("second Load's DevCash = %d, want the ORIGINAL imported value %d — the DB must win, not the new JSON", d.DevCash, original.DevCash)
	}

	// The recreated JSON must be left completely alone: not renamed, not
	// deleted, not re-imported.
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("expected the recreated state.json to still exist untouched: %v", err)
	}
	wantRaw, err := json.MarshalIndent(recreatedRaw, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if string(raw) != string(wantRaw) {
		t.Error("the recreated state.json's content changed — the DB branch must never touch it")
	}
}

// TestImportOfSchema4JSONIsGrandfatheredThenStoredSignedInTheDB is
// design §4.3's grandfather-at-import proof, isolated from any
// subsequent Apply/Save round trip: an unsigned schema-4 fixture (no mac
// key at all, predating SEC-1) must be accepted by the import with no
// MAC check (loadJSON's existing grandfather behaviour, reused
// verbatim), and the row written into state.db must ALREADY be signed
// at CurrentSchema — DB-1 has no unsigned/grandfathered state of its own
// (design §3.2).
func TestImportOfSchema4JSONIsGrandfatheredThenStoredSignedInTheDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	jsonPath := filepath.Join(dir, "state.json")

	raw := `{
		"schema": 4,
		"devCash": 555,
		"xp": 111,
		"sprint": {"index": 0, "unitsDone": 0},
		"ownedItems": [],
		"ownedTints": [],
		"equipped": {}
	}`
	if err := os.WriteFile(jsonPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	d, ok, err := Load(dbPath)
	if err != nil || !ok {
		t.Fatalf("Load (import): ok=%v err=%v", ok, err)
	}
	if d.DevCash != 555 || d.XP != 111 {
		t.Errorf("devCash/xp = (%d,%d), want (555,111)", d.DevCash, d.XP)
	}
	if d.Schema != CurrentSchema {
		t.Errorf("d.Schema = %d, want CurrentSchema (%d)", d.Schema, CurrentSchema)
	}
	if d.Mac == "" {
		t.Error("d.Mac is empty, want a signed tag written at import time")
	}

	schemaCol, payload, macHex := rawReadStateRow(t, dbPath)
	if schemaCol != CurrentSchema {
		t.Errorf("stored schema column = %d, want CurrentSchema (%d)", schemaCol, CurrentSchema)
	}
	if !verifyMACBytes(payload, macHex) {
		t.Error("the row written by the grandfathered import does not verify against its own payload")
	}
}

// TestImportOfTamperedJSONReturnsErrTamperedAndCreatesNoDB is design
// §4.2/§4.3's "failure branches create no DB": a schema>=5 state.json
// whose MAC doesn't verify must propagate ErrTampered exactly as
// loadJSON always has, WITHOUT ever creating a state.db.
func TestImportOfTamperedJSONReturnsErrTamperedAndCreatesNoDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	jsonPath := filepath.Join(dir, "state.json")

	d := richFixture()
	d.Mac = computeMAC(d) // sign it correctly first...
	d.DevCash = 999999    // ...then tamper AFTER signing, so the tag is stale
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, ok, err := Load(dbPath)
	if !errors.Is(err, ErrTampered) {
		t.Errorf("Load err = %v, want it to wrap ErrTampered", err)
	}
	if ok {
		t.Error("ok=true for a tampered state.json, want false")
	}
	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Error("a tampered JSON import must create NO state.db")
	}
	if _, statErr := os.Stat(jsonPath + ".invalid"); statErr != nil {
		t.Errorf("expected %s.invalid to exist: %v", jsonPath, statErr)
	}
}

// TestImportOfFutureSchemaJSONReturnsErrFutureSchemaAndCreatesNoDB
// mirrors the tampered case above for a future-schema state.json.
func TestImportOfFutureSchemaJSONReturnsErrFutureSchemaAndCreatesNoDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	jsonPath := filepath.Join(dir, "state.json")

	raw := `{"schema": ` + itoa(CurrentSchema+1) + `, "devCash": 1}`
	if err := os.WriteFile(jsonPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, ok, err := Load(dbPath)
	if !errors.Is(err, ErrFutureSchema) {
		t.Errorf("Load err = %v, want it to wrap ErrFutureSchema", err)
	}
	if ok {
		t.Error("ok=true for a future-schema state.json, want false")
	}
	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Error("a future-schema JSON import must create NO state.db")
	}
	if _, statErr := os.Stat(jsonPath + ".future"); statErr != nil {
		t.Errorf("expected %s.future to exist: %v", jsonPath, statErr)
	}
}

// TestImportOfCorruptJSONLeavesCorruptQuarantineAndNoDB mirrors the same
// shape for a malformed (unparseable) state.json.
func TestImportOfCorruptJSONLeavesCorruptQuarantineAndNoDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	jsonPath := filepath.Join(dir, "state.json")
	if err := os.WriteFile(jsonPath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, ok, err := Load(dbPath)
	if err == nil {
		t.Fatal("expected an error for a corrupt state.json, got nil")
	}
	if errors.Is(err, ErrTampered) || errors.Is(err, ErrFutureSchema) {
		t.Errorf("Load err = %v, want a plain corrupt error, not a tamper/future sentinel", err)
	}
	if ok {
		t.Error("ok=true for a corrupt state.json, want false")
	}
	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Error("a corrupt JSON import must create NO state.db")
	}
	if _, statErr := os.Stat(jsonPath + ".corrupt"); statErr != nil {
		t.Errorf("expected %s.corrupt to exist: %v", jsonPath, statErr)
	}
}

// TestLegacyRustChainOnAJSONLessFreshMachineWritesTheDB is design §4.4:
// on a machine with neither state.db nor state.json, Load correctly
// reports "no save" — the only branch that may reach a caller's legacy
// Rust import — and the legacy chain's own Save now creates state.db
// rather than state.json, with legacy.go itself completely unmodified.
func TestLegacyRustChainOnAJSONLessFreshMachineWritesTheDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	legacyPath := filepath.Join(dir, "save.json")

	legacyRaw := `{"wallet": 340, "xp": 1240, "level": 5, "current_project": {"index": 2, "work_done": 41.0}, "upgrades": {"chair": 2}}`
	if err := os.WriteFile(legacyPath, []byte(legacyRaw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// This is the exact precondition a caller's loadOrImport checks
	// before running the legacy chain: Load must report "no save" (and
	// nothing else) with no state.db/state.json present at all.
	d, ok, err := Load(dbPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ok {
		t.Fatal("ok=true with no state.db/state.json present, want false — legacy import must be reachable")
	}
	if !reflect.DeepEqual(d, SaveData{}) {
		t.Errorf("d = %+v, want the zero value", d)
	}

	legacy, err := LoadLegacy(legacyPath)
	if err != nil {
		t.Fatalf("LoadLegacy: %v", err)
	}
	if legacy == nil {
		t.Fatal("LoadLegacy returned nil for a valid legacy save")
	}
	imported := ImportLegacy(legacy, game.DefaultCatalog())
	if err := Save(dbPath, imported); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, ok, err := Load(dbPath)
	if err != nil || !ok {
		t.Fatalf("Load after legacy import: ok=%v err=%v", ok, err)
	}
	if reloaded.DevCash != imported.DevCash {
		t.Errorf("reloaded.DevCash = %d, want %d", reloaded.DevCash, imported.DevCash)
	}
	if !reloaded.ImportedFromRust {
		t.Error("reloaded.ImportedFromRust should be true")
	}
	if _, statErr := os.Stat(dbPath); statErr != nil {
		t.Errorf("expected %s to exist: %v", dbPath, statErr)
	}
}

// TestConfigJSONIsUntouchedByEveryDBPath is design §5's config/state
// independence claim, exercised across every DB-1 code path that
// touches state.db/state.json in the same directory: import, a raw
// sqlite tamper, a future user_version, and a corrupt file must all
// leave a sibling config.json completely byte-identical.
func TestConfigJSONIsUntouchedByEveryDBPath(t *testing.T) {
	scenario := func(t *testing.T, run func(dir, dbPath, jsonPath string)) {
		t.Helper()
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.json")
		if err := SaveConfig(configPath, ConfigData{Name: "Widget"}); err != nil {
			t.Fatalf("SaveConfig: %v", err)
		}
		before, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("ReadFile (before): %v", err)
		}

		dbPath := filepath.Join(dir, "state.db")
		jsonPath := filepath.Join(dir, "state.json")
		run(dir, dbPath, jsonPath)

		after, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("ReadFile (after): %v", err)
		}
		if string(before) != string(after) {
			t.Error("config.json changed as a side effect of a state.db/state.json operation")
		}
		cfg, err := LoadConfig(configPath)
		if err != nil || cfg.Name != "Widget" {
			t.Errorf("LoadConfig after scenario = (%+v, %v), want ({Widget}, nil)", cfg, err)
		}
	}

	t.Run("import", func(t *testing.T) {
		scenario(t, func(_, dbPath, jsonPath string) {
			writeSignedJSONFixture(t, jsonPath, richFixture())
			if _, ok, err := Load(dbPath); err != nil || !ok {
				t.Fatalf("Load (import): ok=%v err=%v", ok, err)
			}
		})
	})

	t.Run("sqlite_tamper", func(t *testing.T) {
		scenario(t, func(_, dbPath, _ string) {
			saveRichDB(t, dbPath)
			rawExec(t, dbPath, `DELETE FROM state`)
			if _, ok, err := Load(dbPath); ok || !errors.Is(err, ErrTampered) {
				t.Fatalf("Load (tamper): ok=%v err=%v", ok, err)
			}
		})
	})

	t.Run("future_schema", func(t *testing.T) {
		scenario(t, func(_, dbPath, _ string) {
			saveRichDB(t, dbPath)
			rawExec(t, dbPath, `PRAGMA user_version = 99`)
			if _, ok, err := Load(dbPath); ok || !errors.Is(err, ErrFutureSchema) {
				t.Fatalf("Load (future): ok=%v err=%v", ok, err)
			}
		})
	})

	t.Run("corrupt", func(t *testing.T) {
		scenario(t, func(_, dbPath, _ string) {
			if err := os.WriteFile(dbPath, []byte("garbage, not a database"), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, ok, err := Load(dbPath); ok || err == nil {
				t.Fatalf("Load (corrupt): ok=%v err=%v", ok, err)
			}
		})
	})
}

// itoa avoids importing strconv solely for one fixture string in this
// file.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
