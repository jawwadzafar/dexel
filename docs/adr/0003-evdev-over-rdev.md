# 0003 — evdev over rdev for global input

Status: accepted (v0.2)

## Context
v0.1 counted activity only while the game window had focus, which made the
core premise inert: the user's real work happens in their editor. A global
OS-level listener was always the plan (§3.1). The obvious crate is `rdev`.

## Decision
`evdev` (reading `/dev/input/event*`), Linux-target-scoped, behind the
`global-input` feature. Not `rdev`.

## Rationale
- `rdev`'s Linux backend is X11 `XRecord`, which sees only XWayland clients.
  On a Wayland session (this machine) a Wayland-native editor's keystrokes —
  the entire point — are invisible. `evdev` reads below the display server.
- `rdev` did not even compile here (needs libx11-dev/pkg-config; no sudo).
- Narrower is safer: `rdev` also synthesizes input and exposes key identity;
  surface we deliberately do not want.

## Consequences
- Requires `input` group membership; degrades explicitly to the focused
  provider with a warning otherwise (never a panic, never silence).
- Keycodes are read in one-line predicates and dropped as bools; EV_MSC
  scancodes ignored outright. Auto-repeat and mouse buttons not counted.
- Non-Linux builds get an `UnsupportedPlatform` stub, so the feature is
  portable to enable even where it cannot function.
