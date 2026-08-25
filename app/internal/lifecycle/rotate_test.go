package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rotate_test.go covers the in-process half of PLATFORM_NOTES.md §4's
// rotation — the half docs/plan/BUGS-RESILIENCE.md R6 found missing,
// because `dexel start`'s one-shot RotateLog is not on the code path a
// systemd unit or a launchd agent takes (both exec `dexel runtime`).
//
// Everything here runs against a plain t.TempDir(): this package never
// consults paths.StateDir(), which is exactly what makes that possible.

func TestRotationThreshold(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		want int64
	}{
		{"unset falls back to the 8 MiB cap", "", MaxLogBytes},
		{"an explicit small cap is honoured", "4096", 4096},
		{"zero is not a cap and must not disable rotation", "0", MaxLogBytes},
		{"a negative value is nonsense, not a cap", "-5", MaxLogBytes},
		{"an unparseable value is a typo, not a request", "8MiB", MaxLogBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := RotationThreshold(func(string) string { return tc.env })
			if got != tc.want {
				t.Fatalf("RotationThreshold(%q) = %d, want %d", tc.env, got, tc.want)
			}
		})
	}
}

// TestRotationThresholdReadsItsOwnEnvVar pins the variable NAME, since a
// test and a doc both quote it and neither would notice a rename.
func TestRotationThresholdReadsItsOwnEnvVar(t *testing.T) {
	var asked []string
	RotationThreshold(func(k string) string {
		asked = append(asked, k)
		return ""
	})
	if len(asked) != 1 || asked[0] != "DEXEL_MAX_LOG_BYTES" {
		t.Fatalf("RotationThreshold read %v, want exactly [DEXEL_MAX_LOG_BYTES]", asked)
	}
	if MaxLogBytesEnv != "DEXEL_MAX_LOG_BYTES" {
		t.Fatalf("MaxLogBytesEnv = %q", MaxLogBytesEnv)
	}
}

// newTestWriter opens a RotatingWriter on <tmp>/runtime.log with an
// explicit tiny cap, and returns it with the path.
func newTestWriter(t *testing.T, max int64) (*RotatingWriter, string) {
	t.Helper()
	path := LogPath(t.TempDir())
	w, err := NewRotatingWriter(path, max)
	if err != nil {
		t.Fatalf("NewRotatingWriter: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w, path
}

func mustWrite(t *testing.T, w *RotatingWriter, s string) {
	t.Helper()
	n, err := w.Write([]byte(s))
	if err != nil {
		t.Fatalf("Write(%d bytes): %v", len(s), err)
	}
	if n != len(s) {
		t.Fatalf("Write returned %d, want %d", n, len(s))
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists (stat err %v), want it absent", path, err)
	}
}

func TestRotatingWriterRotatesAtTheThreshold(t *testing.T) {
	w, path := newTestWriter(t, 100)
	first := strings.Repeat("a", 60)
	mustWrite(t, w, first)
	mustNotExist(t, path+".1")
	if got := read(t, path); got != first {
		t.Fatalf("before rotation the log holds %d bytes, want %d", len(got), len(first))
	}

	// This write would take the file to 120 > 100, so it must land in a
	// FRESH file and the 60 bytes already written must survive as .1.
	second := strings.Repeat("b", 60)
	mustWrite(t, w, second)
	if got := read(t, path+".1"); got != first {
		t.Fatalf("runtime.log.1 = %q, want the pre-rotation content", got)
	}
	if got := read(t, path); got != second {
		t.Fatalf("runtime.log = %q, want only the post-rotation write", got)
	}
}

// TestRotatingWriterKeepsExactlyTwoFiles is §4's "keep exactly two
// files": the second rotation replaces .1 and never creates a .2.
func TestRotatingWriterKeepsExactlyTwoFiles(t *testing.T) {
	w, path := newTestWriter(t, 10)
	for _, s := range []string{"first-----", "second----", "third-----"} {
		mustWrite(t, w, s)
	}
	if got := read(t, path+".1"); got != "second----" {
		t.Fatalf("runtime.log.1 = %q, want the second write", got)
	}
	if got := read(t, path); got != "third-----" {
		t.Fatalf("runtime.log = %q, want the third write", got)
	}
	mustNotExist(t, path+".2")
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 2 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("log dir holds %v, want exactly runtime.log and runtime.log.1", names)
	}
}

// TestRotatingWriterCountsWhatWasAlreadyThere is the supervised-restart
// case: a crash-restarted runtime opens a log that is ALREADY near the
// cap and must rotate on its first line, not 8 MiB later.
func TestRotatingWriterCountsWhatWasAlreadyThere(t *testing.T) {
	dir := t.TempDir()
	path := LogPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	inherited := strings.Repeat("x", 95)
	if err := os.WriteFile(path, []byte(inherited), 0o600); err != nil {
		t.Fatalf("seed log: %v", err)
	}
	w, err := NewRotatingWriter(path, 100)
	if err != nil {
		t.Fatalf("NewRotatingWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	mustWrite(t, w, "ten-bytes!")
	if got := read(t, path+".1"); got != inherited {
		t.Fatalf("runtime.log.1 = %q, want the inherited content", got)
	}
	if got := read(t, path); got != "ten-bytes!" {
		t.Fatalf("runtime.log = %q", got)
	}
}

// TestRotatingWriterSurvivesATruncateUnderneathIt is the interaction with
// `dexel logs --truncate`, which empties this very file while the runtime
// holds it open (TruncateLog's doc comment explains why it truncates
// rather than unlinks). Without the re-stat in rotateLocked, the next
// line would rotate a nearly-empty file and destroy the real .1.
func TestRotatingWriterSurvivesATruncateUnderneathIt(t *testing.T) {
	w, path := newTestWriter(t, 100)
	mustWrite(t, w, strings.Repeat("a", 95))
	if err := TruncateLog(path); err != nil {
		t.Fatalf("TruncateLog: %v", err)
	}
	mustWrite(t, w, "ten-bytes!")
	mustNotExist(t, path+".1")
	if got := read(t, path); got != "ten-bytes!" {
		t.Fatalf("runtime.log = %q, want just the post-truncate write", got)
	}
}

// TestRotatingWriterSameFileAs is the tee decision app/main.go's
// attachRuntimeLog makes: under `dexel start` and under launchd our own
// stderr IS the log file (so teeing would double every line); under
// systemd it is a journald socket and under a terminal it is a tty (so
// teeing is the only way the file gets anything).
func TestRotatingWriterSameFileAs(t *testing.T) {
	w, path := newTestWriter(t, 1000)

	same, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("reopen the log: %v", err)
	}
	defer func() { _ = same.Close() }()
	if !w.SameFileAs(same) {
		t.Fatal("SameFileAs(a second descriptor on the same path) = false")
	}

	other, err := os.Create(filepath.Join(t.TempDir(), "not-the-log"))
	if err != nil {
		t.Fatalf("create other file: %v", err)
	}
	defer func() { _ = other.Close() }()
	if w.SameFileAs(other) {
		t.Fatal("SameFileAs(a different file) = true")
	}
	if w.SameFileAs(nil) {
		t.Fatal("SameFileAs(nil) = true")
	}
}

// TestRotatingWriterWritesEveryByteAcrossManyRotations is the property
// that actually matters for a log: rotation loses only what a PREVIOUS
// rotation already archived, never a byte of the line being written.
func TestRotatingWriterWritesEveryByteAcrossManyRotations(t *testing.T) {
	w, path := newTestWriter(t, 64)
	for i := 0; i < 200; i++ {
		mustWrite(t, w, "0123456789abcdef0123456789abcdef\n") // 33 bytes
	}
	cur, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if cur.Size() > 64 {
		t.Fatalf("runtime.log is %d bytes after 200 writes, want <= the 64-byte cap", cur.Size())
	}
	prev, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatalf("stat .1: %v", err)
	}
	if prev.Size() > 64 {
		t.Fatalf("runtime.log.1 is %d bytes, want <= the 64-byte cap", prev.Size())
	}
}

// TestRotatingWriterKeepsWritingWhenItCannotRotate is the failure posture:
// a log we cannot rotate is the pre-existing accepted limit, but a log we
// refuse to write is a runtime nobody can diagnose. The failure is
// announced ONCE, into the log itself, and every line still lands.
//
// The failure is staged by making the log DIRECTORY read-only after the
// file is open: renaming needs write permission on the directory, while
// appending to an already-open (and even a reopened) file does not.
func TestRotatingWriterKeepsWritingWhenItCannotRotate(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can rename inside a 0500 directory")
	}
	w, path := newTestWriter(t, 100)
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	mustWrite(t, w, strings.Repeat("a", 60))
	mustWrite(t, w, strings.Repeat("b", 60)) // would rotate, cannot
	mustNotExist(t, path+".1")

	body := read(t, path)
	if !strings.Contains(body, strings.Repeat("a", 60)) || !strings.Contains(body, strings.Repeat("b", 60)) {
		t.Fatalf("a line was dropped when rotation failed: %q", body)
	}
	const note = "could not rotate"
	if n := strings.Count(body, note); n != 1 {
		t.Fatalf("%q appears %d times, want exactly 1", note, n)
	}

	// And it must not re-announce (or re-attempt) per line.
	mustWrite(t, w, strings.Repeat("c", 60))
	body = read(t, path)
	if n := strings.Count(body, note); n != 1 {
		t.Fatalf("%q appears %d times after a third write, want still 1", note, n)
	}
	if !strings.Contains(body, strings.Repeat("c", 60)) {
		t.Fatal("the third line was dropped")
	}
}
