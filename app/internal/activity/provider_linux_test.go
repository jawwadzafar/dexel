//go:build linux

package activity

import (
	"testing"
	"time"
)

// TestLinuxProviderCoalescesFloodedInputToSampleInterval is S2's missing
// coverage: before this test, nothing exercised LinuxProvider's own
// anti-mash coalescing at all — every existing engine-level test drives a
// stubProvider that reports whatever KeystrokeCount/MouseActive it's told
// to, bypassing the real coalescing logic entirely. This test feeds
// handleEvent (the same private method readLoop calls per raw evdev
// event) a real wall-clock flood of EV_KEY presses and EV_REL motion —
// as fast as this goroutine can loop, i.e. far faster than
// MouseSampleInterval — and asserts the counted rate stays capped at
// roughly one accepted event per MouseSampleInterval, never one per
// injected event.
func TestLinuxProviderCoalescesFloodedInputToSampleInterval(t *testing.T) {
	p := NewLinuxProvider()

	const floodWindow = 3 * MouseSampleInterval
	deadline := time.Now().Add(floodWindow)

	var keyInjected, mouseInjected int
	mouseAccepts := 0
	lastSeenMouseTick := p.lastMouseTick
	for time.Now().Before(deadline) {
		p.handleEvent(evKey, 30 /* arbitrary code < keyCodeCeiling */, keyPressValue)
		keyInjected++

		p.handleEvent(evRel, 0, 1)
		mouseInjected++
		if !p.lastMouseTick.Equal(lastSeenMouseTick) {
			mouseAccepts++
			lastSeenMouseTick = p.lastMouseTick
		}
	}

	// This test is only meaningful if the flood genuinely outpaced the
	// sample interval by a wide margin — guard against a slow/throttled
	// CI box making the loop itself the bottleneck instead of the
	// coalescing logic being exercised.
	if keyInjected < 500 || mouseInjected < 500 {
		t.Fatalf("test invalid: only injected key=%d mouse=%d events in %s — too slow to exercise coalescing, want >= 500 each", keyInjected, mouseInjected, floodWindow)
	}

	// At most one accepted keystroke/mouse-active window per
	// MouseSampleInterval that elapsed during the flood, plus one for a
	// partial window at the very start.
	maxAccepts := uint64(floodWindow/MouseSampleInterval) + 1

	if p.keystrokeCount > maxAccepts {
		t.Errorf("keystrokeCount = %d after flooding %d EV_KEY events over %s (interval %s) — anti-mash coalescing failed to cap the rate (want <= %d)",
			p.keystrokeCount, keyInjected, floodWindow, MouseSampleInterval, maxAccepts)
	}
	if p.keystrokeCount == 0 {
		t.Error("keystrokeCount = 0 after a real flood of EV_KEY events — coalescing should still count roughly one keystroke per interval, not suppress everything")
	}

	if uint64(mouseAccepts) > maxAccepts {
		t.Errorf("mouse registered %d distinct active windows after flooding %d EV_REL events over %s (interval %s) — anti-mash coalescing failed to cap the rate (want <= %d)",
			mouseAccepts, mouseInjected, floodWindow, MouseSampleInterval, maxAccepts)
	}
	if mouseAccepts == 0 {
		t.Error("mouse never registered as active during a real flood of EV_REL events")
	}
}
