//go:build !darwin && !linux

package main

import "github.com/jawwadzafar/dev-companion/app/internal/activity"

// No native capture strategy exists for this platform (ADR 0011 only
// covers macOS + Linux). Degrade to the fake provider rather than refuse
// to run — a blind fake still honestly reports HonestyGlobal/whatever it's
// constructed with, so the mood rules behave the same as a real blind
// provider would.
func platformProvider() activity.Provider {
	return activity.NewFakeProviderFromEnv()
}

const platformProviderName = "fake (no native provider for this platform)"
