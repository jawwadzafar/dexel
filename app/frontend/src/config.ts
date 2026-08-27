// BUILD-TIME CONFIG — the values esbuild's `define` injects at bundle time
// (see build.mjs). They live in ONE place — the build — instead of as string
// literals scattered through the source, which is the whole point: grep the
// .ts sources and there is no github.com URL and no version string outside
// this file's `declare`s and build.mjs's env-define.
//
// build.mjs sources each with a small precedence chain:
//   __DEXEL_REPO_URL__ — process.env.DEXEL_REPO_URL, else the canonical default.
//   __DEXEL_VERSION__  — process.env.DEXEL_VERSION, else the nearest git tag
//                        (`git describe --tags --abbrev=0`), else "v0.1.0".
//   __DEXEL_AUTHOR__   — process.env.DEXEL_AUTHOR, else "Jawwad Zafar".
//   __DEXEL_COMMIT__   — process.env.DEXEL_COMMIT, else the short SHA the
//                        version tag points at (deterministic — see build.mjs),
//                        else "" (empty: the About modal then shows no commit).
//
// esbuild replaces these identifiers verbatim with the JSON strings build.mjs
// computes; they do not otherwise exist at runtime. The `declare const` keeps
// `tsc --noEmit` happy (the substitution has not happened yet at typecheck
// time) and emits no code of its own.
declare const __DEXEL_REPO_URL__: string;
declare const __DEXEL_VERSION__: string;
declare const __DEXEL_AUTHOR__: string;
declare const __DEXEL_COMMIT__: string;

// The canonical repository URL — shown, and opened externally, by the About
// modal (features/about-modal.ts).
export const REPO_URL: string = __DEXEL_REPO_URL__;

// This build's version stamp. There is NO version on the WebSocket wire
// (StateMessage / ConfigView carry none — checked in wire.ts), so the About
// modal shows THIS injected value. If a future server starts sending one,
// prefer that and keep this as the fallback.
export const VERSION: string = __DEXEL_VERSION__;

// The author, shown as "by <AUTHOR>" in the About modal. Injected like the
// values above, never a literal here (default "Jawwad Zafar" lives in
// build.mjs), so a `DEXEL_AUTHOR=... npm run build` flows through to the modal.
export const AUTHOR: string = __DEXEL_AUTHOR__;

// This build's commit, a short SHA — or "" when none is known. Shown next to
// VERSION in the About modal only when non-empty (honest: skip what we don't
// have). Its default is pinned to the version tag's commit, NOT `HEAD`, so the
// committed bundle stays deterministic as ordinary commits accumulate.
export const COMMIT: string = __DEXEL_COMMIT__;

// The About modal's links row. All derived from REPO_URL so they stay
// env-driven: a DEXEL_REPO_URL override moves every link in lockstep, and no
// second github.com literal enters the sources.
export const RELEASES_URL: string = `${REPO_URL}/releases/latest`;
export const ISSUES_URL: string = `${REPO_URL}/issues`;
export const DOCS_URL: string = `${REPO_URL}/blob/main/docs/README.md`;
