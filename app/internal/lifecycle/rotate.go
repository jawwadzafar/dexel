package lifecycle

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// MaxLogBytesEnv overrides MaxLogBytes for one process. It exists so a
// test can watch a real runtime rotate a real log in seconds instead of
// writing 8 MiB of lines, and so a field diagnosis can turn the cap down
// on a box that is filling a small partition. Same shape and same spirit
// as DEXEL_HOME (app/internal/paths) and DEXEL_FAKE_SCRIPT
// (app/internal/activity): an explicit, documented override, never a
// hidden default.
//
// A value that is absent, unparseable or <= 0 leaves MaxLogBytes in
// force — a typo'd env var must not silently disable rotation, and it
// must not rotate on every line either.
const MaxLogBytesEnv = "DEXEL_MAX_LOG_BYTES"

// RotationThreshold is the size at which THIS process rotates its log:
// MaxLogBytes, unless MaxLogBytesEnv says otherwise. getenv is injected
// (os.Getenv in production) exactly as everything else in this package
// takes its inputs, so the decision is unit-testable without touching
// the process environment.
func RotationThreshold(getenv func(string) string) int64 {
	if v := getenv(MaxLogBytesEnv); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return MaxLogBytes
}

// RotatingWriter is an io.Writer over <StateDir>/logs/runtime.log that
// keeps the file under a size cap WHILE THE RUNTIME RUNS — the half of
// PLATFORM_NOTES.md §4's rotation that `dexel start`'s one-shot
// RotateLog could never provide (docs/plan/BUGS-RESILIENCE.md R6).
//
// # Why this exists at all
//
// RotateLog runs in `dexel start`, before the child is spawned. But the
// two SUPERVISED autostart mechanisms — the systemd user unit and the
// launchd agent — exec `dexel runtime` directly, so on those paths
// RotateLog is never called, ever: not at login, not on a crash-restart.
// The runtime therefore has to be able to rotate its own log, on its own,
// with no CLI involved.
//
// # The mechanism, and its one honest limitation
//
// Rotation is checked ON WRITE against a byte counter seeded from the
// file's size at open — no goroutine, no timer, and no stat per line. When
// a write would take the file past max, the file is closed, renamed to
// "<path>.1" (replacing any existing .1, so exactly two files ever exist,
// as §4 specifies) and reopened; the write then lands in the fresh file.
//
// What this owns is the `log` package's output, which is where every line
// the runtime writes comes from. What it does NOT own is file descriptors
// 1 and 2. Under `dexel start` and under launchd those descriptors are
// the log file, opened by the parent (the CLI, or launchd) and inherited
// — and a rename does not move an open descriptor, so after a rotation
// anything written STRAIGHT to fd 1/2, i.e. a Go panic's traceback,
// lands in runtime.log.1 rather than runtime.log. That is the trade
// PLATFORM_NOTES.md §4 takes knowingly: the alternative it rejects is a
// mid-run dup2 of fd 1/2, which is platform-specific and fights the
// launchd/systemd redirections. A panic in the previous 8 MiB window
// being one file over is a far smaller problem than a log file that
// grows without bound for months.
//
// # Concurrency and durability
//
// Every write holds the mutex, so a rotation can never interleave with a
// write or with another rotation. There is no buffering anywhere: each
// Write is one os.File.Write, i.e. one syscall to an O_APPEND
// descriptor, so a log.Fatalf or a SIGKILL cannot lose a line that
// Write already returned on — which is exactly why Close is tidiness
// here and not the mechanism.
type RotatingWriter struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	n    int64 // bytes in the CURRENT file, counted, not stat'ed
	// broken is set once a rotation attempt fails, so the "I could not
	// rotate" line is written into the log once per failure and not once
	// per line for the rest of the process's life.
	broken bool
	// retryAfter throttles rotation ATTEMPTS after a failure. Without it,
	// a log directory that has become read-only would make every single
	// line attempt a close+rename+open for the rest of the process's
	// life, which turns a cosmetic problem (an oversized log) into a
	// performance one.
	retryAfter time.Time
}

// rotateRetryInterval is how long a FAILED rotation waits before it is
// attempted again. Long enough that a permanently broken log directory
// costs nothing measurable, short enough that a transient failure (a full
// disk somebody then clears) self-heals without a restart.
const rotateRetryInterval = 30 * time.Second

// NewRotatingWriter opens path for appending (OpenLog's exact mode and
// 0600) and returns a writer that rotates it at max bytes. A max <= 0 is
// read as "use MaxLogBytes", so a caller can pass a parsed override
// through without checking it first.
//
// Note it does NOT rotate on open: `dexel start` already does that for
// the path it owns, and an oversized file inherited from before this
// runtime existed rotates on the very first line written, which is the
// same outcome one line later.
func NewRotatingWriter(path string, max int64) (*RotatingWriter, error) {
	if max <= 0 {
		max = MaxLogBytes
	}
	f, err := OpenLog(path)
	if err != nil {
		return nil, err
	}
	var n int64
	if info, statErr := f.Stat(); statErr == nil {
		n = info.Size()
	}
	return &RotatingWriter{path: path, max: max, f: f, n: n}, nil
}

// Path is the file this writer currently appends to (the rotated file is
// always Path()+".1"). Exposed for the log line that announces the
// attachment, and for tests.
func (w *RotatingWriter) Path() string { return w.path }

// SameFileAs reports whether f is the very same file (same device+inode)
// this writer holds open. THE question the runtime has to answer before
// it decides whether to tee its log output to stderr as well: under
// `dexel start` and under launchd, stderr already IS this file, and
// teeing there would write every line twice.
//
// A false is returned for anything unstattable — a closed descriptor, a
// pipe on a platform that will not stat it — because "not provably the
// same file" is the safe answer for a caller who will tee on false: a
// duplicated line is ugly, a silently discarded one is a lie.
func (w *RotatingWriter) SameFileAs(f *os.File) bool {
	if f == nil {
		return false
	}
	other, err := f.Stat()
	if err != nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	mine, err := w.f.Stat()
	if err != nil {
		return false
	}
	return os.SameFile(mine, other)
}

// Write appends p, rotating first if p would take the file past max.
//
// A failed rotation is NOT a failed write: the writer keeps the file it
// has and reports the failure once, into that same file, then carries on
// appending. A log we cannot rotate is a log that keeps growing, which is
// the pre-existing accepted limit; a log we refuse to write is a runtime
// nobody can diagnose.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.n+int64(len(p)) > w.max && !time.Now().Before(w.retryAfter) {
		if err := w.rotateLocked(int64(len(p))); err != nil {
			w.retryAfter = time.Now().Add(rotateRetryInterval)
			if !w.broken {
				w.broken = true
				note := fmt.Sprintf("dexel: could not rotate %s: %v — this log will keep growing; `dexel logs --truncate` empties it\n", w.path, err)
				if n, werr := w.f.Write([]byte(note)); werr == nil {
					w.n += int64(n)
				}
			}
		} else {
			w.broken = false
			w.retryAfter = time.Time{}
		}
	}
	n, err := w.f.Write(p)
	w.n += int64(n)
	return n, err
}

// rotateLocked performs the close-rename-reopen. The caller holds mu,
// and `pending` is the size of the write that triggered this.
//
// It re-stats before committing, for one specific reason: `dexel logs
// --truncate` empties this file underneath us (truncating rather than
// unlinking precisely so this descriptor keeps working — see
// TruncateLog), which leaves the counter far ahead of the truth. Without
// the re-sync, the next line after a truncate would rotate a nearly
// empty file and throw away the .1 that still held real history. The
// re-synced size is re-tested against the SAME condition Write applied
// (real size + pending write over the cap), so a truncate cancels the
// rotation and a genuinely-full file still rotates. One stat per
// rotation, i.e. one per 8 MiB, is not a cost.
//
// The close-BEFORE-rename order is for Windows, where renaming a file
// that is still open fails; on POSIX it is merely tidy. Whatever happens,
// this function returns with w.f pointing at an open, appendable file:
// a rename that fails is followed by a reopen of the ORIGINAL path, so a
// rotation failure costs the log nothing but its size cap.
func (w *RotatingWriter) rotateLocked(pending int64) error {
	if info, err := w.f.Stat(); err == nil {
		w.n = info.Size()
		if w.n+pending <= w.max {
			return nil // truncated under us; nothing to rotate
		}
	}
	if err := w.f.Close(); err != nil {
		// The descriptor is gone either way; reopen below decides
		// whether this writer still works.
		_ = err
	}
	renameErr := os.Rename(w.path, w.path+".1")
	f, err := OpenLog(w.path)
	if err != nil {
		// Both the rename and the reopen failed: there is no file to
		// write to. Keep the (closed) handle rather than nil so Write
		// returns a real error instead of panicking.
		return fmt.Errorf("reopen %s after rotation: %w", w.path, err)
	}
	w.f = f
	if renameErr != nil {
		// The reopen re-attached us to the file we could not rename, so
		// its size is whatever it already was.
		if info, statErr := f.Stat(); statErr == nil {
			w.n = info.Size()
		}
		return fmt.Errorf("rotate %s: %w", w.path, renameErr)
	}
	w.n = 0
	return nil
}

// Close closes the underlying file. Tidiness only: writes are unbuffered
// syscalls, so nothing is pending and nothing is lost when a runtime
// exits through log.Fatalf (which skips every defer) or is SIGKILLed.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}
