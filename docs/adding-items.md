# Adding store items

The store catalog is a single Go table — `app/internal/game/catalog.go`
(`catalogItems`). It is the ONE source of truth: the store UI and the scene both
render whatever it says, and saves store only item-**id strings** + equipped ids.

## No migration, ever (for adding items)
Adding items is backward-compatible with zero save migration:
- A new item just appears in the store; existing saves are unaffected (they
  don't own it yet).
- An owned id that no longer exists degrades gracefully (the slot falls back to
  its tier-0 default in `Apply`).
- A save-**schema** bump is required ONLY when the `SaveData` *struct* changes
  (e.g. removing a field), NOT when appending catalog rows.

## The four cases (easiest → most work)

### 1. A new COLOUR of an existing style — trivial, no art
Colours are tinted at render time from the id (see `colours.ts` +
`app/frontend/src/render/tint.ts`). Just append one catalog row:
```go
colourItem("hoodie_zip_forest", "hoodie", "Forest Zip-Up", 160, 2, "flavor…"),
```
Rebuild the bundle (`cd app/frontend && npm run build`) only if a mapping needs
it (usually not for a colour of a known style). Done.

### 2. A new NON-TINTABLE item (keyboard/mouse/beverage/plant/wall/buddy)
Append a `CatalogItem{ID,Slot,Name,Price,MinLevel,Sprite,Thumb,Flavor}` row,
then author its sprite + thumbnail in `tools/gen_assets.py` (deterministic,
palette-pure, self-checked — see the existing builders). Regenerate assets.

### 3. A new STYLE (e.g. a 5th hoodie/chair)
One `colourItem(...)` row per colour you offer, a new grayscale FORM sprite in
`gen_assets.py` for the style, and register the style→base mapping in
`colours.ts` so the tint machinery knows it.

### 4. A new SLOT — the only "bigger" job
Add the slot to `catalogSlots` + a tier-0 default, art in `gen_assets.py`,
geometry (`geometry.ts` SLOT_RECT/Z), render wiring in `scene.ts`, and the
content-free allow-list. See how "monitor" was added (STORE-2.0) as the model.

## Gating
`MinLevel` on the item (0 = LV1/ungated) gates PURCHASE only. Keep the drip
sane; top-tier items should sit a level or two above where cash alone would
allow (the "bite"), most colours available early. See `level_gate_test.go`.

## Gates before shipping any addition
`gen_assets.py` clean+deterministic; `go build/vet`; `bash scripts/test-race.sh`
(catalog + level-gate + content-free); `tsc` + deterministic bundle; and the
GATE — see it in the real running store (build binary, open store, judge by eye).
