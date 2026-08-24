// health.go — GET /api/health's response shape and handler
// (healthResponse, healthHandler, aggregateSource) plus buildVersion, the
// VCS-revision reader that fills its Commit field.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"runtime/debug"
)

// healthResponse is GET /api/health's body: a small, stable, machine-
// readable summary of the things known to silently misbehave (which static
// trees are in play, and whether the frontend is really there) plus two
// build identifiers, so a bug report or an automated check can distinguish
// "the server is fine, the browser lost the socket" from "the server itself
// never found its own files".
//
// Source/PublicSource/AssetsSource are EMBED-1 additions; every field that
// existed before it kept its name and meaning. Commit is PR-2
// (MIGRATION_PLAN.md §PR-2): Version used to BE buildVersion()'s output
// (the git revision, via debug.ReadBuildInfo) — that value now lives in
// Commit, unchanged, and Version becomes the ldflags-stamped semver-or-"dev"
// string (see version.go), because buildVersion() alone cannot report
// anything once a release binary is extracted from its archive with no
// .git directory nearby to have been built "at" in the first place.
type healthResponse struct {
	AssetsDir *string `json:"assetsDir"` // the disk directory /assets/ is served from; null when it is served from the embedded copy
	PublicOk  bool    `json:"publicOk"`  // true iff the serving frontend tree holds index.html
	Version   string  `json:"version"`   // ldflags-stamped semver, or "dev" for a plain `go build`/`go run .` — see version.go
	Commit    string  `json:"commit"`    // buildVersion()'s output: the VCS revision (plus "-dirty"), or "unknown"
	// Source is the aggregate: "embedded" when this binary is serving
	// only itself (the shipped configuration), "disk" when both trees are
	// overridden, "mixed" when one of each.
	Source       string `json:"source"`
	PublicSource string `json:"publicSource"` // "embedded" | "disk"
	AssetsSource string `json:"assetsSource"` // "embedded" | "disk"
}

// healthHandler serves the fixed healthResponse computed once at startup
// (every field is decided during startup and never changes for the life of
// the process) as JSON.
func healthHandler(assetsDir *string, publicOk bool, version, commit, publicSource, assetsSource string) http.HandlerFunc {
	body, err := json.Marshal(healthResponse{
		AssetsDir:    assetsDir,
		PublicOk:     publicOk,
		Version:      version,
		Commit:       commit,
		Source:       aggregateSource(publicSource, assetsSource),
		PublicSource: publicSource,
		AssetsSource: assetsSource,
	})
	if err != nil {
		log.Fatalf("marshal health response: %v", err) // unreachable: healthResponse always marshals
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

// aggregateSource collapses the two per-tree labels into /api/health's
// headline "source": the two agree in both configurations anyone ships or
// debugs end-to-end, and disagree only in a partial dev override.
func aggregateSource(publicSource, assetsSource string) string {
	if publicSource == assetsSource {
		return publicSource
	}
	return sourceMixed
}

// buildVersion returns the VCS revision this binary was built from (plus a
// "-dirty" suffix if the working tree had uncommitted changes at build
// time), via the module build info Go embeds automatically — no ldflags or
// separate VERSION file required. "unknown" if that information isn't
// available (e.g. a binary built with GOFLAGS=-buildvcs=false).
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	rev, dirty := "unknown", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}
