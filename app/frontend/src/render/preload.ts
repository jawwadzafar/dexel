// RENDER layer — sprite pre-warming. Half of the BUG-1 flicker fix (the
// other half is keeping element identity; see render/scene.ts's header).
//
// WHY THIS EXISTS. Keeping the scene's elements alive means a frame swap is
// a `src` change on an element that is already on screen rather than a new
// element — but a `src` change still has to hand the compositor a DECODED
// bitmap. Chrome decodes lazily and ASYNCHRONOUSLY: the first paint after a
// swap to a not-yet-decoded image draws nothing for that image and schedules
// the decode, so the layer is missing for one or more frames even though the
// PNG itself came straight out of the HTTP cache. At 200ms per animation
// tick that reads exactly as "the character blinks on and off for
// milliseconds".
//
// So: fetch AND `HTMLImageElement.decode()` every sprite the scene can ever
// show, once, at startup. After that every swap draws from a bitmap the
// renderer already has. The whole asset directory is 368 KB across 92 files
// (`du -sh app/assets`), all served from the same embedded FileServer this
// page was loaded from, so this is a few hundred KB of one-time work on
// localhost — not a budget worth optimising against the flicker it removes.
//
// This is a warm-up, never a dependency: nothing waits on these promises and
// nothing renders differently if one fails. A genuinely missing assets/
// directory is still reported by exactly one path — the room_back.png
// sentinel listener the scene compositor attaches (render/overlays.ts's
// handleSpriteSentinelError) — so failures here are swallowed rather than
// turned into a second, competing diagnostic.
import { assetUrl } from '../assets';
import { SCENERY } from '../geometry';
import type { CatalogMessage } from '../wire';

const warmed: Record<string, true> = {};
// The warmed images are DELIBERATELY held for the life of the page. A
// detached Image with no references is collectable, and collecting it can
// take the decoded bitmap (and the cache entry keeping the bytes around)
// with it — which would quietly undo the warm-up this module exists for.
const held: HTMLImageElement[] = [];

function warm(file: string | null | undefined): void {
  const url = assetUrl(file);
  if (!url || warmed[url]) return;
  warmed[url] = true;
  const img = new Image();
  held.push(img);
  img.src = url;
  // decode() is what actually populates the DECODED-image cache; loading
  // alone only fills the byte cache, which is not the half that stalls a
  // frame. Guarded because it is the newer of the two APIs and this app
  // must degrade to "loaded but not pre-decoded", never throw.
  if (typeof img.decode === 'function') img.decode().catch(function () { /* warm-up only */ });
}

// Every scene sprite whose filename this frontend knows without the server:
// the room/desk/monitor scenery, and both halves of all nine developer
// animation frames (the hot path — BREATH_SCRIPT / STRETCH_SCRIPT /
// CELEBRATE_SCRIPT and the 5fps typing cycle all swap between these).
export function warmStaticSprites(devFrames: string[]): void {
  SCENERY.forEach(function (s) { warm(s.file); });
  devFrames.forEach(function (frame) {
    warm('dev_form_' + frame + '.png');
    warm('dev_base_' + frame + '.png');
  });
}

// Every sprite the CATALOG can put in the scene: the currently equipped item
// in each slot, and also every item the player might buy and equip next —
// equipping is a click away and the newly equipped sprite would otherwise be
// the one cold image left in the scene. Thumbnails are deliberately not
// warmed here: they are the store grid's business, not the scene's.
export function warmCatalogSprites(catalog: CatalogMessage): void {
  catalog.items.forEach(function (item) {
    warm(item.sprite);
    warm(item.detail);
  });
}
