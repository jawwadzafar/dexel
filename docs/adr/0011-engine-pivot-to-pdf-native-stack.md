# 0011 — Engine pivot: ship the PDF design on its native stack (Go + HTML/NES.css)

Status: accepted (2026-08-21) · Supersedes the engine half of ADR 0001

## Context

The user's design PDF is the product bar: behind-the-shoulder pixel scene,
a monitor showing live terminal lines, a retro-OS **store modal** with
categories/Buy/Equip/preview, style+color customization, NES.css aesthetics,
floating always-on-top delivery, macOS first. The user stated plainly: "part
of our game is also how it looks — if we can't achieve it, change engine or
whatever; main goal is to get the game out."

Honest capability assessment of the current Bevy path against that bar:

- **Nothing in the design is impossible in Bevy.** Sprites, tints, text-in-
  a-region, clickable UI — all achievable.
- **But the design is NATIVE to HTML.** The PDF's own blueprint (the user's
  own design conversation) specifies Go + HTML/NES.css + WebSockets + Wails;
  the mockups are literally NES.css layouts. In Bevy, the store modal,
  retro window chrome, scrollable grids, hover states, and per-item color
  pickers are hundreds of lines of hand-built widget code per screen; in
  HTML they are divs and a CSS file. Same look, a fraction of the labor,
  and far lower risk of "almost right but off" chrome.
- **Verifiability decides it.** This development box is Linux; the user is
  on macOS. A Bevy UI's macOS behavior is invisible from here (we shipped a
  broken-feeling build once already). An HTML UI is *pixel-identical* here
  and there — a headless browser on this box sees exactly what the user
  sees. The overseer can finally verify the product, not a proxy of it.
- **A working head start exists.** The user's Go prototype
  (github.com/jawwadzafar/dev_companion) already has the backend bones:
  activity provider interface + macOS listeners, an engine with per-app-
  class work weights, a catalog/state model, sqlite persistence, a
  WebSocket hub — all aligned with the PDF because both came from the same
  person's design thinking.

## Decision

- The shipping game is **Go backend + HTML/JS/NES.css frontend**, served
  locally (`localhost:8080`), macOS-first, later wrapped by Wails v3 for
  the floating frameless app. Developed in THIS repo under `app/`.
- **Everything engine-agnostic carries over**: the pixel-art generator and
  sprites (PNGs render identically in HTML with `image-rendering: pixelated`),
  the 18-color palette, the economy calibration (ADR 0005 numbers), the
  honesty rules (ADR 0010 — Coding needs keystrokes; blind sources freeze
  the idle clock), privacy boundaries (ADR 0002/0009: counts only, app
  identity only, never titles/content), and all of docs/.
- macOS capture: port ADR 0010's **permissionless**
  `CGEventSourceSecondsSinceLastEventType` sampling to Go via a tiny Cgo
  shim (the user's prototype used a CGEventTap, which demands the
  Accessibility prompt — keep that as an opt-in accuracy upgrade, not the
  default).
- The Rust/Bevy implementation (`companion/`, `activity/`) is **frozen as
  legacy**: kept in-tree, compiling and green as of its final commit,
  excluded from the product path. Not deleted — it is the reference
  implementation of the mechanics and may return if the web stack
  disappoints. The fleetsmith/opencode fleet is likewise retired from the
  critical path (files kept; Claude subagent orchestration has outperformed
  it throughout).

## What would make us revisit
Wails/webview memory footprint breaking the "cheap to leave open all day"
promise (>~100MB), or input-capture gaps the Go/Cgo path cannot close. The
Rust legacy build is retained precisely for that exit.
