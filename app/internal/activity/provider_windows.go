//go:build windows

// Windows activity provider — the end of the "blind on Windows" era
// (ADR 0021).
//
// Until this file existed, `provider_select_other.go` handed Windows a
// permanently blind fake provider: the game ran, the store worked, and
// nothing the user typed ever accrued. That was honest (HonestyBlind is
// exactly the machinery for saying "I cannot see") but it was not a product.
//
// # The mechanism, and why it is the only pure-Go one
//
// Two low-level hooks, WH_KEYBOARD_LL and WH_MOUSE_LL, installed by
// SetWindowsHookExW from a dedicated OS thread running a Win32 message loop.
//
// Low-level hooks are the ONLY hook types whose callback runs inside the
// INSTALLING process, on the installing thread. Every other global hook type
// (WH_KEYBOARD, WH_MOUSE, WH_CBT, ...) is implemented by the OS injecting the
// hook's DLL into every process that generates the event — which requires a
// native DLL, which requires a C toolchain, which is precisely what the
// no-cgo constraint forbids (the cross-compile matrix in
// scripts/build-release.sh depends on `GOOS=windows go build` working from
// Linux; see docs/production-runtime/PLATFORM_NOTES.md). Low-level hooks need
// no DLL, so they are reachable from pure Go via syscall.NewCallback, and
// they are the reason this provider can exist at all.
//
// Being callback-in-our-process also means the 32/64-bit question never
// arises: nothing is injected anywhere, so a 64-bit dexel sees a 32-bit
// app's typing and vice versa.
//
// # What crosses the boundary, and what does not
//
// The callbacks COUNT. That is the whole contract (ADR 0002/0009):
//
//   - Keyboard: WM_KEYDOWN / WM_SYSKEYDOWN increments a keystroke counter.
//     The hook's KBDLLHOOKSTRUCT is dereferenced for exactly one field,
//     vkCode, and only to reject the vkCode==0 pseudo-events the ABI can
//     deliver — the same shape as provider_linux.go reading an evdev `code`
//     into a one-line predicate (`code < keyCodeCeiling`) and dropping it.
//     Nothing derived from a key is stored, logged, or returned. The struct
//     is DECLARED with scanCode, flags, time and dwExtraInfo as BLANK fields
//     (`_`), so the physical key identity is not merely unused — it has no
//     name in this program and cannot be read without editing the type.
//
//   - Mouse: motion, wheel, and button events refresh the MouseActive
//     recency flag. The hook's MSLLHOOKSTRUCT is NEVER dereferenced — it is
//     not even declared in this file — because its first field is the cursor
//     POSITION. A position is content in the ADR 0009 sense (where you
//     clicked is what you were doing), and wParam alone already tells us
//     everything the signal needs.
//
//   - Nothing else. GetWindowTextW does not appear in this file and must not:
//     the window TITLE is the document you have open, and it is the exact
//     thing the darwin provider's HARD BOUNDARY comment refuses to read from
//     CGWindowList. windows_signals_test.go asserts, by scanning this source
//     on the Linux CI box, that neither GetWindowText nor MSLLHOOKSTRUCT
//     ever appears here.
//
// # The hot path is O(1) because the OS enforces it
//
// Windows gives a low-level hook callback LowLevelHooksTimeout milliseconds
// (HKCU\Control Panel\Desktop, 300 by default) to return. Overrun it and the
// OS stops calling the hook — silently, with the handle still looking valid.
// So the callbacks do atomic increments and nothing else: no allocation, no
// mutex a Snapshot caller could be holding, no logging, no map. All
// observation state is the atomics in winTally (windows_signals.go).
//
// That is necessary and not sufficient, and the gap is Go-specific: even an
// allocation-free callback can be delayed past the timeout by the runtime
// UNDERNEATH it — a stop-the-world GC pause, a preemption at a safe point
// inside the callback, a scheduler hiccup on a loaded machine. Nothing here
// can make that impossible. Which is precisely why the watchdog below is not
// decoration: it covers the failure mode this code does not control.
//
// # Belt and braces: the eviction watchdog
//
// Because eviction is silent, the provider cross-checks itself against
// GetLastInputInfo — a content-free, OS-maintained tick count of the last
// input in this session. If the OS says input happened while our hooks
// counted nothing, across winWatchdogStrikes consecutive intervals, the
// hooks were evicted and are reinstalled (on the hook thread, via a posted
// thread message). See winWatchdog for the full argument; the failure it
// prevents is the one provider_linux.go already paid for in the field — a
// frozen counter feeding an idle clock that climbed for 19 hours.
//
// # Field verification status
//
// This repo has no Windows CI runner and no Windows hardware. Everything
// decidable without one has been decided on Linux: the coalescing semantics,
// the eviction rule and the image-path narrowing are pure code in
// windows_signals.go with tests, `GOOS=windows go build` and `go vet` are
// clean, and the syscall surface is written against documented signatures.
// The hook install itself, the message loop, and app identity are
// UNVERIFIED ON HARDWARE — stated the same way desktop/README.md and
// spawn_windows.go state it, rather than implied. ADR 0021 records what a
// first field session should look at.
package activity

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// whKeyboardLL / whMouseLL are the two low-level hook ids
	// (winuser.h WH_KEYBOARD_LL = 13, WH_MOUSE_LL = 14).
	whKeyboardLL = 13
	whMouseLL    = 14

	// hcAction: the hook callback may only act on this nCode. Anything else
	// (in particular a NEGATIVE nCode) must be chained on untouched.
	// Compared as a uintptr because that is how the callback receives it —
	// a negative nCode becomes a very large uintptr, which is exactly why
	// the test is `== hcAction` and never `>= 0`.
	hcAction = 0

	// Keyboard messages the LL keyboard hook delivers in wParam. Only the
	// two DOWN messages are counted: a key press is one keystroke, and
	// counting its release too would double every count. Auto-repeat is not
	// distinguishable here and does not need to be — the anti-mash window
	// (winTally.key) caps the counted rate regardless, which is the same
	// answer provider_linux.go reaches by ignoring EV_KEY value==2.
	wmKeyDown    = 0x0100
	wmSysKeyDown = 0x0104

	// Mouse messages the LL mouse hook delivers in wParam. All of them feed
	// the same recency flag; none of them is counted.
	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmRButtonDown = 0x0204
	wmRButtonUp   = 0x0205
	wmMButtonDown = 0x0207
	wmMButtonUp   = 0x0208
	wmMouseWheel  = 0x020A
	wmXButtonDown = 0x020B
	wmXButtonUp   = 0x020C
	wmMouseHWheel = 0x020E

	// wmQuit ends the message loop (GetMessage returns 0 on it).
	wmQuit = 0x0012
	// wmReinstallHooks is a private thread message (WM_APP + 1) the
	// watchdog posts to ask the hook thread to re-install its hooks. It is
	// deliberately a MESSAGE rather than a direct call from the watchdog
	// goroutine: SetWindowsHookExW binds a low-level hook to the calling
	// thread's message loop, so the install must happen on the thread that
	// will service it.
	wmReinstallHooks = 0x8000 + 1

	// pmNoRemove: PeekMessageW(&msg, 0, 0, 0, PM_NOREMOVE) is how the hook
	// thread FORCES its message queue into existence before it reports
	// ready. A thread has no queue until it first calls a message function,
	// and PostThreadMessageW to a thread without one fails with
	// ERROR_INVALID_THREAD_ID — which would mean Stop's WM_QUIT losing a
	// race with the thread reaching GetMessageW.
	pmNoRemove = 0x0000
)

const (
	// windowsStartWaitTimeout bounds how long Start blocks waiting for the
	// hook thread to report whether the install succeeded. The install is
	// two SetWindowsHookExW calls, so this is ~1000x headroom; it exists so
	// that boot cannot hang on a Win32 call misbehaving in a way nobody
	// here has met. On timeout the provider is simply BLIND-BUT-LIVE: if
	// the install lands late, gen.hookOK flips true and Honesty() recovers
	// on its own, exactly as a Linux rescan recovers a device.
	windowsStartWaitTimeout = 2 * time.Second

	// windowsStopWaitTimeout is the ceiling on Stop, mirroring
	// stopWaitTimeout in provider_linux.go and for the same reason (BUG-9,
	// docs/plan/BUGS.md): persist + provider Stop + http.Shutdown must all
	// fit inside the CLI's stop grace or a graceful stop escalates to a
	// kill. The message loop makes the normal path microseconds — WM_QUIT
	// wakes GetMessageW immediately — so this is a ceiling, not a delay.
	windowsStopWaitTimeout = 500 * time.Millisecond

	// windowsWatchdogInterval is how often the GetLastInputInfo cross-check
	// runs. With winWatchdogStrikes = 4 an evicted hook is reinstalled
	// within ~2 minutes of the user's next typing, and a UAC prompt or lock
	// screen (which legitimately advances GetLastInputInfo while our hooks
	// see nothing) cannot trigger a reinstall on its own.
	windowsWatchdogInterval = 30 * time.Second

	// windowsAppIdentityInterval matches darwinAppIdentityInterval, for the
	// same reason: the engine consumes app identity once per 1s tick, so
	// re-reading it faster only burns CPU in a process whose entire pitch is
	// being invisible.
	windowsAppIdentityInterval = 500 * time.Millisecond

	// windowsPostRetryInterval is how often Stop re-posts WM_QUIT while it
	// waits. Re-posting is harmless (an extra message in a queue nobody
	// will read again) and it covers the one way the post can legitimately
	// fail: the thread's queue not existing yet.
	windowsPostRetryInterval = 20 * time.Millisecond
)

// ---------------------------------------------------------------------------
// Win32 entry points
//
// user32 only, resolved once. golang.org/x/sys/windows already types the
// four calls app identity needs (GetForegroundWindow,
// GetWindowThreadProcessId, OpenProcess, QueryFullProcessImageName), so those
// are used directly; the hook and message-loop calls it does not wrap are
// declared here as LazyProc addresses. Both halves are plain syscalls — no
// cgo, so `GOOS=windows go build` still cross-compiles from anything.
// ---------------------------------------------------------------------------

// winCallNextHookExAddr is read by the hook callbacks, which is why it is an
// atomic rather than a LazyProc: the callbacks run inside the OS's timeout
// budget and must not take the lock LazyProc.Find would. It is written by
// winProcAddrs (below) before any hook can possibly be installed.
var winCallNextHookExAddr atomic.Uintptr

type winProcs struct {
	setWindowsHookExW   uintptr
	unhookWindowsHookEx uintptr
	getMessageW         uintptr
	peekMessageW        uintptr
	postThreadMessageW  uintptr
	getLastInputInfo    uintptr

	// NOTE: there is no getWindowTextW field, and adding one is the change
	// this provider's HARD BOUNDARY forbids. A window title is a document
	// name; see the package comment and windows_signals_test.go.
}

// winProcAddrs resolves every user32 entry point this provider needs, once
// per process, and reports a descriptive error if any is missing (which
// would mean a Windows so unusual that reporting BLIND is the only honest
// answer).
var winProcAddrs = sync.OnceValues(func() (*winProcs, error) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	resolve := func(name string) (uintptr, error) {
		p := user32.NewProc(name)
		if err := p.Find(); err != nil {
			return 0, fmt.Errorf("user32.dll!%s: %w", name, err)
		}
		return p.Addr(), nil
	}
	var (
		procs winProcs
		errs  []error
		err   error
	)
	callNext, err := resolve("CallNextHookEx")
	errs = append(errs, err)
	procs.setWindowsHookExW, err = resolve("SetWindowsHookExW")
	errs = append(errs, err)
	procs.unhookWindowsHookEx, err = resolve("UnhookWindowsHookEx")
	errs = append(errs, err)
	procs.getMessageW, err = resolve("GetMessageW")
	errs = append(errs, err)
	procs.peekMessageW, err = resolve("PeekMessageW")
	errs = append(errs, err)
	procs.postThreadMessageW, err = resolve("PostThreadMessageW")
	errs = append(errs, err)
	procs.getLastInputInfo, err = resolve("GetLastInputInfo")
	errs = append(errs, err)
	if joined := errors.Join(errs...); joined != nil {
		return nil, joined
	}
	// Published BEFORE any hook exists, so the callbacks can never observe
	// a zero here: the only thing that installs a hook is hookThread, and
	// it calls winProcAddrs first.
	winCallNextHookExAddr.Store(callNext)
	return &procs, nil
})

// winMsg is Win32's MSG.
//
// Every field the provider does not need is BLANK (`_`), which is not
// stylistic: MSG.pt is the CURSOR POSITION at the time of the message, and a
// field with no name cannot be read by accident, by a refactor, or by
// someone adding "just a debug log". The struct still has to be the right
// SIZE for GetMessageW to fill it, which is the only reason those bytes are
// described at all.
type winMsg struct {
	hwnd    uintptr
	message uint32
	_       uint32 // (padding to the natural alignment of wParam)
	wParam  uintptr
	lParam  uintptr
	_       uint32   // time
	_       [2]int32 // pt — the cursor position. Unnamed on purpose.
	_       uint32   // lPrivate
}

// winKbdLLHookStruct is Win32's KBDLLHOOKSTRUCT with everything but vkCode
// blanked out. See the package comment's HARD BOUNDARY: scanCode is the
// physical key, flags carries the injected/extended bits, and dwExtraInfo is
// whatever the injector chose to attach. None of them has a name here, so
// none of them can be read.
type winKbdLLHookStruct struct {
	vkCode uint32
	_      uint32  // scanCode
	_      uint32  // flags
	_      uint32  // time
	_      uintptr // dwExtraInfo
}

// winLastInputInfo is Win32's LASTINPUTINFO: a size and a tick count.
// Content-free by construction — there is no event in it.
type winLastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

// ---------------------------------------------------------------------------
// The hook callbacks — THE hot path
// ---------------------------------------------------------------------------

// winActiveProvider is the callbacks' only route back to a provider.
//
// A hook callback is a bare C function pointer with no user-data argument, so
// something process-global has to bridge it to the provider instance. An
// atomic pointer is that something: the load is one instruction (no lock a
// Snapshot caller could be holding), and Stop clearing it means a callback
// that fires during teardown finds nil and simply chains — no events counted
// against a generation that has ended.
var winActiveProvider atomic.Pointer[WindowsProvider]

// winKeyboardCallback / winMouseCallback wrap the two hook procedures once
// per process.
//
// Once, because syscall.NewCallback's registration is PERMANENT and the
// process has a hard cap on how many callbacks it may ever create. Building
// a fresh pair on every Start would spend that budget on every
// `dexel pause` / `dexel resume` cycle and eventually panic in a long-lived
// runtime — which is the shape of bug that only shows up after a user has
// been happy for a month.
var winKeyboardCallback = sync.OnceValue(func() uintptr {
	return syscall.NewCallback(winKeyboardHookProc)
})

var winMouseCallback = sync.OnceValue(func() uintptr {
	return syscall.NewCallback(winMouseHookProc)
})

// winKeyboardHookProc is LowLevelKeyboardProc.
//
// Allocation-free and lock-free, because the OS's LowLevelHooksTimeout is a
// real deadline and overrunning it silently un-hooks us (see the package
// comment and winWatchdog).
//
// The third parameter is DECLARED as *winKbdLLHookStruct rather than as the
// raw `lParam uintptr` the C prototype names, and that is not cosmetic:
// syscall.NewCallback accepts any pointer-sized argument type, so letting
// the ABI hand us a typed pointer removes the uintptr -> unsafe.Pointer
// conversion entirely. That conversion is the one `go vet`'s unsafeptr
// check flags, and rightly — it is unverifiable by construction. This way
// there is no unsafe operation in the hot path at all, and the type system
// (not a comment) is what says which bytes may be looked at.
func winKeyboardHookProc(nCode, wParam uintptr, kb *winKbdLLHookStruct) uintptr {
	if nCode == hcAction && (wParam == wmKeyDown || wParam == wmSysKeyDown) {
		if p := winActiveProvider.Load(); p != nil && kb != nil {
			// The ONE field read. vkCode rejects the vkCode==0 pseudo-event
			// the ABI can deliver, then goes out of scope unrecorded — the
			// evdev argument from ADR 0003, restated: the key identity is an
			// internal predicate, the COUNT is what crosses the boundary.
			if kb.vkCode != 0 {
				p.tally.key(winNowNanos())
			}
		}
	}
	// Chained on unchanged: the pointer goes straight back out as the
	// lParam it arrived as, so the rest of the hook chain sees exactly what
	// the OS sent.
	return winCallNextHook(nCode, wParam, uintptr(unsafe.Pointer(kb)))
}

// winMouseHookProc is LowLevelMouseProc.
//
// lParam points at an MSLLHOOKSTRUCT whose first field is the cursor
// POSITION. It is never dereferenced, and the struct is never even declared
// in this package: wParam alone says which kind of mouse event happened,
// which is all the MouseActive recency signal needs.
//
// Buttons feed the same signal as motion and wheel here. That is a small,
// deliberate divergence from provider_linux.go, where a mouse button arrives
// as EV_KEY with a BTN_* code and is dropped by BOTH branches of handleEvent
// (too high for the keystroke ceiling, not EV_REL) — an artifact of evdev
// sharing one code space between keys and buttons, not a decision. Windows
// hands buttons to the MOUSE hook unambiguously, so counting them as mouse
// activity is the honest reading. It cannot inflate earning: MouseActive is a
// coalesced boolean, never a count.
func winMouseHookProc(nCode, wParam, lParam uintptr) uintptr {
	if nCode == hcAction {
		switch wParam {
		case wmMouseMove, wmMouseWheel, wmMouseHWheel,
			wmLButtonDown, wmLButtonUp, wmRButtonDown, wmRButtonUp,
			wmMButtonDown, wmMButtonUp, wmXButtonDown, wmXButtonUp:
			if p := winActiveProvider.Load(); p != nil {
				p.tally.mouse(winNowNanos())
			}
		}
	}
	return winCallNextHook(nCode, wParam, lParam)
}

// winCallNextHook chains to the rest of the hook chain. The first argument
// (hhk) is 0: it has been ignored since Windows XP, and passing 0 keeps the
// callbacks from needing to reach the provider's handles at all.
func winCallNextHook(nCode, wParam, lParam uintptr) uintptr {
	addr := winCallNextHookExAddr.Load()
	if addr == 0 {
		// Unreachable: a hook cannot be installed before winProcAddrs
		// published this. Returning 0 rather than panicking inside an OS
		// callback is the safe direction, and 0 means "let the system pass
		// the event on".
		return 0
	}
	r, _, _ := syscall.SyscallN(addr, 0, nCode, wParam, lParam)
	return r
}

// ---------------------------------------------------------------------------
// The provider
// ---------------------------------------------------------------------------

// winGeneration is one Start..Stop lifetime's worth of shared state.
//
// It is a separate object, exactly as LinuxProvider allocates a fresh
// WaitGroup per Start, so that a goroutine or hook thread which outlives its
// Stop cannot touch the NEXT generation's state. In particular hookOK lives
// here rather than on the provider: a lingering hook thread from generation N
// storing false must not blind generation N+1.
type winGeneration struct {
	stop  chan struct{}
	ready chan error
	done  chan struct{}
	wg    sync.WaitGroup

	// hookOK is the honesty bit: true only while both hooks are believed
	// installed. Honesty() reads it, so it is the single place "can this
	// provider see input" is answered.
	hookOK atomic.Bool

	// tid is the hook thread's Win32 thread id, 0 until the thread has a
	// message queue. PostThreadMessageW needs it and nothing else does.
	tid atomic.Uint32

	// reinstalls counts watchdog-driven hook reinstalls, for the log line
	// (and so a support report can say "this happened 40 times today").
	reinstalls atomic.Uint64
}

// WindowsProvider observes global keyboard and mouse activity through
// WH_KEYBOARD_LL / WH_MOUSE_LL low-level hooks on a dedicated OS thread, and
// names the foreground application from its process image name. It counts;
// it never reads keys, cursor positions, or window titles. See the package
// comment for the full argument.
type WindowsProvider struct {
	// tally is the lock-free observation state the hook callbacks write.
	// Not behind mu on purpose — see winTally.
	tally winTally

	mu  sync.Mutex
	gen *winGeneration

	// appIdentity is the last app-identity observation, refreshed on its own
	// slower cadence (windowsAppIdentityInterval) like the darwin provider's.
	appIdentity AppIdentity
	// appEverObserved is the capability half of ADR 0019's availability bit,
	// STICKY for the life of the provider. See appIdentityLoop for why
	// "have we ever succeeded" is the honest capability test on Windows.
	appEverObserved atomic.Bool

	// Seams. logf is the log sink; the intervals let a future Windows test
	// drive the loops without sleeping for real seconds.
	logf              func(format string, args ...any)
	watchdogInterval  time.Duration
	appIdentityPeriod time.Duration
}

// NewWindowsProvider constructs an unstarted provider.
func NewWindowsProvider() *WindowsProvider {
	return &WindowsProvider{
		logf:              log.Printf,
		watchdogInterval:  windowsWatchdogInterval,
		appIdentityPeriod: windowsAppIdentityInterval,
	}
}

// Honesty reports HonestyGlobal only while the low-level hooks are actually
// installed — at every other moment (install refused by policy or a secure
// desktop, a reinstall in flight, stopped) it reports HonestyBlind, so ADR
// 0010's engine gating refuses to read "no input" as "the user is idle".
//
// A provider that cannot see must not be counted as seeing nothing. That
// sentence is provider_linux.go's, learned the hard way, and it is why the
// honesty bit is a property of the INSTALL rather than of the platform.
//
// Note the deliberate asymmetry with app identity: Honesty is about INPUT
// visibility only (ADR 0019). This provider can be input-blind while still
// naming the foreground app, and Snapshot reports both truthfully.
func (p *WindowsProvider) Honesty() Honesty {
	p.mu.Lock()
	gen := p.gen
	p.mu.Unlock()
	if gen == nil || !gen.hookOK.Load() {
		return HonestyBlind
	}
	return HonestyGlobal
}

// Start spawns the hook thread, the eviction watchdog, and the app-identity
// sampler, and waits (bounded) for the hook install to report.
//
// A failed install is NOT a failed Start: it returns a descriptive error and
// leaves a blind-but-live provider, which is the contract Provider.Start
// documents and what every other platform here does. App identity keeps
// working in that state — it does not depend on the hooks.
func (p *WindowsProvider) Start() error {
	p.mu.Lock()
	if p.gen != nil {
		p.mu.Unlock()
		return errors.New("windows activity provider: already started")
	}
	if p.logf == nil {
		p.logf = log.Printf
	}
	if p.watchdogInterval <= 0 {
		p.watchdogInterval = windowsWatchdogInterval
	}
	if p.appIdentityPeriod <= 0 {
		p.appIdentityPeriod = windowsAppIdentityInterval
	}
	gen := &winGeneration{
		stop:  make(chan struct{}),
		ready: make(chan error, 1),
		done:  make(chan struct{}),
	}
	p.gen = gen
	// The idle clock starts NOW, not at the last event of a previous
	// generation: the paused/stopped stretch was unobserved time, and
	// unobserved time is not idleness.
	p.tally.reset(winNowNanos())
	p.mu.Unlock()

	// Published before the thread installs anything, so no hook can fire
	// into a nil provider.
	winActiveProvider.Store(p)

	gen.wg.Add(3)
	go p.hookThread(gen)
	go p.watchdogLoop(gen)
	go p.appIdentityLoop(gen)

	go func() {
		gen.wg.Wait()
		close(gen.done)
	}()

	select {
	case err := <-gen.ready:
		if err != nil {
			return fmt.Errorf(
				"windows activity provider: could not install the WH_KEYBOARD_LL/WH_MOUSE_LL hooks, so global input is NOT being observed (reporting BLIND; app identity is unaffected). This is usually an enterprise policy, a Group Policy hook restriction, or a secure/locked desktop at the moment of start; `dexel pause` then `dexel resume` re-attempts: %w",
				err,
			)
		}
		p.logf("activity(windows): WH_KEYBOARD_LL + WH_MOUSE_LL installed; counting keystrokes and mouse activity globally, cross-checked against GetLastInputInfo every %s", p.watchdogInterval)
		return nil
	case <-time.After(windowsStartWaitTimeout):
		return fmt.Errorf(
			"windows activity provider: the hook thread did not report within %s; reporting BLIND for now — if the install lands late the provider recovers to global on its own, with no restart",
			windowsStartWaitTimeout,
		)
	}
}

// Stop ends observation and joins every goroutine, PROMPTLY and with a hard
// ceiling.
//
// Prompt is the point (BUG-9, the bug that made `dexel stop` escalate to
// SIGKILL on Linux): the message loop is the clean answer here, because
// PostThreadMessageW(WM_QUIT) wakes a blocked GetMessageW immediately — this
// provider never has the "a blocking read cannot be interrupted" problem the
// evdev provider had to engineer around.
//
// The ceiling exists anyway. If a goroutine has not returned within
// windowsStopWaitTimeout, Stop says so and returns: the generation is already
// detached, winActiveProvider no longer points here, so nothing the stragglers
// do afterwards is observable.
func (p *WindowsProvider) Stop() error {
	p.mu.Lock()
	gen := p.gen
	p.gen = nil
	p.mu.Unlock()
	if gen == nil {
		return nil
	}

	// Stop counting FIRST: a callback that fires from here on finds nil and
	// only chains.
	winActiveProvider.CompareAndSwap(p, nil)
	gen.hookOK.Store(false)
	close(gen.stop)

	deadline := time.NewTimer(windowsStopWaitTimeout)
	defer deadline.Stop()
	retry := time.NewTicker(windowsPostRetryInterval)
	defer retry.Stop()
	for {
		// Re-posted on every pass rather than once. The post can only fail
		// for one legitimate reason — the target thread has no message
		// queue yet — and that reason resolves itself within microseconds,
		// so retrying inside the existing budget is strictly better than
		// treating the first failure as fatal.
		p.postToHookThread(gen, wmQuit)
		select {
		case <-gen.done:
			return nil
		case <-retry.C:
		case <-deadline.C:
			p.logf("activity(windows): the hook thread and/or its watchdog had not returned %s after WM_QUIT — abandoning the wait so shutdown stays bounded (they are detached and count nothing; they end with the process)", windowsStopWaitTimeout)
			return nil
		}
	}
}

// Snapshot returns the current view of activity.
//
// BLIND (hooks not installed) reports a FROZEN idle clock rather than a
// growing one, and no mouse activity, while keeping the keystroke total it
// already earned — the same invariant LinuxProvider.Snapshot documents:
// those presses really were observed, and unobserved time is not idleness.
//
// App identity is reported in BOTH states, which is where this provider
// diverges from LinuxProvider's blind branch. That branch zeroes app
// identity only because the Linux provider has none at all; here the two
// capabilities are genuinely independent (ADR 0019), so a provider whose
// hooks were refused by policy still honestly names the foreground app
// instead of pretending it cannot see one.
func (p *WindowsProvider) Snapshot() Snapshot {
	now := winNowNanos()
	p.mu.Lock()
	gen := p.gen
	identity := p.appIdentity
	p.mu.Unlock()

	keystrokes, mouseActive, idleSeconds := p.tally.read(now)
	if gen == nil || !gen.hookOK.Load() {
		return Snapshot{
			KeystrokeCount:       keystrokes,
			ActiveApp:            identity.ID,
			ActiveAppDisplay:     identity.Display,
			AppIdentityAvailable: identity.Available,
		}
	}
	return Snapshot{
		KeystrokeCount:       keystrokes,
		MouseActive:          mouseActive,
		IdleSeconds:          idleSeconds,
		ActiveApp:            identity.ID,
		ActiveAppDisplay:     identity.Display,
		AppIdentityAvailable: identity.Available,
	}
}

// ---------------------------------------------------------------------------
// The hook thread
// ---------------------------------------------------------------------------

// hookThread owns the two hooks for the whole life of a generation.
//
// runtime.LockOSThread is not optional here, it is the mechanism: a
// low-level hook is bound to the THREAD that installed it, its callback is
// delivered while that thread pumps messages, and UnhookWindowsHookEx must be
// reachable from a thread that still exists. A goroutine that can be migrated
// between OS threads cannot host any of that.
//
// It deliberately never calls runtime.UnlockOSThread. A locked thread whose
// goroutine returns is TERMINATED by the Go runtime, which is exactly what
// should happen to a thread carrying a Win32 message queue and a (possibly
// stale) hook binding — unlocking would instead return it to the pool for
// some unrelated goroutine to inherit.
func (p *WindowsProvider) hookThread(gen *winGeneration) {
	runtime.LockOSThread()
	defer gen.wg.Done()

	procs, err := winProcAddrs()
	if err != nil {
		gen.ready <- fmt.Errorf("resolving user32 entry points: %w", err)
		return
	}

	// Force the message queue into existence BEFORE reporting ready, so a
	// Stop that arrives the instant Start returns cannot lose its WM_QUIT
	// to ERROR_INVALID_THREAD_ID. PeekMessageW is the documented way to do
	// that; its result is irrelevant.
	var probe winMsg
	syscall.SyscallN(procs.peekMessageW, uintptr(unsafe.Pointer(&probe)), 0, 0, 0, pmNoRemove)
	gen.tid.Store(windows.GetCurrentThreadId())

	keyboardHook, mouseHook, err := winInstallHooks(procs)
	if err != nil {
		gen.ready <- err
		return
	}
	gen.hookOK.Store(true)
	gen.ready <- nil

	defer func() {
		gen.hookOK.Store(false)
		winUnhook(procs, keyboardHook)
		winUnhook(procs, mouseHook)
	}()

	for {
		var msg winMsg
		r, _, callErr := syscall.SyscallN(procs.getMessageW, uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		switch int32(r) {
		case 0:
			// WM_QUIT — the ordinary shutdown path.
			return
		case -1:
			// GetMessage failed, which should not be possible with a valid
			// MSG pointer. Say so and go blind rather than spin.
			p.logf("activity(windows): GetMessageW failed (%v) — the hook thread is exiting and this provider is now BLIND; `dexel pause` then `dexel resume` re-installs the hooks", callErr)
			return
		}
		if msg.hwnd != 0 {
			// This thread creates no windows, so a window message here is
			// not ours to translate or dispatch; ignoring it is correct and
			// keeps the loop's syscall surface at one call.
			continue
		}
		if msg.message == wmReinstallHooks {
			gen.hookOK.Store(false)
			winUnhook(procs, keyboardHook)
			winUnhook(procs, mouseHook)
			keyboardHook, mouseHook, err = winInstallHooks(procs)
			if err != nil {
				p.logf("activity(windows): re-installing the evicted low-level hooks FAILED (%v) — this provider is now BLIND and the engine will not claim idle or onBreak from it (ADR 0010); `dexel pause` then `dexel resume` re-attempts", err)
				return
			}
			// The evicted stretch was time we could not observe, so the
			// idle clock restarts here rather than carrying an unobserved
			// gap into the engine.
			p.tally.reset(winNowNanos())
			gen.hookOK.Store(true)
			p.logf("activity(windows): RECOVERED — the low-level hooks had been evicted (GetLastInputInfo saw input while the hooks counted none for %s) and have been re-installed; honesty restored to global, idle clock restarted, %d reinstall(s) this run", time.Duration(winWatchdogStrikes)*p.watchdogInterval, gen.reinstalls.Load())
		}
	}
}

// winInstallHooks installs both low-level hooks, or neither.
//
// Partial success is treated as failure and rolled back on purpose: a
// keyboard-only provider would report MouseActive false forever while
// claiming HonestyGlobal, which reads downstream as "typing but never
// touching the mouse" — a confident wrong answer rather than a missing one.
// dwThreadId is 0 (system-wide) and hMod is 0, which is what a low-level
// hook wants: no DLL is being injected anywhere, so there is no module to
// name.
func winInstallHooks(procs *winProcs) (keyboard, mouse uintptr, err error) {
	keyboard, err = winSetHook(procs, whKeyboardLL, winKeyboardCallback())
	if err != nil {
		return 0, 0, fmt.Errorf("SetWindowsHookExW(WH_KEYBOARD_LL): %w", err)
	}
	mouse, err = winSetHook(procs, whMouseLL, winMouseCallback())
	if err != nil {
		winUnhook(procs, keyboard)
		return 0, 0, fmt.Errorf("SetWindowsHookExW(WH_MOUSE_LL): %w", err)
	}
	return keyboard, mouse, nil
}

func winSetHook(procs *winProcs, idHook int, callback uintptr) (uintptr, error) {
	h, _, callErr := syscall.SyscallN(procs.setWindowsHookExW, uintptr(idHook), callback, 0, 0)
	if h == 0 {
		if callErr == 0 {
			return 0, errors.New("returned NULL with no error code")
		}
		return 0, callErr
	}
	return h, nil
}

func winUnhook(procs *winProcs, hook uintptr) {
	if hook == 0 {
		return
	}
	syscall.SyscallN(procs.unhookWindowsHookEx, hook)
}

// postToHookThread posts one thread message to the hook thread, if it has a
// thread id yet. Failures are intentionally silent: both callers (Stop's
// WM_QUIT and the watchdog's reinstall request) are already bounded and
// retried, and a post that fails because the thread has ended is not an
// error worth a log line.
func (p *WindowsProvider) postToHookThread(gen *winGeneration, message uint32) {
	tid := gen.tid.Load()
	if tid == 0 {
		return
	}
	procs, err := winProcAddrs()
	if err != nil {
		return
	}
	syscall.SyscallN(procs.postThreadMessageW, uintptr(tid), uintptr(message), 0, 0)
}

// ---------------------------------------------------------------------------
// The eviction watchdog
// ---------------------------------------------------------------------------

// watchdogLoop runs the GetLastInputInfo cross-check on an ordinary
// goroutine (nothing about it needs the hook thread) and asks the hook
// thread to reinstall when winWatchdog concludes the hooks were evicted.
//
// It runs for the whole generation, including while the hooks are down: the
// watchdog's own state machine is reset in that case, so the interval during
// which an install was failing cannot be mistaken for an eviction.
func (p *WindowsProvider) watchdogLoop(gen *winGeneration) {
	defer gen.wg.Done()

	ticker := time.NewTicker(p.watchdogInterval)
	defer ticker.Stop()

	var w winWatchdog
	for {
		select {
		case <-gen.stop:
			return
		case <-ticker.C:
		}
		if !gen.hookOK.Load() {
			w.reset()
			continue
		}
		tick, ok := winLastInputTick()
		if w.observe(p.tally.rawEvents.Load(), tick, ok) {
			n := gen.reinstalls.Add(1)
			p.logf("activity(windows): the low-level hooks appear to have been EVICTED — GetLastInputInfo reports input while the hooks counted none across %d consecutive %s checks (Windows silently un-hooks a callback that overruns LowLevelHooksTimeout). Re-installing them; reinstall #%d this run", winWatchdogStrikes, p.watchdogInterval, n)
			p.postToHookThread(gen, wmReinstallHooks)
			w.reset()
		}
	}
}

// winLastInputTick reads GetLastInputInfo's tick count — a content-free
// timestamp of the last input in this session, and the second opinion the
// watchdog needs. It reports ok=false if the call fails, so the watchdog can
// abstain rather than guess.
func winLastInputTick() (uint32, bool) {
	procs, err := winProcAddrs()
	if err != nil {
		return 0, false
	}
	info := winLastInputInfo{cbSize: uint32(unsafe.Sizeof(winLastInputInfo{}))}
	r, _, _ := syscall.SyscallN(procs.getLastInputInfo, uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0, false
	}
	return info.dwTime, true
}

// ---------------------------------------------------------------------------
// App identity
// ---------------------------------------------------------------------------

// appIdentityLoop refreshes the foreground application's identity on its own
// slower cadence, independent of the hooks (it keeps working when the hooks
// were refused, and it stops when the generation does).
func (p *WindowsProvider) appIdentityLoop(gen *winGeneration) {
	defer gen.wg.Done()

	sample := func() {
		name, observed := winForegroundAppName()
		if observed {
			p.appEverObserved.Store(true)
		}
		// ADR 0019's availability bit, Windows edition. `observed` answers
		// "did THIS query name an app", which is not the same question as
		// "can this provider see app identity here at all" — and the
		// difference matters, because GetForegroundWindow legitimately
		// returns NULL on a bare desktop AND permanently returns NULL in a
		// process with no interactive desktop to look at. Reporting the
		// first case as unavailable would be a lie; reporting the second as
		// "nothing is frontmost" is the exact conflation ADR 0019 was
		// written to kill.
		//
		// So availability is STICKY: false until the mechanism has answered
		// at least once, true from then on. A process that can see the
		// desktop learns it within one sample and thereafter reports empty
		// answers as the real "nothing frontmost" they are; a process that
		// cannot see one never claims otherwise.
		identity := NewAppIdentity(name, p.appEverObserved.Load())
		p.mu.Lock()
		p.appIdentity = identity
		p.mu.Unlock()
	}

	// Sample immediately so Snapshot never has to serve a first window of
	// "unknown" right after Start (same reasoning as darwinAppIdentityInterval).
	sample()

	ticker := time.NewTicker(p.appIdentityPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-gen.stop:
			return
		case <-ticker.C:
			sample()
		}
	}
}

// winForegroundAppName names the process that owns the foreground window,
// and nothing else.
//
// ============================ HARD BOUNDARY ============================
// GetForegroundWindow gives an HWND. From an HWND, exactly two things are
// reachable: the owning process (GetWindowThreadProcessId, which is what ADR
// 0009 allows — application identity) and the window TEXT (GetWindowTextW,
// which is the document you have open, the URL of your tab, the subject of
// the mail you are writing). This function takes the first and must never
// take the second. GetWindowTextW does not appear anywhere in this file, and
// windows_signals_test.go asserts that from the Linux CI box, because the
// forbidden thing is the CALL, not the intention.
//
// The image PATH that QueryFullProcessImageName returns is narrowed to its
// base name immediately — see windowsAppNameFromImagePath's own HARD
// BOUNDARY for why the directories are content and must not travel.
// =======================================================================
//
// observed=false means this query could not name an app. That is NOT the
// same as "no app is in front"; the caller (appIdentityLoop) is what turns
// the distinction into ADR 0019's availability bit.
func winForegroundAppName() (name string, observed bool) {
	hwnd := windows.GetForegroundWindow()
	if hwnd == 0 {
		// Nothing has focus, or this process has no interactive desktop to
		// ask. Both are "cannot name an app right now".
		return "", false
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil || pid == 0 {
		return "", false
	}
	// PROCESS_QUERY_LIMITED_INFORMATION is the narrowest right that answers
	// this question, and unlike PROCESS_QUERY_INFORMATION it is granted for
	// processes at a higher integrity level — so a focused elevated app is
	// still nameable. It cannot read memory, handles, or anything else.
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		// A protected process (anti-cheat, some system processes) can
		// refuse even this. We know something is in front and cannot name
		// it; that is an empty identity, not a capability failure.
		return "", false
	}
	defer func() { _ = windows.CloseHandle(h) }()

	image, err := winProcessImagePath(h)
	if err != nil || image == "" {
		return "", false
	}
	// image is narrowed here and never logged, stored, or returned.
	return windowsAppNameFromImagePath(image), true
}

// winProcessImagePath reads a process's full image path, starting with a
// 1KiB buffer and retrying once at MAX_LONG_PATH.
//
// The two-step exists so the common case does not allocate 64KiB every
// windowsAppIdentityInterval: a normal Program Files path is well under
// 1KiB, and only a genuinely long path pays for the retry.
func winProcessImagePath(process windows.Handle) (string, error) {
	for _, n := range [2]uint32{1024, windows.MAX_LONG_PATH} {
		buf := make([]uint16, n)
		size := n
		err := windows.QueryFullProcessImageName(process, 0, &buf[0], &size)
		if err == nil {
			return windows.UTF16ToString(buf[:size]), nil
		}
		if err != windows.ERROR_INSUFFICIENT_BUFFER {
			return "", err
		}
	}
	return "", errors.New("QueryFullProcessImageName: path longer than MAX_LONG_PATH")
}
