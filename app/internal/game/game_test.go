package game

import (
	"errors"
	"slices"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/activity"
	"github.com/jawwadzafar/dexel/app/internal/engine"
)

// TestCatalogIntegrity guards the catalog against the structural invariants
// that survive STORE-2.0: 9 slots, every slot has at least one item and
// exactly one free (price-0) tier-0 item, the three "nothing" items carry no
// sprite, and every tier-zero mapping resolves. Colours are now ordinary
// items and the tint system is gone, so the old fixed per-slot count and
// defaultTint checks are retired.
func TestCatalogIntegrity(t *testing.T) {
	slots := DefaultSlots()
	if len(slots) != 9 {
		t.Fatalf("got %d slots, want 9", len(slots))
	}
	slotByID := SlotsByID(slots)

	items := DefaultCatalog()
	if len(items) == 0 {
		t.Fatal("catalog is empty")
	}

	seenIDs := map[string]bool{}
	perSlot := map[string]int{}
	freeCountPerSlot := map[string]int{}

	for _, it := range items {
		if it.ID == "" {
			t.Fatalf("item %+v has empty ID", it)
		}
		if seenIDs[it.ID] {
			t.Errorf("duplicate item id %q", it.ID)
		}
		seenIDs[it.ID] = true

		if _, ok := slotByID[it.Slot]; !ok {
			t.Errorf("item %q references unknown slot %q", it.ID, it.Slot)
			continue
		}
		perSlot[it.Slot]++
		if it.Price == 0 {
			freeCountPerSlot[it.Slot]++
		}

		if it.Sprite == nil && it.Thumb != nil {
			t.Errorf("item %q has no sprite but a non-nil thumb", it.ID)
		}
	}

	for _, slot := range slots {
		if perSlot[slot.ID] < 1 {
			t.Errorf("slot %q has no items", slot.ID)
		}
		if freeCountPerSlot[slot.ID] != 1 {
			t.Errorf("slot %q has %d free (price 0) items, want exactly 1", slot.ID, freeCountPerSlot[slot.ID])
		}
	}
	for _, id := range []string{"plant_none", "wall_bare", "buddy_none"} {
		it, ok := ByID(items)[id]
		if !ok {
			t.Errorf("expected explicit nothing-item %q to exist", id)
			continue
		}
		if it.Sprite != nil {
			t.Errorf("nothing-item %q unexpectedly has a sprite", id)
		}
	}

	// Every tier-zero mapping must point at a real, free, correctly-slotted item.
	byID := ByID(items)
	for slotID, itemID := range tierZeroItemBySlot {
		it, ok := byID[itemID]
		if !ok {
			t.Errorf("tierZeroItemBySlot[%q] = %q does not exist in the catalog", slotID, itemID)
			continue
		}
		if it.Slot != slotID {
			t.Errorf("tierZeroItemBySlot[%q] = %q belongs to slot %q", slotID, itemID, it.Slot)
		}
		if it.Price != 0 {
			t.Errorf("tierZeroItemBySlot[%q] = %q is not free (price %d)", slotID, itemID, it.Price)
		}
	}
}

func TestNewGameOwnsAndEquipsEveryTierZeroItem(t *testing.T) {
	g := New()
	for _, slot := range DefaultSlots() {
		id := tierZeroItemBySlot[slot.ID]
		if !g.OwnedItems[id] {
			t.Errorf("tier-0 item %q for slot %q not owned on New()", id, slot.ID)
		}
		ref, ok := g.Equipped[slot.ID]
		if !ok {
			t.Fatalf("slot %q has no Equipped entry on New()", slot.ID)
		}
		if ref.ItemID != id {
			t.Errorf("slot %q equipped %q, want tier-0 %q", slot.ID, ref.ItemID, id)
		}
	}
}

func TestBuyItemSpendsAndOwns(t *testing.T) {
	g := New()
	g.DevCash = 1000
	g.XP = 5000 // LV10: unlock every level-gated item; this test exercises funds/ownership, not the level gate
	if err := g.BuyItem("chair_racer_ember"); err != nil {
		t.Fatalf("BuyItem: %v", err)
	}
	if !g.OwnedItems["chair_racer_ember"] {
		t.Error("chair_racer_ember not marked owned after purchase")
	}
	if g.DevCash != 900 {
		t.Errorf("DevCash = %d, want 900", g.DevCash)
	}
	if err := g.BuyItem("chair_racer_ember"); !errors.Is(err, ErrAlreadyOwned) {
		t.Errorf("BuyItem again: got %v, want ErrAlreadyOwned", err)
	}
}

func TestBuyItemRejectsInsufficientFundsAndUnknownID(t *testing.T) {
	g := New()
	g.XP = 5000 // LV10 so the level gate passes and the funds check is what refuses (DevCash stays 0)
	if err := g.BuyItem("chair_racer_ember"); !errors.Is(err, ErrInsufficientFunds) {
		t.Errorf("got %v, want ErrInsufficientFunds", err)
	}
	if g.OwnedItems["chair_racer_ember"] {
		t.Error("item should not be owned after a failed purchase")
	}
	if err := g.BuyItem("does_not_exist"); !errors.Is(err, ErrUnknownItem) {
		t.Errorf("got %v, want ErrUnknownItem", err)
	}
}

func TestEquipRequiresOwnershipAndCorrectSlot(t *testing.T) {
	g := New()
	g.DevCash = 1000
	g.XP = 5000 // LV10: unlock chair_racer_ember so BuyItem below succeeds regardless of the level gate

	if err := g.EquipItem("chair", "chair_racer_ember"); !errors.Is(err, ErrNotOwned) {
		t.Errorf("equipping unowned item: got %v, want ErrNotOwned", err)
	}
	if err := g.BuyItem("chair_racer_ember"); err != nil {
		t.Fatalf("BuyItem: %v", err)
	}
	if err := g.EquipItem("hoodie", "chair_racer_ember"); !errors.Is(err, ErrSlotMismatch) {
		t.Errorf("equipping into the wrong slot: got %v, want ErrSlotMismatch", err)
	}
	if err := g.EquipItem("chair", "chair_racer_ember"); err != nil {
		t.Fatalf("EquipItem: %v", err)
	}
	if g.Equipped["chair"].ItemID != "chair_racer_ember" {
		t.Errorf("Equipped[chair] = %+v, want chair_racer_ember", g.Equipped["chair"])
	}
}

func TestEquipOneWinsPerSlot(t *testing.T) {
	g := New()
	g.DevCash = 1000
	g.XP = 5000 // LV10: unlock hoodie_zip_slate so this equip test isn't blocked by the level gate
	_ = g.BuyItem("hoodie_zip_slate")
	_ = g.EquipItem("hoodie", "hoodie_zip_slate")
	if g.Equipped["hoodie"].ItemID != "hoodie_zip_slate" {
		t.Fatalf("Equipped[hoodie] = %+v, want hoodie_zip_slate", g.Equipped["hoodie"])
	}
	if !g.OwnedItems["hoodie_classic_indigo"] {
		t.Error("equipping hoodie_zip_slate should not un-own the tier-0 hoodie_classic_indigo")
	}
	// Switch back — own-many, equip-one: nothing was destroyed.
	_ = g.EquipItem("hoodie", "hoodie_classic_indigo")
	if g.Equipped["hoodie"].ItemID != "hoodie_classic_indigo" {
		t.Fatalf("Equipped[hoodie] = %+v, want hoodie_classic_indigo", g.Equipped["hoodie"])
	}
}

// STORE-REDESIGN (docs/plan/ROADMAP.md §STORE-REDESIGN) — the one-click
// combined action. Each sub-test drives one path of BuyAndEquip and checks
// that funds are spent exactly ONCE (never per-step, never double) and that
// the item ends up both owned and equipped.
func TestBuyAndEquipBuysAndEquipsInOneCall(t *testing.T) {
	// Non-tintable slot: buy + equip in one call, price charged once.
	g := New()
	g.DevCash = 1000
	g.XP = 5000 // LV10 so no level gate interferes with this funds/equip path
	if err := g.BuyAndEquip("keyboard", "kb_mech"); err != nil {
		t.Fatalf("BuyAndEquip(kb_mech): %v", err)
	}
	if !g.OwnedItems["kb_mech"] {
		t.Error("kb_mech should be owned after BuyAndEquip")
	}
	if g.Equipped["keyboard"].ItemID != "kb_mech" {
		t.Errorf("Equipped[keyboard] = %+v, want kb_mech", g.Equipped["keyboard"])
	}
	if g.DevCash != 940 { // kb_mech is 60
		t.Errorf("DevCash = %d, want 940 (kb_mech 60, charged once)", g.DevCash)
	}
	// A second click on the now-owned+equipped item is a harmless no-op:
	// nothing more is charged.
	if err := g.BuyAndEquip("keyboard", "kb_mech"); err != nil {
		t.Fatalf("re-BuyAndEquip(kb_mech): %v", err)
	}
	if g.DevCash != 940 {
		t.Errorf("DevCash = %d after re-equip, want 940 (owned item costs nothing)", g.DevCash)
	}
}

func TestBuyAndEquipIsAtomicOnRefusal(t *testing.T) {
	// Cost exceeds funds: nothing is bought, nothing equipped, nothing spent.
	g := New()
	g.DevCash = 50 // NOT enough for chair_racer_ember (100)
	g.XP = 5000
	err := g.BuyAndEquip("chair", "chair_racer_ember")
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("got %v, want ErrInsufficientFunds", err)
	}
	if g.OwnedItems["chair_racer_ember"] {
		t.Error("a refused buy must own NOTHING")
	}
	if g.DevCash != 50 {
		t.Errorf("DevCash = %d, want 50 (untouched on refusal)", g.DevCash)
	}
	if g.Equipped["chair"].ItemID == "chair_racer_ember" {
		t.Error("a refused buy must not equip the item")
	}

	// Level gate wins over affordability and mutates nothing.
	g2 := New()
	g2.DevCash = 100000 // rich, but...
	g2.XP = 0           // ...LV1, below chair_racer_ember's LV3 gate
	if err := g2.BuyAndEquip("chair", "chair_racer_ember"); !errors.Is(err, ErrLevelLocked) {
		t.Fatalf("got %v, want ErrLevelLocked", err)
	}
	if g2.OwnedItems["chair_racer_ember"] || g2.DevCash != 100000 {
		t.Error("a level-locked buy must own nothing and spend nothing")
	}
}

func TestTickAdvancesSprintAndPaysOutDevCashAndXPOnCompletion(t *testing.T) {
	g := New()
	if completed := g.Tick(engine.TickResult{Mood: engine.MoodCoding, WorkUnits: 50}); !completed {
		t.Fatal("expected the first sprint (target 50) to complete on exactly 50 work units")
	}
	if g.DevCash != 25 {
		t.Errorf("DevCash = %d, want 25 (sprint 0's reward)", g.DevCash)
	}
	if g.XP != 40 {
		t.Errorf("XP = %d, want 40 (sprint 0's reward)", g.XP)
	}
	if g.SprintIndex() != 1 {
		t.Errorf("SprintIndex = %d, want 1 (rolled to the next sprint)", g.SprintIndex())
	}
	if g.Progress != 0 {
		t.Errorf("Progress = %v, want 0 (no overshoot)", g.Progress)
	}
}

func TestTickCarriesOvershootIntoTheNextSprint(t *testing.T) {
	g := New()
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, WorkUnits: 55}) // sprint 0 target is 50
	if g.SprintIndex() != 1 {
		t.Fatalf("SprintIndex = %d, want 1", g.SprintIndex())
	}
	if g.Progress != 5 {
		t.Errorf("Progress = %v, want 5 (55-50 overshoot carried forward)", g.Progress)
	}
}

func TestTickWrapsSprintRotation(t *testing.T) {
	g := New()
	for i := 0; i < len(sprints); i++ {
		def := sprintAt(g.SprintIndex())
		g.Tick(engine.TickResult{Mood: engine.MoodCoding, WorkUnits: def.Target})
	}
	if g.SprintIndex() != 0 {
		t.Errorf("SprintIndex after a full rotation = %d, want 0 (wrapped)", g.SprintIndex())
	}
}

// TestStoreOpenFreezesWorkAndDevCashAndMood is the hard requirement from
// docs/ui-spec.md §5.3: while the store is open, keystrokes/idle time must
// not earn work, Dev Cash, or flip the mood — including not flipping to
// onBreak, and including a shopping BURST not retroactively counting once
// the store closes.
func TestStoreOpenFreezesWorkAndDevCashAndMood(t *testing.T) {
	g := New()
	g.Mood = engine.MoodCoding // pretend we were coding right before opening the store
	g.OpenStore(1)

	startCash := g.DevCash
	startProgress := g.Progress

	// A big, suspicious tick: lots of "work" and a mood the engine claims
	// is onBreak — exactly what a global sampler could produce while the
	// user is just clicking around the store modal with the keyboard
	// idle. None of it may land.
	completed := g.Tick(engine.TickResult{Mood: engine.MoodOnBreak, WorkUnits: 999})
	if completed {
		t.Error("a sprint should never complete while the store is open")
	}
	if g.DevCash != startCash {
		t.Errorf("DevCash changed while store open: %d -> %d", startCash, g.DevCash)
	}
	if g.Progress != startProgress {
		t.Errorf("Progress changed while store open: %v -> %v", startProgress, g.Progress)
	}
	if g.Mood != engine.MoodCoding {
		t.Errorf("Mood = %v, want held at coding (must never flip to onBreak while shopping)", g.Mood)
	}

	// Close the store: the NEXT tick's work must reflect only what happens
	// after closing, not a retroactive burst from the frozen period. Tick
	// is a pure function of the TickResult it's handed, so simulate "the
	// engine's baseline already absorbed the shopping keystrokes" by
	// simply handing a normal, small WorkUnits value post-close.
	g.CloseStore(1)
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, WorkUnits: 0.5})
	if g.Progress != startProgress+0.5 {
		t.Errorf("Progress after closing = %v, want %v (only the post-close tick's work)", g.Progress, startProgress+0.5)
	}
}

func TestStateActivityLineIsHonestAndScreenLinesAlwaysElevenTickerAlwaysThree(t *testing.T) {
	g := New()
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, ActiveApp: "code", ActiveAppDisplay: "VS Code"})
	s := g.State()
	// The activity line is now drawn from a small pool per app type, chosen
	// stably rather than frozen to one string (see activity_line.go's
	// stability rule), so State() is pinned to "it is one of the phrasings
	// this type+mood licenses" instead of one literal. The pool for a
	// coding-class app with mood == Coding is the work pool, so this still
	// asserts the honest join ADR 0009 intended — that the line claims
	// working IN VS Code — and activity_line_test.go's matrix owns the
	// per-type rules.
	wantOneOf := renderPool(activity.AppTypeCoding, engine.MoodCoding, "VS Code")
	if !slices.Contains(wantOneOf, s.ActivityLine) {
		t.Errorf("ActivityLine = %q, want one of %q", s.ActivityLine, wantOneOf)
	}
	if s.ActiveState != "coding" {
		t.Errorf("ActiveState = %q, want %q", s.ActiveState, "coding")
	}
	if len(s.ScreenLines) != 11 {
		t.Errorf("len(ScreenLines) = %d, want 11", len(s.ScreenLines))
	}
	if len(s.TickerLines) != 3 {
		t.Errorf("len(TickerLines) = %d, want 3", len(s.TickerLines))
	}
	if len(s.Equipped) != 8 {
		t.Errorf("len(Equipped) = %d, want 8 (every slot, always)", len(s.Equipped))
	}
}

func TestOnBreakScreenLinesEndWithIdleSentinelWithoutMutatingHistory(t *testing.T) {
	g := New()
	g.Mood = engine.MoodCoding
	g.AdvanceTerminal()
	before := g.State().ScreenLines

	g.Mood = engine.MoodOnBreak
	s := g.State()
	if s.ScreenLines[len(s.ScreenLines)-1] != terminalIdleSentinel {
		t.Errorf("last screen line = %q, want the idle sentinel", s.ScreenLines[len(s.ScreenLines)-1])
	}
	// Recovering to coding must resume exactly where the buffer was — the
	// sentinel must not have overwritten the stored history.
	g.Mood = engine.MoodCoding
	after := g.State().ScreenLines
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("stored terminal history changed at index %d: %q -> %q (onBreak's sentinel must be an overlay, not a mutation)", i, before[i], after[i])
		}
	}
}

func TestTickerLinesNeverEqualActivityLine(t *testing.T) {
	real := map[string]bool{"Coding": true, "Working...": true, "In the terminal": true}
	for _, pool := range tickerPools {
		for _, line := range pool {
			if real[line] {
				t.Errorf("ticker line %q collides with a possible real activity line", line)
			}
		}
	}
}

func TestLevelForXPMatchesTheCurve(t *testing.T) {
	cases := []struct {
		xp   uint64
		want int
	}{
		{0, 1}, {99, 1}, {100, 2}, {299, 2}, {300, 3}, {599, 3}, {600, 4},
		{999, 4}, {1000, 5}, {1240, 5}, {1499, 5}, {1500, 6},
	}
	for _, c := range cases {
		if got := levelForXP(c.xp); got != c.want {
			t.Errorf("levelForXP(%d) = %d, want %d", c.xp, got, c.want)
		}
	}
}
