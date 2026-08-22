package game

import (
	"reflect"
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/engine"
)

// runUptime drives `seconds` one-second ticks against g, taking the paused
// path whenever g is paused and the ordinary engine path otherwise —
// exactly what main.go's select loop does (PR-5: "eng.Tick() is not called
// while paused"). It returns the number of seconds the runtime was "up",
// which for a 1Hz tick is simply the tick count: this helper IS the
// definition of uptime the invariant below is asserted against.
//
// pauseAt/resumeAt let a test flip the pause state mid-run at exact second
// boundaries, so the paused stretch's length is known precisely rather
// than inferred from a wall clock.
func runUptime(g *Game, clock *fakeClock, seconds int, pauseAt, resumeAt int) (uptime int) {
	for i := 0; i < seconds; i++ {
		if i == pauseAt {
			g.SetPaused(true)
		}
		if i == resumeAt {
			g.SetPaused(false)
		}
		if g.Paused() {
			g.TickPaused()
		} else {
			// A varied but deterministic observed tick: sometimes
			// coding, sometimes not, so ActiveSeconds and IdleSeconds
			// both grow and the partition is a real three-way split.
			g.Tick(tr(uint64(i%3), i%2 == 0, 0, 0))
		}
		clock.advance(time.Second)
		uptime++
	}
	return uptime
}

// TestActiveIdlePausedPartitionUptime is MIGRATION_PLAN.md §PR-5's first
// exit criterion, and ARCHITECTURE.md Decision 14's stated invariant:
//
//	activeSeconds + idleSeconds + pausedSeconds == seconds the runtime
//	was up during that bucket
//
// It holds for BOTH buckets, and it is what makes paused time honest: the
// second is accounted for, disjointly, in a bucket that says "dexel was
// not looking" — never absorbed into idle, which means "dexel was looking
// and saw nothing".
func TestActiveIdlePausedPartitionUptime(t *testing.T) {
	clock := newFakeClock()
	g := New()
	g.SetClockForTest(clock.now)

	// 40 observed seconds, then 25 paused, then 35 observed again.
	uptime := runUptime(g, clock, 100, 40, 65)

	for _, bucket := range []struct {
		name string
		c    StatCounters
	}{
		{"today", g.State().Stats.Today},
		{"lifetime", g.State().Stats.Lifetime},
	} {
		sum := bucket.c.ActiveSeconds + bucket.c.IdleSeconds + bucket.c.PausedSeconds
		if sum != uint64(uptime) {
			t.Errorf("%s: active(%d) + idle(%d) + paused(%d) = %d, want %d (the runtime's uptime in seconds)",
				bucket.name, bucket.c.ActiveSeconds, bucket.c.IdleSeconds, bucket.c.PausedSeconds, sum, uptime)
		}
		if bucket.c.PausedSeconds != 25 {
			t.Errorf("%s: PausedSeconds = %d, want exactly 25 — the paused stretch's real length", bucket.name, bucket.c.PausedSeconds)
		}
		// The disjointness half, stated separately: a paused second that
		// ALSO landed in idle would still satisfy no sum at all, but a
		// paused second silently REPLACED by an idle one would satisfy
		// the sum while lying. 75 observed seconds is the only honest
		// value for active+idle here.
		if got := bucket.c.ActiveSeconds + bucket.c.IdleSeconds; got != 75 {
			t.Errorf("%s: active+idle = %d, want 75 (the OBSERVED seconds only — paused seconds must not be counted as idle)", bucket.name, got)
		}
	}
}

// TestPausedSecondsAreNeverIdleSeconds isolates the single rule most
// likely to be "fixed" by a future refactor into a bug: a pause must not
// touch IdleSeconds at all. ADR 0010's honesty line is that idle means
// "observed doing nothing" — claiming a pause as idle would be dexel
// asserting an observation it deliberately stopped making.
func TestPausedSecondsAreNeverIdleSeconds(t *testing.T) {
	clock := newFakeClock()
	g := New()
	g.SetClockForTest(clock.now)

	g.SetPaused(true)
	for i := 0; i < 600; i++ { // ten paused minutes
		g.TickPaused()
		clock.advance(time.Second)
	}

	today := g.State().Stats.Today
	if today.PausedSeconds != 600 {
		t.Errorf("PausedSeconds = %d, want 600", today.PausedSeconds)
	}
	if today.IdleSeconds != 0 {
		t.Errorf("IdleSeconds = %d, want 0 — ten paused minutes must never be reported as ten idle minutes", today.IdleSeconds)
	}
	if today.ActiveSeconds != 0 {
		t.Errorf("ActiveSeconds = %d, want 0", today.ActiveSeconds)
	}
}

// TestPauseBlocksEveryAccrual is §PR-5's second exit criterion in full:
// "across a pause, devCash, xp, sprint.unitsDone, keystrokes,
// mouseActiveSeconds, focusSessions, appSwitches are ALL unchanged, and
// idleSeconds did NOT absorb the paused seconds."
//
// The pause is long (an hour) and the ticks it replaces would have been
// generous ones (typing + mouse + a focus bonus + an app switch every
// tick, i.e. the richest tick the economy can produce), so if ANY accrual
// leaked through a pause this test would see it immediately rather than
// as a rounding artefact.
func TestPauseBlocksEveryAccrual(t *testing.T) {
	clock := newFakeClock()
	g := New()
	g.SetClockForTest(clock.now)

	// Earn a real, non-trivial amount first, so "unchanged" is a
	// meaningful claim about live numbers rather than about zeroes.
	for i := 0; i < 300; i++ {
		g.Tick(tr(10, true, 1, 1))
		clock.advance(time.Second)
	}
	before := struct {
		devCash    uint64
		xp         uint64
		progress   float64
		sprintIdx  int
		counters   StatCounters
		coinsToday CoinBreakdown
	}{g.DevCash, g.XP, g.Progress, g.sprintIndex, g.State().Stats.Today, g.CoinsTodaySnapshot()}
	if before.devCash == 0 || before.counters.Keystrokes == 0 || before.counters.SprintsCompleted == 0 {
		t.Fatalf("the pre-pause run earned nothing meaningful (%+v) — this test would prove nothing", before)
	}

	g.SetPaused(true)
	for i := 0; i < 3600; i++ { // one paused hour
		g.TickPaused()
		clock.advance(time.Second)
	}

	after := g.State().Stats.Today
	if g.DevCash != before.devCash {
		t.Errorf("DevCash = %d across a pause, want %d unchanged", g.DevCash, before.devCash)
	}
	if g.XP != before.xp {
		t.Errorf("XP = %d across a pause, want %d unchanged", g.XP, before.xp)
	}
	if g.Progress != before.progress {
		t.Errorf("Progress (sprint.unitsDone) = %v across a pause, want %v unchanged", g.Progress, before.progress)
	}
	if g.sprintIndex != before.sprintIdx {
		t.Errorf("sprintIndex = %d across a pause, want %d unchanged", g.sprintIndex, before.sprintIdx)
	}
	if g.CoinsTodaySnapshot() != before.coinsToday {
		t.Errorf("coinsToday = %+v across a pause, want %+v unchanged", g.CoinsTodaySnapshot(), before.coinsToday)
	}
	for _, f := range []struct {
		name      string
		got, want uint64
	}{
		{"Keystrokes", after.Keystrokes, before.counters.Keystrokes},
		{"MouseActiveSeconds", after.MouseActiveSeconds, before.counters.MouseActiveSeconds},
		{"ActiveSeconds", after.ActiveSeconds, before.counters.ActiveSeconds},
		{"IdleSeconds", after.IdleSeconds, before.counters.IdleSeconds},
		{"SprintsCompleted", after.SprintsCompleted, before.counters.SprintsCompleted},
		{"FocusSessions", after.FocusSessions, before.counters.FocusSessions},
		{"AppSwitches", after.AppSwitches, before.counters.AppSwitches},
	} {
		if f.got != f.want {
			t.Errorf("%s = %d across a pause, want %d unchanged", f.name, f.got, f.want)
		}
	}
	if after.PausedSeconds != 3600 {
		t.Errorf("PausedSeconds = %d, want 3600 — the one counter a pause MAY advance", after.PausedSeconds)
	}
}

// TestTickPausedIsANoOpWhenNotPaused: PausedSeconds may only ever be
// advanced by a genuinely paused second. A stray TickPaused on a running
// runtime (a future refactor calling both branches, say) must credit
// nothing rather than quietly inventing paused time.
func TestTickPausedIsANoOpWhenNotPaused(t *testing.T) {
	g := New()
	for i := 0; i < 10; i++ {
		g.TickPaused()
	}
	if got := g.State().Stats.Today.PausedSeconds; got != 0 {
		t.Errorf("PausedSeconds = %d after TickPaused on an unpaused game, want 0", got)
	}
}

// TestPausedWireStateIsIdlePlusTheBoolean is ARCHITECTURE.md Decision 15:
// no fourth mood. While paused the wire says activeState "idle" — the
// engine's own non-claiming fallback — and `paused: true` carries the
// actual meaning. "onBreak" would be the ADR 0010 lie ("on break because
// you paused me") and "coding" is obviously false.
func TestPausedWireStateIsIdlePlusTheBoolean(t *testing.T) {
	g := New()
	// Establish a live "coding in VS Code" state first, so the test proves
	// the pre-pause claim is DROPPED rather than that it was never there.
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, KeystrokeDelta: 5, ActiveApp: "code", ActiveAppDisplay: "VS Code"})
	if s := g.State(); s.ActiveState != "coding" || s.Paused {
		t.Fatalf("pre-pause state = {activeState:%q paused:%v}, want {coding false}", s.ActiveState, s.Paused)
	}

	g.SetPaused(true)
	s := g.State()
	if !s.Paused {
		t.Error("paused = false on the wire while paused — this bool is the authoritative signal")
	}
	if s.ActiveState != "idle" {
		t.Errorf("activeState = %q while paused, want %q (docs/ui-spec.md §6.1's set stays exactly coding|idle|onBreak)", s.ActiveState, "idle")
	}
	if s.ActivityLine == "Coding in VS Code" {
		t.Errorf("activityLine = %q while paused — dexel must not keep claiming an observation it stopped making", s.ActivityLine)
	}

	g.SetPaused(false)
	if g.State().Paused {
		t.Error("paused = true after resume")
	}
}

// TestSetPausedReportsOnlyRealChanges: idempotence, which is what makes a
// repeated `dexel pause` a genuine no-op all the way down (no second
// provider stop, no second immediate save, no redundant broadcast — see
// main.go's action loop, which gates all three on this return value).
func TestSetPausedReportsOnlyRealChanges(t *testing.T) {
	g := New()
	if !g.SetPaused(true) {
		t.Error("SetPaused(true) on a running game reported no change")
	}
	if g.SetPaused(true) {
		t.Error("SetPaused(true) on an already-paused game reported a change")
	}
	if !g.SetPaused(false) {
		t.Error("SetPaused(false) on a paused game reported no change")
	}
	if g.SetPaused(false) {
		t.Error("SetPaused(false) on a running game reported a change")
	}
}

// TestPauseSpanningMidnightFinalizesTheDay: the day rollover is the one
// piece of bookkeeping that DOES still run while paused, because the
// alternative is worse than either option it replaces — a pause across
// midnight would otherwise credit yesterday's bucket with today's paused
// seconds forever, and yesterday's row would never finalize into history.
func TestPauseSpanningMidnightFinalizesTheDay(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 3, 10, 23, 59, 50, 0, time.Local)}
	g := New()
	g.SetClockForTest(clock.now)

	g.SetPaused(true)
	for i := 0; i < 20; i++ { // 10s before midnight, 10s after
		g.TickPaused()
		clock.advance(time.Second)
	}

	today := g.State().Stats.Today
	if today.PausedSeconds != 10 {
		t.Errorf("today.PausedSeconds = %d, want 10 (only the seconds after midnight)", today.PausedSeconds)
	}
	if got := g.State().Stats.Lifetime.PausedSeconds; got != 20 {
		t.Errorf("lifetime.PausedSeconds = %d, want 20 (lifetime never rolls over)", got)
	}
	hist := g.HistorySnapshot()
	if len(hist) != 1 || hist[0].Date != "2026-03-10" {
		t.Fatalf("history = %+v, want exactly one finalized bucket for 2026-03-10", hist)
	}
	if hist[0].Counters.PausedSeconds != 10 {
		t.Errorf("the finalized day's PausedSeconds = %d, want 10 — a day's row must show its paused band", hist[0].Counters.PausedSeconds)
	}
	// And the same number reaches the dense wire history, which is what
	// the UI would draw the band from.
	wire := g.State().Stats.History
	var found bool
	for _, d := range wire {
		if d.Date == "2026-03-10" {
			found = true
			if d.PausedSeconds != 10 {
				t.Errorf("wire history for 2026-03-10: pausedSeconds = %d, want 10", d.PausedSeconds)
			}
		}
	}
	if !found {
		t.Error("2026-03-10 is missing from the dense wire history")
	}
}

// TestEveryStatCountersFieldReachesTheWireAndTheDelta is the maintenance
// guard PR-5 needed and did not have.
//
// docs/plan/P2-design.md §2.3 claims "a new counter added to StatCounters
// in a future phase joins the session automatically, with no per-field
// maintenance". That is true of the DESIGN but NOT of the implementation:
// subtractCounters (session.go), dayStatFromCounters (history.go) and the
// two session wire views all enumerate the fields by hand, so PR-5 had to
// add PausedSeconds to each of them, and a future counter will too. This
// test turns "forgot one" from a silent zero into a failure: it fills
// EVERY StatCounters field with a distinct non-zero value and asserts each
// one survives the delta and reaches both wire shapes.
func TestEveryStatCountersFieldReachesTheWireAndTheDelta(t *testing.T) {
	typ := reflect.TypeOf(StatCounters{})

	// A fully-populated watermark, and a baseline of half each value, so
	// every delta is non-zero AND differs from the watermark (a mapping
	// that accidentally passes the watermark through instead of the delta
	// fails too).
	var watermark, baseline StatCounters
	wv, bv := reflect.ValueOf(&watermark).Elem(), reflect.ValueOf(&baseline).Elem()
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Type.Kind() != reflect.Uint64 {
			t.Fatalf("StatCounters.%s is %s, not uint64 — this test assumes every counter is a uint64", typ.Field(i).Name, typ.Field(i).Type)
		}
		wv.Field(i).SetUint(uint64(100 + 2*i))
		bv.Field(i).SetUint(uint64(50 + i))
	}

	delta := subtractCounters(watermark, baseline)
	dv := reflect.ValueOf(delta)
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		want := wv.Field(i).Uint() - bv.Field(i).Uint()
		if got := dv.Field(i).Uint(); got != want {
			t.Errorf("subtractCounters dropped or mangled %s: got %d, want %d — add it to subtractCounters (session.go)", name, got, want)
		}
	}

	// Each per-field mapping into the two shapes that enumerate by hand.
	day := dayStatFromCounters("2026-03-10", delta, 7, 9)
	view := sessionViewFromRecord(SessionRecord{Counters: delta}, "")
	dayV, viewV := reflect.ValueOf(day), reflect.ValueOf(view)
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		want := dv.Field(i).Uint()
		if f := dayV.FieldByName(name); !f.IsValid() {
			t.Errorf("DayStat has no %s field — every StatCounters counter must reach the dense wire history (history.go)", name)
		} else if f.Uint() != want {
			t.Errorf("dayStatFromCounters dropped %s: got %d, want %d (history.go)", name, f.Uint(), want)
		}
		if f := viewV.FieldByName(name); !f.IsValid() {
			t.Errorf("SessionView has no %s field — every StatCounters counter must reach the session wire (session.go)", name)
		} else if f.Uint() != want {
			t.Errorf("sessionViewFromRecord dropped %s: got %d, want %d (session.go)", name, f.Uint(), want)
		}
	}
}

// TestSessionSurvivesPauseAndCountsPausedSeconds is P2's pause row
// (docs/plan/P2-design.md §gates: "the session SURVIVES; counters freeze
// because the provider is stopped and eng.Tick() is not called;
// pausedSeconds joins the delta set") — the whole interaction, in one
// place:
//
//   - the session is still active after the pause, with the same id;
//   - every OBSERVED counter is frozen at its pre-pause value;
//   - pausedSeconds appears in the session's own delta, so the gap between
//     the session's wall-clock elapsedSeconds and its observed seconds is
//     EXPLAINED rather than looking like unaccounted idle;
//   - resuming keeps the same session and starts accruing into it again.
//
// "Pause is 'stop watching me', not 'abandon my intention'. Auto-ending on
// pause would make a user's declared container a casualty of a privacy
// action."
func TestSessionSurvivesPauseAndCountsPausedSeconds(t *testing.T) {
	clock := newFakeClock()
	g := New()
	g.SetClockForTest(clock.now)

	if err := g.StartSession("pause interplay"); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	for i := 0; i < 120; i++ {
		g.Tick(tr(4, true, 0, 0))
		clock.advance(time.Second)
	}
	beforeActive := g.State().Sessions.Active
	if beforeActive == nil {
		t.Fatal("no active session before the pause")
	}
	if beforeActive.Keystrokes == 0 {
		t.Fatal("the pre-pause run recorded no keystrokes into the session — this test would prove nothing")
	}

	g.SetPaused(true)
	for i := 0; i < 900; i++ { // fifteen paused minutes
		g.TickPaused()
		clock.advance(time.Second)
	}

	during := g.State().Sessions.Active
	if during == nil {
		t.Fatal("the active session vanished across a pause — pause must never end a session")
	}
	if during.ID != beforeActive.ID {
		t.Errorf("session id changed across a pause: %d -> %d", beforeActive.ID, during.ID)
	}
	if during.Name != "pause interplay" {
		t.Errorf("session name = %q across a pause, want it unchanged", during.Name)
	}
	for _, f := range []struct {
		name      string
		got, want uint64
	}{
		{"keystrokes", during.Keystrokes, beforeActive.Keystrokes},
		{"mouseActiveSeconds", during.MouseActiveSeconds, beforeActive.MouseActiveSeconds},
		{"activeSeconds", during.ActiveSeconds, beforeActive.ActiveSeconds},
		{"idleSeconds", during.IdleSeconds, beforeActive.IdleSeconds},
		{"focusSessions", during.FocusSessions, beforeActive.FocusSessions},
		{"appSwitches", during.AppSwitches, beforeActive.AppSwitches},
		{"sprintsCompleted", during.SprintsCompleted, beforeActive.SprintsCompleted},
		{"coinsEarned", during.CoinsEarned, beforeActive.CoinsEarned},
	} {
		if f.got != f.want {
			t.Errorf("session %s = %d across a pause, want %d frozen", f.name, f.got, f.want)
		}
	}
	if during.PausedSeconds != 900 {
		t.Errorf("session pausedSeconds = %d, want 900 — the paused stretch must be visible IN the session, not just globally", during.PausedSeconds)
	}
	// The whole point of carrying the field: the session's wall-clock
	// length is fully explained by observed + paused seconds, with nothing
	// unaccounted for.
	if got := during.ActiveSeconds + during.IdleSeconds + during.PausedSeconds; got != during.ElapsedSeconds {
		t.Errorf("session active(%d)+idle(%d)+paused(%d) = %d, want elapsedSeconds %d — a session's own time must partition exactly like a day's does",
			during.ActiveSeconds, during.IdleSeconds, during.PausedSeconds, got, during.ElapsedSeconds)
	}

	g.SetPaused(false)
	for i := 0; i < 30; i++ {
		g.Tick(tr(4, true, 0, 0))
		clock.advance(time.Second)
	}
	after := g.State().Sessions.Active
	if after == nil || after.ID != beforeActive.ID {
		t.Fatalf("session after resume = %+v, want the same session still running", after)
	}
	if after.Keystrokes <= during.Keystrokes {
		t.Errorf("session keystrokes = %d after resume, want > %d — accrual must resume", after.Keystrokes, during.Keystrokes)
	}
	if after.PausedSeconds != 900 {
		t.Errorf("session pausedSeconds = %d after resume, want it to stay 900", after.PausedSeconds)
	}
}

// TestIdleAutoEndCannotFireWhilePausedAndFiresOnTheFirstTickAfter is the
// other half of P2's pause row: "The idle auto-end cannot fire while
// paused (no ticks) — it fires on the FIRST tick after resume, backdated,
// which is the self-healing behaviour we want."
//
// The pause here is far longer than SessionIdleTimeoutSeconds, so a rule
// that ran on the paused path would have ended the session mid-pause.
func TestIdleAutoEndCannotFireWhilePausedAndFiresOnTheFirstTickAfter(t *testing.T) {
	clock := newFakeClock()
	g := New()
	g.SetClockForTest(clock.now)

	if err := g.StartSession("long pause"); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	// Real input, so lastActivityAt is a known, non-zero moment.
	for i := 0; i < 120; i++ {
		g.Tick(tr(3, false, 0, 0))
		clock.advance(time.Second)
	}
	lastActivity := clock.t.Add(-time.Second)

	g.SetPaused(true)
	for i := 0; i < 3*SessionIdleTimeoutSeconds; i++ {
		g.TickPaused()
		clock.advance(time.Second)
		if g.State().Sessions.Active == nil {
			t.Fatalf("the session auto-ended DURING a pause (after %ds) — no auto-end rule may run while paused", i+1)
		}
	}

	// Resume: the very first real tick notices the staleness and ends the
	// session, backdated to the last activity actually observed — not to
	// "now", which would blame the pause on the user.
	g.SetPaused(false)
	g.Tick(tr(0, false, 0, 0))
	if g.State().Sessions.Active != nil {
		t.Fatal("the session did not auto-end on the first tick after resume")
	}
	rec, ok := g.TakeEndedSession()
	if !ok {
		t.Fatal("no ended session record was queued")
	}
	if rec.EndReason != endReasonIdle {
		t.Errorf("endReason = %q, want %q", rec.EndReason, endReasonIdle)
	}
	if !rec.EndedAt.Equal(lastActivity) {
		t.Errorf("endedAt = %v, want it backdated to the last observed activity %v", rec.EndedAt, lastActivity)
	}
	// Backdating is what keeps the record internally consistent: a
	// duration that stopped at the last real input cannot contain the
	// paused hours that followed it.
	if rec.Counters.PausedSeconds != 0 {
		t.Errorf("the backdated record's pausedSeconds = %d, want 0 — its watermark was frozen before the pause began", rec.Counters.PausedSeconds)
	}
	if rec.DurationSeconds > uint64(120) {
		t.Errorf("durationSeconds = %d, want <= 120 — the record must not absorb the pause", rec.DurationSeconds)
	}
}

// TestActiveSessionViewCarriesEveryCounter closes the one gap
// TestEveryStatCountersFieldReachesTheWireAndTheDelta cannot reach by
// construction: ActiveSessionView is built by sessionsView from a LIVE
// game, not from a SessionRecord, so its per-field mapping needs a live
// session to be exercised. Same maintenance guard, same reason.
func TestActiveSessionViewCarriesEveryCounter(t *testing.T) {
	clock := newFakeClock()
	g := New()
	g.SetClockForTest(clock.now)
	if err := g.StartSession(""); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	// Enough varied ticks to make every observed counter non-zero, plus a
	// paused stretch for PausedSeconds.
	for i := 0; i < 400; i++ {
		g.Tick(sessionTestTicks(400)[i])
		clock.advance(time.Second)
	}
	g.SetPaused(true)
	for i := 0; i < 5; i++ {
		g.TickPaused()
		clock.advance(time.Second)
	}

	active := g.State().Sessions.Active
	if active == nil {
		t.Fatal("no active session")
	}
	av := reflect.ValueOf(*active)
	typ := reflect.TypeOf(StatCounters{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		f := av.FieldByName(name)
		if !f.IsValid() {
			t.Errorf("ActiveSessionView has no %s field — every StatCounters counter must reach `sessions.active` (session.go)", name)
			continue
		}
		if f.Uint() == 0 {
			t.Errorf("ActiveSessionView.%s = 0 after a run that exercised every counter — it is probably unmapped in sessionsView (session.go)", name)
		}
	}
}
