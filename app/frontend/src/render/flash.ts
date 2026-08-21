// RENDER layer — the flash toast (server "flash" messages, ui-spec.md
// §4.4). Positioned over #store-cash while the store dialog is open, else
// over #sprint-name. Reads the <dialog>'s own native `.open` — a DOM
// query, not a reach into the store feature's internals — to decide
// where to float the toast, exactly as the F1 monolith did.
import { byId } from '../dom';
import type { FlashMessage } from '../wire';

const flash = byId<HTMLDivElement>('flash');
const storeCashBox = byId('store-cash');
const storeDialog = byId<HTMLDialogElement>('store');

let flashTimer: ReturnType<typeof setTimeout> | undefined;
export function showFlash(msg: FlashMessage): void {
  clearTimeout(flashTimer);
  flash.textContent = msg.text || '';
  flash.className = 'visible kind-' + (msg.kind || 'equip');
  if (storeDialog.open) {
    flash.style.left = '384px';
    flash.style.top = '64px';
    flash.style.width = '200px';
    flash.style.textAlign = 'right';
  } else {
    flash.style.left = '18px';
    flash.style.top = '332px';
    flash.style.width = '288px';
    flash.style.textAlign = 'left';
  }
  flashTimer = setTimeout(function () {
    flash.classList.remove('visible');
  }, 1500);
}

// Client-side instant "you can't afford this" feedback (ui-spec.md §4.4):
// flash #store-cash to var(--pot) for 400ms and back. No text change.
export function flashInsufficientFunds(): void {
  storeCashBox.classList.add('flash-insufficient');
  setTimeout(function () { storeCashBox.classList.remove('flash-insufficient'); }, 400);
}
