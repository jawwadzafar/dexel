// RENDER layer — INTERACTION-HARDENING's script half (docs/plan/ROADMAP.md):
// "Sprites must not be draggable (no drag-image-out-to-new-window), scene
// text not selectable; clicks deliberate."
//
// Most of that job is CSS (game.css, "Interaction hardening"): `user-select:
// none` on the layout root with `text` restored on real inputs, and
// `-webkit-user-drag: none` on images. Two things CSS cannot do, and they
// live here.
//
// 1. THE DRAGSTART BACKSTOP. `draggable=false` (dom.ts's spriteImg) plus
//    `-webkit-user-drag: none` between them stop every sprite from being a
//    drag source, which is the case that actually bit us: dragging a sprite
//    and dropping it back onto the window NAVIGATED the webview to
//    /assets/<file>.png, replacing the running game with a bare image and no
//    way back but a reload. But those two only cover elements we authored.
//    A `dragstart` that originates somewhere unforeseen — a link NES.css
//    renders, a text run in a browser whose user-select handling differs, a
//    future contributor's <img> built without spriteImg — would still be
//    able to do it. One capturing listener that cancels dragstart on the
//    whole document closes the class of bug rather than the instances:
//    a cancelled dragstart cannot produce a drop, and a drop is the only way
//    the navigation happens.
//
//    It deliberately does NOT cancel drags that start inside a real input.
//    Dragging selected text out of the name field is ordinary text editing
//    and this frontend has no business breaking it.
//
// 2. THE MIDDLE-CLICK / IMAGE-CONTEXT ESCAPE. A middle click on an <img>
//    inside a link, and the browser's own "open image in new tab", both end
//    at a navigation away from a single-page app that has a live WebSocket
//    and unsaved modal state. There are no <a> elements in this layout today
//    (checked: index.html has none, and no module creates one), so there is
//    nothing to intercept and nothing here pretends to. This comment exists
//    so the next person who adds a link knows it is a decision, not an
//    oversight.
//
// WHAT THIS MUST NOT BREAK, and how it is kept true: clicks. The scene
// container has to keep receiving them cleanly — the brief calls that out as
// the groundwork for SCENE-REACTIONS. So nothing here touches `click`,
// `mousedown`, `pointerdown` or `pointer-events`; the only events cancelled
// are `dragstart` and `selectstart`, neither of which a click depends on. In
// particular `mousedown` is left alone on purpose: cancelling mousedown is
// the common shortcut for "no selection" and it also cancels focus, which
// would break every button and input in the app.

// SCENE-REACTIONS, and why the click routing is NOT here. The groundwork above
// kept the scene receiving clicks; the feature that uses them puts one
// invisible hit region per clickable item INSIDE #scene-sprites and listens on
// it (render/scene.ts, "SCENE-REACTIONS"). That belongs there rather than in
// this module for two reasons. The regions are scene GEOMETRY — room rects out
// of geometry.ts, children of the scene surface, built once with the rest of
// the scene skeleton and shown/hidden by the same render pass that decides
// whether the buddy slot has a buddy in it — and their listeners' only effect
// is to queue a frame on the scene's own reaction scheduler. Routing them
// through here would mean a second module holding scene coordinates and
// reaching into the compositor's state, for no gain: there is no global click
// policy to enforce, and this file's whole job is the two document-level
// guards below. What stays true either way is the rule stated above — nothing
// in this module touches `click`, `pointer-events`, or `mousedown`, so it
// cannot break the reactions, and the reactions cannot weaken the guards.

// Inputs are the one place the user is meant to be able to select, edit and
// drag text. Kept as a predicate rather than a selector string so it also
// covers a focused contenteditable if one is ever added.
function isTextEntry(node: EventTarget | null): boolean {
  if (!(node instanceof Element)) return false;
  const el = node.closest('input, textarea, [contenteditable="true"]');
  return el !== null;
}

export function initInteractionGuards(): void {
  // Capturing, so it runs before anything downstream could act on the drag,
  // and on `document` so it covers the top layer too — an open <dialog> is
  // promoted out of #root's paint order but is still a descendant of the
  // document, which is why this is not bound to #root.
  document.addEventListener(
    'dragstart',
    function (e: DragEvent) {
      if (isTextEntry(e.target)) return;
      e.preventDefault();
    },
    true
  );

  // `user-select: none` already stops a selection from forming in every
  // browser this ships to. This is the same backstop reasoning as above:
  // selectstart is the event a selection has to go through, so cancelling it
  // outside inputs makes "the scene text is not selectable" true even if a
  // CSS rule is ever narrowed by accident. Cheap — it fires only on a real
  // selection attempt, never on an ordinary click.
  document.addEventListener(
    'selectstart',
    function (e: Event) {
      if (isTextEntry(e.target)) return;
      e.preventDefault();
    },
    true
  );
}
