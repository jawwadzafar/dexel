package lifecycle

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PortFileName is the sticky-port record: <StateDir>/lastport, one
// decimal port number and a newline.
//
// It is the fourth file in this package's set, and the ONLY one that
// deliberately OUTLIVES the runtime that wrote it. runtime.json is
// deleted on every clean exit (it answers "is a runtime up right now?");
// this file answers a different question — "which port did the last
// runtime here answer on?" — and that answer is still useful, in fact
// most useful, when no runtime is up.
const PortFileName = "lastport"

// PortFilePath joins PortFileName onto a state directory, like
// RuntimePath/LockPath do for theirs.
func PortFilePath(stateDir string) string { return filepath.Join(stateDir, PortFileName) }

// ReadStickyPort reads the recorded port. A missing file is (0, nil) —
// "no record" is the expected answer on a machine where dexel has never
// run, not a failure. A file that exists but does not hold a usable port
// IS an error, so a caller can say so out loud rather than silently
// behaving as if the record had never been written.
func ReadStickyPort(stateDir string) (int, error) {
	data, err := os.ReadFile(PortFilePath(stateDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read %s: %w", PortFileName, err)
	}
	port, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if convErr != nil {
		return 0, fmt.Errorf("parse %s: %w", PortFileName, convErr)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("%s holds %d, which is not a usable TCP port", PortFileName, port)
	}
	return port, nil
}

// WriteStickyPort records the port a runtime just bound.
//
// Atomically (tmp + rename), for the same reason WriteRuntime is: the
// next `dexel` invocation may read this file at any instant, and a
// half-written "80" where "8081" was meant would point a rebind at the
// wrong port. At 0600 to match every other file this package writes into
// the state directory — the port is not a secret, but nothing in there
// needs to be group-readable either.
func WriteStickyPort(stateDir string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("refusing to record %d as a sticky port", port)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create state dir %s: %w", stateDir, err)
	}
	path := PortFilePath(stateDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(port)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write temp %s: %w", PortFileName, err)
	}
	defer func() { _ = os.Remove(tmp) }() // no-op once the rename succeeds
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s into place: %w", PortFileName, err)
	}
	return nil
}

// StickyAddr decides which address a runtime should TRY first, given the
// address it was asked for and the port the last runtime here used
// (docs/plan/BUGS-RESILIENCE.md R9).
//
// It returns ("", false) — "just use what you were given" — unless BOTH
// halves hold:
//
//   - the requested port is 0, i.e. this is the runtime's own default
//     "let the OS pick" address. An address a human or a script typed a
//     port into is NEVER second-guessed; -addr means what it says.
//   - there is a recorded port to reuse.
//
// The host part of the requested address is preserved exactly, so a
// runtime asked for [::1]:0 retries on [::1], not on 127.0.0.1.
//
// This is only ever an ATTEMPT. The caller binds the returned address and
// falls back to the requested one if that bind fails, which is what makes
// stickiness safe: a port some other process took in the meantime cannot
// stop this runtime from starting, and cannot be stolen from that
// process either.
func StickyAddr(requested string, sticky int) (string, bool) {
	if sticky < 1 || sticky > 65535 {
		return "", false
	}
	host, port, err := net.SplitHostPort(requested)
	if err != nil || port != "0" {
		return "", false
	}
	return net.JoinHostPort(host, strconv.Itoa(sticky)), true
}
