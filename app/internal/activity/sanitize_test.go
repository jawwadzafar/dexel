package activity

import "testing"

func TestSanitizeAppID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"already clean", "code", "code"},
		{"spaces to dashes", "Visual Studio Code", "visual-studio-code"},
		{"mixed case", "Google Chrome", "google-chrome"},
		{"punctuation dropped", "zoom.us", "zoom.us"}, // '.' is allowed
		{"unicode dropped", "微信", ""},
		{"empty", "", ""},
		{"leading/trailing space collapsed", "  Finder  ", "finder"},
		{"very long input capped", stringOfLen(200, 'a'), stringOfLen(MaxAppIDLen, 'a')},
		{"newline and control chars dropped", "code\n\t\x00", "code"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SanitizeAppID(c.in)
			if got != c.want {
				t.Errorf("SanitizeAppID(%q) = %q, want %q", c.in, got, c.want)
			}
			if len(got) > MaxAppIDLen {
				t.Errorf("SanitizeAppID(%q) exceeded MaxAppIDLen: %q (%d bytes)", c.in, got, len(got))
			}
			for _, ch := range got {
				ok := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-'
				if !ok {
					t.Errorf("SanitizeAppID(%q) produced disallowed char %q in %q", c.in, ch, got)
				}
			}
		})
	}
}

func TestFriendlyNameFallsBackToID(t *testing.T) {
	if got := FriendlyName("code"); got != "VS Code" {
		t.Errorf("FriendlyName(code) = %q, want %q", got, "VS Code")
	}
	if got := FriendlyName("some-unknown-app"); got != "some-unknown-app" {
		t.Errorf("FriendlyName(unknown) = %q, want the id unchanged", got)
	}
}

func stringOfLen(n int, ch byte) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ch
	}
	return string(b)
}

// TestAppTypeAndFriendlyNamesDoNotDrift is the reason appTypes lives in this
// file at all. The two maps are keyed by the same sanitized app id and answer
// the same question about it — what is this app, and what do we call it — so
// the only realistic failure mode is someone adding an app to one and
// forgetting the other. Asserting the key sets are IDENTICAL turns that into
// a test failure instead of either a classified app rendering as a bare
// lowercased id ("Coding in goland" -> "goland"), or a nicely named app
// silently falling into AppTypeUnknown and losing its phrasing.
func TestAppTypeAndFriendlyNamesDoNotDrift(t *testing.T) {
	for id := range appTypes {
		if _, ok := friendlyNames[id]; !ok {
			t.Errorf("appTypes[%q] has no friendlyNames entry — a classified app with no display name renders as a bare id", id)
		}
	}
	for id := range friendlyNames {
		if _, ok := appTypes[id]; !ok {
			t.Errorf("friendlyNames[%q] has no appTypes entry — a named app with no class silently becomes AppTypeUnknown", id)
		}
	}
	// SelfAppID must be in NEITHER map: AppTypeOf answers it directly, and a
	// row for it would be a way to reclassify the one id dexel may never
	// narrate (see SelfAppID's doc comment).
	if _, ok := appTypes[SelfAppID]; ok {
		t.Errorf("appTypes has a row for SelfAppID (%q) — dexel's own window is classified by AppTypeOf, deliberately not by the table", SelfAppID)
	}
	if _, ok := friendlyNames[SelfAppID]; ok {
		t.Errorf("friendlyNames has a row for SelfAppID (%q) — the activity line must never have a display name to print for dexel", SelfAppID)
	}
}

// TestAppTypeOf pins the classification itself: at least one id per bucket,
// the aliases that exist because the OS-reported name differs by platform or
// channel, and the two honest non-answers.
func TestAppTypeOf(t *testing.T) {
	cases := []struct {
		id   string
		want AppType
	}{
		// Editors/IDEs and terminals — the only coding-class types, i.e. the
		// only ones where internal/game may claim a keystroke-derived verb.
		{"code", AppTypeCoding},
		{"visual-studio-code", AppTypeCoding},
		{"cursor", AppTypeCoding},
		{"goland", AppTypeCoding},
		{"xcode", AppTypeCoding},
		{"terminal", AppTypeTerminal},
		{"iterm2", AppTypeTerminal},
		{"ghostty", AppTypeTerminal},
		// Browsers. "brave-browser" is what macOS reports for Brave, and is
		// the id behind the "Coding in Brave" bug.
		{"brave-browser", AppTypeBrowser},
		{"google-chrome", AppTypeBrowser},
		{"safari", AppTypeBrowser},
		{"firefox", AppTypeBrowser},
		{"arc", AppTypeBrowser},
		// The rest.
		{"slack", AppTypeComms},
		{"zoom.us", AppTypeComms},
		{"figma", AppTypeDesign},
		{"spotify", AppTypeMedia},
		{"notion", AppTypeNotes},
		{"finder", AppTypeFiles},
		// dexel itself — its own class, never a table row.
		{SelfAppID, AppTypeSelf},
		// The honest non-answers.
		{"", AppTypeUnknown},
		{"some-app-nobody-classified", AppTypeUnknown},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			if got := AppTypeOf(c.id); got != c.want {
				t.Errorf("AppTypeOf(%q) = %q, want %q", c.id, got, c.want)
			}
		})
	}
}

// TestAppTypeIsNeverGuessedFromSubstrings is the regression test for the
// classifier this table replaced. internal/game used to ask
// `strings.Contains(id, "arc")`, `...(id, "browser")`, `...(id, "kitty")` and
// friends, which sorts an unrecognized app into whichever bucket its letters
// happen to hit — a guess presented to the user as a fact. Every id below is
// one a substring matcher would have mis-sorted, and every one must come back
// AppTypeUnknown: an app we do not know is an app we do not describe.
func TestAppTypeIsNeverGuessedFromSubstrings(t *testing.T) {
	for _, id := range []string{
		"arcade",            // contains "arc"
		"my-file-browser",   // contains "browser"
		"hello-kitty",       // contains "kitty"
		"terminal-emulator", // contains "terminal", but is not one we know
		"chrome-remote",     // contains "chrome"
		"bravery",           // contains "brave"
		"code-review-tool",  // contains "code"
		"warpaint",          // contains "warp"
	} {
		t.Run(id, func(t *testing.T) {
			if got := AppTypeOf(id); got != AppTypeUnknown {
				t.Errorf("AppTypeOf(%q) = %q, want %q — classification is an explicit table, never a substring guess", id, got, AppTypeUnknown)
			}
		})
	}
}

// TestAppTypeValuesAreSaneIdentifiers keeps the closed set closed and
// content-free: every AppType is a short lowercase identifier, so a type can
// never be a place a sentence (or anything observed) hides.
func TestAppTypeValuesAreSaneIdentifiers(t *testing.T) {
	all := []AppType{
		AppTypeUnknown, AppTypeCoding, AppTypeTerminal, AppTypeBrowser,
		AppTypeComms, AppTypeDesign, AppTypeMedia, AppTypeNotes,
		AppTypeFiles, AppTypeSelf,
	}
	seen := map[AppType]bool{}
	for _, t2 := range all {
		if seen[t2] {
			t.Errorf("AppType %q is duplicated in the closed set", t2)
		}
		seen[t2] = true
		if len(t2) == 0 || len(t2) > 12 {
			t.Errorf("AppType %q is not a short identifier (%d bytes)", t2, len(t2))
		}
		for _, ch := range string(t2) {
			if ch < 'a' || ch > 'z' {
				t.Errorf("AppType %q contains %q — values must be plain lowercase identifiers", t2, ch)
			}
		}
	}
	// Every value the table can produce must be in that closed set.
	for id, typ := range appTypes {
		if !seen[typ] {
			t.Errorf("appTypes[%q] = %q, which is not one of the declared AppType constants", id, typ)
		}
	}
}
