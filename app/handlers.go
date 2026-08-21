package main

import (
	"log"
	"net/http"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/jawwadzafar/dev-companion/app/internal/game"
)

// handleWS upgrades to a WebSocket, sends the `catalog` message once (it
// never changes at runtime — docs/ui-spec.md §6.1) followed by an
// immediate `state` snapshot ("on connect, then every 1s, and immediately
// after any mutation"), then loops reading actionMessages from the client
// and forwarding them to the single owning goroutine via actions.
//
// If this connection ever sent STORE_OPEN and then disconnects without a
// matching STORE_CLOSE, the defer fires a synthetic STORE_CLOSE so a
// crashed/closed tab can never freeze progression forever
// (docs/ui-spec.md §5.3).
func (h *Hub) handleWS(actions chan<- actionRequest, catalog game.CatalogMessage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			// This server binds to 127.0.0.1 for local use; the frontend
			// may be opened from a file:// origin or a Wails webview
			// during development — accept any origin.
			InsecureSkipVerify: true,
		})
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
			var msg actionMessage
			if err := wsjson.Read(ctx, c, &msg); err != nil {
				return // client disconnected or sent garbage — done
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
