package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/game"
	"github.com/jawwadzafar/dexel/app/internal/store"
)

// TestApplyActionSetName covers SET_NAME's server-side contract
// (docs/ui-spec.md §6.2 / §7): a valid name mutates and answers with a
// welcome flash, an invalid one is an error flash with NO mutation.
func TestApplyActionSetName(t *testing.T) {
	cases := []struct {
		name        string
		payload     string
		wantMutated bool
		wantName    string
		wantKind    string
	}{
		{"plain name", "Pixel", true, "Pixel", "welcome"},
		{"trimmed", "  Pixel  ", true, "Pixel", "welcome"},
		{"control chars stripped", "Pix\nel", true, "Pixel", "welcome"},
		{"over-long is truncated, not rejected", strings.Repeat("z", 40), true, strings.Repeat("z", game.MaxNameLen), "welcome"},
		{"the SKIP default", game.DefaultName, true, game.DefaultName, "welcome"},
		{"empty is rejected", "", false, "", "error"},
		{"whitespace only is rejected", "   ", false, "", "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := game.New()
			g.SetOnboarding(true)

			mutated, flash := applyAction(g, actionMessage{Action: actionSetName, Name: tc.payload}, 1)
			if mutated != tc.wantMutated {
				t.Fatalf("mutated = %v, want %v", mutated, tc.wantMutated)
			}
			if g.ConfigName() != tc.wantName {
				t.Fatalf("ConfigName() = %q, want %q", g.ConfigName(), tc.wantName)
			}
			if flash == nil {
				t.Fatal("SET_NAME produced no flash; §6.2 requires one on success AND on failure")
			}
			if flash.Kind != tc.wantKind {
				t.Fatalf("flash kind = %q, want %q", flash.Kind, tc.wantKind)
			}
			// The onboarding flag must go down exactly when the name goes in.
			if got, want := g.Onboarding(), !tc.wantMutated; got != want {
				t.Fatalf("Onboarding() = %v, want %v", got, want)
			}
			if tc.wantMutated && !strings.Contains(flash.Text, tc.wantName) {
				t.Fatalf("welcome flash %q does not greet the new name %q", flash.Text, tc.wantName)
			}
		})
	}
}

// TestSetNameNeverReachesTheProtectedSave is the privacy/integrity half
// of Phase P1 and the reason the name lives in config.json at all (ADR
// 0014's config/state split): the dexel's name is user-authored config,
// so it must be absent from store.Snapshot — the payload that gets HMAC'd
// into state.db. A regression here would put free text inside the
// protected save, which internal/store's own content-free allow-list
// exists to forbid; this test catches it from the OTHER side, at the
// point where a name actually exists.
func TestSetNameNeverReachesTheProtectedSave(t *testing.T) {
	const name = "Zaphod"

	g := game.New()
	g.SetOnboarding(true)
	if mutated, _ := applyAction(g, actionMessage{Action: actionSetName, Name: name}, 1); !mutated {
		t.Fatal("SET_NAME did not mutate")
	}
	if g.ConfigName() != name {
		t.Fatalf("ConfigName() = %q, want %q", g.ConfigName(), name)
	}

	raw, err := json.Marshal(store.Snapshot(g))
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if strings.Contains(string(raw), name) {
		t.Fatalf("the dexel's name leaked into the protected SaveData:\n%s", raw)
	}
	// Belt and braces: no "name" KEY either, however it might be spelled.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	for k := range fields {
		if strings.Contains(strings.ToLower(k), "name") {
			t.Fatalf("SaveData grew a name-shaped field %q — the name belongs in config.json (ADR 0014)", k)
		}
	}
}

// TestSetNameShowsOnTheWire proves the round trip a client actually sees:
// SET_NAME, then the very next `state` broadcast carries the name and a
// cleared onboarding flag, under the exact camelCase tags
// docs/ui-spec.md §6.1 documents.
func TestSetNameShowsOnTheWire(t *testing.T) {
	g := game.New()
	g.SetOnboarding(true)
	applyAction(g, actionMessage{Action: actionSetName, Name: "Pixel"}, 1)

	raw, err := json.Marshal(g.State())
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	var wire struct {
		Config struct {
			Name string `json:"name"`
		} `json:"config"`
		Onboarding bool `json:"onboarding"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	if wire.Config.Name != "Pixel" {
		t.Fatalf(`state["config"]["name"] = %q, want "Pixel" (exact tags: config, name)`, wire.Config.Name)
	}
	if wire.Onboarding {
		t.Fatal(`state["onboarding"] is still true after SET_NAME`)
	}
	// A fresh, unnamed game must send the block with an EMPTY name rather
	// than omitting it — app/frontend/src/wire.ts types it optional for a
	// stale server, but this server always sends it.
	fresh, err := json.Marshal(game.New().State())
	if err != nil {
		t.Fatalf("marshal fresh state: %v", err)
	}
	// The whole block, verbatim: SET-1 (docs/ui-spec.md §11) added two
	// preference fields alongside the name, and both must be present and
	// FALSE on a fresh game — that default is the feature, not an
	// implementation detail (an on-top window nobody asked for, and away
	// time shown to a user who never chose to see it, are exactly what
	// SET-1 exists to stop).
	if !strings.Contains(string(fresh), `"config":{"name":"","alwaysOnTop":false,"showAwayTime":false}`) {
		t.Fatalf(`a fresh state is missing the empty config block: %s`, fresh)
	}
	if !strings.Contains(string(fresh), `"onboarding":false`) {
		t.Fatalf(`a fresh state is missing "onboarding":false: %s`, fresh)
	}
}

// TestSetNameIsRejectedWithoutAName guards the one wire-level mistake a
// client can make cheaply: sending SET_NAME with no `name` key at all.
func TestSetNameMissingFieldIsRejected(t *testing.T) {
	var msg actionMessage
	if err := json.Unmarshal([]byte(`{"action":"SET_NAME"}`), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	g := game.New()
	g.SetOnboarding(true)
	mutated, flash := applyAction(g, msg, 1)
	if mutated {
		t.Fatal("SET_NAME with no name field mutated the game")
	}
	if flash == nil || flash.Kind != "error" {
		t.Fatalf("want an error flash, got %+v", flash)
	}
	if !g.Onboarding() {
		t.Fatal("a rejected SET_NAME cleared the onboarding flag")
	}
}
