// Package ha — HA(고가용성) 클러스터 관리 서비스
//
// 아키텍처 위치: Go Controller → HA Service → HADriver
//
// 플러그어블 드라이버 패턴을 사용하여 다양한 HA 백엔드를 지원한다:
//   - MemoryDriver: 인메모리 (개발/테스트용)
//   - EtcdDriver: etcd 기반 (분산 노드 등록, 리더 선출, 하트비트)
//
// 핵심 개념:
//   - ClusterNode: 클러스터 구성 노드 (online/offline/fenced 상태)
//   - Quorum: 과반수 노드가 온라인이면 quorum 달성
//   - Fencing: 응답 불가 노드를 격리하는 작업 (IPMI, STONITH 등)
//   - Heartbeat: 주기적 노드 상태 등록 (etcd에 기록)
//
// 클러스터 상태 판단:
//   - healthy: 모든 노드 온라인
//   - degraded: quorum 달성, 일부 노드 오프라인
//   - critical: quorum 미달성
//
// 환경변수:
//   - HCV_HA_DRIVER: 드라이버 선택 (기본값: "memory")
//   - HCV_NODE_NAME: 현재 노드 이름 (미설정 시 hostname 사용)
//
// 스레드 안전성: 드라이버 내부에서 sync.RWMutex로 보호됨
package ha

import (
	"time"
)

// ── 타입 정의 ────────────────────────────────────────

// NodeStatus 는 클러스터 노드의 상태를 나타낸다.
type NodeStatus string

const (
	NodeOnline  NodeStatus = "online"
	NodeOffline NodeStatus = "offline"
	NodeFenced  NodeStatus = "fenced"
)

// ClusterNode 은 HA 클러스터의 노드를 나타낸다.
// IsLeader가 true인 노드가 클러스터 리더이다.
// FenceAgent는 펜싱 방법을 지정한다 (예: "ipmi", "stonith").
type ClusterNode struct {
	Name       string     `json:"name"`
	Status     NodeStatus `json:"status"`
	LastSeen   time.Time  `json:"last_seen"`
	IsLeader   bool       `json:"is_leader"`
	VMCount    int        `json:"vm_count"`
	FenceAgent string     `json:"fence_agent"` // ipmi, stonith, etc.
}

// ClusterStatus 는 HA 클러스터의 전체 상태 요약이다.
// Quorum: 과반수 노드 온라인 여부 (OnlineCount > NodeCount/2)
// Status: "healthy" | "degraded" | "critical"
type ClusterStatus struct {
	Quorum      bool   `json:"quorum"`
	NodeCount   int    `json:"node_count"`
	OnlineCount int    `json:"online_count"`
	Leader      string `json:"leader"`
	Status      string `json:"status"` // healthy, degraded, critical
}

// FenceEvent 는 펜싱 작업의 기록을 나타낸다.
// Action: 펜싱 동작 ("reboot", "off", "on")
type FenceEvent struct {
	ID        string    `json:"id"`
	NodeName  string    `json:"node_name"`
	Reason    string    `json:"reason"`
	Action    string    `json:"action"` // reboot, off, on
	Success   bool      `json:"success"`
	Timestamp time.Time `json:"timestamp"`
}

// ── 서비스 ──────────────────────────────────────────

// Service 는 HA 클러스터 작업을 관리하는 서비스이다.
// 실제 백엔드 작업은 HADriver 인터페이스에 위임한다.
// 동시 호출 안전성: 드라이버 내부에서 보호
type Service struct {
	driver HADriver
}

// NewService 는 기본 인메모리 드라이버로 HA 서비스를 생성한다.
//
// 호출 시점: 개발/테스트 환경에서 사용
func NewService() *Service {
	return NewServiceWithDriver(newMemoryDriver())
}

// NewServiceWithDriver 는 지정된 드라이버로 HA 서비스를 생성한다.
//
// 호출 시점: Controller 초기화 시 설정에 따라 적절한 드라이버를 주입
func NewServiceWithDriver(driver HADriver) *Service {
	return &Service{driver: driver}
}

// GetClusterStatus 는 클러스터 전체 상태를 집계하여 반환한다.
//
// 호출 시점: REST GET /api/v1/cluster/status
// 드라이버 에러 시 Status "unknown"을 반환한다.
// 동시 호출 안전성: 안전
func (s *Service) GetClusterStatus() *ClusterStatus {
	cs, err := s.driver.GetClusterStatus()
	if err != nil {
		return &ClusterStatus{Status: "unknown"}
	}
	return cs
}

// ListNodes 는 모든 클러스터 노드 목록을 반환한다.
//
// 호출 시점: REST GET /api/v1/cluster/nodes, /api/v1/nodes
// 동시 호출 안전성: 안전
func (s *Service) ListNodes() []*ClusterNode {
	nodes, err := s.driver.ListNodes()
	if err != nil {
		return nil
	}
	return nodes
}

// GetNode 는 이름으로 특정 클러스터 노드를 조회한다.
//
// 호출 시점: REST GET /api/v1/nodes/{id}
// 에러 조건: 노드 미존재 (404)
func (s *Service) GetNode(name string) (*ClusterNode, error) {
	return s.driver.GetNode(name)
}

// FenceNode 은 지정된 노드에 대해 펜싱 작업을 시작한다.
//
// 호출 시점: REST POST /api/v1/cluster/fence/{node}
// 부작용: 노드 상태가 "fenced"로 변경됨. EtcdDriver인 경우 etcd에 기록.
// 에러 조건: 노드 미존재
func (s *Service) FenceNode(nodeName, reason, action string) (*FenceEvent, error) {
	return s.driver.FenceNode(nodeName, reason, action)
}

// ListFenceEvents 는 펜싱 이력을 반환한다.
//
// 동시 호출 안전성: 안전
func (s *Service) ListFenceEvents() []FenceEvent {
	events, err := s.driver.ListFenceEvents()
	if err != nil {
		return nil
	}
	return events
}
