// cmd_doctor.go — `dexel doctor`, the one-report diagnostics dump
// (a paste-this-when-reporting-an-issue command).
//
// It answers, in one clean block, every question a bug report needs and
// none it does not: what build this is, what platform it runs on, where
// state/logs/binary/config live, whether a runtime is up right now (and
// where), what this platform's capture strategy can and cannot see, and
// whether autostart is armed.
//
// CONTENT-FREE, by construction (the same boundary
// feature-build-and-verify draws for the wire and for persistence): it
// reports CAPABILITY, PATHS and STATUS only — never a keystroke count, a
// duration, a save, a config value, or anything from inside the state
// dir. `dexel status` already prints the user's preferences; `doctor` is
// the machine-facing sibling that a stranger on an issue tracker can read
// without learning anything about the person who pasted it.
//
// RESILIENT: no single failure aborts the report. A path that will not
// resolve, a runtime that will not answer, an autostart probe that errors
// — each degrades to one honest "(unavailable: ...)" line while every
// other fact still prints. The rendering is a pure function over a
// gathered struct (renderDoctor) so the whole layout is testable from a
// buffer without a runtime, a filesystem, or a terminal.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/jawwadzafar/dexel/app/internal/autostart"
	"github.com/jawwadzafar/dexel/app/internal/lifecycle"
	"github.com/jawwadzafar/dexel/app/internal/paths"
	"github.com/jawwadzafar/dexel/app/internal/store"
)

// doctorInfo is everything `dexel doctor` reports, gathered once so the
// rendering below is a pure function over it. Every string field either
// holds a resolved value or a short "(unavailable: ...)" note — never an
// empty string that would read as "this exists and is blank".
type doctorInfo struct {
	Version string
	Commit  string
	OS      string
	Arch    string

	Self       string
	StateDir   string
	LogDir     string
	LogFile    string
	CacheDir   string
	BinDir     string
	ConfigPath string

	Running bool
	Pid     int
	Port    int
	URL     string
	// RuntimeNote carries a probe failure ("could not probe: ...") when
	// the running/not-running answer itself could not be established.
	RuntimeNote string

	Provider    string // this platform's capture strategy (compile-time)
	AppIdentity string // whether app identity is observable on this platform

	Autostart       string // "OFF", or "ON via <mechanism> (active=<b>)"
	AutostartDetail string // the mechanism's own detail line, if any
}

// cmdDoctor gathers the report and prints it. It takes no flags today (a
// flag set is still parsed so `dexel doctor -h` and a stray flag behave
// like every other verb), and always exits 0: producing the report IS the
// success, exactly as `dexel status` succeeds whether or not a runtime is
// up.
func cmdDoctor(args []string) int {
	// A bare, permissive parse: reject unknown flags with the CLI-wide
	// usage exit (2), but accept -h as a successful no-op — there is
	// nothing to configure, so the command itself is its own help.
	for _, a := range args {
		switch a {
		case "-h", "-help", "--help":
			fmt.Fprintln(os.Stdout, "Usage: dexel doctor")
			fmt.Fprintln(os.Stdout, "\nPrint a diagnostics report (build, paths, runtime, capability, autostart)")
			fmt.Fprintln(os.Stdout, "to paste when reporting an issue. Content-free: no save, config or counts.")
			return 0
		default:
			if len(a) > 0 && a[0] == '-' {
				fmt.Fprintf(os.Stderr, "dexel doctor: unknown flag %q (this command takes none)\n", a)
				return 2
			}
			fmt.Fprintf(os.Stderr, "dexel doctor: unexpected argument %q (this command takes none)\n", a)
			return 2
		}
	}

	renderDoctor(os.Stdout, gatherDoctor())
	return 0
}

// gatherDoctor resolves every fact the report needs, best-effort. Each
// resolution is independent: a failure fills one field with a note and
// leaves the rest intact.
func gatherDoctor() doctorInfo {
	info := doctorInfo{
		Version:     version,
		Commit:      buildVersion(),
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Self:        orNote(os.Executable()),
		StateDir:    orNote(paths.StateDir()),
		LogDir:      orNote(paths.LogDir()),
		LogFile:     orNote(paths.LogFile()),
		CacheDir:    orNote(paths.CacheDir()),
		BinDir:      orNote(paths.BinDir()),
		ConfigPath:  orNote(store.ConfigPath()),
		Provider:    platformProviderName,
		AppIdentity: appIdentityCapability(runtime.GOOS),
	}

	// Runtime status via the READ-ONLY Query (never Discover): doctor
	// reports the world, it does not change it, so it must not delete a
	// stale runtime.json as a side effect of being run.
	if stateDir, err := paths.StateDir(); err == nil {
		client := lifecycle.ProbeClient(lifecycle.DefaultProbeTimeout)
		if st, qerr := lifecycle.Query(stateDir, client); qerr != nil {
			info.RuntimeNote = "could not probe: " + qerr.Error()
		} else if st.Running {
			info.Running = true
			info.Pid = st.Runtime.Pid
			info.Port = st.Runtime.Port
			info.URL = st.Runtime.URL
		}
	} else {
		info.RuntimeNote = "could not resolve the state dir: " + err.Error()
	}

	// Autostart: always ask the OS (the same posture `dexel autostart
	// status` takes), and degrade a probe error to a note.
	if st, err := autostart.Query(); err != nil {
		info.Autostart = "(unavailable: " + err.Error() + ")"
	} else if st.Mechanism == autostart.MechanismNone {
		info.Autostart = "OFF"
	} else {
		info.Autostart = fmt.Sprintf("ON via %s (active=%v)", st.Mechanism, st.Active)
		info.AutostartDetail = st.Detail
	}

	return info
}

// renderDoctor writes the report to w. Pure: no globals beyond the color
// gate (which reads w), so a test drives it with a buffer and a fixed
// doctorInfo.
func renderDoctor(w io.Writer, d doctorInfo) {
	fmt.Fprintln(w, bold(w, "dexel doctor")+" — paste this when reporting an issue")
	fmt.Fprintln(w)

	fmt.Fprintln(w, bold(w, "build"))
	fmt.Fprintf(w, "  version   %s\n", d.Version)
	fmt.Fprintf(w, "  commit    %s\n", d.Commit)
	fmt.Fprintf(w, "  os/arch   %s/%s\n", d.OS, d.Arch)
	fmt.Fprintf(w, "  binary    %s\n", d.Self)
	fmt.Fprintln(w)

	fmt.Fprintln(w, bold(w, "paths"))
	fmt.Fprintf(w, "  state     %s\n", d.StateDir)
	fmt.Fprintf(w, "  logs      %s\n", d.LogDir)
	fmt.Fprintf(w, "  log file  %s\n", d.LogFile)
	fmt.Fprintf(w, "  cache     %s\n", d.CacheDir)
	fmt.Fprintf(w, "  bin       %s\n", d.BinDir)
	fmt.Fprintf(w, "  config    %s\n", d.ConfigPath)
	fmt.Fprintln(w)

	fmt.Fprintln(w, bold(w, "runtime"))
	switch {
	case d.RuntimeNote != "":
		fmt.Fprintf(w, "  running   unknown (%s)\n", d.RuntimeNote)
	case d.Running:
		fmt.Fprintf(w, "  running   yes (pid %d)\n", d.Pid)
		fmt.Fprintf(w, "  url       %s\n", d.URL)
		fmt.Fprintf(w, "  port      %d\n", d.Port)
	default:
		fmt.Fprintln(w, "  running   no")
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, bold(w, "tracking"))
	fmt.Fprintf(w, "  provider  %s\n", d.Provider)
	fmt.Fprintf(w, "  app id    %s\n", d.AppIdentity)
	fmt.Fprintln(w)

	fmt.Fprintln(w, bold(w, "autostart"))
	fmt.Fprintf(w, "  status    %s\n", d.Autostart)
	if d.AutostartDetail != "" {
		fmt.Fprintf(w, "  detail    %s\n", d.AutostartDetail)
	}
}

// appIdentityCapability states, per platform, whether app identity (the
// active application's name) is OBSERVABLE by this build's capture
// strategy — a fixed fact of each provider, not a runtime measurement, so
// it is knowable without starting anything:
//
//   - darwin/windows: observable when the OS grants it (frontmost-app
//     polling / foreground window), and the running game reports the live
//     truth via Snapshot.AppIdentityAvailable;
//   - linux: never — the /dev/input raw reader sees keystrokes and mouse
//     motion but has no view of the active window (provider_linux.go's
//     Snapshot pins AppIdentityAvailable to false);
//   - anywhere else: the blind zero-signal fallback sees nothing at all.
func appIdentityCapability(goos string) string {
	switch goos {
	case "darwin":
		return "yes, when granted (frontmost-app polling)"
	case "windows":
		return "yes, when permitted (foreground window)"
	case "linux":
		return "no (the raw input reader cannot see the active window)"
	default:
		return "no (blind fallback; no native provider for this platform)"
	}
}

// orNote collapses a (value, error) path resolution into a single string:
// the value when it resolved, or a short parenthetical note when it did
// not — so every path line in the report is either a real path or an
// honest reason there is none, never a blank.
func orNote(v string, err error) string {
	if err != nil {
		return "(unavailable: " + err.Error() + ")"
	}
	return v
}
