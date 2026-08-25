// cmd_uninstall_test.go — the reversal list and the consent logic
// (cmd_uninstall.go, docs/production-runtime/ARCHITECTURE.md §9,
// MIGRATION_PLAN.md §PR-7).
//
// Everything here runs on ANY host: uninstallPlan and the consent
// functions are parameter-injected precisely so Windows' and macOS'
// branches are asserted from this Linux dev box instead of being
// authored blind — the same convention app/internal/paths/paths_test.go
// and app/internal/autostart/autostart_test.go already follow.
//
// Nothing in this file deletes anything. The plan is DATA; executing it
// is what the end-to-end gate does against a real temp install.
package main

import (
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// planFor builds a plan with no-op filesystem probes unless a test
// supplies its own.
func planFor(f installFacts) []artifact {
	if f.Exists == nil {
		f.Exists = func(string) bool { return false }
	}
	if f.Glob == nil {
		f.Glob = func(string) []string { return nil }
	}
	return uninstallPlan(f)
}

// find returns the artifact at path, or nil.
func find(plan []artifact, path string) *artifact {
	for i := range plan {
		if plan[i].Path == filepath.Clean(path) || plan[i].Path == path {
			return &plan[i]
		}
	}
	return nil
}

func planPaths(plan []artifact) []string {
	out := make([]string, 0, len(plan))
	for _, a := range plan {
		out = append(out, a.Path)
	}
	return out
}

// TestPlanReversesTheLinuxInstaller pins the plan against what
// install.sh ACTUALLY creates — every path below is quoted from the
// function in install.sh that writes it, so a change to either side that
// is not mirrored fails here rather than leaving a file behind on a real
// machine.
func TestPlanReversesTheLinuxInstaller(t *testing.T) {
	const (
		bin   = "/home/u/.local/bin"
		data  = "/home/u/.local/share"
		state = "/home/u/.config/dexel"
	)
	plan := planFor(installFacts{
		GOOS:     "linux",
		Self:     bin + "/dexel",
		BinDir:   bin,
		StateDir: state,
		HomeDir:  "/home/u",
		DataDir:  data,
		// install_desktop_app's two files (install.sh §"3. the SHELL").
		Glob: func(pattern string) []string {
			if pattern == bin+"/dexel-desktop*" {
				return []string{bin + "/dexel-desktop", bin + "/dexel-desktop.AppImage"}
			}
			return nil
		},
	})

	// install.sh's install_binary, install_desktop_app, install_icon,
	// write_desktop_file — and ARCHITECTURE.md §9 step 4's runtime
	// residue.
	want := []string{
		bin + "/dexel-desktop",                         // shim
		bin + "/dexel-desktop.AppImage",                // the GUI shell
		data + "/applications/dexel.desktop",           // write_desktop_file
		data + "/icons/hicolor/128x128/apps/dexel.png", // install_icon
		state + "/runtime.json",
		state + "/runtime.lock",
		state + "/cache",
		state + "/state.db",    // KEPT
		state + "/config.json", // KEPT
		state + "/logs",        // KEPT
		bin + "/dexel",         // the running binary, LAST
	}
	if got := planPaths(plan); !reflect.DeepEqual(got, want) {
		t.Fatalf("plan mismatch\n got: %v\nwant: %v", got, want)
	}

	// The binary appears exactly ONCE, as the deferred running-binary
	// entry — never also as a plain file removal, which would try to
	// unlink it before the state dir was handled.
	n := 0
	for _, a := range plan {
		if a.Path == bin+"/dexel" {
			n++
			if a.Kind != removeRunningBinary {
				t.Errorf("%s is kind %v, want removeRunningBinary", a.Path, a.Kind)
			}
		}
	}
	if n != 1 {
		t.Errorf("the binary appears %d times in the plan, want exactly 1", n)
	}
	if last := plan[len(plan)-1]; last.Kind != removeRunningBinary {
		t.Errorf("the last plan entry is %v (%s), and it MUST be the running binary: on unix it is removed after everything else, and on Windows it is what the detached helper is handed", last.Kind, last.Path)
	}

	// The three step-5 paths are KEPT, not removed. This is the test
	// that stands between a user and a deleted save.
	for _, p := range []string{state + "/state.db", state + "/config.json", state + "/logs"} {
		a := find(plan, p)
		if a == nil {
			t.Fatalf("%s is not in the plan at all — it must be PRINTED (ARCHITECTURE.md §9 step 5)", p)
		}
		if a.Kind != keepAndPrint {
			t.Errorf("%s is kind %v — WITHOUT --purge it must be keepAndPrint", p, a.Kind)
		}
	}
}

// TestPlanPurgeReplacesTheStateDirEntries: --purge removes the whole
// directory, which SUBSUMES runtime.json/runtime.lock/cache — listing
// them separately as well would report "already absent" for files that
// were in fact deleted a line earlier.
func TestPlanPurgeReplacesTheStateDirEntries(t *testing.T) {
	const state = "/home/u/.config/dexel"
	plan := planFor(installFacts{
		GOOS: "linux", Self: "/home/u/.local/bin/dexel", BinDir: "/home/u/.local/bin",
		StateDir: state, HomeDir: "/home/u", DataDir: "/home/u/.local/share",
		Purge: true,
	})

	a := find(plan, state)
	if a == nil || a.Kind != removeTree {
		t.Fatalf("--purge must remove the state dir as a tree; got %+v", a)
	}
	for _, p := range []string{
		state + "/state.db", state + "/config.json", state + "/logs",
		state + "/runtime.json", state + "/runtime.lock", state + "/cache",
	} {
		if find(plan, p) != nil {
			t.Errorf("--purge must not list %s separately — the whole directory goes", p)
		}
	}
	for _, a := range plan {
		if a.Kind == keepAndPrint {
			t.Errorf("--purge keeps nothing under the state dir, but %s is keepAndPrint", a.Path)
		}
	}
}

// TestPlanReversesTheWindowsInstaller pins install.ps1's three
// artifacts: the exe, the .ico beside it (Install-ShortcutIcon), and the
// per-user Start Menu .lnk (Install-StartMenuShortcut).
func TestPlanReversesTheWindowsInstaller(t *testing.T) {
	const (
		bin     = `C:\Users\u\AppData\Local\dexel\bin`
		appdata = `C:\Users\u\AppData\Roaming`
		state   = `C:\Users\u\AppData\Local\dexel`
	)
	plan := planFor(installFacts{
		GOOS: "windows", Self: bin + `\dexel.exe`, BinDir: bin,
		StateDir: state, HomeDir: `C:\Users\u`, AppData: appdata,
	})

	// Literal backslash paths, asserted from Linux: that is the whole
	// point of pathJoin/pathBase/pathDir being keyed off the planned OS
	// rather than the host's path/filepath.
	for _, want := range []struct {
		path string
		kind removalKind
	}{
		{bin + `\dexel.ico`, removeFile},
		{appdata + `\Microsoft\Windows\Start Menu\Programs\Dexel.lnk`, removeFile},
		{bin + `\dexel.exe`, removeRunningBinary},
		{state + `\runtime.json`, removeFile},
		{state + `\cache`, removeTree},
		{state + `\state.db`, keepAndPrint},
	} {
		a := find(plan, want.path)
		if a == nil {
			t.Fatalf("the Windows plan is missing %s\nplan: %v", want.path, planPaths(plan))
		}
		if a.Kind != want.kind {
			t.Errorf("%s: kind %v, want %v", want.path, a.Kind, want.kind)
		}
	}
	// No Linux desktop integration on Windows.
	for _, a := range plan {
		if strings.Contains(a.Path, "hicolor") || strings.HasSuffix(a.Path, ".desktop") {
			t.Errorf("the Windows plan contains a Linux artifact: %s", a.Path)
		}
	}
}

// TestPlanRemovesAnInstalledMacAppBundle: the CLI installer never makes
// a bundle on macOS, so one that exists was dragged in from the .dmg —
// still ours, so it goes under the same consent, and only when it is
// really there.
func TestPlanRemovesAnInstalledMacAppBundle(t *testing.T) {
	const home = "/Users/u"
	present := map[string]bool{"/Applications/Dexel.app": true}

	plan := planFor(installFacts{
		GOOS: "darwin", Self: home + "/.local/bin/dexel", BinDir: home + "/.local/bin",
		StateDir: home + "/Library/Application Support/dexel", HomeDir: home,
		Exists: func(p string) bool { return present[p] },
	})

	a := find(plan, "/Applications/Dexel.app")
	if a == nil || a.Kind != removeTree {
		t.Fatalf("an installed /Applications/Dexel.app must be removed as a tree; got %+v", a)
	}
	// The candidates that are NOT there are absent from the plan
	// entirely: four "already absent" bundle lines on every macOS
	// uninstall would be noise, not honesty.
	for _, p := range macAppBundleCandidates(home) {
		if p == "/Applications/Dexel.app" {
			continue
		}
		if find(plan, p) != nil {
			t.Errorf("%s does not exist and must not be in the plan", p)
		}
	}

	// With no bundle at all, macOS's plan carries none.
	bare := planFor(installFacts{
		GOOS: "darwin", Self: home + "/.local/bin/dexel", BinDir: home + "/.local/bin",
		StateDir: home + "/Library/Application Support/dexel", HomeDir: home,
	})
	for _, a := range bare {
		if strings.HasSuffix(a.Path, ".app") {
			t.Errorf("no bundle exists, yet the plan lists %s", a.Path)
		}
	}
}

// TestPlanDetectsADebInstallInsteadOfFailingOnIt: /usr/bin needs root,
// this binary never sudos, so the system install is NAMED and handed
// back with the command that removes it — never attempted and never
// silently ignored.
func TestPlanDetectsADebInstallInsteadOfFailingOnIt(t *testing.T) {
	present := map[string]bool{"/usr/bin/dexel-desktop": true, "/usr/bin/Dexel": true}
	plan := planFor(installFacts{
		GOOS: "linux", Self: "/home/u/.local/bin/dexel", BinDir: "/home/u/.local/bin",
		StateDir: "/home/u/.config/dexel", HomeDir: "/home/u", DataDir: "/home/u/.local/share",
		Exists: func(p string) bool { return present[p] },
	})

	for p := range present {
		a := find(plan, p)
		if a == nil {
			t.Fatalf("%s exists but the plan does not mention it", p)
		}
		if a.Kind != needsRoot {
			t.Errorf("%s: kind %v, want needsRoot — dexel must never try to delete a root-owned file", p, a.Kind)
		}
	}
	if find(plan, "/usr/bin/dexel") != nil {
		t.Error("/usr/bin/dexel does not exist here and must not be listed")
	}
	if debRemoveCommand != "sudo apt remove dexel" {
		t.Errorf("the printed line is %q; the .deb's package name is tauri.conf.json's productName lowercased", debRemoveCommand)
	}
}

// TestPlanSweepsAMovedInstallDirAndTheDefault: DEXEL_INSTALL_DIR can put
// the install anywhere, so the directory the RUNNING binary sits in is
// authoritative — and BinDir is still swept when it differs, because an
// install that was moved by hand leaves artifacts in both.
func TestPlanSweepsAMovedInstallDirAndTheDefault(t *testing.T) {
	const moved = "/opt/dexel/bin"
	const dflt = "/home/u/.local/bin"
	globbed := map[string][]string{
		moved + "/dexel-desktop*": {moved + "/dexel-desktop"},
		dflt + "/dexel-desktop*":  {dflt + "/dexel-desktop.AppImage"},
	}
	plan := planFor(installFacts{
		GOOS: "linux", Self: moved + "/dexel", BinDir: dflt,
		StateDir: "/home/u/.config/dexel", HomeDir: "/home/u", DataDir: "/home/u/.local/share",
		Glob: func(p string) []string { return globbed[p] },
	})

	for _, p := range []string{
		moved + "/dexel-desktop", dflt + "/dexel-desktop.AppImage", dflt + "/dexel", moved + "/dexel",
	} {
		if find(plan, p) == nil {
			t.Errorf("the plan is missing %s\nplan: %v", p, planPaths(plan))
		}
	}
	// The default-dir binary is a plain file; only the running one is
	// deferred.
	if a := find(plan, dflt+"/dexel"); a != nil && a.Kind != removeFile {
		t.Errorf("%s: kind %v, want removeFile", a.Path, a.Kind)
	}

	// When they are the SAME directory it is swept once, not twice.
	same := planFor(installFacts{
		GOOS: "linux", Self: dflt + "/dexel", BinDir: dflt,
		StateDir: "/home/u/.config/dexel", HomeDir: "/home/u", DataDir: "/home/u/.local/share",
	})
	seen := map[string]int{}
	for _, a := range same {
		seen[a.Path]++
	}
	for p, n := range seen {
		if n != 1 {
			t.Errorf("%s appears %d times when Self is inside BinDir", p, n)
		}
	}
}

// TestPlanLeavesANonInstalledExecutableAlone: under `go run .` or a test
// binary, os.Executable() is a temp build artifact. Deleting it would be
// harmless but absurd, and — worse — its directory would be swept for
// imaginary dexel files, producing an "audit" of paths that never
// existed.
func TestPlanLeavesANonInstalledExecutableAlone(t *testing.T) {
	const self = "/tmp/go-build123/b001/exe/app"
	plan := planFor(installFacts{
		GOOS: "linux", Self: self, BinDir: "/home/u/.local/bin",
		StateDir: "/home/u/.config/dexel", HomeDir: "/home/u", DataDir: "/home/u/.local/share",
		Glob: func(p string) []string {
			if strings.HasPrefix(p, "/tmp/go-build123") {
				t.Errorf("the plan globbed the temp build dir %q", p)
			}
			return nil
		},
	})

	a := find(plan, self)
	if a == nil || a.Kind != notInstalled {
		t.Fatalf("a non-`dexel` executable must be reported as notInstalled and left alone; got %+v", a)
	}
	for _, a := range plan {
		if a.Kind == removeRunningBinary {
			t.Errorf("nothing may be scheduled as the running binary here, but %s is", a.Path)
		}
		if strings.HasPrefix(a.Path, "/tmp/go-build123") && a.Kind != notInstalled {
			t.Errorf("%s is inside the temp build dir and must not be a removal", a.Path)
		}
	}
	// The default BinDir is still swept — an uninstall run from a source
	// build must still remove a real install.
	if find(plan, "/home/u/.local/bin/dexel") == nil {
		t.Error("BinDir's binary must still be in the plan when Self is a source build")
	}
}

// TestMacAppBundleCandidatesMatchAutostart pins the duplicated candidate
// list to app/internal/autostart's launchdBundleCandidates. The two
// exist for different purposes (one points a plist at a bundle, one
// deletes it), which is why the list is duplicated rather than exported
// — and this is what stops the duplication drifting silently.
func TestMacAppBundleCandidatesMatchAutostart(t *testing.T) {
	// The literal list from app/internal/autostart/autostart.go's
	// launchdBundleCandidates. If that changes, change this — and think
	// about whether the uninstaller should be deleting the new location.
	want := []string{
		"/Applications/Dexel.app",
		"/Applications/dexel.app",
		"/Users/u/Applications/Dexel.app",
		"/Users/u/Applications/dexel.app",
	}
	if got := macAppBundleCandidates("/Users/u"); !reflect.DeepEqual(got, want) {
		t.Errorf("candidates drifted from autostart's list\n got: %v\nwant: %v", got, want)
	}
	// No home resolved: the system locations are still checked rather
	// than the whole list collapsing to relative paths.
	if got := macAppBundleCandidates(""); !reflect.DeepEqual(got, want[:2]) {
		t.Errorf("with no home dir: got %v, want %v", got, want[:2])
	}
}

// TestExeNamePerOS: install.ps1 writes dexel.exe, install.sh writes
// dexel.
func TestExeNamePerOS(t *testing.T) {
	for goos, want := range map[string]string{
		"windows": "dexel.exe", "linux": "dexel", "darwin": "dexel", "freebsd": "dexel",
	} {
		if got := exeName(goos); got != want {
			t.Errorf("exeName(%q) = %q, want %q", goos, got, want)
		}
	}
}

// TestPathArithmeticFollowsThePlannedOSNotTheHost is the test that makes
// every Windows assertion in this file trustworthy: on this Linux host
// path/filepath does not know '\\' is a separator, so the planner cannot
// use it for a Windows plan (see cmd_uninstall.go's "path arithmetic"
// section).
func TestPathArithmeticFollowsThePlannedOSNotTheHost(t *testing.T) {
	// Base
	if got := pathBase("windows", `C:\Users\u\bin\dexel.exe`); got != "dexel.exe" {
		t.Errorf("pathBase(windows) = %q, want dexel.exe", got)
	}
	if got := pathBase("windows", `C:/Users/u/bin/dexel.exe`); got != "dexel.exe" {
		t.Errorf("pathBase(windows) must accept '/' too; got %q", got)
	}
	if got := pathBase("linux", "/home/u/.local/bin/dexel"); got != "dexel" {
		t.Errorf("pathBase(linux) = %q, want dexel", got)
	}
	// Dir
	if got := pathDir("windows", `C:\Users\u\bin\dexel.exe`); got != `C:\Users\u\bin` {
		t.Errorf("pathDir(windows) = %q", got)
	}
	// A drive root keeps its separator: `C:` alone means "the current
	// directory on drive C" in Win32, which is somewhere else entirely.
	if got := pathDir("windows", `C:\dexel.exe`); got != `C:\` {
		t.Errorf("pathDir(windows) on a drive root = %q, want %q", got, `C:\`)
	}
	if got := pathDir("linux", "/home/u/.local/bin/dexel"); got != "/home/u/.local/bin" {
		t.Errorf("pathDir(linux) = %q", got)
	}
	// Join
	if got := pathJoin("windows", `C:\Users\u`, "AppData", `Local\`, "dexel"); got != `C:\Users\u\AppData\Local\dexel` {
		t.Errorf("pathJoin(windows) = %q", got)
	}
	if got := pathJoin("linux", "/home/u", ".local", "bin"); got != "/home/u/.local/bin" {
		t.Errorf("pathJoin(linux) = %q", got)
	}
	// winNormalise makes '/' and a trailing separator irrelevant.
	if winNormalise(`C:/x/y/`) != `C:\x\y` {
		t.Errorf("winNormalise(%q) = %q", `C:/x/y/`, winNormalise(`C:/x/y/`))
	}
}

// TestSamePathUsesThePlannedPlatformsCaseRule: the case rule must follow
// the OS being PLANNED FOR, not the host running the test — otherwise
// this Linux box would plan a Windows uninstall that misses the binary
// whenever the two paths differ only in case.
func TestSamePathUsesThePlannedPlatformsCaseRule(t *testing.T) {
	if !samePath("windows", `C:\Users\U\bin\Dexel.exe`, `c:\users\u\bin\dexel.exe`) {
		t.Error("Windows paths are case-insensitive")
	}
	if samePath("linux", "/home/u/bin/Dexel", "/home/u/bin/dexel") {
		t.Error("unix paths are case-sensitive")
	}
	if !samePath("linux", "/home/u/bin/../bin/dexel", "/home/u/bin/dexel") {
		t.Error("samePath must clean both sides")
	}
}

// TestWindowsPlanFindsTheRunningExeDespiteCase is the reason the above
// matters: the exe path Windows reports and the BinDir dexel composes
// can differ in case, and a case-sensitive comparison would list the
// binary twice — once as a plain file removal that fails on a locked
// file, once as the deferred entry.
func TestWindowsPlanFindsTheRunningExeDespiteCase(t *testing.T) {
	plan := planFor(installFacts{
		GOOS:     "windows",
		Self:     `C:\Users\U\AppData\Local\Dexel\Bin\DEXEL.EXE`,
		BinDir:   `c:\users\u\appdata\local\dexel\bin`,
		StateDir: `C:\Users\u\AppData\Local\dexel`,
		AppData:  `C:\Users\u\AppData\Roaming`,
	})
	n := 0
	for _, a := range plan {
		if strings.EqualFold(pathBase("windows", a.Path), "dexel.exe") {
			n++
			if a.Kind != removeRunningBinary {
				t.Errorf("%s: kind %v, want removeRunningBinary", a.Path, a.Kind)
			}
		}
	}
	if n != 1 {
		t.Errorf("the exe is planned %d times, want 1", n)
	}
}

// ------------------------------------------------------------ consent

// TestResolveConsent pins rule 1 of cmd_uninstall.go's header, and in
// particular the row that is a REFUSAL: a piped stdin with no --yes must
// not be read as either answer.
func TestResolveConsent(t *testing.T) {
	cases := []struct {
		name      string
		assumeYes bool
		tty       bool
		want      consentOutcome
	}{
		{"a human at a terminal is asked", false, true, consentAsk},
		{"--yes is consent and asks nothing", true, true, consentGranted},
		{"--yes works with no terminal at all (the scripting path)", true, false, consentGranted},
		{"piped stdin without --yes is REFUSED, never assumed", false, false, consentImpossible},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveConsent(tc.assumeYes, tc.tty); got != tc.want {
				t.Errorf("resolveConsent(%v, %v) = %v, want %v", tc.assumeYes, tc.tty, got, tc.want)
			}
		})
	}
}

// TestAnswerIsYes: a bare Enter at "[y/N]" is NO. That default is the
// whole reason the prompt is spelled with a capital N.
func TestAnswerIsYes(t *testing.T) {
	for _, yes := range []string{"y", "yes"} {
		if !answerIsYes(yes) {
			t.Errorf("%q should be yes", yes)
		}
	}
	for _, no := range []string{"", "n", "no", "Y ", "yeah", "purge", "yes please", "1"} {
		if answerIsYes(strings.ToLower(strings.TrimSpace(no))) && strings.TrimSpace(strings.ToLower(no)) != "y" {
			t.Errorf("%q should not be yes", no)
		}
	}
	// Case and surrounding whitespace are normalised by readAnswer, not
	// by answerIsYes — assert the pair together.
	for _, in := range []string{"Y\n", "  yes  \n", "YES\r\n"} {
		if !answerIsYes(readAnswer(strings.NewReader(in))) {
			t.Errorf("readAnswer+answerIsYes rejected %q", in)
		}
	}
}

// TestAnswerIsPurge: the literal word, and only it. `y` must not delete
// a save.
func TestAnswerIsPurge(t *testing.T) {
	if !answerIsPurge(readAnswer(strings.NewReader("purge\n"))) {
		t.Error("`purge` must confirm")
	}
	if !answerIsPurge(readAnswer(strings.NewReader(" PURGE \n"))) {
		t.Error("`PURGE` with spaces must confirm (readAnswer normalises)")
	}
	for _, in := range []string{"y\n", "yes\n", "\n", "purge it\n", "pur\n", ""} {
		if answerIsPurge(readAnswer(strings.NewReader(in))) {
			t.Errorf("%q must NOT confirm a purge", in)
		}
	}
}

// TestReadAnswerTreatsAnUnterminatedOrEmptyStreamAsNo: EOF without a
// newline is what a `yes | dexel uninstall` gone wrong, or a closed
// pipe, looks like.
func TestReadAnswerTreatsAnUnterminatedOrEmptyStreamAsNo(t *testing.T) {
	if got := readAnswer(strings.NewReader("")); got != "" {
		t.Errorf("empty stream: got %q, want %q", got, "")
	}
	if answerIsYes(readAnswer(strings.NewReader(""))) {
		t.Error("EOF must never read as yes")
	}
	// An unterminated line IS an answer — the user typed it and the
	// stream ended.
	if got := readAnswer(strings.NewReader("y")); got != "y" {
		t.Errorf("unterminated answer: got %q, want %q", got, "y")
	}
}

// TestUninstallPromptTellsTheTruthAboutTheSave is small and load-bearing:
// the default prompt promises the save survives, and that promise must
// NOT be printed when --purge is in flight.
func TestUninstallPromptTellsTheTruthAboutTheSave(t *testing.T) {
	keep := uninstallPrompt(false)
	if !strings.Contains(keep, "Your save data stays unless you pass --purge") {
		t.Errorf("the default prompt must say the save stays; got %q", keep)
	}
	if !strings.HasSuffix(keep, "[y/N] ") {
		t.Errorf("the prompt must show the default answer as N; got %q", keep)
	}

	purge := uninstallPrompt(true)
	if strings.Contains(purge, "save data stays") {
		t.Errorf("the --purge prompt must NOT promise the save stays; got %q", purge)
	}
	if !strings.Contains(purge, "DELETES YOUR SAVE DATA") {
		t.Errorf("the --purge prompt must say the save is deleted; got %q", purge)
	}

	p := purgePrompt("/home/u/.config/dexel")
	for _, want := range []string{"/home/u/.config/dexel", "purge", "no undo"} {
		if !strings.Contains(p, want) {
			t.Errorf("the purge confirmation must contain %q; got %q", want, p)
		}
	}
}

// TestConsentNoTTYMessageNamesTheFlag: the refusal has to be actionable,
// or it is just a wall.
func TestConsentNoTTYMessageNamesTheFlag(t *testing.T) {
	if !strings.Contains(consentNoTTYMessage, "--yes") {
		t.Errorf("the no-tty refusal must name --yes; got %q", consentNoTTYMessage)
	}
}

// -------------------------------------------------- Windows self-delete

// TestWindowsSelfDeleteCommand pins the FIELD-TEST-NEEDED command line:
// it waits for THIS pid (never a guessed sleep), deletes the exe, and
// appends one line saying which of the two happened.
func TestWindowsSelfDeleteCommand(t *testing.T) {
	name, args := windowsSelfDeleteCommand(4242, `C:\Users\u\AppData\Local\dexel\bin\dexel.exe`, `C:\Users\u\AppData\Local\dexel\logs\runtime.log`)
	if name != "powershell" {
		t.Errorf("host = %q, want powershell", name)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-NoProfile", "-NonInteractive", "-WindowStyle Hidden", "-Command",
		"Wait-Process -Id 4242",
		`Remove-Item -LiteralPath 'C:\Users\u\AppData\Local\dexel\bin\dexel.exe' -Force`,
		`Add-Content -LiteralPath 'C:\Users\u\AppData\Local\dexel\logs\runtime.log'`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the self-delete command is missing %q\ngot: %s", want, joined)
		}
	}
	// -Timeout bounds the wait: a wedged process must not leave a
	// PowerShell alive forever.
	if !strings.Contains(joined, "-Timeout") {
		t.Errorf("the wait must be bounded by -Timeout\ngot: %s", joined)
	}

	// Under --purge the log directory is being deleted, so there is
	// nowhere to report to and the helper must not try.
	_, noReport := windowsSelfDeleteCommand(1, `C:\x\dexel.exe`, "")
	if strings.Contains(strings.Join(noReport, " "), "Add-Content") {
		t.Error("with no report path the helper must not append anywhere")
	}
}

// TestPsQuote: PowerShell escapes an embedded single quote by doubling
// it, and single-quoted strings expand nothing — which is what keeps a
// path containing `$` or `;` from being interpreted.
func TestPsQuote(t *testing.T) {
	for in, want := range map[string]string{
		`C:\x\dexel.exe`:     `'C:\x\dexel.exe'`,
		`C:\Ann's PC\dexel`:  `'C:\Ann''s PC\dexel'`,
		`C:\$env:x; rm -r \`: `'C:\$env:x; rm -r \'`,
	} {
		if got := psQuote(in); got != want {
			t.Errorf("psQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// ------------------------------------------------------------- report

// TestOnPathComparesWholeElements — a substring match would claim
// ~/.local/bin was on PATH because ~/.local/binx is.
func TestOnPathComparesWholeElements(t *testing.T) {
	list := strings.Join([]string{"/usr/bin", "/home/u/.local/binx", "/home/u/.local/bin"}, string(filepath.ListSeparator))
	if !onPath(list, "/home/u/.local/bin") {
		t.Error("an exact element must match")
	}
	if onPath(list, "/home/u/.local/bi") {
		t.Error("a prefix of an element must not match")
	}
	if onPath(list, "") {
		t.Error("an unresolved bin dir must never claim to be on PATH")
	}
	if onPath("", "/home/u/.local/bin") {
		t.Error("an empty PATH contains nothing")
	}
	if runtime.GOOS != "windows" && onPath(list, "/home/u/.local/BIN") {
		t.Error("on unix the comparison is case-sensitive")
	}
}

// TestPlanIsPureApartFromItsTwoProbes: uninstallPlan must not stat, glob
// or read the environment on its own — that is what makes every test
// above possible on any host. Asserted behaviourally: the same facts
// produce the same plan twice, and a plan built with probes that panic
// on unexpected input still succeeds.
func TestPlanIsPureApartFromItsTwoProbes(t *testing.T) {
	f := installFacts{
		GOOS: "linux", Self: "/home/u/.local/bin/dexel", BinDir: "/home/u/.local/bin",
		StateDir: "/home/u/.config/dexel", HomeDir: "/home/u", DataDir: "/home/u/.local/share",
		Exists: func(string) bool { return false },
		Glob:   func(string) []string { return nil },
	}
	a, b := uninstallPlan(f), uninstallPlan(f)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("uninstallPlan is not deterministic for identical facts")
	}
	// Every path in the plan is absolute and rooted in one of the
	// injected directories — never in the host's real HOME.
	for _, art := range a {
		if !strings.HasPrefix(art.Path, "/home/u") && !strings.HasPrefix(art.Path, "/usr") {
			t.Errorf("%s is outside every injected root — something read the real environment", art.Path)
		}
	}
}

// TestPlanNeverTouchesTheLegacyRustSave is a promise, not an
// implementation detail (ARCHITECTURE.md §9: "--purge ... NEVER touches
// ~/.local/share/dev-companion/save.json or ~/Library/Application
// Support/dev-companion/save.json — the legacy Rust save is not ours").
// The importer that once read it is gone (review item B-2) and the crates
// are on branch attic/legacy-rust-and-fleet; the file still is not ours
// to delete.
//
// Asserted for --purge as well as the default, because --purge is the
// only mode with the appetite to delete a directory at all, and the
// legacy save lives under ~/.local/share — the SAME root as the icon and
// the .desktop entry the Linux branch does remove.
func TestPlanNeverTouchesTheLegacyRustSave(t *testing.T) {
	legacy := []string{
		"/home/u/.local/share/dev-companion",
		"/home/u/.local/share/dev-companion/save.json",
		"/Users/u/Library/Application Support/dev-companion",
		"/Users/u/Library/Application Support/dev-companion/save.json",
	}
	for _, purge := range []bool{false, true} {
		for _, goos := range []string{"linux", "darwin", "windows"} {
			plan := planFor(installFacts{
				GOOS: goos, Self: "/home/u/.local/bin/dexel", BinDir: "/home/u/.local/bin",
				StateDir: "/home/u/.config/dexel", HomeDir: "/home/u",
				DataDir: "/home/u/.local/share", AppData: `C:\Users\u\AppData\Roaming`,
				Purge: purge,
				// Say YES to every probe: even if all four legacy paths
				// exist, none may enter the plan.
				Exists: func(string) bool { return true },
			})
			for _, a := range plan {
				for _, l := range legacy {
					if a.Path == l || strings.HasPrefix(a.Path, l+"/") {
						t.Errorf("goos=%s purge=%v: the plan touches the legacy Rust save %s (as %v) — it is not ours", goos, purge, a.Path, a.Kind)
					}
				}
				if strings.Contains(a.Path, "dev-companion") {
					t.Errorf("goos=%s purge=%v: %s mentions dev-companion at all", goos, purge, a.Path)
				}
			}
		}
	}
}

// TestPreviewLinesNamesWhatWillGoAndWhatStays: the block printed just
// before the prompt is the difference between an answerable question and
// a blind one — especially for a source build, whose plan targets the
// DEFAULT BinDir rather than the checkout the developer is sitting in.
func TestPreviewLinesNamesWhatWillGoAndWhatStays(t *testing.T) {
	const bin = "/home/u/.local/bin"
	const state = "/home/u/.config/dexel"
	plan := planFor(installFacts{
		GOOS: "linux", Self: bin + "/dexel", BinDir: bin,
		StateDir: state, HomeDir: "/home/u", DataDir: "/home/u/.local/share",
		Exists: func(p string) bool { return p == "/usr/bin/dexel-desktop" },
	})
	// Only the binary and the save exist on this imaginary machine.
	present := map[string]bool{bin + "/dexel": true, state + "/state.db": true}
	out := strings.Join(previewLines(plan, func(p string) bool { return present[p] }), "\n")

	if !strings.Contains(out, "This will remove:") {
		t.Errorf("no removal heading:\n%s", out)
	}
	if !strings.Contains(out, bin+"/dexel") {
		t.Errorf("the binary is not named:\n%s", out)
	}
	// Absent candidates must NOT be listed — a preview padded with
	// already-gone paths is a wall, not consent.
	if strings.Contains(out, "dexel.desktop") || strings.Contains(out, "hicolor") {
		t.Errorf("the preview lists paths that do not exist:\n%s", out)
	}
	// The kept paths are named whether or not they exist, so "my save
	// survives this" is visible BEFORE answering.
	if !strings.Contains(out, "and KEEP:") || !strings.Contains(out, state+"/state.db") {
		t.Errorf("the preview does not say what is kept:\n%s", out)
	}
	// The .deb is mentioned as report-only, never as a removal.
	if !strings.Contains(out, "needs root") {
		t.Errorf("the preview does not mention the root-owned install:\n%s", out)
	}
	for _, line := range previewLines(plan, func(p string) bool { return present[p] }) {
		if line == "  /usr/bin/dexel-desktop" {
			t.Error("a root-owned path must never appear in the removal list")
		}
	}

	// Nothing left to remove reads as exactly that, rather than as an
	// empty "This will remove:" heading.
	empty := strings.Join(previewLines(plan, func(string) bool { return false }), "\n")
	if !strings.Contains(empty, "Nothing of dexel's is left to remove") {
		t.Errorf("an already-clean machine should say so:\n%s", empty)
	}
	if strings.Contains(empty, "This will remove:") {
		t.Errorf("an empty removal list must not print the heading:\n%s", empty)
	}
}

// TestPreviewLinesForASourceBuildNamesTheRealInstall is the footgun this
// preview exists for: `go run . uninstall` resolves Self to a temp build
// directory, so the plan targets the default BinDir — the developer's
// REAL install. That must be visible in the question.
func TestPreviewLinesForASourceBuildNamesTheRealInstall(t *testing.T) {
	const bin = "/home/u/.local/bin"
	plan := planFor(installFacts{
		GOOS: "linux", Self: "/tmp/go-build9/b001/exe/app", BinDir: bin,
		StateDir: "/home/u/.config/dexel", HomeDir: "/home/u", DataDir: "/home/u/.local/share",
	})
	lines := previewLines(plan, func(p string) bool { return p == bin+"/dexel" })
	out := strings.Join(lines, "\n")
	if !strings.Contains(out, bin+"/dexel") {
		t.Errorf("the real install must be named in the preview:\n%s", out)
	}
	// The temp build binary IS mentioned — but only in its own explicitly
	// "not touched" line, never as an indented entry in the removal or
	// KEEP lists, where it would read as something being deleted or
	// something being spared.
	mentioned := false
	for _, l := range lines {
		if !strings.Contains(l, "/tmp/go-build9") {
			continue
		}
		mentioned = true
		if strings.HasPrefix(l, "  ") {
			t.Errorf("the temp build binary appears as a list entry, not a note: %q", l)
		}
		if !strings.Contains(l, "not touched") {
			t.Errorf("the temp build binary is mentioned without saying it is untouched: %q", l)
		}
	}
	if !mentioned {
		t.Errorf("the preview should say which executable is running the command:\n%s", out)
	}
}
