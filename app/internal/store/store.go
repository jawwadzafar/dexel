// Package store persists game.Game to disk as JSON and imports the legacy
// Rust save on first run, per docs/upgrade-design.md's "Persistence"
// section. It knows about game.Game's public API but nothing about the
// engine or activity packages — persistence is a leaf, not a hub.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jawwadzafar/dev-companion/app/internal/game"
)

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

// SaveData is the on-disk shape at ~/.config/devcompanion/state.json,
// transcribed field-for-field from docs/upgrade-design.md's "Persistence"
// section. This IS the save format — changing a field name silently
// orphans existing saves.
//
// Privacy invariant (ADR 0002/0009, carried from the Rust save): this
// file contains no user content. Sprint names come from the static list
// (never from input); only an index and a work-unit float are stored.
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
}

// CurrentSchema is the schema version this build writes.
const CurrentSchema = 1

// DefaultPath returns ~/.config/devcompanion/state.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "devcompanion", "state.json"), nil
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

	return SaveData{
		Schema:           CurrentSchema,
		DevCash:          g.DevCash,
		XP:               g.XP,
		Sprint:           SprintSave{Index: g.SprintIndex(), UnitsDone: g.Progress},
		OwnedItems:       owned,
		OwnedTints:       tints,
		Equipped:         equipped,
		ImportedFromRust: g.ImportedFromRust,
		ImportedAt:       g.ImportedAt,
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
// over the destination (docs/upgrade-design.md's exact recipe). A crash
// mid-write can never leave a half-written, unparseable state.json behind.
func Save(path string, d SaveData) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create save dir: %w", err)
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal save: %w", err)
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
	return nil
}

// Load reads and parses path. A missing file returns (SaveData{}, false,
// nil) — "no save yet" is not an error. A malformed file is renamed to
// "state.json.corrupt" (never deleted, so a user can send it in) and
// reported as "no save" rather than an error the caller must handle —
// docs/upgrade-design.md: "log once, start fresh... never delete the bad
// file."
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
	return d, true, nil
}

// nowRFC3339 is a test seam for ImportLegacy's timestamp.
var nowRFC3339 = func() string { return time.Now().UTC().Format(time.RFC3339) }
