package game

import (
	"errors"
	"testing"

	"github.com/jawwadzafar/dev-companion/app/internal/engine"
)

// TestCatalogIntegrity guards the transcribed table itself against the
// invariants docs/upgrade-design.md states explicitly: 8 slots, 4 items
// each (32 total), every slot has exactly one free (price-0) tier-0 item,
// exactly the 3 "nothing" items have no sprite, exactly hoodie+chair items
// carry a defaultTint, and every id/slot/tint reference resolves.
func TestCatalogIntegrity(t *testing.T) {
	slots := DefaultSlots()
	if len(slots) != 8 {
		t.Fatalf("got %d slots, want 8", len(slots))
	}
	slotByID := SlotsByID(slots)
	tints := DefaultTints()
	if len(tints) != 6 {
		t.Fatalf("got %d tints, want 6", len(tints))
	}
	tintByID := TintsByID(tints)

	items := DefaultCatalog()
	if len(items) != 32 {
		t.Fatalf("got %d items, want 32", len(items))
	}

	seenIDs := map[string]bool{}
	perSlot := map[string]int{}
	freeCountPerSlot := map[string]int{}
	noSpriteCount := 0
	defaultTintCount := 0

	for _, it := range items {
		if it.ID == "" {
			t.Fatalf("item %+v has empty ID", it)
		}
		if seenIDs[it.ID] {
			t.Errorf("duplicate item id %q", it.ID)
		}
		seenIDs[it.ID] = true

		slot, ok := slotByID[it.Slot]
		if !ok {
			t.Errorf("item %q references unknown slot %q", it.ID, it.Slot)
			continue
		}
		perSlot[it.Slot]++
		if it.Price == 0 {
			freeCountPerSlot[it.Slot]++
		}

		if it.Sprite == nil {
			noSpriteCount++
			if it.Detail != nil || it.Thumb != nil {
				t.Errorf("item %q has no sprite but a non-nil detail/thumb", it.ID)
			}
		}

		if slot.Tintable {
			if it.DefaultTint == nil {
				t.Errorf("tintable slot %q item %q has no DefaultTint", it.Slot, it.ID)
			} else {
				defaultTintCount++
				if _, ok := tintByID[*it.DefaultTint]; !ok {
					t.Errorf("item %q defaultTint %q is not a known tint", it.ID, *it.DefaultTint)
				}
			}
		} else if it.DefaultTint != nil {
			t.Errorf("non-tintable slot %q item %q unexpectedly has a DefaultTint", it.Slot, it.ID)
		}
	}

	for _, slot := range slots {
		if perSlot[slot.ID] != 4 {
			t.Errorf("slot %q has %d items, want 4", slot.ID, perSlot[slot.ID])
		}
		if freeCountPerSlot[slot.ID] != 1 {
			t.Errorf("slot %q has %d free (price 0) items, want exactly 1", slot.ID, freeCountPerSlot[slot.ID])
		}
	}
	if noSpriteCount != 3 {
		t.Errorf("got %d no-sprite items, want exactly 3 (plant_none, wall_bare, buddy_none)", noSpriteCount)
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
	if defaultTintCount != 8 {
		t.Errorf("got %d items with a defaultTint, want 8 (4 hoodie + 4 chair)", defaultTintCount)
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
		if slot.Tintable && ref.TintID == nil {
			t.Errorf("tintable slot %q equipped with a nil tint", slot.ID)
		}
		if !slot.Tintable && ref.TintID != nil {
			t.Errorf("non-tintable slot %q equipped with a non-nil tint", slot.ID)
		}
	}
}

func TestBuyItemSpendsAndOwns(t *testing.T) {
	g := New()
	g.DevCash = 1000
	if err := g.BuyItem("chair_racer"); err != nil {
		t.Fatalf("BuyItem: %v", err)
	}
	if !g.OwnedItems["chair_racer"] {
		t.Error("chair_racer not marked owned after purchase")
	}
	if g.DevCash != 900 {
		t.Errorf("DevCash = %d, want 900", g.DevCash)
	}
	if err := g.BuyItem("chair_racer"); !errors.Is(err, ErrAlreadyOwned) {
		t.Errorf("BuyItem again: got %v, want ErrAlreadyOwned", err)
	}
}

func TestBuyItemRejectsInsufficientFundsAndUnknownID(t *testing.T) {
	g := New()
	if err := g.BuyItem("chair_racer"); !errors.Is(err, ErrInsufficientFunds) {
		t.Errorf("got %v, want ErrInsufficientFunds", err)
	}
	if g.OwnedItems["chair_racer"] {
		t.Error("item should not be owned after a failed purchase")
	}
	if err := g.BuyItem("does_not_exist"); !errors.Is(err, ErrUnknownItem) {
		t.Errorf("got %v, want ErrUnknownItem", err)
	}
}

func TestDefaultTintIsFreeOnOwnershipButExtraTintsCostMoney(t *testing.T) {
	g := New()
	g.DevCash = 1000
	if err := g.BuyItem("chair_racer"); err != nil {
		t.Fatalf("BuyItem: %v", err)
	}
	if !g.IsTintOwned("chair_racer", "ember") {
		t.Error("chair_racer's default tint (ember) should be owned for free the moment the item is")
	}
	if g.IsTintOwned("chair_racer", "neon") {
		t.Error("a non-default tint should not be owned before BuyTint")
	}
	before := g.DevCash
	if err := g.BuyTint("chair_racer", "neon"); err != nil {
		t.Fatalf("BuyTint: %v", err)
	}
	if g.DevCash != before-40 {
		t.Errorf("DevCash = %d, want %d (tint costs 40)", g.DevCash, before-40)
	}
	if !g.IsTintOwned("chair_racer", "neon") {
		t.Error("neon should be owned after BuyTint")
	}
	if err := g.BuyTint("chair_racer", "neon"); !errors.Is(err, ErrAlreadyOwned) {
		t.Errorf("buying the same tint twice: got %v, want ErrAlreadyOwned", err)
	}
	if err := g.BuyTint("chair_racer", "ember"); !errors.Is(err, ErrAlreadyOwned) {
		t.Errorf("buying the already-free default tint: got %v, want ErrAlreadyOwned", err)
	}
}

func TestBuyTintRejectsUnownedItemAndNonTintableSlot(t *testing.T) {
	g := New()
	g.DevCash = 1000
	if err := g.BuyTint("chair_racer", "neon"); !errors.Is(err, ErrNotOwned) {
		t.Errorf("got %v, want ErrNotOwned", err)
	}
	if err := g.BuyTint("kb_mech", "neon"); !errors.Is(err, ErrNotTintable) {
		t.Errorf("got %v, want ErrNotTintable", err)
	}
}

func TestEquipRequiresOwnershipAndCorrectSlotAndTint(t *testing.T) {
	g := New()
	g.DevCash = 1000

	if err := g.EquipItem("chair", "chair_racer", strp("ember")); !errors.Is(err, ErrNotOwned) {
		t.Errorf("equipping unowned item: got %v, want ErrNotOwned", err)
	}
	if err := g.BuyItem("chair_racer"); err != nil {
		t.Fatalf("BuyItem: %v", err)
	}
	if err := g.EquipItem("hoodie", "chair_racer", strp("ember")); !errors.Is(err, ErrSlotMismatch) {
		t.Errorf("equipping into the wrong slot: got %v, want ErrSlotMismatch", err)
	}
	if err := g.EquipItem("chair", "chair_racer", strp("neon")); !errors.Is(err, ErrNotOwned) {
		t.Errorf("equipping an unowned tint: got %v, want ErrNotOwned", err)
	}
	if err := g.EquipItem("chair", "chair_racer", nil); !errors.Is(err, ErrUnknownTint) {
		t.Errorf("equipping a tintable slot with no tint: got %v, want ErrUnknownTint", err)
	}
	if err := g.EquipItem("chair", "chair_racer", strp("ember")); err != nil {
		t.Fatalf("EquipItem with the default (free) tint: %v", err)
	}
	if g.Equipped["chair"].ItemID != "chair_racer" || *g.Equipped["chair"].TintID != "ember" {
		t.Errorf("Equipped[chair] = %+v, want chair_racer/ember", g.Equipped["chair"])
	}
}

func TestEquipOneWinsPerSlot(t *testing.T) {
	g := New()
	g.DevCash = 1000
	_ = g.BuyItem("hoodie_zip")
	_ = g.EquipItem("hoodie", "hoodie_zip", strp("slate"))
	if g.Equipped["hoodie"].ItemID != "hoodie_zip" {
		t.Fatalf("Equipped[hoodie] = %+v, want hoodie_zip", g.Equipped["hoodie"])
	}
	if !g.OwnedItems["hoodie_classic"] {
		t.Error("equipping hoodie_zip should not un-own the tier-0 hoodie_classic")
	}
	// Switch back — own-many, equip-one: nothing was destroyed.
	_ = g.EquipItem("hoodie", "hoodie_classic", strp("indigo"))
	if g.Equipped["hoodie"].ItemID != "hoodie_classic" {
		t.Fatalf("Equipped[hoodie] = %+v, want hoodie_classic", g.Equipped["hoodie"])
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
	g.OpenStore()

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
	g.CloseStore()
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, WorkUnits: 0.5})
	if g.Progress != startProgress+0.5 {
		t.Errorf("Progress after closing = %v, want %v (only the post-close tick's work)", g.Progress, startProgress+0.5)
	}
}

func TestStateActivityLineIsHonestAndScreenLinesAlwaysElevenTickerAlwaysThree(t *testing.T) {
	g := New()
	g.Tick(engine.TickResult{Mood: engine.MoodCoding, ActiveApp: "code", ActiveAppDisplay: "VS Code"})
	s := g.State()
	if s.ActivityLine != "Coding in VS Code" {
		t.Errorf("ActivityLine = %q, want %q", s.ActivityLine, "Coding in VS Code")
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
