//go:build linux

package autostart

import (
	"os"
	"path/filepath"
	"testing"
)

func TestXDGAutostartPath(t *testing.T) {
	got, err := xdgAutostartPath(fakeGetenvFn(nil), fakeHomeFn("/home/dev"))
	if err != nil {
		t.Fatalf("xdgAutostartPath: %v", err)
	}
	want := "/home/dev/.config/autostart/dexel.desktop"
	if got != want {
		t.Fatalf("xdgAutostartPath = %q, want %q", got, want)
	}
}

// TestXDGDesktopContentExact proves PLATFORM_NOTES.md §3.2's .desktop
// entry, byte for byte, Exec pointing at `<exePath> start` (never
// `runtime` — no supervisor on this path).
func TestXDGDesktopContentExact(t *testing.T) {
	got := xdgDesktopContent("/home/dev/.local/bin/dexel")
	want := `[Desktop Entry]
Type=Application
Name=dexel
Comment=dexel developer companion runtime
Exec=/home/dev/.local/bin/dexel start
Terminal=false
X-GNOME-Autostart-enabled=true
`
	if got != want {
		t.Fatalf("xdgDesktopContent mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestXDGAutostartEnableDisableIdempotent proves enable-twice-is-one-
// entry and disable-twice-is-clean against a fake HOME (t.TempDir),
// never this developer's real ~/.config/autostart/.
func TestXDGAutostartEnableDisableIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	path, err := xdgAutostartPath(os.Getenv, os.UserHomeDir)
	if err != nil {
		t.Fatalf("xdgAutostartPath: %v", err)
	}
	if path != filepath.Join(home, ".config", "autostart", "dexel.desktop") {
		t.Fatalf("xdgAutostartPath resolved to %q under fake HOME %q -- test setup is not isolated", path, home)
	}

	if err := enableXDGAutostart("/x/dexel"); err != nil {
		t.Fatalf("enableXDGAutostart: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	present, err := xdgAutostartPresent()
	if err != nil || !present {
		t.Fatalf("xdgAutostartPresent = %v, %v, want true, nil", present, err)
	}

	// enable again: same content, one file, not two.
	if err := enableXDGAutostart("/x/dexel"); err != nil {
		t.Fatalf("enableXDGAutostart (second call): %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("enabling twice changed the file's content:\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	removed, err := disableXDGAutostart()
	if err != nil || !removed {
		t.Fatalf("disableXDGAutostart = %v, %v, want true, nil", removed, err)
	}
	present, err = xdgAutostartPresent()
	if err != nil || present {
		t.Fatalf("xdgAutostartPresent after disable = %v, %v, want false, nil", present, err)
	}

	// disable again: clean, no error, reports nothing removed.
	removed, err = disableXDGAutostart()
	if err != nil {
		t.Fatalf("disableXDGAutostart (second call): %v", err)
	}
	if removed {
		t.Fatal("disableXDGAutostart (second call) reported removing something that was already gone")
	}
}
