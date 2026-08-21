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

// sprints is the static six-sprint rotation, transcribed verbatim from
// docs/upgrade-design.md. Completion rolls to (index+1) % len(sprints) and
// carries any overshoot forward — see Game.Tick.
var sprints = []sprintDef{
	{Name: "Fix Bug #404", Target: 50, DevCash: 25, XP: 40},
	{Name: "Refactor Auth Engine", Target: 75, DevCash: 40, XP: 60},
	{Name: "Add CI Cache", Target: 100, DevCash: 60, XP: 90},
	{Name: "Write the API Docs", Target: 130, DevCash: 80, XP: 120},
	{Name: "Build a Robot", Target: 100, DevCash: 60, XP: 90},
	{Name: "Tame the Flaky Test", Target: 75, DevCash: 40, XP: 60},
}

// UnitLabel is the fixed word docs/ui-spec.md's sprint panel uses ("34 / 75
// units") — a display label, not a unique-per-sprint word.
const UnitLabel = "units"

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
