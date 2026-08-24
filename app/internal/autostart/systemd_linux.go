//go:build linux

// systemd_linux.go implements Linux's PRIMARY autostart mechanism
// (docs/production-runtime/PLATFORM_NOTES.md §3.2): a systemd
// `--user` unit, detected — never assumed — via `systemctl --user
// is-system-running`. It also holds the three package-level dispatch
// functions (enablePlatform/disablePlatform/queryPlatform) for the
// whole linux build, since Linux is the one platform with two
// mechanisms to choose between; xdg_linux.go supplies the fallback
// those functions call into.
//
// Unlike launchd_darwin.go and windows.go, everything here runs for
// real on this repo's Linux dev box, and systemd_linux_test.go proves
// it against a real `systemctl --user` (this developer's real session,
// carefully cleaned up — see that file's doc comment) as well as
// against fake HOME/XDG_CONFIG_HOME for the pure content/path
// functions.
package autostart

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// systemdUnitName is the literal unit filename PLATFORM_NOTES.md §3.2
// specifies.
const systemdUnitName = "dexel.service"

// xdgConfigDir is $XDG_CONFIG_HOME, or ~/.config if unset — shared by
// systemd's unit directory and xdg_linux.go's autostart directory,
// which both live under it.
func xdgConfigDir(getenv func(string) string, homeDir func() (string, error)) (string, error) {
	if v := getenv("XDG_CONFIG_HOME"); v != "" {
		return v, nil
	}
	home, err := homeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config"), nil
}

// systemdUnitPath is ~/.config/systemd/user/dexel.service (or under
// $XDG_CONFIG_HOME) — parameter-injected exactly like
// app/internal/paths/paths.go's stateDirFor, so a test can drive it
// with a fake HOME/XDG_CONFIG_HOME without touching this developer's
// real one.
func systemdUnitPath(getenv func(string) string, homeDir func() (string, error)) (string, error) {
	dir, err := xdgConfigDir(getenv, homeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "systemd", "user", systemdUnitName), nil
}

// systemdUnitContent is PLATFORM_NOTES.md §3.2's unit, byte for byte,
// with ExecStart pointing at exePath — the caller's resolved
// os.Executable(), never the doc's illustrative "%h/.local/bin/dexel"
// (MIGRATION_PLAN.md §PR-6: the binary path baked into any mechanism
// must be the resolved, installed binary, not a guess).
func systemdUnitContent(exePath string) string {
	return fmt.Sprintf(`[Unit]
Description=dexel developer companion runtime
Documentation=https://dexel.jwdlab.com
After=default.target

[Service]
Type=simple
ExecStart=%s runtime
Restart=on-failure
RestartSec=5
# The runtime writes its own log file; journald gets it too, harmlessly.
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`, exePath)
}

// systemdUsableFromOutput is the pure interpretation of `systemctl
// --user is-system-running`'s stdout (PLATFORM_NOTES.md §3.2:
// "systemd is used only if [it] answers AT ALL (it is fine if it
// answers `degraded`)"). Split from the exec call so the interpretation
// itself — "non-empty stdout means it answered" — is unit-testable
// without a real systemd session.
//
// This is deliberately NOT "exit code == 0": `is-system-running`
// exits non-zero for `degraded` (still a real answer, stdout
// "degraded") and ALSO exits non-zero — with EMPTY stdout — when there
// is no user D-Bus session at all (verified on this box: unset
// DBUS_SESSION_BUS_ADDRESS/XDG_RUNTIME_DIR yields exit 1, empty
// stdout, an error on stderr). Empty stdout is therefore the one
// reliable signal for "systemd is not usable here", regardless of exit
// code or of *why* it isn't usable (no systemd, no user bus, command
// missing).
func systemdUsableFromOutput(stdout []byte) bool {
	return len(bytes.TrimSpace(stdout)) > 0
}

// systemdUsable runs the real detection command.
func systemdUsable() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	out, _ := exec.Command("systemctl", "--user", "is-system-running").Output()
	return systemdUsableFromOutput(out)
}

func runSystemctl(args ...string) error {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w (%s)", args, err, bytes.TrimSpace(out))
	}
	return nil
}

// enableSystemd writes the unit and runs exactly the two commands
// PLATFORM_NOTES.md §3.2 specifies for `enable`. Both the file write
// and `systemctl --user enable --now` are naturally idempotent
// (overwriting identical content; enabling an already-enabled unit is
// a no-op to systemd), so calling this twice leaves exactly one entry.
func enableSystemd(exePath string) error {
	path, err := systemdUnitPath(os.Getenv, os.UserHomeDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create systemd user unit dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(systemdUnitContent(exePath)), 0o644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}
	if err := runSystemctl("--user", "daemon-reload"); err != nil {
		return err
	}
	if err := runSystemctl("--user", "enable", "--now", systemdUnitName); err != nil {
		return err
	}
	return nil
}

// disableSystemd is idempotent: if the unit file is already absent,
// there is nothing to do and no error. Otherwise it disables+stops,
// removes the file, and reloads — in that order, so a `daemon-reload`
// always runs last and always sees the file gone.
func disableSystemd() error {
	path, err := systemdUnitPath(os.Getenv, os.UserHomeDir)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil
		}
		return statErr
	}
	// Best-effort: a `disable --now` failure here (e.g. the unit was
	// already disabled, or state had drifted) must not prevent removing
	// the file and reloading below — those two are what actually
	// guarantee "no orphan" — but it IS still reported, at the end, if
	// nothing worse happened first.
	disableErr := runSystemctl("--user", "disable", "--now", systemdUnitName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unit file: %w", err)
	}
	if err := runSystemctl("--user", "daemon-reload"); err != nil {
		return err
	}
	return disableErr
}

// statusSystemd reports `systemctl --user is-enabled dexel.service`,
// but only bothers asking if the unit file exists on disk — on a box
// with no systemd at all (or none usable), asking would just fail with
// an unrelated error.
func statusSystemd() (Status, error) {
	path, err := systemdUnitPath(os.Getenv, os.UserHomeDir)
	if err != nil {
		return Status{}, err
	}
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return Status{}, nil
		}
		return Status{}, statErr
	}
	out, _ := exec.Command("systemctl", "--user", "is-enabled", systemdUnitName).CombinedOutput()
	detail := string(bytes.TrimSpace(out))
	return Status{
		Mechanism: MechanismSystemdUser,
		Active:    detail == "enabled",
		Detail:    fmt.Sprintf("systemd --user: %s (%s)", detail, path),
	}, nil
}

// ---------------------------------------------------------------------
// Package-level dispatch for the whole Linux build (PLATFORM_NOTES.md
// §3.2: systemd primary, XDG autostart fallback, `disable` probes
// both).
// ---------------------------------------------------------------------

func enablePlatform(exePath, logPath string, opts Options) (Result, error) {
	// opts.BareExecutable is macOS-only (it opts out of .app bundle
	// attribution, a concept Linux does not have): the unit/desktop file
	// always names exePath here, so the flag is a no-op rather than an
	// error — `dexel autostart enable --bare` on Linux asks for exactly
	// what Linux already does.
	_ = opts
	if systemdUsable() {
		if err := enableSystemd(exePath); err != nil {
			return Result{}, err
		}
		return Result{Mechanism: MechanismSystemdUser, Program: exePath}, nil
	}
	if err := enableXDGAutostart(exePath); err != nil {
		return Result{}, err
	}
	return Result{Mechanism: MechanismXDGAutostart, Program: exePath}, nil
}

// disablePlatform probes BOTH mechanisms regardless of which one is
// recorded anywhere (PLATFORM_NOTES.md §3.2: "a user who switched
// distros should not be left with an orphan"). Both sides are already
// individually idempotent; running both every time is what makes this
// function idempotent as a whole.
func disablePlatform() error {
	systemdErr := disableSystemd()
	_, xdgErr := disableXDGAutostart()
	if systemdErr != nil {
		return systemdErr
	}
	return xdgErr
}

func queryPlatform() (Status, error) {
	st, err := statusSystemd()
	if err != nil {
		return Status{}, err
	}
	if st.Mechanism != MechanismNone {
		return st, nil
	}
	present, err := xdgAutostartPresent()
	if err != nil {
		return Status{}, err
	}
	if present {
		return Status{
			Mechanism: MechanismXDGAutostart,
			Active:    true,
			Detail:    "xdg-autostart: ~/.config/autostart/dexel.desktop present",
		}, nil
	}
	return Status{Detail: "no autostart mechanism found (checked systemd --user and xdg-autostart)"}, nil
}
