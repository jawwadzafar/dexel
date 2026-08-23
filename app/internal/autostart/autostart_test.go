package autostart

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLaunchdPlistContentExact proves launchdPlistContent (the macOS
// artifact autostart.go generates, un-runnable here — see
// launchd_darwin.go's honesty doc comment) is byte for byte what
// PLATFORM_NOTES.md §3.1 specifies, with the doc's illustrative
// "/Users/USER/..." placeholders substituted for a resolved exePath and
// logPath.
func TestLaunchdPlistContentExact(t *testing.T) {
	got := launchdPlistContent("/Users/dev/.local/bin/dexel", "/Users/dev/Library/Application Support/dexel/logs/runtime.log", "")
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
	got := launchdPlistContent(`/Users/A&B/dexel`, `/tmp/log`, "")
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

// ---------------------------------------------------------------------
// macOS app-bundle attribution (launchdProgram and friends)
// ---------------------------------------------------------------------

// TestLaunchdPlistContentExactBundleProgram is TestLaunchdPlistContentExact's
// twin for the bundle case, and it exists to pin the thing most likely to
// regress by accident: that pointing the job at an .app bundle's executable
// changes ProgramArguments[0] and NOTHING ELSE. RunAtLoad,
// ProcessType=Background, both stdio paths, and above all
// KeepAlive.SuccessfulExit=false — "restart if it crashed, do not restart
// after a clean `dexel stop`", which a bare KeepAlive=true would turn into
// an un-stoppable job — must be byte-identical to the bare-binary plist.
// Asserted as a full literal rather than a diff of two calls so a future
// edit to launchdPlistContent has to be typed out here deliberately.
//
// The program is the CURRENT bundled name, so this literal is also the
// documented shape A in PLATFORM_NOTES.md §3.1 and the two cannot drift
// apart unnoticed. Nothing about space-handling is lost by that: the name
// is display text and has carried a space before, so
// TestLaunchdPlistProgramRoundTrip still feeds spaced paths through
// deliberately.
func TestLaunchdPlistContentExactBundleProgram(t *testing.T) {
	got := launchdPlistContent(
		"/Applications/Dexel.app/Contents/MacOS/Dexel",
		"/Users/dev/Library/Application Support/dexel/logs/runtime.log",
		"com.jawwadzafar.dexel",
	)
	want := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>              <string>com.jawwadzafar.dexel</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Applications/Dexel.app/Contents/MacOS/Dexel</string>
    <string>runtime</string>
  </array>
  <key>AssociatedBundleIdentifiers</key>
  <array>
    <string>com.jawwadzafar.dexel</string>
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
		t.Fatalf("launchdPlistContent(bundle program) mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestLaunchdPlistContentBundleDiffersOnlyInAttribution is the same claim
// stated structurally, and it is the one that cannot rot into agreement
// with a bad change the way two hand-typed literals can: generate the
// bare and bundled plists and prove the ONLY differences are
// ProgramArguments[0] and the four added AssociatedBundleIdentifiers
// lines. Everything supervision-related — RunAtLoad,
// KeepAlive.SuccessfulExit=false, ProcessType, both stdio paths — must be
// untouched.
func TestLaunchdPlistContentBundleDiffersOnlyInAttribution(t *testing.T) {
	const logPath = "/Users/dev/Library/Application Support/dexel/logs/runtime.log"
	bare := launchdPlistContent("/Users/dev/.local/bin/dexel", logPath, "")
	bundled := launchdPlistContent("/Applications/Dexel.app/Contents/MacOS/Dexel", logPath, "com.jawwadzafar.dexel")

	bareLines := splitLines(bare)
	bundledLines := splitLines(bundled)

	// The four AssociatedBundleIdentifiers lines are the only ADDITION.
	if got, want := len(bundledLines)-len(bareLines), 4; got != want {
		t.Fatalf("bundled plist has %d more lines than bare, want %d", got, want)
	}
	assoc := []string{
		"  <key>AssociatedBundleIdentifiers</key>",
		"  <array>",
		"    <string>com.jawwadzafar.dexel</string>",
		"  </array>",
	}
	// Remove the association block, then the two plists must differ on
	// exactly one line: the program.
	var stripped []string
	for i := 0; i < len(bundledLines); i++ {
		if i+len(assoc) <= len(bundledLines) && equalLines(bundledLines[i:i+len(assoc)], assoc) {
			i += len(assoc) - 1
			continue
		}
		stripped = append(stripped, bundledLines[i])
	}
	if len(stripped) != len(bareLines) {
		t.Fatalf("after removing the association block the bundled plist has %d lines, want %d", len(stripped), len(bareLines))
	}
	var diffs []int
	for i := range bareLines {
		if bareLines[i] != stripped[i] {
			diffs = append(diffs, i)
		}
	}
	if len(diffs) != 1 {
		t.Fatalf("want exactly 1 differing line (ProgramArguments[0]), got %d at lines %v", len(diffs), diffs)
	}
	if want := "    <string>/Applications/Dexel.app/Contents/MacOS/Dexel</string>"; stripped[diffs[0]] != want {
		t.Fatalf("the differing line is %q, want %q", stripped[diffs[0]], want)
	}
}

// TestLaunchdPlistContentNoAssociationIsUnchanged is the no-regression
// guarantee spelled out as its own test: an empty associatedBundleID must
// produce a plist with NO trace of the key, so the fallback path (and
// --bare) write exactly what shipped before attribution existed.
func TestLaunchdPlistContentNoAssociationIsUnchanged(t *testing.T) {
	got := launchdPlistContent("/Users/dev/.local/bin/dexel", "/tmp/log", "")
	if contains(got, "AssociatedBundleIdentifiers") {
		t.Fatalf("empty associatedBundleID still emitted the key:\n%s", got)
	}
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// TestLaunchdBundleCandidates pins the search order: /Applications before
// ~/Applications (a system-wide install is what the installer produces),
// and BOTH capitalisations of the bundle name, because the bundle is
// mid-rename from dexel.app to Dexel.app and a case-sensitive volume —
// unusual but supported — would otherwise miss one spelling entirely.
func TestLaunchdBundleCandidates(t *testing.T) {
	got := launchdBundleCandidates("/Users/dev")
	want := []string{
		"/Applications/Dexel.app",
		"/Applications/dexel.app",
		"/Users/dev/Applications/Dexel.app",
		"/Users/dev/Applications/dexel.app",
	}
	if len(got) != len(want) {
		t.Fatalf("launchdBundleCandidates returned %d candidates %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate %d = %q, want %q (full list %v)", i, got[i], want[i], got)
		}
	}
}

func TestBundleServerPath(t *testing.T) {
	for name, want := range map[string]string{
		"Dexel":         "/Applications/Dexel.app/Contents/MacOS/Dexel",
		"Dexel Runtime": "/Applications/Dexel.app/Contents/MacOS/Dexel Runtime",
		"dexel-server":  "/Applications/Dexel.app/Contents/MacOS/dexel-server",
	} {
		if got := bundleServerPath("/Applications/Dexel.app", name); got != want {
			t.Fatalf("bundleServerPath(_, %q) = %q, want %q", name, got, want)
		}
	}
}

// TestBundleServerExecutablesOrderAndContents pins the probe list itself.
// Every one of its properties is load-bearing and none is observable from
// any other test in this file:
//
//   - "Dexel" comes FIRST, so a bundle carrying the current name resolves
//     to it rather than to a leftover legacy copy beside it. That filename
//     is what System Settings displays, which is the whole point of the
//     rename.
//   - "Dexel Runtime" and "dexel-server" are still present, so a bundle
//     installed BEFORE either rename keeps resolving instead of silently
//     degrading to the bare-binary plist on an upgrade the user never
//     asked about.
//   - Neither "dexel-desktop" (the Tauri GUI shell's main binary today,
//     tauri.conf.json's mainBinaryName) nor "dexel" (what that binary was
//     called in every bundle built before that rename) appears. Both are a
//     DIFFERENT program, and a LaunchAgent pointed at either would open a
//     window at every login instead of starting the runtime.
//
// Note what this test can NOT establish, so nobody reads more into it than
// it says: "Dexel" and the legacy "dexel" fold to the same string, so the
// absence of "dexel" from this list does not by itself keep the GUI shell
// out of the plist on a case-insensitive volume. That is
// isExecutableFile's case-exactness rule, and it is
// TestLaunchdProgramOnAPreRenameBundle that proves it.
func TestBundleServerExecutablesOrderAndContents(t *testing.T) {
	want := []string{"Dexel", "Dexel Runtime", "dexel-server"}
	if len(bundleServerExecutables) != len(want) {
		t.Fatalf("bundleServerExecutables = %v, want %v", bundleServerExecutables, want)
	}
	for i := range want {
		if bundleServerExecutables[i] != want[i] {
			t.Fatalf("bundleServerExecutables[%d] = %q, want %q (full list %v)", i, bundleServerExecutables[i], want[i], bundleServerExecutables)
		}
	}
	// EqualFold, not ==: macOS's default APFS volume is case-INsensitive,
	// so a name differing from the shell's main binary only in case is the
	// SAME FILE and just as wrong.
	for _, name := range bundleServerExecutables {
		if strings.EqualFold(name, "dexel-desktop") {
			t.Fatalf("bundleServerExecutables contains %q, which case-insensitively collides with the Tauri shell's own main binary (tauri.conf.json's mainBinaryName)", name)
		}
	}
}

// TestNameIsCaseExactOnDisk covers the predicate that makes the probe list
// safe, against a REAL directory — the only way to exercise it, since the
// behaviour it defends against is a property of the filesystem rather than
// of this code.
//
// It asserts the same outcome on both kinds of volume, which is what makes
// it a portable test rather than a darwin-only one:
//
//   - case-INsensitive (macOS default): os.Stat("<dir>/Dexel") SUCCEEDS and
//     hands back the file stored as "dexel". This function must still say
//     no, because the caller asked for a name the directory does not
//     actually contain.
//   - case-sensitive (Linux CI, and a non-default macOS volume): there is
//     no such file at all, so the answer is no for the ordinary reason.
func TestNameIsCaseExactOnDisk(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dexel"), []byte("gui shell"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dexel Runtime"), []byte("go daemon"), 0o755); err != nil {
		t.Fatal(err)
	}

	if !nameIsCaseExactOnDisk(filepath.Join(dir, "dexel")) {
		t.Fatal(`nameIsCaseExactOnDisk said no to "dexel", which is exactly how it is spelled on disk`)
	}
	if !nameIsCaseExactOnDisk(filepath.Join(dir, "Dexel Runtime")) {
		t.Fatal(`nameIsCaseExactOnDisk said no to "Dexel Runtime", spaces and all`)
	}
	if nameIsCaseExactOnDisk(filepath.Join(dir, "Dexel")) {
		t.Fatal(`nameIsCaseExactOnDisk said yes to "Dexel" when the directory stores "dexel" -- on a case-insensitive volume that is the GUI shell, and this is the check that keeps it out of the LaunchAgent`)
	}
	if nameIsCaseExactOnDisk(filepath.Join(dir, "nothing-here")) {
		t.Fatal("nameIsCaseExactOnDisk said yes to a file that does not exist")
	}
	// A directory that cannot be read is "no", never a panic or an error.
	if nameIsCaseExactOnDisk(filepath.Join(dir, "no-such-dir", "dexel")) {
		t.Fatal("nameIsCaseExactOnDisk said yes inside a directory that does not exist")
	}
}

// TestIsExecutableFileRejectsNonExecutables covers the other two halves of
// the predicate: a directory is not a program, and neither is a file with
// no execute bit. Both would otherwise be baked into a plist as a program
// launchd fails to spawn at every login — a failure visible only in a log
// nobody reads.
func TestIsExecutableFileRejectsNonExecutables(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "Dexel.app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dexel"), []byte("not executable"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isExecutableFile(filepath.Join(dir, "Dexel.app")) {
		t.Fatal("isExecutableFile said yes to a directory")
	}
	if isExecutableFile(filepath.Join(dir, "Dexel")) {
		t.Fatal("isExecutableFile said yes to a file with no execute bit")
	}
	if err := os.Chmod(filepath.Join(dir, "Dexel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !isExecutableFile(filepath.Join(dir, "Dexel")) {
		t.Fatal("isExecutableFile said no to a regular, executable, correctly-spelled file")
	}
}

// TestLaunchdProgramOnAPreRenameBundle is the regression test for the trap
// the rename to "Dexel" created, and it runs against a real directory
// because a fake `exists` set cannot model a case-insensitive filesystem.
//
// The shape: a bundle built BEFORE the main binary was renamed, so
// Contents/MacOS holds `dexel` (the Tauri GUI shell) and `Dexel Runtime`
// (the Go daemon). "Dexel" is the FIRST entry of bundleServerExecutables,
// and on the owner's Mac os.Stat(".../MacOS/Dexel") resolves to `dexel` and
// succeeds — so a probe that trusted os.Stat would put the GUI shell in the
// plist and open a window at every login. The plist must name
// `Dexel Runtime` instead.
//
// On a case-sensitive volume the same assertion holds for the boring
// reason (no ".../MacOS/Dexel" exists), so this test is correct everywhere
// and only STRONG on a case-insensitive one. It reports which kind of
// volume it actually ran on rather than leaving that to be guessed.
func TestLaunchdProgramOnAPreRenameBundle(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "Dexel.app")
	macOS := filepath.Join(bundle, "Contents", "MacOS")
	if err := os.MkdirAll(macOS, 0o755); err != nil {
		t.Fatal(err)
	}
	// Deliberately DIFFERENT contents, so a wrong answer is unmistakable.
	if err := os.WriteFile(filepath.Join(macOS, "dexel"), []byte("tauri gui shell"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(macOS, "Dexel Runtime"), []byte("go daemon"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(macOS, "Dexel")); err == nil {
		t.Log("this volume is case-INsensitive: .../MacOS/Dexel resolves to the GUI shell, so the assertion below is the real thing")
	} else {
		t.Log("this volume is case-sensitive: .../MacOS/Dexel does not exist, so the assertion below holds for the ordinary reason")
	}

	const bare = "/Users/dev/.local/bin/dexel"
	program, bundleID, _ := launchdProgram(bare, []string{bundle}, isExecutableFile, Options{})

	want := filepath.Join(macOS, "Dexel Runtime")
	if program != want {
		t.Fatalf("launchdProgram = %q, want %q -- a plist naming the GUI shell opens a WINDOW at every login instead of starting the runtime", program, want)
	}
	if bundleID != launchdLabel {
		t.Fatalf("launchdProgram bundleID = %q, want %q", bundleID, launchdLabel)
	}
}

// TestLaunchdProgramOnACurrentBundle is the other half: in a bundle built
// AFTER the rename, Contents/MacOS holds `Dexel` (the daemon) and
// `dexel-desktop` (the GUI shell), and the plist must name `Dexel` — the
// capital-D spelling is the whole reason the rename happened, since that
// filename is what System Settings prints.
func TestLaunchdProgramOnACurrentBundle(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "Dexel.app")
	macOS := filepath.Join(bundle, "Contents", "MacOS")
	if err := os.MkdirAll(macOS, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(macOS, "Dexel"), []byte("go daemon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(macOS, "dexel-desktop"), []byte("tauri gui shell"), 0o755); err != nil {
		t.Fatal(err)
	}

	program, _, _ := launchdProgram("/Users/dev/.local/bin/dexel", []string{bundle}, isExecutableFile, Options{})
	want := filepath.Join(macOS, "Dexel")
	if program != want {
		t.Fatalf("launchdProgram = %q, want %q", program, want)
	}
}

// TestInsideAppBundle covers the ancestor walk, including the nested-bundle
// shape real macOS apps actually ship their background helpers in (OneDrive:
// OneDrive.app/Contents/StandaloneUpdater.app/Contents/MacOS/...), and the
// negative cases that must NOT be mistaken for a bundle.
func TestInsideAppBundle(t *testing.T) {
	cases := map[string]bool{
		"/Applications/Dexel.app/Contents/MacOS/Dexel Runtime": true,
		// The legacy name, and the lowercase legacy bundle: both still
		// count as "inside a bundle", because both can still be on disk.
		"/Applications/dexel.app/Contents/MacOS/dexel-server":                                      true,
		"/Applications/OneDrive.app/Contents/StandaloneUpdater.app/Contents/MacOS/OneDriveUpdater": true,
		"/Users/dev/Applications/Dexel.app/Contents/MacOS/Dexel Runtime":                           true,
		"/Users/dev/.local/bin/dexel":                                                              false,
		"/usr/local/bin/dexel":                                                                     false,
		"/Users/dev/apps/dexel":                                                                    false,
		// A directory merely NAMED like an app, with the binary above it,
		// is not "inside" it.
		"/Users/dev/Dexel.app": false,
		// Bare relative path: no ancestor is a bundle.
		"dexel": false,
	}
	for path, want := range cases {
		if got := insideAppBundle(path); got != want {
			t.Fatalf("insideAppBundle(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestLaunchdProgramPrefersAppBundle is the whole point of the change:
// given an installed bundle whose server executable exists and is
// executable, the plist must name THAT, so macOS has a bundle to attribute
// the background item to (System Settings → Login Items & Extensions).
//
// It doubles as the LEGACY-NAME regression test: the only thing on this
// fake disk is the pre-rename `dexel-server`, in the lowercase legacy
// bundle. A bundle installed before the rename must keep resolving —
// dropping the old name from bundleServerExecutables would make this fall
// all the way through to the bare-binary plist, silently.
func TestLaunchdProgramPrefersAppBundle(t *testing.T) {
	const bare = "/Users/dev/.local/bin/dexel"
	candidates := launchdBundleCandidates("/Users/dev")
	// Only the SECOND candidate exists — the lowercase legacy spelling —
	// which also proves the loop does not stop at the first miss.
	exists := onlyThese("/Applications/dexel.app/Contents/MacOS/dexel-server")

	program, bundleID, note := launchdProgram(bare, candidates, exists, Options{})
	if want := "/Applications/dexel.app/Contents/MacOS/dexel-server"; program != want {
		t.Fatalf("launchdProgram = %q, want %q", program, want)
	}
	if bundleID != launchdLabel {
		t.Fatalf("launchdProgram bundleID = %q, want %q -- without AssociatedBundleIdentifiers the only DOCUMENTED half of the attribution is missing", bundleID, launchdLabel)
	}
	if note == "" {
		t.Fatal("launchdProgram returned an empty note for the bundle case; `enable` prints it, so silence here is a silent substitution")
	}
}

// TestLaunchdProgramPrefersCapitalisedBundleFirst: when both spellings are
// present (impossible on a case-insensitive volume, possible on a
// case-sensitive one), the properly-capitalised Dexel.app wins.
func TestLaunchdProgramPrefersCapitalisedBundleFirst(t *testing.T) {
	exists := onlyThese(
		"/Applications/Dexel.app/Contents/MacOS/Dexel Runtime",
		"/Applications/dexel.app/Contents/MacOS/Dexel Runtime",
	)
	program, _, _ := launchdProgram("/Users/dev/.local/bin/dexel", launchdBundleCandidates("/Users/dev"), exists, Options{})
	if want := "/Applications/Dexel.app/Contents/MacOS/Dexel Runtime"; program != want {
		t.Fatalf("launchdProgram = %q, want the capitalised bundle %q", program, want)
	}
}

// TestLaunchdProgramPrefersCurrentNameWithinOneBundle: inside a SINGLE
// bundle carrying both names (a hand-copied leftover, or a bundle unpacked
// over an older one on a case-sensitive volume), the current name wins —
// which is the whole point of the rename, since that filename is what
// System Settings displays.
func TestLaunchdProgramPrefersCurrentNameWithinOneBundle(t *testing.T) {
	exists := onlyThese(
		"/Applications/Dexel.app/Contents/MacOS/dexel-server",
		"/Applications/Dexel.app/Contents/MacOS/Dexel Runtime",
		"/Applications/Dexel.app/Contents/MacOS/Dexel",
	)
	program, _, _ := launchdProgram("/Users/dev/.local/bin/dexel", launchdBundleCandidates("/Users/dev"), exists, Options{})
	if want := "/Applications/Dexel.app/Contents/MacOS/Dexel"; program != want {
		t.Fatalf("launchdProgram = %q, want the current name %q", program, want)
	}
}

// TestLaunchdProgramPrefersBundleLocationOverName pins which loop is the
// outer one, a choice that only shows up in this exact shape: the
// PREFERRED bundle has only the legacy name, a less-preferred bundle has
// the current one. Bundle location is the stronger signal — it is what the
// installer controls — so /Applications must beat ~/Applications even
// though it means writing the legacy filename into the plist.
func TestLaunchdProgramPrefersBundleLocationOverName(t *testing.T) {
	exists := onlyThese(
		"/Applications/Dexel.app/Contents/MacOS/dexel-server",
		"/Users/dev/Applications/Dexel.app/Contents/MacOS/Dexel Runtime",
	)
	program, _, _ := launchdProgram("/Users/dev/.local/bin/dexel", launchdBundleCandidates("/Users/dev"), exists, Options{})
	if want := "/Applications/Dexel.app/Contents/MacOS/dexel-server"; program != want {
		t.Fatalf("launchdProgram = %q, want the more-preferred BUNDLE %q even though its executable carries the legacy name", program, want)
	}
}

// TestLaunchdProgramFallsBackToBareBinary is the no-regression half: with
// no bundle installed anywhere, the behaviour must be byte-identical to
// what shipped before this function existed — the resolved os.Executable().
// A missing app bundle degrades the ICON, never the autostart.
func TestLaunchdProgramFallsBackToBareBinary(t *testing.T) {
	const bare = "/Users/dev/.local/bin/dexel"
	nothingExists := func(string) bool { return false }
	program, bundleID, note := launchdProgram(bare, launchdBundleCandidates("/Users/dev"), nothingExists, Options{})
	if program != bare {
		t.Fatalf("launchdProgram with no bundle = %q, want the bare binary %q", program, bare)
	}
	if bundleID != "" {
		t.Fatalf("launchdProgram bundleID = %q with no bundle installed, want \"\" -- associating the job with a bundle that is not there would be a claim we cannot back", bundleID)
	}
	if note == "" {
		t.Fatal("launchdProgram returned an empty note for the fallback case; the user should be told why the icon will be generic")
	}
}

// TestLaunchdProgramSkipsBundleWithoutServer: a bundle directory that
// exists but carries NONE of bundleServerExecutables inside it is not a
// usable target. Baking it into the plist anyway would produce a job that
// fails to spawn at every login — visible only in a log nobody reads — so
// it must fall through. isExecutable is asked about the EXECUTABLE, never
// the directory, which is what makes this case fall through for free.
func TestLaunchdProgramSkipsBundleWithoutServer(t *testing.T) {
	const bare = "/Users/dev/.local/bin/dexel"
	// The bundle dirs "exist", but neither name does inside them.
	exists := onlyThese("/Applications/Dexel.app", "/Applications/dexel.app")
	program, bundleID, _ := launchdProgram(bare, launchdBundleCandidates("/Users/dev"), exists, Options{})
	if program != bare {
		t.Fatalf("launchdProgram = %q, want the bare binary %q (a bundle with no server executable is not a target)", program, bare)
	}
	if bundleID != "" {
		t.Fatalf("launchdProgram bundleID = %q, want \"\" -- an unusable bundle is not something to associate with", bundleID)
	}
}

// TestLaunchdProgramBareOptionOptsOut: --bare must win even when a perfectly
// good bundle is installed, because the trade-off it declines is real —
// bundle attribution moves the login-time binary off the one `dexel update`
// replaces.
func TestLaunchdProgramBareOptionOptsOut(t *testing.T) {
	const bare = "/Users/dev/.local/bin/dexel"
	everythingExists := func(string) bool { return true }
	program, bundleID, note := launchdProgram(bare, launchdBundleCandidates("/Users/dev"), everythingExists, Options{BareExecutable: true})
	if program != bare {
		t.Fatalf("launchdProgram(--bare) = %q, want %q", program, bare)
	}
	if bundleID != "" {
		t.Fatalf("launchdProgram(--bare) bundleID = %q, want \"\" -- --bare asks for exactly the pre-attribution plist", bundleID)
	}
	if note == "" {
		t.Fatal("launchdProgram(--bare) returned an empty note")
	}
}

// TestLaunchdProgramKeepsExeAlreadyInsideABundle: invoked AS the bundle's
// own executable (`/Applications/Dexel.app/Contents/MacOS/Dexel\ Runtime
// autostart enable`), the resolved exePath is already bundle-attributed and
// must be used verbatim — including when it is a DIFFERENT bundle from any
// candidate, which is the case that would otherwise be a silent redirect
// away from the binary the user actually ran.
func TestLaunchdProgramKeepsExeAlreadyInsideABundle(t *testing.T) {
	exe := "/Users/dev/build/Dexel.app/Contents/MacOS/Dexel Runtime"
	everythingExists := func(string) bool { return true }
	program, bundleID, note := launchdProgram(exe, launchdBundleCandidates("/Users/dev"), everythingExists, Options{})
	if program != exe {
		t.Fatalf("launchdProgram = %q, want the already-bundled exe %q", program, exe)
	}
	if bundleID != launchdLabel {
		t.Fatalf("launchdProgram bundleID = %q, want %q", bundleID, launchdLabel)
	}
	if note == "" {
		t.Fatal("launchdProgram returned an empty note for the already-bundled case")
	}
}

// TestLaunchdPlistProgramRoundTrip proves `status` can read back exactly
// what `enable` wrote — the property the whole "status reports the truth"
// claim rests on — for both the bundled names, for the bare shape, and for
// a path containing an XML metacharacter, which is the only way
// escape/unescape asymmetry would ever show up.
//
// The SPACE in "Dexel Runtime" is pinned here on purpose. Nothing in this
// package splits on whitespace today, and nothing should start to: the
// path goes into a plist <string> and comes back out of one, and launchd
// reads ProgramArguments as an array, never as a command line.
func TestLaunchdPlistProgramRoundTrip(t *testing.T) {
	for _, exe := range []string{
		"/Users/dev/.local/bin/dexel",
		"/Applications/Dexel.app/Contents/MacOS/Dexel",
		"/Applications/Dexel.app/Contents/MacOS/Dexel Runtime",
		"/Applications/Dexel.app/Contents/MacOS/dexel-server",
		`/Users/A&B/Dexel.app/Contents/MacOS/Dexel Runtime`,
		`/Users/<weird>/'quoted"/dexel`,
	} {
		// Both shapes: with and without the association block, since the
		// key sits between ProgramArguments and RunAtLoad and a naive
		// scanner could be thrown off by it.
		for _, assoc := range []string{"", "com.jawwadzafar.dexel"} {
			content := launchdPlistContent(exe, "/tmp/log", assoc)
			if got := launchdPlistProgram(content); got != exe {
				t.Fatalf("launchdPlistProgram round-trip (assoc=%q): got %q, want %q\nplist:\n%s", assoc, got, exe, content)
			}
		}
	}
}

// TestXMLUnescapeHandlesEscapedEntity is the ordering trap called out in
// xmlUnescaper's comment: a path containing the literal text "&lt;" is
// written to the plist as "&amp;lt;" and must come back as "&lt;", not as
// "<lt;".
func TestXMLUnescapeHandlesEscapedEntity(t *testing.T) {
	const literal = `/Users/dev/&lt;/dexel`
	escaped := xmlEscape(literal)
	if escaped != `/Users/dev/&amp;lt;/dexel` {
		t.Fatalf("xmlEscape(%q) = %q", literal, escaped)
	}
	if got := xmlUnescape(escaped); got != literal {
		t.Fatalf("xmlUnescape(%q) = %q, want %q", escaped, got, literal)
	}
}

// TestLaunchdPlistProgramUnknownShapes: a plist this package did not
// generate must read as "" — "could not tell" — so `status` says "unknown"
// rather than half-understanding a hand-edited file.
func TestLaunchdPlistProgramUnknownShapes(t *testing.T) {
	for _, bad := range []string{
		"",
		"not a plist at all",
		// Right key, no <string> after it.
		"<key>ProgramArguments</key>\n<array>\n</array>",
		// Unterminated <string>.
		"<key>ProgramArguments</key>\n<array>\n<string>/Users/dev/dexel",
		// Uses <Program> instead — a valid launchd shape, but not ours.
		"<key>Program</key><string>/Users/dev/dexel</string>",
	} {
		if got := launchdPlistProgram(bad); got != "" {
			t.Fatalf("launchdPlistProgram(%q) = %q, want \"\" (unknown)", bad, got)
		}
	}
}

// onlyThese builds an isExecutable predicate that reports true for exactly
// the listed paths — the injected filesystem every launchdProgram test uses
// instead of touching a real one.
func onlyThese(paths ...string) func(string) bool {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return func(p string) bool { return set[p] }
}
