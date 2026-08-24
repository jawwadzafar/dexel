// lifecycle_handlers.go — the lifecycle control plane's HTTP surface
// (docs/production-runtime/MIGRATION_PLAN.md §PR-4,
// docs/production-runtime/ARCHITECTURE.md §3 Decisions 5 and 8).
//
// The four routes, all sharing one token gate:
//
//	GET  /api/lifecycle/status
//	POST /api/lifecycle/stop
//	POST /api/lifecycle/pause    (PR-5)
//	POST /api/lifecycle/resume   (PR-5)
//
// The two shutdown/pause mechanisms are deliberately different shapes,
// because the risks are different. `stop` pushes onto a private
// `controlCh` that only the select loop reads, and is deliberately NOT a
// WebSocket action: "a web page must never be able to kill the runtime".
// `pause`/`resume` ride the EXISTING `actions` channel (ARCHITECTURE.md
// Decision 5), which is what keeps game.Game single-owner and what gives
// the WebSocket UI a pause button with no second code path — pausing is
// privacy-positive and fully reversible, so there is nothing to protect
// the user from there.
//
// Both routes exist only on a modeRuntime process (main.go's runServe):
// `dexel serve` and the legacy foreground shape never write a
// runtime.json and never mint a token (ARCHITECTURE.md Decision 7), so
// they have nothing valid to check a X-Dexel-Token against and register
// neither route — a request to either path there 404s off the bare mux,
// exactly like any other undefined path, rather than existing in some
// half-authenticated form.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/lifecycle"
)

// lifecycleStatusResponse is GET /api/lifecycle/status's body. Unlike
// /api/health (unauthenticated, and reports only static build-time
// facts), this reports THIS PROCESS'S OWN pid — which is what lets the
// CLI's `dexel status` perform ARCHITECTURE.md Decision 6's pid-equality
// check against runtime.json's copy, the "last defence against pid
// reuse" that a bare live-200-from-the-right-port cannot provide.
type lifecycleStatusResponse struct {
	// Running is always true: only a live process still holding the
	// listener can answer this handler at all, so there is no "false"
	// case to report — its presence just keeps this shape symmetric with
	// `dexel status --json`'s own statusJSON.
	Running       bool   `json:"running"`
	Pid           int    `json:"pid"`
	Port          int    `json:"port"`
	URL           string `json:"url"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	StartedAt     string `json:"startedAt"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
	StatePath     string `json:"statePath"`
	LogPath       string `json:"logPath"`
	// Paused (PR-5, MIGRATION_PLAN.md §PR-5, ARCHITECTURE.md §3's status
	// object and Decision 16's "a paused runtime ... `dexel status`
	// reports paused: true") is the one field here that is a LIVE fact
	// rather than a startup constant, so it is read through a callback at
	// request time (see lifecycleStatusHandler) instead of being captured
	// once. It is what makes a paused-and-forgotten dexel discoverable
	// from a terminal, not only from the UI.
	Paused bool `json:"paused"`
}

// lifecycleStopResponse is POST /api/lifecycle/stop's body: 202 Accepted,
// per ARCHITECTURE.md §3's "`stop` -> 202, then the runtime does exactly
// what the existing `case <-sigCh:` branch does" — the response is sent
// and the connection allowed to finish BEFORE the shutdown sequence's
// httpSrv.Shutdown ever runs (see lifecycleStopHandler).
type lifecycleStopResponse struct {
	Stopping bool `json:"stopping"`
}

// requireLifecycleToken wraps a lifecycle handler with ARCHITECTURE.md
// Decision 8's enforcement:
//
//   - a required X-Dexel-Token header, compared to runtime.json's token
//     in constant time (crypto/subtle) — timing must never let an
//     attacker learn how many leading bytes of a guess were right;
//   - deliberately nothing that would let a CORS preflight succeed. This
//     handler never sets any Access-Control-* header, and it is
//     registered on the mux under its one exact HTTP method (main.go's
//     "GET /api/lifecycle/status" / "POST /api/lifecycle/stop" patterns),
//     so an OPTIONS preflight simply 404s. A browser is REQUIRED by the
//     fetch spec to preflight before sending a custom header
//     cross-origin, and with no Access-Control-Allow-* header ever
//     present in any response under /api/lifecycle/*, that preflight can
//     never succeed and the browser never sends the real request — the
//     same drive-by-CSRF defence B1 already gave the WebSocket, applied
//     here by construction rather than by an explicit Origin allow-list.
//
// Missing token -> 401 (nothing was even offered to check); a token that
// does not match -> 403 (something was offered, and it was wrong). Both
// are refusals a legitimate CLI, which always sends the real token, never
// observes.
func requireLifecycleToken(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(lifecycle.TokenHeader)
		if got == "" {
			http.Error(w, "missing "+lifecycle.TokenHeader, http.StatusUnauthorized)
			return
		}
		// subtle.ConstantTimeCompare only says anything about equality
		// for equal-length inputs; a length mismatch already proves the
		// token is wrong (a real token is always the same fixed length —
		// its length is not a secret), so it is safe to branch on before
		// the constant-time comparison rather than padding to compare.
		if len(got) != len(token) || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "invalid "+lifecycle.TokenHeader, http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// lifecycleStatusHandler serves GET /api/lifecycle/status from the exact
// Runtime record main.go's writeRuntimeFile persisted to runtime.json —
// the same pid, port, url, version, commit and token that file carries,
// so the client-side pid-equality check (lifecycle.Query/Discover,
// app/internal/lifecycle/runtimefile.go) is comparing two copies of the
// same fact rather than two independent guesses at it.
//
// statePath/logPath/startedAt are passed in rather than re-derived here,
// for the same reason runtimeInfo is: main.go already resolved them once
// at startup (savePath via store.DefaultPath, logPath via
// paths.LogFile(), startedAt by parsing runtimeInfo's own StartedAt) and
// this handler must report exactly those, not a second computation that
// could quietly drift from them.
// paused is the one LIVE reader this handler needs (PR-5): main.go passes
// a closure over Hub.getLastState, so the answer comes from the most
// recent state broadcast rather than from game.Game — which only the
// single-owner select loop may touch, and which an HTTP handler goroutine
// must therefore never read directly.
func lifecycleStatusHandler(rt lifecycle.Runtime, statePath, logPath string, startedAt time.Time, paused func() bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		isPaused := false
		if paused != nil {
			isPaused = paused()
		}
		resp := lifecycleStatusResponse{
			Running:       true,
			Pid:           rt.Pid,
			Port:          rt.Port,
			URL:           rt.URL,
			Version:       rt.Version,
			Commit:        rt.Commit,
			StartedAt:     rt.StartedAt,
			UptimeSeconds: int64(time.Since(startedAt) / time.Second),
			StatePath:     statePath,
			LogPath:       logPath,
			Paused:        isPaused,
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Printf("encode lifecycle status response: %v", err)
		}
	}
}

// lifecycleStopHandler serves POST /api/lifecycle/stop. It writes the 202
// response FIRST, then signals controlCh — never the other way round —
// because main.go's select loop answers controlCh by calling the shared
// shutdown() closure, whose httpSrv.Shutdown(ctx) call waits for
// in-flight requests (this one) to finish before it tears down
// connections; writing the response before signalling is what guarantees
// the CLI's `dexel stop` actually receives its 202 rather than racing a
// server that is already halfway through shutting itself down.
//
// The channel send is non-blocking (controlCh is buffered by 1 in
// main.go): a second POST /api/lifecycle/stop landing after the first
// already queued a signal finds the buffer full and does nothing further
// — there is only ever one shutdown to perform, and the default case is
// what stops a duplicate request from blocking this handler goroutine
// forever waiting on a select loop that already decided to exit.
func lifecycleStopHandler(controlCh chan<- struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		if err := json.NewEncoder(w).Encode(lifecycleStopResponse{Stopping: true}); err != nil {
			log.Printf("encode lifecycle stop response: %v", err)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case controlCh <- struct{}{}:
		default:
		}
	}
}

// ---------------------------------------------------------- pause/resume

// lifecyclePauseResponse is POST /api/lifecycle/{pause,resume}'s body —
// ARCHITECTURE.md §3's `{paused: true}` / `{paused: false}` verbatim. The
// value is the state ACTUALLY IN EFFECT once the action has been applied,
// read back from the runtime rather than echoed from the request, so a
// caller never has to take the endpoint's word for what it did.
type lifecyclePauseResponse struct {
	Paused bool `json:"paused"`
}

// lifecyclePauseHandler serves POST /api/lifecycle/pause (want=true) and
// POST /api/lifecycle/resume (want=false).
//
// Unlike stop's fire-and-forget 202, this one WAITS: it submits an
// ordinary actionRequest on the same `actions` channel every WebSocket
// action uses and blocks until the single-owner loop closes req.done —
// which happens only after applyAction has flipped the flag, the provider
// has been stopped (or reset+restarted), the immediate save has been
// written and the new state has been broadcast. So a 200 from here means
// "tracking really is off/on now", not "your request was noted", and
// `dexel pause` can print the truth on the strength of the response alone.
//
// Both failure modes are honest rather than optimistic:
//
//   - the client hanging up (r.Context()) abandons the request, because a
//     caller that stopped listening must not have a pause applied behind
//     its back;
//   - a select loop that is not accepting actions within
//     lifecycleActionTimeout — realistically only when it has already
//     taken the shutdown branch — answers 503, never a fabricated 200.
//     The unbuffered `actions` channel is what makes this detectable at
//     all: there is no queue for a request to disappear into.
func lifecyclePauseHandler(actions chan<- actionRequest, paused func() bool, want bool) http.HandlerFunc {
	verb := "resume"
	if want {
		verb = "pause"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		action := actionResume
		if want {
			action = actionPause
		}
		req := actionRequest{msg: actionMessage{Action: action}, done: make(chan struct{})}

		timeout := time.NewTimer(lifecycleActionTimeout)
		defer timeout.Stop()
		select {
		case actions <- req:
		case <-r.Context().Done():
			// Nothing was submitted, so nothing happened — the state is
			// exactly as it was.
			return
		case <-timeout.C:
			http.Error(w, verb+": the runtime is not accepting actions (it may be shutting down)", http.StatusServiceUnavailable)
			return
		}

		// Submitted: the loop WILL apply it. From here on the only
		// question is whether we can still report the outcome.
		select {
		case <-req.done:
		case <-r.Context().Done():
			return
		}

		effective := want
		if paused != nil {
			effective = paused()
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(lifecyclePauseResponse{Paused: effective}); err != nil {
			log.Printf("encode lifecycle %s response: %v", verb, err)
		}
	}
}

// lifecycleActionTimeout bounds how long a pause/resume request waits for
// the single-owner loop to accept it. The loop's slowest branch is a tick
// (a provider sample plus a broadcast to every open tab, microseconds in
// practice), so anything approaching this bound means the loop is gone.
//
// A var rather than a const purely so a test can shorten it (see
// pause_handlers_test.go's lifecycleActionTimeoutForTest) — nothing in
// production ever assigns to it.
var lifecycleActionTimeout = 3 * time.Second
