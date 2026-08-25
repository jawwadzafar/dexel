package main

import (
	"testing"
	"time"
)

// TestGracefulShutdownFitsInsideStopGrace is BUG-9's arithmetic, asserted
// rather than asserted-in-a-comment.
//
// The bug was not one slow call: it was that the runtime's graceful path
// was budgeted to take EXACTLY as long as `dexel stop` was willing to wait
// (both 5s), so the moment anything ahead of the last phase took real time
// the CLI escalated to SIGKILL — which it then did on every installer gate
// for a day. The runtime's whole graceful budget must therefore stay a
// small fraction of the CLI's patience, with room for the save and for the
// provider's own bounded stop (activity.stopWaitTimeout, 500ms) on top.
//
// The provider's ceiling is not importable from package main (it is
// unexported), so it is spelled out here as the number this test's margin
// has to cover; if it grows, this test's margin is where the conflict
// surfaces.
func TestGracefulShutdownFitsInsideStopGrace(t *testing.T) {
	const providerStopCeiling = 500 * time.Millisecond // activity.stopWaitTimeout
	// A generous allowance for the one unbounded phase, the state save: a
	// local SQLite write measured at ~1ms, given 3 orders of magnitude.
	const persistAllowance = 1 * time.Second

	worst := persistAllowance + providerStopCeiling + httpShutdownGrace
	if worst >= stopGrace {
		t.Fatalf("worst-case graceful shutdown %s does not fit inside `dexel stop`'s %s grace — a graceful stop can be SIGKILLed again (BUG-9)", worst, stopGrace)
	}
	// Not just "fits": fits with the whole grace period to spare, so a
	// slow disk or a loaded machine has somewhere to go.
	if worst > stopGrace/2 {
		t.Errorf("worst-case graceful shutdown %s leaves less than half of the %s stop grace as headroom", worst, stopGrace)
	}
	if httpShutdownGrace >= stopGrace {
		t.Errorf("httpShutdownGrace %s >= stopGrace %s: the HTTP drain alone can outlast the CLI's patience (this is exactly the BUG-9 shape)", httpShutdownGrace, stopGrace)
	}
}
