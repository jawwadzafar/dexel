// identity.go implements Phase P1 — Identity & first minutes
// (docs/plan/PRODUCT-EVOLUTION.md §5 "Phase P1", §2.9 "Onboarding /
// Identity"): the dexel's user-chosen name and the server-computed
// first-launch onboarding flag.
//
// The strict boundary this file exists to keep visible:
//
//   - The name is USER-AUTHORED CONFIG, not observed content. It rides
//     SEC-1's config/state split (ADR 0014): it lives in the unsigned,
//     hand-editable ~/.config/dexel/config.json, never in the protected,
//     HMAC'd SaveData at state.db. internal/store.Snapshot must never
//     learn about it, and SaveData must never grow a field for it — the
//     content-free allow-list on SaveData exists precisely to prove
//     state.db holds no free text.
//   - This package stays pure (no file I/O — see game.go's package doc).
//     The name is held here only as in-memory state that State() echoes
//     onto the wire; the server (app/main.go) owns loading it at boot and
//     writing it back through store.SaveConfig.
//   - onboarding is decided ONCE, server-side, at boot. A client can only
//     ever clear it (by naming the dexel), never set it.
package game

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxNameLen caps a dexel's name at 24 RUNES (not bytes). 24 is what the
// onboarding modal's 200px-wide 8px-font input can show without
// scrolling, and what the hamburger panel's 136px title line can render
// truncated — see docs/ui-spec.md §7. A longer submission is truncated,
// not rejected: a name is a warm first impression, not a form to fail.
const MaxNameLen = 24

// DefaultName is the name the onboarding modal's SKIP path applies (see
// docs/ui-spec.md §7.3). Skipping deliberately SETS a name rather than
// leaving the flag up, so onboarding can never nag a second time — the
// user is opting out of choosing, not opting into being asked again.
const DefaultName = "dexel"

// ErrEmptyName is SET_NAME's rejection when nothing survives
// NormalizeName's trim/strip — an empty name is a no-op, never a stored
// blank that would re-trigger onboarding on the next boot.
var ErrEmptyName = errors.New("name must not be empty")

// NormalizeName is SET_NAME's server-side validation, and the ONLY door
// a name enters the process through (docs/ui-spec.md §6.2: "the server
// validates everything"). In order:
//
//  1. every control character is DROPPED (not replaced) — this is the
//     one free-text field in the whole product, and a newline, a NUL, an
//     ANSI escape or a bidi override in it would reach a log line, the
//     titlebar and config.json;
//  2. surrounding whitespace is trimmed;
//  3. the result is truncated to MaxNameLen runes, counted with
//     utf8.RuneCountInString so a multi-byte name is measured the way it
//     is displayed, never cut mid-rune;
//  4. an empty result is ErrEmptyName.
//
// Deliberately NOT done: any allow-list of "acceptable" characters. A
// name the user writes about their own pet in their own language is
// theirs; the only things filtered are the ones that are not text.
func NormalizeName(raw string) (string, error) {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, raw)
	cleaned = strings.TrimSpace(cleaned)
	if utf8.RuneCountInString(cleaned) > MaxNameLen {
		runes := []rune(cleaned)
		cleaned = strings.TrimSpace(string(runes[:MaxNameLen]))
	}
	if cleaned == "" {
		return "", ErrEmptyName
	}
	return cleaned, nil
}

// ConfigName returns the dexel's current name ("" when unnamed). The
// server reads this after SetConfigName to know exactly what to persist
// to config.json — it writes back the NORMALIZED value, never the raw
// client payload.
func (g *Game) ConfigName() string { return g.configName }

// SetConfigName validates raw through NormalizeName, stores the result,
// and clears the onboarding flag for good. Returns the stored name so
// the caller can write that exact string through to config.json.
//
// On a validation failure NOTHING changes — not the name, not the
// onboarding flag (docs/ui-spec.md §6.2: "never a panic and never a
// partial write").
func (g *Game) SetConfigName(raw string) (string, error) {
	name, err := NormalizeName(raw)
	if err != nil {
		return "", err
	}
	g.configName = name
	g.onboarding = false
	return name, nil
}

// RestoreConfigName seeds the name loaded from config.json at boot
// WITHOUT touching the onboarding flag — the server decides that flag
// separately (SetOnboarding) from "did a save exist" plus this name, and
// must not have it silently cleared as a side effect of loading. A name
// that fails NormalizeName (a hand-edited config.json holding only
// whitespace or control characters) leaves the game unnamed, matching
// store.LoadConfig's own "a malformed config.json degrades to defaults
// and never blocks startup" contract.
func (g *Game) RestoreConfigName(raw string) {
	if name, err := NormalizeName(raw); err == nil {
		g.configName = name
	}
}

// Onboarding reports whether this process is in the first-launch state
// (see StateMessage.Onboarding).
func (g *Game) Onboarding() bool { return g.onboarding }

// SetOnboarding is the server's one-time, boot-only write of the
// first-launch flag. It is called from main.go with (no save existed at
// boot) && (config.json carries no name) — never from an action handler,
// and never in response to anything a client sends.
func (g *Game) SetOnboarding(v bool) { g.onboarding = v }
