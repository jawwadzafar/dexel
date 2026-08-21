package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jawwadzafar/dev-companion/app/internal/engine"
	"github.com/jawwadzafar/dev-companion/app/internal/game"
)

// TestStatsRoundTripThroughSaveLoadApply is Phase A1's (Analytics track,
// docs/plan/ROADMAP.md) persistence exit criterion: "counters survive
// restart" exercised through the exact path main.go uses — Snapshot, Save,
// Load, Apply onto a freshly-constructed game.New().
func TestStatsRoundTripThroughSaveLoadApply(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	g := game.New()
	fakeNow := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	g.SetClockForTest(func() time.Time { return fakeNow })

	g.Tick(engine.TickResult{Mood: engine.MoodCoding, KeystrokeDelta: 42, MouseActive: true, WorkUnits: 50})

	want := Snapshot(g)
	if want.Schema != CurrentSchema {
		t.Fatalf("Snapshot().Schema = %d, want CurrentSchema (%d)", want.Schema, CurrentSchema)
	}
	if want.Stats.Date != "2026-06-15" {
		t.Fatalf("Stats.Date = %q, want 2026-06-15", want.Stats.Date)
	}
	if want.Stats.Today.Keystrokes != 42 || want.Stats.Lifetime.Keystrokes != 42 {
		t.Fatalf("Stats.Today/Lifetime.Keystrokes = (%d,%d), want (42,42)", want.Stats.Today.Keystrokes, want.Stats.Lifetime.Keystrokes)
	}
	if want.Stats.Today.SprintsCompleted != 1 {
		t.Fatalf("Stats.Today.SprintsCompleted = %d, want 1", want.Stats.Today.SprintsCompleted)
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := Load(path)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if got.Stats != want.Stats {
		t.Errorf("Stats after Save/Load = %+v, want %+v", got.Stats, want.Stats)
	}

	g2 := game.New()
	g2.SetClockForTest(func() time.Time { return fakeNow }) // same day: Apply must not roll today over
	Apply(g2, got)

	state := g2.State()
	if state.Stats.Today.Keystrokes != 42 {
		t.Errorf("after Apply: today.Keystrokes = %d, want 42", state.Stats.Today.Keystrokes)
	}
	if state.Stats.Lifetime.Keystrokes != 42 {
		t.Errorf("after Apply: lifetime.Keystrokes = %d, want 42", state.Stats.Lifetime.Keystrokes)
	}
	if state.Stats.Today.SprintsCompleted != 1 {
		t.Errorf("after Apply: today.SprintsCompleted = %d, want 1", state.Stats.Today.SprintsCompleted)
	}
}

// TestApplyRollsOverStatsAcrossASavedMidnight confirms Apply's date-aware
// restore (via game.Game.RestoreStats) really does zero today's bucket
// when the loading game's current local date has moved past the save's
// stats.date, while carrying lifetime forward — the load-time half of
// midnight rollover, exercised through the real Load/Apply path rather
// than calling RestoreStats directly (see the game package's own
// RestoreStats-focused tests for that unit-level coverage).
func TestApplyRollsOverStatsAcrossASavedMidnight(t *testing.T) {
	saved := SaveData{
		Schema: CurrentSchema,
		Stats: StatsSave{
			Date:     "2026-06-15",
			Today:    StatCountersSave{Keystrokes: 500, SprintsCompleted: 3},
			Lifetime: StatCountersSave{Keystrokes: 9000, SprintsCompleted: 40},
		},
	}

	g := game.New()
	g.SetClockForTest(func() time.Time { return time.Date(2026, 6, 16, 8, 0, 0, 0, time.Local) })
	Apply(g, saved)

	state := g.State()
	if state.Stats.Today.Keystrokes != 0 || state.Stats.Today.SprintsCompleted != 0 {
		t.Errorf("today after Apply across a midnight = %+v, want the zero value", state.Stats.Today)
	}
	if state.Stats.Lifetime.Keystrokes != 9000 || state.Stats.Lifetime.SprintsCompleted != 40 {
		t.Errorf("lifetime after Apply across a midnight = %+v, want it carried forward untouched", state.Stats.Lifetime)
	}
}

// TestSchema1FileHasNoStatsKeyAndMigratesToZero is the schema-1 -> 2
// migration test S4 requires whenever CurrentSchema is bumped: a save
// written before this feature existed has no "stats" key at all. Load
// must still accept it (schema 1 <= CurrentSchema 2, not a "future"
// save), and Apply must hand game.Game a well-formed all-zero stats
// bucket rather than panicking or leaving stale/garbage data — the exact
// same "no stats recorded yet" state a brand-new game.New() already
// starts in.
func TestSchema1FileHasNoStatsKeyAndMigratesToZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	// Deliberately hand-written, WITHOUT a "stats" key — exactly what a
	// pre-Phase-A1 (schema 1) build wrote to disk.
	raw := `{
		"schema": 1,
		"devCash": 250,
		"xp": 100,
		"sprint": {"index": 0, "unitsDone": 0},
		"ownedItems": ["chair_basic"],
		"ownedTints": [],
		"equipped": {}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	d, ok, err := Load(path)
	if err != nil {
		t.Fatalf("Load a schema-1 file (older than CurrentSchema %d) should not error: %v", CurrentSchema, err)
	}
	if !ok {
		t.Fatal("Load reported no save for a valid schema-1 file")
	}
	if d.Stats != (StatsSave{}) {
		t.Errorf("schema-1 file's parsed Stats = %+v, want the zero value (no \"stats\" key existed)", d.Stats)
	}

	g := game.New()
	Apply(g, d)
	state := g.State()
	if state.Stats.Today != (game.StatCounters{}) || state.Stats.Lifetime != (game.StatCounters{}) {
		t.Errorf("after migrating a schema-1 save: stats = %+v, want all-zero counters", state.Stats)
	}
	// The rest of the schema-1 data must still load normally — migration
	// must not disturb fields that existed before this feature.
	if g.DevCash != 250 || g.XP != 100 {
		t.Errorf("after migrating a schema-1 save: devCash/xp = (%d,%d), want (250,100)", g.DevCash, g.XP)
	}

	// Re-saving now writes the current (2) schema — the actual migration:
	// the NEXT save this build makes is schema-2-shaped, stats key and all.
	if err := Save(path, Snapshot(g)); err != nil {
		t.Fatalf("Save after migration: %v", err)
	}
	reloaded, ok, err := Load(path)
	if err != nil || !ok {
		t.Fatalf("reload after migration: ok=%v err=%v", ok, err)
	}
	if reloaded.Schema != CurrentSchema {
		t.Errorf("reloaded.Schema = %d, want CurrentSchema (%d) — migration must upgrade the file on next save", reloaded.Schema, CurrentSchema)
	}
}
