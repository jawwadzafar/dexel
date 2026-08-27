# Persistence — what is stored, where, and what happens when it is wrong

Dexel is entirely local and entirely offline. There is no account, no sync, and
no server anywhere but the one on your own loopback interface. This page is
what that means concretely.

Sources: `app/internal/store/{store,db,integrity,config}.go`,
`app/internal/paths/paths.go`. Decisions:
[ADR 0014 (save integrity and the config split)](../adr/0014-save-integrity-hmac-and-config-split.md),
[ADR 0016 (SQLite)](../adr/0016-sqlite-persistence.md),
[ADR 0017 (the chained session log)](../adr/0017-sessions.md).

---

## 1. Where the files are

Everything is under one **state directory**, computed by
`app/internal/paths/paths.go` — the only place in the app that knows a
filesystem location.

| Platform | State directory |
| --- | --- |
| macOS | `~/Library/Application Support/dexel` |
| Linux (and other unix) | `$XDG_CONFIG_HOME/dexel`, else `~/.config/dexel` |
| Windows | `%LOCALAPPDATA%\dexel`, else `~\AppData\Local\dexel` |

`DEXEL_HOME` overrides that **wholesale, on every platform**, returned verbatim
with no `dexel` suffix appended. `XDG_CONFIG_HOME` is deliberately ignored on
macOS — it is a Linux-only knob, and a test pins that.

Inside it:

| File | What it is |
| --- | --- |
| `state.db` | the protected save — SQLite, HMAC'd |
| `config.json` | the unsigned, hand-editable half — see §5 |
| `runtime.json` | the running runtime's pid, url, version, token (mode `0600`) |
| `runtime.lock` | the OS lock that makes single-instance real |
| `lastport` | the port the last runtime here bound, so a crash-restart (or `dexel restart`) comes back on it and open windows reconnect — advisory, mode `0600`, see PLATFORM_NOTES.md §5.1 |
| `logs/runtime.log` | the runtime log; rotated to `.1` past 8 MiB — once by `dexel start`, and continuously by the runtime itself, which is the only rotation the supervised autostart paths ever get (PLATFORM_NOTES.md §4) |
| `cache/` | computed by `paths.CacheDir()` — **no caller exists in this tree** |

`BinDir()` is `~/.local/bin` on **both** Linux and macOS (never
`/usr/local/bin` — installing must not need sudo), `%LOCALAPPDATA%\dexel\bin`
on Windows, and is deliberately *not* affected by `DEXEL_HOME`.

macOS and Windows carry a **one-time relocation hook**: if the legacy
`~/.config/dexel` holds a `state.db` or `state.json` and the new directory
holds neither, the known files are `os.Rename`d across and one line is logged.
It never copies, never merges, never deletes. Linux's hook is a compile-time
no-op.

---

## 2. `state.db`

SQLite, via **`modernc.org/sqlite`** — a pure-Go, transpiled driver with **no
cgo**, chosen so release builds cross-compile with `CGO_ENABLED=0`. Opened as
`sql.Open("sqlite", path+"?_txlock=immediate")` with `SetMaxOpenConns(1)` and
three explicit pragmas, read back and asserted by a test:

```
PRAGMA journal_mode = DELETE
PRAGMA synchronous  = FULL
PRAGMA busy_timeout = 5000
```

**Two tables, and only two.**

```sql
CREATE TABLE IF NOT EXISTS state (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    schema  INTEGER NOT NULL,
    payload BLOB    NOT NULL,
    mac     TEXT    NOT NULL
) STRICT

CREATE TABLE IF NOT EXISTS sessions (
    id       INTEGER PRIMARY KEY,
    ended_at TEXT    NOT NULL,
    payload  BLOB    NOT NULL,
    mac      TEXT    NOT NULL
) STRICT
```

Plus an index `sessions_ended_at ON sessions(ended_at)` that **currently has no
reader** — the only query is `ORDER BY id ASC`. Its own comment scopes it to a
future scrapbook feature, so it is honestly labelled rather than accidental.

The `state` table holds exactly one row, `id = 1`. The `sessions` table is
created **lazily**, only by the first `AppendSession` — a missing `sessions`
table is the honest empty log, checked via `sqlite_master` rather than by
pattern-matching a driver error string.

---

## 3. The schema — exactly one, no migration

`store.CurrentSchema = 1`. There is exactly **one supported save format** and
**no migration engine**. This is the public first release (v0.1.0): there are
no prior *public* saves to preserve, so the schema was reset to a clean baseline
of `1` and every pre-v0.1.0 bump-history and upgrade path was deleted. The whole
load policy is one line:

> A save whose `schema` equals `CurrentSchema` **and** whose MAC verifies is
> used. Anything else — a wrong, older, or future schema; a bad or absent MAC;
> a corrupt container — is quarantined, and the economy starts fresh.
> **Current-schema-or-fresh, nothing else.**

Two rules still run through the load:

- **A version is written in three places that must agree**: the payload's own
  `schema` field, the `state.schema` column, and `PRAGMA user_version`.
  Disagreement between any of them is treated as tampering.
- **No backfill, ever.** A day, or a second, that was never recorded stays
  unrecorded — a save is never re-signed with anything it did not legitimately
  hold.

A **future** save (`schema > CurrentSchema`, which is also how a leftover
pre-v0.1.0 local save now looks, since those used higher, defunct numbers) keeps
its own distinct treatment: quarantined `.future`, `ErrFutureSchema`, **never
downgraded in place**, so a newer build's save is never clobbered by an older
build run once. Every *other* refusal — an older/foreign schema, a bad MAC, a
corrupt file — is `.invalid` / `ErrTampered`.

### Why the schema was reset rather than kept

The reset is load-bearing, not cosmetic. `loadDB` verifies a row's MAC against
the exact bytes **stored** in it, not a re-serialization — so removing fields
from `SaveData` (the old `ownedTints` / per-slot `tintId`, `appSwitches`,
`importedFromRust`/`importedAt`) would **not** by itself invalidate a genuine
old row's tag; it would still verify and load. Changing the schema number is
what forces the reset: an old save carries a higher `user_version`, so it is
refused as a future schema and the next save writes a clean `schema-1` economy.
`internal/store/reset_test.go` pins this end to end.

### No import / migration path

There is no `state.json` → `state.db` import and no legacy-Rust import — both
deleted. `LoadAll` reads `state.db` or reports the one genuine "no save" case; a
stray pre-release `state.json` or `save.json` on disk is simply **ignored** (no
code reads it), so it can never mint an economy. A fresh install of this build
always starts fresh.

---

## 4. The save MAC, and quarantine-never-delete

### What is signed

HMAC-SHA256, hex-encoded, over:

```
"dexel-save-integrity-v1"  ‖  0x00  ‖  json.Marshal(SaveData with Mac zeroed)
```

The domain string separates this from the session log's own chain (§6). The
body is *compact* JSON, not indented.

The key is a single baked-in 32-byte constant in `integrity.go`, and its doc
comment is unusually honest about what that buys:

> On an MIT-licensed local single-player game the key is *necessarily* public. It
> stops casual editing and nothing more. Obfuscation whose inverse lives in the
> same public repo is theatre.

So this is an integrity check, not an anti-cheat system, and it is documented
as an accepted non-goal rather than sold as security.

Two implementation details that matter:

- **On the DB path the MAC is verified against the stored bytes**, never
  against a re-serialisation. The `payload` BLOB *is* the preimage.
- **On the JSON path it must re-serialise**, which makes `omitempty` on any
  newly added field load-bearing. This is not hypothetical: `pausedSeconds`
  shipped *without* `omitempty`, which injected `"pausedSeconds":0` into four
  buckets of every pre-PR-5 file, broke the MAC, and quarantined real saves as
  tampered. There is now a frozen byte-exact fixture and its expected tag in
  `json_upgrade_mac_test.go`, so rotating the key or the domain string fails
  *there* with a message saying it needs a migration and not a new constant.
- Comparison is constant-time on decoded digests via `hmac.Equal` — never on
  hex strings and never with `==`.
- `sprint.unitsDone` is rounded to 6 decimals before signing, in three places,
  so float formatting cannot destabilise the preimage.

### The failure ladder

`loadDB` checks, in this order, and each rung has its own quarantine suffix and
its own message:

| # | Condition | Suffix | Error |
| --- | --- | --- | --- |
| 1 | the file will not open as SQLite | `.corrupt` | corrupt |
| 2 | `PRAGMA quick_check != "ok"` | `.corrupt` | corrupt |
| 3 | `user_version > CurrentSchema` | `.future` | `ErrFutureSchema` |
| 4 | no tables at all | `.corrupt` | "an interrupted first write or a truncated file, not a tampered save" |
| 5 | the `state` table is missing/unreadable, or the row count isn't exactly 1 | `.invalid` | `ErrTampered` |
| 6 | **MAC mismatch** | `.invalid` | `ErrTampered` |
| 7 | the payload will not parse *despite* a valid MAC | `.invalid` | `ErrTampered` — "our own bug, not a cheat, but the response is identical" |
| 8 | the three schema numbers disagree | `.invalid` | `ErrTampered` |
| 9 | payload `schema > CurrentSchema` (defence in depth behind rung 3) | `.future` | `ErrFutureSchema` |
| 10 | payload `schema != CurrentSchema` (older/foreign — this build loads exactly one schema) | `.invalid` | `ErrTampered` |
| 11 | the session chain does not replay | `.invalid` | `ErrTampered` |

`ErrTampered` and `ErrFutureSchema` are distinct sentinels, and both are
distinct from "no save" — a tampered file must never present as a fresh
install, and a test asserts exactly that. On `ErrTampered` the server logs,
starts a **fresh economy**, and still reports `saveExisted = true`, so
onboarding is not offered to someone who did have a save.

### Quarantine, never delete

`quarantine()` closes the database handle **before** renaming, then
`os.Rename`s the file — and renames a `-journal` sibling alongside it, so the
evidence is complete. The message is deliberately explicit:

> original preserved untouched at `<path>` (NOT loaded, NOT deleted, NOT
> overwritten)

The destination name is chosen so a second failure cannot erase the first: the
first quarantine keeps the plain documented name (`state.db.invalid`), a
collision appends a UTC timestamp (`state.db.invalid.20260822T164500Z`), a
same-second collision appends `-1`…`-99`. After 100 attempts it falls back to
the plain name — explicitly chosen as the lesser evil, because "losing older
evidence is bad, leaving a poisoned file in place is worse".

**Nothing on the persistence path is ever deleted.** The only `os.Remove` in
`internal/store` is the temp-file cleanup in `writeFileAtomically`, a no-op
after a successful rename. There is no `os.RemoveAll` and no `os.Truncate`
anywhere in the package.

Suffixes in use: `.corrupt`, `.future`, and `.invalid`.

---

## 5. What is in the save — and what is deliberately not

`SaveData` has 11 fields:

```
schema           int
devCash          uint64
xp               uint64
sprint           { index int, unitsDone float64 }
ownedItems       []string                       // sorted
equipped         map[slot]{ itemId }
stats            { date, today, lifetime, coinsToday, history[], streak }
session          *ActiveSessionSave             // nil when none
sessionLogHead   string                         // opaque chained-MAC token
paused           bool
mac              string
```

Colours are ordinary items now (STORE-2.0), so there is no `ownedTints` and no
per-slot `tintId`. The pre-v0.1.0 back-compat fields are gone too:
`importedFromRust` / `importedAt` (the deleted legacy-Rust import), and the
`appSwitches` counters that were only kept to preserve old MAC preimages. With
no old save required to round-trip, they are deleted outright — every
pre-existing local save resets to fresh on first load with this binary.

**Not in the save, on purpose:**

| | Where it lives instead |
| --- | --- |
| the Dexel's name | `config.json` — §6 |
| session project names | `config.json`'s `sessionNames` map |
| the per-signal work accumulators | nowhere. In-memory floats only; "no work floats ever cross the wire or hit disk" |
| mood, active app | nowhere. Recomputed from the first real tick |
| the store-open hold set | nowhere. Per-connection, per-process |
| the onboarding flag | nowhere. Computed once at boot from (no save) && (no name) |
| the ticker / terminal buffers | nowhere. Cosmetic, rebuilt blank |
| today's longest focus block | nowhere until the day finalises — see [`sessions.md`](sessions.md) §3 |

### The structural proof

`app/internal/store/content_free_test.go` is the disk-side counterpart of the
allow-lists at the observation and wire boundaries. Five tests walk `SaveData`,
`SprintSave`, `EquippedSave`, `StatsSave`, `StatCountersSave`,
`CoinBreakdownSave`, `DayBucketSave`, `StreakSave`, `SessionSave` and
`ActiveSessionSave` by reflection against explicit allow-lists, failing on any
added field, removed field, or changed type, and additionally rejecting any
field name containing `title`, `text`, `content`, `keycode`, `key_code`,
`clipboard`, `url`, `path`, `document`, `message`, `body`, `keyname` or `char`.

For the two session save types the forbidden list gains one more entry:
**`"name"`**. That is the mechanical enforcement of "a project name never
reaches the protected save".

`SaveData`'s allow-list has no name field either, and the test's own comment
says why that is not an omission: "this allow-list staying free of any name
field is itself part of the privacy proof".

There is deliberately **no** content-free test for `ConfigData` — that file is
where free text legitimately lives.

---

## 6. The config split

Two files, not one mixed file.

**`config.json`** — unsigned, hand-editable, three fields:

| Field | What |
| --- | --- |
| `name` | the Dexel's user-chosen name (≤ 24 runes) |
| `sessionNames` | session id (decimal string) → project name (≤ 32 runes) |
| `autostart` | `launchd` / `systemd-user` / `xdg-autostart` / `windows-run` / `""` — **advisory only** |

The reasons, quoted from `config.go`:

- The name is "deliberately the ONLY free text anywhere in dexel's
  persistence". config.json "is never MAC'd, is never touched by Load/Save's
  tamper path, and never influences the protected economy in any way — a
  corrupt or tampered `state.db` cannot take the user's name down with it,
  and a malformed `config.json` can never block the economy."
- `sessionNames` is there because "a *timestamped series* of project names is
  data about the work" — the same artifact class ADR 0013 refused for hourly
  buckets — "so it belongs here … and NOT as a column on the MAC'd `sessions`
  table". Desync is accepted: a logged session simply renders unnamed, and the
  protected counts are never affected.
- `autostart` is advisory because `dexel autostart status` **always asks the
  OS** directly. The field can legitimately drift from reality.

`LoadConfig` degrades both a missing file **and a malformed one** to the zero
value with no error; only a real read failure (permissions) is surfaced.
`SaveConfig` is `MarshalIndent` plus an atomic write and never computes a MAC.
A test drives every DB failure path — raw sqlite tamper, future
`user_version`, an older/foreign schema, a corrupt file — and asserts
`config.json` is untouched by all of them. As one test's name puts it: the refusal costs the user their economy and
not their Dexel's name.

### A verified bug here

`persistConfig` in `app/main.go` builds a **fresh** `store.ConfigData{Name,
SessionNames}` literal — no `Autostart`, and no load-modify-save. So every
`SET_NAME` and every `SESSION_START` rewrites `config.json` with `autostart`
reset to `""`.

This contradicts `config.go`'s own statement that the field "is written ONLY by
`dexel autostart enable`/`disable` — nothing else in this codebase may set it",
and contradicts `main.go`'s own adjacent comment claiming the write is done "as
ONE write for the one shared file, so writing either half never clobbers the
other". `cmd_autostart.go` does the careful load-modify-save and has a test for
it; `persistConfig` has no equivalent test.

Impact is bounded, because the field is advisory and `status` asks the OS — but
the "your config disagrees with the OS" advisory message will fire spuriously
after any rename or session start. Filed in [`BACKLOG.md`](BACKLOG.md) §4.

---

## 7. The session log's chained MAC

Session rows are append-only and each one signs the previous one's tag, so a
row cannot be edited, deleted, reordered or inserted without breaking the
chain.

Each row's MAC is over:

```
"dexel-session-log-v1"  ‖  0x00  ‖  prevMac  ‖  0x00  ‖  payload
```

- `prevMac` is the **previous row's hex string verbatim** (the ASCII bytes, not
  decoded binary); `""` for the genesis row.
- `payload` is the exact bytes stored in that row's BLOB, never a
  re-serialisation.
- The domain string differs from the save's, so a signed state payload can
  never be replayed as a sessions row — and there is a test that tries exactly
  that.
- Appending costs **one** HMAC. No earlier row is ever re-signed.

**The head is the anchor.** `SaveData.SessionLogHead` holds the newest row's
MAC *inside the signed snapshot payload*, and `verifySessionLog` runs strictly
*after* the snapshot's own MAC verifies. Editing the head by hand therefore
fails at the state row's MAC several steps earlier.

Replay walks the rows in `id` order and checks, per row: the id is the next
expected ordinal, the chained MAC verifies, the payload parses, the payload's
own id matches the column, and the denormalised `ended_at` column matches the
payload. Afterwards: an empty log with a non-empty head is tampering, a
non-empty log with an empty head is tampering, and the replayed head must equal
the signed one.

**If the chain breaks, the whole `state.db` is quarantined `.invalid` and
nothing is partially loaded.** The economy is not salvaged from a file whose log
does not verify.

`AppendSession` does the sessions `INSERT` **and** the snapshot upsert in one
transaction. Its comment is unambiguous about why: a crash between them would
leave a row past the signed head, i.e. a false tamper report that resets an
innocent user. A forced-failure test proves the rollback leaves neither the row
nor the new head.

There are tests for all eleven tamper shapes: edit a payload, edit a mac,
delete the last row, delete a middle row, swap ids, hand-write a row, delete
all rows with a non-empty head, drop the table with a non-empty head, edit only
the `ended_at` mirror, edit the head in the snapshot, and copy a valid state
payload into a sessions row.

---

## 8. When writes happen

| Trigger | What is written |
| --- | --- |
| every 30 s (`autosaveInterval`) | `state.db` snapshot |
| a completed session | `state.db` — the sessions row + snapshot, one transaction |
| `SET_NAME` | `config.json`, immediately (write-through) |
| `SESSION_START` | `config.json`, immediately |
| `PAUSE` / `RESUME` | `state.db`, immediately — "a crash right after pausing cannot come back tracking" |
| shutdown (signal or `dexel stop`) | `state.db` |

A hard kill therefore risks **up to 30 seconds** of progress, and `dexel stop`
says so out loud when it has to escalate to one.

A failed `config.json` write is surfaced honestly: the in-memory name stands so
the session still works, but the success toast is replaced by an error flash —
because a warm "hello" for a name that silently will not survive a restart is a
lie.
