package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true }, // Allow all origins
}

// Event represents a real-time event pushed to WebSocket clients.
type Event struct {
	Type      string    `json:"type"` // vm_created, vm_state_changed, node_status, backup_created
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

// EventHub manages WebSocket connections and broadcasts events.
type EventHub struct {
	mu      sync.RWMutex
	clients map[*websocket.Conn]bool
}

// NewEventHub creates an event hub.
func NewEventHub() *EventHub {
	return &EventHub{
		clients: make(map[*websocket.Conn]bool),
	}
}

// HandleWS upgrades HTTP to WebSocket and registers the client.
func (hub *EventHub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err)
		return
	}

	hub.mu.Lock()
	hub.clients[conn] = true
	hub.mu.Unlock()

	slog.Info("websocket client connected", "remote", conn.RemoteAddr())

	// Read loop (keep connection alive, handle close)
	defer func() {
		hub.mu.Lock()
		delete(hub.clients, conn)
		hub.mu.Unlock()
		conn.Close()
		slog.Info("websocket client disconnected", "remote", conn.RemoteAddr())
	}()

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

// Broadcast sends an event to all connected clients.
func (hub *EventHub) Broadcast(event Event) {
	hub.mu.RLock()
	defer hub.mu.RUnlock()

	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	for conn := range hub.clients {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			slog.Warn("websocket write failed", "error", err)
		}
	}
}

// ClientCount returns the number of connected clients.
func (hub *EventHub) ClientCount() int {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return len(hub.clients)
}
