# SEC-1 design — save integrity / anti-cheat (config vs protected split + HMAC)

Design pass for ROADMAP.md "SEC-1 — Save integrity / anti-cheat." This is the
gate: it fixes the config/state split, the integrity mechanism and its canonical
serialization, the key-management level (and its honest ceiling), the tamper
policy, the schema/migration, the privacy confirmation, and the fan-out. No
implementation code is written here. Companion decision record:
`docs/adr/0014-save-integrity-hmac-and-config-split.md`.

The problem, verbatim from the owner: the save is plain JSON at
`~/.config/dexel/state.json`; a user can hand-edit it to mint Dev Cash or grant
items, and we want to deter that. But some fields are legitimately the user's —
the **name** they give their dexel — which they should be free to set. So:
separate editable **config** from protected **game state**, and make tampering
with the protected economy detectable and rejected.

---

## 0. The fork that needs a USER/overseer decision (surface first)

There is exactly one, and it has a recommended default; **SEC-1 ships on the
default without asking.**

- **Fork K — where the HMAC key lives: baked into the binary (DEFAULT) vs the OS
  keychain.** On a **fully-local, single-player, Apache-2.0 open-source** game,
  the attacker who would bother is the machine's own owner — who can read their
  own keychain entry (`security`, `secret-tool`, Credential Manager) and their
  own `machine-id` exactly as easily as they can read the public source. So the
  keychain and machine-derived options raise the *real* ceiling by **≈ nothing**
  over a baked key, while costing three platform backends, a new dependency,
  headless-CI friction, and (for machine-derived) **loss of save portability**
  between machines. **Default taken here: a baked-in key, not obfuscated, openly
  documented.** Choose the keychain only if the owner wants the key out of the
  source tree for a reason *other than* cheat-resistance. Everything below is
  written so swapping the key *source* is a one-function change
  (`integrityKey() []byte`) and nothing else moves.

Is signing even worth it, given the algorithm and key are public in OSS? **Yes —
make the call and ship it.** The realistic threat is not a reverse-engineer; it
is the curious player who opens `state.json` in a text editor, sees
`"devCash": 100`, and edits it — and, once they notice they *can*, stops taking
the economy seriously at all. Defeating **that** is worth ~60 lines + a test.
The honest ceiling (below) is real and stated, but "we can't stop everyone" is
not a reason to stop no one.

---

## 1. Config vs protected split (the enumeration + the file layout)

### 1.1 Which fields are which

| Field (today, on `SaveData`) | Class | Why |
|---|---|---|
| `devCash` | **PROTECTED** | the whole point — the currency |
| `xp` | **PROTECTED** | drives level; economy-adjacent |
| `sprint` (index, unitsDone) | **PROTECTED** | progress toward the next payout |
| `ownedItems` | **PROTECTED** | granting items = minting value |
| `ownedTints` | **PROTECTED** | same |
| `equipped` | **PROTECTED** | must reference owned items; part of the economy graph |
| `stats` (today/lifetime counters, coinsToday, **history**, **streak**) | **PROTECTED** | a faked 999-day streak / inflated history is a cheat too |
| `importedFromRust`, `importedAt` | **PROTECTED** | provenance; no reason a user edits these, and they gate migration semantics |
| `schema` | **PROTECTED** | already inside `SaveData`, so the MAC covers it (a downgrade edit is caught) |

| Field (config) | Class | Why |
|---|---|---|
| dexel **name** (NEW slot) | **CONFIG** | the user names their own pet — theirs to set |
| future cosmetic prefs (theme, always-on-top toggle, sound, window prefs…) | **CONFIG** | preferences, never economy |

The rule that decides the boundary: **does editing this field grant the user
value or misrepresent their play?** If yes → protected. If it is a preference or
a label the user authors about their own companion → config.

Note the name is genuinely **user-authored free text** — the *only* free text
anywhere in persistence. That is the crux of §6 (privacy): it is a different
*category* from observed-activity content, and isolating it in its own file is
what keeps the privacy boundary crisp.

### 1.2 File layout — RECOMMEND two files

**Decision: two files.**
- `~/.config/dexel/state.json` — the existing `SaveData`, now with a `mac` tag. **Signed.**
- `~/.config/dexel/config.json` — `ConfigData` (name + future prefs). **Unsigned, hand-editable by design.**

Over the one-file alternative (a signed protected blob + an unsigned config
section + a tag), two files wins on four counts, all of which are *free* with the
split and *extra code* without it:

1. **Privacy clarity (the decisive one).** `state.json`/`SaveData` stays exactly
   the content-free artifact the structural allow-list test already pins — the
   test is unchanged but for the one `mac string` field. The one free-text field
   in the whole product (the name) lives in a *different* file that is by
   definition where user-authored config goes. One file would force the name
   either outside `SaveData` (awkward in a single struct) or inside it (loosening
   the content-free test that is our privacy proof). Two files keeps the proof
   intact.
2. **"Keep the name on a tamper reset" is automatic.** The tamper path (§4)
   touches only `state.json`; `config.json` is loaded independently and is never
   in scope. One file would need explicit "reset economy but carefully preserve
   the config section" logic.
3. **Blast-radius isolation.** A corrupt or tampered `state.json` cannot take the
   user's name down with it; a malformed `config.json` cannot block the economy.
4. **No cross-file consistency invariant to maintain**, because config never
   affects the economy — the two files are genuinely independent, so there is no
   atomicity requirement *between* them (each is written with the existing atomic
   tmp-write + rename + dir-fsync recipe).

Cost: a second (identical, already-written) atomic write and a second
`DefaultPath`. Negligible.

---

## 2. Integrity mechanism — HMAC-SHA256 over a canonical serialization

### 2.1 The tag

Add one field to `SaveData`:

```
Mac string `json:"mac,omitempty"`   // hex(HMAC-SHA256(preimage)); "" only for grandfathered schema-4
```

- **Save**: set `d.Schema = 5`; quantize the float (§2.3); `d.Mac =
  hex(HMAC-SHA256(key, preimage(d)))`; then `json.MarshalIndent(d)` to disk as
  today.
- **Load**: `json.Unmarshal` → `d`; if `d.Schema >= 5`, recompute
  `want = HMAC-SHA256(key, preimage(d))` and compare `hmac.Equal([]byte(d.Mac
  decoded), want)` — constant-time. Mismatch → tamper policy (§4). Schema ≤ 4 →
  no MAC to check (grandfather, §5).

### 2.2 The preimage — canonical, and drift-proof by construction

```
preimage(d) = domainTag ‖ 0x00 ‖ json.Marshal(dWithMacZeroed)
```

- `domainTag = "dexel-save-integrity-v1"` — domain separation, so the tag can
  never be replayed into another context and so the scheme is versionable (bump
  to `-v2` to rotate). The `schema` int is *already inside `SaveData`*, so a
  schema-downgrade edit is caught for free.
- `dWithMacZeroed` = a copy of `d` with `Mac = ""`. Marshaled **compact**
  (`json.Marshal`, not `MarshalIndent`) — the MAC is over the **typed struct
  re-serialized by our own function**, never over the raw file bytes, so
  whitespace, indentation, and key order in the file are all irrelevant.

**Why this is stable (no false-positives):** Go's `encoding/json` is
deterministic — struct fields serialize in declaration order, `map` keys are
emitted sorted, and every array in `SaveData` is already sorted by `Snapshot`
(`ownedItems`, `ownedTints` via `sort.Strings`; `equipped` map sorted by json;
`history` is ordered oldest-first by construction). `float64` is emitted as its
shortest round-trippable decimal, and `Unmarshal` of that decimal returns the
**identical bits**, so `marshal(unmarshal(x)) == marshal(x)`. A save round-trips
to a byte-identical preimage.

**Why it is drift-proof:** the preimage is **the whole struct minus the tag**,
built from a zeroed-`Mac` copy — *not* a hand-listed field set. Any economy field
added to `SaveData` in the future is protected automatically, with zero
integrity-code changes and no chance of someone forgetting to add it to a manual
preimage builder. This is the single most important design property here.

### 2.3 The float — `sprint.unitsDone`

`unitsDone` is the only `float64` in `SaveData`. Go's shortest-repr round-trip
already guarantees per-value round-trip stability, but two extra rules make the
guarantee explicit, bounded, and testable:

1. **Quantize at snapshot.** `Snapshot` rounds `g.Progress` to 6 decimals
   (`math.Round(v*1e6)/1e6`) before it enters `SaveData`. This means the value
   *written to disk* and the value *fed to the MAC* are the identical `float64`,
   and two logically-equal saves (whose progress differs only by sub-µunit FP
   accumulation) produce the same tag. 6 decimals is far finer than any
   work-unit granularity the economy uses, so no visible progress is lost.
2. **The MAC sees exactly what disk sees.** Because the preimage is a
   re-marshal of the same quantized struct, there is no second, differently-
   rounded representation to disagree with.

(We deliberately do **not** MAC a hand-formatted fixed-point string of the
float; re-marshaling the typed struct is simpler, covers the whole struct
uniformly, and Go's round-trip guarantee makes it exact.)

### 2.4 Encoding

Key: 32 random bytes. Tag: `hex.EncodeToString` of the 32-byte HMAC (64 hex
chars) — ASCII, diff-friendly, obviously-a-digest in the file.

---

## 3. Key management — the honest part

### 3.1 The three options and their real ceilings

| Option | Stops text-editor cheat? | Ceiling on a *local, OSS* game | Cost |
|---|---|---|---|
| **(a) Baked-in key** (const in source/binary) | **Yes** | key is in the public source & binary → a source-reader can recompute | ~0 |
| (b) OS keychain (per-install random key) | Yes | user **owns the keychain** → reads their own entry & recomputes | 3 platform backends, a dep, CI friction |
| (c) Machine-derived (`KDF(machine-id ‖ salt)`) | Yes | `machine-id` is user-readable; **breaks save portability** across machines | KDF + platform machine-id probing; portability loss |

The critical observation: for a **local single-player** game, the attacker and
the machine owner are the **same person**. Every option's ceiling against "a
determined user who has the source + binary" is therefore **the same: zero.**
(b) and (c) move the key out of the source but not out of the *owner's reach* —
so they do not buy cheat-resistance, only complexity, and (c) actively harms a
cozy game where a user might copy their save to a new laptop.

### 3.2 Recommendation

**A baked-in 32-byte key, not obfuscated, openly documented** (`integrity.go`):

```
// integrityKey is the HMAC key for save-integrity checks. On an Apache-2.0
// open-source, fully-local, single-player game this key is NECESSARILY public:
// it is in this source file and in every compiled binary. It exists to stop
// CASUAL save-file editing (opening state.json and changing devCash), which it
// does completely. It does NOT — and here CANNOT — stop a determined user who
// reads this file and recomputes the MAC. That is an accepted, explicit
// non-goal. We do not obfuscate it: obfuscation whose inverse lives in the same
// public repo is theater, and this project prefers an honest limit to a fake one.
```

- **Not obfuscated**, on purpose. XOR-splitting or deriving the key would only
  mislead *us* about how much protection we have; the deobfuscator ships in the
  same repo. Honesty over theater is the house ethic (see the "loud failures" /
  "honest mechanics" lines throughout ROADMAP/ADRs).
- **The honest ceiling, stated in the ADR and the code**: *stops casual JSON
  editing; does not stop a source-armed reverse-engineer — an accepted
  non-goal.* We do not oversell it.
- Swapping to Fork K's keychain later is one function (`integrityKey()`), by
  design.

---

## 4. Tamper policy

**When the MAC does not verify (parsed OK, schema ≥ 5, tag missing or wrong):
reset the protected economy to a fresh baseline, preserve config, log one line,
preserve the offending file.**

Concretely:
- `Load` renames the file to `state.json.invalid` (never deleted — same
  discipline as the existing `.corrupt`/`.future` paths, so the user can send it
  in) and returns a new sentinel `ErrTampered` (wrapped with detail). It returns
  **no data** and never `(_, true, _)`.
- `loadOrImport`, seeing `ErrTampered`, logs one honest line ("save integrity
  check failed; starting a fresh economy — your file was preserved at
  state.json.invalid") and **returns immediately without running the legacy
  import.** The `game.Game` is already `New()` (fresh defaults), so the economy
  starts clean; the next autosave writes a valid, signed schema-5 save.
- `config.json` is loaded on a **separate, independent path** and is untouched —
  the user keeps their dexel's name.

Why not the alternatives:
- **Refuse to load** → a game that won't start. Bad UX for what might be a
  curious edit, and hostile in a cozy single-player context.
- **Load-but-flag** → we *know* it is tampered but honor the inflated values
  anyway; that defeats the entire feature.
- **Reset to baseline** → the cheat is neutralized, the game runs, the name
  survives, no scary modal. In the "start fresh, nothing shipped" context the
  downside of a reset (losing legitimately-earned progress) applies to *nobody*,
  and even in production it is the right trade for a hand-edited save.

**Critical anti-cheat subtlety:** `ErrTampered` must **not** be reported as "no
save" (`SaveData{}, false, nil`). If it were, `loadOrImport` would fall through
to the **legacy-import path**, which *grants items and refunds Dev Cash* — so a
cheater could tamper `state.json` to *trigger a legacy re-grant*. Returning a
distinct error (handled like the future-schema error: no load, no import, start
fresh) closes that vector. Corruption stays a separate, existing path: a
parse failure is still `.corrupt` (no MAC involved); a parse-OK/MAC-fail is the
new `.invalid`. Both preserve the file; both yield a running game.

---

## 5. Schema & migration

**Bump `CurrentSchema` 4 → 5. MAC is verified only at schema ≥ 5.**

- A pre-SEC-1 **schema-4** file has no `mac`. It is **grandfathered**: loaded as
  trusted (no verification), then re-saved as a signed **schema-5** on the next
  persist — a one-time, silent upgrade, never mistaken for tamper. This matches
  every prior additive bump in the codebase (1→2→3→4).
- Because nothing is shipped, this grandfather window exists mainly for the
  owner's own dev saves; it costs one `if d.Schema >= 5` guard and removes any
  chance of a false-positive on an existing local save.
- **`ErrFutureSchema` is preserved unchanged**: schema 6+ → renamed to `.future`
  and refused, never silently downgraded. The three refusal categories are now
  distinct and all non-destructive: `.future` (too new), `.corrupt` (unparseable),
  `.invalid` (MAC fail).

`config.json` is schema-light: a small `{ "name": "" }` today, additive prefs
later; a missing/empty file → defaults, never an error.

---

## 6. Privacy — confirmed unchanged (integrity only)

- **No new observation.** SEC-1 touches no provider, no `activity` crate, no
  `engine`, no `game.State()`/`StateMessage`, and **not** the content-free
  activity `Snapshot`. The MAC is derived entirely from data already persisted.
- **`state.json` stays content-free and still-tested.** `SaveData` gains exactly
  one field, `mac string` — a hex digest, not content; the existing
  `TestSaveDataIsContentFree` allow-list is extended by that one entry (field
  count 10 → 11) and the forbidden-substring guard passes (`mac` matches none).
  Every other content-free structural test is untouched.
- **The name is user-authored config, a different category from observed
  content.** ADR 0002/0009 forbid persisting *content the game observed* —
  keystrokes, titles, clipboard, the user's work. A name the user deliberately
  types to label their own pet is not surveillance; it is theirs. It lives in
  `config.json`, which is explicitly **outside** the content-free allow-list
  (config is where user-chosen text legitimately goes), so it neither weakens
  nor is measured by the observed-state privacy proof. The two-file split is what
  keeps that distinction clean.
- **The tag is not content and cannot carry content**: it is a fixed-width
  digest of the (content-free) protected fields.

---

## 7. Implementation plan — by EXCLUSIVE file ownership

### Contract seam — PIN these exact names (agents must not drift)

**Save (`internal/store`):**
- `SaveData.Mac string json:"mac,omitempty"`.
- `CurrentSchema = 5`.
- New `integrity.go`: `integrityKey() []byte` (baked const),
  `const macDomain = "dexel-save-integrity-v1"`, `macPreimage(d SaveData) []byte`
  (zeroes `Mac`, prepends domain, compact `json.Marshal`), `computeMAC(d SaveData)
  string` (hex HMAC-SHA256), `verifyMAC(d SaveData) bool` (`hmac.Equal`,
  constant-time), `var ErrTampered = errors.New("save integrity check failed")`.
- New `config.go`: `type ConfigData struct { Name string json:"name" }`
  (+ room for prefs), `ConfigPath() (string,error)` → `~/.config/dexel/config.json`,
  `LoadConfig(path) (ConfigData, error)` (missing → zero value, never an error),
  `SaveConfig(path, ConfigData) error` (same atomic tmp+rename+dir-fsync recipe
  as `Save`, **no MAC**).

**Float rule:** `Snapshot` quantizes `g.Progress` → `math.Round(v*1e6)/1e6` into
`SprintSave.UnitsDone`.

### Tasks (exclusive owners, no shared files)

**Task GO-1 — persistence + integrity (owns all of `internal/store/*`)**
The integrity helper, the config file, and the `store.go`/test edits are one
Go package with a compile dependency between them, so they are **one owner**
(the repo's exclusive-ownership rule is per *file*, but two agents in one
package courts collisions). New files: `integrity.go`, `config.go`,
`integrity_test.go`, `config_test.go`. Edits: `store.go` (add `Mac`; bump
`CurrentSchema=5`; `Snapshot` sets `Mac` + quantizes the float; `Save` computes
`Mac` before marshal; `Load` verifies at schema ≥ 5, renames tampered →
`.invalid`, returns `ErrTampered`; grandfather schema ≤ 4; preserve
`ErrFutureSchema`), `content_free_test.go` (add `"Mac": "string"` to the
`SaveData` allow-list, bump the expected field count).
- **Tests** (see §8): tamper→`ErrTampered`+`.invalid`; round-trip verifies;
  float-value stability; rich-state no-false-positive; schema-4 grandfather →
  re-saved signed schema-5; future-schema still refused; config round-trip;
  domain-tag separation.

**Task GO-2 — main wiring (owns `app/main.go`)**
- Load `config.json` alongside `state.json` in `loadOrImport` (independent path;
  missing → defaults; never blocks the economy).
- On `Load` returning `ErrTampered`: log one line, **do not** run the legacy
  import, leave `g` at `New()` defaults (§4).
- `persist()` writes `config.json` too (via `SaveConfig`), independently of the
  state save.
- Stash the loaded name where a future UI can read it (a field on `game.Game`
  set by main, or held in main) — **without** adding it to `SaveData`,
  `StateMessage`, or the content-free surface. No wire change in SEC-1.

**Task DOC-1 — ADR/spec (owns `docs/`)** — this design + ADR 0014 (done); add
the ADR 0014 row to `docs/adr/README.md`; note in `docs/ui-spec.md` that
`config.json` exists and is user-editable (no wire-contract change).

### Waves

`GO-1` → `GO-2` (depends on `ErrTampered`, `LoadConfig`/`SaveConfig`,
`ConfigPath` from GO-1) → **in-game gate (§8)**. `DOC-1` runs in parallel with
GO-1.

### Explicitly deferred (named so it doesn't creep)

Surfacing the name in the UI — a **Settings modal** (via the `add-a-menu-modal`
skill) + a `name` field on the WS `state` contract + a SET_NAME action — is a
**follow-up feature**, not SEC-1. SEC-1 provisions the config slot and its
load/save only. Fork K's OS-keychain key source is likewise named, not built.

---

## 8. Verification exit criteria + the gate

- **Tamper is detected and the policy fires**: a valid signed save, hand-edited
  to inflate `devCash` (or add an `ownedItems` id), on `Load` → `ErrTampered`,
  the file is renamed `state.json.invalid`, and `loadOrImport` starts a fresh
  economy **without** a legacy import. (Regression-guard the anti-cheat vector:
  a tampered file must **not** trigger the legacy re-grant path.)
- **Config edits are allowed**: hand-editing `config.json`'s `name` loads with
  no integrity check and no effect on the economy; a missing/malformed
  `config.json` yields defaults and never blocks startup.
- **Canonical-MAC stability (no false-positives)**: `Snapshot → Save → Load`
  verifies for a *rich* state (owned items + tints, equipped, multi-day history,
  a streak). Round-trip is byte-stable.
- **Float stability across values**: representative `unitsDone` values (0,
  exactly `target`, `12.3456789`, values with long decimal expansions) all
  round-trip MAC-stable after the 6-dp quantization.
- **Migration**: an unsigned schema-4 save loads (grandfathered, balances +
  A1/A2/A3 stats intact) and is re-saved as a **signed schema-5**; a schema-6
  save is still renamed `.future` and refused (`ErrFutureSchema` preserved).
- **Privacy holds and is provable**: `SaveData`'s content-free structural test
  passes with the single added `mac string`; provider / activity `Snapshot` /
  `StateMessage` unchanged; the name lives only in `config.json`.
- **Domain separation**: a tag computed under a different domain string (or key)
  fails verification.

**In-game gate (feature-build-and-verify — no change is trusted until the REAL
running game exercises it):** build the Go binary; run it once against a fresh
config to produce a signed schema-5 `state.json` + a `config.json`; **hand-edit
`state.json` to mint Dev Cash**, restart the binary (fake provider), and confirm
the HUD shows the **reset baseline** (not the inflated number), the log shows the
one integrity line, `state.json.invalid` exists, and the freshly-written
`state.json` re-verifies. Then **hand-edit the name in `config.json`**, restart,
and confirm the name persists and the economy is unaffected. Zero console
errors.
