package game

import (
	"encoding/json"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/engine"
)

// TestAppIdentityAvailableRidesTheWire pins the provider's app-identity
// CAPABILITY bit (activity.Snapshot.AppIdentityAvailable) reaching the
// client verbatim on the `state` message under the exact JSON key
// (`appIdentityAvailable`). This bit formerly gated hiding the app-switch
// stat rows on an app-blind platform (Linux/Wayland, ADR 0009); that
// metric was removed, so the bit no longer has a client consumer, but it
// is still emitted as an honest provider-capability signal and this test
// keeps its plumbing and honest default pinned.
//
// The honest default matters as much as the plumbing: BEFORE any tick a
// fresh game must report the capability as false ("assume app-blind until a
// provider says otherwise"), and a tick must be able to flip it either way.
func TestAppIdentityAvailableRidesTheWire(t *testing.T) {
	marshalFlag := func(g *Game) bool {
		t.Helper()
		raw, err := json.Marshal(g.State())
		if err != nil {
			t.Fatalf("marshal State(): %v", err)
		}
		var wire struct {
			// Exact wire tag the frontend consumes (wire.ts:
			// StateMessage.appIdentityAvailable) — a rename on the Go side
			// would leave this at its zero value and fail the assertions.
			AppIdentityAvailable bool `json:"appIdentityAvailable"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatalf("unmarshal state json: %v", err)
		}
		return wire.AppIdentityAvailable
	}

	// A never-ticked game: honest default is "app-blind" (false).
	g := New()
	if marshalFlag(g) {
		t.Fatal("a fresh, never-ticked game reported appIdentityAvailable=true; the honest default before any observation is false")
	}

	// A tick from a provider that CAN see app identity flips it true.
	g.Tick(engine.TickResult{
		Mood:                 engine.MoodCoding,
		ActiveApp:            "code",
		ActiveAppDisplay:     "VS Code",
		AppIdentityAvailable: true,
	})
	if !marshalFlag(g) {
		t.Fatal("after a tick with AppIdentityAvailable=true, the wire still says false")
	}

	// A subsequent app-blind tick (Linux/Wayland: no ActiveApp, capability
	// false) flips it back — the bit tracks the CURRENT provider, it is not
	// latched on.
	g.Tick(engine.TickResult{
		Mood:                 engine.MoodCoding,
		AppIdentityAvailable: false,
	})
	if marshalFlag(g) {
		t.Fatal("after an app-blind tick, the wire still says true; the capability bit must track the current provider, not latch")
	}
}
