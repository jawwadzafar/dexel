// Shared, centralized presentation-dismissal for every browsable modal
// (Store, Activity, History, Sessions, Settings, About, Onboarding). Wired
// ONCE here and registered from each modal's own file, rather than repeated
// per modal.
//
// DRAG-1 — WHY THE MODALS ARE NON-MODAL NOW.
//   The owner pain: with a modal open, the frameless desktop window's
//   #titlebar (the `data-tauri-drag-region` the shell drags the window by,
//   plus its menu / minimize / close buttons) was completely dead — the
//   window could not be moved until the modal closed. Root cause: the modals
//   were opened with `dialog.showModal()`, which promotes the dialog to the
//   TOP LAYER and makes the ENTIRE rest of the document `inert`. #titlebar is
//   not the dialog, so it went inert too, and an inert element receives no
//   pointer events at all — the Tauri drag script never saw a pointerdown.
//
//   The fix is to open every modal NON-modally with `dialog.show()`. A
//   non-modal dialog is not in the top layer and does NOT inert the page, so
//   #titlebar stays live and the window drags with a modal open. What
//   `showModal()` gave us for free then has to be provided by hand, and that
//   is what this module centralizes:
//
//     * BACKDROP DIM — never came from the dialog's ::backdrop anyway (it is
//       `background: transparent` in game.css); it comes from the shared
//       #scrim element, which each modal already toggles `.visible`. Unchanged.
//
//     * CLICK-AWAY — a click on the dimmed scene dismisses the modal. The
//       #scrim is the manual backdrop: `.visible` gives it `pointer-events`
//       (game.css) and it sits above the scene but below the panel
//       (z-index 400 vs the panel's 1000) and, crucially, starts at y=24 so
//       it never covers #titlebar. A click on it closes the open modal; a
//       click on the panel never reaches it; and because it is above the
//       scene, a click-away never leaks through into a scene reaction.
//
//     * ESC — a NON-modal dialog does NOT close on Esc natively (only
//       showModal() dialogs do). So Esc is handled here, in the CAPTURE phase
//       ahead of features/keybindings.ts, and only while a modal is open (so
//       the menu's own Esc-to-close still works when none is). No text-entry
//       guard: Esc must close the modal even from its name / rename input.
//
// A modal opts out of click-away (onboarding: it must be answered or
// skipped, not clicked away) via `clickAway: false`, and intercepts Esc (the
// store backs out of its preview overlay on the first Esc, closes on the
// second) via `onEscape`.
import { byId } from '../dom';

export interface ModalDismiss {
  // Close the modal. Every centralized dismissal path (scrim click, Esc)
  // funnels through this, as do the modal's own X / shortcut paths.
  close: () => void;
  // Optional Escape interceptor. Return true if Escape was CONSUMED without
  // closing (the store uses this to back out of its preview overlay first).
  // Return false / omit to let Escape close the modal.
  onEscape?: () => boolean;
  // Whether a click on the dimmed backdrop (#scrim) dismisses the modal.
  // Defaults to true; onboarding passes false.
  clickAway?: boolean;
}

interface Entry {
  dialog: HTMLDialogElement;
  close: () => void;
  onEscape?: () => boolean;
  clickAway: boolean;
}

const registry: Entry[] = [];
let wired = false;

// The shared dim backdrop. Lazily resolved so this module has no import-time
// dependency on the element existing yet.
let scrimEl: HTMLDivElement | null = null;
function scrim(): HTMLDivElement {
  if (!scrimEl) scrimEl = byId<HTMLDivElement>('scrim');
  return scrimEl;
}

// The scrim is a SHARED element toggled by whichever modal is up, so its
// visibility must be derived from "is any modal open?", never removed
// unconditionally by one modal's close: the <dialog> 'close' event is
// asynchronous, so an unconditional remove can fire AFTER the next modal has
// already opened and re-shown the scrim, hiding the backdrop (and its
// click-away) out from under a modal that is still open. Recomputing from the
// live open state instead is race-proof. Called synchronously on every
// dismissal (so the backdrop is gone the instant the last modal closes) and
// again on the 'close' event (covering the X / shortcut paths).
export function syncScrim(): void {
  scrim().classList.toggle('visible', openEntry() !== null);
}

export function registerModal(dialog: HTMLDialogElement, opts: ModalDismiss): void {
  registry.push({
    dialog: dialog,
    close: opts.close,
    onEscape: opts.onEscape,
    clickAway: opts.clickAway !== false
  });

  // FOCUS RESTORE — the one behaviour showModal() gave for free that show()
  // does not. A modal dialog restored focus to the element that had it before
  // opening; a non-modal one leaves focus wherever it was, which for the
  // modals that focus their name/rename input (onboarding, sessions) means
  // focus stays on that input AFTER the dialog closes. keybindings.ts then
  // treats every following keystroke as aimed at a text field and drops the
  // global shortcuts until the user clicks elsewhere.
  //
  // The Esc and click-away paths blur SYNCHRONOUSLY before closing (see
  // dismiss()) so the very next key is a global shortcut again. This 'close'
  // listener is the backstop for every OTHER close path (the X button, a
  // shortcut like the store's [S]) — its focus lands on a <button>, which
  // keybindings does not treat as a text field, so async is fine here. The
  // <dialog> 'close' event is queued, not synchronous, which is exactly why
  // the two keyboard/pointer dismissals cannot rely on it alone.
  dialog.addEventListener('close', function () { blurInside(dialog); syncScrim(); });

  wireOnce();
}

// Move focus off any control still inside `dialog` (see the focus-restore
// note in registerModal). No-op unless focus is actually trapped there.
function blurInside(dialog: HTMLDialogElement): void {
  const active = document.activeElement as HTMLElement | null;
  if (active && dialog.contains(active) && typeof active.blur === 'function') {
    active.blur();
  }
}

// Dismiss the open modal: blur any focused control inside it FIRST (so the
// next keystroke is a global shortcut, not one swallowed as text entry) then
// close. Used by the two paths that can dismiss an input-focused modal — Esc
// and the backdrop click.
function dismiss(entry: Entry): void {
  blurInside(entry.dialog);
  entry.close();      // sets dialog.open = false synchronously
  syncScrim();        // ...so the backdrop is gone this tick, not on the async 'close'
}

// The one modal currently open. The modals are mutually exclusive (only one
// is ever up at a time — keybindings.ts routes keys to the first open one),
// so a linear scan for `dialog.open` is enough and needs no open/close
// bookkeeping of its own.
function openEntry(): Entry | null {
  for (let i = 0; i < registry.length; i++) {
    if (registry[i].dialog.open) return registry[i];
  }
  return null;
}

function wireOnce(): void {
  if (wired) return;
  wired = true;

  const scrim = byId<HTMLDivElement>('scrim');

  // A press that BEGAN on the scrim is a genuine backdrop click; a text
  // selection that began inside a panel and drifted out onto the scrim is
  // not. Tracking the mousedown target guards the latter — the same guard
  // the old per-dialog click-away carried.
  let downOnScrim = false;
  scrim.addEventListener('mousedown', function (e: MouseEvent) {
    downOnScrim = e.target === scrim;
  });
  scrim.addEventListener('click', function (e: MouseEvent) {
    if (!downOnScrim) return;
    // A keyboard-activated control fires a synthetic click at detail 0 —
    // that is not a pointer landing on the backdrop.
    if (e.detail === 0) return;
    const entry = openEntry();
    if (entry && entry.clickAway) {
      e.stopPropagation();
      dismiss(entry);
    }
  });

  document.addEventListener('keydown', function (e: KeyboardEvent) {
    if (e.key !== 'Escape' && e.key !== 'Esc') return;
    const entry = openEntry();
    if (!entry) return; // no modal open — leave Esc to keybindings (menu, etc.)
    e.preventDefault();
    e.stopPropagation();
    // A consumed Esc (e.g. the store backing out of its preview) does not
    // close the modal.
    if (entry.onEscape && entry.onEscape()) return;
    dismiss(entry);
  }, true);
}
