package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/game"
	"github.com/jawwadzafar/dexel/app/internal/lifecycle"
)

// actionLoop is a stand-in for main.go's single-owner select loop: one
// goroutine owns the *game.Game and is the only thing that ever touches
// it, exactly as store_gate_test.go's testServer does. `paused` reads the
// flag by running the read ON that goroutine and handing the answer back
// over a channel — reading g.Paused() from the test goroutine would
// itself be the data race game.Game's unlocked design forbids.
type actionLoop struct {
	actions chan actionRequest
	paused  func() bool
}

func newActionLoop(t *testing.T) *actionLoop {
	t.Helper()
	g := game.New()
	actions := make(chan actionRequest)
	queries := make(chan func())
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case <-stop:
				return
			case req := <-actions:
				applyAction(g, req.msg, req.connID)
				close(req.done)
			case fn := <-queries:
				fn()
			}
		}
	}()
	return &actionLoop{
		actions: actions,
		paused: func() bool {
			result := make(chan bool, 1)
			queries <- func() { result <- g.Paused() }
			return <-result
		},
	}
}

// lifecycleActionTimeoutForTest shortens the handler's submit deadline for
// the duration of one test and returns the restore function.
func lifecycleActionTimeoutForTest(d time.Duration) func() {
	saved := lifecycleActionTimeout
	lifecycleActionTimeout = d
	return func() { lifecycleActionTimeout = saved }
}

// --------------------------------------------------- token gate (PR-5)

// TestPauseEndpointsEnforceTheSameTokenDiscipline: PR-5's two verbs go
// through the IDENTICAL requireLifecycleToken wrapper PR-4's do — no
// token 401, wrong token 403, right token through — because a pause
// endpoint that anyone on loopback could call would be a remote
// "stop counting my work" button.
func TestPauseEndpointsEnforceTheSameTokenDiscipline(t *testing.T) {
	// A loop-side goroutine that accepts and applies whatever arrives, so
	// the "right token" case reaches a real 200 rather than the 503 a
	// dead loop would produce.
	loop := newActionLoop(t)
	actions, paused := loop.actions, loop.paused

	for _, verb := range []string{"pause", "resume"} {
		h := requireLifecycleToken(testToken, lifecyclePauseHandler(actions, paused, verb == "pause"))

		for _, tc := range []struct {
			name  string
			token string
			want  int
		}{
			{"no token", "", http.StatusUnauthorized},
			{"wrong token", "0000000000000000000000000000000000000000000000000000000000000000", http.StatusForbidden},
			{"right token", testToken, http.StatusOK},
		} {
			req := httptest.NewRequest(http.MethodPost, "/api/lifecycle/"+verb, nil)
			if tc.token != "" {
				req.Header.Set(lifecycle.TokenHeader, tc.token)
			}
			rec := httptest.NewRecorder()
			h(rec, req)
			if rec.Code != tc.want {
				t.Errorf("%s with %s: status = %d, want %d (body %q)", verb, tc.name, rec.Code, tc.want, rec.Body.String())
			}
			if rec.Code != http.StatusOK && rec.Body.Len() > 0 && json.Valid(rec.Body.Bytes()) {
				// A refusal must not look like a successful JSON answer;
				// http.Error's text/plain body is the honest shape.
				var body map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err == nil {
					if _, ok := body["paused"]; ok {
						t.Errorf("%s with %s: a refusal answered with a `paused` field — a rejected request must never report state", verb, tc.name)
					}
				}
			}
		}
	}
}

// TestPauseHandlerAppliesTheActionAndReportsTheEffectiveState: a 200 means
// the pause is REALLY in effect — the handler blocks until the
// single-owner loop has applied it — and the reported value comes from the
// runtime, not from an echo of the request.
func TestPauseHandlerAppliesTheActionAndReportsTheEffectiveState(t *testing.T) {
	loop := newActionLoop(t)
	actions, livePaused := loop.actions, loop.paused

	post := func(want bool) lifecyclePauseResponse {
		t.Helper()
		h := lifecyclePauseHandler(actions, livePaused, want)
		verb := "resume"
		if want {
			verb = "pause"
		}
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, "/api/lifecycle/"+verb, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s: status = %d, want 200 (%q)", verb, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("POST %s: Content-Type = %q, want application/json", verb, ct)
		}
		var body lifecyclePauseResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("POST %s: decode %q: %v", verb, rec.Body.String(), err)
		}
		return body
	}

	if got := post(true); !got.Paused {
		t.Error("POST /pause answered {paused:false}")
	}
	if !livePaused() {
		t.Error("the game is not paused after a 200 from /pause — the handler must not answer before the action is applied")
	}
	// Idempotent: a second pause is still an honest 200 reporting paused.
	if got := post(true); !got.Paused {
		t.Error("a second POST /pause answered {paused:false}")
	}
	if got := post(false); got.Paused {
		t.Error("POST /resume answered {paused:true}")
	}
	if livePaused() {
		t.Error("the game is still paused after a 200 from /resume")
	}
}

// TestPauseHandlerAnswers503WhenTheLoopIsGone: the runtime has taken the
// shutdown branch (nothing is reading `actions` any more). The honest
// answer is a refusal, never a 200 for a pause that will never be
// applied — and never an indefinite hang either.
func TestPauseHandlerAnswers503WhenTheLoopIsGone(t *testing.T) {
	// Unbuffered and unread: exactly the shape of a select loop that has
	// returned. lifecycleActionTimeout is deliberately shortened for the
	// test's duration rather than waited out.
	saved := lifecycleActionTimeoutForTest(50 * time.Millisecond)
	t.Cleanup(saved)

	h := lifecyclePauseHandler(make(chan actionRequest), func() bool { return false }, true)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h(rec, httptest.NewRequest(http.MethodPost, "/api/lifecycle/pause", nil))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler hung on an unread actions channel — a pause request must never block a handler goroutine forever")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when the runtime is not accepting actions", rec.Code)
	}
}

// --------------------------------------------------------------- status

// TestLifecycleStatusReportsLivePaused: `paused` is read at REQUEST time,
// not captured at startup, so a pause that happens after the runtime
// booted is visible to `dexel status` (ARCHITECTURE.md Decision 16: "a
// paused-and-forgotten dexel must be obvious, never mute").
func TestLifecycleStatusReportsLivePaused(t *testing.T) {
	live := false
	h := lifecycleStatusHandler(
		lifecycle.Runtime{Pid: 7, Port: 8, URL: "http://127.0.0.1:8", Version: "v1.2.3"},
		"/s/state.db", "/s/runtime.log", time.Now(), func() bool { return live })

	get := func() lifecycleStatusResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/api/lifecycle/status", nil))
		var body lifecycleStatusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
		return body
	}

	if get().Paused {
		t.Error("paused = true before anything was paused")
	}
	live = true
	if !get().Paused {
		t.Error("paused = false after the runtime paused — the field must be read live, not captured at startup")
	}
}

// ---------------------------------------------------------- applyAction

// TestApplyActionPauseAndResume pins the pure state transition, including
// the `mutated` contract main.go's loop gates the provider stop/start, the
// engine reset and the immediate save on.
func TestApplyActionPauseAndResume(t *testing.T) {
	g := game.New()

	mutated, flash := applyAction(g, actionMessage{Action: actionPause}, 1)
	if !mutated {
		t.Error("PAUSE on a running game reported mutated=false — the provider would never be stopped and nothing would be saved")
	}
	if flash != nil {
		t.Errorf("PAUSE produced a flash (%+v); pause is a state, rendered from `paused` on the next state broadcast", flash)
	}
	if !g.Paused() {
		t.Error("PAUSE did not pause the game")
	}

	if mutated, _ := applyAction(g, actionMessage{Action: actionPause}, 1); mutated {
		t.Error("a second PAUSE reported mutated=true — a repeated pause must not trigger a second provider stop or a second save")
	}

	if mutated, _ := applyAction(g, actionMessage{Action: actionResume}, 1); !mutated {
		t.Error("RESUME on a paused game reported mutated=false")
	}
	if g.Paused() {
		t.Error("RESUME did not resume the game")
	}
	if mutated, _ := applyAction(g, actionMessage{Action: actionResume}, 1); mutated {
		t.Error("a second RESUME reported mutated=true")
	}
}
