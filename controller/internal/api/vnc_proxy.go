// vnc_proxy.go — VNC over WebSocket 프록시.
//
// QEMU의 VNC 서버(TCP)를 WebSocket으로 프록시하여
// 브라우저에서 noVNC로 VM 콘솔에 접속할 수 있게 한다.
//
// 엔드포인트: GET /api/v1/vms/{id}/console
// 동작: WebSocket 연결 → TCP(localhost:5900+display) 프록시
package api

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var vncUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleVNCConsole — VM VNC 콘솔 WebSocket 프록시 (GET /api/v1/vms/{id}/console).
//
// 클라이언트가 WebSocket으로 연결하면, 해당 VM의 QEMU VNC 포트(5900+display)에
// TCP 연결을 맺고 양방향으로 데이터를 중계한다.
// noVNC 클라이언트에서 사용한다.
func (svc *Services) handleVNCConsole(w http.ResponseWriter, r *http.Request) {
	handle, err := parseVMID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// VM 조회 → VNC 포트 확인
	vm, err := svc.Compute.GetVM(handle)
	if err != nil {
		http.Error(w, "VM not found", http.StatusNotFound)
		return
	}
	if vm.VNCPort == 0 {
		http.Error(w, "VNC not available for this VM (QEMU Real mode required)", http.StatusBadRequest)
		return
	}

	// VNC TCP 연결
	vncAddr := fmt.Sprintf("127.0.0.1:%d", vm.VNCPort)
	tcpConn, err := net.DialTimeout("tcp", vncAddr, 5*time.Second)
	if err != nil {
		slog.Error("VNC connect failed", "handle", handle, "addr", vncAddr, "error", err)
		http.Error(w, fmt.Sprintf("VNC connection failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	defer tcpConn.Close()

	// WebSocket 업그레이드
	wsConn, err := vncUpgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WebSocket upgrade failed", "handle", handle, "error", err)
		return
	}
	defer wsConn.Close()

	slog.Info("VNC console connected", "handle", handle, "vnc_addr", vncAddr)

	// 양방향 프록시: WebSocket ↔ TCP
	done := make(chan struct{}, 2)

	// TCP → WebSocket (VNC → 브라우저)
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 32*1024)
		for {
			n, err := tcpConn.Read(buf)
			if err != nil {
				return
			}
			if err := wsConn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
				return
			}
		}
	}()

	// WebSocket → TCP (브라우저 → VNC)
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			_, msg, err := wsConn.ReadMessage()
			if err != nil {
				return
			}
			if _, err := tcpConn.Write(msg); err != nil {
				return
			}
		}
	}()

	<-done
	slog.Info("VNC console disconnected", "handle", handle)
}

// Ensure io import is used
var _ = io.EOF
