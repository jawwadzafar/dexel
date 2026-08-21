# dev-companion frontend build

TypeScript source for the frontend the Go server serves from `app/public/`
(docs/plan/ROADMAP.md, Frontend architecture track, phase F1). A mechanical,
behaviour-identical port of the former hand-written `app/public/js/game.js`
— not a redesign (that's F2).

## Layout

- `src/wire.ts` — typed mirror of the WebSocket contract (`docs/ui-spec.md`
  §6 / the Go `StateMessage`/`CatalogMessage` types). Types only, no
  runtime code.
- `src/main.ts` — the ported game logic (rendering, the store/activity
  modals, input, the WS client). One file for F1; F2 splits this into
  render/data/logic layers.
- `build.mjs` — the esbuild build script.

## Build

```
cd app/frontend
npm install
npm run build       # bundles + minifies src/main.ts -> ../public/js/game.js (+ .map)
npm run typecheck    # tsc --noEmit, strict
```

## Why the bundle is committed

`app/public/js/game.js` (the build output) is committed to the repo
alongside the TypeScript source. This means `go run .` (from `app/`) always
serves a working game with zero npm/Node dependency for a Go-only user or
CI leg — the frontend toolchain is only needed when *changing* the
frontend. The tradeoff: the committed bundle can drift from the TS source
if someone edits one without the other. CI's frontend job guards against
that by running a fresh `npm run build` and diffing it against the
committed `app/public/js/game.js` — a mismatch fails the build.

If you change `src/*.ts`, always run `npm run build` and commit the
resulting `app/public/js/game.js` (and its `.map`) in the same change.
