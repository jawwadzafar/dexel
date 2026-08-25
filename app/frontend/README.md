# dexel frontend build

TypeScript source for the frontend the Go server serves from `app/public/`
(docs/plan/ROADMAP.md, Frontend architecture track). F1 was a mechanical,
behaviour-identical port of the former hand-written `app/public/js/game.js`
(since renamed to `dexel.js` — EMBED-1, docs/plan/ROADMAP.md) into one file.
F2 (this layout) splits that file into industry-standard layers — same
behaviour, same DOM/WS contract, no redesign.

## Layout

- `src/wire.ts` — typed mirror of the WebSocket contract (`docs/ui-spec.md`
  §6 / the Go `StateMessage`/`CatalogMessage` types). Types only, no
  runtime code. Shared by every layer below.
- `src/env.ts`, `src/dom.ts`, `src/format.ts`, `src/assets.ts`,
  `src/geometry.ts` — small pure/DOM-agnostic utility modules (env flags,
  the `byId` lookup helper, number/duration formatting, the asset URL
  prefix, and the fixed scene geometry from `docs/art-direction.md`).
  Imported by whichever layer needs them; own no state and send no
  actions.
- **DATA/STATE layer** (`src/state/`):
  - `store.ts` — the central typed state store: the latest
    `CatalogMessage`/`StateMessage`, derived catalog indices, and pure
    selectors (`tintHexFor`, `isTintOwned`, `freeDefaultItem`,
    `equippedItemFor`). Every render and feature module reads this;
    nothing but the WS client and `dev/dev-tools.ts` writes to it.
  - `ws-client.ts` — connect/backoff/reconnect, `sendAction`, and the
    STORE_OPEN re-assert (`setStoreOpenHoldDesired`). Knows the wire
    contract; knows nothing about modals or the DOM.
- **RENDER layer** (`src/render/`) — given the current store state,
  update the DOM each module owns; none of them send a `ClientAction`.
  - `tint.ts` — the mask+multiply tint mechanism and generic sprite/
    positioning primitives, reused by the store feature's preview pane.
  - `scene.ts` — the scene compositor (`#scene-sprites`): slot sprites,
    the chair, the developer composite, the dev-frame animation timer.
  - `terminal.ts` — the terminal (`#terminal`) + idle-cursor blink.
  - `chrome.ts` — titlebar / sprint bar / status line / ticker, plus the
    two Phase P1 name echoes (`#status-name`, `#menu-panel-title`).
  - `overlays.ts` — the connection-status overlay and the assets-missing
    banner.
  - `preload.ts` — fetches AND `decode()`s every scene sprite at startup,
    holding the `Image` refs so the decodes are not collected. A sprite
    whose bitmap is not ready yet does not render as "slightly late": a
    fresh `<img>` paints nothing, and a `mask-image` that is not decoded
    masks its fill away entirely, which made the character flash WHITE.
  - `viewport.ts` — the letterbox window scaling (`docs/ui-spec.md` §0.1,
    WINDOW-POLISH): one transform on `#root` sized from the viewport,
    recomputed on load, `resize` and dpr change. Always snaps DOWN to a
    device-pixel-crisp factor (integers at dpr 1, halves at dpr 2), caps at
    `MAX_SCALE` = 3, and centres what is left in a letterbox.
  - `interaction.ts` — INTERACTION-HARDENING's capturing `dragstart` /
    `selectstart` guards. The declarative half lives in `game.css`
    ("Interaction hardening") and `dom.ts`'s `spriteImg()`; this is the
    backstop for elements neither reaches. Touches no click/pointer event —
    the scene must keep receiving clicks.
  - `flash.ts` — the flash toast + the insufficient-funds flash.
  - `audio.ts` — SOUND-1's six chiptune sound effects
    (`docs/ui-spec.md` §13): the only module in this frontend that touches
    an `AudioContext`. Lazily creates one on the first user gesture (a
    browser will not let a page make noise before that), warms and decodes
    all six WAVs from `/sounds/`, and gates every `play()` on the server's
    `config.soundEnabled`. A locked context is a NORMAL state here, not an
    error: the sprint/session jingles fire on server events that dexel's
    window may never have been clicked for, so an un-playable sound is
    retried for a bounded 250ms and then dropped — never queued, never
    thrown. `render/scene.ts` is its only caller.
- **FEATURE/LOGIC layer** (`src/features/`) — each owns its own DOM/UI
  state, reads the store, and is the only place that sends the
  `ClientAction`s for that feature.
  - `store-modal.ts` — the entire store modal: categories, grid, preview
    pane, keyboard handling, and BUY_ITEM/BUY_TINT/EQUIP_ITEM/
    STORE_OPEN/STORE_CLOSE.
  - `activity-modal.ts` — the read-only activity/stats modal.
  - `history-modal.ts` — the read-only 30-day history/streak modal (A3).
  - `onboarding-modal.ts` — Phase P1's first-launch identity modal (name +
    starter colour) and the only sender of SET_NAME. Nothing opens it: it
    follows the server's `state.onboarding` flag in both directions.
  - `menu.ts` — the titlebar hamburger panel (`#menu-open`/`#menu-panel`).
  - `settings-modal.ts` — SET-1's Settings modal (rename, always-on-top,
    away-time display) plus SOUND-1's sound-effects toggle. The only
    sender of `SET_PREF`.
  - `sessions-modal.ts` — Phase P2's Sessions modal + session-complete card.
  - `shell-window.ts` — WINDOW-POLISH's in-page close/minimize buttons, shown
    ONLY when the frameless Tauri shell appended `?shell=1` (`env.ts`'s
    `SHELL_MODE`). A no-op in a browser tab, which keeps the browser page
    pixel-identical. Dragging needs no code here: `#titlebar` carries
    `data-tauri-drag-region="deep"` and tauri injects the handler for it into
    remote-origin pages too — see `desktop/README.md`.
  - `keybindings.ts` — global keydown routing ([S]/Tab/[A]/[H]/[M]),
    delegating to whichever modal (if any) is open. Returns immediately
    for a keydown aimed at a text field: every shortcut is a bare letter,
    so without that guard typing a name would open modals (docs/ui-spec.md
    §5.2).
- `src/dev/` — `?dev=1` harness: `dev-fixtures.ts` (hardcoded catalog +
  state, plus `DEV_STATE_ONBOARDING`, the fresh-install fixture) and
  `dev-tools.ts` (seeds the store from them and installs
  `window.devApply`/`window.devCatalog`/`window.devStateOnboarding`).
- `src/main.ts` — thin entry point: wires the three layers together and
  boots (WS connect, or the dev-mode harness). Owns no DOM, no state.
- `build.mjs` — the esbuild build script.

Adding a new menu/modal touches only a new file under `src/features/` +
an extension to `src/wire.ts` — no existing layer needs to change.

## Build

```
cd app/frontend
npm install
npm run build       # bundles + minifies src/main.ts -> ../public/js/dexel.js (+ .map)
npm run typecheck    # tsc --noEmit, strict
```

## Why the bundle is committed

`app/public/js/dexel.js` (the build output) is committed to the repo
alongside the TypeScript source. This means `go run .` (from `app/`) always
serves a working game with zero npm/Node dependency for a Go-only user or
CI leg — the frontend toolchain is only needed when *changing* the
frontend. The tradeoff: the committed bundle can drift from the TS source
if someone edits one without the other. CI's frontend job guards against
that by running a fresh `npm run build` and diffing it against the
committed `app/public/js/dexel.js` — a mismatch fails the build.

If you change `src/*.ts`, always run `npm run build` and commit the
resulting `app/public/js/dexel.js` (and its `.map`) in the same change.
