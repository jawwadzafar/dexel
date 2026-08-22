# DB-1 design — SQLite persistence, carrying SEC-1's integrity into the DB

Status: design pass (2026-08-22) · Extends SEC-1 (`docs/plan/SEC-1-design.md`,
ADR 0014) · Constrained by the in-flight release pipeline
(`scripts/build-release.sh`: `CGO_ENABLED=0` for linux/windows × amd64/arm64)

Scope: move dexel's **game state** from `~/.config/dexel/state.json` to
**SQLite** at `~/.config/dexel/state.db`, carrying the SEC-1 HMAC forward so
tampering is still detected and quarantined. `config.json` stays plain JSON.
Design only — no implementation in this pass.

---

## 0. Forks that need an OWNER decision (surfaced first)

There are exactly two, both with a recommended default. **DB-1 ships on the
defaults without asking.**

- **Fork DB-A — do DB-1 now, or fold it into P2 when the first log table
  actually exists?** Honest framing: today's state is **< 10 KB, read and
  written as a whole, by one goroutine, every 30 s** (`autosaveInterval`,
  `app/main.go`). SQLite buys that workload **nothing** — not speed, not
  safety (the tmp+fsync+rename+dir-fsync recipe already gives atomicity), not
  queryability nobody queries. Its entire value is as the **platform for
  PRODUCT-EVOLUTION's append-mostly logs** (P2 sessions, P4 moments, P6
  scrapbook reads). So the fork is real: build the container now as pure infra,
  or build it in P2 alongside its first genuine consumer.
  **Default taken: NOW.** Two reasons. (1) The container swap is *cheapest at
  minimum state size* — one snapshot row, one MAC, one import path. Doing it
  mid-P2, with a live session log and a new schema bump in the same PR, is the
  same work plus a much worse blast radius. (2) It is on the owner's
  FULL-EXECUTION MANDATE (`docs/plan/ROADMAP.md`) as its own line item, ahead of
  P1/P2. Choose "fold into P2" only if the owner would rather not pay Fork DB-B
  until a feature actually needs it.

- **Fork DB-B — accept the dependency + binary-size cost of an embedded SQL
  engine?** `modernc.org/sqlite` is a transpiled port of SQLite's C source: it
  turns `app/go.sum` from **2 lines into ~40**, adds ~7 modules
  (`modernc.org/libc`, `memory`, `mathutil`, …), and adds an **estimated
  +8–13 MB to every release binary** (roughly doubling it) — on a project whose
  ADR 0011 made an explicit cost promise about a thing the user leaves open all
  day. The user sees a bigger download today for a capability they cannot see
  until P2. It also adds a **new BSD-3-Clause obligation** to
  `THIRD-PARTY-LICENSES.md`, which ships inside every release archive.
  **Default taken: ACCEPT.** A one-time +10 MB download is not a real cost to a
  desktop user in 2026, the *runtime* cost is ~zero (the engine is only touched
  on open and on a 30 s autosave), and the alternative — hand-rolling an
  append-log format with its own integrity story for P2 — is strictly worse
  engineering than using the most-tested storage engine in existence. The
  **actual** size delta is not guessed: it is a measured Wave-1 exit criterion
  (§7). Choose otherwise only if the owner wants a hard binary-size ceiling.

**Deliberately NOT a fork** (decided here, recorded so nobody re-opens it): the
driver (§1 — the CGO constraint decides it), the table layout (§2 — decided:
signed snapshot row now, chained append tables when P2 needs them), the journal
mode (§4.5 — decided: `DELETE`), and whether the schema counter bumps (§4.1 —
decided: it does **not**).

---

## 1. Driver — `modernc.org/sqlite`, and the constraint that decides it

| Option | cgo? | Release matrix survives? | Verdict |
|---|---|---|---|
| **`modernc.org/sqlite`** (transpiled C → pure Go) | **no** | **yes** — `CGO_ENABLED=0` cross-builds all four targets from the one Linux runner | **CHOSEN** |
| `mattn/go-sqlite3` (cgo wrapper) | yes | **no** | rejected |
| `crawshaw.io/sqlite`, `zombiezen.com/go/sqlite` | wrap cgo or wrap modernc | n/a | rejected (either the same cgo problem, or a second layer over the same engine) |
| No SQLite at all (keep JSON; hand-roll P2's log) | n/a | yes | rejected — see Fork DB-A |

`mattn/go-sqlite3` is not a close call, it is **disqualified by the pipeline
that is being built right now**. `scripts/build-release.sh` builds
linux/amd64, linux/arm64, windows/amd64 and windows/arm64 with
`CGO_ENABLED=0` precisely so they cross-compile from the single Linux
self-hosted runner (its own comment: "pure Go — cross-compiles cleanly from any
host"). Only `darwin/arm64` is `CGO_ENABLED=1`, and only because
`internal/activity/provider_darwin.go` genuinely needs Cocoa — which is exactly
why that target is *already* gated behind a mac runner that does not exist yet.
Adding a cgo dependency to the **shared** code path would drag all four
currently-cross-buildable targets into needing per-target C toolchains
(mingw-w64 for two Windows arches, an aarch64 sysroot for Linux arm64). That
trades a working release pipeline for a database feature. Non-starter.

**Honest status of `modernc.org/sqlite`** (verified 2026-08-22 via pkg.go.dev
and the upstream GitLab README, not from memory):

- Current release **v1.57.0** (published 2026-08-19), embedding **SQLite
  3.53.3**. Actively released — this is not a parked project.
- Imported by **~3,500 packages**; it is *the* standard pure-Go choice, and the
  one every "how do I use SQLite from Go without cgo" answer converges on.
- **Every target in our matrix is supported**: linux/amd64, linux/arm64,
  windows/amd64, windows/arm64, darwin/arm64 (23 GOOS/GOARCH pairs total).
- Registers with `database/sql` under the driver name **`sqlite`**. DSN
  pragmas via `_pragma=name(value)`; transaction locking via `_txlock`.

**Caveats, stated not hidden:**

1. **It is transpiled C, not idiomatic Go.** A bug in the translation layer is
   harder to read and diagnose than a bug in a normal Go library. Upstream's
   answer is that the full SQLite test suite runs against the generated code on
   every supported platform; that is a strong answer, but it is not the same
   thing as "this is small readable Go."
2. **Slower than the cgo driver** — commonly ~1.5–3× on write-heavy
   benchmarks. Completely irrelevant here: one row, one write per 30 s.
3. **`modernc.org/libc` must be pinned to the exact version in
   `modernc.org/sqlite`'s own `go.mod`** — upstream says so explicitly. This is
   a real, repeatable footgun on `go get -u`; §7 makes it a review checkpoint.
4. **`go test -race` will get slower.** The race detector instruments a very
   large amount of transpiled code. `.github/workflows/build.yml`'s `go` job
   runs `go test -race ./...`; if that job's wall time regresses badly, the fix
   is scope (`-race` on the packages that need it), not dropping `-race`.
   Measure it, don't assume (§7 exit criteria).
5. **Locking on network filesystems.** SQLite's POSIX advisory locks are
   unreliable on NFS/SMB, and `~/.config` *can* be network-mounted. Accepted:
   dexel is single-process and single-writer, and WAL — which is worse on
   network filesystems, not better — is not being used (§4.5).

---

## 2. What moves, what stays, and the table layout

### 2.1 Field-by-field

**Moves to `state.db`** — everything on `SaveData` today, i.e. every field
SEC-1 §1.1 classified **PROTECTED**: `schema`, `devCash`, `xp`, `sprint`
(index + unitsDone), `ownedItems`, `ownedTints`, `equipped`, `stats`
(today/lifetime counters, `coinsToday`, the 30-day `history`, `streak`),
`importedFromRust`, `importedAt`, and `mac`.

**Stays exactly as it is — `~/.config/dexel/config.json`, plain JSON,
unsigned, hand-editable.** This is not an oversight, it is ADR 0014's whole
point: the dexel's **name** (and future cosmetic prefs) are the user's to edit,
and putting them in a signed store would make "hand-editing my pet's name" a
tamper event. `config.go` is **untouched by DB-1** — same `ConfigPath()`, same
`LoadConfig`/`SaveConfig`, same `writeFileAtomically` recipe. The
content-free structural proof stays crisp for the same reason it did in SEC-1:
free text lives in exactly one place, and that place is not the protected
store.

**Also stays JSON: the legacy Rust save** (`legacy.go`). It is read-only,
never written, and belongs to a frozen build we do not get to change.

### 2.2 The three layouts, weighed honestly

**(a) Fully normalized** — `kv` for scalars, `owned_items(item_id)`,
`owned_tints(key)`, `equipped(slot, item_id, tint_id)`, `history_days(date,
…counters)`, `streak`.
- *For:* idiomatic SQL; every value individually queryable; a future "show me
  my best week" is a `SELECT`, not a full deserialize.
- *Against:* the SEC-1 MAC becomes a **hand-built preimage over rows**. ADR
  0014's single best property — *"preimage = the whole struct minus the tag,
  so every future economy field is protected automatically, no per-field
  maintenance"* — is destroyed. Every new column becomes a new line in a
  canonical-row-serializer that a future agent can forget, and forgetting it is
  a **silent anti-cheat hole**, not a test failure. Also: ~7 tables and a
  row↔struct mapper for data that is *never* queried piecemeal, because
  `Apply`/`Snapshot` are whole-state operations by construction.

**(b) Single-row snapshot** — one row holding the canonical serialized state +
schema + MAC.
- *For:* `integrity.go` survives essentially verbatim; `content_free_test.go`
  is **completely unchanged** (it reflects over Go structs, which do not
  change); the MAC's auto-protection property is preserved exactly; the
  implementation is small enough to review in one sitting.
- *Against:* SQLite is, for today's data, a **container for a JSON blob**. That
  criticism is fair and I am not going to dress it up — the win is the platform,
  not this row.

**(c) Hybrid** — (b) for the economy snapshot, **plus** normalized
append-mostly tables for the logs that are genuinely coming.
- P2 adds a **sessions log** (one row per finished session), P4 a **moments
  log** (one row per moment fired), P6 reads back over both. Those are
  append-mostly, grow without bound, are read as ranges, and are the exact
  shape a table is good at. Cramming them into the snapshot blob would mean
  re-serializing and re-MACing the entire history on every session end — the
  thing that actually gets slow.

**CHOSEN: (c), built incrementally — the snapshot row **now**, the log tables
**when their feature lands**.** DB-1 creates the snapshot row and nothing else.
Creating empty `sessions`/`moments` tables today would be speculative schema
with no integrity story, no tests, and no consumer, and it would have to be
redesigned by P2 anyway. The hybrid is the *destination*; DB-1 lays the first
half of it and names the second half (§2.4) so P2 does not have to re-derive it.

### 2.3 The chosen layout (DDL)

```sql
-- One schema counter for the whole store (§4.1). Mirrors payload.schema.
PRAGMA user_version = 5;

CREATE TABLE IF NOT EXISTS state (
  id      INTEGER PRIMARY KEY CHECK (id = 1),  -- exactly one row, enforced by SQLite
  schema  INTEGER NOT NULL,                    -- denormalized mirror of payload.schema
  payload BLOB    NOT NULL,                    -- canonical compact JSON of SaveData, mac zeroed
  mac     TEXT    NOT NULL                     -- hex HMAC-SHA256 over macDomain ‖ 0x00 ‖ payload
) STRICT;
```

Four deliberate choices:

- **`STRICT`** (SQLite ≥ 3.37; we embed 3.53.3): SQLite's default type
  affinity would happily accept `UPDATE state SET schema='lots'`. `STRICT`
  rejects it at the engine. Cheap, and it removes a whole class of "what does a
  weird value do" question from the loader.
- **`payload` is `BLOB`, not `TEXT`**: the MAC is verified over the **bytes as
  stored** (§3.1), so the column must round-trip bytes with zero encoding
  interpretation. `BLOB` guarantees that; `TEXT` invites a UTF-8-normalization
  argument nobody should have to have.
- **`CHECK (id = 1)` on the primary key**: "exactly one row" is a schema
  constraint, not a convention a future writer has to remember. (The loader
  still counts rows anyway — see §3.3, row 5: a cheater can drop and recreate
  the table without the constraint.)
- **`schema` is a mirror, not a source of truth.** It exists so
  `sqlite3 state.db 'select schema from state'` answers a support question
  without a MAC key. The **authority** is `payload.schema`, because that one is
  signed. A disagreement between them is a tamper signal (§3.3, row 4).

Write path — one statement pair, one transaction:

```sql
BEGIN IMMEDIATE;
INSERT INTO state (id, schema, payload, mac) VALUES (1, ?, ?, ?)
  ON CONFLICT(id) DO UPDATE
     SET schema = excluded.schema, payload = excluded.payload, mac = excluded.mac;
PRAGMA user_version = <schema>;   -- header write; transactional, journaled with the row
COMMIT;
```

### 2.4 Future tables — named, NOT built by DB-1

The direction P2/P4 should follow, recorded so it is not re-derived:

- **`sessions` (P2)** and **`moments` (P4)**: append-only, one row per event,
  content-free columns only (counts, durations, ISO dates, catalog ids).
  Integrity via a **per-row MAC chained to its predecessor**
  (`row_mac = HMAC(domain-v1-log ‖ prev_mac ‖ canonical_row)`), with the
  **chain head bound into the signed snapshot row**. That makes *appending* one
  MAC computation instead of re-signing the whole log, and — because the head
  is inside the signed payload — makes **deleting or reordering rows detectable**,
  which a bag of independent per-row MACs would not.
- **P2's optional project name is CONFIG, not a log column.** PRODUCT-EVOLUTION
  P2 already says "user-typed CONFIG, never observed." It belongs in
  `config.json`, and the `sessions` table must reference it by nothing at all.
  This is the boundary most at risk of being crossed by accident; it is written
  down here on purpose.
- **P4's earn-only collectible *set* goes in the snapshot payload**, alongside
  `OwnedItems` — it is a small set of catalog ids, and putting it there gets it
  MAC-protected for free (ADR 0014's auto-protection). Only the *log* of when
  each moment fired needs a table.
- **P6 (scrapbook) adds no table**: it is a read over P2/P4 data plus the
  existing history. Read-only, no new integrity story.

---

## 3. Integrity in the DB

### 3.1 The preimage does not change — at all

The SEC-1 preimage is `macDomain ‖ 0x00 ‖ json.Marshal(SaveData with Mac "")`.
The DB's `payload` column stores **exactly those `json.Marshal` bytes**, and
`mac` stores the tag over them. Consequences, all good:

- **`macDomain` stays `"dexel-save-integrity-v1"`.** No rotation, because the
  scheme did not change — only the container did. Rotating it would invalidate
  every existing save for no security benefit, and would break the next bullet.
- **A schema-5 `state.json`'s existing `mac` is valid verbatim as the DB row's
  `mac`.** The one-time import (§4.3) therefore **carries the tag across
  instead of re-signing** — the import *verifies* the data it moves rather than
  laundering it. A migration that re-signs whatever it is handed cannot tell a
  good save from a tampered one; this one can.
- **`integrity.go` is refactored, not rewritten**, and its behaviour is
  bit-identical:

  ```go
  func canonicalBody(d SaveData) []byte          // d.Mac = ""; json.Marshal(d)   (was inline in macPreimage)
  func macPreimage(body []byte) []byte           // macDomain ‖ 0x00 ‖ body
  func computeMACBytes(body []byte) string       // hex HMAC-SHA256
  func verifyMACBytes(body []byte, mac string) bool // hmac.Equal, constant-time

  func computeMAC(d SaveData) string  { return computeMACBytes(canonicalBody(d)) }  // unchanged behaviour
  func verifyMAC(d SaveData) bool     { ... }                                        // unchanged behaviour
  ```

  Every existing test in `integrity_test.go` — domain separation, float
  quantization stability, rich-state no-false-positive — keeps passing against
  the struct-level wrappers, untouched.
- **Verification gets strictly *stronger*.** Today the MAC is checked against a
  *re-serialization* of the parsed struct, so a `state.json` carrying extra
  unknown keys, or different formatting, still verifies. The DB checks the MAC
  against the **stored bytes**, so the payload must be byte-exact. Nothing
  legitimate is affected (this build always writes what it reads), and one
  small forgery surface closes.
- **`content_free_test.go` is untouched.** It reflects over `SaveData`,
  `StatsSave`, `DayBucketSave`, … — Go types DB-1 does not modify. The privacy
  proof is unchanged *by construction*, which was the decisive argument for
  layout (b) over (a).
- **`Snapshot`'s float quantization (`math.Round(v*1e6)/1e6`) stays exactly as
  is.** It is what makes the payload byte-stable across save→load→save, which
  the byte-exact verification above now depends on rather than merely benefits
  from.

### 3.2 Verification on open — the exact gate order

`Load` keeps today's **branch order** (future-schema *before* MAC), because
`app/main.go`'s `loadOrImport` and its tests depend on the distinction between
`ErrFutureSchema`, `ErrTampered`, and "no save":

1. Open (`MkdirAll` the parent first). Open failure → error (not "no save").
2. **`PRAGMA quick_check`** ≠ `ok` → quarantine `.corrupt`, error. This is the
   DB analogue of today's `json.Unmarshal` failure. It is worth doing on every
   open: on a < 64 KB database it costs microseconds, and it converts a
   subtly-corrupt page into a clean startup quarantine instead of a mid-session
   query error hours later.
3. **`PRAGMA user_version` > `CurrentSchema`** → quarantine `.future`,
   `ErrFutureSchema`. Deliberately **before** any MAC work: a future build's
   payload may contain fields this build cannot represent, and we must not even
   consider loading it.
4. `SELECT count(*) FROM state` ≠ 1 → **`ErrTampered`** (see §3.3 row 5).
5. `SELECT schema, payload, mac FROM state WHERE id = 1`;
   `verifyMACBytes(payload, mac)` false → quarantine `.invalid`,
   `ErrTampered`.
6. `json.Unmarshal(payload)` failure → `ErrTampered` + `.invalid`. (A valid MAC
   over unparseable bytes means *our own* bug, not a cheat — but the response is
   identical and it must never degrade to "no save.")
7. Cross-check `payload.schema == state.schema == user_version`; any
   disagreement → `ErrTampered` + `.invalid`. The signed field wins; a mismatch
   means a column or the pragma was edited.
8. `payload.schema > CurrentSchema` → `.future`, `ErrFutureSchema`
   (defence in depth; step 7 already catches the way this happens).
9. Return `(SaveData, true, nil)`.

**No grandfathering in the DB path.** DB-1 ships after SEC-1, so a `state.db`
whose payload is schema < 5 cannot exist — every row this build has ever
written is signed. The schema-≤-4 grandfather branch lives *only* in the JSON
import path (§4.3), where `TestSchema4FileIsGrandfatheredThenResavedSignedSchema5`
already covers it.

**Quarantine mechanics.** Unlike a JSON file, a DB has an open handle and
possible siblings:
- **Close the connection first**, then `os.Rename` — Windows will not rename an
  open file.
- **Rename, do not copy.** Same discipline as `.corrupt`/`.future`/`.invalid`
  today: the poisoned file is moved *aside* so the next `Save` creates a fresh
  one, and the move is atomic. (A copy would leave the game writing into the
  file it just declared untrustworthy.)
- **Move the `-journal` sibling with it** (`state.db-journal` →
  `state.db.invalid-journal`) so the quarantined pair stays openable for
  support, and no stray journal is left beside the new DB.
- If the rename fails, return the sentinel wrapped with the rename failure —
  byte-for-byte the policy `Load` already implements for `.future`/`.invalid`.

### 3.3 SQLite-specific tamper surfaces

The threat model is unchanged (ADR 0014 §3): stop the **curious player with a
text editor**, honestly, and do not pretend to stop someone who reads the
public key out of `integrity.go`. The editor is now `sqlite3` instead of
`vim`, which changes the *surface*, not the ceiling.

| # | What a cheater does | Result |
|---|---|---|
| 1 | `UPDATE state SET payload = <edited JSON>` | MAC fails → `.invalid` + `ErrTampered` |
| 2 | `UPDATE state SET mac = '…'` | MAC fails (cannot forge without recomputing) |
| 3 | Edits both payload and mac *consistently* | Succeeds — and is **exactly** ADR 0014's stated, accepted non-goal. Unchanged by DB-1. |
| 4 | `UPDATE state SET schema = 3` / `PRAGMA user_version = 3` | Caught by the step-7 cross-check against the signed `payload.schema` |
| 5 | `PRAGMA user_version = 99` | `.future` quarantine. Self-inflicted, not a win. |
| 6 | **`DELETE FROM state`** / `DROP TABLE state` | **`ErrTampered`**, *not* "no save". This is the one surface that needed a genuine decision: the whole point of SEC-1's distinct sentinel is that "no save" is the only branch allowed to reach the legacy Rust import, which *grants items and refunds Dev Cash*. An existing-but-empty DB is treated as tamper, so **row deletion cannot trigger a legacy re-grant.** This is strictly better than the JSON design, where deleting `state.json` was indistinguishable from never having one. |
| 7 | `INSERT` a second row (after dropping the `CHECK`) | Row count ≠ 1 → `ErrTampered` |
| 8 | Recreates `state` as a `VIEW` computing a payload | Still needs a valid MAC. No gain. |
| 9 | Deletes the whole `state.db` file | "No save" → legacy chain runs. Identical to deleting `state.json` today; accepted, unchanged. |
| 10 | Hex-edits DB pages, `ATTACH`es another DB, adds triggers | `quick_check` catches structural damage → `.corrupt`; triggers cannot produce a valid MAC |
| 11 | Reverts to an older `state.db` they backed up | Verifies fine — it *is* a genuine save. Save-scumming is out of scope here as it was for JSON (a monotonic counter would be the fix; not worth it, named not built). |

**The rule, stated once:** the MAC must cover *everything a cheater would want
to edit*. It does, because the payload **is** the entire economy — and the two
things outside the payload (`schema` column, `user_version`) are either
mirrors of a signed field (cross-checked, row 4) or fail closed (row 5).

### 3.4 Tamper policy — unchanged from ADR 0014

Quarantine the file, `ErrTampered`, fresh economy from `game.New()`, **no
legacy import**, `config.json` (and therefore the dexel's name) untouched, one
log line, next autosave writes a valid signed DB. Nothing about this changes;
only the filename in the log does.

---

## 4. Versioning, migration, crash-safety

### 4.1 One schema counter, and it does NOT bump for DB-1

**Decision: `CurrentSchema` stays `5`.** DB-1 changes the *container*, not the
save format — `SaveData` gains no field, loses no field, and its JSON encoding
is byte-identical. Bumping to 6 would mean: invalidating the carry-across
property of §3.1 (forcing the import to re-sign rather than verify), burning a
schema number P2 already has earmarked (`4 → 5` in PRODUCT-EVOLUTION's P2
section, now `5 → 6`), and lying about what changed. The next bump belongs to
whoever adds a *field*.

**Decision: one counter, mirrored, not two.** `PRAGMA user_version` carries
`CurrentSchema` — the same number as `payload.schema` and the `schema` column.
A separate "container version" alongside a "logical version" would be two
truths that drift, and the existing suite
(`TestFutureSchema6RefusalStillFiresAfterTheSchema5Bump`) is keyed to one
number. `user_version` earns its place by letting step 3 refuse a future DB
**without parsing or trusting anything**, and by being what `sqlite3
state.db 'pragma user_version'` prints for a support request.

**No `meta` table.** `user_version` is the SQLite idiom for exactly this and
needs no DDL, no row, and no integrity story of its own.

### 4.2 `Load`'s full decision tree

```
dbPath  = ~/.config/dexel/state.db        (DefaultPath())
jsonPath = ~/.config/dexel/state.json     (derived by the store, not by main.go)

state.db exists?
├─ YES → open + verify per §3.2
│         ├─ ok            → (SaveData, true, nil)
│         ├─ future        → .future  quarantine, ErrFutureSchema
│         ├─ tampered      → .invalid quarantine, ErrTampered
│         └─ corrupt       → .corrupt quarantine, error
│        (state.json, if any, is IGNORED — the DB is the source of truth. §4.3)
└─ NO
   ├─ state.json exists? → ONE-TIME IMPORT (§4.3)
   │    ├─ json Load ok (incl. schema ≤ 4 grandfathering) →
   │    │     write the DB → rename state.json → state.json.imported →
   │    │     (SaveData, true, nil)
   │    ├─ ErrTampered      → propagate; NO DB is created; .invalid as today
   │    ├─ ErrFutureSchema  → propagate; NO DB is created; .future  as today
   │    └─ corrupt          → propagate; NO DB is created; .corrupt as today
   └─ neither → (SaveData{}, false, nil)   ← the ONLY "no save": legacy Rust chain runs
```

The failure branches deliberately **create no DB**. A tampered or future-schema
`state.json` must not be converted into anything; it must stay quarantined and
return its sentinel, so `loadOrImport`'s guard still blocks the legacy re-grant
exactly as `TestLegacyImportIsNotReachableViaATamperedFile` asserts.

### 4.3 The one-time JSON import

- **Reuse today's `Load` body verbatim**, renamed `loadJSON(path)`. This is the
  single highest-leverage decision in the migration: `.corrupt` handling,
  future-schema refusal, MAC verification at schema ≥ 5, and schema-≤-4
  grandfathering are **already written, already tested, and already correct**.
  DB-1 does not get to re-derive them; it calls them.
- Import is **verify-then-move, never trust-and-copy**: `loadJSON` verifies the
  MAC (or grandfathers an unsigned schema-≤-4 file), and only a *passing* file
  is written into the DB.
- For a schema-5 source, `payload` is `canonicalBody(d)` and `mac` is the
  file's own tag, **carried across unchanged** (§3.1). For a grandfathered
  schema-≤-4 source there is no tag, so the write signs it at
  `CurrentSchema = 5` — which is precisely what
  `TestSchema4FileIsGrandfatheredThenResavedSignedSchema5` already describes,
  now landing in a DB instead of a file.
- **Then** `state.json` → `state.json.imported`. Order matters: DB written and
  committed *first*, rename second. A crash between them leaves both a valid DB
  and the original JSON; the next open takes the DB branch and simply never
  looks at the JSON again. Idempotent, no double-import, no window where
  neither exists.
- **Import is once, structurally.** The DB branch never consults `jsonPath`, so
  a user who later drops a `state.json` back into place is ignored. That also
  closes an obvious cheat (hand-write a favourable JSON and delete the DB —
  which would work, but requires deleting the DB, i.e. surface #9, already
  accepted).
- **Rename, do not delete.** Same discipline as every other quarantine in this
  package: the file a user might need is never destroyed.

### 4.4 The legacy Rust chain still works

Unchanged, and it must keep being provable. On a fresh machine with **no
`state.db`, no `state.json`, and a Rust `save.json`**: `Load` returns
`(SaveData{}, false, nil)` → `loadOrImport` reaches `LoadLegacy` →
`ImportLegacy` → `Apply` → `store.Save(savePath, imported)`, which now
**creates `state.db`** rather than writing `state.json`. `legacy.go` itself is
**not modified at all** — it produces a `SaveData`, and `SaveData` is exactly
what the DB stores. Test: §6, `TestLegacyRustChainOnAJSONLessFreshMachineWritesTheDB`.

### 4.5 Journal mode and durability — `DELETE`, not WAL

**Decision: `journal_mode = DELETE` (the default, set explicitly and asserted),
`synchronous = FULL`, `busy_timeout = 5000`, `SetMaxOpenConns(1)`.**

WAL is the reflexive answer and it is the **wrong** one here:

- WAL's benefit is *concurrent readers alongside a writer*. dexel has **one
  writer and zero concurrent readers** — `app/main.go`'s single-owner loop is
  the only thing that ever touches the store (`grep` confirms `internal/store`
  is imported by `main.go` and nothing else).
- WAL leaves **`-wal` and `-shm` files at rest**, which makes the quarantine
  story worse: renaming `state.db` alone can strip committed transactions that
  still live in the `-wal`, so "move the file aside" stops being obviously
  correct — the exact kind of subtlety that turns an anti-cheat path into a bug.
- WAL needs **shared memory / mmap**, the least-exercised corner of a
  transpiled SQLite on Windows, and it is **worse on network filesystems**
  (§1 caveat 5).
- Our access pattern is **open → one write → close**, every 30 s. Under WAL
  that means creating and checkpointing a `-wal` on each cycle: pure overhead
  for a benefit we cannot use.

`DELETE` + `synchronous = FULL` gives: **one file at rest**, full atomic
commit, and — importantly — SQLite fsyncs the *directory* itself when it
creates and removes the rollback journal, which is precisely the
belt-and-suspenders `syncDir` step `writeFileAtomically` had to do by hand. A
crash mid-commit leaves `state.db-journal`, and the next open rolls it back
automatically. That is the same guarantee tmp+rename gave, obtained from an
engine that has been proving it for twenty years instead of from ~50 lines we
maintain.

`writeFileAtomically`/`syncDir` **stay** in the package — `config.json` still
needs them, unchanged.

**Set the pragmas as explicit statements after open and read them back**, not
only via the DSN `_pragma=` form. A DSN typo fails *silently*; a read-back
assertion (§6, `TestJournalModeAndSynchronousAreAsDesigned`) does not.

---

## 5. API surface — exactly what changes for callers

`internal/store` is imported by **`app/main.go` and nothing else**. The whole
external contract is held stable:

| Symbol | Change |
|---|---|
| `Load(path) (SaveData, bool, error)` | **signature unchanged**; internals per §4.2 |
| `Save(path string, d SaveData) error` | **signature unchanged**; writes the DB |
| `Snapshot(*game.Game) SaveData` | **unchanged** (float quantization included) |
| `Apply(*game.Game, SaveData)` | **unchanged** |
| `ErrTampered`, `ErrFutureSchema` | **unchanged**, same semantics, same order |
| `SaveData` + every nested `*Save` type | **unchanged** — which is why `content_free_test.go` is untouched |
| `CurrentSchema` | **stays 5** (§4.1) |
| `LoadConfig`/`SaveConfig`/`ConfigPath`/`ConfigData` | **unchanged** |
| `LegacyPath`/`LoadLegacy`/`ImportLegacy` | **unchanged** |
| `writeFileAtomically`/`syncDir` | **kept** (config.json uses them) |
| **`DefaultPath()`** | **returns `~/.config/dexel/state.db`** — the one behavioural change |
| *new, unexported* | `openDB`, `loadJSON` (today's `Load` body), `jsonImportPath(dbPath)`, `quarantine(path, suffix)` |

**`main.go` changes: two log strings, no logic.** Because `DefaultPath()` is
just threaded through to `Load`/`Save`, `loadOrImport` needs no structural
edit — its `ErrTampered` / `ErrFutureSchema` / `ok` / legacy-fallthrough branch
order is already exactly what §4.2 preserves. The two lines worth fixing are
the ones that *construct* a path in their message:

```go
log.Printf("save integrity check failed; starting a fresh economy — your file was preserved at %s.invalid", savePath)
log.Printf("save schema is newer than this build supports; starting fresh — your file was preserved at %s.future: %v", savePath, err)
```

After DB-1, the quarantined file may be `state.json.invalid` (import branch)
rather than `state.db.invalid`, so hard-coding `savePath + ".invalid"` can now
print the wrong filename. Fix: drop the interpolation and log the error, which
already names the real path (`Load` puts it there today: *"original preserved
untouched at %s"*). Honest logging, one-line diff, no behaviour change.

Everything else in `main.go` — `persist()`, the autosave ticker, the
`loadOrInitConfig()` path, `hub.setInitialState` — is untouched.

**Deliberately NOT done in DB-1:** turning the store into a long-lived
`*store.DB` handle. Open→write→close costs ~1–3 ms against a 30 s autosave, and
keeping `Save(path, d)` stateless is what makes `main.go` a two-line diff and
keeps every existing test's `t.TempDir()` shape. The handle refactor is the
right move **when P2's session log makes writes frequent and multi-table** —
named here, not built.

---

## 6. Tests

**Unchanged and still enforced (the point, not an afterthought):**
`content_free_test.go` in full — all four structural tests, same allow-lists,
same field counts, reflecting over the same Go structs. `integrity_test.go`'s
domain-separation, float-quantization-stability, and rich-state
no-false-positive tests, against the struct-level `computeMAC`/`verifyMAC`
wrappers. `store_test.go`'s `Apply` validation and `ImportLegacy`
worked-example / never-loses-currency tests (pure functions, no I/O).

**Retargeted from a `.json` path to a `.db` path (mechanical):**
`TestSaveLoadRoundTrip`, `TestSaveLoadRoundTripVerifiesCleanly`, all of
`stats_test.go`'s round-trip + rollover + retention +
`TestFinalizeOnReloadFinalizesTheStaleRunningDayExactlyOnce`,
`TestConfigNameEditDoesNotAffectStateIntegrity`. `stats_test.go`'s
**raw-JSON-fixture** migration tests (`TestSchema1FileHasNoStatsKey…`,
`TestSchema2FileMigratesToSchema3…`, `TestSchema3FileMigratesToSchema4…`,
`TestFutureSchema6Refusal…`) now write their fixture as `state.json` and load
via the **import** branch — strictly *more* coverage than before, since each one
additionally proves the import path.
`TestConfigPathIsASiblingOfStatePathNamedConfigJSON` needs its expectation and
comment updated for `state.db`.

**New — `db_test.go`:**
- `TestDBSaveLoadRoundTrip` — Snapshot → Save → Load → Apply, rich state.
- `TestFreshInstallCreatesTheDBWithUserVersionAndOneSignedRow`.
- `TestLoadMissingDBIsNotAnError` — `(SaveData{}, false, nil)`.
- `TestTamperedPayloadIsDetectedAndDBRenamedToInvalid` — raw
  `UPDATE state SET payload=…` inflating `devCash`.
- `TestTamperedMacColumnIsDetected`.
- `TestTamperedSchemaColumnIsDetected` / `TestTamperedUserVersionDownIsDetected`
  — the step-7 cross-check.
- `TestFutureUserVersionIsRenamedToFutureNeverDowngradedInPlace`.
- **`TestDeletedStateRowIsTamperNotNoSave`** — the surface-#6 guard.
- `TestExtraStateRowIsTamper`.
- `TestCorruptDBFileIsRenamedToCorrupt` — garble bytes, expect `quick_check` to
  fire.
- **`TestLegacyImportIsNotReachableViaATamperedDB`** — the SEC-1 anti-cheat
  regression guard, ported.
- `TestQuarantineMovesTheJournalSiblingAndClosesTheHandleFirst`.
- `TestJournalModeAndSynchronousAreAsDesigned` — read the pragmas back.
- `TestSaveLeavesNoWalOrShmOrJournalFilesAtRest`.
- **`TestMacPreimageIsByteIdenticalToTheJSONEraPreimage`** — determinism:
  `canonicalBody(d)` equals the exact bytes the pre-DB-1 `macPreimage` hashed,
  so §3.1's carry-across is proven, not asserted.
- `TestSaveIsDeterministicForLogicallyEqualState` — two `Save`s of the same
  `SaveData` produce identical `payload` bytes and identical `mac`.

**New — `migrate_test.go`:**
- `TestOneTimeImportFromStateJSONKeepsBalancesAndRenamesToImported` — devCash,
  XP, owned, equipped, stats/history/streak all intact; `state.db` exists;
  `state.json` gone; `state.json.imported` present.
- `TestImportedJSONMacCarriesOverWithoutRecomputation`.
- `TestImportIsOneTimeAndTheDBWinsAfterwards` — re-create `state.json`, Load,
  assert the DB's values and that the new JSON is untouched.
- `TestImportOfSchema4JSONIsGrandfatheredThenStoredSignedInTheDB`.
- `TestImportOfTamperedJSONReturnsErrTamperedAndCreatesNoDB`.
- `TestImportOfFutureSchemaJSONReturnsErrFutureSchemaAndCreatesNoDB`.
- `TestImportOfCorruptJSONLeavesCorruptQuarantineAndNoDB`.
- `TestLegacyRustChainOnAJSONLessFreshMachineWritesTheDB` — §4.4.
- `TestConfigJSONIsUntouchedByEveryDBPath` — import, tamper, future, corrupt;
  `config.json` byte-identical throughout.

**New — CI, not a Go test:** a **`CGO_ENABLED=0` cross-build gate** in
`.github/workflows/build.yml`, because "the pure-Go driver keeps the release
matrix alive" is a claim that must be *enforced*, not trusted:

```sh
for t in linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
  CGO_ENABLED=0 GOOS=${t%/*} GOARCH=${t#*/} go build ./...
done
CGO_ENABLED=0 go test ./...   # the driver must also RUN without cgo
```

The second line matters as much as the first: it proves the driver is
functional, not merely compilable, in the configuration the shipped binaries
are built in. The existing `-race`/`CGO_ENABLED=1` job stays as it is.

---

## 7. Fan-out — exclusive ownership, waves, exit criteria

### Contract seam — PIN these names (agents must not drift)

`DefaultPath() → ~/.config/dexel/state.db` · `CurrentSchema = 5` (unchanged) ·
`macDomain = "dexel-save-integrity-v1"` (unchanged) · driver name `"sqlite"` ·
table `state(id, schema, payload, mac) STRICT` with `CHECK (id = 1)` ·
`PRAGMA user_version = CurrentSchema` · quarantine suffixes `.invalid`,
`.future`, `.corrupt` (unchanged) · imported JSON → `state.json.imported` ·
new unexported: `openDB`, `loadJSON`, `jsonImportPath`, `quarantine` ·
new in `integrity.go`: `canonicalBody`, `computeMACBytes`, `verifyMACBytes`
(and `macPreimage` now takes `[]byte`).

### Tasks

**Task DB-GO-1 — the store package (owns `app/internal/store/**`, plus
`app/go.mod` and `app/go.sum`).** New `db.go`; edits to `store.go`
(`DefaultPath`, `Load` → the §4.2 tree, `Save` → the DB write, today's `Load`
body renamed `loadJSON`) and `integrity.go` (the byte-level split of §3.1, with
the struct wrappers preserved). New `db_test.go`, `migrate_test.go`; retarget
the tests listed in §6. `legacy.go`, `config.go`, `content_free_test.go`:
**do not touch.** `go.mod`/`go.sum` are folded into this owner rather than split
out, because `go get modernc.org/sqlite` regenerates them as a side effect of
this task — a separate owner would be a guaranteed collision. **Pin
`modernc.org/libc` to the exact version in `modernc.org/sqlite`'s own
`go.mod`** (§1 caveat 3) and say so in the handoff.

**Task DB-GO-2 — main wiring (owns `app/main.go`).** The two log lines in §5.
Nothing else. Depends on DB-GO-1 only for the package to compile.

**Task DB-CI-1 — CI gate (owns `.github/workflows/build.yml`).** Add the §6
`CGO_ENABLED=0` cross-build + non-raced test matrix. **Do not touch
`release.yml` or `scripts/build-release.sh`** — the release-pipeline
modernization is in flight under another owner; `build-release.sh` already
builds `CGO_ENABLED=0` for all four targets, so DB-1 needs no change there,
only the CI proof that it still works.

**Task DB-DOC-1 — docs + licensing (owns `docs/adr/README.md`,
`docs/plan/ROADMAP.md`, `THIRD-PARTY-LICENSES.md`).** Add the ADR 0016 row;
add DB-1's status line to the ROADMAP mandate section; **add
`modernc.org/sqlite` and `modernc.org/libc` (BSD-3-Clause) to
`THIRD-PARTY-LICENSES.md` with the full BSD-3 text** — that file ships inside
every release archive (`scripts/build-release.sh`), so this is a distribution
obligation, not documentation polish. Runs in parallel with DB-GO-1.
(`docs/plan/DB-1-design.md` and `docs/adr/0016-sqlite-persistence.md` are owned
by this design pass and are already written.)

### Waves

```
Wave 1:  DB-GO-1        ‖  DB-DOC-1
Wave 2:  DB-GO-2  →  DB-CI-1        (both need Wave 1's go.mod/API landed)
Wave 3:  overseer clean-cache re-verify  →  real-game gate
```

### Exit criteria

- `go vet ./...` and `go test -race ./...` green from a **clean cache**
  (orchestration-playbook: an agent's green off a stale cache is not evidence),
  and `CGO_ENABLED=0 go test ./...` green.
- `CGO_ENABLED=0 go build ./...` succeeds for **all four** of linux/amd64,
  linux/arm64, windows/amd64, windows/arm64 — the Fork-DB-A/§1 premise,
  enforced in CI.
- `scripts/build-release.sh linux/amd64 windows/arm64` still produces working
  archives.
- **Measured and recorded in the handoff, not estimated:** the release binary
  size before vs after (Fork DB-B's real number), and the `go test -race ./...`
  wall time before vs after (§1 caveat 4).
- Every §6 test present and passing; `content_free_test.go` **byte-identical**
  to its pre-DB-1 form.
- `THIRD-PARTY-LICENSES.md` lists both new BSD-3-Clause components.
- **In-game gate** (`feature-build-and-verify` — no change is trusted until the
  real running game exercises it):
  1. Start from a real pre-DB-1 `~/.config/dexel/state.json` with a nonzero
     balance. Run the binary → HUD shows the **same** Dev Cash, `state.db`
     exists, `state.json.imported` exists, `state.json` is gone, zero console
     errors.
  2. Stop it. `sqlite3 state.db "UPDATE state SET payload = replace(cast(payload as text), '\"devCash\":<n>', '\"devCash\":999999')"`.
     Restart → HUD shows the **reset baseline**, not 999999; one integrity log
     line; `state.db.invalid` exists; the freshly written `state.db`
     re-verifies on a third start.
  3. Stop it. `sqlite3 state.db "DELETE FROM state"`. Restart → the integrity
     line again (**not** "no save"), and **no legacy re-grant** in the log.
  4. Hand-edit the name in `config.json`. Restart → the name persists, the
     economy is unaffected, no integrity line.
  5. On a scratch HOME with no `state.db`, no `state.json`, and a Rust
     `save.json` in place: the legacy import runs once and produces a valid
     signed `state.db`.

---

## 8. Residual risks and explicitly deferred items

- **Downgrade hazard (named, accepted).** After the import, a **pre-DB-1
  build** finds no `state.json`, and on a machine that still has a Rust
  `save.json` it would re-run the legacy import — whose refunds would mint
  currency. Exposure is genuinely tiny (nothing has shipped; the repo is
  private; this is at most one developer machine) and the recovery is one
  command: rename `state.json.imported` back. Recorded rather than engineered
  around, because the only real fix — a marker an *old* build could read — is
  impossible by definition.
- **Save-scumming** (restoring an older, genuinely-signed `state.db`) stays
  possible, exactly as it was with JSON. A monotonic counter in the payload
  would deter it; not worth the complexity today. Named, not built.
- **The honest ceiling is unchanged.** The HMAC key is still public in
  `integrity.go` by deliberate choice (ADR 0014 §3). DB-1 does not raise that
  ceiling and must not be described as if it did — SQLite is a *container*, not
  a security boundary. Anyone who says "it's in a database now, so it's safe"
  is wrong, and this paragraph exists so nobody says it.
- **Deferred, named so they do not creep:** the long-lived `*store.DB` handle
  (P2); the `sessions`/`moments` chained-MAC log tables (§2.4, P2/P4); any
  normalization of the economy snapshot (§2.2 option (a) — rejected, and the
  reason is the auto-protecting MAC, not effort); encryption at rest (pointless
  against a local owner, same argument as the key); Fork K's OS-keychain key
  source (still named, still not built).
