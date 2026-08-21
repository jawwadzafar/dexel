//go:build linux

package main

import "github.com/jawwadzafar/dexel/app/internal/activity"

func platformProvider() activity.Provider {
	return activity.NewLinuxProvider()
}

const platformProviderName = "linux (/dev/input/event* raw reader)"
