// Linux bridge + veth 관리 — ip CLI 기반
//
// BridgeManager는 Linux 네트워크 브릿지와 veth 페어를 관리한다.
// "ip" 명령어를 exec.Command로 실행하므로 시스템에 iproute2가 설치되어 있어야 한다.
//
// 외부 명령 실행:
//   - "ip link add": 브릿지 또는 veth 페어 생성
//   - "ip link set": 인터페이스 활성화, 마스터 설정
//   - "ip link delete": 인터페이스 삭제
//   - "ip addr add": 브릿지에 IP 주소 할당
//
// 테스트 가능성: CommandRunner 인터페이스를 통해 실제 명령 실행을 모킹할 수 있다.
package network

import (
	"fmt"
	"os/exec"
)

// CommandRunner 는 시스템 명령 실행을 추상화하는 인터페이스이다.
// 테스트에서는 MockCommandRunner로 교체하여 실제 명령 실행 없이
// 명령 구성만 검증할 수 있다.
type CommandRunner interface {
	// Run 은 지정된 명령과 인자를 실행한다.
	// 실행 성공 시 nil, 실패 시 에러를 반환한다.
	Run(name string, args ...string) error
}

// ExecCommandRunner 는 실제 os/exec.Command를 사용하는 CommandRunner 구현체이다.
type ExecCommandRunner struct{}

// Run 은 exec.Command로 명령을 실행한다.
func (r *ExecCommandRunner) Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %s: %w", name, args, string(out), err)
	}
	return nil
}

// BridgeManager 는 Linux bridge와 veth 페어를 관리하는 구조체이다.
// CommandRunner를 통해 시스템 명령을 실행하므로 테스트에서 모킹이 가능하다.
type BridgeManager struct {
	runner CommandRunner
}

// NewBridgeManager 는 기본 ExecCommandRunner를 사용하는 BridgeManager를 생성한다.
func NewBridgeManager() *BridgeManager {
	return &BridgeManager{runner: &ExecCommandRunner{}}
}

// NewBridgeManagerWithRunner 는 지정된 CommandRunner를 사용하는 BridgeManager를 생성한다.
// 테스트에서 MockCommandRunner를 주입할 때 사용한다.
func NewBridgeManagerWithRunner(runner CommandRunner) *BridgeManager {
	return &BridgeManager{runner: runner}
}

// CreateBridge 는 Linux 브릿지를 생성하고 활성화한다.
//
// 실행 명령:
//
//	ip link add name {name} type bridge
//	ip link set {name} up
//
// 에러 조건: ip 명령 실행 실패, 권한 부족, 이름 충돌
func (m *BridgeManager) CreateBridge(name string) error {
	if err := m.runner.Run("ip", "link", "add", "name", name, "type", "bridge"); err != nil {
		return fmt.Errorf("create bridge %s: %w", name, err)
	}
	if err := m.runner.Run("ip", "link", "set", name, "up"); err != nil {
		return fmt.Errorf("bring up bridge %s: %w", name, err)
	}
	return nil
}

// DeleteBridge 는 Linux 브릿지를 삭제한다.
//
// 실행 명령:
//
//	ip link delete {name}
//
// 에러 조건: 존재하지 않는 브릿지, 권한 부족
func (m *BridgeManager) DeleteBridge(name string) error {
	if err := m.runner.Run("ip", "link", "delete", name); err != nil {
		return fmt.Errorf("delete bridge %s: %w", name, err)
	}
	return nil
}

// AddPort 는 네트워크 인터페이스를 브릿지에 연결한다.
//
// 실행 명령:
//
//	ip link set {iface} master {bridge}
//
// 에러 조건: 브릿지 또는 인터페이스 미존재, 권한 부족
func (m *BridgeManager) AddPort(bridge, iface string) error {
	if err := m.runner.Run("ip", "link", "set", iface, "master", bridge); err != nil {
		return fmt.Errorf("add port %s to bridge %s: %w", iface, bridge, err)
	}
	return nil
}

// SetBridgeIP 는 브릿지에 IP 주소를 할당한다.
//
// 실행 명령:
//
//	ip addr add {cidr} dev {bridge}
//
// 에러 조건: 브릿지 미존재, 잘못된 CIDR 형식, 권한 부족
func (m *BridgeManager) SetBridgeIP(bridge, cidr string) error {
	if err := m.runner.Run("ip", "addr", "add", cidr, "dev", bridge); err != nil {
		return fmt.Errorf("set IP %s on bridge %s: %w", cidr, bridge, err)
	}
	return nil
}

// CreateVethPair 는 veth 페어를 생성한다.
// 호스트 측 이름: {name}-h, 컨테이너/VM 측 이름: {name}-c
//
// 실행 명령:
//
//	ip link add {name}-h type veth peer name {name}-c
//	ip link set {name}-h up
//	ip link set {name}-c up
//
// 반환값: (hostEnd, containerEnd, error)
func (m *BridgeManager) CreateVethPair(name string) (hostEnd, containerEnd string, err error) {
	hostEnd = name + "-h"
	containerEnd = name + "-c"

	if err := m.runner.Run("ip", "link", "add", hostEnd, "type", "veth", "peer", "name", containerEnd); err != nil {
		return "", "", fmt.Errorf("create veth pair %s: %w", name, err)
	}
	if err := m.runner.Run("ip", "link", "set", hostEnd, "up"); err != nil {
		return "", "", fmt.Errorf("bring up veth %s: %w", hostEnd, err)
	}
	if err := m.runner.Run("ip", "link", "set", containerEnd, "up"); err != nil {
		return "", "", fmt.Errorf("bring up veth %s: %w", containerEnd, err)
	}

	return hostEnd, containerEnd, nil
}

// DeleteVethPair 는 veth 페어를 삭제한다.
// 한 쪽(호스트 측)만 삭제하면 peer도 자동으로 삭제된다.
//
// 실행 명령:
//
//	ip link delete {name}-h
func (m *BridgeManager) DeleteVethPair(name string) error {
	hostEnd := name + "-h"
	if err := m.runner.Run("ip", "link", "delete", hostEnd); err != nil {
		return fmt.Errorf("delete veth pair %s: %w", name, err)
	}
	return nil
}
