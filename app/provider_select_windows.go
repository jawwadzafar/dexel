//go:build windows

package main

import "github.com/jawwadzafar/dexel/app/internal/activity"

// platformProvider returns this OS's native activity capture strategy.
// Split into one file per GOOS (this house rule mirrors the activity
// package's own build tags) so main.go never references a symbol that
// doesn't exist in a non-windows build.
//
// This file is what ended the "blind on Windows" era (ADR 0021): until it
// existed, Windows fell through to provider_select_other.go's deliberately
// blind, zero-signal provider and nothing a Windows user typed ever accrued.
// The real provider is WH_KEYBOARD_LL + WH_MOUSE_LL low-level hooks on a
// dedicated OS thread — pure syscalls, no cgo, so the cross-compile matrix
// is untouched.
//
// It still degrades honestly: if the hook install is refused (enterprise
// policy, a secure desktop) the provider reports HonestyBlind rather than
// pretending to see, exactly as the fake provider did — the difference is
// that now that is the exception and not the only outcome.
func platformProvider() activity.Provider {
	return activity.NewWindowsProvider()
}

const platformProviderName = "windows (WH_KEYBOARD_LL + WH_MOUSE_LL low-level hooks)"
