# 0009 — Show app identity, never window titles

Status: accepted (v0.3)

## Context
The HUD showed generated fiction ("Fix login flow"). The user wants the game
to mirror reality: show what they are actually working in. But window titles
leak file names, document names, page titles, chat contents — exactly what
ADR 0002 promises never to touch.

## Decision
Capture the foreground APPLICATION identity only (app class / app-id, e.g.
"code", "firefox"), sanitized (lowercased, charset-limited, length-capped)
and mapped to a small friendly-name table. Titles, URLs, document names and
process arguments are discarded at the source and never reach a field, log,
or return value — enforced by the same structural-test pattern as ADR 0002.

## Consequences
- The HUD can say "Coding in VS Code" truthfully without weakening the
  privacy story; the privacy README section gains one honest sentence about
  app identity.
- Wayland makes "which app is focused" compositor-specific; the watcher
  probes per-desktop APIs and degrades to None (generic label) rather than
  guessing from /proc.
- The project/reward loop stays (sessions still earn); only the LABEL
  becomes real. Fictional project names are removed.
