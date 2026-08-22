package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmbeddedPublicTreeMatchesDiskExceptSourcemaps is the guard that pays
// for embed.go's explicit pattern list (N-9, docs/plan/
// REVIEW-2026-08-22.md).
//
// publicEmbed used to be one `all:public` directive, which embedded
// app/public/js/dexel.js.map — 230 KB of pure debug artifact, about a
// third of the whole embedded payload — into every shipped binary and
// served it to every user. go:embed has no exclusion syntax, so the map is
// excluded by naming what IS embedded instead. The cost of a list is that
// a NEW file under public/ can be silently left out of the binary while
// still working perfectly in `-public` dev mode: the worst kind of bug,
// invisible until a release.
//
// So: walk the real app/public tree and require every file to be present
// in the embedded copy, except *.map, which must be ABSENT. If someone
// adds public/js/vendor.js, this fails until embed.go names it.
func TestEmbeddedPublicTreeMatchesDiskExceptSourcemaps(t *testing.T) {
	embedded := embeddedPublicFS()

	var onDisk, wantAbsent int
	err := filepath.WalkDir("public", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel("public", path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		_, statErr := fs.Stat(embedded, rel)
		if strings.HasSuffix(rel, ".map") {
			wantAbsent++
			if statErr == nil {
				t.Errorf("%s IS embedded in the binary — sourcemaps must stay on disk only (see embed.go)", rel)
			}
			return nil
		}
		onDisk++
		if statErr != nil {
			t.Errorf("%s exists on disk but is NOT embedded — add it to embed.go's pattern list: %v", rel, statErr)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking app/public: %v", err)
	}
	if onDisk == 0 {
		t.Fatal("found no files under app/public — the walk itself is broken, so this test proves nothing")
	}
	if wantAbsent == 0 {
		t.Log("note: no .map files on disk; the exclusion half of this test is vacuous right now")
	}

	// The other direction: nothing embedded that is not on disk (a stale
	// pattern pointing at a deleted file cannot happen — go:embed fails to
	// compile — but a directory pattern picking up a build artifact can).
	if err := fs.WalkDir(embedded, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".map") {
			t.Errorf("%s is embedded — sourcemaps must be excluded from the binary", path)
		}
		if _, statErr := os.Stat(filepath.Join("public", path)); statErr != nil {
			t.Errorf("%s is embedded but not on disk: %v", path, statErr)
		}
		return nil
	}); err != nil {
		t.Fatalf("walking the embedded public tree: %v", err)
	}

	// index.html must be reachable at the exact name http.FileServer asks
	// for — the one file whose absence turns the whole product into a 404.
	if !fsFileExists(embedded, "index.html") {
		t.Error("index.html is missing from the embedded tree")
	}
}
