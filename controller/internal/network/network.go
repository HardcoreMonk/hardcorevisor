// Package network — SDN(소프트웨어 정의 네트워크) 관리 서비스
//
// 아키텍처 위치: Go Controller → Network Service → NetworkDriver
//
// 플러그어블 드라이버 패턴을 사용하여 다양한 네트워크 백엔드를 지원한다:
//   - MemoryDriver: 인메모리 (개발/테스트용)
//   - NftablesDriver: nft CLI 기반 방화벽 규칙 관리
//
// 핵심 개념:
//   - Zone: SDN 존 (VXLAN, VLAN, Simple, EVPN)
//   - VNet: 존 내의 가상 네트워크 (VLAN 태그 또는 VXLAN VNI 포함)
//   - FirewallRule: nftables 스타일 방화벽 규칙 (in/out, accept/drop/reject)
//
// 스레드 안전성: 드라이버 내부에서 sync.RWMutex로 보호됨
package network

// ── 타입 정의 ────────────────────────────────────────

// Zone 은 SDN 존을 나타낸다 (VXLAN, VLAN, Simple 등).
// 존은 네트워크 분리 단위이며, 하나 이상의 VNet을 포함한다.
type Zone struct {
	Name     string `json:"name"`
	ZoneType string `json:"type"`   // vxlan, vlan, simple, evpn
	MTU      int    `json:"mtu"`
	Bridge   string `json:"bridge"`
	Status   string `json:"status"` // active, pending, error
}

// VNet 은 존 내의 가상 네트워크를 나타낸다.
// VLAN 태그 또는 VXLAN VNI로 식별되며, 서브넷을 포함한다.
type VNet struct {
	ID     string `json:"id"`
	Zone   string `json:"zone"`
	Name   string `json:"name"`
	Tag    int    `json:"tag"` // VLAN tag or VXLAN VNI
	Subnet string `json:"subnet"`
	Status string `json:"status"`
}

// FirewallRule 은 nftables 스타일의 방화벽 규칙을 나타낸다.
// Direction: 트래픽 방향 (in=인바운드, out=아웃바운드)
// Action: 규칙 동작 (accept=허용, drop=차단, reject=거부+응답)
type FirewallRule struct {
	ID        string `json:"id"`
	Direction string `json:"direction"` // in, out
	Action    string `json:"action"`    // accept, drop, reject
	Protocol  string `json:"protocol"`  // tcp, udp, icmp
	Source    string `json:"source"`
	Dest     string `json:"dest"`
	DPort    string `json:"dport"`
	Comment  string `json:"comment"`
	Enabled  bool   `json:"enabled"`
}

// ── 서비스 ──────────────────────────────────────────

// Service 는 SDN 존, 가상 네트워크, 방화벽 규칙을 관리하는 서비스이다.
// 모든 작업은 NetworkDriver 인터페이스에 위임한다.
// 동시 호출 안전성: 드라이버 내부에서 보호
type Service struct {
	driver NetworkDriver
}

// NewService 는 기본 인메모리 드라이버로 네트워크 서비스를 생성한다.
//
// 호출 시점: 개발/테스트 환경에서 사용
// 동시 호출 안전성: 안전 (초기화 시 1회 호출)
func NewService() *Service {
	return NewServiceWithDriver(newMemoryDriver())
}

// NewServiceWithDriver 는 지정된 드라이버로 네트워크 서비스를 생성한다.
//
// 호출 시점: Controller 초기화 시 설정에 따라 적절한 드라이버를 주입
// 동시 호출 안전성: 안전 (초기화 시 1회 호출)
func NewServiceWithDriver(driver NetworkDriver) *Service {
	return &Service{driver: driver}
}

// ListZones 는 모든 SDN 존 목록을 반환한다.
//
// 호출 시점: REST GET /api/v1/network/zones
// 동시 호출 안전성: 안전 (드라이버 내부 RLock)
func (s *Service) ListZones() []*Zone {
	zones, _ := s.driver.ListZones()
	return zones
}

// ListVNets 는 가상 네트워크 목록을 반환한다. zone 파라미터로 존별 필터링 가능.
//
// 호출 시점: REST GET /api/v1/network/vnets?zone=
// 동시 호출 안전성: 안전 (드라이버 내부 RLock)
func (s *Service) ListVNets(zone string) []*VNet {
	vnets, _ := s.driver.ListVNets(zone)
	return vnets
}

// CreateVNet 은 지정된 존에 가상 네트워크를 생성한다.
//
// 호출 시점: REST POST /api/v1/network/vnets
// 동시 호출 안전성: 안전 (드라이버 내부 Lock)
func (s *Service) CreateVNet(zone, name, subnet string, tag int) (*VNet, error) {
	return s.driver.CreateVNet(zone, name, subnet, tag)
}

// DeleteVNet 은 ID로 가상 네트워크를 삭제한다.
//
// 호출 시점: REST DELETE /api/v1/network/vnets/{id}
// 동시 호출 안전성: 안전 (드라이버 내부 Lock)
func (s *Service) DeleteVNet(id string) error {
	return s.driver.DeleteVNet(id)
}

// ListFirewallRules 는 모든 방화벽 규칙 목록을 반환한다.
//
// 호출 시점: REST GET /api/v1/network/firewall
// 동시 호출 안전성: 안전 (드라이버 내부 RLock)
func (s *Service) ListFirewallRules() []*FirewallRule {
	rules, _ := s.driver.ListFirewallRules()
	return rules
}

// CreateFirewallRule 은 새 방화벽 규칙을 추가한다.
//
// 호출 시점: REST POST /api/v1/network/firewall
// 동시 호출 안전성: 안전 (드라이버 내부 Lock)
// 부작용: NftablesDriver인 경우 "nft add rule" 명령 실행
func (s *Service) CreateFirewallRule(direction, action, protocol, source, dest, dport, comment string) (*FirewallRule, error) {
	return s.driver.CreateFirewallRule(direction, action, protocol, source, dest, dport, comment)
}

// DeleteFirewallRule 은 ID로 방화벽 규칙을 삭제한다.
//
// 호출 시점: REST DELETE /api/v1/network/firewall/{id}
// 동시 호출 안전성: 안전 (드라이버 내부 Lock)
func (s *Service) DeleteFirewallRule(id string) error {
	return s.driver.DeleteFirewallRule(id)
}

// CreateZone 은 새 SDN 존을 생성한다.
//
// 호출 시점: REST POST /api/v1/network/zones
// 동시 호출 안전성: 안전 (드라이버 내부 Lock)
func (s *Service) CreateZone(zone *Zone) error {
	return s.driver.CreateZone(zone)
}

// DeleteZone 은 이름으로 SDN 존을 삭제한다.
//
// 호출 시점: REST DELETE /api/v1/network/zones/{name}
// 동시 호출 안전성: 안전 (드라이버 내부 Lock)
func (s *Service) DeleteZone(name string) error {
	return s.driver.DeleteZone(name)
}
