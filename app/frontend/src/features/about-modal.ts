// FEATURE/LOGIC layer — the About modal. Shows dexel's name, its one-line
// tagline, "by <AUTHOR>", this build's VERSION (+commit), a LINKS row (the
// repository URL plus Releases / Report a bug / Docs), a privacy line, and the
// license line. It owns every DOM node under #about and reads NOTHING from the
// store: there is no server state in here (the same "gates nothing" shape
// settings-modal.ts records).
//
// NOT HARD-CODED. The version, commit, author, and every link URL are NOT
// literals in this file — they come from ../config, which reads the values
// esbuild's `define` injected at build time (build.mjs: env override -> git
// tag / tag-commit -> defaults). The derived links (Releases/Issues/Docs) are
// built from REPO_URL, so a single `DEXEL_REPO_URL=...` override moves them all
// in lockstep. This module writes them into the DOM once on load, which is what
// makes a `DEXEL_REPO_URL=... DEXEL_VERSION=... DEXEL_AUTHOR=... npm run build`
// override show up in the modal. There is no version on the WebSocket wire
// (checked in wire.ts), so the injected VERSION is the source shown. The
// privacy and license lines are static text (index.html).
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
import { registerModal } from './modal-dismiss';
import {
  REPO_URL, VERSION, AUTHOR, COMMIT,
  RELEASES_URL, ISSUES_URL, DOCS_URL
} from '../config';

const el = {
  dialog: byId<HTMLDialogElement>('about'),
  openBtn: byId<HTMLButtonElement>('about-open'),
  close: byId<HTMLButtonElement>('about-close'),
  scrim: byId<HTMLDivElement>('scrim'),
  author: byId<HTMLDivElement>('about-author'),
  version: byId<HTMLSpanElement>('about-version'),
  commit: byId<HTMLSpanElement>('about-commit'),
  repo: byId<HTMLAnchorElement>('about-repo'),
  releases: byId<HTMLAnchorElement>('about-releases'),
  issues: byId<HTMLAnchorElement>('about-issues'),
  docs: byId<HTMLAnchorElement>('about-docs'),
  status: byId<HTMLDivElement>('about-repo-status')
};

// Injected build-time values -> DOM, once, on load. This is the whole reason
// the modal reflects an override: nothing here is authored into index.html.
el.author.textContent = 'by ' + AUTHOR;
el.version.textContent = VERSION;
// Commit trails the version, dim, only when the build actually knows one
// (config.COMMIT is "" for a plain dev build) — honest: show what we have.
el.commit.textContent = COMMIT ? ' · ' + COMMIT : '';
el.repo.textContent = REPO_URL;
el.repo.href = REPO_URL;
// The three derived links: text is authored in index.html (RELEASES / REPORT
// BUG / DOCS); only the href is build-injected, so an env override moves them
// all in lockstep with the repo URL.
el.releases.href = RELEASES_URL;
el.issues.href = ISSUES_URL;
el.docs.href = DOCS_URL;

export function isOpen(): boolean {
  return el.dialog.open;
}

export function open(): void {
  if (el.dialog.open) return;
  // A fresh open starts with no copy/fallback status showing.
  el.status.textContent = '';
  el.status.classList.remove('visible');
  el.dialog.show();
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
registerModal(el.dialog, { close: close });

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
// `onCopyFail` is the last-resort behaviour when even the clipboard API is
// gone: the repo link selects its visible URL text; the derived links (which
// show a label, not a URL) just leave the "COPY THE LINK ABOVE" hint up.
function openExternally(url: string, onCopyFail: () => void): void {
  let opened: Window | null = null;
  try {
    opened = window.open(url, '_blank', 'noopener,noreferrer');
  } catch {
    opened = null;
  }
  // A truthy window means a new browser tab, or the shell routed it to the OS
  // browser: done, the game is untouched.
  if (opened) return;
  // Locked-down webview: window.open did nothing. Copy the URL instead.
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(url).then(
      function () { showStatus('LINK COPIED'); },
      onCopyFail
    );
  } else {
    onCopyFail();
  }
}

// Wire an anchor so its ordinary left-click ALWAYS cancels the <a>'s in-page
// navigation first — the href is there for semantics and a real browser's
// middle/cmd-click new-tab, never for a same-window navigation that would
// replace the running game — and routes to an external open instead.
function wireExternalLink(a: HTMLAnchorElement, url: () => string, onCopyFail: () => void): void {
  a.addEventListener('click', function (e: MouseEvent) {
    e.preventDefault();
    openExternally(url(), onCopyFail);
  });
}

// The repo URL is shown in full, so its dead-end fallback selects that text.
wireExternalLink(el.repo, () => REPO_URL, selectRepoText);
// The derived links show a label, not a URL, so there is nothing on screen to
// select in the dead-end case — just say the link could not be opened.
const hintCopyFail = () => showStatus('COULD NOT OPEN LINK');
wireExternalLink(el.releases, () => RELEASES_URL, hintCopyFail);
wireExternalLink(el.issues, () => ISSUES_URL, hintCopyFail);
wireExternalLink(el.docs, () => DOCS_URL, hintCopyFail);

// Keyboard-ownership tier (keybindings.ts): while this modal is open, [I]
// closes it and no bare letter reaches a launcher. Esc is native <dialog>
// behaviour and is deliberately NOT intercepted.
export function handleKeydown(e: KeyboardEvent): void {
  switch (e.key) {
    case 'i': case 'I': close(); break;
    default: break; // Esc: native <dialog> close, resolved by the handler above
  }
}
