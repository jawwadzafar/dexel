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

// reset_test.go pins the public-first-release contract (store.CurrentSchema,
// docs/game/persistence.md): there is exactly ONE supported schema, no
// migration/import path, and any save that is not a current-schema,
// correctly-signed save is refused and the economy starts fresh —
// current-schema-or-fresh, nothing else. The anti-cheat mechanism (the
// HMAC over the whole snapshot) is the very thing that enforces the reset:
// these tests rely on it, they do not weaken it.

// frozenCurrentBody is a FROZEN, byte-exact canonical body in the CURRENT
// (and only) save format (schema 1). It is a literal on purpose:
// regenerating it from today's structs would make it track whatever the
// code currently does, which is exactly what let PR-5's `pausedSeconds`
// upgrade bug ship. Field order is canonicalBody's own (encoding/json:
// struct declaration order, map keys sorted).
const frozenCurrentBody = `{"schema":1,"devCash":125,"xp":190,"sprint":{"index":3,"unitsDone":8.112979},` +
	`"ownedItems":["bev_mug","chair_basic_slate","hoodie_classic_indigo"],` +
	`"equipped":{"chair":{"itemId":"chair_basic_slate"},"hoodie":{"itemId":"hoodie_classic_indigo"}},` +
	`"stats":{"date":"2026-08-23",` +
	`"today":{"keystrokes":33,"mouseActiveSeconds":2,"activeSeconds":17,"idleSeconds":957,"sprintsCompleted":0,"focusSessions":0},` +
	`"lifetime":{"keystrokes":3492,"mouseActiveSeconds":436,"activeSeconds":1774,"idleSeconds":9294,"sprintsCompleted":1,"focusSessions":0},` +
	`"coinsToday":{"keystrokes":0,"mouse":0,"focusSessions":0},` +
	`"history":[{"date":"2026-08-22","counters":{"keystrokes":3459,"mouseActiveSeconds":434,"activeSeconds":1757,"idleSeconds":8337,"sprintsCompleted":1,"focusSessions":0},"coinsEarned":60,"longestFocusBlockSeconds":52}],` +
	`"streak":{"current":1,"longest":1,"lastActiveDate":"2026-08-22"}}}`

// frozenCurrentMac is the tag this build writes alongside frozenCurrentBody:
// HMAC-SHA256 over macDomain ‖ 0x00 ‖ frozenCurrentBody with the baked-in
// key. Frozen so a key or domain rotation fails HERE, in a test, rather than
// in a user's log where it would silently invalidate every save on disk.
const frozenCurrentMac = "9200231a341de57e6e1c0e1f7cfa24987ff56dc19cebd8f38e5d98cbfbaf5cd2"

// TestFrozenCurrentBodyPinsCanonicalFormAndMac is the standing
// upgrade-safety guard. It fails on two distinct mistakes:
//
//  1. canonicalBody re-serializing a parsed current save to DIFFERENT
//     bytes — a field added to SaveData/StatCountersSave without
//     `omitempty`/`omitzero` would inject a key the tag never covered.
//     There is no migration to hide behind: on the DB path the stored-byte
//     MAC would still verify, but any code that re-signs (a Save after a
//     load) would then drift, so the invariant is pinned regardless.
//  2. computeMACBytes producing a different tag for the frozen body — i.e.
//     the HMAC key or the domain tag changed. That rotation invalidates
//     every save already on disk and must be a conscious, tested decision.
func TestFrozenCurrentBodyPinsCanonicalFormAndMac(t *testing.T) {
	if CurrentSchema != 1 {
		t.Fatalf("CurrentSchema = %d, want 1 — this build is the public first release with a single baseline schema", CurrentSchema)
	}

	var d SaveData
	if err := json.Unmarshal([]byte(frozenCurrentBody), &d); err != nil {
		t.Fatalf("the frozen fixture is not valid JSON: %v", err)
	}
	if got := string(canonicalBody(d)); got != frozenCurrentBody {
		t.Errorf("canonicalBody changed the preimage of a current-format save.\n got: %s\nwant: %s\n\n"+
			"A field added to SaveData/StatCountersSave is being emitted into the MAC preimage. Give it\n"+
			"`omitempty` (or `omitzero`), the way Session/SessionLogHead/Paused already do.", got, frozenCurrentBody)
	}
	if mac := computeMACBytes([]byte(frozenCurrentBody)); mac != frozenCurrentMac {
		t.Errorf("computeMACBytes(frozenCurrentBody) = %s, want the frozen %s — the key or the domain tag changed.\n"+
			"That rotation invalidates every save already on disk; it needs a deliberate decision, not a silent constant edit.",
			mac, frozenCurrentMac)
	}
}

// TestFrozenCurrentSaveLoadsCleanly is the positive half: a state.db
// holding the frozen current-schema body and its matching tag loads with
// no error, no quarantine, and the economy intact. This is the "use it"
// branch of current-schema-or-fresh.
func TestFrozenCurrentSaveLoadsCleanly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	if err := Save(path, SaveData{Schema: CurrentSchema, DevCash: 1}); err != nil {
		t.Fatalf("Save (establish DB): %v", err)
	}
	rawUpdateStateRow(t, path, CurrentSchema, []byte(frozenCurrentBody), frozenCurrentMac)

	d, ok, err := Load(path)
	if err != nil {
		t.Fatalf("Load of a valid current-schema save must not error: %v", err)
	}
	if !ok {
		t.Fatal("ok = false for a valid current-schema save")
	}
	if d.DevCash != 125 || d.XP != 190 {
		t.Errorf("devCash/xp = %d/%d, want 125/190 — a valid current save loads intact", d.DevCash, d.XP)
	}
	if _, statErr := os.Stat(path + ".invalid"); statErr == nil {
		t.Error("a valid current save was quarantined to .invalid")
	}
}

// TestOldPreReleaseSaveInTheDBIsRefusedAndStartsFresh is the reset the
// public first release depends on: a genuine pre-release save — a HIGHER
// schema number, carrying now-removed fields (ownedTints, per-slot tintId,
// appSwitches, importedFromRust) and correctly signed over exactly its own
// stored bytes — must be REFUSED, not migrated, and the economy start
// fresh.
//
// This is why CurrentSchema was reset rather than kept: loadDB verifies the
// row's MAC against the STORED bytes, so removing struct fields would NOT
// by itself invalidate this old row's tag — it would still verify. The
// reset comes from the schema number: the old save's user_version is higher
// than this build's, so loadDB refuses it as a future schema, preserves it
// untouched at .future (never downgraded), and the next Save writes a clean
// schema-1 economy. Nothing from the old save reaches the game.
func TestOldPreReleaseSaveInTheDBIsRefusedAndStartsFresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	// A genuine old-format payload as a pre-release build (schema 8, the
	// last pre-public number) actually wrote it: it carries the removed
	// keys and an inflated economy, and its tag signs exactly these bytes.
	const oldBody = `{"schema":8,"devCash":999999,"xp":42,"sprint":{"index":0,"unitsDone":0},` +
		`"ownedItems":["chair_racer","hoodie_classic"],"ownedTints":["ember","indigo"],` +
		`"equipped":{"chair":{"itemId":"chair_racer","tintId":"ember"}},` +
		`"importedFromRust":true,"importedAt":"2026-01-01T00:00:00Z",` +
		`"stats":{"date":"2026-01-01",` +
		`"today":{"keystrokes":1,"mouseActiveSeconds":0,"activeSeconds":0,"idleSeconds":0,"sprintsCompleted":0,"focusSessions":0,"appSwitches":9},` +
		`"lifetime":{"keystrokes":1,"mouseActiveSeconds":0,"activeSeconds":0,"idleSeconds":0,"sprintsCompleted":0,"focusSessions":0,"appSwitches":9},` +
		`"coinsToday":{"keystrokes":0,"mouse":0,"focusSessions":0,"appSwitches":9},` +
		`"history":null,"streak":{"current":0,"longest":0,"lastActiveDate":""}}}`
	oldMac := computeMACBytes([]byte(oldBody))

	// Establish a schema-8 DB (Save sets user_version=8 and the schema
	// column to 8), then swap in the genuine old payload + its own tag —
	// exactly what a pre-release build wrote to its own row.
	if err := Save(path, SaveData{Schema: 8, DevCash: 1}); err != nil {
		t.Fatalf("Save (establish schema-8 DB): %v", err)
	}
	rawUpdateStateRow(t, path, 8, []byte(oldBody), oldMac)

	d, ok, err := Load(path)
	if err == nil {
		t.Fatal("Load accepted a pre-release higher-schema save — it must be refused, not migrated")
	}
	if !errors.Is(err, ErrFutureSchema) {
		t.Errorf("Load err = %v, want it to wrap ErrFutureSchema (a higher schema is refused, never downgraded)", err)
	}
	if ok {
		t.Error("ok = true for a pre-release save, want false")
	}
	if !reflect.DeepEqual(d, SaveData{}) {
		t.Errorf("d = %+v, want the zero value — nothing from the old save may leak through (no migration)", d)
	}
	// Preserved untouched at .future, never downgraded in place; the real
	// state.db is gone so the next Save starts fresh.
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("the old save should have been moved aside (to .future), not left in place")
	}
	if _, statErr := os.Stat(path + ".future"); statErr != nil {
		t.Errorf("expected %s.future to exist (old save preserved, never deleted): %v", path, statErr)
	}

	// The next Save writes a clean, current-schema economy that loads fine.
	g := game.New()
	if err := Save(path, Snapshot(g)); err != nil {
		t.Fatalf("Save (fresh start): %v", err)
	}
	fresh, ok, err := Load(path)
	if err != nil || !ok {
		t.Fatalf("Load after fresh start: ok=%v err=%v", ok, err)
	}
	if fresh.Schema != CurrentSchema || fresh.DevCash != g.DevCash {
		t.Errorf("fresh save = schema %d / devCash %d, want schema %d / devCash %d — a clean current-schema economy",
			fresh.Schema, fresh.DevCash, CurrentSchema, g.DevCash)
	}
}

// TestBelowBaselineSchemaSaveIsRefusedAsTampered exercises loadDB's
// older/foreign-schema branch directly (the d.Schema != CurrentSchema
// refusal): a consistent save at a schema BELOW the baseline — signed over
// its own bytes, with schema column and user_version all agreeing — is not
// a "future" save, so it takes the .invalid/ErrTampered path and the
// economy starts fresh. This is the lower half of "current-schema-or-fresh,
// nothing else".
func TestBelowBaselineSchemaSaveIsRefusedAsTampered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	// schema 0 is below the baseline of 1; keep the row internally
	// consistent (schema column, user_version and payload schema all 0,
	// MAC over the exact bytes) so it is the schema itself — not a
	// disagreement or a bad tag — that gets it refused.
	const belowBody = `{"schema":0,"devCash":777,"xp":0,"sprint":{"index":0,"unitsDone":0},"ownedItems":null,"equipped":null,` +
		`"stats":{"date":"","today":{"keystrokes":0,"mouseActiveSeconds":0,"activeSeconds":0,"idleSeconds":0,"sprintsCompleted":0,"focusSessions":0},` +
		`"lifetime":{"keystrokes":0,"mouseActiveSeconds":0,"activeSeconds":0,"idleSeconds":0,"sprintsCompleted":0,"focusSessions":0},` +
		`"coinsToday":{"keystrokes":0,"mouse":0,"focusSessions":0},"history":null,"streak":{"current":0,"longest":0,"lastActiveDate":""}}}`
	belowMac := computeMACBytes([]byte(belowBody))

	if err := Save(path, SaveData{Schema: CurrentSchema, DevCash: 1}); err != nil {
		t.Fatalf("Save (establish DB): %v", err)
	}
	rawUpdateStateRow(t, path, 0, []byte(belowBody), belowMac)
	// Keep user_version consistent with the payload/column (all 0), so this
	// is refused for being an unsupported schema, NOT for a disagreement.
	rawExec(t, path, `PRAGMA user_version = 0`)

	d, ok, err := Load(path)
	if !errors.Is(err, ErrTampered) {
		t.Errorf("Load err = %v, want it to wrap ErrTampered (a below-baseline schema is a foreign save)", err)
	}
	if ok {
		t.Error("ok = true for a below-baseline schema save, want false")
	}
	if d.DevCash != 0 {
		t.Errorf("d.DevCash = %d, want 0 — nothing from an unsupported-schema save may reach the caller", d.DevCash)
	}
	if _, statErr := os.Stat(path + ".invalid"); statErr != nil {
		t.Errorf("expected %s.invalid to exist: %v", path, statErr)
	}
}
