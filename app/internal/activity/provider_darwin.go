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

// dc_frontmost_app returns ONLY the frontmost application's localized
// display name (e.g. "Visual Studio Code") — never a window title,
// document name, or URL; NSWorkspace does not expose those here and this
// function does not ask for them. Caller must free() the returned pointer.
static char *dc_frontmost_app(void) {
    @autoreleasepool {
        NSRunningApplication *app = [[NSWorkspace sharedWorkspace] frontmostApplication];
        if (!app || !app.localizedName) {
            return strdup("");
        }
        return strdup([app.localizedName UTF8String]);
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
	// darwinSampleInterval: the anti-mash coalescing window (ADR 0005's
	// MOUSE_SAMPLE_SECS, applied uniformly to both signals per the task
	// brief — "mirror the Rust calibration: these caps are load-bearing
	// anti-mashing values"). At most one keystroke and one mouse-active
	// signal are counted per this window, however fast the input arrives.
	darwinSampleInterval = 100 * time.Millisecond
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
	activeApp        string
	activeAppDisplay string
}

// NewDarwinProvider constructs an unstarted provider.
func NewDarwinProvider() *DarwinProvider {
	return &DarwinProvider{}
}

// Honesty: this provider is global by construction — it reads HID system
// state, not window-focus state — so it is never blind.
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

	cName := C.dc_frontmost_app()
	appRaw := C.GoString(cName)
	C.free(unsafe.Pointer(cName))
	sanitized := SanitizeAppID(appRaw)

	p.mu.Lock()
	defer p.mu.Unlock()

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
	p.activeApp = sanitized
	p.activeAppDisplay = FriendlyName(sanitized)
}

// Snapshot returns the current view of activity.
func (p *DarwinProvider) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Snapshot{
		KeystrokeCount:   p.keystrokeCount,
		MouseActive:      time.Now().Before(p.mouseActiveUntil),
		IdleSeconds:      p.idleSeconds,
		ActiveApp:        p.activeApp,
		ActiveAppDisplay: p.activeAppDisplay,
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
