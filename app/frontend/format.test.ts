// Unit tests for the humanized number/duration formatters (docs/ui-spec.md
// §14). Run with Node's built-in runner (no new dependency, no framework):
//
//   node --test format.test.ts        # from app/frontend/
//
// Node >= 22 strips the TypeScript types and runs this directly. It lives
// OUTSIDE src/ so tsconfig's `include: ["src/**/*.ts"]` never type-checks it
// (the node:test/node:assert imports need @types/node, which the runtime
// itself does not) — it is a test harness, never part of the shipped bundle
// (build.mjs bundles only from src/main.ts).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fmtCount, fmtDuration, fmtDayCount, rollsToYears } from './src/format.ts';

test('fmtCount — below 1000 is exact', () => {
  assert.equal(fmtCount(0), '0');
  assert.equal(fmtCount(1), '1');
  assert.equal(fmtCount(88), '88');
  assert.equal(fmtCount(842), '842');
  assert.equal(fmtCount(999), '999');
});

test('fmtCount — 3 sig figs, floored, trailing zeros trimmed', () => {
  assert.equal(fmtCount(1000), '1k');
  assert.equal(fmtCount(1234), '1.23k');   // 3 sig figs, floored (not 1.24k)
  assert.equal(fmtCount(88426), '88.4k');  // the owner's example
  assert.equal(fmtCount(120000), '120k');  // >=100k -> 0 decimals
  assert.equal(fmtCount(1000000), '1M');
  assert.equal(fmtCount(1200000), '1.2M');
  assert.equal(fmtCount(12000000), '12M'); // 12.0M -> trimmed
  assert.equal(fmtCount(1200000000), '1.2B');
  assert.equal(fmtCount(1200000000000), '1.2T');
});

test('fmtCount — FLOOR never over-claims the next unit', () => {
  assert.equal(fmtCount(999999), '999k');       // never "1M"
  assert.equal(fmtCount(999999999), '999M');    // never "1B"
  assert.equal(fmtCount(1999), '1.99k');        // never "2k"
});

test('fmtCount — negatives and junk degrade gracefully', () => {
  assert.equal(fmtCount(-50), '-50');
  assert.equal(fmtCount(-1234), '-1.23k');
  assert.equal(fmtCount(undefined), '0');
  assert.equal(fmtCount(NaN), '0');
  assert.equal(fmtCount(Infinity), '0');
});

test('fmtDuration — seconds and minute boundaries', () => {
  assert.equal(fmtDuration(0), '0s');
  assert.equal(fmtDuration(45), '45s');
  assert.equal(fmtDuration(59), '59s');
  assert.equal(fmtDuration(60), '1m');       // trailing " 0s" dropped
  assert.equal(fmtDuration(90), '1m 30s');
  assert.equal(fmtDuration(3599), '59m 59s');
});

test('fmtDuration — hours, days, years rollups (two units, floored)', () => {
  assert.equal(fmtDuration(3600), '1h');        // hour boundary, " 0m" dropped
  assert.equal(fmtDuration(15810), '4h 23m');   // "263m 30s" -> two units
  assert.equal(fmtDuration(61201), '17h');      // "1020m 1s" -> honest 17h
  assert.equal(fmtDuration(86400), '1d');       // day boundary
  assert.equal(fmtDuration(86400 + 4 * 3600), '1d 4h');
  assert.equal(fmtDuration(365 * 86400), '1y'); // year boundary
  assert.equal(fmtDuration(400 * 86400), '1y 35d');
  assert.equal(fmtDuration(730 * 86400), '2y'); // "2y", trailing " 0d" dropped
});

test('fmtDuration — negatives/junk clamp to 0s', () => {
  assert.equal(fmtDuration(-10), '0s');
  assert.equal(fmtDuration(undefined), '0s');
  assert.equal(fmtDuration(NaN), '0s');
});

test('fmtDayCount — small stays bare, large rolls to years', () => {
  assert.equal(fmtDayCount(0), '0');
  assert.equal(fmtDayCount(12), '12');
  assert.equal(fmtDayCount(364), '364');
  assert.equal(fmtDayCount(365), '1y');
  assert.equal(fmtDayCount(400), '1y 35d');
  assert.equal(fmtDayCount(730), '2y');
});

test('rollsToYears — matches the fmtDayCount threshold', () => {
  assert.equal(rollsToYears(12), false);
  assert.equal(rollsToYears(364), false);
  assert.equal(rollsToYears(365), true);
  assert.equal(rollsToYears(400), true);
  assert.equal(rollsToYears(undefined), false);
});
