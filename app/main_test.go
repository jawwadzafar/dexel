package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/store"
)

// TestEphemeralPortHandshake exercises the actual ADR 0015 / F3-design.md
// §1,§8 T2 mechanism end to end at the net.Listen level: binding
// "127.0.0.1:0" must yield a real, nonzero OS-assigned port via
// ln.Addr(), the handshake line built from that address must be
// well-formed and carry that same port, and wsOriginPatterns derived from
// it must reflect the *resolved* port (never the literal "0" that was
// requested) — exactly the property the webview/Tauri shell depends on to
// find and be authorized against the right port.
func TestEphemeralPortHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on 127.0.0.1:0: %v", err)
	}
	defer ln.Close()

	actualAddr := ln.Addr().String()
	host, port, err := net.SplitHostPort(actualAddr)
	if err != nil {
		t.Fatalf("split resolved addr %q: %v", actualAddr, err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("resolved host = %q, want 127.0.0.1", host)
	}
	if port == "" || port == "0" {
		t.Fatalf("resolved port = %q, want a real nonzero port", port)
	}

	line := handshakeLine(actualAddr)
	want := "DEXEL_LISTENING http://127.0.0.1:" + port
	if line != want {
		t.Fatalf("handshakeLine(%q) = %q, want %q", actualAddr, line, want)
	}

	patterns := wsOriginPatterns(actualAddr)
	wantPatterns := []string{"127.0.0.1:" + port, "localhost:" + port}
	if len(patterns) != len(wantPatterns) || patterns[0] != wantPatterns[0] || patterns[1] != wantPatterns[1] {
		t.Fatalf("wsOriginPatterns(%q) = %v, want %v", actualAddr, patterns, wantPatterns)
	}
}

// TestHandshakeLineFixedPort covers the non-ephemeral default (-addr
// 127.0.0.1:8080, unchanged from before this change): the handshake line
// must show the real requested port, not just work for :0.
func TestHandshakeLineFixedPort(t *testing.T) {
	got := handshakeLine("127.0.0.1:8080")
	want := "DEXEL_LISTENING http://127.0.0.1:8080"
	if got != want {
		t.Fatalf("handshakeLine(\"127.0.0.1:8080\") = %q, want %q", got, want)
	}
}

// TestWsExtraOriginPatterns covers -allow-origin's two accepted input
// shapes (bare host[:port], and a full origin URL from which the host is
// extracted — see ADR 0015/F3-design.md §2's two real Tauri v2 origins),
// comma-separated accumulation via originList, and that a wildcard or
// unparseable value is rejected rather than silently degrading the origin
// check into another -insecure-origin.
func TestWsExtraOriginPatterns(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		want    []string
		wantErr bool
	}{
		{
			name: "bare host",
			in:   []string{"tauri.localhost"},
			want: []string{"tauri.localhost"},
		},
		{
			name: "full origin URL, scheme host only (macOS/Linux Tauri origin)",
			in:   []string{"tauri://localhost"},
			want: []string{"localhost"},
		},
		{
			name: "full origin URL with host (Windows Tauri origin)",
			in:   []string{"http://tauri.localhost"},
			want: []string{"tauri.localhost"},
		},
		{
			name: "multiple values",
			in:   []string{"tauri.localhost", "tauri://localhost"},
			want: []string{"tauri.localhost", "localhost"},
		},
		{
			name:    "wildcard is never allowed",
			in:      []string{"*"},
			wantErr: true,
		},
		{
			name:    "wildcard host inside a URL is never allowed",
			in:      []string{"http://*"},
			wantErr: true,
		},
		{
			name:    "hostless URL is rejected",
			in:      []string{"file://"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := wsExtraOriginPatterns(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("wsExtraOriginPatterns(%v) = %v, nil; want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("wsExtraOriginPatterns(%v) unexpected error: %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("wsExtraOriginPatterns(%v) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("wsExtraOriginPatterns(%v) = %v, want %v", tc.in, got, tc.want)
				}
			}
		})
	}
}

// TestOriginListSetAccumulatesAndSplitsCommas covers the -allow-origin
// flag.Value itself: repeated flag occurrences must accumulate (unlike
// flag.String, which keeps only the last one), and a single occurrence
// may list several comma-separated origins at once.
func TestOriginListSetAccumulatesAndSplitsCommas(t *testing.T) {
	var o originList
	if err := o.Set("tauri.localhost, tauri://localhost"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := o.Set("http://tauri.localhost"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	want := []string{"tauri.localhost", "tauri://localhost", "http://tauri.localhost"}
	if len(o) != len(want) {
		t.Fatalf("originList = %v, want %v", []string(o), want)
	}
	for i := range o {
		if o[i] != want[i] {
			t.Fatalf("originList = %v, want %v", []string(o), want)
		}
	}
	if !strings.Contains(o.String(), "tauri.localhost") {
		t.Fatalf("String() = %q, want it to contain the set values", o.String())
	}
}

// TestHealthHandlerReportsVersionAndCommitSeparately covers PR-2
// (MIGRATION_PLAN.md §PR-2): /api/health must report "version" (the
// ldflags-stamped semver-or-"dev" string, main.version) and "commit"
// (buildVersion()'s VCS-revision output, unchanged) as two INDEPENDENT
// fields — the point being that a release binary, extracted with no .git
// directory anywhere nearby, can still report a real version even though
// buildVersion() alone would say "unknown".
func TestHealthHandlerReportsVersionAndCommitSeparately(t *testing.T) {
	h := healthHandler(nil, true, "v9.9.9", "abc123-dirty", "embedded", "embedded")

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal health response %s: %v", rec.Body.String(), err)
	}
	if got.Version != "v9.9.9" {
		t.Fatalf(`healthResponse.Version = %q, want "v9.9.9"`, got.Version)
	}
	if got.Commit != "abc123-dirty" {
		t.Fatalf(`healthResponse.Commit = %q, want "abc123-dirty"`, got.Commit)
	}
	if got.Version == got.Commit {
		t.Fatalf("Version and Commit must be independently reported, got the same value %q for both", got.Version)
	}
}

// TestVersionLineReflectsTheVersionVar covers `dexel version`'s (and the
// startup log line's) exact output: it must contain the current
// main.version, ldflags-settable via
// `-ldflags "-X main.version=$VERSION"` (scripts/build-release.sh,
// scripts/build-sidecar.sh), and defaults to "dev" for a plain
// `go build`/`go run .` — this test restores that default so no other
// test observes a mutated value.
func TestVersionLineReflectsTheVersionVar(t *testing.T) {
	if version != "dev" {
		t.Fatalf(`main.version = %q at test start, want the "dev" build-time default (this test assumes nothing upstream stamped it, matching a plain "go build")`, version)
	}
	t.Cleanup(func() { version = "dev" })

	version = "v9.9.9"
	line := versionLine()
	if !strings.Contains(line, "v9.9.9") {
		t.Fatalf("versionLine() = %q, want it to contain the stamped version %q", line, "v9.9.9")
	}
	if !strings.Contains(line, buildVersion()) {
		t.Fatalf("versionLine() = %q, want it to also contain buildVersion()'s output %q", line, buildVersion())
	}
}

// TestConfigWriteThroughPreservesAutostart is the regression test for a
// silent clobber: the runtime's config write-through built a fresh
// store.ConfigData with only the two halves it owns, and SaveConfig
// marshals the WHOLE struct — so the zero-value `autostart` field was
// written over the real one on every SET_NAME and every SESSION_START.
//
// store/config.go's own doc comment on that field says it is "written ONLY
// by `dexel autostart enable`/`disable` ... nothing else in this codebase
// may set it", which is the ADR-level explicit-consent posture for
// autostart. The runtime was setting it, to "".
//
// It was low-severity by luck rather than design — the field is advisory
// and `dexel autostart status` always asks the OS directly — but it lost
// which mechanism was installed, and a comment two lines above the bug
// promised that "writing either half never clobbers the other".
func TestConfigWriteThroughPreservesAutostart(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// A config as `dexel autostart enable` leaves it.
	if err := store.SaveConfig(cfgPath, store.ConfigData{
		Name:      "Marvin",
		Autostart: "launchd",
	}); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	// The runtime names a session (the SESSION_START path) and renames the
	// dexel (the SET_NAME path).
	if err := writeConfigThrough(cfgPath, "Zaphod", map[string]string{"7": "Fix Bug #404"}, configPrefs{}); err != nil {
		t.Fatalf("writeConfigThrough: %v", err)
	}

	got, err := store.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got.Autostart != "launchd" {
		t.Errorf("autostart = %q after a name write-through, want %q — the runtime must never touch this field", got.Autostart, "launchd")
	}
	if got.Name != "Zaphod" {
		t.Errorf("name = %q, want %q", got.Name, "Zaphod")
	}
	if got.SessionNames["7"] != "Fix Bug #404" {
		t.Errorf("sessionNames = %v, want session 7 named", got.SessionNames)
	}
}

// TestConfigWriteThroughCarriesPrefsWithoutClobberingTheOtherFields is the
// SET-1 (docs/ui-spec.md §11) half of the lesson above, from both
// directions: a SET_PREF write must carry the preferences AND leave the
// name/sessionNames/autostart it does not own exactly as it found them,
// and a SET_NAME write must not silently switch the preferences back off.
//
// The second half is the one that would actually bite: `alwaysOnTop` and
// `showAwayTime` both DEFAULT to false, so a construct-and-overwrite bug
// here would look exactly like "the user never set it" — the quietest
// possible regression, and the reason both directions are pinned rather
// than just the new one.
func TestConfigWriteThroughCarriesPrefsWithoutClobberingTheOtherFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	if err := store.SaveConfig(cfgPath, store.ConfigData{
		Name:      "Marvin",
		Autostart: "launchd",
	}); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	// The SET_PREF path: both preferences on, everything else untouched.
	if err := writeConfigThrough(cfgPath, "Marvin", map[string]string{"3": "docs pass"}, configPrefs{
		AlwaysOnTop:  true,
		ShowAwayTime: true,
	}); err != nil {
		t.Fatalf("writeConfigThrough (prefs): %v", err)
	}
	got, err := store.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if !got.AlwaysOnTop || !got.ShowAwayTime {
		t.Errorf("prefs = {alwaysOnTop:%v showAwayTime:%v}, want both true", got.AlwaysOnTop, got.ShowAwayTime)
	}
	if got.Autostart != "launchd" || got.Name != "Marvin" || got.SessionNames["3"] != "docs pass" {
		t.Errorf("a prefs write-through disturbed another field: %+v", got)
	}

	// The SET_NAME path, carrying the live prefs as main.go's
	// persistConfig does: the preferences must survive a rename.
	if err := writeConfigThrough(cfgPath, "Zaphod", map[string]string{"3": "docs pass"}, configPrefs{
		AlwaysOnTop:  true,
		ShowAwayTime: true,
	}); err != nil {
		t.Fatalf("writeConfigThrough (rename): %v", err)
	}
	got, err = store.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got.Name != "Zaphod" {
		t.Errorf("name = %q, want %q", got.Name, "Zaphod")
	}
	if !got.AlwaysOnTop || !got.ShowAwayTime {
		t.Errorf("prefs = {alwaysOnTop:%v showAwayTime:%v} after a rename, want both still true", got.AlwaysOnTop, got.ShowAwayTime)
	}
	if got.Autostart != "launchd" {
		t.Errorf("autostart = %q after a rename, want %q", got.Autostart, "launchd")
	}
}

// TestConfigWriteThroughOnAMissingFileStillWrites pins the first-run path:
// no config.json yet is not an error (LoadConfig returns a zero value), so
// the write-through must create the file rather than refuse.
func TestConfigWriteThroughOnAMissingFileStillWrites(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := writeConfigThrough(cfgPath, "Ford", nil, configPrefs{}); err != nil {
		t.Fatalf("writeConfigThrough on a missing file: %v", err)
	}
	got, err := store.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Name != "Ford" {
		t.Errorf("name = %q, want %q", got.Name, "Ford")
	}
	if got.Autostart != "" {
		t.Errorf("autostart = %q on a fresh config, want empty", got.Autostart)
	}
}
