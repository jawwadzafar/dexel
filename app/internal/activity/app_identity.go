package activity

// AppIdentity is the normalized result of ONE app-identity observation: the
// sanitized id, its friendly display name, and whether the provider could
// observe app identity at all in the current process context.
//
// It exists as a pure, cross-platform value (rather than three fields the
// darwin cgo file assembles inline) for two reasons:
//
//  1. The decision "what does the OS's answer mean" is the interesting part
//     and it is testable from any host — provider_darwin.go is build-tagged
//     darwin-only and needs a Mac (and a window server) to exercise, so any
//     logic left inside it is effectively untested on CI. Same reasoning as
//     SanitizeAppID/FriendlyName living here rather than in the cgo file.
//
//  2. It forces the "unknown" case to be spelled out. Before this type,
//     every provider that could not see app identity returned ActiveApp ""
//     — the SAME value as "I looked, and genuinely nothing is frontmost."
//     Downstream those two read identically ("Working..."), which is the
//     ADR 0010 class of lie: a capability failure wearing the costume of an
//     observation. See Available below.
type AppIdentity struct {
	// ID is the sanitized app identifier (see SanitizeAppID), or "" when no
	// app identity was observed.
	ID string
	// Display is FriendlyName(ID), or "" when ID is "".
	Display string
	// Available reports whether app identity is OBSERVABLE in this process
	// context — not whether an app happened to be frontmost. The two are
	// different facts and only this field can tell them apart:
	//
	//   Available && ID != ""  -> a real app is frontmost, here it is
	//   Available && ID == ""  -> looked, nothing is frontmost (bare desktop,
	//                             every window minimized)
	//   !Available             -> this provider cannot see app identity here
	//                             at all (no window server to ask, or the
	//                             platform has no permissionless way to ask).
	//                             Nothing may be claimed about apps, and the
	//                             app-switch counter is not "0 switches" but
	//                             "not measured."
	//
	// Deliberately NOT folded into Honesty: Honesty is about INPUT
	// visibility and the engine gates its OnBreak claim on it (ADR 0010).
	// A provider that sees every keystroke but cannot name the frontmost app
	// is still globally honest about input, and degrading its Honesty to
	// suppress app claims would silently also suppress OnBreak — trading one
	// lie for a different one.
	Available bool
}

// NewAppIdentity normalizes one raw OS-reported application name into the
// content-free identity that may cross the activity boundary.
//
// rawOwnerName is an APPLICATION name and nothing else (macOS
// kCGWindowOwnerName, i.e. the owning process's application name). Callers
// must never pass a window title, document name, or URL — ADR 0002/0009
// forbid those from reaching a Snapshot at all, and SanitizeAppID's cap
// would happily shorten one rather than reject it, so the guarantee lives
// at the call sites (there is exactly one per platform) and not here.
//
// observable is what the platform capture actually established: false means
// the query itself could not be answered (no window-server connection, no
// supported mechanism), which is NOT the same as an answer of "nothing."
// An unobservable identity always normalizes to an empty ID, so a caller
// that ignores Available still cannot be told an app name that was invented.
func NewAppIdentity(rawOwnerName string, observable bool) AppIdentity {
	if !observable {
		return AppIdentity{Available: false}
	}
	id := SanitizeAppID(rawOwnerName)
	if id == "" {
		// Observable, but nothing to name: either genuinely no frontmost
		// app, or a name that sanitized away to nothing. Both are honestly
		// "no app identity" rather than "cannot see app identity."
		return AppIdentity{Available: true}
	}
	return AppIdentity{ID: id, Display: FriendlyName(id), Available: true}
}
