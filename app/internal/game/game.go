// Package game holds the deterministic, persistable game state: mood +
// activity display, the in-progress sprint, Dev Cash/XP, and the owned/
// equipped store items and tints. It is pure — no file I/O — so
// persistence (internal/store) and the server both wrap it without the
// package needing to know about either. Field names and JSON shapes here
// implement docs/ui-spec.md's WebSocket contract and
// docs/upgrade-design.md's model verbatim.
package game

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/engine"
)

// EquippedRef is one slot's equipped item + tint (docs/ui-spec.md §6.1:
// "equipped has an entry for every slot, always... empty is expressed by
// the slot's *_none item"). TintID is nil for a non-tintable slot's item.
type EquippedRef struct {
	ItemID string  `json:"itemId"`
	TintID *string `json:"tintId"`
}

// SprintView is the `sprint` object inside a `state` message.
type SprintView struct {
	Index     int     `json:"index"`
	Name      string  `json:"name"`
	Progress  float64 `json:"progress"`
	Target    float64 `json:"target"`
	UnitLabel string  `json:"unitLabel"`
}

// ConfigView is the `config` object inside a `state` message (Phase P1,
// docs/plan/PRODUCT-EVOLUTION.md §5 "Phase P1 — Identity & first
// minutes"). It carries the USER-AUTHORED half of dexel's persistence —
// the dexel's name, which lives in ~/.config/dexel/config.json, NOT in
// the protected SaveData at state.db (SEC-1, ADR 0014's config/state
// split). Empty string means "not named yet".
//
// This is the one string on the wire that is neither machine-derived nor
// observed: ADR 0014's Consequences section states the category
// distinction outright — "the user-authored name is a *different
// category* from observed activity — data the user deliberately writes
// about their own pet, not surveillance of their work". It is therefore
// allow-listed in content_free_test.go with that citation, and it must
// never be sourced from anything the activity provider saw.
//
// AlwaysOnTop and ShowAwayTime (SET-1, docs/ui-spec.md §11) are the two
// user PREFERENCES this block gained, and they are here — on the wire —
// for one reason: the client must render per the user's preferences
// without ever deciding them itself. The server owns them (they live in
// the same config.json as Name), so they arrive here alongside it and the
// frontend reads them the way it reads everything else, verbatim.
//
// SoundEnabled (SOUND-1, docs/ui-spec.md §13) is the third, and it is on
// the wire for exactly the same reason: the frontend's audio layer
// (app/frontend/src/render/audio.ts) must gate every sound on the user's
// choice without ever remembering that choice itself, so it reads this
// field on each play() the way the Settings modal reads it to paint the
// toggle. Nothing in THIS package plays a sound or branches on it —
// game.Game only holds it, persists it and sends it.
//
// ShowAwayTime in particular is a DISPLAY switch and nothing more. The
// counters it hides (StatCounters.IdleSeconds, and its per-day and
// per-session equivalents) keep being recorded and keep being SENT
// whatever it says — they are content-free durations, and a counter that
// stopped counting when hidden would silently corrupt every total derived
// from it. "We can record not working but not show user" is exactly what
// that split implements: recording is untouched (ADR 0010/0013),
// presentation is the user's call.
type ConfigView struct {
	Name string `json:"name"`
	// AlwaysOnTop is consumed by the desktop shell, not by the page — it
	// rides this block anyway so the Settings modal can render the
	// toggle's current position from server truth like every other
	// control in this app, rather than from a value it remembered
	// locally.
	AlwaysOnTop bool `json:"alwaysOnTop"`
	// ShowAwayTime gates the Activity modal's away rows client-side. See
	// this type's doc comment for why hiding is presentation-only.
	ShowAwayTime bool `json:"showAwayTime"`
	// SoundEnabled gates every sound effect client-side (SOUND-1). It is
	// the one preference here that defaults to TRUE; the default itself
	// lives in store.ConfigData.SoundEnabledOrDefault, never here.
	SoundEnabled bool `json:"soundEnabled"`
}

// StateMessage is the exact `"type":"state"` WebSocket payload
// (docs/ui-spec.md §6.1). Field names ARE the wire contract — changing one
// without updating docs/ui-spec.md breaks the frontend silently.
type StateMessage struct {
	Type         string                 `json:"type"`
	V            int                    `json:"v"`
	ActiveState  string                 `json:"activeState"`
	ActivityLine string                 `json:"activityLine"`
	DevCash      uint64                 `json:"devCash"`
	Level        int                    `json:"level"`
	XP           uint64                 `json:"xp"`
	StoreOpen    bool                   `json:"storeOpen"`
	Sprint       SprintView             `json:"sprint"`
	ScreenLines  []string               `json:"screenLines"`
	TickerLines  []string               `json:"tickerLines"`
	Equipped     map[string]EquippedRef `json:"equipped"`
	OwnedItems   []string               `json:"ownedItems"`
	OwnedTints   []string               `json:"ownedTints"`
	Stats        StatsView              `json:"stats"`
	// Sessions (Phase P2, docs/plan/P2-design.md §6.1) is the `sessions`
	// block: the active session (nil when none), the derived summary
	// (completed/thisWeek/longestSessionSeconds), and the last
	// SessionsWireWindow finished sessions, newest first. Always sent —
	// the P1 `config` precedent (§6.1: "the server always sends the
	// block, it may be empty") — so a stale frontend degrades to "no
	// sessions" rather than breaking.
	Sessions SessionsView `json:"sessions"`
	// Config/Onboarding are Phase P1 (Identity & first minutes) additions.
	// Both are ADDITIVE and optional client-side (app/frontend/src/wire.ts
	// types them `config?`/`onboarding?`) so a stale frontend degrades to
	// "unnamed, no onboarding" rather than breaking.
	Config ConfigView `json:"config"`
	// Onboarding is TRUE only in the first-launch state: no save existed
	// when this process booted AND config.json carries no name. It is
	// computed by the SERVER (main.go, at boot) and flipped off by
	// SET_NAME — the client never decides it, never sets it, and never
	// keeps showing the intro against a false here.
	Onboarding bool `json:"onboarding"`
	// Paused is PR-5's pause signal (docs/production-runtime/
	// ARCHITECTURE.md Decision 15, MIGRATION_PLAN.md §PR-5) and it is the
	// AUTHORITATIVE one: while it is true the activity provider is
	// STOPPED, engine.Engine.Tick is not called, and nothing about the
	// economy or the analytics is accruing.
	//
	// Deliberately a boolean rather than a fourth `activeState` string:
	// docs/ui-spec.md §6.1 pins activeState to exactly `coding | idle |
	// onBreak`, and while paused this field reports `idle` — the
	// engine's own non-claiming fallback. `onBreak` would be ADR 0010's
	// exact lie ("on break because you paused me") and `coding` is
	// obviously false, so the honest encoding is "the mood says nothing,
	// and `paused` says why".
	Paused bool `json:"paused"`
	// AppIdentityAvailable is the provider's app-identity CAPABILITY bit
	// (activity.Snapshot.AppIdentityAvailable, carried through
	// engine.TickResult unchanged): whether this process's provider can
	// observe the foreground application at all. It is content-free by
	// construction — a single bool ABOUT THE PROVIDER, never about the user
	// — and it exists so the client can tell a real "0 app switches" (Mac,
	// identity available) apart from an unobservable one (Linux/Wayland,
	// ADR 0009) and HIDE the app-derived stat rows in the latter case
	// rather than paint a frozen, misleading "0". The frontend types it
	// optional (`wire.ts: appIdentityAvailable?`); an absent field degrades
	// to "assume available, show the rows" — the pre-existing behaviour, so
	// a stale client is never made worse.
	AppIdentityAvailable bool `json:"appIdentityAvailable"`
}

// StatCounters is one bucket's plain activity counts — content-free by
// construction (docs/plan/ROADMAP.md Analytics track, Phase A1: "counts and
// durations only... never content"). Every field is a uint64 count of
// events or elapsed seconds; nothing here can ever hold typed text, a
// keycode, or a window title. Shared shape for both the `today` and
// `lifetime` buckets on StatsView (see that type's doc comment) and for
// Game's own live accumulators below.
type StatCounters struct {
	// Keystrokes is the sum of engine.TickResult.KeystrokeDelta across every
	// tick in the bucket — the same anti-mash-coalesced count the economy
	// already uses, just summed instead of consumed as a rate.
	Keystrokes uint64 `json:"keystrokes"`
	// MouseActiveSeconds counts one second for every tick whose
	// engine.TickResult.MouseActive was true.
	MouseActiveSeconds uint64 `json:"mouseActiveSeconds"`
	// ActiveSeconds counts one second for every tick whose mood was
	// engine.MoodCoding (a real, recent keystroke — ADR 0010).
	ActiveSeconds uint64 `json:"activeSeconds"`
	// IdleSeconds counts one second for every tick whose mood was NOT
	// engine.MoodCoding (Idle or, only for a globally-honest provider,
	// OnBreak — ADR 0010's honesty rules already govern which of those two
	// a given tick can be, so this counter inherits that honesty for free:
	// a blind provider can still contribute IdleSeconds, just never by way
	// of a dishonest OnBreak claim).
	IdleSeconds uint64 `json:"idleSeconds"`
	// SprintsCompleted counts one for every Tick() call that rolled the
	// sprint index over (the same `completed` this package already reports
	// to main.go for the sprint-complete flash).
	SprintsCompleted uint64 `json:"sprintsCompleted"`
	// FocusSessions (A2, docs/plan/A2-design.md §5) sums
	// engine.TickResult.FocusSessionsCompleted across the bucket — a
	// sustained-typing block reaching engine.FocusSessionSeconds. Ships on
	// both platforms (ADR 0012).
	FocusSessions uint64 `json:"focusSessions"`
	// AppSwitches (A2 §5) sums engine.TickResult.AppSwitches across the
	// bucket, subject to engine.AppSwitchDailyCap applied HERE at this
	// daily-aggregation layer (GO-1 deliberately left cap enforcement to
	// the game layer — see Game.recordStats). Always 0 on Linux, which
	// never sets ActiveApp (ADR 0009); shown honestly, no special-casing.
	AppSwitches uint64 `json:"appSwitches"`
	// PausedSeconds (PR-5, docs/production-runtime/ARCHITECTURE.md
	// Decision 14) counts one second for every tick the runtime spent
	// PAUSED — the user having explicitly said "stop watching me", with
	// the provider stopped and no engine tick taken at all.
	//
	// It is its OWN bucket and it must never be folded into IdleSeconds:
	// idle means "observed, and doing nothing", paused means "not
	// observed". Conflating them would be the ADR 0010 lie in the other
	// direction — a suspiciously idle stretch where the honest answer is
	// "dexel wasn't looking".
	//
	// Together with ActiveSeconds/IdleSeconds this partitions every
	// second the runtime was AWAKE, TICKING AND OBSERVING during the
	// bucket, exactly once: ActiveSeconds counts MoodCoding ticks,
	// IdleSeconds counts every OTHER ticked second the provider could
	// actually see, and PausedSeconds counts every second no tick was
	// taken because the user paused. Hence the unit-tested invariant
	// `ActiveSeconds + IdleSeconds + PausedSeconds == awake, observed
	// seconds in that bucket` (see pause_test.go).
	//
	// That is NOT wall-clock uptime, and the difference is deliberate
	// (docs/plan/BUGS-RESILIENCE.md R8, which amended the claim rather
	// than adding a fourth bucket). Every counter here is per-tick, and
	// two things produce no tick at all: a SUSPENDED machine (Go's
	// tickers are armed on the monotonic clock and do not fire during a
	// sleep) and — since R5 — a tick whose provider was BLIND, which
	// accrues to nothing because unobserved time is not idleness. So for
	// any bucket containing a sleep or a blind stretch, the wall-clock
	// span is strictly larger than this sum. A fourth
	// suspendedSeconds/unobservedSeconds bucket is future work, priced in
	// R8: it would have to be threaded through subtractCounters, both
	// session wire views, StatCountersSave and a schema bump.
	PausedSeconds uint64 `json:"pausedSeconds"`
}

// StatsView is the `stats` object inside a `state` message
// (docs/plan/ROADMAP.md Analytics track, Phase A1): `today`'s counters (the
// local-date bucket currently accumulating) plus `lifetime` running totals.
// Deliberately just these two buckets — a rolling multi-day history is
// Phase A3's job (ROADMAP.md), not this one.
type StatsView struct {
	Today    StatCounters `json:"today"`
	Lifetime StatCounters `json:"lifetime"`
	// CoinsToday (A2 §5/§6) is the per-signal split of the DevCash earned
	// so far today, attributed at sprint-payout time (Game.awardCoins) —
	// see CoinBreakdown's doc comment. Sibling to Today/Lifetime, not
	// nested inside StatCounters (it isn't a plain activity count, it's a
	// coin count).
	CoinsToday CoinBreakdown `json:"coinsToday"`
	// History (A3 §5) is the DENSE, zero-filled, date-complete view of the
	// last game.HistoryRetentionDays local dates, ascending, ending with
	// TODAY (live — built from statsToday/coinsToday, not a finalized
	// bucket). Built fresh on every State() call from the sparse persisted
	// history (see buildHistoryView) — storage stays sparse, the wire is
	// dense (§3.2/§5).
	History []DayStat `json:"history"`
	// Streak (A3 §2) is the server-computed effective streak — see
	// Game.effectiveStreak. The client renders these two numbers verbatim
	// and never re-derives them (§2, "the one thing that must be Go").
	Streak StreakView `json:"streak"`
}

// Game is the mutable in-memory state one running companion process holds.
// Concurrency: the server drives every mutating call from a single
// goroutine (see main.go) — Game itself does no locking, so the caller
// owns synchronization.
type Game struct {
	itemsByID map[string]CatalogItem
	tintsByID map[string]Tint
	slotsByID map[string]Slot
	slots     []Slot

	Mood             engine.Mood
	ActiveApp        string
	ActiveAppDisplay string

	// appIdentityAvailable is the last tick's provider capability bit
	// (engine.TickResult.AppIdentityAvailable, itself Snapshot's — see
	// activity/provider.go). NOT persisted and NOT part of the economy or
	// the analytics tally: it is a live description of THIS process's
	// provider, replayed onto the wire (StateMessage.AppIdentityAvailable)
	// so the client can hide app-derived stat rows where app identity
	// cannot be observed (Linux/Wayland, ADR 0009) instead of rendering a
	// misleading frozen "0 app switches". Zero value (false) is the honest
	// default before any tick has been taken: assume app-blind until a
	// provider says otherwise.
	appIdentityAvailable bool

	sprintIndex int
	Progress    float64 // work units into the current sprint

	DevCash uint64
	XP      uint64

	OwnedItems map[string]bool        // item id -> owned
	OwnedTints map[string]bool        // "<itemId>:<tintId>" -> owned (non-default tints only)
	Equipped   map[string]EquippedRef // slot -> {itemId, tintId}, always populated

	// openStoreConns is the set of connection ids currently holding the
	// work gate open (docs/ui-spec.md §5.3). Refcounted by connID rather
	// than a single global bool so that ANY ONE client's STORE_CLOSE or
	// disconnect only releases ITS OWN hold — it can never release a gate
	// a different, still-connected client is holding, and a client that
	// reconnects mid-session (its earlier connID gone, replaced by a new
	// one) never leaves a stale entry that keeps the gate wedged open
	// either, because the old connID's entry is removed on disconnect
	// before the new one ever opens its own. See StoreOpen (the read) and
	// OpenStore/CloseStore (the writes) below.
	openStoreConns map[uint64]bool

	// ImportedFromRust/ImportedAt are set once, by the legacy-import path
	// (internal/store), and carried forward on every subsequent save —
	// Game itself never sets these except when store.Apply restores them.
	ImportedFromRust bool
	ImportedAt       string // RFC3339, "" if never imported

	// Cosmetic, backend-owned scroll state (docs/ui-spec.md §3). Never
	// read by the economy — purely decorative.
	tickerRotation uint64
	tickerLines    []string // cap 3, newest first
	terminalPushes uint64
	terminalLines  []string // cap 11, oldest first, newest last

	// Analytics track Phase A1 (docs/plan/ROADMAP.md): statsDate is the
	// "YYYY-MM-DD" local date statsToday currently represents ("" before
	// the first tick/load — see rolloverStatsIfNewDay). statsLifetime never
	// resets. Accumulated by recordStats on every engine tick, regardless
	// of StoreOpen — unlike Mood/Progress/DevCash (frozen while shopping
	// per docs/ui-spec.md §5.3, "the game cannot know [if a keystroke was
	// aimed at the store], so it must not claim [work]"), these are a
	// passive tally of the same honest signal Tick() already receives every
	// call (main.go's own comment: "the engine's own keystroke baseline
	// keeps advancing" even while the store is open) — analytics, not
	// economy, so there is no honesty reason to freeze them.
	statsDate     string
	statsToday    StatCounters
	statsLifetime StatCounters

	// statsFocusBlockMax (A3 Fork B, §3.3) is the max engine.TickResult.
	// FocusRunSeconds observed so far during the CURRENT statsDate — the
	// per-day "longest focus block" duration. Reset to 0 alongside
	// statsToday/coinsToday on every rollover (rolloverStatsIfNewDay),
	// folded into the finalized DayBucket's LongestFocusBlockSeconds and,
	// for the still-open today, into today's live DayStat entry (§5).
	statsFocusBlockMax uint64

	// history is the SPARSE, persisted rolling window (§3.2): one
	// DayBucket per local day that actually finalized (via finalizeDay at
	// rolloverStatsIfNewDay), oldest first, length capped at
	// HistoryRetentionDays. A day the process never ran produces no
	// bucket — an honest gap, never fabricated. The dense, zero-filled
	// wire view is built fresh from this on every State() call (see
	// buildHistoryView).
	history []DayBucket

	// streakCurrent/streakLongest/streakLastActiveDate are the persisted
	// streak state (§2.2): current is the run length ending at
	// lastActiveDate, longest never decreases, lastActiveDate is the most
	// recent active local date ("" if none ever recorded — fresh or
	// migrated schema-3 save). Updated only at finalizeDay (§2.3); the
	// wire's effective streak folds in today's in-progress activity at
	// read time without mutating these (§2.4, see effectiveStreak).
	streakCurrent        int
	streakLongest        int
	streakLastActiveDate string

	// coinsToday is the "today" half of the Analytics stats, but tracks
	// COIN counts rather than activity counts (A2 §5) — reset alongside
	// statsToday on a day rollover (rolloverStatsIfNewDay), never touched
	// by statsLifetime's rules since there is no lifetime coin-breakdown
	// bucket (docs/plan/A2-design.md §6 only spec's CoinsToday).
	coinsToday CoinBreakdown

	// workKeys/workMouse/workFocus/workSwitch are the in-memory,
	// UN-persisted per-signal work accumulators "since the last sprint
	// award" (docs/plan/A2-design.md §5): every tick that actually
	// advances Progress (i.e. StoreOpen()==false, mirroring Tick's own
	// economy gate) also folds its per-signal share into these four
	// floats via accrueWork. On sprint completion, awardCoins splits that
	// tick's DevCash proportionally across them and resets all four to
	// 0. They deliberately never cross the wire or hit disk (§5: "no work
	// floats ever cross the wire or hit disk") — only the resulting
	// integer CoinBreakdown does.
	workKeys, workMouse, workFocus, workSwitch float64

	// configName/onboarding are Phase P1's identity state
	// (docs/plan/PRODUCT-EVOLUTION.md §5). They are deliberately NOT part
	// of the persisted economy: internal/store.Snapshot never reads them
	// and SaveData has no field for them (ADR 0014's config/state split —
	// the name lives in the unsigned, hand-editable config.json, which
	// this package, being pure and I/O-free, never touches itself). The
	// server (main.go) seeds configName from store.LoadConfig at boot and
	// writes it back through store.SaveConfig after SetConfigName.
	//
	// onboarding is the server-computed first-launch flag: set once at
	// boot to (no save existed AND configName == "") and cleared for good
	// by SetConfigName. Never set by a client.
	configName string
	onboarding bool

	// prefAlwaysOnTop/prefShowAwayTime are SET-1's user preferences
	// (docs/ui-spec.md §11), and they live here for exactly the reasons
	// configName above does: they are user-authored CONFIG, they are
	// persisted to the unsigned config.json and never to the protected
	// SaveData (ADR 0014's split — internal/store.Snapshot must never
	// learn about them), and this package, being pure, never touches that
	// file itself. The server seeds both from store.LoadConfig at boot
	// (RestorePrefs) and writes them back through store.SaveConfig after
	// SetPref.
	//
	// The first two default to FALSE, which is the zero value, so a fresh
	// game and a fresh config.json agree with no defaulting code.
	// prefShowAwayTime changes NOTHING about what this package records:
	// every away second still lands in statsToday/statsLifetime.IdleSeconds
	// and still crosses the wire. It is read by the frontend as a rendering
	// instruction and by nothing else here.
	//
	// prefSoundEnabled (SOUND-1, docs/ui-spec.md §13) breaks the zero-value
	// convenience on purpose: sound is ON by default, so New() sets it to
	// true explicitly and a bare `Game{}` literal would report it off. That
	// is safe because New() is the ONLY constructor in this package and the
	// only bare literal is the throwaway inside PrefKeys(), which reads
	// nothing but the map's keys — but it is worth saying out loud, because
	// it is the one place a preference's value is not simply "whatever Go
	// gave us". Like the other two, nothing here branches on it; the
	// frontend's audio layer is the only thing that acts on it.
	prefAlwaysOnTop  bool
	prefShowAwayTime bool
	prefSoundEnabled bool

	// paused is PR-5's pause state (docs/production-runtime/
	// ARCHITECTURE.md §6). Unlike onboarding it IS persisted
	// (SaveData.Paused, FORK D: "pause is a user intent... a pause that
	// silently evaporated mid-update would be a lie in the other
	// direction"), so a dexel that was paused when it exited comes back
	// paused.
	//
	// This package only HOLDS the flag and stops accruing; the server
	// (main.go) owns the two side effects that make it true —
	// activity.Provider.Stop()/Start() and skipping engine.Engine.Tick()
	// — because game.Game is pure and knows about neither. See SetPaused,
	// TickPaused, and Tick's own doc comment.
	paused bool

	// session/pendingSession/sessionLog/sessionLogHead/sessionNames/
	// sessionLogPersistedID are
	// Phase P2's (docs/plan/P2-design.md, ADR 0017) session state — see
	// session.go, which owns every type and method that touches them.
	// Declared here (rather than session.go) only because Go requires a
	// type's fields in one place; session.go is the file to read for what
	// they mean.
	session        *activeSession
	pendingSession *SessionRecord
	sessionLog     []SessionRecord
	sessionLogHead string
	sessionNames   map[int]string

	// sessionLogPersistedID is the highest session id known to be
	// DURABLY appended to the log (B-3, docs/plan/REVIEW-2026-08-22.md).
	// It is the floor StartSession's id derivation is anchored on, so a
	// record that never reached the disk cannot leave the id sequence
	// pointing past the last real row. Set at boot from the verified log
	// the store hands back, and after each successful append. See
	// session.go's nextSessionID / SetSessionLogPersistedID.
	sessionLogPersistedID int

	// now is a test seam (mirrors internal/engine.Engine's own `now` field)
	// so TestMidnightRollover-style tests can drive statsDate deterministically
	// instead of depending on the wall clock.
	now func() time.Time
}

// New constructs a fresh game against the default catalog, with every
// slot's free tier-0 item owned and equipped (docs/upgrade-design.md:
// "every slot has a free tier-0 item").
func New() *Game {
	slots := DefaultSlots()
	g := &Game{
		itemsByID:   ByID(DefaultCatalog()),
		tintsByID:   TintsByID(DefaultTints()),
		slotsByID:   SlotsByID(slots),
		slots:       slots,
		Mood:        engine.MoodIdle,
		OwnedItems:  map[string]bool{},
		OwnedTints:  map[string]bool{},
		Equipped:    map[string]EquippedRef{},
		tickerLines: make([]string, 3),
		now:         time.Now,
		// SOUND-1: the one preference whose honest default is ON, so it is
		// the one that cannot ride Go's zero value. See prefSoundEnabled.
		prefSoundEnabled: true,
	}
	g.resetTerminal()
	g.GrantTierZeroDefaults()
	return g
}

// resetTerminal seeds an 11-line blank buffer so ScreenLines is always
// exactly 11 strings, even before the first coding tick.
func (g *Game) resetTerminal() {
	g.terminalLines = make([]string, 11)
}

// GrantTierZeroDefaults owns+equips every slot's free tier-0 item. Called
// on New() and re-asserted by internal/store.Apply after loading a save,
// so a save can never accidentally un-grant a guaranteed default.
func (g *Game) GrantTierZeroDefaults() {
	for _, slot := range g.slots {
		itemID := tierZeroItemBySlot[slot.ID]
		g.OwnedItems[itemID] = true
		if _, equipped := g.Equipped[slot.ID]; !equipped {
			item := g.itemsByID[itemID]
			g.Equipped[slot.ID] = EquippedRef{ItemID: itemID, TintID: cloneTintPtr(item.DefaultTint)}
		}
	}
}

// Catalog/Slots/Tints expose the static tables this game was built with
// (used by internal/store for load validation and legacy import).
func (g *Game) Catalog() []CatalogItem {
	items := make([]CatalogItem, 0, len(g.itemsByID))
	for _, it := range g.itemsByID {
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}
func (g *Game) Slots() []Slot { return g.slots }

// ItemByID/TintByID/SlotByID are read-only catalog lookups for callers
// outside this package (internal/store's load validation and legacy
// import) that need to check an id without duplicating the tables.
func (g *Game) ItemByID(id string) (CatalogItem, bool) { it, ok := g.itemsByID[id]; return it, ok }
func (g *Game) TintByID(id string) (Tint, bool)        { t, ok := g.tintsByID[id]; return t, ok }
func (g *Game) SlotByID(id string) (Slot, bool)        { s, ok := g.slotsByID[id]; return s, ok }

// TierZeroItem returns the guaranteed free default item id for a slot, or
// "" for an unknown slot.
func (g *Game) TierZeroItem(slotID string) string { return tierZeroItemBySlot[slotID] }

// SprintIndex returns the static-list index the current sprint uses.
func (g *Game) SprintIndex() int { return g.sprintIndex }

// RestoreSprint sets the in-progress sprint index/progress directly
// (bypassing Tick's completion logic) — used only by internal/store when
// loading or importing a save. Index is clamped into range and progress
// into [0, target] per docs/upgrade-design.md's load-validation rules, so
// a corrupted or stale save can never panic or overshoot silently.
func (g *Game) RestoreSprint(index int, progress float64) {
	index = clampSprintIndex(index)
	target := sprintAt(index).Target
	if progress < 0 {
		progress = 0
	}
	if progress > target {
		progress = target
	}
	g.sprintIndex = index
	g.Progress = progress
}

// IsTintOwned reports whether tintID is usable on itemID — either it is
// that item's implicit free default, or it was bought explicitly.
func (g *Game) IsTintOwned(itemID, tintID string) bool {
	item, ok := g.itemsByID[itemID]
	if ok && item.DefaultTint != nil && *item.DefaultTint == tintID {
		return true
	}
	return g.OwnedTints[ownedTintKey(itemID, tintID)]
}

func ownedTintKey(itemID, tintID string) string { return itemID + ":" + tintID }

// SetEquipped directly sets a slot's equipped ref, bypassing ownership
// validation — used only by internal/store, which has already validated
// (or corrected) the item/tint per docs/upgrade-design.md's load rules.
func (g *Game) SetEquipped(slot, itemID string, tintID *string) {
	g.Equipped[slot] = EquippedRef{ItemID: itemID, TintID: cloneTintPtr(tintID)}
}

func cloneTintPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// --- Action errors -----------------------------------------------------
//
// Every BUY_ITEM/BUY_TINT/EQUIP_ITEM failure mode docs/ui-spec.md §6.2
// enumerates ("unknown itemId/slot/tintId, an item whose slot does not
// match, buying something already owned, equipping something not owned,
// and affordability") maps to exactly one of these.
var (
	ErrUnknownItem       = errors.New("unknown item id")
	ErrUnknownSlot       = errors.New("unknown slot id")
	ErrUnknownTint       = errors.New("unknown tint id")
	ErrSlotMismatch      = errors.New("item does not belong to that slot")
	ErrAlreadyOwned      = errors.New("already owned")
	ErrNotOwned          = errors.New("not owned")
	ErrNotTintable       = errors.New("slot is not tintable")
	ErrInsufficientFunds = errors.New("insufficient dev cash")
	// ErrLevelLocked — the item's CatalogItem.MinLevel exceeds the player's
	// current level (levelForXP(g.XP)). A new refusal mode on top of the
	// docs/ui-spec.md §6.2 set: level-gating is what makes "LV" mean
	// something (docs/game/BACKLOG.md §2). Checked BEFORE affordability so
	// the "reach LV n" message wins even when the player is also broke —
	// the level is the true blocker (money cannot buy a locked item), and
	// the store already advertises the "LV n" badge proactively.
	ErrLevelLocked = errors.New("level too low")
)

// BuyItem spends DevCash to own an item permanently. Buying never
// auto-equips and grants the item's default tint implicitly (it need not
// be added to OwnedTints — see IsTintOwned).
func (g *Game) BuyItem(itemID string) error {
	item, ok := g.itemsByID[itemID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownItem, itemID)
	}
	if g.OwnedItems[itemID] {
		return fmt.Errorf("%w: %s", ErrAlreadyOwned, itemID)
	}
	// Level gate before affordability (see ErrLevelLocked): a below-level
	// item cannot be bought at any price, so "reach LV n" is the truthful
	// message even for a broke player. No state has mutated at this point.
	if lvl := levelForXP(g.XP); lvl < item.MinLevel {
		return fmt.Errorf("%w: reach LV %d (currently LV %d)", ErrLevelLocked, item.MinLevel, lvl)
	}
	if g.DevCash < item.Price {
		return fmt.Errorf("%w: have %d, need %d", ErrInsufficientFunds, g.DevCash, item.Price)
	}
	g.DevCash -= item.Price
	g.OwnedItems[itemID] = true
	return nil
}

// BuyTint buys a non-default colour for an already-owned tintable item.
func (g *Game) BuyTint(itemID, tintID string) error {
	item, ok := g.itemsByID[itemID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownItem, itemID)
	}
	slot, ok := g.slotsByID[item.Slot]
	if !ok || !slot.Tintable {
		return fmt.Errorf("%w: %s", ErrNotTintable, item.Slot)
	}
	if !g.OwnedItems[itemID] {
		return fmt.Errorf("%w: %s", ErrNotOwned, itemID)
	}
	tint, ok := g.tintsByID[tintID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownTint, tintID)
	}
	if g.IsTintOwned(itemID, tintID) {
		return fmt.Errorf("%w: %s", ErrAlreadyOwned, ownedTintKey(itemID, tintID))
	}
	if g.DevCash < tint.Price {
		return fmt.Errorf("%w: have %d, need %d", ErrInsufficientFunds, g.DevCash, tint.Price)
	}
	g.DevCash -= tint.Price
	g.OwnedTints[ownedTintKey(itemID, tintID)] = true
	return nil
}

// EquipItem wears an owned item (+ owned tint, for a tintable slot),
// replacing whatever else occupied that slot. "EQUIP_ITEM with a tintId
// the player does not own is rejected — equipping is not a back door
// around BUY_TINT" (docs/ui-spec.md §6.2).
func (g *Game) EquipItem(slot, itemID string, tintID *string) error {
	item, ok := g.itemsByID[itemID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownItem, itemID)
	}
	if item.Slot != slot {
		return fmt.Errorf("%w: item %s belongs to slot %s, not %s", ErrSlotMismatch, itemID, item.Slot, slot)
	}
	if !g.OwnedItems[itemID] {
		return fmt.Errorf("%w: %s", ErrNotOwned, itemID)
	}
	slotDef, ok := g.slotsByID[slot]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownSlot, slot)
	}

	var finalTint *string
	if slotDef.Tintable {
		want := ""
		if tintID != nil {
			want = *tintID
		}
		if want == "" {
			return fmt.Errorf("%w: slot %s requires a tintId", ErrUnknownTint, slot)
		}
		if _, ok := g.tintsByID[want]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownTint, want)
		}
		if !g.IsTintOwned(itemID, want) {
			return fmt.Errorf("%w: %s", ErrNotOwned, ownedTintKey(itemID, want))
		}
		finalTint = &want
	}
	g.Equipped[slot] = EquippedRef{ItemID: itemID, TintID: finalTint}
	return nil
}

// BuyAndEquip performs BUY (if the item is not yet owned), BUY_TINT (if the
// slot is tintable and the requested tint is not yet owned) and EQUIP as ONE
// atomic transaction — the one-click store's combined action
// (docs/plan/ROADMAP.md §STORE-REDESIGN). It VALIDATES everything before it
// mutates any state, so a refusal (unknown item/slot/tint, slot mismatch,
// level lock, insufficient funds) leaves DevCash, OwnedItems, OwnedTints and
// Equipped exactly as they were — there is never a half-applied purchase
// (bought-but-not-equipped, or item-bought-then-tint-refused). The
// validation mirrors BuyItem / BuyTint / EquipItem exactly (same errors,
// same precedence: the level gate is checked before affordability, and the
// tint must be owned-or-buyable just as EQUIP_ITEM refuses an unowned tint);
// the only thing this method removes is the wait for a client round-trip
// between the steps. Funds are checked against the COMBINED cost so a click
// that would buy both an item and a non-default tint is refused as one unit
// rather than buying the item and then failing on the tint.
func (g *Game) BuyAndEquip(slot, itemID string, tintID *string) error {
	item, ok := g.itemsByID[itemID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownItem, itemID)
	}
	if item.Slot != slot {
		return fmt.Errorf("%w: item %s belongs to slot %s, not %s", ErrSlotMismatch, itemID, item.Slot, slot)
	}
	slotDef, ok := g.slotsByID[slot]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownSlot, slot)
	}

	// ---- validate only (no state has mutated past this block) ----
	var cost uint64
	buyItem := !g.OwnedItems[itemID]
	if buyItem {
		// Level gate before affordability, exactly as BuyItem does: a
		// below-level item cannot be bought at any price, so "reach LV n"
		// is the truthful refusal even for a broke player.
		if lvl := levelForXP(g.XP); lvl < item.MinLevel {
			return fmt.Errorf("%w: reach LV %d (currently LV %d)", ErrLevelLocked, item.MinLevel, lvl)
		}
		cost += item.Price
	}

	var finalTint *string
	buyTint := false
	if slotDef.Tintable {
		want := ""
		if tintID != nil {
			want = *tintID
		}
		if want == "" {
			return fmt.Errorf("%w: slot %s requires a tintId", ErrUnknownTint, slot)
		}
		tint, ok := g.tintsByID[want]
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnknownTint, want)
		}
		if !g.IsTintOwned(itemID, want) {
			buyTint = true
			cost += tint.Price
		}
		finalTint = &want
	}

	if g.DevCash < cost {
		return fmt.Errorf("%w: have %d, need %d", ErrInsufficientFunds, g.DevCash, cost)
	}

	// ---- apply (validation passed; all-or-nothing) ----
	g.DevCash -= cost
	if buyItem {
		g.OwnedItems[itemID] = true
	}
	if buyTint {
		g.OwnedTints[ownedTintKey(itemID, *finalTint)] = true
	}
	g.Equipped[slot] = EquippedRef{ItemID: itemID, TintID: finalTint}
	return nil
}

// OpenStore/CloseStore implement docs/ui-spec.md §5.3's work gate: while
// the store is open, Tick must accrue no work/Dev Cash and must hold the
// last mood (see Tick's doc comment). Both are keyed by the calling
// connection's id (server.go/handlers.go's connID) rather than acting on a
// single global flag: OpenStore adds connID to the held-open set,
// CloseStore removes it (a no-op if connID never held it, or already
// doesn't — this is what makes both an explicit STORE_CLOSE and a
// disconnect-synthesized one, or a repeat of either, always safe to call).
// StoreOpen reports the actual gate state derived from that set.
func (g *Game) OpenStore(connID uint64) {
	if g.openStoreConns == nil {
		g.openStoreConns = map[uint64]bool{}
	}
	g.openStoreConns[connID] = true
}

func (g *Game) CloseStore(connID uint64) {
	delete(g.openStoreConns, connID)
}

// StoreOpen reports whether ANY connection currently holds the store open
// — the earning gate Tick checks. Reconnecting after a blip, or a second
// client closing its own store, can never flip this false while at least
// one connection still holds it open (see openStoreConns's doc comment).
func (g *Game) StoreOpen() bool {
	return len(g.openStoreConns) > 0
}

// Tick folds one engine.TickResult into game state.
//
// docs/ui-spec.md §5.3 ("shopping must not count as work"): while
// StoreOpen, this returns immediately without touching Mood, ActiveApp,
// Progress, or DevCash — the last honest mood is HELD, exactly ADR 0010's
// "the game cannot know, so it must not claim" reasoning applied to
// shopping instead of an unfocused window. The caller (main.go) must still
// invoke engine.Engine.Tick() every second even while the store is open,
// so the engine's OWN keystroke baseline keeps advancing — otherwise a
// shopping burst of keystrokes would retroactively count as work the
// instant the store closes. That is why this method takes an
// already-computed TickResult rather than owning the decision to sample.
func (g *Game) Tick(r engine.TickResult) (completed bool) {
	// now is read ONCE per tick and threaded through every session call
	// below, rather than re-reading g.now() several times, so a single
	// Tick call can never straddle two different clock readings under a
	// test's SetClockForTest (docs/plan/P2-design.md §2.5).
	now := g.now()

	// checkSessionAutoEnd runs FIRST, "before anything else" (§2.5 point
	// 2), using the session's PRE-existing lastActivityAt/watermark —
	// i.e. as of whatever the last real-input tick left them — so a
	// session that went stale while the process was closed auto-ends on
	// the very first tick after reopening, backdated to that last
	// activity rather than to this tick's "now" (the reopen-after-a-
	// long-close self-heal). It never reads or writes statsToday/
	// statsLifetime/DevCash/XP/Progress/Mood, so it cannot violate the
	// lens rule (§1, §7.1's TestSessionDoesNotAffectTheEconomyAtAll) no
	// matter what it decides.
	g.checkSessionAutoEnd(r, now)

	// recordStats runs unconditionally, BEFORE the StoreOpen gate below —
	// see statsDate's doc comment on the Game struct for why analytics
	// isn't frozen the way Mood/Progress/DevCash are. Its return value
	// (whether THIS tick's app-switch, if any, was counted under
	// engine.AppSwitchDailyCap) still needs to reach accrueWork below, so
	// the same tick's economy-side work split agrees with what the
	// analytics layer just counted. recordStats also folds this tick's
	// r.FocusRunSeconds into the active session's longestFocusBlockSeconds
	// accumulator (§2.3), unconditionally, the same "analytics, not
	// economy" rule statsFocusBlockMax already follows.
	switchCounted := g.recordStats(r)

	// advanceSessionActivity (§2.5 point 2) folds THIS tick's real input,
	// if any, into the session's lastActivityAt/watermark — after
	// recordStats, so watermark's snapshot of statsLifetime already
	// includes this tick's own contribution. Runs unconditionally
	// (session counters "follow the analytics rule, not the economy
	// rule" under STORE_OPEN, §2.4), i.e. BEFORE the StoreOpen gate below.
	g.advanceSessionActivity(r, now)

	// The provider's app-identity capability is recorded UNCONDITIONALLY,
	// like the analytics tally above and unlike Mood/ActiveApp below: it
	// describes what THIS provider can see right now, which does not stop
	// being true because the store is open. It never touches the economy
	// or the persisted save — it only rides the next State() onto the wire
	// so the client can hide app-derived rows where identity is unobservable.
	g.appIdentityAvailable = r.AppIdentityAvailable

	if g.StoreOpen() {
		return false
	}
	g.Mood = r.Mood
	g.ActiveApp = r.ActiveApp
	g.ActiveAppDisplay = r.ActiveAppDisplay

	// accrueWork must be gated by the SAME StoreOpen check as Progress
	// itself (unlike recordStats' unconditional analytics tally): a tick
	// that cannot advance Progress must not be allowed to inflate the
	// per-signal work accumulators either, or the proportional coin split
	// at the next payout would attribute coins to work that never
	// actually earned any.
	g.accrueWork(r, switchCounted)

	g.Progress += r.WorkUnits
	def := sprintAt(g.sprintIndex)
	if g.Progress >= def.Target {
		overshoot := g.Progress - def.Target
		g.DevCash += def.DevCash
		g.XP += def.XP
		g.sprintIndex = nextSprintIndex(g.statsLifetime.SprintsCompleted)
		g.Progress = overshoot
		completed = true
		g.statsToday.SprintsCompleted++
		g.statsLifetime.SprintsCompleted++
		g.awardCoins(def.DevCash)
	}
	return completed
}

// accrueWork folds one (economy-eligible, i.e. !StoreOpen) tick's
// per-signal work contribution into the since-last-payout accumulators —
// see their doc comment on the Game struct.
func (g *Game) accrueWork(r engine.TickResult, switchCounted bool) {
	keyWork, mouseWork, focusWork, switchWork := signalWork(r, switchCounted)
	g.workKeys += keyWork
	g.workMouse += mouseWork
	g.workFocus += focusWork
	g.workSwitch += switchWork
}

// awardCoins is coin attribution proper (docs/plan/A2-design.md §5):
// called exactly once per completed sprint, immediately after def.DevCash
// is added to g.DevCash, so this is accounting ON TOP OF the single coin
// source (ADR 0008) — it never mints coins of its own. It splits that
// same devCash proportionally across the four signals' accrued work
// since the last payout, adds the integer result into today's running
// CoinBreakdown, and resets the accumulators for the next sprint.
func (g *Game) awardCoins(devCash uint64) {
	breakdown := splitCoinsProportional(devCash, g.workKeys, g.workMouse, g.workFocus, g.workSwitch)
	g.coinsToday.Keystrokes += breakdown.Keystrokes
	g.coinsToday.Mouse += breakdown.Mouse
	g.coinsToday.FocusSessions += breakdown.FocusSessions
	g.coinsToday.AppSwitches += breakdown.AppSwitches
	g.workKeys, g.workMouse, g.workFocus, g.workSwitch = 0, 0, 0, 0

	// P2 (docs/plan/P2-design.md §2.3): coinsEarned has no monotonic
	// lifetime counter to subtract from (DevCash is spendable), so it is
	// a per-session accumulator updated HERE, at the single sprint
	// payout — the only coin source (ADR 0008). awardCoins is only ever
	// reached from Tick AFTER the StoreOpen early-return, so this can
	// never fire while the store is open (§2.4's "session coins provably
	// do not accrue" — see TestSessionAccruesWhileStoreOpen).
	if g.session != nil {
		g.session.coinsEarned += devCash
	}
}

// recordStats folds one engine.TickResult into the running Analytics Phase
// A1 counters. Called every tick, independent of StoreOpen: unlike the
// economy fields Tick() freezes while shopping, these counters are a
// passive tally of the same already-honest signal the engine hands every
// call (r.Mood/r.KeystrokeDelta/r.MouseActive are computed fresh every
// tick — see main.go's comment on why eng.Tick() itself always runs).
// Using r directly (never g.Mood, which Tick() leaves stale while
// StoreOpen) is what keeps this honest during a shopping session instead
// of double-counting or freezing on a frozen mood.
// recordStats returns switchCounted: whether THIS tick's app-switch (if
// any — r.AppSwitches is 0/1) was accepted under engine.AppSwitchDailyCap.
// The cap is enforced HERE, at this daily-aggregation layer (GO-1's
// TickResult deliberately leaves it uncapped — see engine.go's doc
// comment on AppSwitchDailyCap): once statsToday.AppSwitches has already
// reached the cap, a further switch this same local day is dropped
// entirely — not counted in today, not in lifetime, and (via the
// returned bool) not folded into the app-switch work accumulator either.
func (g *Game) recordStats(r engine.TickResult) (switchCounted bool) {
	g.rolloverStatsIfNewDay()

	g.statsToday.Keystrokes += r.KeystrokeDelta
	g.statsLifetime.Keystrokes += r.KeystrokeDelta

	if r.MouseActive {
		g.statsToday.MouseActiveSeconds++
		g.statsLifetime.MouseActiveSeconds++
	}

	switch {
	case r.Mood == engine.MoodCoding:
		// Observed work. Reached only through a keystroke/mouse delta the
		// provider actually saw, so it needs no honesty gate of its own.
		g.statsToday.ActiveSeconds++
		g.statsLifetime.ActiveSeconds++
	case !r.SeesGlobalInput():
		// The provider could not see input AT ALL this tick
		// (activity.HonestyBlind: a dead evdev handle after a screen
		// lock, a revoked macOS permission). Unobserved time is NOT
		// idleness — "idle" is a positive claim that dexel looked and saw
		// nothing, and ADR 0010 forbids making a claim the runtime cannot
		// support. So this second is counted in NO bucket: not idle, not
		// active, not paused (docs/plan/BUGS-RESILIENCE.md R5 — the
		// analytics half of the dead-fd field bug, whose provider-side
		// half only stopped the `onBreak` MOOD claim; without this gate a
		// 19-hour blind window still added 19 hours to statsToday.
		// IdleSeconds, to lifetime, and to the open session's delta).
		//
		// Counting nothing rather than adding an UnobservedSeconds bucket
		// is deliberate: a new StatCounters field is not free (it must be
		// threaded through subtractCounters, ActiveSessionView,
		// SessionView, StatCountersSave and a schema bump — see
		// session.go's warning on subtractCounters), and the consequence
		// is the same one a machine suspend already has: the three time
		// buckets partition the seconds the runtime was awake AND
		// OBSERVING, not raw wall-clock uptime (R8's amended invariant).
		// A fourth `unobservedSeconds` bucket is recorded as future work
		// in BUGS-RESILIENCE.md R8.
	default:
		// Idle or OnBreak both count as "not coding right now" for this
		// tally; ADR 0010's honesty rules already decided, upstream in the
		// engine, which of those two moods a blind-vs-global provider is
		// allowed to report — nothing extra to re-derive here.
		g.statsToday.IdleSeconds++
		g.statsLifetime.IdleSeconds++
	}

	g.statsToday.FocusSessions += r.FocusSessionsCompleted
	g.statsLifetime.FocusSessions += r.FocusSessionsCompleted

	// A3 Fork B (§3.3): track today's max sustained-typing run length, fed
	// straight from the engine's own tracker — see statsFocusBlockMax's
	// doc comment on the Game struct.
	if r.FocusRunSeconds > g.statsFocusBlockMax {
		g.statsFocusBlockMax = r.FocusRunSeconds
	}

	if r.AppSwitches > 0 && g.statsToday.AppSwitches < engine.AppSwitchDailyCap {
		g.statsToday.AppSwitches++
		g.statsLifetime.AppSwitches++
		switchCounted = true
	}

	// P2 (docs/plan/P2-design.md §2.3): longestFocusBlockSeconds is the
	// session-scoped analogue of statsFocusBlockMax above — a MAX, not a
	// sum, with no monotonic lifetime counter to derive it from, so it is
	// a per-session accumulator updated HERE, unconditionally, every
	// tick, the same "analytics, not economy" rule statsFocusBlockMax
	// itself already follows (this runs before the StoreOpen gate in
	// Tick). It spans the whole session, so it can exceed any single
	// day's statsFocusBlockMax once a session crosses midnight.
	if g.session != nil && r.FocusRunSeconds > g.session.longestFocusBlockSeconds {
		g.session.longestFocusBlockSeconds = r.FocusRunSeconds
	}

	return switchCounted
}

// statsDateFormat is the local-date key format for the daily bucket ("today"
// only — Phase A1 keeps no multi-day history, see StatsView's doc comment).
const statsDateFormat = "2006-01-02"

// rolloverStatsIfNewDay resets statsToday to zero the moment the local date
// no longer matches statsDate — called from recordStats (so an in-process
// midnight crossing rolls over on the very next tick) and from
// RestoreStats (so a save reopened days later starts today's bucket at
// zero immediately, rather than waiting up to a second for the first tick).
// statsLifetime is never touched here. A blind provider's tick still calls
// this exactly like a global one — it depends only on the wall clock the
// process itself reads, never on anything the activity source can or can't
// see, so it stays correct even when Honesty() == HonestyBlind (ADR 0010).
// rolloverStatsIfNewDay is also A3's (§3.3) single finalize point: the
// moment the local date changes, the day that just ENDED (g.statsDate, if
// any — "" before the first tick/load ever ran) is finalized into history
// + the streak BEFORE today's buckets reset. This fires from both call
// sites unchanged: an in-process midnight crossing (via recordStats, so
// the day finalizes on the very next tick) and RestoreStats on load (so a
// save reopened days later finalizes the single last-running day exactly
// once — same-day reload takes the early return above and never
// double-finalizes; a multi-day-gap reload finalizes only that one last
// day, leaving the intervening never-ran days as honest gaps that break
// the streak via updateStreak's own gap rule).
func (g *Game) rolloverStatsIfNewDay() {
	today := g.now().Local().Format(statsDateFormat)
	if g.statsDate == today {
		return
	}
	if g.statsDate != "" {
		g.finalizeDay(g.statsDate, g.statsToday, g.coinsToday, g.statsFocusBlockMax)
	}
	g.statsDate = today
	g.statsToday = StatCounters{}
	g.coinsToday = CoinBreakdown{}
	g.statsFocusBlockMax = 0
}

// SetClockForTest overrides the clock rolloverStatsIfNewDay reads (used
// only for the local-date stats bucketing this file implements) so a test
// outside this package — internal/store's persistence round-trip tests, in
// particular — can drive statsDate deterministically instead of depending
// on the real wall clock. Mirrors the same seam internal/engine.Engine
// already exposes via its own (unexported, same-package) `now` field;
// exported here only because store's tests need to reach it from outside.
// Never call this from non-test code.
func (g *Game) SetClockForTest(now func() time.Time) {
	g.now = now
}

// StatsSnapshot returns the live stats accumulators for internal/store's
// Snapshot to persist — the local date statsToday was captured for, plus
// the today/lifetime buckets themselves.
func (g *Game) StatsSnapshot() (date string, today, lifetime StatCounters) {
	return g.statsDate, g.statsToday, g.statsLifetime
}

// RestoreStats sets the persisted stats buckets directly (bypassing the
// tick-driven accumulation) — used only by internal/store when loading or
// importing a save. If date is no longer today's local date (the process
// was last running on an earlier day, however many midnights ago), today's
// bucket is reset to zero exactly as an in-process midnight rollover would
// — a save can never resurrect a stale "today" after a real day boundary —
// while lifetime is always carried forward untouched.
func (g *Game) RestoreStats(date string, today, lifetime StatCounters) {
	g.statsDate = date
	g.statsToday = today
	g.statsLifetime = lifetime
	g.rolloverStatsIfNewDay()
}

// CoinsTodaySnapshot/RestoreCoinsToday are the CoinBreakdown analogue of
// StatsSnapshot/RestoreStats above, added additively (rather than folded
// into those two) so this task does not have to change their existing
// call sites in internal/store — persisting CoinsToday (schema 3's new
// StatsSave.Today.CoinBreakdown, docs/plan/A2-design.md §5) is Task
// GO-3's job. Call RestoreCoinsToday BEFORE RestoreStats(date, ...): that
// ordering matters — RestoreStats' own rollover check zeroes coinsToday
// whenever `date` turns out stale (running strictly AFTER whatever
// RestoreCoinsToday just set), so a stale save's CoinBreakdown ends up
// correctly zeroed too, exactly the same "a save can never resurrect a
// stale today" rule RestoreStats already applies to StatCounters. Calling
// it in the other order would let stale coin data survive.
func (g *Game) CoinsTodaySnapshot() CoinBreakdown { return g.coinsToday }

func (g *Game) RestoreCoinsToday(cb CoinBreakdown) {
	g.coinsToday = cb
}

// AdvanceTerminal pushes one new #terminal line while coding
// (docs/ui-spec.md §3.2: "coding: push a line every 0.35s"). idle/onBreak
// leave the buffer untouched — State() overlays the onBreak sentinel
// without mutating the stored history, so scrolling resumes exactly where
// it left off once the mood recovers.
func (g *Game) AdvanceTerminal() {
	if g.Mood != engine.MoodCoding {
		return
	}
	line := terminalLine(g.sprintIndex, g.terminalPushes)
	g.terminalPushes++
	g.terminalLines = append(g.terminalLines, line)
	if len(g.terminalLines) > 11 {
		g.terminalLines = g.terminalLines[len(g.terminalLines)-11:]
	}
}

// RotateTicker computes the next #ticker line and pushes it to the front
// (docs/ui-spec.md §3.1: "one new line every 2.5s... newest at the top").
// Runs regardless of StoreOpen — ticker content is cosmetic flavour, not
// economy, so there is no honesty reason to freeze it.
func (g *Game) RotateTicker() {
	line := tickerLine(g.Mood, g.sprintIndex, g.tickerRotation)
	g.tickerRotation++
	g.tickerLines = append([]string{line}, g.tickerLines...)
	if len(g.tickerLines) > 3 {
		g.tickerLines = g.tickerLines[:3]
	}
}

func (g *Game) ownedItemsSorted() []string {
	out := make([]string, 0, len(g.OwnedItems))
	for id, owned := range g.OwnedItems {
		if owned {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func (g *Game) ownedTintsSorted() []string {
	out := make([]string, 0, len(g.OwnedTints))
	for key, owned := range g.OwnedTints {
		if owned {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// State builds the outward-facing `"type":"state"` snapshot.
func (g *Game) State() StateMessage {
	equipped := make(map[string]EquippedRef, len(g.Equipped))
	for slot, ref := range g.Equipped {
		equipped[slot] = EquippedRef{ItemID: ref.ItemID, TintID: cloneTintPtr(ref.TintID)}
	}

	def := sprintAt(g.sprintIndex)

	screen := append([]string(nil), g.terminalLines...)
	if g.Mood == engine.MoodOnBreak && len(screen) > 0 {
		screen[len(screen)-1] = terminalIdleSentinel
	}
	ticker := append([]string(nil), g.tickerLines...)

	// While paused, the wire's mood is pinned to the engine's own
	// non-claiming fallback (ARCHITECTURE.md Decision 15: "While paused,
	// activeState reports idle... paused: true is the authoritative
	// signal"). SetPaused already parks g.Mood/g.ActiveApp there, so this
	// is belt-and-braces for the one state a hand-restored save could
	// otherwise present: paused, with a mood loaded from somewhere else.
	mood := g.Mood
	if g.paused {
		mood = engine.MoodIdle
	}

	return StateMessage{
		Type:         "state",
		V:            1,
		ActiveState:  string(mood),
		ActivityLine: ActivityLine(mood, g.ActiveApp, g.ActiveAppDisplay, g.configName),
		DevCash:      g.DevCash,
		Level:        levelForXP(g.XP),
		XP:           g.XP,
		StoreOpen:    g.StoreOpen(),
		Sprint: SprintView{
			Index:     g.sprintIndex,
			Name:      def.Name,
			Progress:  g.Progress,
			Target:    def.Target,
			UnitLabel: UnitLabel,
		},
		ScreenLines: screen,
		TickerLines: ticker,
		Equipped:    equipped,
		OwnedItems:  g.ownedItemsSorted(),
		OwnedTints:  g.ownedTintsSorted(),
		Stats: StatsView{
			Today:      g.statsToday,
			Lifetime:   g.statsLifetime,
			CoinsToday: g.coinsToday,
			History:    g.buildHistoryView(),
			Streak:     g.buildStreakView(),
		},
		Sessions: g.sessionsView(),
		Config: ConfigView{
			Name:         g.configName,
			AlwaysOnTop:  g.prefAlwaysOnTop,
			ShowAwayTime: g.prefShowAwayTime,
			SoundEnabled: g.prefSoundEnabled,
		},
		Onboarding:           g.onboarding,
		Paused:               g.paused,
		AppIdentityAvailable: g.appIdentityAvailable,
	}
}
