// sessions.go — the completed-session pending/append pipeline: the
// durable-append retry queue (sessionAppendQueue), popEndedSession (the
// pop-persist-celebrate seam runServe's select loop calls after every
// g.Tick and every applyAction), and the session-complete flash text.
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/game"
	"github.com/jawwadzafar/dexel/app/internal/store"
)

// sessionAppendQueue is B-3's exactly-once, in-order durable-append
// queue (docs/plan/REVIEW-2026-08-22.md).
//
// The bug it fixes: popEndedSession used to take the finished record out
// of game.Game and, when store.AppendSession failed, log it, flash "could
// not save session" and DROP it. The record stayed in the game's
// in-memory log, so every id after it was one higher than the DB's next
// row, and the session log's loader treats a gap as tampering — meaning a
// single transient write failure (ENOSPC, a locked DB) silently armed a
// bomb that quarantined the entire save and wiped the economy on the next
// launch.
//
// So a record taken out of the game is now OWED to the disk: it sits here
// until an append succeeds, and the next tick retries it. Order matters as
// much as persistence — each row's MAC chains onto the previous row's, and
// its id must be the next one on disk — so this is strictly FIFO and a
// failure stops the drain rather than skipping ahead. Records are never
// merged, reordered or discarded; the only way one leaves the queue is a
// committed append.
//
// A process that exits with records still queued loses them, which is the
// one honest outcome available: the durable log is the authority, so the
// next boot simply continues from the last row that really landed (see
// game.SetSessionLogPersistedID) with no gap and no false tamper report.
type sessionAppendQueue struct {
	pending []game.SessionRecord
	// notified is true once the user has been told about the CURRENT
	// head's failure. Without it a permanent failure would fire a toast
	// (and a log line) every single tick; the honest report is one
	// message when it first fails, and one when it finally lands.
	notified bool
}

// popEndedSession implements P2's pending-record seam end to end
// (docs/plan/P2-design.md §2.2, §3.1; ADR 0017 Decision 5): called
// immediately after every g.Tick and every applyAction (main's select
// loop, both call sites), it pops at most one completed-session record
// via g.TakeEndedSession, hands it to the append queue, and then drains
// as much of that queue as the disk will accept — via store.AppendSession,
// GO-2's one-transaction API, so each log row and the rewritten signed
// snapshot land together or not at all. Every record that lands is
// celebrated: a `state` broadcast (the record is already reflected there —
// g.State()'s Sessions.Recent is built straight from the same in-memory
// sessionLog finishSession appended to, see game/session.go), then the
// dedicated `sessionComplete` message, then the ordinary gold
// `flash{kind:"session"}` toast with server-composed text. This is the
// SAME path whether the record came from a user's SESSION_STOP or an
// automatic idle/maxDuration end (docs/plan/P2-design.md §2.2's "both...
// must be persisted and celebrated identically").
//
// Returns whether the GAME had a freshly finished record this call — not
// whether anything was persisted. That is exactly what the SESSION_STOP
// call site needs to tell a genuine completion apart from a discarded
// (< 60s) session, for which it owes its own "too short to keep" flash;
// keying it off the append instead (as it used to) would mean a full disk
// or a leftover retry decided which toast an unrelated stop produced.
func popEndedSession(g *game.Game, hub *Hub, savePath string, q *sessionAppendQueue) bool {
	rec, fresh := g.TakeEndedSession()
	if fresh {
		q.pending = append(q.pending, rec)
	}

	for len(q.pending) > 0 {
		head := q.pending[0]
		// store.Snapshot(g) is re-taken per record: AppendSession chains
		// the new row onto d.SessionLogHead, and the previous iteration's
		// success advanced that head on g.
		newHead, err := store.AppendSession(savePath, store.Snapshot(g), store.SessionSaveFromRecord(head))
		if err != nil {
			// The record is NOT dropped — it stays at the head of the
			// queue and the next tick tries again. Surfacing this as an
			// honest error flash, rather than fabricating a "complete"
			// celebration for a write that did not happen, matches every
			// other write-failure in this file (see persistConfig's
			// callers above): the in-memory state stands, the toast tells
			// the truth about persistence instead.
			if !q.notified {
				log.Printf("append session %d failed (keeping it queued, retrying every tick): %v", head.ID, err)
				hub.broadcastFlash(flashMessage{Type: "flash", Kind: "error", Text: "could not save session"})
				q.notified = true
			}
			return fresh
		}
		q.pending = q.pending[1:]
		if q.notified {
			log.Printf("append session %d succeeded on retry", head.ID)
			q.notified = false
		}
		g.SetSessionLogHead(newHead)
		// The store just confirmed this id is on disk — the floor
		// StartSession's next id is derived from (B-3).
		g.SetSessionLogPersistedID(head.ID)

		state := g.State()
		hub.broadcastState(state)

		// The record's own wire view, found by id rather than assumed to
		// be Recent[0]: with a retry queue the record being celebrated is
		// the OLDEST outstanding one, which is not necessarily the newest
		// in the log. game/session.go's private sessionViewFromRecord
		// stays private and unduplicated either way.
		var view game.SessionView
		found := false
		for _, v := range state.Sessions.Recent {
			if v.ID == head.ID {
				view, found = v, true
				break
			}
		}
		if !found {
			// Recent is a fixed window (game.SessionsWireWindow), so a
			// record that spent long enough in the retry queue to be
			// pushed out of it has no wire view to celebrate. It IS
			// persisted — the state broadcast above already reflects
			// that — and inventing a `sessionComplete` with zeroed
			// counters would be a worse answer than a log line.
			log.Printf("session %d appended, but it has aged out of the %d-session wire window — no completion toast", head.ID, game.SessionsWireWindow)
			continue
		}
		hub.broadcastSessionComplete(sessionCompleteMessage{Type: "sessionComplete", V: 1, Session: view})
		hub.broadcastFlash(flashMessage{Type: "flash", Kind: "session", Text: sessionCompleteFlashText(view)})
	}
	return fresh
}

// sessionCompleteFlashText composes the gold toast's text for a completed
// session (docs/plan/P2-design.md §3.1: "e.g. 'auth refactor — 1h 24m
// together.', or 'Session complete — 1h 24m together.' when unnamed") —
// server-side, never assembled by the client (ui-spec §6.1).
func sessionCompleteFlashText(v game.SessionView) string {
	dur := formatSessionDuration(v.DurationSeconds)
	if v.Name != "" {
		return fmt.Sprintf("%s — %s together.", v.Name, dur)
	}
	return fmt.Sprintf("Session complete — %s together.", dur)
}

// formatSessionDuration renders a whole-seconds duration as "XhYm"
// (dropping the hours segment under an hour) for the session-complete
// flash — e.g. 5040s -> "1h 24m", matching docs/plan/P2-design.md §3.1's
// own worked example. Every session this ever runs against is already
// >= game.SessionMinDurationSeconds (60s), since a shorter one is
// discarded before a record is ever produced, so the minutes segment is
// always meaningful even with no hours.
func formatSessionDuration(totalSeconds uint64) string {
	d := time.Duration(totalSeconds) * time.Second
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
