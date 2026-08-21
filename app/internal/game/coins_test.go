package game

import (
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/engine"
)

// tr builds an engine.TickResult whose WorkUnits is computed by the exact
// same formula engine.Engine.Tick uses (engine.go) — keystroke+mouse
// compete for the same engine.MaxRecentRate ceiling, then the focus bonus
// and app-switch work are added on top. Building test fixtures this way
// (rather than picking an arbitrary WorkUnits) is what lets the tests
// below assert EXACT coin splits: signalWork (coins.go) reconstructs this
// same decomposition from the other fields, so a tick built by tr() is
// guaranteed internally consistent, the same guarantee a real
// engine.Engine.Tick() call provides.
func tr(keyDelta uint64, mouseActive bool, focus uint64, switches uint64) engine.TickResult {
	weighted := float64(keyDelta) * engine.KeystrokeWeight
	if mouseActive {
		weighted += engine.MouseSustainedRate * engine.MouseWeight
	}
	if weighted > engine.MaxRecentRate {
		weighted = engine.MaxRecentRate
	}
	work := weighted*engine.WorkPerUnitRate +
		engine.FocusSessionBonusWork*float64(focus) +
		engine.AppSwitchWork*float64(switches)

	mood := engine.MoodIdle
	if keyDelta > 0 {
		mood = engine.MoodCoding
	}
	return engine.TickResult{
		Mood:                   mood,
		KeystrokeDelta:         keyDelta,
		MouseActive:            mouseActive,
		FocusSessionsCompleted: focus,
		AppSwitches:            switches,
		WorkUnits:              work,
	}
}

// coinSum totals a CoinBreakdown's four buckets.
func coinSum(cb CoinBreakdown) uint64 {
	return cb.Keystrokes + cb.Mouse + cb.FocusSessions + cb.AppSwitches
}

// TestCoinBreakdownSumsToEarnedDevCash is the CONSERVATION exit criterion
// (docs/plan/A2-design.md §5/§8): coins have exactly one source — sprint
// completion (ADR 0008) — so however CoinsToday is split across signals,
// the four buckets must sum to EXACTLY the DevCash actually earned today,
// never more (double-pay) and never less (a coin vanishing into no
// bucket). Exercised across TWO sprint completions, with a different
// signal mix each time, to prove the invariant holds cumulatively, not
// just on the first payout.
func TestCoinBreakdownSumsToEarnedDevCash(t *testing.T) {
	g := New()

	// Sprint 0 (target 50, pays 25): 401 ticks of max-rate typing (15
	// keys/tick, sitting exactly at engine.MaxRecentRate so it is not
	// clamped) = 401*0.12 ≈ 48.12 work, plus one tick that completes a
	// focus session for +2 more (≈50.12 total) — a deliberate small
	// overshoot rather than landing exactly on 50, so completion doesn't
	// ride the floating-point knife-edge of summing 0.12 exactly 400
	// times (repeated float64 addition of a non-exact-binary decimal
	// drifts fractionally below the mathematically-exact 48.0).
	for i := 0; i < 401; i++ {
		g.Tick(tr(15, false, 0, 0))
	}
	completed := g.Tick(tr(0, false, 1, 0))
	if !completed {
		t.Fatal("expected sprint 0 to complete just past 50 work units")
	}
	if g.DevCash != 25 {
		t.Fatalf("DevCash after sprint 0 = %d, want 25", g.DevCash)
	}

	breakdown := g.State().Stats.CoinsToday
	if got := coinSum(breakdown); got != g.DevCash {
		t.Errorf("CoinsToday sums to %d, want %d (all of sprint 0's DevCash) — got %+v", got, g.DevCash, breakdown)
	}

	// Sprint 1 (target 75, pays 40): this time pure mouse-only work.
	// mouse work/tick = MouseSustainedRate*MouseWeight*WorkPerUnitRate =
	// 10*0.25*0.008 = 0.02; 3750 ticks = 75.0 exactly.
	var completed2 bool
	for i := 0; i < 3750; i++ {
		if g.Tick(tr(0, true, 0, 0)) {
			completed2 = true
		}
	}
	if !completed2 {
		t.Fatal("expected sprint 1 to complete at exactly 75 work units")
	}
	wantTotal := uint64(25 + 40)
	if g.DevCash != wantTotal {
		t.Fatalf("DevCash after sprint 1 = %d, want %d", g.DevCash, wantTotal)
	}

	breakdown = g.State().Stats.CoinsToday
	if got := coinSum(breakdown); got != g.DevCash {
		t.Errorf("CoinsToday sums to %d, want %d (cumulative DevCash across both sprints) — got %+v", got, g.DevCash, breakdown)
	}
}

// TestCoinAttributionIsProportionalToWorkShare pins down the EXACT split,
// not just that it sums correctly: 401 ticks of max-rate typing contribute
// ≈48.12 of the ≈50.12 work units sprint 0 ends up with (≈96%), and one
// final tick's focus-session completion contributes the remaining 2
// (≈4%) — with no mouse or app-switch activity at all. The 25-coin payout
// must therefore land as exactly 24 keystroke coins + 1 focus-session
// coin (24.002.../0.998... before rounding — the largest-remainder method
// gives the focus bucket its coin), proving attribution really is
// proportional to each signal's accrued work share
// (docs/plan/A2-design.md §5), not e.g. an even split or first-signal-wins.
func TestCoinAttributionIsProportionalToWorkShare(t *testing.T) {
	g := New()
	for i := 0; i < 401; i++ {
		g.Tick(tr(15, false, 0, 0))
	}
	if completed := g.Tick(tr(0, false, 1, 0)); !completed {
		t.Fatal("expected sprint 0 to complete just past 50 work units")
	}

	want := CoinBreakdown{Keystrokes: 24, Mouse: 0, FocusSessions: 1, AppSwitches: 0}
	got := g.State().Stats.CoinsToday
	if got != want {
		t.Errorf("CoinsToday = %+v, want %+v (~96%%/4%% work split of the 25-coin payout)", got, want)
	}
}

// TestWorkAccrualGatedByStoreOpenLikeProgress guards against coin
// attribution laundering "shopping" activity into coins: StatCounters
// keep counting while the store is open (docs/ui-spec.md §5.3, and see
// TestStatsAccumulateEvenWhileStoreIsOpen in stats_test.go), but Progress
// itself is frozen — and this test proves the per-signal work
// accumulators backing CoinBreakdown are frozen in exact lockstep, not
// just "eventually reconciled." A flood of keystrokes while the store is
// open, followed by a sprint completed via mouse-only work after closing
// it, must attribute 100% of the payout to Mouse — if the frozen
// keystrokes had leaked into the accumulators, Keystrokes would show a
// nonzero share instead.
func TestWorkAccrualGatedByStoreOpenLikeProgress(t *testing.T) {
	g := New()
	g.OpenStore(1)
	for i := 0; i < 1000; i++ {
		g.Tick(tr(15, false, 0, 0))
	}
	if g.Progress != 0 || g.DevCash != 0 {
		t.Fatalf("Progress/DevCash changed while store open: (%v, %d), want (0, 0)", g.Progress, g.DevCash)
	}
	g.CloseStore(1)

	// mouse work/tick = 0.02; 2500 ticks = 50.0 exactly (sprint 0's target).
	var completed bool
	for i := 0; i < 2500; i++ {
		if g.Tick(tr(0, true, 0, 0)) {
			completed = true
		}
	}
	if !completed {
		t.Fatal("expected sprint 0 to complete at exactly 50 work units of mouse-only work")
	}
	if g.DevCash != 25 {
		t.Fatalf("DevCash = %d, want 25", g.DevCash)
	}

	want := CoinBreakdown{Keystrokes: 0, Mouse: 25, FocusSessions: 0, AppSwitches: 0}
	got := g.State().Stats.CoinsToday
	if got != want {
		t.Errorf("CoinsToday = %+v, want %+v (the frozen shopping keystrokes must not have leaked into the work accumulators)", got, want)
	}
}
