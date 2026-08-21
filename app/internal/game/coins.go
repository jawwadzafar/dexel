package game

import (
	"sort"

	"github.com/jawwadzafar/dev-companion/app/internal/engine"
)

// CoinBreakdown is the per-signal split of coins ("Dev Cash") attributed
// TODAY, exposed at StatsView.CoinsToday (docs/plan/A2-design.md §5/§6).
// Coins have exactly one source — sprint completion (ADR 0008) — so this
// is not a second payout path; it is purely an accounting split of the
// same integer DevCash a sprint already pays, done at the moment of
// payout (see Game.awardCoins). Every field is a whole-number coin count
// — content-free by construction, same rule as StatCounters.
type CoinBreakdown struct {
	Keystrokes    uint64 `json:"keystrokes"`
	Mouse         uint64 `json:"mouse"`
	FocusSessions uint64 `json:"focusSessions"`
	AppSwitches   uint64 `json:"appSwitches"`
}

// Sum returns the total coins across all four signals — used by A3 (§3.3)
// to compute a day's coinsEarned at finalize, and by the dense wire
// history builder (§5) for today's still-accumulating coinsToday.
func (cb CoinBreakdown) Sum() uint64 {
	return cb.Keystrokes + cb.Mouse + cb.FocusSessions + cb.AppSwitches
}

// signalWork decomposes one tick's engine.TickResult into the four
// signals' individual work contributions, in a way that reproduces
// engine.Engine.Tick's own math exactly (see engine.go): keystroke and
// mouse compete for the SAME clamped weighted-rate ceiling
// (engine.MaxRecentRate), so they are split proportionally to their
// pre-clamp shares of that ceiling rather than computed independently;
// the focus-session bonus and app-switch work are additive on top of
// that, exactly as engine.Tick folds them into WorkUnits. This keeps
// keyWork+mouseWork+focusWork+switchWork == r.WorkUnits whenever
// switchCounted matches r.AppSwitches (true unless the daily cap — see
// Game.recordStats — dropped this tick's switch, which only matters once
// Fork B's AppSwitchWork is flipped off its 0.0 default).
//
// switchCounted is the outcome of that daily-cap check for THIS tick
// (false if there was no switch this tick, or the cap already reached).
func signalWork(r engine.TickResult, switchCounted bool) (keyWork, mouseWork, focusWork, switchWork float64) {
	rawKey := float64(r.KeystrokeDelta) * engine.KeystrokeWeight
	rawMouse := 0.0
	if r.MouseActive {
		rawMouse = engine.MouseSustainedRate * engine.MouseWeight
	}
	rawTotal := rawKey + rawMouse

	weighted := rawTotal
	if weighted > engine.MaxRecentRate {
		weighted = engine.MaxRecentRate
	}
	rateWork := weighted * engine.WorkPerUnitRate

	if rawTotal > 0 {
		keyWork = rateWork * (rawKey / rawTotal)
		mouseWork = rateWork * (rawMouse / rawTotal)
	}

	focusWork = engine.FocusSessionBonusWork * float64(r.FocusSessionsCompleted)
	if switchCounted {
		switchWork = engine.AppSwitchWork
	}
	return keyWork, mouseWork, focusWork, switchWork
}

// splitCoinsProportional splits `total` whole coins across the four
// signals in proportion to their accrued work shares (keyWork, mouseWork,
// focusWork, switchWork — work accrued since the last payout, see
// Game.workKeys et al.), using the largest-remainder method so the four
// integer counts always sum to EXACTLY `total` (coin conservation —
// docs/plan/A2-design.md §5/§8's exit criterion). If no work was accrued
// at all (should not happen in practice — Progress only advances via
// accrued work — but guarded for a degenerate restored-save edge case),
// every coin is attributed to keystrokes as a deterministic fallback.
func splitCoinsProportional(total uint64, keyWork, mouseWork, focusWork, switchWork float64) CoinBreakdown {
	if total == 0 {
		return CoinBreakdown{}
	}

	work := [4]float64{keyWork, mouseWork, focusWork, switchWork}
	sum := work[0] + work[1] + work[2] + work[3]
	if sum <= 0 {
		return CoinBreakdown{Keystrokes: total}
	}

	exact := [4]float64{}
	floors := [4]uint64{}
	var assigned uint64
	for i, w := range work {
		e := float64(total) * (w / sum)
		if e < 0 {
			e = 0
		}
		exact[i] = e
		f := uint64(e)
		floors[i] = f
		assigned += f
	}

	type fracEntry struct {
		idx  int
		frac float64
	}
	fracs := make([]fracEntry, 4)
	for i := range work {
		fracs[i] = fracEntry{idx: i, frac: exact[i] - float64(floors[i])}
	}
	sort.Slice(fracs, func(a, b int) bool {
		if fracs[a].frac != fracs[b].frac {
			return fracs[a].frac > fracs[b].frac
		}
		return fracs[a].idx < fracs[b].idx // deterministic tie-break
	})

	if assigned < total {
		remaining := total - assigned
		for i := 0; i < len(fracs) && uint64(i) < remaining; i++ {
			floors[fracs[i].idx]++
		}
	}

	// Belt-and-suspenders exact-conservation guard: whatever float
	// rounding did above, force the four counts to sum to EXACTLY total
	// by adjusting the largest-share bucket. This makes the conservation
	// invariant hold by construction, not by trusting float arithmetic.
	var sumFloors uint64
	for _, f := range floors {
		sumFloors += f
	}
	if sumFloors != total {
		diff := int64(total) - int64(sumFloors)
		adjustIdx := fracs[0].idx
		floors[adjustIdx] = uint64(int64(floors[adjustIdx]) + diff)
	}

	return CoinBreakdown{
		Keystrokes:    floors[0],
		Mouse:         floors[1],
		FocusSessions: floors[2],
		AppSwitches:   floors[3],
	}
}
