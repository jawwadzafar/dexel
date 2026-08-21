package main

import (
	"context"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// TestMalformedWSMessageFlashesErrorWithoutClosingSocket is the malformed-
// JSON NIT from the fix-wave review: docs/ui-spec.md §6.2 says a rejected
// action gets a `kind:"error"` flash, and handleWS's own doc comment says
// the connection (and any store-open hold it's keeping — §5.3) should be
// left alone. Before this fix, a malformed payload hit wsjson.Read's own
// automatic c.Close(StatusInvalidFramePayloadData, ...) — close code 1007
// — tearing the connection down instead. This dials, writes a raw
// non-JSON text frame, and asserts: (a) an error flash arrives, NOT a
// closed connection, and (b) the connection is still usable afterward for
// an ordinary action.
func TestMalformedWSMessageFlashesErrorWithoutClosingSocket(t *testing.T) {
	ts := newTestServer(t)
	c := dial(t, ts.url)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	if err := c.Write(ctx, websocket.MessageText, []byte("{not valid json")); err != nil {
		cancel()
		t.Fatalf("write malformed frame: %v", err)
	}
	cancel()

	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	var msg map[string]any
	err := wsjson.Read(ctx2, c, &msg)
	cancel2()
	if err != nil {
		t.Fatalf("expected an error flash in response to malformed JSON, got a read error (socket likely closed): %v", err)
	}
	if msg["type"] != "flash" || msg["kind"] != "error" {
		t.Fatalf("response to malformed JSON = %+v, want a flash of kind error", msg)
	}

	// The connection must still be usable: a normal, well-formed action
	// sent right after the malformed one should still work.
	sendAction(t, c, actionMessage{Action: "STORE_OPEN"})
	waitFor(t, time.Second, ts.storeOpen)

	_ = c.Close(websocket.StatusNormalClosure, "")
}
