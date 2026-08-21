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
}
