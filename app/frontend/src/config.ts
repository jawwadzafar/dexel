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
//
// esbuild replaces these two identifiers verbatim with the JSON strings
// build.mjs computes; they do not otherwise exist at runtime. The
// `declare const` keeps `tsc --noEmit` happy (the substitution has not
// happened yet at typecheck time) and emits no code of its own.
declare const __DEXEL_REPO_URL__: string;
declare const __DEXEL_VERSION__: string;

// The canonical repository URL — shown, and opened externally, by the About
// modal (features/about-modal.ts).
export const REPO_URL: string = __DEXEL_REPO_URL__;

// This build's version stamp. There is NO version on the WebSocket wire
// (StateMessage / ConfigView carry none — checked in wire.ts), so the About
// modal shows THIS injected value. If a future server starts sending one,
// prefer that and keep this as the fallback.
export const VERSION: string = __DEXEL_VERSION__;
