package game

import (
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/activity"
	"github.com/jawwadzafar/dexel/app/internal/engine"
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

// TestTickAccumulatesFocusSessionsAndAppSwitches is A2's (§5) extension of
// TestTickAccumulatesDailyAndLifetimeStats above to the two new signals:
// engine.TickResult.FocusSessionsCompleted/AppSwitches must sum into both
// the today and lifetime buckets, the same way every existing A1 counter
// already does.
func TestTickAccumulatesFocusSessionsAndAppSwitches(t *testing.T) {
	g := New()
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, FocusSessionsCompleted: 1, AppSwitches: 1})
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, FocusSessionsCompleted: 0, AppSwitches: 1})
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, FocusSessionsCompleted: 1, AppSwitches: 0})

	today := g.State().Stats.Today
	if today.FocusSessions != 2 {
		t.Errorf("today.FocusSessions = %d, want 2", today.FocusSessions)
	}
	if today.AppSwitches != 2 {
		t.Errorf("today.AppSwitches = %d, want 2", today.AppSwitches)
	}

	lifetime := g.State().Stats.Lifetime
	if lifetime.FocusSessions != 2 {
		t.Errorf("lifetime.FocusSessions = %d, want 2", lifetime.FocusSessions)
	}
	if lifetime.AppSwitches != 2 {
		t.Errorf("lifetime.AppSwitches = %d, want 2", lifetime.AppSwitches)
	}
}

// TestAppSwitchDailyCapLimitsBothTodayAndLifetime proves
// engine.AppSwitchDailyCap is enforced at this daily-aggregation layer
// (docs/plan/A2-design.md §5 — GO-1's engine.TickResult deliberately
// leaves AppSwitches uncapped): feeding far more than the cap's worth of
// switches in one day must stop incrementing AppSwitches, in EITHER
// bucket, the moment the cap is reached.
func TestAppSwitchDailyCapLimitsBothTodayAndLifetime(t *testing.T) {
	g := New()
	for i := 0; i < engine.AppSwitchDailyCap+25; i++ {
		g.Tick(engine.TickResult{Mood: engine.MoodIdle, AppSwitches: 1})
	}

	today := g.State().Stats.Today
	if today.AppSwitches != uint64(engine.AppSwitchDailyCap) {
		t.Errorf("today.AppSwitches = %d, want the cap (%d)", today.AppSwitches, engine.AppSwitchDailyCap)
	}
	lifetime := g.State().Stats.Lifetime
	if lifetime.AppSwitches != uint64(engine.AppSwitchDailyCap) {
		t.Errorf("lifetime.AppSwitches = %d, want the cap (%d) too — capped switches were never counted at all", lifetime.AppSwitches, engine.AppSwitchDailyCap)
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

// --- BUGS-RESILIENCE.md R5: a blind provider's seconds are not idle ------

// TestBlindTicksDoNotAccrueIdleSeconds is R5 — the ANALYTICS half of the
// dead-fd field bug (a GNOME screen lock re-enumerated /dev/input, the
// provider held dead handles for 19 hours). The provider-side fix stopped
// the runtime from CLAIMING onBreak while blind, but statsToday.IdleSeconds
// still climbed one per tick for the whole blind stretch — so "you were
// idle for 19 hours" survived the fix.
//
// The rule under test: idle is a POSITIVE claim ("dexel looked and saw
// nothing"), so a tick where the provider could not see input at all
// (activity.HonestyBlind) must accrue to NO bucket — not idle, not active,
// not paused. Unobserved time is not idleness (ADR 0010). That is also why
// R8's uptime invariant is stated over observed ticks rather than raw
// uptime.
func TestBlindTicksDoNotAccrueIdleSeconds(t *testing.T) {
	g := New()
	fakeNow := time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local)
	g.now = func() time.Time { return fakeNow }

	// Ten honest seconds first, half coding, so both buckets are non-zero
	// and a later freeze is visible as a freeze rather than as a zero.
	for i := 0; i < 10; i++ {
		mood := engine.MoodIdle
		var keys uint64
		if i%2 == 0 {
			mood = engine.MoodCoding
			keys = 3
		}
		g.Tick(engine.TickResult{Honesty: activity.HonestyGlobal, Mood: mood, KeystrokeDelta: keys})
	}
	before := g.State().Stats.Today
	if before.ActiveSeconds != 5 || before.IdleSeconds != 5 {
		t.Fatalf("warm-up: active=%d idle=%d, want 5 and 5", before.ActiveSeconds, before.IdleSeconds)
	}

	// The provider goes blind for an hour. The engine's ADR 0010 gate
	// already refuses MoodOnBreak here, so the mood is MoodIdle — which is
	// exactly the input that used to be counted as idleness.
	for i := 0; i < 3600; i++ {
		g.Tick(engine.TickResult{Honesty: activity.HonestyBlind, Mood: engine.MoodIdle})
	}

	for _, b := range []struct {
		name string
		c    StatCounters
	}{
		{"today", g.State().Stats.Today},
		{"lifetime", g.State().Stats.Lifetime},
	} {
		if b.c.IdleSeconds != 5 {
			t.Errorf("%s.IdleSeconds = %d after an hour of BLIND ticks, want it frozen at 5 — a provider that cannot see input must not claim idleness (R5)", b.name, b.c.IdleSeconds)
		}
		if b.c.ActiveSeconds != 5 {
			t.Errorf("%s.ActiveSeconds = %d, want it frozen at 5 too — a blind tick is not work either", b.name, b.c.ActiveSeconds)
		}
		if b.c.PausedSeconds != 0 {
			t.Errorf("%s.PausedSeconds = %d, want 0 — a blind provider is not a pause (the user never asked dexel to stop looking)", b.name, b.c.PausedSeconds)
		}
	}

	// And the open session sees the same freeze, since its numbers are
	// lifetime deltas.
	if err := g.StartSession(""); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	for i := 0; i < 120; i++ {
		g.Tick(engine.TickResult{Honesty: activity.HonestyBlind, Mood: engine.MoodIdle})
	}
	active := g.State().Sessions.Active
	if active == nil {
		t.Fatal("no active session")
	}
	if active.IdleSeconds != 0 {
		t.Errorf("session idleSeconds = %d across 120 blind ticks, want 0 (R5)", active.IdleSeconds)
	}
}

// TestBlindTicksStillCountContentFreeObservations pins the OTHER half of
// R5's decision: the honesty gate covers the active/idle TIME split only.
// A blind provider on macOS can still report keystroke counts it genuinely
// observed through a non-global path, and those counts are facts, not
// claims about idleness — so Keystrokes/MouseActiveSeconds must keep
// accruing exactly as before. This is what stops the R5 gate from being
// over-applied into "a blind tick is ignored entirely".
func TestBlindTicksStillCountContentFreeObservations(t *testing.T) {
	g := New()
	fakeNow := time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local)
	g.now = func() time.Time { return fakeNow }

	for i := 0; i < 4; i++ {
		g.Tick(engine.TickResult{Honesty: activity.HonestyBlind, Mood: engine.MoodIdle, KeystrokeDelta: 2, MouseActive: true})
	}
	today := g.State().Stats.Today
	if today.Keystrokes != 8 {
		t.Errorf("today.Keystrokes = %d, want 8 — an observed count is not an idleness claim", today.Keystrokes)
	}
	if today.MouseActiveSeconds != 4 {
		t.Errorf("today.MouseActiveSeconds = %d, want 4", today.MouseActiveSeconds)
	}
	if today.IdleSeconds != 0 {
		t.Errorf("today.IdleSeconds = %d, want 0 (R5)", today.IdleSeconds)
	}
}
