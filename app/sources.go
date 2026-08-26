// sources.go — EMBED-1's static-tree source selection: which copy of the
// frontend (resolvePublicSource) and the sprites (resolveAssetsSource) a
// run serves, embedded vs. an explicit disk override, plus the activity
// provider selection (selectProvider) runServe wires up alongside them.
package main

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/jawwadzafar/dexel/app/internal/activity"
	"github.com/jawwadzafar/dexel/app/internal/assets"
)

// selectProvider builds the activity.Provider for this run. -fake-script
// ((or DEXEL_FAKE_SCRIPT)) always wins so a scripted demo/test run is
// never accidentally overridden by a real capture path; otherwise "auto"
// picks this OS's native provider (see provider_select_*.go) and "fake"
// uses the env-driven fake provider.
func selectProvider(kind, fakeScript string) (activity.Provider, string) {
	if fakeScript != "" {
		steps, err := activity.ParseFakeScript(fakeScript)
		if err != nil {
			log.Printf("bad -fake-script %q: %v; falling back to the default demo script", fakeScript, err)
			steps = activity.DefaultFakeScript()
		}
		return maybeAppBlind(activity.NewFakeProvider(steps, activity.HonestyGlobal)), "fake (explicit -fake-script)"
	}
	switch kind {
	case "fake":
		return maybeAppBlind(activity.NewFakeProviderFromEnv()), "fake (DEXEL_FAKE_SCRIPT or built-in demo)"
	case "auto":
		return platformProvider(), platformProviderName
	default:
		log.Fatalf(`unknown -provider %q (want "auto" or "fake")`, kind)
		return nil, ""
	}
}

// maybeAppBlind lets a fake-provider run model a globally-honest input
// source that nonetheless CANNOT name the foreground app — the capability
// combination real Linux/Wayland hosts are in (ADR 0009) — when
// DEXEL_FAKE_APP_BLIND is set (any non-empty value). It exists so the
// adaptive-stats behaviour (hiding app-derived rows where app identity is
// unobservable) can be exercised end-to-end in the real running app with a
// scripted timeline, alongside the default (app-visible) fake. It only ever
// touches the fake provider — never a native capture path — and is a dev/
// demo/test seam exactly like DEXEL_FAKE_SCRIPT.
func maybeAppBlind(p *activity.FakeProvider) activity.Provider {
	if os.Getenv("DEXEL_FAKE_APP_BLIND") != "" {
		return p.WithActiveApp("", "")
	}
	return p
}

// staticSource labels where one of the two static trees is being served
// from this run. Both values reach the outside world verbatim: they are
// logged at startup and reported by /api/health, so a bug report can say
// "the binary was serving its own embedded copy" or "it was serving my
// working tree" without anyone having to guess from flags.
const (
	sourceEmbedded = "embedded"
	sourceDisk     = "disk"
	// sourceMixed is only ever the AGGREGATE "source" field's value: one
	// tree embedded and the other overridden onto disk, which is a normal
	// dev combination (iterate on sprites, keep the shipped frontend) but
	// never something either individual tree reports about itself.
	sourceMixed = "mixed"
)

// resolvePublicSource decides which frontend tree "/" serves (EMBED-1).
//
// publicDir is -public's value. Empty (the default, and the only thing a
// released binary ever sees) means "the copy embed.go compiled into this
// binary" — that is what makes ./dexel work alone in an empty directory. A
// non-empty value is an explicit DEV override: serve that directory off
// disk, so editing app/public and reloading the page needs no Go rebuild.
//
// The override is deliberately the ONLY way to get disk mode. There is no
// implicit "is there a ./public next to my cwd?" probe, and nothing is ever
// created on disk: the old behaviour (default -public "./public", plus an
// ensurePublicDirExists that manufactured an empty directory when that path
// missed) turned "launched from the wrong cwd" into a silent 200 serving a
// bare file listing. Now the default cannot miss, and a bad override warns
// loudly instead of being papered over.
//
// Returns the tree to serve, its source label, and whether index.html is
// really present (/api/health's publicOk).
func resolvePublicSource(publicDir string) (fsys fs.FS, source string, ok bool) {
	if publicDir == "" {
		embedded := embeddedPublicFS()
		ok = fsFileExists(embedded, "index.html")
		if !ok {
			// Unreachable in a binary built from a tree with a committed
			// bundle; worth saying out loud rather than 404ing mutely.
			log.Print("WARNING: the embedded frontend has no index.html — this binary was built without a frontend bundle")
		}
		log.Print("frontend: serving / from the copy embedded in this binary")
		return embedded, sourceEmbedded, ok
	}

	dir := publicDir
	if abs, err := filepath.Abs(publicDir); err == nil {
		dir = abs
	}
	ok = diskIndexExists(dir)
	log.Printf("frontend: -public %s — serving / from disk (dev override)", dir)
	if !ok {
		log.Printf("WARNING: %s has no index.html — the frontend will not load; check -public, or drop the flag to serve the embedded copy", dir)
	}
	return os.DirFS(dir), sourceDisk, ok
}

// resolveAssetsSource decides which sprite tree "/assets/" serves (EMBED-1),
// the same way resolvePublicSource does for the frontend: embedded by
// default, disk only when $DEXEL_ASSETS_DIR explicitly asks for it.
//
// This replaced a multi-strategy runtime lookup (walk upward from the
// executable, then upward from the cwd, then a candidate derived from
// wherever -public resolved) that existed purely because the sprites lived
// outside the binary and had to be FOUND. Embedded, there is nothing to find
// and nothing to disagree about — which also removes the failure mode that
// lookup was built to diagnose: sprites silently 404ing because the binary
// had been moved away from its checkout.
//
// The override is still trusted verbatim (no search), matching
// assets.EnvOverride's long-standing documented contract, but a value that
// does not look like a real assets directory now warns instead of only
// showing up later as 404s.
//
// Returns the tree to serve, the disk directory for /api/health (nil when
// embedded — there is no path to report), and the source label.
func resolveAssetsSource() (fsys fs.FS, dir *string, source string) {
	override, set := assets.OverrideDir()
	if !set {
		log.Print("assets: serving /assets/ from the copy embedded in this binary")
		return embeddedAssetsFS(), nil, sourceEmbedded
	}

	resolved := override
	if abs, err := filepath.Abs(override); err == nil {
		resolved = abs
	}
	log.Printf("assets: $%s=%q — serving /assets/ from disk (dev override)", assets.EnvOverride, override)
	if !assets.HasSentinel(resolved) {
		log.Printf("WARNING: %s has no %s — sprite requests will 404; unset $%s to serve the embedded copy", resolved, assets.SentinelFile, assets.EnvOverride)
	}
	return os.DirFS(resolved), &resolved, sourceDisk
}

// fsFileExists reports whether name is a regular file in fsys — the io/fs
// equivalent of diskIndexExists, used to sanity-check the embedded tree.
func fsFileExists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

// diskIndexExists reports whether dir actually holds the frontend's
// index.html, as opposed to being an empty or wrong directory that
// http.FileServer would otherwise serve as a silent-but-200 bare listing.
func diskIndexExists(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil && !info.IsDir()
}
