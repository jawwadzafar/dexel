package activity

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestSnapshotIsContentFree is the structural privacy test ADR 0002/0009
// require: Snapshot must only ever carry counts, booleans, durations, and
// sanitized/display app identifiers — never key identity, typed text,
// clipboard content, or window titles. This test enumerates every field by
// reflection against an explicit allow-list; adding a new field to
// Snapshot that isn't on the allow-list, or whose name suggests raw
// content, fails this test rather than silently shipping a privacy
// regression.
func TestSnapshotIsContentFree(t *testing.T) {
	// Each entry is a justification, not just a registration. In field order:
	//   KeystrokeCount   — a count of presses, never which key.
	//   MouseActive      — a recency bool, never a position.
	//   IdleSeconds      — a duration.
	//   ActiveApp        — an APPLICATION identity, sanitized and capped
	//                      (SanitizeAppID), never a window title/doc/URL.
	//   ActiveAppDisplay — a static table lookup on ActiveApp only.
	//   AppIdentityAvailable — a bool about the PROVIDER's capability in this
	//                      process context, carrying nothing about the user or
	//                      what they are doing. Added (ADR 0019) because
	//                      ActiveApp == "" was overloaded to mean both
	//                      "nothing is frontmost" and "I cannot see apps from
	//                      here", which made a total capture failure render
	//                      identically to a real observation. A capability bit
	//                      is the smallest thing that can separate them, and
	//                      it is strictly less revealing than the app identity
	//                      it qualifies.
	allowed := map[string]string{
		"KeystrokeCount":       "uint64",
		"MouseActive":          "bool",
		"IdleSeconds":          "float64",
		"ActiveApp":            "string",
		"ActiveAppDisplay":     "string",
		"AppIdentityAvailable": "bool",
	}

	// Field/type names whose presence anywhere on Snapshot is itself a
	// violation, independent of the allow-list above (belt and suspenders:
	// this catches a rename of an allowed field into something that smells
	// like content, e.g. "ActiveApp" -> "ActiveAppTitle").
	forbiddenSubstrings := []string{
		"title", "text", "content", "keycode", "key_code", "clipboard",
		"url", "path", "document", "message", "body", "keyname", "char",
	}

	typ := reflect.TypeOf(Snapshot{})
	if typ.NumField() != len(allowed) {
		t.Fatalf("Snapshot has %d fields, expected exactly %d (%v) — a field was added or removed without updating this privacy test",
			typ.NumField(), len(allowed), allowed)
	}

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		wantType, ok := allowed[f.Name]
		if !ok {
			t.Errorf("Snapshot.%s is not on the content-free allow-list — every field must be justified here", f.Name)
			continue
		}
		if f.Type.String() != wantType {
			t.Errorf("Snapshot.%s has type %s, want %s", f.Name, f.Type.String(), wantType)
		}

		lower := strings.ToLower(f.Name)
		for _, bad := range forbiddenSubstrings {
			if strings.Contains(lower, bad) {
				t.Errorf("Snapshot.%s name contains forbidden substring %q — looks like it could carry content", f.Name, bad)
			}
		}
	}
}

// TestFriendlyNamesCarryNoLongText guards the OTHER place app identity
// could accidentally grow into content: the friendly-name table. Display
// names should read like short app names, not sentences or paths.
func TestFriendlyNamesCarryNoLongText(t *testing.T) {
	const maxFriendlyLen = 40
	for id, name := range friendlyNames {
		if len(name) > maxFriendlyLen {
			t.Errorf("friendlyNames[%q] = %q is suspiciously long (%d bytes) for an app display name", id, name, len(name))
		}
		if strings.ContainsAny(name, "\n\t") {
			t.Errorf("friendlyNames[%q] = %q contains control characters", id, name)
		}
	}
}

// TestDarwinProviderNeverReadsWindowTitle is the structural guard for the one
// place on macOS where a window TITLE is a single dictionary key away.
//
// CGWindowListCopyWindowInfo hands back, for every on-screen window, both
// kCGWindowOwnerName (the owning APPLICATION's name — permissionless, and
// what ADR 0009 allows) and kCGWindowName (the window's TITLE — the document
// you have open, the URL of your tab; forbidden by ADR 0002/0009, and gated
// behind a Screen Recording TCC grant that ADR 0010 refuses to ask for).
// They are adjacent keys on the same dictionary, so "read the title too" is a
// three-character edit away from being correct-looking code.
//
// Snapshot's allow-list above stops a title from ever reaching the boundary
// as a FIELD, but it cannot stop one from being read and logged. This test
// closes that gap by asserting the read expression does not exist. It scans
// the source rather than running the provider deliberately: it must fail on a
// Linux CI box with no window server, where the darwin provider cannot be
// executed at all.
func TestDarwinProviderNeverReadsWindowTitle(t *testing.T) {
	src, err := os.ReadFile("provider_darwin.go")
	if err != nil {
		t.Fatalf("reading provider_darwin.go: %v (this test must run from the package dir)", err)
	}
	// The forbidden thing is the READ, not the identifier: the file discusses
	// kCGWindowName at length in the HARD BOUNDARY comment explaining why it
	// is never read, and that documentation must not be what breaks the test.
	forbidden := []string{
		"CFDictionaryGetValue(w, kCGWindowName)",
		"CFDictionaryGetValue(window, kCGWindowName)",
		"valueForKey:kCGWindowName",
	}
	text := string(src)
	for _, expr := range forbidden {
		if strings.Contains(text, expr) {
			t.Errorf("provider_darwin.go reads the window TITLE via %q — forbidden by ADR 0002/0009 (and it would require a Screen Recording grant, breaking ADR 0010's permissionless property). Read kCGWindowOwnerName only.", expr)
		}
	}
	// Sanity-check the test itself: if the provider stopped using
	// CGWindowList altogether the scan above would pass vacuously and stop
	// guarding anything.
	if !strings.Contains(text, "kCGWindowOwnerName") {
		t.Error("provider_darwin.go no longer mentions kCGWindowOwnerName — this title guard has gone vacuous; re-point it at whatever API replaced CGWindowList")
	}
}
