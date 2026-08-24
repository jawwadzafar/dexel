//go:build windows

// spawn_windows.go — the Windows half of the per-OS process mechanics
// (docs/production-runtime/PLATFORM_NOTES.md §2). Authored against
// the documented flags and unit-tested only for what a non-Windows host
// can check; honestly marked unverified-on-hardware in the same style as
// desktop/README.md, because this repo has no Windows runner
// (PLATFORM_NOTES.md §0's support matrix puts windows at tier 2).
package main

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// detachAttr denies the child every tie to the parent console
// (PLATFORM_NOTES.md §2):
//
//	DETACHED_PROCESS         — the child gets no console at all, so it does
//	                           not die with the one that launched it
//	CREATE_NEW_PROCESS_GROUP — a Ctrl-C in the parent console cannot reach it
//	CREATE_NO_WINDOW         — no console window flashes into existence
//
// Constants come from golang.org/x/sys/windows, which app/go.mod already
// carried as an indirect dependency, so this adds no module to the build.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS |
			windows.CREATE_NEW_PROCESS_GROUP |
			windows.CREATE_NO_WINDOW,
		HideWindow: true,
	}
}

// signalStop: there is no SIGTERM on Windows. PLATFORM_NOTES.md §2 is
// explicit that "the lifecycle endpoint IS the graceful path and the only
// one; fallback is TerminateProcess via os.Process.Kill(), with the same
// loud warning" — and that this asymmetry "is exactly why `stop` is an
// HTTP call first and a signal second".
//
// So in PR-3, on Windows, `dexel stop` is an ungraceful kill and says so
// out loud (see cmdStop's warning): the 30s autosave bounds the loss, the
// same bound ADR 0015 already accepted. PR-4's POST /api/lifecycle/stop
// is what makes Windows graceful, and it is the reason that endpoint is
// on the critical path rather than a nicety.
func signalStop(p *os.Process) error {
	return p.Kill()
}

// signalKill is the same call as signalStop here — TerminateProcess is
// already the harshest thing available, so the escalation is a no-op
// beyond the warning the caller prints.
func signalKill(p *os.Process) error {
	return p.Kill()
}

// processAlive reports whether pid still exists, by opening a handle to
// it and asking for its exit code. STILL_ACTIVE (259) means running.
//
// As on unix this is a SECONDARY check used only to watch a pid we just
// signalled disappear — never the authority on "is dexel running", which
// is the HTTP round-trip (ARCHITECTURE.md Decision 6).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		// The handle opened, so something with that pid exists; failing
		// to read its exit code is not evidence of death.
		return true
	}
	const stillActive = 259
	return code == stillActive
}
