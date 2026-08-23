//go:build linux

// Linux activity provider: a best-effort raw reader of /dev/input/event*
// (evdev) nodes. Counts only — no cgo needed, no keycodes retained, no app
// identity (focus detection is compositor-specific under Wayland; ADR 0009
// notes the watcher should degrade to a generic label rather than guess, so
// this provider simply never sets ActiveApp).
package activity

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Linux input_event layout (linux/input.h) on a 64-bit kernel: a 16-byte
// timeval (two 8-byte kernel longs, time64 ABI) followed by u16 type, u16
// code, s32 value — 24 bytes total. We only ever read type/code/value; the
// timestamp bytes are skipped.
const inputEventSize = 24

// evdev event type/value constants we care about (linux/input-event-codes.h).
const (
	evKey          = 0x01
	evRel          = 0x02
	keyPressValue  = 1     // EV_KEY value==1 is press; 0 is release, 2 is autorepeat
	keyCodeCeiling = 0x100 // mirrors the Rust evdev provider: standard key codes are < 0x100
)

// linuxSampleInterval: alias for the package-wide anti-mash coalescing
// window (MouseSampleInterval, defined in provider.go) — see that
// constant's doc comment for why this and the darwin/engine copies were
// hoisted into one place instead of each declaring their own 100ms.
const linuxSampleInterval = MouseSampleInterval

// LinuxProvider reads raw evdev nodes directly (no cgo, no library). If the
// process cannot open any /dev/input/event* node (the 'input' group is
// commonly required and often absent), it degrades to a blind, all-zero
// provider rather than failing to start — Start returns a descriptive error
// so the caller can surface it, but the provider still behaves safely.
type LinuxProvider struct {
	mu               sync.Mutex
	keystrokeCount   uint64
	lastKeyTick      time.Time
	lastMouseTick    time.Time
	mouseActiveUntil time.Time
	lastAnyInput     time.Time
	blind            bool

	devices []*os.File
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// NewLinuxProvider constructs an unstarted provider.
func NewLinuxProvider() *LinuxProvider {
	return &LinuxProvider{}
}

// Honesty reports HonestyBlind if no input device could be opened — the
// engine must then never claim OnBreak from this provider's IdleSeconds.
func (p *LinuxProvider) Honesty() Honesty {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.blind {
		return HonestyBlind
	}
	return HonestyGlobal
}

// Start opens every readable /dev/input/event* node and begins reading raw
// events from each on its own goroutine. Devices that fail to open (most
// commonly EACCES — the calling user isn't in the 'input' group) are
// skipped individually; only a total failure to open ANY device degrades
// the provider to blind.
func (p *LinuxProvider) Start() error {
	paths, _ := filepath.Glob("/dev/input/event*")

	var opened []*os.File
	var openErrs []error
	for _, path := range paths {
		f, err := os.OpenFile(path, os.O_RDONLY, 0)
		if err != nil {
			openErrs = append(openErrs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		opened = append(opened, f)
	}

	p.mu.Lock()
	p.lastAnyInput = time.Now()
	p.mu.Unlock()

	if len(opened) == 0 {
		p.mu.Lock()
		p.blind = true
		p.mu.Unlock()
		return fmt.Errorf(
			"no readable /dev/input devices (add your user to the 'input' group, or run with access to input devices): %w",
			errors.Join(openErrs...),
		)
	}

	p.mu.Lock()
	p.devices = opened
	p.stopCh = make(chan struct{})
	stop := p.stopCh
	p.mu.Unlock()

	for _, f := range opened {
		p.wg.Add(1)
		go p.readLoop(f, stop)
	}
	return nil
}

func (p *LinuxProvider) readLoop(f *os.File, stop chan struct{}) {
	defer p.wg.Done()
	buf := make([]byte, inputEventSize)
	for {
		select {
		case <-stop:
			return
		default:
		}
		n, err := f.Read(buf)
		if err != nil {
			// Device closed (Stop) or genuinely gone (e.g. unplugged
			// device) — either way this reader is done.
			return
		}
		if n < inputEventSize {
			continue
		}
		typ := binary.LittleEndian.Uint16(buf[16:18])
		code := binary.LittleEndian.Uint16(buf[18:20])
		value := int32(binary.LittleEndian.Uint32(buf[20:24]))
		p.handleEvent(typ, code, value)
	}
}

func (p *LinuxProvider) handleEvent(typ, code uint16, value int32) {
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastAnyInput = now
	switch {
	case typ == evKey && value == keyPressValue && code < keyCodeCeiling:
		if now.Sub(p.lastKeyTick) >= linuxSampleInterval {
			p.keystrokeCount++
			p.lastKeyTick = now
		}
	case typ == evRel:
		if now.Sub(p.lastMouseTick) >= linuxSampleInterval {
			p.lastMouseTick = now
			p.mouseActiveUntil = now.Add(linuxSampleInterval)
		}
	}
}

// Stop closes every opened device and waits for its reader goroutine to
// exit. Note: closing a fd mid-blocking-read is a known rough edge for
// plain os.File on Linux (unlike network fds, there is no netpoller
// integration for character devices) — in the worst case a reader goroutine
// blocks until the next event arrives on that device before it notices the
// stop signal. Acceptable for a best-effort provider; see package doc.
func (p *LinuxProvider) Stop() error {
	p.mu.Lock()
	stop := p.stopCh
	devices := p.devices
	p.stopCh = nil
	p.devices = nil
	p.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	for _, f := range devices {
		_ = f.Close()
	}
	p.wg.Wait()
	return nil
}

// Snapshot returns the current view of activity. A blind provider always
// returns the zero Snapshot — no signal it cannot honestly back.
func (p *LinuxProvider) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.blind {
		return Snapshot{}
	}
	return Snapshot{
		KeystrokeCount: p.keystrokeCount,
		MouseActive:    time.Now().Before(p.mouseActiveUntil),
		IdleSeconds:    time.Since(p.lastAnyInput).Seconds(),
		// ActiveApp / ActiveAppDisplay intentionally left "": focus
		// detection is compositor-specific (X11 vs. the many Wayland
		// compositors) and out of scope here; we report counts honestly
		// rather than guess an app identity from /proc.
		//
		// AppIdentityAvailable is therefore false, and that is now the
		// SAID part rather than the implied part: an empty ActiveApp used
		// to be this provider's way of expressing "I have no focus source"
		// and also what a working provider returns for "nothing is
		// frontmost", so downstream could not tell them apart. This
		// provider is not looking at a bare desktop; it cannot look at all.
		AppIdentityAvailable: false,
	}
}
