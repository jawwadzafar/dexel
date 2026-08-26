package main

import (
	"strings"
	"testing"
)

// TestRenderDoctorRunning pins the report's layout and, more importantly,
// its CONTENT-FREE boundary: every section header and every capability/
// path/status line is present, and nothing a bug report has no business
// carrying (a keystroke count, a duration, a save value) can appear —
// renderDoctor is handed a struct that has no field for such a thing.
func TestRenderDoctorRunning(t *testing.T) {
	var b strings.Builder
	renderDoctor(&b, doctorInfo{
		Version: "v0.1.0", Commit: "c1036b2", OS: "linux", Arch: "amd64",
		Self:     "/home/u/.local/bin/dexel",
		StateDir: "/home/u/.local/state/dexel", LogDir: "/home/u/.local/state/dexel/logs",
		LogFile: "/home/u/.local/state/dexel/logs/runtime.log", CacheDir: "/home/u/.local/state/dexel/cache",
		BinDir: "/home/u/.local/bin", ConfigPath: "/home/u/.local/state/dexel/config.json",
		Running: true, Pid: 4242, Port: 51637, URL: "http://127.0.0.1:51637",
		Provider: "linux (/dev/input/event* raw reader)", AppIdentity: appIdentityCapability("linux"),
		Autostart: "ON via systemd-user (active=true)", AutostartDetail: "unit: dexel.service",
	})
	out := b.String()

	for _, want := range []string{
		"dexel doctor",                                       // title
		"build", "paths", "runtime", "tracking", "autostart", // section headers
		"v0.1.0", "c1036b2", "linux/amd64",
		"/home/u/.local/bin/dexel", "/home/u/.local/state/dexel/config.json",
		"running   yes (pid 4242)", "http://127.0.0.1:51637", "port      51637",
		"linux (/dev/input/event* raw reader)",
		"ON via systemd-user (active=true)", "unit: dexel.service",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor report is missing %q:\n%s", want, out)
		}
	}
}

// TestRenderDoctorNotRunning: the not-running branch prints a clean
// "running   no" and never a pid/url/port it does not have.
func TestRenderDoctorNotRunning(t *testing.T) {
	var b strings.Builder
	renderDoctor(&b, doctorInfo{
		Version: "dev", Commit: "unknown", OS: "linux", Arch: "amd64",
		Self: "/x/dexel", StateDir: "/x", LogDir: "/x/logs", LogFile: "/x/logs/runtime.log",
		CacheDir: "/x/cache", BinDir: "/b", ConfigPath: "/x/config.json",
		Running: false, Provider: "p", AppIdentity: "a", Autostart: "OFF",
	})
	out := b.String()
	if !strings.Contains(out, "running   no") {
		t.Fatalf("not-running report should say `running   no`:\n%s", out)
	}
	for _, forbidden := range []string{"pid ", "http://", "port  "} {
		if strings.Contains(out, forbidden) {
			t.Errorf("a not-running doctor report leaked %q:\n%s", forbidden, out)
		}
	}
}

// TestRenderDoctorProbeUnknown: when the runtime probe itself failed, the
// report says "unknown" with the reason rather than claiming either
// running or not — ADR 0010's honesty rule applied to diagnostics.
func TestRenderDoctorProbeUnknown(t *testing.T) {
	var b strings.Builder
	renderDoctor(&b, doctorInfo{
		OS: "linux", Arch: "amd64", RuntimeNote: "could not probe: connection refused",
		Provider: "p", AppIdentity: "a", Autostart: "OFF",
	})
	out := b.String()
	if !strings.Contains(out, "running   unknown") || !strings.Contains(out, "could not probe: connection refused") {
		t.Fatalf("probe-failure report should say running unknown with the reason:\n%s", out)
	}
}

// TestAppIdentityCapabilityPerOS pins the fixed per-platform truth doctor
// reports: linux never observes app identity (its raw input reader has no
// window view), darwin/windows can when permitted, and everything else is
// blind. These are provider facts (see provider_linux.go's Snapshot and
// the provider_select_*.go strategy strings), so a change here should mean
// a real change in what a platform's provider can see.
func TestAppIdentityCapabilityPerOS(t *testing.T) {
	cases := map[string]string{
		"linux":   "no",
		"darwin":  "yes",
		"windows": "yes",
		"freebsd": "no",
		"plan9":   "no",
	}
	for goos, wantPrefix := range cases {
		got := appIdentityCapability(goos)
		if !strings.HasPrefix(got, wantPrefix) {
			t.Errorf("appIdentityCapability(%q) = %q, want it to start with %q", goos, got, wantPrefix)
		}
	}
	// Linux is the load-bearing one: it must be a hard "no", because the
	// game genuinely cannot attribute typing to an app there.
	if strings.HasPrefix(appIdentityCapability("linux"), "yes") {
		t.Fatal("linux must report app identity as unavailable")
	}
}

// TestOrNote collapses (value, error) into either the value or an honest
// parenthetical — never a blank that would read as "exists and is empty".
func TestOrNote(t *testing.T) {
	if got := orNote("/a/path", nil); got != "/a/path" {
		t.Fatalf("orNote(value, nil) = %q, want the value", got)
	}
	got := orNote("", errBoom{})
	if !strings.HasPrefix(got, "(unavailable:") || !strings.Contains(got, "boom") {
		t.Fatalf("orNote(_, err) = %q, want an (unavailable: ...) note naming the error", got)
	}
}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }
