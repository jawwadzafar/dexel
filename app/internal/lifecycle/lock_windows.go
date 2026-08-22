//go:build windows

package lifecycle

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive, fail-immediately byte-range lock on the
// whole file — Windows' equivalent of flock (ARCHITECTURE.md Decision 7,
// PLATFORM_NOTES.md §5 layer 1). Like flock, Windows releases it when the
// holding process dies, so the no-stale-lock property is the same.
//
// The range is the maximum 64-bit span (offset 0, length 0xFFFFFFFF_FFFFFFFF)
// so it covers the whole file regardless of size — the file is empty, and
// locking [0, huge) is the standard idiom for "the file" here.
func lockFile(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 0xFFFFFFFF, 0xFFFFFFFF, ol,
	)
}

func unlockFile(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 0xFFFFFFFF, 0xFFFFFFFF, ol)
}

// isLockBusy: LockFileEx with LOCKFILE_FAIL_IMMEDIATELY reports
// contention as ERROR_LOCK_VIOLATION (and, on some paths,
// ERROR_SHARING_VIOLATION).
func isLockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
