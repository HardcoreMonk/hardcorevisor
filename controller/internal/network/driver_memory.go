// 인메모리 네트워크 드라이버 — 개발/테스트 전용
//
// 존, VNet, 방화벽 규칙을 Go 맵에 저장한다.
// 프로세스 재시작 시 데이터가 소실되며, 시스템에 아무런 변경을 가하지 않는다.
// NftablesDriver의 기반 드라이버로도 임베딩되어 사용된다.
package network

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// MemoryDriver 는 인메모리 네트워크 드라이버로, 개발/테스트 환경에서 사용한다.
// NetworkDriver 인터페이스를 구현하며, 외부 의존성이 없다.
// NftablesDriver가 이 구조체를 임베딩하여 존/VNet 관리를 위임받는다.
// 기본 존 2개(vxlan-zone, simple-zone)와 VNet 2개가 미리 생성된다.
type MemoryDriver struct {
	mu         sync.RWMutex
	zones      map[string]*Zone
	vnets      map[string]*VNet
	rules      map[string]*FirewallRule
	nextVNetID atomic.Int32
	nextRuleID atomic.Int32
}

// newMemoryDriver 는 기본 존과 VNet이 포함된 MemoryDriver를 생성한다.
//
// 기본 존:
//   - vxlan-zone: VXLAN 존 (MTU 1450, vmbr1 브릿지)
//   - simple-zone: Simple 존 (MTU 1500, vmbr0 브릿지)
//
// 기본 VNet:
//   - vnet-1: prod-network (vxlan-zone, VNI 100, 10.0.1.0/24)
//   - vnet-2: mgmt-network (simple-zone, VLAN 1, 192.168.1.0/24)
func newMemoryDriver() *MemoryDriver {
	d := &MemoryDriver{
		zones: make(map[string]*Zone),
		vnets: make(map[string]*VNet),
		rules: make(map[string]*FirewallRule),
	}
	d.nextVNetID.Store(1)
	d.nextRuleID.Store(1)

	// Default zones
	d.zones["vxlan-zone"] = &Zone{
		Name: "vxlan-zone", ZoneType: "vxlan", MTU: 1450,
		Bridge: "vmbr1", Status: "active",
	}
	d.zones["simple-zone"] = &Zone{
		Name: "simple-zone", ZoneType: "simple", MTU: 1500,
		Bridge: "vmbr0", Status: "active",
	}

	// Default VNets
	d.vnets["vnet-1"] = &VNet{
		ID: "vnet-1", Zone: "vxlan-zone", Name: "prod-network",
		Tag: 100, Subnet: "10.0.1.0/24", Status: "active",
	}
	d.vnets["vnet-2"] = &VNet{
		ID: "vnet-2", Zone: "simple-zone", Name: "mgmt-network",
		Tag: 1, Subnet: "192.168.1.0/24", Status: "active",
	}

	return d
}

// Name 은 드라이버 이름 "memory"를 반환한다.
func (d *MemoryDriver) Name() string { return "memory" }

// ListZones 는 인메모리에 저장된 모든 존을 반환한다.
func (d *MemoryDriver) ListZones() ([]*Zone, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]*Zone, 0, len(d.zones))
	for _, z := range d.zones {
		result = append(result, z)
	}
	return result, nil
}

// ListVNets 는 인메모리 VNet 목록을 반환한다. zone이 빈 문자열이면 전체 반환.
func (d *MemoryDriver) ListVNets(zone string) ([]*VNet, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]*VNet, 0)
	for _, v := range d.vnets {
		if zone == "" || v.Zone == zone {
			result = append(result, v)
		}
	}
	return result, nil
}

// CreateVNet 은 인메모리에 VNet을 생성한다. 존 미존재 시 에러 반환.
func (d *MemoryDriver) CreateVNet(zone, name, subnet string, tag int) (*VNet, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.zones[zone]; !ok {
		return nil, fmt.Errorf("zone not found: %s", zone)
	}
	id := fmt.Sprintf("vnet-%d", d.nextVNetID.Add(1)-1)
	vnet := &VNet{
		ID: id, Zone: zone, Name: name,
		Tag: tag, Subnet: subnet, Status: "active",
	}
	d.vnets[id] = vnet
	return vnet, nil
}

// DeleteVNet 은 인메모리에서 VNet을 삭제한다. 미존재 시 에러 반환.
func (d *MemoryDriver) DeleteVNet(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.vnets[id]; !ok {
		return fmt.Errorf("vnet not found: %s", id)
	}
	delete(d.vnets, id)
	return nil
}

// ListFirewallRules 는 인메모리 방화벽 규칙 목록을 반환한다.
func (d *MemoryDriver) ListFirewallRules() ([]*FirewallRule, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]*FirewallRule, 0, len(d.rules))
	for _, r := range d.rules {
		result = append(result, r)
	}
	return result, nil
}

// CreateFirewallRule 은 인메모리에 방화벽 규칙을 생성한다.
// 새 규칙은 기본적으로 Enabled=true로 설정된다.
func (d *MemoryDriver) CreateFirewallRule(direction, action, protocol, source, dest, dport, comment string) (*FirewallRule, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := fmt.Sprintf("rule-%d", d.nextRuleID.Add(1)-1)
	rule := &FirewallRule{
		ID: id, Direction: direction, Action: action,
		Protocol: protocol, Source: source, Dest: dest,
		DPort: dport, Comment: comment, Enabled: true,
	}
	d.rules[id] = rule
	return rule, nil
}

// DeleteFirewallRule 은 인메모리에서 방화벽 규칙을 삭제한다. 미존재 시 에러 반환.
func (d *MemoryDriver) DeleteFirewallRule(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.rules[id]; !ok {
		return fmt.Errorf("rule not found: %s", id)
	}
	delete(d.rules, id)
	return nil
}

// CreateZone 은 인메모리에 SDN 존을 생성한다. 이름 중복 시 에러 반환.
func (d *MemoryDriver) CreateZone(zone *Zone) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.zones[zone.Name]; ok {
		return fmt.Errorf("zone already exists: %s", zone.Name)
	}
	zone.Status = "active"
	d.zones[zone.Name] = zone
	return nil
}

// DeleteZone 은 인메모리에서 SDN 존을 삭제한다. 미존재 시 에러 반환.
func (d *MemoryDriver) DeleteZone(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.zones[name]; !ok {
		return fmt.Errorf("zone not found: %s", name)
	}
	delete(d.zones, name)
	return nil
}
