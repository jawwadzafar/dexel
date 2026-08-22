package main

import (
	"context"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/jawwadzafar/dexel/app/internal/game"
)

// actionMessage is the JSON shape of a client -> server action
// (docs/ui-spec.md §6.2). Not every field applies to every action —
// e.g. STORE_OPEN carries none of them.
type actionMessage struct {
	Action string `json:"action"`
	ItemID string `json:"itemId,omitempty"`
	Slot   string `json:"slot,omitempty"`
	TintID string `json:"tintId,omitempty"`
	// Name is SET_NAME's only payload (Phase P1, docs/ui-spec.md §6.2).
	// It arrives as RAW client text and is never used unvalidated: the
	// single door it passes through is game.NormalizeName (control
	// characters dropped, trimmed, capped at game.MaxNameLen runes,
	// empty rejected) inside game.Game.SetConfigName.
	Name string `json:"name,omitempty"`
}

// flashMessage is the transient toast (docs/ui-spec.md §6.1 "flash").
type flashMessage struct {
	Type string `json:"type"` // "flash"
	Kind string `json:"kind"` // purchase | equip | sprint | error
	Text string `json:"text"`
}

// actionRequest is how a WS/HTTP handler goroutine asks the single owning
// goroutine (main's select loop) to mutate *game.Game — game.Game does no
// locking itself, so every mutation funnels through this channel to stay
// single-threaded. The owning loop itself broadcasts the resulting
// `state`/`flash` messages (docs/ui-spec.md §6.2: "no dedicated ack" — the
// handler doesn't need anything back beyond "this has been applied,
// you may read the next message now").
type actionRequest struct {
	msg    actionMessage
	connID uint64 // which connection sent it — needed only for STORE_OPEN/CLOSE bookkeeping
	done   chan struct{}
}

// Hub tracks connected WebSocket clients and broadcasts `state`/`catalog`/
// `flash` messages to all of them. It knows nothing about game rules —
// pure connection plumbing.
//
// lastState is a cached copy of the most recent state broadcast, guarded
// by mu. It exists so a freshly-accepted connection can send an immediate
// `state` snapshot (docs/ui-spec.md §6.1: "on connect, then every 1s...")
// WITHOUT calling game.Game.State() from the per-connection HTTP goroutine
// — Game is single-owner, unlocked state (see game.go's doc comment), and
// only the main select loop may touch it. Routing the initial snapshot
// through this cache instead keeps that invariant real rather than
// aspirational.
type Hub struct {
	mu        sync.Mutex
	conns     map[uint64]*websocket.Conn
	nextID    uint64
	lastState game.StateMessage

	// wsOriginPatterns/wsAllowAnyOrigin configure handleWS's WebSocket
	// origin check (B1: the server used to accept any Origin at all,
	// which meant any web page a user merely had open could pop a
	// cross-origin WS to this loopback server and read/mutate the save).
	// wsOriginPatterns is derived from -addr's port by main.go's
	// wsOriginPatterns() so a same-origin browser tab always connects
	// regardless of whether it's addressed via 127.0.0.1 or localhost.
	// wsAllowAnyOrigin is the explicit -insecure-origin opt-out, default
	// false — see main.go's flag description for when it's legitimate.
	wsOriginPatterns []string
	wsAllowAnyOrigin bool
}

// newHub constructs a Hub that authorizes WebSocket upgrades against
// originPatterns (see Hub.wsOriginPatterns's doc comment), or against ANY
// origin if allowAnyOrigin is true (-insecure-origin).
func newHub(originPatterns []string, allowAnyOrigin bool) *Hub {
	return &Hub{
		conns:            map[uint64]*websocket.Conn{},
		wsOriginPatterns: originPatterns,
		wsAllowAnyOrigin: allowAnyOrigin,
	}
}

func (h *Hub) add(c *websocket.Conn) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	id := h.nextID
	h.conns[id] = c
	return id
}

func (h *Hub) remove(id uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, id)
}

func (h *Hub) snapshot() map[uint64]*websocket.Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[uint64]*websocket.Conn, len(h.conns))
	for id, c := range h.conns {
		out[id] = c
	}
	return out
}

// broadcastState pushes a `state` message to every connected client and
// updates the cache handleWS uses for a new connection's initial
// snapshot. A client that fails to receive within the timeout is dropped
// — a slow or dead browser tab must never block the game's 1s tick.
//
// Called only from the single-owner loop (main.go) — never from a
// per-connection goroutine.
func (h *Hub) broadcastState(state game.StateMessage) {
	h.mu.Lock()
	h.lastState = state
	h.mu.Unlock()

	for id, c := range h.snapshot() {
		if !h.send(c, state) {
			h.remove(id)
			_ = c.Close(websocket.StatusInternalError, "write failed")
		}
	}
}

// getLastState returns the most recently broadcast state (or the zero
// value if none has been broadcast yet — see setInitialState). Safe to
// call from any goroutine.
func (h *Hub) getLastState() game.StateMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastState
}

// setInitialState seeds the cache before the HTTP server starts accepting
// connections, so the very first client (arriving before the first 1s
// tick) still gets the real loaded/imported state rather than a blank
// zero value. Called once, from main(), before any other goroutine can
// reach the hub.
func (h *Hub) setInitialState(state game.StateMessage) {
	h.mu.Lock()
	h.lastState = state
	h.mu.Unlock()
}

// broadcastFlash pushes a `flash` message to every connected client
// (docs/ui-spec.md doesn't scope flash to the acting client only — it's a
// single-user desktop companion, so every open view shows the same toast).
func (h *Hub) broadcastFlash(f flashMessage) {
	for id, c := range h.snapshot() {
		if !h.send(c, f) {
			h.remove(id)
			_ = c.Close(websocket.StatusInternalError, "write failed")
		}
	}
}

func (h *Hub) send(c *websocket.Conn, v any) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return wsjson.Write(ctx, c, v) == nil
}
