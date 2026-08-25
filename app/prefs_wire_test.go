// prefs_wire_test.go — SET-1's contract seams (docs/ui-spec.md §11) plus
// SOUND-1's (§13):
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
		// SOUND-1: the value sent is the OPPOSITE of what a fresh game
		// already holds, read off the wire rather than assumed. This test
		// used to hardcode `true` and lean on "every preference defaults
		// to false" — which stopped being true the moment `soundEnabled`
		// arrived defaulting ON, and would have reported the new key as
		// broken when it was working perfectly. A round-trip test must
		// exercise a real CHANGE, and only the current value knows which
		// one that is.
		want := !prefOnTheWire(t, game.New(), key)
		var msg actionMessage
		raw := `{"action":"SET_PREF","key":"` + key + `","value":` + boolLit(want) + `}`
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		if msg.Action != actionSetPref || msg.Key != key || msg.Value != want {
			t.Fatalf("decoded %s as %+v", raw, msg)
		}

		g := game.New()
		mutated, flash := applyAction(g, msg, 1)
		if !mutated {
			t.Errorf("%s did not mutate the game", raw)
		}
		if got := prefOnTheWire(t, g, key); got != want {
			t.Errorf("%s: the wire still reports %v", raw, got)
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
	// soundEnabled is true here without anyone setting it — that IS its
	// documented default (SOUND-1, docs/ui-spec.md §13), and pinning the
	// whole literal block is what makes the default part of the contract
	// rather than an accident of construction.
	if !strings.Contains(string(raw), `"config":{"name":"","alwaysOnTop":true,"showAwayTime":true,"soundEnabled":true}`) {
		t.Fatalf("state's config block does not carry every preference under the documented tags: %s", raw)
	}

	// And the OFF direction reaches the wire too — the half a
	// default-on preference can get wrong: a client that can never send
	// `false` has a toggle that only mutes on paper.
	muted := game.New()
	applyAction(muted, actionMessage{Action: actionSetPref, Key: game.PrefSoundEnabled, Value: false}, 1)
	rawMuted, err := json.Marshal(muted.State())
	if err != nil {
		t.Fatalf("marshal muted state: %v", err)
	}
	if !strings.Contains(string(rawMuted), `"soundEnabled":false`) {
		t.Fatalf("SET_PREF soundEnabled=false did not reach the wire: %s", rawMuted)
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

	// Nothing written yet: defaults, no error, no refusal to answer. The
	// defaults are not all false — SOUND-1's soundEnabled defaults ON —
	// and `status` must report the default the RUNNING game would use, not
	// the zero value of a struct (readPrefs goes through
	// store.ConfigData.SoundEnabledOrDefault for exactly this).
	defaults := prefsJSON{SoundEnabled: true}
	if got := readPrefs(dir); got != defaults {
		t.Errorf("readPrefs on a missing config.json = %+v, want %+v", got, defaults)
	}

	// Exactly what the runtime's write-through produces.
	if err := writeConfigThrough(cfgPath, "Pixel", nil, configPrefs{AlwaysOnTop: true, ShowAwayTime: true, SoundEnabled: true}); err != nil {
		t.Fatalf("writeConfigThrough: %v", err)
	}
	if got := readPrefs(dir); !got.AlwaysOnTop || !got.ShowAwayTime || !got.SoundEnabled {
		t.Errorf("readPrefs = %+v after a write-through setting all three, want all true", got)
	}

	// The muted case has to survive the round trip too, and it is the one
	// a nil-vs-false mistake would break: a user who turned sound OFF must
	// not be reported as on just because off happens to look like absent.
	if err := writeConfigThrough(cfgPath, "Pixel", nil, configPrefs{SoundEnabled: false}); err != nil {
		t.Fatalf("writeConfigThrough (muted): %v", err)
	}
	if got := readPrefs(dir); got.SoundEnabled {
		t.Errorf("readPrefs = %+v after a write-through muting sound, want soundEnabled false", got)
	}

	// A hand-edited, syntactically broken config.json must not stop
	// `status` answering — the same "degrades to defaults, never blocks"
	// contract store.LoadConfig owns.
	if err := os.WriteFile(cfgPath, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write malformed config: %v", err)
	}
	if got := readPrefs(dir); got != defaults {
		t.Errorf("readPrefs on a malformed config.json = %+v, want %+v", got, defaults)
	}
}

// TestStatusPrefsJSONTagsAreTheContractTheShellReads pins the exact tags
// desktop/src-tauri/src/lib.rs parses out of `status --json`.
func TestStatusPrefsJSONTagsAreTheContractTheShellReads(t *testing.T) {
	raw, err := json.Marshal(statusJSON{Prefs: prefsJSON{AlwaysOnTop: true}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"prefs":{"alwaysOnTop":true,"showAwayTime":false,"soundEnabled":false}`) {
		t.Fatalf("status --json's prefs block is not the shape the desktop shell reads: %s", raw)
	}
}

// TestStatusStateDirIsPublishedForTheShellsSettingsWatcher pins `stateDir`
// as the second thing the desktop shell reads out of `status --json`.
//
// The shell used to apply a preference only when the window regained focus,
// which meant a stacking preference was always observed one step stale — it
// read to users as "the always-on-top toggle is reversed". The fix
// (`watch_prefs_file` in desktop/src-tauri/src/lib.rs) watches config.json
// directly and applies the change when it happens, and it locates that file
// by joining "config.json" onto THIS field. So `stateDir` is no longer
// merely informational output for a human reading a terminal; it is the
// shell's only way to find the file without duplicating
// app/internal/paths' per-OS rules in Rust.
//
// Two properties, both load-bearing, both silent if broken:
//   - the key is spelled `stateDir`
//   - it carries NO omitempty, so it is present on the not-running branch
//     too — a preference is config, and the shell honours it with no
//     runtime at all (see prefsJSON's comment).
func TestStatusStateDirIsPublishedForTheShellsSettingsWatcher(t *testing.T) {
	// Not running, and every other optional field empty: this is the
	// leanest answer `status --json` ever prints, and stateDir must
	// survive it.
	raw, err := json.Marshal(statusJSON{Running: false, StateDir: "/tmp/dexel-state"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"stateDir":"/tmp/dexel-state"`) {
		t.Fatalf("status --json does not publish stateDir the way the desktop shell reads it: %s", raw)
	}

	// And it is not dropped when empty — an absent key and an empty string
	// are different answers to the shell, which skips its watcher on the
	// latter rather than watching the wrong path.
	empty, err := json.Marshal(statusJSON{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(empty), `"stateDir":""`) {
		t.Fatalf("stateDir gained an omitempty; the desktop shell reads it unconditionally: %s", empty)
	}
}

// prefOnTheWire reads one preference back out of the ONE place a client can
// see it — the `config` block of a `state` broadcast — by its wire key.
// Keyed by string rather than switched by name on purpose: it then works for
// a preference added after this helper was written, which is the whole point
// of the tests above walking game.PrefKeys() instead of a hand-written list.
func prefOnTheWire(t *testing.T, g *game.Game, key string) bool {
	t.Helper()
	raw, err := json.Marshal(g.State().Config)
	if err != nil {
		t.Fatalf("marshal config view: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal config view: %v", err)
	}
	v, ok := m[key]
	if !ok {
		t.Fatalf("pref %q is settable but absent from the config block (%s)", key, raw)
	}
	b, ok := v.(bool)
	if !ok {
		t.Fatalf("pref %q is %T on the wire, want bool", key, v)
	}
	return b
}

func boolLit(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
