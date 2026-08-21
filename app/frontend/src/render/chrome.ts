// RENDER layer — titlebar / sprint panel / status panel / ticker. Given
// the current state, updates the DOM it owns; no business logic.
import { byId } from '../dom';
import * as store from '../state/store';
import { fmtInt, truncate } from '../format';
import { MOOD_COLOR } from '../geometry';

const hudLevel = byId('hud-level');
const hudCash = byId('hud-cash').querySelector('.value') as HTMLElement;
const sprintName = byId('sprint-name').querySelector('.value') as HTMLElement;
const sprintBar = byId<HTMLProgressElement>('sprint-bar');
const sprintUnits = byId('sprint-units');
const statusDot = byId('status-dot');
const statusLine = byId('status-line');
const ticker = byId<HTMLUListElement>('ticker');

export function renderChrome(): void {
  const state = store.getState();
  if (!state) return;
  // The title bar no longer shows a mood dot (BUG-2) — mood is still
  // conveyed via #status-dot in the bottom status panel.
  const moodColor = MOOD_COLOR[state.activeState] || MOOD_COLOR.idle;
  statusDot.style.background = moodColor;
  hudLevel.textContent = 'LV ' + fmtInt(state.level);
  hudCash.textContent = fmtInt(state.devCash);

  sprintName.textContent = truncate(state.sprint.name, 28);
  sprintBar.max = state.sprint.target;
  sprintBar.value = state.sprint.progress;
  // unitLabel falls back to "units" — a state missing that field (a
  // stale server, or a devApply payload that replaced sprint wholesale
  // without it) must never render the literal string "undefined".
  sprintUnits.textContent = fmtInt(state.sprint.progress) + ' / ' + fmtInt(state.sprint.target) + ' ' + (state.sprint.unitLabel || 'units');

  statusLine.textContent = truncate(state.activityLine, 34);

  const lis = ticker.querySelectorAll('li');
  for (let i = 0; i < lis.length; i++) {
    const raw = (state.tickerLines || [])[i] || '';
    lis[i].textContent = raw ? truncate('> ' + raw, 36) : '';
  }
}
