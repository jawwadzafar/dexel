// Package engine turns raw activity.Snapshot samples into work units and an
// honest mood, once per tick. It is deliberately pure/deterministic beyond
// the injectable clock — no file I/O, no direct OS access — so the
// calibration (ADR 0005) and honesty rules (ADR 0010) are unit-testable
// without a real Provider.
package engine

import (
	"time"

	"github.com/jawwadzafar/dev-companion/app/internal/activity"
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
}

// Engine samples a Provider once per Tick call (the caller drives the 1s
// cadence — see main.go) and turns the delta into work units + mood.
type Engine struct {
	provider activity.Provider
	now      func() time.Time

	initialized        bool
	lastKeystrokeCount uint64
	lastKeystrokeAt    time.Time
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

	// Keystroke delta since the previous tick. The first-ever tick has no
	// baseline, so it must contribute zero work — otherwise a provider that
	// starts with a nonzero counter (or a restart) would hand out free
	// work units it never earned.
	var keyDelta uint64
	if e.initialized && snap.KeystrokeCount > e.lastKeystrokeCount {
		keyDelta = snap.KeystrokeCount - e.lastKeystrokeCount
	}
	e.lastKeystrokeCount = snap.KeystrokeCount
	e.initialized = true

	if keyDelta > 0 {
		e.lastKeystrokeAt = now
	}

	weightedRate := float64(keyDelta) * KeystrokeWeight
	if snap.MouseActive {
		weightedRate += MouseSustainedRate * MouseWeight
	}
	if weightedRate > MaxRecentRate {
		weightedRate = MaxRecentRate
	}
	workUnits := weightedRate * WorkPerUnitRate

	honesty := e.provider.Honesty()

	return TickResult{
		Mood:             e.mood(snap, honesty, now),
		WorkUnits:        workUnits,
		Honesty:          honesty,
		ActiveApp:        snap.ActiveApp,
		ActiveAppDisplay: snap.ActiveAppDisplay,
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
