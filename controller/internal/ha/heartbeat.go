// 하트비트 — 주기적 노드 상태 등록
//
// 지정된 간격으로 etcd에 노드 상태를 등록하여 클러스터 멤버십을 유지한다.
// etcd 키: "ha/nodes/{nodeName}" (JSON 직렬화된 ClusterNode)
//
// 환경변수:
//   - HCV_NODE_NAME: 노드 이름 (미설정 시 os.Hostname() 사용)
package ha

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/store"
)

// Heartbeat 는 주기적으로 etcd에 노드 상태를 등록하는 관리자이다.
// Start()로 백그라운드 고루틴을 시작하고, Stop()으로 중지한다.
type Heartbeat struct {
	store    store.Store
	nodeName string
	interval time.Duration
	cancel   context.CancelFunc
}

// NewHeartbeat 는 하트비트 관리자를 생성한다.
// interval은 하트비트 전송 간격 (권장: 5~10초).
//
// 호출 시점: Controller 초기화 시 HA 드라이버가 "etcd"일 때
func NewHeartbeat(kvStore store.Store, nodeName string, interval time.Duration) *Heartbeat {
	return &Heartbeat{
		store:    kvStore,
		nodeName: nodeName,
		interval: interval,
	}
}

// Start 는 백그라운드 고루틴에서 주기적 하트비트 등록을 시작한다.
// 초기 등록을 즉시 수행한 후, interval마다 반복한다.
// 동시 호출 안전성: 여러 번 호출하면 여러 고루틴이 생성되므로 1회만 호출해야 한다.
func (h *Heartbeat) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel

	go func() {
		ticker := time.NewTicker(h.interval)
		defer ticker.Stop()

		// Initial registration
		h.register(ctx)

		for {
			select {
			case <-ticker.C:
				h.register(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()

	slog.Info("heartbeat started", "node", h.nodeName, "interval", h.interval)
}

// Stop 은 하트비트를 중지한다. 백그라운드 고루틴이 종료된다.
func (h *Heartbeat) Stop() {
	if h.cancel != nil {
		h.cancel()
	}
}

// register 는 etcd에 현재 노드 상태를 등록한다.
// etcd 키: "ha/nodes/{nodeName}", 값: ClusterNode (Status=online, LastSeen=현재시각)
// 등록 실패 시 경고 로그를 출력하지만 중단하지 않는다.
func (h *Heartbeat) register(ctx context.Context) {
	node := &ClusterNode{
		Name:     h.nodeName,
		Status:   NodeOnline,
		LastSeen: time.Now(),
	}

	if err := h.store.Put(ctx, "ha/nodes/"+h.nodeName, node); err != nil {
		slog.Warn("heartbeat registration failed", "node", h.nodeName, "error", err)
	}
}

// GetNodeName 은 현재 노드 이름을 반환한다.
// 우선순위: HCV_NODE_NAME 환경변수 > os.Hostname() > "unknown"
func GetNodeName() string {
	if name := os.Getenv("HCV_NODE_NAME"); name != "" {
		return name
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}
