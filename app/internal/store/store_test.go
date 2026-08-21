package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jawwadzafar/dev-companion/app/internal/game"
)

func strp(s string) *string { return &s }

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	g := game.New()
	g.DevCash = 1000
	g.XP = 500
	if err := g.BuyItem("chair_racer"); err != nil {
		t.Fatalf("BuyItem: %v", err)
	}
	if err := g.BuyTint("chair_racer", "neon"); err != nil {
		t.Fatalf("BuyTint: %v", err)
	}
	if err := g.EquipItem("chair", "chair_racer", strp("neon")); err != nil {
		t.Fatalf("EquipItem: %v", err)
	}
	g.RestoreSprint(3, 12.5)

	want := Snapshot(g)
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !ok {
		t.Fatal("Load reported no save present after Save")
	}
	if got.DevCash != want.DevCash || got.XP != want.XP {
		t.Errorf("devCash/xp = (%d,%d), want (%d,%d)", got.DevCash, got.XP, want.DevCash, want.XP)
	}
	if got.Sprint != want.Sprint {
		t.Errorf("sprint = %+v, want %+v", got.Sprint, want.Sprint)
	}

	g2 := game.New()
	Apply(g2, got)
	if g2.DevCash != want.DevCash {
		t.Errorf("after Apply: DevCash = %d, want %d", g2.DevCash, want.DevCash)
	}
	if g2.SprintIndex() != 3 || g2.Progress != 12.5 {
		t.Errorf("after Apply: sprint = (%d, %v), want (3, 12.5)", g2.SprintIndex(), g2.Progress)
	}
	if !g2.OwnedItems["chair_racer"] {
		t.Error("after Apply: chair_racer not owned")
	}
	if !g2.IsTintOwned("chair_racer", "neon") {
		t.Error("after Apply: chair_racer:neon not owned")
	}
	ref := g2.Equipped["chair"]
	if ref.ItemID != "chair_racer" || ref.TintID == nil || *ref.TintID != "neon" {
		t.Errorf("after Apply: Equipped[chair] = %+v, want chair_racer/neon", ref)
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	d, ok, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ok {
		t.Error("ok=true for a missing file, want false")
	}
	if d.DevCash != 0 || len(d.OwnedItems) != 0 {
		t.Errorf("d = %+v, want zero value", d)
	}
}

func TestSaveIsAtomicViaDotTmpAndLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := Save(path, SaveData{DevCash: 5}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Errorf("dir contents = %v, want exactly [state.json] (no leftover .tmp file)", entries)
	}
}

func TestLoadMalformedFileIsRenamedToCorruptNotDeleted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, ok, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for a malformed save")
	}
	if ok {
		t.Error("ok=true for a malformed save, want false")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("original malformed file should have been renamed away, not left in place")
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Errorf("expected %s.corrupt to exist (never delete the bad file): %v", path, err)
	}
}

func TestApplyFallsBackToTierZeroForUnknownOrUnownedOrWrongSlotItems(t *testing.T) {
	g := game.New()
	Apply(g, SaveData{
		DevCash:    10,
		OwnedItems: []string{"totally_made_up_item"},
		Equipped: map[string]EquippedSave{
			"hoodie": {ItemID: "totally_made_up_item"},
			"chair":  {ItemID: "kb_mech"}, // wrong slot — kb_mech belongs to "keyboard"
			// mouse/beverage/plant/wall/buddy/keyboard: missing entirely
		},
	})
	if g.OwnedItems["totally_made_up_item"] {
		t.Error("unknown item id should not be marked owned")
	}
	if g.DevCash != 10 {
		t.Errorf("DevCash = %d, want 10 (should still apply even with bad entries)", g.DevCash)
	}
	for _, slot := range game.DefaultSlots() {
		ref, ok := g.Equipped[slot.ID]
		if !ok {
			t.Errorf("slot %q has no equipped entry after Apply", slot.ID)
			continue
		}
		if ref.ItemID != g.TierZeroItem(slot.ID) {
			t.Errorf("slot %q equipped %q, want tier-0 fallback %q", slot.ID, ref.ItemID, g.TierZeroItem(slot.ID))
		}
	}
}

func TestApplyClampsUnitsDoneAndSprintIndex(t *testing.T) {
	g := game.New()
	Apply(g, SaveData{Sprint: SprintSave{Index: 999, UnitsDone: -5}})
	if g.SprintIndex() < 0 || g.SprintIndex() >= 6 {
		t.Errorf("SprintIndex = %d, want clamped into [0,6)", g.SprintIndex())
	}
	if g.Progress != 0 {
		t.Errorf("Progress = %v, want 0 (negative clamped up)", g.Progress)
	}
}

// --- Legacy migration ---------------------------------------------------

func TestImportLegacyWorkedExampleFromTheSpec(t *testing.T) {
	// The exact fixture from docs/upgrade-design.md's "Worked example".
	legacy := &legacySaveData{
		Wallet: 340,
		XP:     1240,
		Level:  5,
		CurrentProject: &legacyCurrentProject{
			Index: 2, WorkDone: 41.0,
		},
		Upgrades: map[string]uint8{
			"chair": 2, "keyboard": 1, "monitor": 1, "wall": 2,
		},
	}
	d := ImportLegacy(legacy, game.DefaultCatalog())

	if d.DevCash != 490 {
		t.Errorf("devCash = %d, want 490 (340 + 150 monitor refund)", d.DevCash)
	}
	if d.XP != 1240 {
		t.Errorf("xp = %d, want 1240", d.XP)
	}
	if d.Sprint.Index != 2 || d.Sprint.UnitsDone != 41.0 {
		t.Errorf("sprint = %+v, want {2 41}", d.Sprint)
	}
	if !d.ImportedFromRust {
		t.Error("ImportedFromRust should be true")
	}
	if d.ImportedAt == "" {
		t.Error("ImportedAt should be set")
	}

	owned := map[string]bool{}
	for _, id := range d.OwnedItems {
		owned[id] = true
	}
	for _, want := range []string{
		"hoodie_classic", "chair_basic", "kb_membrane", "mouse_stock",
		"bev_mug", "plant_none", "wall_bare", "buddy_none", // 8 free defaults
		"chair_racer", "chair_exec", // chair tier 2
		"kb_mech",                   // keyboard tier 1
		"wall_poster", "wall_shelf", // wall tier 2
	} {
		if !owned[want] {
			t.Errorf("expected imported ownedItems to contain %q; got %v", want, d.OwnedItems)
		}
	}
	if len(d.OwnedItems) != 13 {
		t.Errorf("ownedItems = %v (%d items), want exactly 13", d.OwnedItems, len(d.OwnedItems))
	}

	wantEquip := map[string]string{
		"hoodie": "hoodie_classic", "chair": "chair_exec", "keyboard": "kb_mech",
		"mouse": "mouse_stock", "beverage": "bev_mug", "plant": "plant_none",
		"wall": "wall_shelf", "buddy": "buddy_none",
	}
	for slot, wantID := range wantEquip {
		got, ok := d.Equipped[slot]
		if !ok || got.ItemID != wantID {
			t.Errorf("Equipped[%q] = %+v, want itemId %q", slot, got, wantID)
		}
	}
	if d.Equipped["chair"].TintID == nil || *d.Equipped["chair"].TintID != "ember" {
		t.Errorf("Equipped[chair].TintID = %v, want ember (chair_exec's default)", d.Equipped["chair"].TintID)
	}
}

// TestImportLegacyNeverLosesCurrency is the invariant
// docs/upgrade-design.md demands explicitly: "an assertion that
// devCash_after >= devCash_before for every input." Table-driven over one
// row per old-track tier plus edge cases.
func TestImportLegacyNeverLosesCurrency(t *testing.T) {
	cases := []struct {
		name   string
		wallet uint64
		ups    map[string]uint8
	}{
		{"no upgrades at all", 0, nil},
		{"zero wallet, everything maxed", 0, map[string]uint8{
			"keyboard": 2, "mouse": 2, "chair": 2, "desk_decor": 2, "wall": 2, "pet": 1, "monitor": 2,
		}},
		{"nonzero wallet, everything maxed", 12345, map[string]uint8{
			"keyboard": 2, "mouse": 2, "chair": 2, "desk_decor": 2, "wall": 2, "pet": 1, "monitor": 2,
		}},
		{"monitor only, tier 1", 10, map[string]uint8{"monitor": 1}},
		{"monitor only, tier 2", 10, map[string]uint8{"monitor": 2}},
		{"desk_decor tier 2 (the one with a real refund)", 0, map[string]uint8{"desk_decor": 2}},
		{"unknown track ignored", 5, map[string]uint8{"underwater_basket_weaving": 9}},
		{"tier above known max clamped down", 5, map[string]uint8{"chair": 200}},
		{"tier zero is a no-op", 5, map[string]uint8{"chair": 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			legacy := &legacySaveData{Wallet: c.wallet, Upgrades: c.ups}
			d := ImportLegacy(legacy, game.DefaultCatalog())
			if d.DevCash < c.wallet {
				t.Errorf("devCash_after (%d) < devCash_before (%d)", d.DevCash, c.wallet)
			}
		})
	}
}

func TestImportLegacyMissingOrMalformedIsNotAnError(t *testing.T) {
	d, err := LoadLegacy(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("LoadLegacy: %v", err)
	}
	if d != nil {
		t.Errorf("d = %+v, want nil", d)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "save.json")
	if err := os.WriteFile(path, []byte("not json at all"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	d, err = LoadLegacy(path)
	if err != nil {
		t.Fatalf("LoadLegacy on malformed file should not error: %v", err)
	}
	if d != nil {
		t.Errorf("malformed legacy save should parse as nil (treated as absent), got %+v", d)
	}
}

func TestLoadLegacyParsesRealRustShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "save.json")
	raw := `{"wallet": 42, "xp": 100, "level": 2, "current_project": {"index": 1, "work_done": 5.5}, "upgrades": {"chair": 1}}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	d, err := LoadLegacy(path)
	if err != nil {
		t.Fatalf("LoadLegacy: %v", err)
	}
	if d == nil {
		t.Fatal("LoadLegacy returned nil for a valid file")
	}
	if d.Wallet != 42 || d.Upgrades["chair"] != 1 {
		t.Errorf("parsed legacy save = %+v", d)
	}
}

func TestLegacyPathMatchesPlatformConvention(t *testing.T) {
	p, err := LegacyPath()
	if err != nil {
		t.Fatalf("LegacyPath: %v", err)
	}
	if p == "" {
		t.Error("LegacyPath returned empty string")
	}
}
