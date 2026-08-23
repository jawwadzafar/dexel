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
//
// Its key set is kept IDENTICAL to appTypes' (below) by sanitize_test.go's
// TestAppTypeAndFriendlyNamesDoNotDrift: every app dexel can classify also
// has a display name, and every app it can name is also classified. The two
// maps answer the same question about the same key, so they are maintained
// as one thing in two shapes rather than two lists that quietly diverge.
// Grouped in the same order as appTypes so a reviewer can diff them by eye.
var friendlyNames = map[string]string{
	// Editors and IDEs.
	"code":               "VS Code",
	"visual-studio-code": "VS Code",
	"code-insiders":      "VS Code Insiders",
	"cursor":             "Cursor",
	"vscodium":           "VSCodium",
	"windsurf":           "Windsurf",
	"zed":                "Zed",
	"xcode":              "Xcode",
	"android-studio":     "Android Studio",
	"intellij-idea":      "IntelliJ IDEA",
	"pycharm":            "PyCharm",
	"goland":             "GoLand",
	"webstorm":           "WebStorm",
	"phpstorm":           "PhpStorm",
	"rubymine":           "RubyMine",
	"clion":              "CLion",
	"rider":              "Rider",
	"datagrip":           "DataGrip",
	"sublime-text":       "Sublime Text",
	"emacs":              "Emacs",

	// Terminals.
	"terminal":  "Terminal",
	"iterm":     "iTerm",
	"iterm2":    "iTerm",
	"alacritty": "Alacritty",
	"kitty":     "Kitty",
	"warp":      "Warp",
	"wezterm":   "WezTerm",
	"ghostty":   "Ghostty",
	"hyper":     "Hyper",
	"tabby":     "Tabby",

	// Browsers.
	"safari":                    "Safari",
	"safari-technology-preview": "Safari Preview",
	"google-chrome":             "Chrome",
	"chrome":                    "Chrome",
	"chromium":                  "Chromium",
	"firefox":                   "Firefox",
	"firefox-developer-edition": "Firefox Dev",
	"microsoft-edge":            "Edge",
	"brave-browser":             "Brave",
	"brave":                     "Brave",
	"arc":                       "Arc",
	"vivaldi":                   "Vivaldi",
	"opera":                     "Opera",
	"orion":                     "Orion",

	// Chat, mail and meetings.
	"slack":           "Slack",
	"discord":         "Discord",
	"zoom.us":         "Zoom",
	"zoom":            "Zoom",
	"microsoft-teams": "Teams",
	"facetime":        "FaceTime",
	"messages":        "Messages",
	"mail":            "Mail",
	"telegram":        "Telegram",
	"whatsapp":        "WhatsApp",
	"signal":          "Signal",

	// Design and graphics.
	"figma":             "Figma",
	"sketch":            "Sketch",
	"aseprite":          "Aseprite",
	"gimp":              "GIMP",
	"inkscape":          "Inkscape",
	"blender":           "Blender",
	"pixelmator-pro":    "Pixelmator Pro",
	"adobe-photoshop":   "Photoshop",
	"adobe-illustrator": "Illustrator",
	"affinity-designer": "Affinity Designer",
	"affinity-photo":    "Affinity Photo",

	// Music and video.
	"spotify":          "Spotify",
	"music":            "Music",
	"quicktime-player": "QuickTime",
	"vlc":              "VLC",
	"iina":             "IINA",
	"podcasts":         "Podcasts",
	"photos":           "Photos",

	// Notes, docs and reading.
	"notion":               "Notion",
	"obsidian":             "Obsidian",
	"bear":                 "Bear",
	"notes":                "Notes",
	"craft":                "Craft",
	"logseq":               "Logseq",
	"textedit":             "TextEdit",
	"preview":              "Preview",
	"pages":                "Pages",
	"numbers":              "Numbers",
	"keynote":              "Keynote",
	"microsoft-word":       "Word",
	"microsoft-excel":      "Excel",
	"microsoft-powerpoint": "PowerPoint",

	// File managers.
	"finder": "Finder",
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

// AppType is a COARSE classification of what kind of application a sanitized
// app id names: an editor, a terminal, a browser, a chat window, ...
//
// It exists because the activity line was caught asserting something the
// signals cannot support. The line's old rule was "mood == Coding and the app
// is not a terminal -> 'Coding in ' + app", and the very first real macOS
// frontmost-app sample produced **"Coding in Brave"**. `Coding` only ever
// meant "a key went down SOMEWHERE in the last 10 seconds" — ADR 0011's
// provider reads a GLOBAL HID timer and cannot know which app received the
// keystroke — so joining it to "the frontmost app is a browser" invents a
// fact. The fix needs to know what KIND of app is in front (may a
// work-verb be claimed here at all?), which is precisely this type.
//
// Deliberately coarse and deliberately a TABLE, not a heuristic:
//
//   - Coarse, because every consumer only asks a coarse question. Nothing
//     downstream benefits from knowing Neovim-in-Kitty from Kitty; ADR 0009's
//     whole spirit is "a small friendly-name table", and this is its sibling.
//
//   - A table rather than the two substring predicates it replaces
//     (isTerminalApp/isBrowserApp in internal/game matched "brave", "arc",
//     "browser", "kitty", ... anywhere in the id), because substring matching
//     GUESSES. "arc" is a substring of any app whose name contains it,
//     "browser" appears in "brave-browser" and in a hypothetical
//     "file-browser", and an unrecognized app was silently sorted into
//     whichever bucket its letters happened to hit. A guess that lands wrong
//     is exactly the ADR 0010 class of lie this project keeps paying to
//     remove, so an id we do not know is AppTypeUnknown — a first-class,
//     honest answer, not a default bucket.
//
// It lives here, next to friendlyNames, rather than in internal/game with the
// phrasing that consumes it, for two reasons. (1) Both maps are keyed by the
// same sanitized app id and answer the same question — "what does this id
// mean?" — so keeping them apart invites exactly one bug: an app added to one
// and forgotten in the other. sanitize_test.go's TestAppTypeAndFriendlyNames
// DoNotDrift asserts the two key sets are IDENTICAL, which is only checkable
// with both in one package. (2) It is a fact about an app identity, not a
// presentation choice; how a type is WORDED is internal/game's business.
//
// Privacy: this adds no new observation. AppTypeOf's input is the already
// sanitized id (ADR 0009: an application identity, never a window title, URL
// or document name) and its output is one value from a closed set below.
// Nothing new crosses the activity boundary and no wire/save type gains a
// field — internal/game derives the type from the id it is already sent.
//
// NOT economy: like the predicates it replaces, this is display language
// only. ADR 0010 explicitly DEFERS per-app-class work weighting (its
// "code/terminal 1.0, meeting 0.35, browser 0.25, music 0" note), and no
// engine or coin code may key off this type until that decision is actually
// made.
type AppType string

const (
	// AppTypeUnknown is an app id with no entry in the table below: a real,
	// named app we simply have not classified. It is the honest answer and
	// the honest default — callers must fall back to phrasing that claims
	// nothing beyond "this app is in front" (see internal/game's presence
	// pool). It is also what an empty id classifies as.
	AppTypeUnknown AppType = "unknown"
	// AppTypeCoding is an editor or IDE. Together with AppTypeTerminal this
	// is the only class where a keystroke-derived WORK verb ("Coding in X")
	// may be claimed: a developer's keystrokes plausibly landed there.
	AppTypeCoding AppType = "coding"
	// AppTypeTerminal is a terminal emulator — a coding-class app for the
	// same reason (this is where the other half of the work happens), kept
	// separate because it has its own long-standing phrasing ("In the
	// terminal", ADR 0009 / docs/ui-spec.md §2.3).
	AppTypeTerminal AppType = "terminal"
	// AppTypeBrowser is a web browser. You can certainly type in one, but
	// the global keystroke timer cannot tell us that you did, so a browser
	// never earns a work verb — see internal/game/activity_line.go.
	AppTypeBrowser AppType = "browser"
	// AppTypeComms is chat, mail and meeting apps — talking to humans.
	AppTypeComms AppType = "comms"
	// AppTypeDesign is a design/graphics tool.
	AppTypeDesign AppType = "design"
	// AppTypeMedia is a music/video player.
	AppTypeMedia AppType = "media"
	// AppTypeNotes is a notes, docs or reading app.
	AppTypeNotes AppType = "notes"
	// AppTypeFiles is a file manager.
	AppTypeFiles AppType = "files"
	// AppTypeSelf is dexel's OWN window. It is not in the table (see
	// SelfAppID for the full reasoning): dexel has no text input, so no
	// keystroke has ever landed in it, and it must never narrate itself as
	// the app you were working in. Its own class exists so a caller cannot
	// accidentally treat dexel as just another unknown app and start naming
	// it.
	AppTypeSelf AppType = "self"
)

// appTypes classifies a sanitized app id (see SanitizeAppID) into one
// AppType. Its key set is kept IDENTICAL to friendlyNames' by
// TestAppTypeAndFriendlyNamesDoNotDrift — adding an app means adding it to
// both maps, which is the point: a classified app that has no display name
// renders as a bare lowercased id, and a named app with no class silently
// loses its phrasing.
//
// Ids are what SanitizeAppID makes of the OS-reported application name, so
// they are lowercase and space-separated words become dashes ("Visual Studio
// Code" -> "visual-studio-code"). Several apps appear twice on purpose: the
// OS name differs by platform, version or channel (Chrome reports "Google
// Chrome" on macOS and "Chrome" elsewhere; iTerm2's localizedName is
// "iTerm2" but "iTerm" is what some builds report).
var appTypes = map[string]AppType{
	// Editors and IDEs. Keystrokes plausibly land here.
	"code":               AppTypeCoding,
	"visual-studio-code": AppTypeCoding,
	"code-insiders":      AppTypeCoding,
	"cursor":             AppTypeCoding,
	"vscodium":           AppTypeCoding,
	"windsurf":           AppTypeCoding,
	"zed":                AppTypeCoding,
	"xcode":              AppTypeCoding,
	"android-studio":     AppTypeCoding,
	"intellij-idea":      AppTypeCoding,
	"pycharm":            AppTypeCoding,
	"goland":             AppTypeCoding,
	"webstorm":           AppTypeCoding,
	"phpstorm":           AppTypeCoding,
	"rubymine":           AppTypeCoding,
	"clion":              AppTypeCoding,
	"rider":              AppTypeCoding,
	"datagrip":           AppTypeCoding,
	"sublime-text":       AppTypeCoding,
	"emacs":              AppTypeCoding,

	// Terminals. Also coding-class: a shell is where the other half of the
	// work happens, and a terminal-hosted editor (vim, neovim, helix)
	// reports its TERMINAL's app name, never its own — so classifying
	// terminals as anything but coding-class would mis-describe a whole
	// style of working.
	"terminal":  AppTypeTerminal,
	"iterm":     AppTypeTerminal,
	"iterm2":    AppTypeTerminal,
	"alacritty": AppTypeTerminal,
	"kitty":     AppTypeTerminal,
	"warp":      AppTypeTerminal,
	"wezterm":   AppTypeTerminal,
	"ghostty":   AppTypeTerminal,
	"hyper":     AppTypeTerminal,
	"tabby":     AppTypeTerminal,

	// Browsers.
	"safari":                    AppTypeBrowser,
	"safari-technology-preview": AppTypeBrowser,
	"google-chrome":             AppTypeBrowser,
	"chrome":                    AppTypeBrowser,
	"chromium":                  AppTypeBrowser,
	"firefox":                   AppTypeBrowser,
	"firefox-developer-edition": AppTypeBrowser,
	"microsoft-edge":            AppTypeBrowser,
	"brave-browser":             AppTypeBrowser,
	"brave":                     AppTypeBrowser,
	"arc":                       AppTypeBrowser,
	"vivaldi":                   AppTypeBrowser,
	"opera":                     AppTypeBrowser,
	"orion":                     AppTypeBrowser,

	// Chat, mail and meetings.
	"slack":           AppTypeComms,
	"discord":         AppTypeComms,
	"zoom.us":         AppTypeComms,
	"zoom":            AppTypeComms,
	"microsoft-teams": AppTypeComms,
	"facetime":        AppTypeComms,
	"messages":        AppTypeComms,
	"mail":            AppTypeComms,
	"telegram":        AppTypeComms,
	"whatsapp":        AppTypeComms,
	"signal":          AppTypeComms,

	// Design and graphics.
	"figma":             AppTypeDesign,
	"sketch":            AppTypeDesign,
	"aseprite":          AppTypeDesign,
	"gimp":              AppTypeDesign,
	"inkscape":          AppTypeDesign,
	"blender":           AppTypeDesign,
	"pixelmator-pro":    AppTypeDesign,
	"adobe-photoshop":   AppTypeDesign,
	"adobe-illustrator": AppTypeDesign,
	"affinity-designer": AppTypeDesign,
	"affinity-photo":    AppTypeDesign,

	// Music and video.
	"spotify":          AppTypeMedia,
	"music":            AppTypeMedia,
	"quicktime-player": AppTypeMedia,
	"vlc":              AppTypeMedia,
	"iina":             AppTypeMedia,
	"podcasts":         AppTypeMedia,
	"photos":           AppTypeMedia,

	// Notes, docs and reading.
	"notion":               AppTypeNotes,
	"obsidian":             AppTypeNotes,
	"bear":                 AppTypeNotes,
	"notes":                AppTypeNotes,
	"craft":                AppTypeNotes,
	"logseq":               AppTypeNotes,
	"textedit":             AppTypeNotes,
	"preview":              AppTypeNotes,
	"pages":                AppTypeNotes,
	"numbers":              AppTypeNotes,
	"keynote":              AppTypeNotes,
	"microsoft-word":       AppTypeNotes,
	"microsoft-excel":      AppTypeNotes,
	"microsoft-powerpoint": AppTypeNotes,

	// File managers.
	"finder": AppTypeFiles,
}

// AppTypeOf classifies a sanitized app id. An id with no table entry is
// AppTypeUnknown — the honest answer, never a guessed bucket.
func AppTypeOf(sanitizedID string) AppType {
	if sanitizedID == "" {
		return AppTypeUnknown
	}
	// Checked before the table (and absent FROM the table) so the one id
	// dexel must never narrate cannot be reclassified by adding a row.
	if IsSelf(sanitizedID) {
		return AppTypeSelf
	}
	if t, ok := appTypes[sanitizedID]; ok {
		return t
	}
	return AppTypeUnknown
}
