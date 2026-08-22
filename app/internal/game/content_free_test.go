package game

import (
	"reflect"
	"strings"
	"testing"
)

// TestStateMessageIsContentFree is S3's clone of
// internal/activity/content_free_test.go's TestSnapshotIsContentFree,
// applied to the thing that actually leaves the process: StateMessage is
// the exact `"type":"state"` WebSocket payload every connected browser
// tab receives (docs/ui-spec.md §6.1). Snapshot being content-free is
// meaningless on its own if something downstream (this struct) is free to
// re-add a window title, a keycode, or typed text on its way out over the
// wire. This test enumerates every field by reflection against an
// explicit allow-list; adding a field to StateMessage that isn't on the
// allow-list, or whose name suggests raw content, fails this test rather
// than silently shipping a privacy regression onto the wire.
func TestStateMessageIsContentFree(t *testing.T) {
	allowed := map[string]string{
		"Type":         "string",
		"V":            "int",
		"ActiveState":  "string",
		"ActivityLine": "string",
		"DevCash":      "uint64",
		"Level":        "int",
		"XP":           "uint64",
		"StoreOpen":    "bool",
		"Sprint":       "game.SprintView",
		"ScreenLines":  "[]string",
		"TickerLines":  "[]string",
		"Equipped":     "map[string]game.EquippedRef",
		"OwnedItems":   "[]string",
		"OwnedTints":   "[]string",
		"Stats":        "game.StatsView",
		// Phase P1 (Identity & first minutes, docs/plan/PRODUCT-EVOLUTION.md
		// §5). Config carries the dexel's name — the ONE string on this
		// wire that is neither a count, a duration, an id, nor a
		// machine-derived app identity. It is allow-listed as
		// USER-AUTHORED CONFIG, on ADR 0014's explicit category
		// distinction: "the user-authored name is a *different category*
		// from observed activity — data the user deliberately writes
		// about their own pet, not surveillance of their work", living in
		// the unsigned config.json outside the protected save. That
		// distinction is what makes this entry legitimate and is ALSO
		// exactly why it buys nothing else: an OBSERVED-content field
		// (a window title, typed text, a path) is still a violation here,
		// still has no allow-list entry, and still fails this test — see
		// TestStateMessageRejectsObservedContentFields below, which
		// proves the guard did not go soft when this field landed.
		// Onboarding is a plain server-computed bool.
		"Config":     "game.ConfigView",
		"Onboarding": "bool",
	}

	// Field/type names whose presence anywhere on StateMessage is itself a
	// violation, independent of the allow-list above (belt and suspenders:
	// this catches a rename of an allowed field into something that smells
	// like content, e.g. "ActivityLine" -> "ActivityLineFullText").
	forbiddenSubstrings := []string{
		"title", "text", "content", "keycode", "key_code", "clipboard",
		"url", "path", "document", "message", "body", "keyname", "char",
	}

	typ := reflect.TypeOf(StateMessage{})
	if typ.NumField() != len(allowed) {
		t.Fatalf("StateMessage has %d fields, expected exactly %d (%v) — a field was added or removed without updating this privacy test",
			typ.NumField(), len(allowed), allowed)
	}

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		wantType, ok := allowed[f.Name]
		if !ok {
			t.Errorf("StateMessage.%s is not on the content-free allow-list — every field must be justified here", f.Name)
			continue
		}
		if f.Type.String() != wantType {
			t.Errorf("StateMessage.%s has type %s, want %s", f.Name, f.Type.String(), wantType)
		}

		lower := strings.ToLower(f.Name)
		for _, bad := range forbiddenSubstrings {
			if strings.Contains(lower, bad) {
				t.Errorf("StateMessage.%s name contains forbidden substring %q — looks like it could carry content", f.Name, bad)
			}
		}
	}
}

// TestSprintViewAndEquippedRefAreContentFree extends the same guard to
// StateMessage's two nested struct types (SprintView, EquippedRef) — the
// top-level field-count check above only proves those TWO fields exist
// with the right named-type; it says nothing about what's INSIDE
// SprintView/EquippedRef, which could otherwise grow a content field
// without ever touching StateMessage itself.
func TestSprintViewAndEquippedRefAreContentFree(t *testing.T) {
	allowedSprintView := map[string]string{
		"Index":     "int",
		"Name":      "string",
		"Progress":  "float64",
		"Target":    "float64",
		"UnitLabel": "string",
	}
	allowedEquippedRef := map[string]string{
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

	checkExact(t, reflect.TypeOf(SprintView{}), allowedSprintView)
	checkExact(t, reflect.TypeOf(EquippedRef{}), allowedEquippedRef)

	// Phase P1: ConfigView is StateMessage's third nested struct type, so
	// it needs the same INSIDE-the-struct coverage — otherwise the
	// top-level "Config is a game.ConfigView" check above would happily
	// let ConfigView itself grow an observed-content field. Exactly one
	// field, exactly a string: the user-authored name (ADR 0014's
	// different-category rationale, quoted in full on the allow-list
	// entry in TestStateMessageIsContentFree). "Name" is not on the
	// forbidden-substring list and must not be — the forbidden list
	// targets OBSERVED content ("title", "text", "content", "keycode",
	// "clipboard", "url", "path", ...), which this is not.
	allowedConfigView := map[string]string{
		"Name": "string",
	}
	checkExact(t, reflect.TypeOf(ConfigView{}), allowedConfigView)
}

// TestStateMessageRejectsObservedContentFields proves the content-free
// guard above still BITES after Phase P1 added the first free-text field
// (ConfigView.Name) to the wire. The worry a reviewer should have when a
// privacy allow-list grows a string is "did the test get weaker?" — so
// this test re-runs the two rules that would have to fail for an
// observed-content field to slip through, against synthetic types shaped
// like the mistakes someone would actually make:
//
//   - a field that is not on the allow-list at all (the count check and
//     the per-field lookup both catch it), and
//   - a field whose NAME smells like observed content, even if someone
//     also added it to an allow-list (the forbidden-substring scan
//     catches it).
//
// It deliberately exercises the rules rather than the production type, so
// it cannot be satisfied by loosening the production allow-list.
func TestStateMessageRejectsObservedContentFields(t *testing.T) {
	forbiddenSubstrings := []string{
		"title", "text", "content", "keycode", "key_code", "clipboard",
		"url", "path", "document", "message", "body", "keyname", "char",
	}
	// The exact field names a privacy regression would arrive as.
	for _, bad := range []string{
		"WindowTitle", "TypedText", "ClipboardContents", "ActiveKeycode",
		"ActiveUrl", "DocumentPath", "LastCharTyped", "KeyName",
	} {
		hit := false
		lower := strings.ToLower(bad)
		for _, sub := range forbiddenSubstrings {
			if strings.Contains(lower, sub) {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("the forbidden-substring scan would NOT flag a field named %q — the observed-content guard has a hole", bad)
		}
	}
	// ...and the name Phase P1 legitimately added must NOT be flagged by
	// that same scan, or the guard is unusable and someone will delete it.
	if lower := strings.ToLower("Name"); func() bool {
		for _, sub := range forbiddenSubstrings {
			if strings.Contains(lower, sub) {
				return true
			}
		}
		return false
	}() {
		t.Error("the forbidden-substring scan flags ConfigView.Name — user-authored config is not observed content (ADR 0014)")
	}
	// The allow-list is exact-count, so ANY unlisted field fails: assert
	// the production type's field count still equals its allow-list, which
	// is what makes "add a field, forget the list" a build failure.
	if got, want := reflect.TypeOf(StateMessage{}).NumField(), 17; got != want {
		t.Errorf("StateMessage has %d fields, this test pins %d — update BOTH allow-lists deliberately, never just the count", got, want)
	}
}

// TestStatsViewAndStatCountersAreContentFree is Phase A1's (Analytics
// track, docs/plan/ROADMAP.md) extension of the same structural guard to
// StateMessage.Stats: StatsView and its nested StatCounters must stay
// exactly counts/durations, never grow a raw field, the same way every
// other wire type in this package is audited.
func TestStatsViewAndStatCountersAreContentFree(t *testing.T) {
	allowedStatsView := map[string]string{
		"Today":      "game.StatCounters",
		"Lifetime":   "game.StatCounters",
		"CoinsToday": "game.CoinBreakdown",
		// A3 (§5): the dense 30-day wire history + the server-computed
		// effective streak. See the checkExact blocks below for
		// DayStat/StreakView's own field-level coverage.
		"History": "[]game.DayStat",
		"Streak":  "game.StreakView",
	}
	allowedStatCounters := map[string]string{
		"Keystrokes":         "uint64",
		"MouseActiveSeconds": "uint64",
		"ActiveSeconds":      "uint64",
		"IdleSeconds":        "uint64",
		"SprintsCompleted":   "uint64",
		"FocusSessions":      "uint64",
		"AppSwitches":        "uint64",
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

	checkExact(t, reflect.TypeOf(StatsView{}), allowedStatsView)
	checkExact(t, reflect.TypeOf(StatCounters{}), allowedStatCounters)

	// CoinBreakdown (A2 §5/§6): today's earned DevCash split by signal —
	// every field a whole-number coin COUNT, never a work float (those
	// deliberately never cross the wire, see Game's workKeys/workMouse/
	// workFocus/workSwitch doc comment) and never anything content-like.
	allowedCoinBreakdown := map[string]string{
		"Keystrokes":    "uint64",
		"Mouse":         "uint64",
		"FocusSessions": "uint64",
		"AppSwitches":   "uint64",
	}
	checkExact(t, reflect.TypeOf(CoinBreakdown{}), allowedCoinBreakdown)

	// A3 (§5) — DayStat: one dense wire history entry. "Date" is a local
	// "YYYY-MM-DD" calendar date, the exact same content-free class
	// internal/store's StatsSave.Date already is (a fixed-format
	// timestamp string, never free text) — allow-listed as plain
	// "string" exactly like that precedent, not flagged by the
	// forbidden-substring scan above (which does not (and must not)
	// treat "date" itself as content-like).
	allowedDayStat := map[string]string{
		"Date":                     "string",
		"Keystrokes":               "uint64",
		"MouseActiveSeconds":       "uint64",
		"ActiveSeconds":            "uint64",
		"IdleSeconds":              "uint64",
		"SprintsCompleted":         "uint64",
		"FocusSessions":            "uint64",
		"AppSwitches":              "uint64",
		"CoinsEarned":              "uint64",
		"IsActive":                 "bool",
		"LongestFocusBlockSeconds": "uint64",
	}
	checkExact(t, reflect.TypeOf(DayStat{}), allowedDayStat)

	// A3 (§2/§5) — StreakView: the server-computed effective streak. Two
	// plain integers, nothing else — no lastActiveDate here (that stays
	// server-side persisted state, never sent on the wire).
	allowedStreakView := map[string]string{
		"Current": "int",
		"Longest": "int",
	}
	checkExact(t, reflect.TypeOf(StreakView{}), allowedStreakView)
}
