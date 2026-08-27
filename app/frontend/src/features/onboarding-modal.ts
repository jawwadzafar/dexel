// FEATURE/LOGIC layer — the onboarding modal (Phase P1 "Identity & first
// minutes", docs/plan/PRODUCT-EVOLUTION.md §5/§2.9, docs/ui-spec.md §7).
// Owns every DOM node under #onboarding and is the only module that sends
// SET_NAME.
//
// Two things make this modal different from the other three, and both are
// deliberate:
//
// 1. NOBODY OPENS IT. There is no button and no key. It opens because the
//    server said `onboarding: true` — which it only does on a genuine
//    fresh install (no save existed at boot AND config.json has no name)
//    — and it closes because a later `state` said false. The client never
//    decides that flag, in either direction (add-a-menu-modal's "the
//    server is the sole source of truth").
//
// 2. IT IS THE APP'S FIRST TEXT INPUT, which is a real hazard: the global
//    keyboard router (./keybindings.ts) binds bare S/A/H/M, so without a
//    guard, typing a name like "sasha" would open the store twice and the
//    activity log once. That guard lives in keybindings.ts (it must, so
//    it protects every future input too) in TWO layers: this modal takes
//    the modal-ownership tier while it is open, and the router
//    additionally ignores any keydown whose target is an input/textarea/
//    contenteditable.
//
// STORE-2.0 (docs/plan/ROADMAP.md §STORE-2.0): the starter-colour swatch
// picker is GONE. Colours are ordinary items now, and the only FREE hoodie
// colour is the tier-0 default the Dexel already wears — there is nothing to
// pick for free, so this modal collapses to a name and a portrait of the
// Dexel as it currently looks (which keeps it to the ~30-second budget
// PRODUCT-EVOLUTION.md §2.9's own risk guard demands). It sends only
// SET_NAME: no equip, no spend, no economy path at all.
import { byId } from '../dom';
import { registerModal } from './modal-dismiss';
import * as store from '../state/store';
import { sendAction } from '../state/ws-client';
import { DEV_RECT, DEV_Z_BASE, DEV_Z_FORM, DEV_Z_STYLE } from '../geometry';
import { buildTintLayer, plainImg, positionEl } from '../render/tint';
import { colourHexForItem, hoodieOverlayFile } from '../colours';
import type { CatalogItem } from '../wire';

// The slot the portrait shows — the hoodie the Dexel is already wearing.
const STARTER_SLOT = 'hoodie';

// The name SKIP applies. Pinned to game.DefaultName on the Go side (see
// internal/game/identity.go and its TestDefaultNameIsAValidName), and
// documented in docs/ui-spec.md §7.3.
//
// WHY skip SETS a name instead of just closing: the server keeps
// onboarding:true until a name exists, and this module re-opens on any
// state that says true. A skip that left the flag up would therefore
// re-open the modal on the very next 1 Hz broadcast — a nag loop — and a
// skip that suppressed the modal locally forever would be the client
// asserting state the server never sent. Naming the Dexel "dexel" is the
// only option that is both honest and quiet: the user opted out of
// CHOOSING a name, not into being asked again.
const SKIP_NAME = 'dexel';

const el = {
  dialog: byId<HTMLDialogElement>('onboarding'),
  scrim: byId<HTMLDivElement>('scrim'),
  preview: byId<HTMLDivElement>('onboarding-preview'),
  nameInput: byId<HTMLInputElement>('onboarding-name'),
  confirm: byId<HTMLButtonElement>('onboarding-confirm'),
  skip: byId<HTMLButtonElement>('onboarding-skip')
};

// awaitingAck suppresses the auto-open between "we sent SET_NAME" and
// "the server's next state confirms onboarding is false". Without it, the
// 1 Hz broadcast that happens to be in flight while our SET_NAME is being
// applied would re-open a modal the user just answered — the same class
// of one-shot loop guard as ws-client.ts's storeReassertSent. It suppresses
// an ACTION, never a render: nothing on screen claims a state the server
// did not send.
let awaitingAck = false;

// nameSubmitted marks "this close was a deliberate confirm/skip", so the
// dialog's single 'close' handler (which every dismissal path funnels
// through — button, Esc, anything future) can tell an answered modal from
// a bare Esc and only synthesize the skip for the latter.
let nameSubmitted = false;

export function isOpen(): boolean { return el.dialog.open; }

function hoodieItem(): CatalogItem | undefined {
  const state = store.getState();
  const eq = state && state.equipped && state.equipped[STARTER_SLOT];
  const equippedItem = eq ? store.getItemById(eq.itemId) : undefined;
  // On a fresh install the equipped hoodie IS the free tier-0 one; fall
  // back to the slot's free default so a missing/unknown equipped id can
  // never leave this modal with nothing to show.
  return equippedItem || store.freeDefaultItem(STARTER_SLOT);
}

// The portrait. Composes the REAL developer sprite stack — the same
// three layers, the same z-order and the same 2x scale render/scene.ts
// uses (#scene-sprites is scale(2)) — then lets #onboarding-preview's
// overflow:hidden crop it to the hood and shoulders. So the colour the
// Dexel wears is literally the colour the game shows, at the exact pixel
// size the game will show it at.
//
// PORTRAIT_CROP is in sprite-local (1x) coordinates, chosen from the
// sprites' own alpha bounds rather than guessed: dev_base_idle occupies
// x 46..146, y 7..76 inside DEV_RECT's 192x76 canvas. x 44..148 therefore
// leaves 2px of air on each side of the widest part of the figure, and
// y 5..57 frames the hands, hood and shoulders and cuts off just above
// local y 58 = room row 150 — exactly where chairs begin painting, so
// this crop looks complete with no chair drawn (and none is).
//
// PORTRAIT_FRAME is fixed to 'idle' rather than following the live pose:
// the 'mouse' pose reaches out to x190 and would clip against the right
// edge of this crop, and a portrait that changes pose under the user
// mid-decision is noise, not life. The LIVE Dexel is the one in the scene;
// this is a still.
const PORTRAIT_CROP = { left: 44, top: 5, w: 104, h: 52 };
const PORTRAIT_SCALE = 2;
const PORTRAIT_FRAME = 'idle';

function renderPreview(item: CatalogItem | undefined): void {
  el.preview.replaceChildren();

  // One 2x-scaled sprite-space canvas, shifted so PORTRAIT_CROP's
  // top-left lands at the box's top-left. Integer scale and integer
  // offsets only — a fractional one would resample the pixel art.
  const canvas = document.createElement('div');
  canvas.style.position = 'absolute';
  canvas.style.left = (-PORTRAIT_CROP.left * PORTRAIT_SCALE) + 'px';
  canvas.style.top = (-PORTRAIT_CROP.top * PORTRAIT_SCALE) + 'px';
  canvas.style.width = (DEV_RECT.w * PORTRAIT_SCALE) + 'px';
  canvas.style.height = (DEV_RECT.h * PORTRAIT_SCALE) + 'px';
  el.preview.appendChild(canvas);

  const rect = { left: 0, top: 0, w: DEV_RECT.w * PORTRAIT_SCALE, h: DEV_RECT.h * PORTRAIT_SCALE };
  const frame = PORTRAIT_FRAME;

  // STORE-2.0: colour comes from the equipped hoodie's id, tinting the
  // shared grayscale form; the style overlay is derived from the same id.
  const form = buildTintLayer('dev_form_' + frame + '.png', colourHexForItem(item && item.id));
  positionEl(form, rect);
  form.style.zIndex = String(DEV_Z_FORM);
  canvas.appendChild(form);

  if (item) {
    const style = plainImg(hoodieOverlayFile(item.id), rect);
    style.style.zIndex = String(DEV_Z_STYLE);
    canvas.appendChild(style);
  }

  const base = plainImg('dev_base_' + frame + '.png', rect);
  base.style.zIndex = String(DEV_Z_BASE);
  canvas.appendChild(base);
}

// Full re-render of the modal's contents from the central store. Safe to
// call while closed (it just writes to hidden nodes).
export function render(): void {
  renderPreview(hoodieItem());
}

export function refreshIfOpen(): void {
  if (el.dialog.open) render();
}

// syncWithServer is the ONLY thing that opens or closes this modal, and
// main.ts calls it on every `state`. Both directions are the server's
// call: open on onboarding:true, close on onboarding:false. A pre-P1
// server sends neither field, which reads as false — correctly, since
// such a server has no SET_NAME handler to answer with.
export function syncWithServer(): void {
  const state = store.getState();
  const wanted = !!(state && state.onboarding);

  if (!wanted) {
    // The server has a name (ours, or one set in another tab / by hand in
    // config.json). Whatever we were waiting for has happened.
    awaitingAck = false;
    if (el.dialog.open) {
      nameSubmitted = true; // an externally-satisfied close is not a skip
      el.dialog.close();
    }
    return;
  }
  if (el.dialog.open || awaitingAck) return;
  open();
}

function open(): void {
  if (el.dialog.open) return;
  nameSubmitted = false;
  render();
  el.dialog.show();
  el.scrim.classList.add('visible');
  // Focus the input so the very first keystroke is the name — the
  // ~30-second budget (PRODUCT-EVOLUTION.md §2.9's guard) has no room
  // for a click first.
  el.nameInput.value = '';
  el.nameInput.focus();
}

// submit sends only the name. The Dexel already wears its free tier-0
// loadout (GrantTierZeroDefaults on the server), so there is no colour to
// equip and no spend — STORE-2.0 removed the starter-colour path.
function submit(rawName: string): void {
  // The server does the real validation (game.NormalizeName: trim, strip
  // control chars, cap at 24 runes, reject empty). This trim + the empty
  // fallback exist so the client never sends something it KNOWS would be
  // rejected, which would leave onboarding:true and re-open the modal.
  const name = rawName.trim() || SKIP_NAME;
  nameSubmitted = true;
  awaitingAck = true;
  sendAction({ action: 'SET_NAME', name: name });
  if (el.dialog.open) el.dialog.close();
}

el.confirm.addEventListener('click', function () { submit(el.nameInput.value); });
el.skip.addEventListener('click', function () { submit(SKIP_NAME); });

// Enter in the name field confirms. Bound HERE, on the input, rather than
// in the global router — the router deliberately never sees keys aimed at
// an input (see this file's header note 2).
el.nameInput.addEventListener('keydown', function (e: KeyboardEvent) {
  if (e.key === 'Enter') {
    e.preventDefault();
    submit(el.nameInput.value);
  }
});

// The one close path every dismissal funnels through (add-a-menu-modal's
// "hang your cleanup on the dialog's own 'close' event"). A close that was
// NOT a deliberate confirm/skip is a bare Esc — treat it as a skip so the
// server gets a name and this modal never asks twice. Never closes into a
// broken state: there is no path out of here that leaves the Dexel
// nameless.
el.dialog.addEventListener('close', function () {
  el.scrim.classList.remove('visible');
  if (!nameSubmitted) {
    nameSubmitted = true;
    awaitingAck = true;
    sendAction({ action: 'SET_NAME', name: SKIP_NAME });
  }
});

// DRAG-1 — this modal is now non-modal (dialog.show()) like every other, so
// Esc no longer closes it natively. Register with the shared helper so Esc
// still closes it (which the 'close' handler above turns into the skip). It
// opts OUT of click-away (`clickAway: false`): onboarding must be answered or
// skipped, never clicked away — which is why it never wired the old
// per-dialog click-away either.
registerModal(el.dialog, {
  close: function () { if (el.dialog.open) el.dialog.close(); },
  clickAway: false
});

// handleKeydown gives this modal the keyboard-ownership tier the other
// three modals have, so bare S/A/H/M can never reach the global router
// while it is open — not even when focus is on a button rather than the
// input. Esc is deliberately NOT handled here: the shared modal helper
// (registerModal above) closes on Esc, and the 'close' handler above turns
// that into the skip.
export function handleKeydown(_e: KeyboardEvent): void {
  /* intentionally inert — presence is the point (see above) */
}
