// Package engine turns raw activity.Snapshot samples into work units and an
// honest mood, once per tick. It is deliberately pure/deterministic beyond
// the injectable clock — no file I/O, no direct OS access — so the
// calibration (ADR 0005) and honesty rules (ADR 0010) are unit-testable
// without a real Provider.
package engine

import (
	"time"

	"github.com/jawwadzafar/dexel/app/internal/activity"
)

// Economy calibration, ported EXACTLY from ADR 0005's rebalance (the
// "bar goes burr on simple mouse move" fix) — do not retune these without
// rereading that ADR; they are the anti-mash economy, not incidental
// numbers:
//
//   - KeystrokeWeight / MouseWeight: event_weight (keystroke 1.0, mouse
//     0.25, matching the Rust activity crate).
//   - MaxRecentRate: the post-rebalance ceiling (120 -> 15) "a human reaches
//     by working," not by mashing.
//   - WorkPerUnitRate: the post-rebalance MIN_WORK_PER_EVENT (0.05 -> 0.008).
//
// work/s = min(weighted_rate, MaxRecentRate) * WorkPerUnitRate, which is
// the ADR's decay-ceiling semantics simplified per the task brief while
// preserving the outcome: real typing ~21 min per 50-work sprint,
// mouse-only strictly slower (see engine_test.go's strategy comparison).
const (
	KeystrokeWeight = 1.0
	MouseWeight     = 0.25

	MaxRecentRate   = 15.0
	WorkPerUnitRate = 0.008
)

// A2 economy constants (docs/plan/A2-design.md §4, ADR 0012), kept in this
// SAME const block as ADR 0005's so all pricing is single-sourced:
//
//   - FocusSessionSeconds / FocusGapToleranceSeconds: a sustained-typing
//     run (keyDelta>0 each tick, gaps no longer than the tolerance) that
//     reaches FocusSessionSeconds completes a "focus session."
//   - FocusSessionBonusWork: the fixed work bonus folded into WorkUnits
//     when a focus session completes. Mouse never sets keyDelta, so mouse
//     can never trigger this — the mouse<typing invariant holds by
//     construction (ADR 0005), not by a runtime check.
//   - AppSwitchWork: Fork B's coin weight per counted app-switch. Default
//     0.0 (Fork B1 — display-only, earns nothing, economy stays identical
//     cross-platform since Linux never reports ActiveApp). Set to 0.1 to
//     flip on Fork B2 (macOS-first capped earning) — a one-constant flip.
//   - AppSwitchDailyCap: Fork B2's daily cap on counted switches.
const (
	FocusSessionSeconds      = 120.0
	FocusGapToleranceSeconds = 3.0
	FocusSessionBonusWork    = 2.0

	AppSwitchWork     = 0.0
	AppSwitchDailyCap = 40
)

// AntiMashSampleInterval documents the coalescing window the activity
// providers enforce (ADR 0005's MOUSE_SAMPLE_SECS, applied uniformly to
// both keystroke and mouse signals per ADR 0011's port). The engine doesn't
// enforce this itself — the providers already coalesced their counters
// before Snapshot() — but MouseSustainedRate below is derived from it (not
// hand-copied) so the two packages can't silently drift apart. It cannot
// live in the const block above because time.Duration.Seconds() is not a
// constant expression.
//
// This is a re-export of activity.MouseSampleInterval, not an
// independently chosen value — engine.go, provider_linux.go, and
// provider_darwin.go each used to declare their own "100ms" constant with
// no static link between them, so retuning ADR 0005's anti-mash window
// meant remembering to edit three files in three packages and trusting
// nobody missed one. activity.MouseSampleInterval (provider.go) is now the
// single source of truth; this alias exists only so existing callers of
// engine.AntiMashSampleInterval don't have to change.
const AntiMashSampleInterval = activity.MouseSampleInterval

// MouseSustainedRate is the equivalent events/sec a continuously mouse-
// active signal contributes to weighted_rate. It equals the anti-mash
// coalescing cap shared with the activity providers (one flagged
// mouse-active signal per AntiMashSampleInterval) — the highest rate a
// provider could ever honestly report for "mouse active," so mouse-only
// play is bounded at this ceiling even under a mashing script, never
// higher.
var MouseSustainedRate = 1.0 / AntiMashSampleInterval.Seconds()

// Honest mood thresholds (ADR 0010).
const (
	// CodingRecencyWindow: a keystroke within this window means Coding.
	// Mouse motion alone never satisfies it — "scrolling docs is not
	// typing."
	CodingRecencyWindow = 10 * time.Second
	// OnBreakIdleThreshold: genuine GLOBAL idleness this long means
	// OnBreak/AFK (ADR 0010 decision 2, v0.4 plan non-negotiable #6).
	OnBreakIdleThreshold = 30 * time.Second
)

// Mood mirrors the Rust companion state machine's honest moods (ADR 0010).
// Values are the exact wire strings docs/ui-spec.md §6.1 mandates
// ("activeState — coding | idle | onBreak. Exactly these three strings,
// lowerCamel") — the engine emits the wire format directly so no layer in
// between has to remember to translate.
type Mood string

const (
	MoodCoding  Mood = "coding"
	MoodIdle    Mood = "idle"
	MoodOnBreak Mood = "onBreak"
)

// TickResult is what one engine tick hands to the game layer.
type TickResult struct {
	Mood      Mood
	WorkUnits float64
	Honesty   activity.Honesty

	ActiveApp        string
	ActiveAppDisplay string

	// KeystrokeDelta/MouseActive are the same raw, already-honest signals
	// WorkUnits was computed from (this tick's counted keystrokes and
	// whether mouse activity was flagged), re-exposed for the game layer's
	// Analytics/Phase A1 daily+lifetime counters (docs/plan/ROADMAP.md
	// "Analytics track"). They carry no more information than WorkUnits
	// already implies — content-free counts/bools, nothing new crossing the
	// engine/game boundary.
	KeystrokeDelta uint64
	MouseActive    bool

	// FocusSessionsCompleted / AppSwitches (A2, docs/plan/A2-design.md §5):
	// 0/1 this tick. FocusSessionsCompleted counts a sustained-typing run
	// (§4) crossing FocusSessionSeconds; AppSwitches counts a change in
	// ActiveApp since the previous tick (0 on Linux, which never sets
	// ActiveApp — ADR 0009). Both are content-free counts already implied
	// by data on the boundary (keystroke timing, sanitized app identity);
	// no new Snapshot field or provider observation backs them.
	FocusSessionsCompleted uint64
	AppSwitches            uint64

	// FocusRunSeconds (A3 Fork B, docs/plan/A3-design.md §7 pinned contract):
	// the length, in whole seconds, of the CURRENT sustained-typing run as of
	// this tick — 0 when no run is active (including the tick a run just
	// broke). Derived from the same focusRunActive/focusRunStart tracker A2
	// already maintains for FocusSessionsCompleted; no new state, no new
	// provider observation, no economy effect. Purely observational, for the
	// game layer to track a per-day max ("longest focus block").
	FocusRunSeconds uint64
}

// SeesGlobalInput reports whether this tick's activity data comes from a
// provider with genuine global visibility (docs/plan/P2-design.md §GO-0 /
// §2.5's pinned contract: "TickResult.SeesGlobalInput() bool, returning
// r.Honesty == activity.HonestyGlobal"). It is a METHOD, not a new field —
// Honesty already crosses the engine/game boundary on TickResult, so this
// adds no data, only a name for the one honesty question P2's idle
// auto-end rule depends on: the rule may fire only when the provider
// genuinely SEES idle. A blind provider (HonestyBlind) must never be read
// as "saw no input" — that is the ADR 0010 lie the mood rules already
// refuse (a blind provider can never produce MoodOnBreak either, see
// mood() below), and the idle auto-end must refuse it identically.
func (r TickResult) SeesGlobalInput() bool {
	return r.Honesty == activity.HonestyGlobal
}

// Engine samples a Provider once per Tick call (the caller drives the 1s
// cadence — see main.go) and turns the delta into work units + mood.
type Engine struct {
	provider activity.Provider
	now      func() time.Time

	initialized        bool
	lastKeystrokeCount uint64
	lastKeystrokeAt    time.Time

	// lastActiveApp is the previous tick's ActiveApp, diffed to derive
	// AppSwitches (A2 §5) — no new observation, ActiveApp is already on
	// Snapshot.
	lastActiveApp string

	// focusRunActive/focusRunStart track the current sustained-typing run
	// (A2 §4). A run starts on a keyDelta>0 tick and is extended by
	// subsequent keyDelta>0 ticks as long as the gap since the last
	// counted keystroke never exceeds FocusGapToleranceSeconds; mouse
	// activity never touches these fields, so mouse-only play can never
	// start, extend, or complete a run — the mouse<typing invariant holds
	// by construction, not by a runtime check.
	focusRunActive bool
	focusRunStart  time.Time
}

// New wires a provider. Call provider.Start() separately — the engine only
// samples, it never manages the provider's lifecycle.
func New(p activity.Provider) *Engine {
	return &Engine{provider: p, now: time.Now}
}

// Tick performs one sampling pass.
func (e *Engine) Tick() TickResult {
	snap := e.provider.Snapshot()
	now := e.now()

	// wasInitialized captures whether a prior tick has already established
	// baselines, before this tick overwrites them below — both the
	// keystroke delta and the app-switch diff must contribute nothing on
	// the first-ever tick (no baseline yet to diff against).
	wasInitialized := e.initialized

	// Keystroke delta since the previous tick. The first-ever tick has no
	// baseline, so it must contribute zero work — otherwise a provider that
	// starts with a nonzero counter (or a restart) would hand out free
	// work units it never earned.
	var keyDelta uint64
	if wasInitialized && snap.KeystrokeCount > e.lastKeystrokeCount {
		keyDelta = snap.KeystrokeCount - e.lastKeystrokeCount
	}
	e.lastKeystrokeCount = snap.KeystrokeCount

	// App-switch: diff this tick's (already sanitized, ADR 0009) ActiveApp
	// against the previous tick's. No new observation — ActiveApp is
	// already on Snapshot. Reads 0 on Linux, which never sets ActiveApp.
	//
	// dexel's OWN window is TRANSPARENT here (activity.SelfAppID): looking
	// at your companion is not a context switch in your work, and counting
	// it made the Activity modal's app-switch number a measure of how often
	// you glanced at dexel. Neither counted nor recorded, so
	// editor -> dexel -> editor is one continuous stretch in the editor
	// rather than two switches. (No economy effect either way at the
	// default AppSwitchWork = 0.0, but a counter shown to the user is a
	// claim, and this one was about dexel rather than about work.)
	var appSwitches uint64
	if !activity.IsSelf(snap.ActiveApp) {
		if wasInitialized && snap.ActiveApp != e.lastActiveApp {
			appSwitches = 1
		}
		e.lastActiveApp = snap.ActiveApp
	}

	e.initialized = true

	prevKeystrokeAt := e.lastKeystrokeAt
	if keyDelta > 0 {
		e.lastKeystrokeAt = now
	}

	// Focus-session run tracker (A2 §4): a sustained-typing run extends on
	// any tick with keyDelta>0; a gap longer than
	// FocusGapToleranceSeconds since the last counted keystroke breaks it.
	// Mouse never sets keyDelta, so mouse-only play can never start,
	// extend, or complete a run.
	var focusSessionsCompleted uint64
	if keyDelta > 0 {
		gapExceeded := !prevKeystrokeAt.IsZero() && now.Sub(prevKeystrokeAt).Seconds() > FocusGapToleranceSeconds
		if !e.focusRunActive || gapExceeded {
			e.focusRunActive = true
			e.focusRunStart = now
		}
		if now.Sub(e.focusRunStart).Seconds() >= FocusSessionSeconds {
			focusSessionsCompleted = 1
			e.focusRunStart = now
		}
	} else if e.focusRunActive && !prevKeystrokeAt.IsZero() && now.Sub(prevKeystrokeAt).Seconds() > FocusGapToleranceSeconds {
		e.focusRunActive = false
	}

	weightedRate := float64(keyDelta) * KeystrokeWeight
	if snap.MouseActive {
		weightedRate += MouseSustainedRate * MouseWeight
	}
	if weightedRate > MaxRecentRate {
		weightedRate = MaxRecentRate
	}
	workUnits := weightedRate * WorkPerUnitRate

	// Fold the A2 contributions into WorkUnits (§4): the focus bonus
	// (unconditional — it is the shipped earning signal) and Fork-B's
	// app-switch work, which is a no-op at the default AppSwitchWork=0.0.
	workUnits += FocusSessionBonusWork * float64(focusSessionsCompleted)
	workUnits += AppSwitchWork * float64(appSwitches)

	honesty := e.provider.Honesty()

	// FocusRunSeconds (A3 Fork B): the current sustained-typing run's length
	// as of this tick, read from the tracker's final state above (so it
	// already reflects this tick's start/extend/break/complete-reset). 0
	// whenever no run is active, including the tick a run just broke.
	var focusRunSeconds uint64
	if e.focusRunActive {
		focusRunSeconds = uint64(now.Sub(e.focusRunStart).Seconds())
	}

	return TickResult{
		Mood:                   e.mood(snap, honesty, now),
		WorkUnits:              workUnits,
		Honesty:                honesty,
		ActiveApp:              snap.ActiveApp,
		ActiveAppDisplay:       snap.ActiveAppDisplay,
		KeystrokeDelta:         keyDelta,
		MouseActive:            snap.MouseActive,
		FocusSessionsCompleted: focusSessionsCompleted,
		AppSwitches:            appSwitches,
		FocusRunSeconds:        focusRunSeconds,
	}
}

// mood implements ADR 0010's honesty rules:
//   - Coding requires a keystroke within CodingRecencyWindow. Mouse motion
//     alone never produces Coding.
//   - OnBreak requires genuine GLOBAL idleness beyond OnBreakIdleThreshold
//     AND a provider that can actually see global input — a blind provider
//     (Honesty() == HonestyBlind) can never produce OnBreak, because it
//     cannot know. "On break because you minimized me" is the lie this
//     guards against.
//   - Otherwise Idle.
func (e *Engine) mood(snap activity.Snapshot, honesty activity.Honesty, now time.Time) Mood {
	if !e.lastKeystrokeAt.IsZero() && now.Sub(e.lastKeystrokeAt) <= CodingRecencyWindow {
		return MoodCoding
	}
	if honesty == activity.HonestyGlobal && snap.IdleSeconds > OnBreakIdleThreshold.Seconds() {
		return MoodOnBreak
	}
	return MoodIdle
}

// Reset clears every piece of engine-local state that carries meaning
// ACROSS ticks, so the next Tick behaves exactly like the first tick of a
// freshly-constructed Engine. It is the resume seam
// (dev_docs/production-runtime/ARCHITECTURE.md Decision 16, MIGRATION_PLAN.md
// §PR-5): called when a paused runtime resumes, BEFORE the provider is
// started again.
//
// Without it, resume would inherit:
//
//   - lastKeystrokeCount — a stale baseline against a provider counter that
//     may have been reset to 0 (FakeProvider) or kept climbing invisibly
//     while stopped, so the first tick after resume could hand out (or
//     refuse) work it never earned;
//   - lastKeystrokeAt — and briefly claim MoodCoding for typing that
//     happened BEFORE the pause (the ADR 0010 lie: a keystroke observed
//     ten hours ago is not "coding right now");
//   - focusRunActive/focusRunStart — and pay a FocusSessionBonusWork bonus
//     for a "sustained" typing run with a ten-hour hole in the middle;
//   - lastActiveApp — and count one fabricated AppSwitch on the first tick
//     back.
//
// Clearing `initialized` is what re-arms Tick's own first-tick guard
// (`wasInitialized`), which is what makes the tick after resume contribute
// exactly zero work no matter what counter the restarted provider reports.
// Deliberately does NOT touch the provider: the engine "only samples, it
// never manages the provider's lifecycle" (see New) — the caller stops and
// starts it.
func (e *Engine) Reset() {
	e.initialized = false
	e.lastKeystrokeCount = 0
	e.lastKeystrokeAt = time.Time{}
	e.lastActiveApp = ""
	e.focusRunActive = false
	e.focusRunStart = time.Time{}
}
