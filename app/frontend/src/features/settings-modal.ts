// FEATURE/LOGIC layer — the Settings modal (SET-1, docs/ui-spec.md §11).
// Owns every DOM node under #settings and is the only module that sends
// SET_PREF. It also sends SET_NAME, which onboarding-modal.ts already
// sends — deliberately the SAME action, not a second rename path: the
// server's validation, its write-through and its `welcome` flash are
// already there, so renaming from here needed no new wire at all
// (docs/ui-spec.md §6.2: "SET_NAME is not restricted to onboarding: a
// later Settings surface can rename with the same action and no new
// wire").
//
// Four sections, one job each (§11.2, extended by SOUND-1 §13):
//   - NAME     — the current name, an input, SAVE NAME.
//   - WINDOW   — the always-on-top preference.
//   - SOUND    — the sound-effects preference, plus the two lines that say
//                exactly what it does and does not make noise for.
//   - PRIVACY  — the show-away-time preference, plus the one honest line
//                that explains it.
//
// GATES NOTHING. It owns a text input, which is the trigger
// add-a-menu-modal §4 asks about — but the answer here is the same one
// P2-design.md §2.4 already gave for the Sessions modal's project-name
// field: the store's gate exists because keyboard-driven SHOPPING is a
// self-feeding money loop, and there is no economy path in here at all
// (no purchase, no equip, no cash). Typing a name is a real keystroke on
// the user's real machine and is honestly counted as one, exactly as
// pressing [G] to get here is.
//
// EVERY CONTROL RENDERS FROM SERVER STATE. The three toggles read
// state.config.{alwaysOnTop,showAwayTime,soundEnabled} and never a
// locally-remembered position, so a second tab, a hand edit of config.json
// plus a restart, or a rejected action all leave the buttons telling the
// truth. The one exception is the name INPUT, which holds an in-progress
// edit — see render()'s comment for why that is a draft and not a claim.
//
// soundEnabled is the first preference here whose default is ON, and the
// only place that shows up in this file is the `!== false` inside
// soundEnabledIn() below: an absent field means "never chosen", and the
// honest render of never-chosen is the default. Painting it as OFF — which
// `!!state.config.soundEnabled` would do — would tell the user they had
// muted something they had not. It is also why #settings-sound's markup
// ships reading ON while the other two ship OFF: the pre-render label has
// to be the default too, or the modal flashes a lie for one frame.
import { byId } from '../dom';
import { enableClickAwayDismiss } from './modal-dismiss';
import * as store from '../state/store';
import { sendAction } from '../state/ws-client';
import { truncate } from '../format';

const el = {
  dialog: byId<HTMLDialogElement>('settings'),
  openBtn: byId<HTMLButtonElement>('settings-open'),
  close: byId<HTMLButtonElement>('settings-close'),
  scrim: byId<HTMLDivElement>('scrim'),

  nameCurrent: byId<HTMLDivElement>('settings-name-current').querySelector('.value') as HTMLElement,
  nameInput: byId<HTMLInputElement>('settings-name'),
  nameSave: byId<HTMLButtonElement>('settings-name-save'),

  onTopBtn: byId<HTMLButtonElement>('settings-ontop'),
  soundBtn: byId<HTMLButtonElement>('settings-sound'),
  awayBtn: byId<HTMLButtonElement>('settings-away')
};

// The width the CURRENTLY row renders, and it matches game.MaxNameLen
// (app/internal/game/identity.go) — the SERVER's cap — deliberately, NOT
// #settings-name's `maxlength`, which is the tighter 12 that keeps the
// status line able to name the frontmost app (see that input's comment in
// index.html). config.json is hand-editable by design (ADR 0014), so a name
// longer than the input allows is a legal thing to be shown here; this is
// the length at which showing it stops and truncation begins. A courtesy
// either way: the SERVER truncates and validates (docs/ui-spec.md §6.2).
const MAX_NAME_LEN = 24;

export function isOpen(): boolean { return el.dialog.open; }

// paintToggle is the whole two-state control: ONE button whose label and
// colour are a pure function of the server's value, the store modal's
// §4.3 one-action-button idea reduced to its smallest case. There is no
// third state to render and no "pending" look — a SET_PREF is answered by
// a full `state` broadcast within the same round trip, and pretending
// otherwise would be the optimistic local edit this app's contract
// forbids.
function paintToggle(btn: HTMLButtonElement, on: boolean): void {
  btn.textContent = on ? 'ON' : 'OFF';
  btn.classList.toggle('is-on', on);
  // Announced, not just coloured: the label alone reads identically to a
  // sighted and an assistive reader, but the pressed-ness is what a
  // toggle actually means.
  btn.setAttribute('aria-pressed', on ? 'true' : 'false');
}

// Full re-render from the central store. Safe to call while closed (it
// only writes to hidden nodes).
//
// DELIBERATELY DOES NOT TOUCH #settings-name. This runs on every ~1 Hz
// `state` broadcast, and re-seeding the input from the server on each one
// would delete whatever the user was halfway through typing. The input is
// a DRAFT — it is seeded once, on open() — and the server's actual name
// is rendered separately, on its own line, so nothing on screen ever
// claims a name the server did not send.
export function render(): void {
  const state = store.getState();
  const config = (state && state.config) || null;
  const name = (config && config.name) || '';
  // An unnamed dexel renders nothing rather than a placeholder.
  el.nameCurrent.textContent = name ? truncate(name, MAX_NAME_LEN) : '';
  paintToggle(el.onTopBtn, !!(config && config.alwaysOnTop));
  // SOUND-1: read through the same default-aware helper the click handler
  // uses, so the painted position and the value a click inverts can never
  // disagree about what "absent" means.
  paintToggle(el.soundBtn, soundEnabledIn(config));
  paintToggle(el.awayBtn, !!(config && config.showAwayTime));
}

export function refreshIfOpen(): void {
  if (el.dialog.open) render();
}

export function open(): void {
  if (el.dialog.open) return;
  render();
  // Seed the draft from the server's current name — this is the "show the
  // current name" half of the rename requirement, and it makes the common
  // case (a small edit to an existing name) a keystroke rather than a
  // retype. Done HERE, on open, and nowhere else: see render().
  const state = store.getState();
  el.nameInput.value = (state && state.config && state.config.name) || '';
  el.dialog.showModal();
  el.scrim.classList.add('visible');
  // Unlike the onboarding and Sessions modals, focus is NOT forced into
  // the input: this modal is three sections and the name is only one of
  // them, so grabbing the caret would make [G]/Esc and the two toggle
  // buttons unreachable from the keyboard until the user clicked away.
}

export function close(): void {
  if (!el.dialog.open) return;
  el.dialog.close();
}

// The one close path every dismissal funnels through — X, [G], Esc — the
// add-a-menu-modal rule ("hang your cleanup on the dialog's own 'close'
// event"). Nothing is pending at close time: every control here commits
// on its own click, so an abandoned draft in the input is simply
// discarded, which is what an un-saved text field means everywhere else.
el.dialog.addEventListener('close', function () {
  el.scrim.classList.remove('visible');
});
el.openBtn.addEventListener('click', open);
el.close.addEventListener('click', close);
enableClickAwayDismiss(el.dialog, close);

// ---------------------------------------------------------------------
// rename — the EXISTING SET_NAME action
// ---------------------------------------------------------------------
// The client trims, and sends nothing at all for an empty result, purely
// so it never fires an action it KNOWS the server will reject
// (game.NormalizeName rejects an empty name outright — docs/ui-spec.md
// §6.2). Everything else is the server's call: control characters, the
// 24-rune cap, and the `welcome` flash all come back from it. The modal
// stays OPEN afterwards — a settings panel is a place you are, not a
// question you answered — and the name line above re-renders from the
// broadcast the server sends back.
function submitName(): void {
  const name = el.nameInput.value.trim();
  if (!name) return;
  sendAction({ action: 'SET_NAME', name: name });
}
el.nameSave.addEventListener('click', submitName);
// Enter in the field saves. Bound HERE, on the input — the global router
// (keybindings.ts) deliberately never sees keys aimed at a text field,
// which is also what lets a name containing "g" be typed in peace.
el.nameInput.addEventListener('keydown', function (e: KeyboardEvent) {
  if (e.key === 'Enter') {
    e.preventDefault();
    submitName();
  }
});

// ---------------------------------------------------------------------
// preferences — SET_PREF
// ---------------------------------------------------------------------
// Each click reads LIVE state at the moment of the click and sends the
// OPPOSITE of what the server currently says — never the opposite of what
// this button last painted (the same rule menu.ts's pause button follows).
// So a stale render, or a value changed in another tab, can never make a
// click flip the wrong way.
function currentAlwaysOnTop(): boolean {
  const state = store.getState();
  return !!(state && state.config && state.config.alwaysOnTop);
}
function currentShowAwayTime(): boolean {
  const state = store.getState();
  return !!(state && state.config && state.config.showAwayTime);
}
// soundEnabledIn is the ONE reading of soundEnabled in this module, shared by
// render() and the click handler. `!== false`, not `!!`: see this file's
// header, and render/audio.ts, which gates playback on the identical rule —
// the button and the speaker must agree about the default or the toggle is a
// lie in one direction.
function soundEnabledIn(config: { soundEnabled?: boolean } | null): boolean {
  return !!config && config.soundEnabled !== false;
}
function currentSoundEnabled(): boolean {
  const state = store.getState();
  return soundEnabledIn((state && state.config) || null);
}
el.onTopBtn.addEventListener('click', function () {
  sendAction({ action: 'SET_PREF', key: 'alwaysOnTop', value: !currentAlwaysOnTop() });
});
el.soundBtn.addEventListener('click', function () {
  sendAction({ action: 'SET_PREF', key: 'soundEnabled', value: !currentSoundEnabled() });
});
el.awayBtn.addEventListener('click', function () {
  sendAction({ action: 'SET_PREF', key: 'showAwayTime', value: !currentShowAwayTime() });
});

// handleKeydown gives this modal the keyboard-ownership tier every other
// modal has, so a bare S/A/H/W/M/P can never reach a launcher while it is
// open — not even when focus sits on one of its buttons. [G] closes,
// exactly as [H] closes #history. Esc is deliberately NOT intercepted:
// native <dialog> closes, and the 'close' handler above resolves it.
export function handleKeydown(e: KeyboardEvent): void {
  switch (e.key) {
    case 'g': case 'G': close(); break;
    default: break; // Esc: native <dialog> behaviour, not intercepted
  }
}
