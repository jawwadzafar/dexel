package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestAcquireLockRefusesASecondHolder is PLATFORM_NOTES.md §5 layer 1:
// while one holder has the lock, a second attempt must FAIL IMMEDIATELY
// (LOCK_NB / LOCKFILE_FAIL_IMMEDIATELY) with the ErrLockedError sentinel,
// not block waiting for the first to exit.
//
// This exercises the real OS primitive even inside one process: flock is
// held per open file DESCRIPTION, so two independent os.OpenFile calls on
// the same path genuinely contend — which is exactly what two `dexel
// runtime` processes do.
func TestAcquireLockRefusesASecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), LockFileName)

	first, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	defer func() { _ = first.Release() }()

	second, err := AcquireLock(path)
	if err == nil {
		_ = second.Release()
		t.Fatal("second AcquireLock succeeded — two runtimes could then share one state dir")
	}
	var busy *ErrLockedError
	if !errors.As(err, &busy) {
		t.Fatalf("second AcquireLock err = %v (%T), want *ErrLockedError so the caller can say \"dexel is already running\"", err, err)
	}
	if busy.Path != path {
		t.Fatalf("ErrLockedError.Path = %q, want %q", busy.Path, path)
	}
}

// TestReleaseMakesTheLockAvailableAgain: a clean `dexel stop` must leave
// the state dir immediately startable, with no cool-off and no leftover
// state to clear.
func TestReleaseMakesTheLockAvailableAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), LockFileName)
	for i := 0; i < 3; i++ {
		l, err := AcquireLock(path)
		if err != nil {
			t.Fatalf("AcquireLock round %d: %v", i, err)
		}
		if err := l.Release(); err != nil {
			t.Fatalf("Release round %d: %v", i, err)
		}
	}
}

// TestReleaseKeepsTheLockFileOnDisk pins the deliberate choice in Lock's
// doc comment: Release must NOT unlink the file. Deleting it would let a
// waiting process lock a different inode and give two processes two
// independent "exclusive" locks.
func TestReleaseKeepsTheLockFileOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), LockFileName)
	l, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file was deleted by Release: %v", err)
	}
}

// TestReleaseIsSafeTwiceAndOnNil: `dexel runtime` releases via defer while
// the OS would have done it anyway, and a double release must not panic
// or report a spurious failure.
func TestReleaseIsSafeTwiceAndOnNil(t *testing.T) {
	var nilLock *Lock
	if err := nilLock.Release(); err != nil {
		t.Fatalf("nil Release: %v", err)
	}
	if got := nilLock.Path(); got != "" {
		t.Fatalf("nil Path = %q, want empty", got)
	}
	path := filepath.Join(t.TempDir(), LockFileName)
	l, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if l.Path() != path {
		t.Fatalf("Path = %q, want %q", l.Path(), path)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

// TestTwoStateDirsAreTwoIndependentLocks is PLATFORM_NOTES.md §5's
// closing rule — "Two state directories = two independent instances, by
// design" — which is the whole reason DEXEL_HOME exists and how the test
// suite and CI stay clear of a developer's real runtime.
func TestTwoStateDirsAreTwoIndependentLocks(t *testing.T) {
	a, err := AcquireLock(filepath.Join(t.TempDir(), LockFileName))
	if err != nil {
		t.Fatalf("lock A: %v", err)
	}
	defer func() { _ = a.Release() }()
	b, err := AcquireLock(filepath.Join(t.TempDir(), LockFileName))
	if err != nil {
		t.Fatalf("lock B in a different state dir: %v — DEXEL_HOME isolation is broken", err)
	}
	defer func() { _ = b.Release() }()
}

// TestConcurrentAcquireYieldsExactlyOneWinner is the contention property
// under -race: many goroutines racing for one lock file must produce
// exactly one holder, and every loser must report ErrLockedError rather
// than an obscure errno.
func TestConcurrentAcquireYieldsExactlyOneWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), LockFileName)
	const n = 16

	var mu sync.Mutex
	var winners []*Lock
	var losers int
	var other []error

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			l, err := AcquireLock(path)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners = append(winners, l)
			case errors.As(err, new(*ErrLockedError)):
				losers++
			default:
				other = append(other, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	for _, l := range winners {
		_ = l.Release()
	}
	if len(other) > 0 {
		t.Fatalf("unexpected errors: %v", other)
	}
	if len(winners) != 1 {
		t.Fatalf("%d goroutines got the lock, want exactly 1", len(winners))
	}
	if losers != n-1 {
		t.Fatalf("%d losers, want %d", losers, n-1)
	}
}
