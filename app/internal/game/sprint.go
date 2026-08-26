package game

// sprintDef is one static-list sprint (docs/upgrade-design.md "Sprints").
// Names are game flavour ONLY — see ActivityLine's doc comment and ADR
// 0009: a sprint name must never be phrased so it could be read as a
// description of the user's real activity.
type sprintDef struct {
	Name    string
	Target  float64
	DevCash uint64
	XP      uint64
}

// sprints is the static sprint rotation — a richer pool of cozy, whimsical
// "fictional dev quest" names so the sprint panel feels varied rather than
// looping the same handful. Every name is GAME FICTION about an imaginary
// task the companion is grinding through; per ADR 0009 none is phrased so it
// could be read as a description of what the REAL user is doing right now
// (they are imperative quest titles, not statements about the user). Targets
// stay in the authored 50-130 spread with rewards following the existing
// spread's shape; no difficulty-scaling. Indices 0 and 1 keep their original
// economy (target/DevCash/XP) verbatim — coins_test/game_test pin them.
//
// Completion does NOT step +1: it jumps to a deterministic-varied slot (see
// nextSprintIndex / sprintOrder) and carries any overshoot forward — see
// Game.Tick.
var sprints = []sprintDef{
	{Name: "Fix Bug #404", Target: 50, DevCash: 25, XP: 40},
	{Name: "Refactor Auth Engine", Target: 75, DevCash: 40, XP: 60},
	{Name: "Squash the Heisenbug", Target: 100, DevCash: 60, XP: 90},
	{Name: "Ship the Hotfix", Target: 60, DevCash: 30, XP: 45},
	{Name: "Green the Build", Target: 75, DevCash: 40, XP: 60},
	{Name: "Untangle the Merge", Target: 90, DevCash: 50, XP: 75},
	{Name: "Migrate the Schema", Target: 110, DevCash: 70, XP: 105},
	{Name: "Golf the Regex", Target: 50, DevCash: 25, XP: 40},
	{Name: "Rotate the Secrets", Target: 90, DevCash: 50, XP: 75},
	{Name: "Deflake the Suite", Target: 75, DevCash: 40, XP: 60},
	{Name: "Bump the Deps", Target: 60, DevCash: 30, XP: 45},
	{Name: "Draft the RFC", Target: 70, DevCash: 35, XP: 55},
	{Name: "Write the API Docs", Target: 130, DevCash: 80, XP: 120},
	{Name: "Add CI Cache", Target: 100, DevCash: 60, XP: 90},
	{Name: "Build a Robot", Target: 100, DevCash: 60, XP: 90},
	{Name: "Tame the Flaky Test", Target: 75, DevCash: 40, XP: 60},
}

// sprintOrder is a fixed, hand-authored PERMUTATION of every sprints index:
// the deterministic order in which completions walk the pool. Using a
// scrambled-but-fixed permutation instead of the old (index+1) step makes
// the rotation feel varied — consecutive sprints no longer march 0,1,2,… —
// while staying fully reproducible (the project forbids wall-clock/RNG;
// screenshots are test artifacts). Because it is a permutation, a full walk
// visits every sprint exactly once and wraps cleanly after len(sprints)
// completions. Slots 0 and 1 stay put so a fresh game's first two sprints
// remain sprints[0] then sprints[1] (the economy tests depend on that); the
// remainder is scrambled. sprintOrderValid (see the test) guards that this
// stays a valid permutation matching len(sprints).
var sprintOrder = []int{0, 1, 9, 4, 13, 6, 2, 11, 15, 7, 3, 14, 5, 12, 8, 10}

// nextSprintIndex is the deterministic-varied selection: given the number of
// sprints completed SO FAR (before the current completion is counted), it
// returns the sprints index the rotation lands on next. It is seeded purely
// off existing deterministic state (the lifetime completion count), so the
// same state always yields the same next sprint, and walking the count over
// a run covers the whole pool. Overshoot-carry is handled by the caller and
// is untouched by this function.
func nextSprintIndex(completedSoFar uint64) int {
	n := len(sprints)
	if n == 0 {
		return 0
	}
	// Fall back to the plain +1 step if the authored order ever drifts out
	// of sync with the pool length, so selection can never panic/skew.
	if len(sprintOrder) != n {
		return int((completedSoFar + 1) % uint64(n))
	}
	pos := int((completedSoFar + 1) % uint64(n))
	return sprintOrder[pos]
}

// UnitLabel is the fixed word docs/ui-spec.md's sprint panel uses ("34 / 75
// units") — a display label, not a unique-per-sprint word.
const UnitLabel = "units"

// SprintPoolLen is the number of sprints in the static rotation. Exported
// so out-of-package tests can assert clamping against the real pool size
// instead of a magic number that rots when the pool grows.
func SprintPoolLen() int { return len(sprints) }

func sprintAt(i int) sprintDef {
	if len(sprints) == 0 {
		return sprintDef{Name: "Working...", Target: 1}
	}
	return sprints[((i%len(sprints))+len(sprints))%len(sprints)]
}

// clampSprintIndex clamps an out-of-range sprint index into
// [0, len(sprints)) — used when validating a loaded/imported save
// (docs/upgrade-design.md: "a sprint.index out of range is clamped").
func clampSprintIndex(i int) int {
	if i < 0 {
		return 0
	}
	if i >= len(sprints) {
		return len(sprints) - 1
	}
	return i
}

// levelForXP mirrors the Rust save's level curve, unchanged in v2
// ("Renaming only... the XP curve (50·(n−1)·n) and the level display are
// unchanged"). Level n is reached once xp >= 50*(n-1)*n; level 1 is the
// floor (threshold 0).
func levelForXP(xp uint64) int {
	level := 1
	for thresholdForLevel(level+1) <= xp {
		level++
	}
	return level
}

func thresholdForLevel(n int) uint64 {
	if n <= 1 {
		return 0
	}
	return uint64(50 * (n - 1) * n)
}
