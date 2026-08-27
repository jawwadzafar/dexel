package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/game"
)

// TestPausedSaveRoundTrips proves a paused save round-trips end to end.
//
// It goes the whole way round — a live paused game with real paused
// seconds in every bucket (today, lifetime, a finalized day, and both
// halves of an in-progress session) through Snapshot, Save, Load and
// Apply, back to a live game that is still paused with every paused
// second intact. This is FORK D's behaviour end to end: "a paused dexel
// restarts paused".
func TestPausedSaveRoundTrips(t *testing.T) {
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

	// The `paused` key really is on disk (paused=true is not omitted).
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
