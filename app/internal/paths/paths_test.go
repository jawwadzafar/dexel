package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeHome returns a homeDir func for stateDirFor/binDirFor's injection
// point, and a getenv func reading only from the given map (so a table
// test drives every OS branch from explicit inputs, never the live
// process's real $HOME/$XDG_CONFIG_HOME/DEXEL_HOME — this is exactly the
// "parameter injection, not runtime.GOOS branching" design the package
// doc comment calls out).
func fakeHome(home string) func() (string, error) {
	return func() (string, error) { return home, nil }
}

func fakeGetenv(env map[string]string) func(string) string {
	return func(key string) string { return env[key] }
}

// TestStateDirForPerOSTable is PR-1's required "table-driven tests per
// OS" (MIGRATION_PLAN.md §PR-1 / PLATFORM_NOTES.md §1's filesystem
// locations table), driven entirely through stateDirFor's injected
// params so it exercises darwin's and windows's branches from whatever
// OS actually runs `go test`.
func TestStateDirForPerOSTable(t *testing.T) {
	const home = "/home/dev"
	cases := []struct {
		name string
		goos string
		env  map[string]string
		want string
	}{
		{
			name: "linux, no XDG_CONFIG_HOME set",
			goos: "linux",
			env:  map[string]string{},
			want: filepath.Join(home, ".config", "dexel"),
		},
		{
			name: "linux, XDG_CONFIG_HOME set",
			goos: "linux",
			env:  map[string]string{"XDG_CONFIG_HOME": "/home/dev/.xdgconfig"},
			want: filepath.Join("/home/dev/.xdgconfig", "dexel"),
		},
		{
			name: "darwin",
			goos: "darwin",
			env:  map[string]string{},
			want: filepath.Join(home, "Library", "Application Support", "dexel"),
		},
		{
			name: "darwin ignores XDG_CONFIG_HOME (Linux-only knob)",
			goos: "darwin",
			env:  map[string]string{"XDG_CONFIG_HOME": "/home/dev/.xdgconfig"},
			want: filepath.Join(home, "Library", "Application Support", "dexel"),
		},
		{
			name: "windows, LOCALAPPDATA set",
			goos: "windows",
			env:  map[string]string{"LOCALAPPDATA": `C:\Users\dev\AppData\Local`},
			want: filepath.Join(`C:\Users\dev\AppData\Local`, "dexel"),
		},
		{
			name: "windows, LOCALAPPDATA unset falls back to home",
			goos: "windows",
			env:  map[string]string{},
			want: filepath.Join(home, "AppData", "Local", "dexel"),
		},
		{
			name: "an unlisted unix (e.g. freebsd) takes the Linux/XDG branch",
			goos: "freebsd",
			env:  map[string]string{},
			want: filepath.Join(home, ".config", "dexel"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stateDirFor(tc.goos, fakeGetenv(tc.env), fakeHome(home))
			if err != nil {
				t.Fatalf("stateDirFor(%q): %v", tc.goos, err)
			}
			if got != tc.want {
				t.Errorf("stateDirFor(%q) = %q, want %q", tc.goos, got, tc.want)
			}
		})
	}
}

// TestBinDirForPerOSTable covers BinDir's per-OS table (PLATFORM_NOTES.md
// §1): ~/.local/bin on Linux AND macOS, %LOCALAPPDATA%\dexel\bin on
// Windows.
func TestBinDirForPerOSTable(t *testing.T) {
	const home = "/home/dev"
	cases := []struct {
		name string
		goos string
		env  map[string]string
		want string
	}{
		{name: "linux", goos: "linux", env: map[string]string{}, want: filepath.Join(home, ".local", "bin")},
		{name: "darwin", goos: "darwin", env: map[string]string{}, want: filepath.Join(home, ".local", "bin")},
		{
			name: "windows, LOCALAPPDATA set",
			goos: "windows",
			env:  map[string]string{"LOCALAPPDATA": `C:\Users\dev\AppData\Local`},
			want: filepath.Join(`C:\Users\dev\AppData\Local`, "dexel", "bin"),
		},
		{
			name: "windows, LOCALAPPDATA unset falls back to home",
			goos: "windows",
			env:  map[string]string{},
			want: filepath.Join(home, "AppData", "Local", "dexel", "bin"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := binDirFor(tc.goos, fakeGetenv(tc.env), fakeHome(home))
			if err != nil {
				t.Fatalf("binDirFor(%q): %v", tc.goos, err)
			}
			if got != tc.want {
				t.Errorf("binDirFor(%q) = %q, want %q", tc.goos, got, tc.want)
			}
		})
	}
}

// TestStateDirLinuxCompat proves PR-1's headline exit criterion: on
// Linux, with $XDG_CONFIG_HOME unset, StateDir() resolves BYTE IDENTICAL
// to what store.DefaultPath() returned before this package existed —
// ~/.config/dexel — so an existing save keeps loading with zero
// migration. It fakes $HOME (never touches the real one — the harness
// requires ~/.config/dexel be protected from every test) and seeds a
// pre-existing state.db there to prove the file is found completely
// untouched: same bytes, same directory, no relocation attempted (there
// is nothing to relocate FROM — Linux's legacy and current locations are
// the same directory by construction).
func TestStateDirLinuxCompat(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("this test asserts the real runtime.GOOS branch StateDir() takes; skipping on %s", runtime.GOOS)
	}
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEXEL_HOME", "")

	legacyDir := filepath.Join(tmpHome, ".config", "dexel")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("seed legacy dir: %v", err)
	}
	seeded := []byte("pre-existing save bytes, must not change")
	statePath := filepath.Join(legacyDir, "state.db")
	if err := os.WriteFile(statePath, seeded, 0o600); err != nil {
		t.Fatalf("seed state.db: %v", err)
	}
	before, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat seeded state.db: %v", err)
	}

	dir, err := StateDir()
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	if dir != legacyDir {
		t.Fatalf("StateDir() = %q, want %q (~/.config/dexel, zero migration)", dir, legacyDir)
	}

	got, err := os.ReadFile(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("read state.db at resolved StateDir: %v", err)
	}
	if string(got) != string(seeded) {
		t.Fatalf("state.db contents changed: got %q, want %q", got, seeded)
	}
	after, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat state.db after StateDir(): %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("state.db was touched (mtime changed from %v to %v) — StateDir() must not write or move anything on Linux", before.ModTime(), after.ModTime())
	}
}

// TestStateDirHomeOverrideIsolatesEverything proves DEXEL_HOME fully
// isolates state, config, logs and cache into one throwaway directory —
// the property the test suite/CI itself relies on to never touch a
// developer's real save.
func TestStateDirHomeOverrideIsolatesEverything(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DEXEL_HOME", tmp)

	state, err := StateDir()
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	if state != tmp {
		t.Fatalf("StateDir() = %q, want DEXEL_HOME override %q", state, tmp)
	}

	logDir, err := LogDir()
	if err != nil {
		t.Fatalf("LogDir: %v", err)
	}
	if want := filepath.Join(tmp, "logs"); logDir != want {
		t.Fatalf("LogDir() = %q, want %q", logDir, want)
	}

	cacheDir, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	if want := filepath.Join(tmp, "cache"); cacheDir != want {
		t.Fatalf("CacheDir() = %q, want %q", cacheDir, want)
	}
}

// TestRelocateLegacyMovesFilesOnceThenNoOps is the direct, OS-agnostic
// unit test of relocateLegacy — the mover both paths_darwin.go's and
// paths_windows.go's relocateHook call — proving it moves state.db,
// config.json and every quarantine/import sibling exactly once, and is a
// no-op on a second call (the files are simply no longer at the legacy
// location to move).
func TestRelocateLegacyMovesFilesOnceThenNoOps(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "legacy")
	newDir := filepath.Join(root, "new")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}

	files := map[string]string{
		"state.db":              "state bytes",
		"config.json":           `{"name":"dev"}`,
		"state.db.invalid":      "invalid quarantine",
		"state.db.future":       "future quarantine",
		"state.db.corrupt":      "corrupt quarantine",
		"state.json.imported":   "imported legacy json",
		"state.json.invalid":    "invalid json quarantine",
		"state.json.future":     "future json quarantine",
		"state.json.corrupt":    "corrupt json quarantine",
		"unrelated-sibling.tmp": "must NOT be moved — not in relocationFiles",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(legacyDir, name), []byte(contents), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	relocated, err := relocateLegacy(legacyDir, newDir)
	if err != nil {
		t.Fatalf("relocateLegacy: %v", err)
	}
	if !relocated {
		t.Fatalf("relocateLegacy reported no relocation on the first call")
	}
	for name := range files {
		if name == "unrelated-sibling.tmp" {
			continue
		}
		got, err := os.ReadFile(filepath.Join(newDir, name))
		if err != nil {
			t.Fatalf("read relocated %s: %v", name, err)
		}
		if string(got) != files[name] {
			t.Fatalf("relocated %s contents = %q, want %q", name, got, files[name])
		}
		if _, err := os.Stat(filepath.Join(legacyDir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s still exists at the legacy dir after relocation (want it MOVED, not copied)", name)
		}
	}
	if _, err := os.Stat(filepath.Join(legacyDir, "unrelated-sibling.tmp")); err != nil {
		t.Fatalf("unrelated-sibling.tmp was moved or removed; relocateLegacy must only touch relocationFiles: %v", err)
	}

	// Second call: the legacy state.db is gone, so this must be a
	// harmless no-op, not an error.
	relocatedAgain, err := relocateLegacy(legacyDir, newDir)
	if err != nil {
		t.Fatalf("relocateLegacy (second call): %v", err)
	}
	if relocatedAgain {
		t.Fatalf("relocateLegacy reported a relocation on the second call — want a no-op")
	}
}

// TestRelocateLegacyNoOpWhenDestinationAlreadyHasState proves a second
// install that already wrote a fresh state.db at newDir is never
// clobbered by a leftover legacy directory.
func TestRelocateLegacyNoOpWhenDestinationAlreadyHasState(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "legacy")
	newDir := filepath.Join(root, "new")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatalf("mkdir new: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "state.db"), []byte("legacy"), 0o600); err != nil {
		t.Fatalf("seed legacy state.db: %v", err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "state.db"), []byte("current"), 0o600); err != nil {
		t.Fatalf("seed new state.db: %v", err)
	}

	relocated, err := relocateLegacy(legacyDir, newDir)
	if err != nil {
		t.Fatalf("relocateLegacy: %v", err)
	}
	if relocated {
		t.Fatalf("relocateLegacy reported a relocation when newDir already had a state.db")
	}
	got, err := os.ReadFile(filepath.Join(newDir, "state.db"))
	if err != nil {
		t.Fatalf("read new state.db: %v", err)
	}
	if string(got) != "current" {
		t.Fatalf("new state.db was overwritten: got %q, want %q", got, "current")
	}
}

// TestRelocateLegacyNoOpWhenLegacyAndNewAreTheSameDir is Linux's shape by
// construction — StateDir() there returns exactly the legacy directory —
// proven directly against relocateLegacy regardless of build tags.
func TestRelocateLegacyNoOpWhenLegacyAndNewAreTheSameDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.db"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed state.db: %v", err)
	}
	relocated, err := relocateLegacy(dir, dir)
	if err != nil {
		t.Fatalf("relocateLegacy: %v", err)
	}
	if relocated {
		t.Fatalf("relocateLegacy reported a relocation when legacyDir == newDir")
	}
}

// TestRelocateLegacyNoOpWhenNothingToRelocate covers a fresh install:
// neither directory has ever had a state.db.
func TestRelocateLegacyNoOpWhenNothingToRelocate(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "legacy")
	newDir := filepath.Join(root, "new")
	relocated, err := relocateLegacy(legacyDir, newDir)
	if err != nil {
		t.Fatalf("relocateLegacy: %v", err)
	}
	if relocated {
		t.Fatalf("relocateLegacy reported a relocation with nothing to relocate")
	}
	if _, err := os.Stat(newDir); !os.IsNotExist(err) {
		t.Fatalf("newDir was created despite nothing to relocate")
	}
}

// TestRelocateLegacyMovesAStateJSONOnlyInstall is SF-6's regression test
// (docs/plan/REVIEW-2026-08-22.md). relocateLegacy used to trigger ONLY
// on legacyDir/state.db, and relocationFiles listed state.json.imported
// but not state.json — so a macOS/Windows install created by a
// post-rename, pre-DB-1 build (state.json, no state.db) relocated
// nothing: the new platform directory came up empty, the user started
// fresh, and their real save sat orphaned in a directory the binary would
// never consult again. That is the exact compat rule the relocation
// exists to honour.
func TestRelocateLegacyMovesAStateJSONOnlyInstall(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "legacy")
	newDir := filepath.Join(root, "new")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "state.json"), []byte(`{"schema":6}`), 0o600); err != nil {
		t.Fatalf("seed state.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "config.json"), []byte(`{"name":"dev"}`), 0o600); err != nil {
		t.Fatalf("seed config.json: %v", err)
	}

	relocated, err := relocateLegacy(legacyDir, newDir)
	if err != nil {
		t.Fatalf("relocateLegacy: %v", err)
	}
	if !relocated {
		t.Fatal("a state.json-only install must relocate — it is a real save with no state.db yet")
	}
	for _, name := range []string{"state.json", "config.json"} {
		if _, err := os.Stat(filepath.Join(newDir, name)); err != nil {
			t.Errorf("%s did not arrive in the new state dir: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(legacyDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s was left behind in the legacy dir (relocation must move, not copy)", name)
		}
	}

	// Idempotent: a second call finds the trigger in newDir and does
	// nothing, and in particular cannot overwrite the relocated save.
	again, err := relocateLegacy(legacyDir, newDir)
	if err != nil {
		t.Fatalf("relocateLegacy (second call): %v", err)
	}
	if again {
		t.Error("relocateLegacy relocated twice")
	}
}

// TestRelocateLegacyNeverOverwritesAnEstablishedStateJSON pins the guard
// that makes the SF-6 fix safe: os.Rename replaces its destination, so a
// newDir that already holds a state.json must stop the relocation dead
// rather than move an older file on top of the live one.
func TestRelocateLegacyNeverOverwritesAnEstablishedStateJSON(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "legacy")
	newDir := filepath.Join(root, "new")
	for _, dir := range []string{legacyDir, newDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "state.json"), []byte("old"), 0o600); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "state.json"), []byte("live"), 0o600); err != nil {
		t.Fatalf("seed new: %v", err)
	}

	relocated, err := relocateLegacy(legacyDir, newDir)
	if err != nil {
		t.Fatalf("relocateLegacy: %v", err)
	}
	if relocated {
		t.Error("relocateLegacy reported a relocation into an established state dir")
	}
	live, err := os.ReadFile(filepath.Join(newDir, "state.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(live) != "live" {
		t.Fatalf("the established state.json was overwritten with %q", live)
	}
}
