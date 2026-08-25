//go:build linux

package activity

import (
	"testing"
	"time"
)

// This file exists because "the Windows provider coalesces the same way the
// Linux one does" is a claim about TWO implementations, and the only place
// both of them compile is this repo's Linux CI runner (provider_linux.go is
// //go:build linux; windows_signals.go is deliberately untagged precisely so
// this comparison is possible).
//
// It drives LinuxProvider.handleEvent and winTally with the same synthetic
// input and asserts they agree. Real time rather than injected time, because
// handleEvent reads time.Now() itself — so the sleeps are the price of
// comparing the real thing rather than a paraphrase of it. They total well
// under a second.

// parityBurst is enough events to prove coalescing without being slow; a
// hundred iterations of two function calls take microseconds, so the whole
// burst lands inside one MouseSampleInterval window on any machine that can
// run this suite at all.
const parityBurst = 100

func winParityKeystrokes(t *testing.T, lp *LinuxProvider) uint64 {
	t.Helper()
	lp.mu.Lock()
	defer lp.mu.Unlock()
	return lp.keystrokeCount
}

// TestWindowsAndLinuxCoalesceKeystrokesIdentically is the anti-mash parity
// check: ADR 0005's clamp must mean the same number on both platforms, or the
// same typing earns different amounts of Dev Cash depending on which OS you
// are on — and MouseSampleInterval's whole reason for being one shared
// constant (see its doc comment) would have been defeated by two
// implementations that merely reference it.
func TestWindowsAndLinuxCoalesceKeystrokesIdentically(t *testing.T) {
	lp := &LinuxProvider{}
	var wt winTally
	wt.reset(winNowNanos())

	// Three bursts, one per window, separated by a full window plus slack.
	for burst := 1; burst <= 3; burst++ {
		for i := 0; i < parityBurst; i++ {
			// evdev: EV_KEY, an ordinary letter code, value 1 (press).
			lp.handleEvent(evKey, 30, keyPressValue)
			wt.key(winNowNanos())
		}
		linuxCount := winParityKeystrokes(t, lp)
		winCount, _, _ := wt.read(winNowNanos())
		if linuxCount != winCount {
			t.Fatalf("after burst %d: linux counted %d keystrokes, windows counted %d — the two providers disagree about ADR 0005's clamp", burst, linuxCount, winCount)
		}
		if int(winCount) != burst {
			t.Fatalf("after burst %d both providers counted %d keystrokes, want %d (one per %s window)", burst, winCount, burst, MouseSampleInterval)
		}
		time.Sleep(MouseSampleInterval + 20*time.Millisecond)
	}
}

// TestWindowsAndLinuxAgreeOnMouseRecency: MouseActive is a coalesced boolean
// on both platforms with the same one-window lifetime.
func TestWindowsAndLinuxAgreeOnMouseRecency(t *testing.T) {
	lp := &LinuxProvider{}
	var wt winTally
	wt.reset(winNowNanos())

	linuxActive := func() bool {
		lp.mu.Lock()
		defer lp.mu.Unlock()
		return time.Now().Before(lp.mouseActiveUntil)
	}
	winActive := func() bool {
		_, active, _ := wt.read(winNowNanos())
		return active
	}

	if linuxActive() != winActive() {
		t.Fatalf("before any input: linux MouseActive=%v, windows MouseActive=%v", linuxActive(), winActive())
	}
	for i := 0; i < parityBurst; i++ {
		lp.handleEvent(evRel, 0, 1) // REL_X motion
		wt.mouse(winNowNanos())
	}
	if !linuxActive() || !winActive() {
		t.Fatalf("right after mouse motion: linux MouseActive=%v, windows MouseActive=%v — both must be true", linuxActive(), winActive())
	}
	// Neither may have counted a keystroke for it.
	if got := winParityKeystrokes(t, lp); got != 0 {
		t.Errorf("linux counted %d keystrokes from mouse motion", got)
	}
	if got, _, _ := wt.read(winNowNanos()); got != 0 {
		t.Errorf("windows counted %d keystrokes from mouse motion", got)
	}

	time.Sleep(MouseSampleInterval + 20*time.Millisecond)
	if linuxActive() || winActive() {
		t.Fatalf("a window after the last mouse event: linux MouseActive=%v, windows MouseActive=%v — both must have expired", linuxActive(), winActive())
	}
}

// TestWindowsCountsMouseButtonsAsMouseActivityUnlikeLinux pins the ONE
// deliberate divergence between the two providers, so it is a recorded
// decision rather than a discrepancy someone later "fixes" in the wrong
// direction.
//
// On Linux a mouse button arrives as EV_KEY with a BTN_* code (0x110+) and is
// dropped by both branches of handleEvent — too high for the keystroke ceiling
// (`code < keyCodeCeiling`), and not EV_REL. That is an artifact of evdev
// sharing one code space between keys and buttons, not a decision anybody
// made. Windows hands buttons to the MOUSE hook unambiguously, so counting
// them as mouse activity is the honest reading of the same physical act.
//
// It cannot inflate earning: MouseActive is a coalesced boolean, never a
// count. What it does change is that a session spent clicking (a designer in
// Figma, a reviewer in a browser) is not misread as idle on Windows.
func TestWindowsCountsMouseButtonsAsMouseActivityUnlikeLinux(t *testing.T) {
	lp := &LinuxProvider{}
	var wt winTally
	wt.reset(winNowNanos())

	const btnLeft = 0x110 // linux/input-event-codes.h BTN_LEFT
	lp.handleEvent(evKey, btnLeft, keyPressValue)
	wt.mouse(winNowNanos()) // what winMouseHookProc does for WM_LBUTTONDOWN

	lp.mu.Lock()
	linuxActive := time.Now().Before(lp.mouseActiveUntil)
	linuxKeys := lp.keystrokeCount
	lp.mu.Unlock()
	winKeys, winMouse, _ := wt.read(winNowNanos())

	if linuxActive {
		t.Error("provider_linux now reports MouseActive for a mouse button; this test's premise (and the comment in winMouseHookProc) needs updating")
	}
	if !winMouse {
		t.Error("provider_windows does not report MouseActive for a mouse button — a click IS mouse activity there")
	}
	if linuxKeys != 0 || winKeys != 0 {
		t.Errorf("a mouse button was counted as a KEYSTROKE (linux=%d, windows=%d) — that would inflate earning on both platforms", linuxKeys, winKeys)
	}
}
