package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/game"
)

func strp(s string) *string { return &s }

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	g := game.New()
	g.DevCash = 1000
	g.XP = 500
	if err := g.BuyItem("chair_racer_ember"); err != nil {
		t.Fatalf("BuyItem: %v", err)
	}
	if err := g.EquipItem("chair", "chair_racer_ember"); err != nil {
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
	if !g2.OwnedItems["chair_racer_ember"] {
		t.Error("after Apply: chair_racer_ember not owned")
	}
	ref := g2.Equipped["chair"]
	if ref.ItemID != "chair_racer_ember" {
		t.Errorf("after Apply: Equipped[chair] = %+v, want chair_racer_ember", ref)
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	d, ok, err := Load(filepath.Join(t.TempDir(), "nope.db"))
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

// TestSaveIsAtomicViaDotTmpAndLeavesNoTempFile is DB-1's retarget of the
// pre-DB-1 write-temp-file-then-rename atomicity proof: state.db's write
// path is now one SQLite transaction (db.go's writeStateRow) rather than
// a hand-rolled tmp+rename, and journal_mode=DELETE (design §4.5) means
// the rollback journal SQLite creates during that transaction is removed
// again on commit — so the directory holds exactly one file at rest,
// same "no leftover temp artifact" property as before, achieved by a
// different mechanism.
func TestSaveIsAtomicViaDotTmpAndLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if err := Save(path, SaveData{DevCash: 5}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.db" {
		t.Errorf("dir contents = %v, want exactly [state.db] (no leftover .tmp/-journal/-wal/-shm file)", entries)
	}
}

// TestLoadMalformedFileIsRenamedToCorruptNotDeleted proves the corrupt
// gate (design §3.2 step 2) fires for a state.db that isn't a SQLite
// file at all — garbled bytes fail at openDB's very first pragma Exec
// (db.go's doc comment), which loadDB treats exactly like a failed
// PRAGMA quick_check: quarantine to ".corrupt", never delete.
func TestLoadMalformedFileIsRenamedToCorruptNotDeleted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if err := os.WriteFile(path, []byte("not a sqlite database, just garbage bytes"), 0o644); err != nil {
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

// TestLoadFutureSchemaIsBackedUpNeverDowngradedInPlace is S4: a save
// written by a NEWER build (Schema > CurrentSchema) — e.g. someone ran an
// older binary once after upgrading — must never be silently treated as
// "no save, start fresh" and then overwritten in place by this older
// build's next autosave. Load must instead preserve the original bytes
// untouched at "<path>.future" and return an error (ok=false, err!=nil,
// wrapping ErrFutureSchema) so the caller surfaces this as a load
// FAILURE rather than quietly discarding newer data.
func TestLoadFutureSchemaIsBackedUpNeverDowngradedInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	future := SaveData{Schema: CurrentSchema + 1, DevCash: 999999}
	if err := Save(path, future); err != nil {
		t.Fatalf("Save: %v", err)
	}
	rawBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile before Load: %v", err)
	}

	d, ok, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for a future-schema save, got nil")
	}
	if !errors.Is(err, ErrFutureSchema) {
		t.Errorf("Load err = %v, want it to wrap ErrFutureSchema", err)
	}
	if ok {
		t.Error("ok=true for a future-schema save, want false (must not be silently treated as usable)")
	}
	if !reflect.DeepEqual(d, SaveData{}) {
		t.Errorf("d = %+v, want the zero value (future-schema data must never be applied by an older build)", d)
	}

	// The original must be gone from path (so nothing downstream — an
	// autosave, a legacy-import Save — can accidentally write a
	// downgraded schema-1 file OVER the newer one)...
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("original future-schema file should have been moved away from path, not left in place")
	}
	// ...and preserved byte-for-byte at path+".future", never deleted.
	rawAfter, err := os.ReadFile(path + ".future")
	if err != nil {
		t.Fatalf("expected %s.future to exist (original must be preserved, never deleted): %v", path, err)
	}
	if string(rawAfter) != string(rawBefore) {
		t.Error(".future backup content differs from the original save — it must be preserved byte-for-byte")
	}

	// Simulate the caller's next step (main.go's loadOrImport falling
	// through to "start fresh" and eventually autosaving) and confirm it
	// cannot clobber the backup: writing a fresh, current-schema save to
	// the same path must never touch path+".future".
	if err := Save(path, SaveData{Schema: CurrentSchema, DevCash: 1}); err != nil {
		t.Fatalf("Save (fresh start): %v", err)
	}
	rawAfterFreshSave, err := os.ReadFile(path + ".future")
	if err != nil {
		t.Fatalf("expected %s.future to still exist after starting fresh: %v", path, err)
	}
	if string(rawAfterFreshSave) != string(rawBefore) {
		t.Error("starting fresh after a future-schema load must not alter the .future backup")
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
	if n := game.SprintPoolLen(); g.SprintIndex() < 0 || g.SprintIndex() >= n {
		t.Errorf("SprintIndex = %d, want clamped into [0,%d)", g.SprintIndex(), n)
	}
	if g.Progress != 0 {
		t.Errorf("Progress = %v, want 0 (negative clamped up)", g.Progress)
	}
}
