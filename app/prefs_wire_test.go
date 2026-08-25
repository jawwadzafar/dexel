// prefs_wire_test.go — SET-1's contract seams (docs/ui-spec.md §11):
// SET_PREF's wire shape and behaviour through applyAction, and the
// `prefs` block `dexel status --json` publishes for the desktop shell.
//
// Sibling of identity_wire_test.go, and for the same reason: these are
// the exact strings two other codebases depend on — the TypeScript
// frontend (app/frontend/src/wire.ts) and the Rust desktop shell
// (desktop/src-tauri/src/lib.rs) — so they are pinned here rather than
// only being exercised indirectly.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/game"
	"github.com/jawwadzafar/dexel/app/internal/store"
)

// TestSetPrefWireShapeRoundTrips is the whole action, decoded from the
// literal JSON a browser sends: `{"action":"SET_PREF","key":...,
// "value":true}` and nothing else.
func TestSetPrefWireShapeRoundTrips(t *testing.T) {
	for _, key := range game.PrefKeys() {
		var msg actionMessage
		raw := `{"action":"SET_PREF","key":"` + key + `","value":true}`
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		if msg.Action != actionSetPref || msg.Key != key || !msg.Value {
			t.Fatalf("decoded %s as %+v", raw, msg)
		}

		g := game.New()
		mutated, flash := applyAction(g, msg, 1)
		if !mutated {
			t.Errorf("%s did not mutate the game", raw)
		}
		// No flash on success — the PAUSE/STORE_OPEN precedent
		// (docs/ui-spec.md §6.2): a preference is a state, not an event.
		if flash != nil {
			t.Errorf("%s produced a flash (%+v); a successful SET_PREF is answered by `state` alone", raw, flash)
		}
	}
}

// TestSetPrefShowsOnTheWireUnderTheDocumentedTags pins the camelCase keys
// docs/ui-spec.md §6.1 documents inside the `config` block — the tags
// wire.ts's ConfigView mirrors.
func TestSetPrefShowsOnTheWireUnderTheDocumentedTags(t *testing.T) {
	g := game.New()
	applyAction(g, actionMessage{Action: actionSetPref, Key: game.PrefAlwaysOnTop, Value: true}, 1)
	applyAction(g, actionMessage{Action: actionSetPref, Key: game.PrefShowAwayTime, Value: true}, 1)

	raw, err := json.Marshal(g.State())
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if !strings.Contains(string(raw), `"config":{"name":"","alwaysOnTop":true,"showAwayTime":true}`) {
		t.Fatalf("state's config block does not carry both preferences under the documented tags: %s", raw)
	}

	// The away durations are STILL on the wire with showAwayTime true or
	// false — hiding is the client's job, and it can only hide a field it
	// is sent (docs/ui-spec.md §11.3).
	off := game.New()
	rawOff, err := json.Marshal(off.State())
	if err != nil {
		t.Fatalf("marshal fresh state: %v", err)
	}
	if !strings.Contains(string(rawOff), `"idleSeconds"`) {
		t.Fatal("idleSeconds is missing from a state broadcast with showAwayTime false — the wire must keep sending it; the CLIENT hides it")
	}
}

// TestSetPrefRejectsAnUnknownKeyOverTheWire is the same validation
// game.SetPref owns, checked at the layer a client actually reaches: an
// unknown key is an error flash with no mutation, so it is never
// write-through'd to config.json either (main.go's loop gates the write
// on `mutated`).
func TestSetPrefRejectsAnUnknownKeyOverTheWire(t *testing.T) {
	for _, raw := range []string{
		`{"action":"SET_PREF","key":"devCash","value":true}`,
		`{"action":"SET_PREF","value":true}`, // no key at all
		`{"action":"SET_PREF"}`,
	} {
		var msg actionMessage
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		g := game.New()
		mutated, flash := applyAction(g, msg, 1)
		if mutated {
			t.Errorf("%s mutated the game", raw)
		}
		if flash == nil || flash.Kind != "error" {
			t.Errorf("%s: want an error flash, got %+v", raw, flash)
		}
	}
}

// TestSetPrefAndSetNameDoNotDisturbEachOther guards the blast
// radius: SET_NAME must not reset a preference, and SET_PREF must not
// touch the name. Both write the SAME file, so a construct-and-overwrite
// mistake in either direction is the P2 clobber lesson repeating.
func TestSetPrefAndSetNameDoNotDisturbEachOther(t *testing.T) {
	g := game.New()
	applyAction(g, actionMessage{Action: actionSetPref, Key: game.PrefAlwaysOnTop, Value: true}, 1)
	applyAction(g, actionMessage{Action: actionSetName, Name: "Pixel"}, 1)
	if !g.AlwaysOnTop() {
		t.Error("SET_NAME cleared the alwaysOnTop preference")
	}
	if g.ConfigName() != "Pixel" {
		t.Errorf("name = %q, want Pixel", g.ConfigName())
	}
	applyAction(g, actionMessage{Action: actionSetPref, Key: game.PrefShowAwayTime, Value: true}, 1)
	if g.ConfigName() != "Pixel" {
		t.Errorf("SET_PREF changed the name to %q", g.ConfigName())
	}
}

// TestReadPrefsReadsTheSameFileTheRuntimeWrites is the seam between the
// two processes that make SET-1's window toggle work: the RUNTIME writes
// config.json (main.go's persistConfig) and the CLI reads it back for
// `status --json`'s `prefs` block, which is the only channel the desktop
// shell has. If these two ever disagree about the filename or the field
// names, the toggle silently stops reaching the window.
func TestReadPrefsReadsTheSameFileTheRuntimeWrites(t *testing.T) {
	// The basename `status` joins onto its already-resolved state dir must
	// be the basename store.ConfigPath() resolves to, or the CLI is
	// reading a file nothing writes.
	canonical, err := store.ConfigPath()
	if err != nil {
		t.Fatalf("store.ConfigPath: %v", err)
	}
	if got := filepath.Base(canonical); got != configFileName {
		t.Fatalf("configFileName = %q but store.ConfigPath() ends in %q", configFileName, got)
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, configFileName)

	// Nothing written yet: defaults, no error, no refusal to answer.
	if got := readPrefs(dir); got != (prefsJSON{}) {
		t.Errorf("readPrefs on a missing config.json = %+v, want both false", got)
	}

	// Exactly what the runtime's write-through produces.
	if err := writeConfigThrough(cfgPath, "Pixel", nil, configPrefs{AlwaysOnTop: true, ShowAwayTime: true}); err != nil {
		t.Fatalf("writeConfigThrough: %v", err)
	}
	if got := readPrefs(dir); !got.AlwaysOnTop || !got.ShowAwayTime {
		t.Errorf("readPrefs = %+v after a write-through setting both, want both true", got)
	}

	// A hand-edited, syntactically broken config.json must not stop
	// `status` answering — the same "degrades to defaults, never blocks"
	// contract store.LoadConfig owns.
	if err := os.WriteFile(cfgPath, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write malformed config: %v", err)
	}
	if got := readPrefs(dir); got != (prefsJSON{}) {
		t.Errorf("readPrefs on a malformed config.json = %+v, want defaults", got)
	}
}

// TestStatusPrefsJSONTagsAreTheContractTheShellReads pins the exact tags
// desktop/src-tauri/src/lib.rs parses out of `status --json`.
func TestStatusPrefsJSONTagsAreTheContractTheShellReads(t *testing.T) {
	raw, err := json.Marshal(statusJSON{Prefs: prefsJSON{AlwaysOnTop: true}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"prefs":{"alwaysOnTop":true,"showAwayTime":false}`) {
		t.Fatalf("status --json's prefs block is not the shape the desktop shell reads: %s", raw)
	}
}
