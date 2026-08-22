// EMBED-1 (docs/plan/ROADMAP.md § "EMBED-1"): the product is ONE file.
//
// Both static trees the server hands to a browser are compiled into the
// binary here, so `./dexel` alone — dropped into an empty directory with no
// public/ and no assets/ next to it — serves the complete game:
//
//   - public/  the committed frontend bundle (index.html, css, fonts,
//     js/dexel.js) served at "/"
//   - assets/  the art agent's sprite/thumbnail PNGs (tools/gen_assets.py's
//     output) served at "/assets/"
//
// This is why assets/ moved from the repository root to app/assets/: go:embed
// can only reach files inside the module that declares it (the "//go:embed
// pattern ... is outside the module" rule), and this module's root is app/.
// The generator writes there now; nothing else about the art pipeline
// changed.
//
// Disk overrides still exist, but only as DEV overrides and only when asked
// for EXPLICITLY (-public <dir> for the frontend, $DEXEL_ASSETS_DIR for the
// sprites) — see main.go's resolvePublicSource/resolveAssetsSource. There is
// deliberately no implicit "is there a ./public next to me?" probe: which
// tree is serving must be a decision the operator made, not one the cwd made
// for them. Whichever wins is logged at startup and reported by /api/health
// as "source".
//
// Only the standard library is involved (embed + io/fs + net/http's
// http.FS) — no new module dependency.
package main

import (
	"embed"
	"io/fs"
	"log"
)

// publicEmbed holds app/public/. The `all:` prefix is required, not
// cosmetic: without it go:embed silently skips files whose names begin with
// "." or "_", which would quietly drop a dotfile the frontend adds later.
//
//go:embed all:public
var publicEmbed embed.FS

// assetsEmbed holds app/assets/ — every PNG tools/gen_assets.py writes.
//
//go:embed all:assets
var assetsEmbed embed.FS

// embeddedPublicFS returns the embedded frontend tree rooted so that
// "index.html" (not "public/index.html") is the path http.FileServer sees,
// making it a drop-in substitute for http.Dir(<disk public dir>).
func embeddedPublicFS() fs.FS { return mustSub(publicEmbed, "public") }

// embeddedAssetsFS returns the embedded sprite tree rooted so that
// "room_back.png" is the path served under the /assets/ prefix.
func embeddedAssetsFS() fs.FS { return mustSub(assetsEmbed, "assets") }

// mustSub strips the embed's leading directory component. A failure here is
// impossible in a binary that compiled — the go:embed directives above
// guarantee the subtree exists — so it is fatal rather than degraded: a
// binary that cannot reach its own embedded frontend has nothing to serve.
func mustSub(f embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		log.Fatalf("embedded %s/ subtree unavailable: %v", dir, err)
	}
	return sub
}
