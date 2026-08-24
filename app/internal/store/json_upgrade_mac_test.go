package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prePR5Body is a FROZEN, byte-exact canonical body as a pre-PR-5 build
// (schema 6, dexel v1.0.0) actually wrote it: no `pausedSeconds` key
// anywhere, no `session`/`sessionLogHead`/`paused` keys, real non-zero
// counters throughout. It is a literal on purpose — regenerating it from
// today's structs would make it track whatever the code currently does,
// which is precisely the property that let this bug ship.
//
// Field order is canonicalBody's own (encoding/json, struct declaration
// order, map keys sorted), because the JSON side of the MAC is verified
// against a RE-SERIALIZATION of the parsed struct, not against the file's
// bytes.
const prePR5Body = `{"schema":6,"devCash":125,"xp":190,"sprint":{"index":3,"unitsDone":8.112979},` +
	`"ownedItems":["bev_mug","chair_basic","hoodie_classic"],"ownedTints":[],` +
	`"equipped":{"chair":{"itemId":"chair_basic","tintId":"slate"},"hoodie":{"itemId":"hoodie_classic","tintId":"indigo"}},` +
	`"stats":{"date":"2026-08-23",` +
	`"today":{"keystrokes":33,"mouseActiveSeconds":2,"activeSeconds":17,"idleSeconds":957,"sprintsCompleted":0,"focusSessions":0,"appSwitches":0},` +
	`"lifetime":{"keystrokes":3492,"mouseActiveSeconds":436,"activeSeconds":1774,"idleSeconds":9294,"sprintsCompleted":1,"focusSessions":0,"appSwitches":0},` +
	`"coinsToday":{"keystrokes":0,"mouse":0,"focusSessions":0,"appSwitches":0},` +
	`"history":[{"date":"2026-08-22","counters":{"keystrokes":3459,"mouseActiveSeconds":434,"activeSeconds":1757,"idleSeconds":8337,"sprintsCompleted":1,"focusSessions":0,"appSwitches":0},"coinsEarned":60,"longestFocusBlockSeconds":52}],` +
	`"streak":{"current":1,"longest":1,"lastActiveDate":"2026-08-22"}}}`

// prePR5Mac is the tag that pre-PR-5 build wrote alongside it: HMAC-SHA256
// over macDomain ‖ 0x00 ‖ prePR5Body with the baked-in key. Frozen for the
// same reason the body is.
const prePR5Mac = "1eeb106255951b922ca39bcb393b7f97c9d84b17603134779936d17150200795"

// TestPrePR5StateJSONImportsUnderItsOriginalMac is the regression test for
// the upgrade bug PR-5 shipped: StatCountersSave.PausedSeconds was added
// WITHOUT `omitempty`, so canonicalBody injected `"pausedSeconds":0` into
// four buckets (today, lifetime, every history day, both session halves)
// of every pre-PR-5 state.json. That changed the MAC preimage, verifyMAC
// failed, and loadJSON quarantined a perfectly good save as tampered —
// a real v1.0.0 save (devCash 125) was refused on a real machine and the
// player silently started a fresh economy at 0.
//
// This is the JSON side specifically. The DB side never had the bug and
// could not have caught it: loadDB verifies verifyMACBytes(payload,
// macHex) against the bytes STORED in the row, so no re-serialization
// happens and no later field can disturb it (which is why
// TestSchema6GrandfatherLoadsAsNotPausedWithNoPausedSeconds, a DB-path
// test, passed throughout). importJSON is the one path whose documented
// invariant — "canonicalBody(d) ... reproduces byte-identical preimage
// bytes" — depends on every field added after a file was written being
// absent from the re-encoding.
func TestPrePR5StateJSONImportsUnderItsOriginalMac(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	jsonPath := jsonImportPath(dbPath)

	if strings.Contains(prePR5Body, "paused") {
		t.Fatalf("the fixture mentions `paused` — it is not a pre-PR-5 payload: %s", prePR5Body)
	}

	// Written INDENTED, exactly as a real state.json is on disk, to prove
	// the MAC never sees file formatting — only the canonical
	// re-serialization.
	signed := prePR5Body[:len(prePR5Body)-1] + `,"mac":"` + prePR5Mac + `"}`
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(signed), "", "  "); err != nil {
		t.Fatalf("indent the fixture: %v", err)
	}
	if err := os.WriteFile(jsonPath, pretty.Bytes(), 0o600); err != nil {
		t.Fatalf("write %s: %v", jsonPath, err)
	}

	d, ok, err := Load(dbPath)
	if err != nil {
		t.Fatalf("Load refused a valid pre-PR-5 state.json: %v\n"+
			"This is the upgrade bug: every field added to SaveData/StatCountersSave after a\n"+
			"save was written must be `omitempty`/`omitzero`, or canonicalBody re-serializes\n"+
			"keys the original MAC never covered and every existing player loses their save.", err)
	}
	if !ok {
		t.Fatal("ok = false for a valid pre-PR-5 state.json — it must import, not be treated as no-save")
	}

	// The economy survived the upgrade intact.
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
	// PR-5's own grandfather rule: absent means zero, never backfilled.
	if d.Stats.Today.PausedSeconds != 0 || d.Stats.Lifetime.PausedSeconds != 0 || d.Paused {
		t.Errorf("pause state invented for a pre-PR-5 save: today=%d lifetime=%d paused=%v",
			d.Stats.Today.PausedSeconds, d.Stats.Lifetime.PausedSeconds, d.Paused)
	}

	// Nothing was quarantined: a healthy save must never leave a
	// .invalid behind, because that file is what a user then has to be
	// told about.
	if _, err := os.Stat(jsonPath + ".invalid"); err == nil {
		t.Error("a valid pre-PR-5 save was quarantined to state.json.invalid")
	}
}

// TestCanonicalBodyReproducesAPrePR5PayloadByteForByte pins importJSON's
// documented invariant directly, one layer below the Load test above:
// parsing a pre-PR-5 payload and re-serializing it must return the SAME
// BYTES. Any future field on SaveData or StatCountersSave that lacks
// `omitempty`/`omitzero` breaks this immediately and says so, instead of
// the breakage surfacing as "an old player's save is tampered".
func TestCanonicalBodyReproducesAPrePR5PayloadByteForByte(t *testing.T) {
	var d SaveData
	if err := json.Unmarshal([]byte(prePR5Body), &d); err != nil {
		t.Fatalf("the frozen fixture is not valid JSON: %v", err)
	}
	got := canonicalBody(d)
	if string(got) != prePR5Body {
		t.Errorf("canonicalBody changed the preimage of a pre-PR-5 save.\n"+
			" got: %s\nwant: %s\n\n"+
			"A field added since that save was written is being emitted into the MAC preimage.\n"+
			"Give it `omitempty` (or `omitzero` for a pointer/struct), the way\n"+
			"Session/SessionLogHead/Paused already do — otherwise every existing state.json\n"+
			"fails its MAC on upgrade and players are silently reset to a fresh economy.",
			got, prePR5Body)
	}
	// The frozen tag is self-consistent with the frozen body, so a
	// deliberate key or domain rotation fails HERE — with a note that it
	// invalidates every save in the wild — rather than in a user's log.
	if mac := computeMACBytes([]byte(prePR5Body)); mac != prePR5Mac {
		t.Errorf("computeMACBytes(prePR5Body) = %s, want the frozen %s — the key or the domain tag changed.\n"+
			"That rotation invalidates every save already on disk; it needs a migration, not a new constant.",
			mac, prePR5Mac)
	}
}
