//go:build !darwin && !linux

package main

import "github.com/jawwadzafar/dev-companion/app/internal/activity"

// No native capture strategy exists for this platform (ADR 0011 only
// covers macOS + Linux). Degrade to a permanently-blind, zero-signal fake
// provider rather than refuse to run — but critically NOT
// NewFakeProviderFromEnv's HonestyGlobal demo script, which would
// fabricate a constant "Coding in VS Code" signal (and DEVCOMPANION_FAKE_
// SCRIPT-driven typing/mouse/idle activity) on a platform this process has
// no actual visibility into. HonestyBlind is the honest answer here — ADR
// 0010's engine then refuses to ever claim onBreak (or anything else)
// from it, exactly as a real capture failure would degrade.
func platformProvider() activity.Provider {
	return activity.NewFakeProvider(nil, activity.HonestyBlind).WithActiveApp("", "")
}

const platformProviderName = "fake (no native provider for this platform; blind, zero-signal)"
