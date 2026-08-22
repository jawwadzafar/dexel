//go:build unix

// spawn_unix.go — the unix half of the two per-OS process mechanics
// `dexel start` and `dexel stop` need (dev_docs/production-runtime/
// PLATFORM_NOTES.md §2). Build-tagged in the same shape as this repo's
// existing provider_select_{linux,darwin,other}.go trio, deliberately:
// ARCHITECTURE.md §10 rejects "a process-wide platform abstraction layer"
// in favour of exactly these small tagged files.
package main

import (
	"errors"
	"os"
	"syscall"
)

// detachAttr is what makes the spawned runtime a CHILD OF INIT rather
// than of the terminal that ran `dexel start` (PLATFORM_NOTES.md §2).
//
// Setsid puts the child in a brand-new session with NO controlling
// terminal. That is the whole mechanism: closing the terminal delivers
// SIGHUP to the foreground process group of ITS session, and after
// Setsid the runtime is not in that session at all, so the hangup cannot
// reach it. Combined with the parent never calling Wait() (and calling
// Process.Release() instead), the child is reparented to init/launchd the
// moment `dexel start` exits.
//
// There is deliberately no Chdir("/"): the runtime opens nothing by
// relative path (EMBED-1 removed the last one — app/embed.go), and
// keeping the cwd is what lets a dev-mode `-public ./public` override
// still resolve if someone passes one through `dexel start`.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// signalStop asks the runtime to shut down gracefully. On unix that is
// SIGTERM, which app/main.go's existing
// `signal.Notify(sigCh, SIGINT, SIGTERM)` handler already answers by
// saving state, stopping the provider, and shutting the HTTP server down
// with a 5s grace — i.e. `dexel stop` has a correct primitive on day one
// (ARCHITECTURE.md §1.3), with no new shutdown path to keep in sync.
//
// NOW vs PR-4: PLATFORM_NOTES.md §2 makes POST /api/lifecycle/stop the
// PREFERRED path and this signal the FALLBACK. That endpoint is PR-4's
// (MIGRATION_PLAN.md §PR-4 edits cmd_lifecycle.go to "call the endpoints
// instead of poking signals"), so in PR-3 the fallback IS the path. The
// observable behaviour is identical either way, because the endpoint's
// handler is specified to reuse the same `case <-sigCh:` body verbatim.
func signalStop(p *os.Process) error {
	return p.Signal(syscall.SIGTERM)
}

// signalKill is the escalation after the grace period: SIGKILL. Its
// caller says so loudly, because an autosave-bounded loss of up to 30s of
// progress is the cost (PLATFORM_NOTES.md §2).
func signalKill(p *os.Process) error {
	return p.Signal(syscall.SIGKILL)
}

// processAlive reports whether pid still exists, via the standard
// signal-0 probe: ESRCH means "no such process", EPERM means "it exists
// but is not ours to signal" — which is still alive.
//
// This is a SECONDARY check only, never the authority on "is dexel
// running": pids are recycled, which is exactly why ARCHITECTURE.md
// Decision 6 makes the HTTP round-trip the authority. It is used solely
// to watch a pid we have just signalled disappear, inside a bounded few
// seconds, where reuse is not a realistic concern.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}
