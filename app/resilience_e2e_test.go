package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/jawwadzafar/dexel/app/internal/lifecycle"
)

// resilience_e2e_test.go drives docs/plan/BUGS-RESILIENCE.md R6 and R9
// against a REAL runtime on a throwaway DEXEL_HOME, started THE WAY A
// SUPERVISOR STARTS IT — `dexel runtime`, exec'd directly, with no CLI
// anywhere in the picture — because that is precisely the path both bugs
// lived on: the systemd unit's `ExecStart=%s runtime` and the launchd
// plist's `ProgramArguments = [<exe>, runtime]` never touch `dexel
// start`, so nothing `start` does on the way through was ever happening
// for a real user's dexel.
//
// It reuses pause_e2e_test.go's e2eDexel harness (one built binary, one
// isolated DEXEL_HOME) and adds the one thing that harness had no reason
// to model: a process this test can SIGKILL and re-exec itself, which is
// what `Restart=on-failure` and `KeepAlive{SuccessfulExit:false}` do.

// supervisedRuntime is one `dexel runtime` process under this test's
// supervision, with its stdout+stderr captured to a file OUTSIDE the
// state directory. That capture is not incidental: it puts the child in
// the systemd shape (fd 1/2 are not the log file, so the runtime tees its
// log output to both) rather than the `dexel start` shape (fd 1/2 ARE the
// log file). Both shapes are exercised — see the two rotation tests.
type supervisedRuntime struct {
	t     *testing.T
	cmd   *exec.Cmd
	stdio string
}

// supervise execs `dexel runtime` with the fake provider, exactly as a
// unit file would, plus whatever extra environment the test needs.
func (d *e2eDexel) supervise(env ...string) *supervisedRuntime {
	d.t.Helper()
	stdio := filepath.Join(filepath.Dir(d.home), "supervisor-stdio.log")
	f, err := os.OpenFile(stdio, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		d.t.Fatalf("open supervisor stdio: %v", err)
	}
	defer func() { _ = f.Close() }()

	cmd := exec.Command(d.bin, "runtime", "-provider", "fake", "-fake-script", "type:1h")
	cmd.Env = append(append(os.Environ(), "DEXEL_HOME="+d.home), env...)
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Start(); err != nil {
		d.t.Fatalf("exec `dexel runtime`: %v", err)
	}
	s := &supervisedRuntime{t: d.t, cmd: cmd, stdio: stdio}
	d.t.Cleanup(s.kill)
	return s
}

// kill is SIGKILL, not SIGTERM: a crash, not a clean shutdown. That
// matters — a clean exit removes runtime.json and would let a "sticky"
// port look sticky for the wrong reason.
func (s *supervisedRuntime) kill() {
	s.t.Helper()
	if s.cmd.Process == nil || s.cmd.ProcessState != nil {
		return
	}
	_ = s.cmd.Process.Kill()
	_, _ = s.cmd.Process.Wait()
}

// stdioText is everything the supervisor saw on the child's stdout+stderr.
func (s *supervisedRuntime) stdioText() string {
	s.t.Helper()
	data, err := os.ReadFile(s.stdio)
	if err != nil {
		s.t.Fatalf("read supervisor stdio: %v", err)
	}
	return string(data)
}

// awaitRuntime polls for a runtime.json that a live round-trip confirms —
// the project's own definition of "there is a runtime to talk to"
// (ARCHITECTURE.md Decision 6, lifecycle.Probe).
func (d *e2eDexel) awaitRuntime(within time.Duration) lifecycle.Runtime {
	d.t.Helper()
	deadline := time.Now().Add(within)
	var lastErr error
	for time.Now().Before(deadline) {
		rt, err := lifecycle.ReadRuntime(d.home)
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if _, err = lifecycle.Probe(d.client, rt); err == nil {
			return rt
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	d.t.Fatalf("no live runtime within %s (last error: %v)", within, lastErr)
	return lifecycle.Runtime{}
}

// dialWS opens a WebSocket exactly as a browser page does — ws:// against
// the URL the runtime published — and returns it open, so a test can hold
// a connection across a crash.
func (d *e2eDexel) dialWS(url string) (*websocket.Conn, context.CancelFunc) {
	d.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(url, "http")+"/ws", nil)
	if err != nil {
		cancel()
		d.t.Fatalf("dial %s: %v", url, err)
	}
	return c, cancel
}

// awaitStateFrame reads until a `state` message arrives — the frame the
// frontend needs before it hides its connection overlay, so "a page
// pointed here works" means exactly this.
func (d *e2eDexel) awaitStateFrame(c *websocket.Conn) {
	d.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for i := 0; i < 20; i++ {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, c, &raw); err != nil {
			d.t.Fatalf("read ws message: %v", err)
		}
		var probe struct{ Type string }
		if err := json.Unmarshal(raw, &probe); err != nil {
			d.t.Fatalf("decode ws message type: %v", err)
		}
		if probe.Type == "state" {
			return
		}
	}
	d.t.Fatal("no `state` frame within 20 messages")
}

// TestE2ESupervisedRestartComesBackOnTheSamePort is R9's exit criterion,
// staged as the bug reported it:
//
//	a supervisor execs `dexel runtime`   (systemd/launchd, not `start`)
//	a page opens a WebSocket to it       (a browser tab, or the Tauri window)
//	the runtime is SIGKILLed             (the crash a multi-day uptime makes real)
//	the supervisor execs it again        (Restart=on-failure / KeepAlive)
//	-> the new runtime answers on THE SAME PORT, and the page reconnects
//	   to the URL it already has, with nobody re-pointing it
//
// Before the fix the restarted runtime took a fresh ephemeral port and
// every open window was a permanently dead grey box.
func TestE2ESupervisedRestartComesBackOnTheSamePort(t *testing.T) {
	d := newE2EDexel(t)

	first := d.supervise()
	rt := d.awaitRuntime(20 * time.Second)
	if rt.Port == 0 {
		t.Fatal("runtime.json reports port 0")
	}

	// A page is open and live BEFORE the crash — otherwise "it
	// reconnects" would be a claim about a connection that never existed.
	page, cancel := d.dialWS(rt.URL)
	d.awaitStateFrame(page)

	// The record must exist while the runtime is up: it is written right
	// after the bind, not on the way out, because a crash has no way out.
	recorded, err := lifecycle.ReadStickyPort(d.home)
	if err != nil {
		t.Fatalf("ReadStickyPort while running: %v", err)
	}
	if recorded != rt.Port {
		t.Fatalf("%s records %d, runtime.json says %d", lifecycle.PortFileName, recorded, rt.Port)
	}

	// ---- the crash -----------------------------------------------------
	first.kill()
	// The open page notices, exactly as the browser's WebSocket does.
	readCtx, readCancel := context.WithTimeout(context.Background(), 15*time.Second)
	var raw json.RawMessage
	for {
		if err := wsjson.Read(readCtx, page, &raw); err != nil {
			break
		}
	}
	readCancel()
	cancel()
	_ = page.Close(websocket.StatusGoingAway, "")

	// A crash leaves runtime.json behind on purpose (ARCHITECTURE.md
	// Decision 6). Remove nothing: the restarted runtime has to cope with
	// exactly what the crash left, which is the real situation.

	// ---- the supervisor's restart ---------------------------------------
	second := d.supervise()
	rt2 := d.awaitRuntime(20 * time.Second)

	if rt2.Pid == rt.Pid {
		t.Fatalf("runtime.json still names the dead pid %d — the restart did not take", rt.Pid)
	}
	if rt2.Port != rt.Port {
		t.Fatalf("the restarted runtime bound port %d, want the same %d it crashed on\nsupervisor saw:\n%s",
			rt2.Port, rt.Port, second.stdioText())
	}
	if rt2.URL != rt.URL {
		t.Fatalf("URL changed across the restart: %s -> %s", rt.URL, rt2.URL)
	}

	// THE claim: the URL the dead page is still retrying now works, with
	// nothing having re-pointed it.
	revived, revivedCancel := d.dialWS(rt.URL)
	defer revivedCancel()
	defer func() { _ = revived.Close(websocket.StatusNormalClosure, "") }()
	d.awaitStateFrame(revived)

	if !strings.Contains(second.stdioText(), "sticky port: rebound") {
		t.Errorf("the restarted runtime did not say it rebound a sticky port:\n%s", second.stdioText())
	}
}

// TestE2ERestartCommandKeepsThePort is the same guarantee for the CLI's
// own escape hatch. `dexel restart` is a CLEAN stop followed by a start,
// so runtime.json is gone by the time the new runtime binds — which is
// exactly why the port record is a separate file that survives a clean
// exit rather than a re-read of runtime.json.
func TestE2ERestartCommandKeepsThePort(t *testing.T) {
	d := newE2EDexel(t)
	d.mustRun("start", "-provider", "fake", "-fake-script", "type:1h")
	before := d.runtimeFile()

	d.mustRun("restart", "-provider", "fake", "-fake-script", "type:1h")
	after := d.runtimeFile()

	if after.Pid == before.Pid {
		t.Fatalf("`restart` left pid %d running — it did not restart anything", before.Pid)
	}
	if after.Port != before.Port {
		t.Fatalf("`restart` moved the runtime from port %d to %d; an open window would be dead", before.Port, after.Port)
	}
}

// TestE2ESupervisedRuntimeRotatesItsOwnLog is R6 under systemd's shape:
// the supervisor owns fd 1/2 (there they are the journal, here a captured
// file), so the log FILE has nobody but the runtime writing to it — and
// the runtime, which no `dexel start` was ever run for, has to rotate it
// itself.
//
// The cap comes from DEXEL_MAX_LOG_BYTES so this takes milliseconds
// instead of 8 MiB of lines. Note what makes the assertion airtight:
// that override is read ONLY by the runtime (lifecycle.RotationThreshold,
// called from attachRuntimeLog). `dexel start`'s one-shot RotateLog uses
// the 8 MiB constant, and no `start` runs here anyway — so a
// runtime.log.1 next to a fresh, tiny runtime.log can only be the
// runtime's own doing.
func TestE2ESupervisedRuntimeRotatesItsOwnLog(t *testing.T) {
	d := newE2EDexel(t)
	const capBytes = 512
	s := d.supervise(fmt.Sprintf("%s=%d", lifecycle.MaxLogBytesEnv, capBytes))
	d.awaitRuntime(20 * time.Second)

	logPath := lifecycle.LogPath(filepath.Join(d.home, "logs"))
	deadline := time.Now().Add(15 * time.Second)
	var rotated bool
	for time.Now().Before(deadline) {
		if _, err := os.Stat(logPath + ".1"); err == nil {
			rotated = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !rotated {
		t.Fatalf("no %s.1 — the supervised runtime never rotated its own log.\nlog file:\n%s\nsupervisor stdio:\n%s",
			logPath, tailFile(t, logPath), s.stdioText())
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat %s: %v", logPath, err)
	}
	// One line may exceed the cap on its own (a line is never split), so
	// the honest bound is "a small multiple of the cap", not the cap.
	if info.Size() > 4*capBytes {
		t.Fatalf("runtime.log is %d bytes with a %d-byte cap", info.Size(), capBytes)
	}

	// The other half of the systemd truth: BOTH destinations got the
	// lines. Before this fix the unit's own comment claimed "the runtime
	// writes its own log file; journald gets it too" while in fact
	// nothing but journald ever saw a line, and `dexel logs` was empty
	// forever on a systemd box.
	if out := d.mustRun("logs", "-n", "5"); strings.TrimSpace(out) == "" {
		t.Error("`dexel logs` shows nothing on the supervised path — the log file got no lines at all")
	}
	if !strings.Contains(s.stdioText(), "dexel listening on") {
		t.Errorf("the supervisor's own stdout/stderr got nothing:\n%s", s.stdioText())
	}
}

// TestE2EStartedRuntimeRotatesItsOwnLog is R6 under the OTHER shape: the
// CLI opened the log file and handed it to the child as fd 1 AND fd 2, so
// the runtime must NOT tee (that would double every line) and must still
// rotate. `start` itself cannot have done the rotating — its RotateLog
// runs once, before the child exists, against a log that is empty here,
// and it does not read DEXEL_MAX_LOG_BYTES at all.
func TestE2EStartedRuntimeRotatesItsOwnLog(t *testing.T) {
	d := newE2EDexel(t)
	logPath := lifecycle.LogPath(filepath.Join(d.home, "logs"))

	cmd := exec.Command(d.bin, "start", "-provider", "fake", "-fake-script", "type:1h")
	cmd.Env = append(os.Environ(), "DEXEL_HOME="+d.home, lifecycle.MaxLogBytesEnv+"=512")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dexel start: %v\n%s", err, out)
	}
	t.Cleanup(func() { _, _, _ = d.run("stop") })

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(logPath + ".1"); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Fatalf("no %s.1 — a detached runtime did not rotate its own log:\n%s", logPath, tailFile(t, logPath))
	}

	// No line may appear twice: fd 2 already WAS this file, so a tee here
	// would duplicate everything the runtime logs.
	body := tailFile(t, logPath) + tailFile(t, logPath+".1")
	const marker = "dexel listening on"
	if n := strings.Count(body, marker); n > 1 {
		t.Fatalf("%q appears %d times across the two log files — the runtime teed into a file that was already its stderr:\n%s", marker, n, body)
	}
}

func tailFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	if len(data) > 4096 {
		data = data[len(data)-4096:]
	}
	return string(data)
}
