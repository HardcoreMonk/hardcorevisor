// Package ha — 플러그어블 HA 백엔드 드라이버 인터페이스
package ha

// HADriver 는 플러그어블 HA 백엔드를 위한 인터페이스이다.
//
// 구현체:
//   - MemoryDriver: 인메모리 (개발/테스트용)
//   - EtcdDriver: etcd 기반 (분산 노드 등록, 하트비트, 펜싱 이벤트 영속화)
//
// 구현 시 주의사항:
//   - 모든 메서드는 동시 호출에 안전해야 한다 (thread-safe)
//   - FenceNode는 상태 변경 메서드 — 노드 상태를 "fenced"로 변경
type HADriver interface {
	// Name 은 드라이버 이름을 반환한다 (예: "memory", "etcd").
	Name() string

	// GetClusterStatus 는 클러스터 전체 상태를 집계하여 반환한다.
	// quorum 계산: OnlineCount > NodeCount/2
	// 멱등성: 읽기 전용
	GetClusterStatus() (*ClusterStatus, error)

	// ListNodes 는 클러스터 노드 목록을 반환한다.
	// 멱등성: 읽기 전용
	ListNodes() ([]*ClusterNode, error)

	// FenceNode 는 지정된 노드에 펜싱을 수행한다.
	// 멱등성: 아님 — 노드 상태를 "fenced"로 변경하고 이벤트를 기록
	// 부작용: 노드 상태 변경, EtcdDriver는 etcd에 영속화
	// 에러 조건: 노드 미존재
	FenceNode(nodeName, reason, action string) (*FenceEvent, error)

	// ListFenceEvents 는 펜싱 이벤트 이력을 반환한다.
	// 멱등성: 읽기 전용
	ListFenceEvents() ([]FenceEvent, error)
}
