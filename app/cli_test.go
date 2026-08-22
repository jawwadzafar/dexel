package main

import (
	"encoding/json"
	"flag"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/lifecycle"
)

// TestClassifyImplementsForkADispatchTable is THE compatibility test of
// PR-3. It pins ARCHITECTURE.md FORK A's four-row table, and in
// particular the row everything else depends on: an argv whose first
// element starts with "-" is the LEGACY foreground runtime, unchanged.
//
// The legacy cases below are not invented — they are the invocations that
// exist in the tree right now:
//
//	.github/workflows/desktop.yml:127  ./dexel-server -addr 127.0.0.1:0 -provider fake
//	desktop/src-tauri/src/lib.rs       sidecar ... -addr 127.0.0.1:0
//	README.md                          go run . -fake-script "type:20s,..."
//
// If any of them ever classified as anything but dispatchLegacy, the
// sidecar assertion, the Tauri shell and the documented dev loop would
// all break at once.
func TestClassifyImplementsForkADispatchTable(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantKind dispatchKind
		wantName string
		wantArgs []string
	}{
		// Row 1: bare dexel.
		{"bare", nil, dispatchBare, "", nil},
		{"bare empty slice", []string{}, dispatchBare, "", nil},

		// Row 3 (checked first because it is the compatibility rule):
		// anything starting with "-" is today's foreground server, and it
		// receives the WHOLE argv.
		{"desktop.yml sidecar assertion", []string{"-addr", "127.0.0.1:0", "-provider", "fake"},
			dispatchLegacy, "", []string{"-addr", "127.0.0.1:0", "-provider", "fake"}},
		{"tauri sidecar", []string{"-addr", "127.0.0.1:0"}, dispatchLegacy, "", []string{"-addr", "127.0.0.1:0"}},
		{"README fake script", []string{"-fake-script", "type:20s,idle:40s"},
			dispatchLegacy, "", []string{"-fake-script", "type:20s,idle:40s"}},
		{"public override", []string{"-public", "./public"}, dispatchLegacy, "", []string{"-public", "./public"}},
		{"gnu style long flag", []string{"--addr=127.0.0.1:0"}, dispatchLegacy, "", []string{"--addr=127.0.0.1:0"}},
		// -h/--help deliberately stay the SERVER's flag usage rather than
		// being re-pointed at the CLI help: they are pre-existing
		// behaviour of the legacy shape.
		{"dash h", []string{"-h"}, dispatchLegacy, "", []string{"-h"}},
		{"dash dash help", []string{"--help"}, dispatchLegacy, "", []string{"--help"}},

		// Row 2: known subcommands, with their own args stripped of the
		// verb.
		{"version", []string{"version"}, dispatchSubcommand, "version", []string{}},
		{"start", []string{"start"}, dispatchSubcommand, "start", []string{}},
		{"start with passthrough flags", []string{"start", "-provider", "fake"},
			dispatchSubcommand, "start", []string{"-provider", "fake"}},
		{"status json", []string{"status", "--json"}, dispatchSubcommand, "status", []string{"--json"}},
		{"serve with today's flags", []string{"serve", "-addr", "127.0.0.1:8080"},
			dispatchSubcommand, "serve", []string{"-addr", "127.0.0.1:8080"}},
		{"runtime", []string{"runtime"}, dispatchSubcommand, "runtime", []string{}},
		{"stop", []string{"stop"}, dispatchSubcommand, "stop", []string{}},
		{"restart", []string{"restart"}, dispatchSubcommand, "restart", []string{}},
		{"open", []string{"open"}, dispatchSubcommand, "open", []string{}},
		{"logs with flags", []string{"logs", "-n", "5"}, dispatchSubcommand, "logs", []string{"-n", "5"}},
		{"help", []string{"help"}, dispatchSubcommand, "help", []string{}},

		// Row 4: an unknown word is an error, NEVER a silent fall-through
		// to starting a server.
		{"typo", []string{"statsu"}, dispatchUnknown, "statsu", []string{}},
		{"pause", []string{"pause"}, dispatchSubcommand, "pause", []string{}},
		{"resume", []string{"resume"}, dispatchSubcommand, "resume", []string{}},
		{"a future command not yet built", []string{"autostart"}, dispatchUnknown, "autostart", []string{}},
		{"a bare path", []string{"state.db"}, dispatchUnknown, "state.db", []string{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.args)
			if got.Kind != tc.wantKind {
				t.Fatalf("classify(%q).Kind = %v, want %v", tc.args, got.Kind, tc.wantKind)
			}
			if got.Name != tc.wantName {
				t.Fatalf("classify(%q).Name = %q, want %q", tc.args, got.Name, tc.wantName)
			}
			if len(got.Args) != len(tc.wantArgs) {
				t.Fatalf("classify(%q).Args = %q, want %q", tc.args, got.Args, tc.wantArgs)
			}
			for i := range got.Args {
				if got.Args[i] != tc.wantArgs[i] {
					t.Fatalf("classify(%q).Args = %q, want %q", tc.args, got.Args, tc.wantArgs)
				}
			}
		})
	}
}

// TestEverySSubcommandIsWiredAndDocumented: the one table in cli.go
// carries both the help text and the implementation, so this asserts
// neither half can be empty — a listed-but-unimplemented word would
// panic on a nil func, and an implemented-but-undescribed one would print
// a blank line in `dexel help`.
func TestEverySubcommandIsWiredAndDocumented(t *testing.T) {
	if len(subcommands) == 0 {
		t.Fatal("no subcommands registered")
	}
	for name, sc := range subcommands {
		if strings.TrimSpace(sc.help) == "" {
			t.Errorf("subcommand %q has no help text", name)
		}
		if sc.run == nil {
			t.Errorf("subcommand %q is listed but not wired (nil run)", name)
		}
	}
	// The words MIGRATION_PLAN.md §PR-3 owns must all be present...
	for _, name := range []string{"start", "stop", "restart", "status", "open", "logs", "serve", "runtime", "version", "help"} {
		if _, ok := subcommands[name]; !ok {
			t.Errorf("§PR-3 requires a %q subcommand and there is none", name)
		}
	}
	// ...as must PR-5's two, now that pause has semantics to flip
	// (MIGRATION_PLAN.md §PR-5).
	for _, name := range []string{"pause", "resume"} {
		if _, ok := subcommands[name]; !ok {
			t.Errorf("§PR-5 requires a %q subcommand and there is none", name)
		}
	}
	// ...and the words later PRs own must NOT be, because a word that is
	// listed but does nothing is worse than an honest "unknown command"
	// (PR-6 owns autostart, PR-7 update/uninstall).
	for _, name := range []string{"autostart", "update", "uninstall"} {
		if _, ok := subcommands[name]; ok {
			t.Errorf("%q is registered but its PR has not landed — it would do nothing", name)
		}
	}
}

// TestUsageListsEverySubcommand: `dexel help` and the unknown-command
// error both print this, and a command missing from it is a command
// nobody can discover.
func TestUsageListsEverySubcommand(t *testing.T) {
	var b strings.Builder
	usage(&b)
	out := b.String()
	for name := range subcommands {
		if !strings.Contains(out, name) {
			t.Errorf("usage() does not mention %q:\n%s", name, out)
		}
	}
	// The legacy shape has to be discoverable too, or a developer who
	// reads only the help concludes it was removed.
	if !strings.Contains(out, "-addr") {
		t.Errorf("usage() does not mention the legacy `dexel -addr ...` shape:\n%s", out)
	}
}

// TestServeModeDefaults pins ARCHITECTURE.md Decision 3's split: the
// detached `runtime` takes an OS-assigned port (a background process
// nobody chose a port for must never fight for 8080), while `serve` and
// the legacy shape keep today's 127.0.0.1:8080 so `go run . serve`
// behaves exactly as `go run .` did before FORK A.
func TestServeModeDefaults(t *testing.T) {
	if got := modeLegacy.defaultAddr(); got != "127.0.0.1:8080" {
		t.Fatalf("modeLegacy default addr = %q, want 127.0.0.1:8080 — this is the byte-compatibility rule", got)
	}
	if got := modeServe.defaultAddr(); got != "127.0.0.1:8080" {
		t.Fatalf("modeServe default addr = %q, want 127.0.0.1:8080", got)
	}
	if got := modeRuntime.defaultAddr(); got != "127.0.0.1:0" {
		t.Fatalf("modeRuntime default addr = %q, want 127.0.0.1:0", got)
	}
	if got := modeServe.flagSetName(); got != "dexel serve" {
		t.Fatalf("modeServe flag set name = %q", got)
	}
	if got := modeRuntime.flagSetName(); got != "dexel runtime" {
		t.Fatalf("modeRuntime flag set name = %q", got)
	}
}

// TestBrowserOpenCommandPerOS pins ARCHITECTURE.md Decision 17's
// URL-opening incantation for every platform, from whichever single host
// runs `go test` — the same convention app/internal/paths' stateDirFor
// table follows. It asserts the COMMAND, deliberately: actually launching
// a browser in a test would be a side effect on the developer's desktop.
func TestBrowserOpenCommandPerOS(t *testing.T) {
	const url = "http://127.0.0.1:51637"
	cases := []struct {
		goos     string
		wantName string
		wantArgs []string
	}{
		{"linux", "xdg-open", []string{url}},
		{"darwin", "open", []string{url}},
		{"windows", "rundll32", []string{"url.dll,FileProtocolHandler", url}},
		// Any other unix falls in the xdg bucket, which is the correct
		// default for freedesktop-conformant systems and an honest
		// failure (an error the CLI prints, with the URL to open by hand)
		// anywhere else.
		{"freebsd", "xdg-open", []string{url}},
	}
	for _, tc := range cases {
		t.Run(tc.goos, func(t *testing.T) {
			name, args := browserOpenCommand(tc.goos, url)
			if name != tc.wantName {
				t.Fatalf("command = %q, want %q", name, tc.wantName)
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Fatalf("args = %q, want %q", args, tc.wantArgs)
			}
			// The URL must be passed as its own argv element on every
			// platform — never interpolated into a shell string, where an
			// "&" in a query would become a command separator.
			if args[len(args)-1] != url {
				t.Fatalf("the url is not the final argv element: %q", args)
			}
		})
	}
}

// TestDesktopAppCandidatesPerOS pins Decision 17's lookup ORDER after the
// PATH probe: BinDir first (where the installer and `dexel update` put
// things), then the platform's app location. Order matters — a user who
// installed via the script and later dropped a .dmg copy in /Applications
// should get the one the CLI manages.
func TestDesktopAppCandidatesPerOS(t *testing.T) {
	cases := []struct {
		goos, binDir, localAppData string
		want                       []string
	}{
		{"linux", "/home/u/.local/bin", "", []string{"/home/u/.local/bin/dexel-desktop"}},
		{"darwin", "/Users/u/.local/bin", "", []string{"/Users/u/.local/bin/dexel-desktop", "/Applications/dexel.app"}},
		{"windows", `C:\Users\u\AppData\Local\dexel\bin`, `C:\Users\u\AppData\Local`, []string{
			`C:\Users\u\AppData\Local\dexel\bin\dexel-desktop.exe`,
			`C:\Users\u\AppData\Local\Programs\dexel\dexel-desktop.exe`,
		}},
		// An unresolvable BinDir must DROP that candidate rather than
		// produce a relative path that would resolve against the cwd.
		{"linux", "", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.goos+"/"+tc.binDir, func(t *testing.T) {
			got := desktopAppCandidates(tc.goos, tc.binDir, tc.localAppData)
			if len(got) != len(tc.want) {
				t.Fatalf("candidates = %q, want %q", got, tc.want)
			}
			for i := range got {
				// Compare with the platform separator normalised away:
				// filepath.Join uses the HOST separator, and this test
				// runs on one host for every goos row.
				if normalizeSep(got[i]) != normalizeSep(tc.want[i]) {
					t.Fatalf("candidate %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func normalizeSep(p string) string {
	return strings.ReplaceAll(p, `\`, "/")
}

// TestRuntimeRecordPinsDecision6sObject checks the exact object
// ARCHITECTURE.md Decision 6 shows, built from a RESOLVED listen address.
func TestRuntimeRecordPinsDecision6sObject(t *testing.T) {
	now := time.Date(2026, 8, 22, 9, 14, 2, 0, time.UTC)
	got, err := runtimeRecord("127.0.0.1:51637", 48213, "tok", now)
	if err != nil {
		t.Fatalf("runtimeRecord: %v", err)
	}
	if got.Schema != lifecycle.RuntimeSchema {
		t.Fatalf("schema = %d, want %d", got.Schema, lifecycle.RuntimeSchema)
	}
	if got.Pid != 48213 {
		t.Fatalf("pid = %d", got.Pid)
	}
	if got.Port != 51637 {
		t.Fatalf("port = %d, want the RESOLVED port", got.Port)
	}
	if got.URL != "http://127.0.0.1:51637" {
		t.Fatalf("url = %q", got.URL)
	}
	if got.StartedAt != "2026-08-22T09:14:02Z" {
		t.Fatalf("startedAt = %q, want RFC3339 UTC", got.StartedAt)
	}
	if got.Token != "tok" {
		t.Fatalf("token = %q", got.Token)
	}
	if got.Version != version {
		t.Fatalf("version = %q, want this build's %q", got.Version, version)
	}
	if got.Commit == "" {
		t.Fatal("commit is empty — buildVersion() always answers something, even \"unknown\"")
	}
}

// TestRuntimeRecordNormalisesAWildcardBind: a wildcard is a valid LISTEN
// address and a meaningless DIAL address, and runtime.json's url is a
// dial address — it is what `dexel status` probes and what `dexel open`
// hands a browser.
func TestRuntimeRecordNormalisesAWildcardBind(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8080", ":8080", "[::]:8080"} {
		t.Run(addr, func(t *testing.T) {
			got, err := runtimeRecord(addr, 1, "t", time.Now())
			if err != nil {
				t.Fatalf("runtimeRecord(%q): %v", addr, err)
			}
			if got.URL != "http://127.0.0.1:8080" {
				t.Fatalf("url = %q, want http://127.0.0.1:8080", got.URL)
			}
			if got.Port != 8080 {
				t.Fatalf("port = %d, want 8080", got.Port)
			}
		})
	}
}

// TestRuntimeRecordRefusesAnAddressWithoutAUsablePort: writing a
// runtime.json with port 0 would publish an address nothing can reach and
// make `dexel status` permanently wrong, so it must fail loudly at write
// time instead.
func TestRuntimeRecordRefusesAnAddressWithoutAUsablePort(t *testing.T) {
	for _, addr := range []string{"127.0.0.1", "127.0.0.1:0", "127.0.0.1:notaport", ""} {
		if _, err := runtimeRecord(addr, 1, "t", time.Now()); err == nil {
			t.Fatalf("runtimeRecord(%q) succeeded, want an error", addr)
		}
	}
}

// TestReachableHost documents which bind hosts are rewritten and which
// are passed through untouched.
func TestReachableHost(t *testing.T) {
	cases := map[string]string{
		"":            "127.0.0.1",
		"0.0.0.0":     "127.0.0.1",
		"::":          "127.0.0.1",
		"[::]":        "127.0.0.1",
		"127.0.0.1":   "127.0.0.1",
		"localhost":   "localhost",
		"192.168.1.5": "192.168.1.5",
	}
	for in, want := range cases {
		if got := reachableHost(in); got != want {
			t.Errorf("reachableHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestUptimeSecondsNeverClaimsWhatItCannotKnow: ADR 0010's rule applied
// to `dexel status`. An absent or unparseable startedAt must report -1
// (which the text output then omits) rather than a plausible-looking 0.
func TestUptimeSeconds(t *testing.T) {
	now := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	if got := uptimeSeconds("2026-08-22T09:00:00Z", now); got != 3600 {
		t.Fatalf("uptime = %d, want 3600", got)
	}
	if got := uptimeSeconds("", now); got != -1 {
		t.Fatalf("uptime for an empty startedAt = %d, want -1 (unknown, not zero)", got)
	}
	if got := uptimeSeconds("not a timestamp", now); got != -1 {
		t.Fatalf("uptime for garbage = %d, want -1", got)
	}
	// A clock that moved backwards (NTP, a suspend, a VM snapshot) must
	// clamp to 0 rather than report a negative age.
	if got := uptimeSeconds("2026-08-22T11:00:00Z", now); got != 0 {
		t.Fatalf("uptime for a future startedAt = %d, want 0", got)
	}
}

// TestStatusJSONShape pins `dexel status --json`'s keys. PR-9's
// resolve_runtime() in desktop/src-tauri/src/lib.rs is specified to read
// this exact output, so the key names are a contract, not an
// implementation detail.
func TestStatusJSONShape(t *testing.T) {
	data, err := json.Marshal(statusJSON{
		Running: true, Pid: 1, Port: 2, URL: "u", Version: "v", Commit: "c",
		StartedAt: "s", Uptime: 3, StateDir: "sd", LogPath: "lp", Cleaned: true, Reason: "r",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{"running", "pid", "port", "url", "version", "commit", "startedAt", "uptimeSeconds", "stateDir", "logPath", "cleanedStaleRuntimeFile", "reason"}
	if len(m) != len(want) {
		t.Fatalf("status --json has %d keys (%v), want exactly %d (%v)", len(m), m, len(want), want)
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Fatalf("status --json is missing %q", k)
		}
	}

	// Not-running must still carry `running` (so a consumer can branch on
	// it) and both paths (so a user can go look), and must NOT carry a
	// pid/url it does not have.
	data, err = json.Marshal(statusJSON{Running: false, StateDir: "sd", LogPath: "lp"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	m = nil
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if running, ok := m["running"].(bool); !ok || running {
		t.Fatalf("running = %v, want an explicit false", m["running"])
	}
	for _, k := range []string{"pid", "port", "url", "version"} {
		if _, ok := m[k]; ok {
			t.Fatalf("a not-running status claims %q — it must be omitted", k)
		}
	}
	for _, k := range []string{"stateDir", "logPath"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("a not-running status omits %q, which is the one thing a user needs", k)
		}
	}
}

// TestFirstNonEmpty covers the "prefer the LIVE answer" rule in
// cmdStatus: after a `dexel update` the file's version and the running
// process's version legitimately differ, and what is RUNNING is the truth.
func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "b", "c"); got != "b" {
		t.Fatalf("got %q, want b", got)
	}
	if got := firstNonEmpty("a", "b"); got != "a" {
		t.Fatalf("got %q, want a", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// TestServeFlagSetDefaultsPerMode is a REGRESSION test for a real bug in
// the first cut of PR-3: -addr's default was left as a hardcoded
// "127.0.0.1:8080" while serveMode.defaultAddr() existed and was
// unit-tested, so `dexel runtime` bound 8080 instead of an ephemeral port
// and two DEXEL_HOMEs would have fought over it. The only way to SEE that
// was to start a real server — which is why the flag set is now built by
// a function this test can call directly.
//
// It asserts the whole flag surface at once, because that surface is the
// legacy compatibility contract: every name, type and default other than
// -addr's must be exactly what the pre-PR-3 global flag.CommandLine had.
func TestServeFlagSetDefaultsPerMode(t *testing.T) {
	wantAddr := map[serveMode]string{
		modeLegacy:  "127.0.0.1:8080",
		modeServe:   "127.0.0.1:8080",
		modeRuntime: "127.0.0.1:0",
	}
	for mode, want := range wantAddr {
		fs, f := newServeFlagSet(mode)
		if got := *f.addr; got != want {
			t.Errorf("mode %d: -addr default = %q, want %q", mode, got, want)
		}
		if got := fs.Lookup("addr").DefValue; got != want {
			t.Errorf("mode %d: -addr DefValue = %q, want %q (this is what `-h` prints)", mode, got, want)
		}
		// The rest of the surface is mode-independent and must match the
		// pre-PR-3 defaults exactly.
		if *f.publicDir != "" {
			t.Errorf("mode %d: -public default = %q, want empty", mode, *f.publicDir)
		}
		if *f.providerKind != "auto" {
			t.Errorf("mode %d: -provider default = %q, want auto", mode, *f.providerKind)
		}
		if *f.fakeScript != "" {
			t.Errorf("mode %d: -fake-script default = %q, want empty", mode, *f.fakeScript)
		}
		if *f.insecureOrigin {
			t.Errorf("mode %d: -insecure-origin defaults to TRUE — that would disable the WS origin check by default", mode)
		}
		if len(f.allowOrigins) != 0 {
			t.Errorf("mode %d: -allow-origin default is non-empty: %v", mode, f.allowOrigins)
		}
		// Every flag that existed before PR-3 must still exist, under the
		// same name: desktop.yml, the Tauri sidecar and README all pass
		// them positionally-by-name.
		for _, name := range []string{"addr", "public", "provider", "fake-script", "insecure-origin", "allow-origin"} {
			if fs.Lookup(name) == nil {
				t.Errorf("mode %d: flag -%s disappeared — a pre-PR-3 invocation would now fail", mode, name)
			}
		}
		// And no NEW flag may appear on the server: PR-3 adds
		// subcommands, not server flags.
		count := 0
		fs.VisitAll(func(*flag.Flag) { count++ })
		if count != 6 {
			t.Errorf("mode %d: the server has %d flags, want exactly the 6 that existed before PR-3", mode, count)
		}
	}
}

// TestLegacyAndServeFlagSetsAreIdentical pins the compatibility rule from
// serveMode's doc comment: modeLegacy and modeServe differ ONLY in the
// flag-set NAME (which appears in flag's own error output) and in the one
// extra stderr line `serve` logs. Their flags, defaults and help strings
// must be identical, or `dexel serve` would not be the honest rename of
// today's bare invocation that ARCHITECTURE.md Decision 3 promises.
func TestLegacyAndServeFlagSetsAreIdentical(t *testing.T) {
	legacyFS, _ := newServeFlagSet(modeLegacy)
	serveFS, _ := newServeFlagSet(modeServe)

	collect := func(fs *flag.FlagSet) map[string][2]string {
		out := map[string][2]string{}
		fs.VisitAll(func(fl *flag.Flag) { out[fl.Name] = [2]string{fl.DefValue, fl.Usage} })
		return out
	}
	if !reflect.DeepEqual(collect(legacyFS), collect(serveFS)) {
		t.Fatal("modeLegacy and modeServe expose different flags/defaults/help — `dexel serve` must be an exact rename of the legacy shape")
	}
}
