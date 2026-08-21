# dev-companion roadmap

The overseer executes this autonomously, phase by phase, without asking the
user what to build. The user supplies roadmaps; the overseer plans, delegates
to subagents, gates in-game, commits, and ships versioned releases. Each
phase is independently shippable and must not regress a prior one.

Ground rules that hold across every phase (do not re-decide):
- **Privacy is absolute** (ADR 0002/0009): every signal is a COUNT or a
  DURATION. Never content. "Copy/paste tracking" means detecting that a
  copy/paste *chord happened* (a counter++), NEVER the clipboard, the text,
  the file, or the app's document. A field that could hold content fails the
  structural test and does not ship.
- **Honest mechanics** (ADR 0010): earning reflects real work; anti-mash
  holds; blind sources never fabricate.
- **In-game gate**: no visual/UX change ships without the overseer rendering
  the real product and judging it — isolated mockups have lied to us twice.
- **Stack**: Go + HTML/NES.css (`app/`), ADR 0011. Rust frozen legacy.

---

## Shipped
- **v1.0.0** — core loop: activity -> sprints -> Dev Cash -> store modal ->
  buy/equip -> the character visibly changes. Honest moods, privacy,
  behind-view scene, loud failures. (tag `v1.0.0`)
- **art track (in progress)** — pushing procedural pixel-art fidelity
  (dithered shading); hero pass shipped, full rollout underway.

---

## Analytics track — "your workflow, as a game"

The game doubles as a private, local workflow tracker. Signals are counted,
priced, and paid out as Dev Cash; the player can see their own analytics.
Three phases, each shippable.

### Phase A1 (v1.1) — Activity log foundation
- Backend records per-signal DAILY counters (today + lifetime), persisted
  alongside the save: keystrokes, mouse-active seconds, active minutes,
  idle minutes, sprints completed. (Signals we ALREADY capture — no new
  provider work; this is aggregation + persistence + a read-only view.)
- New "Activity" modal (same modal pattern as the store, opened from the
  title bar / a key): today's counts + lifetime totals, plain NES.css.
- No new earning yet — establishes the data model + the second modal.
- Exit: modal shows real accumulating counts; counters survive restart;
  structural privacy test covers the new stats types.

### Phase A2 (v1.2) — Priced signals & diversified earning
- Add content-free signal detection: copy/paste CHORD count (Cmd/Ctrl+C /
  +V happened — count only, never clipboard), app-switch count,
  focus-session count (a sustained-typing block). All in the provider layer
  behind the same trait, all counts.
- Each signal type has a coin value (a data table, like the upgrade
  catalog); Dev Cash accrues from the weighted mix, EXTENDING ADR 0005 (the
  anti-mash ceiling and the mouse<typing invariant must still hold — add
  the strategy-comparison test for the new mix).
- Activity modal shows per-signal coin contribution ("keystrokes: 1,240 ->
  12 coins today").
- Exit: earning is signal-diverse; anti-mash invariant re-proven; per-signal
  pricing visible and tuned; migration keeps existing balances.

### Phase A3 (v1.3) — Analytics over time
- Daily/weekly history (a rolling window persisted), streaks, simple bar
  charts (CSS/canvas, pixel-styled), and a couple of honest "workflow
  insights" derived only from counts (busiest hour, longest focus block).
- Exit: history renders, streaks compute correctly across day boundaries,
  no content anywhere in the stored history.

---

## Menus & content track (enabled by the skills below)
Future menus (settings, achievements, more store categories, themes) reuse
the "add-a-modal" skill + the WS-contract-extension pattern. Added as the
user hands roadmaps; not pre-scoped here.

## Deferred (named so they don't creep)
Wails floating window; meeting/mic detection; the tint-hex contrast fix
(slate ~ wall_dark); auto-update/distribution; any Rust revisit.
