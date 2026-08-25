package game

import (
	"reflect"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/contentfree"
)

// contentFreeRegistry is game's guarded root graph: StateMessage, the
// exact `"type":"state"` WebSocket payload every connected browser tab
// receives (docs/ui-spec.md §6.1), plus every named struct type
// reachable from it however many levels deep. Snapshot being
// content-free (internal/activity) is meaningless on its own if
// something downstream is free to re-add a window title, a keycode, or
// typed text on its way out over the wire.
//
// Unlike the incumbent per-struct tests this replaces, a new nested type
// introduced ANYWHERE under StateMessage does not need a new manual
// checkExact call to be caught: internal/contentfree.Audit discovers it
// by walking the real reflect.Type graph and fails if it is not
// registered here (docs/rust-port-evaluation.md §2.6 names this
// exact recursion gap in the old design).
//
// nameException is the one repeated, cited carve-out: a field literally
// named "Name" is legitimate exactly twice on this graph — the dexel's
// own name (ConfigView) and a session's user-authored project name
// (ActiveSessionView, SessionView) — both on ADR 0014's explicit
// category distinction: "the user-authored name is a *different
// category* from observed activity — data the user deliberately writes
// about their own pet, not surveillance of their work". Every other
// "name"-shaped field on this graph is NOT exempted and must still fail.
func nameException() map[string]string {
	return map[string]string{
		"Name": "ADR 0014 (docs/adr/0014-save-integrity-hmac-and-config-split.md): " +
			"user-authored config category, not observed content",
	}
}

func contentFreeRegistry() contentfree.Registry {
	return contentfree.Registry{
		"game.StateMessage": {
			Sample: StateMessage{},
			Allowed: map[string]string{
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
				// Phase P2 (Sessions, docs/plan/P2-design.md §6.1, ADR
				// 0017): the `sessions` block, always sent.
				"Sessions": "game.SessionsView",
				// Phase P1 (Identity & first minutes,
				// docs/plan/PRODUCT-EVOLUTION.md §5). Config carries the
				// dexel's name — the ONE string on this wire that is
				// neither a count, a duration, an id, nor a
				// machine-derived app identity. See ConfigView's own
				// entry below for the ADR 0014 citation on its Name
				// field.
				"Config":     "game.ConfigView",
				"Onboarding": "bool",
				// PR-5 (pause, docs/production-runtime/ARCHITECTURE.md
				// Decision 15): a plain server-computed bool saying
				// whether tracking is currently off — the OPPOSITE of a
				// privacy concern, and it can carry nothing but
				// true/false.
				"Paused": "bool",
			},
		},

		"game.SprintView": {
			Sample: SprintView{},
			Allowed: map[string]string{
				"Index":     "int",
				"Name":      "string",
				"Progress":  "float64",
				"Target":    "float64",
				"UnitLabel": "string",
			},
			// SprintView.Name comes from the STATIC sprint-definition
			// list (sprint.go), never from input — content-free by
			// construction rather than by ADR 0014's user-authored
			// category, but exempted the same way since the scan cannot
			// tell the two apart by name alone.
			Exceptions: map[string]string{
				"Name": "sprint.go: sourced from the static sprintDefs table, never from input — not user-authored, not observed",
			},
		},

		"game.EquippedRef": {
			Sample: EquippedRef{},
			Allowed: map[string]string{
				"ItemID": "string",
				"TintID": "*string",
			},
		},

		// ConfigView (Phase P1 + SET-1): the user-authored dexel name
		// (ADR 0014's different-category rationale) plus SET-1's two user
		// PREFERENCES (docs/ui-spec.md §11). Both preferences are plain
		// bools — a single bit each, chosen by the user about their own
		// dexel, incapable of carrying an observation of any kind:
		//
		//   - AlwaysOnTop: whether the desktop shell pins its window.
		//     Says nothing about the user's work; it is a window property.
		//   - ShowAwayTime: whether the Activity modal DISPLAYS the
		//     already-sent, already-content-free away durations. Note the
		//     direction: this field adds no data to the wire and removes
		//     none — IdleSeconds keeps its own entries on
		//     StatCounters/DayStat/SessionView above, untouched, because
		//     recording is deliberately unchanged (ADR 0010/0013) and only
		//     presentation is the user's call.
		//   - SoundEnabled (SOUND-1, docs/ui-spec.md §13): whether the page
		//     plays its six chiptune effects. One bit, chosen by the user,
		//     about the user's own speakers. It is the purest case of the
		//     category this whole block describes: nothing is recorded when
		//     it is on, nothing is withheld when it is off, and the value
		//     says nothing whatsoever about the work — a muted dexel earns
		//     and counts exactly what a noisy one does
		//     (TestSoundEnabledChangesNothingButItself pins that).
		"game.ConfigView": {
			Sample: ConfigView{},
			Allowed: map[string]string{
				"Name":         "string",
				"AlwaysOnTop":  "bool",
				"ShowAwayTime": "bool",
				"SoundEnabled": "bool",
			},
			Exceptions: nameException(),
		},

		"game.StatsView": {
			Sample: StatsView{},
			Allowed: map[string]string{
				"Today":      "game.StatCounters",
				"Lifetime":   "game.StatCounters",
				"CoinsToday": "game.CoinBreakdown",
				// A3 (§5): the dense 30-day wire history + the
				// server-computed effective streak.
				"History": "[]game.DayStat",
				"Streak":  "game.StreakView",
			},
		},

		"game.StatCounters": {
			Sample: StatCounters{},
			Allowed: map[string]string{
				"Keystrokes":         "uint64",
				"MouseActiveSeconds": "uint64",
				"ActiveSeconds":      "uint64",
				"IdleSeconds":        "uint64",
				"SprintsCompleted":   "uint64",
				"FocusSessions":      "uint64",
				"AppSwitches":        "uint64",
				// PausedSeconds (PR-5, ARCHITECTURE.md Decision 14): one
				// more content-free duration — seconds during which
				// dexel observed nothing.
				"PausedSeconds": "uint64",
			},
		},

		// CoinBreakdown (A2 §5/§6): today's earned DevCash split by
		// signal — every field a whole-number coin COUNT.
		"game.CoinBreakdown": {
			Sample: CoinBreakdown{},
			Allowed: map[string]string{
				"Keystrokes":    "uint64",
				"Mouse":         "uint64",
				"FocusSessions": "uint64",
				"AppSwitches":   "uint64",
			},
		},

		// A3 (§5) — DayStat: one dense wire history entry.
		"game.DayStat": {
			Sample: DayStat{},
			Allowed: map[string]string{
				"Date":                     "string",
				"Keystrokes":               "uint64",
				"MouseActiveSeconds":       "uint64",
				"ActiveSeconds":            "uint64",
				"IdleSeconds":              "uint64",
				"SprintsCompleted":         "uint64",
				"FocusSessions":            "uint64",
				"AppSwitches":              "uint64",
				"PausedSeconds":            "uint64",
				"CoinsEarned":              "uint64",
				"IsActive":                 "bool",
				"LongestFocusBlockSeconds": "uint64",
			},
			// "Date" is a local "YYYY-MM-DD" calendar date, the same
			// fixed-format-timestamp content-free class store's
			// StatsSave.Date already is — not flagged by the
			// forbidden-substring scan (it does not, and must not, treat
			// "date" as content-like), so no exception is needed for it.
		},

		// A3 (§2/§5) — StreakView: the server-computed effective streak.
		"game.StreakView": {
			Sample: StreakView{},
			Allowed: map[string]string{
				"Current": "int",
				"Longest": "int",
			},
		},

		"game.SessionsView": {
			Sample: SessionsView{},
			Allowed: map[string]string{
				"Active":  "*game.ActiveSessionView",
				"Summary": "game.SessionsSummary",
				"Recent":  "[]game.SessionView",
			},
		},

		"game.ActiveSessionView": {
			Sample: ActiveSessionView{},
			Allowed: map[string]string{
				"ID":                       "int",
				"Name":                     "string",
				"StartedAt":                "string",
				"ElapsedSeconds":           "uint64",
				"Keystrokes":               "uint64",
				"MouseActiveSeconds":       "uint64",
				"ActiveSeconds":            "uint64",
				"IdleSeconds":              "uint64",
				"SprintsCompleted":         "uint64",
				"FocusSessions":            "uint64",
				"AppSwitches":              "uint64",
				"PausedSeconds":            "uint64",
				"CoinsEarned":              "uint64",
				"LongestFocusBlockSeconds": "uint64",
			},
			Exceptions: nameException(),
		},

		"game.SessionView": {
			Sample: SessionView{},
			Allowed: map[string]string{
				"ID":                       "int",
				"Name":                     "string",
				"StartedAt":                "string",
				"EndedAt":                  "string",
				"DurationSeconds":          "uint64",
				"Keystrokes":               "uint64",
				"MouseActiveSeconds":       "uint64",
				"ActiveSeconds":            "uint64",
				"IdleSeconds":              "uint64",
				"SprintsCompleted":         "uint64",
				"FocusSessions":            "uint64",
				"AppSwitches":              "uint64",
				"PausedSeconds":            "uint64",
				"CoinsEarned":              "uint64",
				"LongestFocusBlockSeconds": "uint64",
				"EndReason":                "string", // closed set: user|idle|maxDuration
			},
			Exceptions: nameException(),
		},

		"game.SessionsSummary": {
			Sample: SessionsSummary{},
			Allowed: map[string]string{
				"Completed":             "uint64",
				"ThisWeek":              "int",
				"LongestSessionSeconds": "uint64",
			},
		},
	}
}

// TestStateMessageGraphIsContentFree is game's structural privacy test:
// StateMessage must only ever carry counts, booleans, durations, ids,
// closed-set enums, ISO timestamps, or an ADR-0014-cited user-authored
// name — never a window title, a keycode, or typed text — and neither
// may any type reachable from it, at any depth. It runs
// internal/contentfree's recursive Audit against StateMessage as the
// sole root: any unlisted field, any content-smelling name not covered
// by a cited exception, or any nested type nobody registered fails this
// test rather than silently shipping a privacy regression onto the wire.
func TestStateMessageGraphIsContentFree(t *testing.T) {
	roots := []reflect.Type{reflect.TypeOf(StateMessage{})}
	for _, msg := range contentfree.Audit(roots, contentFreeRegistry(), contentfree.DefaultForbidden) {
		t.Error(msg)
	}
}

// TestContentFreeRejectsObservedContentFields proves the guard above
// still BITES after Phase P1/P2 added user-authored name fields to the
// wire. It exercises Audit against synthetic types shaped like the
// mistakes someone would actually make — a field whose NAME smells like
// observed content, allow-listed anyway and with no exception — so this
// test cannot be satisfied by loosening the real registry.
func TestContentFreeRejectsObservedContentFields(t *testing.T) {
	type syntheticLeak struct {
		WindowTitle       string
		TypedText         string
		ClipboardContents string
		ActiveKeycode     string
		ActiveUrl         string
		DocumentPath      string
		LastCharTyped     string
		KeyName           string
	}
	reg := contentfree.Registry{
		"game.syntheticLeak": {
			Sample: syntheticLeak{},
			Allowed: map[string]string{
				"WindowTitle": "string", "TypedText": "string", "ClipboardContents": "string",
				"ActiveKeycode": "string", "ActiveUrl": "string", "DocumentPath": "string",
				"LastCharTyped": "string", "KeyName": "string",
			},
		},
	}
	got := contentfree.Audit([]reflect.Type{reflect.TypeOf(syntheticLeak{})}, reg, contentfree.DefaultForbidden)
	if len(got) < 8 {
		t.Fatalf("expected the forbidden-substring scan to flag all 8 synthetic content-shaped fields even though they are allow-listed, got %d violations: %v", len(got), got)
	}

	// ...and the exact "Name" field Phase P1/P2 legitimately added must
	// still be exempt only where it is explicitly cited, never as a
	// side effect of loosening DefaultForbidden itself.
	found := false
	for _, bad := range contentfree.DefaultForbidden {
		if bad == "name" {
			found = true
		}
	}
	if !found {
		t.Fatal(`DefaultForbidden no longer contains "name" — the ConfigView.Name/SessionView.Name exceptions have gone vacuous, they exempt nothing`)
	}
}

// TestNoUncitedNameExceptionsInThisRegistry guards the registry itself:
// every Exceptions entry this package declares must carry the ADR 0014
// citation, never an empty or vague reason — internal/contentfree.Audit
// already enforces this generically, but this test pins the count so a
// silent proliferation of exceptions (each one a place "name" stops
// being checked) does not go unnoticed.
func TestNoUncitedNameExceptionsInThisRegistry(t *testing.T) {
	const wantExceptionTypes = 3 // ConfigView, ActiveSessionView, SessionView
	got := 0
	for typeName, spec := range contentFreeRegistry() {
		for field, reason := range spec.Exceptions {
			if reason == "" {
				t.Errorf("%s.%s has an uncited exception", typeName, field)
			}
			if field == "Name" && typeName != "game.SprintView" {
				got++
			}
		}
	}
	if got != wantExceptionTypes {
		t.Errorf("expected exactly %d ADR-0014-cited Name exceptions on this graph, found %d — an exception was added or removed without updating this pin", wantExceptionTypes, got)
	}
}
