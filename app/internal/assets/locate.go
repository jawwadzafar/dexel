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

// Attempt records one candidate LocateVerbose checked, in the order it was
// tried, and how it turned out — the raw material for a log line (or an
// /api/health payload) that says exactly what was tried instead of a bare
// "assets route not registered". Strategy is a complete, human-readable
// sentence: which strategy, which literal path it resolved to, and whether
// that path held sentinelFile.
type Attempt struct {
	Strategy string
	Found    bool
}

// Locate finds the assets/ directory using the default strategy (no
// public-dir-derived fallback candidate) — see LocateVerbose.
func Locate() (string, error) {
	dir, _, err := LocateVerbose()
	return dir, err
}

// LocateVerbose is Locate's implementation, plus the full list of Attempts
// made (in order), so a caller can log or report exactly what was checked
// rather than just pass/fail. Lookup order (first hit wins), because this
// binary runs from several very different places — `go run .` from app/
// during development, a built binary sitting somewhere under (or beside)
// the repo once packaged, or a binary run from a completely unrelated cwd:
//
//  1. $DEVCOMPANION_ASSETS_DIR, if set — used as-is, no existence check.
//     The escape hatch for a packaged install where no upward walk or
//     derived candidate could ever find assets/; trusted verbatim on
//     purpose, so a typo'd or stale value here is reported as "found" by
//     this function but should be treated as suspect by the caller (main.go
//     surfaces it via /api/health so a broken override is visible instead
//     of just producing silent 404s).
//  2. Walking upward from the running executable's own directory
//     (os.Executable()), looking at each level for an "assets"
//     subdirectory containing sentinelFile.
//  3. The same upward walk from the process's current working directory
//     — needed because `go run .` builds the binary into a temporary
//     cache directory far from the repo, so step 2 alone would miss the
//     common development invocation (`go run .` from app/, or `go test
//     ./...` from app/, whose test binaries run with cwd set to each
//     package directory).
//  4. Each of extraCandidates, checked directly (no upward walk) for
//     sentinelFile. main.go passes the assets/ directory implied by
//     wherever it actually resolved the frontend's public/ directory to on
//     disk — the same base directory main.go is already trusting for the
//     frontend, so public/ and assets/ are unified onto one resolution
//     rather than two lookups that can silently disagree.
//
// Returns an error only if none of the above finds a directory holding
// sentinelFile.
func LocateVerbose(extraCandidates ...string) (dir string, attempts []Attempt, err error) {
	if envDir := os.Getenv(EnvOverride); envDir != "" {
		attempts = append(attempts, Attempt{
			Strategy: fmt.Sprintf("$%s=%q: used as-is (no existence check)", EnvOverride, envDir),
			Found:    true,
		})
		return envDir, attempts, nil
	}
	attempts = append(attempts, Attempt{Strategy: fmt.Sprintf("$%s: not set", EnvOverride)})

	if exe, exeErr := os.Executable(); exeErr == nil {
		start := filepath.Dir(exe)
		if d, ok := searchUpward(start); ok {
			attempts = append(attempts, Attempt{Strategy: fmt.Sprintf("executable dir upward from %s: found %s", start, d), Found: true})
			return d, attempts, nil
		}
		attempts = append(attempts, Attempt{Strategy: fmt.Sprintf("executable dir upward from %s: not found", start)})
	} else {
		attempts = append(attempts, Attempt{Strategy: fmt.Sprintf("executable dir: os.Executable() failed: %v", exeErr)})
	}

	if cwd, cwdErr := os.Getwd(); cwdErr == nil {
		if d, ok := searchUpward(cwd); ok {
			attempts = append(attempts, Attempt{Strategy: fmt.Sprintf("cwd upward from %s: found %s", cwd, d), Found: true})
			return d, attempts, nil
		}
		attempts = append(attempts, Attempt{Strategy: fmt.Sprintf("cwd upward from %s: not found", cwd)})
	} else {
		attempts = append(attempts, Attempt{Strategy: fmt.Sprintf("cwd: os.Getwd() failed: %v", cwdErr)})
	}

	for _, cand := range extraCandidates {
		if cand == "" {
			continue
		}
		if fileExists(filepath.Join(cand, sentinelFile)) {
			attempts = append(attempts, Attempt{Strategy: fmt.Sprintf("derived candidate %s: found", cand), Found: true})
			return cand, attempts, nil
		}
		attempts = append(attempts, Attempt{Strategy: fmt.Sprintf("derived candidate %s: not found", cand)})
	}

	return "", attempts, fmt.Errorf(
		"could not locate assets/ directory (checked $%s, the executable's directory upward, the working directory upward, and %d derived candidate(s) — set %s to override)",
		EnvOverride, len(extraCandidates), EnvOverride,
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
