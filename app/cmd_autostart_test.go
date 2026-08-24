package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/autostart"
	"github.com/jawwadzafar/dexel/app/internal/store"
)

// TestCmdAutostartRequiresSubcommand and TestCmdAutostartRejectsUnknown
// prove `dexel autostart` and `dexel autostart bogus` fail loudly
// (exit 2) rather than doing nothing or falling through to enable —
// the same "an unknown word is an error, never a silent fall-through"
// posture cli.go's dispatchUnknown branch already applies one level up.
func TestCmdAutostartRequiresSubcommand(t *testing.T) {
	if got := cmdAutostart(nil); got != 2 {
		t.Fatalf("cmdAutostart(nil) = %d, want 2", got)
	}
}

func TestCmdAutostartRejectsUnknownSubcommand(t *testing.T) {
	if got := cmdAutostart([]string{"bogus"}); got != 2 {
		t.Fatalf(`cmdAutostart(["bogus"]) = %d, want 2`, got)
	}
}

// TestRecordAutostartMechanismPreservesOtherFields proves
// recordAutostartMechanism is a load-modify-save (SEC-1 design), not a
// fresh literal: Name and SessionNames set by something else entirely
// must survive both an enable-record and a disable-record, against a
// fake DEXEL_HOME — never this developer's real config.json.
func TestRecordAutostartMechanismPreservesOtherFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEXEL_HOME", home)

	cfgPath, err := store.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	if cfgPath != filepath.Join(home, "config.json") {
		t.Fatalf("ConfigPath = %q, want it under the fake DEXEL_HOME %q -- test is not isolated", cfgPath, home)
	}

	seed := store.ConfigData{Name: "Widget", SessionNames: map[string]string{"1": "project-x"}}
	if err := store.SaveConfig(cfgPath, seed); err != nil {
		t.Fatalf("seed SaveConfig: %v", err)
	}

	if err := recordAutostartMechanism(autostart.MechanismSystemdUser); err != nil {
		t.Fatalf("recordAutostartMechanism(enable): %v", err)
	}
	cfg, err := store.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Autostart != string(autostart.MechanismSystemdUser) {
		t.Fatalf("cfg.Autostart = %q, want %q", cfg.Autostart, autostart.MechanismSystemdUser)
	}
	if cfg.Name != "Widget" || cfg.SessionNames["1"] != "project-x" {
		t.Fatalf("recordAutostartMechanism(enable) clobbered other fields: %+v", cfg)
	}

	// Idempotent: recording the same mechanism twice changes nothing
	// else and leaves the same value.
	if err := recordAutostartMechanism(autostart.MechanismSystemdUser); err != nil {
		t.Fatalf("recordAutostartMechanism(enable, second call): %v", err)
	}
	cfg2, err := store.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !reflect.DeepEqual(cfg2, cfg) {
		t.Fatalf("recording the same mechanism twice changed the config: first=%+v second=%+v", cfg, cfg2)
	}

	// disable's record: back to "", other fields still untouched.
	if err := recordAutostartMechanism(autostart.MechanismNone); err != nil {
		t.Fatalf("recordAutostartMechanism(disable): %v", err)
	}
	cfg3, err := store.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg3.Autostart != "" {
		t.Fatalf("cfg.Autostart after disable = %q, want \"\"", cfg3.Autostart)
	}
	if cfg3.Name != "Widget" || cfg3.SessionNames["1"] != "project-x" {
		t.Fatalf("recordAutostartMechanism(disable) clobbered other fields: %+v", cfg3)
	}
}

// TestAutostartEnableHasExactlyOneCallSite is the consent rule
// (PLATFORM_NOTES.md §3, ARCHITECTURE.md's consent posture) enforced
// structurally rather than only by review: autostart.Enable must be
// reachable from nowhere in this binary except the explicit `enable`
// verb below. A second call site anywhere else in app/ would mean
// something other than the user's own typed command could turn
// autostart on.
func TestAutostartEnableHasExactlyOneCallSite(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	callSites := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", e.Name(), err)
		}
		callSites += strings.Count(string(data), "autostart.Enable(")
	}
	if callSites != 1 {
		t.Fatalf("found %d call site(s) of autostart.Enable( in app/*.go, want exactly 1 (cmd_autostart.go's cmdAutostartEnable)", callSites)
	}
}

// TestAutostartEnableReportsTheWrittenProgramNotTheRequestedOne is the
// honesty property of `enable`'s output, enforced structurally because the
// alternative — actually calling cmdAutostartEnable — would install a real
// launchd job on whatever machine runs `go test`, which this suite must
// never do.
//
// The point: on macOS, autostart.Enable may write an installed
// Dexel.app's own executable into the plist instead of the env.self it was
// handed (app/internal/autostart/autostart.go's launchdProgram explains
// why). Printing env.self would then be a confident lie about what is on
// disk. So cmdAutostartEnable must print autostart.Result.Program — the
// path that was really written — and must not print env.self at all.
func TestAutostartEnableReportsTheWrittenProgramNotTheRequestedOne(t *testing.T) {
	src, err := os.ReadFile("cmd_autostart.go")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	body := enableFuncBody(t, string(src))

	// Only the OUTPUT lines are inspected. env.self legitimately appears
	// elsewhere in the function -- it is what gets PASSED to
	// autostart.Enable, as the request -- and in the comment explaining
	// this very distinction. It just must never be what gets printed.
	printed := printLines(body)
	if len(printed) == 0 {
		t.Fatal("found no fmt.Fprint* lines in cmdAutostartEnable -- this test needs updating")
	}
	if !containsAny(printed, "res.Program") {
		t.Fatal("no printed line in cmdAutostartEnable mentions res.Program: `enable` must report the path autostart actually wrote, not the one it asked for")
	}
	if containsAny(printed, "env.self") {
		t.Fatal("cmdAutostartEnable prints env.self: on macOS the plist may name a different executable, so echoing the request back is a lie about what is on disk")
	}
	// res.Note carries "why that program" (bundle / no bundle / --bare) and
	// is the user's only in-CLI explanation of a substitution they did not
	// ask for. Dropping it would make the substitution silent.
	if !containsAny(printed, "res.Note") {
		t.Fatal("cmdAutostartEnable does not print res.Note: a program substitution the user did not ask for must never be silent")
	}
}

// enableFuncBody returns the source text of cmdAutostartEnable, so the test
// above asserts about that function rather than the whole file (where
// `env.self` could legitimately appear in a comment or another verb).
func enableFuncBody(t *testing.T, src string) string {
	t.Helper()
	const sig = "func cmdAutostartEnable(args []string) int {"
	i := strings.Index(src, sig)
	if i < 0 {
		t.Fatalf("could not find %q in cmd_autostart.go -- this test needs updating alongside the rename", sig)
	}
	rest := src[i+len(sig):]
	// The function ends at the first line that is exactly "}" — gofmt
	// guarantees the closing brace of a top-level func is in column 0.
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatal("could not find the end of cmdAutostartEnable")
	}
	return rest[:end]
}

// printLines is every fmt.Fprint*-shaped line in a function body -- the
// only lines that decide what the user is TOLD, as opposed to what the
// function computes.
func printLines(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.Contains(trimmed, "fmt.Fprint") {
			out = append(out, trimmed)
		}
	}
	return out
}

func containsAny(lines []string, needle string) bool {
	for _, l := range lines {
		if strings.Contains(l, needle) {
			return true
		}
	}
	return false
}
