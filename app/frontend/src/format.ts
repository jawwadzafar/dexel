// Pure formatting/number helpers. No DOM, no state — every layer may
// import these.
export function clamp(n: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, n));
}
// Wire numbers arrive as float64; every on-screen numeric render must be an
// integer (see ui-spec.md's own examples: "4,200 / 5,000", "LV 5", "34 / 75").
// fmtInt stays the exact, un-compacted integer render — used wherever a
// value must read precisely (coin deltas, prices, progress ratios, an
// ordinal id), i.e. everything fmtCount below is deliberately NOT applied to.
export function fmtInt(n: number | undefined): string {
  return String(Math.floor(Number(n) || 0));
}

// fmtCount — the SI-style compact integer used by GitHub/YouTube for big
// COUNTS (keystrokes, clicks, app switches, sprints, sessions, HUD cash).
// Rule (docs/ui-spec.md §14):
//   * below 1000 -> exact ("0", "88", "842", "999"),
//   * >= 1000 -> a k/M/B/T suffix at THREE significant figures, with any
//     trailing ".0"/".00" trimmed ("1k" not "1.0k", "12k" not "12.0k"),
//   * FLOOR (truncate) the fraction, never round up — so a value that has
//     not actually reached the next unit never claims it: 999999 -> "999k"
//     (never "1M"), 1234 -> "1.23k". This keeps the number honest: it can
//     under-state by less than one displayed step, never over-state.
// Negatives render with a leading '-' over the same rule; a non-finite or
// undefined input degrades to "0" (matching fmtInt's own defensiveness).
export function fmtCount(n: number | undefined): string {
  let v = Math.floor(Number(n) || 0);
  if (!isFinite(v)) return '0';
  const sign = v < 0 ? '-' : '';
  v = Math.abs(v);
  if (v < 1000) return sign + v;
  const units: Array<{ d: number; s: string }> = [
    { d: 1e12, s: 'T' },
    { d: 1e9, s: 'B' },
    { d: 1e6, s: 'M' },
    { d: 1e3, s: 'k' }
  ];
  let unit = units[units.length - 1];
  for (let i = 0; i < units.length; i++) {
    if (v >= units[i].d) { unit = units[i]; break; }
  }
  const D = unit.d;
  // 3 significant figures: the integer part of the scaled value has 1..3
  // digits, so give it 2/1/0 decimals respectively.
  const decimals = v >= 100 * D ? 0 : v >= 10 * D ? 1 : 2;
  const factor = Math.pow(10, decimals);
  // Integer-then-divide keeps the FLOOR exact for integer inputs (v*factor
  // stays within Number.MAX_SAFE_INTEGER for every value this app shows).
  const scaled = Math.floor((v * factor) / D) / factor;
  let str = scaled.toFixed(decimals);
  if (str.indexOf('.') >= 0) str = str.replace(/\.?0+$/, '');
  return sign + str + unit.s;
}

const MINUTE = 60;
const HOUR = 3600;
const DAY = 86400;
const DAYS_PER_YEAR = 365;
const YEAR = DAY * DAYS_PER_YEAR; // 365d — NO months (a month's length is
// variable; the owner said skip it), so the rollup jumps straight d -> y.

// fmtDuration renders a whole-seconds duration as humanized prose, rolling
// up units and showing AT MOST the two most-significant ones, flooring the
// lower one (docs/ui-spec.md §14):
//   < 60s   -> "45s"
//   < 60m   -> "3m 30s"  (trailing " 0s" dropped -> "3m")
//   < 24h   -> "2h 15m"  (trailing " 0m" dropped -> "2h")
//   < 365d  -> "3d 4h"   (trailing " 0h" dropped -> "3d")
//   >= 365d -> "1y 35d"  (trailing " 0d" dropped -> "2y")
// Never three units ("2h 15m", never "2h 15m 3s"): the lower units below the
// top two are floored away, which is what turns the raw "1020m 1s" a naive
// minutes render produced into an honest "17h". The wire sends raw seconds
// (never a pre-formatted string); all formatting happens here.
export function fmtDuration(totalSeconds: number | undefined): string {
  const s = Math.max(0, Math.floor(Number(totalSeconds) || 0));
  if (s < MINUTE) return s + 's';
  if (s < HOUR) return twoUnit(Math.floor(s / MINUTE), 'm', Math.floor(s % MINUTE), 's');
  if (s < DAY) return twoUnit(Math.floor(s / HOUR), 'h', Math.floor((s % HOUR) / MINUTE), 'm');
  if (s < YEAR) return twoUnit(Math.floor(s / DAY), 'd', Math.floor((s % DAY) / HOUR), 'h');
  return twoUnit(Math.floor(s / YEAR), 'y', Math.floor((s % YEAR) / DAY), 'd');
}
// "Xhi Ylo", dropping the low unit entirely when it floors to 0 ("2h 0m" ->
// "2h"). Both magnitudes are already floored by the caller.
function twoUnit(hi: number, hiU: string, lo: number, loU: string): string {
  return lo > 0 ? hi + hiU + ' ' + lo + loU : hi + hiU;
}

// fmtDayCount renders a whole-DAY count (a streak, days-tracked, ...). A
// plain small count stays a bare integer (the DOM around it supplies the
// "DAYS" unit); once it reaches a year it rolls up to "1y 35d" via the SAME
// 365-day year fmtDuration uses, and the caller drops its "DAYS" suffix
// because the rolled-up form is already self-describing (docs/ui-spec.md
// §14). Use rollsToYears() to decide that suffix without re-hardcoding 365.
export function fmtDayCount(days: number | undefined): string {
  const d = Math.max(0, Math.floor(Number(days) || 0));
  if (d < DAYS_PER_YEAR) return String(d);
  const y = Math.floor(d / DAYS_PER_YEAR);
  const rem = d % DAYS_PER_YEAR;
  return rem > 0 ? y + 'y ' + rem + 'd' : y + 'y';
}
export function rollsToYears(days: number | undefined): boolean {
  return (Math.floor(Number(days) || 0)) >= DAYS_PER_YEAR;
}

export function truncate(str: string | null | undefined, maxLen: number): string {
  str = str || '';
  if (str.length <= maxLen) return str;
  return str.slice(0, Math.max(0, maxLen - 1)) + '…';
}
