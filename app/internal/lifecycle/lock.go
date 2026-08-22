package lifecycle

import (
	"fmt"
	"os"
	"path/filepath"
)

// Lock is an exclusive, OS-enforced, held-for-the-process-lifetime lock
// on <StateDir>/runtime.lock — layer 1 of PLATFORM_NOTES.md §5's
// three-layer single-instance enforcement, and ARCHITECTURE.md Decision
// 7's "an OS lock, not a file's existence".
//
// Why an OS lock and not "does the file exist": the OS releases an
// flock/LockFileEx when the holding process dies, INCLUDING under
// SIGKILL, a panic, or an OOM kill. So there is no stale-lock class of
// bug at all — nothing has to decide whether a leftover file means a
// live owner. A `kill -9`'d runtime leaves the lock file on disk and the
// lock itself free, and the next `dexel start` takes it immediately.
//
// The zero value is not usable; call AcquireLock.
type Lock struct {
	f    *os.File
	path string
}

// ErrLocked is returned by AcquireLock when another process already holds
// the lock. It is a sentinel so callers can tell "someone else is the
// runtime" (an expected, user-facing outcome: "dexel is already running")
// apart from "the lock file could not be created" (a broken state dir).
type ErrLockedError struct{ Path string }

func (e *ErrLockedError) Error() string {
	return fmt.Sprintf("another process holds %s", e.Path)
}

// AcquireLock creates (if needed) and exclusively locks path, without
// blocking. The returned *Lock must be kept alive for as long as this
// process intends to be THE runtime; Release drops it.
//
// The file is opened 0600 and its content is deliberately left empty: the
// lock lives in the kernel, not in the bytes, and writing a pid into it
// would invite exactly the pid-trusting logic Decision 6 forbids.
// runtime.json is where a pid is published, and it is published only
// after the lock is held.
func AcquireLock(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		if isLockBusy(err) {
			return nil, &ErrLockedError{Path: path}
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return &Lock{f: f, path: path}, nil
}

// Release unlocks and closes the lock file. It deliberately does NOT
// delete the file: another process may already be waiting to lock the
// same inode, and unlinking it underneath them would hand two processes
// two different inodes and therefore two independent locks — the classic
// lockfile-deletion race. A leftover empty runtime.lock is harmless by
// construction (see Lock's doc comment).
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := unlockFile(l.f)
	closeErr := l.f.Close()
	l.f = nil
	if err != nil {
		return fmt.Errorf("unlock %s: %w", l.path, err)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", l.path, closeErr)
	}
	return nil
}

// Path reports the locked file's path, for log lines and error messages.
func (l *Lock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}
