// db.go implements DB-1's SQLite persistence container
// (docs/plan/DB-1-design.md, docs/adr/0016-sqlite-persistence.md): the
// single signed snapshot row at state.db and the full open-gate (§3.2).
// integrity.go's byte-level MAC helpers do the signing; store.go's
// Load/Save are the public dispatchers that call into this file. There is
// no JSON import path: this is the public first release, so there are no
// prior public saves to migrate.
//
// Driver: modernc.org/sqlite — a transpiled-C, pure-Go SQLite (no cgo),
// chosen specifically because scripts/build-release.sh cross-compiles
// linux/windows × amd64/arm64 with CGO_ENABLED=0 from a single Linux
// runner (design §1). It registers under the driver name "sqlite".
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// stateDDL is the DB-1 table layout (design §2.3): exactly one row,
// STRICT typed (SQLite >= 3.37; we embed 3.53.3), CHECK(id=1) enforced
// by the engine itself. `payload` is BLOB, not TEXT, so the column
// round-trips bytes with zero encoding interpretation — the MAC is
// verified against exactly those stored bytes (§3.1), never a
// re-encoding. `schema` mirrors the signed `payload.schema` for a
// support query that doesn't need the MAC key; the payload field is the
// authority (a disagreement is a tamper signal, §3.3 row 4).
const stateDDL = `CREATE TABLE IF NOT EXISTS state (
	id      INTEGER PRIMARY KEY CHECK (id = 1),
	schema  INTEGER NOT NULL,
	payload BLOB    NOT NULL,
	mac     TEXT    NOT NULL
) STRICT`

// sessionsDDL is P2's finished-session append log (docs/plan/P2-design.md
// §5.2, ADR 0017 Decision 5): `payload` is the canonical compact JSON of a
// SessionSave (store.go), `mac` is that row's chained HMAC (§5.3,
// integrity.go's computeLogMAC/verifyLogMAC), `ended_at` is a denormalized
// MIRROR of payload.endedAt kept only so a range read need not parse every
// payload — a disagreement between the two is itself a tamper signal
// (§5.4 step 8, verifySessionLog below). `id INTEGER PRIMARY KEY` is the
// 1-based ordinal and is ALSO inside the signed payload, so renumbering is
// detected. STRICT for the same reason stateDDL is: the engine itself
// rejects a type-confused UPDATE instead of leaving the loader to wonder.
//
// Unlike stateDDL, this table is created LAZILY — only by the first
// AppendSession (ensureSessionsTable below), never by a read path and
// never speculatively by Save — because a MISSING sessions table is the
// honest empty log precisely when SessionLogHead == "" (§5.4's "missing"
// rule, ADR 0017 Decision 5); `state`'s absence, by contrast, is always
// destruction, which is why only IT gets Save's unconditional
// ensureStateTable treatment.
const sessionsDDL = `CREATE TABLE IF NOT EXISTS sessions (
	id       INTEGER PRIMARY KEY,
	ended_at TEXT    NOT NULL,
	payload  BLOB    NOT NULL,
	mac      TEXT    NOT NULL
) STRICT`

// sessionsIndexDDL backs range reads ("sessions this week", P6's future
// scrapbook) without a full table scan (§5.2).
const sessionsIndexDDL = `CREATE INDEX IF NOT EXISTS sessions_ended_at ON sessions(ended_at)`

// fileExists is a small os.Stat wrapper distinguishing "doesn't exist"
// from a real stat error (permissions, etc.) — LoadAll needs that
// distinction at its state.db check, the same way the pre-DB-1 Load
// already did via os.IsNotExist.
func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// databaseHasNoTables reports whether db contains zero tables — the
// signature of a zero-byte or otherwise never-written state.db, which
// SQLite opens without complaint (N-2, see loadDB's use of it). Any query
// error is returned rather than swallowed, so the caller falls back to
// its stricter branch instead of guessing.
func databaseHasNoTables(db *sql.DB) (bool, error) {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table'`).Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

// quarantine renames path aside — never a copy, never a delete
// (§3.2's "Quarantine mechanics"): the poisoned file is moved out of the
// way so the next Save creates a fresh one, and the destination it
// actually used is returned so the caller's message names the real file
// (see quarantinePath: the first quarantine is path+suffix, a second one
// is timestamped rather than clobbering the first). Also moves a "-journal" sibling
// alongside it if one happens to be at rest (a mid-commit crash can
// leave one even under journal_mode=DELETE), so the quarantined pair
// stays openable for support and no stray journal sits beside the file
// that replaces it.
func quarantine(path, suffix string) (dest string, err error) {
	dest = quarantinePath(path, suffix)
	if err := os.Rename(path, dest); err != nil {
		return dest, err
	}
	journalSrc := path + "-journal"
	if _, err := os.Stat(journalSrc); err == nil {
		_ = os.Rename(journalSrc, dest+"-journal")
	}
	return dest, nil
}

// quarantinePath picks a destination for a quarantined file that does not
// already exist (N-1, docs/plan/REVIEW-2026-08-22.md). The old code
// renamed unconditionally to path+suffix, so a SECOND tamper silently
// destroyed the first one's evidence while the message still promised
// "original preserved untouched … NOT deleted". The first quarantine
// keeps the plain, documented name (state.db.invalid); only a collision
// gets a UTC timestamp appended (state.db.invalid.20260822T164500Z), and
// a same-second collision gets a -N counter after that. If even that
// somehow cannot find a free name we fall back to the plain name rather
// than refusing to quarantine at all — losing older evidence is bad,
// leaving a poisoned file in place is worse.
func quarantinePath(path, suffix string) string {
	dest := path + suffix
	if _, err := os.Stat(dest); err != nil {
		return dest
	}
	stamp := dest + "." + time.Now().UTC().Format("20060102T150405Z")
	for i := 0; i < 100; i++ {
		cand := stamp
		if i > 0 {
			cand = fmt.Sprintf("%s-%d", stamp, i)
		}
		if _, err := os.Stat(cand); err != nil {
			return cand
		}
	}
	return dest
}

// openDB opens path with DB-1's fixed pragmas (§4.5): DELETE journal
// mode (never WAL, so no -wal/-shm files are ever left at rest —
// important for the quarantine story), synchronous=FULL,
// busy_timeout=5000, immediate-lock transactions (_txlock=immediate on
// the DSN, matching the design's DDL "BEGIN IMMEDIATE"), and exactly one
// connection (SetMaxOpenConns(1)) — dexel is single-process,
// single-writer, so pooling adds surface area for no benefit.
//
// Pragmas are set as explicit statements, not only via a DSN _pragma=
// form (a DSN typo fails silently; an Exec failure does not), and this
// is where a corrupt / non-SQLite file surfaces: opening a garbled file
// fails at the very first pragma Exec (verified empirically against
// modernc.org/sqlite — it rejects the statement with "file is not a
// database"), which callers must treat as the corrupt gate (§3.2 step 2)
// alongside the explicit PRAGMA quick_check that follows for a file
// whose header is intact but whose pages are not.
//
// openDB deliberately does NOT create the state table — only a write
// path (Save, the JSON importer, via ensureStateTable) is allowed to
// bring the table into existence, so a read-only Load never mutates a
// file it might go on to quarantine.
func openDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create save dir: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode = DELETE",
		"PRAGMA synchronous = FULL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("set %s: %w", pragma, err)
		}
	}
	return db, nil
}

// ensureStateTable creates the state table if it does not already
// exist. Called only from the write path (Save) — see openDB's doc
// comment for why Load never calls this.
func ensureStateTable(db *sql.DB) error {
	_, err := db.Exec(stateDDL)
	return err
}

// ensureSessionsTable creates the sessions table and its ended_at index
// if they do not already exist. Called only from AppendSession — see
// sessionsDDL's doc comment for why a read path must never call this.
func ensureSessionsTable(db *sql.DB) error {
	if _, err := db.Exec(sessionsDDL); err != nil {
		return err
	}
	_, err := db.Exec(sessionsIndexDDL)
	return err
}

// writeStateRowTx upserts the single signed state row and mirrors schema
// into PRAGMA user_version, against an ALREADY-OPEN transaction — the
// shared body writeStateRow (below) and AppendSession (§5.5) both drive,
// so a single-statement bug fix applies to both writers at once. Neither
// statement here begins or commits/rolls back the transaction: that is
// the caller's responsibility, precisely so AppendSession can include the
// sessions INSERT in the SAME transaction as this upsert (design §5.5:
// "this must be one transaction or the design is broken" — a crash
// between an appended log row and this snapshot update would otherwise
// leave a row past the signed head, i.e. a FALSE tamper report that
// resets an innocent user's economy on the next boot).
func writeStateRowTx(tx *sql.Tx, schema int, payload []byte, mac string) error {
	if _, err := tx.Exec(
		`INSERT INTO state (id, schema, payload, mac) VALUES (1, ?, ?, ?)
		   ON CONFLICT(id) DO UPDATE
		     SET schema = excluded.schema, payload = excluded.payload, mac = excluded.mac`,
		schema, payload, mac,
	); err != nil {
		return fmt.Errorf("upsert state row: %w", err)
	}
	// PRAGMA statements don't take bound parameters; schema is our own
	// validated int (CurrentSchema, or a payload's already-parsed schema
	// field), never attacker-controlled string data, so Sprintf here is
	// not an injection risk.
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", schema)); err != nil {
		return fmt.Errorf("set user_version: %w", err)
	}
	return nil
}

// writeStateRow upserts the single signed row and mirrors schema into
// PRAGMA user_version, in its OWN one-statement transaction (design
// §2.3's write path): a crash between the two statements cannot leave
// them disagreeing, because SQLite's own journal makes the pair atomic —
// a transaction replaces the old tmp+fsync+rename+dir-fsync dance
// entirely for this file (config.json keeps that recipe; see
// writeFileAtomically). Used by Save, which does not write a session row
// alongside it; AppendSession (§5.5) instead drives
// writeStateRowTx directly inside its own transaction that also contains
// the sessions INSERT.
func writeStateRow(db *sql.DB, schema int, payload []byte, mac string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	if err := writeStateRowTx(tx, schema, payload, mac); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// failClosed closes db, quarantines path to path+suffix, and returns
// LoadAll's (SaveData{}, nil, false, err) shape — every "original
// preserved untouched at %s" style assertion reads true against a state.db
// path. The []SessionSave return exists only so this shares LoadAll's
// exact 4-tuple shape; every caller is a failure branch, so it is always
// nil here.
func failClosed(db *sql.DB, path, suffix string, reason error) (SaveData, []SessionSave, bool, error) {
	_ = db.Close()
	dest, qErr := quarantine(path, suffix)
	if qErr != nil {
		return SaveData{}, nil, false, fmt.Errorf("%w, and backing up %s to %s failed: %v", reason, path, dest, qErr)
	}
	return SaveData{}, nil, false, fmt.Errorf("%w; original preserved untouched at %s (NOT loaded, NOT deleted, NOT overwritten)", reason, dest)
}

// loadDB implements DB-1 §3.2's exact open-gate order against an existing
// state.db, extended by P2 §5.4's steps 7-9: corrupt (open/quick_check) ->
// future (user_version) -> tampered (row count, MAC, unmarshal, schema
// cross-check) -> [P2] session log presence/replay/head cross-check -> ok.
// It is read-only until the moment it decides to quarantine: no write, no
// CREATE TABLE, ever happens on this path (see openDB's doc comment).
//
// Chain verification (verifySessionLog below) runs STRICTLY AFTER the
// snapshot's own MAC has verified (§5.4: "strictly after the snapshot MAC
// verifies, because the head must be trusted before it can be used as an
// anchor") — which is also why tamper-matrix row 10 ("edit sessionLogHead
// in the snapshot") is caught several steps earlier, by the ordinary
// state-row MAC check, and never even reaches verifySessionLog.
func loadDB(path string) (SaveData, []SessionSave, bool, error) {
	db, err := openDB(path)
	if err != nil {
		// A non-SQLite / garbled file fails at the very first pragma
		// Exec inside openDB — that IS this build's corrupt gate, just
		// surfaced one statement earlier than PRAGMA quick_check itself
		// would run (there is no open *sql.DB left to quarantine-close in
		// this branch; os.Rename alone is safe here since sql.Open is
		// lazy and openDB already closed the handle on its own error
		// path).
		dest, qErr := quarantine(path, ".corrupt")
		if qErr != nil {
			return SaveData{}, nil, false, fmt.Errorf("state.db is corrupt, and backing up %s to %s failed: %v (original error: %v)", path, dest, qErr, err)
		}
		return SaveData{}, nil, false, fmt.Errorf("state.db is corrupt (moved to %s): %w", dest, err)
	}

	var quickCheck string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&quickCheck); err != nil {
		return failClosed(db, path, ".corrupt", fmt.Errorf("state.db failed integrity check: %w", err))
	}
	if quickCheck != "ok" {
		return failClosed(db, path, ".corrupt", fmt.Errorf("state.db failed integrity check: quick_check reported %q", quickCheck))
	}

	var userVersion int
	if err := db.QueryRow("PRAGMA user_version").Scan(&userVersion); err != nil {
		return failClosed(db, path, ".corrupt", fmt.Errorf("state.db: reading user_version: %w", err))
	}
	if userVersion > CurrentSchema {
		return failClosed(db, path, ".future", fmt.Errorf("%w: user_version %d > this build's %d", ErrFutureSchema, userVersion, CurrentSchema))
	}

	var count int
	if err := db.QueryRow("SELECT count(*) FROM state").Scan(&count); err != nil {
		// Most commonly "no such table: state" — a dropped table (§3.3
		// row 6) is structurally identical to an emptied one for our
		// purposes here, and must not be mistaken for "no save" any more
		// than DELETE FROM state is.
		//
		// N-2 (docs/plan/REVIEW-2026-08-22.md): but a state.db with NO
		// tables at all is not a dropped table, it is a torn FIRST write
		// — a zero-byte file, or a process killed between openDB and the
		// first commit. SQLite happily opens both, so they used to be
		// reported as "save integrity check failed", i.e. this build
		// accusing an honest user of cheating over a crash artifact.
		// Same fail-closed handling (quarantine, never "no save"), but
		// the .corrupt suffix and a message that says what actually
		// happened. A DB that has other tables but no `state` is still
		// tampering: something removed it.
		if empty, emptyErr := databaseHasNoTables(db); emptyErr == nil && empty {
			return failClosed(db, path, ".corrupt", fmt.Errorf("state.db holds no tables at all — an interrupted first write or a truncated file, not a tampered save"))
		}
		return failClosed(db, path, ".invalid", fmt.Errorf("%w: state table missing or unreadable: %v", ErrTampered, err))
	}
	if count != 1 {
		return failClosed(db, path, ".invalid", fmt.Errorf("%w: state row count %d, want exactly 1", ErrTampered, count))
	}

	var schemaCol int
	var payload []byte
	var macHex string
	if err := db.QueryRow("SELECT schema, payload, mac FROM state WHERE id = 1").Scan(&schemaCol, &payload, &macHex); err != nil {
		return failClosed(db, path, ".invalid", fmt.Errorf("%w: reading the state row: %v", ErrTampered, err))
	}

	if !verifyMACBytes(payload, macHex) {
		return failClosed(db, path, ".invalid", fmt.Errorf("%w: MAC mismatch", ErrTampered))
	}

	var d SaveData
	if err := json.Unmarshal(payload, &d); err != nil {
		// A valid MAC over unparseable bytes means our own bug, not a
		// cheat — but the response is identical and must never degrade
		// to "no save" (§3.2 step 6).
		return failClosed(db, path, ".invalid", fmt.Errorf("%w: payload did not parse despite a valid MAC: %v", ErrTampered, err))
	}
	// payload is canonicalBody's output, which has Mac zeroed by
	// construction (it's the preimage minus the tag) — so d.Mac is ""
	// straight out of Unmarshal. Restore it from the row's own mac
	// column so callers see the same populated SaveData.Mac they always
	// have (verifyMACBytes above already proved this is the tag that
	// actually matches payload).
	d.Mac = macHex

	if d.Schema != schemaCol || d.Schema != userVersion {
		return failClosed(db, path, ".invalid", fmt.Errorf("%w: schema disagreement (payload=%d, column=%d, user_version=%d)", ErrTampered, d.Schema, schemaCol, userVersion))
	}
	if d.Schema > CurrentSchema {
		// Defence in depth (§3.2 step 8) — step 3's user_version check
		// already catches every way this can actually happen. A future
		// schema keeps its own distinct quarantine (.future) so a newer
		// build's save is preserved, never downgraded in place.
		return failClosed(db, path, ".future", fmt.Errorf("%w: payload schema %d > this build's %d", ErrFutureSchema, d.Schema, CurrentSchema))
	}
	if d.Schema != CurrentSchema {
		// This build loads exactly ONE schema (CurrentSchema). Anything
		// below it is an older/foreign save this build cannot use — there
		// is no migration path (public first release, no prior public
		// saves). Refuse it exactly like any other foreign save: quarantine
		// to .invalid and start fresh. (A higher schema was already handled
		// as a future schema just above.)
		return failClosed(db, path, ".invalid", fmt.Errorf("%w: payload schema %d != this build's only supported schema %d (no migration; starting fresh)", ErrTampered, d.Schema, CurrentSchema))
	}

	// P2 §5.4 steps 7-9: the session log chain. d.SessionLogHead is now a
	// TRUSTED anchor (the snapshot MAC just verified above), which is
	// exactly why this step must not run any earlier.
	sessions, err := verifySessionLog(db, d.SessionLogHead)
	if err != nil {
		return failClosed(db, path, ".invalid", err)
	}

	if err := db.Close(); err != nil {
		return SaveData{}, nil, false, fmt.Errorf("close state.db: %w", err)
	}
	return d, sessions, true, nil
}

// verifySessionLog implements P2 §5.4's steps 7-9 against an
// ALREADY-OPEN db handle, using head (the snapshot's own, already-MAC-
// verified SessionLogHead) as the trusted anchor the replayed chain must
// land on exactly. Returns the verified log oldest-first (ascending id),
// or a nil slice for the honest empty log.
//
// A MISSING `sessions` table is the honest empty log IFF head == "" —
// checked via sqlite_master rather than by pattern-matching a driver's
// error string, which would be fragile across modernc.org/sqlite
// versions (ADR 0017 Decision 5, §5.4 step 7). A table that exists but is
// empty, or a non-empty table paired with an empty head, is
// ErrTampered — a dropped/emptied `sessions` table is structurally
// identical to a dropped/emptied `state` table for this purpose (loadDB's
// own "count != 1" check just above draws the identical line for state).
//
// Every mismatch anywhere in the replay — a row MAC that doesn't chain,
// an out-of-sequence id, a payload.id disagreeing with the row's own id,
// or an ended_at mirror disagreeing with payload.endedAt — is
// ErrTampered, and the replay stops at the first one found (docs/plan/
// P2-design.md §7.3's 11-row tamper matrix).
func verifySessionLog(db *sql.DB, head string) ([]SessionSave, error) {
	var tableExists int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&tableExists); err != nil {
		return nil, fmt.Errorf("%w: checking for the sessions table: %v", ErrTampered, err)
	}
	if tableExists == 0 {
		if head == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: sessions table is missing but a non-empty session log head is signed", ErrTampered)
	}

	rows, err := db.Query(`SELECT id, ended_at, payload, mac FROM sessions ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("%w: reading the sessions table: %v", ErrTampered, err)
	}
	defer func() { _ = rows.Close() }()

	var out []SessionSave
	prev := ""
	want := 1
	for rows.Next() {
		var id int
		var endedAt string
		var payload []byte
		var mac string
		if err := rows.Scan(&id, &endedAt, &payload, &mac); err != nil {
			return nil, fmt.Errorf("%w: reading a sessions row: %v", ErrTampered, err)
		}
		if id != want {
			return nil, fmt.Errorf("%w: sessions row id %d out of sequence (want %d)", ErrTampered, id, want)
		}
		if !verifyLogMAC(prev, payload, mac) {
			return nil, fmt.Errorf("%w: session log chain broken at row id %d", ErrTampered, id)
		}
		var s SessionSave
		if err := json.Unmarshal(payload, &s); err != nil {
			// A valid chain MAC over unparseable bytes means our own bug,
			// not a cheat — same discipline as loadDB's own state-row
			// unmarshal-failure branch above.
			return nil, fmt.Errorf("%w: session row %d payload did not parse despite a valid chain MAC: %v", ErrTampered, id, err)
		}
		if s.ID != id {
			return nil, fmt.Errorf("%w: session row %d payload.id %d disagrees with the row's own id", ErrTampered, id, s.ID)
		}
		if s.EndedAt != endedAt {
			return nil, fmt.Errorf("%w: session row %d ended_at mirror %q disagrees with payload.endedAt %q", ErrTampered, id, endedAt, s.EndedAt)
		}
		out = append(out, s)
		prev = mac
		want++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: iterating the sessions table: %v", ErrTampered, err)
	}

	n := len(out)
	if n == 0 && head != "" {
		return nil, fmt.Errorf("%w: sessions table is empty but a non-empty session log head is signed", ErrTampered)
	}
	if n > 0 && head == "" {
		return nil, fmt.Errorf("%w: %d session row(s) exist but the signed session log head is empty", ErrTampered, n)
	}
	if prev != head {
		return nil, fmt.Errorf("%w: session log head mismatch (replayed %q, signed %q)", ErrTampered, prev, head)
	}
	return out, nil
}

// AppendSession implements P2 §5.5's write path: it appends one finished
// session row AND rewrites the signed snapshot row IN ONE TRANSACTION.
// This must be one transaction or the design is broken (§5.5): a crash
// between the two writes would leave a log row past the signed head —
// i.e. a FALSE tamper report that resets an innocent user's economy on
// the next boot.
//
// Order, matching §5.5 exactly: compute payload_s = json.Marshal(s);
// rowMac = chain(d.SessionLogHead, payload_s); set d.SessionLogHead =
// rowMac and d.Session = nil (the just-finished session is no longer the
// active one); sign d; write both rows in the one transaction; return
// rowMac. The caller (main.go, GO-3) hands rowMac back to the live game
// via Game.SetSessionLogHead — an OPAQUE integrity token game never
// interprets, only ever carries forward.
//
// d is the CALLER's current SaveData (typically Snapshot(g) taken
// immediately before this call, per GO-3's wiring) — its own
// d.SessionLogHead is read as the chain's current head (the anchor the
// new row extends), and its d.Session is expected to already describe
// the session being finished (or be nil/anything, since it is
// unconditionally cleared here rather than trusted).
func AppendSession(path string, d SaveData, s SessionSave) (newHead string, err error) {
	payload, err := json.Marshal(s)
	if err != nil {
		// SessionSave is our own struct of plain JSON-safe types — this
		// cannot fail short of a programming error, mirroring
		// canonicalBody's identical panic-worthy-but-returned-as-error
		// stance for SaveData.
		return "", fmt.Errorf("marshal session payload: %w", err)
	}
	rowMac := computeLogMAC(d.SessionLogHead, payload)

	d.SessionLogHead = rowMac
	d.Session = nil
	d.Sprint.UnitsDone = quantizeUnits(d.Sprint.UnitsDone)
	d.Mac = ""
	statePayload := canonicalBody(d)
	stateMac := computeMACBytes(statePayload)

	db, err := openDB(path)
	if err != nil {
		return "", fmt.Errorf("open state.db: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := ensureStateTable(db); err != nil {
		return "", fmt.Errorf("create state table: %w", err)
	}
	if err := ensureSessionsTable(db); err != nil {
		return "", fmt.Errorf("create sessions table: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return "", fmt.Errorf("begin transaction: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO sessions (id, ended_at, payload, mac) VALUES (?, ?, ?, ?)`,
		s.ID, s.EndedAt, payload, rowMac,
	); err != nil {
		_ = tx.Rollback()
		return "", fmt.Errorf("insert session row: %w", err)
	}
	if err := writeStateRowTx(tx, d.Schema, statePayload, stateMac); err != nil {
		_ = tx.Rollback()
		return "", fmt.Errorf("write state row: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return rowMac, nil
}
