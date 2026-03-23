// Package compute — LXC 컨테이너 런타임 백엔드
//
// # 패키지 목적
//
// LXC(Linux Containers)를 통해 컨테이너 생명주기를 관리한다.
// 경량 Linux 워크로드에 적합하며, VM 대비 빠른 시작과 낮은 오버헤드를 제공한다.
//
// # 아키텍처 (Phase 16)
//
//	Go Controller
//	    │
//	    ├── RustVMMBackend  ──→ vmcore FFI ──→ /dev/kvm  (microVM)
//	    ├── QEMUBackend     ──→ QMP socket ──→ qemu-system-x86_64 (범용 VM)
//	    └── LXCBackend (이 파일) ──→ lxc-* CLI ──→ Linux namespaces/cgroups
//
// # 동작 모드
//
//   - Emulated (에뮬레이션): 인메모리 상태 머신. LXC 바이너리 불필요. 개발/테스트용.
//   - Real (실제): lxc-create/start/stop/freeze/unfreeze/destroy CLI. 프로덕션용.
//
// # Handle 범위
//
// LXC 백엔드의 Handle은 20000부터 시작하여 RustVMM(1~9999), QEMU(10000~19999)과
// 충돌을 방지한다.
//
// # LXC CLI 매핑
//
//	create  → "lxc-create -t download -n {name} -- --dist {dist} --release {rel} --arch amd64"
//	destroy → "lxc-destroy -n {name}"
//	start   → "lxc-start -n {name} -d"
//	stop    → "lxc-stop -n {name}"
//	pause   → "lxc-freeze -n {name}"
//	resume  → "lxc-unfreeze -n {name}"
package compute

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
)

// ContainerStats — 컨테이너 리소스 사용량 통계.
// Real 모드에서는 cgroup v2 파일시스템에서 읽고,
// Emulated 모드에서는 시뮬레이션 값을 반환한다.
type ContainerStats struct {
	CPUUsageNs       int64 `json:"cpu_usage_ns"`
	MemoryUsageBytes int64 `json:"memory_usage_bytes"`
	MemoryLimitBytes int64 `json:"memory_limit_bytes"`
	PIDCount         int   `json:"pid_count"`
	NetRxBytes       int64 `json:"net_rx_bytes"`
	NetTxBytes       int64 `json:"net_tx_bytes"`
}

// LXCBackend — LXC 컨테이너를 관리하는 VMMBackend 구현체.
// Emulated 모드(인메모리, 개발/테스트)와 Real 모드(lxc-* CLI, 프로덕션)를 지원한다.
// Handle ID는 20000부터 시작하여 RustVMM/QEMU와 충돌을 방지한다.
type LXCBackend struct {
	mu            sync.RWMutex
	vms           map[int32]*VMInfo
	configs       map[int32]*LXCConfig
	nextID        atomic.Int32
	emulated      bool
	lxcPath       string // lxc container root path (default: /var/lib/lxc)
	bridgeName    string // default bridge for container networking
	storageDriver storage.StorageDriver
	snapshots     map[int32][]string // handle → snapshot names (emulated)
}

// LXCBackendConfig — LXC 백엔드 설정.
type LXCBackendConfig struct {
	Emulated   bool   // true = in-memory mock (no LXC binary needed)
	LXCPath    string // root path for LXC containers (default: /var/lib/lxc)
	BridgeName string // default bridge name (default: hcvbr0)
}

// NewLXCBackend — LXC 백엔드를 생성한다.
// config가 nil이거나 Emulated=true이면 인메모리 에뮬레이션 모드로 동작한다.
// Handle ID는 20000부터 시작하여 RustVMM(1~9999), QEMU(10000~19999)과 충돌을 방지한다.
func NewLXCBackend(config *LXCBackendConfig) *LXCBackend {
	b := &LXCBackend{
		vms:        make(map[int32]*VMInfo),
		configs:    make(map[int32]*LXCConfig),
		emulated:   true,
		lxcPath:    "/var/lib/lxc",
		bridgeName: "hcvbr0",
		snapshots:  make(map[int32][]string),
	}
	b.nextID.Store(20000) // LXC handles start at 20000

	if config != nil {
		b.emulated = config.Emulated
		if config.LXCPath != "" {
			b.lxcPath = config.LXCPath
		}
		if config.BridgeName != "" {
			b.bridgeName = config.BridgeName
		}
	}

	return b
}

// Name — 백엔드 이름("lxc")을 반환한다. VMMBackend 인터페이스 구현.
// BackendSelector가 백엔드를 식별할 때 사용한다.
func (b *LXCBackend) Name() string { return "lxc" }

// CreateVM — 새 LXC 컨테이너를 생성한다. VMMBackend 인터페이스 구현.
//
// # 매개변수
//   - name: 컨테이너 이름 (LXC 이름으로도 사용)
//   - vcpus: 할당할 가상 CPU 수 (cgroup2 cpu.max로 제한)
//   - memoryMB: 할당할 메모리 (MB) (cgroup2 memory.max로 제한)
//
// # 동작 모드별 처리
//   - Emulated: 인메모리에 VMInfo 등록 + 시뮬레이션 IP 할당 (10.0.0.x)
//   - Real: ZFS rootfs 볼륨 생성(선택) + lxc-create CLI 실행 + VMInfo 등록
//
// # 반환값
//   - 생성된 컨테이너의 VMInfo (상태: "configured", 타입: "container")
//   - 에러: Real 모드에서 rootfs 볼륨 생성 실패 또는 lxc-create 실패
//
// 동시 호출 안전성: 안전 (nextID는 atomic, vms는 mutex 보호)
func (b *LXCBackend) CreateVM(name string, vcpus uint32, memoryMB uint64) (*VMInfo, error) {
	handle := b.nextID.Add(1) - 1

	cfg := DefaultLXCConfig(name)
	cfg.VCPUs = vcpus
	cfg.MemoryMB = memoryMB
	cfg.BridgeName = b.bridgeName

	if !b.emulated {
		// Create ZFS rootfs volume if storage driver is set
		if b.storageDriver != nil {
			volName := fmt.Sprintf("lxc-%s-rootfs", name)
			sizeBytes := memoryMB * 1024 * 1024 * 2 // rootfs = 2x memory as default
			if sizeBytes < 1073741824 {
				sizeBytes = 1073741824 // minimum 1GB
			}
			vol, err := b.storageDriver.CreateVolume("local-zfs", volName, "raw", sizeBytes)
			if err != nil {
				return nil, fmt.Errorf("lxc rootfs volume: %w", err)
			}
			cfg.RootFS = vol.Path
		}
		if err := b.lxcCreate(name, cfg); err != nil {
			return nil, fmt.Errorf("lxc create: %w", err)
		}
	}

	// Assign simulated IP in Emulated mode
	ipAddr := ""
	if b.emulated {
		ipAddr = fmt.Sprintf("10.0.0.%d", (handle%254)+1)
		cfg.IPAddress = ipAddr
	}

	vm := &VMInfo{
		ID:            handle,
		Name:          name,
		State:         "configured",
		VCPUs:         vcpus,
		MemoryMB:      memoryMB,
		Node:          "local",
		Backend:       "lxc",
		Type:          "container",
		RestartPolicy: "always",
		CreatedAt:     time.Now(),
		RootFS:        cfg.RootFS,
		Template:      cfg.Distribution,
		Arch:          cfg.Arch,
		IPAddress:     ipAddr,
	}

	b.mu.Lock()
	b.vms[handle] = vm
	b.configs[handle] = cfg
	b.mu.Unlock()

	slog.Info("lxc container created", "handle", handle, "name", name, "emulated", b.emulated)
	return vm, nil
}

// CreateVMWithTemplate — LXC 템플릿을 지정하여 컨테이너를 생성한다.
func (b *LXCBackend) CreateVMWithTemplate(name string, vcpus uint32, memoryMB uint64, tmpl string) (*VMInfo, error) {
	handle := b.nextID.Add(1) - 1

	cfg := DefaultLXCConfig(name)
	cfg.VCPUs = vcpus
	cfg.MemoryMB = memoryMB
	cfg.BridgeName = b.bridgeName
	if tmpl != "" {
		cfg.Distribution = tmpl
	}

	if !b.emulated {
		if err := b.lxcCreate(name, cfg); err != nil {
			return nil, fmt.Errorf("lxc create: %w", err)
		}
	}

	ipAddr := ""
	if b.emulated {
		ipAddr = fmt.Sprintf("10.0.0.%d", (handle%254)+1)
		cfg.IPAddress = ipAddr
	}

	vm := &VMInfo{
		ID:            handle,
		Name:          name,
		State:         "configured",
		VCPUs:         vcpus,
		MemoryMB:      memoryMB,
		Node:          "local",
		Backend:       "lxc",
		Type:          "container",
		RestartPolicy: "always",
		CreatedAt:     time.Now(),
		RootFS:        cfg.RootFS,
		Template:      cfg.Distribution,
		Arch:          cfg.Arch,
		IPAddress:     ipAddr,
	}

	b.mu.Lock()
	b.vms[handle] = vm
	b.configs[handle] = cfg
	b.mu.Unlock()

	slog.Info("lxc container created with template", "handle", handle, "name", name, "template", cfg.Distribution)
	return vm, nil
}

// DestroyVM — 컨테이너를 삭제한다. VMMBackend 인터페이스 구현.
//
// Real 모드에서는 lxc-destroy CLI를 실행하고, ZFS rootfs 볼륨도 정리한다.
// 인메모리 맵에서 VMInfo, LXCConfig, 스냅샷을 모두 제거한다.
// handle에 해당하는 VM이 없으면 에러를 반환한다.
func (b *LXCBackend) DestroyVM(handle int32) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	vm, ok := b.vms[handle]
	if !ok {
		return fmt.Errorf("VM not found: %d", handle)
	}

	if !b.emulated {
		if err := b.lxcExec("lxc-destroy", "-n", vm.Name); err != nil {
			slog.Warn("lxc-destroy failed", "handle", handle, "error", err)
		}
		// Clean up ZFS volume if storage driver is set
		if b.storageDriver != nil {
			volID := fmt.Sprintf("lxc-%s-rootfs", vm.Name)
			if err := b.storageDriver.DeleteVolume(volID); err != nil {
				slog.Warn("lxc storage cleanup failed", "handle", handle, "volume", volID, "error", err)
			}
		}
	}

	delete(b.vms, handle)
	delete(b.configs, handle)
	delete(b.snapshots, handle)
	return nil
}

// StartVM — 컨테이너를 시작한다 (configured/stopped → running).
// Real 모드: lxc-start -n {name} -d (데몬 모드)
func (b *LXCBackend) StartVM(handle int32) error {
	return b.transition(handle, "running", "lxc-start", "-n", "%NAME%", "-d")
}

// StopVM — 컨테이너를 중지한다 (running/paused → stopped).
// Real 모드: lxc-stop -n {name}
func (b *LXCBackend) StopVM(handle int32) error {
	return b.transition(handle, "stopped", "lxc-stop", "-n", "%NAME%")
}

// PauseVM — 컨테이너를 일시정지한다 (running → paused).
// Real 모드: lxc-freeze -n {name} (cgroup freezer 사용)
func (b *LXCBackend) PauseVM(handle int32) error {
	return b.transition(handle, "paused", "lxc-freeze", "-n", "%NAME%")
}

// ResumeVM — 일시정지된 컨테이너를 재개한다 (paused → running).
// Real 모드: lxc-unfreeze -n {name}
func (b *LXCBackend) ResumeVM(handle int32) error {
	return b.transition(handle, "running", "lxc-unfreeze", "-n", "%NAME%")
}

// GetVM — handle에 해당하는 컨테이너 정보를 반환한다. VMMBackend 인터페이스 구현.
// 동시 호출 안전성: 안전 (RLock)
func (b *LXCBackend) GetVM(handle int32) (*VMInfo, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	vm, ok := b.vms[handle]
	if !ok {
		return nil, fmt.Errorf("VM not found: %d", handle)
	}
	return vm, nil
}

// ListVMs — 모든 LXC 컨테이너 목록을 반환한다. VMMBackend 인터페이스 구현.
// 동시 호출 안전성: 안전 (RLock)
func (b *LXCBackend) ListVMs() []*VMInfo {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]*VMInfo, 0, len(b.vms))
	for _, vm := range b.vms {
		result = append(result, vm)
	}
	return result
}

// ── LXC Template Management ─────────────────────────────

// AvailableTemplates — 사용 가능한 LXC 배포 템플릿 목록을 반환한다.
func (b *LXCBackend) AvailableTemplates() []string {
	return []string{"ubuntu", "alpine", "debian", "centos"}
}

// ── Container Stats ─────────────────────────────────────

// GetContainerStats — 컨테이너 리소스 사용량 통계를 반환한다.
// Real 모드: cgroup v2 파일시스템에서 읽는다.
// Emulated 모드: 시뮬레이션 값을 반환한다.
func (b *LXCBackend) GetContainerStats(handle int32) (*ContainerStats, error) {
	b.mu.RLock()
	vm, ok := b.vms[handle]
	b.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("VM not found: %d", handle)
	}

	if b.emulated {
		return &ContainerStats{
			CPUUsageNs:       int64(vm.VCPUs) * 1_000_000_000,
			MemoryUsageBytes: int64(vm.MemoryMB) * 512 * 1024, // ~50% usage
			MemoryLimitBytes: int64(vm.MemoryMB) * 1024 * 1024,
			PIDCount:         10,
			NetRxBytes:       4096,
			NetTxBytes:       2048,
		}, nil
	}

	return b.readCgroupStats(vm.Name)
}

// ── State transitions ────────────────────────────────

// lxcValidTransitions — LXC 컨테이너 상태 전이 규칙.
// QEMUBackend, RustVMMBackend와 동일한 상태 머신 구조를 사용한다.
//
//	configured → running, stopped
//	running    → paused, stopped
//	paused     → running, stopped
var lxcValidTransitions = map[string]map[string]bool{
	"configured": {"running": true, "stopped": true},
	"running":    {"paused": true, "stopped": true},
	"paused":     {"running": true, "stopped": true},
}

// transition — 컨테이너 상태를 전이한다 (공통 내부 메서드).
//
// lxcValidTransitions에 따라 상태 전이 유효성을 검증한 후,
// Real 모드에서는 LXC CLI 명령을 실행한다.
// args에 "%NAME%"이 있으면 실제 컨테이너 이름으로 치환된다.
// 잘못된 상태 전이 시 code=-5 에러를 반환하여 API 레이어에서 409 Conflict로 변환된다.
func (b *LXCBackend) transition(handle int32, targetState string, cmd string, args ...string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	vm, ok := b.vms[handle]
	if !ok {
		return fmt.Errorf("VM not found: %d", handle)
	}

	allowed, exists := lxcValidTransitions[vm.State]
	if !exists || !allowed[targetState] {
		return fmt.Errorf("vmcore vm_action: invalid state (code=-5)")
	}

	if !b.emulated {
		// Replace %NAME% placeholder with actual container name
		execArgs := make([]string, len(args))
		for i, a := range args {
			if a == "%NAME%" {
				execArgs[i] = vm.Name
			} else {
				execArgs[i] = a
			}
		}
		if err := b.lxcExec(cmd, execArgs...); err != nil {
			return fmt.Errorf("lxc %s: %w", cmd, err)
		}
	}

	vm.State = targetState
	return nil
}

// ── LXC CLI helpers ──────────────────────────────────

// lxcCreate — lxc-create CLI를 실행하여 컨테이너를 생성한다.
// download 템플릿을 사용하여 배포판/릴리스/아키텍처를 지정한다.
func (b *LXCBackend) lxcCreate(name string, cfg *LXCConfig) error {
	args := []string{
		"-t", "download",
		"-n", name,
		"--",
		"--dist", cfg.Distribution,
		"--release", cfg.Release,
		"--arch", cfg.Arch,
	}
	return b.lxcExec("lxc-create", args...)
}

// lxcExec — LXC CLI 명령을 실행하고 결과를 확인한다.
// 실패 시 stdout+stderr를 에러 메시지에 포함한다.
func (b *LXCBackend) lxcExec(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %s: %w", name, args, string(out), err)
	}
	return nil
}

// ── Cgroup v2 stats reader ──────────────────────────

// readCgroupStats — cgroup v2 파일시스템에서 컨테이너 리소스 사용량을 읽는다.
//
// 읽는 파일:
//   - /sys/fs/cgroup/lxc.payload.{name}/cpu.stat     → CPU 사용 시간 (usage_usec)
//   - /sys/fs/cgroup/lxc.payload.{name}/memory.current → 현재 메모리 사용량 (바이트)
//   - /sys/fs/cgroup/lxc.payload.{name}/memory.max    → 메모리 제한 (바이트, "max"면 무제한)
//   - /sys/fs/cgroup/lxc.payload.{name}/pids.current  → 현재 프로세스 수
//
// 파일 읽기 실패 시 해당 필드를 0으로 남기고 에러를 반환하지 않는다 (best-effort).
func (b *LXCBackend) readCgroupStats(containerName string) (*ContainerStats, error) {
	cgroupBase := fmt.Sprintf("/sys/fs/cgroup/lxc.payload.%s", containerName)

	stats := &ContainerStats{}

	// cpu.stat
	if data, err := os.ReadFile(cgroupBase + "/cpu.stat"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "usage_usec ") {
				parts := strings.Fields(line)
				if len(parts) == 2 {
					if v, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
						stats.CPUUsageNs = v * 1000 // usec to ns
					}
				}
			}
		}
	}

	// memory.current
	if data, err := os.ReadFile(cgroupBase + "/memory.current"); err == nil {
		if v, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil {
			stats.MemoryUsageBytes = v
		}
	}

	// memory.max
	if data, err := os.ReadFile(cgroupBase + "/memory.max"); err == nil {
		s := strings.TrimSpace(string(data))
		if s != "max" {
			if v, err := strconv.ParseInt(s, 10, 64); err == nil {
				stats.MemoryLimitBytes = v
			}
		}
	}

	// pids.current
	if data, err := os.ReadFile(cgroupBase + "/pids.current"); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			stats.PIDCount = v
		}
	}

	return stats, nil
}

// ── Storage Integration ─────────────────────────────────

// SetStorageDriver — 스토리지 드라이버를 설정한다 (선택 사항).
// nil이면 스토리지 통합 없이 동작한다 (하위 호환).
func (b *LXCBackend) SetStorageDriver(driver storage.StorageDriver) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.storageDriver = driver
}

// SnapshotContainer — 컨테이너 rootfs의 스토리지 스냅샷을 생성한다.
// Emulated 모드: 인메모리에 스냅샷 이름 저장.
// Real 모드: StorageDriver.CreateSnapshot() 호출.
func (b *LXCBackend) SnapshotContainer(handle int32, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	vm, ok := b.vms[handle]
	if !ok {
		return fmt.Errorf("VM not found: %d", handle)
	}

	if b.emulated {
		b.snapshots[handle] = append(b.snapshots[handle], name)
		slog.Info("lxc snapshot created (emulated)", "handle", handle, "name", name)
		return nil
	}

	if b.storageDriver == nil {
		return fmt.Errorf("storage driver not configured")
	}

	volID := fmt.Sprintf("lxc-%s-rootfs", vm.Name)
	_, err := b.storageDriver.CreateSnapshot(volID, name)
	if err != nil {
		return fmt.Errorf("lxc snapshot: %w", err)
	}
	b.snapshots[handle] = append(b.snapshots[handle], name)
	slog.Info("lxc snapshot created", "handle", handle, "name", name)
	return nil
}

// CloneContainer — 스냅샷에서 새 컨테이너를 복제한다.
// Emulated 모드: 원본 설정을 복사하여 새 컨테이너 생성.
// Real 모드: StorageDriver.CloneSnapshot() + lxc-create.
func (b *LXCBackend) CloneContainer(handle int32, newName string) (int32, error) {
	b.mu.RLock()
	vm, ok := b.vms[handle]
	if !ok {
		b.mu.RUnlock()
		return 0, fmt.Errorf("VM not found: %d", handle)
	}
	snaps := b.snapshots[handle]
	cfg := b.configs[handle]
	b.mu.RUnlock()

	if len(snaps) == 0 {
		return 0, fmt.Errorf("no snapshots for container %d", handle)
	}
	latestSnap := snaps[len(snaps)-1]

	newHandle := b.nextID.Add(1) - 1

	if b.emulated {
		newCfg := DefaultLXCConfig(newName)
		if cfg != nil {
			newCfg.VCPUs = cfg.VCPUs
			newCfg.MemoryMB = cfg.MemoryMB
			newCfg.Distribution = cfg.Distribution
			newCfg.BridgeName = cfg.BridgeName
		}
		ipAddr := fmt.Sprintf("10.0.0.%d", (newHandle%254)+1)
		newVM := &VMInfo{
			ID:            newHandle,
			Name:          newName,
			State:         "configured",
			VCPUs:         vm.VCPUs,
			MemoryMB:      vm.MemoryMB,
			Node:          "local",
			Backend:       "lxc",
			Type:          "container",
			RestartPolicy: "always",
			CreatedAt:     time.Now(),
			RootFS:        fmt.Sprintf("/var/lib/lxc/%s/rootfs", newName),
			Template:      vm.Template,
			Arch:          vm.Arch,
			IPAddress:     ipAddr,
		}
		b.mu.Lock()
		b.vms[newHandle] = newVM
		b.configs[newHandle] = newCfg
		b.mu.Unlock()
		slog.Info("lxc container cloned (emulated)", "source", handle, "clone", newHandle, "name", newName)
		return newHandle, nil
	}

	if b.storageDriver == nil {
		return 0, fmt.Errorf("storage driver not configured")
	}

	snapID := fmt.Sprintf("snap-%s", latestSnap)
	clonedVol, err := b.storageDriver.CloneSnapshot(snapID, fmt.Sprintf("lxc-%s-rootfs", newName))
	if err != nil {
		return 0, fmt.Errorf("lxc clone: %w", err)
	}

	newCfg := DefaultLXCConfig(newName)
	if cfg != nil {
		newCfg.VCPUs = cfg.VCPUs
		newCfg.MemoryMB = cfg.MemoryMB
		newCfg.Distribution = cfg.Distribution
		newCfg.BridgeName = cfg.BridgeName
	}
	newCfg.RootFS = clonedVol.Path

	newVM := &VMInfo{
		ID:            newHandle,
		Name:          newName,
		State:         "configured",
		VCPUs:         vm.VCPUs,
		MemoryMB:      vm.MemoryMB,
		Node:          "local",
		Backend:       "lxc",
		Type:          "container",
		RestartPolicy: "always",
		CreatedAt:     time.Now(),
		RootFS:        clonedVol.Path,
		Template:      vm.Template,
		Arch:          vm.Arch,
	}
	b.mu.Lock()
	b.vms[newHandle] = newVM
	b.configs[newHandle] = newCfg
	b.mu.Unlock()
	slog.Info("lxc container cloned", "source", handle, "clone", newHandle, "name", newName)
	return newHandle, nil
}

// ── Container Migration (CRIU) ──────────────────────────

// MigrateContainer — 컨테이너를 다른 노드로 마이그레이션한다.
// Real 모드: lxc-checkpoint로 상태 저장 후 대상 노드에서 복원 (시뮬레이션).
// Emulated 모드: VMInfo.Node를 targetNode로 변경.
func (b *LXCBackend) MigrateContainer(handle int32, targetNode string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	vm, ok := b.vms[handle]
	if !ok {
		return fmt.Errorf("VM not found: %d", handle)
	}

	if vm.State != "running" {
		return fmt.Errorf("container must be running to migrate, current state: %s", vm.State)
	}

	if vm.Node == targetNode {
		return fmt.Errorf("container %d is already on node %s", handle, targetNode)
	}

	if !b.emulated {
		// Checkpoint on source
		checkpointDir := fmt.Sprintf("/tmp/lxc-checkpoint-%s", vm.Name)
		if err := b.lxcExec("lxc-checkpoint", "-n", vm.Name, "-D", checkpointDir, "-s"); err != nil {
			return fmt.Errorf("lxc checkpoint: %w", err)
		}
		slog.Info("lxc checkpoint created", "handle", handle, "dir", checkpointDir)
		// In real implementation, rsync checkpoint to target and lxc-checkpoint -r
	}

	vm.Node = targetNode
	slog.Info("lxc container migrated", "handle", handle, "target", targetNode)
	return nil
}

// CheckpointContainer — 컨테이너 상태를 지정 디렉터리에 저장한다 (CRIU).
// Emulated 모드: 성공 반환 (no-op).
func (b *LXCBackend) CheckpointContainer(handle int32, dir string) error {
	b.mu.RLock()
	vm, ok := b.vms[handle]
	b.mu.RUnlock()
	if !ok {
		return fmt.Errorf("VM not found: %d", handle)
	}

	if !b.emulated {
		if err := b.lxcExec("lxc-checkpoint", "-n", vm.Name, "-D", dir, "-s"); err != nil {
			return fmt.Errorf("lxc checkpoint: %w", err)
		}
	}

	slog.Info("lxc checkpoint saved", "handle", handle, "dir", dir, "emulated", b.emulated)
	return nil
}

// RestoreContainer — 지정 디렉터리에서 컨테이너 상태를 복원한다 (CRIU).
// Emulated 모드: 상태를 "running"으로 변경.
func (b *LXCBackend) RestoreContainer(handle int32, dir string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	vm, ok := b.vms[handle]
	if !ok {
		return fmt.Errorf("VM not found: %d", handle)
	}

	if !b.emulated {
		if err := b.lxcExec("lxc-checkpoint", "-n", vm.Name, "-D", dir, "-r"); err != nil {
			return fmt.Errorf("lxc restore: %w", err)
		}
	}

	vm.State = "running"
	slog.Info("lxc container restored", "handle", handle, "dir", dir, "emulated", b.emulated)
	return nil
}

// ── Container Exec ──────────────────────────────────────

// ExecContainer — 컨테이너 내에서 명령을 실행한다.
// Real 모드: lxc-attach -n {name} -- {command...} 실행, stdout+stderr 캡처.
// Emulated 모드: 시뮬레이션된 출력 반환.
func (b *LXCBackend) ExecContainer(handle int32, command []string) (string, error) {
	b.mu.RLock()
	vm, ok := b.vms[handle]
	b.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("VM not found: %d", handle)
	}

	if vm.State != "running" {
		return "", fmt.Errorf("invalid state: container must be running to exec, current state: %s", vm.State)
	}

	if b.emulated {
		output := fmt.Sprintf("exec: %s", strings.Join(command, " "))
		slog.Info("lxc exec (emulated)", "handle", handle, "command", command)
		return output, nil
	}

	args := append([]string{"-n", vm.Name, "--"}, command...)
	cmd := exec.Command("lxc-attach", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("lxc-attach: %s: %w", string(out), err)
	}
	return string(out), nil
}
