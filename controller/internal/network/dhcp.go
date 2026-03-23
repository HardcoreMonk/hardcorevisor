// dhcp.go — 경량 DHCP/IP 풀 관리
//
// DHCPManager는 IP 풀과 리스 정보를 인메모리로 관리한다.
// 실제 DHCP 서버를 구동하지 않으며, IP 할당/해제와 dnsmasq 설정 생성을 담당한다.
//
// 외부 의존성 없음: 모든 상태를 Go 맵에 저장한다.
// 프로덕션에서는 GenerateDnsmasqConfig()로 생성된 설정을 dnsmasq에 전달한다.
package network

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// Lease 는 IP 리스 정보를 나타낸다.
type Lease struct {
	IP        net.IP    `json:"ip"`
	MAC       string    `json:"mac,omitempty"`
	Hostname  string    `json:"hostname,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ipPool 은 하나의 IP 풀을 나타낸다.
type ipPool struct {
	subnet     string
	gateway    string
	rangeStart net.IP
	rangeEnd   net.IP
	allocated  map[string]bool // IP string → allocated
	leases     []Lease
}

// DHCPManager 는 IP 풀과 리스를 관리하는 구조체이다.
// 동시 호출 안전성: sync.Mutex로 보호됨.
type DHCPManager struct {
	mu    sync.Mutex
	pools map[string]*ipPool
}

// NewDHCPManager 는 빈 풀로 DHCPManager를 생성한다.
func NewDHCPManager() *DHCPManager {
	return &DHCPManager{
		pools: make(map[string]*ipPool),
	}
}

// AddPool 은 새 IP 풀을 등록한다.
//
// # 매개변수
//   - poolName: 풀 이름 (고유 식별자)
//   - subnet: 서브넷 CIDR (예: "10.0.1.0/24")
//   - gateway: 게이트웨이 IP (예: "10.0.1.1")
//   - rangeStart: 할당 시작 IP (예: net.ParseIP("10.0.1.10"))
//   - rangeEnd: 할당 종료 IP (예: net.ParseIP("10.0.1.200"))
//
// 에러 조건: 풀 이름 중복
func (d *DHCPManager) AddPool(poolName, subnet, gateway string, rangeStart, rangeEnd net.IP) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.pools[poolName]; ok {
		return fmt.Errorf("pool already exists: %s", poolName)
	}

	d.pools[poolName] = &ipPool{
		subnet:     subnet,
		gateway:    gateway,
		rangeStart: rangeStart,
		rangeEnd:   rangeEnd,
		allocated:  make(map[string]bool),
		leases:     make([]Lease, 0),
	}
	return nil
}

// AllocateIP 는 풀에서 다음 사용 가능한 IP를 할당한다.
//
// 할당 전략: rangeStart부터 rangeEnd까지 순차 탐색하여 미할당 IP를 반환한다.
//
// 에러 조건: 풀 미존재, 풀 고갈 (모든 IP 할당됨)
func (d *DHCPManager) AllocateIP(poolName string) (net.IP, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	pool, ok := d.pools[poolName]
	if !ok {
		return nil, fmt.Errorf("pool not found: %s", poolName)
	}

	current := make(net.IP, len(pool.rangeStart))
	copy(current, pool.rangeStart)

	for {
		ipStr := current.String()
		if !pool.allocated[ipStr] {
			pool.allocated[ipStr] = true
			allocatedIP := make(net.IP, len(current))
			copy(allocatedIP, current)
			pool.leases = append(pool.leases, Lease{
				IP:        allocatedIP,
				ExpiresAt: time.Now().Add(24 * time.Hour),
			})
			return allocatedIP, nil
		}

		// Increment IP
		incrementIP(current)

		// Check if we've exceeded the range end
		if compareIP(current, pool.rangeEnd) > 0 {
			return nil, fmt.Errorf("pool %s exhausted: no available IPs", poolName)
		}
	}
}

// ReleaseIP 는 할당된 IP를 풀에 반환한다.
//
// 에러 조건: 풀 미존재, IP가 할당되지 않은 경우
func (d *DHCPManager) ReleaseIP(poolName string, ip net.IP) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	pool, ok := d.pools[poolName]
	if !ok {
		return fmt.Errorf("pool not found: %s", poolName)
	}

	ipStr := ip.String()
	if !pool.allocated[ipStr] {
		return fmt.Errorf("IP not allocated: %s", ipStr)
	}

	delete(pool.allocated, ipStr)

	// Remove lease
	newLeases := make([]Lease, 0, len(pool.leases))
	for _, l := range pool.leases {
		if l.IP.String() != ipStr {
			newLeases = append(newLeases, l)
		}
	}
	pool.leases = newLeases

	return nil
}

// ListLeases 는 지정된 풀의 활성 리스 목록을 반환한다.
// 풀이 존재하지 않으면 빈 슬라이스를 반환한다.
func (d *DHCPManager) ListLeases(poolName string) []Lease {
	d.mu.Lock()
	defer d.mu.Unlock()

	pool, ok := d.pools[poolName]
	if !ok {
		return nil
	}

	result := make([]Lease, len(pool.leases))
	copy(result, pool.leases)
	return result
}

// GenerateDnsmasqConfig 는 dnsmasq 설정 스니펫을 생성한다.
//
// 생성되는 설정:
//   - interface: 브릿지 인터페이스 이름
//   - dhcp-range: IP 범위와 리스 시간
//   - dhcp-option: 게이트웨이 주소
//   - bind-interfaces: 인터페이스 바인딩
//
// 풀이 존재하지 않으면 빈 문자열을 반환한다.
func (d *DHCPManager) GenerateDnsmasqConfig(poolName, bridge string) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	pool, ok := d.pools[poolName]
	if !ok {
		return ""
	}

	return fmt.Sprintf(`interface=%s
dhcp-range=%s,%s,24h
dhcp-option=option:router,%s
bind-interfaces
`, bridge, pool.rangeStart.String(), pool.rangeEnd.String(), pool.gateway)
}

// incrementIP 는 IP 주소를 1 증가시킨다.
func incrementIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}

// compareIP 는 두 IP를 비교한다.
// a < b이면 -1, a == b이면 0, a > b이면 1을 반환한다.
func compareIP(a, b net.IP) int {
	a4 := a.To4()
	b4 := b.To4()
	if a4 != nil && b4 != nil {
		for i := 0; i < 4; i++ {
			if a4[i] < b4[i] {
				return -1
			}
			if a4[i] > b4[i] {
				return 1
			}
		}
		return 0
	}
	// Fallback for IPv6 or mixed
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}
