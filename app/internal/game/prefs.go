// prefs.go implements SET-1's user preferences (docs/ui-spec.md §11 "The
// Settings modal", §6.2's `SET_PREF`): the small, extensible set of
// on/off choices the user makes about their own dexel.
//
// It is a sibling of identity.go, and deliberately so — a preference is
// the same CATEGORY of data as the dexel's name (ADR 0014: "data the
// user deliberately writes about their own pet, not surveillance of
// their work"), it rides the same unsigned, hand-editable config.json,
// and it keeps the same boundaries:
//
//   - This package stays pure. Prefs are held in memory and echoed onto
//     the wire by State(); the server (app/main.go) owns loading them at
//     boot and writing them back through store.SaveConfig, exactly as it
//     does for the name.
//   - internal/store.Snapshot never learns about them, and SaveData never
//     grows a field for them. A preference is not protected economy
//     state, and hiding it behind the MAC would only make the user's own
//     settings tamper-evident, which is nonsense.
//   - Every key is validated HERE, against the allow-list below, before
//     anything is stored (docs/ui-spec.md §6.2: "the server validates
//     everything"). An unknown key is an ordinary error with no state
//     change, never a silently-created field.
//
// WHY ONE `SET_PREF {key, value}` ACTION RATHER THAN ONE ACTION PER
// PREFERENCE: preferences are the part of a UI that grows fastest and
// matters least individually. A per-pref action would mean a new wire
// literal, a new applyAction case and a new frontend call for every
// checkbox, which is how a contract accumulates dead weight. A single
// keyed action with a server-side allow-list keeps the wire flat while
// keeping the validation strict — the client cannot invent a preference,
// because a key this file does not know is rejected.
package game

import (
	"errors"
	"fmt"
	"sort"
)

// The pref keys, as they appear on the wire (docs/ui-spec.md §11.4).
// camelCase, matching every other wire identifier (§6.2's design call)
// AND matching the config.json field names one-for-one, so a user reading
// their own config.json is looking at the same words the UI sends.
//
// Named constants rather than bare strings for the same reason
// actionSetName is: the action handler, the allow-list and the tests must
// not be able to drift apart on a typo.
const (
	PrefAlwaysOnTop  = "alwaysOnTop"
	PrefShowAwayTime = "showAwayTime"
)

// ErrUnknownPref is SET_PREF's rejection for a key that is not on the
// allow-list. Wrapped with the offending key by SetPref so the flash text
// says which one, and so a caller can still errors.Is() it.
var ErrUnknownPref = errors.New("unknown preference")

// prefTargets is THE allow-list: the complete set of preferences a client
// may set, each mapped to the field it writes. Adding a preference is one
// line here plus one field on Game and store.ConfigData — and a key that
// is NOT here cannot be set at all, which is what makes "the client
// cannot invent a preference" structural rather than a promise.
//
// Every value is a *bool today. That is a deliberate, stated limit rather
// than an accident: the wire carries `value` as a bool (see
// app/hub.go's actionMessage), so a future non-boolean preference — a
// theme name, a number — is a WIRE decision to make at that point, not
// something to smuggle in by widening this map first.
func (g *Game) prefTargets() map[string]*bool {
	return map[string]*bool{
		PrefAlwaysOnTop:  &g.prefAlwaysOnTop,
		PrefShowAwayTime: &g.prefShowAwayTime,
	}
}

// PrefKeys returns every settable preference key, sorted — the honest
// answer to "what may I set?", used in SetPref's error text (so a
// rejection tells the user what WOULD have worked) and by the tests that
// pin the allow-list against ConfigView/ConfigData.
func PrefKeys() []string {
	var g Game
	keys := make([]string, 0, len(g.prefTargets()))
	for k := range g.prefTargets() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SetPref applies one preference and reports whether anything actually
// changed. An unknown key changes NOTHING and returns an error
// (docs/ui-spec.md §6.2: "never a panic and never a partial write").
//
// Setting a preference to the value it already holds reports
// mutated=false, which is what makes a repeated SET_PREF a genuine no-op
// all the way down — no broadcast and, in the server's action loop, no
// second config write. Same idempotence contract as SetPaused.
func (g *Game) SetPref(key string, value bool) (mutated bool, err error) {
	target, ok := g.prefTargets()[key]
	if !ok {
		return false, fmt.Errorf("%w %q (settable: %v)", ErrUnknownPref, key, PrefKeys())
	}
	if *target == value {
		return false, nil
	}
	*target = value
	return true, nil
}

// AlwaysOnTop reports the window-pinning preference. Read by the server
// to persist it and echoed on the wire by State(); the only thing that
// ACTS on it is the desktop shell, which reads it out of
// `dexel status --json`'s `prefs` block (app/cmd_lifecycle.go).
func (g *Game) AlwaysOnTop() bool { return g.prefAlwaysOnTop }

// ShowAwayTime reports the away-time DISPLAY preference. Nothing in this
// package branches on it: away seconds are recorded and sent either way
// (see ConfigView's doc comment). It exists to be persisted and to reach
// the frontend, which is where "show or hide" is decided.
func (g *Game) ShowAwayTime() bool { return g.prefShowAwayTime }

// RestorePrefs seeds both preferences from config.json at boot. Like
// RestoreConfigName it is boot-only and takes whatever the file said: a
// bool cannot be malformed the way a name can (encoding/json already
// degraded a non-boolean JSON value to the zero value by refusing the
// whole file — store.LoadConfig's "a malformed config.json yields
// defaults" contract), so there is nothing to validate here and nothing a
// hand edit can do beyond choosing true or false.
func (g *Game) RestorePrefs(alwaysOnTop, showAwayTime bool) {
	g.prefAlwaysOnTop = alwaysOnTop
	g.prefShowAwayTime = showAwayTime
}
