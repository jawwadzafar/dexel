package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestStaticTreesServeFilesOnly is INTERACTION-HARDENING's regression test
// (docs/plan/ROADMAP.md): "The server must NOT serve directory listings
// (/assets/ currently lists all files via http.FileServer). Files only, no
// index pages, both embedded and disk-override modes."
//
// Before filesOnlyFS, `GET /assets/` returned 200 with a generated HTML
// index of every sprite in the product and `GET /js/`, `/css/`, `/fonts/`
// did the same for the frontend — http.FileServer's documented behaviour
// for a directory with no index.html.
//
// The test mounts the two static trees the way runServe does — the SAME
// two calls, including the /assets/ StripPrefix — and asserts BOTH halves
// of the contract, because a fix that only produced 404s would be
// indistinguishable from a broken server: every directory URL 404s, and
// every real file still returns 200 with its bytes intact.
//
// It runs the whole table twice: once against the copy embedded in the
// binary (what a released dexel serves) and once against app/public and
// app/assets on disk (what `-public` / $DEXEL_ASSETS_DIR serve in dev).
// Those are two different fs.FS implementations — embed.FS through fs.Sub
// vs os.DirFS — and the guarantee is about the SERVER, so both have to
// prove it.
func TestStaticTreesServeFilesOnly(t *testing.T) {
	modes := []struct {
		name     string
		publicFS fs.FS
		assetsFS fs.FS
	}{
		{"embedded", embeddedPublicFS(), embeddedAssetsFS()},
		{"disk", os.DirFS("public"), os.DirFS("assets")},
	}

	cases := []struct {
		path string
		want int
		why  string
	}{
		// The four directory URLs a curious user (or a crawler) types.
		{"/assets/", http.StatusNotFound, "the sprite tree's root must not enumerate itself"},
		{"/js/", http.StatusNotFound, "the bundle directory must not enumerate itself"},
		{"/css/", http.StatusNotFound, "the stylesheet directory must not enumerate itself"},
		{"/fonts/", http.StatusNotFound, "the font directory must not enumerate itself"},
		// The same directories WITHOUT the trailing slash. FileServer used
		// to answer these with a 301 to the trailing-slash form, which then
		// listed — so a fix that only looked at the trailing slash would
		// leave the listing one redirect away.
		{"/js", http.StatusNotFound, "a bare directory name must not redirect into a listing"},
		{"/css", http.StatusNotFound, "a bare directory name must not redirect into a listing"},
		// Every real file still works. These are the four content types the
		// frontend actually loads plus one sprite.
		{"/", http.StatusOK, "the frontend root is the one directory that serves an index"},
		{"/js/dexel.js", http.StatusOK, "the bundle must still load"},
		{"/css/game.css", http.StatusOK, "the stylesheet must still load"},
		{"/css/nes.min.css", http.StatusOK, "the vendored stylesheet must still load"},
		{"/assets/room_back.png", http.StatusOK, "sprites must still load"},
		{"/assets/dev_base_idle.png", http.StatusOK, "sprites must still load"},
		// A path that never existed is still an ordinary 404, not a panic.
		{"/nope.txt", http.StatusNotFound, "a missing file is a plain 404"},
		{"/assets/nope.png", http.StatusNotFound, "a missing sprite is a plain 404"},
		// Traversal out of the tree resolves inside it and finds nothing.
		{"/assets/../js/dexel.js", http.StatusOK, "..'s are cleaned before the tree sees them"},
	}

	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			// The exact two mounts runServe registers.
			mux := http.NewServeMux()
			mux.Handle("/", filesOnlyFS(mode.publicFS, true))
			mux.Handle("/assets/", http.StripPrefix("/assets/", filesOnlyFS(mode.assetsFS, false)))
			srv := httptest.NewServer(mux)
			defer srv.Close()

			for _, tc := range cases {
				resp, err := http.Get(srv.URL + tc.path)
				if err != nil {
					t.Fatalf("GET %s: %v", tc.path, err)
				}
				body := make([]byte, 512)
				n, _ := resp.Body.Read(body)
				resp.Body.Close()

				if resp.StatusCode != tc.want {
					t.Errorf("GET %s = %d, want %d (%s)\nbody: %.200q", tc.path, resp.StatusCode, tc.want, tc.why, body[:n])
					continue
				}
				// A 404 must be a real 404 and never FileServer's listing
				// page smuggled under a different status.
				if tc.want == http.StatusNotFound && n > 0 {
					if got := string(body[:n]); len(got) > 0 && got[0] == '<' {
						t.Errorf("GET %s 404'd but returned HTML — that looks like a listing page: %.200q", tc.path, got)
					}
				}
				if tc.want == http.StatusOK && n == 0 {
					t.Errorf("GET %s = 200 but served zero bytes (%s)", tc.path, tc.why)
				}
			}
		})
	}
}

// TestStaticRootIndexIsTheOnlyIndex pins the asymmetry between the two
// mounts, which is the one judgement call in filesOnlyFS: "/" is the
// application and must serve index.html, while "/assets/" is a sprite
// directory whose root is a request for nothing. A refactor that made both
// mounts behave the same way would break one of these two assertions
// whichever way it went.
func TestStaticRootIndexIsTheOnlyIndex(t *testing.T) {
	rec := httptest.NewRecorder()
	filesOnlyFS(embeddedPublicFS(), true).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("frontend mount root = %d, want 200 serving index.html", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" || ct[:9] != "text/html" {
		t.Errorf("frontend mount root Content-Type = %q, want text/html (index.html)", ct)
	}

	// The assets mount after StripPrefix sees "" for a GET /assets/, which
	// is the shape the handler must treat as a root — hence the explicit
	// empty-path request here rather than going through a mux.
	rec2 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/", nil)
	req.URL.Path = ""
	filesOnlyFS(embeddedAssetsFS(), false).ServeHTTP(rec2, req)
	if rec2.Code != http.StatusNotFound {
		t.Errorf("assets mount root = %d, want 404 — it has no index and must not list", rec2.Code)
	}
}
