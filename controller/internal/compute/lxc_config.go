// Package compute — LXC 컨테이너 설정 생성
//
// # 패키지 목적
//
// LXC 컨테이너의 설정 파일 내용을 생성한다.
// lxc_backend.go의 LXCBackend에서 컨테이너 생성 시 사용된다.
//
// # 설정 항목
//
//   - rootfs: 컨테이너 루트 파일시스템 경로
//   - cgroup2: CPU/메모리 자원 제한 (cgroup v2)
//   - 네트워크: veth + bridge 기반 네트워크
//   - 보안: 비특권 컨테이너 기본, 중첩(nesting) 지원
package compute

import (
	"fmt"
	"strings"
)

// LXCConfig — LXC 컨테이너 설정을 정의하는 구조체.
// GenerateConfig()로 LXC 설정 파일 내용을 생성한다.
type LXCConfig struct {
	Name         string   `json:"name"`
	RootFS       string   `json:"rootfs"`
	Arch         string   `json:"arch"`
	Distribution string   `json:"distribution"`
	Release      string   `json:"release"`
	VCPUs        uint32   `json:"vcpus"`
	MemoryMB     uint64   `json:"memory_mb"`
	NetworkType  string   `json:"network_type"`
	BridgeName   string   `json:"bridge_name"`
	IPAddress    string   `json:"ip_address"`
	Gateway      string   `json:"gateway"`
	Mounts       []string `json:"mounts,omitempty"`

	// ── 보안 설정 ──
	// Privileged: true이면 특권 컨테이너 (호스트 root와 동일 권한, 보안 위험)
	Privileged bool `json:"privileged"`
	// Nesting: true이면 컨테이너 내 컨테이너 실행 허용 (cgroup/proc/sys 마운트)
	Nesting bool `json:"nesting"`
	// UIDMapStart/Size: 비특권 컨테이너의 UID 매핑 범위 (예: 100000:65536)
	// 호스트의 UID 100000~165535가 컨테이너 내 UID 0~65535에 매핑된다.
	UIDMapStart int `json:"uid_map_start"`
	UIDMapSize  int `json:"uid_map_size"`
	// GIDMapStart/Size: GID 매핑 (UID 매핑과 동일한 구조)
	GIDMapStart int `json:"gid_map_start"`
	GIDMapSize  int `json:"gid_map_size"`
	// SeccompProfile: syscall 필터링 프로파일 경로 (비특권 컨테이너 기본 보안)
	SeccompProfile string `json:"seccomp_profile"`
	// AppArmorProfile: AppArmor MAC(Mandatory Access Control) 프로파일
	AppArmorProfile string `json:"apparmor_profile"`
	// DropCapabilities: 컨테이너에서 제거할 Linux capabilities (예: "sys_admin")
	DropCapabilities []string `json:"drop_capabilities,omitempty"`
}

// DefaultLXCConfig — 기본 LXC 설정을 반환한다.
//
// 기본값:
//   - Arch: amd64
//   - Distribution: ubuntu
//   - Release: 22.04
//   - NetworkType: veth
//   - BridgeName: hcvbr0
//   - Gateway: 10.0.0.1
//   - VCPUs: 1, MemoryMB: 512
//   - 비특권 컨테이너 (Privileged: false)
func DefaultLXCConfig(name string) *LXCConfig {
	sec := DefaultSecurityConfig()
	return &LXCConfig{
		Name:             name,
		RootFS:           fmt.Sprintf("/var/lib/lxc/%s/rootfs", name),
		Arch:             "amd64",
		Distribution:     "ubuntu",
		Release:          "22.04",
		VCPUs:            1,
		MemoryMB:         512,
		NetworkType:      "veth",
		BridgeName:       "hcvbr0",
		IPAddress:        "",
		Gateway:          "10.0.0.1",
		Privileged:       sec.Privileged,
		Nesting:          sec.Nesting,
		UIDMapStart:      sec.UIDMapStart,
		UIDMapSize:       sec.UIDMapSize,
		GIDMapStart:      sec.GIDMapStart,
		GIDMapSize:       sec.GIDMapSize,
		SeccompProfile:   sec.SeccompProfile,
		AppArmorProfile:  sec.AppArmorProfile,
		DropCapabilities: sec.DropCapabilities,
	}
}

// LXCSecurityDefaults — 보안 기본값 (DefaultSecurityConfig에서 반환).
type LXCSecurityDefaults struct {
	Privileged       bool
	Nesting          bool
	UIDMapStart      int
	UIDMapSize       int
	GIDMapStart      int
	GIDMapSize       int
	SeccompProfile   string
	AppArmorProfile  string
	DropCapabilities []string
}

// DefaultSecurityConfig — 비특권 컨테이너 보안 기본값을 반환한다.
//
// 기본값:
//   - 비특권 (Privileged: false)
//   - UID/GID 매핑: 100000:65536
//   - seccomp: /usr/share/lxc/config/common.seccomp
//   - AppArmor: lxc-container-default-cgns
func DefaultSecurityConfig() LXCSecurityDefaults {
	return LXCSecurityDefaults{
		Privileged:      false,
		Nesting:         false,
		UIDMapStart:     100000,
		UIDMapSize:      65536,
		GIDMapStart:     100000,
		GIDMapSize:      65536,
		SeccompProfile:  "/usr/share/lxc/config/common.seccomp",
		AppArmorProfile: "lxc-container-default-cgns",
	}
}

// GenerateConfig — LXC 설정 파일 내용을 문자열로 생성한다.
//
// 생성되는 설정 항목:
//   - lxc.rootfs.path: 루트 파일시스템 경로
//   - lxc.uts.name: 컨테이너 호스트명
//   - lxc.net.0.*: 네트워크 설정 (veth, bridge, IP, gateway)
//   - lxc.cgroup2.*: CPU/메모리 자원 제한
//   - lxc.arch: 아키텍처
//   - lxc.mount.entry: 마운트 포인트 (선택)
func (c *LXCConfig) GenerateConfig() string {
	var b strings.Builder

	fmt.Fprintf(&b, "lxc.rootfs.path = dir:%s\n", c.RootFS)
	fmt.Fprintf(&b, "lxc.uts.name = %s\n", c.Name)

	// Network
	fmt.Fprintf(&b, "lxc.net.0.type = %s\n", c.NetworkType)
	fmt.Fprintf(&b, "lxc.net.0.link = %s\n", c.BridgeName)
	b.WriteString("lxc.net.0.flags = up\n")
	if c.IPAddress != "" {
		b.WriteString(fmt.Sprintf("lxc.net.0.ipv4.address = %s/24\n", c.IPAddress))
	}
	if c.Gateway != "" {
		b.WriteString(fmt.Sprintf("lxc.net.0.ipv4.gateway = %s\n", c.Gateway))
	}

	// Cgroup v2 resource limits
	for k, v := range c.GenerateCgroupLimits() {
		b.WriteString(fmt.Sprintf("%s = %s\n", k, v))
	}

	// Architecture
	b.WriteString(fmt.Sprintf("lxc.arch = %s\n", c.Arch))

	// Security: UID/GID mapping (unprivileged containers)
	if !c.Privileged {
		uidStart := c.UIDMapStart
		uidSize := c.UIDMapSize
		gidStart := c.GIDMapStart
		gidSize := c.GIDMapSize
		if uidStart == 0 {
			uidStart = 100000
		}
		if uidSize == 0 {
			uidSize = 65536
		}
		if gidStart == 0 {
			gidStart = 100000
		}
		if gidSize == 0 {
			gidSize = 65536
		}
		b.WriteString(fmt.Sprintf("lxc.idmap = u 0 %d %d\n", uidStart, uidSize))
		b.WriteString(fmt.Sprintf("lxc.idmap = g 0 %d %d\n", gidStart, gidSize))
	}

	// Seccomp profile
	if c.SeccompProfile != "" {
		b.WriteString(fmt.Sprintf("lxc.seccomp.profile = %s\n", c.SeccompProfile))
	}

	// AppArmor profile
	if c.AppArmorProfile != "" {
		b.WriteString(fmt.Sprintf("lxc.apparmor.profile = %s\n", c.AppArmorProfile))
	}

	// Nesting support
	if c.Nesting {
		b.WriteString("lxc.mount.auto = cgroup:rw proc:mixed sys:mixed\n")
	}

	// Drop capabilities
	for _, cap := range c.DropCapabilities {
		b.WriteString(fmt.Sprintf("lxc.cap.drop = %s\n", cap))
	}

	// Mounts
	for _, mount := range c.Mounts {
		b.WriteString(fmt.Sprintf("lxc.mount.entry = %s\n", mount))
	}

	return b.String()
}

// GenerateCgroupLimits — cgroup v2 리소스 제한 키-값 맵을 반환한다.
//
// 반환 항목:
//   - lxc.cgroup2.memory.max: 메모리 제한 (바이트)
//   - lxc.cgroup2.cpu.max: CPU 쿼터 (vcpus * 100000 period 100000)
func (c *LXCConfig) GenerateCgroupLimits() map[string]string {
	limits := make(map[string]string)

	// Memory limit in bytes
	memoryBytes := c.MemoryMB * 1024 * 1024
	limits["lxc.cgroup2.memory.max"] = fmt.Sprintf("%d", memoryBytes)

	// CPU quota: vcpus * 100000 per 100000 period
	cpuQuota := c.VCPUs * 100000
	limits["lxc.cgroup2.cpu.max"] = fmt.Sprintf("%d 100000", cpuQuota)

	return limits
}
