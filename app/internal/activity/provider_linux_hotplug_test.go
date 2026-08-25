//go:build linux

package activity

import (
	"strings"
	"testing"
	"time"
)

// The tests in this file cover the 2026-08-25 field failure directly:
// GNOME's screen-lock power management re-enumerated the USB input devices
// under a 19h-old runtime, every reader goroutine died silently on ENODEV,
// and the provider went on reporting HonestyGlobal with no input at all —
// so the engine accrued idle/onBreak for hours from a source that could not
// see anything. They drive the real provider through the scripted device
// seam (see provider_linux_scripted_test.go): real readLoop, real rescan
// loop, real honesty logic, fake OS.

func startScripted(t *testing.T, paths ...string) (*LinuxProvider, *ScriptedDevices, *LogSink) {
	t.Helper()
	p, devices, sink := NewLinuxProviderWithScriptedDevices(paths...)
	if err := p.Start(); err != nil {
		t.Fatalf("Start() with %d scripted devices: %v", len(paths), err)
	}
	t.Cleanup(func() { _ = p.Stop() })
	waitFor(t, 2*time.Second, "all scripted devices to be open", func() bool {
		return p.OpenDeviceCount() == len(paths)
	})
	return p, devices, sink
}

// TestLinuxProviderDropsDeadDeviceAndRescans is defect #1: a device whose
// read fails must be dropped from the open set (not left in it as a dead
// fd), closed, and followed by a rescan.
func TestLinuxProviderDropsDeadDeviceAndRescans(t *testing.T) {
	p, devices, sink := startScripted(t, "/dev/input/event0", "/dev/input/event1")

	devices.Kill("/dev/input/event1")

	waitFor(t, 2*time.Second, "the dead device to be dropped", func() bool {
		return p.OpenDeviceCount() == 1
	})

	// The surviving device is still read from: partial visibility keeps
	// working rather than tearing the whole provider down.
	before := p.KeystrokeCount()
	if !devices.Inject("/dev/input/event0", rawEvent(evKey, 30, keyPressValue)) {
		t.Fatal("could not inject a keystroke into the surviving device")
	}
	waitFor(t, 2*time.Second, "the surviving device to still count keystrokes", func() bool {
		return p.KeystrokeCount() > before
	})

	if got := p.Honesty(); got != HonestyGlobal {
		t.Errorf("Honesty() with 1 of 2 devices alive = %v, want HonestyGlobal — a routine unplug must not freeze earning for a user who is visibly typing", got)
	}

	// The dead fd was closed, and exactly one line reported it.
	if live, open := devices.LiveHandles(), p.OpenDeviceCount(); live != open {
		t.Errorf("scripted fd bookkeeping: %d handles live but %d devices open — the dead device's fd was not closed", live, open)
	}
	if n := sink.Count("died"); n != 1 {
		t.Errorf("device death logged %d times, want exactly 1 line: %q", n, sink.Lines())
	}

	// Content-freedom of what this provider logs (ADR 0002): counts, and at
	// most a /dev/input/eventN device path. A device's own reported NAME
	// (EVIOCGNAME) is exactly the kind of thing a debugging session would
	// reach for and must never appear, so the assertion is structural — any
	// /dev-ish token in any line has to be an event-node path.
	for _, line := range sink.Lines() {
		for _, tok := range strings.Fields(line) {
			if strings.HasPrefix(tok, "/dev") && !strings.HasPrefix(tok, "/dev/input/event") {
				t.Errorf("log line names something other than an event node (%q): %q", tok, line)
			}
		}
	}
}

// TestLinuxProviderGoesBlindWhenEveryDeviceDies is defect #2 at the
// provider level: with ZERO devices open the provider cannot see input, so
// it must say HonestyBlind and must stop advancing the idle clock. Reading
// silence from dead devices as "the user is idle" is the exact ADR 0010
// lie this fix exists to kill.
func TestLinuxProviderGoesBlindWhenEveryDeviceDies(t *testing.T) {
	p, devices, sink := startScripted(t, "/dev/input/event0", "/dev/input/event1")

	if got := p.Honesty(); got != HonestyGlobal {
		t.Fatalf("Honesty() with devices open = %v, want HonestyGlobal", got)
	}

	devices.KillAll()

	waitFor(t, 2*time.Second, "the provider to go blind", func() bool {
		return p.Honesty() == HonestyBlind
	})
	if got := p.OpenDeviceCount(); got != 0 {
		t.Errorf("open device count while blind = %d, want 0", got)
	}

	// The idle clock is FROZEN while blind, however much time passes: a
	// provider that cannot see input has not observed idleness.
	p.ForceIdleAge(19*time.Hour + 48*time.Minute) // the field failure's own duration
	snap := p.Snapshot()
	if snap.IdleSeconds != 0 {
		t.Errorf("blind Snapshot().IdleSeconds = %v, want 0 — unobserved time is not idleness", snap.IdleSeconds)
	}
	if snap.MouseActive {
		t.Error("blind Snapshot().MouseActive = true, want false")
	}

	// Every fd was closed on the way down.
	if live := devices.LiveHandles(); live != 0 {
		t.Errorf("%d scripted fds still live after every device died, want 0", live)
	}
	if n := sink.Count("BLIND"); n != 1 {
		t.Errorf("going blind logged %d times, want exactly 1: %q", n, sink.Lines())
	}
}

// TestLinuxProviderKeystrokeCounterStaysMonotonicWhileBlind guards the
// engine's delta arithmetic across a blind stretch. The engine derives work
// from KeystrokeCount deltas, so a blind Snapshot that zeroed the counter
// would make the FIRST sighted tick after recovery look like thousands of
// keystrokes arrived in one second — free work units nobody earned.
func TestLinuxProviderKeystrokeCounterStaysMonotonicWhileBlind(t *testing.T) {
	p, devices, _ := startScripted(t, "/dev/input/event0")

	for i := 0; i < 3; i++ {
		if !devices.InjectKeystroke("/dev/input/event0") {
			t.Fatal("inject keystroke")
		}
		time.Sleep(2 * linuxSampleInterval)
	}
	waitFor(t, 2*time.Second, "keystrokes to be counted", func() bool {
		return p.Snapshot().KeystrokeCount >= 3
	})
	earned := p.Snapshot().KeystrokeCount

	devices.KillAll()
	waitFor(t, 2*time.Second, "the provider to go blind", func() bool {
		return p.Honesty() == HonestyBlind
	})

	if got := p.Snapshot().KeystrokeCount; got != earned {
		t.Errorf("blind Snapshot().KeystrokeCount = %d, want the %d already observed (monotonic) — zeroing it hands the engine a bogus delta on recovery", got, earned)
	}
}

// TestLinuxProviderRecoversAfterReEnumeration is the whole field scenario
// end to end: the nodes die, NEW nodes appear at the SAME paths (a new
// inode/rdev each), and the provider must reopen them without a restart,
// restore HonestyGlobal, resume counting, restart the idle clock from
// recovery (never claiming the blind gap as observed idleness), and hold
// exactly one fd per node.
func TestLinuxProviderRecoversAfterReEnumeration(t *testing.T) {
	p, devices, sink := startScripted(t, "/dev/input/event0", "/dev/input/event1")

	devices.KillAll()
	waitFor(t, 2*time.Second, "the provider to go blind", func() bool {
		return p.Honesty() == HonestyBlind
	})
	p.ForceIdleAge(time.Hour) // blind time, unobserved

	// GNOME re-enumerated: same paths, brand new node instances.
	devices.Add("/dev/input/event0")
	devices.Add("/dev/input/event1")

	waitFor(t, 2*time.Second, "the re-enumerated devices to be reopened", func() bool {
		return p.OpenDeviceCount() == 2
	})
	if got := p.Honesty(); got != HonestyGlobal {
		t.Errorf("Honesty() after recovery = %v, want HonestyGlobal", got)
	}

	// The idle clock restarted at recovery rather than reporting the blind
	// hour it could not see.
	if idle := p.Snapshot().IdleSeconds; idle > 5 {
		t.Errorf("Snapshot().IdleSeconds right after recovery = %v, want ~0 — the blind stretch must not be reported as observed idleness", idle)
	}

	// Counting resumed on the NEW nodes.
	before := p.KeystrokeCount()
	if !devices.InjectKeystroke("/dev/input/event0") {
		t.Fatal("inject into the re-enumerated device")
	}
	waitFor(t, 2*time.Second, "counting to resume on the new nodes", func() bool {
		return p.KeystrokeCount() > before
	})

	// One fd per open node, no double-open of any node instance, and the
	// old (dead) fds are gone.
	if live, open := devices.LiveHandles(), p.OpenDeviceCount(); live != open {
		t.Errorf("scripted fd bookkeeping: %d handles live but %d devices open — fd leak across a re-enumeration cycle", live, open)
	}
	if got := devices.OpensPerNode(); got != 1 {
		t.Errorf("some node instance was opened %d times, want 1 — the open set must be keyed by node instance so a rescan never double-opens", got)
	}
	if n := sink.Count("RECOVERED"); n != 1 {
		t.Errorf("recovery logged %d times, want exactly 1: %q", n, sink.Lines())
	}
}

// TestLinuxProviderSurvivesRepeatedDeathRecoveryCycles is the stability
// half: the failure in the field was a suspend/lock cycle, which happens
// many times a day. Ten cycles must leave the provider in exactly the same
// state as one, with no fd growth and no reader goroutine pile-up (the
// -race run and Stop()'s wg.Wait() catch the latter).
func TestLinuxProviderSurvivesRepeatedDeathRecoveryCycles(t *testing.T) {
	p, devices, _ := startScripted(t, "/dev/input/event0", "/dev/input/event1")

	for cycle := 0; cycle < 10; cycle++ {
		devices.KillAll()
		waitFor(t, 2*time.Second, "blind after kill", func() bool {
			return p.Honesty() == HonestyBlind && devices.LiveHandles() == 0
		})

		devices.Add("/dev/input/event0")
		devices.Add("/dev/input/event1")
		waitFor(t, 2*time.Second, "recovery after re-enumeration", func() bool {
			return p.OpenDeviceCount() == 2 && p.Honesty() == HonestyGlobal
		})

		if live := devices.LiveHandles(); live != 2 {
			t.Fatalf("cycle %d: %d scripted fds live, want exactly 2 (one per open node)", cycle, live)
		}
		if got := devices.OpensPerNode(); got != 1 {
			t.Fatalf("cycle %d: a node instance was opened %d times, want 1", cycle, got)
		}
	}

	before := p.KeystrokeCount()
	if !devices.InjectKeystroke("/dev/input/event1") {
		t.Fatal("inject after 10 cycles")
	}
	waitFor(t, 2*time.Second, "counting to still work after 10 cycles", func() bool {
		return p.KeystrokeCount() > before
	})
}

// TestLinuxProviderStartsBlindAndRecoversWhenADeviceAppears covers the
// other real shape of "no devices": the process starts before the device
// set exists or is readable (no 'input' group, udev not settled, a dock not
// plugged in yet). Start must report the failure, stay BLIND rather than
// pretending to see, and still recover on its own when a node shows up —
// no restart needed.
func TestLinuxProviderStartsBlindAndRecoversWhenADeviceAppears(t *testing.T) {
	p, devices, _ := NewLinuxProviderWithScriptedDevices()
	err := p.Start()
	t.Cleanup(func() { _ = p.Stop() })
	if err == nil {
		t.Fatal("Start() with zero devices returned nil error, want a descriptive one")
	}
	if got := p.Honesty(); got != HonestyBlind {
		t.Errorf("Honesty() with zero devices = %v, want HonestyBlind", got)
	}
	if snap := p.Snapshot(); snap.IdleSeconds != 0 || snap.MouseActive {
		t.Errorf("blind Snapshot() = %+v, want a frozen idle clock and no mouse", snap)
	}

	devices.Add("/dev/input/event0")
	waitFor(t, 2*time.Second, "the late device to be picked up", func() bool {
		return p.Honesty() == HonestyGlobal && p.OpenDeviceCount() == 1
	})
}

// TestLinuxProviderPicksUpANewlyPluggedDevice: the periodic rescan is not
// only a repair mechanism — it is also how a keyboard plugged in after
// launch starts being counted, which the open-once-at-Start provider never
// did either.
func TestLinuxProviderPicksUpANewlyPluggedDevice(t *testing.T) {
	p, devices, _ := startScripted(t, "/dev/input/event0")

	devices.Add("/dev/input/event5")
	waitFor(t, 2*time.Second, "the newly plugged device to be opened", func() bool {
		return p.OpenDeviceCount() == 2
	})

	before := p.KeystrokeCount()
	if !devices.InjectKeystroke("/dev/input/event5") {
		t.Fatal("inject into the newly plugged device")
	}
	waitFor(t, 2*time.Second, "the new device's input to be counted", func() bool {
		return p.KeystrokeCount() > before
	})
	if got := devices.OpensPerNode(); got != 1 {
		t.Errorf("a node instance was opened %d times across repeated rescans, want 1", got)
	}
}

// TestLinuxProviderBrokenDeviceDoesNotSpin is the failure mode the repair
// mechanism could itself introduce: a node that stays in /dev/input and
// opens fine but whose every read fails would otherwise cycle
// death -> rescan -> reopen -> death as fast as the machine allows, burning
// CPU and writing a log line each time. The reopen backoff must bound both.
func TestLinuxProviderBrokenDeviceDoesNotSpin(t *testing.T) {
	p, devices, sink := startScripted(t, "/dev/input/event0", "/dev/input/event1")

	devices.Break("/dev/input/event1") // still enumerated, reads always fail

	waitFor(t, 2*time.Second, "the broken device to be dropped", func() bool {
		return p.OpenDeviceCount() == 1
	})
	// Long enough for ~60 rescan ticks at the test's 5ms interval.
	time.Sleep(300 * time.Millisecond)

	// One immediate retry is expected (a transient error should self-heal);
	// what must NOT happen is an unbounded reopen loop.
	if got := devices.OpensPerNode(); got > 2 {
		t.Errorf("a broken node was opened %d times in 300ms — the reopen backoff is not bounding the death/rescan cycle", got)
	}
	if n := sink.Count("died"); n > 2 {
		t.Errorf("a broken node logged %d death lines in 300ms, want at most 2 (one per accepted retry): %q", n, sink.Lines())
	}
	// The healthy device is untouched by its neighbour's flapping.
	if got := p.OpenDeviceCount(); got != 1 {
		t.Errorf("open device count = %d, want 1 (the healthy device stays open)", got)
	}
	if got := p.Honesty(); got != HonestyGlobal {
		t.Errorf("Honesty() with one healthy device = %v, want HonestyGlobal", got)
	}
}

// TestLinuxProviderStopReleasesEverything: Stop must close every fd, join
// every goroutine (a leaked reader would keep an fd and keep writing to
// provider state after Stop), and report BLIND afterwards — a stopped
// provider observes nothing, which is what pause means (PR-5).
func TestLinuxProviderStopReleasesEverything(t *testing.T) {
	p, devices, sink := NewLinuxProviderWithScriptedDevices("/dev/input/event0", "/dev/input/event1")
	if err := p.Start(); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	waitFor(t, 2*time.Second, "devices to open", func() bool { return p.OpenDeviceCount() == 2 })

	if err := p.Stop(); err != nil {
		t.Fatalf("Stop(): %v", err)
	}
	if live := devices.LiveHandles(); live != 0 {
		t.Errorf("%d scripted fds live after Stop(), want 0", live)
	}
	if got := p.Honesty(); got != HonestyBlind {
		t.Errorf("Honesty() after Stop() = %v, want HonestyBlind — a stopped provider sees nothing", got)
	}
	// Stop's own fd closes are not device deaths and must not be logged as
	// such: the shutdown path closes the fds itself, so a "died" line there
	// would be noise in the log on every pause.
	if n := sink.Count("died"); n != 0 {
		t.Errorf("Stop() logged %d device-death lines, want 0: %q", n, sink.Lines())
	}

	// And a restart works (pause -> resume), with a fresh device set.
	if err := p.Start(); err != nil {
		t.Fatalf("Start() after Stop(): %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })
	waitFor(t, 2*time.Second, "devices to reopen after restart", func() bool {
		return p.OpenDeviceCount() == 2 && p.Honesty() == HonestyGlobal
	})
	if live := devices.LiveHandles(); live != 2 {
		t.Errorf("%d scripted fds live after restart, want 2", live)
	}
}
