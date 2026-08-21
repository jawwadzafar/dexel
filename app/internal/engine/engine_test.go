package engine

import (
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/activity"
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

// TestMaxRecentRateIsABackstopNotASilentCap is S2's missing coverage: it
// asserts MaxRecentRate (15) genuinely sits ABOVE the highest weighted
// rate a REAL, honestly-coalesced provider could ever hand Tick() —
// max sustained keystrokes/sec (bounded by activity.MouseSampleInterval's
// coalescing, one counted keystroke per interval) plus a continuously
// mouse-active signal (MouseSustainedRate, the same coalescing cap applied
// to mouse). If MaxRecentRate ever regressed to sit AT or BELOW that real
// achievable rate, the ceiling would silently become the thing capping
// ordinary fast-but-honest typing+mousing, rather than existing purely as
// a backstop against a fabricated/scripted signal claiming a rate no real
// coalesced provider could produce.
func TestMaxRecentRateIsABackstopNotASilentCap(t *testing.T) {
	maxRealKeystrokesPerSecond := 1.0 / activity.MouseSampleInterval.Seconds()
	maxRealWeightedRate := maxRealKeystrokesPerSecond*KeystrokeWeight + MouseSustainedRate*MouseWeight

	if maxRealWeightedRate >= MaxRecentRate {
		t.Fatalf("max real achievable weighted rate (%.3f, from %.3f keystrokes/s + mouse) >= MaxRecentRate (%.3f) — the ceiling would silently clamp honest max-speed input instead of only backstopping fabricated rates",
			maxRealWeightedRate, maxRealKeystrokesPerSecond, MaxRecentRate)
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

// strategyTotals accumulates one simulated strategy's results over the
// fixed comparison window.
type strategyTotals struct {
	work          float64
	maxTickWork   float64
	focusSessions uint64
	keyDelta      uint64
	appSwitches   uint64
}

// runStrategy drives a fresh Engine for windowTicks 1-second ticks, one
// simulated wall-clock second apart (matching the 1s cadence main.go
// drives in production — see engine.go's Tick doc), feeding genSnap(i)'s
// Snapshot at tick i.
func runStrategy(windowTicks int, genSnap func(tick int) activity.Snapshot) strategyTotals {
	p := &stubProvider{honesty: activity.HonestyGlobal}
	e := New(p)
	fakeNow := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return fakeNow }

	var totals strategyTotals
	for i := 0; i < windowTicks; i++ {
		p.snap = genSnap(i)
		r := e.Tick()
		totals.work += r.WorkUnits
		if r.WorkUnits > totals.maxTickWork {
			totals.maxTickWork = r.WorkUnits
		}
		totals.focusSessions += r.FocusSessionsCompleted
		totals.keyDelta += r.KeystrokeDelta
		totals.appSwitches += r.AppSwitches
		fakeNow = fakeNow.Add(time.Second)
	}
	return totals
}

// TestStrategyComparisonA2 is docs/plan/A2-design.md §4's strategy-
// comparison table — the test the design doc says implementers MUST add.
// It extends TestMouseOnlyEarnsLessThanTyping (kept green, unchanged
// above) to prove the A2 economy stays diversified and anti-mash-safe:
//   - deepFocus > steadyTypist: the focus-session bonus only ADDS on top
//     of real typing, it never substitutes for it.
//   - steadyTypist > mouseOnly: ADR 0005's typing>mouse invariant,
//     RE-PROVEN under the A2 economy (steadyTypist never completes a
//     focus session — its 10s-off gaps exceed FocusGapToleranceSeconds
//     and its 30s-on bursts never reach FocusSessionSeconds — so this
//     comparison isolates the pre-A2 keystroke-vs-mouse weighting).
//   - appSwitchMasher == idle == 0: at the shipped default
//     (AppSwitchWork = 0.0, Fork B1) an app-switch masher earns nothing,
//     identical to doing nothing at all.
//   - mouse can never trigger a focus session (mouseOnly.focusSessions
//     stays 0), proving the mouse<typing invariant holds BY CONSTRUCTION,
//     not merely by the weight numbers.
func TestStrategyComparisonA2(t *testing.T) {
	const windowTicks = 300

	// deepFocus: a realistic 6 keys/s typist, CONTINUOUSLY, for the whole
	// window — long enough to complete multiple FocusSessionSeconds runs.
	var deepFocusKeys uint64
	deepFocus := runStrategy(windowTicks, func(tick int) activity.Snapshot {
		deepFocusKeys += 6
		return activity.Snapshot{KeystrokeCount: deepFocusKeys}
	})

	// steadyTypist: the SAME 6 keys/s rate, but in 30s-on/10s-off cycles.
	// The 10s gap exceeds FocusGapToleranceSeconds (breaking any run), and
	// no single on-burst reaches FocusSessionSeconds — so it should never
	// earn the focus bonus, only the plain keystroke work ADR 0005 already
	// priced.
	var steadyKeys uint64
	steadyTypist := runStrategy(windowTicks, func(tick int) activity.Snapshot {
		if tick%40 < 30 { // 30 ticks on, 10 ticks off, repeating
			steadyKeys += 6
		}
		return activity.Snapshot{KeystrokeCount: steadyKeys}
	})

	// mouseOnly: mouse-active every tick, no keys ever — the most
	// favorable case a mouse-mashing script could achieve (§ existing
	// TestMouseOnlyEarnsLessThanTyping above).
	mouseOnly := runStrategy(windowTicks, func(tick int) activity.Snapshot {
		return activity.Snapshot{MouseActive: true}
	})

	// appSwitchMasher: ActiveApp changes every single tick, no keys, no
	// mouse. At the default AppSwitchWork = 0.0 (Fork B1) this must earn
	// nothing — same as idle.
	appSwitchMasher := runStrategy(windowTicks, func(tick int) activity.Snapshot {
		if tick%2 == 0 {
			return activity.Snapshot{ActiveApp: "app-a"}
		}
		return activity.Snapshot{ActiveApp: "app-b"}
	})

	// idle: nothing happens, ever.
	idle := runStrategy(windowTicks, func(tick int) activity.Snapshot {
		return activity.Snapshot{}
	})

	t.Logf("strategy ordering over %d ticks: deepFocus=%.6f (focusSessions=%d) > steadyTypist=%.6f > mouseOnly=%.6f > appSwitchMasher=%.6f == idle=%.6f",
		windowTicks, deepFocus.work, deepFocus.focusSessions, steadyTypist.work, mouseOnly.work, appSwitchMasher.work, idle.work)

	// Sanity: the fixture actually exercised what it claims to.
	if deepFocus.focusSessions == 0 {
		t.Fatalf("deepFocus completed 0 focus sessions over %d ticks — fixture never exercised the bonus it's meant to test", windowTicks)
	}
	if steadyTypist.focusSessions != 0 {
		t.Errorf("steadyTypist should never complete a focus session (30s-on/10s-off never sustains %gs continuous typing), got %d completed", FocusSessionSeconds, steadyTypist.focusSessions)
	}
	if mouseOnly.focusSessions != 0 {
		t.Errorf("mouseOnly completed %d focus session(s) — mouse must NEVER be able to trigger one (mouse never sets keyDelta)", mouseOnly.focusSessions)
	}

	// The required ordering, per §4's table.
	if !(deepFocus.work > steadyTypist.work) {
		t.Errorf("deepFocus (%.6f) should earn strictly more than steadyTypist (%.6f) — the focus bonus must ADD to real typing", deepFocus.work, steadyTypist.work)
	}
	if !(steadyTypist.work > mouseOnly.work) {
		t.Errorf("steadyTypist (%.6f) should earn strictly more than mouseOnly (%.6f) — ADR 0005's typing>mouse invariant must still hold under the A2 economy", steadyTypist.work, mouseOnly.work)
	}
	if appSwitchMasher.work != 0 {
		t.Errorf("appSwitchMasher should earn exactly 0 at the default AppSwitchWork=0.0 (Fork B1), got %.6f", appSwitchMasher.work)
	}
	if idle.work != 0 {
		t.Errorf("idle should earn exactly 0, got %.6f", idle.work)
	}
	if appSwitchMasher.work != idle.work {
		t.Errorf("appSwitchMasher (%.6f) should equal idle (%.6f) at the default app-switch weight — a switch-masher must not out-earn doing nothing", appSwitchMasher.work, idle.work)
	}

	// The focus bonus should be visible and self-consistent: deepFocus's
	// total must equal its plain keystroke work (no mouse/app-switch
	// contribution in this strategy) plus exactly
	// completedSessions*FocusSessionBonusWork — proving the bonus really
	// padded the total rather than the ordering above being an artifact
	// of the ceiling clamp (6 keys/s is well under MaxRecentRate, so no
	// clamping occurs here).
	keystrokeOnlyWork := float64(deepFocus.keyDelta) * KeystrokeWeight * WorkPerUnitRate
	wantWork := keystrokeOnlyWork + float64(deepFocus.focusSessions)*FocusSessionBonusWork
	if diff := deepFocus.work - wantWork; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("deepFocus total work %.6f != keystroke work %.6f + bonus %.6f (%d session(s) * %.2f) = %.6f",
			deepFocus.work, keystrokeOnlyWork, float64(deepFocus.focusSessions)*FocusSessionBonusWork, deepFocus.focusSessions, FocusSessionBonusWork, wantWork)
	}

	// No non-focus-completing strategy should ever exceed the pre-A2
	// per-tick ceiling (MaxRecentRate*WorkPerUnitRate) — only a tick that
	// completes a focus session may exceed it, and only by the bonus.
	preA2Ceiling := MaxRecentRate * WorkPerUnitRate
	for name, s := range map[string]strategyTotals{
		"steadyTypist":    steadyTypist,
		"mouseOnly":       mouseOnly,
		"appSwitchMasher": appSwitchMasher,
		"idle":            idle,
	} {
		if s.maxTickWork > preA2Ceiling+1e-9 {
			t.Errorf("%s's single-tick work (%.6f) exceeded the pre-A2 ceiling (%.6f) without ever completing a focus session", name, s.maxTickWork, preA2Ceiling)
		}
	}
}

// TestFocusRunSecondsGrowsAndResetsOnBreak is the A3 Fork B / GO-0 coverage
// (docs/plan/A3-design.md §7 pinned contract): TickResult.FocusRunSeconds
// must track the length of the CURRENT sustained-typing run — growing tick
// by tick while typing continues within FocusGapToleranceSeconds, and
// dropping back to exactly 0 the moment a gap exceeding
// FocusGapToleranceSeconds breaks the run. It must stay well clear of
// FocusSessionSeconds so the completion-reset branch (which restarts the
// run tracker for the NEXT session count) never fires and confuses the
// growth assertion.
func TestFocusRunSecondsGrowsAndResetsOnBreak(t *testing.T) {
	p := &stubProvider{honesty: activity.HonestyGlobal}
	e := New(p)
	fakeNow := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return fakeNow }

	var keyCount uint64
	tick := func(typed bool) TickResult {
		if typed {
			keyCount++
		}
		p.snap = activity.Snapshot{KeystrokeCount: keyCount}
		r := e.Tick()
		fakeNow = fakeNow.Add(time.Second)
		return r
	}

	// No run yet: idle ticks report FocusRunSeconds == 0.
	if r := tick(false); r.FocusRunSeconds != 0 {
		t.Fatalf("idle tick before any typing: FocusRunSeconds = %d, want 0", r.FocusRunSeconds)
	}

	// A run starts on the first typed tick; FocusRunSeconds must grow by
	// ~1 each subsequent typed tick (1s apart, well under
	// FocusGapToleranceSeconds and far short of FocusSessionSeconds).
	r := tick(true) // run starts this tick
	if r.FocusRunSeconds != 0 {
		t.Fatalf("run-start tick: FocusRunSeconds = %d, want 0 (no elapsed time yet)", r.FocusRunSeconds)
	}
	const sustainedTicks = 10
	if sustainedTicks >= int(FocusSessionSeconds) {
		t.Fatalf("test fixture invalid: sustainedTicks (%d) must stay under FocusSessionSeconds (%g) to avoid the completion-reset branch", sustainedTicks, FocusSessionSeconds)
	}
	for i := 1; i <= sustainedTicks; i++ {
		r = tick(true)
		if r.FocusRunSeconds != uint64(i) {
			t.Errorf("sustained-typing tick %d: FocusRunSeconds = %d, want %d", i, r.FocusRunSeconds, i)
		}
	}

	// A gap exceeding FocusGapToleranceSeconds breaks the run: advance the
	// clock past tolerance with no keystroke, then the next tick (typed or
	// not) must observe the run already broken.
	fakeNow = fakeNow.Add(time.Duration(FocusGapToleranceSeconds*1000+1) * time.Millisecond)
	r = tick(false)
	if r.FocusRunSeconds != 0 {
		t.Errorf("after a gap > FocusGapToleranceSeconds: FocusRunSeconds = %d, want 0 (run broken)", r.FocusRunSeconds)
	}
	if r.FocusSessionsCompleted != 0 {
		t.Fatalf("test fixture invalid: broke-run tick unexpectedly completed a focus session")
	}

	// And a fresh typed tick after the break starts a brand-new run at 0,
	// not a continuation of the old one.
	r = tick(true)
	if r.FocusRunSeconds != 0 {
		t.Errorf("first typed tick after a break: FocusRunSeconds = %d, want 0 (new run, not a continuation)", r.FocusRunSeconds)
	}
}
