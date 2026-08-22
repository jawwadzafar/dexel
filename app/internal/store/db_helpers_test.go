package store

import "testing"

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
