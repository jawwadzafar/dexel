package game

import (
	"errors"
	"testing"
)

// xpForLevel returns an XP value that lands the player exactly at level n
// (the level curve's threshold, 50*(n-1)*n — see thresholdForLevel).
func xpForLevel(n int) uint64 { return thresholdForLevel(n) }

// TestBuyItemRefusesBelowLevel: a level-gated item cannot be bought while
// under its MinLevel, even with cash to spare, and the refusal mutates
// nothing (no ownership, no coins spent).
func TestBuyItemRefusesBelowLevel(t *testing.T) {
	g := New()
	g.DevCash = 10000 // plenty — affordability must NOT be the thing that refuses
	g.XP = 0          // LV1

	const item = "hoodie_cloak_neon" // MinLevel 8, price 500
	beforeCash := g.DevCash
	err := g.BuyItem(item)
	if !errors.Is(err, ErrLevelLocked) {
		t.Fatalf("BuyItem(%q) at LV1 with cash: got %v, want ErrLevelLocked", item, err)
	}
	if g.OwnedItems[item] {
		t.Errorf("%q marked owned after a refused (level-locked) purchase", item)
	}
	if g.DevCash != beforeCash {
		t.Errorf("DevCash = %d after a refused purchase, want unchanged %d", g.DevCash, beforeCash)
	}
}

// TestBuyItemLevelCheckPrecedesFunds: when the player is BOTH under-level
// and broke, the level gate wins — "reach LV n" is the truthful message
// (money cannot buy a locked item). Still no mutation.
func TestBuyItemLevelCheckPrecedesFunds(t *testing.T) {
	g := New()
	g.DevCash = 0 // broke
	g.XP = 0      // LV1

	const item = "chair_antigrav_cobalt" // MinLevel 8, price 500
	if err := g.BuyItem(item); !errors.Is(err, ErrLevelLocked) {
		t.Fatalf("BuyItem(%q) broke and under-level: got %v, want ErrLevelLocked (level checked before funds)", item, err)
	}
	if g.OwnedItems[item] {
		t.Errorf("%q owned after a refused purchase", item)
	}
}

// TestBuyItemAllowsAtOrAboveLevel: at exactly the required level (and with
// funds) the same item buys normally and spends its price.
func TestBuyItemAllowsAtOrAboveLevel(t *testing.T) {
	// Buy at EXACTLY MinLevel, and again (a different item) above it.
	cases := []struct {
		item     string
		minLevel int
		price    uint64
		atLevel  int
	}{
		{"hoodie_cloak_neon", 8, 500, 8}, // exactly at MinLevel
		{"buddy_cat", 10, 300, 10},       // the highest gate, exactly
		{"kb_neon", 7, 300, 9},           // comfortably above MinLevel
	}
	for _, tc := range cases {
		g := New()
		g.DevCash = 10000
		g.XP = xpForLevel(tc.atLevel)
		if got := levelForXP(g.XP); got != tc.atLevel {
			t.Fatalf("test setup: levelForXP(%d) = %d, want %d", g.XP, got, tc.atLevel)
		}
		before := g.DevCash
		if err := g.BuyItem(tc.item); err != nil {
			t.Fatalf("BuyItem(%q) at LV%d (MinLevel %d): unexpected error %v", tc.item, tc.atLevel, tc.minLevel, err)
		}
		if !g.OwnedItems[tc.item] {
			t.Errorf("%q not owned after a successful at/above-level purchase", tc.item)
		}
		if g.DevCash != before-tc.price {
			t.Errorf("DevCash = %d after buying %q, want %d (price %d)", g.DevCash, tc.item, before-tc.price, tc.price)
		}
	}
}

// TestLowTierGateBoundary exercises the gate at the low end too: a
// first-upgrade item (kb_mech, MinLevel 2) is refused at LV1 and buys the
// moment the player reaches LV2 — the "steady drip" starts immediately.
func TestLowTierGateBoundary(t *testing.T) {
	const item = "kb_mech" // MinLevel 2, price 60

	g := New()
	g.DevCash = 10000
	g.XP = 0 // LV1
	if err := g.BuyItem(item); !errors.Is(err, ErrLevelLocked) {
		t.Fatalf("BuyItem(%q) at LV1: got %v, want ErrLevelLocked", item, err)
	}

	g2 := New()
	g2.DevCash = 10000
	g2.XP = xpForLevel(2) // exactly LV2
	if err := g2.BuyItem(item); err != nil {
		t.Fatalf("BuyItem(%q) at LV2: unexpected error %v", item, err)
	}
	if !g2.OwnedItems[item] {
		t.Errorf("%q not owned after buying at LV2", item)
	}
}
