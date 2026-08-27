// FEATURE/LOGIC layer — the "How to play" modal (HOWTO-1). A tester who
// installed dexel said they had "no idea what to do or what it does" once the
// window appeared, so this is the one screen that explains, in a few tight
// pixel-art lines, WHAT dexel is (a pixel-art dev who works while you do), THE
// LOOP (type anywhere -> your dev advances the sprint -> finished sprints pay
// coins -> spend them in the store on things that appear on the desk), and THE
// KEYS (S/A/H/W and Esc). It owns every DOM node under #howto and reads NOTHING
// from the store — there is no server state in here (the same "gates nothing"
// shape settings-modal.ts / about-modal.ts record).
//
// SAME MECHANICS AS EVERY OTHER MODAL. It is a native <dialog> opened
// non-modally (dialog.show()), dimmed by the shared #scrim, dismissed through
// the shared modal helper (registerModal -> Esc + click-away), and given a
// keyboard-ownership tier in features/keybindings.ts (bound to "?"). It is NOT
// a second competing full-screen modal layered over onboarding: it opens only
// AFTER the first-launch onboarding (naming the dexel) has closed — see
// syncFirstRun() and its call site in main.ts, right after
// onboardingModal.syncWithServer().
//
// HOW "SEEN" IS PERSISTED — localStorage ONLY, no save-schema change. The
// project is on a clean schema-1 / no-migration save model, and this intro is
// a pure client-side courtesy: nothing the server or the economy needs to
// know. So the "already seen it" flag lives in localStorage (SEEN_KEY below),
// never in SaveData and never on the WebSocket wire — adding a field to either
// would drag in a content-free-test allow-list entry and a save-compat concern
// for what is really just "don't nag this browser again". Every localStorage
// access is wrapped (a locked-down webview can throw on access): a failure
// degrades to "treat as not-yet-seen", which at worst shows the intro once
// more — never a crash, never a nag loop (the first-run gate below still fires
// at most once per page load).
import { byId } from '../dom';
import { registerModal } from './modal-dismiss';
import * as store from '../state/store';
import * as menu from './menu';

// The localStorage flag. Presence (any value) means "this browser has seen the
// intro; never auto-open it again". Namespaced so it never collides with a
// future pref.
const SEEN_KEY = 'dexel.howToPlaySeen';

const el = {
  dialog: byId<HTMLDialogElement>('howto'),
  openBtn: byId<HTMLButtonElement>('howto-open'),
  close: byId<HTMLButtonElement>('howto-close'),
  gotIt: byId<HTMLButtonElement>('howto-got-it'),
  scrim: byId<HTMLDivElement>('scrim')
};

function hasSeen(): boolean {
  try {
    return window.localStorage.getItem(SEEN_KEY) !== null;
  } catch {
    // A webview that throws on storage access: treat as not-seen. The
    // first-run guard (syncFirstRun) still fires at most once per page load,
    // so the worst case is the intro showing once, not a loop.
    return false;
  }
}

function markSeen(): void {
  try {
    window.localStorage.setItem(SEEN_KEY, '1');
  } catch {
    // Nothing to do — a browser that cannot persist the flag will simply be
    // offered the intro again on a future first run. Never fatal.
  }
}

export function isOpen(): boolean {
  return el.dialog.open;
}

export function open(): void {
  if (el.dialog.open) return;
  el.dialog.show();
  el.scrim.classList.add('visible');
}

export function close(): void {
  if (!el.dialog.open) return;
  el.dialog.close();
}

// First-run auto-open. main.ts calls this on every `state`, right AFTER
// onboardingModal.syncWithServer() has opened/closed the identity modal, so
// the two never double-cover the screen: this only ever opens once onboarding
// is gone.
//
// The rule is "show the intro exactly once, to a genuine first-time player,
// the moment they finish naming their dexel". Two signals gate it:
//
//   1. The localStorage SEEN flag — once dismissed, never auto-open again
//      (in any session, on this browser). This alone makes it non-nagging.
//   2. onboardingSeenThisSession — we only auto-open for someone who actually
//      went THROUGH onboarding this session (state.onboarding was true, then
//      became false). A RETURNING user (an existing save, so onboarding is
//      false from the first tick) never trips this, which is exactly the
//      "a non-first-run must NOT show it" requirement — and it means we lean
//      on the server's own honest first-launch signal rather than inventing a
//      second one. Piggybacking on onboarding also means a reload AFTER
//      naming (onboarding already false, flag not yet set because the user
//      never clicked "GOT IT") does NOT re-show it, since onboarding is never
//      true again on that later load.
let onboardingSeenThisSession = false;

export function syncFirstRun(): void {
  const state = store.getState();
  const onboardingNow = !!(state && state.onboarding);
  if (onboardingNow) {
    onboardingSeenThisSession = true;
    return; // wait for onboarding to be answered/skipped first
  }
  if (hasSeen()) return;               // already dismissed on this browser
  if (!onboardingSeenThisSession) return; // returning user — not a first run
  // Onboarding just completed this session and the intro has never been
  // dismissed here: show it once. Consume the transition so a later state
  // tick can't re-open it, and stand down if another modal somehow owns the
  // screen (it shouldn't at this instant, but never fight for the surface).
  onboardingSeenThisSession = false;
  if (anyModalOpen()) return;
  open();
}

// True if any browsable modal is currently open. The modals are mutually
// exclusive; this is only a safety check so the first-run auto-open never
// stacks on top of one that is somehow already up.
function anyModalOpen(): boolean {
  const dialogs = document.querySelectorAll('dialog');
  for (let i = 0; i < dialogs.length; i++) {
    if ((dialogs[i] as HTMLDialogElement).open) return true;
  }
  return false;
}

// The one close path every dismissal funnels through — X, GOT IT, [?], Esc,
// click-away — the add-a-menu-modal rule ("hang cleanup on the dialog's own
// 'close' event"). Any dismissal counts as "seen": the user has now looked at
// the intro, so it must never auto-open at them again.
el.dialog.addEventListener('close', function () {
  el.scrim.classList.remove('visible');
  markSeen();
});

el.openBtn.addEventListener('click', function () { menu.close(); open(); });
el.close.addEventListener('click', close);
el.gotIt.addEventListener('click', close);
registerModal(el.dialog, { close: close });

// Keyboard-ownership tier (keybindings.ts): while this modal is open, "?"
// closes it and no bare letter reaches a launcher. Esc is owned by the shared
// modal helper (modal-dismiss.ts) in the capture phase, so it is not handled
// here — "presence is the point" (onboarding-modal.ts's handleKeydown note).
export function handleKeydown(e: KeyboardEvent): void {
  if (e.key === '?') close();
}
