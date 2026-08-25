package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/lifecycle"
	"github.com/jawwadzafar/dexel/app/internal/paths"
)

// resilience_test.go covers the two things a SUPERVISED runtime now does
// for itself, because the systemd unit and the launchd agent both exec
// `dexel runtime` and never `dexel start` (docs/plan/BUGS-RESILIENCE.md
// R6 and R9):
//
//	attachRuntimeLog  rotates the log in-process (R6)
//	listenRuntime     rebinds the port the last runtime here used (R9)
//
// The end-to-end proof — a real detached runtime, SIGKILLed and restarted
// the way a supervisor restarts it — is in resilience_e2e_test.go. These
// are the fast, deterministic halves.

// freePort binds :0, reads the port the OS assigned and gives it back.
// It is the standard "a port that was free a moment ago" trick, and the
// standard caveat applies: another process could claim it in between. It
// is used only where the alternative would be to hardcode a port number.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close probe listener: %v", err)
	}
	return port
}

func listenPort(t *testing.T, ln net.Listener) int {
	t.Helper()
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %s is not TCP", ln.Addr())
	}
	return tcp.Port
}

// TestListenRuntimeReusesTheRecordedPort is R9's happy path: the port the
// previous runtime in this state directory bound is still free, so the
// new one lands on it and every already-open page reconnects by itself.
func TestListenRuntimeReusesTheRecordedPort(t *testing.T) {
	dir := t.TempDir()
	want := freePort(t)
	if err := lifecycle.WriteStickyPort(dir, want); err != nil {
		t.Fatalf("WriteStickyPort: %v", err)
	}

	ln, err := listenRuntime(modeRuntime, dir, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listenRuntime: %v", err)
	}
	defer func() { _ = ln.Close() }()

	if got := listenPort(t, ln); got != want {
		t.Fatalf("listenRuntime bound port %d, want the recorded %d", got, want)
	}
}

// TestListenRuntimeFallsBackWhenTheRecordedPortIsTaken is the case that
// makes stickiness SAFE: somebody else holds the port, so the runtime
// takes an OS-assigned one instead of failing to start — and then records
// what it actually got, so the NEXT restart is sticky again.
func TestListenRuntimeFallsBackWhenTheRecordedPortIsTaken(t *testing.T) {
	dir := t.TempDir()
	squatter, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("squatter listen: %v", err)
	}
	defer func() { _ = squatter.Close() }()
	taken := listenPort(t, squatter)
	if err := lifecycle.WriteStickyPort(dir, taken); err != nil {
		t.Fatalf("WriteStickyPort: %v", err)
	}

	ln, err := listenRuntime(modeRuntime, dir, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listenRuntime: %v", err)
	}
	defer func() { _ = ln.Close() }()
	got := listenPort(t, ln)
	if got == taken {
		t.Fatalf("listenRuntime bound %d, the port the squatter holds", got)
	}

	// The record must now name the port we really have, or the next
	// restart would keep chasing a port somebody else owns.
	recordStickyPort(dir, ln)
	recorded, err := lifecycle.ReadStickyPort(dir)
	if err != nil {
		t.Fatalf("ReadStickyPort: %v", err)
	}
	if recorded != got {
		t.Fatalf("record says %d, listener is on %d", recorded, got)
	}
}

// TestListenRuntimeSurvivesACorruptRecord: a hand-mangled record is a
// hint we cannot use, not a reason to refuse to start.
func TestListenRuntimeSurvivesACorruptRecord(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(lifecycle.PortFilePath(dir), []byte("port: eighty\n"), 0o600); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	ln, err := listenRuntime(modeRuntime, dir, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listenRuntime with a corrupt record: %v", err)
	}
	defer func() { _ = ln.Close() }()
	if listenPort(t, ln) == 0 {
		t.Fatal("listenRuntime bound port 0")
	}
}

// TestListenRuntimeNeverOverridesAnExplicitAddr: `-addr` means what it
// says. A record pointing somewhere else must not move the listener.
func TestListenRuntimeNeverOverridesAnExplicitAddr(t *testing.T) {
	dir := t.TempDir()
	recorded := freePort(t)
	if err := lifecycle.WriteStickyPort(dir, recorded); err != nil {
		t.Fatalf("WriteStickyPort: %v", err)
	}
	asked := freePort(t)
	if asked == recorded {
		t.Skip("the two probe ports collided; nothing to distinguish")
	}

	ln, err := listenRuntime(modeRuntime, dir, fmt.Sprintf("127.0.0.1:%d", asked))
	if err != nil {
		t.Fatalf("listenRuntime: %v", err)
	}
	defer func() { _ = ln.Close() }()
	if got := listenPort(t, ln); got != asked {
		t.Fatalf("listenRuntime bound %d, want the explicitly requested %d", got, asked)
	}
}

// TestListenRuntimeIgnoresTheRecordOutsideRuntimeMode: `dexel serve` and
// the legacy foreground shape take no lock and write no runtime.json
// (ARCHITECTURE.md Decision 7), and they must not inherit a real
// runtime's port either — a scratch instance stealing the port a
// supervisor's runtime is about to want back is exactly the fight
// Decision 7 avoids.
//
// If the mode gate were missing this would bind `recorded` exactly, so a
// mismatch is a real signal; the only false-failure is the OS handing out
// that one port out of ~28000 by chance.
func TestListenRuntimeIgnoresTheRecordOutsideRuntimeMode(t *testing.T) {
	dir := t.TempDir()
	recorded := freePort(t)
	if err := lifecycle.WriteStickyPort(dir, recorded); err != nil {
		t.Fatalf("WriteStickyPort: %v", err)
	}
	for _, mode := range []serveMode{modeServe, modeLegacy} {
		ln, err := listenRuntime(mode, dir, "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listenRuntime(%v): %v", mode, err)
		}
		got := listenPort(t, ln)
		_ = ln.Close()
		if got == recorded {
			t.Fatalf("mode %v reused the recorded port %d", mode, recorded)
		}
	}
}

// TestListenRuntimeReportsABindFailureAsItAlwaysDid pins the legacy
// message shape: the foreground shape's output is a compatibility
// contract (see serveMode's doc comment), so the error text for a port
// that is genuinely taken must still read "listen on <addr>: ...".
func TestListenRuntimeReportsABindFailureAsItAlwaysDid(t *testing.T) {
	squatter, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("squatter listen: %v", err)
	}
	defer func() { _ = squatter.Close() }()
	addr := squatter.Addr().String()

	if _, err = listenRuntime(modeLegacy, "", addr); err == nil {
		t.Fatal("listenRuntime on an occupied explicit port succeeded")
	}
	if !strings.HasPrefix(err.Error(), "listen on "+addr+": ") {
		t.Fatalf("error is %q, want it to start with %q", err, "listen on "+addr+": ")
	}
}

// TestAttachRuntimeLogRotatesInProcess is R6's core claim: a runtime that
// nobody ever runs `dexel start` for still keeps its own log under the
// cap. No CLI is involved here at all — this is the function the
// `runtime` entry point calls, driven with a tiny cap.
func TestAttachRuntimeLogRotatesInProcess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEXEL_HOME", home)
	t.Setenv(lifecycle.MaxLogBytesEnv, "2048")
	logPath, err := paths.LogFile()
	if err != nil {
		t.Fatalf("resolve log path: %v", err)
	}

	// log's output is process-global; put it back whatever happens.
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	closeLog := attachRuntimeLog()
	defer closeLog()

	for i := 0; i < 200; i++ {
		log.Printf("a line of the kind a long-running runtime writes on a bad day: %d", i)
	}

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat %s: %v", logPath, err)
	}
	if info.Size() > 4096 {
		t.Fatalf("runtime.log is %d bytes with a 2048-byte cap — nothing rotated it", info.Size())
	}
	prev, err := os.Stat(logPath + ".1")
	if err != nil {
		t.Fatalf("stat %s.1: %v — the runtime never rotated", logPath, err)
	}
	if prev.Size() == 0 {
		t.Fatal("runtime.log.1 is empty")
	}
	// And the rotation kept exactly two files, as PLATFORM_NOTES.md §4
	// specifies.
	if _, err := os.Stat(logPath + ".2"); !os.IsNotExist(err) {
		t.Fatalf("runtime.log.2 exists (stat err %v)", err)
	}
}

// TestAttachRuntimeLogSurvivesAnUnwritableLogDir: logging that cannot be
// set up must leave logging exactly where it was, never abort the
// runtime. A dexel that refuses to observe activity because it could not
// open a log file would be a far worse bug than an unrotated log.
func TestAttachRuntimeLogSurvivesAnUnwritableLogDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write into a 0500 directory")
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
	t.Setenv("DEXEL_HOME", home)

	var captured strings.Builder
	log.SetOutput(&captured)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	closeLog := attachRuntimeLog()
	closeLog()
	if !strings.Contains(captured.String(), "inherited stderr") {
		t.Fatalf("attachRuntimeLog said %q; want it to report that logging stayed where it was", captured.String())
	}
	// The output it could not replace must still be the one we set.
	log.Print("still audible")
	if !strings.Contains(captured.String(), "still audible") {
		t.Fatal("attachRuntimeLog redirected log output despite failing to open a file")
	}
}
