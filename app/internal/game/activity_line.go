package game

import (
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/activity"
	"github.com/jawwadzafar/dexel/app/internal/engine"
)

// ActivityLine composes the ONE true sentence on screen (docs/ui-spec.md
// §2.3: "the only place on screen that says anything true about the user's
// machine") from the engine's honest mood plus the sanitized app identity.
//
// It is explicitly NOT the same kind of string as TickerLines: the ticker is
// the character's own chatter and is allowed to be fiction (v0.4 plan
// non-negotiable #5, docs/ui-spec.md §2.3: "never let a ticker line borrow a
// word from the real one"). Everything this function returns must be TRUE of
// the two signals it is given, and nothing else. That boundary is the whole
// reason this file is careful.
//
// The two signals, precisely:
//
//	mood == Coding  ->  a key went down SOMEWHERE in the last
//	                    engine.CodingRecencyWindow. ADR 0011's macOS provider
//	                    polls a GLOBAL HID timer; it cannot know WHICH app
//	                    received the keystroke. ADR 0010 guarantees mouse
//	                    motion alone never produces Coding.
//	mood == Idle    ->  at the desk (recent mouse, or shortly after typing).
//	mood == OnBreak ->  genuine global idleness, only ever claimed by a
//	                    provider that can actually see input.
//	appID           ->  the frontmost application RIGHT NOW, sanitized to an
//	                    app identity (ADR 0009) — never a window title.
//
// Joining "someone typed" to "this app is in front" is an INFERENCE, and the
// old rule made it unconditionally: `mood == Coding && !isTerminalApp` emitted
// "Coding in " + app. The first real frontmost-app sample on macOS produced
// **"Coding in Brave"** — a sentence about typing code into a browser, from
// data that never said where the keystroke landed. So:
//
//  1. A work verb ("Coding in X") is claimed ONLY for coding-class apps
//     (activity.AppTypeCoding / AppTypeTerminal), where a developer's
//     keystrokes plausibly went. See activityLinePools.work.
//  2. Every other app type gets PRESENCE phrasing, true no matter where the
//     keystrokes landed ("In X", "Browsing in X", "X has focus").
//  3. An app we cannot classify gets presence phrasing too, never a guess
//     (activity.AppTypeUnknown is a first-class answer, not a bucket).
//
// The phrasing is drawn from a small pool per app type so the panel feels
// alive rather than printing one frozen string forever — stably, see
// activityLineComposer.pick for the no-churn rule.
func ActivityLine(mood engine.Mood, appID, appDisplay string) string {
	return defaultActivityLineComposer.line(mood, appID, appDisplay)
}

// activityLineRerollInterval is how long a chosen phrasing sticks while the
// frontmost app does not change. See activityLineComposer.pick.
const activityLineRerollInterval = 45 * time.Second

// maxActivityLineLen is the width docs/ui-spec.md §2.3 gives #status-line:
// "34 chars max" at 8px in the 288px content box. A pool entry that does not
// fit for the current app's display name is simply not offered (pick), which
// keeps long friendly names ("Affinity Designer") from choosing a phrasing
// that would be clipped by the frontend.
const maxActivityLineLen = 34

// activityLinePools holds the phrasings allowed for one app type, split by
// how much the two signals actually license.
//
// EVERY line in EVERY pool must be true for its app type in its tier — that
// is the invariant this whole file exists to hold, and
// activity_line_test.go's matrix test is what enforces it. A cute line that
// asserts an activity dexel cannot observe is a bug, not flavour.
//
// The "{app}" token is replaced with the friendly display name. A pool entry
// may deliberately omit it ("In the terminal") — naming the CLASS of app in
// front is as true as naming the app, and it is the phrasing ADR 0009 and
// docs/ui-spec.md §2.3 already shipped.
type activityLinePools struct {
	// work is claimed only when mood == Coding AND this is a coding-class
	// app type. These are the only lines that assert the user was WORKING,
	// and they may only exist for app types where the global keystroke could
	// plausibly have landed. Empty for every other type — that emptiness is
	// what makes "Coding in Brave" unrepresentable rather than merely
	// unreachable.
	work []string
	// atDesk lines assert something about the PERSON as well as the app, so
	// they are offered only while the engine says the user is at the desk
	// (mood Coding or Idle — ADR 0010: "Idle = at the desk"). They are
	// withheld on OnBreak, where the user is provably away and a line like
	// "Browsing in Chrome" would be describing an empty chair.
	atDesk []string
	// always lines assert ONLY what the OS told us — that this application
	// is frontmost — so they are true in every mood, including OnBreak, and
	// for an app type we could not classify at all. Every pool ends up with
	// activityLinePresence appended here.
	always []string
}

// activityLinePresence is the generic presence pool, shared by every app
// type. Each line says exactly one thing: the OS reports this application as
// the frontmost one. That is true whether the user is typing, mousing, or
// out at lunch, and true whether or not we know what kind of app it is —
// which is why this is the pool the unknown case falls back to.
var activityLinePresence = []string{
	"In {app}",
	"Over in {app}",
	"{app} is up front",
	"{app} has focus",
	"On screen: {app}",
	"Frontmost: {app}",
}

// activityLinePoolsByType is the per-app-type phrasing table, keyed by the
// classification in internal/activity (which is in turn keyed by the
// sanitized app id, next to the friendly-name table so the two cannot
// drift). Adding an app type without adding a row here degrades to presence
// phrasing only — honest, if bland — and
// TestEveryAppTypeHasAPool fails so nobody has to notice by accident.
var activityLinePoolsByType = map[activity.AppType]activityLinePools{
	// An editor is frontmost and a key went down in the last few seconds:
	// this is the one join ADR 0009 always intended, and the only app class
	// where a developer's keystrokes plausibly landed.
	activity.AppTypeCoding: {
		work: []string{
			// The canonical ADR 0009 line.
			"Coding in {app}",
			// The most literal reading of the two signals there is: keys
			// went down, this editor is in front.
			"Typing in {app}",
			// Recent keystrokes + an editor in front = at the keyboard,
			// working. The most interpretive line in this file; it claims
			// engagement, not a specific act.
			"Heads-down in {app}",
		},
		// True of the app, in any mood: the frontmost app is an editor.
		always: withPresence("In the editor"),
	},
	// A terminal is coding-class for the same reason, and keeps the phrasing
	// ADR 0009 / docs/ui-spec.md §2.3 already shipped. A terminal-hosted
	// editor (vim, helix) reports the TERMINAL's app name, so this pool has
	// to cover that style of working too — which "Coding in Ghostty" does.
	activity.AppTypeTerminal: {
		work: []string{
			"Coding in {app}",
			"Typing in {app}",
			// The original line, still exactly true: a terminal emulator is
			// the frontmost app.
			"In the terminal",
		},
		always: withPresence("In the terminal"),
	},
	// A browser NEVER gets a work verb: you can certainly type in one, but
	// the global keystroke timer cannot tell us that you did, and asserting
	// it is the exact bug this change removes.
	activity.AppTypeBrowser: {
		atDesk: []string{
			// A browser is frontmost and the user is at the desk. This says
			// a browser session is in front of them — not what is in the
			// tab, which dexel does not and will not know (ADR 0009).
			"Browsing in {app}",
		},
		always: withPresence("In the browser"),
	},
	// Chat, mail and meetings get presence phrasing and nothing more. There
	// is deliberately no "Chatting in {app}" or "In a meeting": the frontmost
	// app being Slack does not mean a message is being written, and Zoom
	// being frontmost does not mean a call is running (it is just as likely
	// the launcher window). Both would be inventing an activity.
	activity.AppTypeComms: {
		always: activityLinePresence,
	},
	activity.AppTypeDesign: {
		// True of the app: the frontmost app is a design tool. Says nothing
		// about whether anything is being designed.
		always: withPresence("In the design tool"),
	},
	activity.AppTypeMedia: {
		// Deliberately about the APP, not playback: "Spotify is playing"
		// would be a claim dexel cannot make (it never sees playback state,
		// and the app is just as often paused).
		always: withPresence("In the media app"),
	},
	activity.AppTypeNotes: {
		always: withPresence("In the docs"),
	},
	activity.AppTypeFiles: {
		always: withPresence("In the file browser"),
	},
	// A named app we have not classified. Presence only — the honest answer
	// (see activity.AppTypeUnknown), and never a guessed category.
	activity.AppTypeUnknown: {
		always: activityLinePresence,
	},
	// dexel's own window never reaches a pool (line() short-circuits it, and
	// activity.SelfAppID explains why at length). The empty pool is here so
	// that if it ever DID reach one, the result would be the no-app fallback
	// rather than a sentence naming dexel.
	activity.AppTypeSelf: {},
}

// withPresence returns the generic presence pool plus the app type's own
// class line ("In the terminal"), which is the shape almost every pool's
// `always` tier wants. It copies rather than appending in place so two types
// can never end up sharing (and mutating) one backing array.
func withPresence(classLines ...string) []string {
	out := make([]string, 0, len(activityLinePresence)+len(classLines))
	out = append(out, activityLinePresence...)
	return append(out, classLines...)
}

// activityLineComposer turns (mood, app identity) into one line. It exists as
// a value with an injected seed and clock — rather than a bare function
// reaching for math/rand and time.Now — so the phrasing choice is a pure,
// table-testable function of its inputs, the way every other pure helper in
// this package is tested.
type activityLineComposer struct {
	// seed perturbs the phrasing choice. The default is 0 on purpose: with a
	// fixed seed the line is a pure function of (app id, mood, clock
	// bucket), so a screenshot taken at a known time is reproducible — this
	// project relies on that elsewhere (screenLines is server-owned
	// specifically to stay "deterministic and screenshot-reproducible").
	// Variety comes from the app and the clock, not from process-level
	// randomness, and this file therefore imports no math/rand at all.
	seed uint64
	// now is the clock the reroll bucket is read from.
	now func() time.Time
	// rerollAfter is how long one phrasing sticks; 0 disables rerolling
	// entirely (the phrasing then depends only on the app and the mood).
	rerollAfter time.Duration
}

var defaultActivityLineComposer = &activityLineComposer{
	seed:        0,
	now:         time.Now,
	rerollAfter: activityLineRerollInterval,
}

func (c *activityLineComposer) line(mood engine.Mood, appID, appDisplay string) string {
	appType := activity.AppTypeOf(appID)

	// dexel's own window is treated exactly like "no app identity at all"
	// (activity.SelfAppID's doc comment has the reasoning, and
	// activity.AppTypeOf classifies SelfAppID via IsSelf): the mood is still
	// reported, but no claim is made about WHERE the typing happened,
	// because a keystroke can never have landed in dexel — it has no text
	// input. "Coding in dexel" was the observed bug this guards, and
	// TestActivityLineNeverClaimsYouTypedInDexel is its regression test.
	//
	// These two strings stay fixed rather than joining the pools: with no app
	// to name there is nothing left to vary except the mood itself, and the
	// mood is already on screen as the dot beside this line.
	if appID == "" || appType == activity.AppTypeSelf {
		if mood == engine.MoodCoding {
			return "Coding"
		}
		return "Working..."
	}

	display := appDisplay
	if display == "" {
		// An honest raw id beats a fabricated name — the same fallback
		// activity.FriendlyName makes, repeated here because a caller may
		// pass an id with no display name at all.
		display = appID
	}

	pool := activityLinePoolsByType[appType].poolFor(mood)
	if len(pool) == 0 {
		// Defensive: an app type with no pool at all. Presence is always
		// true, so fall back to it rather than inventing anything.
		pool = activityLinePresence
	}
	return c.pick(pool, appID, display)
}

// poolFor picks the tier the two signals license for this mood. The tiers are
// strictly ordered by how much they claim: work (the user was typing HERE) >
// atDesk (the user is present with this app) > always (this app is frontmost).
func (p activityLinePools) poolFor(mood engine.Mood) []string {
	if mood == engine.MoodCoding && len(p.work) > 0 {
		// Coding-class app plus a keystroke in the last few seconds: say so,
		// and say only that. The presence lines are not merged in here so
		// the true, informative reading is what the panel actually shows
		// while the user is working.
		return p.work
	}
	if mood != engine.MoodOnBreak {
		return append(append(make([]string, 0, len(p.always)+len(p.atDesk)), p.always...), p.atDesk...)
	}
	// OnBreak: the user is provably away, so only lines about the app itself
	// survive.
	return p.always
}

// pick chooses one rendered line from pool.
//
// THE STABILITY RULE, which matters as much as the honesty rule: the state
// broadcast runs at ~1 Hz (docs/ui-spec.md §6), so a phrasing that re-rolled
// per tick would flicker once a second and read as a broken widget. The
// choice is therefore a deterministic hash of
//
//	(seed, sanitized app id, floor(now / rerollAfter))
//
// which means it changes on exactly two events and no others: the frontmost
// app changes, or the reroll bucket advances (every
// activityLineRerollInterval = 45s of wall clock). Between those it is
// byte-identical however many times ActivityLine is called —
// TestActivityLineDoesNotChurnAtOneHertz ticks two simulated minutes and
// asserts it. Calling math/rand here, once per tick, is precisely the trap.
//
// The mood is deliberately NOT part of the hash: it selects the POOL (see
// poolFor), so a mood change can still change the line. That is a real state
// change the user is watching happen — the mood dot next to the line changes
// with it — not churn.
func (c *activityLineComposer) pick(pool []string, appID, display string) string {
	// Render first, then offer only what fits #status-line's 34 chars. A
	// pool entry too long for THIS app's display name is dropped rather than
	// clipped by the frontend; if nothing fits, the shortest rendering wins
	// (it is still true — being clipped is a display problem, not a lie).
	fits := make([]string, 0, len(pool))
	shortest := ""
	for _, tpl := range pool {
		line := strings.ReplaceAll(tpl, "{app}", display)
		if len(line) <= maxActivityLineLen {
			fits = append(fits, line)
		}
		if shortest == "" || len(line) < len(shortest) {
			shortest = line
		}
	}
	if len(fits) == 0 {
		return shortest
	}
	return fits[c.index(appID, len(fits))]
}

// index is the deterministic chooser behind the stability rule above.
func (c *activityLineComposer) index(appID string, n int) int {
	bucket := int64(0)
	if c.rerollAfter > 0 {
		// Nanoseconds rather than seconds so a sub-second rerollAfter (only
		// a test would set one) cannot divide by zero.
		bucket = c.now().UnixNano() / c.rerollAfter.Nanoseconds()
	}
	h := fnv.New64a()
	// Field-separated so ("ab", 1) and ("a", "b1") cannot collide into the
	// same phrasing by accident.
	h.Write([]byte(strconv.FormatUint(c.seed, 10)))
	h.Write([]byte{'|'})
	h.Write([]byte(appID))
	h.Write([]byte{'|'})
	h.Write([]byte(strconv.FormatInt(bucket, 10)))
	return int(h.Sum64() % uint64(n))
}
