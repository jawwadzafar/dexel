// sessions_test.go is P2's store-layer test suite (docs/plan/P2-design.md
// §7.3, ADR 0017 Decision 5): the chained-MAC session log's round trip,
// its 11-row tamper matrix, Load/LoadAll's identical verification through
// both entry points, and AppendSession's one-transaction atomicity.
// Session NAMES belong to config.go's own tests (config_test.go) — this
// file never touches them.
package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/game"
)

// sampleSessionSave builds a deterministic, distinguishable SessionSave
// for id — every numeric field is a function of id so two different rows
// are never accidentally byte-identical, which matters for the tamper
// tests below (each edit must target a value that is actually present).
func sampleSessionSave(id int, startedAt, endedAt time.Time, reason string) SessionSave {
	return SessionSave{
		ID:                       id,
		StartedAt:                startedAt.UTC().Format(time.RFC3339),
		EndedAt:                  endedAt.UTC().Format(time.RFC3339),
		DurationSeconds:          uint64(endedAt.Sub(startedAt).Seconds()),
		Counters:                 StatCountersSave{Keystrokes: uint64(id * 100), ActiveSeconds: uint64(id * 50)},
		CoinsEarned:              uint64(id * 3),
		LongestFocusBlockSeconds: uint64(id * 20),
		EndReason:                reason,
	}
}

// appendSessions appends n sequential, non-overlapping finished sessions
// (ids 1..n) onto path's existing chain, returning them in the order
// appended (oldest first — the same order LoadAll returns). path must
// already have a state.db (any Save call) before this is called.
func appendSessions(t *testing.T, path string, n int) []SessionSave {
	t.Helper()
	d, _, ok, err := LoadAll(path)
	if err != nil || !ok {
		t.Fatalf("LoadAll before appends: ok=%v err=%v", ok, err)
	}
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	saves := make([]SessionSave, 0, n)
	for i := 1; i <= n; i++ {
		start := base.Add(time.Duration(i) * time.Hour)
		end := start.Add(10 * time.Minute)
		s := sampleSessionSave(i, start, end, "user")
		newHead, err := AppendSession(path, d, s)
		if err != nil {
			t.Fatalf("AppendSession %d: %v", i, err)
		}
		d.SessionLogHead = newHead
		d.Session = nil
		saves = append(saves, s)
	}
	return saves
}

// buildThreeRowLogFixture returns a fresh state.db with a signed
// snapshot (no active session) and a valid 3-row session log — the
// shared starting point for every tamper-matrix test below.
func buildThreeRowLogFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if err := Save(path, SaveData{Schema: CurrentSchema, DevCash: 500}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	appendSessions(t, path, 3)
	return path
}

// assertSessionTampered is the shared assertion for every tamper-matrix
// row: Load must fail wrapping ErrTampered, report ok=false, and the DB
// must be quarantined to path+".invalid" — renamed, never deleted, the
// original preserved verbatim (docs/plan/P2-design.md §5.4's failClosed
// contract, unchanged by P2).
func assertSessionTampered(t *testing.T, path string) {
	t.Helper()
	d, ok, err := Load(path)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !errors.Is(err, ErrTampered) {
		t.Errorf("err = %v, want it to wrap ErrTampered", err)
	}
	if ok {
		t.Error("ok = true, want false")
	}
	if !reflect.DeepEqual(d, SaveData{}) {
		t.Errorf("d = %+v, want the zero value", d)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("%s should have been renamed away (quarantined), but still exists", path)
	}
	if _, statErr := os.Stat(path + ".invalid"); statErr != nil {
		t.Errorf("expected %s.invalid to exist (original preserved, never deleted): %v", path, statErr)
	}
}

// --- Schema / round-trip (docs/plan/P2-design.md §7.3's opening bullet) --

// TestRoundTripWithActiveSessionAndAThreeRowLog is §7.3's second bullet:
// an active session and a 3-row finished-session log coexist and both
// survive a save/reload cycle intact. AppendSession itself always clears
// the active session (§5.5: "the just-finished session is no longer the
// active one"), so the active session here is written by an ORDINARY Save
// on top of an already-built log — exactly how main.go's 30s autosave
// would persist "3 sessions done, a 4th now in progress."
func TestRoundTripWithActiveSessionAndAThreeRowLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if err := Save(path, SaveData{Schema: CurrentSchema, DevCash: 99}); err != nil {
		t.Fatalf("Save (initial): %v", err)
	}

	want := appendSessions(t, path, 3)

	after3, _, ok, err := LoadAll(path)
	if err != nil || !ok {
		t.Fatalf("LoadAll after 3 appends: ok=%v err=%v", ok, err)
	}
	active := &ActiveSessionSave{
		ID:                       4,
		StartedAt:                time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC).Format(time.RFC3339),
		LastActivityAt:           time.Date(2026, 1, 5, 9, 30, 0, 0, time.UTC).Format(time.RFC3339),
		Baseline:                 StatCountersSave{Keystrokes: 1000},
		Watermark:                StatCountersSave{Keystrokes: 1200},
		CoinsEarned:              7,
		LongestFocusBlockSeconds: 300,
	}
	after3.Session = active
	if err := Save(path, after3); err != nil {
		t.Fatalf("Save (active session on top of the log): %v", err)
	}

	d, sessions, ok, err := LoadAll(path)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if !reflect.DeepEqual(d.Session, active) {
		t.Errorf("d.Session = %+v, want %+v", d.Session, active)
	}
	if len(sessions) != 3 {
		t.Fatalf("len(sessions) = %d, want 3", len(sessions))
	}
	for i, s := range sessions {
		if !reflect.DeepEqual(s, want[i]) {
			t.Errorf("sessions[%d] = %+v, want %+v (byte-equal, oldest-first order)", i, s, want[i])
		}
	}
}

// TestASessionEndedAcrossASaveReloadAppearsExactlyOnce is §7.3's third
// bullet: appending a session, then persisting again through an ordinary
// Save/Load cycle (as an autosave or restart would), must never
// duplicate or drop that session.
func TestASessionEndedAcrossASaveReloadAppearsExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if err := Save(path, SaveData{Schema: CurrentSchema}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	d, _, ok, err := LoadAll(path)
	if err != nil || !ok {
		t.Fatalf("LoadAll: ok=%v err=%v", ok, err)
	}

	start := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	s := sampleSessionSave(1, start, start.Add(5*time.Minute), "user")
	newHead, err := AppendSession(path, d, s)
	if err != nil {
		t.Fatalf("AppendSession: %v", err)
	}
	d.SessionLogHead = newHead
	d.Session = nil

	if err := Save(path, d); err != nil {
		t.Fatalf("Save (reload cycle): %v", err)
	}

	_, sessions, ok, err := LoadAll(path)
	if err != nil || !ok {
		t.Fatalf("LoadAll (after reload): ok=%v err=%v", ok, err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want exactly 1", len(sessions))
	}
	if sessions[0].ID != 1 {
		t.Errorf("sessions[0].ID = %d, want 1", sessions[0].ID)
	}
}

// --- The 11-row tamper matrix (docs/plan/P2-design.md §7.3) -------------

// TestSessionTamperRow1EditPayloadIsDetected: editing a row's payload
// (here, its coinsEarned) without touching its mac breaks the row's own
// chain MAC.
func TestSessionTamperRow1EditPayloadIsDetected(t *testing.T) {
	path := buildThreeRowLogFixture(t)
	endedAt, payload, mac := rawReadSessionRow(t, path, 2)
	tampered := bytes.Replace(payload, []byte(`"coinsEarned":6`), []byte(`"coinsEarned":999999`), 1)
	if bytes.Equal(tampered, payload) {
		t.Fatal("fixture assumption wrong: coinsEarned:6 not found in row 2's payload")
	}
	rawUpdateSessionRow(t, path, 2, endedAt, tampered, mac)
	assertSessionTampered(t, path)
}

// TestSessionTamperRow2EditMacIsDetected: flipping the mac column alone
// (payload untouched) must fail the same chain check.
func TestSessionTamperRow2EditMacIsDetected(t *testing.T) {
	path := buildThreeRowLogFixture(t)
	endedAt, payload, mac := rawReadSessionRow(t, path, 2)
	tamperedMac := flipHexChar(t, mac)
	rawUpdateSessionRow(t, path, 2, endedAt, payload, tamperedMac)
	assertSessionTampered(t, path)
}

// TestSessionTamperRow3DeleteLastRowBreaksTheHeadCheck: deleting the
// newest row leaves the replayed chain one row short of the signed head.
func TestSessionTamperRow3DeleteLastRowBreaksTheHeadCheck(t *testing.T) {
	path := buildThreeRowLogFixture(t)
	rawExec(t, path, `DELETE FROM sessions WHERE id = 3`)
	assertSessionTampered(t, path)
}

// TestSessionTamperRow4DeleteMiddleRowBreaksTheIdSequence: deleting a
// middle row breaks the strictly-ascending id sequence the replay walks.
func TestSessionTamperRow4DeleteMiddleRowBreaksTheIdSequence(t *testing.T) {
	path := buildThreeRowLogFixture(t)
	rawExec(t, path, `DELETE FROM sessions WHERE id = 2`)
	assertSessionTampered(t, path)
}

// TestSessionTamperRow5SwapTwoRowsIdsIsDetected: relabeling two rows' ids
// (their payload/mac stay attached to their original content) is caught
// — either by the chain MAC no longer matching its new position, or by
// payload.id disagreeing with the row's new id; either is ErrTampered.
func TestSessionTamperRow5SwapTwoRowsIdsIsDetected(t *testing.T) {
	path := buildThreeRowLogFixture(t)
	rawExec(t, path, `UPDATE sessions SET id = -1 WHERE id = 1`)
	rawExec(t, path, `UPDATE sessions SET id = 1 WHERE id = 2`)
	rawExec(t, path, `UPDATE sessions SET id = 2 WHERE id = -1`)
	assertSessionTampered(t, path)
}

// TestSessionTamperRow6AppendingAHandWrittenRowIsDetected: an attacker
// without the key cannot produce a valid chain MAC for a forged row.
func TestSessionTamperRow6AppendingAHandWrittenRowIsDetected(t *testing.T) {
	path := buildThreeRowLogFixture(t)
	rawExec(t, path, `INSERT INTO sessions (id, ended_at, payload, mac) VALUES (4, '2026-01-01T00:00:00Z', x'7b7d', 'deadbeef')`)
	assertSessionTampered(t, path)
}

// TestSessionTamperRow7DeleteAllRowsWithNonEmptyHeadIsDetected: emptying
// the table while the snapshot still signs a non-empty head is tamper,
// never mistaken for a legitimately empty log.
func TestSessionTamperRow7DeleteAllRowsWithNonEmptyHeadIsDetected(t *testing.T) {
	path := buildThreeRowLogFixture(t)
	rawExec(t, path, `DELETE FROM sessions`)
	assertSessionTampered(t, path)
}

// TestSessionTamperRow8DropTableWithNonEmptyHeadIsDetected: dropping the
// table entirely, with a non-empty signed head, is the DB-1-established
// "missing != no save" rule applied to the sessions table.
func TestSessionTamperRow8DropTableWithNonEmptyHeadIsDetected(t *testing.T) {
	path := buildThreeRowLogFixture(t)
	rawExec(t, path, `DROP TABLE sessions`)
	assertSessionTampered(t, path)
}

// TestSessionTamperRow9EditEndedAtMirrorOnlyIsDetected: the ended_at
// column existing purely as a denormalized mirror (§5.2) means editing
// it alone — payload and mac untouched, so the chain MAC still verifies
// — must still be caught, by the mirror-vs-payload cross-check.
func TestSessionTamperRow9EditEndedAtMirrorOnlyIsDetected(t *testing.T) {
	path := buildThreeRowLogFixture(t)
	_, payload, mac := rawReadSessionRow(t, path, 2)
	rawUpdateSessionRow(t, path, 2, "2099-01-01T00:00:00Z", payload, mac)
	assertSessionTampered(t, path)
}

// TestSessionTamperRow10EditingSessionLogHeadInTheSnapshotFailsAtTheStateMacFirst:
// editing sessionLogHead inside the SIGNED snapshot payload (not a
// sessions row at all) is caught several steps earlier than
// verifySessionLog even runs — by the ordinary state-row MAC check,
// exactly as docs/plan/P2-design.md §7.3 row 10 states.
func TestSessionTamperRow10EditingSessionLogHeadInTheSnapshotFailsAtTheStateMacFirst(t *testing.T) {
	path := buildThreeRowLogFixture(t)
	schema, payload, mac := rawReadStateRow(t, path)
	tampered := bytes.Replace(payload, []byte(`"sessionLogHead":"`), []byte(`"sessionLogHead":"ZZ`), 1)
	if bytes.Equal(tampered, payload) {
		t.Fatal("fixture assumption wrong: sessionLogHead key not found in the state payload")
	}
	rawUpdateStateRow(t, path, schema, tampered, mac)
	assertSessionTampered(t, path)
}

// TestSessionTamperRow11CopyingAStatePayloadIntoASessionsRowFailsDomainSeparation:
// the state row's own valid (payload, mac) pair, replayed verbatim as a
// sessions row, must fail — logDomain != macDomain (integrity.go), the
// exact purpose domain separation exists to serve.
func TestSessionTamperRow11CopyingAStatePayloadIntoASessionsRowFailsDomainSeparation(t *testing.T) {
	path := buildThreeRowLogFixture(t)
	_, statePayload, stateMac := rawReadStateRow(t, path)
	rawExec(t, path,
		`INSERT INTO sessions (id, ended_at, payload, mac) VALUES (4, '2026-01-01T00:00:00Z', ?, ?)`,
		statePayload, stateMac,
	)
	assertSessionTampered(t, path)
}

// flipHexChar returns mac with its first character flipped to a
// different hex digit — enough to break hmac.Equal without needing to
// know anything about the original value.
func flipHexChar(t *testing.T, mac string) string {
	t.Helper()
	if len(mac) == 0 {
		t.Fatal("flipHexChar: empty mac")
	}
	b := []byte(mac)
	if b[0] == '0' {
		b[0] = '1'
	} else {
		b[0] = '0'
	}
	return string(b)
}

// --- Load verifies identically to LoadAll (§5.4: "cannot be skipped") ---

// TestLoadWrapperVerifiesTheSessionChainIdenticallyToLoadAll proves a
// tampered log is caught through BOTH entry points — the convenient
// Load(path) 3-tuple wrapper is never the hole LoadAll's full check
// would have caught.
func TestLoadWrapperVerifiesTheSessionChainIdenticallyToLoadAll(t *testing.T) {
	path1 := buildThreeRowLogFixture(t)
	rawExec(t, path1, `DELETE FROM sessions WHERE id = 2`)
	_, ok1, err1 := Load(path1)
	if ok1 || !errors.Is(err1, ErrTampered) {
		t.Errorf("Load: ok=%v err=%v, want ok=false and an ErrTampered-wrapping error", ok1, err1)
	}

	path2 := buildThreeRowLogFixture(t)
	rawExec(t, path2, `DELETE FROM sessions WHERE id = 2`)
	_, _, ok2, err2 := LoadAll(path2)
	if ok2 || !errors.Is(err2, ErrTampered) {
		t.Errorf("LoadAll: ok=%v err=%v, want ok=false and an ErrTampered-wrapping error", ok2, err2)
	}
}

// --- AppendSession atomicity (§5.5: "must be one transaction") ----------

// TestAppendSessionForcedFailureLeavesNeitherTheRowNorTheNewHead exercises
// the load-bearing property of §5.5's ONE-transaction write path: if
// anything inside AppendSession's transaction fails, NEITHER the sessions
// row NOR the rewritten (advanced) snapshot head may land — otherwise a
// crash there would leave a log row past the signed head, i.e. a FALSE
// tamper report resetting an innocent user's economy on the next boot.
// The failure is forced by reusing an already-used session id (sessions.id
// is an INTEGER PRIMARY KEY, so the INSERT itself violates uniqueness) —
// a real crash would instead interrupt the transaction mid-flight, but
// SQLite's own atomic commit makes both cases exercise the identical
// rollback guarantee this test checks for.
func TestAppendSessionForcedFailureLeavesNeitherTheRowNorTheNewHead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if err := Save(path, SaveData{Schema: CurrentSchema, DevCash: 10}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	d, _, ok, err := LoadAll(path)
	if err != nil || !ok {
		t.Fatalf("LoadAll: ok=%v err=%v", ok, err)
	}

	start := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	s1 := sampleSessionSave(1, start, start.Add(10*time.Minute), "user")
	head1, err := AppendSession(path, d, s1)
	if err != nil {
		t.Fatalf("AppendSession (first, should succeed): %v", err)
	}
	d.SessionLogHead = head1
	d.Session = nil

	dup := sampleSessionSave(1, start.Add(time.Hour), start.Add(70*time.Minute), "user") // same ID=1 again
	if _, err := AppendSession(path, d, dup); err == nil {
		t.Fatal("expected AppendSession to fail on a duplicate session id, got nil")
	}

	db, err := openDB(path)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	_ = db.Close()
	if count != 1 {
		t.Errorf("sessions row count = %d, want 1 (the failed append must not have inserted a second row)", count)
	}

	after, _, ok, err := LoadAll(path)
	if err != nil || !ok {
		t.Fatalf("LoadAll after the failed append: ok=%v err=%v", ok, err)
	}
	if after.SessionLogHead != head1 {
		t.Errorf("SessionLogHead = %q, want unchanged %q (a failed append must not advance the signed head)", after.SessionLogHead, head1)
	}
}

// --- Conversion helpers GO-3 calls (docs/plan/P2-design.md §8) ----------

// TestSessionSaveFromRecordAndSessionRecordsFromSaveRoundTrip proves the
// exported conversion pair main.go uses on both ends of the log
// (AppendSession's input, RestoreSessionLog's input) round-trips a
// game.SessionRecord's every field, including both timestamps as the
// same instant (not necessarily the same *time.Time bits — RFC3339
// round-trips the instant, which is what Equal checks, not ==).
func TestSessionSaveFromRecordAndSessionRecordsFromSaveRoundTrip(t *testing.T) {
	rec := game.SessionRecord{
		ID:                       7,
		StartedAt:                time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		EndedAt:                  time.Date(2026, 3, 1, 10, 30, 0, 0, time.UTC),
		DurationSeconds:          5400,
		Counters:                 game.StatCounters{Keystrokes: 500, ActiveSeconds: 3000},
		CoinsEarned:              12,
		LongestFocusBlockSeconds: 900,
		EndReason:                "user",
	}
	save := SessionSaveFromRecord(rec)
	back, err := SessionRecordsFromSave([]SessionSave{save})
	if err != nil {
		t.Fatalf("SessionRecordsFromSave: %v", err)
	}
	if len(back) != 1 {
		t.Fatalf("len(back) = %d, want 1", len(back))
	}
	got := back[0]
	if got.ID != rec.ID || got.DurationSeconds != rec.DurationSeconds || got.Counters != rec.Counters ||
		got.CoinsEarned != rec.CoinsEarned || got.LongestFocusBlockSeconds != rec.LongestFocusBlockSeconds || got.EndReason != rec.EndReason {
		t.Errorf("round trip = %+v, want fields matching %+v", got, rec)
	}
	if !got.StartedAt.Equal(rec.StartedAt) || !got.EndedAt.Equal(rec.EndedAt) {
		t.Errorf("timestamps = (%v,%v), want (%v,%v)", got.StartedAt, got.EndedAt, rec.StartedAt, rec.EndedAt)
	}
}
