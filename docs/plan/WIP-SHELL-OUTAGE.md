# WIP note — session shell outage (2026-08-25)

The Claude Code session's Bash tool died environment-wide (~10:5x local): every
command exits 1 with no stdout/stderr — including `true` — sandbox on or off,
in the main session and in subagents. File tools (Read/Write/Edit) still work.

## Status update 11:09: item 1 LANDED (gated green); outage resolved (owner cleaned /tmp)

## Original notes
1. **Linux provider hotplug fix** (field bug: GNOME lock re-enumerated
   /dev/input, provider held dead fds 19h48m, idle accrued falsely):
   - `app/internal/activity/provider_linux.go` (rewritten: fstat-keyed device
     identity, 15s rescan + death-triggered rescan with per-node backoff,
     len(devices)==0 → HonestyBlind → idle frozen, recovery resets
     lastAnyInput so blind time is never back-charged; blind Snapshot keeps
     the monotonic KeystrokeCount)
   - `app/internal/activity/provider_linux_{scripted,hotplug,engine}_test.go` (new)
   - **NEVER COMPILED** — the author agent's shell was already dead. Next
     session MUST: `gofmt -l`, `go build ./...`, `go vet ./...`,
     `bash scripts/test-race.sh`, expect small compile fixes, then the live
     30s startup check, then commit.
2. A resilience bug-hunt agent (suspend/clock/long-uptime family →
   docs/plan/BUGS-RESILIENCE.md) was in flight — its report may or may not
   have landed; check the tree.

## Field state
- The owner's live dexel was restarted (fresh pid) and tracks again — the
  system-level symptom is resolved; the CODE fix is item 1 above.
- Everything through commit 9cb1ddc is pushed and green; nothing broken landed.

## Outage ROOT CAUSE (identified by agent probes): EDQUOT on /tmp
The session scratchpad filled /tmp (test binaries, temp HOMEs, screenshots,
a fetched pwsh). The harness captures command output under /tmp → a 2-byte
write fails → every Bash call exits 1 with no output. FIX from any terminal:
`rm -rf /tmp/claude-1000/-home-darkmirror-repo-jawwadzafar-dev-companion/*/scratchpad/*`
then the session's shell works again immediately.

## ALSO LANDED (uncommitted): docs/plan/BUGS-RESILIENCE.md
9 REAL bugs (R1-R9) from the suspend/clock/long-uptime hunt — headline: session
auto-end/16h-cap can never fire across laptop sleep (monotonic clock freezes);
focus runs survive suspend and pay unearned bonuses (Engine.Reset exists,
nothing calls it on resume); game-side idle still accrues while the provider is
blind (the analytics half of the field bug — the provider fix alone doesn't
cover it); log rotation never runs under systemd/launchd supervision; ephemeral
port + supervisor restart = permanently dead windows; a backward date step
corrupts history/streak; the active+idle+paused==uptime invariant is false
across suspend (needs an owner call: 4th bucket vs "awake" wording). Fix waves
should follow the report's per-bug fix sketches + file ownership.

Delete this file once the provider fix + the R-bug fix waves are landed.
