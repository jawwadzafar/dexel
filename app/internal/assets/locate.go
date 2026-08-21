// Package assets locates the repository's top-level assets/ directory —
// the art agent's 82 sprite/thumbnail PNGs (docs/art-direction.md's
// manifest) — from wherever this binary happens to be running.
//
// It exists because app/public/ (served at "/") and the repo's assets/
// (served at "/assets/") are two different static trees owned by two
// different agents, and only one of them (public/) is guaranteed to sit
// next to the binary in every run mode. A symlink from
// app/public/assets -> ../../assets was tried and removed: a real route
// with a real lookup strategy survives being packaged into a binary that
// no longer lives inside a git checkout, which a symlink committed to git
// does not.
package assets

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvOverride is the environment variable that, when set to a non-empty
// value, is used verbatim as the assets directory — no search, no
// existence check. The escape hatch for a packaged install (e.g. a Wails
// bundle's Resources dir) where no upward walk from the executable or the
// cwd could ever find assets/.
const EnvOverride = "DEVCOMPANION_ASSETS_DIR"

// sentinelFile is a file every real assets/ directory must contain
// (docs/art-direction.md "Fixed scenery") — used to tell a real assets/
// directory apart from an empty or unrelated one of the same name.
const sentinelFile = "room_back.png"

// Locate finds the assets/ directory. Lookup order (first hit wins),
// because this binary runs from two very different places — `go run .`
// from app/ during development, and a built binary sitting somewhere
// under (or beside) the repo once packaged:
//
//  1. $DEVCOMPANION_ASSETS_DIR, if set — used as-is.
//  2. Walking upward from the running executable's own directory
//     (os.Executable()), looking at each level for an "assets"
//     subdirectory containing sentinelFile.
//  3. The same upward walk from the process's current working directory
//     — needed because `go run .` builds the binary into a temporary
//     cache directory far from the repo, so step 2 alone would miss the
//     common development invocation (`go run .` from app/, or `go test
//     ./...` from app/, whose test binaries run with cwd set to each
//     package directory).
//
// Returns an error only if none of the three finds a directory holding
// sentinelFile.
func Locate() (string, error) {
	if dir := os.Getenv(EnvOverride); dir != "" {
		return dir, nil
	}
	if exe, err := os.Executable(); err == nil {
		if dir, ok := searchUpward(filepath.Dir(exe)); ok {
			return dir, nil
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if dir, ok := searchUpward(cwd); ok {
			return dir, nil
		}
	}
	return "", fmt.Errorf(
		"could not locate assets/ directory (checked $%s, the executable's directory upward, and the working directory upward — set %s to override)",
		EnvOverride, EnvOverride,
	)
}

// searchUpward walks from start toward the filesystem root, testing
// start/assets, start/../assets, start/../../assets, ... and returns the
// first one that contains sentinelFile.
func searchUpward(start string) (string, bool) {
	dir := start
	for {
		candidate := filepath.Join(dir, "assets")
		if fileExists(filepath.Join(candidate, sentinelFile)) {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
