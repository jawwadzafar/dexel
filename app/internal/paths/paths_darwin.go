//go:build darwin

package paths

import (
	"os"
	"path/filepath"
)

// relocateHook runs the one-time relocation on macOS
// (PLATFORM_NOTES.md §1): a from-source run before this package existed
// hardcoded ~/.config/dexel on every OS, so a real macOS user's state may
// still be sitting there. If home can't be resolved, there is nothing
// safe to relocate from — StateDir's own homeDir() call will have
// already failed loudly in that case, so this simply no-ops.
func relocateHook(newStateDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	_, err = relocateLegacy(filepath.Join(home, ".config", "dexel"), newStateDir)
	return err
}
