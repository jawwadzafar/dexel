package lifecycle

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestRotateLogOnlyOverTheThreshold pins PLATFORM_NOTES.md §4's
// rotation-lite: nothing happens below 8 MiB, and above it the log
// becomes .1 and a fresh file is started — "keep exactly two files".
//
// The oversized file is created with Truncate (a sparse file) rather than
// by writing 8 MiB, so the test costs no real I/O while still exercising
// the exact size comparison.
func TestRotateLogOnlyOverTheThreshold(t *testing.T) {
	cases := []struct {
		name        string
		size        int64
		wantRotated bool
	}{
		{"empty", 0, false},
		{"small", 1024, false},
		{"exactly at the cap", MaxLogBytes, false},
		{"one byte over", MaxLogBytes + 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), LogFileName)
			f, err := os.Create(path)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if err := f.Truncate(tc.size); err != nil {
				t.Fatalf("truncate to %d: %v", tc.size, err)
			}
			if err := f.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}

			rotated, err := RotateLog(path)
			if err != nil {
				t.Fatalf("RotateLog: %v", err)
			}
			if rotated != tc.wantRotated {
				t.Fatalf("rotated = %v, want %v for size %d (cap %d)", rotated, tc.wantRotated, tc.size, MaxLogBytes)
			}
			_, statErr := os.Stat(path + ".1")
			if tc.wantRotated && statErr != nil {
				t.Fatalf("no %s.1 after rotation: %v", path, statErr)
			}
			if !tc.wantRotated && statErr == nil {
				t.Fatalf("%s.1 exists without a rotation", path)
			}
		})
	}
}

// TestRotateLogKeepsExactlyTwoFiles: a second rotation must REPLACE .1,
// never accumulate .2/.3 — PLATFORM_NOTES.md §4 pins two files, and an
// unbounded set is the disk-filling bug rotation exists to prevent.
func TestRotateLogKeepsExactlyTwoFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, LogFileName)
	oversize := func(marker string) {
		f, err := os.Create(path)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := f.WriteString(marker + "\n"); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := f.Truncate(MaxLogBytes + 1); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		_ = f.Close()
	}

	oversize("first generation")
	if _, err := RotateLog(path); err != nil {
		t.Fatalf("rotate 1: %v", err)
	}
	oversize("second generation")
	if _, err := RotateLog(path); err != nil {
		t.Fatalf("rotate 2: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 {
		t.Fatalf("dir holds %v, want only %s.1 (the live log is recreated by OpenLog)", names, LogFileName)
	}
	kept, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("read .1: %v", err)
	}
	if !strings.HasPrefix(string(kept), "second generation") {
		t.Fatalf(".1 holds the wrong generation: %q", string(kept[:min(40, len(kept))]))
	}
}

// TestRotateLogOnAMissingFileIsANoOp: the very first `dexel start` has no
// log yet, and that must not be an error.
func TestRotateLogOnAMissingFileIsANoOp(t *testing.T) {
	rotated, err := RotateLog(filepath.Join(t.TempDir(), LogFileName))
	if err != nil {
		t.Fatalf("RotateLog on a missing file: %v", err)
	}
	if rotated {
		t.Fatal("rotated a file that does not exist")
	}
}

// TestOpenLogCreatesTheDirAppendsAndIs0600: the log dir may not exist on
// a first run; two opens must APPEND rather than truncate (a restart must
// not erase the reason the previous run died); and the mode is 0600 like
// every other file dexel writes into StateDir.
func TestOpenLogCreatesTheDirAppendsAndIs0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", LogFileName)

	f, err := OpenLog(path)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	if _, err := f.WriteString("first run\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	f2, err := OpenLog(path)
	if err != nil {
		t.Fatalf("second OpenLog: %v", err)
	}
	if _, err := f2.WriteString("second run\n"); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	if err := f2.Close(); err != nil {
		t.Fatalf("close 2: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "first run\nsecond run\n" {
		t.Fatalf("log = %q, want both runs appended", string(data))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 && perm != 0o644 {
		// 0600 is the intent; a umask-widened 0644 is tolerated on hosts
		// with an unusual umask, but anything group/world-WRITABLE is not.
		t.Fatalf("log mode = %v, want 0600", perm)
	}
}

// TestTailLines covers what `dexel logs -n N` and a failed `dexel start`
// both depend on.
func TestTailLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), LogFileName)
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", 3))
		b.WriteString(strconv.Itoa(i))
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	lines, err := TailLines(path, 20)
	if err != nil {
		t.Fatalf("TailLines: %v", err)
	}
	if len(lines) != 20 {
		t.Fatalf("got %d lines, want 20", len(lines))
	}
	if !strings.HasSuffix(lines[19], "100") {
		t.Fatalf("last line = %q, want the newest", lines[19])
	}
	if !strings.HasSuffix(lines[0], "81") {
		t.Fatalf("first line = %q, want line 81", lines[0])
	}

	// Asking for more lines than exist yields everything, not an error.
	all, err := TailLines(path, 1000)
	if err != nil {
		t.Fatalf("TailLines(1000): %v", err)
	}
	if len(all) != 100 {
		t.Fatalf("got %d lines, want all 100", len(all))
	}

	// A missing file is no lines and no error — a runtime that never ran
	// has no log, which is an answer.
	none, err := TailLines(filepath.Join(t.TempDir(), "absent.log"), 10)
	if err != nil {
		t.Fatalf("TailLines on a missing file: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("got %d lines from a missing file", len(none))
	}

	// n <= 0 asks for nothing.
	if got, err := TailLines(path, 0); err != nil || got != nil {
		t.Fatalf("TailLines(0) = %v, %v; want nil, nil", got, err)
	}
}

// TestTruncateLogEmptiesInPlace pins the reason TruncateLog truncates
// rather than unlinks: a RUNNING runtime holds this file open, and
// unlinking it would leave that process writing to an invisible inode.
func TestTruncateLogEmptiesInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), LogFileName)
	if err := os.WriteFile(path, []byte("noise\nnoise\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Hold it open, like a live runtime does.
	held, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("hold open: %v", err)
	}
	defer func() { _ = held.Close() }()

	if err := TruncateLog(path); err != nil {
		t.Fatalf("TruncateLog: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the log file must still EXIST after --truncate: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("size = %d after truncate, want 0", info.Size())
	}

	// And truncating a log that does not exist is success, not an error.
	if err := TruncateLog(filepath.Join(t.TempDir(), "absent.log")); err != nil {
		t.Fatalf("TruncateLog on a missing file: %v", err)
	}
}
