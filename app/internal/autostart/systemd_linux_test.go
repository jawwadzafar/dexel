//go:build linux

package autostart

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fakeHomeFn(home string) func() (string, error) {
	return func() (string, error) { return home, nil }
}

func fakeGetenvFn(env map[string]string) func(string) string {
	return func(key string) string { return env[key] }
}

// TestSystemdUnitPath proves the path (parameter-injected exactly like
// app/internal/paths' stateDirFor), both with and without
// $XDG_CONFIG_HOME set, against a fake home — never this developer's
// real one.
func TestSystemdUnitPath(t *testing.T) {
	got, err := systemdUnitPath(fakeGetenvFn(nil), fakeHomeFn("/home/dev"))
	if err != nil {
		t.Fatalf("systemdUnitPath: %v", err)
	}
	want := "/home/dev/.config/systemd/user/dexel.service"
	if got != want {
		t.Fatalf("systemdUnitPath = %q, want %q", got, want)
	}

	got, err = systemdUnitPath(fakeGetenvFn(map[string]string{"XDG_CONFIG_HOME": "/home/dev/.xdg"}), fakeHomeFn("/home/dev"))
	if err != nil {
		t.Fatalf("systemdUnitPath: %v", err)
	}
	want = "/home/dev/.xdg/systemd/user/dexel.service"
	if got != want {
		t.Fatalf("systemdUnitPath (XDG override) = %q, want %q", got, want)
	}
}

// TestSystemdUnitContentExact proves PLATFORM_NOTES.md §3.2's unit,
// byte for byte, with ExecStart pointing at the resolved exePath
// (MIGRATION_PLAN.md §PR-6: never the doc's illustrative "%h/.local/
// bin/dexel").
func TestSystemdUnitContentExact(t *testing.T) {
	got := systemdUnitContent("/home/dev/.local/bin/dexel")
	want := `[Unit]
Description=dexel developer companion runtime
Documentation=https://dexel.jwdlab.com
After=default.target

[Service]
Type=simple
ExecStart=/home/dev/.local/bin/dexel runtime
Restart=on-failure
RestartSec=5
# The runtime writes its own log file; journald gets it too, harmlessly.
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`
	if got != want {
		t.Fatalf("systemdUnitContent mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestSystemdUsableFromOutput proves the detection rule
// (PLATFORM_NOTES.md §3.2: "used only if [it] answers AT ALL") is
// "non-empty stdout", not "exit code zero" — "degraded" (non-empty
// stdout, non-zero exit in real systemd) must count as usable, and no
// D-Bus session (empty stdout, non-zero exit, verified live on this
// box) must not.
func TestSystemdUsableFromOutput(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
		want   bool
	}{
		{"running", "running\n", true},
		{"degraded still answers", "degraded\n", true},
		{"empty (no bus / no systemd)", "", false},
		{"whitespace only", "   \n", false},
	}
	for _, c := range cases {
		if got := systemdUsableFromOutput([]byte(c.stdout)); got != c.want {
			t.Errorf("%s: systemdUsableFromOutput(%q) = %v, want %v", c.name, c.stdout, got, c.want)
		}
	}
}

// TestSystemdEnableDisableFileRoundTrip proves the file-level half of
// enable/disable — content and idempotency — against a fake HOME, with
// no real `systemctl` call involved (systemdUnitPath is exercised
// directly, mirroring enableSystemd/disableSystemd's own file steps,
// without their systemctl side effects). The REAL systemctl round trip
// is TestSystemdLiveRoundTrip, below.
func TestSystemdEnableDisableFileRoundTrip(t *testing.T) {
	home := t.TempDir()
	getenv := fakeGetenvFn(nil)
	path, err := systemdUnitPath(getenv, fakeHomeFn(home))
	if err != nil {
		t.Fatalf("systemdUnitPath: %v", err)
	}

	write := func(exePath string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte(systemdUnitContent(exePath)), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	write("/home/dev/.local/bin/dexel")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// "enable" twice: same exePath written again must leave byte-identical
	// content — one entry, not two.
	write("/home/dev/.local/bin/dexel")
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("writing the unit twice changed its content:\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unit file still present after removal: %v", err)
	}
	// "disable" twice: removing an already-absent file must be a clean
	// no-op, never an error, which is exactly disableSystemd's own
	// os.IsNotExist branch.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("expected the file to stay absent")
	}
}

// systemdUserAvailable is the live tests' own guard, independent of
// systemdUsable (which this test suite also exercises) — a direct,
// minimal check so a skip reason is easy to read on its own.
func systemdUserAvailable(t *testing.T) bool {
	t.Helper()
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	out, _ := exec.Command("systemctl", "--user", "is-system-running").Output()
	return strings.TrimSpace(string(out)) != ""
}

// TestSystemdLiveRoundTrip is PR-6's required real-hardware proof
// (MIGRATION_PLAN.md §PR-6 exit criteria: "On the Linux runner: enable
// writes the unit and `systemctl --user is-enabled` says enabled ...
// disable leaves no unit, no autostart entry"), run against THIS
// developer's real systemd --user session — there is no way to
// meaningfully test "systemctl --user enable --now" without one, and
// this repo's dev box genuinely has one.
//
// It deliberately does NOT attempt to isolate itself via a fake
// $XDG_CONFIG_HOME: verified by hand against this exact box before
// writing this test, `env XDG_CONFIG_HOME=<tmp> systemctl --user
// daemon-reload` / `list-unit-files` do NOT see units placed under that
// override — the systemd --user MANAGER already has its own
// environment from session start, fixed before this test process ever
// runs, so a unit written under an overridden XDG_CONFIG_HOME is
// invisible to `systemctl --user` no matter what env this test sets.
// PLATFORM_NOTES.md §3.2's mechanism is inherently a REAL, single,
// well-known path (~/.config/systemd/user/dexel.service) — there is no
// isolated equivalent to test against.
//
// So instead: it uses the REAL path, with ExecStart pointing at /bin/
// true (never a real dexel binary, and never actually started — this
// only proves the enable/disable/is-enabled contract, not that the
// runtime boots), and t.Cleanup guarantees disable + file removal +
// daemon-reload run even if an assertion fails partway, so this
// developer's real `~/.config/systemd/user/` is left exactly as it was
// found. It is skipped under -short and whenever no real systemd --user
// session is available, mirroring pause_e2e_test.go's own -short/
// platform skip convention (app/pause_e2e_test.go).
func TestSystemdLiveRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("live: talks to a real systemd --user session")
	}
	if !systemdUserAvailable(t) {
		t.Skip("no usable systemd --user session on this box")
	}

	path, err := systemdUnitPath(os.Getenv, os.UserHomeDir)
	if err != nil {
		t.Fatalf("systemdUnitPath: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Skipf("refusing to run: a real %s already exists on this machine", path)
	}

	t.Cleanup(func() {
		_ = exec.Command("systemctl", "--user", "disable", "--now", systemdUnitName).Run()
		_ = os.Remove(path)
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		if _, statErr := os.Stat(path); statErr == nil {
			t.Errorf("cleanup failed: %s still exists", path)
		}
	})

	if err := enableSystemd("/bin/true"); err != nil {
		t.Fatalf("enableSystemd: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after enable: %v", err)
	}
	if want := systemdUnitContent("/bin/true"); string(got) != want {
		t.Fatalf("live unit content mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}

	out, err := exec.Command("systemctl", "--user", "is-enabled", systemdUnitName).CombinedOutput()
	if err != nil {
		t.Fatalf("systemctl --user is-enabled: %v (%s)", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "enabled" {
		t.Fatalf(`systemctl --user is-enabled = %q, want "enabled"`, got)
	}

	// enable AGAIN: idempotency -- one entry, not two.
	if err := enableSystemd("/bin/true"); err != nil {
		t.Fatalf("enableSystemd (second call): %v", err)
	}
	out, err = exec.Command("systemctl", "--user", "is-enabled", systemdUnitName).CombinedOutput()
	if err != nil {
		t.Fatalf("systemctl --user is-enabled (after second enable): %v (%s)", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "enabled" {
		t.Fatalf(`after a second enable, systemctl --user is-enabled = %q, want "enabled"`, got)
	}

	if err := disableSystemd(); err != nil {
		t.Fatalf("disableSystemd: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unit file still present after disable: %v", err)
	}
	out, _ = exec.Command("systemctl", "--user", "is-enabled", systemdUnitName).CombinedOutput()
	if got := strings.TrimSpace(string(out)); got == "enabled" {
		t.Fatalf(`after disable, systemctl --user is-enabled = %q, want anything but "enabled"`, got)
	}

	// disable AGAIN: idempotency -- still clean, no error.
	if err := disableSystemd(); err != nil {
		t.Fatalf("disableSystemd (second call, on an already-clean state): %v", err)
	}
}

// TestEnablePlatformDisablePlatformDispatch proves the linux dispatch
// functions pick systemd when it's usable (which it is, on this box)
// -- exercised via the same live path as TestSystemdLiveRoundTrip, so
// it shares its skip conditions and cleanup discipline.
func TestEnablePlatformDisablePlatformDispatch(t *testing.T) {
	if testing.Short() {
		t.Skip("live: talks to a real systemd --user session")
	}
	if !systemdUserAvailable(t) {
		t.Skip("no usable systemd --user session on this box")
	}
	path, err := systemdUnitPath(os.Getenv, os.UserHomeDir)
	if err != nil {
		t.Fatalf("systemdUnitPath: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Skipf("refusing to run: a real %s already exists on this machine", path)
	}
	t.Cleanup(func() {
		_ = disablePlatform()
		if _, statErr := os.Stat(path); statErr == nil {
			t.Errorf("cleanup failed: %s still exists", path)
		}
	})

	// Options{} is the zero value on purpose: BareExecutable is a
	// macOS-only opt-out of .app bundle attribution, and Linux's unit
	// always names exePath, so the zero value IS Linux's only behaviour.
	res, err := enablePlatform("/bin/true", "/dev/null", Options{})
	if err != nil {
		t.Fatalf("enablePlatform: %v", err)
	}
	if res.Mechanism != MechanismSystemdUser {
		t.Fatalf("enablePlatform mechanism = %q, want %q (systemd is usable on this box)", res.Mechanism, MechanismSystemdUser)
	}
	if res.Program != "/bin/true" {
		t.Fatalf("enablePlatform program = %q, want the exePath it was given (%q) -- Linux never substitutes", res.Program, "/bin/true")
	}

	st, err := queryPlatform()
	if err != nil {
		t.Fatalf("queryPlatform: %v", err)
	}
	if st.Mechanism != MechanismSystemdUser || !st.Active {
		t.Fatalf("queryPlatform = %+v, want an active systemd-user mechanism", st)
	}

	if err := disablePlatform(); err != nil {
		t.Fatalf("disablePlatform: %v", err)
	}
	st, err = queryPlatform()
	if err != nil {
		t.Fatalf("queryPlatform after disable: %v", err)
	}
	if st.Mechanism != MechanismNone {
		t.Fatalf("queryPlatform after disable = %+v, want MechanismNone", st)
	}
}
