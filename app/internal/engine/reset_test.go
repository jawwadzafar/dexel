package engine

import (
	"reflect"
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/activity"
)

// TestResetMakesTheNextTickBehaveLikeTheFirst is PR-5's resume seam
// (dev_docs/production-runtime/ARCHITECTURE.md Decision 16,
// MIGRATION_PLAN.md §PR-5's "resume after a long pause grants no work on
// the first tick and no focus bonus from a pre-pause run (Engine.Reset()
// proven)").
//
// The post-pause snapshot deliberately reports a DIFFERENT app and a
// wildly advanced keystroke counter, because those are the two fields
// whose staleness would otherwise be paid for: a real provider's
// monotonic counter keeps climbing through a pause dexel was not
// watching, and the foreground app is very likely to have changed while
// the user was away.
func TestResetMakesTheNextTickBehaveLikeTheFirst(t *testing.T) {
	p := &stubProvider{honesty: activity.HonestyGlobal}
	e := New(p)

	// A real pre-pause typing run, in "code".
	var keys uint64
	for i := 0; i < 30; i++ {
		keys += 6
		p.snap = activity.Snapshot{KeystrokeCount: keys, ActiveApp: "code"}
		// i == 0 is the engine's own first-tick guard: no baseline yet,
		// so zero work is correct there and only there.
		if got := e.Tick(); i > 0 && got.WorkUnits == 0 {
			t.Fatalf("tick %d earned nothing — the pre-pause run must be real for this test to prove anything", i)
		}
	}

	e.Reset()

	// Resume: a million keystrokes happened unobserved, in a different app.
	p.snap = activity.Snapshot{KeystrokeCount: keys + 1_000_000, ActiveApp: "firefox"}
	first := e.Tick()
	if first.WorkUnits != 0 {
		t.Errorf("the first tick after Reset earned %v work units, want 0 — a million keystrokes dexel never observed must never be paid for", first.WorkUnits)
	}
	if first.KeystrokeDelta != 0 {
		t.Errorf("the first tick after Reset reported keystrokeDelta %d, want 0", first.KeystrokeDelta)
	}
	if first.Mood != MoodIdle {
		t.Errorf("the first tick after Reset reported mood %q, want %q — a keystroke observed before the pause is not 'coding right now'", first.Mood, MoodIdle)
	}
	if first.AppSwitches != 0 {
		t.Errorf("the first tick after Reset counted %d app switches, want 0 — there is no previous tick to have switched FROM", first.AppSwitches)
	}
	if first.FocusSessionsCompleted != 0 {
		t.Errorf("the first tick after Reset completed %d focus sessions, want 0", first.FocusSessionsCompleted)
	}
	if first.FocusRunSeconds != 0 {
		t.Errorf("the first tick after Reset reported focusRunSeconds %d, want 0", first.FocusRunSeconds)
	}

	// ...and the tick AFTER that behaves normally again, so Reset clears
	// state rather than disabling the engine.
	p.snap = activity.Snapshot{KeystrokeCount: keys + 1_000_006, ActiveApp: "firefox"}
	second := e.Tick()
	if second.KeystrokeDelta != 6 || second.WorkUnits == 0 || second.Mood != MoodCoding {
		t.Errorf("the second tick after Reset = %+v, want a normal 6-keystroke coding tick", second)
	}
	if second.AppSwitches != 0 {
		t.Errorf("the second tick after Reset counted %d app switches, want 0 — the post-Reset baseline was firefox and it has not changed", second.AppSwitches)
	}
}

// TestWithoutResetTheFirstTickBackWouldLie is the control for the test
// above: the SAME resume, minus the Reset, on a separate engine. It exists
// so the assertions above are known to be load-bearing rather than
// coincidentally true — if a future refactor made them pass with or
// without Reset, this test fails and says so.
func TestWithoutResetTheFirstTickBackWouldLie(t *testing.T) {
	p := &stubProvider{honesty: activity.HonestyGlobal}
	e := New(p)

	var keys uint64
	for i := 0; i < 30; i++ {
		keys += 6
		p.snap = activity.Snapshot{KeystrokeCount: keys, ActiveApp: "code"}
		e.Tick()
	}

	// No Reset. Same resume snapshot as the test above.
	p.snap = activity.Snapshot{KeystrokeCount: keys + 1_000_000, ActiveApp: "firefox"}
	first := e.Tick()
	if first.KeystrokeDelta == 0 {
		t.Error("control: without Reset the stale keystroke baseline did NOT produce a phantom delta — the work-accounting half of Reset is no longer load-bearing; re-derive it before deleting anything")
	}
	if first.Mood != MoodCoding {
		t.Error("control: without Reset the stale lastKeystrokeAt did NOT produce a false `coding` claim — the mood half of Reset is no longer load-bearing; re-derive it before deleting anything")
	}
	if first.AppSwitches == 0 {
		t.Error("control: without Reset the stale lastActiveApp did NOT produce a fabricated app switch — the app-switch half of Reset is no longer load-bearing; re-derive it before deleting anything")
	}
}

// TestResetPreventsSwallowedWorkWhenTheProviderCounterRestartsAtZero is
// the OTHER direction of the same staleness, and the one a fake or
// restartable provider actually produces: activity.FakeProvider's
// Stop/Start resets its reference time, so its KeystrokeCount comes back
// at 0 (a real evdev provider's, by contrast, keeps climbing — see the
// test above).
//
// Without Reset, `snap.KeystrokeCount > e.lastKeystrokeCount` stays false
// until the restarted counter climbs past the pre-pause value, so real
// post-resume typing would be SILENTLY SWALLOWED — earning nothing and
// reporting `idle` while the user types. Reset makes the restarted
// counter the new baseline, so the second tick back earns normally.
func TestResetPreventsSwallowedWorkWhenTheProviderCounterRestartsAtZero(t *testing.T) {
	p := &stubProvider{honesty: activity.HonestyGlobal}
	e := New(p)

	var keys uint64
	for i := 0; i < 100; i++ {
		keys += 6
		p.snap = activity.Snapshot{KeystrokeCount: keys}
		e.Tick()
	}

	// Control first: without Reset, real typing after the restart earns
	// nothing at all.
	control := New(p)
	control.lastKeystrokeCount, control.initialized = keys, true
	p.snap = activity.Snapshot{KeystrokeCount: 6}
	if r := control.Tick(); r.KeystrokeDelta != 0 || r.WorkUnits != 0 {
		t.Fatalf("control: a restarted-at-zero counter unexpectedly produced %+v — this test can no longer detect the swallowed-work bug", r)
	}

	e.Reset()
	p.snap = activity.Snapshot{KeystrokeCount: 6}
	if r := e.Tick(); r.KeystrokeDelta != 0 || r.WorkUnits != 0 {
		t.Errorf("the first tick after Reset = %+v, want zero work (it is establishing the new baseline)", r)
	}
	p.snap = activity.Snapshot{KeystrokeCount: 12}
	second := e.Tick()
	if second.KeystrokeDelta != 6 {
		t.Errorf("the second tick after Reset reported keystrokeDelta %d, want 6 — post-resume typing must be counted, not swallowed", second.KeystrokeDelta)
	}
	if second.WorkUnits == 0 {
		t.Error("the second tick after Reset earned no work — post-resume typing must earn normally")
	}
	if second.Mood != MoodCoding {
		t.Errorf("the second tick after Reset reported mood %q, want coding — the user really is typing", second.Mood)
	}
}

// TestNoFocusBonusAcrossAPause pins the outcome §PR-5 names ("no focus
// bonus from a pre-pause run") end to end: a run 119 seconds into
// FocusSessionSeconds when the pause began must not complete on the first
// keystroke ten hours later.
//
// Honest note on WHY this passes, so nobody mistakes it for proof of
// Reset alone: FocusGapToleranceSeconds is the first line of defence —
// Tick already breaks a run whose gap since the last counted keystroke
// exceeds the tolerance, and a ten-hour pause exceeds it by any measure.
// Reset is the SECOND, structural line: it clears focusRunActive/
// focusRunStart outright, so the guarantee no longer depends on the
// tolerance rule being reachable on the first tick back (it is evaluated
// only when that tick has keyDelta > 0). This test asserts the property,
// not the mechanism, so it keeps holding if either line is refactored —
// and starts failing if BOTH are.
func TestNoFocusBonusAcrossAPause(t *testing.T) {
	now := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	p := &stubProvider{honesty: activity.HonestyGlobal}
	e := New(p)
	e.now = func() time.Time { return now }

	var keys uint64
	for i := 0; i < 119; i++ {
		keys++
		p.snap = activity.Snapshot{KeystrokeCount: keys}
		if r := e.Tick(); r.FocusSessionsCompleted != 0 {
			t.Fatalf("a focus session completed early, at tick %d — the setup is wrong", i)
		}
		now = now.Add(time.Second)
	}

	e.Reset()
	now = now.Add(10 * time.Hour)
	keys++
	p.snap = activity.Snapshot{KeystrokeCount: keys}
	r := e.Tick()
	if r.FocusSessionsCompleted != 0 {
		t.Error("a focus session completed on the first tick after a ten-hour pause — a 'sustained' run with a ten-hour hole in it is exactly the claim this must never make")
	}
	if r.WorkUnits != 0 {
		t.Errorf("the first tick after the pause earned %v work units, want 0", r.WorkUnits)
	}

	// And a genuine post-resume run still completes normally, so the
	// protection is not simply "focus sessions are broken after a pause".
	for i := 0; i < int(FocusSessionSeconds)+1; i++ {
		keys++
		p.snap = activity.Snapshot{KeystrokeCount: keys}
		r = e.Tick()
		now = now.Add(time.Second)
		if r.FocusSessionsCompleted == 1 {
			return
		}
	}
	t.Error("no focus session completed during a full post-resume sustained run — Reset must clear the run, not disable focus tracking")
}

// TestResetClearsEveryCrossTickField is the maintenance guard: Reset must
// zero every field of Engine that carries meaning ACROSS ticks, so a field
// added later cannot silently survive a pause. It works by reflection over
// the struct rather than by an enumerated list, so "added a field, forgot
// Reset" is a failure here instead of a subtle honesty bug in production.
//
// `provider` and `now` are deliberately EXCLUDED: they are the engine's
// injected collaborators, not state — Reset must not detach the provider
// it samples ("the engine only samples, it never manages the provider's
// lifecycle") nor throw away a test's clock.
func TestResetClearsEveryCrossTickField(t *testing.T) {
	collaborators := map[string]bool{"provider": true, "now": true}

	p := &stubProvider{honesty: activity.HonestyGlobal}
	e := New(p)
	// Drive it into a state where every cross-tick field is non-zero:
	// initialized, a keystroke count and timestamp, an active app, and a
	// live focus run.
	e.now = func() time.Time { return time.Date(2026, 3, 10, 9, 0, 30, 0, time.UTC) }
	p.snap = activity.Snapshot{KeystrokeCount: 1, ActiveApp: "code"}
	e.Tick()
	p.snap = activity.Snapshot{KeystrokeCount: 2, ActiveApp: "code"}
	e.Tick()

	v := reflect.ValueOf(*e)
	typ := v.Type()
	var stillZero []string
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if collaborators[name] {
			continue
		}
		if v.Field(i).IsZero() {
			stillZero = append(stillZero, name)
		}
	}
	if len(stillZero) > 0 {
		t.Fatalf("this test could not make %v non-zero before Reset, so it cannot prove Reset clears them — extend the setup above", stillZero)
	}

	e.Reset()

	after := reflect.ValueOf(*e)
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if collaborators[name] {
			if after.Field(i).IsZero() {
				t.Errorf("Reset cleared %s — the provider and the clock are collaborators, not cross-tick state", name)
			}
			continue
		}
		if !after.Field(i).IsZero() {
			t.Errorf("Reset left %s non-zero (%v) — every field that carries meaning across ticks must be cleared, or it survives a pause and lies on the other side", name, after.Field(i))
		}
	}
}
