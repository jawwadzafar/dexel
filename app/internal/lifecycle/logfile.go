package lifecycle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxLogBytes is PLATFORM_NOTES.md §4's rotation-lite threshold: 8 MiB.
const MaxLogBytes int64 = 8 << 20

// tailWindow bounds how much of the log TailLines reads from the end.
// 1 MiB is far more than any -n a human types and keeps `dexel logs`
// from paging an 8 MiB file into memory to print twenty lines.
const tailWindow int64 = 1 << 20

// RotateLog implements PLATFORM_NOTES.md §4's "rotation-lite, at start
// only": if the log is over MaxLogBytes, rename it to "<path>.1"
// (replacing any existing .1) so exactly two files ever exist. It is
// called ONCE, by `dexel start`, before the child is spawned — never on a
// timer, never mid-run, no goroutine, no dependency.
//
// The stated, accepted limit (PLATFORM_NOTES.md §4): a runtime that runs
// for months without a restart can exceed the cap, because nothing
// rotates while it runs. That is tolerable because the runtime is nearly
// silent at steady state (app/main.go logs ~10 lines at startup and then
// only on failure — the 30s autosave logs nothing when it succeeds), and
// the escape hatches are `dexel logs --truncate` and `dexel restart`,
// which rotates on the way through.
//
// Every failure here is returned, not swallowed: a log we cannot rotate
// is a log that keeps growing, and the caller decides whether that is
// worth blocking a start over (it is not — cmdStart warns and continues).
func RotateLog(path string) (rotated bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Size() <= MaxLogBytes {
		return false, nil
	}
	if err := os.Rename(path, path+".1"); err != nil {
		return false, fmt.Errorf("rotate %s: %w", path, err)
	}
	return true, nil
}

// OpenLog opens the runtime log for appending at 0600, creating the log
// directory if needed (PLATFORM_NOTES.md §4). The returned file is what
// `dexel start` hands the detached child as BOTH stdout and stderr — the
// child's `log` package output (stderr) and anything a panic writes both
// land here, which is the whole reason a detached runtime is debuggable
// at all.
//
// O_APPEND matters for correctness, not just tidiness: stdout and stderr
// are the same file description here, and append-mode writes are
// positioned atomically by the kernel, so the two streams interleave by
// line instead of overwriting each other at a shared stale offset.
func OpenLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return f, nil
}

// TruncateLog empties the log in place (`dexel logs --truncate`).
// Truncating rather than deleting is deliberate: a RUNNING runtime holds
// this file open, and unlinking it would leave that process writing to an
// invisible inode until it restarted, so `dexel logs` would then show an
// empty file forever while the disk quietly filled.
func TruncateLog(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("truncate %s: %w", path, err)
	}
	return f.Close()
}

// TailLines returns the last n lines of path, oldest first. A missing
// file yields no lines and no error — a runtime that has never started
// has no log, and that is an answer, not a failure.
//
// It reads at most tailWindow bytes from the end, so the first line
// returned may be a partial line when the window cuts mid-line; that line
// is dropped rather than shown truncated, unless the window covers the
// whole file.
func TailLines(path string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	start, size := int64(0), info.Size()
	partial := false
	if size > tailWindow {
		start = size - tailWindow
		partial = true
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek %s: %w", path, err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil, nil
	}
	lines := strings.Split(text, "\n")
	if partial && len(lines) > 1 {
		lines = lines[1:] // drop the line the window cut in half
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}
