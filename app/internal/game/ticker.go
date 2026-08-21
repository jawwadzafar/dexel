package game

import "github.com/jawwadzafar/dexel/app/internal/engine"

// Ticker and terminal pools are STATIC, GAME-FLAVOR text only
// (docs/ui-spec.md §3: "No string rendered in #ticker or #terminal may be
// derived from anything on the user's machine... The pools are compile-time
// []string constants in the backend.") Nothing here is derived from what
// the user actually typed, clicked, or ran.

// tickerPools partitions the ticker's seed lines by activeState, verbatim
// from docs/ui-spec.md §3.1.
var tickerPools = map[engine.Mood][]string{
	engine.MoodCoding: {
		"Compiling...",
		"Resolving dependencies...",
		"Analyzing schematic...",
		"Running unit 42...",
		"Linting the linter...",
		"Type-checking module 7...",
		"Rebuilding index...",
	},
	engine.MoodIdle: {
		"Waiting on input...",
		"Cursor blinking...",
		"Reading the docs...",
		"Watching for changes...",
	},
	engine.MoodOnBreak: {
		"Idle timer running...",
		"Screen saver engaged...",
		"Sipping something warm...",
		"Nothing to compile.",
	},
}

// tickerLine implements the deterministic selection formula ui-spec §3.1
// gives verbatim: pool[(sprintIndex*7 + tickCount) % len(pool)].
func tickerLine(mood engine.Mood, sprintIndex int, tickCount uint64) string {
	pool := tickerPools[mood]
	if len(pool) == 0 {
		return ""
	}
	idx := (sprintIndex*7 + int(tickCount%uint64(len(pool)))) % len(pool)
	if idx < 0 {
		idx += len(pool)
	}
	return pool[idx]
}

// terminalPool is the seed pool for #terminal (docs/ui-spec.md §3.2). Every
// line is <= 30 chars (the font's per-line cap).
var terminalPool = []string{
	"func handleRequest(ctx) error {",
	"  if err != nil { return err }",
	"-> ok  parser        1.4s",
	"warning: unused import 'fmt'",
	"[ 62%] building target...",
	"test result: ok. 41 passed",
	"resolved 118 deps in 0.9s",
	"   Compiling companion v0.2",
}

// terminalIdleSentinel is the fixed last line shown while onBreak
// (docs/ui-spec.md §3.2: "the last line reads -- idle --").
const terminalIdleSentinel = "-- idle --"

// terminalLine selects the next scrolled-in line deterministically. No
// exact formula is mandated for the terminal (unlike the ticker's), but
// §6.1's field note requires screenLines to be "deterministic and
// screenshot-reproducible" — this mirrors the ticker's own formula shape
// so the same reasoning applies, using a separate counter (pushCount) so
// the two surfaces don't echo each other's sequence.
func terminalLine(sprintIndex int, pushCount uint64) string {
	if len(terminalPool) == 0 {
		return ""
	}
	idx := (sprintIndex*11 + int(pushCount%uint64(len(terminalPool)))) % len(terminalPool)
	if idx < 0 {
		idx += len(terminalPool)
	}
	return terminalPool[idx]
}
