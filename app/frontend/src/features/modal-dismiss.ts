// Shared click-away dismissal for the browsable modals (Activity, Store,
// History, Sessions, Settings). Added ONCE here and wired from each modal's
// own file, rather than repeated five times.
//
// Owner pain this fixes: with a modal open, the frameless desktop window's
// titlebar (menu / minimize / close) is unreachable because the modal sits
// in the top layer and swallows the pointer. A click in the empty area
// outside the panel now dismisses it, so the next click reaches the
// titlebar again. Esc still closes (native <dialog> behaviour, untouched).
//
// WHY THE RECT TEST, NOT `event.target === dialog`:
//   The dialogs carry 12px of padding and lay their children out
//   absolutely, so there are empty gaps INSIDE the panel where a click's
//   target is the <dialog> element itself. The naive target check would
//   dismiss on those inner clicks. Comparing the pointer against the
//   panel's getBoundingClientRect() instead treats the whole panel box
//   (padding, border, gaps and all) as "inside" and only the backdrop area
//   beyond it as "outside".
//
//   It is also transform-proof: each modal is scaled by the shared
//   window-fit transform (BUG-2), and getBoundingClientRect() reports the
//   POST-transform box in the same viewport coordinate space as
//   clientX/clientY, so the test holds at 1x, 2x and every fractional
//   letterbox scale without any coordinate maths of its own.
//
// The opening click never reaches here: it targets a top-bar / menu button
// that is not an ancestor of the dialog, so it is not dispatched to the
// dialog's own click handler. A mousedown-started-inside guard additionally
// stops a text selection that begins in a name input and releases outside
// from being read as a click-away.
export function enableClickAwayDismiss(dialog: HTMLDialogElement, close: () => void): void {
  let downInside = false;

  dialog.addEventListener('mousedown', function (e: MouseEvent) {
    downInside = pointInDialog(dialog, e.clientX, e.clientY);
  });

  dialog.addEventListener('click', function (e: MouseEvent) {
    // A press that began inside the panel is never a click-away, even if the
    // release drifted onto the backdrop (e.g. dragging a text selection out).
    if (downInside) return;
    // Keyboard-activated controls fire a synthetic click at (0,0) with
    // detail 0 — that is not a pointer landing on the backdrop, so ignore it.
    if (e.detail === 0) return;
    if (!pointInDialog(dialog, e.clientX, e.clientY)) close();
  });
}

function pointInDialog(dialog: HTMLDialogElement, x: number, y: number): boolean {
  const r = dialog.getBoundingClientRect();
  return x >= r.left && x <= r.right && y >= r.top && y <= r.bottom;
}
