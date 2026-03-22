// Package network — 플러그어블 네트워크 백엔드 드라이버 인터페이스
package network

// NetworkDriver 는 플러그어블 네트워크 백엔드를 위한 인터페이스이다.
//
// 구현체:
//   - MemoryDriver: 인메모리 (개발/테스트용, 시스템 변경 없음)
//   - NftablesDriver: nft CLI 기반 (방화벽 규칙만 실제 적용, 존/VNet은 인메모리)
//
// 구현 시 주의사항:
//   - 모든 메서드는 동시 호출에 안전해야 한다 (thread-safe)
//   - 에러 반환 시 래핑된 에러 형식을 사용한다
type NetworkDriver interface {
	// Name 은 드라이버 이름을 반환한다 (예: "memory", "nftables").
	Name() string

	// ListZones 는 SDN 존 목록을 반환한다.
	// 멱등성: 읽기 전용, 부작용 없음
	ListZones() ([]*Zone, error)

	// ListVNets 는 가상 네트워크 목록을 반환한다. zone이 빈 문자열이면 전체 반환.
	// 멱등성: 읽기 전용, 부작용 없음
	ListVNets(zone string) ([]*VNet, error)

	// CreateVNet 은 존에 가상 네트워크를 생성한다.
	// 멱등성: 아님 — 호출할 때마다 새 VNet 생성
	// 에러 조건: 존 미존재
	CreateVNet(zone, name, subnet string, tag int) (*VNet, error)

	// DeleteVNet 은 가상 네트워크를 삭제한다.
	// 에러 조건: VNet 미존재
	DeleteVNet(id string) error

	// ListFirewallRules 는 방화벽 규칙 목록을 반환한다.
	// 멱등성: 읽기 전용, 부작용 없음
	ListFirewallRules() ([]*FirewallRule, error)

	// CreateFirewallRule 은 방화벽 규칙을 생성한다.
	// 멱등성: 아님 — 호출할 때마다 새 규칙 생성
	// 부작용: NftablesDriver는 "nft add rule" 명령 실행
	CreateFirewallRule(direction, action, protocol, source, dest, dport, comment string) (*FirewallRule, error)

	// DeleteFirewallRule 은 방화벽 규칙을 삭제한다.
	// 에러 조건: 규칙 미존재
	DeleteFirewallRule(id string) error
}
