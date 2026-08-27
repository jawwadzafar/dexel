// BUILD-TIME CONFIG — the values esbuild's `define` injects at bundle time
// (see build.mjs). They live in ONE place — the build — instead of as string
// literals scattered through the source, which is the whole point: grep the
// .ts sources and there is no github.com URL and no version string outside
// this file's `declare`s and build.mjs's env-define.
//
// build.mjs sources each with a small precedence chain:
//   __DEXEL_REPO_URL__ — process.env.DEXEL_REPO_URL, else the canonical default.
//   __DEXEL_WEBSITE__  — process.env.DEXEL_WEBSITE, else the GitHub Pages home.
//   __DEXEL_VERSION__  — process.env.DEXEL_VERSION, else the nearest git tag
//                        (`git describe --tags --abbrev=0`), else "v0.1.0".
//   __DEXEL_AUTHOR__   — process.env.DEXEL_AUTHOR, else "Jawwad Zafar".
//
// esbuild replaces these identifiers verbatim with the JSON strings build.mjs
// computes; they do not otherwise exist at runtime. The `declare const` keeps
// `tsc --noEmit` happy (the substitution has not happened yet at typecheck
// time) and emits no code of its own.
declare const __DEXEL_REPO_URL__: string;
declare const __DEXEL_WEBSITE__: string;
declare const __DEXEL_VERSION__: string;
declare const __DEXEL_AUTHOR__: string;

// The canonical repository URL — the About modal's GitHub link, opened
// externally (features/about-modal.ts).
export const REPO_URL: string = __DEXEL_REPO_URL__;

// The project's website — the About modal's Website link, opened externally.
// Defaults to the GitHub Pages home (build.mjs); a DEXEL_WEBSITE override (e.g.
// a custom domain later) flows straight through to the modal.
export const WEBSITE_URL: string = __DEXEL_WEBSITE__;

// This build's version stamp. There is NO version on the WebSocket wire
// (StateMessage / ConfigView carry none — checked in wire.ts), so the About
// modal shows THIS injected value. If a future server starts sending one,
// prefer that and keep this as the fallback.
export const VERSION: string = __DEXEL_VERSION__;

// The author, shown in the About modal's "© 2026 <AUTHOR>" license line.
// Injected like the values above, never a literal here (default "Jawwad Zafar"
// lives in build.mjs), so a `DEXEL_AUTHOR=... npm run build` flows through to
// the modal.
export const AUTHOR: string = __DEXEL_AUTHOR__;
