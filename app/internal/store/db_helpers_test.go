package store

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// rawReadStateRow opens dbPath directly (bypassing Load's own integrity
// gate entirely) and reads the state row's columns exactly as they sit
// on disk — the test-side counterpart of a support engineer running
// `sqlite3 state.db 'select schema, payload, mac from state'` (DB-1
// design §3.3).
func rawReadStateRow(t *testing.T, dbPath string) (schema int, payload []byte, mac string) {
	t.Helper()
	db, err := openDB(dbPath)
	if err != nil {
		t.Fatalf("rawReadStateRow: openDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.QueryRow("SELECT schema, payload, mac FROM state WHERE id = 1").Scan(&schema, &payload, &mac); err != nil {
		t.Fatalf("rawReadStateRow: SELECT: %v", err)
	}
	return schema, payload, mac
}

// rawUpdateStateRow opens dbPath directly and overwrites the state row's
// columns — the sqlite3-level analogue of hand-editing the old
// state.json in a text editor (DB-1 design §3.3's threat surfaces #1-4):
// `sqlite3 state.db "UPDATE state SET payload=..., mac=..., schema=..."`.
// Any column left at its existing value should be passed back unchanged
// by the caller (rawReadStateRow first, mutate, then this).
func rawUpdateStateRow(t *testing.T, dbPath string, schema int, payload []byte, mac string) {
	t.Helper()
	db, err := openDB(dbPath)
	if err != nil {
		t.Fatalf("rawUpdateStateRow: openDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE state SET schema = ?, payload = ?, mac = ? WHERE id = 1`, schema, payload, mac); err != nil {
		t.Fatalf("rawUpdateStateRow: UPDATE: %v", err)
	}
}

// rawExec runs a raw SQL statement against dbPath directly — for the
// tamper surfaces that aren't a column UPDATE at all (DELETE FROM state,
// INSERT a second row, PRAGMA user_version = N, ...).
func rawExec(t *testing.T, dbPath string, sql string, args ...any) {
	t.Helper()
	db, err := openDB(dbPath)
	if err != nil {
		t.Fatalf("rawExec: openDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(sql, args...); err != nil {
		t.Fatalf("rawExec(%q): %v", sql, err)
	}
}

// rawReadSessionRow reads one `sessions` row's columns directly — the
// test-side counterpart of `sqlite3 state.db 'select ended_at, payload,
// mac from sessions where id = N'` (P2's tamper matrix, docs/plan/
// P2-design.md §7.3).
func rawReadSessionRow(t *testing.T, dbPath string, id int) (endedAt string, payload []byte, mac string) {
	t.Helper()
	db, err := openDB(dbPath)
	if err != nil {
		t.Fatalf("rawReadSessionRow: openDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.QueryRow(`SELECT ended_at, payload, mac FROM sessions WHERE id = ?`, id).Scan(&endedAt, &payload, &mac); err != nil {
		t.Fatalf("rawReadSessionRow: SELECT: %v", err)
	}
	return endedAt, payload, mac
}

// rawUpdateSessionRow overwrites one `sessions` row's columns directly —
// the sqlite3-level analogue of hand-editing a session row, mirroring
// rawUpdateStateRow's role for the state row.
func rawUpdateSessionRow(t *testing.T, dbPath string, id int, endedAt string, payload []byte, mac string) {
	t.Helper()
	db, err := openDB(dbPath)
	if err != nil {
		t.Fatalf("rawUpdateSessionRow: openDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE sessions SET ended_at = ?, payload = ?, mac = ? WHERE id = ?`, endedAt, payload, mac, id); err != nil {
		t.Fatalf("rawUpdateSessionRow: UPDATE: %v", err)
	}
}

// writeSignedRawJSON writes a HAND-WRITTEN state.json body to path with
// the correct "mac" key spliced in, and returns nothing but a t.Fatal on
// failure.
//
// Why it exists (B-1, docs/plan/REVIEW-2026-08-22.md): the schema
// migration tests are about FIELD absence — a save written before some
// key existed must load with that field at its zero value — and they say
// that by hand-writing JSON with the key genuinely missing, which no
// round-trip through SaveData can reproduce. They used to rely on the
// unsigned-schema<=4 grandfather path to get such a fixture accepted.
// That path was a no-key mint and is gone: every save now needs a valid
// MAC at every schema. So the fixture is signed here instead.
//
// The MAC is computed over canonicalBody(parsed struct), never over the
// file's bytes (integrity.go), which is exactly why splicing a key into
// the raw text works: the absent keys stay absent in the file, the tag
// still verifies, and the migration behaviour under test is unchanged.
// A signed schema-1 file is synthetic — nothing ever wrote one — but the
// code path it exercises (json.Unmarshal of a body missing newer keys,
// then Apply) is the real one, and it is the same path a signed schema-5
// or -6 save takes today.
func writeSignedRawJSON(t *testing.T, path, raw string) {
	t.Helper()
	var d SaveData
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("writeSignedRawJSON: the fixture is not valid JSON: %v", err)
	}
	d.Mac = ""
	mac := computeMAC(d)
	open := strings.Index(raw, "{")
	if open < 0 {
		t.Fatalf("writeSignedRawJSON: the fixture is not a JSON object: %s", raw)
	}
	signed := raw[:open+1] + fmt.Sprintf("\n\t\t\"mac\": %q,", mac) + raw[open+1:]
	if err := os.WriteFile(path, []byte(signed), 0o644); err != nil {
		t.Fatalf("writeSignedRawJSON: WriteFile: %v", err)
	}
}
