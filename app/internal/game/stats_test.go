package game

import (
	"testing"
	"time"

	"github.com/jawwadzafar/dev-companion/app/internal/engine"
)

// TestTickAccumulatesDailyAndLifetimeStats is Phase A1's (Analytics track,
// docs/plan/ROADMAP.md) core accumulation test: keystrokes, mouse-active
// seconds, active/idle seconds, and sprints completed all land in both the
// `today` and `lifetime` buckets, summed across ticks.
func TestTickAccumulatesDailyAndLifetimeStats(t *testing.T) {
	g := New()
	fakeNow := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return fakeNow }

	g.Tick(engine.TickResult{Mood: engine.MoodCoding, KeystrokeDelta: 5, MouseActive: false, WorkUnits: 0})
	g.Tick(engine.TickResult{Mood: engine.MoodIdle, KeystrokeDelta: 0, MouseActive: true, WorkUnits: 0})
	g.Tick(engine.TickResult{Mood: engine.MoodOnBreak, KeystrokeDelta: 3, MouseActive: true, WorkUnits: 0})

	today := g.State().Stats.Today
	if today.Keystrokes != 8 {
		t.Errorf("today.Keystrokes = %d, want 8", today.Keystrokes)
	}
	if today.MouseActiveSeconds != 2 {
		t.Errorf("today.MouseActiveSeconds = %d, want 2", today.MouseActiveSeconds)
	}
	if today.ActiveSeconds != 1 {
		t.Errorf("today.ActiveSeconds = %d, want 1 (only the Coding tick)", today.ActiveSeconds)
	}
	if today.IdleSeconds != 2 {
		t.Errorf("today.IdleSeconds = %d, want 2 (Idle + OnBreak ticks)", today.IdleSeconds)
	}

	lifetime := g.State().Stats.Lifetime
	if lifetime != today {
		t.Errorf("lifetime = %+v, want it to equal today (%+v) on day one", lifetime, today)
	}
}

// TestTickCountsSprintCompletionInStats confirms SprintsCompleted increments
// exactly when Tick's own `completed` return value is true, in both buckets.
func TestTickCountsSprintCompletionInStats(t *testing.T) {
	g := New()
	completed := g.Tick(engine.TickResult{Mood: engine.MoodCoding, WorkUnits: 50}) // sprint 0 target is 50
	if !completed {
		t.Fatal("expected the first sprint to complete on exactly 50 work units")
	}
	if g.State().Stats.Today.SprintsCompleted != 1 {
		t.Errorf("today.SprintsCompleted = %d, want 1", g.State().Stats.Today.SprintsCompleted)
	}
	if g.State().Stats.Lifetime.SprintsCompleted != 1 {
		t.Errorf("lifetime.SprintsCompleted = %d, want 1", g.State().Stats.Lifetime.SprintsCompleted)
	}

	// A tick that does NOT complete a sprint must not increment either
	// bucket.
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, WorkUnits: 1})
	if g.State().Stats.Today.SprintsCompleted != 1 {
		t.Errorf("today.SprintsCompleted after a non-completing tick = %d, want still 1", g.State().Stats.Today.SprintsCompleted)
	}
}

// TestStatsAccumulateEvenWhileStoreIsOpen is the deliberate contrast with
// TestStoreOpenFreezesWorkAndDevCashAndMood in game_test.go: Mood/Progress/
// DevCash freeze while shopping (docs/ui-spec.md §5.3 — "the game cannot
// know [if a keystroke was aimed at the store], so it must not claim
// [work]"), but the Analytics stats are a passive tally of the SAME
// already-honest per-tick signal main.go hands Tick() every second
// regardless of StoreOpen (see main.go's own comment: the engine's
// keystroke baseline "keeps advancing" while the store is open) — there is
// no honesty reason to also freeze a passive count, so this asserts they
// keep counting.
func TestStatsAccumulateEvenWhileStoreIsOpen(t *testing.T) {
	g := New()
	g.OpenStore(1)

	g.Tick(engine.TickResult{Mood: engine.MoodCoding, KeystrokeDelta: 4, MouseActive: true, WorkUnits: 999})

	today := g.State().Stats.Today
	if today.Keystrokes != 4 {
		t.Errorf("today.Keystrokes while store open = %d, want 4 (stats are not gated by StoreOpen)", today.Keystrokes)
	}
	if today.MouseActiveSeconds != 1 {
		t.Errorf("today.MouseActiveSeconds while store open = %d, want 1", today.MouseActiveSeconds)
	}
	if today.ActiveSeconds != 1 {
		t.Errorf("today.ActiveSeconds while store open = %d, want 1 (uses the fresh TickResult.Mood, not the frozen g.Mood)", today.ActiveSeconds)
	}
	// Meanwhile the economy really is frozen — sanity-check the contrast
	// this test exists to document.
	if g.DevCash != 0 || g.Progress != 0 {
		t.Errorf("DevCash/Progress changed while store open: (%d, %v), want (0, 0)", g.DevCash, g.Progress)
	}
}

// TestMidnightRolloverResetsTodayKeepsLifetime is Phase A1's exit
// criterion "counters survive restart" applied to an in-process day
// boundary: the moment the injected clock crosses local midnight, the
// very next tick must zero `today` while `lifetime` keeps accumulating
// undisturbed.
func TestMidnightRolloverResetsTodayKeepsLifetime(t *testing.T) {
	g := New()
	// Constructed in time.Local (not UTC) and 2 hours apart either side of
	// local midnight, so this crosses a LOCAL day boundary (what
	// rolloverStatsIfNewDay actually keys on) regardless of the machine's
	// UTC offset running the test — a naive UTC-midnight boundary can
	// silently land on the same local calendar day in a positive-offset
	// timezone (e.g. UTC+4) and make this assertion vacuously pass/fail.
	day1 := time.Date(2026, 1, 1, 23, 0, 0, 0, time.Local)
	g.now = func() time.Time { return day1 }

	g.Tick(engine.TickResult{Mood: engine.MoodCoding, KeystrokeDelta: 10})
	if g.State().Stats.Today.Keystrokes != 10 {
		t.Fatalf("today.Keystrokes before rollover = %d, want 10", g.State().Stats.Today.Keystrokes)
	}

	day2 := time.Date(2026, 1, 2, 1, 0, 0, 0, time.Local)
	g.now = func() time.Time { return day2 }
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, KeystrokeDelta: 3})

	today := g.State().Stats.Today
	if today.Keystrokes != 3 {
		t.Errorf("today.Keystrokes after midnight = %d, want 3 (today reset, only the post-midnight tick counted)", today.Keystrokes)
	}
	lifetime := g.State().Stats.Lifetime
	if lifetime.Keystrokes != 13 {
		t.Errorf("lifetime.Keystrokes after midnight = %d, want 13 (10 + 3, never reset)", lifetime.Keystrokes)
	}
}

// TestRestoreStatsRollsOverAStaleDateImmediately covers the load-time half
// of midnight rollover: a save whose stats.date is an earlier day (the app
// was closed across one or more midnights) must present today's bucket as
// zero from the moment it's restored — not just after the first tick, ~1s
// later, ticks — while lifetime carries forward untouched.
func TestRestoreStatsRollsOverAStaleDateImmediately(t *testing.T) {
	g := New()
	g.now = func() time.Time { return time.Date(2026, 3, 5, 9, 0, 0, 0, time.Local) }

	stale := StatCounters{Keystrokes: 500, MouseActiveSeconds: 40, ActiveSeconds: 30, IdleSeconds: 20, SprintsCompleted: 2}
	lifetime := StatCounters{Keystrokes: 9000, MouseActiveSeconds: 800, ActiveSeconds: 700, IdleSeconds: 600, SprintsCompleted: 40}
	g.RestoreStats("2026-03-03", stale, lifetime)

	today := g.State().Stats.Today
	if today != (StatCounters{}) {
		t.Errorf("today after restoring a stale date = %+v, want the zero value", today)
	}
	if got := g.State().Stats.Lifetime; got != lifetime {
		t.Errorf("lifetime after RestoreStats = %+v, want %+v (carried forward untouched)", got, lifetime)
	}
}

// TestRestoreStatsKeepsTodayWhenDateMatches is the non-rollover half: a
// save whose stats.date IS today's local date must restore today's bucket
// as-is (the normal same-day restart case).
func TestRestoreStatsKeepsTodayWhenDateMatches(t *testing.T) {
	g := New()
	g.now = func() time.Time { return time.Date(2026, 3, 5, 9, 0, 0, 0, time.Local) }

	today := StatCounters{Keystrokes: 12, MouseActiveSeconds: 3, ActiveSeconds: 2, IdleSeconds: 1, SprintsCompleted: 0}
	lifetime := StatCounters{Keystrokes: 12, MouseActiveSeconds: 3, ActiveSeconds: 2, IdleSeconds: 1, SprintsCompleted: 0}
	g.RestoreStats("2026-03-05", today, lifetime)

	if got := g.State().Stats.Today; got != today {
		t.Errorf("today after RestoreStats (same date) = %+v, want %+v (unchanged)", got, today)
	}
}
