// Build script for the dexel frontend (F1, docs/plan/ROADMAP.md).
// Bundles + minifies app/frontend/src/main.ts into app/public/js/dexel.js
// with a sourcemap alongside it.
//
// The bundle is named after the product (EMBED-1, docs/plan/ROADMAP.md):
// it was `game.js` while the frontend was a hand-written script, and the
// rename moves in lockstep with index.html's <script src>, the CI drift
// checks in .github/workflows/{build,release}.yml, and the release script.
// app/public/ is embedded into the Go binary by app/embed.go, so a rebuilt
// bundle reaches a running server either by rebuilding the binary or by
// starting it with the `-public` dev override.
//
// `iife` format mirrors the original hand-written script, which was a plain
// immediately-invoked function loaded via a bare <script src="js/dexel.js">
// tag (no type="module") in index.html.
import { build } from 'esbuild';

await build({
  entryPoints: ['src/main.ts'],
  outfile: '../public/js/dexel.js',
  bundle: true,
  minify: true,
  sourcemap: true,
  format: 'iife',
  target: ['es2020'],
  logLevel: 'info'
});
