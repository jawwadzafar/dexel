package game

import (
	"strings"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/activity"
	"github.com/jawwadzafar/dexel/app/internal/engine"
)

// TestActivityLineNeverClaimsYouTypedInDexel is the regression test for a
// line a user actually saw in the shipping app: **"Coding in dexel"**.
//
// It is built from two individually-true signals that must not be joined
// this way. `Coding` means "a key went down SOMEWHERE in the last 10s" — the
// macOS provider reads a global HID timer (ADR 0011), it cannot see which
// app received the keystroke. `ActiveApp` means "the frontmost app RIGHT
// NOW". So clicking dexel's window within 10 seconds of typing in your
// editor produced a sentence asserting you had been typing into dexel — a
// window with no text input, where not one character has ever been entered.
//
// Same family as the lie ADR 0010 already forbids ("On break because you
// minimized me"): a claim the data cannot support, made anyway because two
// numbers happened to line up.
func TestActivityLineNeverClaimsYouTypedInDexel(t *testing.T) {
	for _, mood := range []engine.Mood{engine.MoodCoding, engine.MoodIdle, engine.MoodOnBreak} {
		t.Run(string(mood), func(t *testing.T) {
			got := ActivityLine(mood, activity.SelfAppID, "dexel")
			if strings.Contains(strings.ToLower(got), "dexel") {
				t.Errorf("ActivityLine(%s, self) = %q — dexel must never name ITSELF as the app you were working in", mood, got)
			}
			// The mood itself is still reported honestly; only the
			// attribution is dropped.
			want := "Working..."
			if mood == engine.MoodCoding {
				want = "Coding"
			}
			if got != want {
				t.Errorf("ActivityLine(%s, self) = %q, want %q — the mood stays, the WHERE goes", mood, got, want)
			}
		})
	}
}

// TestActivityLineStillAttributesRealApps pins the other half: this is a
// narrow fix about dexel's claims about itself, NOT a retreat from naming
// apps. You really can type in an editor, a terminal or a browser, so those
// readings stay exactly as ADR 0009 specified them.
func TestActivityLineStillAttributesRealApps(t *testing.T) {
	cases := []struct {
		mood     engine.Mood
		id, disp string
		want     string
	}{
		{engine.MoodCoding, "code", "VS Code", "Coding in VS Code"},
		{engine.MoodIdle, "chrome", "Chrome", "Browsing in Chrome"},
		{engine.MoodCoding, "iterm2", "iTerm", "In the terminal"},
		{engine.MoodIdle, "figma", "Figma", "In Figma"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			if got := ActivityLine(tc.mood, tc.id, tc.disp); got != tc.want {
				t.Errorf("ActivityLine(%s, %q) = %q, want %q", tc.mood, tc.id, got, tc.want)
			}
		})
	}
}
