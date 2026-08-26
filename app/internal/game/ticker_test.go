package game

import (
	"strings"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/engine"
)

// terminalLineWidthCap is the font's per-line cap for #terminal
// (docs/ui-spec.md §3.2 / ticker.go's own doc comment).
const terminalLineWidthCap = 30

// TestTerminalPoolIsRichAndWithinWidth guards the expanded #terminal pool:
// it roughly doubled (>= 14 lines), and every fake code/build/log line is
// non-empty and inside the width cap so it never clips on screen.
func TestTerminalPoolIsRichAndWithinWidth(t *testing.T) {
	if len(terminalPool) < 14 {
		t.Errorf("terminalPool has %d lines, want it roughly doubled (>= 14)", len(terminalPool))
	}
	for i, line := range terminalPool {
		if line == "" {
			t.Errorf("terminalPool[%d] is empty", i)
		}
		if len(line) > terminalLineWidthCap {
			t.Errorf("terminalPool[%d] %q is %d chars, over the %d-char cap",
				i, line, len(line), terminalLineWidthCap)
		}
	}
	// The idle sentinel stays a fixed overlay, distinct from the pool.
	for i, line := range terminalPool {
		if line == terminalIdleSentinel {
			t.Errorf("terminalPool[%d] duplicates the idle sentinel %q", i, terminalIdleSentinel)
		}
	}
}

// TestTickerPoolsAreRichAndClean guards the expanded mood ticker pools:
// each mood pool grew (roughly doubled), lines are non-empty, and no line
// collides with a possible real activity line (the anti-leak guard ADR
// 0009 / ui-spec §3 requires — a ticker line must never read like a claim
// about the user's real activity).
func TestTickerPoolsAreRichAndClean(t *testing.T) {
	wantMin := map[engine.Mood]int{
		engine.MoodCoding:  12,
		engine.MoodIdle:    6,
		engine.MoodOnBreak: 6,
	}
	for mood, min := range wantMin {
		if got := len(tickerPools[mood]); got < min {
			t.Errorf("tickerPools[%v] has %d lines, want >= %d (roughly doubled)", mood, got, min)
		}
	}

	// A ticker line must not be identical to any phrasing a real activity
	// line could take — reuse the same guard set game_test.go pins.
	forbidden := map[string]bool{"Coding": true, "Working...": true, "In the terminal": true}
	for mood, pool := range tickerPools {
		for i, line := range pool {
			if line == "" {
				t.Errorf("tickerPools[%v][%d] is empty", mood, i)
			}
			if forbidden[line] {
				t.Errorf("tickerPools[%v][%d] = %q collides with a real activity line", mood, i, line)
			}
		}
	}
}

// TestTickerLineIsDeterministicPerFormula proves tickerLine is reproducible
// and follows the exact §3.1 formula pool[(sprintIndex*7 + tickCount) % len].
func TestTickerLineIsDeterministicPerFormula(t *testing.T) {
	moods := []engine.Mood{engine.MoodCoding, engine.MoodIdle, engine.MoodOnBreak}
	for _, mood := range moods {
		pool := tickerPools[mood]
		for _, sprintIndex := range []int{0, 1, 7, 15} {
			for tc := uint64(0); tc < 40; tc++ {
				got := tickerLine(mood, sprintIndex, tc)
				// Determinism: same inputs, same output.
				if again := tickerLine(mood, sprintIndex, tc); again != got {
					t.Fatalf("tickerLine(%v,%d,%d) not deterministic: %q vs %q", mood, sprintIndex, tc, got, again)
				}
				// Matches the published formula.
				want := pool[(sprintIndex*7+int(tc%uint64(len(pool))))%len(pool)]
				if got != want {
					t.Fatalf("tickerLine(%v,%d,%d)=%q, formula wants %q", mood, sprintIndex, tc, got, want)
				}
			}
		}
	}
}

// TestTerminalLineIsDeterministicPerFormula proves terminalLine is
// reproducible and follows its (sprintIndex*11 + pushCount) % len shape.
func TestTerminalLineIsDeterministicPerFormula(t *testing.T) {
	for _, sprintIndex := range []int{0, 1, 7, 15} {
		for pc := uint64(0); pc < 40; pc++ {
			got := terminalLine(sprintIndex, pc)
			if again := terminalLine(sprintIndex, pc); again != got {
				t.Fatalf("terminalLine(%d,%d) not deterministic: %q vs %q", sprintIndex, pc, got, again)
			}
			want := terminalPool[(sprintIndex*11+int(pc%uint64(len(terminalPool))))%len(terminalPool)]
			if got != want {
				t.Fatalf("terminalLine(%d,%d)=%q, formula wants %q", sprintIndex, pc, got, want)
			}
		}
	}
}

// TestMonitorContentVariesAcrossSprints is the flavour payoff: because the
// sprint index now jumps around the pool, consecutive sprints surface a
// different slice of terminal/ticker lines rather than repeating. Assert
// the terminal pushes for two different sprint indices are not identical.
func TestMonitorContentVariesAcrossSprints(t *testing.T) {
	collect := func(sprintIndex int) string {
		var b strings.Builder
		for pc := uint64(0); pc < 6; pc++ {
			b.WriteString(terminalLine(sprintIndex, pc))
			b.WriteByte('\n')
		}
		return b.String()
	}
	if collect(0) == collect(1) {
		t.Error("terminal content identical for sprint 0 and 1 — monitor does not vary by sprint")
	}
}
