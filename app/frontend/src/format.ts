// Pure formatting/number helpers. No DOM, no state — every layer may
// import these.
export function clamp(n: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, n));
}
// Wire numbers arrive as float64; every on-screen numeric render must be an
// integer (see ui-spec.md's own examples: "4,200 / 5,000", "LV 5", "34 / 75").
export function fmtInt(n: number | undefined): string {
  return String(Math.floor(Number(n) || 0));
}
// Renders a whole-seconds duration count from state.stats (Analytics
// track Phase A1) as "Xm Ys" (or just "Ys" under a minute) — the wire
// sends raw seconds (see docs handed to the overseer for ui-spec.md's
// patch), never a pre-formatted string; all formatting happens here.
export function fmtDuration(totalSeconds: number | undefined): string {
  const s = Math.max(0, Math.floor(Number(totalSeconds) || 0));
  const m = Math.floor(s / 60);
  const rem = s % 60;
  if (m <= 0) return rem + 's';
  return m + 'm ' + rem + 's';
}
export function truncate(str: string | null | undefined, maxLen: number): string {
  str = str || '';
  if (str.length <= maxLen) return str;
  return str.slice(0, Math.max(0, maxLen - 1)) + '…';
}
