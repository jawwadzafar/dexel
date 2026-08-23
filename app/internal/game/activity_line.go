package game

import (
	"strings"

	"github.com/jawwadzafar/dexel/app/internal/activity"
	"github.com/jawwadzafar/dexel/app/internal/engine"
)

// ActivityLine composes ADR 0009's verb map — "Coding in X / Browsing in X
// / In the terminal / In X / Working..." — from the engine's honest mood
// plus the sanitized app identity. This is the REAL, truthful activity
// line; it is separate from the game-flavor TickerLines (v0.4 plan
// non-negotiable #5: the ticker is fiction, this line is not).
func ActivityLine(mood engine.Mood, appID, appDisplay string) string {
	switch {
	// dexel's own window is treated exactly like "no app identity at all"
	// (activity.SelfAppID's doc comment has the reasoning): the mood is
	// still reported, but no claim is made about WHERE the typing happened,
	// because a keystroke can never have landed in dexel. "Coding in dexel"
	// was the observed bug this guards.
	case appID == "" || activity.IsSelf(appID):
		if mood == engine.MoodCoding {
			return "Coding"
		}
		return "Working..."
	case isTerminalApp(appID):
		return "In the terminal"
	case mood == engine.MoodCoding:
		return "Coding in " + appDisplay
	case isBrowserApp(appID):
		return "Browsing in " + appDisplay
	default:
		return "In " + appDisplay
	}
}

// isTerminalApp/isBrowserApp are small, coarse classifiers over the
// sanitized app id — display language only, NOT economy weighting (ADR
// 0010 explicitly defers per-app-class work weighting; this file never
// touches work units).
func isTerminalApp(id string) bool {
	return containsAny(id, "terminal", "iterm", "alacritty", "kitty", "warp", "wezterm")
}

func isBrowserApp(id string) bool {
	return containsAny(id, "chrome", "firefox", "safari", "edge", "brave", "arc", "browser")
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
