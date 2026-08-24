// Package autostart implements `dexel autostart enable|disable|status`
// (docs/production-runtime/MIGRATION_PLAN.md §PR-6,
// docs/production-runtime/PLATFORM_NOTES.md §3): one CLI verb, three
// user-level, no-sudo mechanisms — launchd on macOS, systemd `--user`
// (with XDG autostart as a detected, not assumed, fallback) on Linux,
// the HKCU `Run` key on Windows.
//
// The one rule every mechanism obeys (PLATFORM_NOTES.md §3,
// ARCHITECTURE.md's consent posture): autostart is never enabled by
// anything but an explicit `enable` call — this package exposes no
// other way to write a mechanism, and app/cmd_autostart.go is the only
// caller in the whole binary. `Enable` is idempotent (calling it twice
// leaves exactly one entry, never two); `Disable`, on Linux, probes
// BOTH mechanisms regardless of what config.json recorded, so a user
// who switched distros — or hand-edited the file — is never left with
// an orphaned entry.
//
// Per-OS split: this file holds everything that is pure and therefore
// testable on any host — the Mechanism/Status vocabulary and the exact
// artifact-content generators for macOS's plist and Windows' registry
// value, kept untagged for the same reason app/internal/paths/paths.go
// keeps stateDirFor/binDirFor untagged and parameter-injected (see that
// file's doc comment): a single test binary, on whatever OS actually
// runs `go test`, can assert every platform's generated content
// byte-for-byte. The three build-tagged sibling files (launchd_darwin.go,
// systemd_linux.go + xdg_linux.go, windows.go — mirroring the existing
// provider_select_{darwin,linux,other}.go trio) each supply exactly the
// three functions below want: enablePlatform, disablePlatform,
// queryPlatform. Only systemd_linux.go/xdg_linux.go can be exercised for
// real from this repo's Linux dev box; launchd_darwin.go and windows.go
// are authored and content-tested only, exactly like desktop/README.md's
// "AUTHORED, NOT YET BUILT" honesty split for code nobody here can run.
package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

// Mechanism identifies which autostart mechanism is in play. The four
// non-empty values are exactly config.json's `autostart` field's
// documented enum (PLATFORM_NOTES.md §3); MechanismNone is both "nothing
// configured" and the value `disable` writes back.
type Mechanism string

const (
	MechanismNone         Mechanism = ""
	MechanismLaunchd      Mechanism = "launchd"
	MechanismSystemdUser  Mechanism = "systemd-user"
	MechanismXDGAutostart Mechanism = "xdg-autostart"
	MechanismWindowsRun   Mechanism = "windows-run"
)

// Status is what `dexel autostart status` reports — always asked of the
// OS directly (never trusted from config.json alone, which can drift:
// a hand-deleted unit file, a hand-edited config.json), the same
// "staleness is resolved by asking" posture ARCHITECTURE.md applies to
// runtime.json.
type Status struct {
	// Mechanism is which mechanism the OS currently shows evidence of
	// (an existing unit/plist/desktop-file/registry-value),
	// MechanismNone if nothing was found.
	Mechanism Mechanism
	// Active is the OS's own answer to "is it actually enabled/loaded
	// right now" (systemctl is-enabled == "enabled", the launchd job is
	// bootstrapped, the registry value exists) — distinct from
	// Mechanism != MechanismNone, which only means an artifact exists.
	Active bool
	// Detail is a short, human-readable explanation for `status` to
	// print — raw command output where one exists, or a plain sentence
	// where the answer came from a file check alone.
	Detail string
}

// Options tunes Enable. The zero value is the default behaviour on
// every platform, so a caller that has no opinion passes Options{}.
type Options struct {
	// BareExecutable forces macOS's plist to name exePath itself even
	// when an installed .app bundle is present — i.e. it opts OUT of
	// the bundle attribution described in launchdProgram below.
	//
	// Why this escape hatch exists at all: bundle attribution buys a
	// real icon and name in System Settings, but it also moves the
	// binary that runs at login from the one `dexel update` replaces
	// (~/.local/bin/dexel) to the one the .app ships. That is a real
	// trade-off, not a strict win, so the user gets a way to say no
	// without hand-editing a plist. Ignored on Linux and Windows,
	// which have no bundle concept — see enablePlatform there.
	BareExecutable bool
}

// Result is what Enable actually did. Mechanism is the value the caller
// persists in config.json; Program and Note exist so the CLI can print
// the TRUTH about what got written rather than echoing back the exePath
// it passed in — which, with macOS bundle attribution, is no longer
// necessarily the path that ends up in the plist.
type Result struct {
	// Mechanism is which mechanism was installed.
	Mechanism Mechanism
	// Program is the executable path actually baked into the artifact.
	// On Linux and Windows this is always exePath; on macOS it may be
	// an app bundle's own executable instead (see launchdProgram).
	Program string
	// Note is a short human-readable "why that program", empty when
	// there is nothing worth saying.
	Note string
}

// Enable installs the current platform's autostart mechanism so dexel
// starts at login, pointed at exePath — which callers MUST resolve via
// os.Executable(), never os.Args[0], because this is baked into a file
// or registry value that will be read back at the NEXT login, long
// after this process (and however it happened to be invoked) is gone —
// and with its stdio/log destination at logPath where the mechanism
// supports one. Returns a Result whose Mechanism the caller persists in
// config.json (SEC-1 design; the caller's job, not this package's —
// autostart has no opinion on where config.json lives) and whose
// Program is the path that was really written.
//
// exePath is a REQUEST, not a promise, on macOS only: see launchdProgram
// for why an installed .app bundle's own executable is preferred there,
// and Options.BareExecutable for how to decline that.
func Enable(exePath, logPath string, opts Options) (Result, error) {
	return enablePlatform(exePath, logPath, opts)
}

// Disable removes autostart. On Linux this probes BOTH systemd-user and
// xdg-autostart regardless of which one is recorded anywhere, per
// PLATFORM_NOTES.md §3.2 ("a user who switched distros should not be
// left with an orphan"). Idempotent: disabling twice is still clean and
// returns no error the second time.
func Disable() error {
	return disablePlatform()
}

// Query asks the OS whether autostart is active right now, and by which
// mechanism.
func Query() (Status, error) {
	return queryPlatform()
}

// ---------------------------------------------------------------------
// macOS launchd plist — content generation only (kept here, untagged,
// so it is unit-testable on this Linux dev box; the actual
// `launchctl`-driven enablePlatform/disablePlatform/queryPlatform live
// in launchd_darwin.go, unverified on hardware).
// ---------------------------------------------------------------------

// launchdLabel is the launchd job label, matching
// desktop/src-tauri/tauri.conf.json's `identifier` — one identity for
// the product (PLATFORM_NOTES.md §3.1).
const launchdLabel = "com.jawwadzafar.dexel"

// launchdPlistPath is ~/Library/LaunchAgents/<label>.plist.
func launchdPlistPath(homeDir string) string {
	return filepath.Join(homeDir, "Library", "LaunchAgents", launchdLabel+".plist")
}

// launchdPlistContent is PLATFORM_NOTES.md §3.1's plist, byte for byte,
// with exePath/logPath substituted for the doc's illustrative
// "/Users/USER/..." placeholders — exePath is the program launchd will
// run, resolved by launchdProgram from the caller's os.Executable(),
// never a guessed path.
//
// KeepAlive.SuccessfulExit=false (not bare KeepAlive=true) is
// load-bearing: it means "restart if it crashed, do not restart after a
// clean `dexel stop`" — a bare KeepAlive=true would make `dexel stop`
// un-stoppable, which the doc calls a bug, not supervision.
// ProcessType=Background opts into background CPU/IO scheduling,
// correct for a 1Hz ticker. ProgramArguments runs `runtime`, not
// `start`: launchd itself is the supervisor and owns stdio redirection
// here, so `start`'s lock/log/readiness machinery would be redundant.
//
// associatedBundleID, when non-empty, adds the one key launchd.plist(5)
// documents for exactly this problem:
//
//	AssociatedBundleIdentifiers <string or array of strings>
//	  This optional key indicates which bundles are associated with this
//	  job in the System Settings Login Items UI. If an app installs a
//	  legacy plist the plist should include this key with a value of the
//	  app's bundle identifier.
//
// An empty associatedBundleID omits the key entirely, so the no-bundle
// plist is byte-identical to the one that shipped before app-bundle
// attribution existed — pinned by test, because "the fallback changes
// nothing" is the claim that makes this safe.
func launchdPlistContent(exePath, logPath, associatedBundleID string) string {
	exe := xmlEscape(exePath)
	log := xmlEscape(logPath)
	assoc := ""
	if associatedBundleID != "" {
		assoc = fmt.Sprintf(`  <key>AssociatedBundleIdentifiers</key>
  <array>
    <string>%s</string>
  </array>
`, xmlEscape(associatedBundleID))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>              <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>runtime</string>
  </array>
%s  <key>RunAtLoad</key>          <true/>
  <key>KeepAlive</key>
  <dict><key>SuccessfulExit</key><false/></dict>
  <key>ProcessType</key>        <string>Background</string>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, launchdLabel, exe, assoc, log, log)
}

// xmlEscaper covers the five predefined XML entities — plenty for a
// filesystem path, which is the only untrusted-shaped input this ever
// receives, but cheap enough to apply unconditionally rather than
// assume paths never contain '&', '<', '>', '"' or '\”.
var xmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

func xmlEscape(s string) string { return xmlEscaper.Replace(s) }

// xmlUnescaper is xmlEscaper's inverse, used by launchdPlistProgram to
// read a path back out of a plist we generated. "&amp;" is listed LAST
// on purpose: strings.NewReplacer tries its patterns in argument order
// at each position, so an escaped literal "&lt;" — which appears in the
// file as "&amp;lt;" — matches "&amp;" first and unescapes to "&lt;",
// rather than being eaten as "<lt;". Round-trip is asserted in the test.
var xmlUnescaper = strings.NewReplacer(
	"&lt;", "<",
	"&gt;", ">",
	"&quot;", `"`,
	"&apos;", "'",
	"&amp;", "&",
)

func xmlUnescape(s string) string { return xmlUnescaper.Replace(s) }

// ---------------------------------------------------------------------
// macOS background-item attribution — why the plist may name a path that
// is NOT the running executable, and gains an
// AssociatedBundleIdentifiers key.
//
// THE PROBLEM. System Settings → General → Login Items & Extensions →
// "Allow in the Background" (the UI over macOS 13+'s Background Task
// Management store) listed dexel as a generic `exec` icon named "dexel",
// subtitled "Item from unidentified developer".
//
// WHAT MACOS ACTUALLY USES, and how confident we are of each part:
//
//  1. AssociatedBundleIdentifiers — DOCUMENTED, and the only officially
//     supported lever for a hand-installed ~/Library/LaunchAgents plist.
//     launchd.plist(5) says, verbatim: "This optional key indicates which
//     bundles are associated with this job in the System Settings Login
//     Items UI. If an app installs a legacy plist the plist should
//     include this key with a value of the app's bundle identifier."
//     Apple DTS recommends it repeatedly on the developer forums, and at
//     least one report there confirms the ICON is picked up from the
//     associated bundle once it is set. So we set it.
//
//  2. Naming an executable INSIDE the .app bundle — STRONG CIRCUMSTANTIAL
//     EVIDENCE, not documentation. On the machine this was investigated
//     on, every third-party LaunchAgent that shows a real icon and name
//     (OneDrive's StandaloneUpdater and SyncReporter, Microsoft
//     Defender's helper, Intune's MDM agent, Company Portal's SSO XPC
//     service) names an executable inside an .app bundle — often a nested
//     one — while every one that shows a generic icon (FortiClient's
//     bin/*, XQuartz's libexec/*) names a bare Unix executable. Apple's
//     own framing is "responsible code", resolved through the Launch
//     Services database, and LaunchServices does resolve a path inside a
//     bundle to that bundle. We follow the pattern that demonstrably
//     works in the field; we do not claim Apple documents it.
//
//  3. The plist's `Label` — CONFIRMED IRRELEVANT to the displayed name.
//     The known counter-example is nix-darwin, whose labels are
//     `org.nixos.nix-daemon` but whose ProgramArguments[0] is /bin/sh:
//     the pane shows two entries literally named "sh". Attribution
//     follows the process actually exec'd, which is a second reason to
//     name a real Mach-O inside a bundle rather than a wrapper script.
//
// WHAT THIS DOES NOT FIX, and must not be claimed to. The "Item from
// unidentified developer" subtitle is read from the code-signing
// CERTIFICATE of the responsible code. Every dexel binary is ad-hoc
// ("linker-signed") — that is simply what the Go toolchain emits on
// Apple Silicon, where an unsigned Mach-O cannot execute at all — and an
// ad-hoc signature carries no certificate chain and no TeamIdentifier,
// so there is no developer name to read. The owner of the machine this
// was investigated on already saw "unidentified developer" against
// exactly such an ad-hoc-signed binary, which is the direct evidence
// that ad-hoc signing does not move that subtitle.
//
// Worse, and stated here so nobody is surprised: because "responsible
// code" tracking keys off the Team Identifier, a binary with no Team
// Identifier may cause macOS to IGNORE AssociatedBundleIdentifiers
// outright (Apple DTS says exactly this for a LaunchDaemon running
// bash). So items 1 and 2 above may buy nothing at all until the project
// has a real Developer ID signature. They are cheap, correct, and the
// documented/observed-in-the-field things to do — not a guarantee.
// docs/production-runtime/PLATFORM_NOTES.md §3.1 records the full
// verdict and what a real signature would cost.
// ---------------------------------------------------------------------

// bundleServerExecutables are the names the dexel Go binary may carry
// inside the Tauri bundle (desktop/src-tauri ships it as an external
// binary/sidecar next to the Tauri shell's own executable), newest
// FIRST. Whichever one is found, it is the SAME program as the bare
// `dexel` — same subcommands, `runtime` included — so launchd can invoke
// it exactly as it invokes the bare binary today.
//
// # Why the current name is "Dexel", capitalised
//
// This is the ONE dexel artifact whose filename a human reads, which is
// why it is the one exception to "every artifact is spelled lowercase
// `dexel`". System Settings names a background item after the executable
// that is actually exec'd (§3.1.1's finding 3: not the launchd Label,
// not the enclosing bundle), so this filename IS the pane's display
// string. It has read "dexel-server" and then "Dexel Runtime"; the owner
// asked for exactly "Dexel". PLATFORM_NOTES.md §3.1.2 and §3.1.3 record
// each generation and what the pane showed.
//
// The name only became available once the Tauri shell's own main binary
// stopped being called `dexel`. `Contents/MacOS/` is one flat directory
// and macOS's default APFS volume is case-INsensitive, so `Dexel` and
// `dexel` are the SAME FILE there; while the shell owned `dexel`, an
// externalBin called `Dexel` would have been silently overwritten by it
// at bundle time (tauri-bundler copies external binaries before the main
// binary, with a plain fs::copy and no error), and this LaunchAgent
// would have opened a GUI window at every login. The shell's binary is
// now `dexel-desktop` — see desktop/src-tauri/Cargo.toml's [package]
// header.
//
// # Why the OLD names are still probed
//
// A bundle installed before a rename still contains only the older name.
// Dropping it would make `enable` silently fall through to the
// bare-binary plist on those machines — a downgrade the user would never
// be told about beyond one line of note text. Probing all three costs
// two extra stats per bundle candidate.
//
// # What is deliberately NOT in this list, and the trap that makes it
// # subtler than it looks
//
// Neither "dexel-desktop" (the Tauri shell's main binary today) nor
// "dexel" (what it was called in every bundle built before that rename).
// Pointing a LaunchAgent at either would open a GUI window at login
// instead of starting the runtime.
//
// Leaving them out is NOT sufficient on its own, and this is the part
// that is easy to get wrong: on a case-insensitive volume, probing for
// "Dexel" inside a PRE-RENAME bundle — one holding `dexel` (the GUI
// shell) and `Dexel Runtime` (the daemon) — resolves to `dexel`, the GUI
// shell, because the two names are one file. The first entry of this
// list would then win and the login item would open a window. What
// prevents that is not this list's contents but its predicate:
// isExecutableFile requires the name to match the DIRECTORY ENTRY'S OWN
// SPELLING (see nameIsCaseExactOnDisk), so on a pre-rename bundle
// ".../MacOS/Dexel" is rejected and the probe falls through to
// "Dexel Runtime" — the correct file — while on a post-rename bundle
// ".../MacOS/Dexel" is spelled exactly that way on disk and is accepted.
// TestLaunchdProgramOnAPreRenameBundle exercises that against a real
// directory.
var bundleServerExecutables = []string{"Dexel", "Dexel Runtime", "dexel-server"}

// isExecutableFile is launchdProgram's real-filesystem predicate: a
// regular file (never a directory or symlink-to-nothing) with at least
// one execute bit, whose name is spelled on disk exactly as asked. All
// three halves matter — an .app directory that exists with none of
// bundleServerExecutables inside it, or a zero-permission leftover, must
// not be baked into a plist as a program launchd will fail to spawn at
// every login; and the case-exactness is what stops a pre-rename bundle
// resolving ".../MacOS/Dexel" to the GUI shell it actually stores as
// `dexel` (see bundleServerExecutables' last section).
//
// It lives in this untagged file, rather than beside the launchd code it
// serves, so that both halves are compiled and unit-tested on every
// host. The bug it guards against is a macOS-filesystem property, but
// the code that guards against it does not have to be macOS-only, and a
// guard that only ever runs on the one machine nobody runs `go test` on
// is not much of a guard.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if info.Mode().Perm()&0o111 == 0 {
		return false
	}
	return nameIsCaseExactOnDisk(path)
}

// nameIsCaseExactOnDisk reports whether path's final component matches
// the spelling of the actual directory entry, byte for byte.
//
// os.Stat cannot answer this: on a case-insensitive volume it happily
// resolves "Dexel" to a file stored as "dexel" and reports success, and
// there is no POSIX call that asks "what is this file really called?".
// Reading the parent directory is the only way to see the stored
// spelling — the entries os.ReadDir returns are the names as recorded,
// not as requested.
//
// A missing directory, or an unreadable one, reads as "no" rather than
// as an error: every caller is asking "is this a usable target?", and
// "cannot tell" and "no" lead to the same, safe, fall-through.
//
// The cost is one directory read per candidate. Contents/MacOS holds two
// files.
func nameIsCaseExactOnDisk(path string) bool {
	dir, base := filepath.Split(path)
	if base == "" {
		return false
	}
	entries, err := os.ReadDir(filepath.Clean(dir))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() == base {
			return true
		}
	}
	return false
}

// launchdBundleCandidates is where an installed dexel .app might be, in
// preference order.
//
// Both capitalisations are listed even though macOS's default APFS
// volume is case-INsensitive (so "/Applications/Dexel.app" already finds
// a directory named "dexel.app"): a case-sensitive volume is a
// supported, if unusual, configuration, and the bundle is mid-rename
// from `dexel.app` to `Dexel.app`. Listing both costs one extra stat and
// removes a whole class of "works on my Mac" failure. /Applications
// precedes ~/Applications because a system-wide install is the one the
// installer produces.
func launchdBundleCandidates(homeDir string) []string {
	return []string{
		"/Applications/Dexel.app",
		"/Applications/dexel.app",
		filepath.Join(homeDir, "Applications", "Dexel.app"),
		filepath.Join(homeDir, "Applications", "dexel.app"),
	}
}

// bundleServerPath is <bundle>/Contents/MacOS/<name>.
func bundleServerPath(bundlePath, name string) string {
	return filepath.Join(bundlePath, "Contents", "MacOS", name)
}

// insideAppBundle reports whether exePath already lives inside a .app —
// i.e. whether the caller is ALREADY running as the bundle's own
// executable (`/Applications/Dexel.app/Contents/MacOS/Dexel\ Runtime
// autostart enable`). In that case the resolved os.Executable() is
// already bundle-attributed and must be used verbatim: substituting a
// "candidate" bundle would be a silent, surprising redirect away from
// the very binary the user invoked.
//
// The check walks ancestors rather than matching a fixed
// ".app/Contents/MacOS/" substring so a nested bundle (which is how
// OneDrive and Defender ship their helpers) is recognised too.
func insideAppBundle(exePath string) bool {
	dir := filepath.Dir(exePath)
	for {
		if strings.EqualFold(filepath.Ext(dir), ".app") {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// launchdProgram decides the two attribution-related fields of the
// plist: what ProgramArguments[0] should be, and what bundle identifier
// (if any) to associate the job with.
//
// The returned bundleID is launchdLabel — which is not a coincidence and
// not a guess: PLATFORM_NOTES.md §3.1 pins the launchd label, the Tauri
// bundle's `identifier`, and therefore the installed .app's
// CFBundleIdentifier to the ONE string "com.jawwadzafar.dexel", so the
// product has one identity. If that ever diverges the association
// becomes a wrong hint in a Settings pane — cosmetic, never functional,
// since launchd.plist(5) scopes the key to the Login Items UI.
//
// bundleID is deliberately empty in the two cases where asserting an
// association would be a claim we cannot back: no bundle was found (so
// there is no such bundle to associate with), and --bare (where the user
// asked for precisely the pre-existing behaviour, and gets a plist
// byte-identical to it).
//
// isExecutable is injected so this — the whole decision — is unit
// testable on any host with no filesystem at all. It must report "this
// path is a regular, executable file"; a bundle directory that exists
// but carries none of bundleServerExecutables inside it is NOT a usable
// target and must fall through to the next candidate, because a plist
// naming a non-existent program is a login-time failure the user would
// only ever see in a log.
//
// The fallback is deliberate and total: if nothing is found, the
// behaviour is byte-identical to before this function existed. A missing
// app bundle degrades the icon, never the autostart.
func launchdProgram(exePath string, candidates []string, isExecutable func(string) bool, opts Options) (program, bundleID, note string) {
	if opts.BareExecutable {
		return exePath, "", "--bare requested: the plist names this executable and asserts no bundle association, so System Settings will show a generic icon"
	}
	if insideAppBundle(exePath) {
		return exePath, launchdLabel, "this executable already lives inside an .app bundle, so macOS has a bundle to attribute the item to"
	}
	// Bundle location is the stronger signal, so it is the OUTER loop:
	// the most-preferred bundle wins even if it only carries the legacy
	// executable name, rather than a less-preferred bundle winning by
	// having the newer name.
	for _, bundle := range candidates {
		for _, name := range bundleServerExecutables {
			server := bundleServerPath(bundle, name)
			if !isExecutable(server) {
				continue
			}
			return server, launchdLabel, fmt.Sprintf("pointed at %s's own executable and associated with %s, so System Settings can attribute the item to the app (the bare %s is untouched and is still what `dexel update` replaces)", bundle, launchdLabel, exePath)
		}
	}
	return exePath, "", "no installed .app bundle found, so the plist names the bare binary and asserts no association (generic icon in System Settings)"
}

// launchdPlistProgram reads ProgramArguments[0] back out of a plist this
// package generated, so `status` can report which program is actually
// registered rather than assuming. It is deliberately a narrow scan of
// OUR OWN generated shape, not a general plist parser: the alternative
// (howett.net/plist or a shell-out to PlistBuddy) is a dependency or a
// subprocess for one string, and a hand-edited or foreign plist should
// read as "unknown" (empty) rather than be half-understood. Callers must
// treat "" as "could not tell" and say so, never as "no program".
func launchdPlistProgram(plistXML string) string {
	const key = "<key>ProgramArguments</key>"
	i := strings.Index(plistXML, key)
	if i < 0 {
		return ""
	}
	rest := plistXML[i+len(key):]
	open := strings.Index(rest, "<string>")
	if open < 0 {
		return ""
	}
	rest = rest[open+len("<string>"):]
	end := strings.Index(rest, "</string>")
	if end < 0 {
		return ""
	}
	return xmlUnescape(rest[:end])
}

// ---------------------------------------------------------------------
// Windows HKCU Run key — value shape only (kept here, untagged, for the
// same reason as the plist above: golang.org/x/sys/windows/registry is
// itself //go:build windows, so the actual registry.OpenKey/
// SetStringValue calls in windows.go can only be authored and
// cross-compile-checked from this box, never run — but the exact string
// written there is ordinary, platform-agnostic Go and can be asserted
// here.)
// ---------------------------------------------------------------------

const (
	// windowsRunKeyPath is relative to HKEY_CURRENT_USER.
	windowsRunKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
	windowsRunValueName = "dexel"
)

// windowsRunValueData is PLATFORM_NOTES.md §3.3's value data: the
// resolved, installed binary path, quoted, plus " start" — `start`, not
// `runtime`, because the Run key has no supervisor of its own, the same
// reasoning as the Linux XDG fallback's `dexel start` (xdg_linux.go).
func windowsRunValueData(exePath string) string {
	return fmt.Sprintf(`"%s" start`, exePath)
}

// ---------------------------------------------------------------------
// --linger (PLATFORM_NOTES.md §3.2: "A documented --linger flag exists
// for the headless-workstation case" — `loginctl enable-linger`, which
// keeps a systemd --user session alive with nobody logged in). Kept
// separate from Enable: linger is never implied by plain `enable`,
// because a companion that tracks *your keyboard* running with no
// session logged in is surprising by default, not a sensible default.
// ---------------------------------------------------------------------

// EnableLinger runs `loginctl enable-linger <user>` for the current
// user. It is a systemd-user/Linux-only concept; called on any other
// platform it fails with a clear, explicit error rather than silently
// doing nothing.
func EnableLinger() error {
	u, err := user.Current()
	if err != nil {
		return fmt.Errorf("determine current user: %w", err)
	}
	return enableLinger(runtime.GOOS, execCombinedOutput, u.Username)
}

// enableLinger is EnableLinger's parameter-injected core: goos and run
// are both explicit so a test can drive the "not Linux" guard and the
// success/failure paths without ever invoking a real loginctl, which
// would durably change this (or any) machine's real systemd-logind
// state — a cost this repo's test suite must never impose, unlike the
// throwaway systemd --user unit PR-6's own tests round-trip for real.
func enableLinger(goos string, run func(name string, args ...string) ([]byte, error), username string) error {
	if goos != "linux" {
		return fmt.Errorf("--linger is a systemd-user (Linux) concept; not applicable on %s", goos)
	}
	out, err := run("loginctl", "enable-linger", username)
	if err != nil {
		return fmt.Errorf("loginctl enable-linger: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func execCombinedOutput(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}
