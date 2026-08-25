// cmd_uninstall.go — `dexel uninstall [--purge] [--yes]`
// (docs/production-runtime/ARCHITECTURE.md §9 and Decision 3,
// docs/production-runtime/MIGRATION_PLAN.md §PR-7).
//
// This is the exact REVERSAL of what install.sh and install.ps1 create,
// and it is written as a reversal rather than as a list of rm calls: the
// plan is a pure function (uninstallPlan) over a handful of injected
// facts about the machine, so "what would this delete?" is answerable in
// a unit test on any host, for every OS, without deleting anything. The
// installers are the specification, so each entry below cites the
// function in install.sh / install.ps1 that produced the file it removes.
//
// THE PROMISE this verb makes, and the reason it exists at all: nobody
// should have to be told "now hand-delete these five paths". The README
// and both installers still PRINT the manual list (a user who deleted
// the binary before reading this needs it), but the supported answer is
// one command that leaves nothing behind.
//
// THREE RULES it obeys.
//
//  1. CONSENT, twice, and never implied. Removing software is not
//     reversible by an undo, and the save file is a companion's whole
//     life. A plain `uninstall` asks once; `--purge` asks a SECOND time
//     and demands the literal word `purge` (ARCHITECTURE.md §9), because
//     "y" is a reflex and typing a word is a decision. `--yes` is for
//     scripts and skips both — an explicit flag is itself consent.
//     A non-tty stdin without `--yes` REFUSES rather than assuming
//     either answer: silently reading EOF as "no" would make a piped
//     uninstall look like it ran, and reading it as "yes" would delete
//     someone's save because they forgot a flag.
//
//  2. IDEMPOTENT, and honest about it. Every artifact is reported as
//     `removed` or `already absent`, and a second run is a clean exit 0
//     that removed nothing. "Already gone" is not an error; a
//     permission failure is.
//
//  3. NEVER ROOT, never silently partial. This binary never sudos
//     (ARCHITECTURE.md's no-sudo posture, install.sh's "Never uses
//     sudo"). A system-wide install from the release's .deb lives in
//     /usr/bin and cannot be removed without root, so it is DETECTED and
//     the exact `sudo apt remove dexel` line is printed — a failure
//     there would be an uninstall that stopped halfway for a file it was
//     never going to own.
//
// THE PLATFORM ASYMMETRY worth stating up front, because it is the one
// place the code cannot be uniform: on unix a running process keeps its
// image after the inode is unlinked, so `dexel` deletes its own binary
// as the last step and finishes normally. On Windows the file is held
// open by the loader and `Remove-Item` on it fails — so a detached
// PowerShell is spawned that waits for THIS pid to exit and then deletes
// the exe (windowsSelfDeleteCommand). That pattern is authored and
// unit-tested here, and honestly marked FIELD-TEST-NEEDED in the same
// style desktop/README.md uses for code nobody on this project can run.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jawwadzafar/dexel/app/internal/autostart"
	"github.com/jawwadzafar/dexel/app/internal/store"
)

// ---------------------------------------------------------------------
// the reversal list
// ---------------------------------------------------------------------

// removalKind is what should HAPPEN to one path. The two "not a
// removal" kinds are as important as the two removals: an uninstall that
// prints only what it deleted leaves the user guessing about everything
// it did not.
type removalKind int

const (
	// removeFile is os.Remove — one file, never a tree.
	removeFile removalKind = iota
	// removeTree is os.RemoveAll — a directory we created (cache/, an
	// installed .app bundle, and the whole StateDir under --purge).
	removeTree
	// removeRunningBinary is the executable running this very process.
	// Deferred to LAST on every OS, and on Windows handed to a detached
	// helper because the file is locked (see this file's header).
	removeRunningBinary
	// keepAndPrint is never touched — reported so the user knows exactly
	// what stayed and where (ARCHITECTURE.md §9 step 5: "PRINT, do not
	// delete: state.db, config.json, logs/").
	keepAndPrint
	// needsRoot is a system-wide artifact this binary refuses to try:
	// detected, named, and handed to the user with the command that
	// actually removes it.
	needsRoot
	// notInstalled is the running executable when it is NOT an installed
	// dexel — a `go run .` or test binary. A separate kind from
	// keepAndPrint on purpose: "your save is deliberately preserved" and
	// "this temp build is not mine to delete" are different facts, and
	// listing the second one under a "KEPT" heading beside the first is
	// the kind of blurred report that makes a user distrust both.
	notInstalled
)

// artifact is one line of the plan: what to do, to which path, and the
// human-readable name of the thing — the report prints all three, so a
// user can audit the removal without knowing the installers.
type artifact struct {
	Kind removalKind
	Path string
	What string
}

// installFacts is everything uninstallPlan needs to know about the
// machine. Every field is injected rather than read from the process,
// for the reason app/internal/paths/paths.go and cmd_lifecycle.go's
// desktopAppCandidates both give: a single `go test` binary, on whatever
// host happens to run it, must be able to drive every platform's branch
// with plain temp directories. The two function fields are the only
// filesystem access the plan performs, and they exist because two
// entries are DISCOVERED rather than known — an installed .app bundle,
// and whatever `dexel-desktop*` files the installer left beside the
// binary.
type installFacts struct {
	GOOS     string // runtime.GOOS
	Self     string // os.Executable() — "" if it could not be resolved
	BinDir   string // paths.BinDir() — "" if it could not be resolved
	StateDir string // paths.StateDir()
	HomeDir  string // os.UserHomeDir() — macOS ~/Applications only
	DataDir  string // $XDG_DATA_HOME or ~/.local/share — Linux desktop integration
	AppData  string // %APPDATA% — the Windows Start Menu
	Purge    bool   // --purge: the StateDir goes too

	// Exists reports whether a path is there at all (os.Stat in
	// production). Used ONLY for the discovered entries; a fixed,
	// known-location artifact is always listed and reported as
	// "already absent" when missing, because a plan that hides what it
	// looked for cannot be audited.
	Exists func(string) bool
	// Glob is filepath.Glob with its error dropped — a malformed
	// pattern here would be a programming error, not a machine state.
	Glob func(string) []string
}

// exeName is the installed binary's filename per OS. install.ps1 writes
// `dexel.exe`; install.sh writes `dexel`.
func exeName(goos string) string {
	if goos == "windows" {
		return "dexel.exe"
	}
	return "dexel"
}

// ---------------------------------------------------------------------
// path arithmetic for the OS being PLANNED FOR
//
// path/filepath is compiled for the HOST. On this Linux dev box
// filepath.Base(`C:\...\dexel.exe`) returns the WHOLE string, because
// '\' is not a separator there — so a planner built on filepath would
// produce a correct plan on real Windows and a silently wrong one in
// every test that asserts the Windows branch. That is precisely the
// "logic hidden behind a runtime.GOOS branch nobody can test" this
// repo's convention rejects (app/internal/paths/paths.go's doc comment).
//
// So the three operations the plan needs are keyed off the planned goos,
// and Windows' rule — accept either separator, write '\' — is asserted
// from here rather than assumed. Three small functions, and the whole
// Windows reversal list becomes checkable on Linux.
// ---------------------------------------------------------------------

// winSeps: Windows accepts both, and every Win32 path API normalises
// them, so a %APPDATA% or os.Executable() carrying '/' must not defeat a
// comparison.
const winSeps = `\/`

// winNormalise makes two Windows paths comparable: one separator, no
// trailing one.
func winNormalise(p string) string {
	return strings.TrimRight(strings.ReplaceAll(p, "/", `\`), `\`)
}

// pathBase is filepath.Base for the planned platform.
func pathBase(goos, p string) string {
	if goos != "windows" {
		return filepath.Base(p)
	}
	p = strings.TrimRight(p, winSeps)
	if i := strings.LastIndexAny(p, winSeps); i >= 0 {
		return p[i+1:]
	}
	return p
}

// pathDir is filepath.Dir for the planned platform. `C:\dexel.exe`
// yields `C:\`, not `C:` — the latter means "the current directory on
// drive C" in Win32, which is a different place.
func pathDir(goos, p string) string {
	if goos != "windows" {
		return filepath.Dir(p)
	}
	q := strings.TrimRight(p, winSeps)
	i := strings.LastIndexAny(q, winSeps)
	if i < 0 {
		return "."
	}
	if i > 0 && q[i-1] == ':' {
		return q[:i+1]
	}
	return q[:i]
}

// pathJoin is filepath.Join for the planned platform. The first element
// keeps its leading separator (a Windows absolute path starts with a
// drive letter, so there is normally none, but a UNC-ish `\\host\share`
// must not be mangled into a relative path).
func pathJoin(goos string, elem ...string) string {
	if goos != "windows" {
		return filepath.Join(elem...)
	}
	out := ""
	for _, e := range elem {
		if e == "" {
			continue
		}
		if out == "" {
			out = strings.TrimRight(e, winSeps)
			continue
		}
		out += `\` + strings.Trim(e, winSeps)
	}
	return out
}

// samePath compares two paths for "the same file", with the case rule of
// the platform being planned for — not of the host running the test.
// Windows filesystems are case-insensitive; unix ones are not (macOS's
// default APFS volume is the awkward middle case, and is treated as
// case-sensitive here on purpose: a false "these differ" only costs one
// extra already-absent line in the report, while a false "these are the
// same" would skip a real removal).
func samePath(goos, a, b string) bool {
	if goos == "windows" {
		return strings.EqualFold(winNormalise(a), winNormalise(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// macAppBundleCandidates is where an installed Dexel.app might be.
//
// Composed here rather than imported from app/internal/autostart (whose
// launchdBundleCandidates is the same list for a different purpose):
// duplicating four strings is cheaper than exporting a helper from a
// package whose own doc comment scopes it to autostart mechanisms, and
// cmd_lifecycle.go's desktopAppCandidates already sets the precedent
// that the CLI composes its own candidate lists from injected roots.
// TestMacAppBundleCandidatesMatchAutostart pins the two lists to each
// other so the duplication cannot drift silently.
//
// Both capitalisations are listed for the reason autostart gives: the
// bundle is mid-rename from `dexel.app` to `Dexel.app`, and a
// case-sensitive volume is a supported if unusual configuration.
func macAppBundleCandidates(homeDir string) []string {
	out := []string{"/Applications/Dexel.app", "/Applications/dexel.app"}
	if homeDir != "" {
		out = append(out,
			filepath.Join(homeDir, "Applications", "Dexel.app"),
			filepath.Join(homeDir, "Applications", "dexel.app"))
	}
	return out
}

// debInstalledPaths are the files the release's .deb drops into /usr/bin
// (desktop/src-tauri/tauri.conf.json: mainBinaryName `dexel-desktop`,
// externalBin `binaries/Dexel`, plus the CLI under its own name if a
// future package ships it). install.sh deliberately never uses that .deb
// — "dpkg needs root, and this installer does not sudo" — so anything
// found here came from `apt`/`dpkg` and must be removed by it.
var debInstalledPaths = []string{
	"/usr/bin/dexel-desktop",
	"/usr/bin/Dexel",
	"/usr/bin/dexel",
	"/usr/local/bin/dexel-desktop",
}

// debRemoveCommand is the exact line printed when any of the above is
// found. The package name is tauri.conf.json's productName lowercased,
// which is what tauri-bundler's debian target writes into the control
// file.
const debRemoveCommand = "sudo apt remove dexel"

// uninstallPlan builds the ordered reversal list. Pure apart from
// f.Exists / f.Glob.
//
// ORDER IS PART OF THE CONTRACT: the running binary is always the LAST
// entry (see this file's header on why), and the state directory comes
// after the desktop artifacts so a purge cannot destroy the log a later
// failure would be reported to.
func uninstallPlan(f installFacts) []artifact {
	var out []artifact
	add := func(k removalKind, path, what string) {
		if path == "" {
			return
		}
		out = append(out, artifact{Kind: k, Path: path, What: what})
	}

	exe := exeName(f.GOOS)
	selfIsInstalledBinary := f.Self != "" &&
		strings.EqualFold(pathBase(f.GOOS, f.Self), exe)

	// Where the installers put things. The directory the RUNNING binary
	// sits in is authoritative — DEXEL_INSTALL_DIR / $env:DEXEL_INSTALL_DIR
	// may have moved the whole install off BinDir, and the default is
	// then wrong — while BinDir is the documented default and is
	// included whenever it differs, so an install that was moved by hand
	// still gets its old directory swept.
	//
	// dir(Self) is included ONLY when Self is really an installed
	// `dexel`: under `go run .` or a test binary it is a temp build
	// directory, and listing plausible-but-imaginary dexel artifacts
	// inside it would be noise pretending to be an audit.
	var dirs []string
	if selfIsInstalledBinary {
		dirs = append(dirs, pathDir(f.GOOS, f.Self))
	}
	if f.BinDir != "" {
		known := false
		for _, d := range dirs {
			if samePath(f.GOOS, d, f.BinDir) {
				known = true
			}
		}
		if !known {
			dirs = append(dirs, f.BinDir)
		}
	}

	for _, d := range dirs {
		// The binary itself (install.sh step 7 / install.ps1's
		// Install-Binary) — unless it is the one running us, which is
		// the deferred last entry instead.
		bin := pathJoin(f.GOOS, d, exe)
		if !(f.Self != "" && samePath(f.GOOS, bin, f.Self)) {
			add(removeFile, bin, "the dexel binary")
		}
		// install.sh's install_desktop_app writes TWO files here: the
		// ~84 MB `dexel-desktop.AppImage` and the `dexel-desktop` shim
		// that adapts it to the name `dexel open` looks for. Matched by
		// name PREFIX rather than a hardcoded pair, so the
		// `dexel-desktop.exe` a future Windows bundle drops beside the
		// exe is swept by the same rule with no edit here — the same
		// "a rule about shape, not a list" reasoning cli.go's dispatch
		// applies to argv.
		for _, p := range f.Glob(pathJoin(f.GOOS, d, "dexel-desktop*")) {
			add(removeFile, p, "the optional desktop shell (installed beside the binary)")
		}
		if f.GOOS == "windows" {
			// install.ps1's Install-ShortcutIcon: the .lnk stores a PATH
			// to its icon, so the .ico had to live next to the exe.
			add(removeFile, pathJoin(f.GOOS, d, "dexel.ico"), "the Start Menu shortcut's icon")
		}
	}

	switch f.GOOS {
	case "windows":
		// install.ps1's Install-StartMenuShortcut, per-user Programs.
		// GetFolderPath('Programs') has no Go equivalent without a
		// syscall; %APPDATA%\Microsoft\Windows\Start Menu\Programs is
		// where it points, and is install.ps1's own documented fallback.
		if f.AppData != "" {
			add(removeFile,
				pathJoin(f.GOOS, f.AppData, "Microsoft", "Windows", "Start Menu", "Programs", "Dexel.lnk"),
				"the Start Menu shortcut")
		}
	case "darwin":
		// The CLI installer never creates a bundle on macOS ("this
		// installed the CLI only"), so a bundle present here was dragged
		// in from the .dmg by hand. It is still OURS — same product,
		// same identifier — so it is removed under the same consent,
		// and only when it actually exists: listing four candidate
		// bundles as "already absent" on every macOS uninstall would be
		// noise, not honesty.
		for _, b := range macAppBundleCandidates(f.HomeDir) {
			if f.Exists(b) {
				add(removeTree, b, "the Dexel.app bundle")
			}
		}
	default:
		// Linux, and every other unix (same "default is the Linux row"
		// convention as paths.stateDirFor).
		if f.DataDir != "" {
			// install.sh's write_desktop_file and install_icon.
			add(removeFile, pathJoin(f.GOOS, f.DataDir, "applications", "dexel.desktop"),
				"the app-grid launcher entry")
			add(removeFile, pathJoin(f.GOOS, f.DataDir, "icons", "hicolor", "128x128", "apps", "dexel.png"),
				"the launcher icon")
		}
		for _, p := range debInstalledPaths {
			if f.Exists(p) {
				add(needsRoot, p, "installed system-wide by the release's .deb")
			}
		}
	}

	// State. ARCHITECTURE.md §9 splits this precisely: the runtime's own
	// scratch (steps 4) goes without asking, because it is machine state
	// with no user meaning and a stale runtime.json/lock after the
	// binary is gone is pure litter; the save, the settings and the logs
	// (step 5) are PRINTED, never deleted, unless --purge.
	if f.Purge {
		add(removeTree, f.StateDir, "the whole state directory — config, save, logs, runtime files")
	} else {
		add(removeFile, pathJoin(f.GOOS, f.StateDir, "runtime.json"), "the runtime discovery file")
		add(removeFile, pathJoin(f.GOOS, f.StateDir, "runtime.lock"), "the single-runtime lock")
		add(removeTree, pathJoin(f.GOOS, f.StateDir, "cache"), "the update download cache")
		add(keepAndPrint, pathJoin(f.GOOS, f.StateDir, "state.db"), "your dexel's save — reinstalling resumes the same dexel")
		add(keepAndPrint, pathJoin(f.GOOS, f.StateDir, "config.json"), "your settings")
		add(keepAndPrint, pathJoin(f.GOOS, f.StateDir, "logs"), "the runtime log")
	}

	// Last, always.
	if f.Self != "" {
		if selfIsInstalledBinary {
			add(removeRunningBinary, f.Self, "the dexel binary (the executable running this command)")
		} else {
			add(notInstalled, f.Self,
				"the executable running this command — not an installed `dexel`, so it is left alone")
		}
	}
	return out
}

// ---------------------------------------------------------------------
// consent
// ---------------------------------------------------------------------

// consentOutcome is what the flags plus the shape of stdin MEAN, before
// any question is asked. Split out as a pure function for the same
// reason cli.go splits classify from dispatch: the interesting logic is
// the decision, and it should be testable without a terminal.
type consentOutcome int

const (
	// consentGranted: --yes was passed. An explicit flag is consent, and
	// no question is asked (this is the scripting path).
	consentGranted consentOutcome = iota
	// consentAsk: there is a human on the other end; ask.
	consentAsk
	// consentImpossible: stdin is not a terminal and --yes was not
	// passed. REFUSE — see rule 1 in this file's header.
	consentImpossible
)

func resolveConsent(assumeYes, stdinIsTTY bool) consentOutcome {
	switch {
	case assumeYes:
		return consentGranted
	case stdinIsTTY:
		return consentAsk
	default:
		return consentImpossible
	}
}

// uninstallPrompt is the first question. It states what happens to the
// save data, and it must state it CORRECTLY under --purge — a prompt
// that promises "your save data stays" while --purge is in flight would
// be the exact class of "looks right, isn't" output this project
// refuses.
func uninstallPrompt(purge bool) string {
	if purge {
		return "This removes dexel from this machine, and --purge DELETES YOUR SAVE DATA with it. Continue? [y/N] "
	}
	return "This removes dexel from this machine. Your save data stays unless you pass --purge. Continue? [y/N] "
}

// purgePrompt is the second question, asked only for --purge and only
// when there is a human to ask (ARCHITECTURE.md §9: "--purge removes the
// whole StateDir after typing the word purge"). Typing a word rather
// than a letter is the point: `y` is muscle memory.
func purgePrompt(stateDir string) string {
	return fmt.Sprintf("--purge will delete %s: the config, the logs, and the save that IS your dexel's whole life. There is no undo.\nType the word `purge` to confirm: ", stateDir)
}

// previewLines is the "here is what I am about to delete" block printed
// immediately before the first prompt. Only paths that ACTUALLY EXIST are
// listed: a preview padded with a dozen already-absent candidates is a
// wall, and a wall is not informed consent. Kept and root-owned paths get
// one summary line each so the reader knows they were considered.
//
// Pure (exists is injected) so the shape of the block is pinned by test
// rather than by whatever happens to be on the machine running it.
func previewLines(plan []artifact, exists func(string) bool) []string {
	var remove, keep, root, notOurs []string
	for _, a := range plan {
		switch a.Kind {
		case keepAndPrint:
			keep = append(keep, a.Path)
		case needsRoot:
			root = append(root, a.Path)
		case notInstalled:
			notOurs = append(notOurs, a.Path)
		default:
			if exists(a.Path) {
				remove = append(remove, a.Path)
			}
		}
	}
	var out []string
	if len(remove) == 0 {
		out = append(out, "Nothing of dexel's is left to remove at:")
		for _, p := range keep {
			out = append(out, "  (kept) "+p)
		}
	} else {
		out = append(out, "This will remove:")
		for _, p := range remove {
			out = append(out, "  "+p)
		}
	}
	if len(keep) > 0 && len(remove) > 0 {
		out = append(out, "and KEEP:")
		for _, p := range keep {
			out = append(out, "  "+p)
		}
	}
	for _, p := range notOurs {
		out = append(out, fmt.Sprintf("(%s is running this command and is not an installed dexel — it is not touched.)", p))
	}
	if len(root) > 0 {
		out = append(out, fmt.Sprintf("A system-wide .deb install was also found (%d path(s)); it needs root and will only be reported, never touched.", len(root)))
	}
	out = append(out, "")
	return out
}

// consentNoTTYMessage is what a piped/redirected invocation without
// --yes is told. It names the flag, because the fix is one flag.
const consentNoTTYMessage = "dexel: uninstall needs a terminal to confirm, and stdin is not one.\ndexel: pass --yes to uninstall without being asked (the scripting path), or run it from a terminal."

// readAnswer reads one line and reports it lower-cased and trimmed. EOF
// yields "" — which every caller treats as "no", correctly, because a
// stream that ended without answering did not consent.
func readAnswer(in io.Reader) string {
	r := bufio.NewReader(in)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(line))
}

// answerIsYes: only an explicit y/yes. Anything else — including empty,
// which is what pressing Enter at a "[y/N]" gives — is no.
func answerIsYes(answer string) bool {
	return answer == "y" || answer == "yes"
}

// answerIsPurge: the literal word, nothing else. Not "y", not "PURGE
// please", not "purge the thing".
func answerIsPurge(answer string) bool {
	return answer == "purge"
}

// stdinIsTerminal is the one impure part of the consent path, kept to a
// single line so everything above it is testable.
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// ---------------------------------------------------------------------
// the Windows self-delete pattern
// ---------------------------------------------------------------------

// windowsSelfDeleteCommand builds the detached helper that removes the
// exe after THIS process exits. FIELD-TEST-NEEDED: authored and
// unit-tested for the exact command line it produces, never executed on
// real Windows by this project (same honesty split as
// desktop/README.md's "AUTHORED, NOT YET BUILT").
//
// Why PowerShell rather than the classic `cmd /c ping -n 3 ... & del`
// trick: `Wait-Process -Id` waits for the ACTUAL pid instead of sleeping
// a guessed number of seconds, so the delete happens as soon as it can
// succeed and never races a slow exit. PowerShell is present on every
// supported Windows (install.ps1 IS PowerShell, so requiring it here
// adds no dependency the install did not already have).
//
// -Timeout bounds the wait so a wedged process cannot leave a helper
// alive forever; -ErrorAction SilentlyContinue on the wait covers the
// benign race where the process is already gone before the helper
// starts.
//
// reportPath, when non-empty, is appended one line saying what happened
// — the only way a detached, hidden helper can report anything. It is
// the runtime log, so `dexel logs` shows it; under --purge that
// directory is being deleted, so the caller passes "" and the helper
// simply deletes and exits.
func windowsSelfDeleteCommand(pid int, exePath, reportPath string) (string, []string) {
	var b strings.Builder
	fmt.Fprintf(&b, "Wait-Process -Id %d -Timeout 120 -ErrorAction SilentlyContinue; ", pid)
	fmt.Fprintf(&b, "Remove-Item -LiteralPath %s -Force -ErrorAction SilentlyContinue; ", psQuote(exePath))
	if reportPath != "" {
		fmt.Fprintf(&b, "$m = if (Test-Path -LiteralPath %s) { 'dexel: uninstall could NOT remove ' } else { 'dexel: uninstall removed ' }; ", psQuote(exePath))
		fmt.Fprintf(&b, "Add-Content -LiteralPath %s -Value ($m + %s)", psQuote(reportPath), psQuote(exePath))
	}
	return "powershell", []string{
		"-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden",
		"-Command", b.String(),
	}
}

// psQuote single-quotes a string for PowerShell, where the escape for an
// embedded single quote is to double it. Single quotes (not double) so
// nothing inside a path — `$`, a backtick, a semicolon — is expanded by
// the shell that receives it.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// ---------------------------------------------------------------------
// the verb
// ---------------------------------------------------------------------

// uninstall status strings, spelled once so the report and its tests
// cannot drift.
const (
	statusRemoved = "removed"
	statusAbsent  = "already absent"
	statusKept    = "kept"
	statusRoot    = "needs root"
	statusNotOurs = "not ours"
)

// cmdUninstall is the whole verb: consent, stop, un-autostart, remove,
// report.
func cmdUninstall(args []string) int {
	fs := flag.NewFlagSet("dexel uninstall", flag.ExitOnError)
	purge := fs.Bool("purge", false, "ALSO delete the state directory -- config, logs and the save. Asks a second time unless --yes")
	yes := fs.Bool("yes", false, "skip every confirmation (the scripting path); an explicit flag is itself the consent")
	y := fs.Bool("y", false, "alias for --yes")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	assumeYes := *yes || *y

	env, code := cliEnvOrReport()
	if env == nil {
		return code
	}

	home, _ := os.UserHomeDir()
	plan := uninstallPlan(installFacts{
		GOOS:     env.goos,
		Self:     env.self,
		BinDir:   env.binDir,
		StateDir: env.stateDir,
		HomeDir:  home,
		DataDir:  xdgDataDir(home),
		AppData:  os.Getenv("APPDATA"),
		Purge:    *purge,
		Exists:   func(p string) bool { _, err := os.Stat(p); return err == nil },
		Glob:     func(pattern string) []string { m, _ := filepath.Glob(pattern); return m },
	})

	// ---- consent ----
	switch resolveConsent(assumeYes, stdinIsTerminal()) {
	case consentImpossible:
		fmt.Fprintln(env.errOut, consentNoTTYMessage)
		return 2
	case consentAsk:
		// Say WHICH install is about to go before asking. Without this the
		// question is unanswerable in the one case that matters: a source
		// build (`go run . uninstall`) resolves its Self to a temp build
		// directory, so the plan targets the DEFAULT BinDir — i.e. the
		// real install on the developer's machine, not the checkout they
		// are sitting in. Naming the paths turns that from a surprise into
		// a decision.
		for _, line := range previewLines(plan, func(p string) bool { _, err := os.Stat(p); return err == nil }) {
			fmt.Fprintln(env.out, line)
		}
		fmt.Fprint(env.out, uninstallPrompt(*purge))
		if !answerIsYes(readAnswer(os.Stdin)) {
			fmt.Fprintln(env.out, "cancelled; nothing was removed")
			return 1
		}
		if *purge {
			fmt.Fprint(env.out, purgePrompt(env.stateDir))
			if !answerIsPurge(readAnswer(os.Stdin)) {
				fmt.Fprintln(env.out, "cancelled; nothing was removed (the word `purge`, exactly, is what confirms it)")
				return 1
			}
		}
	case consentGranted:
		// --yes: no questions. Say what is about to happen anyway, so
		// even a scripted run leaves a legible trace of the decision.
		if *purge {
			fmt.Fprintf(env.out, "uninstalling dexel, and --purge --yes will delete %s\n", env.stateDir)
		} else {
			fmt.Fprintln(env.out, "uninstalling dexel; your save data stays (--purge removes it)")
		}
	}

	var warnings []string
	warn := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		warnings = append(warnings, msg)
		fmt.Fprintf(env.errOut, "dexel: %s\n", msg)
	}

	// ---- 1. stop the runtime (endpoint-first; not-running is fine) ----
	//
	// env.stop() is `dexel stop` itself, not a reimplementation: same
	// endpoint-primary path, same signal fallback, same "not running is
	// success". A stop that FAILS does not abort the uninstall — on unix
	// the unlink succeeds regardless of who holds the file open, and
	// refusing to uninstall because a wedged process would not die is
	// exactly the "now hand-delete these files" outcome this verb
	// exists to prevent.
	fmt.Fprintln(env.out, "")
	if rc := env.stop(); rc != 0 {
		warn("could not confirm the runtime stopped (exit %d) — continuing; it will die with its binary", rc)
	}

	// ---- 2. disable autostart (probes every mechanism) ----
	//
	// autostart.Disable is idempotent and, on Linux, probes BOTH
	// systemd-user and xdg-autostart regardless of what config.json
	// records — which is precisely what an uninstall needs: leaving an
	// orphaned login entry pointing at a deleted binary is the worst
	// possible leftover, because it fails once per login, forever.
	if err := autostart.Disable(); err != nil {
		warn("autostart disable: %v — check `dexel autostart status` before deleting anything by hand", err)
	} else {
		fmt.Fprintln(env.out, "autostart disabled (all mechanisms probed)")
	}
	// Clear config.json's record ONLY when the file already exists and
	// is being kept. Writing it would otherwise CREATE a config file in
	// a directory we are about to delete (or that the user never had),
	// which is a strange thing for an uninstall to leave behind. When
	// the file stays, clearing it means a later reinstall does not read
	// "autostart: systemd-user" about a unit that no longer exists.
	if !*purge {
		if cfgPath, err := store.ConfigPath(); err == nil {
			if _, statErr := os.Stat(cfgPath); statErr == nil {
				if err := recordAutostartMechanism(autostart.MechanismNone); err != nil {
					warn("cleared autostart but could not update %s: %v", cfgPath, err)
				}
			}
		}
	}

	// ---- 3. remove everything on the plan ----
	type result struct {
		status string
		art    artifact
	}
	results := make([]result, 0, len(plan))
	failed := 0
	var selfEntry *artifact
	removedDesktopEntry := false

	for i := range plan {
		a := plan[i]
		switch a.Kind {
		case keepAndPrint:
			results = append(results, result{statusKept, a})
		case notInstalled:
			results = append(results, result{statusNotOurs, a})
		case needsRoot:
			results = append(results, result{statusRoot, a})
		case removeRunningBinary:
			// Deferred; the plan guarantees this is the last entry.
			selfEntry = &plan[i]
		case removeFile, removeTree:
			status, err := removePath(a)
			if err != nil {
				warn("could not remove %s: %v", a.Path, err)
				failed++
				status = "FAILED"
			}
			if status == statusRemoved && strings.HasSuffix(a.Path, "dexel.desktop") {
				removedDesktopEntry = true
			}
			results = append(results, result{status, a})
		}
	}

	// ---- 4. best-effort desktop database refresh ----
	//
	// Same reasoning as install.sh's write_desktop_file: GNOME, KDE and
	// most environments notice the change themselves, and this only
	// refreshes the MIME cache for the ones that read it. A failure is
	// information, never an error — and it is skipped entirely when no
	// entry was actually removed, so the common "nothing was installed"
	// run does not shell out for nothing.
	if removedDesktopEntry {
		appDir := filepath.Join(xdgDataDir(home), "applications")
		if bin, err := exec.LookPath("update-desktop-database"); err == nil {
			if out, err := exec.Command(bin, appDir).CombinedOutput(); err != nil {
				fmt.Fprintf(env.out, "  (update-desktop-database reported a problem: %v %s — the entry is removed anyway)\n",
					err, strings.TrimSpace(string(out)))
			}
		}
	}

	// ---- 5. the running binary, last ----
	selfStatus := ""
	if selfEntry != nil {
		if env.goos == "windows" {
			// The file is held open by the loader; deleting it from
			// here CANNOT work. Hand it to a detached helper that
			// waits for this pid.
			reportPath := env.logPath
			if *purge {
				reportPath = "" // that directory is gone
			}
			name, cmdArgs := windowsSelfDeleteCommand(os.Getpid(), selfEntry.Path, reportPath)
			if err := launchDetached(name, cmdArgs); err != nil {
				warn("could not schedule the removal of %s: %v — delete it by hand once this process exits", selfEntry.Path, err)
				failed++
				selfStatus = "FAILED to schedule"
			} else {
				selfStatus = "scheduled — a detached helper deletes it the moment this process exits"
			}
		} else {
			// Unix: a running process keeps its image after the inode
			// is unlinked, so this succeeds and the rest of this
			// function keeps running out of memory that no longer has
			// a name on disk.
			if err := os.Remove(selfEntry.Path); err != nil {
				if os.IsNotExist(err) {
					selfStatus = statusAbsent
				} else {
					warn("could not remove %s: %v", selfEntry.Path, err)
					failed++
					selfStatus = "FAILED"
				}
			} else {
				selfStatus = statusRemoved
			}
		}
		results = append(results, result{selfStatus, *selfEntry})
	}

	// ---- 6. the report ----
	fmt.Fprintln(env.out, "")
	fmt.Fprintln(env.out, "Uninstall report")
	for _, r := range results {
		if r.art.Kind == keepAndPrint || r.art.Kind == needsRoot || r.art.Kind == notInstalled {
			continue
		}
		fmt.Fprintf(env.out, "  %-14s %s\n", r.status, r.art.Path)
		if r.status != statusAbsent {
			fmt.Fprintf(env.out, "  %-14s %s\n", "", r.art.What)
		}
	}

	var kept, root, notOurs []result
	for _, r := range results {
		switch r.art.Kind {
		case keepAndPrint:
			kept = append(kept, r)
		case needsRoot:
			root = append(root, r)
		case notInstalled:
			notOurs = append(notOurs, r)
		}
	}
	for _, r := range notOurs {
		fmt.Fprintln(env.out, "")
		fmt.Fprintf(env.out, "NOT TOUCHED: %s\n      %s\n", r.art.Path, r.art.What)
	}
	if len(kept) > 0 {
		fmt.Fprintln(env.out, "")
		fmt.Fprintln(env.out, "KEPT — nothing below was touched:")
		for _, r := range kept {
			fmt.Fprintf(env.out, "  %s\n      %s\n", r.art.Path, r.art.What)
		}
		fmt.Fprintf(env.out, "\nYour dexel is still in %s. Reinstalling resumes the SAME dexel —\n", env.stateDir)
		fmt.Fprintln(env.out, "same save, same settings. `dexel uninstall --purge` is how you end it.")
	}
	if *purge {
		fmt.Fprintln(env.out, "")
		fmt.Fprintf(env.out, "PURGED: %s is gone, save included. A reinstall starts a new dexel.\n", env.stateDir)
	}
	if len(root) > 0 {
		fmt.Fprintln(env.out, "")
		fmt.Fprintln(env.out, "NEEDS ROOT — dexel never sudos, so these are yours to remove:")
		for _, r := range root {
			fmt.Fprintf(env.out, "  %s\n", r.art.Path)
		}
		fmt.Fprintf(env.out, "\n  %s\n", debRemoveCommand)
		fmt.Fprintln(env.out, "\nThat is a system-wide install from the release's .deb — a different")
		fmt.Fprintln(env.out, "install than this one, owned by your package manager.")
	}

	// ARCHITECTURE.md §9 step 6: print the PATH line, never un-edit a
	// shell rc. Neither installer ever edited one (install.sh's
	// path_advice PRINTS the line for the detected shell and says so),
	// so anything on PATH was added by hand and is the user's to remove.
	if env.goos != "windows" && env.binDir != "" {
		if onPath(os.Getenv("PATH"), env.binDir) {
			fmt.Fprintln(env.out, "")
			fmt.Fprintf(env.out, "%s is still on your PATH. If you added that line to a shell\n", env.binDir)
			fmt.Fprintln(env.out, "config yourself, remove it — dexel does not edit your dotfiles, in either")
			fmt.Fprintln(env.out, "direction.")
		}
	}

	fmt.Fprintln(env.out, "")
	if failed > 0 {
		fmt.Fprintf(env.errOut, "dexel: uninstall finished with %d problem(s) above; %d path(s) could not be removed\n", len(warnings), failed)
		return 1
	}
	fmt.Fprintln(env.out, "dexel is uninstalled. Thanks for the workdays.")
	return 0
}

// removePath performs one removal and classifies the outcome.
// "Already gone" is a SUCCESS — that is what makes a second `uninstall`
// a clean exit 0 rather than a wall of errors about files the first run
// correctly deleted.
func removePath(a artifact) (string, error) {
	if a.Kind == removeTree {
		if _, err := os.Stat(a.Path); err != nil {
			if os.IsNotExist(err) {
				return statusAbsent, nil
			}
			// Cannot even look at it; RemoveAll will report the real
			// problem below.
		}
		if err := os.RemoveAll(a.Path); err != nil {
			return "", err
		}
		return statusRemoved, nil
	}
	if err := os.Remove(a.Path); err != nil {
		if os.IsNotExist(err) {
			return statusAbsent, nil
		}
		return "", err
	}
	return statusRemoved, nil
}

// xdgDataDir is $XDG_DATA_HOME, or ~/.local/share — the base install.sh
// computes as DATADIR for the icon and the .desktop entry. Composed here
// from an injected home for the same reason cmd_lifecycle.go composes
// desktopAppCandidates locally: it is a CLI-local reversal of one
// installer line, not a location the runtime ever writes to (which is
// what app/internal/paths owns).
func xdgDataDir(home string) string {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return v
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "share")
}

// onPath reports whether dir is an element of a PATH-style list. Pure,
// and split out so the "$PATH element" comparison (exact element, never
// a substring — `/home/u/.local/binx` must not match `/home/u/.local/bin`)
// is testable.
func onPath(pathEnv, dir string) bool {
	if dir == "" {
		return false
	}
	for _, e := range filepath.SplitList(pathEnv) {
		if e != "" && samePath(runtime.GOOS, e, dir) {
			return true
		}
	}
	return false
}
