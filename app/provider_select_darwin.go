//go:build darwin

package main

import "github.com/jawwadzafar/dev-companion/app/internal/activity"

// platformProvider returns this OS's native activity capture strategy.
// Split into one file per GOOS (this house rule mirrors the activity
// package's own build tags) so main.go never references a symbol that
// doesn't exist in a non-darwin build.
func platformProvider() activity.Provider {
	return activity.NewDarwinProvider()
}

const platformProviderName = "darwin (permissionless CGEventSource polling)"
