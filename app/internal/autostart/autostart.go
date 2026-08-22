// Package autostart implements `dexel autostart enable|disable|status`
// (dev_docs/production-runtime/MIGRATION_PLAN.md §PR-6,
// dev_docs/production-runtime/PLATFORM_NOTES.md §3): one CLI verb, three
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

// Enable installs the current platform's autostart mechanism so dexel
// starts at login, pointed at exePath — which callers MUST resolve via
// os.Executable(), never os.Args[0], because this is baked into a file
// or registry value that will be read back at the NEXT login, long
// after this process (and however it happened to be invoked) is gone —
// and with its stdio/log destination at logPath where the mechanism
// supports one. Returns which mechanism was used, for the caller to
// persist in config.json (SEC-1 design; the caller's job, not this
// package's — autostart has no opinion on where config.json lives).
func Enable(exePath, logPath string) (Mechanism, error) {
	return enablePlatform(exePath, logPath)
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
// "/Users/USER/..." placeholders — exePath is always the caller's
// resolved os.Executable(), never a guessed path.
//
// KeepAlive.SuccessfulExit=false (not bare KeepAlive=true) is
// load-bearing: it means "restart if it crashed, do not restart after a
// clean `dexel stop`" — a bare KeepAlive=true would make `dexel stop`
// un-stoppable, which the doc calls a bug, not supervision.
// ProcessType=Background opts into background CPU/IO scheduling,
// correct for a 1Hz ticker. ProgramArguments runs `runtime`, not
// `start`: launchd itself is the supervisor and owns stdio redirection
// here, so `start`'s lock/log/readiness machinery would be redundant.
func launchdPlistContent(exePath, logPath string) string {
	exe := xmlEscape(exePath)
	log := xmlEscape(logPath)
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
  <key>RunAtLoad</key>          <true/>
  <key>KeepAlive</key>
  <dict><key>SuccessfulExit</key><false/></dict>
  <key>ProcessType</key>        <string>Background</string>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, launchdLabel, exe, log, log)
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
