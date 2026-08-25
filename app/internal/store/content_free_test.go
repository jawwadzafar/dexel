package store

import (
	"reflect"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/contentfree"
)

// contentFreeRegistry is store's guarded root graph: SaveData (the
// on-disk, MAC-protected shape at ~/.config/dexel/state.json — "this
// file contains no user content") and ConfigData (the on-disk, UNSIGNED
// shape at config.json — the one place free text legitimately lives,
// per ADR 0014's config/state split), plus every named struct type
// reachable from either, however many levels deep.
//
// Being content-free upstream (activity.Snapshot, game.StateMessage) is
// meaningless if the thing actually written to the user's disk is free
// to grow a raw field later, and — the specific weakness this file's
// walker-based design closes (docs/rust-port-evaluation.md §2.6) — a
// content-capable field nested two levels into SaveData no longer needs
// a human to remember a manual checkExact call: internal/contentfree.Audit
// discovers every reachable type by walking the real reflect.Type graph
// and fails this test if any of them is not registered here.
func contentFreeRegistry() contentfree.Registry {
	return contentfree.Registry{
		"store.SaveData": {
			Sample: SaveData{},
			Allowed: map[string]string{
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
				// Session/SessionLogHead (P2, docs/plan/P2-design.md
				// §5.1, ADR 0017 Decision 5, schema 6): the in-progress
				// session and the session log's opaque chain head.
				// Deliberately no Name field anywhere under Session — a
				// project name is user-authored CONFIG (§2.7), never a
				// column here; see TestSessionSaveTypesNeverCarryAName
				// below.
				"Session":        "*store.ActiveSessionSave",
				"SessionLogHead": "string",
				// Mac (SEC-1, ADR 0014): the hex HMAC-SHA256 tag over the
				// rest of this struct — a fixed-width DIGEST, not
				// content, and a deterministic function of the
				// (already content-free) fields above.
				"Mac": "string",
				// Paused (PR-5, ARCHITECTURE.md Decision 16 / FORK D,
				// schema 7): a single bool recording that the user asked
				// dexel to STOP observing — a user intent, like
				// ConfigView.Name, but unlike a name it is not text and
				// carries no information beyond one bit.
				"Paused": "bool",
			},
		},

		"store.SprintSave": {
			Sample: SprintSave{},
			Allowed: map[string]string{
				"Index":     "int",
				"UnitsDone": "float64",
			},
		},

		"store.EquippedSave": {
			Sample: EquippedSave{},
			Allowed: map[string]string{
				"ItemID": "string",
				"TintID": "*string",
			},
		},

		"store.StatsSave": {
			Sample: StatsSave{},
			Allowed: map[string]string{
				"Date":       "string",
				"Today":      "store.StatCountersSave",
				"Lifetime":   "store.StatCountersSave",
				"CoinsToday": "store.CoinBreakdownSave",
				// A3 (§5): the persisted rolling history + streak state.
				"History": "[]store.DayBucketSave",
				"Streak":  "store.StreakSave",
			},
		},

		"store.StatCountersSave": {
			Sample: StatCountersSave{},
			Allowed: map[string]string{
				"Keystrokes":         "uint64",
				"MouseActiveSeconds": "uint64",
				"ActiveSeconds":      "uint64",
				"IdleSeconds":        "uint64",
				"SprintsCompleted":   "uint64",
				"FocusSessions":      "uint64",
				"AppSwitches":        "uint64",
				// PausedSeconds (PR-5, schema 7): the persisted third
				// time bucket — seconds during which dexel observed
				// nothing at all. Reused verbatim in the today/lifetime
				// buckets, every finalized DayBucketSave, and both
				// halves of an ActiveSessionSave.
				"PausedSeconds": "uint64",
			},
		},

		"store.CoinBreakdownSave": {
			Sample: CoinBreakdownSave{},
			Allowed: map[string]string{
				"Keystrokes":    "uint64",
				"Mouse":         "uint64",
				"FocusSessions": "uint64",
				"AppSwitches":   "uint64",
			},
		},

		"store.DayBucketSave": {
			Sample: DayBucketSave{},
			Allowed: map[string]string{
				"Date":                     "string",
				"Counters":                 "store.StatCountersSave",
				"CoinsEarned":              "uint64",
				"LongestFocusBlockSeconds": "uint64",
			},
		},

		"store.StreakSave": {
			Sample: StreakSave{},
			Allowed: map[string]string{
				"Current":        "int",
				"Longest":        "int",
				"LastActiveDate": "string",
			},
		},

		"store.ActiveSessionSave": {
			Sample: ActiveSessionSave{},
			Allowed: map[string]string{
				"ID":                       "int",
				"StartedAt":                "string",
				"LastActivityAt":           "string",
				"Baseline":                 "store.StatCountersSave",
				"Watermark":                "store.StatCountersSave",
				"CoinsEarned":              "uint64",
				"LongestFocusBlockSeconds": "uint64",
			},
			// Deliberately NO Exceptions entry: unlike game's wire
			// views, this type must never carry a Name field at all
			// (§2.7) — see TestSessionSaveTypesNeverCarryAName.
		},

		"store.SessionSave": {
			Sample: SessionSave{},
			Allowed: map[string]string{
				"ID":                       "int",
				"StartedAt":                "string",
				"EndedAt":                  "string",
				"DurationSeconds":          "uint64",
				"Counters":                 "store.StatCountersSave",
				"CoinsEarned":              "uint64",
				"LongestFocusBlockSeconds": "uint64",
				"EndReason":                "string", // user|idle|maxDuration
			},
		},

		// ConfigData (SEC-1 design §1, ADR 0014) is the SECOND root: the
		// unsigned config.json shape. Name and SessionNames are the ONLY
		// two places free text legitimately lives anywhere in dexel's
		// persistence (design §1.1, P2-design.md §2.7) — both cited
		// exceptions below. Autostart is a closed-set string written
		// only by `dexel autostart enable/disable`.
		//
		// AlwaysOnTop/ShowAwayTime (SET-1, docs/ui-spec.md §11) are user
		// PREFERENCES: one bit each, written by the user through the
		// Settings modal (SET_PREF) or by hand in this very file. A bool
		// cannot carry a window title, a path or typed text, and neither
		// says anything about the work — one is a window property, the
		// other decides whether the Activity modal DISPLAYS durations that
		// are recorded and persisted either way (recording is unchanged:
		// StatCountersSave.IdleSeconds above keeps its own entry, and
		// nothing in SaveData branches on this field).
		//
		// SoundEnabled (SOUND-1, docs/ui-spec.md §13) is a third such
		// preference and is listed as *bool rather than bool for a reason
		// that has nothing to do with privacy: sound defaults ON, so the
		// pointer is what distinguishes "never chosen" from "chosen off"
		// (see ConfigData.SoundEnabledOrDefault). A *bool carries exactly
		// the same three-valued nothing a bool carries two of — no text, no
		// path, no observation of the work — and the audit lists the
		// pointer type explicitly so a later change from *bool to bool (or
		// to *string) cannot slip past unreviewed.
		"store.ConfigData": {
			Sample: ConfigData{},
			Allowed: map[string]string{
				"Name":         "string",
				"SessionNames": "map[string]string",
				"Autostart":    "string",
				"AlwaysOnTop":  "bool",
				"ShowAwayTime": "bool",
				"SoundEnabled": "*bool",
			},
			Exceptions: map[string]string{
				"Name": "ADR 0014 (docs/adr/0014-save-integrity-hmac-and-config-split.md): " +
					"the dexel's own user-authored name — the ONE piece of free text SEC-1 design §1.1 " +
					"deliberately carves out of the protected save",
				"SessionNames": "ADR 0017 Decision 2 / P2-design.md §2.7: per-session user-authored project names, " +
					"keyed by session id — the second (and last) piece of legitimate free text, kept in the " +
					"unsigned config.json specifically so it can never reach the MAC'd SaveData graph",
			},
		},
	}
}

// TestSaveDataAndConfigGraphsAreContentFree is store's structural
// privacy test, run over ALL of this package's persisted roots at once
// (Audit's reachability/rot cross-checks are only meaningful against the
// full declared root set — auditing one root at a time would wrongly
// flag the other roots' whole graphs as "registered but unreachable"):
//
//   - SaveData, the protected save: must only ever carry counts,
//     durations, ids, closed-set enums, ISO timestamps, or a
//     fixed-width digest — never a window title, a keycode, typed text,
//     or (per §2.7) even a name — and neither may any type reachable
//     from it, at any depth.
//   - ConfigData, config.json: deliberately WHERE free text lives (SEC-1
//     design §1.2), so the two fields that legitimately carry it must be
//     exactly the two cited exceptions — no more, no fewer — and
//     Autostart, the one other field on this type, stays a plain
//     closed-set string.
//   - SessionSave, the standalone finished-session row shape (see its
//     root declaration's comment below for why it needs its own root).
//
// internal/contentfree.Audit walks all three graphs: any unlisted field,
// any content-smelling name without a cited exception, or any nested
// type nobody registered fails this test rather than silently shipping a
// privacy regression into a save file.
func TestSaveDataAndConfigGraphsAreContentFree(t *testing.T) {
	roots := []reflect.Type{
		reflect.TypeOf(SaveData{}),
		reflect.TypeOf(ConfigData{}),
		// SessionSave is a THIRD, independent root, not a descendant of
		// SaveData by Go type reachability: finished sessions are
		// chain-MAC'd rows in the separate `sessions` table (db.go's
		// sessionsDDL), each a standalone marshaled SessionSave BLOB —
		// "exactly a table's shape" (§5.1/§5.2) — never nested inside
		// SaveData itself. It still needs the identical structural
		// guard, so it is declared as its own root rather than assumed
		// covered by SaveData's graph.
		reflect.TypeOf(SessionSave{}),
	}
	for _, msg := range contentfree.Audit(roots, contentFreeRegistry(), contentfree.DefaultForbidden) {
		t.Error(msg)
	}
}

// TestSessionSaveTypesNeverCarryAName is the direct structural analogue
// of P1's own proof that the dexel's name never reaches SaveData
// (§2.7): ActiveSessionSave and SessionSave must have NO field whose
// name matches "name" at all — not even behind a cited exception,
// because unlike game's wire views there is no legitimate reason for one
// to exist here. This is asserted independent of the registry above (so
// it cannot be satisfied by quietly adding a Name exception to those two
// TypeSpecs) by scanning the live type's fields directly.
func TestSessionSaveTypesNeverCarryAName(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(ActiveSessionSave{}), reflect.TypeOf(SessionSave{})} {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.Name == "Name" || f.Name == "Names" {
				t.Errorf("%s.%s: session save types must never carry a name field (§2.7) — a project name is user-authored CONFIG, kept in ConfigData.SessionNames, never a column here", typ.Name(), f.Name)
			}
		}
	}

	reg := contentFreeRegistry()
	for _, name := range []string{"store.ActiveSessionSave", "store.SessionSave"} {
		if len(reg[name].Exceptions) != 0 {
			t.Errorf("%s carries a contentfree.Exceptions entry — session save types must have none (§2.7)", name)
		}
	}
}

// TestContentFreeRejectsObservedContentFields proves the guard above
// still BITES. It exercises Audit against a synthetic type shaped like
// the mistakes someone would actually make — a field whose NAME smells
// like observed content, allow-listed anyway and with no exception — so
// this test cannot be satisfied by loosening the real registry.
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
		"store.syntheticLeak": {
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
}
