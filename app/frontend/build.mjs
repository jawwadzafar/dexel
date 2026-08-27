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
//
// The sourcemap is still emitted and still committed (N-9, docs/plan/
// REVIEW-2026-08-22.md): CI's bundle-drift check diffs
// app/public/js/dexel.js.map alongside the bundle, and `-public
// app/public` serves it in dev so devtools can map a stack trace back to
// TypeScript. What changed is that app/embed.go no longer compiles it
// INTO the binary — it was ~230 KB, about a third of the whole embedded
// payload, shipped to every end user as pure debug weight. So: keep
// emitting it, keep committing it, do not embed it. embed_test.go fails
// if either half of that drifts.
import { build } from 'esbuild';
import { execSync } from 'node:child_process';

// ABOUT — the repo URL, website URL, version, and author the About modal shows
// are injected here, NOT hard-coded in the .ts sources (src/config.ts is the
// typed reader of these defines, and its only consumer is
// features/about-modal.ts).
//
// DEXEL_REPO_URL: an env override, else the canonical repository. Shown as the
// modal's GitHub link.
//
// DEXEL_WEBSITE: an env override, else the GitHub Pages home
// (https://jawwadzafar.github.io/dexel — the site's eventual home; can be
// pointed at a custom domain later). Shown as the modal's Website link.
//
// DEXEL_AUTHOR: an env override, else "Jawwad Zafar". Shown in the © line.
//
// DEXEL_VERSION: an env override, else the NEAREST git tag
// (`git describe --tags --abbrev=0`), else "v0.1.0". `--abbrev=0` — the tag
// itself, not `<tag>-<n>-g<sha>` — is deliberate: it keeps the committed
// bundle DETERMINISTIC and pinned to the frozen v0.1.0 release as ordinary
// commits accumulate past the tag (a plain `--always` describe would drift
// the version into a commit SHA on the next commit and churn the bundle).
// It mirrors the Go side's version stamp, which is v0.1.0 during the freeze
// (app/version.go). If the tag is ever gone entirely, describe errors and
// the catch falls back to the same "v0.1.0" default.
function resolveVersion() {
  if (process.env.DEXEL_VERSION) return process.env.DEXEL_VERSION;
  try {
    const tag = execSync('git describe --tags --abbrev=0', {
      stdio: ['ignore', 'pipe', 'ignore']
    }).toString().trim();
    if (tag) return tag;
  } catch {
    // no tags / not a git checkout — fall through to the default
  }
  return 'v0.1.0';
}

const REPO_URL = process.env.DEXEL_REPO_URL || 'https://github.com/jawwadzafar/dexel';
const WEBSITE = process.env.DEXEL_WEBSITE || 'https://jawwadzafar.github.io/dexel';
const VERSION = resolveVersion();
const AUTHOR = process.env.DEXEL_AUTHOR || 'Jawwad Zafar';

await build({
  entryPoints: ['src/main.ts'],
  outfile: '../public/js/dexel.js',
  bundle: true,
  minify: true,
  sourcemap: true,
  format: 'iife',
  target: ['es2020'],
  // Substituted verbatim into the bundle; src/config.ts reads them. JSON is
  // esbuild's required form for a string define.
  define: {
    __DEXEL_REPO_URL__: JSON.stringify(REPO_URL),
    __DEXEL_WEBSITE__: JSON.stringify(WEBSITE),
    __DEXEL_VERSION__: JSON.stringify(VERSION),
    __DEXEL_AUTHOR__: JSON.stringify(AUTHOR)
  },
  logLevel: 'info'
});
