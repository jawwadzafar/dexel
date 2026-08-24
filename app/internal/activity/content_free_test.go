package activity

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/contentfree"
)

// contentFreeRegistry is activity's guarded root graph. Snapshot is
// currently flat (no nested struct fields), but the registry is still
// walked through internal/contentfree.Audit rather than hand-rolled
// reflection, so the day a Snapshot field grows a nested struct, that
// struct is discovered and required to be registered here automatically
// — see internal/contentfree's package doc for why that recursion is the
// whole point (dev_docs/rust-port-evaluation.md §2.6).
func contentFreeRegistry() contentfree.Registry {
	return contentfree.Registry{
		// Each entry is a justification, not just a registration. In
		// field order:
		//   KeystrokeCount   — a count of presses, never which key.
		//   MouseActive      — a recency bool, never a position.
		//   IdleSeconds      — a duration.
		//   ActiveApp        — an APPLICATION identity, sanitized and
		//                      capped (SanitizeAppID), never a window
		//                      title/doc/URL.
		//   ActiveAppDisplay — a static table lookup on ActiveApp only.
		//   AppIdentityAvailable — a bool about the PROVIDER's capability
		//                      in this process context, carrying nothing
		//                      about the user or what they are doing.
		//                      Added (ADR 0019) because ActiveApp == ""
		//                      was overloaded to mean both "nothing is
		//                      frontmost" and "I cannot see apps from
		//                      here", which made a total capture failure
		//                      render identically to a real observation.
		"activity.Snapshot": {
			Sample: Snapshot{},
			Allowed: map[string]string{
				"KeystrokeCount":       "uint64",
				"MouseActive":          "bool",
				"IdleSeconds":          "float64",
				"ActiveApp":            "string",
				"ActiveAppDisplay":     "string",
				"AppIdentityAvailable": "bool",
			},
		},
	}
}

// TestSnapshotIsContentFree is the structural privacy test ADR 0002/0009
// require: Snapshot must only ever carry counts, booleans, durations, and
// sanitized/display app identifiers — never key identity, typed text,
// clipboard content, or window titles. It runs internal/contentfree's
// recursive Audit against Snapshot as the sole root of this package's
// guarded graph: any field not on the allow-list, any field whose name
// smells like content, or (as this package's own graph grows nested
// structs) any nested type nobody registered, fails this test rather
// than silently shipping a privacy regression.
func TestSnapshotIsContentFree(t *testing.T) {
	roots := []reflect.Type{reflect.TypeOf(Snapshot{})}
	for _, msg := range contentfree.Audit(roots, contentFreeRegistry(), contentfree.DefaultForbidden) {
		t.Error(msg)
	}
}

// TestContentFreeRejectsObservedContentFields proves the guard above
// still BITES: it exercises Audit itself against a synthetic type shaped
// like the mistakes someone would actually make (a field with a
// content-smelling name, allow-listed anyway), so the check cannot be
// satisfied by loosening the real registry.
func TestContentFreeRejectsObservedContentFields(t *testing.T) {
	type syntheticLeak struct {
		WindowTitle       string
		TypedText         string
		ClipboardContents string
		ActiveKeycode     string
		ActiveUrl         string
		DocumentPath      string
		LastCharTyped     string
		KeyName           string
	}
	reg := contentfree.Registry{
		"activity.syntheticLeak": {
			Sample: syntheticLeak{},
			Allowed: map[string]string{
				"WindowTitle": "string", "TypedText": "string", "ClipboardContents": "string",
				"ActiveKeycode": "string", "ActiveUrl": "string", "DocumentPath": "string",
				"LastCharTyped": "string", "KeyName": "string",
			},
		},
	}
	got := contentfree.Audit([]reflect.Type{reflect.TypeOf(syntheticLeak{})}, reg, contentfree.DefaultForbidden)
	if len(got) < 8 {
		t.Fatalf("expected the forbidden-substring scan to flag all 8 synthetic content-shaped fields even though they are allow-listed, got %d violations: %v", len(got), got)
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
