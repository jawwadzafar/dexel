// Build script for the dev-companion frontend (F1, docs/plan/ROADMAP.md).
// Bundles + minifies app/frontend/src/main.ts into app/public/js/game.js
// (what the Go server already serves — no change needed on that side) with
// a sourcemap alongside it. `iife` format mirrors the old hand-written
// game.js, which was a plain immediately-invoked function loaded via a bare
// <script src="js/game.js"> tag (no type="module") in index.html.
import { build } from 'esbuild';

await build({
  entryPoints: ['src/main.ts'],
  outfile: '../public/js/game.js',
  bundle: true,
  minify: true,
  sourcemap: true,
  format: 'iife',
  target: ['es2020'],
  logLevel: 'info'
});
