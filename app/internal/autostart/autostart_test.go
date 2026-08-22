package autostart

import (
	"errors"
	"testing"
)

// TestLaunchdPlistContentExact proves launchdPlistContent (the macOS
// artifact autostart.go generates, un-runnable here — see
// launchd_darwin.go's honesty doc comment) is byte for byte what
// PLATFORM_NOTES.md §3.1 specifies, with the doc's illustrative
// "/Users/USER/..." placeholders substituted for a resolved exePath and
// logPath.
func TestLaunchdPlistContentExact(t *testing.T) {
	got := launchdPlistContent("/Users/dev/.local/bin/dexel", "/Users/dev/Library/Application Support/dexel/logs/runtime.log")
	want := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>              <string>com.jawwadzafar.dexel</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/dev/.local/bin/dexel</string>
    <string>runtime</string>
  </array>
  <key>RunAtLoad</key>          <true/>
  <key>KeepAlive</key>
  <dict><key>SuccessfulExit</key><false/></dict>
  <key>ProcessType</key>        <string>Background</string>
  <key>StandardOutPath</key>
  <string>/Users/dev/Library/Application Support/dexel/logs/runtime.log</string>
  <key>StandardErrorPath</key>
  <string>/Users/dev/Library/Application Support/dexel/logs/runtime.log</string>
</dict>
</plist>
`
	if got != want {
		t.Fatalf("launchdPlistContent mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestLaunchdPlistContentEscapesXML proves a path containing an XML
// special character round-trips safely rather than corrupting the
// plist — an edge case the doc's plain example doesn't exercise, but a
// real path could (e.g. a username containing an ampersand-safe but
// XML-unsafe character).
func TestLaunchdPlistContentEscapesXML(t *testing.T) {
	got := launchdPlistContent(`/Users/A&B/dexel`, `/tmp/log`)
	if want := "<string>/Users/A&amp;B/dexel</string>"; !contains(got, want) {
		t.Fatalf("launchdPlistContent did not escape '&':\n%s", got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// TestLaunchdPlistPath proves the path itself: ~/Library/LaunchAgents/
// com.jawwadzafar.dexel.plist — the identifier matching
// desktop/src-tauri/tauri.conf.json's, per PLATFORM_NOTES.md §3.1.
func TestLaunchdPlistPath(t *testing.T) {
	got := launchdPlistPath("/Users/dev")
	want := "/Users/dev/Library/LaunchAgents/com.jawwadzafar.dexel.plist"
	if got != want {
		t.Fatalf("launchdPlistPath(%q) = %q, want %q", "/Users/dev", got, want)
	}
}

// TestWindowsRunValueDataExact proves the exact HKCU Run value data
// PLATFORM_NOTES.md §3.3 specifies: the resolved binary path, quoted,
// plus " start" (never "runtime" — the Run key has no supervisor).
func TestWindowsRunValueDataExact(t *testing.T) {
	got := windowsRunValueData(`C:\Users\dev\AppData\Local\dexel\bin\dexel.exe`)
	want := `"C:\Users\dev\AppData\Local\dexel\bin\dexel.exe" start`
	if got != want {
		t.Fatalf("windowsRunValueData mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestWindowsRunKeyShape(t *testing.T) {
	if windowsRunKeyPath != `Software\Microsoft\Windows\CurrentVersion\Run` {
		t.Fatalf("windowsRunKeyPath = %q, want the documented HKCU Run path", windowsRunKeyPath)
	}
	if windowsRunValueName != "dexel" {
		t.Fatalf("windowsRunValueName = %q, want %q", windowsRunValueName, "dexel")
	}
}

// TestEnableLingerGuardsNonLinux proves --linger refuses cleanly on any
// non-Linux goos, WITHOUT ever invoking a real command — run is never
// called in this branch.
func TestEnableLingerGuardsNonLinux(t *testing.T) {
	calls := 0
	run := func(name string, args ...string) ([]byte, error) {
		calls++
		return nil, nil
	}
	for _, goos := range []string{"darwin", "windows"} {
		err := enableLinger(goos, run, "dev")
		if err == nil {
			t.Fatalf("enableLinger(%q, ...) = nil error, want a guard error", goos)
		}
	}
	if calls != 0 {
		t.Fatalf("enableLinger invoked run %d times on non-Linux goos, want 0", calls)
	}
}

// TestEnableLingerLinuxRunsLoginctl proves the Linux branch shells out
// to exactly `loginctl enable-linger <username>`, via an injected fake
// `run` — never a real loginctl, which would durably change this
// machine's real systemd-logind state.
func TestEnableLingerLinuxRunsLoginctl(t *testing.T) {
	var gotName string
	var gotArgs []string
	run := func(name string, args ...string) ([]byte, error) {
		gotName, gotArgs = name, args
		return []byte("ok"), nil
	}
	if err := enableLinger("linux", run, "dev"); err != nil {
		t.Fatalf("enableLinger: %v", err)
	}
	if gotName != "loginctl" || len(gotArgs) != 2 || gotArgs[0] != "enable-linger" || gotArgs[1] != "dev" {
		t.Fatalf("enableLinger ran %s %v, want loginctl [enable-linger dev]", gotName, gotArgs)
	}
}

func TestEnableLingerLinuxPropagatesFailure(t *testing.T) {
	run := func(name string, args ...string) ([]byte, error) {
		return []byte("nope"), errors.New("boom")
	}
	if err := enableLinger("linux", run, "dev"); err == nil {
		t.Fatal("enableLinger: want an error when the underlying command fails")
	}
}

// TestMechanismValues pins the four non-empty Mechanism strings against
// PLATFORM_NOTES.md §3's documented config.json enum, since a typo here
// would silently desync the CLI's recorded value from what `dexel
// autostart status`/disable expect to see.
func TestMechanismValues(t *testing.T) {
	cases := map[Mechanism]string{
		MechanismNone:         "",
		MechanismLaunchd:      "launchd",
		MechanismSystemdUser:  "systemd-user",
		MechanismXDGAutostart: "xdg-autostart",
		MechanismWindowsRun:   "windows-run",
	}
	for m, want := range cases {
		if string(m) != want {
			t.Fatalf("Mechanism %v = %q, want %q", m, string(m), want)
		}
	}
}
