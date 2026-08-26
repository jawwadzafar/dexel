package game

import (
	"sort"
	"testing"
)

// slotGroups returns the catalog items grouped by slot, each group in the
// catalog's own cheapest-first order (index 0 is the slot's free tier-0
// default). STORE-2.0 turned colours into items, so a slot no longer holds a
// fixed count — the tests below re-derive shape rather than assuming it.
func slotGroups(t *testing.T) map[string][]CatalogItem {
	t.Helper()
	groups := map[string][]CatalogItem{}
	for _, it := range catalogItems {
		groups[it.Slot] = append(groups[it.Slot], it)
	}
	return groups
}

// affordabilityLevel is the earliest level at which a pure hoarder — a
// fresh player saving EVERY coin toward one item, buying nothing else —
// could afford `price`. It walks the real deterministic sprint rotation
// (sprintOrder is the play order: a fresh game starts on sprints[0] =
// sprintOrder[0], and nextSprintIndex then steps through sprintOrder), so
// it is the strongest possible affordability claim: if MinLevel exceeds
// THIS level, then no player — however frugal — can buy the item before
// reaching MinLevel, i.e. the level gate is provably the binding
// constraint for everyone. XP outpaces DevCash per sprint, so the level a
// hoarder reaches when they can first afford an item is what we compare
// MinLevel against.
func affordabilityLevel(price uint64) int {
	var cash, xp uint64
	for cycle := 0; cycle < 1000; cycle++ {
		for _, idx := range sprintOrder {
			d := sprints[idx]
			cash += d.DevCash
			xp += d.XP
			if cash >= price {
				return levelForXP(xp)
			}
		}
	}
	return 1 << 30
}

// TestMinLevelLadder pins the owner-approved level-gating ladder under
// STORE-2.0: every item's MinLevel is sane (0..10), every slot has exactly
// one free (price-0) item and it is that slot's ungated (MinLevel 0) tier-0
// default, and within a slot MinLevel never decreases as price rises — so a
// pricier colour/style is never gated LOWER than a cheaper one.
func TestMinLevelLadder(t *testing.T) {
	groups := slotGroups(t)

	// Every item: MinLevel in a sane range.
	for _, it := range catalogItems {
		if it.MinLevel < 0 || it.MinLevel > 10 {
			t.Errorf("item %q MinLevel = %d, want within [0,10]", it.ID, it.MinLevel)
		}
	}

	// Every slot: exactly one free item, and it is the tier-0 default,
	// ungated (MinLevel 0).
	for _, slot := range DefaultSlots() {
		items := groups[slot.ID]
		if len(items) == 0 {
			t.Errorf("slot %q has no items", slot.ID)
			continue
		}
		free := 0
		for _, it := range items {
			if it.Price == 0 {
				free++
				if it.MinLevel != 0 {
					t.Errorf("free item %q (slot %s) MinLevel = %d, want 0 (ungated LV1)", it.ID, slot.ID, it.MinLevel)
				}
				if it.ID != tierZeroItemBySlot[slot.ID] {
					t.Errorf("slot %s free item %q is not the tier-0 default %q", slot.ID, it.ID, tierZeroItemBySlot[slot.ID])
				}
			}
		}
		if free != 1 {
			t.Errorf("slot %q has %d free (price 0) items, want exactly 1", slot.ID, free)
		}
	}

	// Per-slot ladder: sorted by price ascending, MinLevel is
	// non-decreasing (a pricier item is never gated lower than a cheaper).
	for slotID, items := range groups {
		byPrice := append([]CatalogItem(nil), items...)
		sort.SliceStable(byPrice, func(i, j int) bool { return byPrice[i].Price < byPrice[j].Price })
		for i := 1; i < len(byPrice); i++ {
			if byPrice[i].MinLevel < byPrice[i-1].MinLevel {
				t.Errorf("slot %s: %q (price %d, MinLevel %d) is gated lower than the cheaper %q (price %d, MinLevel %d) — ladder must not decrease",
					slotID, byPrice[i].ID, byPrice[i].Price, byPrice[i].MinLevel,
					byPrice[i-1].ID, byPrice[i-1].Price, byPrice[i-1].MinLevel)
			}
		}
	}
}

// TestTopTierLockBites is the crossover guard: for every slot's most
// expensive item, its MinLevel must be STRICTLY above the level at which a
// hoarder could first afford it. That is the whole point of the feature —
// the owner's "good stuff should stay locked behind a level even when you
// could afford it": level, not cash, is the binding constraint on the top
// tier.
func TestTopTierLockBites(t *testing.T) {
	groups := slotGroups(t)
	for slotID, items := range groups {
		top := items[0]
		for _, it := range items {
			if it.Price > top.Price {
				top = it
			}
		}
		afford := affordabilityLevel(top.Price)
		if top.MinLevel <= afford {
			t.Errorf("slot %s top item %q (price %d): MinLevel %d does NOT bite — affordable at LV%d, so cash is the constraint, not level",
				slotID, top.ID, top.Price, top.MinLevel, afford)
		} else {
			t.Logf("slot %-8s %-22s price %3d  affordable@LV%d  MinLevel LV%d  bite +%d",
				slotID, top.ID, top.Price, afford, top.MinLevel, top.MinLevel-afford)
		}
	}
}
