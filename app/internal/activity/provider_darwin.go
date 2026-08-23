//go:build darwin

// Darwin activity provider — ADR 0010's permissionless capture path.
//
// This is the ONLY file in this package that touches Cgo/Objective-C
// (house rule: keep it minimal and quarantined in one file so the rest of
// the module stays plain, cross-compiling, and testable from Linux).
//
// Why permissionless: CGEventSourceSecondsSinceLastEventType reads a
// system-maintained "seconds since last event of this type" counter from
// the HID system state. Unlike a CGEventTap it needs no Accessibility
// prompt, no run loop, and never sees the event's content — only a
// timestamp delta. Polling it on a 50ms ticker (ADR 0010 decision 1) is
// how we turn "seconds since" into "did a new event just happen."
//
// # Why app identity does NOT come from NSWorkspace (ADR 0019)
//
// This file used to read [[NSWorkspace sharedWorkspace] frontmostApplication].
// That was measured to be broken in every long-lived process here, not just
// under launchd: NSWorkspace's running-application state is a CACHE that
// AppKit refreshes from LaunchServices notifications delivered on the
// process's MAIN RUN LOOP. A Go server never runs a CFRunLoop — main blocks
// in http.Serve and sampling happens on a goroutine — so the cache is
// populated by the first query and then never updated again.
//
// Measured on this machine with a probe of exactly this shape (no run loop,
// polling from a secondary thread), focus changed four times via osascript:
// frontmostApplication returned the SAME app for all 24 samples, and
// runningApplications.count never moved off 91, while CGWindowList tracked
// every switch. Under the real LaunchAgent the stock binary froze on
// "In Finder" across three focus changes. The production symptom (an empty
// app forever) is the same freeze with a different first sample: the agent
// starts at LOGIN, when nothing is frontmost yet, so the frozen value is nil.
// Foreground mode was never actually working either — it froze on whatever
// launched it, which happened to be the terminal, so it looked right.
//
// A frozen value is worse than no value: it is a confident wrong answer, the
// exact ADR 0010 failure mode. So NSWorkspace is not used as a fallback
// either; CGWindowListCopyWindowInfo is the sole source, and when it cannot
// answer we say so (Snapshot.AppIdentityAvailable) rather than substituting
// a stale guess.
package activity

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework CoreGraphics

#import <Cocoa/Cocoa.h>
#import <CoreGraphics/CoreGraphics.h>

// dc_seconds_since wraps CGEventSourceSecondsSinceLastEventType for the HID
// system state — global, permissionless, content-free (a timestamp delta,
// never the event itself). `type` is a raw CGEventType value (see the Go
// cgEvent* constants below); passed as int across the cgo boundary because
// cgo cannot express CGEventType literals in a Go const block.
static double dc_seconds_since(int type) {
    double s = CGEventSourceSecondsSinceLastEventType(kCGEventSourceStateHIDSystemState, (CGEventType)type);
    if (s < 0) {
        s = 0;
    }
    return s;
}

// dc_frontmost_owner_name returns ONLY the APPLICATION NAME that owns the
// frontmost on-screen window (e.g. "Visual Studio Code"). Caller must free()
// the returned pointer. *available is set to 0 if app identity could not be
// observed at all in this process context, 1 if it could (in which case an
// empty return means "looked, nothing is frontmost").
//
// ============================ HARD BOUNDARY ============================
// This function reads kCGWindowOwnerName and NOTHING ELSE from each window
// dictionary. It must NEVER read kCGWindowName.
//
// kCGWindowName is the window TITLE — "invoice-final.pdf", the URL of the
// tab you have open, the name of the file you are editing. It is exactly the
// content ADR 0002/0009 forbid from crossing this boundary, and reading it
// additionally requires a Screen Recording TCC grant, which would destroy
// ADR 0010's permissionless property. Both reasons are independently fatal.
// Measured on this machine: under a LaunchAgent the returned dictionaries do
// not even CONTAIN the kCGWindowName key (no Screen Recording grant), while
// kCGWindowOwnerName is present and correct — the owner name is the
// permissionless half of this API by design.
//
// If you are here to add "just the document name too": no. Snapshot's
// allow-list test (content_free_test.go) will reject the field it would have
// to travel in, and that test is the contract, not a formality.
// =======================================================================
static char *dc_frontmost_owner_name(int *available) {
    @autoreleasepool {
        // kCGWindowListOptionOnScreenOnly returns on-screen windows in
        // front-to-back z-order, so the FIRST match walking forward is the
        // frontmost one. kCGWindowListExcludeDesktopElements drops the
        // desktop/wallpaper pseudo-windows owned by Finder and Dock, which
        // would otherwise sit in the list and be mistaken for real windows.
        CFArrayRef windows = CGWindowListCopyWindowInfo(
            kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements,
            kCGNullWindowID);
        if (!windows) {
            // No window server to ask (e.g. a process with no GUI session at
            // all). Report the capability as unavailable rather than
            // returning "" — "" means "nothing is frontmost", which is a
            // claim we have not earned the right to make.
            *available = 0;
            return strdup("");
        }
        *available = 1;

        char *result = strdup("");
        CFIndex count = CFArrayGetCount(windows);
        for (CFIndex i = 0; i < count; i++) {
            CFDictionaryRef w = CFArrayGetValueAtIndex(windows, i);

            // Layer 0 is the normal application-window layer. Everything
            // above it is system chrome that is permanently "in front" and
            // never what the human is working in: the menu bar and Control
            // Center sit at 25, the Dock at 20, status items and tooltips
            // higher still. Without this filter the answer would be
            // "Control Center" essentially always (observed: the first six
            // entries of a real 44-window list were all layer 25).
            CFNumberRef layerRef = CFDictionaryGetValue(w, kCGWindowLayer);
            int layer = -1;
            if (!layerRef || !CFNumberGetValue(layerRef, kCFNumberIntType, &layer) || layer != 0) {
                continue;
            }

            // Fully transparent windows are real entries in this list but are
            // not what anyone is looking at — several apps park invisible
            // helper windows at layer 0. Skip them so they cannot shadow the
            // window actually in front.
            CFNumberRef alphaRef = CFDictionaryGetValue(w, kCGWindowAlpha);
            double alpha = 1.0;
            if (alphaRef) {
                CFNumberGetValue(alphaRef, kCFNumberDoubleType, &alpha);
            }
            if (alpha <= 0.0) {
                continue;
            }

            CFStringRef owner = CFDictionaryGetValue(w, kCGWindowOwnerName);
            if (!owner) {
                continue;
            }
            const char *utf8 = [(__bridge NSString *)owner UTF8String];
            if (!utf8 || utf8[0] == '\0') {
                continue;
            }
            free(result);
            result = strdup(utf8);
            break;
        }
        CFRelease(windows);
        return result;
    }
}
*/
import "C"

import (
	"sync"
	"time"
	"unsafe"
)

// CGEventType values sampled per ADR 0011 (kCGEventKeyDown=10,
// kCGEventMouseMoved=5, kCGEventLeftMouseDragged=6, kCGEventScrollWheel=22).
const (
	cgEventMouseMoved  = 5
	cgEventLeftDragged = 6
	cgEventKeyDown     = 10
	cgEventScrollWheel = 22
)

const (
	// darwinPollInterval: how often we ask CoreGraphics "how long since".
	darwinPollInterval = 50 * time.Millisecond
	// darwinSampleInterval: alias for the package-wide anti-mash
	// coalescing window (MouseSampleInterval, defined in provider.go, same
	// package — no import needed even though this file is build-tagged
	// darwin-only). See that constant's doc comment for why this and the
	// linux/engine copies were hoisted into one place instead of each
	// declaring their own 100ms.
	darwinSampleInterval = MouseSampleInterval
	// darwinAppIdentityInterval: how often the frontmost app is re-read.
	// Deliberately much slower than darwinPollInterval because the two
	// queries cost wildly different amounts: the idle timers are a scalar
	// read, while CGWindowListCopyWindowInfo builds a CFArray of a
	// dictionary per on-screen window (44 on a normal desktop here) that
	// we then walk and release. Doing that 20x/second to answer a question
	// the engine only asks once per tick (main.go's 1s cadence) would burn
	// CPU in a process whose entire pitch is being invisible. 500ms still
	// resolves app switches finer than the engine can consume them.
	darwinAppIdentityInterval = 500 * time.Millisecond
)

// DarwinProvider polls CoreGraphics HID idle timers on a 50ms ticker
// goroutine. No permission is requested or required.
type DarwinProvider struct {
	stopCh chan struct{}
	doneCh chan struct{}

	mu               sync.Mutex
	keystrokeCount   uint64
	lastKeyTick      time.Time
	lastMouseTick    time.Time
	mouseActiveUntil time.Time
	idleSeconds      float64

	// appIdentity is the last app-identity observation, re-sampled every
	// darwinAppIdentityInterval rather than on every 50ms poll tick.
	// lastAppSampleAt is zero until the first sample so Snapshot never has
	// to serve a 500ms window of "unknown" right after Start.
	appIdentity     AppIdentity
	lastAppSampleAt time.Time
}

// NewDarwinProvider constructs an unstarted provider.
func NewDarwinProvider() *DarwinProvider {
	return &DarwinProvider{}
}

// Honesty: this provider is global by construction — it reads HID system
// state, not window-focus state — so it is never blind.
//
// This stays HonestyGlobal even when app identity is unavailable: Honesty is
// the engine's gate for its OnBreak claim (ADR 0010) and the idle timers this
// provider reads are genuinely global whether or not a window server will
// tell us which app is in front. App-identity availability travels
// separately, on Snapshot.AppIdentityAvailable.
func (p *DarwinProvider) Honesty() Honesty { return HonestyGlobal }

// Start launches the polling goroutine. Always succeeds: there is no
// permission to fail on (that is the entire point of ADR 0010's approach).
func (p *DarwinProvider) Start() error {
	p.mu.Lock()
	already := p.stopCh != nil
	p.mu.Unlock()
	if already {
		return nil
	}
	p.mu.Lock()
	p.stopCh = make(chan struct{})
	p.doneCh = make(chan struct{})
	stop, done := p.stopCh, p.doneCh
	p.mu.Unlock()
	go p.pollLoop(stop, done)
	return nil
}

// Stop halts the polling goroutine and waits for it to exit.
func (p *DarwinProvider) Stop() error {
	p.mu.Lock()
	stop := p.stopCh
	done := p.doneCh
	p.stopCh = nil
	p.doneCh = nil
	p.mu.Unlock()
	if stop == nil {
		return nil
	}
	close(stop)
	<-done
	return nil
}

func (p *DarwinProvider) pollLoop(stop, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(darwinPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			p.sample()
		}
	}
}

func (p *DarwinProvider) sample() {
	now := time.Now()

	keyIdle := float64(C.dc_seconds_since(C.int(cgEventKeyDown)))
	mouseIdle := minf(
		float64(C.dc_seconds_since(C.int(cgEventMouseMoved))),
		float64(C.dc_seconds_since(C.int(cgEventLeftDragged))),
		float64(C.dc_seconds_since(C.int(cgEventScrollWheel))),
	)
	overallIdle := minf(keyIdle, mouseIdle)

	// App identity is sampled on its own, slower cadence (see
	// darwinAppIdentityInterval). Read the "is it due" state under the lock
	// so two concurrent samples can't both decide to do the expensive work,
	// then release it for the cgo call itself — holding the mutex across a
	// CGWindowList round-trip would stall every Snapshot() caller.
	p.mu.Lock()
	appDue := p.lastAppSampleAt.IsZero() || now.Sub(p.lastAppSampleAt) >= darwinAppIdentityInterval
	if appDue {
		p.lastAppSampleAt = now
	}
	p.mu.Unlock()

	var freshIdentity AppIdentity
	if appDue {
		// available is an out-parameter distinguishing "no window server to
		// ask" from "asked, nothing is frontmost" — see AppIdentity.Available.
		var available C.int
		cName := C.dc_frontmost_owner_name(&available)
		ownerName := C.GoString(cName)
		C.free(unsafe.Pointer(cName))
		// ownerName is an APPLICATION name (kCGWindowOwnerName) and is the
		// only string that crosses out of the cgo boundary in this file. No
		// window title is read, so none can be passed here.
		freshIdentity = NewAppIdentity(ownerName, available != 0)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if appDue {
		p.appIdentity = freshIdentity
	}

	// Anti-mash coalescing: "idle < one poll tick" means a fresh event
	// landed since we last polled; only actually COUNT it if the sample
	// interval has elapsed since the last count. This caps both signals at
	// 1/darwinSampleInterval no matter how fast the human (or a script)
	// drives the input — the exact anti-mashing property ADR 0005 requires,
	// ported to a polling (rather than event-stream) capture strategy.
	pollWindow := darwinPollInterval.Seconds()
	if keyIdle < pollWindow && now.Sub(p.lastKeyTick) >= darwinSampleInterval {
		p.keystrokeCount++
		p.lastKeyTick = now
	}
	if mouseIdle < pollWindow && now.Sub(p.lastMouseTick) >= darwinSampleInterval {
		p.lastMouseTick = now
		p.mouseActiveUntil = now.Add(darwinSampleInterval)
	}

	p.idleSeconds = overallIdle
}

// Snapshot returns the current view of activity.
func (p *DarwinProvider) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Snapshot{
		KeystrokeCount:       p.keystrokeCount,
		MouseActive:          time.Now().Before(p.mouseActiveUntil),
		IdleSeconds:          p.idleSeconds,
		ActiveApp:            p.appIdentity.ID,
		ActiveAppDisplay:     p.appIdentity.Display,
		AppIdentityAvailable: p.appIdentity.Available,
	}
}

func minf(vals ...float64) float64 {
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}
