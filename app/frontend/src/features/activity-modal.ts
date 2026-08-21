// FEATURE/LOGIC layer — the activity modal (Analytics track Phase A1). A
// read-only rendering of state.stats.{today,lifetime}, straight off the
// central store like everything else (no client-side derivation: the
// server is the sole source of truth). Deliberately sends NO open/close
// action to the server, unlike the store modal: this modal is read-only
// and gates nothing (no earning to freeze), and the counts it displays
// come entirely from the server's own per-tick sampling of the real,
// global activity provider — never from anything this page does. Opening
// it via the [A] key IS a real keystroke on the user's system and gets
// honestly counted like any other keypress (exactly as pressing [S] to
// open the store already does); this page never simulates or
// double-counts a keystroke on top of that by rendering the dialog, so
// there is no separate inflation risk to gate against.
import { byId } from '../dom';
import * as store from '../state/store';
import { fmtDuration, fmtInt } from '../format';
import type { StatBlock, Stats } from '../wire';

const el = {
  activity: byId<HTMLDialogElement>('activity'),
  activityOpenBtn: byId<HTMLButtonElement>('activity-open'),
  activityClose: byId<HTMLButtonElement>('activity-close'),
  scrim: byId<HTMLDivElement>('scrim'),
  statTodayKeystrokes: byId('stat-today-keystrokes'),
  statTodayMouse: byId('stat-today-mouse'),
  statTodayActive: byId('stat-today-active'),
  statTodayIdle: byId('stat-today-idle'),
  statTodaySprints: byId('stat-today-sprints'),
  statLifeKeystrokes: byId('stat-life-keystrokes'),
  statLifeMouse: byId('stat-life-mouse'),
  statLifeActive: byId('stat-life-active'),
  statLifeIdle: byId('stat-life-idle'),
  statLifeSprints: byId('stat-life-sprints')
};

export function isOpen(): boolean { return el.activity.open; }

export function renderActivity(): void {
  const state = store.getState();
  if (!state) return;
  const stats: Partial<Stats> = state.stats || {};
  const today: Partial<StatBlock> = stats.today || {};
  const life: Partial<StatBlock> = stats.lifetime || {};
  el.statTodayKeystrokes.textContent = fmtInt(today?.keystrokes);
  el.statTodayMouse.textContent = fmtDuration(today?.mouseActiveSeconds);
  el.statTodayActive.textContent = fmtDuration(today?.activeSeconds);
  el.statTodayIdle.textContent = fmtDuration(today?.idleSeconds);
  el.statTodaySprints.textContent = fmtInt(today?.sprintsCompleted);
  el.statLifeKeystrokes.textContent = fmtInt(life?.keystrokes);
  el.statLifeMouse.textContent = fmtDuration(life?.mouseActiveSeconds);
  el.statLifeActive.textContent = fmtDuration(life?.activeSeconds);
  el.statLifeIdle.textContent = fmtDuration(life?.idleSeconds);
  el.statLifeSprints.textContent = fmtInt(life?.sprintsCompleted);
}

export function refreshIfOpen(): void {
  if (el.activity.open) renderActivity();
}

export function open(): void {
  if (el.activity.open) return;
  renderActivity();
  el.activity.showModal();
  el.scrim.classList.add('visible');
}
export function close(): void {
  if (!el.activity.open) return;
  el.activity.close();
}
el.activity.addEventListener('close', function () {
  el.scrim.classList.remove('visible');
});
el.activityOpenBtn.addEventListener('click', open);
el.activityClose.addEventListener('click', close);

export function handleKeydown(e: KeyboardEvent): void {
  switch (e.key) {
    case 'a': case 'A': close(); break;
    default: break; // Esc: native <dialog> behaviour, not intercepted
  }
}
