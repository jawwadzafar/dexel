package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/game"
)

// TestSchema6GrandfatherLoadsAsNotPausedWithNoPausedSeconds is
// MIGRATION_PLAN.md §PR-5's migration criterion, first half: "a schema-6
// save loads with paused=false".
//
// This is the whole of the 6 -> 7 migration: a schema-6 payload has
// neither the `paused` key nor any `pausedSeconds` key, json.Unmarshal
// leaves them at their zero values, and those zero values ARE the honest
// answer for a save written by a build that had no pause feature. Nothing
// is backfilled — in particular no recorded IdleSeconds is retroactively
// reinterpreted as paused time, which would be fabrication dressed up as
// migration.
func TestSchema6GrandfatherLoadsAsNotPausedWithNoPausedSeconds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	// The fixture is a HAND-BUILT, byte-exact pre-PR-5 payload rather than
	// `Save(SaveData{Schema: 6, ...})`, so that what a real pre-PR-5 file
	// looked like is pinned HERE and cannot drift with the structs. (When
	// this test was written PausedSeconds lacked `omitempty` and a
	// Save-produced "schema 6" file still carried `"pausedSeconds":0`,
	// which would have proven nothing; that missing tag turned out to be
	// a save-losing upgrade bug on the JSON path — see
	// TestPrePR5StateJSONImportsUnderItsOriginalMac in
	// json_upgrade_mac_test.go. Hand-building the fixture stays correct
	// either way.)
	// Real, non-zero counters throughout, so "the new fields are zero" is
	// a claim about the NEW fields only while every pre-existing one is
	// proven intact alongside them.
	const prePR5Payload = `{"schema":6,"devCash":4242,"xp":777,` +
		`"sprint":{"index":2,"unitsDone":12.5},` +
		`"ownedItems":null,"ownedTints":null,"equipped":null,` +
		`"stats":{"date":"2026-03-10",` +
		`"today":{"keystrokes":900,"mouseActiveSeconds":0,"activeSeconds":100,"idleSeconds":400,"sprintsCompleted":0,"focusSessions":0,"appSwitches":0},` +
		`"lifetime":{"keystrokes":9000,"mouseActiveSeconds":0,"activeSeconds":1000,"idleSeconds":4000,"sprintsCompleted":0,"focusSessions":0,"appSwitches":0},` +
		`"coinsToday":{"keystrokes":0,"mouse":0,"focusSessions":0,"appSwitches":0},` +
		`"history":null,"streak":{"current":0,"longest":0,"lastActiveDate":""}}}`
	if strings.Contains(prePR5Payload, "paused") {
		t.Fatalf("the fixture mentions `paused` — it is not a pre-PR-5 payload: %s", prePR5Payload)
	}

	// Establish the DB (which also sets user_version = 6, so the payload's
	// schema, the schema column and user_version all agree — a
	// disagreement is itself treated as tampering), then swap in the
	// hand-built payload with a MAC computed over exactly those bytes,
	// which is precisely what a schema-6 build would have written.
	if err := Save(path, SaveData{Schema: 6, DevCash: 1}); err != nil {
		t.Fatalf("Save (establish a schema-6 DB): %v", err)
	}
	body := []byte(prePR5Payload)
	rawUpdateStateRow(t, path, 6, body, computeMACBytes(body))

	d, ok, err := Load(path)
	if err != nil {
		t.Fatalf("Load a schema-6 save under CurrentSchema %d: %v", CurrentSchema, err)
	}
	if !ok {
		t.Fatal("ok = false, want true — a schema-6 save must load, not be quarantined")
	}
	if d.Paused {
		t.Error("d.Paused = true for a schema-6 save, want false — absent means not paused")
	}
	if d.Stats.Today.PausedSeconds != 0 || d.Stats.Lifetime.PausedSeconds != 0 {
		t.Errorf("PausedSeconds = (today %d, lifetime %d) for a schema-6 save, want (0, 0) — nothing may be backfilled",
			d.Stats.Today.PausedSeconds, d.Stats.Lifetime.PausedSeconds)
	}
	// Every pre-existing field intact.
	if d.DevCash != 4242 || d.XP != 777 || d.Sprint.Index != 2 || d.Stats.Today.Keystrokes != 900 || d.Stats.Today.IdleSeconds != 400 {
		t.Errorf("a schema-6 grandfather load disturbed pre-existing fields: %+v", d)
	}

	// ...and it applies to a live game as "not paused", so the runtime
	// starts observing normally for a user who never paused anything.
	g := game.New()
	Apply(g, d)
	if g.Paused() {
		t.Error("after Apply of a schema-6 save the game is paused — a save that predates pause must never come back paused")
	}

	// The next Save upgrades the file to schema 7 in place, the same way
	// every prior bump did.
	if err := Save(path, Snapshot(g)); err != nil {
		t.Fatalf("Save (re-persist): %v", err)
	}
	reloaded, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load after re-persist: %v", err)
	}
	if reloaded.Schema != CurrentSchema {
		t.Errorf("reloaded.Schema = %d, want CurrentSchema (%d) — migration must upgrade the file on the next save", reloaded.Schema, CurrentSchema)
	}
}

// TestSchema7PausedSaveRoundTrips is §PR-5's migration criterion, second
// half: "a schema-7 paused save round-trips".
//
// It goes the whole way round — a live paused game with real paused
// seconds in every bucket (today, lifetime, a finalized day, and both
// halves of an in-progress session) through Snapshot, Save, Load and
// Apply, back to a live game that is still paused with every paused
// second intact. This is FORK D's behaviour end to end: "a paused dexel
// restarts paused".
func TestSchema7PausedSaveRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	d := SaveData{
		Schema:  CurrentSchema,
		DevCash: 500,
		Paused:  true,
		Stats: StatsSave{
			Date:     "2026-03-11",
			Today:    StatCountersSave{Keystrokes: 10, ActiveSeconds: 30, IdleSeconds: 40, PausedSeconds: 50},
			Lifetime: StatCountersSave{Keystrokes: 100, ActiveSeconds: 300, IdleSeconds: 400, PausedSeconds: 500},
			History: []DayBucketSave{{
				Date:     "2026-03-10",
				Counters: StatCountersSave{ActiveSeconds: 111, IdleSeconds: 222, PausedSeconds: 333},
			}},
		},
		Session: &ActiveSessionSave{
			ID:             1,
			StartedAt:      time.Date(2026, 3, 11, 9, 0, 0, 0, time.UTC).Format(time.RFC3339),
			LastActivityAt: time.Date(2026, 3, 11, 9, 5, 0, 0, time.UTC).Format(time.RFC3339),
			Baseline:       StatCountersSave{PausedSeconds: 60},
			Watermark:      StatCountersSave{PausedSeconds: 90},
		},
	}
	if err := Save(path, d); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The key really is on disk this time (paused=true is not omitted),
	// which is what makes the grandfather test's absence-check meaningful.
	_, payload, _ := rawReadStateRow(t, path)
	var onDisk map[string]any
	if err := json.Unmarshal(payload, &onDisk); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if onDisk["paused"] != true {
		t.Errorf("the persisted payload's `paused` = %v, want true", onDisk["paused"])
	}

	got, ok, err := Load(path)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if !got.Paused {
		t.Error("got.Paused = false after a round-trip of a paused save")
	}
	if got.Stats.Today.PausedSeconds != 50 || got.Stats.Lifetime.PausedSeconds != 500 {
		t.Errorf("PausedSeconds after round-trip = (today %d, lifetime %d), want (50, 500)", got.Stats.Today.PausedSeconds, got.Stats.Lifetime.PausedSeconds)
	}
	if len(got.Stats.History) != 1 || got.Stats.History[0].Counters.PausedSeconds != 333 {
		t.Errorf("the finalized day's PausedSeconds did not survive: %+v", got.Stats.History)
	}
	if got.Session == nil || got.Session.Baseline.PausedSeconds != 60 || got.Session.Watermark.PausedSeconds != 90 {
		t.Errorf("the session's baseline/watermark PausedSeconds did not survive: %+v", got.Session)
	}

	// ...and through Apply into a live game, which is what main.go does at
	// boot: still paused, with the paused seconds visible on the wire.
	g := game.New()
	// A session log must be restored before Apply (P2's §8 ordering rule).
	Apply(g, got)
	if !g.Paused() {
		t.Fatal("after Apply the game is not paused — FORK D requires a paused dexel to restart paused")
	}
	state := g.State()
	if !state.Paused {
		t.Error("state.paused = false on the wire after loading a paused save")
	}
	if state.ActiveState != "idle" {
		t.Errorf("state.activeState = %q for a game restored as paused, want idle", state.ActiveState)
	}
	if state.Stats.Lifetime.PausedSeconds != 500 {
		t.Errorf("wire lifetime.pausedSeconds = %d, want 500", state.Stats.Lifetime.PausedSeconds)
	}
	if state.Sessions.Active == nil || state.Sessions.Active.PausedSeconds == 0 {
		t.Errorf("the restored session's pausedSeconds did not reach the wire: %+v", state.Sessions.Active)
	}
}

// TestPausedSurvivesAFullSnapshotApplyCycleBothWays proves the flag is
// carried by Snapshot AND Apply in both directions — the failure mode a
// one-directional test misses is a Snapshot that hardcodes false (a paused
// dexel that quietly forgets on the next autosave, silently resuming
// observation after a restart: the privacy-relevant direction).
func TestPausedSurvivesAFullSnapshotApplyCycleBothWays(t *testing.T) {
	for _, paused := range []bool{true, false} {
		g := game.New()
		g.SetPaused(paused)
		d := Snapshot(g)
		if d.Paused != paused {
			t.Errorf("Snapshot of a paused=%v game produced Paused=%v", paused, d.Paused)
		}
		restored := game.New()
		Apply(restored, d)
		if restored.Paused() != paused {
			t.Errorf("Apply of Paused=%v produced a game with Paused()=%v", d.Paused, restored.Paused())
		}
	}
}

// TestTamperedPausedFlagIsRefused: `paused` lives inside the MAC-protected
// SaveData rather than in the unsigned config.json, so hand-flipping it
// with a text editor (or sqlite3) invalidates the save exactly like
// hand-editing devCash does. That is the reason it belongs in the
// protected file at all — a privacy posture that could be silently flipped
// on disk would not be a posture.
func TestTamperedPausedFlagIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if err := Save(path, SaveData{Schema: CurrentSchema, DevCash: 1, Paused: true}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	schema, payload, mac := rawReadStateRow(t, path)
	var d SaveData
	if err := json.Unmarshal(payload, &d); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	d.Paused = false // "un-pause me behind dexel's back"
	tampered, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rawUpdateStateRow(t, path, schema, tampered, mac)

	if _, ok, err := Load(path); ok || !errors.Is(err, ErrTampered) {
		t.Errorf("Load of a save with a hand-flipped `paused` = (ok=%v, err=%v), want refused with ErrTampered", ok, err)
	}
	if _, statErr := os.Stat(path + ".invalid"); statErr != nil {
		t.Errorf("expected the tampered save to be quarantined to %s.invalid: %v", path, statErr)
	}
}
