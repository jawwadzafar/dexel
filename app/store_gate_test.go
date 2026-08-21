package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/jawwadzafar/dev-companion/app/internal/game"
)

// testServer wires the same handleWS + action-processing shape main() uses
// (minus the engine/timers), so the disconnect-clears-storeOpen behavior
// (docs/ui-spec.md §5.3: "the connection dropping while storeOpen is true
// must clear the flag server-side") can be exercised end-to-end without a
// real activity provider.
//
// game.Game is single-owner, unlocked state (by design — see game.go's
// doc comment): only the goroutine below ever touches g directly. A test
// that peeked at g.StoreOpen from the test goroutine would itself be a
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
	hub := newHub()
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
				applyAction(ts.g, req.msg)
				close(req.done)
			case fn := <-ts.queries:
				fn()
			}
		}
	}()

	ts.url = "ws" + srv.URL[len("http"):] + "/ws"
	return ts
}

// storeOpen reads g.StoreOpen safely by running the read ON the owning
// goroutine and handing the result back over a channel — never touches g
// from the calling (test) goroutine.
func (ts *testServer) storeOpen() bool {
	result := make(chan bool, 1)
	ts.queries <- func() { result <- ts.g.StoreOpen }
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
