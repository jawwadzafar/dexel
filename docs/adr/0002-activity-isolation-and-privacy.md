# 0002 — Activity isolation: separate crate, content-free events

Status: accepted (v0.1 architecture)

## Context
The game monitors keyboard/mouse activity. That is radioactive unless the
boundary between "observing activity" and "reading content" is structural,
not promised.

## Decision
A plain-Rust `activity` crate with **zero Bevy dependency** (compiler-enforced
direction: `companion -> activity`, never back). Everything crosses one
boundary: `ActivityEvent { Keystroke, MouseMoved, FocusChanged(bool) }` —
counts and transitions only. No key identity, no text, no clipboard, no
window titles. A unit test (`activity_events_are_content_free`) is written so
adding a payload field stops it compiling.

## Consequences
- New signals (git, editor, AI-agent state — and later, global input and
  active-app detection) are new providers behind the same trait; game systems
  never change. Proven twice: `GlobalInputProvider` and `ActiveAppWatcher`
  touched zero game logic.
- The privacy claim is testable and reviewable, not aspirational. The
  boundaries PR reviewer treats a violation as an automatic veto.
