package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/engine"
	"github.com/jawwadzafar/dexel/app/internal/game"
)

// TestSaveLoadRoundTripVerifiesCleanly is SEC-1's basic exit criterion
// (docs/plan/SEC-1-design.md §8): a save written by this build's own Save
// verifies on Load with no tamper, no error, and ok=true — the ordinary,
// non-adversarial path must be completely unaffected by the new MAC.
func TestSaveLoadRoundTripVerifiesCleanly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	g := game.New()
	g.DevCash = 12345
	want := Snapshot(g)
	if want.Schema != CurrentSchema {
		t.Fatalf("Snapshot().Schema = %d, want CurrentSchema (%d)", want.Schema, CurrentSchema)
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := Load(path)
	if err != nil {
		t.Fatalf("Load of a clean, freshly-signed save should not error: %v", err)
	}
	if !ok {
		t.Fatal("Load reported no save for a valid signed save")
	}
	if got.Mac == "" {
		t.Error("Save should have written a non-empty Mac")
	}
	if got.DevCash != 12345 {
		t.Errorf("DevCash = %d, want 12345", got.DevCash)
	}
}

// TestTamperedDevCashIsDetectedAndFileRenamedToInvalid is SEC-1/DB-1's
// central anti-cheat exit criterion (docs/plan/SEC-1-design.md §4/§8,
// docs/plan/DB-1-design.md §3.3 row 1): a valid signed row, hand-edited
// at the sqlite level to inflate devCash (exactly the "opened state.db
// with sqlite3" scenario the whole feature exists to catch — the DB-era
// analogue of "opened state.json in a text editor"), must fail
// verification on Load, be renamed to "state.db.invalid" (never
// deleted), and yield ErrTampered rather than silently accepting the
// inflated value.
func TestTamperedDevCashIsDetectedAndFileRenamedToInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	g := game.New()
	g.DevCash = 100
	if err := Save(path, Snapshot(g)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Hand-edit the row at the sqlite level, the way a curious player
	// with sqlite3 would: bump devCash in the payload without touching
	// the (now stale) mac.
	schema, origPayload, origMac := rawReadStateRow(t, path)
	var d SaveData
	if err := json.Unmarshal(origPayload, &d); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if d.DevCash != 100 {
		t.Fatalf("precondition: DevCash = %d, want 100 before tampering", d.DevCash)
	}
	d.DevCash = 999999 // the cheat
	tamperedPayload := canonicalBody(d)
	rawUpdateStateRow(t, path, schema, tamperedPayload, origMac)

	got, ok, err := Load(path)
	if err == nil {
		t.Fatal("expected an error loading a tampered save, got nil")
	}
	if !errors.Is(err, ErrTampered) {
		t.Errorf("Load err = %v, want it to wrap ErrTampered", err)
	}
	if ok {
		t.Error("ok=true for a tampered save, want false — an inflated devCash must never be honored")
	}
	if !reflect.DeepEqual(got, SaveData{}) {
		t.Errorf("d = %+v, want the zero value (tampered data must never be applied)", got)
	}

	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("tampered file should have been moved away from path, not left in place")
	}
	if _, statErr := os.Stat(path + ".invalid"); statErr != nil {
		t.Errorf("expected %s.invalid to exist (tampered original must be preserved, never deleted): %v", path, statErr)
	}
}

// TestAddingAnOwnedItemIsAlsoDetected regression-guards that tamper
// detection is not devCash-specific: because the MAC covers the WHOLE
// struct (SEC-1 design §2.2's "drift-proof by construction"), granting an
// item by hand-editing ownedItems must be caught exactly the same way as
// inflating devCash.
func TestAddingAnOwnedItemIsAlsoDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	g := game.New()
	if err := Save(path, Snapshot(g)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	schema, origPayload, origMac := rawReadStateRow(t, path)
	var d SaveData
	if err := json.Unmarshal(origPayload, &d); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	d.OwnedItems = append(d.OwnedItems, "chair_racer_ember") // the cheat: grant an unowned item
	tamperedPayload := canonicalBody(d)
	rawUpdateStateRow(t, path, schema, tamperedPayload, origMac)

	_, ok, err := Load(path)
	if !errors.Is(err, ErrTampered) {
		t.Errorf("Load err = %v, want it to wrap ErrTampered", err)
	}
	if ok {
		t.Error("ok=true for a save with a hand-granted item, want false")
	}
	if _, statErr := os.Stat(path + ".invalid"); statErr != nil {
		t.Errorf("expected %s.invalid to exist: %v", path, statErr)
	}
}

// TestErrTamperedIsDistinctFromNotFoundAndFromEachOther is the
// regression-guard the design calls out explicitly (SEC-1 design §4,
// "Critical anti-cheat subtlety"): ErrTampered must never be
// indistinguishable from "no save" (a missing file), because a caller
// (main.go's loadOrImport) that only checked `ok` — rather than also
// checking `err` — would treat a tampered file exactly like "no save
// yet" and fall through to the legacy-import path, which grants items
// and refunds Dev Cash. Both cases report ok=false, but only one of them
// carries no error at all, and only the tampered one wraps ErrTampered
// (never ErrFutureSchema, and vice versa).
func TestErrTamperedIsDistinctFromNotFoundAndFromEachOther(t *testing.T) {
	dir := t.TempDir()

	// Case 1: no save at all.
	_, okMissing, errMissing := Load(filepath.Join(dir, "nope.db"))
	if okMissing {
		t.Error("ok=true for a missing file, want false")
	}
	if errMissing != nil {
		t.Errorf("err = %v for a missing file, want nil — this is the ONLY (ok=false, err=nil) case", errMissing)
	}

	// Case 2: a tampered, schema>=5 save.
	tamperedPath := filepath.Join(dir, "tampered.db")
	g := game.New()
	g.DevCash = 50
	if err := Save(tamperedPath, Snapshot(g)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	schema, origPayload, origMac := rawReadStateRow(t, tamperedPath)
	var d SaveData
	if err := json.Unmarshal(origPayload, &d); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	d.DevCash = 999999
	tamperedPayload := canonicalBody(d)
	rawUpdateStateRow(t, tamperedPath, schema, tamperedPayload, origMac)
	_, okTampered, errTampered := Load(tamperedPath)
	if okTampered {
		t.Error("ok=true for a tampered file, want false")
	}
	if errTampered == nil {
		t.Fatal("err = nil for a tampered file, want a non-nil error wrapping ErrTampered")
	}
	if !errors.Is(errTampered, ErrTampered) {
		t.Errorf("err = %v, want it to wrap ErrTampered", errTampered)
	}
	if errors.Is(errTampered, ErrFutureSchema) {
		t.Error("a MAC-mismatch error must not also satisfy errors.Is(_, ErrFutureSchema) — the two refusal categories are distinct")
	}

	// The anti-cheat property main.go's loadOrImport actually depends on:
	// a caller can tell these two (ok=false) cases apart by checking err,
	// and MUST, because only case 1 is safe to fall through to
	// legacy-import on.
	if (errMissing == nil) == (errTampered == nil) {
		t.Fatal("missing-file and tampered-file must be distinguishable by err — one is nil, the other is not")
	}
}

// TestDomainSeparationCausesVerificationFailure is SEC-1's domain-tag exit
// criterion (docs/plan/SEC-1-design.md §8): a tag computed under a
// different domain string must fail verifyMAC, proving macDomain actually
// participates in the MAC rather than being decorative.
func TestDomainSeparationCausesVerificationFailure(t *testing.T) {
	d := SaveData{Schema: CurrentSchema, DevCash: 42}
	d.Mac = computeMAC(d)
	if !verifyMAC(d) {
		t.Fatal("precondition: a freshly-computed MAC should verify")
	}

	// Recompute a MAC as if macDomain were something else entirely, by
	// hashing a manually-built preimage that swaps the domain tag but
	// otherwise matches macPreimage's construction (domain ‖ 0x00 ‖
	// compact JSON of d with Mac zeroed).
	dZeroed := d
	dZeroed.Mac = ""
	body, err := json.Marshal(dZeroed)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	otherDomain := "some-other-domain-v1"
	preimage := append([]byte(otherDomain), 0x00)
	preimage = append(preimage, body...)

	mac := hmac.New(sha256.New, integrityKey())
	mac.Write(preimage)
	d.Mac = hex.EncodeToString(mac.Sum(nil))
	if verifyMAC(d) {
		t.Error("a MAC computed under a different domain tag verified successfully — domain separation is not working")
	}
}

// TestUnitsDoneQuantizationMakesMacStableAcrossSaveLoadSave is SEC-1's
// float-stability exit criterion (docs/plan/SEC-1-design.md §2.3/§8): a
// sprint.unitsDone value with a long decimal expansion must still
// round-trip through Save -> Load -> Save with a MAC that verifies at
// every step, because quantizeUnits makes the value written to disk and
// the value fed to the MAC identical every time.
func TestUnitsDoneQuantizationMakesMacStableAcrossSaveLoadSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	// Sprint index 1's target is 75 (internal/game/sprint.go) — every value
	// here stays under that so RestoreSprint's [0, target] clamp (a
	// separate, deliberate load-validation rule, not the quantization
	// this test is about) never fires and masks what's being tested.
	for _, raw := range []float64{0, 12.3456789, 74.9999995, 25.0 / 3.0} {
		g := game.New()
		g.RestoreSprint(1, raw)

		want := Snapshot(g)
		if err := Save(path, want); err != nil {
			t.Fatalf("Save(%v): %v", raw, err)
		}
		got, ok, err := Load(path)
		if err != nil || !ok {
			t.Fatalf("Load after Save(%v): ok=%v err=%v", raw, ok, err)
		}
		wantQuantized := quantizeUnits(raw)
		if got.Sprint.UnitsDone != wantQuantized {
			t.Errorf("raw %v: UnitsDone after round-trip = %v, want quantized %v", raw, got.Sprint.UnitsDone, wantQuantized)
		}

		// Save again from the reloaded value (the "Load -> Save" half of
		// the exit criterion) and confirm it still verifies with no
		// further drift.
		if err := Save(path, got); err != nil {
			t.Fatalf("second Save(%v): %v", raw, err)
		}
		got2, ok2, err2 := Load(path)
		if err2 != nil || !ok2 {
			t.Fatalf("Load after second Save(%v): ok=%v err=%v", raw, ok2, err2)
		}
		if got2.Sprint.UnitsDone != wantQuantized {
			t.Errorf("raw %v: UnitsDone after second round-trip = %v, want %v (must not drift further)", raw, got2.Sprint.UnitsDone, wantQuantized)
		}
	}
}

// TestRichStateSaveLoadRoundTripHasNoFalsePositive is SEC-1's canonical-
// MAC-stability exit criterion (docs/plan/SEC-1-design.md §8): a rich
// state — owned items, owned tints, equipped slots, multi-day history,
// and a streak — must Snapshot -> Save -> Load with the MAC verifying,
// proving the preimage's determinism (sorted slices, sorted map keys,
// exact float round-trip) holds for more than a trivial empty struct.
func TestRichStateSaveLoadRoundTripHasNoFalsePositive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	g := game.New()
	g.DevCash = 5000
	g.XP = 2500
	if err := g.BuyItem("chair_racer_ember"); err != nil {
		t.Fatalf("BuyItem: %v", err)
	}
	if err := g.EquipItem("chair", "chair_racer_ember"); err != nil {
		t.Fatalf("EquipItem: %v", err)
	}
	g.RestoreSprint(3, 12.5)

	fakeNow := time.Date(2026, 6, 20, 9, 0, 0, 0, time.Local)
	g.SetClockForTest(func() time.Time { return fakeNow })
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, KeystrokeDelta: 100, MouseActive: true, WorkUnits: 30})
	g.RestoreHistory([]game.DayBucket{
		{Date: "2026-06-18", Counters: game.StatCounters{Keystrokes: 1000, ActiveSeconds: 3600}, CoinsEarned: 40},
		{Date: "2026-06-19", Counters: game.StatCounters{Keystrokes: 900, ActiveSeconds: 3000}, CoinsEarned: 35},
	})
	g.RestoreStreak(2, 5, "2026-06-19")

	want := Snapshot(g)
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, ok, err := Load(path)
	if err != nil {
		t.Fatalf("Load of a rich, freshly-signed save should verify with no error: %v", err)
	}
	if !ok {
		t.Fatal("Load reported no save for a valid rich signed save")
	}
}

// TestStrayStateJSONIsIgnoredNeverImportedNeverMints proves the unsigned
// mint vector is closed BY CONSTRUCTION now that the state.json import
// path is gone (public first release, no migration). A hand-written
// state.json claiming an absurd balance sits next to a missing state.db;
// there is no code that reads it, so Load sees "no save at all" and the
// economy starts fresh. Nothing is minted, no state.db is created from it,
// the stray file is left exactly where it was (untouched — nothing
// consults it), and a sibling config.json — the unsigned, hand-editable
// half of the split — is untouched too.
//
// This replaces the old B-1 unsigned-import refusal test: the refusal used
// to happen inside importJSON; with importJSON deleted there is nothing to
// refuse — the file is simply inert.
func TestStrayStateJSONIsIgnoredNeverImportedNeverMints(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	jsonPath := filepath.Join(dir, "state.json")
	configPath := filepath.Join(dir, "config.json")

	if err := SaveConfig(configPath, ConfigData{Name: "Pixel"}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	configBefore, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile (config before): %v", err)
	}

	// A hand-written save claiming a billion Dev Cash, in the old JSON
	// shape. Under a build with an import path this was the mint to close;
	// under this build it is just an unread file.
	raw := `{
		"schema": 1,
		"devCash": 999999999,
		"xp": 424242,
		"sprint": {"index": 5, "unitsDone": 0},
		"ownedItems": ["chair_exec_ember", "kb_split", "mouse_trackball", "wall_shelf", "buddy_cat"],
		"equipped": {}
	}`
	if err := os.WriteFile(jsonPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	d, ok, loadErr := Load(dbPath)
	if loadErr != nil {
		t.Fatalf("Load err = %v, want nil — a stray state.json is not an error, it is simply ignored", loadErr)
	}
	if ok {
		t.Error("ok = true, want false — a stray state.json must not count as a save")
	}
	if d.DevCash != 0 {
		t.Errorf("d.DevCash = %d, want 0 — nothing from an unread file may reach the caller", d.DevCash)
	}
	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Error("a state.db was created — nothing may be minted from a stray state.json")
	}
	stray, readErr := os.ReadFile(jsonPath)
	if readErr != nil {
		t.Fatalf("the stray state.json should be left in place (nothing reads it): %v", readErr)
	}
	if string(stray) != raw {
		t.Error("the stray state.json bytes changed — an ignored file must not be touched")
	}
	configAfter, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile (config after): %v", err)
	}
	if string(configAfter) != string(configBefore) {
		t.Error("config.json changed while ignoring a stray state.json — the name must survive")
	}
}

// TestASecondQuarantineDoesNotDestroyTheFirstOne is N-1's regression
// guard, on the state.db path: `quarantine` used to rename unconditionally
// to path+suffix, so a second tampered save silently overwrote the first
// one's evidence while the message still promised "original preserved
// untouched ... NOT deleted". The first quarantine keeps the plain
// documented name; the second gets a timestamped one and BOTH files
// survive.
func TestASecondQuarantineDoesNotDestroyTheFirstOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	for i, devCash := range []uint64{111, 222} {
		// Each round: write a fresh, validly-signed save, then hand-edit
		// its payload at the sqlite level so it fails verification and is
		// quarantined. (The first round's quarantine renames state.db away,
		// so the second Save creates a brand-new one.)
		g := game.New()
		g.DevCash = devCash
		if err := Save(path, Snapshot(g)); err != nil {
			t.Fatalf("Save #%d: %v", i, err)
		}
		schema, origPayload, origMac := rawReadStateRow(t, path)
		var d SaveData
		if err := json.Unmarshal(origPayload, &d); err != nil {
			t.Fatalf("Unmarshal #%d: %v", i, err)
		}
		d.DevCash = devCash + 1_000_000 // the cheat
		rawUpdateStateRow(t, path, schema, canonicalBody(d), origMac)
		if _, _, err := Load(path); !errors.Is(err, ErrTampered) {
			t.Fatalf("Load #%d err = %v, want ErrTampered", i, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var invalids []string
	for _, e := range entries {
		if strings.Contains(e.Name(), ".invalid") {
			invalids = append(invalids, e.Name())
		}
	}
	if len(invalids) != 2 {
		t.Fatalf("quarantined files = %v, want 2 distinct ones (the second tamper must not overwrite the first)", invalids)
	}
	// The FIRST quarantine keeps the plain .invalid name and still exists.
	if _, statErr := os.Stat(filepath.Join(dir, "state.db.invalid")); statErr != nil {
		t.Errorf("the FIRST quarantine must keep the plain .invalid name and survive: %v", statErr)
	}
}

// TestATamperedFileNeverPresentsAsNoSave is the regression-guard SEC-1
// design §4 calls out by name, kept after B-2 deleted the legacy-Rust
// import it was originally written to protect. Load's tampered-file
// return is (SaveData{}, false, non-nil error wrapping ErrTampered),
// never the (SaveData{}, false, nil) shape that means "no save at all" —
// the shape main.go's loadOrImport treats as a genuinely fresh install
// (and which used to additionally unlock an unbounded legacy re-grant).
// The distinction still matters with legacy gone: "fresh install" drives
// onboarding, and a tampered save must never masquerade as one.
func TestATamperedFileNeverPresentsAsNoSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	g := game.New()
	g.DevCash = 10
	if err := Save(path, Snapshot(g)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	schema, origPayload, origMac := rawReadStateRow(t, path)
	var d SaveData
	if err := json.Unmarshal(origPayload, &d); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	d.DevCash = 999999
	tamperedPayload := canonicalBody(d)
	rawUpdateStateRow(t, path, schema, tamperedPayload, origMac)

	// This mirrors main.go's loadOrImport: `data, ok, err := store.Load(...)`
	// followed by branching on ok, with err inspected for the error cases.
	// The legacy-import path in loadOrImport only runs when ok is false
	// AND err is nil. Prove that combination never occurs for a tampered
	// file.
	_, ok, loadErr := Load(path)
	looksLikeAFreshInstall := !ok && loadErr == nil
	if looksLikeAFreshInstall {
		t.Fatal("a tampered file must never present as (ok=false, err=nil) — that shape is reserved for \"no save at all\"")
	}
	if !errors.Is(loadErr, ErrTampered) {
		t.Errorf("loadErr = %v, want it to wrap ErrTampered", loadErr)
	}
}
