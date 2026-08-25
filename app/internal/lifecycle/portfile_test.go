package lifecycle

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// portfile_test.go covers the sticky-port record (docs/plan/
// BUGS-RESILIENCE.md R9): the one file in this package that deliberately
// OUTLIVES the runtime that wrote it, so a supervisor's crash-restart —
// and `dexel restart`, and a bookmarked tab — come back on the port the
// open pages are already retrying.

func TestStickyPortRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := WriteStickyPort(dir, 51423); err != nil {
		t.Fatalf("WriteStickyPort: %v", err)
	}
	got, err := ReadStickyPort(dir)
	if err != nil {
		t.Fatalf("ReadStickyPort: %v", err)
	}
	if got != 51423 {
		t.Fatalf("ReadStickyPort = %d, want 51423", got)
	}
	// Rewriting replaces rather than appends: the record is "the last
	// port", not a history.
	if err := WriteStickyPort(dir, 51424); err != nil {
		t.Fatalf("WriteStickyPort (second): %v", err)
	}
	if got, err = ReadStickyPort(dir); err != nil || got != 51424 {
		t.Fatalf("ReadStickyPort after rewrite = %d, %v; want 51424, nil", got, err)
	}
}

// TestReadStickyPortMissingIsNotAFailure: on a machine where dexel has
// never run there is no record, and "no record" is an answer.
func TestReadStickyPortMissingIsNotAFailure(t *testing.T) {
	got, err := ReadStickyPort(t.TempDir())
	if err != nil {
		t.Fatalf("ReadStickyPort on an empty dir: %v", err)
	}
	if got != 0 {
		t.Fatalf("ReadStickyPort = %d, want 0", got)
	}
}

// TestReadStickyPortRejectsNonsense: a file that EXISTS but holds no
// usable port is reported as an error, so app/main.go can say so out loud
// instead of silently pretending there was no record.
func TestReadStickyPortRejectsNonsense(t *testing.T) {
	for _, content := range []string{"", "  ", "not-a-port", "0", "-1", "70000", "8080 8081"} {
		dir := t.TempDir()
		if err := os.WriteFile(PortFilePath(dir), []byte(content), 0o600); err != nil {
			t.Fatalf("seed record: %v", err)
		}
		got, err := ReadStickyPort(dir)
		if err == nil {
			t.Fatalf("ReadStickyPort(%q) = %d, nil; want an error", content, got)
		}
		if got != 0 {
			t.Fatalf("ReadStickyPort(%q) returned %d alongside its error; want 0", content, got)
		}
	}
}

func TestWriteStickyPortRefusesAnUnusablePort(t *testing.T) {
	for _, port := range []int{0, -1, 65536} {
		dir := t.TempDir()
		if err := WriteStickyPort(dir, port); err == nil {
			t.Fatalf("WriteStickyPort(%d) succeeded; want a refusal", port)
		}
		if _, err := os.Stat(PortFilePath(dir)); !os.IsNotExist(err) {
			t.Fatalf("WriteStickyPort(%d) left a file behind", port)
		}
	}
}

// TestWriteStickyPortLeavesNoTempFile: the write is tmp+rename (a reader
// must never see a half-written port), and the temp name must not linger.
func TestWriteStickyPortLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteStickyPort(dir, 1234); err != nil {
		t.Fatalf("WriteStickyPort: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != PortFileName {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("state dir holds %v, want exactly [%s]", names, PortFileName)
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(PortFilePath(dir))
		if statErr != nil {
			t.Fatalf("stat: %v", statErr)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("record mode is %v, want 0600 like every other file this package writes", info.Mode().Perm())
		}
	}
}

// TestWriteStickyPortCreatesTheStateDir: a supervisor can start a runtime
// on a machine whose state dir does not exist yet.
func TestWriteStickyPortCreatesTheStateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not", "created", "yet")
	if err := WriteStickyPort(dir, 4321); err != nil {
		t.Fatalf("WriteStickyPort into a missing dir: %v", err)
	}
	if got, err := ReadStickyPort(dir); err != nil || got != 4321 {
		t.Fatalf("ReadStickyPort = %d, %v", got, err)
	}
}

// TestStickyAddr is the whole policy in one table: the record is consulted
// ONLY for the runtime's own "let the OS pick" default, never to
// second-guess an address a human typed a port into.
func TestStickyAddr(t *testing.T) {
	for _, tc := range []struct {
		name      string
		requested string
		sticky    int
		want      string
		wantOK    bool
	}{
		{"the runtime default with a record reuses it", "127.0.0.1:0", 45678, "127.0.0.1:45678", true},
		{"an explicit port is never overridden", "127.0.0.1:8080", 45678, "", false},
		{"no record means nothing to reuse", "127.0.0.1:0", 0, "", false},
		{"a nonsense record is not an address", "127.0.0.1:0", 70000, "", false},
		{"the host is preserved verbatim", "[::1]:0", 45678, "[::1]:45678", true},
		{"an empty host stays empty", ":0", 45678, ":45678", true},
		{"an unsplittable address is left alone", "not-an-address", 45678, "", false},
		{"a bare host with no port is left alone", "127.0.0.1", 45678, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := StickyAddr(tc.requested, tc.sticky)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("StickyAddr(%q, %d) = %q, %v; want %q, %v", tc.requested, tc.sticky, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
