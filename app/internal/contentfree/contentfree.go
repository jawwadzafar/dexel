// Package contentfree is the recursive structural privacy walker that
// backs every content_free_test.go in this repo
// (app/internal/{activity,game,store}). It exists to close a specific,
// documented weakness in the incumbent per-struct allow-list tests
// (dev_docs/rust-port-evaluation.md §2.6): those tests enumerate a single
// type's fields against a hardcoded map, which is genuinely deny-by-
// default at that ONE type, but does not recurse — a content-capable
// field on a struct nested two levels down is uncaught unless a human
// remembered to add a second, manual checkExact call for that specific
// nested type. Rust's exhaustive destructuring + a recursive serde_json
// key walk closes that gap for free; this package is the ~150-line Go
// back-port the same document names as the cheap, honest fix.
//
// The design keeps the incumbent's per-type allow-list shape (a map of
// field name -> exact declared type string, which is what actually made
// the old tests deny-by-default) but stops relying on a human to
// remember which nested types need their own checkExact call. Instead:
//
//  1. Audit walks the REAL reflect.Type graph starting from a small set
//     of root types (Snapshot, StateMessage, SaveData, ConfigData — see
//     each package's content_free_test.go), unwrapping every pointer,
//     slice, array, and map it finds, and collects every named struct
//     type reachable anywhere in that graph — however many levels deep.
//  2. Every type the walk reaches MUST have a registered TypeSpec, or
//     Audit reports "reachable but unregistered" — this is the actual
//     recursion win: a brand-new nested type introduced anywhere in the
//     wire or save graph breaks the build until a human consciously
//     allow-lists it, because there is no longer a manual call site to
//     forget.
//  3. Every registered TypeSpec is validated on its own terms too,
//     independent of reachability: exact field count, exact per-field
//     type, and the forbidden-substring scan on every field name unless
//     an explicit, cited Exception applies. This is what makes "remove
//     an allow-list entry" and "rename a field without updating the
//     list" both fail even before reachability is considered — plain
//     allow-list rot, not a recursion problem.
//  4. Every registered TypeSpec must ALSO be reachable from the declared
//     roots, or Audit reports a stale entry — dead allow-list entries
//     rot just as quietly as missing ones.
//
// This package knows nothing about activity/game/store — it operates
// purely on reflect.Type and a caller-supplied Registry, so it stays a
// leaf dependency importable by all three packages' tests without any
// import-cycle risk.
package contentfree

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// TypeSpec is one guarded type's complete field allow-list.
type TypeSpec struct {
	// Sample is a zero-value instance of the guarded type, e.g.
	// game.SprintView{}. Audit reflects on it directly — this is the only
	// way this package ever learns about a caller's type, and it is why
	// this package never needs to import activity/game/store itself.
	Sample interface{}

	// Allowed maps field name -> the field's EXACT reflect.Type.String()
	// (e.g. "uint64", "*string", "game.SprintView",
	// "map[string]game.EquippedRef", "[]game.SessionView"). This is
	// exact-count: NumField() of Sample's type must equal len(Allowed),
	// which is what makes "a field was added or removed" a failure on
	// its own, with no recursion needed.
	Allowed map[string]string

	// Exceptions carries the small, explicit list of field names on THIS
	// type that are exempt from the forbidden-substring scan, each
	// mapped to a non-empty citation explaining why the name is safe
	// despite matching a forbidden pattern (e.g. an ADR reference). An
	// exception with no citation is itself a violation — see
	// validateType. This is deliberately per-(type,field), never global:
	// "Name" is fine on game.ConfigView (user-authored config, ADR 0014)
	// and must NOT be fine on store.SessionSave (which must never carry
	// one at all).
	Exceptions map[string]string
}

// Registry is the full set of guarded types for one package's audit,
// keyed by reflect.Type.String() (e.g. "game.StateMessage",
// "store.SaveData").
type Registry map[string]TypeSpec

// DefaultForbidden is the field-name substrings whose presence anywhere
// in the guarded graph is itself a privacy violation, independent of any
// allow-list — belt and suspenders against a rename of an allowed field
// into something that smells like content (e.g. "ActiveApp" ->
// "ActiveAppTitle"), and now checked on every reachable type, not just
// the ones a human remembered to enumerate.
//
// This is the incumbent list (activity/game/store's three
// content_free_test.go files, before this package existed) plus "name",
// promoted from a one-off addition store's session-save test made for
// itself to a global rule: a bare "name" field is exactly the shape a
// privacy regression (a project name, a document name, a contact name)
// would arrive as, and the couple of places a user-authored name IS
// legitimate (ADR 0014's config category) are handled as explicit,
// cited TypeSpec.Exceptions rather than by leaving the whole substring
// off the list.
var DefaultForbidden = []string{
	"title", "text", "content", "keycode", "key_code", "clipboard",
	"url", "path", "document", "message", "body", "keyname", "char",
	"name",
}

// Audit runs the full recursive structural privacy check: it discovers
// every named struct type reachable from roots, requires each to be
// registered in reg, requires every entry in reg to be reachable, and
// validates every registered TypeSpec's own field contract (count, type,
// forbidden-substring scan minus cited exceptions). It returns one
// human-readable violation string per problem found; a nil/empty result
// means the graph is exactly as guarded as reg claims.
//
// Callers should report every returned string (t.Error, not t.Fatal) so
// a single test run surfaces every problem in the graph at once, the
// same way the incumbent per-type tests already did.
func Audit(roots []reflect.Type, reg Registry, forbidden []string) []string {
	var violations []string

	reachable := discoverReachable(roots)

	for name := range reachable {
		if _, ok := reg[name]; !ok {
			violations = append(violations, fmt.Sprintf(
				"%s is reachable from the guarded root graph but has no contentfree.TypeSpec registered — "+
					"a new type slipped into the wire/save graph unguarded; add an explicit, justified allow-list entry for it",
				name))
		}
	}

	for name := range reg {
		if _, ok := reachable[name]; !ok {
			violations = append(violations, fmt.Sprintf(
				"%s is registered in the content-free allow-list but is not reachable from any declared root — "+
					"remove the stale entry or fix the declared roots (a dead allow-list entry rots just as quietly as a missing one)",
				name))
		}
	}

	for name, spec := range reg {
		violations = append(violations, validateType(name, spec, forbidden)...)
	}

	sort.Strings(violations)
	return violations
}

// validateType checks one registered TypeSpec against its own Sample via
// reflection, independent of whether it is currently reachable from any
// root. This is what makes rot (a removed allow-list entry, a renamed
// field, a retyped field) fail even before the recursive-reachability
// question is asked at all.
func validateType(name string, spec TypeSpec, forbidden []string) []string {
	var out []string

	if spec.Sample == nil {
		return []string{fmt.Sprintf("%s: TypeSpec.Sample is nil — registration bug, cannot validate", name)}
	}
	t := reflect.TypeOf(spec.Sample)
	if t.String() != name {
		out = append(out, fmt.Sprintf(
			"registry key %q does not match its Sample's reflect type %q — registration bug (fix the map key or the Sample)",
			name, t.String()))
	}

	if t.NumField() != len(spec.Allowed) {
		out = append(out, fmt.Sprintf(
			"%s has %d fields, expected exactly %d per its allow-list — a field was added or removed (or the allow-list rotted) without the other side being updated",
			name, t.NumField(), len(spec.Allowed)))
	}

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		wantType, ok := spec.Allowed[f.Name]
		if !ok {
			out = append(out, fmt.Sprintf("%s.%s is not on the content-free allow-list — every field must be justified", name, f.Name))
			continue
		}
		if f.Type.String() != wantType {
			out = append(out, fmt.Sprintf("%s.%s has type %s, want %s", name, f.Name, f.Type.String(), wantType))
		}

		if reason, exempt := spec.Exceptions[f.Name]; exempt && strings.TrimSpace(reason) != "" {
			continue
		}
		lower := strings.ToLower(f.Name)
		for _, bad := range forbidden {
			if strings.Contains(lower, bad) {
				out = append(out, fmt.Sprintf(
					"%s.%s name contains forbidden substring %q and is not on a properly cited exceptions list — looks like it could carry content",
					name, f.Name, bad))
			}
		}
	}

	// Rot in the other direction: an allow-list entry naming a field that
	// no longer exists on the real type (a rename or removal on the
	// production side that the test side never followed).
	for fname := range spec.Allowed {
		if _, ok := t.FieldByName(fname); !ok {
			out = append(out, fmt.Sprintf(
				"%s: allow-list entry %q does not correspond to any real field on the type (rot) — remove it or fix the rename",
				name, fname))
		}
	}

	// Every exception must point at an allow-listed field and carry an
	// actual citation — an exception is a deliberate, justified carve-out
	// (ADR 0014's config-category names), never a silent way to sneak a
	// forbidden-looking name past the scan.
	for fname, reason := range spec.Exceptions {
		if _, ok := spec.Allowed[fname]; !ok {
			out = append(out, fmt.Sprintf("%s: exception %q does not correspond to an allow-listed field", name, fname))
		}
		if strings.TrimSpace(reason) == "" {
			out = append(out, fmt.Sprintf("%s: exception %q has no citation — every exception must name the reason it is safe (e.g. an ADR)", name, fname))
		}
	}

	return out
}

// discoverReachable walks roots and every field reachable from them
// (through any nesting of pointers, slices, arrays, and maps) and
// returns every named STRUCT type it found, keyed by reflect.Type.String().
// This is the recursion: it does not stop at one level, and it does not
// need a human to have registered a checkExact call for whatever it
// finds — that is Audit's job, against this function's result.
func discoverReachable(roots []reflect.Type) map[string]reflect.Type {
	visited := make(map[string]reflect.Type)
	queue := append([]reflect.Type{}, roots...)

	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		if t == nil || t.Kind() != reflect.Struct {
			continue
		}
		name := t.String()
		if _, ok := visited[name]; ok {
			continue // already walked this type's fields once; revisiting buys nothing
		}
		visited[name] = t

		for i := 0; i < t.NumField(); i++ {
			queue = append(queue, underlyingStructs(t.Field(i).Type)...)
		}
	}

	return visited
}

// underlyingStructs unwraps t through any chain of Ptr/Slice/Array/Map
// and returns every named struct type it bottoms out at — 0 for a leaf
// (string, uint64, bool, ...), 1 for a direct or wrapped struct
// (SprintView, *ActiveSessionView, []SessionView, map[string]EquippedRef),
// and up to 2 for a map whose KEY is also a struct (not used anywhere in
// this codebase today, but handled rather than assumed away).
func underlyingStructs(t reflect.Type) []reflect.Type {
	switch t.Kind() {
	case reflect.Ptr, reflect.Slice, reflect.Array:
		return underlyingStructs(t.Elem())
	case reflect.Map:
		out := underlyingStructs(t.Key())
		out = append(out, underlyingStructs(t.Elem())...)
		return out
	case reflect.Struct:
		return []reflect.Type{t}
	default:
		return nil
	}
}
