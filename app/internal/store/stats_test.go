package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

// TestSchema2FileMigratesToSchema3WithNewCountersAndCoinsZero is the
// schema-2 -> 3 migration test A2's Task GO-3 requires (docs/plan/
// A2-design.md §7/§8): a save written by the Phase-A1 (schema 2) build has
// devCash/xp/owned items and A1's stats counters, but no "focusSessions",
// "appSwitches", or "coinsToday" keys at all. Load must still accept it
// (schema 2 <= CurrentSchema 3, not a "future" save) with every existing
// balance intact, and Apply must hand game.Game well-formed ZERO values
// for the new A2 counters/coins — never panic, never invent nonzero data —
// exactly mirroring TestSchema1FileHasNoStatsKeyAndMigratesToZero's proof
// for the 1->2 bump. This is the live-user-save non-destructiveness
// invariant the task spec calls out explicitly.
func TestSchema2FileMigratesToSchema3WithNewCountersAndCoinsZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	// Deliberately hand-written, schema 2 shaped: has "stats" with A1's
	// five counters, but no focusSessions/appSwitches/coinsToday keys —
	// exactly what a pre-Phase-A2 build wrote to disk.
	raw := `{
		"schema": 2,
		"devCash": 4200,
		"xp": 777,
		"sprint": {"index": 2, "unitsDone": 3.5},
		"ownedItems": ["chair_basic", "chair_racer"],
		"ownedTints": [],
		"equipped": {},
		"stats": {
			"date": "2026-06-15",
			"today": {
				"keystrokes": 42,
				"mouseActiveSeconds": 10,
				"activeSeconds": 50,
				"idleSeconds": 5,
				"sprintsCompleted": 1
			},
			"lifetime": {
				"keystrokes": 9000,
				"mouseActiveSeconds": 500,
				"activeSeconds": 4000,
				"idleSeconds": 900,
				"sprintsCompleted": 40
			}
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	d, ok, err := Load(path)
	if err != nil {
		t.Fatalf("Load a schema-2 file (older than CurrentSchema %d) should not error: %v", CurrentSchema, err)
	}
	if !ok {
		t.Fatal("Load reported no save for a valid schema-2 file")
	}
	if d.Stats.Today.FocusSessions != 0 || d.Stats.Today.AppSwitches != 0 {
		t.Errorf("schema-2 file's parsed Today FocusSessions/AppSwitches = (%d,%d), want (0,0) — no such keys existed",
			d.Stats.Today.FocusSessions, d.Stats.Today.AppSwitches)
	}
	if d.Stats.Lifetime.FocusSessions != 0 || d.Stats.Lifetime.AppSwitches != 0 {
		t.Errorf("schema-2 file's parsed Lifetime FocusSessions/AppSwitches = (%d,%d), want (0,0)",
			d.Stats.Lifetime.FocusSessions, d.Stats.Lifetime.AppSwitches)
	}
	if d.Stats.CoinsToday != (CoinBreakdownSave{}) {
		t.Errorf("schema-2 file's parsed CoinsToday = %+v, want the zero value (no \"coinsToday\" key existed)", d.Stats.CoinsToday)
	}
	// Pre-existing A1 counters must survive untouched.
	if d.Stats.Today.Keystrokes != 42 || d.Stats.Lifetime.Keystrokes != 9000 {
		t.Errorf("schema-2 file's parsed keystroke counters = (%d,%d), want (42,9000) — migration must not disturb pre-existing counters",
			d.Stats.Today.Keystrokes, d.Stats.Lifetime.Keystrokes)
	}

	g := game.New()
	fakeNow := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	g.SetClockForTest(func() time.Time { return fakeNow })
	Apply(g, d)
	state := g.State()

	// Existing balances intact — the live-save non-destructiveness
	// invariant the task spec calls out.
	if g.DevCash != 4200 || g.XP != 777 {
		t.Errorf("after migrating a schema-2 save: devCash/xp = (%d,%d), want (4200,777)", g.DevCash, g.XP)
	}
	if g.SprintIndex() != 2 || g.Progress != 3.5 {
		t.Errorf("after migrating a schema-2 save: sprint = (%d,%v), want (2,3.5)", g.SprintIndex(), g.Progress)
	}
	if !g.OwnedItems["chair_racer"] {
		t.Error("after migrating a schema-2 save: chair_racer should still be owned")
	}
	if state.Stats.Today.Keystrokes != 42 || state.Stats.Lifetime.Keystrokes != 9000 {
		t.Errorf("after migrating a schema-2 save: today/lifetime keystrokes = (%d,%d), want (42,9000)",
			state.Stats.Today.Keystrokes, state.Stats.Lifetime.Keystrokes)
	}

	// New A2 counters/coins default to 0 on a schema-2 save.
	if state.Stats.Today.FocusSessions != 0 || state.Stats.Today.AppSwitches != 0 {
		t.Errorf("after migrating a schema-2 save: today FocusSessions/AppSwitches = (%d,%d), want (0,0)",
			state.Stats.Today.FocusSessions, state.Stats.Today.AppSwitches)
	}
	if state.Stats.Lifetime.FocusSessions != 0 || state.Stats.Lifetime.AppSwitches != 0 {
		t.Errorf("after migrating a schema-2 save: lifetime FocusSessions/AppSwitches = (%d,%d), want (0,0)",
			state.Stats.Lifetime.FocusSessions, state.Stats.Lifetime.AppSwitches)
	}
	if state.Stats.CoinsToday != (game.CoinBreakdown{}) {
		t.Errorf("after migrating a schema-2 save: CoinsToday = %+v, want the zero value", state.Stats.CoinsToday)
	}

	// Re-saving now writes the current (3) schema — the actual migration.
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

// TestSchema3SaveRoundTripsWithNewCountersAndCoins is the schema-3
// save->load->equal counterpart to TestSchema2FileMigratesToSchema3...
// above: once a save already carries the new A2 fields, they must survive
// a full Save/Load cycle byte-for-byte, exercised through the exact
// Snapshot/Save/Load/Apply path main.go uses (mirrors
// TestStatsRoundTripThroughSaveLoadApply's shape, extended to the new
// counters and CoinsToday).
func TestSchema3SaveRoundTripsWithNewCountersAndCoins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	g := game.New()
	fakeNow := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	g.SetClockForTest(func() time.Time { return fakeNow })

	g.RestoreStats("2026-06-15",
		game.StatCounters{Keystrokes: 42, FocusSessions: 3, AppSwitches: 7},
		game.StatCounters{Keystrokes: 9000, FocusSessions: 30, AppSwitches: 70},
	)
	g.RestoreCoinsToday(game.CoinBreakdown{Keystrokes: 6, Mouse: 2, FocusSessions: 4, AppSwitches: 0})

	want := Snapshot(g)
	if want.Schema != CurrentSchema {
		t.Fatalf("Snapshot().Schema = %d, want CurrentSchema (%d)", want.Schema, CurrentSchema)
	}
	if want.Stats.Today.FocusSessions != 3 || want.Stats.Today.AppSwitches != 7 {
		t.Fatalf("Stats.Today FocusSessions/AppSwitches = (%d,%d), want (3,7)", want.Stats.Today.FocusSessions, want.Stats.Today.AppSwitches)
	}
	if want.Stats.CoinsToday != (CoinBreakdownSave{Keystrokes: 6, Mouse: 2, FocusSessions: 4, AppSwitches: 0}) {
		t.Fatalf("Stats.CoinsToday = %+v, want {6,2,4,0}", want.Stats.CoinsToday)
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := Load(path)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if got.Stats != want.Stats {
		t.Errorf("Stats after Save/Load = %+v, want %+v (save->load->equal)", got.Stats, want.Stats)
	}

	g2 := game.New()
	g2.SetClockForTest(func() time.Time { return fakeNow }) // same day: Apply must not roll today over
	Apply(g2, got)
	state := g2.State()

	if state.Stats.Today.FocusSessions != 3 || state.Stats.Today.AppSwitches != 7 {
		t.Errorf("after Apply: today FocusSessions/AppSwitches = (%d,%d), want (3,7)", state.Stats.Today.FocusSessions, state.Stats.Today.AppSwitches)
	}
	if state.Stats.Lifetime.FocusSessions != 30 || state.Stats.Lifetime.AppSwitches != 70 {
		t.Errorf("after Apply: lifetime FocusSessions/AppSwitches = (%d,%d), want (30,70)", state.Stats.Lifetime.FocusSessions, state.Stats.Lifetime.AppSwitches)
	}
	if state.Stats.CoinsToday != (game.CoinBreakdown{Keystrokes: 6, Mouse: 2, FocusSessions: 4, AppSwitches: 0}) {
		t.Errorf("after Apply: CoinsToday = %+v, want {6,2,4,0}", state.Stats.CoinsToday)
	}
}

// TestFutureSchema4RefusalStillFiresAfterTheSchema3Bump re-proves, at the
// literal number CurrentSchema (3) + 1 = 4, that ErrFutureSchema still
// refuses to load a newer-than-supported save rather than silently
// downgrading it — the invariant the task spec requires stay intact
// through this bump. TestLoadFutureSchemaIsBackedUpNeverDowngradedInPlace
// already proves this generically (using CurrentSchema+1); this test pins
// the concrete number 4 so a future reader can see, without doing the
// arithmetic, that schema 4 specifically is refused post-bump.
func TestFutureSchema4RefusalStillFiresAfterTheSchema3Bump(t *testing.T) {
	if CurrentSchema != 3 {
		t.Fatalf("CurrentSchema = %d, want 3 — this test's literal schema-4 refusal check assumes the A2 bump landed at exactly 3", CurrentSchema)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	raw := `{"schema": 4, "devCash": 999999}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	d, ok, err := Load(path)
	if err == nil {
		t.Fatal("expected an error loading a schema-4 save against CurrentSchema 3, got nil")
	}
	if !errors.Is(err, ErrFutureSchema) {
		t.Errorf("Load err = %v, want it to wrap ErrFutureSchema", err)
	}
	if ok {
		t.Error("ok=true for a schema-4 save, want false — it must be refused, not silently downgraded")
	}
	if !reflect.DeepEqual(d, SaveData{}) {
		t.Errorf("d = %+v, want the zero value", d)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("schema-4 file should have been moved away from path (renamed to .future), not left in place")
	}
	if _, statErr := os.Stat(path + ".future"); statErr != nil {
		t.Errorf("expected %s.future to exist (schema-4 original must be preserved, never deleted): %v", path, statErr)
	}
}
