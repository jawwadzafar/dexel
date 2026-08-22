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
