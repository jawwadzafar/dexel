# 0001 — Rust + Bevy, desktop-first, offline-first

Status: accepted (project inception)

## Context
A cozy desktop companion that reacts to real computer activity. It runs for
hours beside the user's work, so idle cost, memory, and trust matter more
than iteration speed. The original product discussion explicitly rejected
web/Electron stacks.

## Decision
Rust with Bevy (pinned exact version, currently 0.19.1), Bevy UI only — no
HTML/Electron/webview. No backend, no database, no network calls at all:
state is a local JSON save under the OS data dir.

## Consequences
- Single native binary; cheap to keep running all day.
- Offline-first is also the privacy story's foundation: nothing can phone
  home because there is no home to phone.
- Bevy ECS fits "many small independent behaviors" (mood, progress,
  animation) and its compile-time cost is paid by `dynamic_linking` in dev.
- No SVG/native web asset formats (see ADR 0004); no auto-update without a
  deliberate later decision (options recorded in the README roadmap).
