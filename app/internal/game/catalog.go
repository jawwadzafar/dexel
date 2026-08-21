package game

// Catalog data transcribed verbatim from docs/upgrade-design.md v2 ("Slots
// and items", "Tints") — eight slots, four items each (32 items, 8 free
// tier-0 defaults), six shared tints. This table is the ONLY place this
// content lives; the store UI and the scene both render whatever it says
// (docs/upgrade-design.md principle 5).
//
// Sprite/detail/thumb filenames follow docs/art-direction.md's actual
// per-item asset tables (the ground truth — verified against the files
// tools/gen_assets.py really produces under assets/), which is NOT one
// uniform rule across slots:
//   - chair (tintable, and the ONLY slot whose full-size asset is itself
//     split): sprite = "<id>_form.png" (grayscale, tinted at runtime),
//     detail = "<id>_detail.png" (palette-pure overlay).
//   - hoodie (also tintable, but NOT split at full size): sprite =
//     "hoodie_<style>.png", a single palette-pure style overlay,
//     detail = nil. The actual runtime tint target for hoodie is the
//     SHARED developer body sprite (dev_form_<frame>.png) composited by
//     the frontend independently of this catalog — docs/upgrade-design.md
//     "Scene contract": "The developer is the one composite that is not
//     a slot — the hoodie slot's form + the hoodie style overlay + the
//     frame-driven base layer." This catalog only carries the per-item
//     style overlay; the frontend already owns the developer-composite
//     special case per that doc, independent of any field here.
//   - buddy_bot is a 2-frame blink animation (buddy_bot_a.png/_b.png,
//     "differs from A only in the 2 screen eye pixels" —
//     docs/art-direction.md); sprite points at frame A, the frontend
//     derives frame B by the same _a/_b convention it already uses for
//     dev_form_type_a/b (no field here encodes animation).
//   - every other item with a real sprite: sprite = "<id>.png",
//     detail = nil.
//   - the three explicit "nothing" items (plant_none, wall_bare,
//     buddy_none): sprite/detail/thumb all nil — ui-spec §4.2 renders the
//     word NOTHING for these instead of a thumbnail/preview image.
//
// thumb: "thumb_<id>.png" for every NON-tintable item with a real sprite
// (verified against real generated files); nil for the three "nothing"
// items. For the two tintable slots (hoodie, chair), docs/art-direction.md's
// own thumbnail rule ("For tintable items the thumbnail must be two
// derived files, thumb_<id>_form.png and thumb_<id>_detail.png, so the
// store card's thumbnail can use the same CSS tint recipe") produces TWO
// files per item, which the single `thumb` field cannot represent. Rather
// than leave that convention implicit (the previous version of this file
// shipped `thumb: nil` for these items and made the frontend derive the
// two filenames itself from the item id — a silent, undocumented wire
// contract nobody could see by reading the JSON), those two filenames are
// now explicit wire fields: `thumbForm`/`thumbDetail`, populated for
// hoodie/chair items and nil for every other item (which instead uses
// plain `thumb`). This is the seam this reconciliation pass closes; see
// this agent's handoff report for the exact proposed docs/ui-spec.md §6.1
// wording.

// Slot is one of the eight scene positions from docs/ui-spec.md §4.1's
// category list, in that exact order (the frontend renders slots "in the
// order given" per the catalog message's own field note).
type Slot struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Tintable bool   `json:"tintable"`
}

// Tint is one of the six shared colours (docs/upgrade-design.md "Tints").
type Tint struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Hex   string `json:"hex"`
	Price uint64 `json:"price"`
}

// CatalogItem is one purchasable style within a slot. Sprite/Detail/Thumb/
// DefaultTint are pointers so the wire JSON can emit a real `null` (per
// ui-spec §6.1: "sprite is null for the none items; detail is null for
// untinted slots") rather than an empty string.
type CatalogItem struct {
	ID          string  `json:"id"`
	Slot        string  `json:"slot"`
	Name        string  `json:"name"`
	Price       uint64  `json:"price"`
	Sprite      *string `json:"sprite"`
	Detail      *string `json:"detail"`
	Thumb       *string `json:"thumb"`
	ThumbForm   *string `json:"thumbForm"`
	ThumbDetail *string `json:"thumbDetail"`
	DefaultTint *string `json:"defaultTint"`
	Flavor      string  `json:"flavor"`
}

// CatalogMessage is the exact shape of the `"type":"catalog"` WebSocket
// message (docs/ui-spec.md §6.1) — sent once on connect.
type CatalogMessage struct {
	Type  string        `json:"type"`
	V     int           `json:"v"`
	Slots []Slot        `json:"slots"`
	Tints []Tint        `json:"tints"`
	Items []CatalogItem `json:"items"`
}

func strp(s string) *string { return &s }

// Slots, in the exact order docs/ui-spec.md §4.1's category list gives.
var catalogSlots = []Slot{
	{ID: "hoodie", Name: "Hoodie", Tintable: true},
	{ID: "chair", Name: "Chair", Tintable: true},
	{ID: "keyboard", Name: "Keyboard", Tintable: false},
	{ID: "mouse", Name: "Mouse", Tintable: false},
	{ID: "beverage", Name: "Beverage", Tintable: false},
	{ID: "plant", Name: "Plant", Tintable: false},
	{ID: "wall", Name: "Wall", Tintable: false},
	{ID: "buddy", Name: "Buddy", Tintable: false},
}

// Six shared tints (docs/upgrade-design.md "Tints"); each item's
// DefaultTint is granted free the moment the item is owned, the other five
// cost Price each, per (item, tint) pair.
var catalogTints = []Tint{
	{ID: "slate", Name: "Classic Black", Hex: "#2b2b33", Price: 40},
	{ID: "cobalt", Name: "Cobalt Blue", Hex: "#4a7fa8", Price: 40},
	{ID: "forest", Name: "Forest Green", Hex: "#4e8b4f", Price: 40},
	{ID: "ember", Name: "Cyberpunk Orange", Hex: "#a45c3a", Price: 40},
	{ID: "neon", Name: "Neon Pink", Hex: "#e86aa4", Price: 40},
	{ID: "indigo", Name: "Midnight Indigo", Hex: "#6a5aa0", Price: 40},
}

// tierZeroItemBySlot names the always-owned, always-affordable default item
// for each slot — the item docs/upgrade-design.md guarantees exists so
// `equipped` never contains a null (the three "nothing" slots use an
// explicit no-sprite item; the other five slots have a real free style).
var tierZeroItemBySlot = map[string]string{
	"hoodie":   "hoodie_classic",
	"chair":    "chair_basic",
	"keyboard": "kb_membrane",
	"mouse":    "mouse_stock",
	"beverage": "bev_mug",
	"plant":    "plant_none",
	"wall":     "wall_bare",
	"buddy":    "buddy_none",
}

// catalogItems is the full 32-item table, ordered per slot as given in
// docs/upgrade-design.md ("cheapest first, the free default first of
// all" — naturally true here since price 0 items are listed first).
var catalogItems = []CatalogItem{
	// --- Hoodie (tintable) ---
	{ID: "hoodie_classic", Slot: "hoodie", Name: "Classic Pullover", Price: 0,
		Sprite: strp("hoodie_classic.png"), Detail: nil, Thumb: nil,
		ThumbForm: strp("thumb_hoodie_classic_form.png"), ThumbDetail: strp("thumb_hoodie_classic_detail.png"),
		DefaultTint: strp("indigo"), Flavor: "Drawstrings, one pocket, no opinions."},
	{ID: "hoodie_zip", Slot: "hoodie", Name: "Zip-Up", Price: 120,
		Sprite: strp("hoodie_zip.png"), Detail: nil, Thumb: nil,
		ThumbForm: strp("thumb_hoodie_zip_form.png"), ThumbDetail: strp("thumb_hoodie_zip_detail.png"),
		DefaultTint: strp("slate"), Flavor: "For when the office is exactly two degrees off."},
	{ID: "hoodie_tech", Slot: "hoodie", Name: "Techwear", Price: 300,
		Sprite: strp("hoodie_tech.png"), Detail: nil, Thumb: nil,
		ThumbForm: strp("thumb_hoodie_tech_form.png"), ThumbDetail: strp("thumb_hoodie_tech_detail.png"),
		DefaultTint: strp("forest"), Flavor: "Straps that hold nothing. Reflective, though."},
	{ID: "hoodie_cloak", Slot: "hoodie", Name: "Night Cloak", Price: 500,
		Sprite: strp("hoodie_cloak.png"), Detail: nil, Thumb: nil,
		ThumbForm: strp("thumb_hoodie_cloak_form.png"), ThumbDetail: strp("thumb_hoodie_cloak_detail.png"),
		DefaultTint: strp("neon"), Flavor: "Ships at 3am or not at all."},

	// --- Chair (tintable) ---
	{ID: "chair_basic", Slot: "chair", Name: "Basic Office", Price: 0,
		Sprite: strp("chair_basic_form.png"), Detail: strp("chair_basic_detail.png"), Thumb: nil,
		ThumbForm: strp("thumb_chair_basic_form.png"), ThumbDetail: strp("thumb_chair_basic_detail.png"),
		DefaultTint: strp("slate"), Flavor: `Adjusts in one axis. That axis is "no".`},
	{ID: "chair_racer", Slot: "chair", Name: "Racer", Price: 100,
		Sprite: strp("chair_racer_form.png"), Detail: strp("chair_racer_detail.png"), Thumb: nil,
		ThumbForm: strp("thumb_chair_racer_form.png"), ThumbDetail: strp("thumb_chair_racer_detail.png"),
		DefaultTint: strp("ember"), Flavor: "Bolstered wings. Zero laps completed."},
	{ID: "chair_exec", Slot: "chair", Name: "Executive Leather", Price: 300,
		Sprite: strp("chair_exec_form.png"), Detail: strp("chair_exec_detail.png"), Thumb: nil,
		ThumbForm: strp("thumb_chair_exec_form.png"), ThumbDetail: strp("thumb_chair_exec_detail.png"),
		DefaultTint: strp("ember"), Flavor: "Tufted. Reclines further than the deadline."},
	{ID: "chair_antigrav", Slot: "chair", Name: "Anti-Gravity", Price: 500,
		Sprite: strp("chair_antigrav_form.png"), Detail: strp("chair_antigrav_detail.png"), Thumb: nil,
		ThumbForm: strp("thumb_chair_antigrav_form.png"), ThumbDetail: strp("thumb_chair_antigrav_detail.png"),
		DefaultTint: strp("cobalt"), Flavor: "Floats. Physics pending review."},

	// --- Keyboard ---
	{ID: "kb_membrane", Slot: "keyboard", Name: "Stock Membrane", Price: 0,
		Sprite: strp("kb_membrane.png"), Detail: nil, Thumb: strp("thumb_kb_membrane.png"),
		DefaultTint: nil, Flavor: "Came with the machine. Still here."},
	{ID: "kb_mech", Slot: "keyboard", Name: "Mechanical", Price: 60,
		Sprite: strp("kb_mech.png"), Detail: nil, Thumb: strp("thumb_kb_mech.png"),
		DefaultTint: nil, Flavor: "Audible from the next room. Intentionally."},
	{ID: "kb_split", Slot: "keyboard", Name: "Split Ergo", Price: 180,
		Sprite: strp("kb_split.png"), Detail: nil, Thumb: strp("thumb_kb_split.png"),
		DefaultTint: nil, Flavor: "Two halves, one wrist, endless smugness."},
	{ID: "kb_neon", Slot: "keyboard", Name: "Neon 60%", Price: 300,
		Sprite: strp("kb_neon.png"), Detail: nil, Thumb: strp("thumb_kb_neon.png"),
		DefaultTint: nil, Flavor: "Fewer keys, more colours, same bugs."},

	// --- Mouse ---
	{ID: "mouse_stock", Slot: "mouse", Name: "Stock Mouse", Price: 0,
		Sprite: strp("mouse_stock.png"), Detail: nil, Thumb: strp("thumb_mouse_stock.png"),
		DefaultTint: nil, Flavor: "Two buttons and a wheel. It works."},
	{ID: "mouse_gaming", Slot: "mouse", Name: "Gaming Mouse", Price: 50,
		Sprite: strp("mouse_gaming.png"), Detail: nil, Thumb: strp("thumb_mouse_gaming.png"),
		DefaultTint: nil, Flavor: "Seven buttons. Two are bound."},
	{ID: "mouse_trackball", Slot: "mouse", Name: "Trackball", Price: 150,
		Sprite: strp("mouse_trackball.png"), Detail: nil, Thumb: strp("thumb_mouse_trackball.png"),
		DefaultTint: nil, Flavor: "The wrist thanks you. The cursor does not."},
	{ID: "mouse_vertical", Slot: "mouse", Name: "Vertical Ergo", Price: 220,
		Sprite: strp("mouse_vertical.png"), Detail: nil, Thumb: strp("thumb_mouse_vertical.png"),
		DefaultTint: nil, Flavor: "Held like a handshake with your desk."},

	// --- Beverage ---
	{ID: "bev_mug", Slot: "beverage", Name: "Chipped Mug", Price: 0,
		Sprite: strp("bev_mug.png"), Detail: nil, Thumb: strp("thumb_bev_mug.png"),
		DefaultTint: nil, Flavor: "The chip is load-bearing."},
	{ID: "bev_thermos", Slot: "beverage", Name: "Thermos", Price: 40,
		Sprite: strp("bev_thermos.png"), Detail: nil, Thumb: strp("thumb_bev_thermos.png"),
		DefaultTint: nil, Flavor: "Still hot at 4pm. Suspiciously."},
	{ID: "bev_teacup", Slot: "beverage", Name: "Tea & Saucer", Price: 90,
		Sprite: strp("bev_teacup.png"), Detail: nil, Thumb: strp("thumb_bev_teacup.png"),
		DefaultTint: nil, Flavor: "A saucer. On a developer's desk."},
	{ID: "bev_energy", Slot: "beverage", Name: "Energy Can", Price: 140,
		Sprite: strp("bev_energy.png"), Detail: nil, Thumb: strp("thumb_bev_energy.png"),
		DefaultTint: nil, Flavor: "Tastes like a changelog."},

	// --- Plant ---
	{ID: "plant_none", Slot: "plant", Name: "Bare Desk", Price: 0,
		Sprite: nil, Detail: nil, Thumb: nil,
		DefaultTint: nil, Flavor: "Minimalism, or forgetfulness."},
	{ID: "plant_succulent", Slot: "plant", Name: "Succulent", Price: 50,
		Sprite: strp("plant_succulent.png"), Detail: nil, Thumb: strp("thumb_plant_succulent.png"),
		DefaultTint: nil, Flavor: "Survives neglect. Relatable."},
	{ID: "plant_monstera", Slot: "plant", Name: "Monstera", Price: 140,
		Sprite: strp("plant_monstera.png"), Detail: nil, Thumb: strp("thumb_plant_monstera.png"),
		DefaultTint: nil, Flavor: "Big leaves. Bigger commitment."},
	{ID: "plant_bonsai", Slot: "plant", Name: "Bonsai", Price: 260,
		Sprite: strp("plant_bonsai.png"), Detail: nil, Thumb: strp("thumb_plant_bonsai.png"),
		DefaultTint: nil, Flavor: "Pruned more carefully than the git history."},

	// --- Wall ---
	{ID: "wall_bare", Slot: "wall", Name: "Bare Wall", Price: 0,
		Sprite: nil, Detail: nil, Thumb: nil,
		DefaultTint: nil, Flavor: "Ready for anything."},
	{ID: "wall_poster", Slot: "wall", Name: `"Works On My Machine"`, Price: 80,
		Sprite: strp("wall_poster.png"), Detail: nil, Thumb: strp("thumb_wall_poster.png"),
		DefaultTint: nil, Flavor: "The oldest defence."},
	{ID: "wall_shelf", Slot: "wall", Name: "Shelf: Books & Trophy", Price: 200,
		Sprite: strp("wall_shelf.png"), Detail: nil, Thumb: strp("thumb_wall_shelf.png"),
		DefaultTint: nil, Flavor: "Four books, one trophy, zero pages read."},
	{ID: "wall_neon", Slot: "wall", Name: "Neon Sign", Price: 380,
		Sprite: strp("wall_neon.png"), Detail: nil, Thumb: strp("thumb_wall_neon.png"),
		DefaultTint: nil, Flavor: "Casts a glow on every late commit."},

	// --- Buddy ---
	{ID: "buddy_none", Slot: "buddy", Name: "No Buddy", Price: 0,
		Sprite: nil, Detail: nil, Thumb: nil,
		DefaultTint: nil, Flavor: "Solo run."},
	{ID: "buddy_duck", Slot: "buddy", Name: "Rubber Duck", Price: 60,
		Sprite: strp("buddy_duck.png"), Detail: nil, Thumb: strp("thumb_buddy_duck.png"),
		DefaultTint: nil, Flavor: "Best listener on the team."},
	{ID: "buddy_bot", Slot: "buddy", Name: "Desk Bot", Price: 250,
		// 2-frame blink animation (buddy_bot_a.png/_b.png); sprite points
		// at frame A — see this file's top-of-file doc comment.
		Sprite: strp("buddy_bot_a.png"), Detail: nil, Thumb: strp("thumb_buddy_bot.png"),
		DefaultTint: nil, Flavor: "Blinks. Judges. Blinks again."},
	{ID: "buddy_cat", Slot: "buddy", Name: "Sleeping Cat", Price: 300,
		Sprite: strp("buddy_cat.png"), Detail: nil, Thumb: strp("thumb_buddy_cat.png"),
		DefaultTint: nil, Flavor: "Has opinions about the keyboard. Asleep."},
}

// DefaultCatalog returns the full, static item table.
func DefaultCatalog() []CatalogItem { return catalogItems }

// DefaultSlots returns the eight slots, in category-list order.
func DefaultSlots() []Slot { return catalogSlots }

// DefaultTints returns the six shared tints.
func DefaultTints() []Tint { return catalogTints }

// ByID indexes the catalog for O(1) lookups.
func ByID(catalog []CatalogItem) map[string]CatalogItem {
	m := make(map[string]CatalogItem, len(catalog))
	for _, it := range catalog {
		m[it.ID] = it
	}
	return m
}

// TintsByID indexes the tint table.
func TintsByID(tints []Tint) map[string]Tint {
	m := make(map[string]Tint, len(tints))
	for _, t := range tints {
		m[t.ID] = t
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
		Tints: DefaultTints(),
		Items: DefaultCatalog(),
	}
}
