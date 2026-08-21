# 0007 — Always-on-top by default, F10 toggle

Status: accepted (v0.2)

## Context
Genre research: Rusty's Retirement presents as a desktop overlay; Bongo Cat
sits on the taskbar. A companion the editor buries is one the user never sees
react — with global input landed, the game works while unfocused, but only
matters if it is visible.

## Decision
`WindowLevel::AlwaysOnTop` by default; F10 toggles at runtime.

## Consequences
- A keybinding, not a config: the moments it must turn off (screen share,
  fullscreen video) are momentary.
- Registered in `run()` only — shotcap has no user at a keyboard.
- Future: a compact/mini mode is the natural next step of the same idea.
