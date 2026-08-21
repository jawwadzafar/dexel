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
