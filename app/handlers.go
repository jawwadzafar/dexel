package main

import (
	"encoding/json"
	"log"
	"net/http"

	"nhooyr.io/websocket"

	"github.com/jawwadzafar/dexel/app/internal/game"
)

// handleWS upgrades to a WebSocket, sends the `catalog` message once (it
// never changes at runtime — docs/ui-spec.md §6.1) followed by an
// immediate `state` snapshot ("on connect, then every 1s, and immediately
// after any mutation"), then loops reading actionMessages from the client
// and forwarding them to the single owning goroutine via actions.
//
// If this connection ever sent STORE_OPEN and then disconnects without a
// matching STORE_CLOSE, the defer fires a synthetic STORE_CLOSE (scoped to
// this connID only) so a crashed/closed tab can never freeze progression
// forever (docs/ui-spec.md §5.3). game.Game.CloseStore(connID) only ever
// removes THIS connID's own hold from the refcounted set
// (internal/game/game.go's openStoreConns) — it can never release a hold a
// different, still-connected client is keeping open, and it's a no-op if
// this connID never held one. The openedStore bookkeeping below is purely
// an optimization (skip the synthetic action when we already know it's
// unnecessary) — correctness comes from CloseStore(connID) itself being
// idempotent and connID-scoped, not from this flag being exactly right.
func (h *Hub) handleWS(actions chan<- actionRequest, catalog game.CatalogMessage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		acceptOpts := &websocket.AcceptOptions{
			// This server binds to 127.0.0.1 by default. The request host
			// is always authorized (nhooyr.io/websocket's own rule), so
			// the normal browser tab loaded from this server works with
			// no entry here; h.wsOriginPatterns (derived from -addr's
			// port by main.go's wsOriginPatterns()) only covers the other
			// loopback hostname the frontend may be addressed by
			// (localhost vs. 127.0.0.1) when that differs from the
			// listen address. An empty/missing Origin header (non-browser
			// clients, e.g. a Wails webview) is accepted unconditionally
			// by the library — this option only constrains browser-sent
			// Origins. h.wsAllowAnyOrigin is the explicit -insecure-origin
			// opt-in (default false) for exactly the embedded-webview case
			// where the Origin header won't match any loopback pattern at
			// all (e.g. a file:// or app:// origin) — see main.go's flag
			// description; it must never be turned on together with an
			// -addr that binds beyond loopback.
			OriginPatterns: h.wsOriginPatterns,
		}
		if h.wsAllowAnyOrigin {
			acceptOpts.InsecureSkipVerify = true
		}
		c, err := websocket.Accept(w, r, acceptOpts)
		if err != nil {
			log.Printf("ws accept: %v", err)
			return
		}
		connID := h.add(c)
		openedStore := false
		defer func() {
			h.remove(connID)
			_ = c.Close(websocket.StatusNormalClosure, "")
			if openedStore {
				done := make(chan struct{})
				actions <- actionRequest{msg: actionMessage{Action: "STORE_CLOSE"}, connID: connID, done: done}
				<-done
			}
		}()

		ctx := r.Context()
		if !h.send(c, catalog) {
			return
		}
		if !h.send(c, h.getLastState()) {
			return
		}

		for {
			// Read the raw frame ourselves (rather than wsjson.Read)
			// specifically so a malformed JSON payload doesn't
			// automatically close the socket: wsjson.Read calls
			// c.Close(StatusInvalidFramePayloadData, ...) — a close code
			// 1007 — the instant json.Unmarshal fails, which is far
			// harsher than docs/ui-spec.md §6.2 calls for. A malformed
			// client message should surface as an ordinary `kind:"error"`
			// flash to that one client, exactly like any other rejected
			// action, and leave the connection (and the store-open gate
			// it may be holding — see this function's doc comment) alone.
			_, data, err := c.Read(ctx)
			if err != nil {
				return // client disconnected (or a real transport error) — done
			}
			var msg actionMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				h.send(c, flashMessage{Type: "flash", Kind: "error", Text: "malformed message"})
				continue
			}
			switch msg.Action {
			case "STORE_OPEN":
				openedStore = true
			case "STORE_CLOSE":
				openedStore = false
			}
			done := make(chan struct{})
			actions <- actionRequest{msg: msg, connID: connID, done: done}
			<-done
		}
	}
}
