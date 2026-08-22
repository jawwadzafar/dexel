// Package assets is the disk-side helper for dexel's sprite tree
// (app/assets/ — the art agent's PNGs, tools/gen_assets.py's output,
// docs/art-direction.md's manifest).
//
// Since EMBED-1 (docs/plan/ROADMAP.md) the sprites are compiled INTO the
// server binary (app/embed.go), so the running product no longer has to
// find them anywhere: nothing is located at runtime and nothing can be moved
// away from the binary. Two callers still need the real directory on disk,
// and that is all this package is for now:
//
//  1. main.go's DEV override — $DEXEL_ASSETS_DIR, via OverrideDir and
//     HasSentinel — so a sprite can be regenerated and reloaded without
//     rebuilding Go.
//  2. Go tests that assert catalog entries against the real PNG files
//     (internal/game's catalog/assets test), which run with a cwd inside the
//     checkout and therefore want the search below, not the embedded copy.
//
// Locate/LocateVerbose are what serve case 2 (and any future tool that wants
// the checkout's own assets/): they walk upward looking for the directory,
// because a test binary's cwd is its own package directory, several levels
// below app/.
//
// Historical note, so the layout is not "fixed" back: assets/ used to live
// at the REPOSITORY ROOT, and app/public/assets was once a symlink to it.
// Both are gone. go:embed can only reach files inside the module that
// declares the directive, and this module's root is app/ — so the sprites
// had to move to app/assets/ for the single-binary build to be possible at
// all.
package assets

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvOverride is the environment variable that, when set to a non-empty
// value, is used verbatim as the assets directory — no search, no existence
// check. Since EMBED-1 this is a DEV override (serve sprites off disk
// instead of the embedded copy) rather than the escape hatch a packaged
// install depended on; a packaged install needs nothing, because the
// sprites are inside the binary.
const EnvOverride = "DEXEL_ASSETS_DIR"

// SentinelFile is a file every real assets directory must contain
// (docs/art-direction.md "Fixed scenery") — used to tell a real assets
// directory apart from an empty or unrelated one of the same name. Exported
// so main.go can say WHICH file was missing when an override looks wrong.
const SentinelFile = "room_back.png"

// Attempt records one candidate LocateVerbose checked, in the order it was
// tried, and how it turned out — the raw material for a log line that says
// exactly what was tried instead of a bare "assets directory not found".
// Strategy is a complete, human-readable sentence: which strategy, which
// literal path it resolved to, and whether that path held SentinelFile.
type Attempt struct {
	Strategy string
	Found    bool
}

// OverrideDir reports $DEXEL_ASSETS_DIR and whether it was set to a
// non-empty value. This is the whole of main.go's disk-override decision
// (EMBED-1): set means "serve /assets/ from this path, verbatim", unset
// means "serve the embedded copy" — no search either way, so an override
// typo can never be masked by a lucky upward walk finding some other
// checkout's assets.
func OverrideDir() (string, bool) {
	dir := os.Getenv(EnvOverride)
	return dir, dir != ""
}

// HasSentinel reports whether dir looks like a real assets directory (it
// holds SentinelFile). main.go uses it to WARN about a bad override without
// refusing it — the override stays verbatim by contract.
func HasSentinel(dir string) bool {
	return fileExists(filepath.Join(dir, SentinelFile))
}

// Locate finds the checkout's assets directory using the default strategy
// (no extra candidates) — see LocateVerbose.
func Locate() (string, error) {
	dir, _, err := LocateVerbose()
	return dir, err
}

// LocateVerbose is Locate's implementation, plus the full list of Attempts
// made (in order), so a caller can report exactly what was checked rather
// than just pass/fail. Lookup order (first hit wins):
//
//  1. $DEXEL_ASSETS_DIR, if set — used as-is, no existence check. Trusted
//     verbatim on purpose (see EnvOverride), so a typo'd value is reported
//     as "found" here and should be treated as suspect by the caller.
//  2. Walking upward from the running executable's own directory
//     (os.Executable()), looking at each level for an "assets"
//     subdirectory containing SentinelFile. With the sprites at
//     app/assets/, a binary built into app/ hits this on its first probe.
//  3. The same upward walk from the process's current working directory —
//     the case that matters most now: `go test ./...` from app/ runs each
//     test binary with cwd set to that package's own directory (e.g.
//     app/internal/game), so the walk climbs to app/ and finds app/assets.
//  4. Each of extraCandidates, checked directly (no upward walk) for
//     SentinelFile.
//
// Returns an error only if none of the above finds a directory holding
// SentinelFile. Note that a plain server run never calls this at all: the
// running server serves the embedded sprites, or an explicit OverrideDir.
func LocateVerbose(extraCandidates ...string) (dir string, attempts []Attempt, err error) {
	if envDir, set := OverrideDir(); set {
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
		if fileExists(filepath.Join(cand, SentinelFile)) {
			attempts = append(attempts, Attempt{Strategy: fmt.Sprintf("derived candidate %s: found", cand), Found: true})
			return cand, attempts, nil
		}
		attempts = append(attempts, Attempt{Strategy: fmt.Sprintf("derived candidate %s: not found", cand)})
	}

	return "", attempts, fmt.Errorf(
		"could not locate the assets directory (app/assets — checked $%s, the executable's directory upward, the working directory upward, and %d derived candidate(s) — set %s to override)",
		EnvOverride, len(extraCandidates), EnvOverride,
	)
}

// searchUpward walks from start toward the filesystem root, testing
// start/assets, start/../assets, start/../../assets, ... and returns the
// first one that contains SentinelFile.
func searchUpward(start string) (string, bool) {
	dir := start
	for {
		candidate := filepath.Join(dir, "assets")
		if fileExists(filepath.Join(candidate, SentinelFile)) {
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
