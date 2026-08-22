// sessions_wire_test.go is P2's server-wiring test suite (docs/plan/
// P2-design.md §7.4, ADR 0017), modelled on identity_wire_test.go: the
// applyAction contract for SESSION_START/SESSION_STOP, the Fork P2-A
// privacy proof restated at the point a session name actually exists, the
// wire shape of the `sessions` block and the `sessionComplete` envelope,
// and the write-through-immediately persistence guarantee SESSION_STOP
// shares with SET_NAME.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/game"
	"github.com/jawwadzafar/dexel/app/internal/store"
)

// TestApplyActionSessionStartStop covers SESSION_START/SESSION_STOP's
// server-side contract (docs/plan/P2-design.md §2.2, §6.2; docs/ui-spec.md
// §9.6): a table over applyAction asserting (mutated, flash.Kind) for
// every case §7.4 names.
func TestApplyActionSessionStartStop(t *testing.T) {
	cases := []struct {
		name        string
		setup       func(t *testing.T, g *game.Game)
		msg         actionMessage
		wantMutated bool
		wantKind    string // "" means "no flash at all"
	}{
		{
			name:        "start unnamed",
			msg:         actionMessage{Action: actionSessionStart},
			wantMutated: true,
			wantKind:    "session",
		},
		{
			name:        "start named",
			msg:         actionMessage{Action: actionSessionStart, Name: "auth refactor"},
			wantMutated: true,
			wantKind:    "session",
		},
		{
			name: "start while active is rejected",
			setup: func(t *testing.T, g *game.Game) {
				if err := g.StartSession(""); err != nil {
					t.Fatalf("setup StartSession: %v", err)
				}
			},
			msg:         actionMessage{Action: actionSessionStart},
			wantMutated: false,
			wantKind:    "error",
		},
		{
			name: "stop",
			setup: func(t *testing.T, g *game.Game) {
				if err := g.StartSession(""); err != nil {
					t.Fatalf("setup StartSession: %v", err)
				}
			},
			msg: actionMessage{Action: actionSessionStop},
			// applyAction itself returns NO flash for a successful STOP —
			// the "too short to keep" vs. the real sessionComplete
			// celebration is decided by main's action-loop AFTER popping
			// g.TakeEndedSession(), not here (see popEndedSession).
			wantMutated: true,
			wantKind:    "",
		},
		{
			name:        "stop with none active is rejected",
			msg:         actionMessage{Action: actionSessionStop},
			wantMutated: false,
			wantKind:    "error",
		},
		{
			name:        "a name with control characters is normalized, not rejected",
			msg:         actionMessage{Action: actionSessionStart, Name: "auth\x00\x01 refactor"},
			wantMutated: true,
			wantKind:    "session",
		},
		{
			name:        "a 40-rune name is truncated to 32, not rejected",
			msg:         actionMessage{Action: actionSessionStart, Name: strings.Repeat("z", 40)},
			wantMutated: true,
			wantKind:    "session",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := game.New()
			if tc.setup != nil {
				tc.setup(t, g)
			}
			mutated, flash := applyAction(g, tc.msg, 1)
			if mutated != tc.wantMutated {
				t.Fatalf("mutated = %v, want %v (flash=%+v)", mutated, tc.wantMutated, flash)
			}
			gotKind := ""
			if flash != nil {
				gotKind = flash.Kind
			}
			if gotKind != tc.wantKind {
				t.Fatalf("flash kind = %q, want %q (flash=%+v)", gotKind, tc.wantKind, flash)
			}
		})
	}
}

// TestSessionNameNeverReachesTheProtectedSave is the Fork P2-A proof
// (docs/plan/P2-design.md §2.7, ADR 0017 Decision 2), the direct analogue
// of identity_wire_test.go's TestSetNameNeverReachesTheProtectedSave: a
// project name must be absent from BOTH store.Snapshot(g) (the signed
// state.db row) and the appended sessions row's own payload — the name
// belongs to config.json alone.
func TestSessionNameNeverReachesTheProtectedSave(t *testing.T) {
	const name = "Top Secret Project Zorblatt"

	g := game.New()
	clock := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	g.SetClockForTest(func() time.Time { return clock })

	if mutated, flash := applyAction(g, actionMessage{Action: actionSessionStart, Name: name}, 1); !mutated {
		t.Fatalf("SESSION_START did not mutate (flash=%+v)", flash)
	}
	if got := g.SessionNames(); len(got) != 1 {
		t.Fatalf("SessionNames() = %v, want exactly one entry", got)
	}

	clock = clock.Add(90 * time.Second) // >= game.SessionMinDurationSeconds
	if mutated, flash := applyAction(g, actionMessage{Action: actionSessionStop}, 1); !mutated {
		t.Fatalf("SESSION_STOP did not mutate (flash=%+v)", flash)
	}
	rec, ok := g.TakeEndedSession()
	if !ok {
		t.Fatal("SESSION_STOP produced no completed record for a 90s session")
	}

	snapRaw, err := json.Marshal(store.Snapshot(g))
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	payloadRaw, err := json.Marshal(store.SessionSaveFromRecord(rec))
	if err != nil {
		t.Fatalf("marshal session payload: %v", err)
	}

	for _, shape := range []struct {
		label string
		raw   []byte
	}{
		{"the protected SaveData snapshot", snapRaw},
		{"the appended sessions row's own payload", payloadRaw},
	} {
		if strings.Contains(string(shape.raw), name) {
			t.Fatalf("the session name leaked into %s:\n%s", shape.label, shape.raw)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(shape.raw, &fields); err != nil {
			t.Fatalf("unmarshal %s: %v", shape.label, err)
		}
		for k := range fields {
			if strings.Contains(strings.ToLower(k), "name") {
				t.Fatalf("%s grew a name-shaped field %q — the name belongs in config.json (ADR 0017 Decision 2):\n%s", shape.label, k, shape.raw)
			}
		}
	}
}

// TestSessionShowsOnTheWire proves the round trip a client actually sees:
// a fresh game always sends the `sessions` block (§6.1's "the server
// always sends the block, it may be empty" precedent), and SESSION_START
// puts the normalized name and id straight onto `sessions.active` under
// the exact camelCase tags docs/plan/P2-design.md §6.1 pins.
func TestSessionShowsOnTheWire(t *testing.T) {
	fresh, err := json.Marshal(game.New().State())
	if err != nil {
		t.Fatalf("marshal fresh state: %v", err)
	}
	if !strings.Contains(string(fresh), `"sessions":{"active":null`) {
		t.Fatalf(`a fresh state is missing "sessions":{"active":null,...}: %s`, fresh)
	}

	g := game.New()
	if mutated, flash := applyAction(g, actionMessage{Action: actionSessionStart, Name: "auth refactor"}, 1); !mutated {
		t.Fatalf("SESSION_START did not mutate (flash=%+v)", flash)
	}

	raw, err := json.Marshal(g.State())
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	var wire struct {
		Sessions struct {
			Active *struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"active"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	if wire.Sessions.Active == nil {
		t.Fatalf(`state["sessions"]["active"] is still null after SESSION_START: %s`, raw)
	}
	if wire.Sessions.Active.ID != 1 {
		t.Fatalf(`state["sessions"]["active"]["id"] = %d, want 1`, wire.Sessions.Active.ID)
	}
	if wire.Sessions.Active.Name != "auth refactor" {
		t.Fatalf(`state["sessions"]["active"]["name"] = %q, want "auth refactor"`, wire.Sessions.Active.Name)
	}
}

// TestSessionCompleteMessageShape pins the `sessionComplete` envelope's
// literal JSON shape (docs/plan/P2-design.md §3.1/§6.1, docs/ui-spec.md
// §9.6): `{"type":"sessionComplete","v":1,"session":{...}}`, sent by
// Hub.broadcastSessionComplete.
func TestSessionCompleteMessageShape(t *testing.T) {
	msg := sessionCompleteMessage{
		Type: "sessionComplete",
		V:    1,
		Session: game.SessionView{
			ID:              7,
			Name:            "auth refactor",
			StartedAt:       "2026-01-01T09:00:00Z",
			EndedAt:         "2026-01-01T10:24:00Z",
			DurationSeconds: 5040,
			Keystrokes:      4182,
			EndReason:       "user",
		},
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"type", "v", "session"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("sessionComplete envelope missing %q: %s", key, raw)
		}
	}
	if !strings.Contains(string(raw), `"type":"sessionComplete"`) {
		t.Fatalf(`envelope missing "type":"sessionComplete": %s`, raw)
	}
	if !strings.Contains(string(raw), `"v":1`) {
		t.Fatalf(`envelope missing "v":1: %s`, raw)
	}
	var session struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(fields["session"], &session); err != nil {
		t.Fatalf("unmarshal nested session: %v", err)
	}
	if session.ID != 7 || session.Name != "auth refactor" {
		t.Fatalf("nested session = %+v, want id=7 name=%q", session, "auth refactor")
	}
}

// TestSessionStopPersistsImmediately proves SESSION_STOP shares SET_NAME's
// write-through urgency (docs/plan/P2-design.md §2.2 step 5/§5.5): a
// completed (>=60s) session's mtime on state.db advances the instant it is
// popped and appended — via popEndedSession, the exact function main's
// select loop calls — never waiting for the 30s autosave.
func TestSessionStopPersistsImmediately(t *testing.T) {
	dir := t.TempDir()
	savePath := filepath.Join(dir, "state.db")

	g := game.New()
	clock := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	g.SetClockForTest(func() time.Time { return clock })

	if err := store.Save(savePath, store.Snapshot(g)); err != nil {
		t.Fatalf("initial Save: %v", err)
	}
	before, err := os.Stat(savePath)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	if mutated, flash := applyAction(g, actionMessage{Action: actionSessionStart}, 1); !mutated {
		t.Fatalf("SESSION_START did not mutate (flash=%+v)", flash)
	}
	clock = clock.Add(90 * time.Second)
	if mutated, flash := applyAction(g, actionMessage{Action: actionSessionStop}, 1); !mutated {
		t.Fatalf("SESSION_STOP did not mutate (flash=%+v)", flash)
	}

	// A filesystem mtime's resolution can be coarser than this test's own
	// wall-clock gap between the two os.Stat calls; sleeping a tick makes a
	// real advance unambiguous rather than a false negative from two
	// stats landing in the same tick.
	time.Sleep(10 * time.Millisecond)

	hub := newHub(nil, false)
	if !popEndedSession(g, hub, savePath) {
		t.Fatal("popEndedSession popped nothing after a >=60s SESSION_STOP")
	}

	after, err := os.Stat(savePath)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !after.ModTime().After(before.ModTime()) {
		t.Fatalf("state.db mtime did not advance on SESSION_STOP: before=%v after=%v", before.ModTime(), after.ModTime())
	}

	// The chain head handed back must also be live on g, not just on disk
	// — SetSessionLogHead is what keeps the NEXT AppendSession's chain
	// anchored correctly.
	if g.SessionLogHead() == "" {
		t.Fatal("g.SessionLogHead() is still empty after popEndedSession appended a row")
	}
}

// TestPopEndedSessionDiscardsShortSessions covers Fork P2-E end to end at
// this layer: a session stopped under game.SessionMinDurationSeconds
// produces no poppable record (popEndedSession returns false) and leaves
// state.db entirely untouched — no sessions row, no mtime change — which
// is what lets main's action-loop tell "too short to keep" apart from a
// real completion.
func TestPopEndedSessionDiscardsShortSessions(t *testing.T) {
	dir := t.TempDir()
	savePath := filepath.Join(dir, "state.db")

	g := game.New()
	clock := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	g.SetClockForTest(func() time.Time { return clock })

	if err := store.Save(savePath, store.Snapshot(g)); err != nil {
		t.Fatalf("initial Save: %v", err)
	}
	before, err := os.Stat(savePath)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	if mutated, flash := applyAction(g, actionMessage{Action: actionSessionStart}, 1); !mutated {
		t.Fatalf("SESSION_START did not mutate (flash=%+v)", flash)
	}
	clock = clock.Add(5 * time.Second) // well under the 60s minimum
	if mutated, flash := applyAction(g, actionMessage{Action: actionSessionStop}, 1); !mutated {
		t.Fatalf("SESSION_STOP did not mutate (flash=%+v)", flash)
	}

	hub := newHub(nil, false)
	if popEndedSession(g, hub, savePath) {
		t.Fatal("popEndedSession popped a record for a <60s session — Fork P2-E requires a silent discard")
	}
	if g.SessionLogHead() != "" {
		t.Fatalf("SessionLogHead() = %q, want empty — a discarded session must never touch the chain", g.SessionLogHead())
	}

	after, err := os.Stat(savePath)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("state.db was touched by a discarded (<60s) session: before=%v after=%v", before.ModTime(), after.ModTime())
	}
}
