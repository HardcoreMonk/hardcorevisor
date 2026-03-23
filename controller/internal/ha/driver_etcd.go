// etcd 기반 HA 드라이버 — 분산 노드 등록 및 펜싱 영속화
//
// etcd에 노드 상태와 펜싱 이벤트를 저장하여 클러스터 재시작 후에도 유지한다.
// MemoryDriver를 임베딩하여 기본 동작(quorum 계산, 펜싱 로직)을 위임받는다.
//
// etcd 키 규칙:
//   - "ha/nodes/{name}": 노드 상태 (JSON 직렬화된 ClusterNode)
//   - "ha/fence-events/{id}": 펜싱 이벤트 (JSON 직렬화된 FenceEvent)
//
// 폴백: etcd 접근 불가 시 임베딩된 MemoryDriver의 데이터 사용
package ha

import (
	"context"
	"encoding/json"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/store"
)

// EtcdDriver 는 etcd 기반의 분산 HA 상태를 관리하는 드라이버이다.
// MemoryDriver를 임베딩하여 기본 동작을 위임받고,
// etcd에 노드 상태와 펜싱 이벤트를 영속화한다.
type EtcdDriver struct {
	MemoryDriver                    // embed for base behavior
	store          store.Store
	nodeName       string
	leaderElection *LeaderElection
}

// NewEtcdDriver 는 etcd 기반 HA 드라이버를 생성한다.
// 현재 노드를 etcd에 등록하고, 폴백을 위해 MemoryDriver를 임베딩한다.
// etcd 키: "ha/nodes/{nodeName}" (JSON 직렬화된 ClusterNode)
//
// 호출 시점: Controller 초기화 시 HA 드라이버가 "etcd"일 때
func NewEtcdDriver(kvStore store.Store, nodeName string) *EtcdDriver {
	d := &EtcdDriver{
		store:    kvStore,
		nodeName: nodeName,
	}
	d.MemoryDriver = *newMemoryDriver()

	// Register this node in etcd
	ctx := context.Background()
	_ = kvStore.Put(ctx, "ha/nodes/"+nodeName, &ClusterNode{
		Name:       nodeName,
		Status:     NodeOnline,
		LastSeen:   time.Now(),
		FenceAgent: "ipmi",
	})

	return d
}

// Name 은 드라이버 이름 "etcd"를 반환한다.
func (d *EtcdDriver) Name() string { return "etcd" }

// GetClusterStatus 는 etcd에서 노드 상태를 읽어 quorum을 계산한다.
// ListNodes()를 호출하여 노드 목록을 가져온 후 집계한다.
func (d *EtcdDriver) GetClusterStatus() (*ClusterStatus, error) {
	nodes, err := d.ListNodes()
	if err != nil {
		return nil, err
	}

	cs := &ClusterStatus{NodeCount: len(nodes)}
	for _, n := range nodes {
		if n.Status == NodeOnline {
			cs.OnlineCount++
		}
		if n.IsLeader {
			cs.Leader = n.Name
		}
	}
	cs.Quorum = cs.OnlineCount > cs.NodeCount/2
	if cs.OnlineCount == cs.NodeCount {
		cs.Status = "healthy"
	} else if cs.Quorum {
		cs.Status = "degraded"
	} else {
		cs.Status = "critical"
	}
	return cs, nil
}

// ListNodes 는 etcd의 "ha/nodes/" 접두사로 노드 목록을 조회한다.
// etcd 접근 불가 또는 저장된 노드가 없으면 MemoryDriver로 폴백한다.
// JSON 파싱 실패한 항목은 건너뛴다.
func (d *EtcdDriver) ListNodes() ([]*ClusterNode, error) {
	kvs, err := d.store.List(context.Background(), "ha/nodes/")
	if err != nil {
		// Fall back to memory driver
		return d.MemoryDriver.ListNodes()
	}

	// If no nodes stored in etcd yet, fall back to memory driver
	if len(kvs) == 0 {
		return d.MemoryDriver.ListNodes()
	}

	// Parse nodes from stored JSON
	nodes := make([]*ClusterNode, 0, len(kvs))
	for _, kv := range kvs {
		var node ClusterNode
		if err := json.Unmarshal(kv.Value, &node); err != nil {
			continue // skip malformed entries
		}
		nodes = append(nodes, &node)
	}

	if len(nodes) == 0 {
		return d.MemoryDriver.ListNodes()
	}

	return nodes, nil
}

// FenceNode 은 노드를 펜싱하고 이벤트를 etcd에 영속화한다.
//
// 처리 순서:
//  1. MemoryDriver.FenceNode() 호출 (인메모리 상태 변경)
//  2. 펜싱 이벤트를 "ha/fence-events/{id}" 키로 etcd에 저장
//  3. 노드 상태를 "ha/nodes/{name}" 키로 etcd에 갱신 (NodeFenced)
//
// etcd 저장 실패는 무시 (best-effort 영속화)
func (d *EtcdDriver) FenceNode(nodeName, reason, action string) (*FenceEvent, error) {
	event, err := d.MemoryDriver.FenceNode(nodeName, reason, action)
	if err != nil {
		return nil, err
	}

	// Persist fence event to etcd
	ctx := context.Background()
	_ = d.store.Put(ctx, "ha/fence-events/"+event.ID, event)

	// Update node status in etcd
	_ = d.store.Put(ctx, "ha/nodes/"+nodeName, &ClusterNode{
		Name:       nodeName,
		Status:     NodeFenced,
		LastSeen:   time.Now(),
		FenceAgent: "ipmi",
	})

	return event, nil
}

// IsLeader 는 LeaderElection이 설정된 경우 위임하고,
// 그렇지 않으면 true를 반환한다 (단일 노드 폴백).
func (d *EtcdDriver) IsLeader() bool {
	d.mu.RLock()
	le := d.leaderElection
	d.mu.RUnlock()
	if le != nil {
		return le.IsLeader()
	}
	return true
}

// GetLeader 는 LeaderElection이 설정된 경우 위임하고,
// 그렇지 않으면 현재 노드 이름을 반환한다.
func (d *EtcdDriver) GetLeader() (string, error) {
	d.mu.RLock()
	le := d.leaderElection
	d.mu.RUnlock()
	if le != nil {
		return le.GetLeader()
	}
	return d.nodeName, nil
}

// WatchNodes 는 etcd에서 "ha/nodes/" 접두사를 감시하고 콜백을 호출한다.
// etcd 접근 불가 시 no-op으로 동작한다.
func (d *EtcdDriver) WatchNodes(ctx context.Context, callback func(nodeName, status string)) error {
	// In current implementation, delegate to memory driver's no-op watcher
	// since store.Store doesn't expose Watch API directly.
	// Full etcd Watch would require direct clientv3 access.
	return d.MemoryDriver.WatchNodes(ctx, callback)
}

// SetLeaderElection 은 EtcdDriver에 LeaderElection을 연결한다.
func (d *EtcdDriver) SetLeaderElection(le *LeaderElection) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.leaderElection = le
}
