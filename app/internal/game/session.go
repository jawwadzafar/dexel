// session.go implements Phase P2 — Sessions & the session-complete moment
// (docs/plan/P2-design.md, ADR 0017): a user-declared "I sat down to work"
// container that is a LENS over tracking that already happens, never a
// second economy.
//
// The load-bearing invariant, stated once because it governs every method
// in this file: no code path in Tick may behave differently — for
// DevCash/XP/Progress/Mood/ActiveApp, or any statsToday/statsLifetime
// counter — because a session happens to be open. A session's own numbers
// are always `watermark − baseline`, a difference of two reads of the
// same monotonic StatCounters lifetime bucket, which makes "session ⊆
// global" and "no double-counting" true BY CONSTRUCTION rather than by a
// runtime clamp (§2.3). The two exceptions with no monotonic lifetime
// counter to subtract from — coinsEarned and longestFocusBlockSeconds —
// are per-session ACCUMULATORS updated at their single source-of-truth
// call sites in game.go (Game.awardCoins, Game.recordStats): never both
// an accumulator and a delta for the same number.
//
// Names are deliberately absent from every type in this file
// (SessionRecord, activeSession): a project name is USER-AUTHORED CONFIG
// (§2.7, mirroring identity.go's dexel-name split), kept out of anything
// that could reach the protected save. sessionNames (id -> name) is held
// on Game only as the in-memory mirror of config.json's sessionNames map
// — exactly configName's existing relationship to the unsigned config
// file — and is looked up by id only at wire-build time (sessionsView),
// never stored on a SessionRecord itself.
package game

import (
	"errors"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/engine"
)

// --- Pinned constants (docs/plan/P2-design.md §2.6) --------------------

const (
	// SessionMinDurationSeconds: a session stopped (by any means — the
	// user, an idle auto-end, or the max-duration cap) under this length
	// is DISCARDED (Fork P2-E) — no log row, no counter, no scold. This is
	// what makes the anti-mash guarantee structural: mashing start/stop
	// produces literally nothing.
	SessionMinDurationSeconds = 60

	// SessionIdleTimeoutSeconds (2h): a session auto-ends when this many
	// seconds pass with no real observed input, backdated to the last
	// activity actually seen (§2.5) — gated on the provider's
	// engine.TickResult.SeesGlobalInput().
	SessionIdleTimeoutSeconds = 7200

	// SessionMaxDurationSeconds (16h): a hard cap no honest workday
	// reaches — the only bound a blind provider has, since it cannot see
	// idle at all.
	SessionMaxDurationSeconds = 57600

	// MaxSessionNameLen (32 runes): wider than MaxNameLen (24) because a
	// project name is not a pet name.
	MaxSessionNameLen = 32

	// SessionsWireWindow: how many finished sessions StateMessage.Sessions
	// carries in `recent`, newest first. Storage (sessionLog) is
	// unbounded — this bounds only the wire.
	SessionsWireWindow = 10

	// SessionsWeekDays: the "sessions this week" window, in local dates
	// including today.
	SessionsWeekDays = 7
)

// --- Errors --------------------------------------------------------------

var (
	// ErrSessionAlreadyActive is SESSION_START's rejection when a session
	// is already open — exactly one session at a time (§2.2 step 1).
	ErrSessionAlreadyActive = errors.New("a session is already active")
	// ErrSessionNotActive is SESSION_STOP's rejection when no session is
	// open (§2.2 "Stop", step 1).
	ErrSessionNotActive = errors.New("no session is active")
)

// endReason's closed three-value set (§2.5 point 5) — the same shape as
// engine.Mood's closed set.
const (
	endReasonUser        = "user"
	endReasonIdle        = "idle"
	endReasonMaxDuration = "maxDuration"
)

// activeSession is the in-progress session's private bookkeeping. Every
// field here is either a timestamp, an id, or a StatCounters/accumulator
// — never a name (see this file's package doc comment).
type activeSession struct {
	id        int
	startedAt time.Time

	// lastActivityAt/watermark are updated TOGETHER, only on a tick with
	// real observed input (§2.5: "lastActivityAt advances on any tick
	// with KeystrokeDelta > 0 || MouseActive... and the watermark
	// advances at the SAME moment") — see advanceSessionActivity. This is
	// what keeps an idle/maxDuration auto-end's committed counters
	// mutually consistent with its backdated duration: watermark is
	// frozen at exactly the moment dexel last actually saw the user, so
	// an auto-end's idleSeconds can never exceed its own durationSeconds.
	// A live query (ActiveSession, sessionsView) does NOT read this
	// frozen watermark — it reads the CURRENT statsLifetime directly
	// (always honest, always climbing) — watermark exists purely to feed
	// a future auto-ended record.
	lastActivityAt time.Time
	watermark      StatCounters

	// baseline is the lifetime StatCounters snapshot taken at
	// SESSION_START — the subtrahend every session number is a delta
	// against, forever, for this session's lifetime.
	baseline StatCounters

	// coinsEarned/longestFocusBlockSeconds are the two per-session
	// accumulators §2.3 requires (no monotonic lifetime counter exists
	// for either) — updated at their single call sites in game.go
	// (awardCoins, recordStats), never here.
	coinsEarned              uint64
	longestFocusBlockSeconds uint64
}

// SessionRecord is one session's outward-facing shape — returned live by
// ActiveSession (in-progress; EndedAt zero, EndReason "") and by
// TakeEndedSession/RestoreSessionLog/SessionLogSnapshot (finished;
// EndReason one of endReasonUser/Idle/MaxDuration). Field names mirror
// docs/plan/P2-design.md §5.2's SessionSave exactly, using time.Time
// rather than RFC3339 strings — this is the pure, I/O-free in-memory
// shape; internal/store's GO-2 converts these timestamps to strings when
// building the persisted SessionSave/ActiveSessionSave, and this
// package's own sessionsView (State()) converts them to strings for the
// wire. Deliberately no Name field — see this file's package doc comment.
type SessionRecord struct {
	ID                       int
	StartedAt                time.Time
	EndedAt                  time.Time // zero for a live, not-yet-ended session
	DurationSeconds          uint64
	Counters                 StatCounters
	CoinsEarned              uint64
	LongestFocusBlockSeconds uint64
	EndReason                string // "" while live; user|idle|maxDuration once ended
}

// subtractCounters returns watermark − baseline, field by field, floored
// at 0 (defensive only — every real call site's watermark is a later or
// equal read of the same monotonically-increasing lifetime bucket the
// baseline was taken from, so this floor should never actually bite).
// This single function IS the "session ⊆ global, no double-counting by
// construction" guarantee (§2.3) — every session number in this file
// flows through it.
func subtractCounters(watermark, baseline StatCounters) StatCounters {
	sub := func(a, b uint64) uint64 {
		if a < b {
			return 0
		}
		return a - b
	}
	return StatCounters{
		Keystrokes:         sub(watermark.Keystrokes, baseline.Keystrokes),
		MouseActiveSeconds: sub(watermark.MouseActiveSeconds, baseline.MouseActiveSeconds),
		ActiveSeconds:      sub(watermark.ActiveSeconds, baseline.ActiveSeconds),
		IdleSeconds:        sub(watermark.IdleSeconds, baseline.IdleSeconds),
		SprintsCompleted:   sub(watermark.SprintsCompleted, baseline.SprintsCompleted),
		FocusSessions:      sub(watermark.FocusSessions, baseline.FocusSessions),
		// PausedSeconds (PR-5, MIGRATION_PLAN.md §PR-5: "pausedSeconds
		// joins P2's session delta set"; docs/plan/P2-design.md §2.3's
		// "(+ PausedSeconds once PR-5 lands)"). A session SURVIVES a
		// pause, so the honest reading of its own numbers has to include
		// how much of its wall-clock length was spent not being watched —
		// otherwise a two-hour session with a 90-minute pause in it would
		// look like 90 minutes of unexplained idle.
		//
		// Note for a future counter: §2.3 claims a new StatCounters field
		// "joins the session automatically, with no per-field
		// maintenance". That is true of the DESIGN (every session number
		// is watermark−baseline over the same struct) but NOT of this
		// implementation, which enumerates the fields by hand — so this
		// line, and the two wire views below, had to be added
		// deliberately. Adding a StatCounters field without touching them
		// silently drops it from every session; the delta-set test in
		// session_test.go is what turns that into a failure.
		PausedSeconds: sub(watermark.PausedSeconds, baseline.PausedSeconds),
	}
}

// wallClock strips the MONOTONIC reading from a time.Now() reading, so
// every timestamp this file *stores* is a pure wall-clock value
// (docs/plan/BUGS-RESILIENCE.md R1/R2).
//
// Why this exists at all: a Go time.Time from time.Now() carries two
// clocks, and Sub/Before/After use the monotonic one whenever BOTH
// operands have it. On Linux CLOCK_MONOTONIC does not advance while the
// machine is suspended, so an in-memory session timestamp compared
// against a live time.Now() measures AWAKE seconds only — a laptop that
// sleeps over a weekend never reaches the 2h idle auto-end, the 16h cap
// becomes a cap on 16 hours of awake time, and elapsedSeconds freezes on
// screen. The very same comparisons behave as wall time for a session
// RESTORED from disk, because time.Parse produces no monotonic reading —
// so the bug was really an asymmetry between the live and reloaded paths.
//
// Round(0) at the storage points removes the asymmetry by construction:
// startedAt/lastActivityAt never carry a monotonic reading, so every
// downstream Sub/Before/After falls back to the wall clock (Go's rule
// when EITHER operand lacks one) whether the value came from
// StartSession or from a save file. durationSecondsBetween's floor at 0
// is the guard a backward wall step needs.
func wallClock(t time.Time) time.Time { return t.Round(0) }

// durationSecondsBetween returns end−start in whole seconds, floored at 0
// (a defensive clamp against a backdated end that could theoretically
// land before startedAt — should never happen, since every endAt this
// file computes is either "now" or a lastActivityAt initialized to
// startedAt and only ever advanced forward).
func durationSecondsBetween(start, end time.Time) uint64 {
	d := end.Sub(start)
	if d < 0 {
		d = 0
	}
	return uint64(d / time.Second)
}

// --- Lifecycle: start/stop (§2.2) ---------------------------------------

// StartSession begins a new session (SESSION_START). Rejects with
// ErrSessionAlreadyActive if one is already open — exactly one session at
// a time (§2.2 step 1); the single-owner action loop in main.go
// serializes concurrent clients for free, so this needs no locking of its
// own. name is normalized through NormalizeSessionName — an empty result
// is legal (unnamed is first-class) — and the result is folded into
// g.sessionNames keyed by the session's id, exactly mirroring
// SetConfigName's "game stays pure, the server owns the I/O" split: the
// caller (main.go) reads the whole map back via SessionNames() and writes
// it through store.SaveConfig immediately, the same write-through
// urgency persistConfig already gives SET_NAME (§2.2 step 5).
//
// The id is nextSessionID() — the ordinal the record WILL have if it
// survives past SessionMinDurationSeconds (§2.2 step 2) — so a discarded
// short session simply leaves its id to be reused by the next start, and
// StartSession's sessionNames write for that reused id naturally
// overwrites (or, for an unnamed restart, clears) whatever a previous,
// discarded attempt at that same id might have set — §2.7's "accepted
// desync" doubling as the user's natural "clear that name" path.
func (g *Game) StartSession(name string) error {
	if g.session != nil {
		return ErrSessionAlreadyActive
	}
	// wallClock: session timestamps are stored WITHOUT a monotonic
	// reading, so the 2h idle auto-end, the 16h cap and elapsedSeconds all
	// measure wall time — see wallClock's doc comment (R1/R2).
	now := wallClock(g.now())
	baseline := g.statsLifetime
	g.session = &activeSession{
		id:             g.nextSessionID(),
		startedAt:      now,
		lastActivityAt: now,
		baseline:       baseline,
		watermark:      baseline,
	}

	normalized := g.NormalizeSessionName(name)
	if g.sessionNames == nil {
		g.sessionNames = map[int]string{}
	}
	if normalized == "" {
		delete(g.sessionNames, g.session.id)
	} else {
		g.sessionNames[g.session.id] = normalized
	}
	return nil
}

// StopSession ends the active session (SESSION_STOP, endReason "user").
// Rejects with ErrSessionNotActive if none is open — no state change
// (§2.2 "Stop" step 1). On success the session is ALWAYS cleared; whether
// it produced a poppable record (via TakeEndedSession) depends only on
// SessionMinDurationSeconds (Fork P2-E) — see finishSession. The
// watermark used for the final counters is the CURRENT statsLifetime
// (§2.2 step 2: "advance the watermark to the current lifetime counters,
// so a stop mid-keystroke captures everything"), not the frozen
// last-activity snapshot idle/maxDuration auto-ends use.
func (g *Game) StopSession() error {
	if g.session == nil {
		return ErrSessionNotActive
	}
	g.finishSession(g.statsLifetime, g.now(), endReasonUser)
	return nil
}

// finishSession is the single end-path shared by StopSession and
// checkSessionAutoEnd (§2.2's "pending-record seam": "both a user stop
// and an automatic end produce a record... and both must be... celebrated
// identically, so they share one path"). watermark is the StatCounters
// to compute the final delta against — the CURRENT statsLifetime for a
// user stop, or the session's own FROZEN watermark for an idle/
// maxDuration auto-end (see checkSessionAutoEnd). endAt/reason are the
// already-decided end timestamp and endReason.
//
// Always clears the active session. If the resulting duration is under
// SessionMinDurationSeconds, the session is discarded outright (Fork
// P2-E) — applied uniformly regardless of endReason, since an
// auto-ended session can be exactly as short as a mashed one: no log
// row, no counter added to sessionLog, and TakeEndedSession will report
// nothing. Otherwise the record is appended to the in-memory sessionLog
// (the log cache §4/§7's summary and `recent` are built from — updated
// HERE, synchronously, so a State() call issued immediately after this
// already reflects the just-finished session, before internal/store's
// GO-2 has necessarily persisted it) and queued as the one pending
// record for TakeEndedSession to pop.
func (g *Game) finishSession(watermark StatCounters, endAt time.Time, reason string) {
	s := g.session
	if s == nil {
		return
	}
	g.session = nil

	// wallClock: EndedAt is a STORED timestamp too (it survives into
	// SessionRecord and, through internal/store, into the save as
	// RFC3339 — which strips the monotonic reading anyway). Stripping it
	// here means the in-memory record and the reloaded one are identical
	// in clock semantics, not just in printed form (R1).
	endAt = wallClock(endAt)
	durationSeconds := durationSecondsBetween(s.startedAt, endAt)
	if durationSeconds < SessionMinDurationSeconds {
		return
	}

	rec := SessionRecord{
		ID:                       s.id,
		StartedAt:                s.startedAt,
		EndedAt:                  endAt,
		DurationSeconds:          durationSeconds,
		Counters:                 subtractCounters(watermark, s.baseline),
		CoinsEarned:              s.coinsEarned,
		LongestFocusBlockSeconds: s.longestFocusBlockSeconds,
		EndReason:                reason,
	}
	g.sessionLog = append(g.sessionLog, rec)
	g.pendingSession = &rec
}

// TakeEndedSession pops the one pending completed-session record, if any
// (§2.2's pending-record seam). main.go calls this immediately after
// g.Tick(...) and immediately after applyAction(...) — either call site
// may be the one that produced it (an auto-end during Tick, or a user
// SESSION_STOP during applyAction). Returns (zero, false) when nothing is
// pending, including immediately after a discarded (<
// SessionMinDurationSeconds) stop or auto-end, which intentionally
// produces no record to pop.
func (g *Game) TakeEndedSession() (SessionRecord, bool) {
	if g.pendingSession == nil {
		return SessionRecord{}, false
	}
	rec := *g.pendingSession
	g.pendingSession = nil
	return rec, true
}

// ActiveSession reports the in-progress session's LIVE view, if one is
// open: (zero, false) otherwise. Counters are subtractCounters(current
// statsLifetime, baseline) — always the honest, currently-true delta,
// never the frozen last-activity watermark (that snapshot exists solely
// to feed a future auto-ended record — see activeSession's doc comment).
// EndedAt is zero and EndReason is "" (this session has not ended).
func (g *Game) ActiveSession() (SessionRecord, bool) {
	s := g.session
	if s == nil {
		return SessionRecord{}, false
	}
	return SessionRecord{
		ID:                       s.id,
		StartedAt:                s.startedAt,
		DurationSeconds:          durationSecondsBetween(s.startedAt, g.now()),
		Counters:                 subtractCounters(g.statsLifetime, s.baseline),
		CoinsEarned:              s.coinsEarned,
		LongestFocusBlockSeconds: s.longestFocusBlockSeconds,
	}, true
}

// --- Auto-end (§2.5) -----------------------------------------------------

// checkSessionAutoEnd is evaluated at the TOP of Tick, before recordStats
// touches anything, using whatever lastActivityAt/watermark this
// session's PREVIOUS real-input tick left behind — so a session that went
// stale while the process was closed (or merely idle) auto-ends on the
// very first tick that notices, backdated honestly rather than blamed on
// "now" (§2.5's reopen-after-a-long-close self-heal).
//
// Rule order: idle (2h, only when r.SeesGlobalInput() — a blind provider
// can never know "idle" and must not claim it, ADR 0010) is checked
// first; the 16h hard cap (every provider, honest or blind) second. A
// session that is BOTH stale enough to idle-end and old enough to have
// hit the cap ends via the idle rule, which produces the shorter, more
// honest of the two possible records.
//
// Both bounds are WALL-clock bounds: lastActivityAt/startedAt are stored
// through wallClock, so neither comparison can be measured on a
// monotonic clock that stops during a machine suspend (R1). A laptop that
// sleeps overnight mid-session therefore auto-ends on the first tick
// after wake, backdated to the last real input — the same self-heal a
// reopened save has always had.
func (g *Game) checkSessionAutoEnd(r engine.TickResult, now time.Time) {
	s := g.session
	if s == nil {
		return
	}

	if r.SeesGlobalInput() && now.Sub(s.lastActivityAt) >= SessionIdleTimeoutSeconds*time.Second {
		g.finishSession(s.watermark, s.lastActivityAt, endReasonIdle)
		return
	}

	capBoundary := s.startedAt.Add(SessionMaxDurationSeconds * time.Second)
	if now.Before(capBoundary) {
		return
	}
	// §2.5 point 4: "backdated to lastActivityAt when that is known,
	// otherwise startedAt + cap" — i.e. lastActivityAt UNLESS it has
	// itself caught up to (or past) the cap boundary through continued
	// real activity, in which case the boundary itself is the ceiling: a
	// hard cap must never be exceeded by the record it produces.
	endAt := s.lastActivityAt
	if endAt.After(capBoundary) {
		endAt = capBoundary
	}
	g.finishSession(s.watermark, endAt, endReasonMaxDuration)
}

// advanceSessionActivity folds this tick's real input, if any, into the
// active session's lastActivityAt/watermark (§2.5: both advance "at the
// SAME moment"). Called from Tick AFTER recordStats, so watermark's
// snapshot of statsLifetime already includes this tick's own
// contribution, and unconditionally (before the StoreOpen gate) — session
// bookkeeping follows the analytics rule, not the economy rule (§2.4).
func (g *Game) advanceSessionActivity(r engine.TickResult, now time.Time) {
	s := g.session
	if s == nil {
		return
	}
	if r.KeystrokeDelta > 0 || r.MouseActive {
		// wallClock for the same reason StartSession uses it: this is a
		// STORED session timestamp, and the idle auto-end compares it
		// against a later now (R1).
		s.lastActivityAt = wallClock(now)
		s.watermark = g.statsLifetime
	}
}

// --- Names (§2.7) --------------------------------------------------------

// NormalizeSessionName is the ONLY door a project name enters the process
// through. Unlike NormalizeName, an empty result is LEGAL — "unnamed" is
// a first-class session state, not a rejection — so this returns a bare
// string, never an error (docs/plan/P2-design.md §2.7).
func (g *Game) NormalizeSessionName(raw string) string {
	return normalizeUserText(raw, MaxSessionNameLen)
}

// SessionNames returns a defensive copy of the id -> name map (§2.7) for
// internal/store's GO-2/GO-3 to persist through store.SaveConfig — the
// exact "game stays pure, the server owns the I/O" split ConfigName/
// SetConfigName already use for the dexel's own name.
func (g *Game) SessionNames() map[int]string {
	out := make(map[int]string, len(g.sessionNames))
	for id, name := range g.sessionNames {
		out[id] = name
	}
	return out
}

// RestoreSessionNames seeds the id -> name map loaded from config.json at
// boot. Every value is run through NormalizeSessionName — matching
// RestoreConfigName's contract that a malformed, hand-edited config.json
// degrades (here: an entry that normalizes to "" is dropped) rather than
// blocking startup (§2.7). Per docs/plan/P2-design.md §8 (GO-2's task),
// callers must invoke this — and RestoreSessionLog — BEFORE RestoreStats,
// the same A3 ordering rule RestoreHistory/RestoreStreak already follow.
func (g *Game) RestoreSessionNames(names map[int]string) {
	g.sessionNames = make(map[int]string, len(names))
	for id, raw := range names {
		if name := g.NormalizeSessionName(raw); name != "" {
			g.sessionNames[id] = name
		}
	}
}

// --- The finished-session log cache (§5.6) ------------------------------

// SessionLogHead returns the opaque chained-MAC head internal/store's
// GO-2 hands back from AppendSession — Game never interprets this string,
// exactly the relationship ImportedFromRust/ImportedAt already have with
// the store (see Game's doc comment on those fields).
func (g *Game) SessionLogHead() string { return g.sessionLogHead }

// SetSessionLogHead sets the opaque chained-MAC head — called by
// internal/store's GO-2 after a successful AppendSession, and by the
// server's boot-time restore path with whatever head LoadAll verified.
func (g *Game) SetSessionLogHead(head string) { g.sessionLogHead = head }

// RestoreSessionLog seeds the in-memory finished-session cache from
// internal/store's GO-2 at boot (the FULL verified log, oldest-first/
// ascending id — nextSessionID's derivation depends on this ordering to
// stay monotonic across restarts). Per §8's ordering rule, call this —
// and RestoreSessionNames — BEFORE RestoreStats.
//
// It also raises the persisted-id floor to the last restored record's id
// (B-3): these records came out of the verified on-disk log, so their
// last id IS the last durable row.
func (g *Game) RestoreSessionLog(records []SessionRecord) {
	g.sessionLog = append([]SessionRecord(nil), records...)
	if n := len(records); n > 0 {
		g.SetSessionLogPersistedID(records[n-1].ID)
	}
}

// SetSessionLogPersistedID raises the durable-append floor to id, and is
// B-3's fix for the id-derivation half of the bug (docs/plan/
// REVIEW-2026-08-22.md).
//
// Before it, StartSession derived its id from len(sessionLog)+1 alone.
// That slice holds finished records whether or not they ever reached the
// disk, so ONE failed store.AppendSession — a full disk, a locked DB —
// permanently offset every future id by one from the DB's next row
// number. The append log's loader treats a gap as tampering, so the next
// launch quarantined the whole save and wiped an honest user's economy.
// It also broke in the other direction: a boot that could not convert the
// verified log into records restored an EMPTY cache while the DB still
// held rows, and the next append collided with an existing id.
//
// Anchoring on the last id the store confirmed it wrote closes both. The
// setter only ever RAISES the floor, so callers may invoke it in any
// order relative to RestoreSessionLog without a late, lower value
// re-opening the hole.
func (g *Game) SetSessionLogPersistedID(id int) {
	if id > g.sessionLogPersistedID {
		g.sessionLogPersistedID = id
	}
}

// SessionLogPersistedID reports the highest session id known to be
// durably appended (B-3). Exposed for the server's own logging and for
// the regression tests that pin the id sequence.
func (g *Game) SessionLogPersistedID() int { return g.sessionLogPersistedID }

// nextSessionID is the ordinal the next started session will claim if it
// lives long enough to be kept (B-3). It is the higher of the two floors
// that must both be respected:
//
//   - len(sessionLog): every finished record this process knows about,
//     including any whose durable append has not landed yet. Reusing one
//     of those ids would duplicate a row.
//   - sessionLogPersistedID: the last id the store actually wrote. Going
//     below it would collide with a row on disk (see
//     SetSessionLogPersistedID for how the two can disagree).
//
// The server must never let a finished record be dropped instead of
// retried — otherwise a gap appears on disk no id derivation can repair.
// popEndedSession (main.go) owns that half.
func (g *Game) nextSessionID() int {
	next := len(g.sessionLog)
	if g.sessionLogPersistedID > next {
		next = g.sessionLogPersistedID
	}
	return next + 1
}

// SessionLogSnapshot returns a defensive copy of the full in-memory
// finished-session cache, oldest first, unbounded (§5.6: "storage is
// unbounded" — only the wire's `recent` is windowed, see sessionsView).
func (g *Game) SessionLogSnapshot() []SessionRecord {
	out := make([]SessionRecord, len(g.sessionLog))
	copy(out, g.sessionLog)
	return out
}

// --- Restart survival for the ACTIVE session (§5.1) ---------------------

// ActiveSessionSnapshot returns the in-progress session's persistable
// fields for internal/store's GO-2 Snapshot (SaveData.Session,
// ActiveSessionSave §5.1) — baseline AND watermark both, separately (not
// their delta), because a reloaded session must be able to resume
// exactly, including the frozen-at-last-activity watermark an idle
// auto-end may need on the very next tick. ok is false when no session is
// active, in which case the other return values are zero and GO-2 must
// persist SaveData.Session as nil/omitted.
func (g *Game) ActiveSessionSnapshot() (ok bool, id int, startedAt, lastActivityAt time.Time, baseline, watermark StatCounters, coinsEarned, longestFocusBlockSeconds uint64) {
	s := g.session
	if s == nil {
		return false, 0, time.Time{}, time.Time{}, StatCounters{}, StatCounters{}, 0, 0
	}
	return true, s.id, s.startedAt, s.lastActivityAt, s.baseline, s.watermark, s.coinsEarned, s.longestFocusBlockSeconds
}

// RestoreActiveSession restores an in-progress session exactly as
// internal/store's GO-2 persisted it (ActiveSessionSave §5.1) — used only
// when loading or importing a save, called BEFORE the first Tick/State()
// so a stale session can auto-end honestly on that very first tick (§2.5
// point 2's reopen-after-a-long-close self-heal). id <= 0 means "no
// active session was saved" (a fresh game, or a pre-P2 schema-5 save) and
// clears any in-memory session outright.
func (g *Game) RestoreActiveSession(id int, startedAt, lastActivityAt time.Time, baseline, watermark StatCounters, coinsEarned, longestFocusBlockSeconds uint64) {
	if id <= 0 {
		g.session = nil
		return
	}
	// wallClock is a no-op for a value that came out of time.Parse (a
	// parsed time never carries a monotonic reading), and is applied
	// anyway so "a stored session timestamp is wall-clock" holds by
	// construction on EVERY path into activeSession, not just the two
	// that happen to need it today (R1).
	g.session = &activeSession{
		id:                       id,
		startedAt:                wallClock(startedAt),
		lastActivityAt:           wallClock(lastActivityAt),
		baseline:                 baseline,
		watermark:                watermark,
		coinsEarned:              coinsEarned,
		longestFocusBlockSeconds: longestFocusBlockSeconds,
	}
}

// --- Wire (§6.1) ----------------------------------------------------------

// ActiveSessionView is the `sessions.active` object — null when no
// session is open. Every field is a count, a duration, an id, an ISO
// timestamp, or (Name) the one user-authored string, allow-listed exactly
// like ConfigView.Name (see content_free_test.go).
type ActiveSessionView struct {
	ID                       int    `json:"id"`
	Name                     string `json:"name"` // "" when unnamed
	StartedAt                string `json:"startedAt"`
	ElapsedSeconds           uint64 `json:"elapsedSeconds"` // SERVER-computed
	Keystrokes               uint64 `json:"keystrokes"`
	MouseActiveSeconds       uint64 `json:"mouseActiveSeconds"`
	ActiveSeconds            uint64 `json:"activeSeconds"`
	IdleSeconds              uint64 `json:"idleSeconds"`
	SprintsCompleted         uint64 `json:"sprintsCompleted"`
	FocusSessions            uint64 `json:"focusSessions"`
	PausedSeconds            uint64 `json:"pausedSeconds"`
	CoinsEarned              uint64 `json:"coinsEarned"`
	LongestFocusBlockSeconds uint64 `json:"longestFocusBlockSeconds"`
}

// SessionView is one finished session, as sent in `sessions.recent` and
// in the `sessionComplete` message's `session` field.
type SessionView struct {
	ID                       int    `json:"id"`
	Name                     string `json:"name"`
	StartedAt                string `json:"startedAt"`
	EndedAt                  string `json:"endedAt"`
	DurationSeconds          uint64 `json:"durationSeconds"`
	Keystrokes               uint64 `json:"keystrokes"`
	MouseActiveSeconds       uint64 `json:"mouseActiveSeconds"`
	ActiveSeconds            uint64 `json:"activeSeconds"`
	IdleSeconds              uint64 `json:"idleSeconds"`
	SprintsCompleted         uint64 `json:"sprintsCompleted"`
	FocusSessions            uint64 `json:"focusSessions"`
	PausedSeconds            uint64 `json:"pausedSeconds"`
	CoinsEarned              uint64 `json:"coinsEarned"`
	LongestFocusBlockSeconds uint64 `json:"longestFocusBlockSeconds"`
	EndReason                string `json:"endReason"` // user|idle|maxDuration
}

// SessionsSummary is `sessions.summary` — all three numbers derived from
// the verified log itself (§4), so none needs a second protected counter
// that could drift from it.
type SessionsSummary struct {
	Completed             uint64 `json:"completed"`
	ThisWeek              int    `json:"thisWeek"`
	LongestSessionSeconds uint64 `json:"longestSessionSeconds"`
}

// SessionsView is `StateMessage.Sessions` (the `sessions` block, §6.1).
type SessionsView struct {
	Active  *ActiveSessionView `json:"active"` // null when none
	Summary SessionsSummary    `json:"summary"`
	Recent  []SessionView      `json:"recent"` // newest first, <= SessionsWireWindow
}

// sessionsView is State()'s entry point for the `sessions` block.
func (g *Game) sessionsView() SessionsView {
	var active *ActiveSessionView
	if s := g.session; s != nil {
		c := subtractCounters(g.statsLifetime, s.baseline)
		active = &ActiveSessionView{
			ID:        s.id,
			Name:      g.sessionNames[s.id],
			StartedAt: s.startedAt.Format(time.RFC3339),
			// Wall-clock by construction: startedAt carries no monotonic
			// reading (wallClock), so this delta is the same wall-clock
			// quantity the startedAt field above prints — the two halves
			// of this frame can no longer disagree across a suspend (R2).
			ElapsedSeconds:           durationSecondsBetween(s.startedAt, g.now()),
			Keystrokes:               c.Keystrokes,
			MouseActiveSeconds:       c.MouseActiveSeconds,
			ActiveSeconds:            c.ActiveSeconds,
			IdleSeconds:              c.IdleSeconds,
			SprintsCompleted:         c.SprintsCompleted,
			FocusSessions:            c.FocusSessions,
			PausedSeconds:            c.PausedSeconds,
			CoinsEarned:              s.coinsEarned,
			LongestFocusBlockSeconds: s.longestFocusBlockSeconds,
		}
	}

	recent := make([]SessionView, 0, SessionsWireWindow)
	for i := len(g.sessionLog) - 1; i >= 0 && len(recent) < SessionsWireWindow; i-- {
		recent = append(recent, sessionViewFromRecord(g.sessionLog[i], g.sessionNames[g.sessionLog[i].ID]))
	}

	return SessionsView{
		Active:  active,
		Summary: g.sessionsSummary(),
		Recent:  recent,
	}
}

func sessionViewFromRecord(rec SessionRecord, name string) SessionView {
	return SessionView{
		ID:                       rec.ID,
		Name:                     name,
		StartedAt:                rec.StartedAt.Format(time.RFC3339),
		EndedAt:                  rec.EndedAt.Format(time.RFC3339),
		DurationSeconds:          rec.DurationSeconds,
		Keystrokes:               rec.Counters.Keystrokes,
		MouseActiveSeconds:       rec.Counters.MouseActiveSeconds,
		ActiveSeconds:            rec.Counters.ActiveSeconds,
		IdleSeconds:              rec.Counters.IdleSeconds,
		SprintsCompleted:         rec.Counters.SprintsCompleted,
		FocusSessions:            rec.Counters.FocusSessions,
		PausedSeconds:            rec.Counters.PausedSeconds,
		CoinsEarned:              rec.CoinsEarned,
		LongestFocusBlockSeconds: rec.LongestFocusBlockSeconds,
		EndReason:                rec.EndReason,
	}
}

// sessionsSummary computes §4's three derived numbers from the verified
// log cache (g.sessionLog) — completed is simply its length, thisWeek
// counts entries whose local end-date falls within the last
// SessionsWeekDays local dates INCLUDING today, and longestSessionSeconds
// is the max DurationSeconds across the whole log (never bounded by the
// wire's SessionsWireWindow).
func (g *Game) sessionsSummary() SessionsSummary {
	today := g.now().Local().Format(statsDateFormat)
	weekStart := addDaysToDateString(today, -(SessionsWeekDays - 1))

	var longest uint64
	var thisWeek int
	for _, rec := range g.sessionLog {
		if rec.DurationSeconds > longest {
			longest = rec.DurationSeconds
		}
		endDate := rec.EndedAt.Local().Format(statsDateFormat)
		if endDate >= weekStart && endDate <= today {
			thisWeek++
		}
	}

	return SessionsSummary{
		Completed:             uint64(len(g.sessionLog)),
		ThisWeek:              thisWeek,
		LongestSessionSeconds: longest,
	}
}
