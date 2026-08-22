//go:build unix

package lifecycle

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lockFile takes an exclusive, non-blocking flock (PLATFORM_NOTES.md §5
// layer 1). LOCK_NB is what makes a second `dexel start` REPORT the
// running instance instead of hanging forever waiting for it to exit.
//
// golang.org/x/sys was already in app/go.mod as an indirect dependency
// (via modernc.org/sqlite); this promotes it to direct, which adds no
// module to the build (ARCHITECTURE.md Decision 7).
func lockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}

// isLockBusy distinguishes "someone else holds it" from a real failure.
// flock reports contention as EWOULDBLOCK; EAGAIN is the same errno value
// on Linux and a distinct one on some BSDs, so both are matched rather
// than assuming the platform.
func isLockBusy(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EACCES)
}
