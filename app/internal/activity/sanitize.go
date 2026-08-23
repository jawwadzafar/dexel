package activity

import "strings"

// MaxAppIDLen is the hard cap on a sanitized app identifier (ADR 0009:
// "sanitized ... length-capped").
const MaxAppIDLen = 32

// SanitizeAppID lowercases raw (an OS-reported application display name,
// e.g. NSWorkspace's localizedName), keeps only [a-z0-9._-], and caps the
// result at MaxAppIDLen bytes. This is the ONLY transform allowed between
// "what the OS told us" and "what leaves this package" for app identity —
// window titles, document names, and URLs never reach this function because
// callers never pass them in (ADR 0002/0009).
func SanitizeAppID(raw string) string {
	lower := strings.ToLower(raw)
	b := make([]byte, 0, len(lower))
	for i := 0; i < len(lower) && len(b) < MaxAppIDLen; i++ {
		c := lower[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			b = append(b, c)
		case c == ' ':
			// Common case: "Visual Studio Code" -> "visual-studio-code".
			// Collapse-friendly: never emit a leading/trailing/doubled '-'.
			if len(b) > 0 && b[len(b)-1] != '-' {
				b = append(b, '-')
			}
		default:
			// Every other byte (unicode, punctuation, control chars) is
			// simply dropped rather than mapped — dropping can never
			// reconstruct content, substitution rules can accumulate bugs.
		}
	}
	for len(b) > 0 && b[len(b)-1] == '-' {
		b = b[:len(b)-1]
	}
	return string(b)
}

// SelfAppID is dexel's OWN sanitized app id — what SanitizeAppID returns
// when the frontmost application is dexel's own window (the Tauri bundle's
// localizedName is its productName, "dexel").
//
// It exists because dexel must never narrate itself as the app you were
// working in. The keystroke signal is GLOBAL and instantaneous ("a key went
// down somewhere in the last 10s", ADR 0011's CGEventSource timer); the app
// identity is the frontmost app RIGHT NOW. Joining them into "Coding in X"
// is an inference, and for X = dexel it is a provably false one: dexel has
// no text input, so nobody has ever typed a character into it. Clicking your
// companion's window right after typing in your editor produced "Coding in
// dexel" — the ADR 0010 class of lie ("On break because you minimized me"),
// wearing a different hat.
//
// Deliberately NOT extended to browsers or chat apps: you really can type in
// those, so "Coding in Chrome" is an honest reading of the same two signals.
// This is only about dexel's claims about ITSELF.
const SelfAppID = "dexel"

// IsSelf reports whether a sanitized app id is dexel's own window.
func IsSelf(sanitizedID string) bool { return sanitizedID == SelfAppID }

// friendlyNames maps a sanitized app id to a human-friendly display name.
// Deliberately small and coarse (ADR 0009: "mapped to a small friendly-name
// table"); an id with no entry falls back to itself via FriendlyName.
var friendlyNames = map[string]string{
	"code":               "VS Code",
	"visual-studio-code": "VS Code",
	"cursor":             "Cursor",
	"xcode":              "Xcode",
	"android-studio":     "Android Studio",
	"intellij-idea":      "IntelliJ IDEA",
	"pycharm":            "PyCharm",
	"goland":             "GoLand",
	"terminal":           "Terminal",
	"iterm2":             "iTerm",
	"iterm":              "iTerm",
	"alacritty":          "Alacritty",
	"kitty":              "Kitty",
	"safari":             "Safari",
	"google-chrome":      "Chrome",
	"chrome":             "Chrome",
	"firefox":            "Firefox",
	"microsoft-edge":     "Edge",
	"brave-browser":      "Brave",
	"arc":                "Arc",
	"slack":              "Slack",
	"discord":            "Discord",
	"zoom.us":            "Zoom",
	"zoom":               "Zoom",
	"microsoft-teams":    "Teams",
	"facetime":           "FaceTime",
	"notion":             "Notion",
	"figma":              "Figma",
	"spotify":            "Spotify",
	"music":              "Music",
	"finder":             "Finder",
}

// FriendlyName returns a human-readable display name for a sanitized app
// id, or the id itself if no mapping exists (better an honest raw id than a
// fabricated one).
func FriendlyName(sanitizedID string) string {
	if name, ok := friendlyNames[sanitizedID]; ok {
		return name
	}
	return sanitizedID
}
