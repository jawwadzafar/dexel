// RENDER layer — the terminal (#terminal), ui-spec.md §3.2 /
// art-direction.md "screen region". Owns the idle-cursor blink timer.
import { byId } from '../dom';
import * as store from '../state/store';
import { truncate } from '../format';

const terminal = byId<HTMLDivElement>('terminal');

// idle cursor blink (0.5s), applied to the last terminal line.
let cursorOn = true;
setInterval(function () {
  cursorOn = !cursorOn;
  const state = store.getState();
  const cursor = terminal.querySelector('.cursor');
  if (cursor) cursor.classList.toggle('off', !cursorOn || !state || state.activeState !== 'idle');
}, 500);

export function renderTerminal(): void {
  terminal.innerHTML = '';
  const state = store.getState();
  if (!state) return;
  const lines = (state.screenLines || []).slice(0, 11);
  while (lines.length < 11) lines.unshift('');
  const onBreak = state.activeState === 'onBreak';
  lines.forEach(function (text, idx) {
    const isLast = idx === lines.length - 1;
    const div = document.createElement('div');
    div.className = 'line';
    const recentCount = 2;
    const isRecent = !onBreak && (idx >= lines.length - recentCount);
    if (isRecent) div.classList.add('recent');
    const shown = isLast && onBreak ? '-- idle --' : truncate(text, 30);
    div.textContent = shown;
    if (isLast && state!.activeState === 'idle') {
      const cursor = document.createElement('span');
      cursor.className = 'cursor' + (cursorOn ? '' : ' off');
      div.appendChild(cursor);
    }
    terminal.appendChild(div);
  });
}
