# app-rs/ parity contract — P0b golden captures index

Owner: this P0a/P0b execution pass. Scope per
`docs/rust-port-evaluation.md` §5 (P0a/P0b) and §6 (scorecard). This
directory is `app-rs/`'s designated home for parity/comparison artifacts,
distinct from the plan prose's own `docs/parity/` naming — kept
separate to avoid collision with concurrent agents also touching this
repo. Everything here is captured from the REAL, unmodified Go server
(`app/`), driven by the fake activity provider, over its real `/ws`
WebSocket — no mocks, no hand-written fixtures.

## Candidate parity-freeze point

**HEAD at capture time: `3c81774b69b38132f6efef54428372177060ea9c`**
(branch `main`, `git rev-parse HEAD` at the moment these goldens were
taken). Per §5.1's rule ("parity is measured against a frozen tag, never
against `main`"), this commit is the **candidate** for the `PARITY-BASELINE`
tag the plan calls for — recorded here as data; tagging it is the
overseer's decision, not this agent's (out of scope: this agent does not
tag or commit). `main` was observed moving during this capture (per §5.1's
own warning, and confirmed here: the live server's `state` messages
already carry `pausedSeconds` fields and a top-level `paused` flag that
`app/frontend/src/wire.ts` does not yet declare — see "Drift observed"
below), so whichever commit is actually tagged should be re-diffed against
these goldens before being trusted as the frozen target.

## How these were captured

- Go binary built from `app/` at the above HEAD: `go build -o dexel-probe .`
  (plain `go build`, no ldflags — this is a capture tool, not a release
  artifact).
- Run with **both `HOME` and `DEXEL_HOME` pointed at fresh, empty temp
  directories** (not just `DEXEL_HOME` — the server's legacy-save importer
  in `app/main.go` (`resolveLegacySavePath` et al., `app/internal/store/
  legacy.go`) reads `$HOME/.local/share/dev-companion/save.json` directly,
  ignoring `DEXEL_HOME`; a real save on this box was imported into the
  first capture attempt as a result. Re-run with a fresh `HOME` too and the
  server correctly logged "no save and no legacy save found: starting
  fresh").
- Invocation: `-addr 127.0.0.1:0 -provider fake -fake-script
  "type:20s,idle:15s,mouse:15s,type:20s" -insecure-origin`.
  `-insecure-origin` was used only so a plain Node WebSocket client (no
  browser, no Origin-spoofing) could connect for this capture — it does
  not change the wire format, only the same-origin gate (G6's rule is
  about the frontend, unaffected).
- Client: `node` (v24, built-in `WebSocket` global — no npm install
  needed), a small script that connects to `/ws`, records every frame with
  a millisecond timestamp, sends `SET_NAME`, `SESSION_START` shortly after
  connecting and `SESSION_STOP` at ~70s, then serializes every frame to
  `raw_capture_full.jsonl`.
- Total run: 72 s wall clock, covering the fake-script's full
  `type:20s,idle:15s,mouse:15s,type:20s` cycle (70 s) plus a couple of
  seconds of margin. 87 frames captured: 1 `catalog`, 77 `state`, 3
  `flash`, 1 `sessionComplete`.
- `/api/health` was captured separately with a plain `curl` against the
  same running instance.
- The server was killed by PID (`kill $(cat server.pid)`), never `pkill`/
  `pgrep -f`; the two temp home directories were removed after capture.

## Goldens index

| File | What it is |
|---|---|
| `health.json` | One real `GET /api/health` response. |
| `catalog.json` | The one real `catalog` message sent on connect (`CatalogMessage`: `slots`, `tints`, `items`). |
| `state_sequence.jsonl` | 10 representative real `state` frames, one JSON object per line (`{"tMillis": <ms since connect>, "state": <StateMessage>}`), spanning: connect/onboarding (t=24ms), early typing (t=762ms), mid typing (t≈12.8s), end of first typing phase (t≈24.8s), early idle (t≈26.8s), mid idle (t≈35.8s), end of idle (t≈44.8s), start of the mouse-then-typing tail (t≈46.8s), mid tail (t≈60.8s), and the final frame after `SESSION_STOP` (t≈71.8s). |
| `session_flow.json` | The real `SESSION_START` -> `flash{kind:"session"}` -> ... -> `SESSION_STOP` -> `sessionComplete` -> `flash{kind:"session"}` sequence for one ~70s session (`durationSeconds: 69` — the fake script's clock, not wall clock, drives the counters). |
| `client_actions.md` | The full `ClientAction` union transcribed verbatim from `app/frontend/src/wire.ts`, with example payloads; two of the eight actions (`SET_NAME`, `SESSION_START`/`SESSION_STOP`) are backed by live captured frames, the other five (`BUY_ITEM`, `BUY_TINT`, `EQUIP_ITEM`, `STORE_OPEN`, `STORE_CLOSE`) are transcribed from source, not live-captured (no coins had accrued in-window to exercise the store). |
| `raw_capture_full.jsonl` | The complete, unedited 87-frame capture in connection order, both directions (`dir: "recv"` for server->client, `dir: "meta"` for the client actions sent and connection lifecycle) — the provenance record the curated files above were extracted from. |

## The contract, as testable statements (for a future `parity-check.sh`)

- **WS `state` message** — `StateMessage` per `wire.ts`: `type`, `v`,
  `activeState` (`"coding"|"idle"|"onBreak"`), `activityLine`, `devCash`,
  `level`, `xp`, `storeOpen`, `sprint{index,name,progress,target,unitLabel?}`,
  `screenLines` (always exactly 11), `tickerLines` (always exactly 3),
  `equipped` (one entry per slot, always), `ownedItems`, `ownedTints`,
  optional `stats`, `config`, `onboarding`, `sessions`. **Drift observed
  live and not yet reflected in `wire.ts`:** every captured frame also
  carries `paused: false` at the top level and `pausedSeconds` inside every
  `StatBlock`/`DayStat`/`ActiveSessionView`/`SessionView` — this is PR-4/
  PR-5's pause feature (`app/lifecycle_handlers.go`,
  `app/internal/game/pause.go`) landing on `main` after `wire.ts` was last
  read for this capture; exactly the live-drift problem §5.1 warns about.
  Any `app-rs/` port and any re-freeze of the parity tag must re-check this
  field against `wire.ts` at the actual freeze commit, not against this
  document.
- **WS `catalog` message** — sent once per connection, static thereafter:
  `type`, `v`, `slots[]`, `tints[]`, `items[]` (each item: `id`, `slot`,
  `name`, `price`, `sprite`, `detail`, exactly one of `thumb` or
  `thumbForm`+`thumbDetail` non-null, `defaultTint`, `flavor`).
  See `catalog.json`.
- **WS `flash` message** — `type`, optional `kind`, optional `text`. Both
  observed kinds here: `"welcome"` (after `SET_NAME`) and `"session"`
  (session start and session end).
- **WS `sessionComplete` message** — `type`, `v`, `session: SessionView`
  (all fields present, including `endReason: "user"|"idle"|"maxDuration"`).
- **Client actions** — the 8-variant union in `client_actions.md`; server
  normalizes `SET_NAME`/`SESSION_START` names (trim, drop control chars,
  cap length) rather than trusting the client.
- **HTTP surface** — `/` and `/assets/` (embedded or disk, per
  `-public`/`DEXEL_ASSETS_DIR`), `/api/health` (`healthResponse`:
  `assetsDir`, `publicOk`, `version`, `commit`, `source`, `publicSource`,
  `assetsSource` — see `health.json`), `/ws` (same-origin gated by
  `wsOriginPatterns`, unless `-insecure-origin`).
- **`DEXEL_LISTENING` handshake** — first stdout line on bind:
  `"DEXEL_LISTENING http://<addr>"`, verbatim, observed in this capture's
  server log as `DEXEL_LISTENING http://127.0.0.1:45761`.
- **Privacy** — every captured frame was grepped for this box's real
  username and home-directory paths; none appear. The only free-text
  string in the whole capture is `"Probe"`, the name this capture itself
  supplied via `SET_NAME` — not real user content.

## Known gaps (P1+ scope, not this pass)

- `runtime.json` schema and `dexel status --json` were not captured (P0a's
  cross-compile probe took priority within this pass's time budget; the
  plan lists these under the same P0b bullet but they are CLI/lifecycle
  surface, not WS/HTTP surface, and are lower-risk to capture later since
  they don't depend on a live fake-script run).
- The five store-related client actions have no live-captured example
  frame (see `client_actions.md`).
- No Go test yet regenerates these goldens and fails on drift (the plan's
  P0b exit criterion) — these are a manual capture, not yet wired into CI
  or a `scripts/parity-check.sh` harness. That harness is P1+ scope.
