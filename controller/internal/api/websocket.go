// websocket.go — WebSocket 기반 실시간 이벤트 브로드캐스트.
//
// gorilla/websocket 라이브러리를 사용하여 클라이언트에게 실시간 이벤트를 푸시한다.
// EventHub가 연결된 모든 클라이언트를 관리하고, Broadcast()로 이벤트를 전송한다.
//
// # 엔드포인트
//
//	GET /ws → WebSocket 업그레이드 → 이벤트 수신 (읽기 전용)
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocket 업그레이더 — 모든 출처를 허용한다 (개발 환경용).
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Event — WebSocket 클라이언트에게 전송되는 실시간 이벤트.
// Type 예시: "vm_created", "vm_state_changed", "node_status", "backup_created"
type Event struct {
	Type      string    `json:"type"` // vm_created, vm_state_changed, node_status, backup_created
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

// EventHub — WebSocket 연결을 관리하고 이벤트를 브로드캐스트하는 허브.
// RWMutex로 동시 접근을 보호한다 (브로드캐스트 시 RLock, 연결/해제 시 Lock).
type EventHub struct {
	mu      sync.RWMutex
	clients map[*websocket.Conn]bool
}

// NewEventHub — 새 이벤트 허브를 생성한다.
func NewEventHub() *EventHub {
	return &EventHub{
		clients: make(map[*websocket.Conn]bool),
	}
}

// HandleWS — HTTP 요청을 WebSocket으로 업그레이드하고 클라이언트를 등록한다.
// 연결이 유지되는 동안 읽기 루프를 실행하며, 연결 종료 시 자동으로 등록을 해제한다.
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

// Broadcast — 연결된 모든 WebSocket 클라이언트에게 이벤트를 전송한다.
// 개별 클라이언트 전송 실패 시 경고 로그를 남기지만 다른 클라이언트에는 영향을 주지 않는다.
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

// ClientCount — 현재 연결된 WebSocket 클라이언트 수를 반환한다.
func (hub *EventHub) ClientCount() int {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return len(hub.clients)
}
