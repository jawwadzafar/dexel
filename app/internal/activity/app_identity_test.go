package activity

import "testing"

// TestNewAppIdentityStates walks the full state table AppIdentity.Available
// documents. The point of the type is that these three states are
// distinguishable; a regression that flattens any two of them back together
// is exactly the bug ADR 0019 was written for, so each row asserts all three
// fields rather than just the one it is "about".
func TestNewAppIdentityStates(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		observable  bool
		wantID      string
		wantDisplay string
		wantAvail   bool
	}{
		{
			name:        "observed app maps through sanitize and the friendly table",
			raw:         "Visual Studio Code",
			observable:  true,
			wantID:      "visual-studio-code",
			wantDisplay: "VS Code",
			wantAvail:   true,
		},
		{
			name:        "observed app with no friendly-table entry keeps its honest raw id",
			raw:         "Some Internal Tool",
			observable:  true,
			wantID:      "some-internal-tool",
			wantDisplay: "some-internal-tool",
			wantAvail:   true,
		},
		{
			// The genuinely-nothing-frontmost case: a bare desktop, or every
			// window minimized. We looked, so the capability is available.
			name:       "observable but nothing frontmost is available with an empty id",
			raw:        "",
			observable: true,
			wantAvail:  true,
		},
		{
			// A name that sanitizes away entirely (all non-[a-z0-9._-]) is
			// still an ANSWER, not a capability failure.
			name:       "observable but unsanitizable name is available with an empty id",
			raw:        "日本語",
			observable: true,
			wantAvail:  true,
		},
		{
			// The failure this whole change exists for: no window server to
			// ask. Must NOT look like "nothing is frontmost".
			name:       "unobservable reports unavailable",
			raw:        "",
			observable: false,
			wantAvail:  false,
		},
		{
			// Defence in depth: even if a caller hands over a name alongside
			// observable=false (a confused platform layer), an unavailable
			// identity must never carry an app name downstream — otherwise
			// the "cannot see" state could still make a claim.
			name:       "unobservable discards any name handed to it",
			raw:        "Visual Studio Code",
			observable: false,
			wantAvail:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NewAppIdentity(tc.raw, tc.observable)
			if got.ID != tc.wantID {
				t.Errorf("ID = %q, want %q", got.ID, tc.wantID)
			}
			if got.Display != tc.wantDisplay {
				t.Errorf("Display = %q, want %q", got.Display, tc.wantDisplay)
			}
			if got.Available != tc.wantAvail {
				t.Errorf("Available = %v, want %v", got.Available, tc.wantAvail)
			}
		})
	}
}

// TestNewAppIdentityDisplayNeverOutlivesID pins the invariant the game layer
// leans on: Display is a pure function of ID, so it can never name an app
// that ID does not. (ActivityLine renders Display but branches on ID; a
// Display set while ID is empty would print an app name in a state the
// branch treats as "no app".)
func TestNewAppIdentityDisplayNeverOutlivesID(t *testing.T) {
	for _, raw := range []string{"", "   ", "!!!", "Finder", "Brave Browser"} {
		for _, observable := range []bool{true, false} {
			got := NewAppIdentity(raw, observable)
			if got.ID == "" && got.Display != "" {
				t.Errorf("NewAppIdentity(%q, %v) = %+v: Display set with an empty ID", raw, observable, got)
			}
			if got.ID != "" && got.Display == "" {
				t.Errorf("NewAppIdentity(%q, %v) = %+v: ID set with an empty Display", raw, observable, got)
			}
			if !got.Available && got.ID != "" {
				t.Errorf("NewAppIdentity(%q, %v) = %+v: unavailable identity carries an app id", raw, observable, got)
			}
		}
	}
}

// TestNewAppIdentityIsSelfAware confirms the self-transparency machinery
// still sees dexel's own window through the new path. macOS reports the
// bundle's application name as kCGWindowOwnerName, so "Dexel" must land on
// SelfAppID — if it didn't, dexel would start narrating itself as the app
// you were working in (see SelfAppID's doc comment).
func TestNewAppIdentityIsSelfAware(t *testing.T) {
	for _, raw := range []string{"Dexel", "dexel"} {
		got := NewAppIdentity(raw, true)
		if !IsSelf(got.ID) {
			t.Errorf("NewAppIdentity(%q, true).ID = %q, want it to satisfy IsSelf (== %q)", raw, got.ID, SelfAppID)
		}
	}
}
