// Package activity defines the platform-neutral contract that turns real
// keyboard/mouse activity into normalized, content-free signals the engine
// understands (ADR 0002/0009: counts and app identity only — never key
// identity, text, clipboard, or window titles). Platform-specific capture
// lives in build-tagged files in this same package (provider_darwin.go,
// provider_linux.go); the game and server never import anything but this
// interface.
package activity

// Snapshot is a point-in-time view of system activity, sampled by a
// Provider. Every field is content-free by construction — counts, a
// boolean, a duration, and a sanitized/display app identifier. Adding a
// field that could hold typed text, a keycode, or a window title is a
// structural privacy violation; see content_free_test.go, which is written
// so such a field stops the package compiling^Wpassing tests.
type Snapshot struct {
	// KeystrokeCount is a monotonic counter of keystrokes observed since the
	// provider started. Never decreases while the provider runs. It counts
	// presses, never which key.
	KeystrokeCount uint64

	// MouseActive reports whether mouse motion, drag, or scroll happened
	// recently (within the provider's own anti-mash coalescing window —
	// see ADR 0005). It is a recency flag, not a position or a count.
	MouseActive bool

	// IdleSeconds is the time since the last input of ANY kind the provider
	// can see (the minimum over every tracked input type). For a blind
	// provider (Honesty() == HonestyBlind) this value must not be trusted
	// by callers to mean anything — the engine's mood rules gate on
	// Honesty(), not on this field alone.
	IdleSeconds float64

	// ActiveApp is the sanitized application identifier (lowercase,
	// [a-z0-9._-], capped at 32 bytes — see SanitizeAppID) of the
	// foreground application, or "" if unknown. Per ADR 0009 this is an
	// APPLICATION identity only, never a window title, document name, or
	// URL — those are discarded at the source and never reach this struct.
	ActiveApp string

	// ActiveAppDisplay is a friendly human-readable name for ActiveApp
	// (e.g. "VS Code" for "code"), or "" if unknown. Derived purely from
	// ActiveApp via a small static lookup table (FriendlyName) — never
	// derived from anything else the OS reports.
	ActiveAppDisplay string
}

// Honesty describes what a Provider can actually observe, so the engine's
// mood rules (ADR 0010) can refuse to claim things a blind source cannot
// know. This is the Go port of the Rust game's ActivitySource.is_global().
type Honesty int

const (
	// HonestyGlobal: the provider observes input system-wide, independent of
	// which window (if any) is focused. IdleSeconds is a real global idle
	// clock and OnBreak may honestly be derived from it.
	HonestyGlobal Honesty = iota
	// HonestyBlind: the provider cannot see global input (permission
	// missing, unsupported platform, no readable device). The engine must
	// never report OnBreak from a blind provider — "on break because you
	// minimized me" was the exact lie ADR 0010 was written to kill.
	HonestyBlind
)

// Provider is implemented by each platform capture strategy. It is started
// once by the app and sampled by the engine on every tick.
type Provider interface {
	// Start begins observation. It is always safe to call, even when the
	// underlying capability is unavailable (missing permission, absent
	// device, unsupported platform): it returns a descriptive error and the
	// provider degrades to reporting blind zero-signal snapshots rather
	// than panicking or lying about what it can see.
	Start() error
	// Stop ends observation and releases any resources.
	Stop() error
	// Snapshot returns the current view of activity. Safe to call
	// concurrently with Start/Stop and from multiple goroutines.
	Snapshot() Snapshot
	// Honesty reports what this provider can actually see. The engine gates
	// its OnBreak claim on this, not on Snapshot alone.
	Honesty() Honesty
}
