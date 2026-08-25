// Environment flags shared across layers. `?dev=1` is the documented
// dev-mode contract (see src/dev/dev-tools.ts): it seeds hardcoded catalog
// + state instead of connecting over WS, and exposes window.devApply for
// an external harness to drive specific states for a screenshot.
export const DEV_MODE = new URLSearchParams(location.search).get('dev') === '1';

// WINDOW-POLISH (docs/plan/ROADMAP.md): is this page being displayed inside
// dexel's own frameless Tauri window, or in an ordinary browser tab?
//
// The shell answers by appending `?shell=1` to the loopback URL it loads
// (desktop/src-tauri/src/lib.rs's shell_url), and it is the ONLY thing that
// does. This flag is not a capability probe — it is the shell DECLARING
// itself, which is the right shape because the two facts it gates are
// decisions the shell made, not things the page can detect:
//
//   1. the native frame is gone (`decorations(false)`), so the page must
//      supply close/minimize controls, and
//   2. the titlebar is the window's drag handle.
//
// Deliberately NOT `typeof window.__TAURI__ !== 'undefined'`. That global is
// injected into every document the webview loads, so it would also be true in
// a hypothetical future decorated window — and it says nothing about whether
// there is a frame to replace. Detecting the wrong fact here would show close
// and minimize buttons on top of a native titlebar that already has them.
export const SHELL_MODE = new URLSearchParams(location.search).get('shell') === '1';
