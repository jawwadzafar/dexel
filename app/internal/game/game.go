// Package game holds the deterministic, persistable game state: mood +
// activity display, the in-progress sprint, Dev Cash/XP, and the owned/
// equipped store items and tints. It is pure — no file I/O — so
// persistence (internal/store) and the server both wrap it without the
// package needing to know about either. Field names and JSON shapes here
// implement docs/ui-spec.md's WebSocket contract and
// docs/upgrade-design.md's model verbatim.
package game

import (
	"errors"
	"fmt"
	"sort"

	"github.com/jawwadzafar/dev-companion/app/internal/engine"
)

// EquippedRef is one slot's equipped item + tint (docs/ui-spec.md §6.1:
// "equipped has an entry for every slot, always... empty is expressed by
// the slot's *_none item"). TintID is nil for a non-tintable slot's item.
type EquippedRef struct {
	ItemID string  `json:"itemId"`
	TintID *string `json:"tintId"`
}

// SprintView is the `sprint` object inside a `state` message.
type SprintView struct {
	Index     int     `json:"index"`
	Name      string  `json:"name"`
	Progress  float64 `json:"progress"`
	Target    float64 `json:"target"`
	UnitLabel string  `json:"unitLabel"`
}

// StateMessage is the exact `"type":"state"` WebSocket payload
// (docs/ui-spec.md §6.1). Field names ARE the wire contract — changing one
// without updating docs/ui-spec.md breaks the frontend silently.
type StateMessage struct {
	Type         string                 `json:"type"`
	V            int                    `json:"v"`
	ActiveState  string                 `json:"activeState"`
	ActivityLine string                 `json:"activityLine"`
	DevCash      uint64                 `json:"devCash"`
	Level        int                    `json:"level"`
	XP           uint64                 `json:"xp"`
	StoreOpen    bool                   `json:"storeOpen"`
	Sprint       SprintView             `json:"sprint"`
	ScreenLines  []string               `json:"screenLines"`
	TickerLines  []string               `json:"tickerLines"`
	Equipped     map[string]EquippedRef `json:"equipped"`
	OwnedItems   []string               `json:"ownedItems"`
	OwnedTints   []string               `json:"ownedTints"`
}

// Game is the mutable in-memory state one running companion process holds.
// Concurrency: the server drives every mutating call from a single
// goroutine (see main.go) — Game itself does no locking, so the caller
// owns synchronization.
type Game struct {
	itemsByID map[string]CatalogItem
	tintsByID map[string]Tint
	slotsByID map[string]Slot
	slots     []Slot

	Mood             engine.Mood
	ActiveApp        string
	ActiveAppDisplay string

	sprintIndex int
	Progress    float64 // work units into the current sprint

	DevCash uint64
	XP      uint64

	OwnedItems map[string]bool        // item id -> owned
	OwnedTints map[string]bool        // "<itemId>:<tintId>" -> owned (non-default tints only)
	Equipped   map[string]EquippedRef // slot -> {itemId, tintId}, always populated

	// openStoreConns is the set of connection ids currently holding the
	// work gate open (docs/ui-spec.md §5.3). Refcounted by connID rather
	// than a single global bool so that ANY ONE client's STORE_CLOSE or
	// disconnect only releases ITS OWN hold — it can never release a gate
	// a different, still-connected client is holding, and a client that
	// reconnects mid-session (its earlier connID gone, replaced by a new
	// one) never leaves a stale entry that keeps the gate wedged open
	// either, because the old connID's entry is removed on disconnect
	// before the new one ever opens its own. See StoreOpen (the read) and
	// OpenStore/CloseStore (the writes) below.
	openStoreConns map[uint64]bool

	// ImportedFromRust/ImportedAt are set once, by the legacy-import path
	// (internal/store), and carried forward on every subsequent save —
	// Game itself never sets these except when store.Apply restores them.
	ImportedFromRust bool
	ImportedAt       string // RFC3339, "" if never imported

	// Cosmetic, backend-owned scroll state (docs/ui-spec.md §3). Never
	// read by the economy — purely decorative.
	tickerRotation uint64
	tickerLines    []string // cap 3, newest first
	terminalPushes uint64
	terminalLines  []string // cap 11, oldest first, newest last
}

// New constructs a fresh game against the default catalog, with every
// slot's free tier-0 item owned and equipped (docs/upgrade-design.md:
// "every slot has a free tier-0 item").
func New() *Game {
	slots := DefaultSlots()
	g := &Game{
		itemsByID:   ByID(DefaultCatalog()),
		tintsByID:   TintsByID(DefaultTints()),
		slotsByID:   SlotsByID(slots),
		slots:       slots,
		Mood:        engine.MoodIdle,
		OwnedItems:  map[string]bool{},
		OwnedTints:  map[string]bool{},
		Equipped:    map[string]EquippedRef{},
		tickerLines: make([]string, 3),
	}
	g.resetTerminal()
	g.GrantTierZeroDefaults()
	return g
}

// resetTerminal seeds an 11-line blank buffer so ScreenLines is always
// exactly 11 strings, even before the first coding tick.
func (g *Game) resetTerminal() {
	g.terminalLines = make([]string, 11)
}

// GrantTierZeroDefaults owns+equips every slot's free tier-0 item. Called
// on New() and re-asserted by internal/store.Apply after loading a save,
// so a save can never accidentally un-grant a guaranteed default.
func (g *Game) GrantTierZeroDefaults() {
	for _, slot := range g.slots {
		itemID := tierZeroItemBySlot[slot.ID]
		g.OwnedItems[itemID] = true
		if _, equipped := g.Equipped[slot.ID]; !equipped {
			item := g.itemsByID[itemID]
			g.Equipped[slot.ID] = EquippedRef{ItemID: itemID, TintID: cloneTintPtr(item.DefaultTint)}
		}
	}
}

// Catalog/Slots/Tints expose the static tables this game was built with
// (used by internal/store for load validation and legacy import).
func (g *Game) Catalog() []CatalogItem {
	items := make([]CatalogItem, 0, len(g.itemsByID))
	for _, it := range g.itemsByID {
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}
func (g *Game) Slots() []Slot { return g.slots }

// ItemByID/TintByID/SlotByID are read-only catalog lookups for callers
// outside this package (internal/store's load validation and legacy
// import) that need to check an id without duplicating the tables.
func (g *Game) ItemByID(id string) (CatalogItem, bool) { it, ok := g.itemsByID[id]; return it, ok }
func (g *Game) TintByID(id string) (Tint, bool)        { t, ok := g.tintsByID[id]; return t, ok }
func (g *Game) SlotByID(id string) (Slot, bool)        { s, ok := g.slotsByID[id]; return s, ok }

// TierZeroItem returns the guaranteed free default item id for a slot, or
// "" for an unknown slot.
func (g *Game) TierZeroItem(slotID string) string { return tierZeroItemBySlot[slotID] }

// SprintIndex returns the static-list index the current sprint uses.
func (g *Game) SprintIndex() int { return g.sprintIndex }

// RestoreSprint sets the in-progress sprint index/progress directly
// (bypassing Tick's completion logic) — used only by internal/store when
// loading or importing a save. Index is clamped into range and progress
// into [0, target] per docs/upgrade-design.md's load-validation rules, so
// a corrupted or stale save can never panic or overshoot silently.
func (g *Game) RestoreSprint(index int, progress float64) {
	index = clampSprintIndex(index)
	target := sprintAt(index).Target
	if progress < 0 {
		progress = 0
	}
	if progress > target {
		progress = target
	}
	g.sprintIndex = index
	g.Progress = progress
}

// IsTintOwned reports whether tintID is usable on itemID — either it is
// that item's implicit free default, or it was bought explicitly.
func (g *Game) IsTintOwned(itemID, tintID string) bool {
	item, ok := g.itemsByID[itemID]
	if ok && item.DefaultTint != nil && *item.DefaultTint == tintID {
		return true
	}
	return g.OwnedTints[ownedTintKey(itemID, tintID)]
}

func ownedTintKey(itemID, tintID string) string { return itemID + ":" + tintID }

// SetEquipped directly sets a slot's equipped ref, bypassing ownership
// validation — used only by internal/store, which has already validated
// (or corrected) the item/tint per docs/upgrade-design.md's load rules.
func (g *Game) SetEquipped(slot, itemID string, tintID *string) {
	g.Equipped[slot] = EquippedRef{ItemID: itemID, TintID: cloneTintPtr(tintID)}
}

func cloneTintPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// --- Action errors -----------------------------------------------------
//
// Every BUY_ITEM/BUY_TINT/EQUIP_ITEM failure mode docs/ui-spec.md §6.2
// enumerates ("unknown itemId/slot/tintId, an item whose slot does not
// match, buying something already owned, equipping something not owned,
// and affordability") maps to exactly one of these.
var (
	ErrUnknownItem       = errors.New("unknown item id")
	ErrUnknownSlot       = errors.New("unknown slot id")
	ErrUnknownTint       = errors.New("unknown tint id")
	ErrSlotMismatch      = errors.New("item does not belong to that slot")
	ErrAlreadyOwned      = errors.New("already owned")
	ErrNotOwned          = errors.New("not owned")
	ErrNotTintable       = errors.New("slot is not tintable")
	ErrInsufficientFunds = errors.New("insufficient dev cash")
)

// BuyItem spends DevCash to own an item permanently. Buying never
// auto-equips and grants the item's default tint implicitly (it need not
// be added to OwnedTints — see IsTintOwned).
func (g *Game) BuyItem(itemID string) error {
	item, ok := g.itemsByID[itemID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownItem, itemID)
	}
	if g.OwnedItems[itemID] {
		return fmt.Errorf("%w: %s", ErrAlreadyOwned, itemID)
	}
	if g.DevCash < item.Price {
		return fmt.Errorf("%w: have %d, need %d", ErrInsufficientFunds, g.DevCash, item.Price)
	}
	g.DevCash -= item.Price
	g.OwnedItems[itemID] = true
	return nil
}

// BuyTint buys a non-default colour for an already-owned tintable item.
func (g *Game) BuyTint(itemID, tintID string) error {
	item, ok := g.itemsByID[itemID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownItem, itemID)
	}
	slot, ok := g.slotsByID[item.Slot]
	if !ok || !slot.Tintable {
		return fmt.Errorf("%w: %s", ErrNotTintable, item.Slot)
	}
	if !g.OwnedItems[itemID] {
		return fmt.Errorf("%w: %s", ErrNotOwned, itemID)
	}
	tint, ok := g.tintsByID[tintID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownTint, tintID)
	}
	if g.IsTintOwned(itemID, tintID) {
		return fmt.Errorf("%w: %s", ErrAlreadyOwned, ownedTintKey(itemID, tintID))
	}
	if g.DevCash < tint.Price {
		return fmt.Errorf("%w: have %d, need %d", ErrInsufficientFunds, g.DevCash, tint.Price)
	}
	g.DevCash -= tint.Price
	g.OwnedTints[ownedTintKey(itemID, tintID)] = true
	return nil
}

// EquipItem wears an owned item (+ owned tint, for a tintable slot),
// replacing whatever else occupied that slot. "EQUIP_ITEM with a tintId
// the player does not own is rejected — equipping is not a back door
// around BUY_TINT" (docs/ui-spec.md §6.2).
func (g *Game) EquipItem(slot, itemID string, tintID *string) error {
	item, ok := g.itemsByID[itemID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownItem, itemID)
	}
	if item.Slot != slot {
		return fmt.Errorf("%w: item %s belongs to slot %s, not %s", ErrSlotMismatch, itemID, item.Slot, slot)
	}
	if !g.OwnedItems[itemID] {
		return fmt.Errorf("%w: %s", ErrNotOwned, itemID)
	}
	slotDef, ok := g.slotsByID[slot]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownSlot, slot)
	}

	var finalTint *string
	if slotDef.Tintable {
		want := ""
		if tintID != nil {
			want = *tintID
		}
		if want == "" {
			return fmt.Errorf("%w: slot %s requires a tintId", ErrUnknownTint, slot)
		}
		if _, ok := g.tintsByID[want]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownTint, want)
		}
		if !g.IsTintOwned(itemID, want) {
			return fmt.Errorf("%w: %s", ErrNotOwned, ownedTintKey(itemID, want))
		}
		finalTint = &want
	}
	g.Equipped[slot] = EquippedRef{ItemID: itemID, TintID: finalTint}
	return nil
}

// OpenStore/CloseStore implement docs/ui-spec.md §5.3's work gate: while
// the store is open, Tick must accrue no work/Dev Cash and must hold the
// last mood (see Tick's doc comment). Both are keyed by the calling
// connection's id (server.go/handlers.go's connID) rather than acting on a
// single global flag: OpenStore adds connID to the held-open set,
// CloseStore removes it (a no-op if connID never held it, or already
// doesn't — this is what makes both an explicit STORE_CLOSE and a
// disconnect-synthesized one, or a repeat of either, always safe to call).
// StoreOpen reports the actual gate state derived from that set.
func (g *Game) OpenStore(connID uint64) {
	if g.openStoreConns == nil {
		g.openStoreConns = map[uint64]bool{}
	}
	g.openStoreConns[connID] = true
}

func (g *Game) CloseStore(connID uint64) {
	delete(g.openStoreConns, connID)
}

// StoreOpen reports whether ANY connection currently holds the store open
// — the earning gate Tick checks. Reconnecting after a blip, or a second
// client closing its own store, can never flip this false while at least
// one connection still holds it open (see openStoreConns's doc comment).
func (g *Game) StoreOpen() bool {
	return len(g.openStoreConns) > 0
}

// Tick folds one engine.TickResult into game state.
//
// docs/ui-spec.md §5.3 ("shopping must not count as work"): while
// StoreOpen, this returns immediately without touching Mood, ActiveApp,
// Progress, or DevCash — the last honest mood is HELD, exactly ADR 0010's
// "the game cannot know, so it must not claim" reasoning applied to
// shopping instead of an unfocused window. The caller (main.go) must still
// invoke engine.Engine.Tick() every second even while the store is open,
// so the engine's OWN keystroke baseline keeps advancing — otherwise a
// shopping burst of keystrokes would retroactively count as work the
// instant the store closes. That is why this method takes an
// already-computed TickResult rather than owning the decision to sample.
func (g *Game) Tick(r engine.TickResult) (completed bool) {
	if g.StoreOpen() {
		return false
	}
	g.Mood = r.Mood
	g.ActiveApp = r.ActiveApp
	g.ActiveAppDisplay = r.ActiveAppDisplay

	g.Progress += r.WorkUnits
	def := sprintAt(g.sprintIndex)
	if g.Progress >= def.Target {
		overshoot := g.Progress - def.Target
		g.DevCash += def.DevCash
		g.XP += def.XP
		g.sprintIndex = (g.sprintIndex + 1) % len(sprints)
		g.Progress = overshoot
		completed = true
	}
	return completed
}

// AdvanceTerminal pushes one new #terminal line while coding
// (docs/ui-spec.md §3.2: "coding: push a line every 0.35s"). idle/onBreak
// leave the buffer untouched — State() overlays the onBreak sentinel
// without mutating the stored history, so scrolling resumes exactly where
// it left off once the mood recovers.
func (g *Game) AdvanceTerminal() {
	if g.Mood != engine.MoodCoding {
		return
	}
	line := terminalLine(g.sprintIndex, g.terminalPushes)
	g.terminalPushes++
	g.terminalLines = append(g.terminalLines, line)
	if len(g.terminalLines) > 11 {
		g.terminalLines = g.terminalLines[len(g.terminalLines)-11:]
	}
}

// RotateTicker computes the next #ticker line and pushes it to the front
// (docs/ui-spec.md §3.1: "one new line every 2.5s... newest at the top").
// Runs regardless of StoreOpen — ticker content is cosmetic flavour, not
// economy, so there is no honesty reason to freeze it.
func (g *Game) RotateTicker() {
	line := tickerLine(g.Mood, g.sprintIndex, g.tickerRotation)
	g.tickerRotation++
	g.tickerLines = append([]string{line}, g.tickerLines...)
	if len(g.tickerLines) > 3 {
		g.tickerLines = g.tickerLines[:3]
	}
}

func (g *Game) ownedItemsSorted() []string {
	out := make([]string, 0, len(g.OwnedItems))
	for id, owned := range g.OwnedItems {
		if owned {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func (g *Game) ownedTintsSorted() []string {
	out := make([]string, 0, len(g.OwnedTints))
	for key, owned := range g.OwnedTints {
		if owned {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// State builds the outward-facing `"type":"state"` snapshot.
func (g *Game) State() StateMessage {
	equipped := make(map[string]EquippedRef, len(g.Equipped))
	for slot, ref := range g.Equipped {
		equipped[slot] = EquippedRef{ItemID: ref.ItemID, TintID: cloneTintPtr(ref.TintID)}
	}

	def := sprintAt(g.sprintIndex)

	screen := append([]string(nil), g.terminalLines...)
	if g.Mood == engine.MoodOnBreak && len(screen) > 0 {
		screen[len(screen)-1] = terminalIdleSentinel
	}
	ticker := append([]string(nil), g.tickerLines...)

	return StateMessage{
		Type:         "state",
		V:            1,
		ActiveState:  string(g.Mood),
		ActivityLine: ActivityLine(g.Mood, g.ActiveApp, g.ActiveAppDisplay),
		DevCash:      g.DevCash,
		Level:        levelForXP(g.XP),
		XP:           g.XP,
		StoreOpen:    g.StoreOpen(),
		Sprint: SprintView{
			Index:     g.sprintIndex,
			Name:      def.Name,
			Progress:  g.Progress,
			Target:    def.Target,
			UnitLabel: UnitLabel,
		},
		ScreenLines: screen,
		TickerLines: ticker,
		Equipped:    equipped,
		OwnedItems:  g.ownedItemsSorted(),
		OwnedTints:  g.ownedTintsSorted(),
	}
}
