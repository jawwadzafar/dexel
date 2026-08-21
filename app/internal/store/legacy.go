package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/jawwadzafar/dev-companion/app/internal/game"
)

// LegacyPath returns the frozen Rust/Bevy build's save location (ADR
// 0011), per docs/upgrade-design.md's "One-time import" section:
//
//	macOS   ~/Library/Application Support/dev-companion/save.json
//	Linux   ~/.local/share/dev-companion/save.json
func LegacyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "dev-companion", "save.json"), nil
	}
	return filepath.Join(home, ".local", "share", "dev-companion", "save.json"), nil
}

// legacyCurrentProject mirrors the Rust CurrentProjectSave.
type legacyCurrentProject struct {
	Index    int     `json:"index"`
	WorkDone float32 `json:"work_done"`
}

// legacySaveData mirrors the Rust game's SaveData struct exactly (field
// names are the on-disk JSON contract; see companion/src/lib.rs and
// docs/upgrade-design.md's worked example).
type legacySaveData struct {
	Wallet         uint64                `json:"wallet"`
	XP             uint32                `json:"xp"`
	Level          uint32                `json:"level"`
	CurrentProject *legacyCurrentProject `json:"current_project"`
	Upgrades       map[string]uint8      `json:"upgrades"`
}

// legacyGrant is one row of the migration table docs/upgrade-design.md
// gives verbatim ("[DESIGN CALL] the upgrade-tier mapping"). Paid/Value
// are cumulative totals for owning UP TO that tier (matching the old
// save's semantics: `upgrades[track]` stores the highest tier owned, and
// v0.3's own per-tier costs were cumulative).
type legacyGrant struct {
	Items []string
	Paid  uint64
	Value uint64
}

// legacyMigrationTable is transcribed row-for-row from
// docs/upgrade-design.md's table — NOT computed by a generic "N cheapest"
// rule, because the table itself has an exception (`pet` maps to
// buddy_cat, which is not the cheapest buddy item, because the old pet
// track WAS a cat). The doc says to test this with fixtures, not derive
// it by hand; this table plus store_test.go's worked-example test IS that
// fixture.
var legacyMigrationTable = map[string]map[uint8]legacyGrant{
	"keyboard": {
		1: {Items: []string{"kb_mech"}, Paid: 30, Value: 60},
		2: {Items: []string{"kb_mech", "kb_split"}, Paid: 150, Value: 240},
	},
	"mouse": {
		1: {Items: []string{"mouse_gaming"}, Paid: 20, Value: 50},
		2: {Items: []string{"mouse_gaming", "mouse_trackball"}, Paid: 110, Value: 200},
	},
	"chair": {
		1: {Items: []string{"chair_racer"}, Paid: 100, Value: 100},
		2: {Items: []string{"chair_racer", "chair_exec"}, Paid: 400, Value: 400},
	},
	"desk_decor": {
		// desk_decor fans out across TWO new slots (plant + buddy) — it was
		// the one old `Accumulate` track that rendered two objects at once.
		1: {Items: []string{"plant_succulent"}, Paid: 50, Value: 50},
		2: {Items: []string{"plant_succulent", "buddy_duck"}, Paid: 180, Value: 110},
	},
	"wall": {
		1: {Items: []string{"wall_poster"}, Paid: 80, Value: 80},
		2: {Items: []string{"wall_poster", "wall_shelf"}, Paid: 280, Value: 280},
	},
	"pet": {
		1: {Items: []string{"buddy_cat"}, Paid: 250, Value: 300},
	},
	"monitor": {
		// The Monitor slot doesn't exist in v2 (docs/upgrade-design.md:
		// "the Monitor is not a slot") — old monitor purchases are
		// refunded in full, nothing is granted.
		1: {Items: nil, Paid: 150, Value: 0},
		2: {Items: nil, Paid: 550, Value: 0},
	},
}

// LoadLegacy reads and parses the legacy Rust save at path, if present.
// Returns (nil, nil) if the file doesn't exist — "no legacy save" is not
// an error. The file is opened READ-ONLY and never modified, moved, or
// deleted (docs/upgrade-design.md: "the legacy build must stay
// launchable").
func LoadLegacy(path string) (*legacySaveData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var d legacySaveData
	if err := json.Unmarshal(data, &d); err != nil {
		// A corrupted legacy save must not block startup — treat as absent
		// (docs/upgrade-design.md: "If the Rust save is missing or
		// malformed, start fresh and log one line").
		return nil, nil
	}
	return &d, nil
}

// ImportLegacy converts a legacy Rust save into SaveData, per
// docs/upgrade-design.md's "One-time import from the Rust save":
//
//   - wallet -> devCash, xp -> xp, current_project.index -> sprint.index
//     (clamped 0..5), current_project.work_done -> sprint.unitsDone.
//   - each old track's owned tier grants the migration table's items and
//     adds max(0, paid-value) to devCash as a refund — so devCash_after >=
//     devCash_before for every input, by construction (see store_test.go).
//   - every slot also gets its free tier-0 item, exactly as a fresh
//     install would.
//   - equip rule: for each slot, equip the MOST EXPENSIVE owned item (with
//     its default tint), matching what the old build last rendered (it
//     only ever showed the highest owned tier). A slot with nothing
//     bought keeps its tier-0 item.
//   - an unknown track id is ignored; a tier above a track's known max is
//     clamped down.
func ImportLegacy(legacy *legacySaveData, catalog []game.CatalogItem) SaveData {
	itemsByID := game.ByID(catalog)
	slots := game.DefaultSlots()

	ownedItems := map[string]bool{}
	for _, slot := range slots {
		ownedItems[slotTierZero(slot.ID)] = true
	}

	devCash := legacy.Wallet
	for track, tier := range legacy.Upgrades {
		tiers, ok := legacyMigrationTable[track]
		if !ok || tier == 0 {
			continue
		}
		maxTier := uint8(0)
		for t := range tiers {
			if t > maxTier {
				maxTier = t
			}
		}
		useTier := tier
		if useTier > maxTier {
			useTier = maxTier
		}
		row, ok := tiers[useTier]
		if !ok {
			continue
		}
		for _, id := range row.Items {
			ownedItems[id] = true
		}
		if row.Paid > row.Value {
			devCash += row.Paid - row.Value
		}
	}

	equipped := map[string]EquippedSave{}
	for _, slot := range slots {
		best := ""
		var bestPrice uint64
		for id := range ownedItems {
			item, ok := itemsByID[id]
			if !ok || item.Slot != slot.ID {
				continue
			}
			if best == "" || item.Price > bestPrice {
				best = id
				bestPrice = item.Price
			}
		}
		if best == "" {
			best = slotTierZero(slot.ID)
		}
		var tintID *string
		if slot.Tintable {
			if item, ok := itemsByID[best]; ok && item.DefaultTint != nil {
				v := *item.DefaultTint
				tintID = &v
			}
		}
		equipped[slot.ID] = EquippedSave{ItemID: best, TintID: tintID}
	}

	ownedList := make([]string, 0, len(ownedItems))
	for id, ok := range ownedItems {
		if ok {
			ownedList = append(ownedList, id)
		}
	}
	sort.Strings(ownedList)

	sprintIndex, unitsDone := 0, 0.0
	if legacy.CurrentProject != nil {
		sprintIndex = clampInt(legacy.CurrentProject.Index, 0, 5)
		unitsDone = float64(legacy.CurrentProject.WorkDone)
		if unitsDone < 0 {
			unitsDone = 0
		}
	}

	return SaveData{
		Schema:           CurrentSchema,
		DevCash:          devCash,
		XP:               uint64(legacy.XP),
		Sprint:           SprintSave{Index: sprintIndex, UnitsDone: unitsDone},
		OwnedItems:       ownedList,
		OwnedTints:       nil, // migration grants only default tints, which are implicit
		Equipped:         equipped,
		ImportedFromRust: true,
		ImportedAt:       nowRFC3339(),
	}
}

// slotTierZero finds a slot's free (price 0) item directly from the live
// catalog — it derives the answer rather than hardcoding it a second time,
// so it cannot drift from game.TierZeroItem (TestCatalogIntegrity in the
// game package already asserts there is exactly one such item per slot).
func slotTierZero(slotID string) string {
	for _, it := range game.DefaultCatalog() {
		if it.Slot == slotID && it.Price == 0 {
			return it.ID
		}
	}
	return ""
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
