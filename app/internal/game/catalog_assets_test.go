package game

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jawwadzafar/dev-companion/app/internal/assets"
)

// TestCatalogSpriteFilesExistOnDisk walks every filename the catalog
// references (Sprite, Detail, Thumb, ThumbForm, ThumbDetail) and asserts
// each one exists under the repository's real assets/ directory. The wire
// contract in this file is transcribed by hand against docs/art-direction.md's
// manifest — this test is what keeps that transcription honest against the
// files tools/gen_assets.py actually produced, rather than against the
// doc comment's claims about them.
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
		check("sprite", it.ID, it.Sprite)
		check("detail", it.ID, it.Detail)
		check("thumb", it.ID, it.Thumb)
		check("thumbForm", it.ID, it.ThumbForm)
		check("thumbDetail", it.ID, it.ThumbDetail)
	}

	// Sanity floor: if this drops to 0 the assets dir was found but the
	// catalog's pointer fields were somehow all nil — a bug in this test,
	// not a passing catalog.
	if checked == 0 {
		t.Fatal("checked 0 catalog file references — test is not exercising anything")
	}
}
