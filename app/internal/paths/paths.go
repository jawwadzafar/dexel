// Package paths is the ONLY place in dexel that knows a filesystem
// location (dev_docs/production-runtime/ARCHITECTURE.md "Decision 9 — A
// new app/internal/paths package is the only place that knows a path",
// dev_docs/production-runtime/MIGRATION_PLAN.md §PR-1). It resolves the
// per-OS user-level locations documented in
// dev_docs/production-runtime/PLATFORM_NOTES.md §1:
//
//	StateDir  state.db, config.json, runtime.json, runtime.lock
//	LogDir    <StateDir>/logs
//	BinDir    where the installer put (and `dexel update` replaces) the binary
//	CacheDir  <StateDir>/cache — update downloads, deleted after use
//
// The critical compatibility rule (PLATFORM_NOTES.md §1, ARCHITECTURE.md
// §4): Linux keeps ~/.config/dexel exactly as
// store.DefaultPath()/store.ConfigPath() hardcoded it before this package
// existed, because it is where the only real saves in existence today
// live. $XDG_CONFIG_HOME is honoured on top of that so the value is
// configuration rather than a second hardcode, but an unset
// $XDG_CONFIG_HOME on Linux must resolve BYTE IDENTICAL to the pre-existing
// behaviour — see TestStateDirLinuxCompat.
//
// Every exported function's actual OS-branching logic lives in the
// unexported, parameter-injected core below (stateDirFor / binDirFor) so a
// single test binary — on whatever OS actually runs `go test` — can drive
// every platform's branch table-style, per this repo's testing convention
// of not hiding untestable logic behind a bare runtime.GOOS branch. The
// three build-tagged files (paths_darwin.go / paths_windows.go /
// paths_other.go, mirroring the existing provider_select_*.go trio) each
// supply exactly one function, relocateHook, whose Linux implementation is
// a no-op BY CONSTRUCTION — which file is compiled in, not a runtime
// condition that could rot silently — because PLATFORM_NOTES.md §1 states
// "Linux never takes this branch".
package paths

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
)

// dexelHomeEnv overrides StateDir wholesale, on every platform
// (PLATFORM_NOTES.md §1: "DEXEL_HOME overrides StateDir on every
// platform"). It exists so the test suite, CI, and a throwaway dev
// instance can run without ever touching the developer's real state —
// see TestStateDirHomeOverrideIsolatesEverything.
const dexelHomeEnv = "DEXEL_HOME"

// relocationFiles is the exact set a one-time relocation
// (PLATFORM_NOTES.md §1 "One-time relocation (macOS, Windows only)")
// moves out of the legacy ~/.config/dexel and into the real StateDir:
// the live state.db and config.json, plus every quarantine/import
// sibling app/internal/store/db.go's Load path can produce —
// state.db.invalid / .future / .corrupt from db.go's quarantine(), and
// state.json.imported from db.go's importJSON().
var relocationFiles = []string{
	"state.db",
	"config.json",
	"state.db.invalid",
	"state.db.future",
	"state.db.corrupt",
	"state.json.imported",
}

// stateDirFor is StateDir's pure core: every per-OS branch
// PLATFORM_NOTES.md §1's table documents, decided entirely from the
// explicit goos/getenv/homeDir inputs rather than the live process's
// runtime.GOOS/os.Getenv/os.UserHomeDir. It does NOT apply the DEXEL_HOME
// override — that is StateDir's job, once, so relocation logic below can
// tell "the user asked for DEXEL_HOME" apart from "this is the platform
// default".
func stateDirFor(goos string, getenv func(string) string, homeDir func() (string, error)) (string, error) {
	switch goos {
	case "windows":
		if v := getenv("LOCALAPPDATA"); v != "" {
			return filepath.Join(v, "dexel"), nil
		}
		home, err := homeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		// %LOCALAPPDATA% is unset only in unusual/test environments;
		// AppData\Local is where it points in practice.
		return filepath.Join(home, "AppData", "Local", "dexel"), nil
	case "darwin":
		home, err := homeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support", "dexel"), nil
	default:
		// Linux, and every other unix PLATFORM_NOTES.md doesn't call out
		// by name — the "Linux" row of the table, XDG-first.
		if v := getenv("XDG_CONFIG_HOME"); v != "" {
			return filepath.Join(v, "dexel"), nil
		}
		home, err := homeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		return filepath.Join(home, ".config", "dexel"), nil
	}
}

// binDirFor is BinDir's pure core, same shape as stateDirFor.
// PLATFORM_NOTES.md §1: BinDir is `~/.local/bin` on Linux AND macOS
// (never `/usr/local/bin` — ARCHITECTURE.md: the installer must work
// without sudo), and `%LOCALAPPDATA%\dexel\bin` on Windows.
func binDirFor(goos string, getenv func(string) string, homeDir func() (string, error)) (string, error) {
	if goos == "windows" {
		if v := getenv("LOCALAPPDATA"); v != "" {
			return filepath.Join(v, "dexel", "bin"), nil
		}
		home, err := homeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		return filepath.Join(home, "AppData", "Local", "dexel", "bin"), nil
	}
	home, err := homeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "bin"), nil
}

// StateDir resolves the directory holding state.db, config.json,
// runtime.json and runtime.lock (PLATFORM_NOTES.md §1). DEXEL_HOME, if
// set, overrides it wholesale on every platform. Otherwise the platform
// default (stateDirFor) is used, and — once, harmlessly, and only on
// macOS/Windows, per relocateHook's per-OS implementation — a legacy
// ~/.config/dexel left behind by a from-source run on those platforms
// before this package existed is relocated into it.
func StateDir() (string, error) {
	if v := os.Getenv(dexelHomeEnv); v != "" {
		return v, nil
	}
	dir, err := stateDirFor(runtime.GOOS, os.Getenv, os.UserHomeDir)
	if err != nil {
		return "", err
	}
	if err := relocateHook(dir); err != nil {
		return "", fmt.Errorf("relocate legacy state dir: %w", err)
	}
	return dir, nil
}

// LogDir is <StateDir>/logs (PLATFORM_NOTES.md §1 — "Logs under StateDir
// on every platform", ARCHITECTURE.md §4).
func LogDir() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "logs"), nil
}

// CacheDir is <StateDir>/cache (PLATFORM_NOTES.md §1). `dexel update`
// writes only here and to BinDir — never used as the final staging step
// for a binary swap, because it may not share a filesystem with BinDir
// (PLATFORM_NOTES.md §6).
func CacheDir() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cache"), nil
}

// BinDir is where the installer places (and `dexel update` replaces) the
// binary (PLATFORM_NOTES.md §1). Unlike StateDir it is NOT overridden by
// DEXEL_HOME — DEXEL_HOME steers where state lives, not where the binary
// that reads it does.
func BinDir() (string, error) {
	return binDirFor(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

// relocateLegacy moves relocationFiles from legacyDir into newDir, once.
// It is the pure, OS-agnostic mover both paths_darwin.go and
// paths_windows.go's relocateHook call — kept parameter-injected (two
// explicit directories, no environment lookups of its own) so it is
// directly unit-testable with plain temp directories regardless of host
// OS. Ordering mirrors app/internal/store/db.go's importJSON precedent:
// the destination directory is created first, then each file that exists
// is moved (os.Rename — never copy, never merge, never delete-without-
// moving), then one line is logged naming both paths. A no-op when
// legacyDir == newDir (which is exactly what "Linux never takes this
// branch" reduces to, since Linux's StateDir already IS the legacy
// directory), when newDir already has a state.db (already relocated, or
// this install never used the legacy location), or when legacyDir has no
// state.db (nothing to relocate).
func relocateLegacy(legacyDir, newDir string) (relocated bool, err error) {
	if legacyDir == newDir {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(newDir, "state.db")); err == nil {
		return false, nil
	}
	legacyDB := filepath.Join(legacyDir, "state.db")
	if _, err := os.Stat(legacyDB); err != nil {
		return false, nil
	}
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		return false, fmt.Errorf("create %s: %w", newDir, err)
	}
	for _, name := range relocationFiles {
		src := filepath.Join(legacyDir, name)
		if _, statErr := os.Stat(src); statErr != nil {
			continue
		}
		if err := os.Rename(src, filepath.Join(newDir, name)); err != nil {
			return false, fmt.Errorf("relocate %s: %w", src, err)
		}
	}
	log.Printf("dexel: relocated state from %s to %s", legacyDir, newDir)
	return true, nil
}
