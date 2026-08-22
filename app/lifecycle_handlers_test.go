package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/lifecycle"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd"

func okHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// --------------------------------------------------------- token gate

// TestRequireLifecycleTokenMissing pins ARCHITECTURE.md Decision 8's
// first refusal: nothing offered at all gets 401, never a pass-through.
func TestRequireLifecycleTokenMissing(t *testing.T) {
	h := requireLifecycleToken(testToken, okHandler)
	req := httptest.NewRequest(http.MethodGet, "/api/lifecycle/status", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d for a missing token", rec.Code, http.StatusUnauthorized)
	}
}

// TestRequireLifecycleTokenWrong exercises a mismatch at the FIRST byte,
// at the LAST byte, and at a wrong length — all three must be refused
// with 403, which is the outcome subtle.ConstantTimeCompare exists to
// keep true regardless of WHERE the mismatch falls (a length check short
// -circuits before the constant-time compare only when the lengths
// themselves already differ, which is not a secret — the token's length
// is fixed and known).
func TestRequireLifecycleTokenWrong(t *testing.T) {
	cases := map[string]string{
		"mismatch at first byte": "F" + testToken[1:],
		"mismatch at last byte":  testToken[:len(testToken)-1] + "F",
		"wrong length (shorter)": testToken[:len(testToken)-1],
		"wrong length (longer)":  testToken + "0",
		"empty-ish garbage":      strings.Repeat("z", len(testToken)),
	}
	for name, got := range cases {
		t.Run(name, func(t *testing.T) {
			h := requireLifecycleToken(testToken, okHandler)
			req := httptest.NewRequest(http.MethodGet, "/api/lifecycle/status", nil)
			req.Header.Set(lifecycle.TokenHeader, got)
			rec := httptest.NewRecorder()
			h(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d for a wrong token (%s)", rec.Code, http.StatusForbidden, name)
			}
		})
	}
}

// TestRequireLifecycleTokenRight is the positive case: the exact token
// passes through to the wrapped handler untouched.
func TestRequireLifecycleTokenRight(t *testing.T) {
	called := false
	h := requireLifecycleToken(testToken, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/lifecycle/status", nil)
	req.Header.Set(lifecycle.TokenHeader, testToken)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for the right token", rec.Code)
	}
	if !called {
		t.Fatal("the wrapped handler was never called for the right token")
	}
}

// TestLifecyclePreflightGetsNoPermissiveHeaders is ARCHITECTURE.md
// Decision 8's CSRF defence, tested the way a browser actually probes
// it: an OPTIONS preflight carrying Origin/Access-Control-Request-*
// headers, against the SAME mux registration main.go uses (a
// method-scoped pattern per route). No response along the way may ever
// carry an Access-Control-Allow-* header — that is what makes the
// browser refuse to send the real request with its custom
// X-Dexel-Token header.
func TestLifecyclePreflightGetsNoPermissiveHeaders(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET /api/lifecycle/status", requireLifecycleToken(testToken,
		lifecycleStatusHandler(lifecycle.Runtime{Pid: 1, Port: 2, URL: "http://127.0.0.1:2", Version: "v9.9.9"}, "/s/state.db", "/s/logs/runtime.log", time.Now())))
	mux.Handle("POST /api/lifecycle/stop", requireLifecycleToken(testToken, lifecycleStopHandler(make(chan struct{}, 1))))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	for _, path := range []string{"/api/lifecycle/status", "/api/lifecycle/stop"} {
		req, err := http.NewRequest(http.MethodOptions, ts.URL+path, nil)
		if err != nil {
			t.Fatalf("build OPTIONS request: %v", err)
		}
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", lifecycle.TokenHeader)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("OPTIONS %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("OPTIONS %s answered 200 — a preflight must never succeed against /api/lifecycle/*", path)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("OPTIONS %s: Access-Control-Allow-Origin = %q, want no CORS header at all", path, got)
		}
		if got := resp.Header.Get("Access-Control-Allow-Headers"); got != "" {
			t.Fatalf("OPTIONS %s: Access-Control-Allow-Headers = %q, want no CORS header at all", path, got)
		}
		if got := resp.Header.Get("Access-Control-Allow-Methods"); got != "" {
			t.Fatalf("OPTIONS %s: Access-Control-Allow-Methods = %q, want no CORS header at all", path, got)
		}
	}
}

// -------------------------------------------------------------- status

// TestLifecycleStatusHandlerShape pins GET /api/lifecycle/status's body:
// every field this PR's slice promises (running/pid/port/url/version/
// commit/startedAt/uptimeSeconds/statePath/logPath), with pid/version
// carrying the values `dexel status`'s pid-equality check and
// is-this-dexel test key off.
func TestLifecycleStatusHandlerShape(t *testing.T) {
	rt := lifecycle.Runtime{
		Pid: 4242, Port: 51637, URL: "http://127.0.0.1:51637",
		Version: "v9.9.9", Commit: "abc1234", StartedAt: "2026-08-22T09:14:02Z",
		Token: testToken,
	}
	started := time.Now().Add(-30 * time.Second)
	h := lifecycleStatusHandler(rt, "/state/state.db", "/state/logs/runtime.log", started)

	req := httptest.NewRequest(http.MethodGet, "/api/lifecycle/status", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var got lifecycleStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, rec.Body.String())
	}
	if !got.Running {
		t.Fatal("running = false, want true — only a live process can answer this handler at all")
	}
	if got.Pid != rt.Pid {
		t.Fatalf("pid = %d, want %d (runtime.json's own pid — the CLI's pid-equality check)", got.Pid, rt.Pid)
	}
	if got.Port != rt.Port || got.URL != rt.URL || got.Version != rt.Version || got.Commit != rt.Commit || got.StartedAt != rt.StartedAt {
		t.Fatalf("response = %+v, does not match runtimeInfo %+v", got, rt)
	}
	if got.UptimeSeconds < 29 || got.UptimeSeconds > 31 {
		t.Fatalf("uptimeSeconds = %d, want ~30 (startedAt was 30s ago)", got.UptimeSeconds)
	}
	if got.StatePath != "/state/state.db" {
		t.Fatalf("statePath = %q, want /state/state.db", got.StatePath)
	}
	if got.LogPath != "/state/logs/runtime.log" {
		t.Fatalf("logPath = %q, want /state/logs/runtime.log", got.LogPath)
	}

	// The response must never leak the capability token itself — it is
	// the credential that gets you IN, not something this endpoint hands
	// back out.
	if strings.Contains(rec.Body.String(), rt.Token) {
		t.Fatal("the status response body contains the capability token")
	}
}

// ---------------------------------------------------------------- stop

// TestLifecycleStopHandlerRespondsAcceptedAndSignalsControl pins
// ARCHITECTURE.md §3's "stop -> 202, then the runtime does exactly what
// the existing case <-sigCh: branch does": the handler answers 202
// BEFORE (or without depending on) anything reading controlCh, and
// exactly one signal lands on it.
func TestLifecycleStopHandlerRespondsAcceptedAndSignalsControl(t *testing.T) {
	controlCh := make(chan struct{}, 1)
	h := lifecycleStopHandler(controlCh)

	req := httptest.NewRequest(http.MethodPost, "/api/lifecycle/stop", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	var got lifecycleStopResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.Stopping {
		t.Fatal("stopping = false, want true")
	}

	select {
	case <-controlCh:
		// good: the handler signalled the single-owner loop's shutdown
		// trigger.
	default:
		t.Fatal("controlCh received nothing — the handler must signal a shutdown")
	}
}

// TestLifecycleStopHandlerSecondCallNeverBlocks is the duplicate-request
// safety net: controlCh is buffered by exactly 1 (main.go), so a second
// POST /api/lifecycle/stop landing after the first already queued a
// signal must return immediately rather than hanging the handler
// goroutine on a channel nobody is draining anymore.
func TestLifecycleStopHandlerSecondCallNeverBlocks(t *testing.T) {
	controlCh := make(chan struct{}, 1)
	h := lifecycleStopHandler(controlCh)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 2; i++ {
			req := httptest.NewRequest(http.MethodPost, "/api/lifecycle/stop", nil)
			rec := httptest.NewRecorder()
			h(rec, req)
			if rec.Code != http.StatusAccepted {
				t.Errorf("call #%d: status = %d, want %d", i, rec.Code, http.StatusAccepted)
			}
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a second /api/lifecycle/stop call blocked — controlCh's buffered send must never wait")
	}
}
