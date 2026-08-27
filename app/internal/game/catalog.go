package game

// Catalog data for STORE-2.0 (docs/plan/ROADMAP.md §STORE-2.0): the runtime
// TINT system is gone. Colours are now ordinary purchasable ITEMS — each
// (style, colour) pair on the two formerly-tintable slots (hoodie, chair) is
// its own catalog id/price/level-gate, and a new "monitor" slot offers a set
// of bezel-colour item skins (the screen rect stays fixed/load-bearing — that
// is stage B's art concern, not this table's). This table is the ONLY place
// this content lives; the store UI and the scene render whatever it says.
//
// Sprite/thumb filenames follow ONE uniform convention now that nothing is
// tinted at runtime and nothing is split into form+detail layers:
//   - every item with a real sprite: sprite = "<id>.png",
//     thumb = "thumb_<id>.png".
//   - buddy_bot is a 2-frame blink animation (buddy_bot_a.png/_b.png,
//     docs/art-direction.md); sprite points at frame A, the frontend derives
//     frame B by the same _a/_b convention it already uses for the developer.
//   - the three explicit "nothing" items (plant_none, wall_bare, buddy_none):
//     sprite/thumb both nil — ui-spec §4.2 renders the word NOTHING for these.
// The colour-item sprites/thumbs for hoodie/chair/monitor are produced by
// tools/gen_assets.py (STORE-2.0 stage B), which renders each colour to a
// FIXED file matching these ids — this table names them ahead of that art.

// Slot is one of the scene positions from docs/ui-spec.md §4.1's category
// list, in that exact order. Nothing is tintable any more (STORE-2.0), so the
// old Tintable flag is gone.
type Slot struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CatalogItem is one purchasable item within a slot. Sprite/Thumb are
// pointers so the wire JSON can emit a real `null` for the "nothing" items
// (plant_none/wall_bare/buddy_none) rather than an empty string.
type CatalogItem struct {
	ID    string `json:"id"`
	Slot  string `json:"slot"`
	Name  string `json:"name"`
	Price uint64 `json:"price"`
	// MinLevel is the minimum player level (levelForXP, see sprint.go)
	// required to BUY this item; 0 (the omitted/zero value) means LV1 =
	// ungated. It gates PURCHASE only, never equipping an already-owned
	// item, and is orthogonal to Price — a locked item is shown greyed
	// with a "LV n" badge even when the player can afford it. The ladder
	// and the crossover tuning (top-tier MinLevels sit ABOVE the level at
	// which the item is merely affordable, so level — not cash — is the
	// binding constraint) live in catalog_test.go.
	MinLevel int     `json:"minLevel"`
	Sprite   *string `json:"sprite"`
	Thumb    *string `json:"thumb"`
	Flavor   string  `json:"flavor"`
}

// CatalogMessage is the exact shape of the `"type":"catalog"` WebSocket
// message (docs/ui-spec.md §6.1) — sent once on connect.
type CatalogMessage struct {
	Type  string        `json:"type"`
	V     int           `json:"v"`
	Slots []Slot        `json:"slots"`
	Items []CatalogItem `json:"items"`
}

func strp(s string) *string { return &s }

// spriteFor/thumbFor build the uniform "<id>.png"/"thumb_<id>.png" filenames.
func spriteFor(id string) *string { return strp(id + ".png") }
func thumbFor(id string) *string  { return strp("thumb_" + id + ".png") }

// Slots, in the exact order docs/ui-spec.md §4.1's category list gives, with
// the new "monitor" slot appended last (STORE-2.0's ninth tab).
var catalogSlots = []Slot{
	{ID: "hoodie", Name: "Hoodie"},
	{ID: "chair", Name: "Chair"},
	{ID: "keyboard", Name: "Keyboard"},
	{ID: "mouse", Name: "Mouse"},
	{ID: "beverage", Name: "Beverage"},
	{ID: "plant", Name: "Plant"},
	{ID: "wall", Name: "Wall"},
	{ID: "buddy", Name: "Buddy"},
	{ID: "monitor", Name: "Monitor"},
}

// tierZeroItemBySlot names the always-owned, always-affordable default item
// for each slot — the item that guarantees `equipped` never contains a null.
// The three "nothing" slots use an explicit no-sprite item; the other six
// have a real free style/colour (hoodie/chair now name a specific free
// COLOUR of their base style, since colours are items).
var tierZeroItemBySlot = map[string]string{
	"hoodie":   "hoodie_classic_indigo",
	"chair":    "chair_basic_slate",
	"keyboard": "kb_membrane",
	"mouse":    "mouse_stock",
	"beverage": "bev_mug",
	"plant":    "plant_none",
	"wall":     "wall_bare",
	"buddy":    "buddy_none",
	"monitor":  "monitor_slate",
}

// colourItem builds one (style, colour) CatalogItem with the uniform sprite/
// thumb convention. name is the full display name ("Indigo Pullover").
func colourItem(id, slot, name string, price uint64, minLevel int, flavor string) CatalogItem {
	return CatalogItem{
		ID: id, Slot: slot, Name: name, Price: price, MinLevel: minLevel,
		Sprite: spriteFor(id), Thumb: thumbFor(id), Flavor: flavor,
	}
}

// catalogItems is the full item table (STORE-2.0). Ordered per slot, and
// within a slot cheapest-first with the free default first of all — the
// store renders slots in this order and each tab shows its slot's cards.
//
// Colour scheme: the two formerly-tintable slots offer four STYLES each, and
// each style offers a curated set of four COLOURS drawn from the old tint
// palette (slate/cobalt/forest/ember/neon/indigo). A style's signature colour
// is priced at the old style price; the other three colours cost the old
// per-tint surcharge (+40) on top, and every colour of a style shares that
// style's MinLevel — so the level ladder still gates by style tier while
// colour is a pure cash choice. The free tier-0 default of the slot is the
// base style's signature colour (preserving the pre-STORE-2.0 default look).
var catalogItems = []CatalogItem{
	// --- Hoodie (classic 0/LV1, zip 120/LV2, tech 300/LV3, cloak 500/LV6) ---
	colourItem("hoodie_classic_indigo", "hoodie", "Indigo Pullover", 0, 0, "Drawstrings, one pocket, no opinions."),
	colourItem("hoodie_classic_slate", "hoodie", "Slate Pullover", 40, 0, "Drawstrings, one pocket, no opinions."),
	colourItem("hoodie_classic_cobalt", "hoodie", "Cobalt Pullover", 40, 0, "Drawstrings, one pocket, no opinions."),
	colourItem("hoodie_classic_ember", "hoodie", "Ember Pullover", 40, 0, "Drawstrings, one pocket, no opinions."),

	colourItem("hoodie_zip_slate", "hoodie", "Slate Zip-Up", 120, 2, "For when the office is exactly two degrees off."),
	colourItem("hoodie_zip_cobalt", "hoodie", "Cobalt Zip-Up", 160, 2, "For when the office is exactly two degrees off."),
	colourItem("hoodie_zip_ember", "hoodie", "Ember Zip-Up", 160, 2, "For when the office is exactly two degrees off."),
	colourItem("hoodie_zip_indigo", "hoodie", "Indigo Zip-Up", 160, 2, "For when the office is exactly two degrees off."),

	colourItem("hoodie_tech_forest", "hoodie", "Forest Techwear", 300, 3, "Straps that hold nothing. Reflective, though."),
	colourItem("hoodie_tech_slate", "hoodie", "Slate Techwear", 340, 3, "Straps that hold nothing. Reflective, though."),
	colourItem("hoodie_tech_cobalt", "hoodie", "Cobalt Techwear", 340, 3, "Straps that hold nothing. Reflective, though."),
	colourItem("hoodie_tech_neon", "hoodie", "Neon Techwear", 340, 3, "Straps that hold nothing. Reflective, though."),

	colourItem("hoodie_cloak_neon", "hoodie", "Neon Night Cloak", 500, 6, "Ships at 3am or not at all."),
	colourItem("hoodie_cloak_slate", "hoodie", "Slate Night Cloak", 540, 6, "Ships at 3am or not at all."),
	colourItem("hoodie_cloak_indigo", "hoodie", "Indigo Night Cloak", 540, 6, "Ships at 3am or not at all."),
	colourItem("hoodie_cloak_ember", "hoodie", "Ember Night Cloak", 540, 6, "Ships at 3am or not at all."),

	// --- Chair (basic 0/LV1, racer 100/LV2, exec 300/LV3, antigrav 500/LV6) ---
	colourItem("chair_basic_slate", "chair", "Slate Office Chair", 0, 0, `Adjusts in one axis. That axis is "no".`),
	colourItem("chair_basic_cobalt", "chair", "Cobalt Office Chair", 40, 0, `Adjusts in one axis. That axis is "no".`),
	colourItem("chair_basic_forest", "chair", "Forest Office Chair", 40, 0, `Adjusts in one axis. That axis is "no".`),
	colourItem("chair_basic_ember", "chair", "Ember Office Chair", 40, 0, `Adjusts in one axis. That axis is "no".`),

	colourItem("chair_racer_ember", "chair", "Ember Racer", 100, 2, "Bolstered wings. Zero laps completed."),
	colourItem("chair_racer_slate", "chair", "Slate Racer", 140, 2, "Bolstered wings. Zero laps completed."),
	colourItem("chair_racer_cobalt", "chair", "Cobalt Racer", 140, 2, "Bolstered wings. Zero laps completed."),
	colourItem("chair_racer_neon", "chair", "Neon Racer", 140, 2, "Bolstered wings. Zero laps completed."),

	colourItem("chair_exec_ember", "chair", "Ember Executive", 300, 3, "Tufted. Reclines further than the deadline."),
	colourItem("chair_exec_slate", "chair", "Slate Executive", 340, 3, "Tufted. Reclines further than the deadline."),
	colourItem("chair_exec_cobalt", "chair", "Cobalt Executive", 340, 3, "Tufted. Reclines further than the deadline."),
	colourItem("chair_exec_forest", "chair", "Forest Executive", 340, 3, "Tufted. Reclines further than the deadline."),

	colourItem("chair_antigrav_cobalt", "chair", "Cobalt Anti-Grav", 500, 6, "Floats. Physics pending review."),
	colourItem("chair_antigrav_slate", "chair", "Slate Anti-Grav", 540, 6, "Floats. Physics pending review."),
	colourItem("chair_antigrav_forest", "chair", "Forest Anti-Grav", 540, 6, "Floats. Physics pending review."),
	colourItem("chair_antigrav_neon", "chair", "Neon Anti-Grav", 540, 6, "Floats. Physics pending review."),

	// --- Keyboard (unchanged) ---
	{ID: "kb_membrane", Slot: "keyboard", Name: "Stock Membrane", Price: 0,
		Sprite: strp("kb_membrane.png"), Thumb: strp("thumb_kb_membrane.png"),
		Flavor: "Came with the machine. Still here."},
	{ID: "kb_mech", Slot: "keyboard", Name: "Mechanical", Price: 60, MinLevel: 2,
		Sprite: strp("kb_mech.png"), Thumb: strp("thumb_kb_mech.png"),
		Flavor: "Audible from the next room. Intentionally."},
	{ID: "kb_split", Slot: "keyboard", Name: "Split Ergo", Price: 180, MinLevel: 3,
		Sprite: strp("kb_split.png"), Thumb: strp("thumb_kb_split.png"),
		Flavor: "Two halves, one wrist, endless smugness."},
	{ID: "kb_neon", Slot: "keyboard", Name: "Neon 60%", Price: 300, MinLevel: 5,
		Sprite: strp("kb_neon.png"), Thumb: strp("thumb_kb_neon.png"),
		Flavor: "Fewer keys, more colours, same bugs."},

	// --- Mouse (unchanged) ---
	{ID: "mouse_stock", Slot: "mouse", Name: "Stock Mouse", Price: 0,
		Sprite: strp("mouse_stock.png"), Thumb: strp("thumb_mouse_stock.png"),
		Flavor: "Two buttons and a wheel. It works."},
	{ID: "mouse_gaming", Slot: "mouse", Name: "Gaming Mouse", Price: 50, MinLevel: 2,
		Sprite: strp("mouse_gaming.png"), Thumb: strp("thumb_mouse_gaming.png"),
		Flavor: "Seven buttons. Two are bound."},
	{ID: "mouse_trackball", Slot: "mouse", Name: "Trackball", Price: 150, MinLevel: 3,
		Sprite: strp("mouse_trackball.png"), Thumb: strp("thumb_mouse_trackball.png"),
		Flavor: "The wrist thanks you. The cursor does not."},
	{ID: "mouse_vertical", Slot: "mouse", Name: "Vertical Ergo", Price: 220, MinLevel: 5,
		Sprite: strp("mouse_vertical.png"), Thumb: strp("thumb_mouse_vertical.png"),
		Flavor: "Held like a handshake with your desk."},

	// --- Beverage (unchanged) ---
	{ID: "bev_mug", Slot: "beverage", Name: "Chipped Mug", Price: 0,
		Sprite: strp("bev_mug.png"), Thumb: strp("thumb_bev_mug.png"),
		Flavor: "The chip is load-bearing."},
	{ID: "bev_thermos", Slot: "beverage", Name: "Thermos", Price: 40, MinLevel: 2,
		Sprite: strp("bev_thermos.png"), Thumb: strp("thumb_bev_thermos.png"),
		Flavor: "Still hot at 4pm. Suspiciously."},
	{ID: "bev_teacup", Slot: "beverage", Name: "Tea & Saucer", Price: 90, MinLevel: 3,
		Sprite: strp("bev_teacup.png"), Thumb: strp("thumb_bev_teacup.png"),
		Flavor: "A saucer. On a developer's desk."},
	{ID: "bev_energy", Slot: "beverage", Name: "Energy Can", Price: 140, MinLevel: 4,
		Sprite: strp("bev_energy.png"), Thumb: strp("thumb_bev_energy.png"),
		Flavor: "Tastes like a changelog."},

	// --- Plant (unchanged) ---
	{ID: "plant_none", Slot: "plant", Name: "Bare Desk", Price: 0,
		Sprite: nil, Thumb: nil, Flavor: "Minimalism, or forgetfulness."},
	{ID: "plant_succulent", Slot: "plant", Name: "Succulent", Price: 50, MinLevel: 2,
		Sprite: strp("plant_succulent.png"), Thumb: strp("thumb_plant_succulent.png"),
		Flavor: "Survives neglect. Relatable."},
	{ID: "plant_monstera", Slot: "plant", Name: "Monstera", Price: 140, MinLevel: 3,
		Sprite: strp("plant_monstera.png"), Thumb: strp("thumb_plant_monstera.png"),
		Flavor: "Big leaves. Bigger commitment."},
	{ID: "plant_bonsai", Slot: "plant", Name: "Bonsai", Price: 260, MinLevel: 5,
		Sprite: strp("plant_bonsai.png"), Thumb: strp("thumb_plant_bonsai.png"),
		Flavor: "Pruned more carefully than the git history."},

	// --- Wall (unchanged) ---
	{ID: "wall_bare", Slot: "wall", Name: "Bare Wall", Price: 0,
		Sprite: nil, Thumb: nil, Flavor: "Ready for anything."},
	{ID: "wall_poster", Slot: "wall", Name: `"Works On My Machine"`, Price: 80, MinLevel: 2,
		Sprite: strp("wall_poster.png"), Thumb: strp("thumb_wall_poster.png"),
		Flavor: "The oldest defence."},
	{ID: "wall_shelf", Slot: "wall", Name: "Shelf: Books & Trophy", Price: 200, MinLevel: 3,
		Sprite: strp("wall_shelf.png"), Thumb: strp("thumb_wall_shelf.png"),
		Flavor: "Four books, one trophy, zero pages read."},
	{ID: "wall_neon", Slot: "wall", Name: "Neon Sign", Price: 380, MinLevel: 6,
		Sprite: strp("wall_neon.png"), Thumb: strp("thumb_wall_neon.png"),
		Flavor: "Casts a glow on every late commit."},

	// --- Buddy (unchanged) ---
	{ID: "buddy_none", Slot: "buddy", Name: "No Buddy", Price: 0,
		Sprite: nil, Thumb: nil, Flavor: "Solo run."},
	{ID: "buddy_duck", Slot: "buddy", Name: "Rubber Duck", Price: 60, MinLevel: 2,
		Sprite: strp("buddy_duck.png"), Thumb: strp("thumb_buddy_duck.png"),
		Flavor: "Best listener on the team."},
	{ID: "buddy_bot", Slot: "buddy", Name: "Desk Bot", Price: 250, MinLevel: 4,
		// 2-frame blink animation (buddy_bot_a.png/_b.png); sprite points
		// at frame A — see this file's top-of-file doc comment.
		Sprite: strp("buddy_bot_a.png"), Thumb: strp("thumb_buddy_bot.png"),
		Flavor: "Blinks. Judges. Blinks again."},
	{ID: "buddy_cat", Slot: "buddy", Name: "Sleeping Cat", Price: 300, MinLevel: 8,
		Sprite: strp("buddy_cat.png"), Thumb: strp("thumb_buddy_cat.png"),
		Flavor: "Has opinions about the keyboard. Asleep."},

	// --- Monitor (STORE-2.0: bezel-colour item skins; screen rect fixed) ---
	colourItem("monitor_slate", "monitor", "Slate Monitor", 0, 0, "Matte bezel, honest pixels."),
	colourItem("monitor_cobalt", "monitor", "Cobalt Monitor", 80, 2, "A cool frame for warm takes."),
	colourItem("monitor_forest", "monitor", "Forest Monitor", 160, 3, "Evergreen edges, deciduous focus."),
	colourItem("monitor_neon", "monitor", "Neon Monitor", 280, 5, "The bezel glows so the code doesn't have to."),
}

// DefaultCatalog returns the full, static item table.
func DefaultCatalog() []CatalogItem { return catalogItems }

// DefaultSlots returns the slots, in category-list order.
func DefaultSlots() []Slot { return catalogSlots }

// ByID indexes the catalog for O(1) lookups.
func ByID(catalog []CatalogItem) map[string]CatalogItem {
	m := make(map[string]CatalogItem, len(catalog))
	for _, it := range catalog {
		m[it.ID] = it
	}
	return m
}

// SlotsByID indexes the slot table.
func SlotsByID(slots []Slot) map[string]Slot {
	m := make(map[string]Slot, len(slots))
	for _, s := range slots {
		m[s.ID] = s
	}
	return m
}

// NewCatalogMessage builds the `"type":"catalog"` payload from the static
// tables (docs/ui-spec.md §6.1).
func NewCatalogMessage() CatalogMessage {
	return CatalogMessage{
		Type:  "catalog",
		V:     1,
		Slots: DefaultSlots(),
		Items: DefaultCatalog(),
	}
}
