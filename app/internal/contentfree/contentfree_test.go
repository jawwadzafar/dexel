package contentfree

import (
	"reflect"
	"strings"
	"testing"
)

// The synthetic types below exist ONLY to exercise Audit's own logic in
// isolation from any production package. They deliberately mirror the
// shapes real regressions would take: a struct nested two levels deep
// that nobody remembered to register, a forbidden-looking field name
// that IS on the allow-list (proving the scan still fires), a rotted
// allow-list entry, and a cited exception.

type leafOK struct {
	Count uint64
}

type middleOK struct {
	Leaf leafOK
	Tag  string
}

type rootOK struct {
	Middle middleOK
	Items  []middleOK
}

func okRegistry() Registry {
	return Registry{
		"contentfree.rootOK": {
			Sample:  rootOK{},
			Allowed: map[string]string{"Middle": "contentfree.middleOK", "Items": "[]contentfree.middleOK"},
		},
		"contentfree.middleOK": {
			Sample:  middleOK{},
			Allowed: map[string]string{"Leaf": "contentfree.leafOK", "Tag": "string"},
		},
		"contentfree.leafOK": {
			Sample:  leafOK{},
			Allowed: map[string]string{"Count": "uint64"},
		},
	}
}

func TestAuditPassesOnAFullyRegisteredCleanGraph(t *testing.T) {
	reg := okRegistry()
	got := Audit([]reflect.Type{reflect.TypeOf(rootOK{})}, reg, DefaultForbidden)
	if len(got) != 0 {
		t.Fatalf("expected a clean graph to pass, got violations: %v", got)
	}
}

// TestAuditCatchesAnUnregisteredNestedType is this package's proof of
// the actual recursion win over the incumbent design: a type reachable
// two levels down (rootOK -> middleOK -> deepUnregistered) that nobody
// added a registry entry for must fail, with no manual checkExact call
// required to have existed for it to be caught.
type deepUnregistered struct {
	Value string
}

type middleWithUnregisteredChild struct {
	Leaf   leafOK
	Hidden deepUnregistered
}

func TestAuditCatchesAnUnregisteredNestedType(t *testing.T) {
	reg := Registry{
		"contentfree.middleWithUnregisteredChild": {
			Sample:  middleWithUnregisteredChild{},
			Allowed: map[string]string{"Leaf": "contentfree.leafOK", "Hidden": "contentfree.deepUnregistered"},
		},
		"contentfree.leafOK": okRegistry()["contentfree.leafOK"],
		// deepUnregistered is deliberately absent.
	}

	got := Audit([]reflect.Type{reflect.TypeOf(middleWithUnregisteredChild{})}, reg, DefaultForbidden)
	if !anyContains(got, "contentfree.deepUnregistered is reachable") {
		t.Fatalf("expected a violation naming the unregistered nested type, got: %v", got)
	}
}

// TestAuditFlagsAForbiddenFieldNameEvenWhenAllowListed proves the
// forbidden-substring scan fires independent of the allow-list: a field
// named WindowTitle that IS correctly counted and typed on the allow-list
// must still fail, because its name alone is the violation.
type withBadFieldName struct {
	Count       uint64
	WindowTitle string
}

func TestAuditFlagsAForbiddenFieldNameEvenWhenAllowListed(t *testing.T) {
	reg := Registry{
		"contentfree.withBadFieldName": {
			Sample:  withBadFieldName{},
			Allowed: map[string]string{"Count": "uint64", "WindowTitle": "string"}, // deliberately allow-listed anyway
		},
	}
	got := Audit([]reflect.Type{reflect.TypeOf(withBadFieldName{})}, reg, DefaultForbidden)
	if !anyContains(got, `forbidden substring "title"`) {
		t.Fatalf("expected the forbidden-substring scan to fire on an allow-listed WindowTitle field, got: %v", got)
	}
}

// TestAuditCatchesARemovedAllowListEntry is the rot-detection mutation:
// dropping one field from the allow-list without touching the real type
// must fail via the exact-count check, with no recursion needed to catch
// it.
func TestAuditCatchesARemovedAllowListEntry(t *testing.T) {
	reg := Registry{
		"contentfree.leafOK": {
			Sample:  leafOK{},
			Allowed: map[string]string{}, // Count dropped
		},
	}
	got := Audit([]reflect.Type{reflect.TypeOf(leafOK{})}, reg, DefaultForbidden)
	if !anyContains(got, "expected exactly 0") {
		t.Fatalf("expected an exact-count violation after dropping an allow-list entry, got: %v", got)
	}
}

// TestAuditCatchesARenamedFieldOrphaningItsAllowListEntry covers the
// OTHER rot direction: the allow-list still has the same number of
// entries as the type has fields, but one entry no longer names a real
// field (a rename on the production side with no matching test-side
// edit) — a case the exact-count check alone would miss.
type renamed struct {
	CountRenamed uint64
}

func TestAuditCatchesARenamedFieldOrphaningItsAllowListEntry(t *testing.T) {
	reg := Registry{
		"contentfree.renamed": {
			Sample:  renamed{},
			Allowed: map[string]string{"Count": "uint64"}, // stale name, count still matches
		},
	}
	got := Audit([]reflect.Type{reflect.TypeOf(renamed{})}, reg, DefaultForbidden)
	if !anyContains(got, `allow-list entry "Count" does not correspond to any real field`) {
		t.Fatalf("expected a rot violation for the orphaned allow-list entry, got: %v", got)
	}
}

// TestAuditRequiresACitedException proves an exception with no reason is
// itself a violation — an uncited carve-out is indistinguishable from a
// silently weakened scan.
type withUncitedException struct {
	Name string
}

func TestAuditRequiresACitedException(t *testing.T) {
	reg := Registry{
		"contentfree.withUncitedException": {
			Sample:     withUncitedException{},
			Allowed:    map[string]string{"Name": "string"},
			Exceptions: map[string]string{"Name": ""},
		},
	}
	got := Audit([]reflect.Type{reflect.TypeOf(withUncitedException{})}, reg, DefaultForbidden)
	if !anyContains(got, "has no citation") {
		t.Fatalf("expected an uncited exception to be flagged, got: %v", got)
	}
	if !anyContains(got, `forbidden substring "name"`) {
		t.Fatalf("expected the uncited exception to NOT suppress the forbidden-substring scan, got: %v", got)
	}
}

// TestAuditHonorsAProperlyCitedException is the positive mirror: the
// identical shape, but with a real citation, must produce zero
// violations — exceptions work, they are just not free.
type withCitedException struct {
	Name string
}

func TestAuditHonorsAProperlyCitedException(t *testing.T) {
	reg := Registry{
		"contentfree.withCitedException": {
			Sample:     withCitedException{},
			Allowed:    map[string]string{"Name": "string"},
			Exceptions: map[string]string{"Name": "ADR 0014: user-authored config category, not observed content"},
		},
	}
	got := Audit([]reflect.Type{reflect.TypeOf(withCitedException{})}, reg, DefaultForbidden)
	if len(got) != 0 {
		t.Fatalf("expected a properly cited exception to pass clean, got: %v", got)
	}
}

// staleUnused is registered below but never referenced by any type in
// the declared root graph — the dead-allow-list-entry case.
type staleUnused struct {
	Count uint64
}

// TestAuditCatchesAStaleRegistryEntry covers registry-side rot: a type
// registered but no longer reachable from any declared root (e.g. the
// root type stopped referencing it, or the entry was never reachable to
// begin with) must be flagged rather than silently tolerated as dead
// weight nobody notices — otherwise a rot check that only ran in one
// direction would itself be a hole.
func TestAuditCatchesAStaleRegistryEntry(t *testing.T) {
	reg := okRegistry()
	reg["contentfree.staleUnused"] = TypeSpec{
		Sample:  staleUnused{},
		Allowed: map[string]string{"Count": "uint64"},
	}
	got := Audit([]reflect.Type{reflect.TypeOf(rootOK{})}, reg, DefaultForbidden)
	if !anyContains(got, "contentfree.staleUnused is registered") {
		t.Fatalf("expected a stale-entry violation for staleUnused, got: %v", got)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one violation (the stale entry), got %d: %v", len(got), got)
	}
}

func anyContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
