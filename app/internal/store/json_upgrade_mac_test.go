package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/game"
)

// currentBody is a FROZEN, byte-exact canonical body in the CURRENT save
// format (schema 8, post-STORE-2.0): the runtime tint system is gone, so
// there is no `ownedTints` key and no `tintId` inside any equipped entry,
// and equipped/owned items name colour-item ids (`chair_basic_slate`,
// `hoodie_classic_indigo`) rather than the pre-STORE-2.0 un-coloured style
// ids. It is a literal on purpose — regenerating it from today's structs
// would make it track whatever the code currently does, which is precisely
// the property that let PR-5's `pausedSeconds` upgrade bug ship.
//
// Field order is canonicalBody's own (encoding/json, struct declaration
// order, map keys sorted), because the JSON side of the MAC is verified
// against a RE-SERIALIZATION of the parsed struct, not against the file's
// bytes.
const currentBody = `{"schema":8,"devCash":125,"xp":190,"sprint":{"index":3,"unitsDone":8.112979},` +
	`"ownedItems":["bev_mug","chair_basic_slate","hoodie_classic_indigo"],` +
	`"equipped":{"chair":{"itemId":"chair_basic_slate"},"hoodie":{"itemId":"hoodie_classic_indigo"}},` +
	`"stats":{"date":"2026-08-23",` +
	`"today":{"keystrokes":33,"mouseActiveSeconds":2,"activeSeconds":17,"idleSeconds":957,"sprintsCompleted":0,"focusSessions":0,"appSwitches":0},` +
	`"lifetime":{"keystrokes":3492,"mouseActiveSeconds":436,"activeSeconds":1774,"idleSeconds":9294,"sprintsCompleted":1,"focusSessions":0,"appSwitches":0},` +
	`"coinsToday":{"keystrokes":0,"mouse":0,"focusSessions":0,"appSwitches":0},` +
	`"history":[{"date":"2026-08-22","counters":{"keystrokes":3459,"mouseActiveSeconds":434,"activeSeconds":1757,"idleSeconds":8337,"sprintsCompleted":1,"focusSessions":0,"appSwitches":0},"coinsEarned":60,"longestFocusBlockSeconds":52}],` +
	`"streak":{"current":1,"longest":1,"lastActiveDate":"2026-08-22"}}}`

// currentMac is the tag this build writes alongside currentBody: HMAC-SHA256
// over macDomain ‖ 0x00 ‖ currentBody with the baked-in key. Frozen for the
// same reason the body is — a key or domain rotation must fail HERE, not in
// a user's log.
const currentMac = "5bd423e2d3805f3b0bd9922945fe8c4b7942e0faba97a5031814b624ee643727"

// pre20Body is a FROZEN, byte-exact canonical body as a pre-STORE-2.0 build
// (schema 6, dexel v1.0.0) actually wrote it: it carries the now-removed
// `ownedTints` key, a `tintId` inside every equipped entry, and the old
// un-coloured style ids (`chair_basic`, `hoodie_classic`). Its MAC was
// computed over exactly these bytes.
//
// STORE-2.0 (owner-accepted OPTION A, "start fresh"): the tint fields no
// longer exist on SaveData, so json.Unmarshal DROPS `ownedTints`/`tintId`
// and canonicalBody re-serializes a DIFFERENT body (no tint keys, schema
// still 6, old ids). That body no longer matches pre20Mac, so this genuine
// old save is REFUSED as tampered and quarantined — never silently
// migrated, never half-applied. This is the anti-cheat guarantee working as
// designed: a foreign/stale preimage fails, and the fresh-economy path is
// entered cleanly. See TestPreSTORE20SaveIsRefusedAndQuarantined below.
const pre20Body = `{"schema":6,"devCash":125,"xp":190,"sprint":{"index":3,"unitsDone":8.112979},` +
	`"ownedItems":["bev_mug","chair_basic","hoodie_classic"],"ownedTints":[],` +
	`"equipped":{"chair":{"itemId":"chair_basic","tintId":"slate"},"hoodie":{"itemId":"hoodie_classic","tintId":"indigo"}},` +
	`"stats":{"date":"2026-08-23",` +
	`"today":{"keystrokes":33,"mouseActiveSeconds":2,"activeSeconds":17,"idleSeconds":957,"sprintsCompleted":0,"focusSessions":0,"appSwitches":0},` +
	`"lifetime":{"keystrokes":3492,"mouseActiveSeconds":436,"activeSeconds":1774,"idleSeconds":9294,"sprintsCompleted":1,"focusSessions":0,"appSwitches":0},` +
	`"coinsToday":{"keystrokes":0,"mouse":0,"focusSessions":0,"appSwitches":0},` +
	`"history":[{"date":"2026-08-22","counters":{"keystrokes":3459,"mouseActiveSeconds":434,"activeSeconds":1757,"idleSeconds":8337,"sprintsCompleted":1,"focusSessions":0,"appSwitches":0},"coinsEarned":60,"longestFocusBlockSeconds":52}],` +
	`"streak":{"current":1,"longest":1,"lastActiveDate":"2026-08-22"}}}`

// pre20Mac is the tag that pre-STORE-2.0 build wrote alongside pre20Body:
// HMAC-SHA256 over macDomain ‖ 0x00 ‖ pre20Body (tint keys and all) with the
// baked-in key. It verifies against the ORIGINAL bytes, and deliberately
// NOT against the tint-stripped re-serialization this build produces —
// which is the whole point of the refusal test below.
const pre20Mac = "1eeb106255951b922ca39bcb393b7f97c9d84b17603134779936d17150200795"

// TestCurrentFormatStateJSONImportsUnderItsMac is the upgrade-safety
// regression test, retargeted to the current save format. It is the JSON
// import side specifically: importJSON's documented invariant is that
// canonicalBody(d) reproduces byte-identical preimage bytes for a parsed
// save, so its own tag carries across verbatim and the save is accepted.
// The bug this guards against (PR-5's `pausedSeconds` without `omitempty`)
// was: a field added AFTER a save was written got emitted into
// canonicalBody, changed the MAC preimage, and every existing state.json
// failed verifyMAC and was quarantined as tampered — a real v1.0.0 save
// (devCash 125) refused on a real machine, the player silently reset to a
// fresh economy at 0. Any FUTURE field added without `omitempty`/`omitzero`
// re-breaks this the same way, and this test says so.
//
// The DB side never had the bug and could not catch it: loadDB verifies
// verifyMACBytes(payload, macHex) against the bytes STORED in the row, so
// no re-serialization happens and no later field can disturb it. importJSON
// is the one path whose correctness depends on the omitempty discipline.
func TestCurrentFormatStateJSONImportsUnderItsMac(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	jsonPath := jsonImportPath(dbPath)

	if strings.Contains(currentBody, "ownedTints") || strings.Contains(currentBody, "tintId") {
		t.Fatalf("the current fixture still carries a tint key — the tint system was removed in STORE-2.0: %s", currentBody)
	}

	// Written INDENTED, exactly as a real state.json is on disk, to prove
	// the MAC never sees file formatting — only the canonical
	// re-serialization.
	signed := currentBody[:len(currentBody)-1] + `,"mac":"` + currentMac + `"}`
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(signed), "", "  "); err != nil {
		t.Fatalf("indent the fixture: %v", err)
	}
	if err := os.WriteFile(jsonPath, pretty.Bytes(), 0o600); err != nil {
		t.Fatalf("write %s: %v", jsonPath, err)
	}

	d, ok, err := Load(dbPath)
	if err != nil {
		t.Fatalf("Load refused a valid current-format state.json: %v\n"+
			"This is the upgrade bug: every field added to SaveData/StatCountersSave after a\n"+
			"save was written must be `omitempty`/`omitzero`, or canonicalBody re-serializes\n"+
			"keys the original MAC never covered and every existing player loses their save.", err)
	}
	if !ok {
		t.Fatal("ok = false for a valid current-format state.json — it must import, not be treated as no-save")
	}

	// The economy imported intact.
	if d.DevCash != 125 || d.XP != 190 {
		t.Errorf("devCash/xp = %d/%d, want 125/190 — the import must not lose progress", d.DevCash, d.XP)
	}
	if d.Stats.Lifetime.Keystrokes != 3492 || d.Stats.Today.IdleSeconds != 957 {
		t.Errorf("counters disturbed by the import: lifetime.keystrokes=%d today.idleSeconds=%d",
			d.Stats.Lifetime.Keystrokes, d.Stats.Today.IdleSeconds)
	}
	if len(d.Stats.History) != 1 || d.Stats.History[0].CoinsEarned != 60 {
		t.Errorf("history lost or altered by the import: %+v", d.Stats.History)
	}

	// Nothing was quarantined: a healthy save must never leave a
	// .invalid behind, because that file is what a user then has to be
	// told about.
	if _, err := os.Stat(jsonPath + ".invalid"); err == nil {
		t.Error("a valid current-format save was quarantined to state.json.invalid")
	}
}

// TestCanonicalBodyReproducesACurrentPayloadByteForByte pins importJSON's
// documented invariant directly, one layer below the Load test above:
// parsing a current-format payload and re-serializing it must return the
// SAME BYTES. Any future field on SaveData or StatCountersSave that lacks
// `omitempty`/`omitzero` breaks this immediately and says so, instead of
// the breakage surfacing as "an old player's save is tampered". This is
// the standing "any field added FROM NOW ON must be omitempty" guard.
func TestCanonicalBodyReproducesACurrentPayloadByteForByte(t *testing.T) {
	var d SaveData
	if err := json.Unmarshal([]byte(currentBody), &d); err != nil {
		t.Fatalf("the frozen fixture is not valid JSON: %v", err)
	}
	got := canonicalBody(d)
	if string(got) != currentBody {
		t.Errorf("canonicalBody changed the preimage of a current-format save.\n"+
			" got: %s\nwant: %s\n\n"+
			"A field added since that save was written is being emitted into the MAC preimage.\n"+
			"Give it `omitempty` (or `omitzero` for a pointer/struct), the way\n"+
			"Session/SessionLogHead/Paused already do — otherwise every existing state.json\n"+
			"fails its MAC on upgrade and players are silently reset to a fresh economy.",
			got, currentBody)
	}
	// The frozen tag is self-consistent with the frozen body, so a
	// deliberate key or domain rotation fails HERE — with a note that it
	// invalidates every save in the wild — rather than in a user's log.
	if mac := computeMACBytes([]byte(currentBody)); mac != currentMac {
		t.Errorf("computeMACBytes(currentBody) = %s, want the frozen %s — the key or the domain tag changed.\n"+
			"That rotation invalidates every save already on disk; it needs a migration, not a new constant.",
			mac, currentMac)
	}
}

// TestPreSTORE20SaveIsRefusedAndQuarantined is the anti-cheat / OPTION-A
// guarantee for the format change STORE-2.0 made. A genuine, correctly
// signed pre-STORE-2.0 save (schema 6, carrying `ownedTints`, per-slot
// `tintId`, and the old un-coloured item ids) must NOT be silently migrated
// or half-applied: because SaveData no longer has the tint fields,
// json.Unmarshal drops them and canonicalBody re-serializes a body that no
// longer matches the file's original MAC, so verifyMAC fails and loadJSON
// treats the file exactly like any other tampered/foreign save — the
// owner-accepted "start fresh" outcome (OPTION A). The refusal is the same
// mechanism that stops a hand-edited or copied-in save; we are relying on
// it, not weakening it.
//
// Concretely this proves: Load returns ErrTampered (not "no save", not
// ErrFutureSchema), ok=false, a zero SaveData; the original is preserved at
// state.json.invalid (never deleted, never overwritten); NO state.db is
// created (so the next Save starts a fresh economy); and pre20Mac genuinely
// verified the ORIGINAL tint-carrying bytes, so this is a real old save
// being refused, not a fixture that was never valid.
func TestPreSTORE20SaveIsRefusedAndQuarantined(t *testing.T) {
	// Sanity: the fixture really is a pre-STORE-2.0 payload, and its tag
	// really signed those original tint-carrying bytes.
	if !strings.Contains(pre20Body, "ownedTints") || !strings.Contains(pre20Body, "tintId") {
		t.Fatalf("pre20Body is meant to carry the removed tint keys but does not: %s", pre20Body)
	}
	if mac := computeMACBytes([]byte(pre20Body)); mac != pre20Mac {
		t.Fatalf("pre20Mac does not sign pre20Body's original bytes (%s != %s) — the fixture is not a genuine old save",
			mac, pre20Mac)
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	jsonPath := jsonImportPath(dbPath)

	// Written exactly as the old build wrote it: original bytes + its own
	// (now-stale, against the new format) tag.
	signed := pre20Body[:len(pre20Body)-1] + `,"mac":"` + pre20Mac + `"}`
	if err := os.WriteFile(jsonPath, []byte(signed), 0o600); err != nil {
		t.Fatalf("write %s: %v", jsonPath, err)
	}

	d, ok, err := Load(dbPath)
	if err == nil {
		t.Fatal("Load accepted a pre-STORE-2.0 save whose MAC no longer covers the tint-stripped body — the format change must refuse it, not silently migrate it")
	}
	if !errors.Is(err, ErrTampered) {
		t.Errorf("Load err = %v, want it to wrap ErrTampered (a stale/foreign preimage is refused exactly like a tampered save)", err)
	}
	if ok {
		t.Error("ok=true for a pre-STORE-2.0 save, want false — it must be refused, not half-applied")
	}
	if !reflect.DeepEqual(d, SaveData{}) {
		t.Errorf("d = %+v, want the zero value (nothing from the old save may leak through)", d)
	}

	// OPTION A: original preserved at .invalid, never deleted/overwritten;
	// no DB created, so the very next Save starts a fresh economy.
	if _, statErr := os.Stat(jsonPath); !os.IsNotExist(statErr) {
		t.Error("the refused save should have been renamed away from jsonPath (to .invalid), not left in place")
	}
	if _, statErr := os.Stat(jsonPath + ".invalid"); statErr != nil {
		t.Errorf("expected %s.invalid to exist (original must be preserved, never deleted): %v", jsonPath, statErr)
	}
	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Error("a refused import must create NO state.db — the fresh economy starts on the next Save")
	}
}

// TestPreSTORE20SaveInTheDBLoadsWithCosmeticsResetAndEconomyIntact pins the
// OTHER, and far more common, STORE-2.0 upgrade path honestly: an existing
// v1.0.0 player's save lives in state.db (DB-1 migrated everyone off
// state.json long ago), so on upgrade it is read by loadDB — which verifies
// verifyMACBytes(payload, macHex) against the bytes STORED in the row, NOT a
// re-serialization. The tag was computed over exactly those stored bytes
// (tint keys and all), and the HMAC key/domain are unchanged by STORE-2.0,
// so the MAC still verifies and the row loads. json.Unmarshal then silently
// drops the removed `ownedTints`/`tintId`, and Apply drops the old
// un-coloured item ids that are no longer in the catalog, falling each slot
// back to its tier-0 default.
//
// So the ACTUAL behaviour is NOT "refused -> fresh economy" here (that is
// only the state.json import path, TestPreSTORE20SaveIsRefusedAndQuarantined
// above, which re-serializes and so trips the MAC): it is graceful
// degradation — DevCash / XP / stats / streak / history survive intact,
// unchanged item ids (bev_mug) stay owned, and only the tint-era cosmetics
// reset to defaults. This is coherent and safe: nothing is half-applied or
// corrupted, no old item is silently kept, and the save is never rewritten
// with anything it did not legitimately own. It is documented here so the
// two paths' divergent outcomes are pinned side by side rather than
// discovered in the field.
func TestPreSTORE20SaveInTheDBLoadsWithCosmeticsResetAndEconomyIntact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	// Stand up a real schema-6 DB the way a pre-STORE-2.0 build would have:
	// Save at schema 6 sets user_version=6 and the schema column to 6, so
	// after the row swap below payload-schema, column and user_version all
	// agree (loadDB treats any disagreement as tampering).
	if err := Save(path, SaveData{Schema: 6, DevCash: 1}); err != nil {
		t.Fatalf("Save (establish schema-6 DB): %v", err)
	}
	// Swap in the genuine pre-STORE-2.0 payload bytes (carrying ownedTints,
	// tintId and the old un-coloured ids) with the tag that signed exactly
	// those bytes — precisely what a schema-6 build wrote to its own row.
	rawUpdateStateRow(t, path, 6, []byte(pre20Body), pre20Mac)

	d, ok, err := Load(path)
	if err != nil {
		t.Fatalf("loadDB refused a genuine pre-STORE-2.0 DB row (its MAC signs the STORED bytes, key/domain unchanged): %v", err)
	}
	if !ok {
		t.Fatal("ok=false for a valid pre-STORE-2.0 DB row — the stored-bytes MAC must still verify")
	}

	// Economy intact — the tint fields dropping out of the parsed struct
	// does not touch cash/xp/counters.
	if d.DevCash != 125 || d.XP != 190 {
		t.Errorf("devCash/xp = %d/%d, want 125/190 — the DB-path load must preserve the economy", d.DevCash, d.XP)
	}
	if d.Stats.Lifetime.Keystrokes != 3492 || len(d.Stats.History) != 1 {
		t.Errorf("counters/history disturbed: lifetime.keystrokes=%d history=%d", d.Stats.Lifetime.Keystrokes, len(d.Stats.History))
	}

	g := game.New()
	Apply(g, d)
	// Cash/xp carried through Apply.
	if g.DevCash != 125 || g.XP != 190 {
		t.Errorf("after Apply: devCash/xp = %d/%d, want 125/190", g.DevCash, g.XP)
	}
	// An unchanged id (bev_mug) stays owned; the old un-coloured ids are
	// dropped (not in the STORE-2.0 catalog).
	if !g.OwnedItems["bev_mug"] {
		t.Error("after Apply: bev_mug (an unchanged id) should still be owned")
	}
	if g.OwnedItems["chair_basic"] || g.OwnedItems["hoodie_classic"] {
		t.Error("after Apply: pre-STORE-2.0 un-coloured ids must NOT be owned — they are gone from the catalog")
	}
	// Every slot equips its tier-0 default: the old equipped ids resolved
	// to nothing and fell back cleanly.
	for _, slot := range g.Slots() {
		if got, want := g.Equipped[slot.ID].ItemID, g.TierZeroItem(slot.ID); got != want {
			t.Errorf("slot %q equipped %q, want the tier-0 default %q — a dropped old id must fall back to default", slot.ID, got, want)
		}
	}
}
