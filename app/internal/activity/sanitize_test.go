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
