// Package store persists game.Game to a local SQLite database
// (docs/plan/DB-1-design.md, docs/adr/0016-sqlite-persistence.md). It
// knows about
// game.Game's public API but nothing about the engine or activity
// packages — persistence is a leaf, not a hub. config.json (config.go)
// stays plain, unsigned, hand-editable JSON — DB-1 only moves the
// protected economy snapshot at state.db; a one-time migration reads a
// pre-DB-1 state.json exactly once (db.go's importJSON) and never
// touches it again afterward.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/game"
	"github.com/jawwadzafar/dexel/app/internal/paths"
)

// ErrFutureSchema is Load's error (wrapped with detail) when the save
// file's schema is newer than CurrentSchema — see Load's doc comment.
var ErrFutureSchema = errors.New("save schema is newer than this build supports")

// EquippedSave is one slot's persisted equip choice.
type EquippedSave struct {
	ItemID string  `json:"itemId"`
	TintID *string `json:"tintId,omitzero"`
}

// SprintSave is the persisted in-progress sprint.
type SprintSave struct {
	Index     int     `json:"index"`
	UnitsDone float64 `json:"unitsDone"`
}

// StatCountersSave is one persisted stats bucket — content-free by
// construction (Analytics track Phase A1, docs/plan/ROADMAP.md: "counts and
// durations only"). Deliberately its OWN declaration rather than an import
// of game.StatCounters: this package "knows about game.Game's public API
// but nothing about the engine or activity packages" (this file's own doc
// comment), and every other persisted type here (SprintSave, EquippedSave)
// already follows that same decoupling even where the shape happens to
// coincide with a game package type.
type StatCountersSave struct {
	Keystrokes         uint64 `json:"keystrokes"`
	MouseActiveSeconds uint64 `json:"mouseActiveSeconds"`
	ActiveSeconds      uint64 `json:"activeSeconds"`
	IdleSeconds        uint64 `json:"idleSeconds"`
	SprintsCompleted   uint64 `json:"sprintsCompleted"`
	// FocusSessions and AppSwitches (A2, docs/plan/A2-design.md §5/§7 Task
	// GO-3) mirror game.StatCounters' fields of the same name, added in
	// schema 3 (see CurrentSchema's doc comment). A schema-2 file has
	// neither key; json.Unmarshal leaves them at 0, the correct "none
	// observed yet" state — additive, non-breaking, same pattern as the
	// schema 1->2 bump.
	FocusSessions uint64 `json:"focusSessions"`
	AppSwitches   uint64 `json:"appSwitches"`
	// PausedSeconds (PR-5, docs/production-runtime/ARCHITECTURE.md
	// Decision 14) mirrors game.StatCounters.PausedSeconds, added in
	// schema 7 (see CurrentSchema's doc comment). A schema-6 file has no
	// such key; json.Unmarshal leaves it at 0, the correct "this build
	// never recorded a paused second" state — additive and non-breaking,
	// the same pattern as every bump before it.
	//
	// It reaches THREE places for the price of one, because every one of
	// them reuses this type: the today/lifetime buckets (StatsSave), every
	// finalized day (DayBucketSave.Counters), and both halves of an
	// in-progress session (ActiveSessionSave.Baseline/Watermark) — which
	// is exactly the "a counter added to it later (e.g. PR-5's
	// pausedSeconds...) joins the session with no edit here" property
	// ActiveSessionSave's own doc comment predicted.
	//
	// `omitempty` IS LOAD-BEARING, and not for file size. The JSON side of
	// the MAC verifies a save against canonicalBody(d) — a
	// re-serialization of the parsed struct (integrity.go) — so importJSON
	// only holds its documented invariant ("canonicalBody(d) ... reproduces
	// byte-identical preimage bytes") while every field added AFTER a file
	// was written stays absent from that re-encoding. Without omitempty
	// this field injected `"pausedSeconds":0` into four buckets of every
	// pre-PR-5 state.json, changing the preimage and failing the MAC: a
	// real v1.0.0 save was refused as tampered and the player silently
	// started a fresh economy. Every other field this wave added
	// (Session/SessionLogHead/Paused) already carries omitempty/omitzero
	// for exactly this reason; this one did not, and that was the bug.
	// Same rule for every future field. See
	// TestPrePR5StateJSONImportsUnderItsOriginalMac.
	PausedSeconds uint64 `json:"pausedSeconds,omitempty"`
}

// CoinBreakdownSave is the persisted analogue of game.CoinBreakdown — the
// per-signal split of today's earned DevCash (A2, docs/plan/A2-design.md
// §5/§7 Task GO-3), added in schema 3. Every field is a whole-number coin
// count — content-free by construction, same rule as StatCountersSave.
// Deliberately its own declaration rather than an import of
// game.CoinBreakdown, for the same decoupling reason StatCountersSave's
// doc comment gives.
type CoinBreakdownSave struct {
	Keystrokes    uint64 `json:"keystrokes"`
	Mouse         uint64 `json:"mouse"`
	FocusSessions uint64 `json:"focusSessions"`
	AppSwitches   uint64 `json:"appSwitches"`
}

// StatsSave is the persisted `stats` object added in schema 2 (see
// CurrentSchema's doc comment): Date is the local "YYYY-MM-DD" Today was
// captured for ("" if the game was never ticked before this save), Today
// is that date's bucket, Lifetime is the running total. A schema-1 file
// has no "stats" key at all; json.Unmarshal leaves this whole field at its
// zero value (Date "", both buckets all-zero counters), which Apply then
// hands to game.Game.RestoreStats — the exact same "no stats recorded yet"
// state a schema-2 file would represent explicitly, so no separate
// migration code is needed beyond the schema bump itself.
//
// CoinsToday (schema 3, A2 §5/§7 Task GO-3) is the persisted analogue of
// game.StatsView.CoinsToday — the per-signal coin split earned so far
// today. It lives on StatsSave (not inside StatCountersSave) for the same
// reason game.CoinBreakdown is a sibling of StatCounters on StatsView: it
// isn't a plain activity count, it's a coin count. A schema-2 file has no
// "coinsToday" key; it defaults to the zero value, same additive pattern.
//
// History/Streak (schema 4, Analytics Phase A3, docs/plan/A3-design.md
// §4/§7 Task GO-2) are the persisted analogues of game.Game's
// HistorySnapshot/StreakSnapshot: History is the sparse, oldest-first
// rolling window of finalized day buckets (at most game.HistoryRetentionDays
// long); Streak is the cross-window streak state (current/longest/
// lastActiveDate) that must survive independently of the retained window
// (see StreakSave's doc comment). A schema-3 file has neither key;
// json.Unmarshal leaves History nil (an empty slice, len 0) and Streak at
// its zero value (StreakSave{}, i.e. current 0, longest 0, lastActiveDate
// "") — the correct "nothing recorded over time yet" state. We
// deliberately do NOT backfill either from Today/Lifetime: we never had
// per-day data before this bump, so any backfill would invent days (and a
// fabricated streak) that never happened.
type StatsSave struct {
	Date       string            `json:"date"`
	Today      StatCountersSave  `json:"today"`
	Lifetime   StatCountersSave  `json:"lifetime"`
	CoinsToday CoinBreakdownSave `json:"coinsToday"`
	History    []DayBucketSave   `json:"history"`
	Streak     StreakSave        `json:"streak"`
}

// DayBucketSave is the persisted analogue of game.DayBucket (Analytics
// Phase A3, docs/plan/A3-design.md §4/§7 Task GO-2) — one finalized day's
// counters, reusing StatCountersSave verbatim for the seven A1/A2 counts
// (the existing content-free structural coverage on that type already
// applies here) plus two new scalar, content-free additions: CoinsEarned
// (that day's total DevCash split, mirroring CoinBreakdownSave's own
// per-signal counts) and LongestFocusBlockSeconds (Fork B: a per-day max
// sustained-typing run duration). Deliberately its own declaration rather
// than an import of game.DayBucket, for the same decoupling reason
// StatCountersSave's doc comment gives.
type DayBucketSave struct {
	Date                     string           `json:"date"`
	Counters                 StatCountersSave `json:"counters"`
	CoinsEarned              uint64           `json:"coinsEarned"`
	LongestFocusBlockSeconds uint64           `json:"longestFocusBlockSeconds"`
}

// StreakSave is the persisted analogue of game.StreakView (Analytics
// Phase A3, docs/plan/A3-design.md §2.2/§4/§7 Task GO-2) — the streak
// state that must survive PAST the retained history window (a streak can
// outlive game.HistoryRetentionDays, so it cannot be recomputed from the
// pruned buckets and must be its own persisted counter). Current is the
// length of the run ending at LastActiveDate; Longest is the running
// max, never decreasing; LastActiveDate is the local "YYYY-MM-DD" of the
// most recently active day ("" if none yet) — the same content-free date
// class StatsSave.Date already is.
type StreakSave struct {
	Current        int    `json:"current"`
	Longest        int    `json:"longest"`
	LastActiveDate string `json:"lastActiveDate"`
}

// ActiveSessionSave is the persisted in-progress session (P2, docs/plan/
// P2-design.md §5.1, ADR 0017 Decision 5) — one of the two additive
// fields SaveData gains in schema 6 (SaveData.Session below). Single,
// mutable, rewritten on every autosave — exactly the snapshot row's own
// shape — so it is MAC-protected for free by the same whole-struct-minus-
// the-tag preimage every other SaveData field already gets, needing no
// integrity code of its own.
//
// Baseline AND Watermark are both persisted, separately, rather than
// their delta: a reloaded session must be able to resume exactly,
// including the frozen-at-last-activity watermark an idle auto-end may
// need on the very next tick (game.Game.ActiveSessionSnapshot's doc
// comment). Reusing StatCountersSave for both is deliberate (§5.1): the
// existing structural content-free coverage on that type applies for
// free, and a counter added to it later (e.g. PR-5's pausedSeconds, which
// takes schema 6->7) joins the session with no edit here.
//
// Deliberately no Name field, mirroring game.SessionRecord's own doc
// comment: a project name is USER-AUTHORED CONFIG (§2.7), kept out of
// anything that reaches the protected save.
type ActiveSessionSave struct {
	ID                       int              `json:"id"`
	StartedAt                string           `json:"startedAt"`      // RFC3339
	LastActivityAt           string           `json:"lastActivityAt"` // RFC3339
	Baseline                 StatCountersSave `json:"baseline"`
	Watermark                StatCountersSave `json:"watermark"`
	CoinsEarned              uint64           `json:"coinsEarned"`
	LongestFocusBlockSeconds uint64           `json:"longestFocusBlockSeconds"`
}

// SessionSave is one FINISHED session's persisted shape (P2, docs/plan/
// P2-design.md §5.2, ADR 0017 Decision 5) — the canonical compact JSON
// that becomes one `sessions` row's `payload` BLOB (db.go's sessionsDDL),
// chain-MAC'd rather than embedded in the snapshot (§5.1: finished
// sessions are append-mostly and unbounded — "exactly a table's shape").
// A BLOB of the whole record, not a column per counter, for the same
// reason DB-1 rejected normalizing the economy snapshot: the chain MAC
// covers the whole struct, so every future session field is protected
// automatically, with no hand-built row serializer whose omission would
// be a silent anti-cheat hole rather than a test failure (§5.2).
//
// EndReason is one of "user"/"idle"/"maxDuration" (game.go's closed
// endReason* constants, mirrored here as a plain string — same class as
// engine.Mood's own closed-set string encoding). Deliberately no Name
// field, for the identical reason ActiveSessionSave has none (§2.7).
type SessionSave struct {
	ID                       int              `json:"id"`
	StartedAt                string           `json:"startedAt"` // RFC3339
	EndedAt                  string           `json:"endedAt"`   // RFC3339 — also the sessions row's ended_at mirror
	DurationSeconds          uint64           `json:"durationSeconds"`
	Counters                 StatCountersSave `json:"counters"`
	CoinsEarned              uint64           `json:"coinsEarned"`
	LongestFocusBlockSeconds uint64           `json:"longestFocusBlockSeconds"`
	EndReason                string           `json:"endReason"` // user|idle|maxDuration
}

// SaveData is the on-disk shape at ~/.config/dexel/state.json,
// transcribed field-for-field from docs/upgrade-design.md's "Persistence"
// section. This IS the save format — changing a field name silently
// orphans existing saves.
//
// Privacy invariant (ADR 0002/0009, carried from the Rust save): this
// file contains no user content. Sprint names come from the static list
// (never from input); only an index and a work-unit float are stored.
//
// Mac (SEC-1, docs/plan/SEC-1-design.md §2, ADR 0014) is the hex
// HMAC-SHA256 tag of this struct with Mac itself zeroed (see
// integrity.go's macPreimage/computeMAC) — a fixed-width digest, not
// content, so it does not weaken the privacy invariant above. Since B-1
// (docs/plan/REVIEW-2026-08-22.md) an EMPTY Mac is never trusted at any
// schema: the schema<=4 grandfather window — which let an unsigned,
// hand-written file be imported and then signed, minting an economy with
// no key at all — is closed, so a save that arrives without a valid tag
// is treated exactly like one with a forged tag (see loadJSON).
type SaveData struct {
	Schema     int                     `json:"schema"`
	DevCash    uint64                  `json:"devCash"`
	XP         uint64                  `json:"xp"`
	Sprint     SprintSave              `json:"sprint"`
	OwnedItems []string                `json:"ownedItems"`
	OwnedTints []string                `json:"ownedTints"`
	Equipped   map[string]EquippedSave `json:"equipped"`
	// ImportedFromRust/ImportedAt are VESTIGIAL as of B-2
	// (docs/plan/REVIEW-2026-08-22.md): the legacy-Rust import that was
	// the only thing that ever set them is deleted. They stay in the
	// struct — both are `omitempty` and both round-trip through
	// Snapshot/Apply unchanged — so a save written by an earlier build
	// keeps verifying against its own MAC and keeps reporting the truth
	// about where it came from. Nothing sets them any more; nothing new
	// should.
	ImportedFromRust bool      `json:"importedFromRust,omitempty"`
	ImportedAt       string    `json:"importedAt,omitempty"` // RFC3339, "" if never imported
	Stats            StatsSave `json:"stats"`
	// Session/SessionLogHead (P2, docs/plan/P2-design.md §5.1, ADR 0017
	// Decision 5, schema 6) are the two additive fields for the in-progress
	// session and the chained session-log's HEAD. Session is nil/omitted
	// when no session is active — a schema-5 payload has neither key, so
	// json.Unmarshal leaves Session nil and SessionLogHead "", which §5.4's
	// gate accepts as "no active session, the honest empty log" (see
	// CurrentSchema's doc comment below). SessionLogHead is an OPAQUE
	// chained-MAC token (integrity.go's computeLogMAC) that this package
	// alone computes and verifies; game.Game only ever carries it forward,
	// exactly like ImportedFromRust/ImportedAt.
	Session        *ActiveSessionSave `json:"session,omitzero"`
	SessionLogHead string             `json:"sessionLogHead,omitempty"`
	// Paused (PR-5, docs/production-runtime/ARCHITECTURE.md Decision
	// 16 and FORK D, schema 7) persists the user's pause INTENT across a
	// restart: "pause is a user intent, and `dexel update` restarts the
	// runtime; a pause that silently evaporated mid-update would be a lie
	// in the other direction". A paused dexel therefore comes back paused
	// — and says so loudly on startup (main.go logs one line) and in
	// `dexel status`, so a paused-and-forgotten dexel is never mute.
	//
	// `omitempty` because false is both the zero value and the correct
	// grandfathered default: a schema-6-or-earlier payload has no key at
	// all, json.Unmarshal leaves this false, and Apply hands that to
	// game.Game.RestorePaused — exactly the "not paused" state such a
	// save represents.
	Paused bool   `json:"paused,omitempty"`
	Mac    string `json:"mac,omitempty"`
}

// CurrentSchema is the schema version this build writes.
//
// Bumped 1 -> 2 for Analytics track Phase A1 (docs/plan/ROADMAP.md): the
// new `stats` field above. This is a genuine save-format change, not just
// an additive JSON key an old build could safely ignore — Load's existing
// "future schema" guard (see Load's doc comment and ErrFutureSchema) exists
// specifically so an OLDER build run once against a NEWER save can never
// silently re-save it minus the fields it doesn't know about; bumping the
// number is what makes that guard actually fire for this change instead of
// letting a schema-1-shaped build quietly drop everyone's stats the first
// time it writes. Migration schema 1 -> 2 needs no dedicated code: a
// schema-1 file simply has no "stats" key, json.Unmarshal leaves SaveData's
// Stats field at its zero value (StatsSave{} — empty date, all-zero
// counters), and Apply hands that straight to game.Game.RestoreStats,
// which is exactly the right "no stats recorded yet" starting state for a
// save that predates this feature.
//
// Bumped 2 -> 3 for Analytics track Phase A2 (docs/plan/A2-design.md §7
// Task GO-3): StatCountersSave gains FocusSessions/AppSwitches and
// StatsSave gains CoinsToday (CoinBreakdownSave). Same additive-migration
// reasoning as the 1->2 bump above applies verbatim: a schema-2 file has
// none of these keys, json.Unmarshal leaves them at their zero value (0
// counts, an all-zero CoinBreakdownSave), and Apply hands that straight to
// game.Game.RestoreCoinsToday/RestoreStats — exactly the "nothing observed
// yet" state a schema-3 file would represent explicitly. No dedicated
// migration code beyond this bump; existing devCash/xp/owned/equipped and
// the pre-existing stats counters are read exactly as before.
//
// Bumped 3 -> 4 for Analytics track Phase A3 (docs/plan/A3-design.md §4/§7
// Task GO-2): StatsSave gains History ([]DayBucketSave) and Streak
// (StreakSave). Same additive-migration reasoning as the 1->2 and 2->3
// bumps above applies verbatim: a schema-3 file has neither key,
// json.Unmarshal leaves History as an empty (nil) slice and Streak at its
// zero value (StreakSave{}), and Apply hands those straight to
// game.Game.RestoreHistory/RestoreStreak — exactly the "no history/streak
// recorded yet" state a schema-4 file would represent explicitly. We
// deliberately do NOT backfill history or a streak from the existing
// Today/Lifetime counters on migration: this build never had per-day
// data before now, so inventing past days (or a streak built from them)
// would be dishonest fabrication, not migration. No dedicated migration
// code beyond this bump; existing devCash/xp/owned/equipped and every
// pre-existing stats field (today/lifetime/coinsToday) are read exactly
// as before. ErrFutureSchema is unchanged by this bump: a schema-5 save
// is still renamed to ".future" and refused, never silently downgraded.
//
// Bumped 4 -> 5 for SEC-1 (docs/plan/SEC-1-design.md,
// docs/adr/0014-save-integrity-hmac-and-config-split.md): SaveData gains
// Mac, the hex HMAC-SHA256 tag over the rest of the struct (see
// integrity.go). Unlike every prior bump, this one is not purely
// additive in the usual "old build ignores an unknown key" sense —
// schema >= 5 is the signal that a save carries a MAC at all, so the bump
// is load-bearing for the anti-cheat mechanism itself, not just a version
// label. A pre-SEC-1 schema-4-or-earlier file has no "mac" key. Load used
// to grandfather such a file in as trusted and re-sign it; since B-1
// (docs/plan/REVIEW-2026-08-22.md) it is REFUSED as tampered, because
// that window was an unsigned mint anyone could walk through and nothing
// legitimate has produced a schema<=4 file since. ErrFutureSchema is
// unchanged by this bump: a schema-6 save is still renamed to ".future"
// and refused, never silently downgraded (see
// TestFutureSchema7RefusalStillFiresAfterTheSchema6Bump, which now pins
// the concrete future number one bump later — see that test's own doc
// comment for the renaming history).
//
// Bumped 5 -> 6 for P2 (docs/plan/P2-design.md §5.6, ADR 0017 Decision 6):
// SaveData gains Session (*ActiveSessionSave, the in-progress session) and
// SessionLogHead (the chained-MAC head of the append-only `sessions`
// table, db.go). Additive in exactly the way every prior bump was: a
// schema-5 payload has neither key, json.Unmarshal leaves Session nil and
// SessionLogHead "", and no `sessions` table exists yet — which
// verifySessionLog's gate (db.go, §5.4) accepts as "no active session,
// the honest empty log", the correct starting state for a save that
// predates P2. No dedicated migration code beyond this bump; nothing is
// backfilled, because inventing past sessions would be fabrication, not
// migration (§5.6: "we never had sessions before, so inventing past ones
// would be fabrication").
//
// Bumped 6 -> 7 for PR-5 (pause; docs/production-runtime/
// MIGRATION_PLAN.md §PR-5, ARCHITECTURE.md §6 Decisions 14/16 and FORK D
// — P2/Sessions had already claimed 5 -> 6, so pause takes the next
// number, per MIGRATION_PLAN.md sequencing constraint 3's "only ONE
// schema-bumping task in flight at a time"): SaveData gains Paused (the
// persisted pause intent) and StatCountersSave gains PausedSeconds (the
// third, disjoint time bucket). Additive in exactly the way every prior
// bump was: a schema-6 payload has neither key, json.Unmarshal leaves
// Paused false and every PausedSeconds 0, and Apply hands those to
// game.Game.RestorePaused/RestoreStats — respectively "not paused" and
// "this build never recorded a paused second", which is precisely what a
// pre-PR-5 save means. Nothing is backfilled: pause did not exist before
// this bump, so inventing paused time (or, worse, reinterpreting some
// recorded IdleSeconds as paused) would be fabrication, not migration. No
// dedicated migration code beyond the bump. ErrFutureSchema is unchanged:
// a schema-8 save is still renamed ".future" and refused, never silently
// downgraded (see TestFutureSchema8RefusalStillFiresAfterTheSchema7Bump,
// which pins the concrete future number one bump later again).
const CurrentSchema = 7

// DefaultPath returns <StateDir>/state.db — on Linux with
// $XDG_CONFIG_HOME unset this is byte-identical to the
// ~/.config/dexel/state.db this function hardcoded before
// app/internal/paths existed (DB-1, docs/plan/DB-1-design.md §5 — the one
// behavioural change to this package's contract: everything else
// Load/Save/Snapshot/Apply exposed before DB-1 is unchanged), which is
// PR-1's whole point: zero migration for the only platform with real
// saves today (docs/production-runtime/MIGRATION_PLAN.md §PR-1,
// PLATFORM_NOTES.md §1). paths.StateDir() is the only place that knows
// the actual per-OS location, including DEXEL_HOME's override and the
// one-time macOS/Windows relocation. Prior builds wrote
// ~/.config/dexel/state.json at this same basename-minus-extension; see
// jsonImportPath and Load's doc comment for the one-time migration from
// that file into this one.
func DefaultPath() (string, error) {
	dir, err := paths.StateDir()
	if err != nil {
		return "", fmt.Errorf("resolve state dir: %w", err)
	}
	return filepath.Join(dir, "state.db"), nil
}

// quantizeUnits rounds v to 6 decimal places (SEC-1 design §2.3):
// sprint.unitsDone is the only float64 on SaveData, and quantizing it at
// snapshot time means the value written to disk and the value fed into
// the MAC preimage are the identical float64 bits, so two logically-equal
// saves (whose progress differs only by sub-µunit floating-point
// accumulation) produce byte-identical preimages and the same MAC. 6
// decimals is far finer than any work-unit granularity the economy uses,
// so no visible progress is lost. Save re-applies this defensively to
// whatever SaveData it is given, so a caller that builds a SaveData by
// hand (bypassing Snapshot) still gets a MAC that matches what ends up on
// disk.
func quantizeUnits(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

// Snapshot extracts a SaveData from the live game state. Arrays are
// sorted so the file is byte-stable across saves and diffable (map keys
// are already sorted by encoding/json).
func Snapshot(g *game.Game) SaveData {
	equipped := make(map[string]EquippedSave, len(g.Equipped))
	for slot, ref := range g.Equipped {
		var tint *string
		if ref.TintID != nil {
			v := *ref.TintID
			tint = &v
		}
		equipped[slot] = EquippedSave{ItemID: ref.ItemID, TintID: tint}
	}
	owned := make([]string, 0, len(g.OwnedItems))
	for id, ok := range g.OwnedItems {
		if ok {
			owned = append(owned, id)
		}
	}
	sort.Strings(owned)
	tints := make([]string, 0, len(g.OwnedTints))
	for key, ok := range g.OwnedTints {
		if ok {
			tints = append(tints, key)
		}
	}
	sort.Strings(tints)

	statsDate, statsToday, statsLifetime := g.StatsSnapshot()
	coinsToday := g.CoinsTodaySnapshot()

	historySnap := g.HistorySnapshot()
	history := make([]DayBucketSave, len(historySnap))
	for i, b := range historySnap {
		history[i] = dayBucketToSave(b)
	}
	streakCurrent, streakLongest, streakLastActiveDate := g.StreakSnapshot()

	// Session (P2, docs/plan/P2-design.md §5.1, ADR 0017 Decision 5): the
	// in-progress session, if any — nil/omitted when none is active
	// (ok == false), which is what makes a schema-5-shaped "no session"
	// state indistinguishable from a schema-6 save between sessions.
	var activeSession *ActiveSessionSave
	if ok, id, startedAt, lastActivityAt, baseline, watermark, coinsEarned, longestFocusBlockSeconds := g.ActiveSessionSnapshot(); ok {
		activeSession = &ActiveSessionSave{
			ID:                       id,
			StartedAt:                startedAt.UTC().Format(time.RFC3339),
			LastActivityAt:           lastActivityAt.UTC().Format(time.RFC3339),
			Baseline:                 statCountersToSave(baseline),
			Watermark:                statCountersToSave(watermark),
			CoinsEarned:              coinsEarned,
			LongestFocusBlockSeconds: longestFocusBlockSeconds,
		}
	}

	return SaveData{
		Schema:           CurrentSchema,
		DevCash:          g.DevCash,
		XP:               g.XP,
		Sprint:           SprintSave{Index: g.SprintIndex(), UnitsDone: quantizeUnits(g.Progress)},
		OwnedItems:       owned,
		OwnedTints:       tints,
		Equipped:         equipped,
		ImportedFromRust: g.ImportedFromRust,
		ImportedAt:       g.ImportedAt,
		Stats: StatsSave{
			Date:       statsDate,
			Today:      statCountersToSave(statsToday),
			Lifetime:   statCountersToSave(statsLifetime),
			CoinsToday: coinBreakdownToSave(coinsToday),
			History:    history,
			Streak: StreakSave{
				Current:        streakCurrent,
				Longest:        streakLongest,
				LastActiveDate: streakLastActiveDate,
			},
		},
		Session:        activeSession,
		SessionLogHead: g.SessionLogHead(),
		// Paused (PR-5, schema 7): the pause intent, persisted so a paused
		// dexel restarts paused (FORK D). main.go writes an IMMEDIATE save
		// on both pause and resume rather than waiting for the 30s
		// autosave, so the boolean on disk can never disagree with what
		// the user just asked for by more than a crash window.
		Paused: g.Paused(),
	}
}

// dayBucketToSave/dayBucketFromSave convert between game.DayBucket and its
// persisted analogue DayBucketSave (Analytics Phase A3, A3-design.md §4),
// reusing statCountersToSave/statCountersFromSave for the embedded counter
// bucket.
func dayBucketToSave(b game.DayBucket) DayBucketSave {
	return DayBucketSave{
		Date:                     b.Date,
		Counters:                 statCountersToSave(b.Counters),
		CoinsEarned:              b.CoinsEarned,
		LongestFocusBlockSeconds: b.LongestFocusBlockSeconds,
	}
}

func dayBucketFromSave(b DayBucketSave) game.DayBucket {
	return game.DayBucket{
		Date:                     b.Date,
		Counters:                 statCountersFromSave(b.Counters),
		CoinsEarned:              b.CoinsEarned,
		LongestFocusBlockSeconds: b.LongestFocusBlockSeconds,
	}
}

func statCountersToSave(c game.StatCounters) StatCountersSave {
	return StatCountersSave{
		Keystrokes:         c.Keystrokes,
		MouseActiveSeconds: c.MouseActiveSeconds,
		ActiveSeconds:      c.ActiveSeconds,
		IdleSeconds:        c.IdleSeconds,
		SprintsCompleted:   c.SprintsCompleted,
		FocusSessions:      c.FocusSessions,
		AppSwitches:        c.AppSwitches,
		PausedSeconds:      c.PausedSeconds,
	}
}

func statCountersFromSave(c StatCountersSave) game.StatCounters {
	return game.StatCounters{
		Keystrokes:         c.Keystrokes,
		MouseActiveSeconds: c.MouseActiveSeconds,
		ActiveSeconds:      c.ActiveSeconds,
		IdleSeconds:        c.IdleSeconds,
		SprintsCompleted:   c.SprintsCompleted,
		FocusSessions:      c.FocusSessions,
		AppSwitches:        c.AppSwitches,
		PausedSeconds:      c.PausedSeconds,
	}
}

func coinBreakdownToSave(c game.CoinBreakdown) CoinBreakdownSave {
	return CoinBreakdownSave{
		Keystrokes:    c.Keystrokes,
		Mouse:         c.Mouse,
		FocusSessions: c.FocusSessions,
		AppSwitches:   c.AppSwitches,
	}
}

func coinBreakdownFromSave(c CoinBreakdownSave) game.CoinBreakdown {
	return game.CoinBreakdown{
		Keystrokes:    c.Keystrokes,
		Mouse:         c.Mouse,
		FocusSessions: c.FocusSessions,
		AppSwitches:   c.AppSwitches,
	}
}

// Apply restores a SaveData onto a freshly-constructed game.New(). Every
// value is validated against the live catalog per docs/upgrade-design.md's
// "Load validation, never trust the file" rules: an unknown itemId/tintId
// is dropped, sprint.index is clamped, an equipped entry naming an
// unowned/unknown/wrong-slot item falls back to that slot's tier-0 item, a
// missing slot is filled with its tier-0 item, and unitsDone is clamped to
// [0, target]. None of this can panic or reject the whole file — a
// corrupted save degrades field-by-field, never all-or-nothing.
func Apply(g *game.Game, d SaveData) {
	g.DevCash = d.DevCash
	g.XP = d.XP
	g.RestoreSprint(d.Sprint.Index, d.Sprint.UnitsDone)
	g.ImportedFromRust = d.ImportedFromRust
	g.ImportedAt = d.ImportedAt

	// RestoreActiveSession/SetSessionLogHead (P2, docs/plan/P2-design.md
	// §5.1/§5.5, ADR 0017 Decision 5): the in-progress session and the
	// session log's opaque chain head, both carried on SaveData itself.
	// Neither interacts with rolloverStatsIfNewDay, so — unlike History/
	// Streak/CoinsToday just below — there is no ordering constraint
	// forcing this before or after RestoreStats; it sits here beside the
	// other "carried forward, this package's own concern" fields
	// (ImportedFromRust/ImportedAt immediately above).
	//
	// Note this does NOT restore the FINISHED session log or session
	// names: those are not part of SaveData at all (the former lives in
	// the `sessions` table, returned by LoadAll; the latter lives in
	// config.json, ConfigData.SessionNames) — main.go (GO-3) must call
	// Game.RestoreSessionLog(SessionRecordsFromSave(...)) and
	// Game.RestoreSessionNames(SessionNamesFromConfig(...)) itself, and
	// per §8's ordering rule, BEFORE calling this function (Apply
	// triggers RestoreStats, and RestoreSessionLog/RestoreSessionNames
	// must run before RestoreStats — the same A3 ordering rule
	// RestoreHistory/RestoreStreak already follow below).
	id, startedAt, lastActivityAt, baseline, watermark, coinsEarned, longestFocusBlockSeconds, ok := activeSessionSaveToRestoreArgs(d.Session)
	if !ok {
		id = 0
	}
	g.RestoreActiveSession(id, startedAt, lastActivityAt, baseline, watermark, coinsEarned, longestFocusBlockSeconds)
	g.SetSessionLogHead(d.SessionLogHead)

	// Paused (PR-5, schema 7, ARCHITECTURE.md FORK D): restore the pause
	// intent. Like the two calls above this has no ordering constraint
	// against RestoreStats — it is a flag the server READS after Apply
	// returns (to decide whether to start the activity provider at all),
	// not an input to the day-rollover logic.
	g.RestorePaused(d.Paused)

	// RestoreHistory/RestoreStreak MUST run before RestoreStats (A3,
	// docs/plan/A3-design.md §4/§7 Task GO-2, extending A2's
	// RestoreCoinsToday-before-RestoreStats rule below): RestoreStats
	// triggers rolloverStatsIfNewDay, whose finalizeDay call — fired when
	// d.Stats.Date is stale — appends to and updates whatever history and
	// streak are ALREADY in place at that moment. Restoring them first is
	// what makes a multi-day-gap reload finalize the save's last running
	// day into the RESTORED history/streak (exactly once — see
	// game.Game.RestoreHistory's doc comment) rather than into empty ones,
	// and what makes a same-day reload's early return leave the restored
	// history/streak untouched.
	history := make([]game.DayBucket, len(d.Stats.History))
	for i, b := range d.Stats.History {
		history[i] = dayBucketFromSave(b)
	}
	g.RestoreHistory(history)
	g.RestoreStreak(d.Stats.Streak.Current, d.Stats.Streak.Longest, d.Stats.Streak.LastActiveDate)
	// RestoreCoinsToday MUST run before RestoreStats: RestoreStats' own
	// rollover check zeroes coinsToday whenever d.Stats.Date turns out
	// stale, and that check must run strictly AFTER this call for a stale
	// save's CoinBreakdown to end up correctly zeroed too (see
	// game.Game.RestoreCoinsToday's doc comment).
	g.RestoreCoinsToday(coinBreakdownFromSave(d.Stats.CoinsToday))
	g.RestoreStats(d.Stats.Date, statCountersFromSave(d.Stats.Today), statCountersFromSave(d.Stats.Lifetime))

	g.OwnedItems = map[string]bool{}
	for _, id := range d.OwnedItems {
		if _, ok := g.ItemByID(id); ok {
			g.OwnedItems[id] = true
		}
	}
	// Every slot's free tier-0 item is always owned, regardless of what
	// the file said — a save can never un-grant a guaranteed default.
	g.GrantTierZeroDefaults()

	g.OwnedTints = map[string]bool{}
	for _, key := range d.OwnedTints {
		itemID, tintID, ok := splitTintKey(key)
		if !ok {
			continue
		}
		if !g.OwnedItems[itemID] {
			continue
		}
		if _, ok := g.ItemByID(itemID); !ok {
			continue
		}
		if _, ok := g.TintByID(tintID); !ok {
			continue
		}
		g.OwnedTints[key] = true
	}

	for _, slot := range g.Slots() {
		entry, hasEntry := d.Equipped[slot.ID]
		itemID := entry.ItemID
		if !hasEntry || itemID == "" {
			itemID = g.TierZeroItem(slot.ID)
		}
		item, ok := g.ItemByID(itemID)
		if !ok || item.Slot != slot.ID || !g.OwnedItems[itemID] {
			itemID = g.TierZeroItem(slot.ID)
			item, _ = g.ItemByID(itemID)
		}

		var tintPtr *string
		if slot.Tintable {
			want := ""
			if entry.TintID != nil {
				want = *entry.TintID
			}
			if want == "" || !g.IsTintOwned(itemID, want) {
				if item.DefaultTint != nil {
					want = *item.DefaultTint
				} else {
					want = ""
				}
			}
			if want != "" {
				tintPtr = &want
			}
		}
		g.SetEquipped(slot.ID, itemID, tintPtr)
	}
}

// activeSessionSaveToRestoreArgs converts an ActiveSessionSave into
// RestoreActiveSession's argument shape. ok is false (and every other
// return zeroed) for a nil s, or for a save whose RFC3339 timestamps fail
// to parse. The latter should never actually happen for a snapshot that
// already verified its MAC — a byte edit to either timestamp string would
// have broken that check first — so this is treated as this package's
// own bug degrading gracefully to "no active session," matching Apply's
// own doc comment: "a corrupted save degrades field-by-field, never
// all-or-nothing."
func activeSessionSaveToRestoreArgs(s *ActiveSessionSave) (id int, startedAt, lastActivityAt time.Time, baseline, watermark game.StatCounters, coinsEarned, longestFocusBlockSeconds uint64, ok bool) {
	if s == nil {
		return 0, time.Time{}, time.Time{}, game.StatCounters{}, game.StatCounters{}, 0, 0, false
	}
	parsedStartedAt, err1 := time.Parse(time.RFC3339, s.StartedAt)
	parsedLastActivityAt, err2 := time.Parse(time.RFC3339, s.LastActivityAt)
	if err1 != nil || err2 != nil {
		return 0, time.Time{}, time.Time{}, game.StatCounters{}, game.StatCounters{}, 0, 0, false
	}
	return s.ID, parsedStartedAt.UTC(), parsedLastActivityAt.UTC(), statCountersFromSave(s.Baseline), statCountersFromSave(s.Watermark), s.CoinsEarned, s.LongestFocusBlockSeconds, true
}

// sessionRecordToSave converts a finished game.SessionRecord into its
// persisted SessionSave shape (§5.2) — SessionSaveFromRecord's body.
func sessionRecordToSave(rec game.SessionRecord) SessionSave {
	return SessionSave{
		ID:                       rec.ID,
		StartedAt:                rec.StartedAt.UTC().Format(time.RFC3339),
		EndedAt:                  rec.EndedAt.UTC().Format(time.RFC3339),
		DurationSeconds:          rec.DurationSeconds,
		Counters:                 statCountersToSave(rec.Counters),
		CoinsEarned:              rec.CoinsEarned,
		LongestFocusBlockSeconds: rec.LongestFocusBlockSeconds,
		EndReason:                rec.EndReason,
	}
}

// sessionRecordFromSave converts one verified SessionSave (LoadAll's
// output) back into a game.SessionRecord — SessionRecordsFromSave's body.
// An error here means a chain-MAC-verified payload contained an RFC3339
// timestamp that failed to parse: this package's own bug, never
// tampering (a byte edit to either string would already have broken the
// chain MAC that verified this payload before this function ever runs).
func sessionRecordFromSave(s SessionSave) (game.SessionRecord, error) {
	startedAt, err := time.Parse(time.RFC3339, s.StartedAt)
	if err != nil {
		return game.SessionRecord{}, fmt.Errorf("parse startedAt %q: %w", s.StartedAt, err)
	}
	endedAt, err := time.Parse(time.RFC3339, s.EndedAt)
	if err != nil {
		return game.SessionRecord{}, fmt.Errorf("parse endedAt %q: %w", s.EndedAt, err)
	}
	return game.SessionRecord{
		ID:                       s.ID,
		StartedAt:                startedAt.UTC(),
		EndedAt:                  endedAt.UTC(),
		DurationSeconds:          s.DurationSeconds,
		Counters:                 statCountersFromSave(s.Counters),
		CoinsEarned:              s.CoinsEarned,
		LongestFocusBlockSeconds: s.LongestFocusBlockSeconds,
		EndReason:                s.EndReason,
	}, nil
}

// SessionSaveFromRecord converts one finished game.SessionRecord into the
// SessionSave shape AppendSession persists (db.go, §5.5) — the exported
// counterpart of Snapshot for the single finished session GO-3 appends on
// SESSION_STOP or an automatic end (Game.TakeEndedSession).
func SessionSaveFromRecord(rec game.SessionRecord) SessionSave {
	return sessionRecordToSave(rec)
}

// SessionRecordsFromSave converts LoadAll's verified []SessionSave
// (already oldest-first, ascending id) into []game.SessionRecord for
// main.go to hand to Game.RestoreSessionLog — see Apply's doc comment
// for why this must run BEFORE store.Apply (which triggers RestoreStats).
// An error here means one of the verified records carried an unparseable
// RFC3339 timestamp (see sessionRecordFromSave's doc comment) — this
// package's own bug; it should never actually fire against a chain that
// verified.
func SessionRecordsFromSave(saves []SessionSave) ([]game.SessionRecord, error) {
	out := make([]game.SessionRecord, len(saves))
	for i, s := range saves {
		rec, err := sessionRecordFromSave(s)
		if err != nil {
			return nil, fmt.Errorf("session record %d: %w", s.ID, err)
		}
		out[i] = rec
	}
	return out, nil
}

// SessionNamesFromConfig converts ConfigData.SessionNames (config.go) —
// string-keyed because JSON object keys must be strings (§2.7) — into the
// int-keyed map Game.RestoreSessionNames expects. A key that fails to
// parse as a decimal int (a hand-edited config.json) is skipped rather
// than blocking startup — the same "malformed config.json degrades, never
// blocks" contract LoadConfig itself already keeps (config.go).
// RestoreSessionNames re-normalizes every value it is given, so this
// function passes raw strings through untouched.
func SessionNamesFromConfig(cfg ConfigData) map[int]string {
	out := make(map[int]string, len(cfg.SessionNames))
	for key, name := range cfg.SessionNames {
		id, err := strconv.Atoi(key)
		if err != nil {
			continue
		}
		out[id] = name
	}
	return out
}

// SessionNamesToConfig converts Game.SessionNames()'s int-keyed map into
// the string-keyed shape ConfigData.SessionNames requires (§2.7) for
// main.go to hand to SaveConfig after SESSION_START — the exact inverse
// of SessionNamesFromConfig.
func SessionNamesToConfig(names map[int]string) map[string]string {
	out := make(map[string]string, len(names))
	for id, name := range names {
		out[strconv.Itoa(id)] = name
	}
	return out
}

func splitTintKey(key string) (itemID, tintID string, ok bool) {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == ':' {
			return key[:i], key[i+1:], true
		}
	}
	return "", "", false
}

// Save writes d to path (state.db) via one SQLite transaction — an
// upsert of the single signed row plus PRAGMA user_version, both
// committed together (db.go's writeStateRow; DB-1 design §2.3/§4.5).
// This replaces the old write-temp-file + fsync + rename + dir-fsync
// recipe for THIS file only: config.json still uses that recipe verbatim
// (writeFileAtomically below, used by SaveConfig in config.go) because
// DB-1 does not touch config.json at all. A SQLite transaction gives the
// same "never a half-written, unparseable file" guarantee state.json's
// atomic-rename dance gave, obtained from the engine rather than from
// ~50 lines we maintain by hand.
//
// SEC-1's integrity scheme carries over unchanged (docs/plan/SEC-1-design.md
// §2, ADR 0014, DB-1 design §3.1): before marshaling, Save quantizes
// d.Sprint.UnitsDone (quantizeUnits — a no-op if Snapshot already
// quantized it, but this makes Save itself correct for any caller, not
// just ones that went through Snapshot) and computes the MAC over
// canonicalBody(d) (integrity.go), writing both the payload bytes and the
// resulting tag into the row — self-consistently signed regardless of
// what schema value d carries. Verification of that tag is Load's job.
func Save(path string, d SaveData) error {
	d.Sprint.UnitsDone = quantizeUnits(d.Sprint.UnitsDone)
	d.Mac = ""
	payload := canonicalBody(d)
	mac := computeMACBytes(payload)

	db, err := openDB(path)
	if err != nil {
		return fmt.Errorf("open state.db: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := ensureStateTable(db); err != nil {
		return fmt.Errorf("create state table: %w", err)
	}
	if err := writeStateRow(db, d.Schema, payload, mac); err != nil {
		return fmt.Errorf("write state.db: %w", err)
	}
	return nil
}

// writeFileAtomically writes data to path via the tmp-write + fsync +
// rename + dir-fsync recipe (docs/upgrade-design.md): a crash mid-write
// can never leave a half-written, unparseable file behind. Shared by Save
// (state.json) and SaveConfig (config.json, config.go) — SEC-1 design
// §1.2 calls the second file's write "a second, identical, already-
// written atomic write."
func writeFileAtomically(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create save dir: %w", err)
	}

	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create temp save file: %w", err)
	}
	defer func() { _ = os.Remove(tmpPath) }() // no-op once the rename below succeeds

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temp save file: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync temp save file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp save file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp save file into place: %w", err)
	}

	// Belt-and-suspenders durability: a rename's entry into its directory
	// is itself an unordered write that can still be sitting in volatile
	// cache when the file's own data is already durable (f.Sync() above
	// only forced the CONTENT out) — a power cut right after the rename
	// can leave the directory pointing at nothing, or the old file, even
	// though the new content was safely on disk. fsync-ing the directory
	// forces that directory-entry update out too, exactly like the
	// content sync above. Surfaced as an error like every other step in
	// this function, even though the rename itself already succeeded and
	// the caller (main.go's autosave/shutdown persist()) only logs it —
	// callers that care about durability guarantees should see this the
	// same way they'd see any other save failure, not have it silently
	// swallowed inside here.
	if dirErr := syncDir(filepath.Dir(path)); dirErr != nil {
		return fmt.Errorf("save renamed into place but syncing its directory failed (rename may not survive a power cut): %w", dirErr)
	}
	return nil
}

// syncDir opens dir and calls Sync on it, forcing any pending directory-
// entry metadata (e.g. Save's os.Rename above) out to durable storage.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// LoadAll implements DB-1's full decision tree (docs/plan/DB-1-design.md
// §4.2), extended by P2 with the session log (docs/plan/P2-design.md
// §5.4): state.db is the source of truth once it exists (loadDB, db.go,
// §3.2's gate order extended by §5.4's steps 7-9); a state.db-less
// machine that still has a state.json runs the one-time import
// (importJSON, db.go, §4.3), reusing loadJSON below verbatim for
// verification; neither existing is the ONLY "no save" case —
// (SaveData{}, nil, false, nil) — that may reach the caller's legacy
// Rust import (main.go's loadOrImport). A stat failure other than "does
// not exist" (e.g. a permission error) is surfaced as an error, exactly
// as it always was.
//
// A one-time JSON import always returns a nil session log: the source
// state.json predates P2 by construction (state.db did not exist yet,
// and once it does the DB branch is taken forever after — §4.3's "import
// is one-time structurally"), so it can only ever be schema <= 5 and
// carry no session data at all — the honest empty log, exactly what a
// nil slice represents.
//
// Every quarantine/tamper/future-schema property loadJSON's own doc
// comment describes below still holds — LoadAll just decides FIRST which
// of the two files (or neither) it's evaluating.
func LoadAll(path string) (SaveData, []SessionSave, bool, error) {
	exists, err := fileExists(path)
	if err != nil {
		return SaveData{}, nil, false, fmt.Errorf("stat %s: %w", path, err)
	}
	if exists {
		return loadDB(path)
	}

	jsonPath := jsonImportPath(path)
	jsonExists, err := fileExists(jsonPath)
	if err != nil {
		return SaveData{}, nil, false, fmt.Errorf("stat %s: %w", jsonPath, err)
	}
	if !jsonExists {
		return SaveData{}, nil, false, nil
	}
	d, ok, err := importJSON(path, jsonPath)
	return d, nil, ok, err
}

// Load is a THIN WRAPPER over LoadAll that verifies the session log chain
// identically and then discards it (docs/plan/P2-design.md §5.4:
// "verification cannot be skipped by construction... a two-function API
// where the convenient one skips integrity is exactly the silent hole ADR
// 0016 warns about"). Every existing call site (main.go's loadOrImport)
// and every pre-P2 test keeps working unchanged, and still exercises the
// chain check on every call — there is no way to reach a SaveData without
// LoadAll's full gate having run, including the session log.
func Load(path string) (SaveData, bool, error) {
	d, _, ok, err := LoadAll(path)
	return d, ok, err
}

// loadJSON reads and parses a JSON save file directly — this is
// pre-DB-1's entire Load function, renamed verbatim (DB-1 design
// §4.3/§5/§7: "Reuse today's Load body verbatim... this is the single
// highest-leverage decision in the migration"). DB-1's Load (above) only
// ever calls this for the one-time state.json -> state.db import
// (importJSON, db.go); state.db itself is never touched by this
// function.
//
// A missing file returns (SaveData{}, false, nil) — "no save yet" is not
// an error. A malformed file is renamed to "state.json.corrupt" (never
// deleted, so a user can send it in) and reported as "no save" rather
// than an error the caller must handle — docs/upgrade-design.md: "log
// once, start fresh... never delete the bad file."
//
// A save whose Schema is NEWER than CurrentSchema (i.e. written by a
// build ahead of this one — the classic "ran an older build once after
// upgrading" scenario) is handled differently from a malformed file:
// this build does not understand every field that save might carry, so
// silently returning (SaveData{}, false, nil) — "no save, start fresh" —
// would be actively dangerous. The caller, seeing "no save," would go on
// to either start completely fresh OR run the legacy-import path, and
// either way its very next write would overwrite the newer data — a
// silent downgrade that destroys progress the moment someone runs an
// older build once, with no error and no way back. Instead: the
// original file is renamed to "<path>.future" completely untouched, and
// this function returns an error (never (_, true, _) for a future
// schema) so the caller treats this as a load FAILURE, not "no save" —
// the same way an unreadable file is surfaced — while the future-schema
// data sits recoverable at its own path rather than being silently
// clobbered by whatever this older build does next.
//
// SEC-1 (docs/plan/SEC-1-design.md §4, ADR 0014): a save whose Schema is
// >= 5 (i.e. this build's or a prior SEC-1-era build's) that parses fine
// but whose Mac does not verify (integrity.go's verifyMAC) is handled the
// same way as a future schema, and deliberately NOT the same way as a
// missing file: the original is renamed to "<path>.invalid" (never
// deleted) and this function returns an error wrapping ErrTampered,
// never (_, true, _) and never (SaveData{}, false, nil). That last
// distinction is the entire point — collapsing a tamper failure into "no
// save" makes a failed load indistinguishable from a fresh install, which
// pre-B-2 additionally unlocked the unbounded legacy-Rust re-grant and
// still decides onboarding today. Returning a distinct sentinel closes
// that the same way ErrFutureSchema already does: the caller must treat
// "load failed" and "no save yet" as different things.
//
// B-1 (docs/plan/REVIEW-2026-08-22.md): the MAC is required at EVERY
// schema. A schema-4-or-earlier file has no Mac to check, which is now a
// refusal rather than a free pass — see the check itself below for why
// the old grandfather window was strictly easier to abuse than forging a
// tag.
func loadJSON(path string) (SaveData, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SaveData{}, false, nil
		}
		return SaveData{}, false, fmt.Errorf("read save file: %w", err)
	}
	var d SaveData
	if err := json.Unmarshal(data, &d); err != nil {
		corruptPath := quarantinePath(path, ".corrupt")
		_ = os.Rename(path, corruptPath)
		return SaveData{}, false, fmt.Errorf("parse save file (moved to %s): %w", corruptPath, err)
	}
	if d.Schema > CurrentSchema {
		futurePath := quarantinePath(path, ".future")
		if renameErr := os.Rename(path, futurePath); renameErr != nil {
			return SaveData{}, false, fmt.Errorf("%w: schema %d > this build's %d, and backing up %s to %s failed: %v",
				ErrFutureSchema, d.Schema, CurrentSchema, path, futurePath, renameErr)
		}
		return SaveData{}, false, fmt.Errorf("%w: schema %d > this build's %d; original preserved untouched at %s (NOT loaded, NOT deleted, NOT overwritten)",
			ErrFutureSchema, d.Schema, CurrentSchema, futurePath)
	}
	// B-1 (docs/plan/REVIEW-2026-08-22.md): the MAC is required at EVERY
	// schema, not only >= 5. The old `d.Schema >= 5 &&` guard was a
	// no-key mint: a hand-written {"schema":4,"devCash":999999999}
	// state.json was accepted unverified and then SIGNED by importJSON,
	// which is strictly easier than defeating the MAC. Nothing legitimate
	// produces a schema<=4 file any more (the only ones that ever existed
	// were pre-SEC-1 dev artifacts on the machine this was built on; the
	// product never shipped one), so an unsigned save is now treated
	// exactly like a forged one: quarantined to .invalid, ErrTampered,
	// fresh economy. config.json is a separate, unsigned, deliberately
	// hand-editable file and is untouched by this path, so the dexel's
	// name survives.
	if !verifyMAC(d) {
		// "unsigned" and "wrong tag" are the same refusal but a support
		// reader deserves to know which one happened.
		reason := "MAC mismatch"
		if d.Mac == "" {
			reason = fmt.Sprintf("no MAC at all (schema %d save file, unsigned)", d.Schema)
		}
		invalidPath := quarantinePath(path, ".invalid")
		if renameErr := os.Rename(path, invalidPath); renameErr != nil {
			return SaveData{}, false, fmt.Errorf("%w: %s, and backing up %s to %s failed: %v",
				ErrTampered, reason, path, invalidPath, renameErr)
		}
		return SaveData{}, false, fmt.Errorf("%w: %s; original preserved untouched at %s (NOT loaded, NOT deleted, NOT overwritten)",
			ErrTampered, reason, invalidPath)
	}
	return d, true, nil
}
