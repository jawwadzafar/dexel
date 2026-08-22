package game

import (
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/activity"
	"github.com/jawwadzafar/dexel/app/internal/engine"
)

// sessionTestTicks builds a deterministic, varied tick sequence (reusing
// coins_test.go's tr() helper, which reproduces engine.Engine.Tick's own
// WorkUnits formula exactly) — enough variety and length to complete
// several sprints, exercising every counter this file touches.
func sessionTestTicks(n int) []engine.TickResult {
	out := make([]engine.TickResult, n)
	for i := 0; i < n; i++ {
		key := uint64(i % 7)
		mouse := i%3 == 0
		var focus, switches uint64
		if i%5 == 4 {
			focus = 1
		}
		if i%17 == 0 {
			switches = 1
		}
		out[i] = tr(key, mouse, focus, switches)
	}
	return out
}

// fakeClock is a small mutable time source shared by one or more *Game
// instances in a test, so their g.now fields can be driven in lockstep
// (docs/plan/P2-design.md §7.2's restart/backdating tests all need
// deterministic, shared timestamps).
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newFakeClock() *fakeClock {
	// time.Local (not UTC): the game's own date bucketing
	// (rolloverStatsIfNewDay) reads g.now().Local(), and this sandbox's
	// system zone is not UTC — constructing in time.Local keeps every
	// test's "local date" reasoning correct regardless of the machine's
	// offset.
	return &fakeClock{t: time.Date(2026, 3, 10, 9, 0, 0, 0, time.Local)}
}

// --- §7.2: accounting invariants ---------------------------------------

// TestSessionCountsAreSubsetOfLifetime asserts, at every checkpoint
// through a running session, that the live session view is EXACTLY
// lifetime_now - lifetime_at_start for every counter, and therefore never
// exceeds the lifetime bucket itself — the "session ⊆ global, no
// double-counting" guarantee §2.3 says holds BY CONSTRUCTION.
func TestSessionCountsAreSubsetOfLifetime(t *testing.T) {
	g := New()
	if err := g.StartSession("proj"); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	for i, r := range sessionTestTicks(200) {
		g.Tick(r)
		if i%25 != 0 {
			continue
		}
		rec, ok := g.ActiveSession()
		if !ok {
			t.Fatalf("tick %d: ActiveSession() reports no session", i)
		}
		_, _, lifetime := g.StatsSnapshot()
		want := subtractCounters(lifetime, StatCounters{})
		if rec.Counters != want {
			t.Fatalf("tick %d: session counters = %+v, want lifetime-since-start %+v", i, rec.Counters, want)
		}
		if rec.Counters.Keystrokes > lifetime.Keystrokes ||
			rec.Counters.MouseActiveSeconds > lifetime.MouseActiveSeconds ||
			rec.Counters.ActiveSeconds > lifetime.ActiveSeconds ||
			rec.Counters.IdleSeconds > lifetime.IdleSeconds {
			t.Fatalf("tick %d: session counters %+v exceed lifetime %+v", i, rec.Counters, lifetime)
		}
	}
}

// TestSessionDoesNotAffectTheEconomyAtAll is the LENS property (§1, §7.1):
// the exact same tick sequence run with a session open, versus with none
// ever started, must yield a byte-identical economy at every checkpoint —
// DevCash, XP, Progress, sprintIndex, and every today/lifetime counter.
func TestSessionDoesNotAffectTheEconomyAtAll(t *testing.T) {
	withSession := New()
	if err := withSession.StartSession("control"); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	without := New()

	ticks := sessionTestTicks(600)
	for i, r := range ticks {
		completedWith := withSession.Tick(r)
		completedWithout := without.Tick(r)
		if completedWith != completedWithout {
			t.Fatalf("tick %d: completed differs (with session=%v, without=%v)", i, completedWith, completedWithout)
		}
		if withSession.DevCash != without.DevCash {
			t.Fatalf("tick %d: DevCash differs: %d vs %d", i, withSession.DevCash, without.DevCash)
		}
		if withSession.XP != without.XP {
			t.Fatalf("tick %d: XP differs: %d vs %d", i, withSession.XP, without.XP)
		}
		if withSession.Progress != without.Progress {
			t.Fatalf("tick %d: Progress differs: %v vs %v", i, withSession.Progress, without.Progress)
		}
		if withSession.SprintIndex() != without.SprintIndex() {
			t.Fatalf("tick %d: sprintIndex differs: %d vs %d", i, withSession.SprintIndex(), without.SprintIndex())
		}
	}

	sw, sw2 := withSession.State(), without.State()
	if sw.Stats.Today != sw2.Stats.Today {
		t.Fatalf("Stats.Today differs: %+v vs %+v", sw.Stats.Today, sw2.Stats.Today)
	}
	if sw.Stats.Lifetime != sw2.Stats.Lifetime {
		t.Fatalf("Stats.Lifetime differs: %+v vs %+v", sw.Stats.Lifetime, sw2.Stats.Lifetime)
	}
	if sw.Stats.CoinsToday != sw2.Stats.CoinsToday {
		t.Fatalf("Stats.CoinsToday differs: %+v vs %+v", sw.Stats.CoinsToday, sw2.Stats.CoinsToday)
	}
	if withSession.DevCash == 0 {
		t.Fatal("test is vacuous: no sprint ever completed (DevCash still 0)")
	}
}

// TestSessionCompleteGrantsNoCoinsOrXP re-proves ADR 0005/0008: ending a
// session (even one during which sprints completed and coins were
// tracked as "earned during it") leaves DevCash/XP/Progress exactly as
// they already were the instant before the stop.
func TestSessionCompleteGrantsNoCoinsOrXP(t *testing.T) {
	g := New()
	if err := g.StartSession("proj"); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	for _, r := range sessionTestTicks(150) {
		g.Tick(r)
	}
	// Make sure the session survives past the discard threshold.
	g.now = func() time.Time { return time.Now().Add(time.Minute) }

	wantDevCash, wantXP, wantProgress := g.DevCash, g.XP, g.Progress

	if err := g.StopSession(); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	if g.DevCash != wantDevCash || g.XP != wantXP || g.Progress != wantProgress {
		t.Fatalf("stopping a session mutated the economy: DevCash %d->%d, XP %d->%d, Progress %v->%v",
			wantDevCash, g.DevCash, wantXP, g.XP, wantProgress, g.Progress)
	}

	rec, ok := g.TakeEndedSession()
	if !ok {
		t.Fatal("TakeEndedSession reported nothing pending after a >60s stop")
	}
	if rec.CoinsEarned == 0 {
		t.Fatal("test is vacuous: no coins were tracked as earned during the session")
	}
	if g.DevCash != wantDevCash {
		t.Fatalf("DevCash changed after popping the ended session: %d -> %d", wantDevCash, g.DevCash)
	}
}

// TestStartStopGrantsNothing mashes SESSION_START/SESSION_STOP 1,000
// times (each pair completing in well under a second) and asserts the
// anti-mash guarantee is structural: no coins, no log rows, no completed
// count — not even one.
func TestStartStopGrantsNothing(t *testing.T) {
	g := New()
	for i := 0; i < 1000; i++ {
		if err := g.StartSession(""); err != nil {
			t.Fatalf("iteration %d: StartSession: %v", i, err)
		}
		if err := g.StopSession(); err != nil {
			t.Fatalf("iteration %d: StopSession: %v", i, err)
		}
		if _, ok := g.TakeEndedSession(); ok {
			t.Fatalf("iteration %d: a sub-minute stop produced a poppable record", i)
		}
	}
	if g.DevCash != 0 || g.XP != 0 {
		t.Fatalf("DevCash/XP moved from start/stop mashing: %d / %d", g.DevCash, g.XP)
	}
	if n := len(g.SessionLogSnapshot()); n != 0 {
		t.Fatalf("sessionLog has %d entries, want 0", n)
	}
	if c := g.State().Sessions.Summary.Completed; c != 0 {
		t.Fatalf("Summary.Completed = %d, want 0", c)
	}
}

// TestShortSessionIsDiscarded is Fork P2-E: a session under
// SessionMinDurationSeconds produces no record, no log row, no counter —
// and StopSession itself still reports success (a discard is never an
// error/scold).
func TestShortSessionIsDiscarded(t *testing.T) {
	clock := newFakeClock()
	g := New()
	g.now = clock.now

	if err := g.StartSession("too quick"); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	clock.advance(30 * time.Second) // < SessionMinDurationSeconds

	if err := g.StopSession(); err != nil {
		t.Fatalf("StopSession on a short session returned an error: %v", err)
	}
	if _, ok := g.TakeEndedSession(); ok {
		t.Fatal("a 30s session produced a poppable record")
	}
	if _, ok := g.ActiveSession(); ok {
		t.Fatal("the discarded session is still reported active")
	}
	if n := len(g.SessionLogSnapshot()); n != 0 {
		t.Fatalf("sessionLog has %d entries, want 0", n)
	}
	// The id is left to be reused (§2.2 step 2) — the next start gets the
	// same ordinal since sessionLog never grew.
	if err := g.StartSession(""); err != nil {
		t.Fatalf("StartSession (reuse): %v", err)
	}
	rec, _ := g.ActiveSession()
	if rec.ID != 1 {
		t.Fatalf("reused session id = %d, want 1 (the discarded attempt must not have consumed an id)", rec.ID)
	}
}

// TestSessionSurvivesRestartWithoutDoubleCounting simulates a process
// restart mid-session: g1's live state is captured through the exact
// Snapshot seams internal/store's GO-2 will use (StatsSnapshot,
// ActiveSessionSnapshot), fed into a fresh g2 through the matching
// Restore seams, and g2 must continue the SAME session from exactly where
// g1 left off — never resetting to zero, never double-counting the
// restored baseline.
func TestSessionSurvivesRestartWithoutDoubleCounting(t *testing.T) {
	clock := newFakeClock()
	g1 := New()
	g1.now = clock.now

	// A little activity before the session even starts, so baseline != 0
	// — the strongest form of the "subset, not from-zero" claim.
	for _, r := range sessionTestTicks(20) {
		g1.Tick(r)
	}
	if err := g1.StartSession("carry-over"); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	for _, r := range sessionTestTicks(80) {
		clock.advance(time.Second)
		g1.Tick(r)
	}

	preRestartRec, ok := g1.ActiveSession()
	if !ok {
		t.Fatal("g1 lost its active session before restart")
	}

	date, today, lifetime := g1.StatsSnapshot()
	activeOK, id, startedAt, lastActivityAt, baseline, watermark, coinsEarned, focusMax := g1.ActiveSessionSnapshot()
	if !activeOK {
		t.Fatal("ActiveSessionSnapshot reports no active session")
	}
	logSnapshot := g1.SessionLogSnapshot()

	g2 := New()
	g2.now = clock.now
	// Mirrors internal/store's documented restore ordering (§8: restore
	// the session log/names before RestoreStats).
	g2.RestoreSessionLog(logSnapshot)
	g2.RestoreActiveSession(id, startedAt, lastActivityAt, baseline, watermark, coinsEarned, focusMax)
	g2.RestoreStats(date, today, lifetime)

	postRestoreRec, ok := g2.ActiveSession()
	if !ok {
		t.Fatal("g2 did not restore an active session")
	}
	if postRestoreRec.Counters != preRestartRec.Counters {
		t.Fatalf("restored session counters = %+v, want %+v (unchanged across restart)", postRestoreRec.Counters, preRestartRec.Counters)
	}
	if postRestoreRec.ID != preRestartRec.ID {
		t.Fatalf("restored session id = %d, want %d", postRestoreRec.ID, preRestartRec.ID)
	}

	// Continue ticking g2: counters must continue growing from the
	// restored point, never resetting to zero and never double-adding
	// the baseline that was already folded in before restart.
	for _, r := range sessionTestTicks(30) {
		clock.advance(time.Second)
		g2.Tick(r)
	}
	afterRec, _ := g2.ActiveSession()
	if afterRec.Counters.Keystrokes < postRestoreRec.Counters.Keystrokes {
		t.Fatalf("session keystrokes went BACKWARDS after restart+more ticks: %d -> %d",
			postRestoreRec.Counters.Keystrokes, afterRec.Counters.Keystrokes)
	}
	_, _, lifetimeAfter := g2.StatsSnapshot()
	wantAfter := subtractCounters(lifetimeAfter, baseline)
	if afterRec.Counters != wantAfter {
		t.Fatalf("post-restart session counters = %+v, want lifetime-since-baseline %+v (no double count)", afterRec.Counters, wantAfter)
	}
}

// TestSessionSpansMidnight asserts a session crossing local midnight is a
// non-event for the session itself (§2.4): today's bucket resets, but the
// session (a pure function of the never-resetting lifetime bucket) keeps
// growing, and the session does NOT end.
func TestSessionSpansMidnight(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 3, 10, 23, 59, 0, 0, time.Local)}
	g := New()
	g.now = clock.now

	if err := g.StartSession("overnight"); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	g.Tick(tr(5, false, 0, 0)) // before midnight

	preDate, preToday, _ := g.StatsSnapshot()
	if preDate != "2026-03-10" {
		t.Fatalf("statsDate = %q before midnight, want 2026-03-10", preDate)
	}

	clock.advance(2 * time.Minute) // now 00:01:00 on the 11th
	g.Tick(tr(7, false, 0, 0))     // this tick crosses midnight

	postDate, postToday, lifetime := g.StatsSnapshot()
	if postDate != "2026-03-11" {
		t.Fatalf("statsDate = %q after midnight, want 2026-03-11", postDate)
	}
	if postToday.Keystrokes != 7 {
		t.Fatalf("today.Keystrokes after rollover = %d, want 7 (reset, then this tick's 7)", postToday.Keystrokes)
	}
	if preToday.Keystrokes != 5 {
		t.Fatalf("sanity: pre-midnight today.Keystrokes = %d, want 5", preToday.Keystrokes)
	}

	rec, ok := g.ActiveSession()
	if !ok {
		t.Fatal("the session ended across midnight — it must not")
	}
	if rec.Counters.Keystrokes != lifetime.Keystrokes {
		t.Fatalf("session keystrokes = %d, want %d (lifetime since a zero baseline)", rec.Counters.Keystrokes, lifetime.Keystrokes)
	}
	if rec.Counters.Keystrokes != 12 {
		t.Fatalf("session keystrokes = %d, want 12 (5 before + 7 after midnight)", rec.Counters.Keystrokes)
	}
}

// TestSessionAccruesWhileStoreOpen re-proves §2.4's split rule: session
// counters keep advancing while STORE_OPEN (the analytics rule), but
// coinsEarned provably does not (the economy rule) — even across enough
// ticks to have completed a sprint had the store been closed.
func TestSessionAccruesWhileStoreOpen(t *testing.T) {
	g := New()
	if err := g.StartSession("shopping"); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	const connID = 1
	g.OpenStore(connID)

	for _, r := range sessionTestTicks(200) {
		g.Tick(r)
	}

	rec, ok := g.ActiveSession()
	if !ok {
		t.Fatal("session ended while store was open")
	}
	if rec.Counters.Keystrokes == 0 {
		t.Fatal("session keystrokes did not advance while STORE_OPEN")
	}
	if rec.CoinsEarned != 0 {
		t.Fatalf("session coinsEarned = %d while STORE_OPEN, want 0", rec.CoinsEarned)
	}
	if g.DevCash != 0 {
		t.Fatalf("DevCash = %d while STORE_OPEN, want 0 (economy frozen)", g.DevCash)
	}

	g.CloseStore(connID)
}

// --- §7.2: auto-end -------------------------------------------------------

// TestIdleAutoEndBackdatesTheEnd: after SessionIdleTimeoutSeconds of no
// real input (global provider), the session ends on the NEXT tick,
// backdated to the last real activity, with counters taken as of that
// same watermark — never claiming the idle gap as part of the session.
func TestIdleAutoEndBackdatesTheEnd(t *testing.T) {
	clock := newFakeClock()
	g := New()
	g.now = clock.now

	if err := g.StartSession("focused"); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	clock.advance(5 * time.Minute)
	g.Tick(tr(10, false, 0, 0)) // the last real activity
	lastActivity := clock.t

	clock.advance(SessionIdleTimeoutSeconds*time.Second + time.Second) // well past the 2h timeout
	completed := g.Tick(engine.TickResult{Mood: engine.MoodOnBreak})   // no real input; global honesty (default)
	if completed {
		t.Fatal("idle-detecting tick reported a sprint completion, unexpected")
	}

	rec, ok := g.TakeEndedSession()
	if !ok {
		t.Fatal("idle timeout did not end the session")
	}
	if rec.EndReason != endReasonIdle {
		t.Fatalf("endReason = %q, want %q", rec.EndReason, endReasonIdle)
	}
	if !rec.EndedAt.Equal(lastActivity) {
		t.Fatalf("endedAt = %v, want the backdated last-activity time %v", rec.EndedAt, lastActivity)
	}
	if rec.DurationSeconds != 300 {
		t.Fatalf("durationSeconds = %d, want 300 (the 5 minutes before the idle gap)", rec.DurationSeconds)
	}
	if rec.Counters.Keystrokes != 10 {
		t.Fatalf("session keystrokes = %d, want 10 (only the pre-idle tick's contribution)", rec.Counters.Keystrokes)
	}
	if rec.Counters.IdleSeconds >= SessionIdleTimeoutSeconds {
		t.Fatalf("session idleSeconds = %d includes the post-idle-gap silence — must be excluded by the backdated watermark", rec.Counters.IdleSeconds)
	}
	if _, active := g.ActiveSession(); active {
		t.Fatal("session still reports active after the idle auto-end")
	}
}

// TestBlindProviderNeverIdleAutoEnds: a blind-honesty tick stream must
// never trigger the idle rule (ADR 0010 — "idle" is unknowable to a blind
// provider) no matter how long the gap; only the 16h hard cap can end it.
func TestBlindProviderNeverIdleAutoEnds(t *testing.T) {
	clock := newFakeClock()
	g := New()
	g.now = clock.now

	if err := g.StartSession(""); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	clock.advance(time.Minute)
	g.Tick(engine.TickResult{Honesty: activity.HonestyBlind, Mood: engine.MoodCoding, KeystrokeDelta: 5})

	// Blow well past the idle timeout with no further real input, still
	// blind — the idle rule must not fire.
	clock.advance(3 * time.Hour)
	g.Tick(engine.TickResult{Honesty: activity.HonestyBlind, Mood: engine.MoodIdle})
	if _, ok := g.TakeEndedSession(); ok {
		t.Fatal("a blind provider's idle gap ended the session — ADR 0010 violation")
	}
	if _, ok := g.ActiveSession(); !ok {
		t.Fatal("session unexpectedly ended")
	}

	// Push total elapsed past the 16h hard cap — the fallback a blind
	// provider actually has.
	clock.t = clock.t.Add(SessionMaxDurationSeconds * time.Second)
	g.Tick(engine.TickResult{Honesty: activity.HonestyBlind, Mood: engine.MoodIdle})

	rec, ok := g.TakeEndedSession()
	if !ok {
		t.Fatal("the 16h cap did not end a blind provider's session")
	}
	if rec.EndReason != endReasonMaxDuration {
		t.Fatalf("endReason = %q, want %q", rec.EndReason, endReasonMaxDuration)
	}
}

// TestMaxDurationCap covers the 16h hard cap two ways: the ordinary tick
// flow (duration bounded BY the cap, honestly backdated to the last real
// activity before it) and the defensive clamp for a corrupted/hand-edited
// restore whose lastActivityAt somehow already exceeds its own cap
// boundary (duration then equals the cap EXACTLY, never more).
func TestMaxDurationCap(t *testing.T) {
	t.Run("ordinary flow never exceeds the cap", func(t *testing.T) {
		clock := newFakeClock()
		g := New()
		g.now = clock.now
		if err := g.StartSession(""); err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		// Real activity every hour — comfortably under the 2h idle
		// timeout throughout, so ONLY the 16h hard cap is under test
		// here (a single jump straight to the boundary would trip the
		// idle rule first, since lastActivityAt would still be stale
		// from SESSION_START).
		for i := 0; i < 16; i++ {
			clock.advance(time.Hour)
			g.Tick(tr(3, false, 0, 0))
		}

		rec, ok := g.TakeEndedSession()
		if !ok {
			t.Fatal("the 16h cap did not end the session")
		}
		if rec.EndReason != endReasonMaxDuration {
			t.Fatalf("endReason = %q, want %q", rec.EndReason, endReasonMaxDuration)
		}
		if rec.DurationSeconds > SessionMaxDurationSeconds {
			t.Fatalf("durationSeconds = %d, exceeds the %d cap", rec.DurationSeconds, SessionMaxDurationSeconds)
		}
	})

	t.Run("the clamp holds exactly at the cap for a corrupted restore", func(t *testing.T) {
		clock := newFakeClock()
		g := New()
		g.now = clock.now

		startedAt := clock.t
		// A hand-edited/corrupted lastActivityAt one hour PAST this
		// session's own cap boundary — the defensive branch
		// (endAt.After(capBoundary)) exists precisely for this.
		corruptLastActivity := startedAt.Add(SessionMaxDurationSeconds*time.Second + time.Hour)
		g.RestoreActiveSession(1, startedAt, corruptLastActivity, StatCounters{}, StatCounters{}, 0, 0)

		clock.t = corruptLastActivity
		g.Tick(engine.TickResult{Mood: engine.MoodIdle})

		rec, ok := g.TakeEndedSession()
		if !ok {
			t.Fatal("the cap did not fire against a corrupted restore")
		}
		if rec.EndReason != endReasonMaxDuration {
			t.Fatalf("endReason = %q, want %q", rec.EndReason, endReasonMaxDuration)
		}
		if rec.DurationSeconds != SessionMaxDurationSeconds {
			t.Fatalf("durationSeconds = %d, want exactly the cap (%d)", rec.DurationSeconds, SessionMaxDurationSeconds)
		}
	})
}

// TestReopenAfterLongCloseEndsOnFirstTick: a session restored from a save
// written many hours ago ends on the very FIRST tick after boot,
// backdated to its restored lastActivityAt — the self-heal §2.5 promises
// instead of inventing a multi-hour session out of the closed-app gap.
func TestReopenAfterLongCloseEndsOnFirstTick(t *testing.T) {
	clock := newFakeClock()
	startedAt := clock.t
	lastActivity := startedAt.Add(20 * time.Minute)

	g := New()
	g.now = clock.now
	g.RestoreActiveSession(3, startedAt, lastActivity, StatCounters{Keystrokes: 40}, StatCounters{Keystrokes: 91}, 12, 300)

	clock.t = lastActivity.Add(10 * time.Hour) // "reopened" long after last activity

	g.Tick(engine.TickResult{Mood: engine.MoodIdle}) // the very first tick after "boot"

	rec, ok := g.TakeEndedSession()
	if !ok {
		t.Fatal("the stale session did not end on the first tick")
	}
	if rec.EndReason != endReasonIdle {
		t.Fatalf("endReason = %q, want %q", rec.EndReason, endReasonIdle)
	}
	if !rec.EndedAt.Equal(lastActivity) {
		t.Fatalf("endedAt = %v, want the restored lastActivityAt %v", rec.EndedAt, lastActivity)
	}
	if rec.Counters.Keystrokes != 51 { // 91 - 40
		t.Fatalf("session keystrokes = %d, want 51 (restored watermark - baseline)", rec.Counters.Keystrokes)
	}
}

// TestSecondStartRejected / TestStopWithoutSessionRejected: both failure
// modes leave state untouched and report the pinned sentinel errors.
func TestSecondStartRejected(t *testing.T) {
	g := New()
	if err := g.StartSession("first"); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	before, _ := g.ActiveSession()

	if err := g.StartSession("second"); err != ErrSessionAlreadyActive {
		t.Fatalf("second StartSession error = %v, want ErrSessionAlreadyActive", err)
	}
	after, ok := g.ActiveSession()
	if !ok || after.ID != before.ID {
		t.Fatal("the rejected second start mutated the active session")
	}
}

func TestStopWithoutSessionRejected(t *testing.T) {
	g := New()
	if err := g.StopSession(); err != ErrSessionNotActive {
		t.Fatalf("StopSession with none active = %v, want ErrSessionNotActive", err)
	}
	if _, ok := g.TakeEndedSession(); ok {
		t.Fatal("a rejected stop produced a poppable record")
	}
}

// TestSessionFocusBlockMaxNeverExceedsDayMax: the session-scoped max
// (spanning however many days the session has been open) is always >=
// the CURRENT day's own max, and strictly greater once a taller block
// happened on an earlier day within the same session.
func TestSessionFocusBlockMaxNeverExceedsDayMax(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 3, 10, 23, 58, 0, 0, time.Local)}
	g := New()
	g.now = clock.now

	if err := g.StartSession("multi-day"); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// KeystrokeDelta > 0 alongside FocusRunSeconds: a real sustained-typing
	// block implies real keystrokes, which is also what keeps
	// lastActivityAt/watermark from going stale. The gap to day 2 is kept
	// well under the 2h idle timeout (a few minutes, exactly like
	// TestSessionSpansMidnight) so ONLY the midnight rollover is under
	// test here, not the auto-end rule.
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, KeystrokeDelta: 5, FocusRunSeconds: 900}) // day 1's tall block
	rec, _ := g.ActiveSession()
	if rec.LongestFocusBlockSeconds != 900 {
		t.Fatalf("session focus max = %d, want 900", rec.LongestFocusBlockSeconds)
	}

	clock.advance(3 * time.Minute) // cross into day 2
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, KeystrokeDelta: 5, FocusRunSeconds: 120})

	today := g.State().Stats.History[len(g.State().Stats.History)-1]
	rec, _ = g.ActiveSession()
	if today.LongestFocusBlockSeconds != 120 {
		t.Fatalf("today's (day 2) focus max = %d, want 120", today.LongestFocusBlockSeconds)
	}
	if rec.LongestFocusBlockSeconds != 900 {
		t.Fatalf("session focus max = %d, want 900 (day 1's block still the session-wide max)", rec.LongestFocusBlockSeconds)
	}
	if rec.LongestFocusBlockSeconds < today.LongestFocusBlockSeconds {
		t.Fatalf("session focus max %d is less than today's %d — invariant violated", rec.LongestFocusBlockSeconds, today.LongestFocusBlockSeconds)
	}
}

// TestSessionsThisWeekWindow: the wire's `sessions.summary.thisWeek`
// counts finished sessions whose local end-date falls within the last
// SessionsWeekDays local dates INCLUDING today, and excludes one dated
// exactly SessionsWeekDays days ago.
func TestSessionsThisWeekWindow(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 3, 15, 12, 0, 0, 0, time.Local)} // "today"
	g := New()
	g.now = clock.now

	mk := func(id int, endedDaysAgo int) SessionRecord {
		ended := clock.t.AddDate(0, 0, -endedDaysAgo)
		return SessionRecord{
			ID:              id,
			StartedAt:       ended.Add(-time.Hour),
			EndedAt:         ended,
			DurationSeconds: 3600,
			EndReason:       endReasonUser,
		}
	}

	g.RestoreSessionLog([]SessionRecord{
		mk(1, 0), // today — inside the window
		mk(2, 6), // exactly SessionsWeekDays-1 days ago — inside (the window's far edge)
		mk(3, 7), // exactly SessionsWeekDays days ago — OUTSIDE the window
		mk(4, 20),
	})

	summary := g.State().Sessions.Summary
	if summary.Completed != 4 {
		t.Fatalf("Completed = %d, want 4", summary.Completed)
	}
	if summary.ThisWeek != 2 {
		t.Fatalf("ThisWeek = %d, want 2 (ids 1 and 2; id 3 is exactly %d days ago and must be excluded)", summary.ThisWeek, SessionsWeekDays)
	}
	if summary.LongestSessionSeconds != 3600 {
		t.Fatalf("LongestSessionSeconds = %d, want 3600", summary.LongestSessionSeconds)
	}
}

// TestSessionNameNormalization: control characters dropped, surrounding
// whitespace trimmed, truncated at MaxSessionNameLen runes, and — unlike
// a dexel's own name — an empty result is legal, never an error.
func TestSessionNameNormalization(t *testing.T) {
	g := New()

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain", "auth refactor", "auth refactor"},
		{"control chars dropped", "auth\x00\x07 refactor\x1b", "auth refactor"},
		{"trimmed", "   auth refactor   ", "auth refactor"},
		{"empty is legal", "", ""},
		{"whitespace-only normalizes to empty", "   \t  ", ""},
		{"control-only normalizes to empty", "\x00\x01\x02", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := g.NormalizeSessionName(c.raw); got != c.want {
				t.Errorf("NormalizeSessionName(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}

	long := ""
	for i := 0; i < 40; i++ {
		long += "x"
	}
	got := g.NormalizeSessionName(long)
	if len([]rune(got)) != MaxSessionNameLen {
		t.Fatalf("a 40-rune name normalized to %d runes, want %d", len([]rune(got)), MaxSessionNameLen)
	}
}

// TestSessionNamesRoundTripThroughStartAndRestore covers §2.7's
// name-storage split end to end within this package's surface:
// SESSION_START seeds sessionNames keyed by id, RestoreSessionNames
// degrades a malformed entry, and neither ever appears on a
// SessionRecord (structurally impossible — see SessionRecord's fields).
func TestSessionNamesRoundTripThroughStartAndRestore(t *testing.T) {
	g := New()
	if err := g.StartSession("auth refactor"); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	rec, _ := g.ActiveSession()
	if got := g.SessionNames()[rec.ID]; got != "auth refactor" {
		t.Fatalf("SessionNames()[%d] = %q, want %q", rec.ID, got, "auth refactor")
	}

	if err := g.StopSession(); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	g.now = func() time.Time { return time.Now().Add(time.Minute) }

	// Starting unnamed at the SAME id (since the previous, >=60s session
	// stop wasn't discarded, the log grew — but starting a name for a
	// FRESH id and then clearing it exercises the "unnamed deletes the
	// key" path directly.
	if err := g.StartSession("temp"); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	rec2, _ := g.ActiveSession()
	if err := g.StopSession(); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	if err := g.StartSession(""); err != nil {
		t.Fatalf("StartSession (unnamed reuse would need the log to not have grown): %v", err)
	}
	rec3, _ := g.ActiveSession()
	if rec3.ID == rec2.ID {
		if _, present := g.SessionNames()[rec3.ID]; present {
			t.Fatalf("SessionNames()[%d] still present after an unnamed restart at the same id", rec3.ID)
		}
	}

	// RestoreSessionNames degrades a malformed/hand-edited entry rather
	// than blocking (mirrors RestoreConfigName's contract).
	g2 := New()
	g2.RestoreSessionNames(map[int]string{
		1: "  clean name  ",
		2: "\x00\x01", // normalizes to "" -> dropped
		3: "auth" + string([]byte{0x1b}) + "refactor",
	})
	names := g2.SessionNames()
	if names[1] != "clean name" {
		t.Fatalf("names[1] = %q, want %q", names[1], "clean name")
	}
	if _, ok := names[2]; ok {
		t.Fatal("an all-control-character entry survived RestoreSessionNames")
	}
	if names[3] != "authrefactor" {
		t.Fatalf("names[3] = %q, want %q", names[3], "authrefactor")
	}
}

// TestNextSessionIdIsAnchoredOnTheLastPersistedRow is B-3's game-side half
// (docs/plan/REVIEW-2026-08-22.md). StartSession used to derive its id
// from len(sessionLog)+1 alone, so the id sequence believed whatever the
// in-memory cache happened to hold. Two ways that diverges from the disk:
//
//   - the server could not convert the verified log into records and
//     restored an EMPTY cache while the DB still held rows (main.go's
//     degrade-field-by-field branch). The next id would then be 1 — a
//     collision with a row already on disk.
//   - a record was finished but its append has not landed yet, so the
//     cache is AHEAD of the disk. The next id must still not reuse it.
//
// The floor is therefore the higher of "records I know about" and "the
// last id the store confirmed it wrote", and the setter only raises.
func TestNextSessionIdIsAnchoredOnTheLastPersistedRow(t *testing.T) {
	t.Run("an empty cache with persisted rows does not restart at 1", func(t *testing.T) {
		g := New()
		g.RestoreSessionLog(nil) // the degraded conversion branch
		g.SetSessionLogPersistedID(5)
		if err := g.StartSession(""); err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		active, _ := g.ActiveSession()
		if active.ID != 6 {
			t.Errorf("id = %d, want 6 (one past the last row on disk)", active.ID)
		}
	})

	t.Run("a cache ahead of the disk wins", func(t *testing.T) {
		g := New()
		g.RestoreSessionLog([]SessionRecord{{ID: 1}, {ID: 2}, {ID: 3}})
		g.SetSessionLogPersistedID(2) // row 3's append has not landed
		if err := g.StartSession(""); err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		active, _ := g.ActiveSession()
		if active.ID != 4 {
			t.Errorf("id = %d, want 4 — an unpersisted record still owns its id", active.ID)
		}
	})

	t.Run("the floor only ever rises", func(t *testing.T) {
		g := New()
		g.SetSessionLogPersistedID(9)
		g.SetSessionLogPersistedID(2)
		if got := g.SessionLogPersistedID(); got != 9 {
			t.Errorf("SessionLogPersistedID = %d, want 9 — a lower value must not re-open the gap", got)
		}
	})

	t.Run("RestoreSessionLog seeds the floor from the restored rows", func(t *testing.T) {
		g := New()
		g.RestoreSessionLog([]SessionRecord{{ID: 1}, {ID: 2}})
		if got := g.SessionLogPersistedID(); got != 2 {
			t.Errorf("SessionLogPersistedID = %d, want 2", got)
		}
	})
}
