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
// Gates nothing: it captures keyboard input, but unlike the store it
// cannot mint money, because the engine's economy gate is about ACCRUING
// work and this modal exists only before any work has been accrued — a
// fresh install with 0 Dev Cash and no sprint progress. (It also cannot
// spend: the starter colour is applied with EQUIP_ITEM against a tint the
// player already owns, never BUY_TINT. There is no free-cash path here
// and no new economy path at all.)
import { byId } from '../dom';
import * as store from '../state/store';
import { sendAction } from '../state/ws-client';
import { truncate } from '../format';
import { DEV_RECT, DEV_Z_BASE, DEV_Z_FORM, DEV_Z_STYLE } from '../geometry';
import { buildTintLayer, plainImg, positionEl, swatchColor } from '../render/tint';
import type { CatalogItem, CatalogTint } from '../wire';

// The slot the starter colour applies to. Phase P1 offers exactly one
// choice on exactly one slot — the hoodie the Dexel is already wearing —
// because "keep it to ~30 seconds... not a wizard"
// (PRODUCT-EVOLUTION.md §2.9's own risk guard).
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
  tintName: byId('onboarding-tint-name'),
  colourLabel: byId('onboarding-colour-label'),
  colourHint: byId('onboarding-colour-hint'),
  swatches: byId<HTMLDivElement>('onboarding-swatches'),
  confirm: byId<HTMLButtonElement>('onboarding-confirm'),
  skip: byId<HTMLButtonElement>('onboarding-skip')
};

// Local UI state. selectedTint is a pure UI selection (which chip is
// outlined, what colour the preview shows) — it becomes real state only
// when EQUIP_ITEM is sent and the server broadcasts it back.
let selectedTint: string | null = null;

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
  // never leave this modal with nothing to tint.
  return equippedItem || store.freeDefaultItem(STARTER_SLOT);
}

// The tint currently selected, defaulting to whatever the Dexel is
// already wearing (server truth), then to the item's own default tint.
function effectiveTint(item: CatalogItem | undefined): string | null {
  if (selectedTint) return selectedTint;
  const state = store.getState();
  const eq = state && state.equipped && state.equipped[STARTER_SLOT];
  if (eq && eq.tintId) return eq.tintId;
  return item ? item.defaultTint : null;
}

// The portrait. Composes the REAL developer sprite stack — the same
// three layers, the same z-order and the same 2x scale render/scene.ts
// uses (#scene-sprites is scale(2)) — then lets #onboarding-preview's
// overflow:hidden crop it to the hood and shoulders. So the colour the
// chip promises is literally the colour the Dexel is wearing, at the
// exact pixel size the game will show it at.
//
// PORTRAIT_CROP is in sprite-local (1x) coordinates, chosen from the
// sprites' own alpha bounds rather than guessed: dev_base_idle occupies
// x 46..146, y 7..76 inside DEV_RECT's 192x76 canvas. x 44..148 therefore
// leaves 2px of air on each side of the widest part of the figure, and
// y 5..57 frames the hands, hood and shoulders and cuts off just above
// local y 58 = room row 150 — exactly where chairs begin painting, so
// this crop looks complete with no chair drawn (and none is).
//
// PORTRAIT_FRAME is fixed to 'idle' rather than following
// currentDevFrame(): the 'mouse' pose reaches out to x190 and would clip
// against the right edge of this crop, and a portrait that changes pose
// under the user mid-decision is noise, not life. The LIVE Dexel is the
// one in the scene; this is a still.
const PORTRAIT_CROP = { left: 44, top: 5, w: 104, h: 52 };
const PORTRAIT_SCALE = 2;
const PORTRAIT_FRAME = 'idle';

function renderPreview(item: CatalogItem | undefined, tintId: string | null): void {
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

  const form = buildTintLayer('dev_form_' + frame + '.png', store.tintHexFor(tintId));
  positionEl(form, rect);
  form.style.zIndex = String(DEV_Z_FORM);
  canvas.appendChild(form);

  if (item && item.sprite) {
    const style = plainImg(item.sprite, rect);
    style.style.zIndex = String(DEV_Z_STYLE);
    canvas.appendChild(style);
  }

  const base = plainImg('dev_base_' + frame + '.png', rect);
  base.style.zIndex = String(DEV_Z_BASE);
  canvas.appendChild(base);

  // The chosen colour's own name, under the portrait — the store's
  // preview pane does the same thing (#store-preview-color), and it is
  // what makes a swatch row of six squares nameable.
  const tint = tintId ? store.getTintById(tintId) : undefined;
  el.tintName.textContent = tint ? truncate(tint.name, 24) : '';
}

// One chip per catalog tint, in catalog order (the frontend hardcodes
// nothing about the tint table). Ownership comes from the server
// (store.isTintOwned reads state.ownedTints + the item's implicit free
// default) — an unowned colour is shown SLASHED and inert rather than
// hidden, so the row reads as "this is your colour, and there are more to
// earn" instead of quietly shrinking to whatever a fresh install happens
// to grant. If a later catalog change makes more tier-0 tints free, this
// row lights up with zero code change here.
function renderSwatches(item: CatalogItem | undefined, tintId: string | null): void {
  const catalog = store.getCatalog();
  el.swatches.replaceChildren();
  if (!catalog || !item) return;
  let lockedCount = 0;
  catalog.tints.forEach(function (tint: CatalogTint) {
    const owned = store.isTintOwned(item, tint.id);
    if (!owned) lockedCount++;
    const chip = document.createElement('div');
    chip.className = 'swatch' + (owned ? '' : ' unowned') + (tint.id === tintId ? ' selected' : '');
    chip.style.background = swatchColor(tint.hex);
    chip.dataset.tint = tint.id;
    chip.title = owned ? tint.name : tint.name + ' — ' + tint.price + ' in the store';
    if (owned) {
      chip.addEventListener('click', function (ev) {
        ev.stopPropagation();
        selectedTint = tint.id;
        render();
      });
    }
    el.swatches.appendChild(chip);
  });
  el.colourLabel.textContent = 'STARTER COLOUR';
  // Said out loud, because on a real fresh install FIVE of the six are
  // locked (only the hoodie's own default tint is owned — see
  // internal/game/catalog.go: every tint in catalogTints costs 40, and
  // GrantTierZeroDefaults grants items, not tints). Without this line the
  // row reads as a broken picker; with it, it reads as a wardrobe you
  // have barely started.
  el.colourHint.textContent = lockedCount > 0 ? 'MORE TO EARN IN THE STORE' : '';
}

// Full re-render of the modal's contents from the central store. Safe to
// call while closed (it just writes to hidden nodes).
export function render(): void {
  const item = hoodieItem();
  const tintId = effectiveTint(item);
  renderSwatches(item, tintId);
  renderPreview(item, tintId);
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
  selectedTint = null;
  nameSubmitted = false;
  render();
  el.dialog.showModal();
  el.scrim.classList.add('visible');
  // Focus the input so the very first keystroke is the name — the
  // ~30-second budget (PRODUCT-EVOLUTION.md §2.9's guard) has no room
  // for a click first.
  el.nameInput.value = '';
  el.nameInput.focus();
}

// submit sends the starter colour first, then the name. Order matters
// only for the flash: SET_NAME's "Hello, <name>!" is the moment worth
// landing last, so it is the toast the user is left looking at.
//
// The colour is sent as EQUIP_ITEM against a tint the player ALREADY owns
// (renderSwatches only wires clicks on owned chips) — the existing equip
// path, no BUY_TINT, no new economy path. It is skipped entirely when the
// selection already matches what is equipped, so the common case sends
// exactly one action.
function submit(rawName: string): void {
  const item = hoodieItem();
  const tintId = effectiveTint(item);
  const state = store.getState();
  const eq = state && state.equipped && state.equipped[STARTER_SLOT];
  const alreadyWorn = !!eq && !!item && eq.itemId === item.id && eq.tintId === tintId;
  if (item && !alreadyWorn) {
    sendAction({ action: 'EQUIP_ITEM', slot: STARTER_SLOT, itemId: item.id, tintId: tintId });
  }

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

// handleKeydown gives this modal the keyboard-ownership tier the other
// three modals have, so bare S/A/H/M can never reach the global router
// while it is open — not even when focus is on a button rather than the
// input. Esc is deliberately NOT intercepted: native <dialog> closes, and
// the 'close' handler above turns that into the skip.
export function handleKeydown(_e: KeyboardEvent): void {
  /* intentionally inert — presence is the point (see above) */
}
