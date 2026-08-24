//go:build linux

// xdg_linux.go implements Linux's FALLBACK autostart mechanism
// (docs/production-runtime/PLATFORM_NOTES.md §3.2): an XDG
// autostart .desktop entry, used only when systemd_linux.go's
// systemdUsable() says no user systemd session exists. It has no
// supervision of its own, so its Exec line runs `dexel start` — never
// `runtime` — to get `start`'s lock check, log rotation and readiness
// report for free.
package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

// xdgAutostartPath is ~/.config/autostart/dexel.desktop (or under
// $XDG_CONFIG_HOME) — same xdgConfigDir helper systemd_linux.go's unit
// path uses, both XDG-relative by definition.
func xdgAutostartPath(getenv func(string) string, homeDir func() (string, error)) (string, error) {
	dir, err := xdgConfigDir(getenv, homeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "autostart", "dexel.desktop"), nil
}

// xdgDesktopContent is PLATFORM_NOTES.md §3.2's .desktop entry, byte
// for byte, Exec pointing at exePath (the caller's resolved
// os.Executable()) followed by `start`.
func xdgDesktopContent(exePath string) string {
	return fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=dexel
Comment=dexel developer companion runtime
Exec=%s start
Terminal=false
X-GNOME-Autostart-enabled=true
`, exePath)
}

// enableXDGAutostart writes the .desktop file. Idempotent: writing
// identical content twice is a no-op change.
func enableXDGAutostart(exePath string) error {
	path, err := xdgAutostartPath(os.Getenv, os.UserHomeDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create autostart dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(xdgDesktopContent(exePath)), 0o644); err != nil {
		return fmt.Errorf("write autostart desktop file: %w", err)
	}
	return nil
}

// disableXDGAutostart removes the .desktop file if present. Idempotent:
// removing an already-absent file is reported as "nothing to do", not
// an error.
func disableXDGAutostart() (removed bool, err error) {
	path, err := xdgAutostartPath(os.Getenv, os.UserHomeDir)
	if err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// xdgAutostartPresent reports whether the .desktop file exists — the
// only "is it enabled" signal XDG autostart offers
// (PLATFORM_NOTES.md §3.2: "no way to answer 'is it enabled?' beyond
// 'does the file exist?'").
func xdgAutostartPresent() (bool, error) {
	path, err := xdgAutostartPath(os.Getenv, os.UserHomeDir)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
