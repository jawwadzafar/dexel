//go:build !darwin && !windows

package paths

// relocateHook is a no-op on Linux (and every other non-darwin,
// non-windows target). PLATFORM_NOTES.md §1: "Linux never takes this
// branch" — Linux's StateDir already IS ~/.config/dexel (or
// $XDG_CONFIG_HOME/dexel), so there is nothing to relocate FROM. That
// guarantee is deliberately load-bearing at the build-tag level, not a
// runtime GOOS check inside relocateLegacy that could rot silently — this
// file is the only relocateHook that ever compiles into a Linux binary,
// mirroring the existing provider_select_*.go per-OS split.
func relocateHook(newStateDir string) error {
	return nil
}
