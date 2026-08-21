package activity

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// FakeKind names one segment of a scripted fake activity timeline.
type FakeKind string

const (
	FakeType  FakeKind = "type"  // simulated steady typing
	FakeMouse FakeKind = "mouse" // simulated mouse-only activity
	FakeIdle  FakeKind = "idle"  // simulated genuine idleness
)

// FakeStep is one segment of a script: "be in this state for this long."
type FakeStep struct {
	Kind     FakeKind
	Duration time.Duration
}

// fakeTypingRate is the simulated keystroke rate during a "type" step, in
// keys/second — a realistic sustained-typing speed (ADR 0005's own examples
// use 5-10 keys/s), NOT the anti-mash ceiling.
const fakeTypingRate = 6.0

// FakeProvider is a scriptable Provider for tests and for verifying the UI
// on a box with no real input-device access (this Linux dev box; the
// overseer's headless verification). It never touches the OS: its
// Snapshot is a pure function of wall-clock time elapsed since Start, so
// repeated calls are consistent and nothing drifts from a background
// goroutine.
type FakeProvider struct {
	mu        sync.Mutex
	script    []FakeStep
	total     time.Duration
	startedAt time.Time
	started   bool
	honesty   Honesty

	activeApp        string
	activeAppDisplay string
}

// NewFakeProvider builds a provider from an explicit script, looping
// forever once started. An empty script is a valid, permanently-blind
// zero-signal provider (useful for testing the "no signal" path).
func NewFakeProvider(script []FakeStep, honesty Honesty) *FakeProvider {
	p := &FakeProvider{
		script:           script,
		honesty:          honesty,
		activeApp:        "code",
		activeAppDisplay: "VS Code",
	}
	for _, s := range script {
		p.total += s.Duration
	}
	return p
}

// WithActiveApp overrides the constant fake app identity the provider
// reports (default "code" / "VS Code").
func (p *FakeProvider) WithActiveApp(id, display string) *FakeProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.activeApp = id
	p.activeAppDisplay = display
	return p
}

// NewFakeProviderFromEnv reads DEXEL_FAKE_SCRIPT (format
// "type:30s,idle:40s,mouse:10s") and builds a looping FakeProvider from it.
// An unset or unparseable script falls back to a short built-in demo
// timeline so `go run` always has something to show.
func NewFakeProviderFromEnv() *FakeProvider {
	steps, err := ParseFakeScript(os.Getenv("DEXEL_FAKE_SCRIPT"))
	if err != nil {
		steps = DefaultFakeScript()
	}
	return NewFakeProvider(steps, HonestyGlobal)
}

// DefaultFakeScript is the built-in demo timeline used when no script is
// supplied: type for a while, go idle long enough to trip OnBreak, then
// mouse-only for a while.
func DefaultFakeScript() []FakeStep {
	return []FakeStep{
		{Kind: FakeType, Duration: 20 * time.Second},
		{Kind: FakeIdle, Duration: 40 * time.Second},
		{Kind: FakeMouse, Duration: 15 * time.Second},
	}
}

// ParseFakeScript parses "kind:duration,kind:duration,..." into steps.
// kind is one of type/mouse/idle; duration is anything time.ParseDuration
// accepts (e.g. "30s", "2m").
func ParseFakeScript(s string) ([]FakeStep, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty fake script")
	}
	parts := strings.Split(s, ",")
	steps := make([]FakeStep, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("bad fake script step %q (want kind:duration)", part)
		}
		kind := FakeKind(strings.ToLower(strings.TrimSpace(kv[0])))
		switch kind {
		case FakeType, FakeMouse, FakeIdle:
		default:
			return nil, fmt.Errorf("unknown fake script kind %q", kind)
		}
		d, err := time.ParseDuration(strings.TrimSpace(kv[1]))
		if err != nil {
			return nil, fmt.Errorf("bad duration in step %q: %w", part, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("step %q must have a positive duration", part)
		}
		steps = append(steps, FakeStep{Kind: kind, Duration: d})
	}
	if len(steps) == 0 {
		return nil, errors.New("fake script had no valid steps")
	}
	return steps, nil
}

// Start records the reference time the script begins at. Idempotent.
func (p *FakeProvider) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started {
		p.startedAt = time.Now()
		p.started = true
	}
	return nil
}

// Stop is a no-op (nothing to release).
func (p *FakeProvider) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.started = false
	return nil
}

// Honesty returns the honesty this provider was constructed with, so tests
// can exercise both a global and a blind fake source.
func (p *FakeProvider) Honesty() Honesty {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.honesty
}

// Snapshot computes the current simulated state purely from elapsed
// wall-clock time since Start and the script — no background goroutine, no
// drift, deterministic given a fixed startedAt.
func (p *FakeProvider) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.started || len(p.script) == 0 || p.total <= 0 {
		return Snapshot{ActiveApp: p.activeApp, ActiveAppDisplay: p.activeAppDisplay}
	}

	elapsed := time.Since(p.startedAt)
	cycles := int64(elapsed / p.total)
	remainder := elapsed % p.total

	// Walk the script to find (a) which step `remainder` currently falls
	// in, and (b) how much cumulative "type" time has elapsed within this
	// partial cycle — needed to keep KeystrokeCount monotonic across loops.
	var cur FakeStep
	var offsetIntoStep time.Duration
	var typeSecondsInRemainder float64
	var typeSecondsPerCycle float64
	acc := time.Duration(0)
	found := false
	for _, step := range p.script {
		if step.Kind == FakeType {
			typeSecondsPerCycle += step.Duration.Seconds()
		}
		if !found {
			if remainder < acc+step.Duration {
				cur = step
				offsetIntoStep = remainder - acc
				if step.Kind == FakeType {
					typeSecondsInRemainder += offsetIntoStep.Seconds()
				}
				found = true
			} else {
				if step.Kind == FakeType {
					typeSecondsInRemainder += step.Duration.Seconds()
				}
			}
		}
		acc += step.Duration
	}
	if !found {
		// Shouldn't happen (remainder < total by construction), but stay
		// honest rather than panic on a rounding edge.
		cur = p.script[len(p.script)-1]
	}

	totalTypeSeconds := float64(cycles)*typeSecondsPerCycle + typeSecondsInRemainder
	keystrokes := uint64(totalTypeSeconds * fakeTypingRate)

	snap := Snapshot{
		KeystrokeCount:   keystrokes,
		ActiveApp:        p.activeApp,
		ActiveAppDisplay: p.activeAppDisplay,
	}
	switch cur.Kind {
	case FakeType:
		snap.MouseActive = false
		snap.IdleSeconds = 0
	case FakeMouse:
		snap.MouseActive = true
		snap.IdleSeconds = 0
	case FakeIdle:
		snap.MouseActive = false
		snap.IdleSeconds = offsetIntoStep.Seconds()
	}
	return snap
}
