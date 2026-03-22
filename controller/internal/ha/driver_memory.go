// 인메모리 HA 드라이버 — 개발/테스트 전용
//
// 3개의 기본 노드(node-01~03)를 제공하며,
// 펜싱 이벤트를 인메모리 슬라이스에 기록한다.
// EtcdDriver의 기반 드라이버로도 임베딩되어 사용된다.
package ha

import (
	"fmt"
	"sync"
	"time"
)

// MemoryDriver 는 인메모리 상태로 HADriver를 구현한다.
// EtcdDriver가 이 구조체를 임베딩하여 기본 동작을 위임받는다.
type MemoryDriver struct {
	mu          sync.RWMutex
	nodes       map[string]*ClusterNode
	fenceEvents []FenceEvent
	fenceNextID int
}

// newMemoryDriver 는 기본 클러스터 노드가 포함된 MemoryDriver를 생성한다.
//
// 기본 노드:
//   - node-01: 온라인, 리더, VM 2개, IPMI 펜스 에이전트
//   - node-02: 온라인, VM 1개, IPMI 펜스 에이전트
//   - node-03: 온라인, VM 0개, IPMI 펜스 에이전트
func newMemoryDriver() *MemoryDriver {
	now := time.Now()
	d := &MemoryDriver{
		nodes:       make(map[string]*ClusterNode),
		fenceEvents: make([]FenceEvent, 0),
		fenceNextID: 1,
	}

	d.nodes["node-01"] = &ClusterNode{
		Name: "node-01", Status: NodeOnline, LastSeen: now,
		IsLeader: true, VMCount: 2, FenceAgent: "ipmi",
	}
	d.nodes["node-02"] = &ClusterNode{
		Name: "node-02", Status: NodeOnline, LastSeen: now,
		IsLeader: false, VMCount: 1, FenceAgent: "ipmi",
	}
	d.nodes["node-03"] = &ClusterNode{
		Name: "node-03", Status: NodeOnline, LastSeen: now,
		IsLeader: false, VMCount: 0, FenceAgent: "ipmi",
	}

	return d
}

// Name 은 드라이버 이름 "memory"를 반환한다.
func (d *MemoryDriver) Name() string { return "memory" }

// GetClusterStatus 는 클러스터 전체 상태를 집계한다.
// quorum 판단: OnlineCount > NodeCount/2 (과반수)
// 상태 판단: 전체 온라인→healthy, quorum→degraded, 그 외→critical
func (d *MemoryDriver) GetClusterStatus() (*ClusterStatus, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	cs := &ClusterStatus{NodeCount: len(d.nodes)}
	for _, n := range d.nodes {
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

// ListNodes 는 인메모리에 저장된 모든 클러스터 노드를 반환한다.
func (d *MemoryDriver) ListNodes() ([]*ClusterNode, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]*ClusterNode, 0, len(d.nodes))
	for _, n := range d.nodes {
		result = append(result, n)
	}
	return result, nil
}

// FenceNode 은 지정된 노드에 펜싱을 수행한다.
// 노드 상태를 NodeFenced로 변경하고 펜싱 이벤트를 기록한다.
// 에러 조건: 노드 미존재
func (d *MemoryDriver) FenceNode(nodeName, reason, action string) (*FenceEvent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	node, ok := d.nodes[nodeName]
	if !ok {
		return nil, fmt.Errorf("node not found: %s", nodeName)
	}

	event := FenceEvent{
		ID:        fmt.Sprintf("fence-%d", d.fenceNextID),
		NodeName:  nodeName,
		Reason:    reason,
		Action:    action,
		Success:   true,
		Timestamp: time.Now(),
	}
	d.fenceNextID++
	d.fenceEvents = append(d.fenceEvents, event)

	node.Status = NodeFenced
	return &event, nil
}

// ListFenceEvents 는 인메모리 펜싱 이벤트 이력을 반환한다.
// 슬라이스 복사본을 반환하여 외부 수정으로부터 보호한다.
func (d *MemoryDriver) ListFenceEvents() ([]FenceEvent, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]FenceEvent, len(d.fenceEvents))
	copy(result, d.fenceEvents)
	return result, nil
}
