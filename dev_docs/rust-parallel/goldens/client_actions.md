# ClientAction verbs — transcribed from `app/frontend/src/wire.ts`

Source: `app/frontend/src/wire.ts`, the `ClientAction` discriminated union
(read in full at HEAD; see CONTRACT.md for the exact commit). This is the
complete client -> server verb set as of that commit — every row below is
a variant of one TypeScript union, not a partial list.

```ts
export type ClientAction =
  | { action: 'BUY_ITEM'; itemId: string }
  | { action: 'BUY_TINT'; itemId: string; tintId: string }
  | { action: 'EQUIP_ITEM'; slot: string; itemId: string; tintId: string | null }
  | { action: 'STORE_OPEN' }
  | { action: 'STORE_CLOSE' }
  | { action: 'SET_NAME'; name: string }
  | { action: 'SESSION_START'; name?: string }
  | { action: 'SESSION_STOP' };
```

## Example payloads (as sent over the WS `/ws` connection, one JSON object per line)

```json
{"action":"BUY_ITEM","itemId":"chair_deluxe"}
{"action":"BUY_TINT","itemId":"chair_basic","tintId":"slate"}
{"action":"EQUIP_ITEM","slot":"chair","itemId":"chair_basic","tintId":"slate"}
{"action":"EQUIP_ITEM","slot":"mouse","itemId":"mouse_stock","tintId":null}
{"action":"STORE_OPEN"}
{"action":"STORE_CLOSE"}
{"action":"SET_NAME","name":"Probe"}
{"action":"SESSION_START"}
{"action":"SESSION_START","name":"golden-capture"}
{"action":"SESSION_STOP"}
```

Two of these (`SET_NAME`, `SESSION_START`) were exercised for real against
the live Go server during this capture — see `raw_capture_full.jsonl` for
the verbatim frames sent and received:

- `{"action":"SET_NAME","name":"Probe"}` sent at t=300ms produced
  `config.name` becoming `"Probe"` on the next `state` broadcast and a
  `{"type":"flash","kind":"welcome","text":"Hello, Probe!"}` message.
- `{"action":"SESSION_START","name":"golden-capture"}` sent at t≈624ms
  produced `{"type":"flash","kind":"session","text":"Session started."}`
  and populated `sessions.active` on the next `state` broadcast.
- `{"action":"SESSION_STOP"}` sent at t≈70019ms (after a ~70s session)
  produced the `sessionComplete` message and a closing `flash` — see
  `session_flow.json`.

`BUY_ITEM`, `BUY_TINT`, `EQUIP_ITEM`, `STORE_OPEN`, `STORE_CLOSE` were
**not** exercised live in this capture (no coins had accrued to buy
anything within the window, per the anti-mashing clamp — see
`state_sequence.jsonl`, `devCash` stays `0` throughout); their payload
shapes above are transcribed directly from the TS union and from
`docs/ui-spec.md` §6.2, not captured from a live frame. A future capture
pass (P1+) should drive a longer or coin-seeded fake run to get live
examples of these five.

## Server -> client message types (for cross-reference)

`ServerMessage = CatalogMessage | StateMessage | FlashMessage | SessionCompleteMessage`
— see `catalog.json`, `state_sequence.jsonl`, and `session_flow.json`
respectively for live captures of each.
