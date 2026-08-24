//go:build windows

// windows.go implements Windows' autostart mechanism
// (docs/production-runtime/PLATFORM_NOTES.md §3.3): one value under
// HKCU\Software\Microsoft\Windows\CurrentVersion\Run, via
// golang.org/x/sys/windows/registry (already a direct dependency of
// app/go.mod) — no COM, no IShellLink, no scheduler XML, and reversible
// with a single DeleteValue.
//
// STATUS: AUTHORED, NOT VERIFIED ON HARDWARE — this repo's dev box is
// Linux, and golang.org/x/sys/windows/registry is itself
// `//go:build windows`, so this file cannot even be TYPE-CHECKED here,
// only cross-COMPILED (`GOOS=windows go build ./...`) and, for its test
// file, cross-compiled as a test binary that is never run. The same
// honesty split desktop/README.md and launchd_darwin.go carry:
//
//	| | |
//	|---|---|
//	| The exact value name/data (windowsRunValueData, autostart.go) | unit-tested on Linux, byte for byte |
//	| registry.OpenKey/SetStringValue/DeleteValue actually invoked   | never run |
//	| The Run-key-fires-at-login behaviour, the console-flash cost   | never observed |
package autostart

import (
	"fmt"

	"golang.org/x/sys/windows/registry"
)

func enablePlatform(exePath, logPath string, opts Options) (Result, error) {
	// logPath is unused here: the Run key has no supervisor and no log
	// redirection of its own — `dexel start` (which this points at, via
	// windowsRunValueData) owns its own log file, the same reasoning as
	// the Linux XDG fallback.
	//
	// opts is unused for the same reason it is unused on Linux:
	// BareExecutable opts out of macOS .app bundle attribution, and the
	// Run key always names exePath. A no-op, not an error.
	_ = logPath
	_ = opts
	key, _, err := registry.CreateKey(registry.CURRENT_USER, windowsRunKeyPath, registry.SET_VALUE)
	if err != nil {
		return Result{}, fmt.Errorf("open/create Run key: %w", err)
	}
	defer key.Close()
	if err := key.SetStringValue(windowsRunValueName, windowsRunValueData(exePath)); err != nil {
		return Result{}, fmt.Errorf("set Run value: %w", err)
	}
	return Result{Mechanism: MechanismWindowsRun, Program: exePath}, nil
}

// disablePlatform deletes the value. Idempotent: deleting an
// already-absent value is reported as success, not an error.
func disablePlatform() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, windowsRunKeyPath, registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return fmt.Errorf("open Run key: %w", err)
	}
	defer key.Close()
	if err := key.DeleteValue(windowsRunValueName); err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return fmt.Errorf("delete Run value: %w", err)
	}
	return nil
}

func queryPlatform() (Status, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, windowsRunKeyPath, registry.QUERY_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return Status{}, nil
		}
		return Status{}, fmt.Errorf("open Run key: %w", err)
	}
	defer key.Close()
	value, _, err := key.GetStringValue(windowsRunValueName)
	if err != nil {
		if err == registry.ErrNotExist {
			return Status{}, nil
		}
		return Status{}, fmt.Errorf("read Run value: %w", err)
	}
	return Status{
		Mechanism: MechanismWindowsRun,
		Active:    true,
		Detail:    fmt.Sprintf("HKCU Run\\%s = %s", windowsRunValueName, value),
	}, nil
}
