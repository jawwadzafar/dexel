# 0014 — Save integrity: config/state split + baked-key HMAC, honest about its ceiling

Status: accepted (2026-08-22, SEC-1 design pass) · Honours ADR 0002/0009 (privacy), extends the schema-4 save (ADR 0013 lineage)

## Context

The save is plain JSON at `~/.config/dexel/state.json` (schema 4). Any user can
open it in a text editor and change `"devCash": 100` to `"devCash": 999999`, or
add item ids to `ownedItems`, and the game trusts it verbatim on next load. The
owner wants to **deter casual cheating** ("we don't want cheaters") — but some
fields are legitimately the user's to set: the **name** they give their Dexel,
and future cosmetic preferences. Those must stay freely editable while the
economy-critical fields become tamper-evident.

This is a **fully-local, single-player, Apache-2.0 open-source** game. That
last fact governs everything below: the key and the algorithm are visible in
the public source. (Historical note: the project's license was later changed
from Apache-2.0 to **MIT**; the reasoning here is unaffected — it turns only on
the source being *public*, which both licenses make it.) So the honest goal is **"stop casual JSON editing,"** not
"defeat a determined attacker who has the source + binary." We refuse to
oversell it.

Nothing is shipped yet (repo private, no external cohort of saves), so the
clean target shape can be designed directly rather than migrated into.

## Owner-decision fork (one, with a recommended default)

**Key storage: baked-in key (RECOMMENDED DEFAULT) vs OS keychain.** Implementation
proceeds on the baked key unless the owner chooses otherwise. Rationale in
Decision §3: on a local, single-player, OSS game the attacker *is* the machine
owner, who can read their own keychain and their own `machine-id` just as easily
as they can read the source — so the keychain/machine-derived options buy
**essentially zero** additional ceiling over a baked key while costing real
cross-platform work, a new dependency, and (for machine-derived) save
portability. Choose the keychain only if the owner explicitly wants the key out
of the source tree for reasons other than cheat-resistance.

## Decision

**1. Split user-config from protected game-state into two files.**
- `~/.config/dexel/state.json` — the existing `SaveData` (DevCash, XP, sprint,
  ownedItems, ownedTints, equipped, stats/history/streak, import provenance),
  now carrying an HMAC tag. **Protected.**
- `~/.config/dexel/config.json` — user-authored config: the Dexel's **name**
  (+ future cosmetic prefs). **Unsigned, hand-editable by design.**

Two files rather than one mixed file, because the split then falls out for
free: the content-free structural guard stays pinned to `state.json`
**unchanged** (privacy story intact, §6); "keep the name on a tamper reset"
needs no special code (config is a different file, never touched by the state
path); and a corrupt/tampered state can never take the user's chosen name down
with it.

**2. Integrity = HMAC-SHA256 over a canonical re-serialization of the protected
struct.** A new `mac` field on `SaveData` holds the hex tag. The MAC preimage is
`domain-tag ‖ json.Marshal(SaveData with mac zeroed)` — i.e. **the whole struct
minus the tag itself**, compact-marshaled. Computing it over the *typed struct*
(not the file bytes) makes it immune to whitespace/indent/key-order; Go's
`encoding/json` is deterministic (struct fields in declaration order, map keys
sorted, arrays already sorted by `Snapshot`) and round-trips `float64`
bit-exactly, so `marshal(unmarshal(x)) == marshal(x)`. `sprint.unitsDone` (the
one float) is **quantized to 6 decimals at snapshot time** so logically-equal
saves and reloads produce byte-identical preimages. Verify with
`hmac.Equal` (constant-time). **Making the preimage "the whole struct minus the
tag" means every future economy field is protected automatically** — no
per-field maintenance, the opposite of a fragile hand-built preimage.

**3. Key management = a baked-in 32-byte key, NOT obfuscated, honestly
documented.** It stops the text-editor cheat completely. Its ceiling —
**stated, not hidden** — is that a user who reads the public source (or extracts
the binary) can recompute the MAC; that is an **accepted, explicit non-goal**.
We do **not** obfuscate the key: obfuscation whose inverse lives in the same
public repo is theater, and this project's ethic is honesty over theater. The
keychain and machine-derived options do not raise this ceiling for a *local*
attacker who owns the machine, so they are not worth their cost (see Fork).

**4. Tamper policy = reset the protected economy to a fresh baseline, preserve
config, log one line.** A parse-OK-but-MAC-mismatch save is renamed to
`state.json.invalid` (never deleted — same discipline as `.corrupt`/`.future`),
`Load` returns a new `ErrTampered` sentinel, and `loadOrImport` starts the
economy fresh **without running the legacy import** (so tampering can't trigger
a legacy re-grant). The next autosave writes a valid signed save. `config.json`
(the name) is loaded independently and untouched. Refuse-to-load is bad UX
(broken game); load-but-flag would honor the cheat; reset neutralizes the cheat,
keeps the game running, and keeps the name — the right call for the "start
fresh, nothing shipped" context.

**5. Schema 4 → 5, additive; MAC verified only at schema ≥ 5.** A pre-SEC-1
schema-4 file has no `mac`; it is **grandfathered** (loaded as trusted) and
re-saved as a signed schema-5 on the next persist — never mistaken for tamper.
`ErrFutureSchema` is preserved unchanged (schema 6+ → `.future`, refused).

## Consequences

- **Privacy holds and stays provable.** No provider, no activity `Snapshot`, no
  `StateMessage`, no new observation. `state.json` remains content-free and its
  structural allow-list test remains in force (gaining one `string` field,
  `mac` — a digest, not content). The user-authored **name** is a *different
  category* from observed activity — data the user deliberately writes about
  their own pet, not surveillance of their work — and it lives in `config.json`,
  explicitly outside the content-free allow-list. ADR 0002/0009's invariant
  ("no observed content persisted") is unchanged, and the two-file split keeps
  that boundary crisp rather than muddying it with a free-text field.
- **Casual cheating is deterred; the limit is stated, not sold.** Editing
  `state.json` in an editor now fails verification and resets the economy. A
  determined user with the source can still recompute the MAC — documented as a
  non-goal, not a bug.
- **Every future economy field is auto-protected** (preimage = whole struct
  minus tag); adding a field needs no integrity-code change.
- **Migration is trivial and safe**: schema-4 saves grandfather in and upgrade
  on next save; future-schema refusal still fires; balances/counters intact.
- **Self-healing**: a tampered or corrupt state resets to a valid signed save on
  next run, with the file preserved for inspection and the name kept.
- **Deferred, named so it doesn't creep**: surfacing the name in the UI (a
  Settings modal + a WS field) is a follow-up feature — SEC-1 only provisions
  the config slot and its load/save. The OS-keychain option remains named, not
  built.
