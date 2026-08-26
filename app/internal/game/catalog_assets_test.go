package game

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/assets"
)

// unchangedArtSlots are the slots whose sprites STORE-2.0 did not touch —
// their PNGs already exist under assets/ and this test asserts that on disk.
// The colour-item slots (hoodie/chair) and the new monitor slot get their
// sprites from tools/gen_assets.py in STORE-2.0 stage B; until that lands the
// on-disk check for those files is deferred (see TestCatalogSpriteFilenames
// for the naming-convention guard that covers every item, art or not).
var unchangedArtSlots = map[string]bool{
	"keyboard": true, "mouse": true, "beverage": true,
	"plant": true, "wall": true, "buddy": true,
}

// TestCatalogSpriteFilenames pins the uniform STORE-2.0 filename convention
// for EVERY catalog item (whether or not its PNG exists yet): a real-sprite
// item names "<id>.png" and "thumb_<id>.png"; the three "nothing" items name
// neither; buddy_bot is the one animated exception (frame A). This is the
// contract stage B's generator must satisfy.
func TestCatalogSpriteFilenames(t *testing.T) {
	nothing := map[string]bool{"plant_none": true, "wall_bare": true, "buddy_none": true}
	for _, it := range DefaultCatalog() {
		switch {
		case nothing[it.ID]:
			if it.Sprite != nil || it.Thumb != nil {
				t.Errorf("nothing-item %q must have nil sprite and thumb, got %v/%v", it.ID, it.Sprite, it.Thumb)
			}
		case it.ID == "buddy_bot":
			// 2-frame blink animation: sprite points at frame A.
			if it.Sprite == nil || *it.Sprite != "buddy_bot_a.png" {
				t.Errorf("buddy_bot sprite = %v, want buddy_bot_a.png", it.Sprite)
			}
			if it.Thumb == nil || *it.Thumb != "thumb_buddy_bot.png" {
				t.Errorf("buddy_bot thumb = %v, want thumb_buddy_bot.png", it.Thumb)
			}
		default:
			wantSprite := it.ID + ".png"
			wantThumb := "thumb_" + it.ID + ".png"
			if it.Sprite == nil || *it.Sprite != wantSprite {
				t.Errorf("item %q sprite = %v, want %q", it.ID, it.Sprite, wantSprite)
			}
			if it.Thumb == nil || *it.Thumb != wantThumb {
				t.Errorf("item %q thumb = %v, want %q", it.ID, it.Thumb, wantThumb)
			}
		}
	}
}

// TestCatalogSpriteFilesExistOnDisk asserts that every filename the catalog
// references for an UNCHANGED-art slot (unchangedArtSlots) exists as a real
// file under the repo's assets/ directory — the transcription-honesty guard
// for the art STORE-2.0 left in place. The colour-item and monitor sprites
// are stage B's deliverable and are deliberately not required here yet.
func TestCatalogSpriteFilesExistOnDisk(t *testing.T) {
	dir, err := assets.Locate()
	if err != nil {
		t.Fatalf("could not locate assets/ directory: %v", err)
	}

	checked := 0
	check := func(field, itemID string, file *string) {
		if file == nil {
			return
		}
		checked++
		path := filepath.Join(dir, *file)
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Errorf("item %q: %s %q not found under %s (%v)", itemID, field, *file, dir, statErr)
			return
		}
		if info.IsDir() {
			t.Errorf("item %q: %s %q resolves to a directory under %s, not a file", itemID, field, *file, dir)
		}
	}

	for _, it := range DefaultCatalog() {
		if !unchangedArtSlots[it.Slot] {
			continue
		}
		check("sprite", it.ID, it.Sprite)
		check("thumb", it.ID, it.Thumb)
	}

	// Sanity floor: if this drops to 0 the assets dir was found but no
	// unchanged-art item had a sprite/thumb — a bug in this test, not a
	// passing catalog.
	if checked == 0 {
		t.Fatal("checked 0 catalog file references — test is not exercising anything")
	}
}
