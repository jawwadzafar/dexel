package store

import (
	"reflect"
	"strings"
	"testing"
)

// TestSaveDataIsContentFree is S3's clone of
// internal/activity/content_free_test.go's TestSnapshotIsContentFree,
// applied to the thing that actually persists to disk: SaveData is the
// exact on-disk shape at ~/.config/dexel/state.json (store.go's doc
// comment: "this file contains no user content"). Being content-free
// upstream (Snapshot, StateMessage) is meaningless if the one thing
// actually written to a file on the user's disk is free to grow a raw
// field later. This test enumerates every field by reflection against an
// explicit allow-list; adding a field to SaveData that isn't on the
// allow-list, or whose name suggests raw content, fails this test rather
// than silently shipping a privacy regression into a save file.
func TestSaveDataIsContentFree(t *testing.T) {
	allowed := map[string]string{
		"Schema":           "int",
		"DevCash":          "uint64",
		"XP":               "uint64",
		"Sprint":           "store.SprintSave",
		"OwnedItems":       "[]string",
		"OwnedTints":       "[]string",
		"Equipped":         "map[string]store.EquippedSave",
		"ImportedFromRust": "bool",
		"ImportedAt":       "string",
		"Stats":            "store.StatsSave",
		// Session/SessionLogHead (P2, docs/plan/P2-design.md §5.1, ADR
		// 0017 Decision 5, schema 6): the in-progress session and the
		// session log's opaque chain head. Session's own nested field-level
		// coverage is TestSessionSaveAndActiveSessionSaveAreContentFree
		// below — deliberately no Name field on either, mirroring the
		// dexel's own name being kept off this allow-list (see the Mac
		// field's comment just below): a project name is user-authored
		// CONFIG (§2.7), never a column here.
		"Session":        "*store.ActiveSessionSave",
		"SessionLogHead": "string",
		// Mac (SEC-1, docs/plan/SEC-1-design.md §6, ADR
		// 0014-save-integrity-hmac-and-config-split.md): the hex
		// HMAC-SHA256 tag over the rest of this struct — a fixed-width
		// DIGEST field, not content, and it cannot carry content since it
		// is a deterministic function of the (already content-free)
		// fields above. This is the one field allowed to be added here
		// without itself needing a privacy justification beyond "it's a
		// digest." The dexel's user-authored NAME, by contrast, is
		// deliberately NOT here: it lives entirely outside SaveData, in
		// its own unsigned config.json (see config.go's ConfigData doc
		// comment for why), so this allow-list staying free of any name
		// field is itself part of the privacy proof, not an omission.
		"Mac": "string",
		// Paused (PR-5, dev_docs/production-runtime/ARCHITECTURE.md
		// Decision 16 / FORK D, schema 7): a single bool recording that
		// the user asked dexel to STOP observing. It is a user intent,
		// like ConfigView.Name — but unlike a name it is not text and
		// carries no information beyond one bit, so it needs no ADR 0014
		// category argument to sit inside the protected save; it belongs
		// here precisely BECAUSE it must be MAC-protected (a save whose
		// `paused` could be flipped by hand is a save whose privacy
		// posture could be flipped by hand).
		"Paused": "bool",
	}

	// Field/type names whose presence anywhere on SaveData is itself a
	// violation, independent of the allow-list above (belt and suspenders:
	// this catches a rename of an allowed field into something that smells
	// like content).
	forbiddenSubstrings := []string{
		"title", "text", "content", "keycode", "key_code", "clipboard",
		"url", "path", "document", "message", "body", "keyname", "char",
	}

	typ := reflect.TypeOf(SaveData{})
	if typ.NumField() != len(allowed) {
		t.Fatalf("SaveData has %d fields, expected exactly %d (%v) — a field was added or removed without updating this privacy test",
			typ.NumField(), len(allowed), allowed)
	}

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		wantType, ok := allowed[f.Name]
		if !ok {
			t.Errorf("SaveData.%s is not on the content-free allow-list — every field must be justified here", f.Name)
			continue
		}
		if f.Type.String() != wantType {
			t.Errorf("SaveData.%s has type %s, want %s", f.Name, f.Type.String(), wantType)
		}

		lower := strings.ToLower(f.Name)
		for _, bad := range forbiddenSubstrings {
			if strings.Contains(lower, bad) {
				t.Errorf("SaveData.%s name contains forbidden substring %q — looks like it could carry content", f.Name, bad)
			}
		}
	}
}

// TestSprintSaveAndEquippedSaveAreContentFree extends the same guard to
// SaveData's two nested struct types — the top-level field-count check
// above only proves those two fields exist with the right named type; it
// says nothing about what's INSIDE SprintSave/EquippedSave, which could
// otherwise grow a content field without ever touching SaveData itself.
func TestSprintSaveAndEquippedSaveAreContentFree(t *testing.T) {
	allowedSprintSave := map[string]string{
		"Index":     "int",
		"UnitsDone": "float64",
	}
	allowedEquippedSave := map[string]string{
		"ItemID": "string",
		"TintID": "*string",
	}

	checkExact := func(t *testing.T, typ reflect.Type, allowed map[string]string) {
		t.Helper()
		if typ.NumField() != len(allowed) {
			t.Fatalf("%s has %d fields, expected exactly %d (%v)", typ.Name(), typ.NumField(), len(allowed), allowed)
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			wantType, ok := allowed[f.Name]
			if !ok {
				t.Errorf("%s.%s is not on the content-free allow-list", typ.Name(), f.Name)
				continue
			}
			if f.Type.String() != wantType {
				t.Errorf("%s.%s has type %s, want %s", typ.Name(), f.Name, f.Type.String(), wantType)
			}
		}
	}

	checkExact(t, reflect.TypeOf(SprintSave{}), allowedSprintSave)
	checkExact(t, reflect.TypeOf(EquippedSave{}), allowedEquippedSave)
}

// TestStatsSaveAndStatCountersSaveAreContentFree is Phase A1's (Analytics
// track, docs/plan/ROADMAP.md) extension of the same structural guard to
// the schema-2 `stats` field: StatsSave and its nested StatCountersSave
// must stay exactly a date string plus plain uint64 counts/durations,
// never grow a raw field, the same way SprintSave/EquippedSave already are
// above.
func TestStatsSaveAndStatCountersSaveAreContentFree(t *testing.T) {
	allowedStatsSave := map[string]string{
		"Date":       "string",
		"Today":      "store.StatCountersSave",
		"Lifetime":   "store.StatCountersSave",
		"CoinsToday": "store.CoinBreakdownSave",
		// History/Streak (Analytics Phase A3, docs/plan/A3-design.md
		// §4/§7 Task GO-2): the persisted rolling history + streak state.
		// Their own nested field-level coverage is
		// TestDayBucketSaveAndStreakSaveAreContentFree below.
		"History": "[]store.DayBucketSave",
		"Streak":  "store.StreakSave",
	}
	allowedStatCountersSave := map[string]string{
		"Keystrokes":         "uint64",
		"MouseActiveSeconds": "uint64",
		"ActiveSeconds":      "uint64",
		"IdleSeconds":        "uint64",
		"SprintsCompleted":   "uint64",
		"FocusSessions":      "uint64",
		"AppSwitches":        "uint64",
		// PausedSeconds (PR-5, schema 7, ARCHITECTURE.md Decision 14):
		// the persisted third time bucket — seconds during which dexel
		// observed nothing at all. The same content-free duration class
		// as every sibling, and because this type is reused it lands in
		// the today/lifetime buckets, every finalized DayBucketSave and
		// both halves of an ActiveSessionSave at once.
		"PausedSeconds": "uint64",
	}
	// CoinBreakdownSave (A2, docs/plan/A2-design.md §5/§7 Task GO-3): the
	// persisted per-signal coin split. Every field a whole-number coin
	// count — content-free by construction, same rule as
	// StatCountersSave above.
	allowedCoinBreakdownSave := map[string]string{
		"Keystrokes":    "uint64",
		"Mouse":         "uint64",
		"FocusSessions": "uint64",
		"AppSwitches":   "uint64",
	}

	checkExact := func(t *testing.T, typ reflect.Type, allowed map[string]string) {
		t.Helper()
		if typ.NumField() != len(allowed) {
			t.Fatalf("%s has %d fields, expected exactly %d (%v)", typ.Name(), typ.NumField(), len(allowed), allowed)
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			wantType, ok := allowed[f.Name]
			if !ok {
				t.Errorf("%s.%s is not on the content-free allow-list", typ.Name(), f.Name)
				continue
			}
			if f.Type.String() != wantType {
				t.Errorf("%s.%s has type %s, want %s", typ.Name(), f.Name, f.Type.String(), wantType)
			}
			lower := strings.ToLower(f.Name)
			forbiddenSubstrings := []string{
				"title", "text", "content", "keycode", "key_code", "clipboard",
				"url", "path", "document", "message", "body", "keyname", "char",
			}
			for _, bad := range forbiddenSubstrings {
				if strings.Contains(lower, bad) {
					t.Errorf("%s.%s name contains forbidden substring %q — looks like it could carry content", typ.Name(), f.Name, bad)
				}
			}
		}
	}

	checkExact(t, reflect.TypeOf(StatsSave{}), allowedStatsSave)
	checkExact(t, reflect.TypeOf(StatCountersSave{}), allowedStatCountersSave)
	checkExact(t, reflect.TypeOf(CoinBreakdownSave{}), allowedCoinBreakdownSave)
}

// TestDayBucketSaveAndStreakSaveAreContentFree is Analytics Phase A3's
// (docs/plan/A3-design.md §4/§7 Task GO-2) extension of the same
// structural guard to the schema-4 `stats.history`/`stats.streak` nested
// types: DayBucketSave must stay exactly a calendar date string, a reused
// StatCountersSave bucket, and two plain uint64 counts/durations;
// StreakSave must stay exactly two plain ints and one calendar date
// string — the same content-free class StatsSave.Date already is (see
// TestStatsSaveAndStatCountersSaveAreContentFree's allow-list comment).
// Never grow a raw field on either, the same way every other nested save
// type above is pinned.
func TestDayBucketSaveAndStreakSaveAreContentFree(t *testing.T) {
	allowedDayBucketSave := map[string]string{
		"Date":                     "string",
		"Counters":                 "store.StatCountersSave",
		"CoinsEarned":              "uint64",
		"LongestFocusBlockSeconds": "uint64",
	}
	allowedStreakSave := map[string]string{
		"Current":        "int",
		"Longest":        "int",
		"LastActiveDate": "string",
	}

	checkExact := func(t *testing.T, typ reflect.Type, allowed map[string]string) {
		t.Helper()
		if typ.NumField() != len(allowed) {
			t.Fatalf("%s has %d fields, expected exactly %d (%v)", typ.Name(), typ.NumField(), len(allowed), allowed)
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			wantType, ok := allowed[f.Name]
			if !ok {
				t.Errorf("%s.%s is not on the content-free allow-list", typ.Name(), f.Name)
				continue
			}
			if f.Type.String() != wantType {
				t.Errorf("%s.%s has type %s, want %s", typ.Name(), f.Name, f.Type.String(), wantType)
			}
			lower := strings.ToLower(f.Name)
			forbiddenSubstrings := []string{
				"title", "text", "content", "keycode", "key_code", "clipboard",
				"url", "path", "document", "message", "body", "keyname", "char",
			}
			for _, bad := range forbiddenSubstrings {
				if strings.Contains(lower, bad) {
					t.Errorf("%s.%s name contains forbidden substring %q — looks like it could carry content", typ.Name(), f.Name, bad)
				}
			}
		}
	}

	checkExact(t, reflect.TypeOf(DayBucketSave{}), allowedDayBucketSave)
	checkExact(t, reflect.TypeOf(StreakSave{}), allowedStreakSave)
}

// TestSessionSaveAndActiveSessionSaveAreContentFree is P2's (docs/plan/
// P2-design.md §5.1/§5.2, ADR 0017 Decision 5) extension of the same
// structural guard to the schema-6 `session`/`sessionLogHead` fields and
// the `sessions` table's payload shape: ActiveSessionSave must stay
// exactly an id, two RFC3339 timestamp strings, two reused
// StatCountersSave buckets and two plain uint64 counts/durations;
// SessionSave must stay exactly an id, two RFC3339 timestamp strings, a
// duration, a reused StatCountersSave bucket, two plain uint64 counts/
// durations, and a closed-set endReason string. Deliberately NO field
// named anything like "name" on either — a project name is user-authored
// CONFIG (§2.7, ConfigData.SessionNames in config.go), never a column
// here; this test is the direct structural analogue of P1's own proof
// that the dexel's name never reaches SaveData.
func TestSessionSaveAndActiveSessionSaveAreContentFree(t *testing.T) {
	allowedActiveSessionSave := map[string]string{
		"ID":                       "int",
		"StartedAt":                "string",
		"LastActivityAt":           "string",
		"Baseline":                 "store.StatCountersSave",
		"Watermark":                "store.StatCountersSave",
		"CoinsEarned":              "uint64",
		"LongestFocusBlockSeconds": "uint64",
	}
	allowedSessionSave := map[string]string{
		"ID":                       "int",
		"StartedAt":                "string",
		"EndedAt":                  "string",
		"DurationSeconds":          "uint64",
		"Counters":                 "store.StatCountersSave",
		"CoinsEarned":              "uint64",
		"LongestFocusBlockSeconds": "uint64",
		"EndReason":                "string",
	}

	checkExact := func(t *testing.T, typ reflect.Type, allowed map[string]string) {
		t.Helper()
		if typ.NumField() != len(allowed) {
			t.Fatalf("%s has %d fields, expected exactly %d (%v)", typ.Name(), typ.NumField(), len(allowed), allowed)
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			wantType, ok := allowed[f.Name]
			if !ok {
				t.Errorf("%s.%s is not on the content-free allow-list", typ.Name(), f.Name)
				continue
			}
			if f.Type.String() != wantType {
				t.Errorf("%s.%s has type %s, want %s", typ.Name(), f.Name, f.Type.String(), wantType)
			}
			lower := strings.ToLower(f.Name)
			forbiddenSubstrings := []string{
				"title", "text", "content", "keycode", "key_code", "clipboard",
				"url", "path", "document", "message", "body", "keyname", "char",
				"name",
			}
			for _, bad := range forbiddenSubstrings {
				if strings.Contains(lower, bad) {
					t.Errorf("%s.%s name contains forbidden substring %q — looks like it could carry content", typ.Name(), f.Name, bad)
				}
			}
		}
	}

	checkExact(t, reflect.TypeOf(ActiveSessionSave{}), allowedActiveSessionSave)
	checkExact(t, reflect.TypeOf(SessionSave{}), allowedSessionSave)
}
