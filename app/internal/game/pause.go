// pause.go implements PR-5 — honest pause/resume (dev_docs/production-runtime/
// ARCHITECTURE.md §6 Decisions 13-16, MIGRATION_PLAN.md §PR-5) — on the
// game side: the persisted `paused` flag, the PausedSeconds bucket, and the
// one tick path a paused runtime takes.
//
// The idea the whole feature is built on (Decision 13): pause is the user
// withdrawing consent to be observed, so dexel stops OBSERVING — the
// server calls activity.Provider.Stop() and never calls
// engine.Engine.Tick() — rather than sampling and discarding. Nothing in
// this package can enforce that (game.Game is pure and knows about neither
// the provider nor the engine); what this package guarantees instead is
// the half that IS its own: while paused, no code path here accrues
// anything except PausedSeconds.
//
// Consequently there is no "gate" here in the STORE_OPEN sense. STORE_OPEN
// runs recordStats(r) unconditionally, because shopping is a few seconds
// INSIDE a tracked session and counting those seconds is honest
// (Decision 13's explicit contrast). Pause has no TickResult to record at
// all — Tick is simply never called — so pause cannot be, and is not,
// implemented as an early return inside Tick.
package game

import "github.com/jawwadzafar/dexel/app/internal/engine"

// Paused reports whether tracking is currently paused. The server reads
// this every tick to decide between eng.Tick()+g.Tick(r) and
// g.TickPaused(), and on startup to decide whether to start the provider
// at all.
func (g *Game) Paused() bool { return g.paused }

// SetPaused sets the pause state and reports whether it CHANGED — a
// repeated `dexel pause` is a no-op, not a second pause, so the caller
// (main.go's action loop) knows when the provider/engine side effects and
// the immediate save are actually owed.
//
// Pausing also parks the two fields that would otherwise keep asserting an
// observation nobody is making any more: Mood drops to the non-claiming
// engine.MoodIdle and ActiveApp/ActiveAppDisplay clear. Holding "coding in
// VS Code" frozen on the wire for the length of a pause — the way
// STORE_OPEN legitimately holds the last mood for a few seconds of
// shopping — would be exactly ADR 0010's forbidden claim, because with the
// provider stopped there is no longer anything backing it.
//
// Nothing else is touched: DevCash, XP, Progress, the sprint, every
// StatCounters field, the active session and its baseline all survive a
// pause untouched, which is what makes resume a continuation rather than a
// reset (only engine-LOCAL recency state is reset, by the server, via
// engine.Engine.Reset).
func (g *Game) SetPaused(paused bool) (changed bool) {
	if g.paused == paused {
		return false
	}
	g.paused = paused
	if paused {
		g.Mood = engine.MoodIdle
		g.ActiveApp = ""
		g.ActiveAppDisplay = ""
	}
	return true
}

// RestorePaused sets the pause state directly from a loaded save
// (internal/store's Apply) — the load-time counterpart of SetPaused,
// deliberately without SetPaused's mood-parking side effect, for the same
// reason RestoreSprint/RestoreStats are plain setters: Apply is
// reconstructing a state, not performing the user action that produced it.
// A save with no `paused` key (schema 6 and earlier) leaves this false,
// which is the correct "not paused" default.
func (g *Game) RestorePaused(paused bool) { g.paused = paused }

// TickPaused is the ONLY thing that happens on a 1s tick while paused. It
// is the paused half of Decision 14's partition: the tick did happen (the
// runtime was up for that second) but it was not an OBSERVED second, so it
// is credited to PausedSeconds in both the today and lifetime buckets and
// to nothing else.
//
// What deliberately does NOT happen here, and why:
//
//   - no ActiveSeconds/IdleSeconds — those are the two "observed" halves,
//     and this second was not observed;
//   - no Keystrokes/MouseActiveSeconds/FocusSessions/AppSwitches — there
//     is no engine.TickResult to fold in, because the engine was not
//     ticked and the provider was stopped;
//   - no Progress/DevCash/XP/sprint completion and no work accumulators;
//   - no checkSessionAutoEnd. An active session SURVIVES a pause
//     (docs/plan/P2-design.md §gates: "pause is 'stop watching me', not
//     'abandon my intention'"), and its idle auto-end deliberately cannot
//     fire while paused — it fires on the first REAL tick after resume,
//     backdated to the last activity actually seen, which is the same
//     self-healing behaviour a long process close already gets;
//   - no advanceSessionActivity — there is no input to advance from, so
//     the session's lastActivityAt/watermark stay frozen exactly where
//     the last observed input left them.
//
// The day rollover DOES still run: a pause spanning midnight must finalize
// the day that ended (so its paused band is recorded in history) and start
// crediting the new day's own PausedSeconds, exactly as a ticking runtime
// would.
//
// Calling this while not paused is a no-op rather than a silent
// mis-credit: PausedSeconds may only ever be advanced by a genuinely
// paused second.
func (g *Game) TickPaused() {
	if !g.paused {
		return
	}
	g.rolloverStatsIfNewDay()
	g.statsToday.PausedSeconds++
	g.statsLifetime.PausedSeconds++
}
