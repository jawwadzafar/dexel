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
	if !reflect.DeepEqual(got.Stats, want.Stats) {
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
	if !reflect.DeepEqual(d.Stats, StatsSave{}) {
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
	if !reflect.DeepEqual(got.Stats, want.Stats) {
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

// TestFutureSchema5RefusalStillFiresAfterTheSchema4Bump re-proves, at the
// literal number CurrentSchema (4) + 1 = 5, that ErrFutureSchema still
// refuses to load a newer-than-supported save rather than silently
// downgrading it — the invariant Analytics Phase A3's Task GO-2
// (docs/plan/A3-design.md §4/§7) requires stay intact through the 3->4
// bump: "ErrFutureSchema is preserved unchanged." (This test previously
// pinned schema 4 as the future/refused schema, back when CurrentSchema
// was 3; it now pins schema 5 the same way, one bump later.)
// TestLoadFutureSchemaIsBackedUpNeverDowngradedInPlace already proves this
// generically (using CurrentSchema+1); this test pins the concrete number
// 5 so a future reader can see, without doing the arithmetic, that schema
// 5 specifically is refused post-bump.
func TestFutureSchema5RefusalStillFiresAfterTheSchema4Bump(t *testing.T) {
	if CurrentSchema != 4 {
		t.Fatalf("CurrentSchema = %d, want 4 — this test's literal schema-5 refusal check assumes the A3 bump landed at exactly 4", CurrentSchema)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	raw := `{"schema": 5, "devCash": 999999}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	d, ok, err := Load(path)
	if err == nil {
		t.Fatal("expected an error loading a schema-5 save against CurrentSchema 4, got nil")
	}
	if !errors.Is(err, ErrFutureSchema) {
		t.Errorf("Load err = %v, want it to wrap ErrFutureSchema", err)
	}
	if ok {
		t.Error("ok=true for a schema-5 save, want false — it must be refused, not silently downgraded")
	}
	if !reflect.DeepEqual(d, SaveData{}) {
		t.Errorf("d = %+v, want the zero value", d)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("schema-5 file should have been moved away from path (renamed to .future), not left in place")
	}
	if _, statErr := os.Stat(path + ".future"); statErr != nil {
		t.Errorf("expected %s.future to exist (schema-5 original must be preserved, never deleted): %v", path, statErr)
	}
}

// --- Analytics Phase A3 (docs/plan/A3-design.md §4/§7 Task GO-2) --------

// TestSchema3FileMigratesToSchema4WithEmptyHistoryAndZeroStreak is the
// schema-3 -> 4 migration test the task spec requires: a save written by
// the Phase-A2 (schema 3) build has devCash/xp/owned items/equipped and
// the full A1+A2 stats counters, but no "history" or "streak" keys at
// all. Load must still accept it (schema 3 <= CurrentSchema 4, not a
// "future" save) with every existing balance/counter intact, and Apply
// must hand game.Game an EMPTY history and a ZERO streak — never
// fabricate/backfill days — exactly mirroring
// TestSchema2FileMigratesToSchema3WithNewCountersAndCoinsZero's proof for
// the 2->3 bump.
func TestSchema3FileMigratesToSchema4WithEmptyHistoryAndZeroStreak(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	// Deliberately hand-written, schema 3 shaped: has "stats" with all of
	// A1+A2's counters and coinsToday, but no "history"/"streak" keys —
	// exactly what a pre-Phase-A3 build wrote to disk.
	raw := `{
		"schema": 3,
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
				"sprintsCompleted": 1,
				"focusSessions": 3,
				"appSwitches": 7
			},
			"lifetime": {
				"keystrokes": 9000,
				"mouseActiveSeconds": 500,
				"activeSeconds": 4000,
				"idleSeconds": 900,
				"sprintsCompleted": 40,
				"focusSessions": 30,
				"appSwitches": 70
			},
			"coinsToday": {"keystrokes": 6, "mouse": 2, "focusSessions": 4, "appSwitches": 0}
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	d, ok, err := Load(path)
	if err != nil {
		t.Fatalf("Load a schema-3 file (older than CurrentSchema %d) should not error: %v", CurrentSchema, err)
	}
	if !ok {
		t.Fatal("Load reported no save for a valid schema-3 file")
	}
	if len(d.Stats.History) != 0 {
		t.Errorf("schema-3 file's parsed History = %+v, want empty (no \"history\" key existed)", d.Stats.History)
	}
	if d.Stats.Streak != (StreakSave{}) {
		t.Errorf("schema-3 file's parsed Streak = %+v, want the zero value (no \"streak\" key existed)", d.Stats.Streak)
	}
	// Pre-existing A1/A2 fields must survive untouched.
	if d.Stats.Today.Keystrokes != 42 || d.Stats.Today.FocusSessions != 3 || d.Stats.Today.AppSwitches != 7 {
		t.Errorf("schema-3 file's parsed Today = %+v, want keystrokes 42/focusSessions 3/appSwitches 7 intact", d.Stats.Today)
	}
	if d.Stats.CoinsToday != (CoinBreakdownSave{Keystrokes: 6, Mouse: 2, FocusSessions: 4, AppSwitches: 0}) {
		t.Errorf("schema-3 file's parsed CoinsToday = %+v, want {6,2,4,0} intact", d.Stats.CoinsToday)
	}

	g := game.New()
	// Same local date as the save's stats.date: Apply must not trigger a
	// rollover/finalize here — this test is about the migration default,
	// not the separate finalize-on-reload behaviour (see
	// TestFinalizeOnReloadFinalizesTheStaleRunningDayExactlyOnce below).
	fakeNow := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	g.SetClockForTest(func() time.Time { return fakeNow })
	Apply(g, d)

	if got := g.HistorySnapshot(); len(got) != 0 {
		t.Errorf("after migrating a schema-3 save: HistorySnapshot() = %+v, want empty (no backfill/fabrication)", got)
	}
	current, longest, lastActiveDate := g.StreakSnapshot()
	if current != 0 || longest != 0 || lastActiveDate != "" {
		t.Errorf("after migrating a schema-3 save: streak = (%d,%d,%q), want (0,0,\"\") (no fabricated streak)", current, longest, lastActiveDate)
	}

	// Existing balances/counters intact — the live-save
	// non-destructiveness invariant the task spec calls out.
	if g.DevCash != 4200 || g.XP != 777 {
		t.Errorf("after migrating a schema-3 save: devCash/xp = (%d,%d), want (4200,777)", g.DevCash, g.XP)
	}
	if !g.OwnedItems["chair_racer"] {
		t.Error("after migrating a schema-3 save: chair_racer should still be owned")
	}
	state := g.State()
	if state.Stats.Today.Keystrokes != 42 || state.Stats.Lifetime.Keystrokes != 9000 {
		t.Errorf("after migrating a schema-3 save: today/lifetime keystrokes = (%d,%d), want (42,9000)",
			state.Stats.Today.Keystrokes, state.Stats.Lifetime.Keystrokes)
	}
	if state.Stats.CoinsToday != (game.CoinBreakdown{Keystrokes: 6, Mouse: 2, FocusSessions: 4, AppSwitches: 0}) {
		t.Errorf("after migrating a schema-3 save: CoinsToday = %+v, want {6,2,4,0}", state.Stats.CoinsToday)
	}

	// Re-saving now writes the current (4) schema — the actual migration.
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

// TestSchema4RoundTripsHistoryAndStreak is the schema-4 save->load->equal
// exit criterion the task spec requires: once a save already carries a
// non-empty history and a non-zero streak, both must survive a full
// Save/Load/Apply cycle exactly, exercised through the exact
// Snapshot/Save/Load/Apply path main.go uses (mirrors
// TestSchema3SaveRoundTripsWithNewCountersAndCoins's shape, extended to
// History/Streak).
func TestSchema4RoundTripsHistoryAndStreak(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	g := game.New()
	fakeNow := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	g.SetClockForTest(func() time.Time { return fakeNow })

	wantHistory := []game.DayBucket{
		{Date: "2026-06-13", Counters: game.StatCounters{Keystrokes: 100, ActiveSeconds: 400}, CoinsEarned: 12, LongestFocusBlockSeconds: 900},
		{Date: "2026-06-14", Counters: game.StatCounters{Keystrokes: 200, ActiveSeconds: 500}, CoinsEarned: 20, LongestFocusBlockSeconds: 1200},
	}
	g.RestoreHistory(wantHistory)
	g.RestoreStreak(2, 5, "2026-06-14")
	g.RestoreStats("2026-06-15", game.StatCounters{Keystrokes: 42}, game.StatCounters{Keystrokes: 9042})

	want := Snapshot(g)
	if want.Schema != CurrentSchema {
		t.Fatalf("Snapshot().Schema = %d, want CurrentSchema (%d)", want.Schema, CurrentSchema)
	}
	if len(want.Stats.History) != 2 {
		t.Fatalf("Snapshot().Stats.History has %d entries, want 2", len(want.Stats.History))
	}
	if want.Stats.History[0].Date != "2026-06-13" || want.Stats.History[0].CoinsEarned != 12 || want.Stats.History[0].LongestFocusBlockSeconds != 900 {
		t.Errorf("Snapshot().Stats.History[0] = %+v, want date 2026-06-13/coinsEarned 12/longestFocusBlockSeconds 900", want.Stats.History[0])
	}
	if want.Stats.Streak != (StreakSave{Current: 2, Longest: 5, LastActiveDate: "2026-06-14"}) {
		t.Fatalf("Snapshot().Stats.Streak = %+v, want {2,5,2026-06-14}", want.Stats.Streak)
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := Load(path)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got.Stats.History, want.Stats.History) {
		t.Errorf("History after Save/Load = %+v, want %+v (save->load->equal)", got.Stats.History, want.Stats.History)
	}
	if got.Stats.Streak != want.Stats.Streak {
		t.Errorf("Streak after Save/Load = %+v, want %+v", got.Stats.Streak, want.Stats.Streak)
	}

	g2 := game.New()
	g2.SetClockForTest(func() time.Time { return fakeNow }) // same day: Apply must not roll today over or finalize
	Apply(g2, got)

	gotHistory := g2.HistorySnapshot()
	if !reflect.DeepEqual(gotHistory, wantHistory) {
		t.Errorf("after Apply: HistorySnapshot() = %+v, want %+v", gotHistory, wantHistory)
	}
	current, longest, lastActiveDate := g2.StreakSnapshot()
	if current != 2 || longest != 5 || lastActiveDate != "2026-06-14" {
		t.Errorf("after Apply: streak = (%d,%d,%q), want (2,5,2026-06-14)", current, longest, lastActiveDate)
	}
}

// TestHistoryRetentionSurvivesASaveReload proves the 30-day retention
// window (game.HistoryRetentionDays) is enforced by the persistence path
// itself, not just by the in-process game package: 35 consecutive days
// are finalized (each via RestoreStats's rollover, exactly as an
// in-process midnight crossing or a load would), producing 35 candidate
// buckets, and the persisted/round-tripped history must still show
// exactly the last 30 (oldest 5 pruned) after a full Snapshot/Save/Load/
// Apply cycle.
func TestHistoryRetentionSurvivesASaveReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	dateOf := func(dayOffset int) string {
		return time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local).AddDate(0, 0, dayOffset).Format("2006-01-02")
	}

	g := game.New()
	const totalDays = 35
	for i := 0; i < totalDays; i++ {
		clockDate := time.Date(2026, 6, 1, 10, 0, 0, 0, time.Local).AddDate(0, 0, i+1)
		g.SetClockForTest(func() time.Time { return clockDate })
		// RestoreStats(date, today, lifetime): sets statsDate to day i,
		// then rolloverStatsIfNewDay notices the clock's "today" (day
		// i+1) has moved past it and finalizes day i — the same
		// mechanism a real load-time or in-process midnight rollover
		// uses, driven here to build up more than HistoryRetentionDays
		// of finalized buckets deterministically.
		g.RestoreStats(dateOf(i), game.StatCounters{ActiveSeconds: 400, Keystrokes: uint64(i)}, game.StatCounters{})
	}

	preSave := g.HistorySnapshot()
	if len(preSave) != game.HistoryRetentionDays {
		t.Fatalf("before save: HistorySnapshot() has %d entries, want %d (HistoryRetentionDays)", len(preSave), game.HistoryRetentionDays)
	}
	if preSave[0].Date != dateOf(totalDays-game.HistoryRetentionDays) {
		t.Errorf("before save: oldest retained day = %s, want %s (the oldest %d days pruned)",
			preSave[0].Date, dateOf(totalDays-game.HistoryRetentionDays), totalDays-game.HistoryRetentionDays)
	}
	if preSave[len(preSave)-1].Date != dateOf(totalDays-1) {
		t.Errorf("before save: newest retained day = %s, want %s", preSave[len(preSave)-1].Date, dateOf(totalDays-1))
	}

	if err := Save(path, Snapshot(g)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := Load(path)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if len(got.Stats.History) != game.HistoryRetentionDays {
		t.Fatalf("after Save/Load: Stats.History has %d entries, want %d", len(got.Stats.History), game.HistoryRetentionDays)
	}

	g2 := game.New()
	finalClockDate := time.Date(2026, 6, 1, 10, 0, 0, 0, time.Local).AddDate(0, 0, totalDays)
	g2.SetClockForTest(func() time.Time { return finalClockDate }) // same day as g ended on: no further finalize on Apply
	Apply(g2, got)

	after := g2.HistorySnapshot()
	if len(after) != game.HistoryRetentionDays {
		t.Fatalf("after Apply: HistorySnapshot() has %d entries, want %d", len(after), game.HistoryRetentionDays)
	}
	if after[0].Date != dateOf(totalDays-game.HistoryRetentionDays) {
		t.Errorf("after Apply: oldest retained day = %s, want %s", after[0].Date, dateOf(totalDays-game.HistoryRetentionDays))
	}
	if after[len(after)-1].Date != dateOf(totalDays-1) {
		t.Errorf("after Apply: newest retained day = %s, want %s", after[len(after)-1].Date, dateOf(totalDays-1))
	}
}

// TestFinalizeOnReloadFinalizesTheStaleRunningDayExactlyOnce is the task
// spec's required proof of §2.5 case 8 / §3.3's "finalize-on-reload"
// semantics through the ACTUAL Apply path (not by calling game's
// internals directly): a save whose stats.date is several days stale —
// exactly the "reopened the app days later" scenario — must finalize that
// one last running day into history + the streak EXACTLY ONCE on Apply,
// and a subsequent SAME-DAY reload of the resulting state must NOT
// double-finalize it.
func TestFinalizeOnReloadFinalizesTheStaleRunningDayExactlyOnce(t *testing.T) {
	// A save last written on 2026-06-10 with an ACTIVE day's worth of
	// activity (ActiveSeconds >= game.ActiveDayMinSeconds) — no history,
	// no streak yet (this is that day's very first-ever finalize).
	saved := SaveData{
		Schema: CurrentSchema,
		Stats: StatsSave{
			Date:       "2026-06-10",
			Today:      StatCountersSave{Keystrokes: 500, ActiveSeconds: 400, SprintsCompleted: 3},
			Lifetime:   StatCountersSave{Keystrokes: 9000, ActiveSeconds: 400, SprintsCompleted: 40},
			CoinsToday: CoinBreakdownSave{Keystrokes: 6},
		},
	}

	g := game.New()
	// The process is reopened 5 days later: 2026-06-15.
	reopenDate := time.Date(2026, 6, 15, 8, 0, 0, 0, time.Local)
	g.SetClockForTest(func() time.Time { return reopenDate })
	Apply(g, saved)

	history := g.HistorySnapshot()
	if len(history) != 1 {
		t.Fatalf("after reload-Apply across a %d-day gap: HistorySnapshot() has %d entries, want exactly 1 (the stale running day, finalized ONCE)",
			5, len(history))
	}
	if history[0].Date != "2026-06-10" {
		t.Errorf("finalized bucket's Date = %q, want 2026-06-10", history[0].Date)
	}
	if history[0].Counters.ActiveSeconds != 400 || history[0].Counters.Keystrokes != 500 {
		t.Errorf("finalized bucket's Counters = %+v, want the stale-day's Today counters (activeSeconds 400, keystrokes 500)", history[0].Counters)
	}

	current, longest, lastActiveDate := g.StreakSnapshot()
	if current != 1 || longest != 1 || lastActiveDate != "2026-06-10" {
		t.Errorf("streak after reload-Apply = (%d,%d,%q), want (1,1,2026-06-10) — the stale active day should have started a fresh 1-day streak",
			current, longest, lastActiveDate)
	}

	state := g.State()
	if state.Stats.Today != (game.StatCounters{}) {
		t.Errorf("today's bucket after reload-Apply = %+v, want the zero value (today, 2026-06-15, has had no activity yet)", state.Stats.Today)
	}
	if state.Stats.Lifetime.Keystrokes != 9000 {
		t.Errorf("lifetime.Keystrokes after reload-Apply = %d, want 9000 (carried forward untouched)", state.Stats.Lifetime.Keystrokes)
	}

	// Now: a SAME-DAY reload of the resulting state (Snapshot -> Apply
	// again, clock unchanged) must NOT re-finalize 2026-06-10 a second
	// time and must NOT touch the streak either.
	reSaved := Snapshot(g)
	g2 := game.New()
	g2.SetClockForTest(func() time.Time { return reopenDate }) // same day
	Apply(g2, reSaved)

	history2 := g2.HistorySnapshot()
	if len(history2) != 1 {
		t.Fatalf("after a SAME-DAY reload: HistorySnapshot() has %d entries, want still exactly 1 (no double-finalize)", len(history2))
	}
	if history2[0] != history[0] {
		t.Errorf("after a SAME-DAY reload: the finalized bucket changed to %+v, want unchanged %+v", history2[0], history[0])
	}
	current2, longest2, lastActiveDate2 := g2.StreakSnapshot()
	if current2 != current || longest2 != longest || lastActiveDate2 != lastActiveDate {
		t.Errorf("after a SAME-DAY reload: streak changed to (%d,%d,%q), want unchanged (%d,%d,%q)",
			current2, longest2, lastActiveDate2, current, longest, lastActiveDate)
	}
}
