package paths

import (
	"path/filepath"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/lifecycle"
)

// TestRuntimeLockAndLogFilesUnderDexelHome pins PLATFORM_NOTES.md §1's
// table for the three files PR-3 adds, and pins them THROUGH the
// DEXEL_HOME override — which is the mechanism the whole end-to-end test
// suite and CI rely on to run a real runtime without touching a
// developer's real state ("Two state directories = two independent
// instances, by design", PLATFORM_NOTES.md §5).
func TestRuntimeLockAndLogFilesUnderDexelHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv(dexelHomeEnv, home)

	got, err := RuntimeFile()
	if err != nil {
		t.Fatalf("RuntimeFile: %v", err)
	}
	if want := filepath.Join(home, "runtime.json"); got != want {
		t.Fatalf("RuntimeFile = %q, want %q", got, want)
	}

	got, err = LockFile()
	if err != nil {
		t.Fatalf("LockFile: %v", err)
	}
	if want := filepath.Join(home, "runtime.lock"); got != want {
		t.Fatalf("LockFile = %q, want %q", got, want)
	}

	got, err = LogFile()
	if err != nil {
		t.Fatalf("LogFile: %v", err)
	}
	// PLATFORM_NOTES.md §1: logs live at <StateDir>/logs/runtime.log on
	// EVERY platform — an accepted, deliberate deviation from macOS's
	// ~/Library/Logs convention, so `uninstall --purge` has exactly one
	// directory to remove.
	if want := filepath.Join(home, "logs", "runtime.log"); got != want {
		t.Fatalf("LogFile = %q, want %q", got, want)
	}
}

// TestRuntimeFileNamesMatchLifecycle closes the one seam this package's
// additions introduce: the three basenames are spelled BOTH here (so
// paths stays dependency-free) and in app/internal/lifecycle (next to the
// code that reads the files). Nothing but a test can stop those two
// spellings from drifting apart, and drift would mean the CLI writing
// runtime.json where nothing looks for it.
func TestRuntimeFileNamesMatchLifecycle(t *testing.T) {
	if runtimeFileName != lifecycle.RuntimeFileName {
		t.Fatalf("paths says %q, lifecycle says %q", runtimeFileName, lifecycle.RuntimeFileName)
	}
	if lockFileName != lifecycle.LockFileName {
		t.Fatalf("paths says %q, lifecycle says %q", lockFileName, lifecycle.LockFileName)
	}
	if logFileName != lifecycle.LogFileName {
		t.Fatalf("paths says %q, lifecycle says %q", logFileName, lifecycle.LogFileName)
	}
}

// TestRuntimeFilesSitBesideStateAndConfig pins the co-location
// ARCHITECTURE.md Decision 9 states: state.db, config.json, runtime.json
// and runtime.lock share ONE directory, and logs/ and cache/ are its
// children. `dexel uninstall --purge` removing a single tree depends on
// this being true rather than assumed.
func TestRuntimeFilesSitBesideStateAndConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv(dexelHomeEnv, home)

	stateDir, err := StateDir()
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	runtimeFile, err := RuntimeFile()
	if err != nil {
		t.Fatalf("RuntimeFile: %v", err)
	}
	lockFile, err := LockFile()
	if err != nil {
		t.Fatalf("LockFile: %v", err)
	}
	logFile, err := LogFile()
	if err != nil {
		t.Fatalf("LogFile: %v", err)
	}
	if filepath.Dir(runtimeFile) != stateDir {
		t.Fatalf("runtime.json is in %q, not the state dir %q", filepath.Dir(runtimeFile), stateDir)
	}
	if filepath.Dir(lockFile) != stateDir {
		t.Fatalf("runtime.lock is in %q, not the state dir %q", filepath.Dir(lockFile), stateDir)
	}
	if filepath.Dir(filepath.Dir(logFile)) != stateDir {
		t.Fatalf("the log dir %q is not directly under the state dir %q", filepath.Dir(logFile), stateDir)
	}
}
