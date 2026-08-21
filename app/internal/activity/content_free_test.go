package activity

import (
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
	allowed := map[string]string{
		"KeystrokeCount":   "uint64",
		"MouseActive":      "bool",
		"IdleSeconds":      "float64",
		"ActiveApp":        "string",
		"ActiveAppDisplay": "string",
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
