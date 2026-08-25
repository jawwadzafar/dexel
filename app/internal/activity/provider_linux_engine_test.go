//go:build linux

// This is the one INTEGRATION test for the 2026-08-25 field failure: the
// real engine sampling the real Linux provider (only the OS device layer is
// scripted), proving the invariant the failure broke — idle may only accrue
// while the provider can actually observe input.
//
// It lives in the EXTERNAL activity_test package on purpose: internal/engine
// imports internal/activity, so an in-package test file importing the engine
// would be an import cycle. It drives the provider through the test-only
// seam declared in provider_linux_scripted_test.go.
package activity_test

import (
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/activity"
	"github.com/jawwadzafar/dexel/app/internal/engine"
)

func waitForCond(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, msg)
}

// TestEngineFreezesIdleWhileTheLinuxProviderIsBlind is the regression test
// for what happened on the owner's machine: the provider held fds to
// re-enumerated (dead) /dev/input nodes for 19h48m, read silence, and the
// engine honestly-but-wrongly accrued idle — and then onBreak — from a
// source that could not have seen a keystroke if there had been one.
//
// The four phases below are the honesty state machine as the ENGINE sees it:
// sighted-and-idle really is onBreak; blind is never onBreak, and the idle
// clock does not advance at all; recovery does not retroactively claim the
// blind stretch as idleness; and after recovery the engine counts again.
func TestEngineFreezesIdleWhileTheLinuxProviderIsBlind(t *testing.T) {
	p, devices, _ := activity.NewLinuxProviderWithScriptedDevices("/dev/input/event0", "/dev/input/event1")
	if err := p.Start(); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	defer func() { _ = p.Stop() }()
	waitForCond(t, 2*time.Second, "the scripted devices to open", func() bool {
		return p.OpenDeviceCount() == 2
	})

	eng := engine.New(p)
	eng.Tick() // first tick establishes the engine's baselines

	// Phase 1 — SIGHTED and genuinely idle: the engine must claim onBreak.
	// Without this the rest proves nothing: it is what makes the frozen
	// phase below a real difference in behaviour rather than a mood that
	// never fires anyway.
	p.ForceIdleAge(2 * engine.OnBreakIdleThreshold)
	r := eng.Tick()
	if r.Mood != engine.MoodOnBreak {
		t.Fatalf("sighted provider idle for %s: Mood = %q, want %q", 2*engine.OnBreakIdleThreshold, r.Mood, engine.MoodOnBreak)
	}
	if !r.SeesGlobalInput() {
		t.Fatal("sighted provider: SeesGlobalInput() = false, want true")
	}

	// Some real, observed typing before the devices die — so the recovery
	// phase can prove the engine's keystroke-delta arithmetic survives the
	// blind gap without a windfall.
	for i := 0; i < 3; i++ {
		if !devices.InjectKeystroke("/dev/input/event0") {
			t.Fatal("inject keystroke")
		}
		time.Sleep(2 * activity.MouseSampleInterval)
	}
	waitForCond(t, 2*time.Second, "the typing to be counted", func() bool {
		return p.Snapshot().KeystrokeCount >= 3
	})
	eng.Tick() // consume that delta, so the engine's baseline is up to date

	// Phase 2 — every device dies (GNOME re-enumerating the USB input
	// devices on screen lock). The provider is BLIND, and the engine must
	// freeze: no onBreak claim, and an idle clock that does not advance
	// however long the blind stretch lasts.
	devices.KillAll()
	waitForCond(t, 2*time.Second, "the provider to go blind", func() bool {
		return p.Honesty() == activity.HonestyBlind
	})

	p.ForceIdleAge(19*time.Hour + 48*time.Minute) // the field failure's own duration
	for i := 0; i < 5; i++ {
		r = eng.Tick()
		if r.SeesGlobalInput() {
			t.Errorf("blind provider tick %d: SeesGlobalInput() = true, want false", i)
		}
		if r.Mood == engine.MoodOnBreak {
			t.Errorf("blind provider tick %d: Mood = %q — a provider with zero open input devices cannot know the user is on a break (ADR 0010)", i, r.Mood)
		}
		if idle := p.Snapshot().IdleSeconds; idle != 0 {
			t.Errorf("blind provider tick %d: Snapshot().IdleSeconds = %v, want 0 — idle must not accrue while nothing is being observed", i, idle)
		}
		if r.KeystrokeDelta != 0 || r.WorkUnits != 0 {
			t.Errorf("blind provider tick %d: KeystrokeDelta = %d, WorkUnits = %v, want 0 and 0", i, r.KeystrokeDelta, r.WorkUnits)
		}
		time.Sleep(time.Millisecond)
	}

	// Phase 3 — the devices come back as NEW nodes at the same paths. The
	// provider reopens them on its own (no restart), honesty is restored,
	// and the first sighted tick must NOT claim onBreak: the 19h blind
	// stretch was unobserved, not idle. That is the same ADR 0010 lie, just
	// delayed by a recovery.
	devices.Add("/dev/input/event0")
	devices.Add("/dev/input/event1")
	waitForCond(t, 2*time.Second, "the provider to recover", func() bool {
		return p.Honesty() == activity.HonestyGlobal && p.OpenDeviceCount() == 2
	})

	r = eng.Tick()
	if !r.SeesGlobalInput() {
		t.Error("after recovery: SeesGlobalInput() = false, want true")
	}
	if r.Mood == engine.MoodOnBreak {
		t.Errorf("first tick after recovery: Mood = %q — the blind stretch must not be back-charged as observed idleness", r.Mood)
	}
	if r.KeystrokeDelta != 0 {
		t.Errorf("first tick after recovery: KeystrokeDelta = %d, want 0 — the provider's counter is monotonic across a blind gap, so recovery must not hand out work nobody earned", r.KeystrokeDelta)
	}

	// Phase 4 — counting resumes on the new nodes, and a genuinely idle
	// sighted provider claims onBreak again: the freeze was temporary and
	// scoped to blindness, not a permanent degradation.
	if !devices.InjectKeystroke("/dev/input/event0") {
		t.Fatal("inject into the re-enumerated device")
	}
	waitForCond(t, 2*time.Second, "counting to resume after recovery", func() bool {
		return p.Snapshot().KeystrokeCount >= 4
	})
	r = eng.Tick()
	if r.KeystrokeDelta != 1 {
		t.Errorf("tick after recovery typing: KeystrokeDelta = %d, want exactly 1", r.KeystrokeDelta)
	}
	if r.Mood != engine.MoodCoding {
		t.Errorf("tick after recovery typing: Mood = %q, want %q", r.Mood, engine.MoodCoding)
	}

	// The onBreak rule works again too. Asserted through a FRESH engine
	// rather than by waiting out CodingRecencyWindow on `eng`: a new engine
	// carries no keystroke-recency memory, so this isolates the honesty rule
	// under test (global visibility + real idleness => onBreak) from the
	// separate coding-recency rule, and costs no wall-clock seconds.
	fresh := engine.New(p)
	fresh.Tick() // baseline
	p.ForceIdleAge(2 * engine.OnBreakIdleThreshold)
	if got := fresh.Tick().Mood; got != engine.MoodOnBreak {
		t.Errorf("recovered provider idle for %s: Mood = %q, want %q — onBreak must work again once the provider can see", 2*engine.OnBreakIdleThreshold, got, engine.MoodOnBreak)
	}
}
