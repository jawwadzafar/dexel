#!/usr/bin/env node
// ---------------------------------------------------------------------------
// build-hero-sprites.mjs — bake the landing page's animated dexel hero frames
// from the game's CURRENT sprite art.
//
// WHY THIS EXISTS
//   The hero on the landing page shows a live, looping pixel animation of the
//   dexel character (the typing loop: type_a <-> type_b). That character is
//   never a single PNG in the game — it is COMPOSITED at runtime from three
//   grayscale/overlay layers (see app/frontend/src/render/scene.ts and
//   docs/art-direction.md "The CSS tint mechanism"):
//
//       1. dev_form_<frame>.png   grayscale garment "form", recoloured by the
//                                 equipped hoodie's tint (CSS mask + multiply)
//       2. hoodie_<style>.png     palette-pure style overlay, drawn untinted
//       3. dev_base_<frame>.png   skin / hair / hands, drawn on top
//
//   This script reproduces that exact composite in pure Node (no npm deps),
//   for the DEFAULT character a fresh install wears — the free tier-0
//   "classic indigo" hoodie — and bakes each animation frame to a single flat
//   PNG the page can cross-swap with trivial CSS. Because it reads straight
//   from app/assets/ every time it runs, and the Pages build (see
//   .github/workflows/pages.yml) runs it before deploying, the deployed hero
//   always reflects the newest art. Re-run it locally after any art change:
//
//       node site/tools/build-hero-sprites.mjs
//
//   Output: site/assets/sprites/dexel_frame_a.png, dexel_frame_b.png
//           (tightly cropped to the figure, both frames sharing ONE crop box
//            so they line up pixel-for-pixel when swapped).
//
// SOURCE OF TRUTH
//   - Layer stack + z-order:  app/frontend/src/render/scene.ts (DEV_Z_FORM <
//                             DEV_Z_STYLE < DEV_Z_BASE), colours.ts.
//   - Default hoodie is classic/indigo: app/frontend/src/dev/dev-fixtures.ts
//     (hoodie_classic_indigo, price 0, minLevel 0) -> overlay hoodie_classic.png.
//   - Tint hex table mirrors tools/gen_assets.py via colours.ts (indigo #6a5aa0).
//   If the art's layer contract changes, update the constants below.
// ---------------------------------------------------------------------------

import zlib from 'node:zlib';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(__dirname, '..', '..');          // repo root
const ASSETS = path.join(REPO, 'app', 'assets');           // READ ONLY
const OUT = path.join(REPO, 'site', 'assets', 'sprites');  // we own site/

// --- What to bake -----------------------------------------------------------
// The two typing frames the game cycles at ~5fps for the "coding" state.
const FRAMES = ['type_a', 'type_b'];
// Default equipped hoodie a fresh install wears (colours.ts / dev-fixtures.ts).
const HOODIE_STYLE = 'classic';
const HOODIE_TINT = '#6a5aa0'; // "indigo" (colours.ts, mirrors gen_assets.py)
const CROP_PAD = 2;            // px of transparent breathing room around figure

// ===========================================================================
// Minimal PNG codec (8-bit RGBA, non-interlaced) — no external dependencies.
// The game's sprites are all colortype 6, bit depth 8, interlace 0 (verified),
// so this handles exactly that shape and refuses anything else loudly.
// ===========================================================================

const PNG_SIG = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);

// CRC32 (PNG polynomial), table built once.
const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();
function crc32(buf) {
  let c = 0xffffffff;
  for (let i = 0; i < buf.length; i++) c = CRC_TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function paeth(a, b, c) {
  const p = a + b - c;
  const pa = Math.abs(p - a), pb = Math.abs(p - b), pc = Math.abs(p - c);
  if (pa <= pb && pa <= pc) return a;
  if (pb <= pc) return b;
  return c;
}

// Decode a PNG file -> { width, height, data: Buffer(w*h*4) straight RGBA }.
function decodePng(file) {
  const buf = fs.readFileSync(file);
  if (!buf.subarray(0, 8).equals(PNG_SIG)) throw new Error(`${file}: not a PNG`);
  let off = 8;
  let width = 0, height = 0, bitDepth = 0, colorType = 0, interlace = 0;
  const idat = [];
  while (off < buf.length) {
    const len = buf.readUInt32BE(off);
    const type = buf.toString('ascii', off + 4, off + 8);
    const body = buf.subarray(off + 8, off + 8 + len);
    if (type === 'IHDR') {
      width = body.readUInt32BE(0);
      height = body.readUInt32BE(4);
      bitDepth = body[8];
      colorType = body[9];
      interlace = body[12];
    } else if (type === 'IDAT') {
      idat.push(Buffer.from(body));
    } else if (type === 'IEND') {
      break;
    }
    off += 12 + len; // len + type(4) + data + crc(4)
  }
  if (bitDepth !== 8 || colorType !== 6 || interlace !== 0) {
    throw new Error(`${file}: unsupported PNG (bitDepth=${bitDepth} colorType=${colorType} interlace=${interlace}); expected 8/6/0`);
  }
  const raw = zlib.inflateSync(Buffer.concat(idat));
  const bpp = 4;
  const stride = width * bpp;
  const out = Buffer.alloc(height * stride);
  let prev = Buffer.alloc(stride); // all zero for the first row
  let ri = 0;
  for (let y = 0; y < height; y++) {
    const filter = raw[ri++];
    const line = raw.subarray(ri, ri + stride);
    ri += stride;
    const cur = out.subarray(y * stride, y * stride + stride);
    for (let x = 0; x < stride; x++) {
      const a = x >= bpp ? cur[x - bpp] : 0;   // left
      const b = prev[x];                        // up
      const c = x >= bpp ? prev[x - bpp] : 0;   // up-left
      let v = line[x];
      switch (filter) {
        case 0: break;
        case 1: v = (v + a) & 0xff; break;
        case 2: v = (v + b) & 0xff; break;
        case 3: v = (v + ((a + b) >> 1)) & 0xff; break;
        case 4: v = (v + paeth(a, b, c)) & 0xff; break;
        default: throw new Error(`${file}: bad filter ${filter} on row ${y}`);
      }
      cur[x] = v;
    }
    prev = cur;
  }
  return { width, height, data: out };
}

// Encode straight RGBA -> PNG Buffer using filter 0 (None) on every row.
function encodePng(width, height, data) {
  const stride = width * 4;
  const rawFiltered = Buffer.alloc((stride + 1) * height);
  for (let y = 0; y < height; y++) {
    rawFiltered[y * (stride + 1)] = 0; // filter: None
    data.copy(rawFiltered, y * (stride + 1) + 1, y * stride, y * stride + stride);
  }
  const idat = zlib.deflateSync(rawFiltered, { level: 9 });

  function chunk(type, body) {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(body.length, 0);
    const typeBuf = Buffer.from(type, 'ascii');
    const crcBuf = Buffer.alloc(4);
    crcBuf.writeUInt32BE(crc32(Buffer.concat([typeBuf, body])), 0);
    return Buffer.concat([len, typeBuf, body, crcBuf]);
  }
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8;   // bit depth
  ihdr[9] = 6;   // colour type: RGBA
  ihdr[10] = 0;  // compression
  ihdr[11] = 0;  // filter
  ihdr[12] = 0;  // interlace
  return Buffer.concat([
    PNG_SIG,
    chunk('IHDR', ihdr),
    chunk('IDAT', idat),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

// ===========================================================================
// Compositing
// ===========================================================================

function hexToRgb(hex) {
  const h = hex.replace('#', '');
  return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)];
}

// A tinted grayscale form, matching the game's CSS mask+multiply exactly:
//   fill  = solid tint, masked by the form's alpha
//   shade = the form image, multiply-blended over the fill
// => out.rgb = tint.rgb * form.rgb / 255 ; out.a = form.a
function tintForm(form, tintHex) {
  const [tr, tg, tb] = hexToRgb(tintHex);
  const out = Buffer.alloc(form.data.length);
  for (let i = 0; i < out.length; i += 4) {
    out[i]     = Math.round((form.data[i]     * tr) / 255);
    out[i + 1] = Math.round((form.data[i + 1] * tg) / 255);
    out[i + 2] = Math.round((form.data[i + 2] * tb) / 255);
    out[i + 3] = form.data[i + 3];
  }
  return { width: form.width, height: form.height, data: out };
}

// Straight-alpha "source-over": paint `top` over `bottom` (both w*h RGBA).
function over(bottom, top) {
  if (bottom.width !== top.width || bottom.height !== top.height) {
    throw new Error('over(): layer size mismatch');
  }
  const out = Buffer.alloc(bottom.data.length);
  for (let i = 0; i < out.length; i += 4) {
    const ta = top.data[i + 3] / 255;
    const ba = bottom.data[i + 3] / 255;
    const oa = ta + ba * (1 - ta);
    for (let c = 0; c < 3; c++) {
      const tc = top.data[i + c];
      const bc = bottom.data[i + c];
      out[i + c] = oa === 0 ? 0 : Math.round((tc * ta + bc * ba * (1 - ta)) / oa);
    }
    out[i + 3] = Math.round(oa * 255);
  }
  return { width: bottom.width, height: bottom.height, data: out };
}

// Compose one animation frame -> full-canvas RGBA (192x76).
function composeFrame(frame) {
  const form = decodePng(path.join(ASSETS, `dev_form_${frame}.png`));
  const hoodie = decodePng(path.join(ASSETS, `hoodie_${HOODIE_STYLE}.png`));
  const base = decodePng(path.join(ASSETS, `dev_base_${frame}.png`));
  let img = tintForm(form, HOODIE_TINT); // 1. tinted garment form
  img = over(img, hoodie);               // 2. style overlay (untinted)
  img = over(img, base);                 // 3. skin / hair / hands
  return img;
}

// Union alpha bounding box across several full-canvas frames.
function unionAlphaBBox(frames) {
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
  for (const f of frames) {
    for (let y = 0; y < f.height; y++) {
      for (let x = 0; x < f.width; x++) {
        if (f.data[(y * f.width + x) * 4 + 3] !== 0) {
          if (x < minX) minX = x;
          if (x > maxX) maxX = x;
          if (y < minY) minY = y;
          if (y > maxY) maxY = y;
        }
      }
    }
  }
  if (maxX < minX) throw new Error('all frames are fully transparent');
  return { minX, minY, maxX, maxY };
}

function crop(img, box) {
  const w = box.maxX - box.minX + 1;
  const h = box.maxY - box.minY + 1;
  const out = Buffer.alloc(w * h * 4);
  for (let y = 0; y < h; y++) {
    const srcStart = ((box.minY + y) * img.width + box.minX) * 4;
    img.data.copy(out, y * w * 4, srcStart, srcStart + w * 4);
  }
  return { width: w, height: h, data: out };
}

// ===========================================================================
// Run
// ===========================================================================

function main() {
  fs.mkdirSync(OUT, { recursive: true });
  const composed = FRAMES.map(composeFrame);

  // Tight, SHARED crop so both frames register pixel-for-pixel, padded.
  const raw = unionAlphaBBox(composed);
  const first = composed[0];
  const box = {
    minX: Math.max(0, raw.minX - CROP_PAD),
    minY: Math.max(0, raw.minY - CROP_PAD),
    maxX: Math.min(first.width - 1, raw.maxX + CROP_PAD),
    maxY: Math.min(first.height - 1, raw.maxY + CROP_PAD),
  };

  const labels = ['a', 'b'];
  composed.forEach((img, i) => {
    const cropped = crop(img, box);
    const file = path.join(OUT, `dexel_frame_${labels[i]}.png`);
    fs.writeFileSync(file, encodePng(cropped.width, cropped.height, cropped.data));
    console.log(`  wrote ${path.relative(REPO, file)}  (${cropped.width}x${cropped.height}, from ${FRAMES[i]})`);
  });
  console.log(`Baked ${composed.length} hero frame(s): hoodie=${HOODIE_STYLE} tint=${HOODIE_TINT}`);
}

main();
