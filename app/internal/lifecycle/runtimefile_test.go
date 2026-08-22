package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// healthBody is a stand-in for app/main.go's healthResponse — the exact
// shape Probe must accept, with the two fields (version, source) it uses
// as its "this really is a dexel" test.
const healthBody = `{"assetsDir":null,"publicOk":true,"version":"v9.9.9","commit":"abc1234","source":"embedded","publicSource":"embedded","assetsSource":"embedded"}`

// healthServer starts a loopback server that answers /api/health like a
// real runtime, recording the token header it was sent so a test can
// prove the CLI half of ARCHITECTURE.md Decision 8's contract is already
// wired (the server-side enforcement is PR-4's).
func healthServer(t *testing.T, body string) (*httptest.Server, *string) {
	t.Helper()
	var seenToken string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		seenToken = r.Header.Get(TokenHeader)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &seenToken
}

// TestWriteRuntimeIsAtomicAnd0600 pins the two properties
// ARCHITECTURE.md Decision 6 and Decision 8 name explicitly: the file is
// written atomically (so a polling `dexel start` can never read a
// half-written object) and at mode 0600 (because it carries the
// capability token).
func TestWriteRuntimeIsAtomicAnd0600(t *testing.T) {
	dir := t.TempDir()
	want := Runtime{
		Pid: 4242, Port: 51637, URL: "http://127.0.0.1:51637",
		Version: "v9.9.9", Commit: "abc1234",
		StartedAt: "2026-08-22T09:14:02Z", Token: strings.Repeat("a", 64),
	}
	if err := WriteRuntime(dir, want); err != nil {
		t.Fatalf("WriteRuntime: %v", err)
	}

	path := RuntimePath(dir)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("runtime.json mode = %v, want 0600 — it carries the capability token (ARCHITECTURE.md Decision 8)", got)
		}
	}

	// Atomicity's observable consequence: no temp file is left behind,
	// and the final file parses completely.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("left a temp file behind: %s", e.Name())
		}
	}

	got, err := ReadRuntime(dir)
	if err != nil {
		t.Fatalf("ReadRuntime: %v", err)
	}
	if got.Schema != RuntimeSchema {
		t.Fatalf("schema = %d, want %d (WriteRuntime must stamp it)", got.Schema, RuntimeSchema)
	}
	want.Schema = RuntimeSchema
	if got != want {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// TestReadRuntimeRejectsUnusableFiles covers every way runtime.json can
// exist and still be worthless. Each of these must be an ERROR (which
// Discover then turns into "not running" plus a cleanup), never a
// half-trusted Runtime.
func TestReadRuntimeRejectsUnusableFiles(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantSub string
	}{
		{"garbage", "not json at all", "parse runtime.json"},
		{"future schema", fmt.Sprintf(`{"schema":%d,"pid":1,"port":2}`, RuntimeSchema+1), "newer than this dexel understands"},
		{"no pid", `{"schema":1,"pid":0,"port":8080}`, "incomplete"},
		{"no port", `{"schema":1,"pid":123,"port":0}`, "incomplete"},
		{"negative pid", `{"schema":1,"pid":-5,"port":8080}`, "incomplete"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(RuntimePath(dir), []byte(tc.content), 0o600); err != nil {
				t.Fatalf("seed: %v", err)
			}
			_, err := ReadRuntime(dir)
			if err == nil {
				t.Fatalf("ReadRuntime accepted %q — it must not", tc.content)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantSub)
			}
		})
	}
}

// TestReadRuntimeMissingFileIsErrNoRuntime: "no file" is the ordinary
// answer on a machine where dexel has never been started, and must be
// distinguishable from a broken file so Discover does not report a
// cleanup that never happened.
func TestReadRuntimeMissingFileIsErrNoRuntime(t *testing.T) {
	_, err := ReadRuntime(t.TempDir())
	if !errors.Is(err, ErrNoRuntime) {
		t.Fatalf("err = %v, want ErrNoRuntime", err)
	}
}

// TestReadRuntimeDerivesURLWhenAbsent: url is redundant with port, and a
// file written by an older/other writer that omits it must still be
// usable rather than probing the empty string.
func TestReadRuntimeDerivesURLWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(RuntimePath(dir), []byte(`{"schema":1,"pid":9,"port":1234}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := ReadRuntime(dir)
	if err != nil {
		t.Fatalf("ReadRuntime: %v", err)
	}
	if got.URL != "http://127.0.0.1:1234" {
		t.Fatalf("derived url = %q, want http://127.0.0.1:1234", got.URL)
	}
}

// TestNewTokenIsHexAndUnique pins ARCHITECTURE.md Decision 8's "32 bytes
// of crypto/rand, hex" and "regenerated on every start" — a token that
// repeated across starts would be a credential with a lifetime nobody
// asked for.
func TestNewTokenIsHexAndUnique(t *testing.T) {
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		tok, err := NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if !hex64.MatchString(tok) {
			t.Fatalf("token %q is not 64 hex chars (32 crypto/rand bytes)", tok)
		}
		if seen[tok] {
			t.Fatalf("NewToken repeated %q — it must be fresh on every start", tok)
		}
		seen[tok] = true
	}
}

// TestDiscoverConfirmsALiveRuntimeByRoundTrip is the positive half of
// ARCHITECTURE.md Decision 6: a runtime.json whose URL answers
// /api/health with a dexel body is RUNNING, the file survives, and the
// capability token was sent on the wire.
func TestDiscoverConfirmsALiveRuntimeByRoundTrip(t *testing.T) {
	ts, seenToken := healthServer(t, healthBody)
	dir := t.TempDir()
	if err := WriteRuntime(dir, Runtime{Pid: os.Getpid(), Port: 1, URL: ts.URL, Token: "deadbeef"}); err != nil {
		t.Fatalf("WriteRuntime: %v", err)
	}

	st, err := Discover(dir, ProbeClient(0))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !st.Running {
		t.Fatalf("Running = false (reason %q), want true", st.Reason)
	}
	if st.Cleaned {
		t.Fatal("Cleaned = true — a LIVE runtime's file must never be deleted")
	}
	if st.Health.Version != "v9.9.9" {
		t.Fatalf("health version = %q, want v9.9.9", st.Health.Version)
	}
	if *seenToken != "deadbeef" {
		t.Fatalf("server saw %s = %q, want the runtime.json token — the CLI half of Decision 8's contract", TokenHeader, *seenToken)
	}
	if _, err := os.Stat(RuntimePath(dir)); err != nil {
		t.Fatalf("runtime.json was removed under a live runtime: %v", err)
	}
}

// TestDiscoverTreatsADeadPortAsStaleAndCleansUp is THE staleness test,
// and the reason ARCHITECTURE.md Decision 6 exists: a runtime.json with a
// perfectly plausible pid and port, pointing at nothing. It must report
// not-running AND delete the file — "a pidfile alone cannot survive pid
// reuse; a live HTTP round-trip can."
//
// The pid used is this test process's own, which is very much alive, so
// the test proves the decision is made by the ROUND-TRIP and could not
// have been made by a pid check.
func TestDiscoverTreatsADeadPortAsStaleAndCleansUp(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadURL := "http://" + ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	dir := t.TempDir()
	if err := WriteRuntime(dir, Runtime{Pid: os.Getpid(), Port: 1, URL: deadURL, Token: "t"}); err != nil {
		t.Fatalf("WriteRuntime: %v", err)
	}

	st, err := Discover(dir, ProbeClient(0))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if st.Running {
		t.Fatal("Running = true for a closed port — Discover trusted the file")
	}
	if !st.Cleaned {
		t.Fatal("Cleaned = false — a stale runtime.json must be removed")
	}
	if _, err := os.Stat(RuntimePath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime.json still exists after a stale discovery (stat err = %v)", err)
	}
}

// TestDiscoverRejectsAForeignListener: something else answering on that
// port is exactly what pid/port reuse looks like from the outside. A 200
// with a non-dexel body must be treated as stale, not as dexel.
func TestDiscoverRejectsAForeignListener(t *testing.T) {
	for _, body := range []string{`{}`, `{"version":""}`, `{"hello":"world"}`, `[]`} {
		t.Run(body, func(t *testing.T) {
			ts, _ := healthServer(t, body)
			dir := t.TempDir()
			if err := WriteRuntime(dir, Runtime{Pid: os.Getpid(), Port: 1, URL: ts.URL}); err != nil {
				t.Fatalf("WriteRuntime: %v", err)
			}
			st, err := Discover(dir, ProbeClient(0))
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if st.Running {
				t.Fatalf("Running = true for a non-dexel body %q", body)
			}
			if !st.Cleaned {
				t.Fatalf("Cleaned = false for a non-dexel body %q", body)
			}
		})
	}
}

// TestDiscoverRejectsANon200: a server on that port that errors is not a
// runtime we can control, so it is not a runtime.
func TestDiscoverRejectsANon200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer ts.Close()
	dir := t.TempDir()
	if err := WriteRuntime(dir, Runtime{Pid: os.Getpid(), Port: 1, URL: ts.URL}); err != nil {
		t.Fatalf("WriteRuntime: %v", err)
	}
	st, err := Discover(dir, ProbeClient(0))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if st.Running || !st.Cleaned {
		t.Fatalf("Running=%v Cleaned=%v, want false/true for a 500", st.Running, st.Cleaned)
	}
	if !strings.Contains(st.Reason, "http 500") {
		t.Fatalf("Reason = %q, want it to name the status", st.Reason)
	}
}

// TestDiscoverNoFileIsNotACleanup: "never started" must not claim it
// cleaned something up, because `dexel start`/`status` print that claim
// to the user.
func TestDiscoverNoFileIsNotACleanup(t *testing.T) {
	dir := t.TempDir()
	st, err := Discover(dir, ProbeClient(0))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if st.Running {
		t.Fatal("Running = true with no runtime.json")
	}
	if st.Cleaned {
		t.Fatal("Cleaned = true with no runtime.json to clean")
	}
	if !strings.Contains(st.Reason, "no runtime.json") {
		t.Fatalf("Reason = %q, want it to say there is no runtime.json", st.Reason)
	}
}

// TestDiscoverRemovesAnUnparseableFile: a corrupt runtime.json is itself
// proof of staleness — WriteRuntime renames a COMPLETE file into place,
// so no live runtime ever leaves a broken one.
func TestDiscoverRemovesAnUnparseableFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(RuntimePath(dir), []byte("{ truncated"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	st, err := Discover(dir, ProbeClient(0))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if st.Running || !st.Cleaned {
		t.Fatalf("Running=%v Cleaned=%v, want false/true", st.Running, st.Cleaned)
	}
	if _, err := os.Stat(RuntimePath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt runtime.json survived Discover (stat err = %v)", err)
	}
}

// TestRemoveRuntimeIsIdempotent: the clean-exit path and the stale-cleanup
// path can legitimately race, so a second removal must be success.
func TestRemoveRuntimeIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRuntime(dir, Runtime{Pid: 1, Port: 2}); err != nil {
		t.Fatalf("WriteRuntime: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := RemoveRuntime(dir); err != nil {
			t.Fatalf("RemoveRuntime #%d: %v", i, err)
		}
	}
}

// TestPathHelpersUseTheDocumentedBasenames pins PLATFORM_NOTES.md §1's
// table: the discovery file, the lock and the log have fixed names, and
// app/internal/paths joins them onto the state/log directory.
func TestPathHelpersUseTheDocumentedBasenames(t *testing.T) {
	if got, want := RuntimePath("/s"), filepath.Join("/s", "runtime.json"); got != want {
		t.Fatalf("RuntimePath = %q, want %q", got, want)
	}
	if got, want := LockPath("/s"), filepath.Join("/s", "runtime.lock"); got != want {
		t.Fatalf("LockPath = %q, want %q", got, want)
	}
	if got, want := LogPath("/s/logs"), filepath.Join("/s/logs", "runtime.log"); got != want {
		t.Fatalf("LogPath = %q, want %q", got, want)
	}
}

// TestRuntimeJSONFieldNames pins the wire names ARCHITECTURE.md Decision
// 6 shows, because runtime.json is read by `dexel status --json`
// consumers and (from PR-9) by the Tauri shell: renaming a key silently
// is a breaking change to a documented file.
func TestRuntimeJSONFieldNames(t *testing.T) {
	data, err := json.Marshal(Runtime{Schema: 1, Pid: 1, Port: 2, URL: "u", Version: "v", Commit: "c", StartedAt: "s", Token: "t"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{"schema", "pid", "port", "url", "version", "commit", "startedAt", "token"}
	if len(m) != len(want) {
		t.Fatalf("runtime.json has %d keys (%v), want exactly %d (%v)", len(m), m, len(want), want)
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Fatalf("runtime.json is missing key %q", k)
		}
	}
}

// TestQueryNeverDeletesTheFile is a REGRESSION test for a real race in
// the first cut of PR-3, where `dexel start`'s readiness poll used the
// CLEANING Discover: the child writes runtime.json immediately after
// net.Listen (before http.Server.Serve is up), so a probe landing in that
// window failed, and the poller deleted the file the child had just
// correctly written — turning a perfectly healthy start into a
// ten-second timeout waiting for a file nobody would write again.
//
// Query must therefore be purely observational, on EVERY not-running
// path.
func TestQueryNeverDeletesTheFile(t *testing.T) {
	// A bound-but-not-serving listener: the exact startup window that
	// caused the bug. The connection is accepted by the kernel and then
	// nothing answers, so the probe times out.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	dir := t.TempDir()
	if err := WriteRuntime(dir, Runtime{Pid: os.Getpid(), Port: 1, URL: "http://" + ln.Addr().String()}); err != nil {
		t.Fatalf("WriteRuntime: %v", err)
	}

	st, err := Query(dir, ProbeClient(150*time.Millisecond))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if st.Running {
		t.Fatal("Running = true against a bound-but-not-serving listener")
	}
	if st.Cleaned {
		t.Fatal("Query reported Cleaned — Query must never delete anything")
	}
	if !st.HasFile {
		t.Fatal("HasFile = false although runtime.json is right there")
	}
	if _, err := os.Stat(RuntimePath(dir)); err != nil {
		t.Fatalf("Query DELETED runtime.json — this is the race that broke `dexel start`: %v", err)
	}

	// Same for a corrupt file, and for no file at all.
	if err := os.WriteFile(RuntimePath(dir), []byte("{ nope"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := Query(dir, ProbeClient(0)); err != nil {
		t.Fatalf("Query on a corrupt file: %v", err)
	}
	if _, err := os.Stat(RuntimePath(dir)); err != nil {
		t.Fatalf("Query deleted a corrupt runtime.json: %v", err)
	}

	empty := t.TempDir()
	st, err = Query(empty, ProbeClient(0))
	if err != nil {
		t.Fatalf("Query on an empty dir: %v", err)
	}
	if st.HasFile || st.Running || st.Cleaned {
		t.Fatalf("Query on an empty dir = %+v, want all-false", st)
	}
}

// TestDiscoverIsQueryPlusCleanup pins the split: Discover's ONLY addition
// is deleting an unconfirmable file and saying it did.
func TestDiscoverIsQueryPlusCleanup(t *testing.T) {
	ts, _ := healthServer(t, healthBody)
	dir := t.TempDir()
	if err := WriteRuntime(dir, Runtime{Pid: os.Getpid(), Port: 1, URL: ts.URL}); err != nil {
		t.Fatalf("WriteRuntime: %v", err)
	}

	// Live: Query and Discover must agree, and neither may clean.
	q, err := Query(dir, ProbeClient(0))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	d, err := Discover(dir, ProbeClient(0))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !q.Running || !d.Running || q.Cleaned || d.Cleaned {
		t.Fatalf("live: Query=%+v Discover=%+v", q, d)
	}

	// Dead: Query observes, Discover cleans.
	ts.Close()
	q, err = Query(dir, ProbeClient(0))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if q.Running || q.Cleaned || !q.HasFile {
		t.Fatalf("dead Query = %+v", q)
	}
	if _, err := os.Stat(RuntimePath(dir)); err != nil {
		t.Fatalf("Query deleted the file: %v", err)
	}
	d, err = Discover(dir, ProbeClient(0))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if d.Running || !d.Cleaned {
		t.Fatalf("dead Discover = %+v, want Running=false Cleaned=true", d)
	}
	if _, err := os.Stat(RuntimePath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Discover did not delete the stale file (stat err = %v)", err)
	}
}
