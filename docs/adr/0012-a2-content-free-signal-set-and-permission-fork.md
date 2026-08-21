# 0012 — Analytics A2 signal set: permissionless-derivable only, copy/paste behind a permission fork

Status: accepted (2026-08-21, A2 design pass) · Extends ADR 0005, honours ADR 0002/0009/0010

## Context

Phase A2 (ROADMAP.md "Phase A2 (v1.2)") asked for three new content-free
earning signals: (a) copy/paste **chord** count (Cmd/Ctrl+C / +V happened),
(b) app-switch count, (c) focus-session count (a sustained-typing block).
Each must stay a COUNT (ADR 0002/0009), must not regress ADR 0010's
**permissionless** promise on macOS, and must not break ADR 0005's anti-mash
economy (typing > mouse; a bounded ceiling).

The design pass audited the real providers against the platform APIs:

- **macOS is permissionless via `CGEventSourceSecondsSinceLastEventType`**
  (`provider_darwin.go`). That API reports "seconds since the last event of a
  given **type**" — the types we read are keyDown, mouseMoved, mouseDragged,
  scrollWheel. It exposes **no keycode and no modifier flags**. A Cmd+C is a
  keyDown, byte-for-byte indistinguishable from any other keyDown at this
  layer. Distinguishing a copy/paste chord *requires* a `CGEventTap`, which
  *requires* the Accessibility permission — the exact prompt ADR 0010
  deliberately avoids (kept only as a deferred opt-in accuracy upgrade).

- **Linux reads raw evdev** (`provider_linux.go`) and *does* see `code`
  today, but deliberately keeps only "is this a key press below the ceiling"
  — it never retains keycode identity. Detecting Ctrl+C/V there would force
  the provider to **internally inspect specific keycodes** (KEY_LEFTCTRL +
  KEY_C/KEY_V). Even though only a count would cross the Snapshot boundary,
  that is a new internal observation of key identity — a privacy regression
  in spirit (ADR 0002's "no key identity") and, worse, **asymmetric**: a
  signal that works on Linux but is structurally impossible on the
  macOS-first platform.

- **App identity is already observed content-free** on macOS
  (NSWorkspace `localizedName` → `SanitizeAppID`, sanctioned by ADR 0009).
  Linux intentionally never sets `ActiveApp` (Wayland focus is
  compositor-specific; ADR 0009 says degrade, not guess).

- **Keystroke timing is already on the boundary** (`Snapshot.KeystrokeCount`,
  and the engine's per-tick delta + recency clock). A "sustained typing
  block" is derivable from it with **zero new observation**.

## Decision

**A2 ships only signals derivable from information the providers already
observe content-free, on both platforms, permissionlessly. No new Snapshot
field and no new provider observation are added.**

1. **Focus-session — SHIP as the new EARNING signal (both platforms).**
   Derived in the engine from the existing per-tick keystroke delta/recency:
   a sustained-typing run of `FocusSessionSeconds` (gap-tolerant) completes a
   session and grants a fixed work bonus. Needs nothing the provider does not
   already emit. Mouse can never trigger it, so ADR 0005's mouse<typing
   invariant holds by construction.

2. **App-switch — SHIP as a TRACKED + DISPLAYED counter (macOS), earning
   OFF by default.** Derived in the engine by diffing `TickResult.ActiveApp`
   across ticks (1s granularity naturally coalesces rapid flips). It is
   content-free (app identity already crosses the boundary), but it is
   **macOS-only** — Linux reports no app identity, so it would read 0 there.
   Enabling it to *earn* would make the economy platform-asymmetric, so its
   coin weight is 0 by default; the count is shown as honest analytics.
   Enabling earning is a flagged fork (see below).

3. **Copy/paste chord — DROP from A2.** Not achievable permissionlessly on
   macOS (no keycode identity from `CGEventSource`); the Linux-only path
   would require internal keycode inspection and is asymmetric. It is
   **deferred to a future explicit opt-in `CGEventTap` "precise input"
   permission phase** (already named as a deferred opt-in in ADR 0010),
   where exact per-chord counts become possible *with the user's consent*.

## The fork (surfaced to the user/overseer)

- **Fork A — copy/paste:** ship A2 **without** copy/paste (recommended), or
  open a dedicated opt-in permission phase now. Recommendation: ship without;
  revisit as an opt-in so the default install stays promptless.
- **Fork B — app-switch earning:** keep app-switch **display-only**
  (recommended, keeps the economy identical cross-platform), or enable it to
  earn macOS-first at a small capped weight, accepting platform-asymmetric
  earning until Linux focus detection lands (A3).

## Consequences

- The `Snapshot` struct and `activity.content_free_test.go` are **unchanged**
  — positive evidence that A2 adds no new observation surface. New count
  fields appear only on the internal `engine.TickResult` and on the wire
  (`StatCounters`, `StatsView`), whose structural allow-list tests are
  extended.
- The economy stays a single coin source (sprint completion, ADR 0008);
  focus-session adds a bounded work bonus, so ADR 0005's ceiling and
  typing>mouse ordering are preserved and re-proven by a new strategy test.
- Migration: save schema bumps 2→3 (additive count fields); the existing
  future-schema-refusal guard (`ErrFutureSchema`) protects old builds, and a
  schema-2 file loads with the new counters defaulting to 0.
- The permissionless promise (ADR 0010) is intact: the default install still
  never prompts. Copy/paste precision remains available later, behind consent.
