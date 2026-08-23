# 0016 — SQLite persistence: pure-Go driver, one signed snapshot row, SEC-1 integrity carried in whole

Status: accepted (2026-08-22, DB-1 design pass) · Extends ADR 0014 (save integrity) · Honours ADR 0002/0009 (privacy) · Constrained by the `CGO_ENABLED=0` release matrix

## Context

Game state lives in `~/.config/dexel/state.json` (schema 5), HMAC-signed per ADR
0014. The owner's DB-1 mandate (`docs/plan/ROADMAP.md`) moves it to SQLite while
keeping the SEC-1 integrity guarantee and leaving `config.json` as plain,
user-editable JSON.

Two facts govern every decision below.

**First, the release pipeline.** `scripts/build-release.sh` builds linux/amd64,
linux/arm64, windows/amd64 and windows/arm64 with **`CGO_ENABLED=0`**,
specifically so all four cross-compile from the one Linux self-hosted runner
this private repo has. Only `darwin/arm64` uses cgo, and only because
`provider_darwin.go` needs Cocoa — which is exactly why that target is already
gated behind a macOS runner that does not exist yet. A cgo SQLite driver in the
*shared* code path would drag those four targets into needing per-target C
toolchains. That is a hard constraint, not a preference.

**Second, the shape of the data.** The state is **< 10 KB**, read and written
**as a whole**, by **one goroutine**, every **30 s**, with **no concurrent
readers** (`internal/store` is imported by `app/main.go` and nothing else). For
*that* workload SQLite buys nothing — not speed, not atomicity (the
tmp+fsync+rename+dir-fsync recipe already has it). Its value is as the platform
for PRODUCT-EVOLUTION's append-mostly logs: P2 sessions, P4 moments, P6
scrapbook reads. This ADR is therefore honest that DB-1 is **infrastructure
bought ahead of its consumer**, and it is taken now because the container swap
is cheapest at minimum state size.

## Owner-decision forks (two, both with a recommended default)

1. **Timing — now, or folded into P2 with its first real log table?**
   **Default: now.** Swapping the container while the state is one snapshot row
   is far cheaper than doing it mid-P2 alongside a live session log and a schema
   bump in the same change. DB-1 is also its own mandate line item.
2. **Cost — accept an embedded SQL engine's footprint?** `go.sum` goes from 2
   lines to ~40; the release binary grows by an **estimated +8–13 MB** (roughly
   double), on a project whose ADR 0011 made a cost promise. **Default: accept.**
   Runtime cost is ~zero (the engine is touched on open and on a 30 s autosave);
   the alternative — hand-rolling P2's append log and its integrity story — is
   strictly worse engineering. The real size delta is a **measured** exit
   criterion, not a guess.

## Decision

**1. Driver: `modernc.org/sqlite` (pure Go, no cgo).** v1.57.0 (2026-08-19)
embedding SQLite 3.53.3, imported by ~3,500 packages, supporting 23 GOOS/GOARCH
pairs — **including every target in our matrix**. `mattn/go-sqlite3` is not a
close call; it is disqualified by the constraint above. Stated caveats, not
hidden: it is transpiled C rather than readable Go; it is ~1.5–3× slower than
the cgo driver (irrelevant at one row per 30 s); `modernc.org/libc` **must** be
pinned to the exact version in its `go.mod`; and `go test -race` will get slower
because the detector instruments a lot of generated code (measured, not assumed).

**2. Layout: one signed snapshot row now; chained append tables when a feature
needs them.**
`state(id INTEGER PRIMARY KEY CHECK (id = 1), schema INTEGER, payload BLOB, mac TEXT) STRICT`
— `payload` is the canonical compact JSON of `SaveData` with `mac` zeroed, i.e.
**byte-for-byte the same preimage body ADR 0014 already signs**.
Full normalization (`kv` + `owned_items` + `history_days` + …) was rejected
because it would destroy ADR 0014's single best property — *preimage = the whole
struct minus the tag, so every future economy field is protected automatically* —
replacing it with a hand-built row serializer whose omission is a **silent
anti-cheat hole rather than a test failure**, for queryability that
`Snapshot`/`Apply` (whole-state by construction) never uses. The destination is
a **hybrid**: this snapshot row plus normalized, append-mostly `sessions` (P2)
and `moments` (P4) tables, each carrying a per-row MAC chained to its
predecessor with the chain head bound into the signed snapshot — so appending
costs one MAC and *deleting* a row is detectable. Those tables are **named here,
built by their own phase**; DB-1 creates no speculative empty schema.

**3. Integrity: the SEC-1 scheme is carried in whole, and gets slightly
stronger.** Same key, same `macDomain = "dexel-save-integrity-v1"`, same
6-decimal float quantization, same `hmac.Equal`. `integrity.go` is *refactored*
(byte-level `canonicalBody`/`computeMACBytes`/`verifyMACBytes` under unchanged
struct-level `computeMAC`/`verifyMAC` wrappers), not rewritten, so every
existing integrity test still applies. Two consequences worth naming: an
existing signed `state.json`'s tag is **valid verbatim** as the row's `mac`, so
the migration **verifies what it moves instead of re-signing it**; and the MAC is
now checked against the **stored bytes** rather than a re-serialization, closing
a small forgery surface. `SaveData` and every nested type are unchanged, so
`content_free_test.go` is **byte-identical** — the privacy proof holds *by
construction*, which was the deciding argument for this layout.

**4. Tamper policy unchanged, plus one genuine improvement.** MAC mismatch,
corrupt DB, or a `schema`/`user_version` disagreement with the signed payload →
the DB (and its `-journal` sibling) is **renamed** aside to
`.invalid`/`.corrupt`/`.future`, never deleted; `Load` returns `ErrTampered` or
`ErrFutureSchema`; the economy resets from `game.New()`; the legacy Rust import
stays **unreachable**; `config.json` and the Dexel's name are untouched. The
improvement: **an existing DB with the `state` row deleted is `ErrTampered`, not
"no save"** — so `DELETE FROM state` cannot reach the legacy re-grant path.
Under JSON, deleting the file was indistinguishable from never having one.

**5. Versioning: one counter, and it does not bump.** `CurrentSchema` **stays
5** — DB-1 changes the container, not the save format, and bumping would break
the tag carry-across and steal the number P2 has earmarked. `PRAGMA
user_version` mirrors that same counter (no second "container version" to
drift, no `meta` table), and exists so a future-version DB can be refused
**before** anything is parsed or trusted. Gate order is preserved exactly:
corrupt → future → row-count → MAC → cross-check.

**6. Migration: verify, then move, once.** No `state.db` + a `state.json`
present → today's `Load` body (kept verbatim as `loadJSON`, with its `.corrupt`
handling, future refusal, MAC check and schema-≤-4 grandfathering intact) runs;
only a **passing** file is written into the DB; **then** `state.json` →
`state.json.imported`. DB written first, rename second, so a crash between them
leaves a valid DB and an ignored JSON. The DB branch never consults the JSON
again, so import is one-time structurally. Failure branches (tampered, future,
corrupt) **create no DB** and propagate their sentinel unchanged. Fresh installs
create the DB on first save; the legacy Rust chain is untouched and now writes
`state.db`.

**7. Journal mode: `DELETE` (+ `synchronous=FULL`, `busy_timeout=5000`, one
connection), not WAL.** WAL's benefit is concurrent readers, which Dexel does
not have. WAL would leave `-wal`/`-shm` files at rest — making "move the file
aside" no longer obviously correct, which is precisely the kind of subtlety that
turns an anti-cheat path into a bug — needs shared memory (the least-exercised
corner of a transpiled SQLite on Windows), is *worse* on network filesystems,
and would checkpoint on every open→write→close cycle for no gain. `DELETE` keeps
**one file at rest**, and SQLite's own journal create/remove fsyncs the
directory — the belt-and-suspenders step `writeFileAtomically` had to do by
hand. `config.json` keeps that atomic-write recipe unchanged.

## Consequences

- **The release matrix survives, and CI proves it.** A `CGO_ENABLED=0`
  cross-build of all four targets plus a non-raced `CGO_ENABLED=0 go test`
  becomes a build-workflow gate, so "the pure-Go driver keeps cross-compilation
  working" is enforced rather than trusted.
- **Callers barely move.** `internal/store`'s entire external contract —
  `Load`/`Save`/`Snapshot`/`Apply`/`ErrTampered`/`ErrFutureSchema`, `SaveData`
  and every nested type, `CurrentSchema`, the whole config and legacy surface —
  is unchanged. `DefaultPath()` returns `state.db`, and `main.go`'s diff is
  **two log strings** (both of which hard-coded a `savePath + ".invalid"` that
  can now be a `.json` path — so logging the error, which already names the real
  file, is the honest fix). `loadOrImport`'s branch order needs no edit.
- **Privacy is unchanged and still provable.** No provider change, no
  `Snapshot`/`StateMessage` change, no new observation, no new column carrying
  anything but counts, durations, ISO dates and catalog ids.
  `content_free_test.go` is byte-identical. P2's optional project name stays on
  the **config** side — written into this ADR because it is the boundary most
  likely to be crossed by accident once a `sessions` table exists.
- **The anti-cheat ceiling is exactly where ADR 0014 left it.** The key is still
  public in the source by deliberate choice. A user who reads it can still forge
  a row, the same way they could forge a file. SQLite is a **container, not a
  security boundary**, and "it's in a database now, so it's safe" is wrong.
- **Costs accepted, and measured.** ~7 new modules and two new BSD-3-Clause
  entries in `THIRD-PARTY-LICENSES.md` (which ships in every release archive —
  a distribution obligation, not documentation polish); an estimated +8–13 MB
  per binary and a slower `-race` run, both recorded as real measurements in the
  implementation handoff rather than left as estimates.
- **One named residual risk.** After the import, a **pre-DB-1** build finds no
  `state.json` and, on a machine that still has a Rust `save.json`, would re-run
  the legacy import and its refunds. Exposure is one developer machine (nothing
  has shipped, repo private) and recovery is renaming `state.json.imported`
  back. Recorded rather than engineered around: the only real fix is a marker an
  *old* build could read, which is impossible by definition.
- **Deferred, named so they do not creep:** the long-lived `*store.DB` handle
  (P2, when writes become frequent and multi-table); the chained-MAC
  `sessions`/`moments` tables (P2/P4); normalization of the economy snapshot
  (rejected on the auto-protecting-MAC argument, not on effort); a monotonic
  anti-save-scum counter; encryption at rest; Fork K's OS-keychain key source.
