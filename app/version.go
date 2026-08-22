// version.go — dexel's build-time version stamp
// (dev_docs/production-runtime/MIGRATION_PLAN.md §PR-2).
package main

// version is a semver-ish release tag (e.g. "v9.9.9"), set at BUILD time
// via:
//
//	go build -ldflags "-X main.version=$VERSION"
//
// scripts/build-release.sh and scripts/build-sidecar.sh both set it from
// `git describe --tags --always --dirty` run against the SOURCE tree at
// build time. It stays "dev" for a plain `go build`/`go run .`.
//
// This exists because buildVersion() (main.go) — which reads
// runtime/debug.ReadBuildInfo()'s vcs.revision — answers a DIFFERENT
// question: "what commit was this binary's module built from", derived
// from Go's own embedded VCS stamp. That works for a local `go run .`
// but says nothing once a release binary is unpacked from its distributed
// archive on a machine with no .git directory anywhere nearby — which is
// exactly the shape every real install takes (PLATFORM_NOTES.md, the
// installer in PR-8). version is the answer that survives that trip:
// baked into the binary's rodata at build time, not read from a VCS
// directory that travelled with it. /api/health reports BOTH — "version"
// (this) and "commit" (buildVersion(), unchanged) — because they answer
// different questions and neither replaces the other.
var version = "dev"

// versionLine is `dexel version`'s exact stdout, and the string the
// startup log line is built from; /api/health reports the same two
// underlying values (version, buildVersion()) as separate JSON fields
// rather than this formatted line, but all three can never disagree with
// each other about what build this binary is.
func versionLine() string {
	return "dexel " + version + " (commit " + buildVersion() + ")"
}
