// FEATURE layer — the frameless shell's window controls (WINDOW-POLISH,
// docs/plan/ROADMAP.md; mechanism written up in desktop/README.md).
//
// THE SHAPE OF THE PROBLEM. The Tauri shell builds its window with
// `decorations(false)`, so there is no native titlebar: no close button, no
// minimise button, and nothing to drag the window by. The game's own titlebar
// has to be all three. But the exact same index.html is served to an ordinary
// browser tab, which has all three already and whose appearance the brief
// requires to stay pixel-identical. So every one of those three things is
// gated on SHELL_MODE (env.ts's ?shell=1), and this module is the only place
// that knows the difference.
//
// HOW THE PAGE CAN DO THIS AT ALL — the part that needed verifying, because
// the webview is pointed at a REMOTE origin (`WebviewUrl::External` on
// http://127.0.0.1:<ephemeral port>), not at bundled `tauri://` content.
// Verified against the tauri 2.11.5 source, not inferred:
//
//   * The IPC plumbing is injected UNCONDITIONALLY. tauri's
//     manager/webview.rs pushes `__TAURI_INTERNALS__`, the invoke script and
//     every PLUGIN init script into `initialization_scripts` with no
//     local-vs-remote condition. So this page really does get a working
//     `invoke`, and it really does get the core window plugin's drag.js —
//     which is what makes index.html's `data-tauri-drag-region="deep"` on
//     #titlebar function on a remote origin. Dragging therefore needs NO code
//     in this file at all.
//   * `window.__TAURI__` — the public JS API surface this file uses — is a
//     SEPARATE injection, gated on `app.withGlobalTauri`. That is now `true`
//     in tauri.conf.json precisely so this module can use the documented
//     `getCurrentWindow().minimize()/.close()` instead of the internal
//     `__TAURI_INTERNALS__.invoke('plugin:window|close')` channel, whose name
//     tauri documents as unstable.
//   * The ACL is what actually decides. A remote origin matches ONLY a
//     capability that declares `remote.urls`, and `core:window:default`
//     (inside `core:default`) grants read-only getters — not close, not
//     minimize, not start_dragging. Hence the new
//     desktop/src-tauri/capabilities/loopback-window-controls.json.
//
// FAILURE IS LOUD, NEVER SILENT (ADR 0010). A frameless window whose close
// button does nothing is unclosable — the worst outcome this change could
// produce. So: if ?shell=1 says we are frameless but the Tauri API is not
// reachable (a capability typo, a port spelling the URL pattern does not
// match, an older shell), the buttons are STILL shown and a click reports the
// failure in the flash toast and the console rather than doing nothing at all.
// A visible complaint is recoverable — the user can still kill the window from
// the OS and the log says why — and a dead button is not diagnosable.
import { SHELL_MODE } from '../env';
import { byId } from '../dom';
import { showFlash } from '../render/flash';

// The two calls this module makes, and nothing else. Typed narrowly on
// purpose: widening this to the whole Tauri API surface would invite a future
// module to reach for a window/fs/shell call that the capability does not
// grant, and the failure would be a runtime ACL denial rather than a type
// error.
interface TauriWindowHandle {
  minimize(): Promise<void>;
  close(): Promise<void>;
}
declare global {
  interface Window {
    __TAURI__?: {
      window?: {
        getCurrentWindow?: () => TauriWindowHandle;
      };
      // macOS only, and only for the minimise button — see minimizeAction().
      app?: {
        hide?: () => Promise<void>;
      };
    };
  }
}

// MINIMISE ON macOS IS `hide`, NOT `minimize`, AND THAT IS NOT A WORKAROUND.
//
// `NSWindow.miniaturize()` — what `window.minimize()` calls — always parks a
// live THUMBNAIL of the window in the Dock's right-hand section, beside the
// Trash. The app icon stays in the left section at the same time, so a
// minimised dexel is present in the Dock TWICE, which is what the owner
// reported. No window-level API opts out of it: the only switch is macOS's
// global "Minimize windows into application icon" preference, which belongs
// to the user and not to us, and which we must not be silently depending on.
//
// Hiding the application does what the button is actually for — put dexel
// away, leave one Dock icon, bring it back by clicking that icon. It suits
// this app in particular: the window is only ever a VIEW (see the shell's
// module docs), the runtime keeps counting either way, and there is exactly
// one window, so "hide the app" and "hide the window" mean the same thing
// here in a way they would not in a multi-window editor.
//
// Everywhere else `minimize()` is right and is kept: a Linux or Windows
// taskbar has one entry per window and minimising to it is the expected
// behaviour, with no duplication to avoid.
//
// The macOS test is the user-agent rather than a Tauri OS call, deliberately:
// the os plugin is not in this shell's capability list and adding a plugin to
// answer one boolean would widen the page's granted surface for no reason.
// The string is also checked defensively — if it ever fails to match, the
// worst case is the pre-existing minimise behaviour, not a dead button.
function isMacOS(): boolean {
  const ua = navigator.userAgent || '';
  return ua.indexOf('Mac OS X') !== -1 || ua.indexOf('Macintosh') !== -1;
}

// The minimise button's action: hide the app on macOS, minimise the window
// everywhere else. Falls back to minimise if `app.hide` is missing (an older
// shell, or a capability that does not grant core:app:allow-app-hide), so a
// version skew between the page and the shell degrades to the old behaviour
// instead of to a button that does nothing.
function minimizeAction(w: TauriWindowHandle): Promise<void> {
  if (isMacOS()) {
    const app = window.__TAURI__ && window.__TAURI__.app;
    if (app && typeof app.hide === 'function') return app.hide();
  }
  return w.minimize();
}

function currentWindow(): TauriWindowHandle | null {
  const api = window.__TAURI__;
  const get = api && api.window && api.window.getCurrentWindow;
  if (typeof get !== 'function') return null;
  try {
    return get();
  } catch (e) {
    console.error('dexel: the Tauri window API is present but unusable', e);
    return null;
  }
}

// One click handler shape for both buttons. `verb` is only used in the two
// honest failure messages.
function wire(id: string, verb: string, act: (w: TauriWindowHandle) => Promise<void>): void {
  byId<HTMLButtonElement>(id).addEventListener('click', function () {
    const win = currentWindow();
    if (!win) {
      // The capability/config half is broken. Say so where a user can see it
      // AND where a log can be pasted into a bug report.
      console.error(
        'dexel: cannot ' + verb + ' — window.__TAURI__ is not available on this page. ' +
        'The shell asked for shell mode (?shell=1) but the Tauri window API did not reach it: ' +
        'check app.withGlobalTauri in tauri.conf.json and the remote.urls pattern in ' +
        'capabilities/loopback-window-controls.json against this page origin ' + location.origin + '.'
      );
      showFlash({ type: 'flash', kind: 'error', text: 'CANNOT ' + verb.toUpperCase() + ' - SEE CONSOLE' });
      return;
    }
    act(win).catch(function (e) {
      // Reached when the API exists but the ACL denied the command — tauri
      // logs "not allowed on origin [remote: ...]" on the Rust side.
      console.error('dexel: ' + verb + ' was rejected by the shell', e);
      showFlash({ type: 'flash', kind: 'error', text: verb.toUpperCase() + ' REFUSED - SEE CONSOLE' });
    });
  });
}

export function initShellWindow(): void {
  if (!SHELL_MODE) return;
  // The one class every shell-mode CSS rule hangs off (game.css "Shell
  // mode"). Set before anything paints, so there is no frame of a
  // browser-shaped titlebar in a frameless window.
  document.body.classList.add('shell');
  wire('win-minimize', 'minimize', minimizeAction);
  wire('win-close', 'close', function (w) { return w.close(); });
}
