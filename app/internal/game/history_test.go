package game

import (
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/engine"
)

// --- §2.5 edge case 8: fresh/migrated save --------------------------------

// TestFreshSaveStreakStartsAtZero covers §2.5 edge case 8: a fresh game
// (equivalently, a migrated schema-3 save with no history/streak keys,
// which RestoreStreak("", 0, 0) would represent) has lastActiveDate == ""
// and so must report an effective streak of 0 — no backfill, no
// invented past days.
func TestFreshSaveStreakStartsAtZero(t *testing.T) {
	g := New()
	g.now = func() time.Time { return time.Date(2026, 6, 1, 9, 0, 0, 0, time.Local) }
	g.Tick(engine.TickResult{Mood: engine.MoodIdle})

	streak := g.State().Stats.Streak
	if streak.Current != 0 || streak.Longest != 0 {
		t.Errorf("fresh save streak = %+v, want {0 0}", streak)
	}
}

// TestMigratedSaveWithEmptyLastActiveDateStreaksZero is the explicit
// RestoreStreak("", ...) half of edge case 8 — a schema-3 save loaded via
// internal/store's future GO-2 Apply path restores an empty
// lastActiveDate exactly like a brand new game.
func TestMigratedSaveWithEmptyLastActiveDateStreaksZero(t *testing.T) {
	g := New()
	g.RestoreStreak(0, 0, "")
	g.now = func() time.Time { return time.Date(2026, 6, 1, 9, 0, 0, 0, time.Local) }
	g.Tick(engine.TickResult{Mood: engine.MoodIdle})

	streak := g.State().Stats.Streak
	if streak.Current != 0 || streak.Longest != 0 {
		t.Errorf("migrated (empty lastActiveDate) streak = %+v, want {0 0}", streak)
	}
}

// --- §2.5 edge case 1: consecutive days -----------------------------------

// TestConsecutiveActiveDaysIncrementStreak drives three consecutive local
// days, each crossing the ActiveDayMinSeconds threshold, and asserts
// current increments by 1 at each finalize (i.e. once the FOLLOWING day's
// first tick rolls the previous day over).
func TestConsecutiveActiveDaysIncrementStreak(t *testing.T) {
	g := New()
	activeTick := engine.TickResult{Mood: engine.MoodCoding}

	tickSeconds := func(y, m, d, seconds int) {
		g.now = func() time.Time { return time.Date(y, time.Month(m), d, 8, 0, 0, 0, time.Local) }
		for i := 0; i < seconds; i++ {
			g.Tick(activeTick)
		}
	}

	// Day 1: active (>= threshold). Streak isn't updated until day 1
	// FINALIZES, i.e. on day 2's first tick.
	tickSeconds(2026, 6, 1, ActiveDayMinSeconds)
	if s := g.State().Stats.Streak; s.Current != 1 {
		// Today-in-progress already counts as +1 per §2.4 even before
		// finalize, since lastActiveDate is still "".
		t.Errorf("day 1 (today, active, no prior streak) effective current = %d, want 1", s.Current)
	}

	// Day 2: active again — day 1 finalizes on this day's first tick,
	// setting current=1/lastActiveDate=day1; then day 2's own activity
	// folds in at read time as current+1 = 2.
	tickSeconds(2026, 6, 2, ActiveDayMinSeconds)
	if s := g.State().Stats.Streak; s.Current != 2 {
		t.Errorf("day 2 (consecutive active) effective current = %d, want 2", s.Current)
	}

	// Day 3: active again — day 2 finalizes (current becomes 2 for real),
	// day 3 folds in as 3.
	tickSeconds(2026, 6, 3, ActiveDayMinSeconds)
	if s := g.State().Stats.Streak; s.Current != 3 {
		t.Errorf("day 3 (consecutive active) effective current = %d, want 3", s.Current)
	}
	if s := g.State().Stats.Streak; s.Longest != 3 {
		t.Errorf("day 3 longest = %d, want 3", s.Longest)
	}
}

// --- §2.5 edge case 2: gap breaks the streak -------------------------------

// TestGapBreaksStreakButPreservesLongest covers §2.5 edge case 2: two
// active days build a streak of 2, then a day is skipped entirely (the
// process never runs, so no finalize call ever sees it) and the next
// active day resets current to 1 while longest is preserved.
func TestGapBreaksStreakButPreservesLongest(t *testing.T) {
	g := New()
	activeTick := engine.TickResult{Mood: engine.MoodCoding}
	tickSeconds := func(y, m, d, seconds int) {
		g.now = func() time.Time { return time.Date(y, time.Month(m), d, 8, 0, 0, 0, time.Local) }
		for i := 0; i < seconds; i++ {
			g.Tick(activeTick)
		}
	}

	tickSeconds(2026, 6, 1, ActiveDayMinSeconds) // day 1 active
	tickSeconds(2026, 6, 2, ActiveDayMinSeconds) // day 2 active; day 1 finalizes -> current=1, longest=1
	if s := g.State().Stats.Streak; s.Longest != 2 {
		t.Fatalf("after 2 consecutive active days, longest = %d, want 2", s.Longest)
	}

	// Skip day 3 entirely (no tick at all that day). Day 4 is active:
	// day 2 finalizes with dD=2026-06-04's PRIOR statsDate (2026-06-02),
	// which is >1 day before 2026-06-04, so updateStreak resets to 1.
	tickSeconds(2026, 6, 4, ActiveDayMinSeconds)
	s := g.State().Stats.Streak
	if s.Current != 1 {
		t.Errorf("after a gap day, effective current = %d, want 1 (reset)", s.Current)
	}
	if s.Longest != 2 {
		t.Errorf("after a gap day, longest = %d, want 2 (preserved record)", s.Longest)
	}
}

// --- §2.5 edge case 6: an inactive (but not skipped) day does not extend --

// TestInactiveDayDoesNotExtendStreak covers §2.5 edge case 6: a day that
// DID run (ticks happened, statsDate was set) but never crossed
// ActiveDayMinSeconds must not extend the streak when it finalizes — same
// as a fully-skipped day, from the streak algorithm's point of view.
func TestInactiveDayDoesNotExtendStreak(t *testing.T) {
	g := New()
	tickSeconds := func(y, m, d int, mood engine.Mood, seconds int) {
		g.now = func() time.Time { return time.Date(y, time.Month(m), d, 8, 0, 0, 0, time.Local) }
		for i := 0; i < seconds; i++ {
			g.Tick(engine.TickResult{Mood: mood})
		}
	}

	tickSeconds(2026, 6, 1, engine.MoodCoding, ActiveDayMinSeconds) // day 1 active
	// Day 2 runs (a few ticks) but stays below the threshold.
	tickSeconds(2026, 6, 2, engine.MoodCoding, ActiveDayMinSeconds-1)
	// Day 3: day 2 finalizes as INACTIVE -> must not extend; day 1's
	// streak of 1 is now >=2 days stale relative to day 3, so it's dead.
	tickSeconds(2026, 6, 3, engine.MoodCoding, 1)

	s := g.State().Stats.Streak
	if s.Current != 0 {
		t.Errorf("after an inactive middle day, effective current on day 3 (barely active) = %d, want 0 (day 1's streak already dead, day 3 alone not yet >= threshold)", s.Current)
	}
}

// --- §2.5 edge case 3: today in progress + no-double-count -----------------

// TestTodayInProgressShowsRunAliveThroughYesterday covers the first half
// of §2.5 edge case 3: yesterday was active, today hasn't crossed the
// threshold yet — the effective streak must still show the run alive
// through yesterday, not reset to 0 at midnight.
func TestTodayInProgressShowsRunAliveThroughYesterday(t *testing.T) {
	g := New()
	activeTick := engine.TickResult{Mood: engine.MoodCoding}
	g.now = func() time.Time { return time.Date(2026, 6, 1, 8, 0, 0, 0, time.Local) }
	for i := 0; i < ActiveDayMinSeconds; i++ {
		g.Tick(activeTick)
	}

	// Move to day 2, a single tick BELOW threshold — day 1 finalizes
	// (current=1), today (day 2) is not yet active.
	g.now = func() time.Time { return time.Date(2026, 6, 2, 8, 0, 0, 0, time.Local) }
	g.Tick(engine.TickResult{Mood: engine.MoodIdle})

	s := g.State().Stats.Streak
	if s.Current != 1 {
		t.Errorf("today-in-progress (not yet active), streak = %d, want 1 (run alive through yesterday)", s.Current)
	}
}

// TestTodayCrossingThresholdAddsOneWithNoDoubleCount covers the second
// half of §2.5 edge case 3: once today crosses ActiveDayMinSeconds, the
// effective streak becomes yesterday's run + 1, and calling State()
// repeatedly afterward (still the same today) must never double-count.
func TestTodayCrossingThresholdAddsOneWithNoDoubleCount(t *testing.T) {
	g := New()
	activeTick := engine.TickResult{Mood: engine.MoodCoding}
	g.now = func() time.Time { return time.Date(2026, 6, 1, 8, 0, 0, 0, time.Local) }
	for i := 0; i < ActiveDayMinSeconds; i++ {
		g.Tick(activeTick)
	}

	g.now = func() time.Time { return time.Date(2026, 6, 2, 8, 0, 0, 0, time.Local) }
	for i := 0; i < ActiveDayMinSeconds; i++ {
		g.Tick(activeTick)
	}

	first := g.State().Stats.Streak
	if first.Current != 2 {
		t.Fatalf("today crossed threshold, streak = %d, want 2 (yesterday's 1 + today's +1)", first.Current)
	}

	// Read again (no further ticks, no clock change) — must be stable,
	// not incremented again.
	second := g.State().Stats.Streak
	if second.Current != 2 {
		t.Errorf("repeated read of the same in-progress today = %d, want still 2 (no double count)", second.Current)
	}

	// One more tick the same day must also not double count.
	g.Tick(activeTick)
	third := g.State().Stats.Streak
	if third.Current != 2 {
		t.Errorf("another tick the same already-active today = %d, want still 2", third.Current)
	}
}

// --- §2.5 edge case 4: streak death -----------------------------------

// TestStreakDeathAfterTwoOrMoreInactiveDaysKeepsLongest covers §2.5 edge
// case 4: once the last active day is >= 2 days in the past, the
// effective current streak reads 0, but longest still shows the record.
func TestStreakDeathAfterTwoOrMoreInactiveDaysKeepsLongest(t *testing.T) {
	g := New()
	activeTick := engine.TickResult{Mood: engine.MoodCoding}
	tick := func(y, m, d int, r engine.TickResult) {
		g.now = func() time.Time { return time.Date(y, time.Month(m), d, 8, 0, 0, 0, time.Local) }
		g.Tick(r)
	}
	tickN := func(y, m, d, n int, r engine.TickResult) {
		g.now = func() time.Time { return time.Date(y, time.Month(m), d, 8, 0, 0, 0, time.Local) }
		for i := 0; i < n; i++ {
			g.Tick(r)
		}
	}

	tickN(2026, 6, 1, ActiveDayMinSeconds, activeTick)
	tickN(2026, 6, 2, ActiveDayMinSeconds, activeTick) // day1 finalizes active; longest becomes 2 once day2 finalizes below
	if s := g.State().Stats.Streak; s.Longest < 2 {
		t.Fatalf("expected longest >= 2 after two consecutive active days, got %d", s.Longest)
	}

	// Jump forward 3 days with no activity at all (idle, below
	// threshold) — day 2 finalizes active (current=2, longest=2), but by
	// the time we reach day 5, lastActiveDate (day 2) is 3 days stale.
	tick(2026, 6, 5, engine.TickResult{Mood: engine.MoodIdle})

	s := g.State().Stats.Streak
	if s.Current != 0 {
		t.Errorf("streak >=2 days stale, effective current = %d, want 0 (dead)", s.Current)
	}
	if s.Longest != 2 {
		t.Errorf("streak death, longest = %d, want 2 (record preserved)", s.Longest)
	}
}

// --- §2.5 edge case 5: streak longer than the retention window -----------

// TestStreakSurvivesBeyondRetentionWindow covers §2.5 edge case 5: a
// streak of 40 consecutive active days (> HistoryRetentionDays == 30)
// must report current == 40, because current is a persisted counter, not
// derived from the (pruned-to-30) history buckets.
func TestStreakSurvivesBeyondRetentionWindow(t *testing.T) {
	if HistoryRetentionDays != 30 {
		t.Fatalf("this test assumes HistoryRetentionDays == 30, got %d", HistoryRetentionDays)
	}
	g := New()
	activeTick := engine.TickResult{Mood: engine.MoodCoding}

	const days = 40
	start := time.Date(2026, 1, 1, 8, 0, 0, 0, time.Local)
	for i := 0; i < days; i++ {
		day := start.AddDate(0, 0, i)
		g.now = func() time.Time { return day }
		for s := 0; s < ActiveDayMinSeconds; s++ {
			g.Tick(activeTick)
		}
	}

	s := g.State().Stats.Streak
	if s.Current != days {
		t.Errorf("40-day streak (> 30-day retention) effective current = %d, want %d", s.Current, days)
	}
	if s.Longest != days {
		t.Errorf("40-day streak longest = %d, want %d", s.Longest, days)
	}
	if got := len(g.HistorySnapshot()); got != HistoryRetentionDays {
		t.Errorf("persisted history length = %d, want pruned to %d", got, HistoryRetentionDays)
	}
}

// --- §2.5 edge case 7: calendar boundaries ---------------------------------

// TestStreakCrossesYearBoundary covers §2.5 edge case 7 (Dec 31 -> Jan 1):
// two consecutive active days spanning a year boundary must be treated
// as adjacent, extending the streak exactly like any other consecutive
// pair.
func TestStreakCrossesYearBoundary(t *testing.T) {
	g := New()
	activeTick := engine.TickResult{Mood: engine.MoodCoding}
	tickDay := func(y, m, d int) {
		g.now = func() time.Time { return time.Date(y, time.Month(m), d, 8, 0, 0, 0, time.Local) }
		for i := 0; i < ActiveDayMinSeconds; i++ {
			g.Tick(activeTick)
		}
	}

	tickDay(2026, 12, 31)
	tickDay(2027, 1, 1)
	tickDay(2027, 1, 2) // finalizes Jan 1 as consecutive with Dec 31

	s := g.State().Stats.Streak
	if s.Current != 3 {
		t.Errorf("streak crossing Dec 31 -> Jan 1 -> Jan 2 = %d, want 3 (all adjacent)", s.Current)
	}
}

// TestStreakCrossesMonthBoundary covers the Feb 28 -> Mar 1 half of §2.5
// edge case 7 (2026 is not a leap year, so Feb has 28 days).
func TestStreakCrossesMonthBoundary(t *testing.T) {
	g := New()
	activeTick := engine.TickResult{Mood: engine.MoodCoding}
	tickDay := func(y, m, d int) {
		g.now = func() time.Time { return time.Date(y, time.Month(m), d, 8, 0, 0, 0, time.Local) }
		for i := 0; i < ActiveDayMinSeconds; i++ {
			g.Tick(activeTick)
		}
	}

	tickDay(2026, 2, 28)
	tickDay(2026, 3, 1)
	tickDay(2026, 3, 2) // finalizes Mar 1 as consecutive with Feb 28

	s := g.State().Stats.Streak
	if s.Current != 3 {
		t.Errorf("streak crossing Feb 28 -> Mar 1 -> Mar 2 = %d, want 3 (all adjacent, non-leap year)", s.Current)
	}
}

// --- dense-wire builder (§5) -----------------------------------------------

// TestHistoryViewIsDenseAscendingAndEndsWithLiveToday exercises the wire
// builder's shape guarantees: exactly HistoryRetentionDays entries,
// ascending dates, the final entry is today (live, matching statsToday),
// and a day with no persisted bucket zero-fills rather than being
// omitted.
func TestHistoryViewIsDenseAscendingAndEndsWithLiveToday(t *testing.T) {
	g := New()
	g.now = func() time.Time { return time.Date(2026, 6, 1, 8, 0, 0, 0, time.Local) }
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, KeystrokeDelta: 7})

	history := g.State().Stats.History
	if len(history) != HistoryRetentionDays {
		t.Fatalf("history length = %d, want %d", len(history), HistoryRetentionDays)
	}

	// Ascending, ending with today, with no gaps.
	for i := 1; i < len(history); i++ {
		want := addDaysToDateString(history[i-1].Date, 1)
		if history[i].Date != want {
			t.Fatalf("history[%d].Date = %s, want %s (must be the day after history[%d])", i, history[i].Date, want, i-1)
		}
	}
	last := history[len(history)-1]
	if last.Date != "2026-06-01" {
		t.Errorf("last history entry date = %s, want 2026-06-01 (today)", last.Date)
	}
	if last.Keystrokes != 7 {
		t.Errorf("today's live history entry Keystrokes = %d, want 7 (matching statsToday)", last.Keystrokes)
	}

	// Every day before today has no persisted bucket (game is brand new)
	// -> must zero-fill, not be omitted.
	for i := 0; i < len(history)-1; i++ {
		d := history[i]
		if d.Keystrokes != 0 || d.ActiveSeconds != 0 || d.IsActive {
			t.Errorf("history[%d] (%s, no bucket) = %+v, want all-zero", i, d.Date, d)
		}
	}
}

// TestHistoryViewIsActiveMatchesThreshold checks that the wire's IsActive
// bool agrees exactly with ActiveDayMinSeconds — both for a finalized
// persisted bucket and for today's live entry.
func TestHistoryViewIsActiveMatchesThreshold(t *testing.T) {
	g := New()
	activeTick := engine.TickResult{Mood: engine.MoodCoding}

	// Day 1: exactly at the threshold.
	g.now = func() time.Time { return time.Date(2026, 6, 1, 8, 0, 0, 0, time.Local) }
	for i := 0; i < ActiveDayMinSeconds; i++ {
		g.Tick(activeTick)
	}
	// Day 2: one second below the threshold (today, live, not yet active).
	g.now = func() time.Time { return time.Date(2026, 6, 2, 8, 0, 0, 0, time.Local) }
	for i := 0; i < ActiveDayMinSeconds-1; i++ {
		g.Tick(activeTick)
	}

	history := g.State().Stats.History
	var day1, day2 *DayStat
	for i := range history {
		switch history[i].Date {
		case "2026-06-01":
			day1 = &history[i]
		case "2026-06-02":
			day2 = &history[i]
		}
	}
	if day1 == nil || day2 == nil {
		t.Fatalf("expected both 2026-06-01 and 2026-06-02 in the 30-day window ending 2026-06-02, got dates %v", datesOf(history))
	}
	if !day1.IsActive {
		t.Errorf("day1 (exactly at threshold, %d seconds) IsActive = false, want true", ActiveDayMinSeconds)
	}
	if day2.IsActive {
		t.Errorf("day2 (one below threshold, live today) IsActive = true, want false")
	}

	// Cross one more tick so today reaches the threshold too.
	g.Tick(activeTick)
	history = g.State().Stats.History
	for i := range history {
		if history[i].Date == "2026-06-02" {
			if !history[i].IsActive {
				t.Errorf("day2 after crossing threshold IsActive = false, want true")
			}
		}
	}
}

func datesOf(history []DayStat) []string {
	out := make([]string, len(history))
	for i, d := range history {
		out[i] = d.Date
	}
	return out
}

// --- retention pruning ------------------------------------------------------

// TestHistoryRetentionPrunesOldestBucket drives more than
// HistoryRetentionDays worth of finalized days and asserts the persisted
// (sparse) history caps at exactly HistoryRetentionDays, dropping the
// OLDEST bucket first (FIFO), while the streak (a separate, unbounded
// counter) keeps counting correctly regardless.
func TestHistoryRetentionPrunesOldestBucket(t *testing.T) {
	g := New()
	tick := engine.TickResult{Mood: engine.MoodCoding}

	start := time.Date(2026, 1, 1, 8, 0, 0, 0, time.Local)
	const totalDays = HistoryRetentionDays + 5
	for i := 0; i < totalDays; i++ {
		day := start.AddDate(0, 0, i)
		g.now = func() time.Time { return day }
		g.Tick(tick)
	}

	buckets := g.HistorySnapshot()
	if len(buckets) != HistoryRetentionDays {
		t.Fatalf("persisted history length = %d, want exactly %d", len(buckets), HistoryRetentionDays)
	}
	// totalDays days ticked, one tick each => totalDays-1 days finalized
	// (the last, still-running day is never finalized). The oldest
	// retained bucket must be the (totalDays-1-HistoryRetentionDays)-th
	// finalized day chronologically, i.e. NOT day 0.
	if buckets[0].Date == "2026-01-01" {
		t.Errorf("oldest retained bucket is still day 0 (2026-01-01) — pruning did not drop the oldest entries")
	}
	// Ascending order preserved.
	for i := 1; i < len(buckets); i++ {
		if buckets[i].Date <= buckets[i-1].Date {
			t.Fatalf("persisted history not ascending: buckets[%d].Date=%s <= buckets[%d].Date=%s", i, buckets[i].Date, i-1, buckets[i-1].Date)
		}
	}
}

// --- finalize-on-rollover ---------------------------------------------------

// TestFinalizeOnRolloverAppendsExactlyOneBucketAndUpdatesStreak is the
// core §3.3 integration test: crossing one local-date boundary appends
// exactly one DayBucket (for the day that just ended) and runs
// updateStreak for it — no more, no less, and NOT for "today" (which is
// still in progress and never finalized while running).
func TestFinalizeOnRolloverAppendsExactlyOneBucketAndUpdatesStreak(t *testing.T) {
	g := New()
	activeTick := engine.TickResult{Mood: engine.MoodCoding}

	g.now = func() time.Time { return time.Date(2026, 6, 1, 8, 0, 0, 0, time.Local) }
	for i := 0; i < ActiveDayMinSeconds; i++ {
		g.Tick(activeTick)
	}
	if got := len(g.HistorySnapshot()); got != 0 {
		t.Fatalf("before any rollover, persisted history length = %d, want 0 (today is never finalized while running)", got)
	}

	// Cross into day 2 — day 1 must finalize on THIS first tick.
	g.now = func() time.Time { return time.Date(2026, 6, 2, 8, 0, 0, 0, time.Local) }
	g.Tick(engine.TickResult{Mood: engine.MoodIdle})

	buckets := g.HistorySnapshot()
	if len(buckets) != 1 {
		t.Fatalf("after one day-boundary crossing, persisted history length = %d, want exactly 1", len(buckets))
	}
	if buckets[0].Date != "2026-06-01" {
		t.Errorf("finalized bucket date = %s, want 2026-06-01 (the day that just ended)", buckets[0].Date)
	}
	if buckets[0].Counters.ActiveSeconds != ActiveDayMinSeconds {
		t.Errorf("finalized bucket ActiveSeconds = %d, want %d", buckets[0].Counters.ActiveSeconds, ActiveDayMinSeconds)
	}

	current, longest, lastActive := g.StreakSnapshot()
	if current != 1 || longest != 1 || lastActive != "2026-06-01" {
		t.Errorf("streak after finalize = (current=%d longest=%d lastActive=%s), want (1 1 2026-06-01)", current, longest, lastActive)
	}

	// A second tick the SAME day (still day 2) must NOT finalize again.
	g.Tick(engine.TickResult{Mood: engine.MoodIdle})
	if got := len(g.HistorySnapshot()); got != 1 {
		t.Errorf("after a second same-day tick, persisted history length = %d, want still 1 (no double-finalize)", got)
	}
}

// TestFinalizeOnRolloverSumsCoinsEarnedAndFocusBlockMax covers §3.3's
// per-day aggregation: coinsEarned must equal that day's total
// CoinBreakdown, and (Fork B) the finalized LongestFocusBlockSeconds must
// be the MAX FocusRunSeconds observed that day, not merely the last
// value.
func TestFinalizeOnRolloverSumsCoinsEarnedAndFocusBlockMax(t *testing.T) {
	g := New()
	day1 := time.Date(2026, 6, 1, 8, 0, 0, 0, time.Local)
	g.now = func() time.Time { return day1 }

	// Drive one full sprint to completion so awardCoins actually mints a
	// CoinBreakdown for day 1 (sprint 0's target is 50 work units, see
	// game_test.go's own convention).
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, WorkUnits: 50, FocusRunSeconds: 12})
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, FocusRunSeconds: 40}) // higher mid-day peak
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, FocusRunSeconds: 5})  // lower than the peak — must not overwrite it

	wantCoins := g.CoinsTodaySnapshot().Sum()
	if wantCoins == 0 {
		t.Fatal("test setup problem: expected day 1 to have earned some coins before rollover")
	}

	day2 := time.Date(2026, 6, 2, 8, 0, 0, 0, time.Local)
	g.now = func() time.Time { return day2 }
	g.Tick(engine.TickResult{Mood: engine.MoodIdle})

	buckets := g.HistorySnapshot()
	if len(buckets) != 1 {
		t.Fatalf("expected exactly 1 finalized bucket, got %d", len(buckets))
	}
	if buckets[0].CoinsEarned != wantCoins {
		t.Errorf("finalized CoinsEarned = %d, want %d (day 1's full coin total)", buckets[0].CoinsEarned, wantCoins)
	}
	if buckets[0].LongestFocusBlockSeconds != 40 {
		t.Errorf("finalized LongestFocusBlockSeconds = %d, want 40 (the day's max, not the last tick's 5)", buckets[0].LongestFocusBlockSeconds)
	}
}

// --- Restore round-trip smoke test (GO-1's exposed API surface) -----------

// TestRestoreHistoryAndStreakRoundTrip is a lightweight sanity check of
// the Game API surface GO-2 will depend on (HistorySnapshot/
// StreakSnapshot/RestoreHistory/RestoreStreak) — a full save/load
// round-trip through internal/store is GO-2's own test, not this
// package's, but GO-1 must prove these four methods at least agree with
// each other.
func TestRestoreHistoryAndStreakRoundTrip(t *testing.T) {
	g := New()
	seed := []DayBucket{
		{Date: "2026-05-01", Counters: StatCounters{ActiveSeconds: 400}, CoinsEarned: 3, LongestFocusBlockSeconds: 120},
		{Date: "2026-05-02", Counters: StatCounters{ActiveSeconds: 10}, CoinsEarned: 0, LongestFocusBlockSeconds: 0},
	}
	g.RestoreHistory(seed)
	g.RestoreStreak(2, 5, "2026-05-02")

	got := g.HistorySnapshot()
	if len(got) != len(seed) {
		t.Fatalf("HistorySnapshot length = %d, want %d", len(got), len(seed))
	}
	for i := range seed {
		if got[i] != seed[i] {
			t.Errorf("HistorySnapshot[%d] = %+v, want %+v", i, got[i], seed[i])
		}
	}

	current, longest, lastActive := g.StreakSnapshot()
	if current != 2 || longest != 5 || lastActive != "2026-05-02" {
		t.Errorf("StreakSnapshot = (%d %d %s), want (2 5 2026-05-02)", current, longest, lastActive)
	}
}
