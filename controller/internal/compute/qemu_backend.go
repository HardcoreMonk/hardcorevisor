// Package compute — QEMU 백엔드: 범용 VM 워크로드 관리
//
// # 패키지 목적
//
// QEMU Machine Protocol(QMP)을 사용하여 VM 생명주기를 관리한다.
// Windows VM, GPU 패스스루, 레거시 OS, 전체 디바이스 에뮬레이션에 적합하다.
//
// # 아키텍처 (ADR-006)
//
//	Go Controller
//	    │
//	    ├── QEMUBackend (이 파일) ──→ QMP unix socket ──→ qemu-system-x86_64
//	    │                         ──→ libvirt (향후 구현)
//	    └── RustVMMBackend        ──→ vmcore FFI ──→ /dev/kvm
//
// # 동작 모드
//
//   - Emulated (에뮬레이션): 인메모리 상태 머신. QEMU 바이너리 불필요. 개발/테스트용.
//   - Real (실제): QMP unix socket으로 qemu-system-x86_64 프로세스 제어. 프로덕션용.
//
// # QMP 명령 매핑
//
//	start  → "cont" (계속 실행)
//	stop   → "system_powerdown" (전원 종료)
//	pause  → "stop" (일시정지)
//	resume → "cont" (재개)
//
// # Handle 범위
//
// QEMU 백엔드의 Handle은 10000부터 시작하여 RustVMM(1~9999)과 충돌을 방지한다.
package compute

import (
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// qemuProcess — QEMU 프로세스와 QMP 소켓 경로를 추적한다 (Real 모드에서 사용).
type qemuProcess struct {
	socketPath string
	cmd        *exec.Cmd
}

// QEMUBackend — QEMU/QMP를 통해 VM을 관리하는 백엔드.
// 현재 구현: Emulated 모드(인메모리, 개발/테스트).
// 프로덕션: Real 모드(QMP unix socket 또는 libvirt API).
type QEMUBackend struct {
	mu          sync.RWMutex
	vms         map[int32]*VMInfo
	processes   map[int32]*qemuProcess
	nextID      atomic.Int32
	qmpAddr     string // QMP socket base path, e.g. /var/run/hcv
	emulated    bool   // true = in-memory emulation (no real QEMU)
	defaultDisk string // default disk image path (optional)
	networkMode string // "user", "tap", "none"
}

// QEMUConfig — QEMU 백엔드 설정.
type QEMUConfig struct {
	QMPSocket   string // unix socket path, e.g. /var/run/qemu/qmp.sock
	Emulated    bool   // true = in-memory mock (no QEMU binary needed)
	DefaultDisk string // default disk image path (optional)
	NetworkMode string // "user" (SLIRP), "tap", "none" (default: "user")
}

// NewQEMUBackend — QEMU 백엔드를 생성한다.
// config가 nil이거나 Emulated=true이면 인메모리 에뮬레이션 모드로 동작한다.
// Handle ID는 10000부터 시작하여 RustVMM(1~9999)과 충돌을 방지한다.
func NewQEMUBackend(config *QEMUConfig) *QEMUBackend {
	b := &QEMUBackend{
		vms:       make(map[int32]*VMInfo),
		processes: make(map[int32]*qemuProcess),
		emulated:  true,
	}
	b.nextID.Store(10000) // QEMU handles start at 10000 to avoid collision with rustvmm

	if config != nil {
		b.qmpAddr = config.QMPSocket
		b.emulated = config.Emulated
		b.defaultDisk = config.DefaultDisk
		b.networkMode = config.NetworkMode
	}
	if b.networkMode == "" {
		b.networkMode = "user"
	}

	return b
}

func (b *QEMUBackend) Name() string { return "qemu" }

func (b *QEMUBackend) CreateVM(name string, vcpus uint32, memoryMB uint64) (*VMInfo, error) {
	handle := b.nextID.Add(1) - 1

	if !b.emulated {
		if err := b.qmpCreateVM(handle, name, vcpus, memoryMB); err != nil {
			return nil, fmt.Errorf("qemu create: %w", err)
		}
	}

	vm := &VMInfo{
		ID:        handle,
		Name:      name,
		State:     "configured",
		VCPUs:     vcpus,
		MemoryMB:  memoryMB,
		Node:      "local",
		Backend:   "qemu",
		CreatedAt: time.Now(),
	}

	b.mu.Lock()
	b.vms[handle] = vm
	b.mu.Unlock()

	return vm, nil
}

func (b *QEMUBackend) DestroyVM(handle int32) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	vm, ok := b.vms[handle]
	if !ok {
		return fmt.Errorf("VM not found: %d", handle)
	}

	if !b.emulated {
		// Send QMP quit command first
		if err := b.qmpCommand(handle, "quit"); err != nil {
			// Log but continue to kill process
			_ = err
		}
		// Kill QEMU process if in Real mode
		if proc, ok := b.processes[handle]; ok {
			if proc.cmd != nil && proc.cmd.Process != nil {
				proc.cmd.Process.Kill()
				proc.cmd.Wait()
			}
			delete(b.processes, handle)
		}
	}

	_ = vm
	delete(b.vms, handle)
	return nil
}

func (b *QEMUBackend) StartVM(handle int32) error {
	return b.transition(handle, "running", "cont")
}

func (b *QEMUBackend) StopVM(handle int32) error {
	return b.transition(handle, "stopped", "system_powerdown")
}

func (b *QEMUBackend) PauseVM(handle int32) error {
	return b.transition(handle, "paused", "stop")
}

func (b *QEMUBackend) ResumeVM(handle int32) error {
	return b.transition(handle, "running", "cont")
}

func (b *QEMUBackend) GetVM(handle int32) (*VMInfo, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	vm, ok := b.vms[handle]
	if !ok {
		return nil, fmt.Errorf("VM not found: %d", handle)
	}
	return vm, nil
}

func (b *QEMUBackend) ListVMs() []*VMInfo {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]*VMInfo, 0, len(b.vms))
	for _, vm := range b.vms {
		result = append(result, vm)
	}
	return result
}

// ── State transitions ────────────────────────────────

// QEMU VM 상태 전이 규칙 (kvm_mgr.rs의 VmState와 동일)
//
//	configured → running, stopped
//	running    → paused, stopped
//	paused     → running, stopped
var qemuValidTransitions = map[string]map[string]bool{
	"configured": {"running": true, "stopped": true},
	"running":    {"paused": true, "stopped": true},
	"paused":     {"running": true, "stopped": true},
}

func (b *QEMUBackend) transition(handle int32, targetState, qmpCmd string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	vm, ok := b.vms[handle]
	if !ok {
		return fmt.Errorf("VM not found: %d", handle)
	}

	allowed, exists := qemuValidTransitions[vm.State]
	if !exists || !allowed[targetState] {
		return fmt.Errorf("vmcore vm_action: invalid state (code=-5)")
	}

	if !b.emulated {
		if err := b.qmpCommand(handle, qmpCmd); err != nil {
			return fmt.Errorf("qemu %s: %w", qmpCmd, err)
		}
	}

	vm.State = targetState
	return nil
}

// ── QMP Protocol ─────────────────────────────────────────

// qmpSocketPath returns the QMP socket path for a given VM handle.
func (b *QEMUBackend) qmpSocketPath(handle int32) string {
	base := b.qmpAddr
	if base == "" {
		base = "/var/run/hcv"
	}
	return fmt.Sprintf("%s/qmp-%d.sock", base, handle)
}

// qmpCreateVM launches a QEMU process with QMP enabled.
// In Real mode, it attempts to exec qemu-system-x86_64 and connect via QMP.
func (b *QEMUBackend) qmpCreateVM(handle int32, name string, vcpus uint32, memoryMB uint64) error {
	socketPath := b.qmpSocketPath(handle)

	// Check if qemu-system-x86_64 is available
	qemuBin, err := exec.LookPath("qemu-system-x86_64")
	if err != nil {
		return fmt.Errorf("qemu-system-x86_64 not found: %w", err)
	}

	args := []string{
		"-name", name,
		"-machine", "q35,accel=kvm",
		"-smp", fmt.Sprintf("%d", vcpus),
		"-m", fmt.Sprintf("%d", memoryMB),
		"-qmp", fmt.Sprintf("unix:%s,server,nowait", socketPath),
		"-nographic",
		"-nodefaults",
	}

	// Disk: use default disk image if configured
	diskPath := b.defaultDisk
	if diskPath != "" {
		args = append(args, "-drive", fmt.Sprintf("file=%s,format=qcow2,if=virtio", diskPath))
	}

	// Network
	switch b.networkMode {
	case "tap":
		args = append(args, "-netdev", "tap,id=net0,ifname=tap0,script=no,downscript=no")
		args = append(args, "-device", "virtio-net-pci,netdev=net0")
	case "user":
		args = append(args, "-netdev", "user,id=net0,hostfwd=tcp::2222-:22")
		args = append(args, "-device", "virtio-net-pci,netdev=net0")
	default: // none
		args = append(args, "-nic", "none")
	}

	cmd := exec.Command(qemuBin, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("qemu start failed: %w", err)
	}

	proc := &qemuProcess{
		socketPath: socketPath,
		cmd:        cmd,
	}
	b.processes[handle] = proc

	// Wait briefly for QMP socket to become available, then connect
	// to verify the process started correctly.
	time.Sleep(500 * time.Millisecond)

	client, err := QMPDial(socketPath, 5*time.Second)
	if err != nil {
		// Kill the process if we can't connect
		cmd.Process.Kill()
		cmd.Wait()
		delete(b.processes, handle)
		return fmt.Errorf("QMP socket connection failed for handle %d: %w", handle, err)
	}
	client.Close()

	return nil
}

// qmpCommand sends a QMP command to a running QEMU instance.
// In Real mode, it connects to the QMP unix socket and executes the command.
func (b *QEMUBackend) qmpCommand(handle int32, command string) error {
	proc, ok := b.processes[handle]
	if !ok {
		return fmt.Errorf("no QEMU process for handle %d: socket connection failed", handle)
	}

	client, err := QMPDial(proc.socketPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("QMP connect for handle %d: %w", handle, err)
	}
	defer client.Close()

	if err := client.Execute(command, nil); err != nil {
		return fmt.Errorf("QMP command %q for handle %d: %w", command, handle, err)
	}

	return nil
}
