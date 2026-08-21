# 0010 — Mac-first mechanics rescue: global signals without permissions, honest moods

Status: accepted (2026-08-21, after the first real macOS field test)

## Context

The first human run on macOS failed on the mechanics, not the art:
keystrokes weren't captured (the global provider is evdev = Linux-only, so
macOS silently fell back to counting keys typed INTO the game window),
"Coding" showed while merely mousing over the window, and minimizing the
game flipped the mood to "On break" — claiming the user is slacking at the
exact moment they went to work in their editor. The user's earlier Go
prototype (github.com/jawwadzafar/dev_companion) had these mechanics right
on macOS; this ADR ports its load-bearing ideas.

## Decision

1. **macOS global signals via polling `CGEventSourceSecondsSinceLastEventType`
   — zero permissions.** The Go version used a CGEventTap (exact counts, but
   requires the Accessibility prompt). Polling "seconds since last keydown /
   mouse event" from the HID system state needs NO permission, no run loop,
   no Objective-C, and no new crates (a two-line C FFI onto CoreGraphics).
   At a 50ms poll it slightly undercounts >20 keys/s bursts — an
   anti-mashing feature, not a bug. The tap can arrive later as an opt-in
   accuracy upgrade; the trait boundary makes that invisible to the game.

2. **Moods mean what they say (ported from the Go companion state machine):**
   - `Coding` requires a recent KEYSTROKE. Mouse motion alone never shows
     Coding — scrolling docs is not typing.
   - `Idle` = at the desk: recent mouse activity, or shortly after typing.
   - `OnBreak` ONLY from genuine global idleness. When the active source is
     the focused-window fallback (no global signals), an unfocused window
     FREEZES the idle clock and holds `Idle`: the game cannot know, so it
     must not claim. "On break because you minimized me" was a lie.

3. **The store must be discoverable**: a permanent `[Tab] Store` hint in the
   HUD. A feature nothing points to does not exist.

## Consequences
- `GlobalInputProvider::new()` now works on macOS and Linux; only Windows
  gets `UnsupportedPlatform`.
- `ActivityMeter` tracks keystroke recency separately from the blended rate;
  `ActivitySource` exposes `is_global()` so honesty rules can key off the
  source's actual capability.
- Deferred, in order: per-app-class work weighting (the Go engine's
  weights: code/terminal 1.0, meeting 0.35, browser 0.25, music 0),
  macOS frontmost-app for the activity line (NSWorkspace), CGEventTap
  opt-in for exact counts.
- Untestable-here caveat: the macOS backend cannot be compiled or run from
  this Linux box; it ships small, obvious, and behind the same graceful
  degradation as everything else, and the first mac launch is the test.
