//go:build !darwin && !linux && !windows

package main

import "github.com/jawwadzafar/dexel/app/internal/activity"

// No native capture strategy exists for this platform. macOS, Linux and
// Windows each have a real provider now (provider_select_darwin.go,
// provider_select_linux.go, provider_select_windows.go — the last of those
// added by ADR 0021); everything else Go can target lands here.
//
// Degrade to a permanently-blind, zero-signal fake provider rather than
// refuse to run — but critically NOT NewFakeProviderFromEnv's HonestyGlobal
// demo script, which would fabricate a constant "Coding in VS Code" signal
// (and DEXEL_FAKE_SCRIPT-driven typing/mouse/idle activity) on a platform
// this process has no actual visibility into. HonestyBlind is the honest
// answer here — ADR 0010's engine then refuses to ever claim onBreak (or
// anything else) from it, exactly as a real capture failure would degrade.
//
// This file is deliberately kept rather than deleted along with the last
// tier-2 gap: "some OS Go supports that we have never thought about" is a
// permanent category (a BSD, Solaris, plan9, js/wasm), and a build that
// silently reused another platform's provider selection would be worse than
// one that says it cannot see.
func platformProvider() activity.Provider {
	return activity.NewFakeProvider(nil, activity.HonestyBlind).WithActiveApp("", "")
}

const platformProviderName = "fake (no native provider for this platform; blind, zero-signal)"
