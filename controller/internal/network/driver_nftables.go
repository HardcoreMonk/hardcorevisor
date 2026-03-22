// nftables 방화벽 드라이버 — nft CLI 기반
//
// 방화벽 규칙만 nft 명령으로 실제 적용하며,
// 존과 VNet 관리는 임베딩된 MemoryDriver에 위임한다.
//
// nftables 테이블 구조:
//   - 테이블: inet hcv_filter (자동 생성)
//   - 체인: hcv_forward (type filter, hook forward, priority 0, policy accept)
//
// 외부 명령 실행:
//   - "nft add table": 테이블 생성
//   - "nft add chain": 체인 생성
//   - "nft add rule": 규칙 추가
//
// 주의: root 권한이 필요할 수 있다. 권한 부족 시 인메모리에만 저장 (best-effort).
package network

import (
	"fmt"
	"os/exec"
	"strings"
)

// NftablesDriver 는 nft CLI를 통해 방화벽 규칙을 관리하는 드라이버이다.
// MemoryDriver를 임베딩하여 존/VNet은 인메모리로 관리하고,
// 방화벽 규칙만 nft 명령으로 실제 시스템에 적용한다.
// nft 실행 실패 시에도 인메모리에는 규칙이 저장된다 (Enabled=false로 표시).
type NftablesDriver struct {
	MemoryDriver // embed for zones/vnets
	tableName    string
	chainName    string
}

// NewNftablesDriver 는 기본 테이블/체인 이름으로 NftablesDriver를 생성한다.
// 기본값: 테이블 "hcv_filter", 체인 "hcv_forward"
//
// 호출 시점: Controller 초기화 시 nftables 드라이버 선택 시
func NewNftablesDriver() *NftablesDriver {
	d := &NftablesDriver{
		tableName: "hcv_filter",
		chainName: "hcv_forward",
	}
	d.MemoryDriver = *newMemoryDriver()
	return d
}

// Name 은 드라이버 이름 "nftables"를 반환한다.
func (d *NftablesDriver) Name() string { return "nftables" }

// ensureTable 은 nftables 테이블과 체인이 없으면 생성한다.
// 이미 존재하는 경우 에러를 무시한다 (멱등).
// 부작용: "nft add table/chain" 명령 실행 (시스템 방화벽 변경)
func (d *NftablesDriver) ensureTable() error {
	cmds := []string{
		fmt.Sprintf("add table inet %s", d.tableName),
		fmt.Sprintf("add chain inet %s %s { type filter hook forward priority 0 ; policy accept ; }", d.tableName, d.chainName),
	}
	for _, cmd := range cmds {
		exec.Command("nft", strings.Fields(cmd)...).Run() // ignore errors (may already exist)
	}
	return nil
}

// CreateFirewallRule 은 방화벽 규칙을 인메모리에 저장한 후 nft 명령으로 적용한다.
//
// 처리 순서:
//  1. MemoryDriver에 규칙 생성 (인메모리 저장)
//  2. ensureTable()로 테이블/체인 존재 확인
//  3. "nft add rule" 명령으로 실제 적용
//
// nft 실행 실패 시에도 에러를 반환하지 않으며, 규칙의 Enabled를 false로 표시한다.
// 부작용: 성공 시 시스템 방화벽 규칙 추가
func (d *NftablesDriver) CreateFirewallRule(direction, action, protocol, source, dest, dport, comment string) (*FirewallRule, error) {
	// Create in memory first
	rule, err := d.MemoryDriver.CreateFirewallRule(direction, action, protocol, source, dest, dport, comment)
	if err != nil {
		return nil, err
	}

	// Apply via nft (best effort -- may fail without permissions)
	nftRule := buildNftRule(direction, action, protocol, source, dest, dport, comment)
	if err := d.ensureTable(); err != nil {
		// Log warning but don't fail
		return rule, nil
	}
	cmd := fmt.Sprintf("add rule inet %s %s %s", d.tableName, d.chainName, nftRule)
	if out, err := exec.Command("nft", strings.Fields(cmd)...).CombinedOutput(); err != nil {
		// Log but don't fail -- rule exists in memory even if nft fails
		rule.Enabled = false // mark as not applied
		_ = out
	}

	return rule, nil
}

// buildNftRule 은 구성 요소들로 nftables 규칙 문자열을 조립한다.
// 예: "tcp ip saddr 10.0.0.0/8 ip daddr 192.168.1.0/24 dport 80 accept"
func buildNftRule(direction, action, protocol, source, dest, dport, comment string) string {
	var parts []string
	if protocol != "" {
		parts = append(parts, protocol)
	}
	if source != "" {
		parts = append(parts, "ip saddr", source)
	}
	if dest != "" {
		parts = append(parts, "ip daddr", dest)
	}
	if dport != "" {
		parts = append(parts, "dport", dport)
	}
	parts = append(parts, action)
	if comment != "" {
		parts = append(parts, fmt.Sprintf("comment \"%s\"", comment))
	}
	return strings.Join(parts, " ")
}

// DeleteFirewallRule 은 인메모리에서 방화벽 규칙을 삭제한다.
//
// 주의: nftables에서 실제 규칙을 삭제하려면 handle 번호가 필요하지만,
// 현재 handle 번호를 추적하지 않는다.
// 프로덕션에서는 flush 후 전체 규칙을 재적용하는 방식이 필요하다.
func (d *NftablesDriver) DeleteFirewallRule(id string) error {
	// Delete from memory
	if err := d.MemoryDriver.DeleteFirewallRule(id); err != nil {
		return err
	}
	return nil
}
