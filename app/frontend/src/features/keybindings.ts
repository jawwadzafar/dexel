// FEATURE/LOGIC layer — global keyboard routing (ui-spec.md §5.2): [S] /
// Tab open the store, [A] opens the activity log, [H] opens the history
// modal (Analytics track Phase A3, docs/plan/A3-design.md §6/§7 Task
// TS-1), [W] opens the Sessions modal (Phase P2, docs/plan/P2-design.md
// §6.3 — "work session"; S/Tab/A/H/M are taken, W is free), [G] opens the
// Settings modal (SET-1, docs/ui-spec.md §11 — G for the gear; S/A/H/W/M/P
// and Tab were all taken by then), and while any one modal is open its own
// keydown handler owns the keyboard (Esc always falls through to native
// <dialog> behaviour, never intercepted here).
// This module knows the modal features' public
// open()/isOpen()/handleKeydown() surface — it never reaches into their
// DOM.
//
// The hamburger menu (./menu.ts) is deliberately NOT given the same
// keyboard-ownership tier as a modal: it never captures keys the way a
// <dialog> with real content does, it just closes on Esc, and it steps
// aside for [S]/[A]/[H]/[W]/[G]/Tab so those shortcuts keep working
// identically whether or not the menu happens to be open. [M] toggles the
// menu itself.
import * as storeModal from './store-modal';
import * as activityModal from './activity-modal';
import * as historyModal from './history-modal';
import * as onboardingModal from './onboarding-modal';
import * as sessionsModal from './sessions-modal';
import * as settingsModal from './settings-modal';
import * as menu from './menu';

// Phase P1 hazard guard. Every shortcut this module owns is a BARE letter
// (S/A/H/W/G/M/P) with no modifier, which was harmless while the app had no text
// input — and became a real bug the moment the onboarding modal added one:
// typing the name "sasha" would fire [S], [A], [S], [H], [A] and open three
// modals over the input the user was typing into.
//
// So: a keydown whose target is a text-entry element belongs to that
// element, full stop. This is checked FIRST, before even the modal
// ownership tiers below, and deliberately covers input/textarea/select and
// anything contenteditable rather than just the one id that exists today,
// so the next feature that adds a field inherits the fix instead of
// rediscovering the bug. Esc is not special-cased: a native <dialog>
// handles Esc itself, above this listener, so returning early here never
// traps anyone.
function isTextEntryTarget(target: EventTarget | null): boolean {
  const node = target as HTMLElement | null;
  if (!node || !node.tagName) return false;
  const tag = node.tagName.toUpperCase();
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || node.isContentEditable === true;
}

export function init(): void {
  document.addEventListener('keydown', function (e: KeyboardEvent) {
    if (isTextEntryTarget(e.target)) return;
    // Onboarding takes the ownership tier first: it is modal over
    // everything and must not let a bare letter reach the launchers even
    // when focus sits on one of its buttons rather than its input.
    if (onboardingModal.isOpen()) {
      onboardingModal.handleKeydown(e);
      return;
    }
    if (storeModal.isOpen()) {
      storeModal.handleKeydown(e);
      return;
    }
    if (activityModal.isOpen()) {
      activityModal.handleKeydown(e);
      return;
    }
    if (historyModal.isOpen()) {
      historyModal.handleKeydown(e);
      return;
    }
    // Sessions (Phase P2) owns an input (the project name field) just
    // like onboarding does, so it claims this tier even though its own
    // handleKeydown only handles [W] — "presence is the point", the same
    // reasoning onboarding-modal.ts's handleKeydown comment records.
    if (sessionsModal.isOpen()) {
      sessionsModal.handleKeydown(e);
      return;
    }
    // Settings (SET-1) owns an input too — the rename field — so it claims
    // this tier for exactly the reason Sessions and onboarding do: while it
    // is open, a bare letter must not reach a launcher even when focus sits
    // on one of its toggle buttons rather than on the field.
    if (settingsModal.isOpen()) {
      settingsModal.handleKeydown(e);
      return;
    }
    if (menu.isOpen() && e.key === 'Escape') {
      e.preventDefault();
      menu.close();
      return;
    }
    if (e.key === 's' || e.key === 'S') { e.preventDefault(); menu.close(); storeModal.open(); }
    else if (e.key === 'Tab') { e.preventDefault(); menu.close(); storeModal.open(); }
    else if (e.key === 'a' || e.key === 'A') { e.preventDefault(); menu.close(); activityModal.open(); }
    else if (e.key === 'h' || e.key === 'H') { e.preventDefault(); menu.close(); historyModal.open(); }
    else if (e.key === 'w' || e.key === 'W') { e.preventDefault(); menu.close(); sessionsModal.open(); }
    else if (e.key === 'g' || e.key === 'G') { e.preventDefault(); menu.close(); settingsModal.open(); }
    else if (e.key === 'm' || e.key === 'M') { e.preventDefault(); menu.toggle(); }
  });
}
