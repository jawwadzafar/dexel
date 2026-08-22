package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/jawwadzafar/dexel/app/internal/game"
)

// testServer wires the same handleWS + action-processing shape main() uses
// (minus the engine/timers), so the disconnect-clears-storeOpen behavior
// (docs/ui-spec.md §5.3: "the connection dropping while storeOpen is true
// must clear the flag server-side") can be exercised end-to-end without a
// real activity provider.
//
// game.Game is single-owner, unlocked state (by design — see game.go's
// doc comment): only the goroutine below ever touches g directly. A test
// that peeked at g.StoreOpen() from the test goroutine would itself be a
// data race, so storeOpen() below routes the read through the same
// channel actions use, and runs it ON the owning goroutine.
type testServer struct {
	url     string
	g       *game.Game
	queries chan func()
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	ts := &testServer{
		g:       game.New(),
		queries: make(chan func()),
	}
	// websocket.Dial (used by dial() below) never sends an Origin header,
	// which handlers.go's authenticateOrigin accepts unconditionally
	// regardless of wsOriginPatterns/wsAllowAnyOrigin — nil/false here
	// exercises the real production values (a bare httptest server has no
	// meaningful "port" to derive patterns from anyway).
	hub := newHub(nil, false)
	hub.setInitialState(ts.g.State())
	catalog := game.NewCatalogMessage()
	actions := make(chan actionRequest)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", hub.handleWS(actions, catalog))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case <-stop:
				return
			case req := <-actions:
				applyAction(ts.g, req.msg, req.connID)
				close(req.done)
			case fn := <-ts.queries:
				fn()
			}
		}
	}()

	ts.url = "ws" + srv.URL[len("http"):] + "/ws"
	return ts
}

// storeOpen reads g.StoreOpen() safely by running the read ON the owning
// goroutine and handing the result back over a channel — never touches g
// from the calling (test) goroutine.
func (ts *testServer) storeOpen() bool {
	result := make(chan bool, 1)
	ts.queries <- func() { result <- ts.g.StoreOpen() }
	return <-result
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Drain the initial `catalog` and `state` messages so they don't
	// confuse callers that only care about actions in this test.
	for i := 0; i < 2; i++ {
		var discard any
		ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
		_ = wsjson.Read(ctx2, c, &discard)
		cancel2()
	}
	return c
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func sendAction(t *testing.T, c *websocket.Conn, msg actionMessage) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, c, msg); err != nil {
		t.Fatalf("write action: %v", err)
	}
}

func TestStoreOpenGate_ExplicitClose(t *testing.T) {
	ts := newTestServer(t)
	c := dial(t, ts.url)

	sendAction(t, c, actionMessage{Action: "STORE_OPEN"})
	waitFor(t, time.Second, ts.storeOpen)

	sendAction(t, c, actionMessage{Action: "STORE_CLOSE"})
	waitFor(t, time.Second, func() bool { return !ts.storeOpen() })

	_ = c.Close(websocket.StatusNormalClosure, "")
}

// TestStoreOpenGate_ClearedOnDisconnect is the exact scenario ui-spec §5.3
// calls out: "a crashed tab freezes progression forever" if disconnecting
// doesn't clear the flag. This dials, opens the store, then disconnects
// WITHOUT ever sending STORE_CLOSE, and asserts the server-side flag
// clears anyway.
func TestStoreOpenGate_ClearedOnDisconnect(t *testing.T) {
	ts := newTestServer(t)
	c := dial(t, ts.url)

	sendAction(t, c, actionMessage{Action: "STORE_OPEN"})
	waitFor(t, time.Second, ts.storeOpen)

	// Simulate a crashed/closed tab: drop the connection with no
	// STORE_CLOSE at all.
	_ = c.Close(websocket.StatusAbnormalClosure, "")

	waitFor(t, 2*time.Second, func() bool { return !ts.storeOpen() })
}

// TestStoreOpenGate_DisconnectAfterExplicitCloseIsANoOp guards against a
// double-STORE_CLOSE (once from the client, once synthesized on
// disconnect) doing anything surprising — it shouldn't reopen or error.
func TestStoreOpenGate_DisconnectAfterExplicitCloseIsANoOp(t *testing.T) {
	ts := newTestServer(t)
	c := dial(t, ts.url)

	sendAction(t, c, actionMessage{Action: "STORE_OPEN"})
	waitFor(t, time.Second, ts.storeOpen)
	sendAction(t, c, actionMessage{Action: "STORE_CLOSE"})
	waitFor(t, time.Second, func() bool { return !ts.storeOpen() })

	_ = c.Close(websocket.StatusNormalClosure, "")
	waitFor(t, time.Second, func() bool { return !ts.storeOpen() })
}

// TestStoreOpenGate_SecondClientCannotReleaseFirstClientsHold is the B2
// "money mint" regression: with a single global bool (no refcount), any
// client sending STORE_CLOSE — or simply disconnecting — flips storeOpen
// to false for EVERYONE, including a different client that is still
// looking at an open store modal. That would let work/Dev Cash accrue
// while client A's modal is still up. This dials two clients, has only A
// open the store, then has B open-and-close (and finally disconnect)
// without ever opening it itself, and asserts A's hold is never released
// by any of B's actions.
func TestStoreOpenGate_SecondClientCannotReleaseFirstClientsHold(t *testing.T) {
	ts := newTestServer(t)
	a := dial(t, ts.url)
	b := dial(t, ts.url)

	sendAction(t, a, actionMessage{Action: "STORE_OPEN"})
	waitFor(t, time.Second, ts.storeOpen)

	// B never opened the store itself; STORE_CLOSE from B must be a no-op
	// against A's hold.
	sendAction(t, b, actionMessage{Action: "STORE_CLOSE"})
	if !ts.storeOpen() {
		t.Fatal("client B's STORE_CLOSE released client A's store-open hold")
	}

	// B disconnecting (with no STORE_OPEN of its own ever sent) must also
	// leave A's hold intact.
	_ = b.Close(websocket.StatusAbnormalClosure, "")
	time.Sleep(50 * time.Millisecond) // let the disconnect's synthetic close (if any) land
	if !ts.storeOpen() {
		t.Fatal("client B disconnecting released client A's store-open hold")
	}

	// Sanity: A closing its OWN hold still works.
	sendAction(t, a, actionMessage{Action: "STORE_CLOSE"})
	waitFor(t, time.Second, func() bool { return !ts.storeOpen() })

	_ = a.Close(websocket.StatusNormalClosure, "")
}

// TestStoreOpenGate_ReconnectReopensGate is the B2 reconnect regression:
// a client whose connection blips (server restart, network hiccup) and
// reconnects must re-establish its store-open hold rather than silently
// losing it — the frontend re-sends STORE_OPEN on the WS 'open' event
// when its own modal is still open (dexel.js), and the server must accept
// it under the NEW connID exactly like a fresh open.
func TestStoreOpenGate_ReconnectReopensGate(t *testing.T) {
	ts := newTestServer(t)
	c1 := dial(t, ts.url)

	sendAction(t, c1, actionMessage{Action: "STORE_OPEN"})
	waitFor(t, time.Second, ts.storeOpen)

	// Simulate the blip: the connection drops (old connID's hold is
	// released) ...
	_ = c1.Close(websocket.StatusAbnormalClosure, "")
	waitFor(t, 2*time.Second, func() bool { return !ts.storeOpen() })

	// ... and the client reconnects under a brand new connID and
	// re-asserts STORE_OPEN, exactly as dexel.js's WS 'open' handler does
	// when its local modal is still showing.
	c2 := dial(t, ts.url)
	sendAction(t, c2, actionMessage{Action: "STORE_OPEN"})
	waitFor(t, time.Second, ts.storeOpen)

	_ = c2.Close(websocket.StatusNormalClosure, "")
}
