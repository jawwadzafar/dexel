package game

import "testing"

// slotGroups returns the catalog items grouped by slot, each group in the
// catalog's own cheapest-first order (which is also rank order: index 0 is
// the free tier-0 default, index 3 the fanciest). catalogItems is authored
// as exactly four items per slot, in slot order, so a simple chunk walk is
// faithful — the test below re-derives that shape rather than trusting it.
func slotGroups(t *testing.T) map[string][]CatalogItem {
	t.Helper()
	groups := map[string][]CatalogItem{}
	var order []string
	for _, it := range catalogItems {
		if _, seen := groups[it.Slot]; !seen {
			order = append(order, it.Slot)
		}
		groups[it.Slot] = append(groups[it.Slot], it)
	}
	for _, slot := range order {
		if len(groups[slot]) != 4 {
			t.Fatalf("slot %q has %d items, expected 4 (the ladder assumes rank 0..3)", slot, len(groups[slot]))
		}
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

// TestMinLevelLadder pins the owner-approved level-gating ladder: every
// item's MinLevel is sane (0..10), the free tier-0 default of every slot
// is ungated (MinLevel 0 = LV1), MinLevel never decreases as an item gets
// fancier within its slot, and each rank sits in its band — first upgrade
// LV2-3, mid LV4-6, top/show-off LV7-10.
func TestMinLevelLadder(t *testing.T) {
	groups := slotGroups(t)

	// Every item: MinLevel in a sane range.
	for _, it := range catalogItems {
		if it.MinLevel < 0 || it.MinLevel > 10 {
			t.Errorf("item %q MinLevel = %d, want within [0,10]", it.ID, it.MinLevel)
		}
	}

	// Free tier-0 defaults are ungated (LV1).
	for slot, id := range tierZeroItemBySlot {
		var found bool
		for _, it := range groups[slot] {
			if it.ID == id {
				found = true
				if it.MinLevel != 0 {
					t.Errorf("tier-0 default %q (slot %s) MinLevel = %d, want 0 (ungated LV1)", id, slot, it.MinLevel)
				}
			}
		}
		if !found {
			t.Errorf("tier-0 default %q not found in slot %s", id, slot)
		}
	}

	// Per-slot ladder: rank 0 free, ranks monotonically non-decreasing,
	// and each rank inside its owner-approved band.
	bands := [4][2]int{{0, 0}, {2, 3}, {4, 6}, {7, 10}}
	for slot, items := range groups {
		if items[0].MinLevel != 0 {
			t.Errorf("slot %s rank-0 %q MinLevel = %d, want 0", slot, items[0].ID, items[0].MinLevel)
		}
		for rank, it := range items {
			lo, hi := bands[rank][0], bands[rank][1]
			if it.MinLevel < lo || it.MinLevel > hi {
				t.Errorf("slot %s rank-%d %q MinLevel = %d, want within [%d,%d]", slot, rank, it.ID, it.MinLevel, lo, hi)
			}
			if rank > 0 && it.MinLevel < items[rank-1].MinLevel {
				t.Errorf("slot %s: %q (MinLevel %d) is gated lower than the cheaper %q (MinLevel %d) — ladder must not decrease",
					slot, it.ID, it.MinLevel, items[rank-1].ID, items[rank-1].MinLevel)
			}
		}
	}
}

// TestTopTierLockBites is the crossover guard: for every slot's fanciest
// (rank-3) item, its MinLevel must be STRICTLY above the level at which a
// hoarder could first afford it. That is the whole point of the feature —
// the owner's "good stuff should stay locked behind a level even when you
// could afford it": level, not cash, is the binding constraint on the top
// tier. (Mid and cheap tiers are deliberately left roughly cash-bound and
// are not asserted here.)
func TestTopTierLockBites(t *testing.T) {
	groups := slotGroups(t)
	for slot, items := range groups {
		top := items[3]
		afford := affordabilityLevel(top.Price)
		if top.MinLevel <= afford {
			t.Errorf("slot %s top item %q (price %d): MinLevel %d does NOT bite — affordable at LV%d, so cash is the constraint, not level",
				slot, top.ID, top.Price, top.MinLevel, afford)
		} else {
			t.Logf("slot %-8s %-16s price %3d  affordable@LV%d  MinLevel LV%d  bite +%d",
				slot, top.ID, top.Price, afford, top.MinLevel, top.MinLevel-afford)
		}
	}
}
