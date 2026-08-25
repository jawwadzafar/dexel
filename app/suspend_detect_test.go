package main

import (
	"testing"
	"time"
)

// TestSuspendGapExceeded is BUGS-RESILIENCE.md R3/R4's detector, tested as
// the pure function it was extracted into. A real suspend cannot be staged
// from a test — Go offers no way to hand a time.Time a monotonic reading
// that disagrees with its wall reading — so the two deltas the tick loop
// measures are passed in directly, which is exactly the pair the loop
// computes from one time.Now() per tick.
//
// The load-bearing row is "normal operation": both clocks advance together
// at the 1 Hz tick cadence, the gap is zero, and the detector must NOT
// fire. Nothing about non-suspend behaviour may change, and eng.Reset()
// throws away the engine's keystroke baseline and focus run, so a
// false positive would cost real, earned work.
func TestSuspendGapExceeded(t *testing.T) {
	for _, tc := range []struct {
		name       string
		wall, mono time.Duration
		wantGap    time.Duration
		wantFire   bool
	}{
		{
			name: "normal 1Hz tick: both clocks agree",
			wall: tickInterval, mono: tickInterval,
			wantGap: 0, wantFire: false,
		},
		{
			name: "a late tick under load: both clocks lag together",
			wall: 4 * time.Second, mono: 4 * time.Second,
			wantGap: 0, wantFire: false,
		},
		{
			name: "sub-millisecond scheduling jitter between the two reads",
			wall: tickInterval + 300*time.Microsecond, mono: tickInterval,
			wantGap: 300 * time.Microsecond, wantFire: false,
		},
		{
			name: "a 59s gap stays under the threshold",
			wall: 60 * time.Second, mono: time.Second,
			wantGap: 59 * time.Second, wantFire: false,
		},
		{
			name: "a 5-minute nap: wall moved, the monotonic clock did not",
			wall: 5*time.Minute + time.Second, mono: time.Second,
			wantGap: 5 * time.Minute, wantFire: true,
		},
		{
			name: "an overnight suspend",
			wall: 8*time.Hour + time.Second, mono: time.Second,
			wantGap: 8 * time.Hour, wantFire: true,
		},
		{
			name: "a large forward NTP/RTC step with no suspend at all",
			wall: time.Hour + tickInterval, mono: tickInterval,
			wantGap: time.Hour, wantFire: true,
		},
		{
			name: "a backward wall step never fires: engine state is purely monotonic",
			wall: -3 * time.Hour, mono: tickInterval,
			wantGap: -3*time.Hour - tickInterval, wantFire: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gap, fire := suspendGapExceeded(tc.wall, tc.mono, suspendGapThreshold)
			if gap != tc.wantGap {
				t.Errorf("gap = %v, want %v", gap, tc.wantGap)
			}
			if fire != tc.wantFire {
				t.Errorf("fired = %v, want %v (wall %v, mono %v, threshold %v)", fire, tc.wantFire, tc.wall, tc.mono, suspendGapThreshold)
			}
		})
	}
}

// TestSuspendGapMeasuredFromOneTimeNowIsZero is the other half of "never
// fires in normal operation", measured the way the tick loop actually
// measures it: from real time.Now() readings, one stripped of its
// monotonic reading (Round(0)) and one not. Over a real, tiny interval the
// two deltas must agree to well under the threshold — if Round(0)/Sub did
// not behave as R3's fix assumes, this is where it would show.
func TestSuspendGapMeasuredFromOneTimeNowIsZero(t *testing.T) {
	prev := time.Now()
	prevWall := prev.Round(0)
	for i := 0; i < 200; i++ {
		now := time.Now()
		gap, fire := suspendGapExceeded(now.Round(0).Sub(prevWall), now.Sub(prev), suspendGapThreshold)
		if fire {
			t.Fatalf("detector fired on a live, un-suspended interval (gap %v)", gap)
		}
		if gap > time.Millisecond || gap < -time.Millisecond {
			t.Fatalf("wall−mono gap = %v over a live interval, want ~0", gap)
		}
		prev, prevWall = now, now.Round(0)
	}
}
