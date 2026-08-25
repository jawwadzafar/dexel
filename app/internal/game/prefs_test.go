package game

import (
	"errors"
	"reflect"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/engine"
)

// TestSetPrefAppliesEveryAllowListedKey walks the allow-list itself
// rather than naming keys by hand, so a preference added to
// prefTargets() without a working setter fails here immediately.
//
// It flips each preference AWAY FROM whatever a fresh game holds instead
// of setting them all to true. It used to do the latter, on the stated
// assumption that "every preference defaults to false" — an assumption
// SOUND-1's soundEnabled (default ON) retired. Testing the flip rather
// than a fixed value keeps this default-agnostic, which is what a test
// that walks the allow-list has to be if the allow-list is allowed to
// grow a preference with a different default.
func TestSetPrefAppliesEveryAllowListedKey(t *testing.T) {
	for _, key := range PrefKeys() {
		g := New()
		before := prefFromView(t, g, key)
		want := !before
		mutated, err := g.SetPref(key, want)
		if err != nil {
			t.Fatalf("SetPref(%q, %v): %v", key, want, err)
		}
		if !mutated {
			t.Errorf("SetPref(%q, %v) on a fresh game (which holds %v) reported no mutation", key, want, before)
		}
		// The value has to be visible in the ONE place the client can
		// see it: the state broadcast.
		if got := prefFromView(t, g, key); got != want {
			t.Errorf("SetPref(%q, %v) did not reach ConfigView (still %v)", key, want, got)
		}
	}
}

// prefFromView reads one preference out of ConfigView BY ITS WIRE TAG,
// via reflection over the same struct TestPrefKeysMatchesConfigViewsBoolFields
// audits. Deliberately not a switch on the key: a hand-written switch is a
// second list to keep in step with the allow-list, and the last one silently
// became wrong (its default branch failed the build for a key it simply had
// not been taught). Reflection makes "every settable key is readable on the
// wire" the thing being tested rather than something this helper assumes.
func prefFromView(t *testing.T, g *Game, key string) bool {
	t.Helper()
	view := reflect.ValueOf(g.State().Config)
	typ := view.Type()
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Tag.Get("json") != key {
			continue
		}
		if view.Field(i).Kind() != reflect.Bool {
			t.Fatalf("ConfigView field for pref %q is %s, want bool", key, view.Field(i).Kind())
		}
		return view.Field(i).Bool()
	}
	t.Fatalf("pref %q has no field on ConfigView — a preference the client cannot read back", key)
	return false
}

// TestPrefKeysMatchesConfigViewsBoolFields is the anti-rot check: the
// allow-list and the wire block must describe the same set of
// preferences. A field added to ConfigView with no settable key (dead on
// arrival), or a key with no wire field (invisible once set), both fail
// here.
func TestPrefKeysMatchesConfigViewsBoolFields(t *testing.T) {
	var wireBools []string
	typ := reflect.TypeOf(ConfigView{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() == reflect.Bool {
			wireBools = append(wireBools, f.Tag.Get("json"))
		}
	}
	got := PrefKeys()
	if len(got) != len(wireBools) {
		t.Fatalf("PrefKeys() = %v but ConfigView's bool fields are %v — every preference must be both settable and visible", got, wireBools)
	}
	for _, key := range got {
		found := false
		for _, tag := range wireBools {
			if tag == key {
				found = true
			}
		}
		if !found {
			t.Errorf("pref key %q has no matching json tag on ConfigView (tags: %v) — a preference the client cannot read back", key, wireBools)
		}
	}
}

// TestSetPrefRejectsAnUnknownKeyAndChangesNothing is the server-side
// validation contract (docs/ui-spec.md §6.2: "the server validates
// everything ... never a partial write"): a client cannot invent a
// preference.
func TestSetPrefRejectsAnUnknownKeyAndChangesNothing(t *testing.T) {
	g := New()
	before := g.State().Config
	for _, key := range []string{"", "AlwaysOnTop", "always_on_top", "devCash", "showawaytime"} {
		mutated, err := g.SetPref(key, true)
		if err == nil {
			t.Errorf("SetPref(%q) was accepted — only PrefKeys() may be set", key)
		}
		if !errors.Is(err, ErrUnknownPref) {
			t.Errorf("SetPref(%q) error = %v, want it to wrap ErrUnknownPref", key, err)
		}
		if mutated {
			t.Errorf("SetPref(%q) reported a mutation", key)
		}
	}
	if g.State().Config != before {
		t.Errorf("config = %+v after only-rejected SetPref calls, want %+v unchanged", g.State().Config, before)
	}
}

// TestSetPrefIsIdempotent pins the no-op contract SetPaused already
// establishes: re-sending the value already held reports mutated=false,
// which is what stops the server's action loop writing config.json again
// for a preference that did not change.
func TestSetPrefIsIdempotent(t *testing.T) {
	g := New()
	if mutated, err := g.SetPref(PrefShowAwayTime, false); err != nil || mutated {
		t.Errorf("SetPref(showAwayTime,false) on a fresh game = (%v, %v), want (false, nil) — it is already false", mutated, err)
	}
	if mutated, _ := g.SetPref(PrefShowAwayTime, true); !mutated {
		t.Fatal("the first real change reported no mutation")
	}
	if mutated, _ := g.SetPref(PrefShowAwayTime, true); mutated {
		t.Error("re-setting showAwayTime to true reported a mutation — a repeat SET_PREF must be a genuine no-op")
	}
}

// TestPrefDefaultsAndRestoreFromConfig is the set of defaults the owner
// asked for, pinned: a fresh dexel does NOT pin its window and does NOT
// show away time, but it DOES have sound on (SOUND-1: "default ON" — a
// companion that ships silent is a companion nobody discovers has a
// voice). A restart restores whatever the user chose, in both directions.
func TestPrefDefaultsAndRestoreFromConfig(t *testing.T) {
	g := New()
	if g.AlwaysOnTop() || g.ShowAwayTime() {
		t.Fatalf("a fresh game has alwaysOnTop=%v showAwayTime=%v, want both false", g.AlwaysOnTop(), g.ShowAwayTime())
	}
	if !g.SoundEnabled() {
		t.Fatal("a fresh game has sound OFF — SOUND-1's default is ON")
	}
	g.RestorePrefs(true, true, false)
	if !g.AlwaysOnTop() || !g.ShowAwayTime() {
		t.Error("RestorePrefs(true,true,...) did not take")
	}
	if g.SoundEnabled() {
		t.Error("RestorePrefs could not restore a MUTE — the one direction a default-on preference has to be able to remember")
	}
	g.RestorePrefs(false, false, true)
	if g.AlwaysOnTop() || g.ShowAwayTime() {
		t.Error("RestorePrefs(false,false,...) did not take — a preference turned back off must restore off")
	}
	if !g.SoundEnabled() {
		t.Error("RestorePrefs(...,true) did not restore sound")
	}
}

// TestSoundEnabledChangesNothingButItself is the SOUND-1 sibling of
// TestShowAwayTimeNeverChangesWhatIsRecorded below, and it exists for the
// same reason: a preference about PRESENTATION must not reach the economy.
// Muting dexel is not paying dexel less.
func TestSoundEnabledChangesNothingButItself(t *testing.T) {
	run := func(sound bool) StateMessage {
		g := New()
		if _, err := g.SetPref(PrefSoundEnabled, sound); err != nil {
			t.Fatalf("SetPref: %v", err)
		}
		g.Tick(engine.TickResult{Mood: engine.MoodCoding, KeystrokeDelta: 9})
		g.Tick(engine.TickResult{Mood: engine.MoodCoding, KeystrokeDelta: 7})
		g.Tick(engine.TickResult{Mood: engine.MoodIdle})
		return g.State()
	}
	on, off := run(true), run(false)
	if on.Stats.Today != off.Stats.Today {
		t.Errorf("today's counters differ by the sound preference:\n on:  %+v\n off: %+v", on.Stats.Today, off.Stats.Today)
	}
	if on.DevCash != off.DevCash || on.XP != off.XP || on.Sprint.Progress != off.Sprint.Progress {
		t.Errorf("the economy differs by the sound preference: on={cash:%d xp:%d progress:%v} off={cash:%d xp:%d progress:%v}",
			on.DevCash, on.XP, on.Sprint.Progress, off.DevCash, off.XP, off.Sprint.Progress)
	}
	// And the flag itself is the ONLY thing that moved.
	if on.Config.SoundEnabled == off.Config.SoundEnabled {
		t.Error("the two runs report the same soundEnabled — the fixture is not exercising the preference")
	}
}

// TestShowAwayTimeNeverChangesWhatIsRecorded is the load-bearing honesty
// test for this whole feature, and the reason the owner's requirement is
// worded "we can record not working but not show user": hiding away time
// is a PRESENTATION choice and must not touch the counters (ADR 0010's
// honest mood machine and ADR 0013's analytics are unchanged by SET-1).
//
// If this ever fails, every total derived from IdleSeconds has silently
// become wrong for anyone who kept the default — the worst possible
// version of this feature.
func TestShowAwayTimeNeverChangesWhatIsRecorded(t *testing.T) {
	tick := func(showAway bool) StatCounters {
		g := New()
		if _, err := g.SetPref(PrefShowAwayTime, showAway); err != nil {
			t.Fatalf("SetPref: %v", err)
		}
		g.Tick(engine.TickResult{Mood: engine.MoodCoding, KeystrokeDelta: 4})
		g.Tick(engine.TickResult{Mood: engine.MoodIdle})
		g.Tick(engine.TickResult{Mood: engine.MoodIdle})
		g.Tick(engine.TickResult{Mood: engine.MoodOnBreak})
		return g.State().Stats.Today
	}
	hidden, shown := tick(false), tick(true)
	if hidden.IdleSeconds == 0 {
		t.Fatal("no away seconds were recorded at all — the fixture is not exercising the counter")
	}
	if hidden != shown {
		t.Errorf("today's counters differ by preference:\n hidden: %+v\n shown:  %+v\nshowAwayTime must change only what is DISPLAYED, never what is recorded", hidden, shown)
	}
	// And the durations must still cross the wire while hidden — the
	// client is what hides them, from a field it can only hide if it is
	// sent (docs/ui-spec.md §11.3).
	if got := shown.IdleSeconds; hidden.IdleSeconds != got {
		t.Errorf("idleSeconds on the wire = %d while hidden, %d while shown", hidden.IdleSeconds, got)
	}
}
