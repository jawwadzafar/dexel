// Package store persists game.Game to disk as JSON and imports the legacy
// Rust save on first run, per docs/upgrade-design.md's "Persistence"
// section. It knows about game.Game's public API but nothing about the
// engine or activity packages — persistence is a leaf, not a hub.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/game"
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
// content, so it does not weaken the privacy invariant above. It is ""
// only for a grandfathered schema-4-or-earlier save that predates this
// field (see Load's doc comment and CurrentSchema's).
type SaveData struct {
	Schema           int                     `json:"schema"`
	DevCash          uint64                  `json:"devCash"`
	XP               uint64                  `json:"xp"`
	Sprint           SprintSave              `json:"sprint"`
	OwnedItems       []string                `json:"ownedItems"`
	OwnedTints       []string                `json:"ownedTints"`
	Equipped         map[string]EquippedSave `json:"equipped"`
	ImportedFromRust bool                    `json:"importedFromRust,omitempty"`
	ImportedAt       string                  `json:"importedAt,omitempty"` // RFC3339, "" if never imported
	Stats            StatsSave               `json:"stats"`
	Mac              string                  `json:"mac,omitempty"`
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
// schema >= 5 is the signal that MAC verification is IN EFFECT (Load
// verifies only at schema >= 5), so the bump is load-bearing for the
// anti-cheat mechanism itself, not just a version label. A pre-SEC-1
// schema-4-or-earlier file has no "mac" key; Load grandfathers it in as
// trusted (no verification), and the next Save re-persists it as a
// signed schema-5 file — never mistaken for tampering. ErrFutureSchema is
// unchanged by this bump: a schema-6 save is still renamed to ".future"
// and refused, never silently downgraded (see
// TestFutureSchema6RefusalStillFiresAfterTheSchema5Bump).
const CurrentSchema = 5

// DefaultPath returns ~/.config/dexel/state.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "dexel", "state.json"), nil
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

func splitTintKey(key string) (itemID, tintID string, ok bool) {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == ':' {
			return key[:i], key[i+1:], true
		}
	}
	return "", "", false
}

// Save writes d to path atomically: write state.json.tmp, fsync, rename
// over the destination (docs/upgrade-design.md's exact recipe, factored
// out as writeFileAtomically below so SaveConfig — config.go — can reuse
// it verbatim for config.json). A crash mid-write can never leave a
// half-written, unparseable state.json behind.
//
// SEC-1 (docs/plan/SEC-1-design.md §2, ADR 0014): before marshaling, Save
// quantizes d.Sprint.UnitsDone (quantizeUnits — a no-op if Snapshot
// already quantized it, but this makes Save itself correct for any
// caller, not just ones that went through Snapshot) and then computes and
// sets d.Mac via computeMAC (integrity.go), so every file Save writes is
// self-consistently signed regardless of what schema value it carries —
// verification of that tag is Load's job, gated on schema >= 5.
func Save(path string, d SaveData) error {
	d.Sprint.UnitsDone = quantizeUnits(d.Sprint.UnitsDone)
	d.Mac = computeMAC(d)

	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal save: %w", err)
	}
	return writeFileAtomically(path, data)
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

// Load reads and parses path. A missing file returns (SaveData{}, false,
// nil) — "no save yet" is not an error. A malformed file is renamed to
// "state.json.corrupt" (never deleted, so a user can send it in) and
// reported as "no save" rather than an error the caller must handle —
// docs/upgrade-design.md: "log once, start fresh... never delete the bad
// file."
//
// A save whose Schema is NEWER than CurrentSchema (i.e. written by a
// build ahead of this one — the classic "ran an older build once after
// upgrading" scenario) is handled differently from a malformed file:
// this build does not understand every field that save might carry, so
// silently returning (SaveData{}, false, nil) — "no save, start fresh" —
// would be actively dangerous. main.go's loadOrImport, seeing "no save,"
// would go on to either start completely fresh OR run the legacy-import
// path, and either way its very next autosave (or the immediate
// legacy-import Save call) would overwrite path with a schema-1-shaped
// rewrite of what used to be newer data — a silent downgrade that
// destroys progress the moment someone runs an older build once, with no
// error and no way back. Instead: the original file is renamed to
// "<path>.future" completely untouched, and Load returns an error (never
// (_, true, _) for a future schema) so the caller treats this as a load
// FAILURE, not "no save" — the same way an unreadable file is surfaced —
// while the future-schema data sits recoverable at its own path rather
// than being silently clobbered by whatever this older build does next.
//
// SEC-1 (docs/plan/SEC-1-design.md §4, ADR 0014): a save whose Schema is
// >= 5 (i.e. this build's or a prior SEC-1-era build's) that parses fine
// but whose Mac does not verify (integrity.go's verifyMAC) is handled the
// same way as a future schema, and deliberately NOT the same way as a
// missing file: the original is renamed to "<path>.invalid" (never
// deleted) and Load returns an error wrapping ErrTampered, never
// (_, true, _) and never (SaveData{}, false, nil). That last distinction
// is the entire point — collapsing a tamper failure into "no save" would
// let main.go's loadOrImport fall through to the legacy-import path,
// which grants items and refunds Dev Cash, so a hand-edited state.json
// could trigger a legacy re-grant. Returning a distinct sentinel closes
// that vector the exact same way ErrFutureSchema already does: the
// caller must treat "load failed" and "no save yet" as different things.
// A schema-4-or-earlier file has no Mac to check at all — it is
// grandfathered in as trusted, and the next Save call re-persists it
// signed at schema 5 (see CurrentSchema's doc comment).
func Load(path string) (SaveData, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SaveData{}, false, nil
		}
		return SaveData{}, false, fmt.Errorf("read save file: %w", err)
	}
	var d SaveData
	if err := json.Unmarshal(data, &d); err != nil {
		corruptPath := path + ".corrupt"
		_ = os.Rename(path, corruptPath)
		return SaveData{}, false, fmt.Errorf("parse save file (moved to %s): %w", corruptPath, err)
	}
	if d.Schema > CurrentSchema {
		futurePath := path + ".future"
		if renameErr := os.Rename(path, futurePath); renameErr != nil {
			return SaveData{}, false, fmt.Errorf("%w: schema %d > this build's %d, and backing up %s to %s failed: %v",
				ErrFutureSchema, d.Schema, CurrentSchema, path, futurePath, renameErr)
		}
		return SaveData{}, false, fmt.Errorf("%w: schema %d > this build's %d; original preserved untouched at %s (NOT loaded, NOT deleted, NOT overwritten)",
			ErrFutureSchema, d.Schema, CurrentSchema, futurePath)
	}
	if d.Schema >= 5 && !verifyMAC(d) {
		invalidPath := path + ".invalid"
		if renameErr := os.Rename(path, invalidPath); renameErr != nil {
			return SaveData{}, false, fmt.Errorf("%w: MAC mismatch, and backing up %s to %s failed: %v",
				ErrTampered, path, invalidPath, renameErr)
		}
		return SaveData{}, false, fmt.Errorf("%w: MAC mismatch; original preserved untouched at %s (NOT loaded, NOT deleted, NOT overwritten)",
			ErrTampered, invalidPath)
	}
	return d, true, nil
}

// nowRFC3339 is a test seam for ImportLegacy's timestamp.
var nowRFC3339 = func() string { return time.Now().UTC().Format(time.RFC3339) }
