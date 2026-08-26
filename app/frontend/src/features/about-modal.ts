// FEATURE/LOGIC layer — the About modal. Shows dexel's name, its one-line
// tagline, this build's VERSION, and a link to the repository. It owns every
// DOM node under #about and reads NOTHING from the store: there is no server
// state in here (the same "gates nothing" shape settings-modal.ts records).
//
// NOT HARD-CODED. The version and repo URL are NOT literals in this file —
// they come from ../config, which reads the two values esbuild's `define`
// injected at build time (build.mjs: env override -> nearest git tag ->
// "v0.1.0"). This module writes them into the DOM once on load, which is what
// makes a `DEXEL_REPO_URL=... DEXEL_VERSION=... npm run build` override show
// up in the modal. There is no version on the WebSocket wire (checked in
// wire.ts), so the injected VERSION is the source shown.
//
// THE LINK MUST NEVER REPLACE THE RUNNING GAME. This app deliberately
// disables in-webview navigation (render/interaction.ts kills the context
// menu and the drag-to-navigate escape; the frameless shell has no chrome to
// get back with). So the repo link's click ALWAYS preventDefault()s the
// in-page navigation and opens the URL on a SEPARATE surface instead:
//   - a plain browser: window.open(url, '_blank') opens a new tab, and the
//     Tauri shell routes that same call out to the OS default browser;
//   - if window.open is blocked / returns null (a locked-down webview), fall
//     back to copying the URL to the clipboard, and failing THAT, select the
//     URL text so the user is one Ctrl/Cmd+C away from copying it by hand.
// The game surface is never navigated, in any of those paths, at 1x or 2x.
import { byId } from '../dom';
import { enableClickAwayDismiss } from './modal-dismiss';
import { REPO_URL, VERSION } from '../config';

const el = {
  dialog: byId<HTMLDialogElement>('about'),
  openBtn: byId<HTMLButtonElement>('about-open'),
  close: byId<HTMLButtonElement>('about-close'),
  scrim: byId<HTMLDivElement>('scrim'),
  version: byId<HTMLSpanElement>('about-version'),
  repo: byId<HTMLAnchorElement>('about-repo'),
  status: byId<HTMLDivElement>('about-repo-status')
};

// Injected build-time values -> DOM, once, on load. This is the whole reason
// the modal reflects an override: nothing here is authored into index.html.
el.version.textContent = VERSION;
el.repo.textContent = REPO_URL;
el.repo.href = REPO_URL;

export function isOpen(): boolean {
  return el.dialog.open;
}

export function open(): void {
  if (el.dialog.open) return;
  // A fresh open starts with no copy/fallback status showing.
  el.status.textContent = '';
  el.status.classList.remove('visible');
  el.dialog.showModal();
  el.scrim.classList.add('visible');
}

export function close(): void {
  if (!el.dialog.open) return;
  el.dialog.close();
}

// The one close path every dismissal funnels through — X, [I], Esc,
// click-away — the add-a-menu-modal rule ("hang cleanup on the dialog's own
// 'close' event"). Nothing is pending at close time.
el.dialog.addEventListener('close', function () {
  el.scrim.classList.remove('visible');
});
el.openBtn.addEventListener('click', open);
el.close.addEventListener('click', close);
enableClickAwayDismiss(el.dialog, close);

function showStatus(text: string): void {
  el.status.textContent = text;
  el.status.classList.add('visible');
}

// Ultimate fallback: select the URL text so it can be copied by hand. Done
// programmatically — not a user 'selectstart', so interaction.ts's guard
// does not cancel it — and the CSS gives #about-repo `user-select: text` so
// the highlight actually shows.
function selectRepoText(): void {
  showStatus('COPY THE LINK ABOVE');
  const sel = window.getSelection ? window.getSelection() : null;
  if (!sel) return;
  const range = document.createRange();
  range.selectNodeContents(el.repo);
  sel.removeAllRanges();
  sel.addRange(range);
}

// See this file's header for why every path here preventDefault()s the
// in-page navigation first (the caller does) and never lets the game reload.
function openExternally(): void {
  let opened: Window | null = null;
  try {
    opened = window.open(REPO_URL, '_blank', 'noopener,noreferrer');
  } catch {
    opened = null;
  }
  // A truthy window means a new browser tab, or the shell routed it to the OS
  // browser: done, the game is untouched.
  if (opened) return;
  // Locked-down webview: window.open did nothing. Copy the URL instead.
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(REPO_URL).then(
      function () { showStatus('LINK COPIED'); },
      selectRepoText
    );
  } else {
    selectRepoText();
  }
}

el.repo.addEventListener('click', function (e: MouseEvent) {
  // ALWAYS cancel the <a>'s in-page navigation first — the href is there for
  // semantics and a real browser's middle/cmd-click new-tab, never for a
  // same-window navigation that would replace the running game.
  e.preventDefault();
  openExternally();
});

// Keyboard-ownership tier (keybindings.ts): while this modal is open, [I]
// closes it and no bare letter reaches a launcher. Esc is native <dialog>
// behaviour and is deliberately NOT intercepted.
export function handleKeydown(e: KeyboardEvent): void {
  switch (e.key) {
    case 'i': case 'I': close(); break;
    default: break; // Esc: native <dialog> close, resolved by the handler above
  }
}
