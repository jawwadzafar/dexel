// Package store persists game.Game to a local SQLite database
// (docs/plan/DB-1-design.md, docs/adr/0016-sqlite-persistence.md). It
// knows about
// game.Game's public API but nothing about the engine or activity
// packages — persistence is a leaf, not a hub. config.json (config.go)
// stays plain, unsigned, hand-editable JSON — DB-1 only moves the
// protected economy snapshot at state.db.
//
// There is exactly ONE supported save format (CurrentSchema). This is the
// first public release (v0.1.0): there are no prior public saves to
// migrate, so there is no import path and no schema-upgrade code. A load
// either finds a current-schema, correctly-signed save and uses it, or it
// quarantines whatever it found (wrong/older/future schema, bad MAC,
// corrupt) and the caller starts a fresh economy. Current-schema-or-fresh,
// nothing else.
package store

import (
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

// EquippedSave is one slot's persisted equip choice. STORE-2.0: item-only
// (colours are items now, so there is no tint to persist).
type EquippedSave struct {
	ItemID string `json:"itemId"`
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
	FocusSessions      uint64 `json:"focusSessions"`
	// PausedSeconds (PR-5, docs/production-runtime/ARCHITECTURE.md
	// Decision 14) mirrors game.StatCounters.PausedSeconds — the seconds
	// during which dexel observed nothing at all. It reaches THREE places
	// for the price of one, because every one of them reuses this type:
	// the today/lifetime buckets (StatsSave), every finalized day
	// (DayBucketSave.Counters), and both halves of an in-progress session
	// (ActiveSessionSave.Baseline/Watermark).
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
// content, so it does not weaken the privacy invariant above. An EMPTY
// Mac is never trusted: a save that arrives without a valid tag is
// treated exactly like one with a forged tag (see loadDB), so a
// hand-written, unsigned file can never mint an economy.
type SaveData struct {
	Schema     int                     `json:"schema"`
	DevCash    uint64                  `json:"devCash"`
	XP         uint64                  `json:"xp"`
	Sprint     SprintSave              `json:"sprint"`
	OwnedItems []string                `json:"ownedItems"`
	Equipped   map[string]EquippedSave `json:"equipped"`
	Stats      StatsSave               `json:"stats"`
	// Session/SessionLogHead (P2, docs/plan/P2-design.md §5.1, ADR 0017
	// Decision 5) are the in-progress session and the chained session-log's
	// HEAD. Session is nil/omitted when no session is active, which §5.4's
	// gate accepts as "no active session, the honest empty log".
	// SessionLogHead is an OPAQUE chained-MAC token (integrity.go's
	// computeLogMAC) that this package alone computes and verifies;
	// game.Game only ever carries it forward, never interpreting it.
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
	// `omitempty` because false is the zero value and the correct
	// "not paused" default.
	Paused bool   `json:"paused,omitempty"`
	Mac    string `json:"mac,omitempty"`
}

// CurrentSchema is the schema version this build writes, and the ONLY
// schema it will load. This is the public first release (v0.1.0), so it is
// reset to a clean baseline of 1: there are no prior PUBLIC saves to
// preserve, so there is no bump history and no migration/upgrade code to
// carry one schema into another. Any pre-release local save (which used
// higher, now-defunct schema numbers) is simply refused on first load with
// this binary and the economy starts fresh — the owner's accepted
// "start fresh, no migration" stance.
//
// The number is load-bearing for the reset, not just a label. loadDB
// verifies a row's MAC against the exact bytes STORED in it (not a
// re-serialization), so removing fields from SaveData alone would NOT
// invalidate a genuine old-schema row's tag — it would still verify and
// load. Resetting the schema is what forces the reset: an old save carries
// a higher number, so loadDB refuses it as a future schema
// (ErrFutureSchema, quarantined to ".future", never downgraded in place)
// and the next Save writes a clean schema-1 economy.
//
// The load rule is exactly one line of policy: a save whose Schema equals
// CurrentSchema and whose MAC verifies is used; ANYTHING else — a wrong,
// older, or future schema; a bad or absent MAC; a corrupt container — is
// quarantined and the caller starts fresh. Current-schema-or-fresh,
// nothing else. A future schema (Schema > CurrentSchema) keeps its own
// distinct quarantine (".future", ErrFutureSchema) so a newer build's save
// is never clobbered by an older build run once; every other refusal is
// ".invalid"/ErrTampered.
const CurrentSchema = 1

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
// one-time macOS/Windows relocation.
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
		equipped[slot] = EquippedSave{ItemID: ref.ItemID}
	}
	owned := make([]string, 0, len(g.OwnedItems))
	for id, ok := range g.OwnedItems {
		if ok {
			owned = append(owned, id)
		}
	}
	sort.Strings(owned)

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
		Schema:     CurrentSchema,
		DevCash:    g.DevCash,
		XP:         g.XP,
		Sprint:     SprintSave{Index: g.SprintIndex(), UnitsDone: quantizeUnits(g.Progress)},
		OwnedItems: owned,
		Equipped:   equipped,
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
		PausedSeconds:      c.PausedSeconds,
	}
}

func coinBreakdownToSave(c game.CoinBreakdown) CoinBreakdownSave {
	return CoinBreakdownSave{
		Keystrokes:    c.Keystrokes,
		Mouse:         c.Mouse,
		FocusSessions: c.FocusSessions,
	}
}

func coinBreakdownFromSave(c CoinBreakdownSave) game.CoinBreakdown {
	return game.CoinBreakdown{
		Keystrokes:    c.Keystrokes,
		Mouse:         c.Mouse,
		FocusSessions: c.FocusSessions,
	}
}

// Apply restores a SaveData onto a freshly-constructed game.New(). Every
// value is validated against the live catalog per docs/upgrade-design.md's
// "Load validation, never trust the file" rules: an unknown owned itemId
// is dropped, sprint.index is clamped, an equipped entry naming an
// unowned/unknown/wrong-slot item falls back to that slot's tier-0 item, a
// missing slot is filled with its tier-0 item, and unitsDone is clamped to
// [0, target]. None of this can panic or reject the whole file — a
// corrupted save degrades field-by-field, never all-or-nothing.
func Apply(g *game.Game, d SaveData) {
	g.DevCash = d.DevCash
	g.XP = d.XP
	g.RestoreSprint(d.Sprint.Index, d.Sprint.UnitsDone)

	// RestoreActiveSession/SetSessionLogHead (P2, docs/plan/P2-design.md
	// §5.1/§5.5, ADR 0017 Decision 5): the in-progress session and the
	// session log's opaque chain head, both carried on SaveData itself.
	// Neither interacts with rolloverStatsIfNewDay, so — unlike History/
	// Streak/CoinsToday just below — there is no ordering constraint
	// forcing this before or after RestoreStats.
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

	for _, slot := range g.Slots() {
		entry, hasEntry := d.Equipped[slot.ID]
		itemID := entry.ItemID
		if !hasEntry || itemID == "" {
			itemID = g.TierZeroItem(slot.ID)
		}
		// An equipped entry naming an unknown/wrong-slot/unowned item falls
		// back to that slot's tier-0 default. STORE-2.0: this also cleanly
		// absorbs a pre-STORE-2.0 save whose equipped slot named a now-gone
		// item id (the old un-coloured style id, e.g. "chair_racer") — it is
		// unknown to the new catalog, so the slot resets to its default
		// colour rather than the file being rejected.
		if item, ok := g.ItemByID(itemID); !ok || item.Slot != slot.ID || !g.OwnedItems[itemID] {
			itemID = g.TierZeroItem(slot.ID)
		}
		g.SetEquipped(slot.ID, itemID)
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

// LoadAll is the single load entry point (docs/plan/DB-1-design.md §4.2,
// extended by P2's session log, docs/plan/P2-design.md §5.4): state.db is
// the one source of truth. If it exists, loadDB runs §3.2's full gate
// order (corrupt -> future -> tampered -> session-log -> ok) and returns
// the verified snapshot plus its session log; if it does not exist, this
// is a genuinely fresh install — (SaveData{}, nil, false, nil), the ONLY
// "no save" case, which is what lets main.go's caller show the first-launch
// intro. A stat failure other than "does not exist" (e.g. a permission
// error) is surfaced as an error.
//
// There is no state.json import path any more: this is the public first
// release, so there are no prior public saves to import, and a stray
// state.json a pre-release build might have left is simply ignored (loadDB
// never reads it). Every quarantine/tamper/future-schema property lives in
// loadDB (db.go); LoadAll just decides whether a state.db is there to
// evaluate at all.
func LoadAll(path string) (SaveData, []SessionSave, bool, error) {
	exists, err := fileExists(path)
	if err != nil {
		return SaveData{}, nil, false, fmt.Errorf("stat %s: %w", path, err)
	}
	if !exists {
		return SaveData{}, nil, false, nil
	}
	return loadDB(path)
}

// Load is a THIN WRAPPER over LoadAll that verifies the session log chain
// identically and then discards it (docs/plan/P2-design.md §5.4:
// "verification cannot be skipped by construction... a two-function API
// where the convenient one skips integrity is exactly the silent hole ADR
// 0016 warns about"). Every call still exercises the chain check — there
// is no way to reach a SaveData without LoadAll's full gate having run,
// including the session log.
func Load(path string) (SaveData, bool, error) {
	d, _, ok, err := LoadAll(path)
	return d, ok, err
}
