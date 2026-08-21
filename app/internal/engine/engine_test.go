package engine

import (
	"testing"
	"time"

	"github.com/jawwadzafar/dev-companion/app/internal/activity"
)

// stubProvider is a minimal activity.Provider test double: it hands back
// whatever Snapshot the test sets, with no real capture behind it. Used
// instead of activity.FakeProvider here because these tests need exact,
// hand-controlled deltas per tick (a wall-clock-driven fake would make the
// strategy-comparison assertion timing-flaky).
type stubProvider struct {
	snap    activity.Snapshot
	honesty activity.Honesty
}

func (s *stubProvider) Start() error                { return nil }
func (s *stubProvider) Stop() error                 { return nil }
func (s *stubProvider) Snapshot() activity.Snapshot { return s.snap }
func (s *stubProvider) Honesty() activity.Honesty   { return s.honesty }

// TestMouseOnlyEarnsLessThanTyping is the strategy-comparison test ADR 0005
// says history proved essential: a boundedness test ("work units stay
// finite") is not enough, because the OLD economy also passed one before a
// 6x rebalance was needed. This test would have caught that regression: it
// asserts mouse-only-at-maximum-sustained-rate earns strictly less work per
// tick than a realistic typist, by roughly the ADR's own numbers (~21 min
// typing vs. ~42 min mouse-only per 50-work sprint => typing should earn
// noticeably more than 2x per unit time... actually mouse should earn
// roughly HALF of typing's rate).
func TestMouseOnlyEarnsLessThanTyping(t *testing.T) {
	const ticks = 60

	// Typing strategy: a realistic sustained typist, ~6 keys/s (within
	// ADR 0005's cited 5-10 keys/s human range), well under the anti-mash
	// ceiling of 10/s a provider could ever report.
	typingProvider := &stubProvider{honesty: activity.HonestyGlobal}
	typingEngine := New(typingProvider)
	var typingTotal float64
	var keyCount uint64
	for i := 0; i < ticks; i++ {
		keyCount += 6
		typingProvider.snap = activity.Snapshot{KeystrokeCount: keyCount}
		typingTotal += typingEngine.Tick().WorkUnits
	}

	// Mouse-only strategy: continuously flagged mouse-active every single
	// tick — the most favorable case a mouse-mashing script could ever
	// achieve, since the provider's own anti-mash coalescing caps the
	// signal at exactly this rate no matter how fast the input arrives.
	mouseProvider := &stubProvider{honesty: activity.HonestyGlobal}
	mouseEngine := New(mouseProvider)
	var mouseTotal float64
	for i := 0; i < ticks; i++ {
		mouseProvider.snap = activity.Snapshot{MouseActive: true}
		mouseTotal += mouseEngine.Tick().WorkUnits
	}

	if mouseTotal >= typingTotal {
		t.Fatalf("mouse-only (%.6f work) should earn strictly less than typing (%.6f work) over %d ticks",
			mouseTotal, typingTotal, ticks)
	}

	// The ADR's own numbers put typing at roughly double mouse-only's rate
	// (21 min vs 42 min per identical 50-work sprint). Assert the ratio is
	// in the right ballpark (not testing an exact float, just that this
	// isn't an accidental razor-thin win).
	ratio := typingTotal / mouseTotal
	if ratio < 1.5 {
		t.Errorf("expected typing to earn at least 1.5x mouse-only's rate (ADR 0005: ~2x), got ratio %.3f (typing=%.6f mouse=%.6f)",
			ratio, typingTotal, mouseTotal)
	}
}

// TestMouseOnlyNeverReachesTheCeilingAlone confirms mouse-only alone (no
// typing at all) never gets anywhere near MaxRecentRate, i.e. the ceiling
// clamp is not what's holding mouse-only down — the WEIGHT is.
func TestMouseOnlyNeverReachesTheCeilingAlone(t *testing.T) {
	p := &stubProvider{honesty: activity.HonestyGlobal, snap: activity.Snapshot{MouseActive: true}}
	e := New(p)
	r := e.Tick()
	maxPossibleMouseWork := MaxRecentRate * WorkPerUnitRate
	if r.WorkUnits >= maxPossibleMouseWork {
		t.Fatalf("mouse-only work/tick (%.6f) reached the rate ceiling (%.6f) — weighting isn't doing its job", r.WorkUnits, maxPossibleMouseWork)
	}
}

// TestMoodHonestyTable is the mood table ADR 0010 requires: Coding only
// from a recent keystroke, OnBreak only from genuine global idleness on a
// provider that can see it, Idle otherwise — and NEVER OnBreak from a
// blind provider, no matter how large IdleSeconds claims to be.
func TestMoodHonestyTable(t *testing.T) {
	cases := []struct {
		name         string
		honesty      activity.Honesty
		keystrokeAgo time.Duration // 0 means "no keystroke yet"
		idleSeconds  float64
		want         Mood
	}{
		{"fresh keystroke means coding", activity.HonestyGlobal, 2 * time.Second, 0, MoodCoding},
		{"keystroke at the edge of the window still codes", activity.HonestyGlobal, CodingRecencyWindow - time.Millisecond, 0, MoodCoding},
		{"keystroke just past the window does not code", activity.HonestyGlobal, CodingRecencyWindow + time.Second, 40, MoodOnBreak},
		{"mouse alone never codes, and short idle is just idle", activity.HonestyGlobal, 0, 5, MoodIdle},
		{"genuine long global idle is on break", activity.HonestyGlobal, 0, 45, MoodOnBreak},
		{"idle at the exact threshold is not yet on break", activity.HonestyGlobal, 0, OnBreakIdleThreshold.Seconds(), MoodIdle},
		{"blind provider never claims on break, however idle", activity.HonestyBlind, 0, 999999, MoodIdle},
		{"blind provider still recognizes a fresh keystroke as coding", activity.HonestyBlind, 1 * time.Second, 999999, MoodCoding},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &stubProvider{honesty: c.honesty}
			e := New(p)
			fakeNow := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			e.now = func() time.Time { return fakeNow }

			// The engine's first-ever tick never counts a delta (it has no
			// baseline yet — see TestFirstTickNeverAwardsFreeWork), so
			// establish the baseline with a zero-delta warm-up tick first.
			var count uint64
			p.snap = activity.Snapshot{KeystrokeCount: count}
			e.Tick()

			if c.keystrokeAgo > 0 {
				// Register a real keystroke delta at time T, then advance
				// the clock by keystrokeAgo — mirrors "a keystroke happened
				// keystrokeAgo in the past."
				count++
				p.snap = activity.Snapshot{KeystrokeCount: count}
				e.Tick()
				fakeNow = fakeNow.Add(c.keystrokeAgo)
			}
			// The measurement tick must NOT introduce a new keystroke delta
			// (count stays exactly where it was) — otherwise every case
			// would look like "a keystroke just happened right now."
			p.snap = activity.Snapshot{KeystrokeCount: count, IdleSeconds: c.idleSeconds}
			got := e.Tick().Mood
			if got != c.want {
				t.Errorf("mood = %v, want %v", got, c.want)
			}
		})
	}
}

// TestFirstTickNeverAwardsFreeWork guards against a provider that starts
// with an already-nonzero KeystrokeCount (a restart, or a provider that
// began counting before Start()) handing out work the engine never
// observed as a delta.
func TestFirstTickNeverAwardsFreeWork(t *testing.T) {
	p := &stubProvider{honesty: activity.HonestyGlobal, snap: activity.Snapshot{KeystrokeCount: 500}}
	e := New(p)
	r := e.Tick()
	if r.WorkUnits != 0 {
		t.Errorf("first tick awarded %.6f work units from a preexisting counter, want 0", r.WorkUnits)
	}
}
