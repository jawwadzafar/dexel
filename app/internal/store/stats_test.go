package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/engine"
	"github.com/jawwadzafar/dexel/app/internal/game"
)

// TestStatsRoundTripThroughSaveLoadApply is Phase A1's (Analytics track,
// docs/plan/ROADMAP.md) persistence exit criterion: "counters survive
// restart" exercised through the exact path main.go uses — Snapshot, Save,
// Load, Apply onto a freshly-constructed game.New().
func TestStatsRoundTripThroughSaveLoadApply(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	g := game.New()
	fakeNow := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	g.SetClockForTest(func() time.Time { return fakeNow })

	g.Tick(engine.TickResult{Mood: engine.MoodCoding, KeystrokeDelta: 42, MouseActive: true, WorkUnits: 50})

	want := Snapshot(g)
	if want.Schema != CurrentSchema {
		t.Fatalf("Snapshot().Schema = %d, want CurrentSchema (%d)", want.Schema, CurrentSchema)
	}
	if want.Stats.Date != "2026-06-15" {
		t.Fatalf("Stats.Date = %q, want 2026-06-15", want.Stats.Date)
	}
	if want.Stats.Today.Keystrokes != 42 || want.Stats.Lifetime.Keystrokes != 42 {
		t.Fatalf("Stats.Today/Lifetime.Keystrokes = (%d,%d), want (42,42)", want.Stats.Today.Keystrokes, want.Stats.Lifetime.Keystrokes)
	}
	if want.Stats.Today.SprintsCompleted != 1 {
		t.Fatalf("Stats.Today.SprintsCompleted = %d, want 1", want.Stats.Today.SprintsCompleted)
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := Load(path)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got.Stats, want.Stats) {
		t.Errorf("Stats after Save/Load = %+v, want %+v", got.Stats, want.Stats)
	}

	g2 := game.New()
	g2.SetClockForTest(func() time.Time { return fakeNow }) // same day: Apply must not roll today over
	Apply(g2, got)

	state := g2.State()
	if state.Stats.Today.Keystrokes != 42 {
		t.Errorf("after Apply: today.Keystrokes = %d, want 42", state.Stats.Today.Keystrokes)
	}
	if state.Stats.Lifetime.Keystrokes != 42 {
		t.Errorf("after Apply: lifetime.Keystrokes = %d, want 42", state.Stats.Lifetime.Keystrokes)
	}
	if state.Stats.Today.SprintsCompleted != 1 {
		t.Errorf("after Apply: today.SprintsCompleted = %d, want 1", state.Stats.Today.SprintsCompleted)
	}
}

// TestApplyRollsOverStatsAcrossASavedMidnight confirms Apply's date-aware
// restore (via game.Game.RestoreStats) really does zero today's bucket
// when the loading game's current local date has moved past the save's
// stats.date, while carrying lifetime forward — the load-time half of
// midnight rollover, exercised through the real Load/Apply path rather
// than calling RestoreStats directly (see the game package's own
// RestoreStats-focused tests for that unit-level coverage).
func TestApplyRollsOverStatsAcrossASavedMidnight(t *testing.T) {
	saved := SaveData{
		Schema: CurrentSchema,
		Stats: StatsSave{
			Date:     "2026-06-15",
			Today:    StatCountersSave{Keystrokes: 500, SprintsCompleted: 3},
			Lifetime: StatCountersSave{Keystrokes: 9000, SprintsCompleted: 40},
		},
	}

	g := game.New()
	g.SetClockForTest(func() time.Time { return time.Date(2026, 6, 16, 8, 0, 0, 0, time.Local) })
	Apply(g, saved)

	state := g.State()
	if state.Stats.Today.Keystrokes != 0 || state.Stats.Today.SprintsCompleted != 0 {
		t.Errorf("today after Apply across a midnight = %+v, want the zero value", state.Stats.Today)
	}
	if state.Stats.Lifetime.Keystrokes != 9000 || state.Stats.Lifetime.SprintsCompleted != 40 {
		t.Errorf("lifetime after Apply across a midnight = %+v, want it carried forward untouched", state.Stats.Lifetime)
	}
}

// TestStatsCountersAndCoinsRoundTrip proves the A2 counters (FocusSessions)
// and CoinsToday survive a full Save/Load/Apply cycle byte-for-byte,
// exercised through the exact Snapshot/Save/Load/Apply path main.go uses
// (mirrors TestStatsRoundTripThroughSaveLoadApply's shape, extended to the
// per-signal counters and CoinsToday).
func TestStatsCountersAndCoinsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	g := game.New()
	fakeNow := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	g.SetClockForTest(func() time.Time { return fakeNow })

	g.RestoreStats("2026-06-15",
		game.StatCounters{Keystrokes: 42, FocusSessions: 3},
		game.StatCounters{Keystrokes: 9000, FocusSessions: 30},
	)
	g.RestoreCoinsToday(game.CoinBreakdown{Keystrokes: 6, Mouse: 2, FocusSessions: 4})

	want := Snapshot(g)
	if want.Schema != CurrentSchema {
		t.Fatalf("Snapshot().Schema = %d, want CurrentSchema (%d)", want.Schema, CurrentSchema)
	}
	if want.Stats.Today.FocusSessions != 3 {
		t.Fatalf("Stats.Today.FocusSessions = %d, want 3", want.Stats.Today.FocusSessions)
	}
	if want.Stats.CoinsToday != (CoinBreakdownSave{Keystrokes: 6, Mouse: 2, FocusSessions: 4}) {
		t.Fatalf("Stats.CoinsToday = %+v, want {6,2,4}", want.Stats.CoinsToday)
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := Load(path)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got.Stats, want.Stats) {
		t.Errorf("Stats after Save/Load = %+v, want %+v (save->load->equal)", got.Stats, want.Stats)
	}

	g2 := game.New()
	g2.SetClockForTest(func() time.Time { return fakeNow }) // same day: Apply must not roll today over
	Apply(g2, got)
	state := g2.State()

	if state.Stats.Today.FocusSessions != 3 {
		t.Errorf("after Apply: today FocusSessions = %d, want 3", state.Stats.Today.FocusSessions)
	}
	if state.Stats.Lifetime.FocusSessions != 30 {
		t.Errorf("after Apply: lifetime FocusSessions = %d, want 30", state.Stats.Lifetime.FocusSessions)
	}
	if state.Stats.CoinsToday != (game.CoinBreakdown{Keystrokes: 6, Mouse: 2, FocusSessions: 4}) {
		t.Errorf("after Apply: CoinsToday = %+v, want {6,2,4}", state.Stats.CoinsToday)
	}
}

// --- Analytics Phase A3 (docs/plan/A3-design.md §4/§7 Task GO-2) --------

// TestHistoryAndStreakRoundTrip is the save->load->equal exit criterion
// for analytics history/streak: once a save carries a non-empty history
// and a non-zero streak, both must survive a full Save/Load/Apply cycle
// exactly, exercised through the exact Snapshot/Save/Load/Apply path
// main.go uses (mirrors TestStatsCountersAndCoinsRoundTrip's shape,
// extended to History/Streak).
func TestHistoryAndStreakRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	g := game.New()
	fakeNow := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	g.SetClockForTest(func() time.Time { return fakeNow })

	wantHistory := []game.DayBucket{
		{Date: "2026-06-13", Counters: game.StatCounters{Keystrokes: 100, ActiveSeconds: 400}, CoinsEarned: 12, LongestFocusBlockSeconds: 900},
		{Date: "2026-06-14", Counters: game.StatCounters{Keystrokes: 200, ActiveSeconds: 500}, CoinsEarned: 20, LongestFocusBlockSeconds: 1200},
	}
	g.RestoreHistory(wantHistory)
	g.RestoreStreak(2, 5, "2026-06-14")
	g.RestoreStats("2026-06-15", game.StatCounters{Keystrokes: 42}, game.StatCounters{Keystrokes: 9042})

	want := Snapshot(g)
	if want.Schema != CurrentSchema {
		t.Fatalf("Snapshot().Schema = %d, want CurrentSchema (%d)", want.Schema, CurrentSchema)
	}
	if len(want.Stats.History) != 2 {
		t.Fatalf("Snapshot().Stats.History has %d entries, want 2", len(want.Stats.History))
	}
	if want.Stats.History[0].Date != "2026-06-13" || want.Stats.History[0].CoinsEarned != 12 || want.Stats.History[0].LongestFocusBlockSeconds != 900 {
		t.Errorf("Snapshot().Stats.History[0] = %+v, want date 2026-06-13/coinsEarned 12/longestFocusBlockSeconds 900", want.Stats.History[0])
	}
	if want.Stats.Streak != (StreakSave{Current: 2, Longest: 5, LastActiveDate: "2026-06-14"}) {
		t.Fatalf("Snapshot().Stats.Streak = %+v, want {2,5,2026-06-14}", want.Stats.Streak)
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := Load(path)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got.Stats.History, want.Stats.History) {
		t.Errorf("History after Save/Load = %+v, want %+v (save->load->equal)", got.Stats.History, want.Stats.History)
	}
	if got.Stats.Streak != want.Stats.Streak {
		t.Errorf("Streak after Save/Load = %+v, want %+v", got.Stats.Streak, want.Stats.Streak)
	}

	g2 := game.New()
	g2.SetClockForTest(func() time.Time { return fakeNow }) // same day: Apply must not roll today over or finalize
	Apply(g2, got)

	gotHistory := g2.HistorySnapshot()
	if !reflect.DeepEqual(gotHistory, wantHistory) {
		t.Errorf("after Apply: HistorySnapshot() = %+v, want %+v", gotHistory, wantHistory)
	}
	current, longest, lastActiveDate := g2.StreakSnapshot()
	if current != 2 || longest != 5 || lastActiveDate != "2026-06-14" {
		t.Errorf("after Apply: streak = (%d,%d,%q), want (2,5,2026-06-14)", current, longest, lastActiveDate)
	}
}

// TestHistoryRetentionSurvivesASaveReload proves the 30-day retention
// window (game.HistoryRetentionDays) is enforced by the persistence path
// itself, not just by the in-process game package: 35 consecutive days
// are finalized (each via RestoreStats's rollover, exactly as an
// in-process midnight crossing or a load would), producing 35 candidate
// buckets, and the persisted/round-tripped history must still show
// exactly the last 30 (oldest 5 pruned) after a full Snapshot/Save/Load/
// Apply cycle.
func TestHistoryRetentionSurvivesASaveReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	dateOf := func(dayOffset int) string {
		return time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local).AddDate(0, 0, dayOffset).Format("2006-01-02")
	}

	g := game.New()
	const totalDays = 35
	for i := 0; i < totalDays; i++ {
		clockDate := time.Date(2026, 6, 1, 10, 0, 0, 0, time.Local).AddDate(0, 0, i+1)
		g.SetClockForTest(func() time.Time { return clockDate })
		// RestoreStats(date, today, lifetime): sets statsDate to day i,
		// then rolloverStatsIfNewDay notices the clock's "today" (day
		// i+1) has moved past it and finalizes day i — the same
		// mechanism a real load-time or in-process midnight rollover
		// uses, driven here to build up more than HistoryRetentionDays
		// of finalized buckets deterministically.
		g.RestoreStats(dateOf(i), game.StatCounters{ActiveSeconds: 400, Keystrokes: uint64(i)}, game.StatCounters{})
	}

	preSave := g.HistorySnapshot()
	if len(preSave) != game.HistoryRetentionDays {
		t.Fatalf("before save: HistorySnapshot() has %d entries, want %d (HistoryRetentionDays)", len(preSave), game.HistoryRetentionDays)
	}
	if preSave[0].Date != dateOf(totalDays-game.HistoryRetentionDays) {
		t.Errorf("before save: oldest retained day = %s, want %s (the oldest %d days pruned)",
			preSave[0].Date, dateOf(totalDays-game.HistoryRetentionDays), totalDays-game.HistoryRetentionDays)
	}
	if preSave[len(preSave)-1].Date != dateOf(totalDays-1) {
		t.Errorf("before save: newest retained day = %s, want %s", preSave[len(preSave)-1].Date, dateOf(totalDays-1))
	}

	if err := Save(path, Snapshot(g)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := Load(path)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if len(got.Stats.History) != game.HistoryRetentionDays {
		t.Fatalf("after Save/Load: Stats.History has %d entries, want %d", len(got.Stats.History), game.HistoryRetentionDays)
	}

	g2 := game.New()
	finalClockDate := time.Date(2026, 6, 1, 10, 0, 0, 0, time.Local).AddDate(0, 0, totalDays)
	g2.SetClockForTest(func() time.Time { return finalClockDate }) // same day as g ended on: no further finalize on Apply
	Apply(g2, got)

	after := g2.HistorySnapshot()
	if len(after) != game.HistoryRetentionDays {
		t.Fatalf("after Apply: HistorySnapshot() has %d entries, want %d", len(after), game.HistoryRetentionDays)
	}
	if after[0].Date != dateOf(totalDays-game.HistoryRetentionDays) {
		t.Errorf("after Apply: oldest retained day = %s, want %s", after[0].Date, dateOf(totalDays-game.HistoryRetentionDays))
	}
	if after[len(after)-1].Date != dateOf(totalDays-1) {
		t.Errorf("after Apply: newest retained day = %s, want %s", after[len(after)-1].Date, dateOf(totalDays-1))
	}
}

// TestFinalizeOnReloadFinalizesTheStaleRunningDayExactlyOnce is the task
// spec's required proof of §2.5 case 8 / §3.3's "finalize-on-reload"
// semantics through the ACTUAL Apply path (not by calling game's
// internals directly): a save whose stats.date is several days stale —
// exactly the "reopened the app days later" scenario — must finalize that
// one last running day into history + the streak EXACTLY ONCE on Apply,
// and a subsequent SAME-DAY reload of the resulting state must NOT
// double-finalize it.
func TestFinalizeOnReloadFinalizesTheStaleRunningDayExactlyOnce(t *testing.T) {
	// A save last written on 2026-06-10 with an ACTIVE day's worth of
	// activity (ActiveSeconds >= game.ActiveDayMinSeconds) — no history,
	// no streak yet (this is that day's very first-ever finalize).
	saved := SaveData{
		Schema: CurrentSchema,
		Stats: StatsSave{
			Date:       "2026-06-10",
			Today:      StatCountersSave{Keystrokes: 500, ActiveSeconds: 400, SprintsCompleted: 3},
			Lifetime:   StatCountersSave{Keystrokes: 9000, ActiveSeconds: 400, SprintsCompleted: 40},
			CoinsToday: CoinBreakdownSave{Keystrokes: 6},
		},
	}

	g := game.New()
	// The process is reopened 5 days later: 2026-06-15.
	reopenDate := time.Date(2026, 6, 15, 8, 0, 0, 0, time.Local)
	g.SetClockForTest(func() time.Time { return reopenDate })
	Apply(g, saved)

	history := g.HistorySnapshot()
	if len(history) != 1 {
		t.Fatalf("after reload-Apply across a %d-day gap: HistorySnapshot() has %d entries, want exactly 1 (the stale running day, finalized ONCE)",
			5, len(history))
	}
	if history[0].Date != "2026-06-10" {
		t.Errorf("finalized bucket's Date = %q, want 2026-06-10", history[0].Date)
	}
	if history[0].Counters.ActiveSeconds != 400 || history[0].Counters.Keystrokes != 500 {
		t.Errorf("finalized bucket's Counters = %+v, want the stale-day's Today counters (activeSeconds 400, keystrokes 500)", history[0].Counters)
	}

	current, longest, lastActiveDate := g.StreakSnapshot()
	if current != 1 || longest != 1 || lastActiveDate != "2026-06-10" {
		t.Errorf("streak after reload-Apply = (%d,%d,%q), want (1,1,2026-06-10) — the stale active day should have started a fresh 1-day streak",
			current, longest, lastActiveDate)
	}

	state := g.State()
	if state.Stats.Today != (game.StatCounters{}) {
		t.Errorf("today's bucket after reload-Apply = %+v, want the zero value (today, 2026-06-15, has had no activity yet)", state.Stats.Today)
	}
	if state.Stats.Lifetime.Keystrokes != 9000 {
		t.Errorf("lifetime.Keystrokes after reload-Apply = %d, want 9000 (carried forward untouched)", state.Stats.Lifetime.Keystrokes)
	}

	// Now: a SAME-DAY reload of the resulting state (Snapshot -> Apply
	// again, clock unchanged) must NOT re-finalize 2026-06-10 a second
	// time and must NOT touch the streak either.
	reSaved := Snapshot(g)
	g2 := game.New()
	g2.SetClockForTest(func() time.Time { return reopenDate }) // same day
	Apply(g2, reSaved)

	history2 := g2.HistorySnapshot()
	if len(history2) != 1 {
		t.Fatalf("after a SAME-DAY reload: HistorySnapshot() has %d entries, want still exactly 1 (no double-finalize)", len(history2))
	}
	if history2[0] != history[0] {
		t.Errorf("after a SAME-DAY reload: the finalized bucket changed to %+v, want unchanged %+v", history2[0], history[0])
	}
	current2, longest2, lastActiveDate2 := g2.StreakSnapshot()
	if current2 != current || longest2 != longest || lastActiveDate2 != lastActiveDate {
		t.Errorf("after a SAME-DAY reload: streak changed to (%d,%d,%q), want unchanged (%d,%d,%q)",
			current2, longest2, lastActiveDate2, current, longest, lastActiveDate)
	}
}
