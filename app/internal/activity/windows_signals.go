// windows_signals.go — the PURE half of the Windows activity provider.
//
// Deliberately NOT build-tagged. provider_windows.go is `//go:build windows`
// and this repo has no Windows CI runner, so anything left inside that file
// is code nobody can execute before a user does. The same argument
// app_identity.go already makes for SanitizeAppID/NewAppIdentity applies
// twice over here, because the interesting parts of a hook-based provider
// are not the syscalls — they are three decisions:
//
//  1. WHEN does an observed event become a counted keystroke, and when does
//     it only refresh the mouse-recency flag (ADR 0005's anti-mash
//     coalescing — this file must produce the SAME answers as
//     provider_linux.go's handleEvent, and windows_signals_test.go pins
//     that equivalence).
//  2. WHEN do we conclude the OS silently evicted our low-level hook (the
//     GetLastInputInfo cross-check — see winWatchdog).
//  3. WHAT part of a process image path is allowed to become an app
//     identity (the base name, never the directories — see
//     windowsAppNameFromImagePath).
//
// All three are ordinary Go over ordinary values, so all three are tested on
// the Linux box that builds this repo. provider_windows.go is left holding
// only syscalls and lifecycle plumbing.
package activity

import (
	"strings"
	"sync/atomic"
	"time"
)

// windowsSampleInterval: alias for the package-wide anti-mash coalescing
// window (MouseSampleInterval, defined in provider.go). Same reasoning as
// linuxSampleInterval and darwinSampleInterval — see MouseSampleInterval's
// doc comment for why the three platforms reference one constant instead of
// each declaring their own 100ms.
const windowsSampleInterval = MouseSampleInterval

// winMonoBase anchors winNowNanos to process start.
//
// The provider's hot path is a Win32 hook callback that the OS gives a few
// hundred milliseconds to return before it evicts the hook (see
// provider_windows.go). It therefore may not allocate, and it may not take a
// mutex — so every timestamp it records has to be a plain int64 it can store
// atomically, not a time.Time. time.Since reads the runtime's MONOTONIC
// clock (QueryPerformanceCounter on Windows), which is also what makes these
// deltas immune to a wall-clock jump: a user correcting their timezone or an
// NTP step must never register as an hour of idleness.
var winMonoBase = time.Now()

// winNowNanos is the monotonic nanosecond reading every winTally timestamp
// is expressed in. Cheap and allocation-free by construction.
func winNowNanos() int64 { return int64(time.Since(winMonoBase)) }

// winTally is the Windows provider's whole observation state: five atomics
// and a counter. It is the lock-free counterpart of the mutex-guarded fields
// LinuxProvider keeps, and it exists in that shape for one reason — the
// low-level hook callback runs on the hook thread inside the OS's timeout
// budget, so it must be O(1) with no allocation and no lock that a Snapshot
// caller could be holding.
//
// Content-free by construction: there is nowhere in here to put a key. The
// only things that ever cross into it are a monotonic timestamp and the fact
// that SOMETHING happened — which is exactly the evdev argument ADR 0003
// made ("keycodes are read in one-line predicates and dropped as bools"),
// restated for a different ABI.
type winTally struct {
	// keystrokes is the monotonic counted-keystroke total. It survives
	// blind stretches and Stop/Start cycles deliberately: those presses
	// really were observed, and zeroing the counter would hand the engine a
	// bogus negative delta the moment observation resumes (the same
	// invariant LinuxProvider.Snapshot documents for its blind branch).
	keystrokes atomic.Uint64

	// rawEvents counts EVERY event the hooks delivered, before coalescing.
	// It is not a signal and never reaches a Snapshot — it is the liveness
	// input the eviction watchdog compares against GetLastInputInfo.
	rawEvents atomic.Uint64

	// lastKeyNanos / lastMouseNanos are the last COUNTED tick of each kind
	// — the anti-mash gate, not the last event.
	lastKeyNanos   atomic.Int64
	lastMouseNanos atomic.Int64

	// mouseActiveUntilNanos is how long MouseActive stays true after a
	// counted mouse tick (one coalescing window, exactly as on Linux).
	mouseActiveUntilNanos atomic.Int64

	// lastAnyInputNanos is the idle clock's origin: the last event of ANY
	// kind, updated BEFORE coalescing (mirroring provider_linux.go's
	// `p.lastAnyInput = now` sitting above its switch). Coalescing shapes
	// the ECONOMY; it must not make a fast typist look idle.
	lastAnyInputNanos atomic.Int64
}

// reset restarts the observation window at now without touching the
// keystroke total.
//
// It is called at Start and again after the watchdog reinstalls an evicted
// hook, and the second case is the important one: the stretch during which
// the hook was dead is time this provider COULD NOT SEE, so handing it to
// the engine as observed idleness would be the ADR 0010 lie on a delay
// (LinuxProvider does the same thing when a rescan recovers its first
// device). The idle clock therefore starts again from the moment we can see
// again, not from the last event we happened to catch before going dark.
func (t *winTally) reset(now int64) {
	// One full window in the past, NOT zero. The coalescing gates below are
	// `now - lastTick >= window`, so a zero here would swallow the first
	// event of a fresh generation (and, at process start, every event in the
	// first 100ms). provider_linux.go gets this for free — its lastKeyTick
	// is a zero time.Time, so `now.Sub(lastKeyTick)` is decades — and an
	// int64 nanosecond counter has no such natural "long ago" value, so it
	// has to be said explicitly.
	t.lastKeyNanos.Store(now - int64(windowsSampleInterval))
	t.lastMouseNanos.Store(now - int64(windowsSampleInterval))
	t.mouseActiveUntilNanos.Store(0)
	t.lastAnyInputNanos.Store(now)
}

// key records one observed key-down. Mirrors provider_linux.go's
// `typ == evKey && value == keyPressValue` branch: the idle clock always
// moves, the COUNT only moves once per coalescing window.
func (t *winTally) key(now int64) {
	t.rawEvents.Add(1)
	t.lastAnyInputNanos.Store(now)
	if now-t.lastKeyNanos.Load() >= int64(windowsSampleInterval) {
		t.lastKeyNanos.Store(now)
		t.keystrokes.Add(1)
	}
}

// mouse records one observed mouse event (motion, wheel, or button).
// Mirrors provider_linux.go's `typ == evRel` branch: a counted tick keeps
// MouseActive true for exactly one coalescing window.
func (t *winTally) mouse(now int64) {
	t.rawEvents.Add(1)
	t.lastAnyInputNanos.Store(now)
	if now-t.lastMouseNanos.Load() >= int64(windowsSampleInterval) {
		t.lastMouseNanos.Store(now)
		t.mouseActiveUntilNanos.Store(now + int64(windowsSampleInterval))
	}
}

// read returns the three input signals as of now.
func (t *winTally) read(now int64) (keystrokes uint64, mouseActive bool, idleSeconds float64) {
	return t.keystrokes.Load(),
		now < t.mouseActiveUntilNanos.Load(),
		time.Duration(now - t.lastAnyInputNanos.Load()).Seconds()
}

// winWatchdogStrikes is how many consecutive suspicious watchdog intervals
// it takes to conclude the hook was evicted rather than merely quiet.
//
// One interval is not enough, because there is a legitimate way for
// GetLastInputInfo to advance while our hooks see nothing: input delivered
// to a DIFFERENT desktop — a UAC consent prompt, the lock screen, the
// Ctrl-Alt-Del secure desktop. A low-level hook installed on the
// interactive desktop is not called for those, and reinstalling on account
// of one would mean re-hooking every time the user typed a password.
// Requiring the condition to hold across winWatchdogStrikes intervals turns
// "a UAC prompt happened" into a non-event while still catching a genuine
// eviction inside winWatchdogStrikes × winWatchdogInterval.
const winWatchdogStrikes = 4

// winWatchdog decides whether the OS has silently evicted our low-level
// hooks, by cross-checking two facts that must agree.
//
// Windows enforces a timeout (HKCU\Control Panel\Desktop\LowLevelHooksTimeout,
// 300ms by default) on every low-level hook callback. A callback that
// overruns it does not get an error: the OS stops calling the hook and
// leaves the handle looking perfectly valid. Nothing about the provider's own
// state changes, so a hook-only provider cannot tell "the user stopped
// typing" from "we stopped being told about it" — and the second one, read
// as the first, is precisely the field failure the Linux provider already
// paid for (a frozen keystroke counter feeding an idle clock that climbed
// for 19 hours; see provider_linux.go's package comment).
//
// GetLastInputInfo is the belt to the hook's braces. It is a
// system-maintained tick count of the last input event in this session —
// content-free by construction (there is no event in it, only a timestamp,
// the same property that makes macOS's CGEventSource timers usable under
// ADR 0010) and maintained by the OS itself rather than by a callback of
// ours that can be evicted. So:
//
//	GetLastInputInfo advanced  &&  our hooks counted nothing
//	  -> the hooks are not being called; reinstall them.
//
// The two are independent sources for the same fact, which is the only kind
// of cross-check worth having. The watchdog is deliberately NOT used as an
// idle SOURCE: substituting GetLastInputInfo for the hooks would give the
// engine an idle clock while the keystroke count stayed frozen — honest
// about one signal and silently broken about the other, which is worse than
// saying HonestyBlind out loud.
type winWatchdog struct {
	primed     bool
	strikes    int
	lastEvents uint64
	lastTick   uint32
}

// reset returns the watchdog to its unprimed state — called whenever the
// hooks are known to be down (so the interval during which they were being
// reinstalled cannot count as a strike).
func (w *winWatchdog) reset() {
	w.primed = false
	w.strikes = 0
	w.lastEvents = 0
	w.lastTick = 0
}

// observe folds one watchdog interval into the decision.
//
// rawEvents is winTally.rawEvents (every event the hooks delivered, coalesced
// or not — coalescing would hide exactly the low rate an eviction produces).
// lastInputTick is GetLastInputInfo's dwTime, and osInputReadable is whether
// that call succeeded: if the cross-check itself is unavailable there is no
// second opinion, so the watchdog abstains rather than guessing.
//
// Returns true at most once per suspected eviction (the strike counter is
// cleared as it fires), so a caller that reinstalls on true cannot be driven
// into a reinstall loop by one bad interval.
func (w *winWatchdog) observe(rawEvents uint64, lastInputTick uint32, osInputReadable bool) bool {
	if !osInputReadable {
		// No second opinion available. Abstain, and require the strike run
		// to build up again from scratch once it comes back.
		w.strikes = 0
		w.primed = false
		return false
	}
	if !w.primed {
		// First interval: we have levels, not deltas. Record and wait.
		w.primed = true
		w.lastEvents = rawEvents
		w.lastTick = lastInputTick
		return false
	}

	// != rather than > on both: rawEvents is monotonic but dwTime is a
	// GetTickCount value that wraps every ~49.7 days, and a wrap must read
	// as "input happened", not as "time went backwards".
	sawHookEvents := rawEvents != w.lastEvents
	sawOSInput := lastInputTick != w.lastTick
	w.lastEvents = rawEvents
	w.lastTick = lastInputTick

	if sawOSInput && !sawHookEvents {
		w.strikes++
	} else {
		w.strikes = 0
	}
	if w.strikes >= winWatchdogStrikes {
		w.strikes = 0
		return true
	}
	return false
}

// windowsAppNameFromImagePath reduces a process image path to the APPLICATION
// NAME that is allowed to become an app identity: the base name, with a
// trailing ".exe" removed.
//
// ============================ HARD BOUNDARY ============================
// The DIRECTORIES are dropped here and never travel, and that is a privacy
// decision, not tidiness. A real image path is
// `C:\Users\jawwad\AppData\Local\Programs\Microsoft VS Code\Code.exe` — it
// carries the account name, and for a portable or project-local binary it
// can carry a client name, a repository name, or the contents of a
// directory the user considers private. That is content in ADR 0002/0009's
// sense, and the only reason this function receives a full path at all is
// that QueryFullProcessImageName has no "just the name" mode.
//
// So the path is narrowed to its last segment IMMEDIATELY, before anything
// else can look at it: provider_windows.go never logs it, never stores it,
// and holds it only in the local variable it passes here. The result then
// goes through NewAppIdentity -> SanitizeAppID like every other platform's,
// so the same lowercase/[a-z0-9._-]/32-byte cap applies.
// =======================================================================
//
// ".exe" is stripped case-insensitively so `Code.exe`, `CODE.EXE`, and
// `Code` all land on the id "code" — which is what makes the existing
// friendlyNames table (written against macOS bundle names) work unchanged
// for `code`, `chrome`, `firefox`, `slack`, `spotify`, `obsidian`, and the
// rest. Only ".exe" is stripped: any other trailing extension is part of the
// name as far as this function is concerned, because inventing a general
// extension-stripping rule is how you eventually strip a meaningful suffix.
func windowsAppNameFromImagePath(image string) string {
	// Both separators, because a native path (\Device\HarddiskVolume3\...)
	// and a Win32 path can both reach here and Go's filepath is
	// host-flavoured (this file compiles on Linux, where '\\' is an
	// ordinary character).
	if i := strings.LastIndexAny(image, `\/`); i >= 0 {
		image = image[i+1:]
	}
	if len(image) >= 4 && strings.EqualFold(image[len(image)-4:], ".exe") {
		image = image[:len(image)-4]
	}
	return image
}
