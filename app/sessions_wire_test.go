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
	if !popEndedSession(g, hub, savePath, &sessionAppendQueue{}) {
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
	if popEndedSession(g, hub, savePath, &sessionAppendQueue{}) {
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

// TestAFailedAppendKeepsTheRecordQueuedAndTheLogContiguous is B-3's
// regression test (docs/plan/REVIEW-2026-08-22.md), built from the
// review's own reproduction.
//
// The old failure: popEndedSession took the finished record out of the
// game, failed to append it, logged "could not save session" and dropped
// it. The record stayed in the in-memory log, so the NEXT session's id
// (len(sessionLog)+1) was one past what the DB would accept, and the row
// that eventually landed left a gap. The session-log loader treats a gap
// as tampering — the review proved that consequence directly: rows 1 and
// 3 produce "sessions row id 3 out of sequence (want 2)", the whole
// state.db is quarantined and the economy is wiped. One transient ENOSPC,
// and the next launch accuses the user of cheating.
//
// What this pins now: the append failure is transient, the record stays
// owed, a second session finishes while the first is still owed, and when
// the disk comes back BOTH land, in order, as rows 1 and 2 — so a fresh
// store.LoadAll verifies clean instead of quarantining.
//
// The forced failure is a directory sitting where state.db belongs:
// modernc.org/sqlite cannot open it, which is exactly the shape of the
// real-world failures (ENOSPC, a locked DB, a permissions oddity) without
// needing any of them.
func TestAFailedAppendKeepsTheRecordQueuedAndTheLogContiguous(t *testing.T) {
	dir := t.TempDir()
	savePath := filepath.Join(dir, "state.db")
	if err := os.Mkdir(savePath, 0o755); err != nil {
		t.Fatalf("Mkdir (the forced append failure): %v", err)
	}

	g := game.New()
	clock := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	g.SetClockForTest(func() time.Time { return clock })
	hub := newHub(nil, false)
	q := &sessionAppendQueue{}

	runSession := func() {
		t.Helper()
		if mutated, flash := applyAction(g, actionMessage{Action: actionSessionStart}, 1); !mutated {
			t.Fatalf("SESSION_START did not mutate (flash=%+v)", flash)
		}
		clock = clock.Add(90 * time.Second)
		if mutated, flash := applyAction(g, actionMessage{Action: actionSessionStop}, 1); !mutated {
			t.Fatalf("SESSION_STOP did not mutate (flash=%+v)", flash)
		}
	}

	// --- Session 1: finishes, cannot be persisted ------------------------
	runSession()
	if !popEndedSession(g, hub, savePath, q) {
		t.Fatal("popEndedSession reported no fresh record after a >=60s stop")
	}
	if len(q.pending) != 1 {
		t.Fatalf("queued records = %d, want 1 — a failed append must KEEP the record, not drop it", len(q.pending))
	}
	if g.SessionLogHead() != "" {
		t.Error("SessionLogHead advanced for an append that failed")
	}
	if g.SessionLogPersistedID() != 0 {
		t.Errorf("SessionLogPersistedID = %d, want 0 — nothing has been persisted yet", g.SessionLogPersistedID())
	}

	// --- Session 2: finishes while the first is still owed ---------------
	runSession()
	popEndedSession(g, hub, savePath, q)
	if len(q.pending) != 2 {
		t.Fatalf("queued records = %d, want 2", len(q.pending))
	}
	ids := []int{}
	for _, rec := range g.SessionLogSnapshot() {
		ids = append(ids, rec.ID)
	}
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("session ids = %v, want [1 2] — an unpersisted record must not consume the next id twice or skip one", ids)
	}

	// --- The disk comes back ---------------------------------------------
	if err := os.Remove(savePath); err != nil {
		t.Fatalf("Remove (repairing the save path): %v", err)
	}
	if popEndedSession(g, hub, savePath, q) {
		t.Error("popEndedSession reported a fresh record when only the retry ran")
	}
	if len(q.pending) != 0 {
		t.Fatalf("queued records = %d after the disk came back, want 0 — the whole backlog must drain in order", len(q.pending))
	}
	if g.SessionLogPersistedID() != 2 {
		t.Errorf("SessionLogPersistedID = %d, want 2", g.SessionLogPersistedID())
	}

	// --- The next boot must load clean, NOT quarantine -------------------
	data, sessions, ok, err := store.LoadAll(savePath)
	if err != nil {
		t.Fatalf("LoadAll after the retried appends: %v — this is the false-tamper wipe B-3 describes", err)
	}
	if !ok {
		t.Fatal("LoadAll reported no save after two appended sessions")
	}
	if len(sessions) != 2 || sessions[0].ID != 1 || sessions[1].ID != 2 {
		t.Fatalf("persisted session ids = %+v, want rows 1 and 2 with no gap", sessions)
	}
	if data.SessionLogHead != g.SessionLogHead() {
		t.Errorf("signed head %q != live head %q", data.SessionLogHead, g.SessionLogHead())
	}
	if _, statErr := os.Stat(savePath + ".invalid"); !os.IsNotExist(statErr) {
		t.Error("state.db was quarantined — the save was destroyed by a transient append failure, which is exactly B-3")
	}

	// And the next session after a restart continues the sequence at 3.
	g2 := game.New()
	recs, err := store.SessionRecordsFromSave(sessions)
	if err != nil {
		t.Fatalf("SessionRecordsFromSave: %v", err)
	}
	g2.RestoreSessionLog(recs)
	if err := g2.StartSession(""); err != nil {
		t.Fatalf("StartSession after restore: %v", err)
	}
	active, _ := g2.ActiveSession()
	if active.ID != 3 {
		t.Errorf("the first session id after a restore of rows 1-2 = %d, want 3", active.ID)
	}
}

// TestNoSaveMeansAFreshStartEvenWithALegacyRustSavePresent is B-2's
// regression test: the legacy-Rust import is gone, so a fabricated
// ~/.local/share/dev-companion/save.json — the file that used to mint an
// unbounded wallet on every boot with no save.db present — now grants
// nothing at all, and loadOrImport reports the honest fresh install.
func TestNoSaveMeansAFreshStartEvenWithALegacyRustSavePresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacyDir := filepath.Join(home, ".local", "share", "dev-companion")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	legacy := `{"wallet":18446744073709551615,"xp":9999,"level":99,` +
		`"upgrades":{"chair":9,"keyboard":9,"mouse":9,"pet":9,"wall":9,"desk_decor":9,"monitor":9}}`
	if err := os.WriteFile(filepath.Join(legacyDir, "save.json"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	g := game.New()
	fresh := game.New()
	savePath := filepath.Join(t.TempDir(), "state.db")

	if found := loadOrImport(g, savePath); found {
		t.Error("loadOrImport reported an existing save — a legacy Rust save must no longer count as one")
	}
	if g.DevCash != fresh.DevCash {
		t.Errorf("DevCash = %d, want the fresh-install default %d — nothing may be minted from an unsigned file", g.DevCash, fresh.DevCash)
	}
	if len(g.OwnedItems) != len(fresh.OwnedItems) {
		t.Errorf("owned items = %d, want the fresh-install %d", len(g.OwnedItems), len(fresh.OwnedItems))
	}
	if _, err := os.Stat(savePath); !os.IsNotExist(err) {
		t.Error("a state.db was written on a fresh-install boot with no save")
	}
}
