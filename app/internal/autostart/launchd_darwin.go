//go:build darwin

// launchd_darwin.go implements macOS's autostart mechanism
// (dev_docs/production-runtime/PLATFORM_NOTES.md §3.1): a launchd user
// agent, chosen over Login Items because a plist + one `launchctl` call
// works for a bare CLI with no app bundle, no Objective-C/Swift call
// and no Automation permission prompt.
//
// STATUS: AUTHORED, NOT VERIFIED ON HARDWARE — this repo's dev box is
// Linux (MIGRATION_PLAN.md §PR-6's exit criteria says so explicitly),
// the same honesty split desktop/README.md already carries for code
// nobody here can run:
//
//	| | |
//	|---|---|
//	| The plist's exact XML (launchdPlistContent, autostart.go)     | unit-tested here, byte for byte |
//	| `launchctl bootstrap`/`bootout`/`print` actually invoked      | never run |
//	| The load-at-login/KeepAlive/ProcessType behaviour             | never observed |
//
// The first real signal is a build+run on an actual Mac. Until then,
// treat enablePlatform/disablePlatform/queryPlatform below as
// doc-verified against PLATFORM_NOTES.md §3.1's exact command sequence,
// not hardware-verified.
package autostart

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
)

func launchdGUIDomain() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("determine current user: %w", err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return "", fmt.Errorf("parse uid %q: %w", u.Uid, err)
	}
	return fmt.Sprintf("gui/%d", uid), nil
}

// enablePlatform writes the plist and loads it. PLATFORM_NOTES.md
// §3.1's primary command is `launchctl bootstrap`, with `load -w` as
// the fallback for older macOS — both are attempted, bootstrap first,
// and bootstrap's "already bootstrapped" case is tolerated (idempotent
// enable), since bootstrap errors if the label is already loaded.
func enablePlatform(exePath, logPath string) (Mechanism, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return MechanismNone, fmt.Errorf("resolve home dir: %w", err)
	}
	path := launchdPlistPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return MechanismNone, fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(launchdPlistContent(exePath, logPath)), 0o644); err != nil {
		return MechanismNone, fmt.Errorf("write plist: %w", err)
	}

	domain, err := launchdGUIDomain()
	if err != nil {
		return MechanismNone, err
	}

	if err := runLaunchctl("bootstrap", domain, path); err != nil {
		// bootstrap fails if the label is already loaded (an idempotent
		// second `enable`) — reconcile by bootout-then-bootstrap rather
		// than trusting a substring match on its error text, which
		// varies by macOS version.
		_ = runLaunchctl("bootout", domain+"/"+launchdLabel)
		if err := runLaunchctl("bootstrap", domain, path); err != nil {
			// Older macOS: bootstrap isn't the right verb at all.
			if err := runLaunchctl("load", "-w", path); err != nil {
				return MechanismNone, fmt.Errorf("launchctl bootstrap/load: %w", err)
			}
		}
	}
	return MechanismLaunchd, nil
}

// disablePlatform unloads the job (bootout, falling back to unload -w
// on older macOS) and removes the plist. Idempotent: if the plist is
// already absent there is nothing to unload or remove.
func disablePlatform() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}
	path := launchdPlistPath(home)
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil
		}
		return statErr
	}

	domain, err := launchdGUIDomain()
	if err != nil {
		return err
	}
	bootoutErr := runLaunchctl("bootout", domain+"/"+launchdLabel)
	if bootoutErr != nil {
		bootoutErr = runLaunchctl("unload", "-w", path)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}
	return bootoutErr
}

// queryPlatform reports `launchctl print <domain>/<label>`'s exit code
// plus the plist's existence, exactly as PLATFORM_NOTES.md §3.1
// specifies.
func queryPlatform() (Status, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Status{}, fmt.Errorf("resolve home dir: %w", err)
	}
	path := launchdPlistPath(home)
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return Status{}, nil
		}
		return Status{}, statErr
	}
	domain, err := launchdGUIDomain()
	if err != nil {
		return Status{}, err
	}
	printErr := runLaunchctl("print", domain+"/"+launchdLabel)
	return Status{
		Mechanism: MechanismLaunchd,
		Active:    printErr == nil,
		Detail:    fmt.Sprintf("launchd: plist present at %s, launchctl print %s", path, boolWord(printErr == nil)),
	}, nil
}

func runLaunchctl(args ...string) error {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %w (%s)", args, err, bytes.TrimSpace(out))
	}
	return nil
}

func boolWord(b bool) string {
	if b {
		return "succeeded"
	}
	return "failed"
}
