---
name: add-a-menu-modal
description: |
  The repeatable recipe for adding a new menu/modal to the dev-companion
  frontend (an Activity log, Settings, Achievements, ...): copy the store
  modal's pattern (native <dialog>, the DOM/id contract, mouse + keyboard +
  Esc, the one-action-button precedence idea), extend the WS
  CompanionState/CatalogMessage contract with camelCase fields, keep the
  server as the sole source of truth (the client never asserts state the
  server didn't send), wire the modal's open/close as an honest signal if it
  should gate anything (like STORE_OPEN freezing earning), and update
  docs/ui-spec.md. Use when adding a new modal/menu to app/public, or
  extending the WebSocket state/catalog contract.
x-fleetsmith-origin: human
---

# Add a menu/modal

The store modal (`docs/ui-spec.md` §4, implemented in `app/public/index.html`
+ `app/public/js/game.js`, backed by `app/internal/game/catalog.go` +
`app/hub.go`) is the one modal that exists today. `docs/plan/ROADMAP.md`'s
"Menus & content track" says future menus (an Activity log, Settings,
Achievements, more store categories) reuse this exact pattern — do not invent
a second modal idiom.

## 1. Spec it in `docs/ui-spec.md` first, not in code

Add a numbered section shaped like §4 (The store modal): exact pixel rects for
every element (this app is 640x400 fixed, integer px only, no `%`/`em`/`vh` —
§0 ground rules), the DOM id contract, mouse targets, keyboard bindings, and
any `[DESIGN CALL]` where the requirements were ambiguous. Implementers should
be able to build it from the doc alone, the same way §4 was written to be
implementable without the source mockups.

## 2. Copy the store modal's mechanics, don't reinvent them

- **Native `<dialog>` opened with `showModal()`.** Free focus-trapping and
  `Esc`-to-close. Close via `dialog.close()` from *any* trigger (X button, a
  key, Esc) and hang your cleanup on the dialog's own `'close'` event, not on
  each trigger individually — this is what makes X/key/Esc all reach the same
  code path (`game.js`'s `closeStore()` / the `store.addEventListener('close', ...)` 
  handler).
- **A `#scrim` div** if the modal should visually dim the scene behind it, but
  decide deliberately whether clicking it closes the modal — the store's
  answer is *no* (§4.5's `[DESIGN CALL]`: the whole point of the store is
  watching the scene change live behind it; a click-to-close scrim punishes
  you for looking at what you just bought). Don't default to "scrim closes on
  click" without asking whether this modal has the same "watch it happen
  live" property.
- **Mouse AND keyboard AND Esc, all three, from day one.** The pre-modal
  Bevy build shipped keyboard-only and it was treated as a real gap once the
  HTML rewrite made mouse free (`docs/ui-spec.md` §5.1). Every hover state,
  click target, and scroll region should work before you call the modal done.
- **One action button per interactive row, precedence-ordered.** The store's
  §4.3 table (not owned -> `BUY`; not affordable -> disabled `NEED <price>`;
  owned but not equipped -> `EQUIP`; equipped -> disabled `✓ EQUIPPED`) is the
  template: enumerate every state a row can be in, give it exactly one button
  whose label and action are a pure function of that state, and order the
  conditions so exactly one always matches.

## 3. The WS-contract-extension checklist

Extending `CompanionState`/`CatalogMessage` (or adding a new message type) is
the load-bearing part — get this wrong and the frontend either can't render
the modal or silently drifts from the server.

- [ ] **camelCase everywhere on the wire.** `docs/ui-spec.md` §6.2's own
  `[DESIGN CALL]`: the payload is consumed by JS, one casing convention across
  wire and DOM removes a whole class of typo bugs. `itemId`, not `item_id`.
- [ ] **The server is the sole source of truth.** The client renders
  `state`/`catalog` verbatim and does zero client-side derivation of anything
  that could instead be a field — `docs/ui-spec.md` §3's rule ("the frontend
  receives ... already-chosen literal strings and does zero client-side
  assembly") generalizes: if the client ever asserts a piece of UI state the
  server didn't send it, that state can desync from reality. Every mutation
  is answered by a full `state` broadcast, never an optimistic local edit
  reconciled later.
- [ ] **New fields go on the `content_free_test.go` allow-list in the same
  change.** `StateMessage`/`SaveData`/`Snapshot` are all reflection-audited
  (see `feature-build-and-verify`); a new field with no allow-list entry
  fails the build, by design.
- [ ] **New static content (a new catalog category, a new modal's item list)
  is a data table, not new code per item.** Follow `catalog.go`'s pattern:
  one Go struct, one slice literal, `sprite`/`detail`/`thumb`/`thumbForm`/
  `thumbDetail` as `*string` so the wire JSON emits real `null` rather than
  `""` for the slots that don't apply.
- [ ] **Validate server-side, reject cleanly.** Unknown ids, an item whose
  slot doesn't match, buying something owned, equipping something unowned,
  unaffordable purchases — every one of these is a `flash` of kind `error`
  and an unchanged `state`, never a panic, never a partial write
  (`docs/ui-spec.md` §6.2).
- [ ] **A disconnect must not freeze anything forever.** `STORE_OPEN`'s flag
  is cleared server-side on connection drop (§5.3) — if your modal's open
  state gates something (see below), make sure closing the WS connection
  clears that gate too, or a crashed tab locks progression.

## 4. If the modal should gate anything, make the gate an honest signal

`STORE_OPEN`/`STORE_CLOSE` exist because a global keyboard sampler can't tell
a keystroke aimed at a modal from one aimed at your editor — left ungated,
keyboard-driven shopping would accrue "work" and mint free currency (a real
bug caught pre-merge: "money-mint reproduced live at full typing rate" when
the gate had a bug). If your new modal captures keyboard input:

1. Send an explicit open/close action (`{"action":"MODAL_OPEN"}` /
   `{"action":"MODAL_CLOSE"}`), don't infer it from other messages.
2. While open, the engine must not accrue work/cash and must **freeze**
   rather than guess at the idle/AFK clock — same reasoning as ADR 0010's
   "an unfocused window freezes the idle clock: the game cannot know, so it
   must not claim."
3. Echo the open/closed flag back in `state` so a second client or a
   reconnect can tell progression is paused.
4. Test the disconnect-clears-the-flag path explicitly (see
   `app/store_gate_test.go`'s pattern) — this is exactly the kind of bug that
   only shows up when a client vanishes mid-session, and it minted money once.

## 5. Verify it in the real running game

This is not optional and not different from any other UX change — see the
`feature-build-and-verify` skill's exact build+screenshot recipe. Open the new
modal in the real app via a headless-browser screenshot, confirm mouse click,
`Enter`, and `Esc` all work, and confirm the scene still updates live behind
it if that's the intended behaviour.
