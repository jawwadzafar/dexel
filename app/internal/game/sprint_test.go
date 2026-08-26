package game

import (
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/engine"
)

// sprintNameWidthCap is the panel-width budget for a sprint name. The
// widest name the design ever shipped was "Refactor Auth Engine" (20
// chars); every entry in the pool must fit inside that so the sprint panel
// never clips (docs/ui-spec.md's sprint panel). This is a flavour guard,
// not a wire contract.
const sprintNameWidthCap = 20

// TestSprintPoolIsWellFormed pins the shape of the expanded sprint pool:
// it is a rich rotation (not the old 6), every name fits the panel, every
// entry has authored targets in the 50-130 spread with non-zero rewards,
// and indices 0 and 1 keep the exact economy the coin/tick tests pin.
func TestSprintPoolIsWellFormed(t *testing.T) {
	if len(sprints) < 14 {
		t.Fatalf("sprints pool has %d entries, want a rich rotation (>= 14)", len(sprints))
	}

	for i, s := range sprints {
		if s.Name == "" {
			t.Errorf("sprints[%d] has an empty name", i)
		}
		if len(s.Name) > sprintNameWidthCap {
			t.Errorf("sprints[%d] name %q is %d chars, over the %d-char panel cap",
				i, s.Name, len(s.Name), sprintNameWidthCap)
		}
		if s.Target < 50 || s.Target > 130 {
			t.Errorf("sprints[%d] (%q) target = %v, want within the authored 50-130 spread",
				i, s.Name, s.Target)
		}
		if s.DevCash == 0 || s.XP == 0 {
			t.Errorf("sprints[%d] (%q) has a zero reward: DevCash=%d XP=%d",
				i, s.Name, s.DevCash, s.XP)
		}
	}

	// Names must be unique so the panel never shows two identically-named
	// sprints as if they were distinct quests.
	seen := map[string]int{}
	for i, s := range sprints {
		if j, dup := seen[s.Name]; dup {
			t.Errorf("duplicate sprint name %q at indices %d and %d", s.Name, j, i)
		}
		seen[s.Name] = i
	}

	// Indices 0 and 1 are the economy anchors the coin/tick tests pin.
	if got := sprints[0]; got.Target != 50 || got.DevCash != 25 || got.XP != 40 {
		t.Errorf("sprints[0] economy = %+v, want target 50 / DevCash 25 / XP 40", got)
	}
	if got := sprints[1]; got.Target != 75 || got.DevCash != 40 || got.XP != 60 {
		t.Errorf("sprints[1] economy = %+v, want target 75 / DevCash 40 / XP 60", got)
	}
}

// TestSprintOrderIsAValidPermutation guards the selection order: it must be
// a permutation of every sprints index (each exactly once, matching length)
// so a full walk visits every sprint and wraps cleanly. Slots 0 and 1 are
// fixed so a fresh game's first two sprints stay sprints[0] then sprints[1].
func TestSprintOrderIsAValidPermutation(t *testing.T) {
	if len(sprintOrder) != len(sprints) {
		t.Fatalf("len(sprintOrder)=%d != len(sprints)=%d — nextSprintIndex would fall back to +1",
			len(sprintOrder), len(sprints))
	}
	seen := make([]bool, len(sprints))
	for pos, idx := range sprintOrder {
		if idx < 0 || idx >= len(sprints) {
			t.Fatalf("sprintOrder[%d]=%d is out of range [0,%d)", pos, idx, len(sprints))
		}
		if seen[idx] {
			t.Errorf("sprintOrder repeats index %d — not a permutation", idx)
		}
		seen[idx] = true
	}
	if sprintOrder[0] != 0 {
		t.Errorf("sprintOrder[0]=%d, want 0 (a fresh game starts on sprints[0])", sprintOrder[0])
	}
	if sprintOrder[1] != 1 {
		t.Errorf("sprintOrder[1]=%d, want 1 (first completion rolls to sprints[1])", sprintOrder[1])
	}
	// It must be genuinely varied, not the old ascending 0,1,2,3,… loop.
	ascending := true
	for i, idx := range sprintOrder {
		if idx != i {
			ascending = false
			break
		}
	}
	if ascending {
		t.Error("sprintOrder is the plain ascending sequence — selection is not varied")
	}
}

// TestNextSprintIndexIsDeterministicAndCoversPool proves the selection is
// reproducible (same completion count -> same index, every call) and that
// walking the count across one full cycle covers every sprint exactly once.
func TestNextSprintIndexIsDeterministicAndCoversPool(t *testing.T) {
	n := len(sprints)

	// Determinism: the same input always yields the same output.
	for c := uint64(0); c < uint64(3*n); c++ {
		if a, b := nextSprintIndex(c), nextSprintIndex(c); a != b {
			t.Fatalf("nextSprintIndex(%d) not deterministic: %d vs %d", c, a, b)
		}
	}

	// Coverage over one full walk (completion counts 0..n-1), and never a
	// same-sprint-twice-in-a-row step.
	seen := make([]bool, n)
	prev := 0 // a fresh game starts on sprints[0]
	for c := uint64(0); c < uint64(n); c++ {
		next := nextSprintIndex(c)
		if next == prev {
			t.Errorf("nextSprintIndex(%d)=%d repeats the current sprint %d back-to-back", c, next, prev)
		}
		seen[next] = true
		prev = next
	}
	for i, ok := range seen {
		if !ok {
			t.Errorf("sprint index %d never selected across a full walk — pool not fully covered", i)
		}
	}

	// After a full walk of n completions the rotation wraps back to start.
	if got := nextSprintIndex(uint64(n) - 1); got != sprintOrder[0] {
		t.Errorf("nextSprintIndex(n-1)=%d, want %d (wrap to the start of the order)", got, sprintOrder[0])
	}
}

// TestSprintSequenceIsReproducibleAcrossGames drives two independent games
// through identical ticks and asserts they produce the exact same sequence
// of completed sprint names — the "same state -> same sprint" guarantee the
// deterministic-varied selection must keep — and that the sequence is not
// the old predictable +1 march.
func TestSprintSequenceIsReproducibleAcrossGames(t *testing.T) {
	run := func() []string {
		g := New()
		var names []string
		// Enough completions to walk well past one full cycle.
		for i := 0; i < len(sprints)*2+3; i++ {
			def := sprintAt(g.SprintIndex())
			names = append(names, def.Name)
			g.Tick(engine.TickResult{Mood: engine.MoodCoding, WorkUnits: def.Target})
		}
		return names
	}

	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("sequence lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("sprint sequence not reproducible at step %d: %q vs %q", i, a[i], b[i])
		}
	}

	// Sanity: the observed index sequence is not the strict +1 loop.
	g := New()
	varied := false
	prev := g.SprintIndex()
	for i := 0; i < len(sprints); i++ {
		def := sprintAt(g.SprintIndex())
		g.Tick(engine.TickResult{Mood: engine.MoodCoding, WorkUnits: def.Target})
		cur := g.SprintIndex()
		if cur != (prev+1)%len(sprints) {
			varied = true
		}
		prev = cur
	}
	if !varied {
		t.Error("sprint selection still marches strictly +1 — variety not achieved")
	}
}
