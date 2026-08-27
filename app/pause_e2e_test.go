package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/jawwadzafar/dexel/app/internal/game"
	"github.com/jawwadzafar/dexel/app/internal/lifecycle"
)

// pause_e2e_test.go drives PR-5 end to end against a REAL detached
// runtime on a throwaway DEXEL_HOME: the actual binary, the actual CLI
// verbs, the actual HTTP endpoints, the actual WebSocket wire.
//
// It exists because every unit test in this PR proves a piece in
// isolation, and the claim PR-5 actually makes is a composite one: pause
// the running product and the numbers STOP — not "the accrual function
// returns early". The fake provider is deliberately scripted to type
// continuously for an hour, so at every point where this test asserts
// "nothing accrued" the provider would have been producing a steady
// keystroke stream had anyone been sampling it. Nothing here mocks the
// thing under test.
//
// It is skipped under -short (it builds a binary and spends ~15s of real
// wall-clock waiting for 1Hz ticks) and on Windows (the detached-spawn
// and process-lookup paths there are unverified on this host, exactly as
// desktop/README.md says).

// e2eDexel builds the binary once and returns a runner bound to a fresh
// DEXEL_HOME, so state, config, logs and runtime.json are all isolated
// from any real dexel on this machine (PR-1's DEXEL_HOME contract).
type e2eDexel struct {
	t      *testing.T
	bin    string
	home   string
	client *http.Client
}

func newE2EDexel(t *testing.T) *e2eDexel {
	t.Helper()
	if testing.Short() {
		t.Skip("e2e: builds a binary and waits on real 1Hz ticks")
	}
	if runtime.GOOS == "windows" {
		t.Skip("e2e: detached spawn / process lookup unverified on windows")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "dexel")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	d := &e2eDexel{
		t:      t,
		bin:    bin,
		home:   filepath.Join(dir, "home"),
		client: lifecycle.ProbeClient(5 * time.Second),
	}
	if err := os.MkdirAll(d.home, 0o700); err != nil {
		t.Fatalf("mkdir DEXEL_HOME: %v", err)
	}
	t.Cleanup(func() {
		// Never leave a runtime behind, even on a failed assertion.
		_, _, _ = d.run("stop")
	})
	return d
}

// run executes one CLI verb against this test's DEXEL_HOME.
func (d *e2eDexel) run(args ...string) (stdout, stderr string, code int) {
	d.t.Helper()
	cmd := exec.Command(d.bin, args...)
	cmd.Env = append(os.Environ(), "DEXEL_HOME="+d.home)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code = 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		d.t.Fatalf("run %v: %v", args, err)
	}
	return out.String(), errb.String(), code
}

// mustRun fails the test unless the verb exits 0.
func (d *e2eDexel) mustRun(args ...string) string {
	d.t.Helper()
	stdout, stderr, code := d.run(args...)
	if code != 0 {
		d.t.Fatalf("dexel %v exited %d\nstdout: %s\nstderr: %s", args, code, stdout, stderr)
	}
	return stdout
}

// runtimeFile reads the discovery file the detached runtime wrote — the
// only place its port and capability token exist.
func (d *e2eDexel) runtimeFile() lifecycle.Runtime {
	d.t.Helper()
	rt, err := lifecycle.ReadRuntime(d.home)
	if err != nil {
		d.t.Fatalf("read runtime.json: %v", err)
	}
	return rt
}

// statusJSON runs `dexel status --json` and decodes it.
func (d *e2eDexel) statusJSON() statusJSON {
	d.t.Helper()
	out := d.mustRun("status", "--json")
	var st statusJSON
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		d.t.Fatalf("decode status --json %q: %v", out, err)
	}
	return st
}

// state opens a WebSocket, reads messages until a `state` arrives, and
// returns it — the exact payload a browser tab receives, which is the
// only place the frozen-counter claim can honestly be checked.
func (d *e2eDexel) state(rt lifecycle.Runtime) game.StateMessage {
	d.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(rt.URL, "http") + "/ws"
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		d.t.Fatalf("dial %s: %v", url, err)
	}
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()
	for i := 0; i < 10; i++ {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, c, &raw); err != nil {
			d.t.Fatalf("read ws message: %v", err)
		}
		var probe struct{ Type string }
		if err := json.Unmarshal(raw, &probe); err != nil {
			d.t.Fatalf("decode ws message type: %v", err)
		}
		if probe.Type != "state" {
			continue
		}
		var st game.StateMessage
		if err := json.Unmarshal(raw, &st); err != nil {
			d.t.Fatalf("decode state: %v", err)
		}
		return st
	}
	d.t.Fatal("no `state` message arrived within 10 messages")
	return game.StateMessage{}
}

// post is a raw HTTP POST to a lifecycle endpoint with an explicit token,
// used to prove the 401/403/200 discipline against the real server.
func (d *e2eDexel) post(rt lifecycle.Runtime, path, token string) (int, string) {
	d.t.Helper()
	req, err := http.NewRequest(http.MethodPost, rt.URL+path, nil)
	if err != nil {
		d.t.Fatalf("build POST %s: %v", path, err)
	}
	if token != "" {
		req.Header.Set(lifecycle.TokenHeader, token)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		d.t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, string(body)
}

// TestE2EPauseStopsTheNumbersAndSurvivesARestart is §PR-5's in-product
// criterion, minus the pixels (the frontend is a separate follow-up):
//
//	start -> the numbers move
//	pause via the CLI -> the fake provider keeps typing and NOTHING moves
//	                     (every observed counter frozen; paused:true on
//	                      the wire; pausedSeconds is the only thing
//	                      climbing)
//	resume -> the numbers move again, from a clean slate
//	restart while paused -> still paused (FORK D)
func TestE2EPauseStopsTheNumbersAndSurvivesARestart(t *testing.T) {
	d := newE2EDexel(t)

	// A provider that types, without interruption, for an hour: whenever
	// this test asserts "frozen", the only reason nothing moved is that
	// dexel stopped looking.
	const script = "type:1h"
	d.mustRun("start", "-provider", "fake", "-fake-script", script)
	rt := d.runtimeFile()

	if st := d.statusJSON(); !st.Running || st.Paused {
		t.Fatalf("status right after start = %+v, want running and not paused", st)
	}

	// ---- Phase 1: it really is accruing --------------------------------
	first := d.state(rt)
	if first.Paused {
		t.Fatal("paused:true on the wire immediately after a fresh start")
	}
	time.Sleep(3 * time.Second)
	running := d.state(rt)
	if running.Stats.Lifetime.Keystrokes <= first.Stats.Lifetime.Keystrokes {
		t.Fatalf("keystrokes did not climb while running (%d -> %d) — the fake provider is not producing signal, so the frozen-counter assertions below would prove nothing",
			first.Stats.Lifetime.Keystrokes, running.Stats.Lifetime.Keystrokes)
	}
	if running.Stats.Lifetime.PausedSeconds != 0 {
		t.Errorf("pausedSeconds = %d while never paused, want 0", running.Stats.Lifetime.PausedSeconds)
	}

	// ---- Phase 2: the endpoint's own discipline ------------------------
	// Proven against the REAL server, on the real route, before the CLI
	// is trusted to have used it correctly.
	if code, body := d.post(rt, "/api/lifecycle/pause", ""); code != http.StatusUnauthorized {
		t.Errorf("POST /api/lifecycle/pause with no token = %d (%q), want 401", code, body)
	}
	if code, body := d.post(rt, "/api/lifecycle/pause", strings.Repeat("f", len(rt.Token))); code != http.StatusForbidden {
		t.Errorf("POST /api/lifecycle/pause with a wrong token = %d (%q), want 403", code, body)
	}
	if st := d.state(rt); st.Paused {
		t.Fatal("a REFUSED pause request paused the runtime anyway")
	}

	// ---- Phase 3: pause via the CLI ------------------------------------
	out := d.mustRun("pause")
	if !strings.Contains(out, "PAUSED") {
		t.Errorf("`dexel pause` said %q, want it to mention PAUSED", out)
	}
	paused := d.state(rt)
	if !paused.Paused {
		t.Fatal("paused:false on the wire after `dexel pause`")
	}
	if paused.ActiveState != "idle" {
		t.Errorf("activeState = %q while paused, want idle (no fourth mood)", paused.ActiveState)
	}
	if st := d.statusJSON(); !st.Paused {
		t.Error("`dexel status --json` does not report paused after a pause — a paused dexel must never be mute")
	}
	if out := d.mustRun("status"); !strings.Contains(out, "PAUSED") {
		t.Errorf("`dexel status` text output does not mention PAUSED:\n%s", out)
	}

	// The claim: five seconds of continuous simulated typing, and not one
	// observed counter moves.
	time.Sleep(5 * time.Second)
	stillPaused := d.state(rt)
	if !stillPaused.Paused {
		t.Fatal("the runtime un-paused itself")
	}
	frozen := []struct {
		name      string
		got, want uint64
	}{
		{"keystrokes", stillPaused.Stats.Lifetime.Keystrokes, paused.Stats.Lifetime.Keystrokes},
		{"mouseActiveSeconds", stillPaused.Stats.Lifetime.MouseActiveSeconds, paused.Stats.Lifetime.MouseActiveSeconds},
		{"activeSeconds", stillPaused.Stats.Lifetime.ActiveSeconds, paused.Stats.Lifetime.ActiveSeconds},
		{"idleSeconds", stillPaused.Stats.Lifetime.IdleSeconds, paused.Stats.Lifetime.IdleSeconds},
		{"sprintsCompleted", stillPaused.Stats.Lifetime.SprintsCompleted, paused.Stats.Lifetime.SprintsCompleted},
		{"focusSessions", stillPaused.Stats.Lifetime.FocusSessions, paused.Stats.Lifetime.FocusSessions},
		{"devCash", stillPaused.DevCash, paused.DevCash},
		{"xp", stillPaused.XP, paused.XP},
	}
	for _, f := range frozen {
		if f.got != f.want {
			t.Errorf("%s moved across a pause: %d -> %d — the provider was supposed to be STOPPED", f.name, f.want, f.got)
		}
	}
	if stillPaused.Sprint.Progress != paused.Sprint.Progress {
		t.Errorf("sprint progress moved across a pause: %v -> %v", paused.Sprint.Progress, stillPaused.Sprint.Progress)
	}
	// ...and the one counter that MUST move, so the paused seconds are
	// accounted for rather than vanishing.
	if stillPaused.Stats.Lifetime.PausedSeconds <= paused.Stats.Lifetime.PausedSeconds {
		t.Errorf("pausedSeconds did not climb while paused (%d -> %d) — paused time must be recorded, not dropped",
			paused.Stats.Lifetime.PausedSeconds, stillPaused.Stats.Lifetime.PausedSeconds)
	}
	if stillPaused.Stats.Lifetime.IdleSeconds != paused.Stats.Lifetime.IdleSeconds {
		t.Error("idleSeconds absorbed paused time — paused is not idle")
	}

	// ---- Phase 4: restart while paused, still paused (FORK D) ---------
	d.mustRun("stop")
	d.mustRun("start", "-provider", "fake", "-fake-script", script)
	rt = d.runtimeFile()
	if st := d.statusJSON(); !st.Running || !st.Paused {
		t.Fatalf("status after restarting a paused dexel = %+v, want running AND paused (FORK D)", st)
	}
	afterRestart := d.state(rt)
	if !afterRestart.Paused {
		t.Fatal("a paused dexel came back unpaused — SaveData.Paused did not survive the restart")
	}
	// The startup log has to SAY so, or a paused-and-forgotten dexel is
	// mute (ARCHITECTURE.md Decision 16).
	logOut := d.mustRun("logs", "-n", "200")
	if !strings.Contains(logOut, "PAUSED") {
		t.Errorf("the runtime log does not announce the paused startup:\n%s", logOut)
	}
	// And it must still not be accruing after the restart.
	time.Sleep(3 * time.Second)
	stillPausedAfterRestart := d.state(rt)
	if stillPausedAfterRestart.Stats.Lifetime.Keystrokes != afterRestart.Stats.Lifetime.Keystrokes {
		t.Errorf("keystrokes moved after restarting PAUSED (%d -> %d) — the provider must not be started at boot for a paused save",
			afterRestart.Stats.Lifetime.Keystrokes, stillPausedAfterRestart.Stats.Lifetime.Keystrokes)
	}

	// ---- Phase 5: resume, and accrual restarts fresh -------------------
	out = d.mustRun("resume")
	if !strings.Contains(out, "RESUMED") {
		t.Errorf("`dexel resume` said %q, want it to mention RESUMED", out)
	}
	resumed := d.state(rt)
	if resumed.Paused {
		t.Fatal("paused:true on the wire after `dexel resume`")
	}
	if st := d.statusJSON(); st.Paused {
		t.Error("`dexel status --json` still reports paused after a resume")
	}
	pausedSecondsAtResume := resumed.Stats.Lifetime.PausedSeconds
	if pausedSecondsAtResume == 0 {
		t.Error("pausedSeconds is 0 after a pause+restart+resume cycle — the paused time was lost")
	}
	time.Sleep(4 * time.Second)
	final := d.state(rt)
	if final.Stats.Lifetime.Keystrokes <= resumed.Stats.Lifetime.Keystrokes {
		t.Errorf("keystrokes did not climb after resume (%d -> %d) — accrual must resume",
			resumed.Stats.Lifetime.Keystrokes, final.Stats.Lifetime.Keystrokes)
	}
	if final.Stats.Lifetime.PausedSeconds != pausedSecondsAtResume {
		t.Errorf("pausedSeconds moved after resume (%d -> %d) — nothing may be credited as paused while running",
			pausedSecondsAtResume, final.Stats.Lifetime.PausedSeconds)
	}
	// The invariant, on real data from a real runtime, over the whole
	// life of this state.db.
	c := final.Stats.Lifetime
	if c.ActiveSeconds+c.IdleSeconds+c.PausedSeconds == 0 {
		t.Fatal("every time bucket is zero — the runtime never ticked")
	}
	t.Logf("final lifetime buckets: active=%d idle=%d paused=%d (sum %d)",
		c.ActiveSeconds, c.IdleSeconds, c.PausedSeconds, c.ActiveSeconds+c.IdleSeconds+c.PausedSeconds)
}

// TestE2EPauseRefusedWhenNothingIsRunning: `dexel pause` with no runtime
// must FAIL loudly, unlike `dexel stop` (for which "already not running"
// is success). Exiting 0 would let a user believe tracking had been
// turned off when the next `dexel start` would happily start observing.
func TestE2EPauseRefusedWhenNothingIsRunning(t *testing.T) {
	d := newE2EDexel(t)
	for _, verb := range []string{"pause", "resume"} {
		stdout, stderr, code := d.run(verb)
		if code == 0 {
			t.Errorf("`dexel %s` with no runtime exited 0 — there was nothing to %s\nstdout: %s", verb, verb, stdout)
		}
		if !strings.Contains(stderr, "not running") {
			t.Errorf("`dexel %s` with no runtime said %q, want it to say the runtime is not running", verb, stderr)
		}
	}
}
