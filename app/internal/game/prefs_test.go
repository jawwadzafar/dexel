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
func TestSetPrefAppliesEveryAllowListedKey(t *testing.T) {
	for _, key := range PrefKeys() {
		g := New()
		mutated, err := g.SetPref(key, true)
		if err != nil {
			t.Fatalf("SetPref(%q, true): %v", key, err)
		}
		if !mutated {
			t.Errorf("SetPref(%q, true) on a fresh game reported no mutation — every preference defaults to false", key)
		}
		// The value has to be visible in the ONE place the client can
		// see it: the state broadcast.
		cfg := g.State().Config
		switch key {
		case PrefAlwaysOnTop:
			if !cfg.AlwaysOnTop {
				t.Errorf("SetPref(%q) did not reach ConfigView.AlwaysOnTop", key)
			}
		case PrefShowAwayTime:
			if !cfg.ShowAwayTime {
				t.Errorf("SetPref(%q) did not reach ConfigView.ShowAwayTime", key)
			}
		default:
			t.Errorf("PrefKeys() reports %q, which this test does not check on the wire — add it", key)
		}
	}
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

// TestPrefsDefaultToFalseAndRestoreFromConfig is the default the owner
// asked for, pinned: a fresh dexel does NOT pin its window and does NOT
// show away time, and a restart restores whatever the user chose.
func TestPrefsDefaultToFalseAndRestoreFromConfig(t *testing.T) {
	g := New()
	if g.AlwaysOnTop() || g.ShowAwayTime() {
		t.Fatalf("a fresh game has alwaysOnTop=%v showAwayTime=%v, want both false", g.AlwaysOnTop(), g.ShowAwayTime())
	}
	g.RestorePrefs(true, true)
	if !g.AlwaysOnTop() || !g.ShowAwayTime() {
		t.Error("RestorePrefs(true,true) did not take")
	}
	g.RestorePrefs(false, false)
	if g.AlwaysOnTop() || g.ShowAwayTime() {
		t.Error("RestorePrefs(false,false) did not take — a preference turned back off must restore off")
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
