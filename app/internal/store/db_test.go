package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/game"
)

// TestDBSaveLoadRoundTrip is DB-1's basic exit criterion
// (docs/plan/DB-1-design.md §6/§7): Snapshot -> Save -> Load -> Apply on
// a rich state, through the real state.db container end to end.
func TestDBSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	g := game.New()
	g.DevCash = 4000
	g.XP = 900
	if err := g.BuyItem("chair_racer_ember"); err != nil {
		t.Fatalf("BuyItem: %v", err)
	}
	if err := g.EquipItem("chair", "chair_racer_ember"); err != nil {
		t.Fatalf("EquipItem: %v", err)
	}
	g.RestoreSprint(2, 30.5)

	want := Snapshot(g)
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !ok {
		t.Fatal("Load reported no save after Save")
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
	if !g2.OwnedItems["chair_racer_ember"] {
		t.Error("after Apply: chair_racer_ember not owned")
	}
}

// TestFreshInstallCreatesTheDBWithUserVersionAndOneSignedRow is the
// design's §2.3 DDL made concrete: a first-ever Save on a path with
// neither state.db nor state.json creates exactly one row (id=1),
// PRAGMA user_version mirroring CurrentSchema, and a MAC that verifies
// against the stored payload bytes.
func TestFreshInstallCreatesTheDBWithUserVersionAndOneSignedRow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	if err := Save(path, SaveData{Schema: CurrentSchema, DevCash: 42}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	db, err := openDB(path)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	var userVersion int
	if err := db.QueryRow("PRAGMA user_version").Scan(&userVersion); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if userVersion != CurrentSchema {
		t.Errorf("user_version = %d, want CurrentSchema (%d)", userVersion, CurrentSchema)
	}

	var count int
	if err := db.QueryRow("SELECT count(*) FROM state").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("row count = %d, want exactly 1", count)
	}

	var schemaCol int
	var payload []byte
	var macHex string
	if err := db.QueryRow("SELECT schema, payload, mac FROM state WHERE id = 1").Scan(&schemaCol, &payload, &macHex); err != nil {
		t.Fatalf("select row: %v", err)
	}
	if schemaCol != CurrentSchema {
		t.Errorf("schema column = %d, want CurrentSchema (%d)", schemaCol, CurrentSchema)
	}
	if !verifyMACBytes(payload, macHex) {
		t.Error("the freshly-written row's MAC does not verify against its own payload")
	}
}

// TestLoadMissingDBIsNotAnError is Load's "(SaveData{}, false, nil)" case
// with no state.db present at all — the ONLY genuine "no save" case
// (design §4.2), the one that reports (SaveData{}, false, nil).
func TestLoadMissingDBIsNotAnError(t *testing.T) {
	d, ok, err := Load(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ok {
		t.Error("ok=true for a missing state.db and state.json, want false")
	}
	if !reflect.DeepEqual(d, SaveData{}) {
		t.Errorf("d = %+v, want the zero value", d)
	}
}

// saveRichDB is a small helper shared by the tamper tests below: it
// Saves a non-trivial SaveData at path and returns it, so each test
// starts from a real, MAC-verified row rather than an all-zero one.
func saveRichDB(t *testing.T, path string) SaveData {
	t.Helper()
	g := game.New()
	g.DevCash = 100
	g.XP = 50
	want := Snapshot(g)
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return want
}

// TestTamperedPayloadIsDetectedAndDBRenamedToInvalid is the sqlite3-level
// anti-cheat exit criterion (design §3.3 row 1, and the literal in-game
// gate command in §7): `UPDATE state SET payload = ...` inflating
// devCash, done here via a raw SQL string replace against the stored
// BLOB (cast to TEXT) — the same command a curious player would type at
// a sqlite3 prompt — must fail verification and quarantine to .invalid.
func TestTamperedPayloadIsDetectedAndDBRenamedToInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	saveRichDB(t, path)

	rawExec(t, path, `UPDATE state SET payload = cast(replace(cast(payload as text), '"devCash":100', '"devCash":999999') as blob)`)

	_, ok, err := Load(path)
	if !errors.Is(err, ErrTampered) {
		t.Errorf("Load err = %v, want it to wrap ErrTampered", err)
	}
	if ok {
		t.Error("ok=true after a raw payload UPDATE, want false")
	}
	if _, statErr := os.Stat(path + ".invalid"); statErr != nil {
		t.Errorf("expected %s.invalid to exist: %v", path, statErr)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("tampered state.db should have been moved away, not left in place")
	}
}

// TestTamperedMacColumnIsDetected is design §3.3 row 2: overwriting only
// the mac column (without being able to recompute a valid one) must fail
// verification exactly like a payload edit does.
func TestTamperedMacColumnIsDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	saveRichDB(t, path)

	rawExec(t, path, `UPDATE state SET mac = 'deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef' WHERE id = 1`)

	_, ok, err := Load(path)
	if !errors.Is(err, ErrTampered) {
		t.Errorf("Load err = %v, want it to wrap ErrTampered", err)
	}
	if ok {
		t.Error("ok=true after a raw mac UPDATE, want false")
	}
	if _, statErr := os.Stat(path + ".invalid"); statErr != nil {
		t.Errorf("expected %s.invalid to exist: %v", path, statErr)
	}
}

// TestTamperedSchemaColumnIsDetected is design §3.3 row 4 / §3.2 step 7:
// the `schema` column is a mirror of the signed payload.schema, not an
// independent source of truth, so editing only the column (leaving the
// signed payload's own schema field untouched) must be caught by the
// cross-check, not silently accepted.
func TestTamperedSchemaColumnIsDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	saveRichDB(t, path)

	rawExec(t, path, `UPDATE state SET schema = 3 WHERE id = 1`)

	_, ok, err := Load(path)
	if !errors.Is(err, ErrTampered) {
		t.Errorf("Load err = %v, want it to wrap ErrTampered", err)
	}
	if ok {
		t.Error("ok=true after a raw schema-column UPDATE, want false")
	}
	if _, statErr := os.Stat(path + ".invalid"); statErr != nil {
		t.Errorf("expected %s.invalid to exist: %v", path, statErr)
	}
}

// TestTamperedUserVersionDownIsDetected is design §3.3 row 4's other
// half: PRAGMA user_version disagreeing with the signed payload.schema
// (here, edited DOWN rather than up — an up-edit is covered by
// TestFutureUserVersionIsRenamedToFutureNeverDowngradedInPlace) must also
// fail the cross-check. With CurrentSchema reset to 1, "down" is 0 — still
// a genuine disagreement with the payload's own schema, so the row's MAC
// verifies and the cross-check is what refuses it.
func TestTamperedUserVersionDownIsDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	saveRichDB(t, path)

	rawExec(t, path, `PRAGMA user_version = 0`)

	_, ok, err := Load(path)
	if !errors.Is(err, ErrTampered) {
		t.Errorf("Load err = %v, want it to wrap ErrTampered", err)
	}
	if ok {
		t.Error("ok=true after lowering user_version by hand, want false")
	}
	if _, statErr := os.Stat(path + ".invalid"); statErr != nil {
		t.Errorf("expected %s.invalid to exist: %v", path, statErr)
	}
}

// TestFutureUserVersionIsRenamedToFutureNeverDowngradedInPlace is design
// §3.3 row 5 / §3.2 step 3: `PRAGMA user_version = 99` must be refused as
// a future-schema DB — quarantined to .future, never loaded, never
// silently downgraded on the next Save.
func TestFutureUserVersionIsRenamedToFutureNeverDowngradedInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	saveRichDB(t, path)

	rawExec(t, path, `PRAGMA user_version = 99`)

	_, ok, err := Load(path)
	if !errors.Is(err, ErrFutureSchema) {
		t.Errorf("Load err = %v, want it to wrap ErrFutureSchema", err)
	}
	if ok {
		t.Error("ok=true after PRAGMA user_version = 99, want false")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("future-user_version state.db should have been moved away, not left in place")
	}
	if _, statErr := os.Stat(path + ".future"); statErr != nil {
		t.Errorf("expected %s.future to exist (never delete, never downgrade in place): %v", path, statErr)
	}

	// The next Save (an older-build's "start fresh" reaction, exactly as
	// the JSON-era TestLoadFutureSchemaIsBackedUpNeverDowngradedInPlace
	// proves) must not touch the .future backup.
	if err := Save(path, SaveData{Schema: CurrentSchema, DevCash: 1}); err != nil {
		t.Fatalf("Save (fresh start): %v", err)
	}
	if _, statErr := os.Stat(path + ".future"); statErr != nil {
		t.Errorf("expected %s.future to still exist after starting fresh: %v", path, statErr)
	}
}

// TestDeletedStateRowIsTamperNotNoSave is DB-1's single genuine
// improvement over the JSON design (§3.3 row 6, §3.4, ADR 0016 point 4):
// `DELETE FROM state` on an existing, otherwise-healthy state.db must
// report ErrTampered, NOT "no save" — an emptied table must never be
// indistinguishable from a machine that never had a save, because "no
// save" is the only shape allowed to reach a legacy Rust re-grant.
func TestDeletedStateRowIsTamperNotNoSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	saveRichDB(t, path)

	rawExec(t, path, `DELETE FROM state`)

	d, ok, err := Load(path)
	if err == nil {
		t.Fatal("expected an error after DELETE FROM state, got nil")
	}
	if !errors.Is(err, ErrTampered) {
		t.Errorf("Load err = %v, want it to wrap ErrTampered", err)
	}
	if ok {
		t.Error("ok=true after DELETE FROM state, want false")
	}
	if !reflect.DeepEqual(d, SaveData{}) {
		t.Errorf("d = %+v, want the zero value", d)
	}
	// Specifically: this must NOT be the (ok=false, err=nil) "no save"
	// shape a caller's loadOrImport uses to gate the legacy re-grant.
	wouldRunLegacyImport := !ok && err == nil
	if wouldRunLegacyImport {
		t.Fatal("an emptied state table must never present as (ok=false, err=nil) — that would let DELETE FROM state trigger a legacy re-grant")
	}
	if _, statErr := os.Stat(path + ".invalid"); statErr != nil {
		t.Errorf("expected %s.invalid to exist: %v", path, statErr)
	}
}

// TestExtraStateRowIsTamper is design §3.3 row 7: a second row (only
// reachable by first dropping the CHECK(id=1) constraint the STRICT
// table enforces — proven separately by the driver rejecting a direct
// INSERT with id=2 against the real DDL) must be caught by the row-count
// gate exactly like a deleted row is, just on the other side of 1.
func TestExtraStateRowIsTamper(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	want := saveRichDB(t, path)
	schema, payload, mac := rawReadStateRow(t, path)
	if schema != want.Schema {
		t.Fatalf("precondition: schema = %d, want %d", schema, want.Schema)
	}

	// A real INSERT of a second row against the actual CHECK(id=1)
	// constraint is rejected by the engine itself (verified interactively
	// against modernc.org/sqlite during implementation: "CHECK constraint
	// failed: id=1"). Simulating the only way a cheater could still get a
	// second row — drop and recreate the table without the constraint —
	// mirrors design §3.3 row 7 ("Recreates state as a VIEW..." / drops
	// the CHECK) rather than re-deriving that the constraint itself
	// works.
	rawExec(t, path, `DROP TABLE state`)
	rawExec(t, path, `CREATE TABLE state (id INTEGER PRIMARY KEY, schema INTEGER NOT NULL, payload BLOB NOT NULL, mac TEXT NOT NULL)`)
	rawExec(t, path, `INSERT INTO state (id, schema, payload, mac) VALUES (1, ?, ?, ?)`, schema, payload, mac)
	rawExec(t, path, `INSERT INTO state (id, schema, payload, mac) VALUES (2, ?, ?, ?)`, schema, payload, mac)

	_, ok, err := Load(path)
	if !errors.Is(err, ErrTampered) {
		t.Errorf("Load err = %v, want it to wrap ErrTampered", err)
	}
	if ok {
		t.Error("ok=true with two state rows, want false")
	}
	if _, statErr := os.Stat(path + ".invalid"); statErr != nil {
		t.Errorf("expected %s.invalid to exist: %v", path, statErr)
	}
}

// TestRealCheckConstraintRejectsASecondRow is a narrower unit check
// backing TestExtraStateRowIsTamper's comment: against the ACTUAL DDL
// (stateDDL), inserting a second row under the real CHECK(id=1)
// constraint fails at the engine, before Load is ever involved.
func TestRealCheckConstraintRejectsASecondRow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	saveRichDB(t, path)

	db, err := openDB(path)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	_, err = db.Exec(`INSERT INTO state (id, schema, payload, mac) VALUES (2, 5, x'00', 'a')`)
	if err == nil {
		t.Fatal("expected the real CHECK(id = 1) constraint to reject a second row, got nil error")
	}
}

// TestCorruptDBFileIsRenamedToCorrupt is design §3.2 step 2 / §3.3 row
// 10: a state.db that isn't a SQLite file at all — garbled bytes, as if
// the disk itself corrupted it — must be quarantined to .corrupt and
// never silently treated as "no save."
func TestCorruptDBFileIsRenamedToCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	garbage := make([]byte, 256)
	for i := range garbage {
		garbage[i] = byte(i % 256)
	}
	if err := os.WriteFile(path, garbage, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	d, ok, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for a corrupt state.db, got nil")
	}
	if errors.Is(err, ErrTampered) || errors.Is(err, ErrFutureSchema) {
		t.Errorf("Load err = %v, want a plain corrupt error (no sentinel), not ErrTampered/ErrFutureSchema", err)
	}
	if ok {
		t.Error("ok=true for a corrupt state.db, want false")
	}
	if !reflect.DeepEqual(d, SaveData{}) {
		t.Errorf("d = %+v, want the zero value", d)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("corrupt state.db should have been moved away, not left in place")
	}
	if _, statErr := os.Stat(path + ".corrupt"); statErr != nil {
		t.Errorf("expected %s.corrupt to exist (never delete the bad file): %v", path, statErr)
	}
}

// TestTamperedDBNeverPresentsAsNoSave: a tampered state.db (here, an
// emptied state table) must return ErrTampered, never the
// (ok=false, err=nil) shape reserved for "no save at all". main.go's
// loadOrImport keys onboarding — and, historically, a now-deleted legacy
// re-grant — off exactly that distinction, so collapsing a refused save
// into "fresh install" must remain impossible.
func TestTamperedDBNeverPresentsAsNoSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	saveRichDB(t, path)
	rawExec(t, path, `DELETE FROM state`)

	// Mirrors main.go's loadOrImport: only (ok=false, err=nil) is treated
	// as a genuinely fresh install.
	_, ok, loadErr := Load(path)
	looksLikeAFreshInstall := !ok && loadErr == nil
	if looksLikeAFreshInstall {
		t.Fatal("a tampered state.db must never present as (ok=false, err=nil) — that shape is reserved for \"no save at all\"")
	}
	if !errors.Is(loadErr, ErrTampered) {
		t.Errorf("loadErr = %v, want it to wrap ErrTampered", loadErr)
	}
}

// TestQuarantineMovesTheJournalSiblingAndClosesTheHandleFirst is a
// direct unit test of quarantine (db.go) — design §3.2's "Quarantine
// mechanics": a "-journal" sibling at rest (e.g. left by a crash mid-
// commit even under journal_mode=DELETE) must move alongside the main
// file, and the rename must never leave the original AND the
// destination both present.
func TestQuarantineMovesTheJournalSiblingAndClosesTheHandleFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if err := os.WriteFile(path, []byte("main file contents"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	journalPath := path + "-journal"
	if err := os.WriteFile(journalPath, []byte("stale journal contents"), 0o644); err != nil {
		t.Fatalf("WriteFile (journal): %v", err)
	}

	dest, err := quarantine(path, ".corrupt")
	if err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	if dest != path+".corrupt" {
		t.Fatalf("quarantine dest = %q, want %q (the FIRST quarantine keeps the plain name)", dest, path+".corrupt")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("original path should no longer exist after quarantine")
	}
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Error("original -journal sibling should no longer exist after quarantine")
	}
	mainRaw, err := os.ReadFile(path + ".corrupt")
	if err != nil {
		t.Fatalf("expected %s.corrupt to exist: %v", path, err)
	}
	if string(mainRaw) != "main file contents" {
		t.Error("quarantined main file content changed — quarantine must rename, never rewrite")
	}
	journalRaw, err := os.ReadFile(path + ".corrupt-journal")
	if err != nil {
		t.Fatalf("expected %s.corrupt-journal to exist: %v", path, err)
	}
	if string(journalRaw) != "stale journal contents" {
		t.Error("quarantined journal sibling content changed")
	}
}

// TestJournalModeAndSynchronousAreAsDesigned reads DB-1's fixed pragmas
// back rather than trusting the DSN silently applied them (design §4.5's
// explicit instruction: "Set the pragmas as explicit statements... and
// read them back... a DSN typo fails silently; a read-back assertion
// does not").
func TestJournalModeAndSynchronousAreAsDesigned(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if err := Save(path, SaveData{Schema: CurrentSchema}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	db, err := openDB(path)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if journalMode != "delete" {
		t.Errorf("journal_mode = %q, want %q (never WAL, design §4.5)", journalMode, "delete")
	}

	var synchronous int
	if err := db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatalf("read synchronous: %v", err)
	}
	const synchronousFull = 2 // SQLite's own encoding: 0=OFF, 1=NORMAL, 2=FULL
	if synchronous != synchronousFull {
		t.Errorf("synchronous = %d, want %d (FULL)", synchronous, synchronousFull)
	}

	var busyTimeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busyTimeout)
	}
}

// TestSaveLeavesNoWalOrShmOrJournalFilesAtRest is design §4.5's whole
// argument for DELETE over WAL made concrete: after Save, exactly one
// file sits in the directory — no -wal, no -shm, no -journal — because
// journal_mode=DELETE removes its rollback journal again on commit.
func TestSaveLeavesNoWalOrShmOrJournalFilesAtRest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if err := Save(path, SaveData{Schema: CurrentSchema, DevCash: 7}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// A second Save (the ongoing-autosave shape) must leave the directory
	// in exactly the same one-file state, not accumulate anything.
	if err := Save(path, SaveData{Schema: CurrentSchema, DevCash: 8}); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.db" {
		t.Errorf("dir contents = %v, want exactly [state.db] (no -wal/-shm/-journal at rest)", entries)
	}
}

// TestMacPreimageIsDomainTagThenNulThenCompactJSON pins the MAC preimage
// construction at the byte level, independent of this package's own
// helpers: canonicalBody(d) fed through macPreimage must equal EXACTLY
// domainTag ‖ 0x00 ‖ compact-json(d with Mac zeroed), reconstructed here
// by hand from encoding/json directly. This is what makes the DB's
// "verify against the stored payload bytes" (design §3.1) well-defined.
func TestMacPreimageIsDomainTagThenNulThenCompactJSON(t *testing.T) {
	d := SaveData{
		Schema:     CurrentSchema,
		DevCash:    12345,
		XP:         678,
		Sprint:     SprintSave{Index: 2, UnitsDone: 30.5},
		OwnedItems: []string{"chair_basic_slate", "chair_racer_ember"},
		Mac:        "this-must-be-zeroed-before-hashing",
	}

	got := macPreimage(canonicalBody(d))

	dZeroed := d
	dZeroed.Mac = ""
	independentBody, err := json.Marshal(dZeroed)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := append([]byte(macDomain), 0x00)
	want = append(want, independentBody...)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("macPreimage(canonicalBody(d)) differs from the independently-reconstructed preimage (domainTag ‖ 0x00 ‖ compact-json):\ngot:  %s\nwant: %s", got, want)
	}
}

// TestSaveIsDeterministicForLogicallyEqualState is design §6's byte-
// stability exit criterion: two Saves of the same logical SaveData must
// produce identical stored payload bytes and identical mac — no
// nondeterministic map iteration, no timestamp creeping in, nothing.
func TestSaveIsDeterministicForLogicallyEqualState(t *testing.T) {
	d := SaveData{
		Schema:     CurrentSchema,
		DevCash:    500,
		XP:         200,
		Sprint:     SprintSave{Index: 1, UnitsDone: 10.25},
		OwnedItems: []string{"chair_basic_slate", "chair_racer_ember"},
		Equipped: map[string]EquippedSave{
			"chair": {ItemID: "chair_racer_ember"},
		},
	}

	dir := t.TempDir()
	path1 := filepath.Join(dir, "a.db")
	path2 := filepath.Join(dir, "b.db")
	if err := Save(path1, d); err != nil {
		t.Fatalf("Save (1): %v", err)
	}
	if err := Save(path2, d); err != nil {
		t.Fatalf("Save (2): %v", err)
	}

	_, payload1, mac1 := rawReadStateRow(t, path1)
	_, payload2, mac2 := rawReadStateRow(t, path2)
	if !reflect.DeepEqual(payload1, payload2) {
		t.Errorf("payload bytes differ between two Saves of the same logical state:\n%s\nvs\n%s", payload1, payload2)
	}
	if mac1 != mac2 {
		t.Errorf("mac differs between two Saves of the same logical state: %q vs %q", mac1, mac2)
	}
}

// TestAZeroByteStateDBIsReportedAsCorruptNotTampered is N-2's regression
// test (docs/plan/REVIEW-2026-08-22.md). SQLite opens a zero-byte file
// happily — it is a valid empty database — and a process killed between
// openDB and the first commit leaves exactly that. The loader used to
// report it as "save integrity check failed: state table missing or
// unreadable" and quarantine it as .invalid: this build accusing an
// honest user of tampering over a torn first write.
//
// The handling is deliberately unchanged where it matters (fail closed,
// quarantine, never "no save" — a dropped table must not become a fresh
// start by another name); what changes is that a database with NO tables
// at all is named for what it is, and lands under .corrupt with the other
// unusable artifacts.
func TestAZeroByteStateDBIsReportedAsCorruptNotTampered(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{"zero-byte file", func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
		}},
		{"valid database, no tables", func(t *testing.T, path string) {
			db, err := openDB(path)
			if err != nil {
				t.Fatalf("openDB: %v", err)
			}
			if _, err := db.Exec("PRAGMA user_version = 0"); err != nil {
				t.Fatalf("Exec: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "state.db")
			tc.setup(t, path)

			_, ok, err := Load(path)
			if err == nil {
				t.Fatal("Load returned no error for an unusable state.db")
			}
			if ok {
				t.Error("ok = true for an unusable state.db")
			}
			if errors.Is(err, ErrTampered) {
				t.Errorf("err wraps ErrTampered: %v — a torn first write is not a cheat", err)
			}
			if !strings.Contains(err.Error(), "interrupted first write") {
				t.Errorf("err = %v, want it to name the real cause", err)
			}
			if _, statErr := os.Stat(path + ".corrupt"); statErr != nil {
				t.Errorf("expected %s.corrupt to exist: %v", path, statErr)
			}
			if _, statErr := os.Stat(path + ".invalid"); statErr == nil {
				t.Error("the file was quarantined as .invalid — that suffix means tampering")
			}
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Error("the unusable state.db was left in place; the next Save must start from a clean file")
			}
		})
	}
}

// TestADroppedStateTableIsStillTampering is the line N-2 must not blur: a
// database that HAS tables but is missing `state` was not interrupted
// mid-write — something removed it — and that stays ErrTampered with a
// .invalid quarantine.
func TestADroppedStateTableIsStillTampering(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	saveRichDB(t, path)
	rawExec(t, path, `CREATE TABLE IF NOT EXISTS decoy (id INTEGER PRIMARY KEY)`)
	rawExec(t, path, `DROP TABLE state`)

	_, ok, err := Load(path)
	if !errors.Is(err, ErrTampered) {
		t.Errorf("err = %v, want it to wrap ErrTampered", err)
	}
	if ok {
		t.Error("ok = true after the state table was dropped")
	}
	if _, statErr := os.Stat(path + ".invalid"); statErr != nil {
		t.Errorf("expected %s.invalid: %v", path, statErr)
	}
}
