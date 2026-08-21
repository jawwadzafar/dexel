package game

import "time"

// Analytics Phase A3 (docs/plan/A3-design.md, ADR 0013): a rolling 30-day
// history of the A1/A2 daily counters + server-computed streaks. See
// package doc comments on StatsView (game.go) for the wire shape and on
// Game's history/streakCurrent/streakLongest/streakLastActiveDate/
// statsFocusBlockMax fields for the persisted shape. This file holds the
// A3-specific types and the finalize/streak/dense-wire-builder logic;
// rolloverStatsIfNewDay (game.go) is the single call site that drives
// finalizeDay.
const (
	// HistoryRetentionDays (§3.1) is the rolling window's length: the wire
	// `history` array always has exactly this many entries, and the
	// persisted sparse `history` slice is pruned to at most this many
	// buckets. A streak is NOT bounded by this — see StreakView's doc
	// comment.
	HistoryRetentionDays = 30

	// ActiveDayMinSeconds (§2.1) is the threshold a local calendar date's
	// StatCounters.ActiveSeconds must reach to count as an "active day"
	// for both the streak algorithm and DayStat.IsActive/DayBucket
	// (default 300s = 5 minutes of real engine.MoodCoding, not one stray
	// keypress).
	ActiveDayMinSeconds = 300
)

// DayBucket is one PERSISTED, finalized day (§3.2) — the sparse storage
// shape internal/store's GO-2 task saves/restores via
// HistorySnapshot/RestoreHistory. Only days that actually ran produce a
// bucket; a day the process never ran is an honest gap, never a
// fabricated zero. Reuses StatCounters verbatim (the same seven A1/A2
// counters) so the existing structural coverage on that type already
// applies here; only the two scalar additions are new.
type DayBucket struct {
	// Date is the local "YYYY-MM-DD" calendar date this bucket finalized
	// for (statsDateFormat).
	Date string
	// Counters is that day's final StatCounters snapshot (statsToday at
	// the moment of finalize).
	Counters StatCounters
	// CoinsEarned is that day's CoinBreakdown.Sum() at finalize (§3.3).
	CoinsEarned uint64
	// LongestFocusBlockSeconds (Fork B) is the max
	// engine.TickResult.FocusRunSeconds observed during this day.
	LongestFocusBlockSeconds uint64
}

// DayStat is the DENSE wire shape (§5) — one entry of StatsView.History.
// Unlike DayBucket, every calendar date in the retained window gets one of
// these, zero-filled for a day with no persisted bucket (or no bucket yet,
// for today). IsActive is set here, server-side, by the exact same
// threshold (§2.1) the streak algorithm uses, so the client never
// re-derives — and can never drift from — "what counts as active".
type DayStat struct {
	Date               string `json:"date"`
	Keystrokes         uint64 `json:"keystrokes"`
	MouseActiveSeconds uint64 `json:"mouseActiveSeconds"`
	ActiveSeconds      uint64 `json:"activeSeconds"`
	IdleSeconds        uint64 `json:"idleSeconds"`
	SprintsCompleted   uint64 `json:"sprintsCompleted"`
	FocusSessions      uint64 `json:"focusSessions"`
	AppSwitches        uint64 `json:"appSwitches"`
	CoinsEarned        uint64 `json:"coinsEarned"`
	IsActive           bool   `json:"isActive"`
	// LongestFocusBlockSeconds (Fork B) mirrors DayBucket's field of the
	// same name; for the final (today, live) entry this is
	// Game.statsFocusBlockMax rather than a finalized bucket's value.
	LongestFocusBlockSeconds uint64 `json:"longestFocusBlockSeconds"`
}

// StreakView is the `stats.streak` wire shape (§2, §5) — the server-
// computed EFFECTIVE streak (today's in-progress activity already folded
// in, per effectiveStreak). The client renders these two numbers verbatim
// and never re-derives them: streaks depend on lastActiveDate, which is
// persisted cross-window state not reconstructible from the sent
// `history` window alone (a streak can outlive HistoryRetentionDays).
type StreakView struct {
	Current int `json:"current"`
	Longest int `json:"longest"`
}

// finalizeDay is §3.3's single append-a-bucket-and-update-the-streak
// operation, called from rolloverStatsIfNewDay (game.go) for the local
// date that just ended, BEFORE statsToday/coinsToday/statsFocusBlockMax
// reset for the new day. date/counters/coins/focusBlockMax are that
// ending day's final values (g.statsDate/g.statsToday/g.coinsToday/
// g.statsFocusBlockMax at the moment rolloverStatsIfNewDay notices the
// date changed).
func (g *Game) finalizeDay(date string, counters StatCounters, coins CoinBreakdown, focusBlockMax uint64) {
	g.history = append(g.history, DayBucket{
		Date:                     date,
		Counters:                 counters,
		CoinsEarned:              coins.Sum(),
		LongestFocusBlockSeconds: focusBlockMax,
	})
	if len(g.history) > HistoryRetentionDays {
		g.history = g.history[len(g.history)-HistoryRetentionDays:]
	}
	g.updateStreak(date, counters.ActiveSeconds >= ActiveDayMinSeconds)
}

// updateStreak is §2.3's streak update, applied at finalize for the day
// that just ended (date, dD in the design doc) with its active flag
// already decided by the caller (finalizeDay).
func (g *Game) updateStreak(date string, active bool) {
	// An inactive finished day never extends the streak; aliveness for a
	// currently-in-progress today is decided at READ time instead (§2.4,
	// effectiveStreak) — finalize only ever runs for days that have
	// already ended.
	if !active {
		return
	}

	switch {
	case g.streakLastActiveDate == "":
		g.streakCurrent = 1
	case date == addDaysToDateString(g.streakLastActiveDate, 1):
		// Consecutive calendar date: extend the run.
		g.streakCurrent++
	case date == g.streakLastActiveDate:
		// Re-finalize guard: the same day finalized twice (should not
		// happen via the normal rollover call site, but finalizeDay must
		// stay idempotent for this exact date) — no-op on the counter.
	default:
		// Any other date (a gap of >1 calendar day since
		// lastActiveDate) starts a fresh run of 1.
		g.streakCurrent = 1
	}

	g.streakLastActiveDate = date
	if g.streakCurrent > g.streakLongest {
		g.streakLongest = g.streakCurrent
	}
}

// effectiveStreak is §2.4's read-time computation: today's in-progress
// activity is folded in WITHOUT mutating the persisted streakCurrent/
// streakLongest/streakLastActiveDate — those only ever change at
// finalizeDay. today is g.statsDate (the local date statsToday currently
// represents); todayActive is whether statsToday's ActiveSeconds has
// already crossed ActiveDayMinSeconds.
func (g *Game) effectiveStreak(today string, todayActive bool) StreakView {
	var base int
	switch {
	case g.streakLastActiveDate == "":
		base = 0
	case g.streakLastActiveDate == today:
		// Defensive: lastActiveDate already IS today (e.g. a same-day
		// re-finalize guard fired earlier today) — already counted, no
		// further +1 below (the guard on the final line handles this).
		base = g.streakCurrent
	case g.streakLastActiveDate == addDaysToDateString(today, -1):
		// The run is alive through yesterday — still shown even before
		// today's first active tick.
		base = g.streakCurrent
	default:
		// Last active >= 2 days ago (or never): the streak is dead.
		base = 0
	}

	current := base
	if todayActive && g.streakLastActiveDate != today {
		// No-double-count guard: only add today's +1 if lastActiveDate
		// isn't already today (which would mean base already counts it).
		current = base + 1
	}

	longest := g.streakLongest
	if current > longest {
		longest = current
	}
	return StreakView{Current: current, Longest: longest}
}

// buildStreakView is State()'s entry point for the `stats.streak` field —
// effectiveStreak applied to the CURRENT live day (g.statsDate/
// g.statsToday), which is always fresh by the time State() runs: every
// caller of State() (main.go's Tick loop, and the initial post-load
// State() right after store.Apply's RestoreStats) already ran
// rolloverStatsIfNewDay first.
func (g *Game) buildStreakView() StreakView {
	todayActive := g.statsToday.ActiveSeconds >= ActiveDayMinSeconds
	return g.effectiveStreak(g.statsDate, todayActive)
}

// buildHistoryView is §5's dense-wire builder: exactly HistoryRetentionDays
// entries, one per local calendar date from today-(N-1) to today
// inclusive, ascending, zero-filled for any date with no persisted
// DayBucket. The final entry is always today, LIVE — built from
// statsToday/coinsToday/statsFocusBlockMax rather than a finalized
// bucket, so it grows through the day.
func (g *Game) buildHistoryView() []DayStat {
	today := g.statsDate

	byDate := make(map[string]DayBucket, len(g.history))
	for _, b := range g.history {
		byDate[b.Date] = b
	}

	out := make([]DayStat, HistoryRetentionDays)
	start := addDaysToDateString(today, -(HistoryRetentionDays - 1))
	for i := 0; i < HistoryRetentionDays; i++ {
		date := addDaysToDateString(start, i)
		if date == today {
			out[i] = dayStatFromCounters(date, g.statsToday, g.coinsToday.Sum(), g.statsFocusBlockMax)
			continue
		}
		if b, ok := byDate[date]; ok {
			out[i] = dayStatFromCounters(date, b.Counters, b.CoinsEarned, b.LongestFocusBlockSeconds)
			continue
		}
		out[i] = DayStat{Date: date}
	}
	return out
}

// dayStatFromCounters builds one dense DayStat from a StatCounters +
// coinsEarned + focusBlockMax — shared by buildHistoryView's "today,
// live" branch and its "persisted bucket" branch so the two stay
// identical in shape.
func dayStatFromCounters(date string, c StatCounters, coinsEarned uint64, focusBlockMax uint64) DayStat {
	return DayStat{
		Date:                     date,
		Keystrokes:               c.Keystrokes,
		MouseActiveSeconds:       c.MouseActiveSeconds,
		ActiveSeconds:            c.ActiveSeconds,
		IdleSeconds:              c.IdleSeconds,
		SprintsCompleted:         c.SprintsCompleted,
		FocusSessions:            c.FocusSessions,
		AppSwitches:              c.AppSwitches,
		CoinsEarned:              coinsEarned,
		IsActive:                 c.ActiveSeconds >= ActiveDayMinSeconds,
		LongestFocusBlockSeconds: focusBlockMax,
	}
}

// addDaysToDateString is the calendar-date arithmetic §2.1 requires: pure
// Year/Month/Day field math (time.Time.AddDate), never a 24-hour
// duration delta, on date-only values parsed with no location component
// (statsDateFormat has no time-of-day, so parsing is location-
// independent) — this is what makes DST transitions and month/year
// boundaries impossible to corrupt the adjacency check. date must already
// be in statsDateFormat ("YYYY-MM-DD"); a malformed input (should never
// happen — every date string in this package is produced by
// now().Local().Format(statsDateFormat)) returns date unchanged rather
// than panicking.
func addDaysToDateString(date string, days int) string {
	t, err := time.Parse(statsDateFormat, date)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, days).Format(statsDateFormat)
}

// HistorySnapshot returns the persisted, sparse history buckets for
// internal/store's GO-2 Snapshot to save — oldest first, at most
// HistoryRetentionDays long (§7 pinned contract).
func (g *Game) HistorySnapshot() []DayBucket {
	out := make([]DayBucket, len(g.history))
	copy(out, g.history)
	return out
}

// StreakSnapshot returns the persisted streak state for internal/store's
// GO-2 Snapshot to save (§7 pinned contract).
func (g *Game) StreakSnapshot() (current, longest int, lastActiveDate string) {
	return g.streakCurrent, g.streakLongest, g.streakLastActiveDate
}

// RestoreHistory sets the persisted history buckets directly (bypassing
// finalizeDay) — used only by internal/store when loading a save. Per §4
// ("Restore ordering"), the caller MUST invoke this — and RestoreStreak —
// BEFORE RestoreStats: RestoreStats triggers rolloverStatsIfNewDay, whose
// finalizeDay call (if the restored date is stale) appends to and updates
// whatever history/streak are already in place at that moment, so calling
// this first is what makes a multi-day-gap reload finalize into the
// RESTORED history/streak rather than empty ones. Input is defensively
// re-pruned to HistoryRetentionDays (a corrupted or hand-edited save can
// never resurrect a longer window than the game itself would ever
// produce).
func (g *Game) RestoreHistory(history []DayBucket) {
	g.history = append([]DayBucket(nil), history...)
	if len(g.history) > HistoryRetentionDays {
		g.history = g.history[len(g.history)-HistoryRetentionDays:]
	}
}

// RestoreStreak sets the persisted streak state directly (bypassing
// updateStreak) — used only by internal/store when loading a save. See
// RestoreHistory's doc comment for the required call ordering relative to
// RestoreStats.
func (g *Game) RestoreStreak(current, longest int, lastActiveDate string) {
	g.streakCurrent = current
	g.streakLongest = longest
	g.streakLastActiveDate = lastActiveDate
}
