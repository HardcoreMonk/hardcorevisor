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
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
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
		ID:            handle,
		Name:          name,
		State:         "configured",
		VCPUs:         vcpus,
		MemoryMB:      memoryMB,
		Node:          "local",
		Backend:       "qemu",
		Type:          "vm",
		RestartPolicy: "always",
		CreatedAt:     time.Now(),
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
	if err := b.transition(handle, "running", "cont"); err != nil {
		return err
	}
	// In Real mode, verify state via QueryStatus
	if !b.emulated {
		if err := b.verifyQMPStatus(handle, true); err != nil {
			slog.Warn("QMP post-start verification failed", "handle", handle, "error", err)
		}
	}
	return nil
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
	cp := *vm
	return &cp, nil
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

// ── Snapshot operations ─────────────────────────────────

// SnapshotVM 은 VM의 현재 상태에 대한 스냅샷을 생성한다.
//
// 동작 모드별 처리:
//   - Emulated 모드: VM 메타데이터(Snapshots 맵)에 스냅샷 이름과 시각을 기록 (시뮬레이션)
//   - Real 모드: QMP "savevm" 명령을 전송하여 QEMU 내부 스냅샷을 생성
//     QMP 소켓에 연결 (5초 타임아웃) → savevm 실행 → 연결 해제
//
// 매개변수:
//   - handle: VM 핸들 ID (10000+)
//   - name: 스냅샷 이름 (고유해야 함)
//
// 에러 조건: VM 미존재, QMP 소켓 연결 실패, savevm 명령 실패
// 부작용: Real 모드에서 QEMU 디스크 이미지에 스냅샷 데이터가 기록됨
// 동시 호출 안전성: 안전 (mu.Lock 사용)
func (b *QEMUBackend) SnapshotVM(handle int32, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	vm, ok := b.vms[handle]
	if !ok {
		return fmt.Errorf("VM not found: %d", handle)
	}

	if b.emulated {
		// Store snapshot name in VM metadata (simulated)
		if vm.Snapshots == nil {
			vm.Snapshots = make(map[string]time.Time)
		}
		vm.Snapshots[name] = time.Now()
		return nil
	}

	// Real mode: send QMP savevm command
	proc, ok := b.processes[handle]
	if !ok {
		return fmt.Errorf("no QEMU process for handle %d", handle)
	}

	client, err := QMPDial(proc.socketPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("QMP connect for snapshot handle %d: %w", handle, err)
	}
	defer client.Close()

	if err := client.Execute("savevm", map[string]any{"name": name}); err != nil {
		return fmt.Errorf("QMP savevm for handle %d: %w", handle, err)
	}

	return nil
}

// RestoreSnapshot 은 VM을 이전에 저장된 스냅샷 상태로 복원한다.
//
// 동작 모드별 처리:
//   - Emulated 모드: Snapshots 맵에서 스냅샷 이름 존재 여부만 확인 (시뮬레이션)
//   - Real 모드: QMP "loadvm" 명령을 전송하여 QEMU 내부 스냅샷에서 복원
//     QMP 소켓에 연결 (5초 타임아웃) → loadvm 실행 → 연결 해제
//
// 매개변수:
//   - handle: VM 핸들 ID (10000+)
//   - name: 복원할 스냅샷 이름
//
// 에러 조건: VM 미존재, 스냅샷 이름 미존재 (Emulated), QMP 연결/명령 실패 (Real)
// 부작용: Real 모드에서 VM의 메모리/디스크 상태가 스냅샷 시점으로 변경됨
// 동시 호출 안전성: 안전 (mu.Lock 사용)
func (b *QEMUBackend) RestoreSnapshot(handle int32, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	vm, ok := b.vms[handle]
	if !ok {
		return fmt.Errorf("VM not found: %d", handle)
	}

	if b.emulated {
		// Verify snapshot exists in metadata
		if vm.Snapshots == nil || vm.Snapshots[name].IsZero() {
			return fmt.Errorf("snapshot not found: %s", name)
		}
		return nil
	}

	// Real mode: send QMP loadvm command
	proc, ok := b.processes[handle]
	if !ok {
		return fmt.Errorf("no QEMU process for handle %d", handle)
	}

	client, err := QMPDial(proc.socketPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("QMP connect for restore handle %d: %w", handle, err)
	}
	defer client.Close()

	if err := client.Execute("loadvm", map[string]any{"name": name}); err != nil {
		return fmt.Errorf("QMP loadvm for handle %d: %w", handle, err)
	}

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

	// Exponential backoff retry for QMP socket connection.
	// Max 5 retries: 100ms → 200ms → 400ms → 800ms → 1600ms
	var client *QMPClient
	backoff := 100 * time.Millisecond
	const maxRetries = 5
	for attempt := 0; attempt < maxRetries; attempt++ {
		time.Sleep(backoff)
		client, err = QMPDial(socketPath, 5*time.Second)
		if err == nil {
			break
		}
		slog.Debug("QMP connect retry", "handle", handle, "attempt", attempt+1, "backoff", backoff, "error", err)
		backoff *= 2
	}
	if err != nil {
		// Kill the process if we can't connect after all retries
		cmd.Process.Kill()
		cmd.Wait()
		delete(b.processes, handle)
		return fmt.Errorf("QMP socket connection failed for handle %d after %d retries: %w", handle, maxRetries, err)
	}
	client.Close()

	// Start process monitor goroutine
	go b.monitorProcess(handle)

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

// monitorProcess 는 QEMU 프로세스를 주기적으로 감시하는 고루틴이다.
// 프로세스가 죽으면 VM 상태를 "stopped"로 갱신한다.
// 감시 주기: 5초
func (b *QEMUBackend) monitorProcess(handle int32) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		b.mu.RLock()
		proc, procOk := b.processes[handle]
		vm, vmOk := b.vms[handle]
		b.mu.RUnlock()

		if !procOk || !vmOk {
			// VM or process entry removed (e.g., DestroyVM called)
			return
		}

		if proc.cmd == nil || proc.cmd.Process == nil {
			return
		}

		// Signal(0) checks if process is alive without sending a signal
		err := proc.cmd.Process.Signal(syscall.Signal(0))
		if err != nil {
			slog.Warn("QEMU process died", "handle", handle, "error", err)
			b.mu.Lock()
			if v, ok := b.vms[handle]; ok {
				v.State = "stopped"
			}
			delete(b.processes, handle)
			b.mu.Unlock()
			return
		}

		_ = vm // used for existence check above
	}
}

// verifyQMPStatus queries the QEMU process state via QMP after a state transition
// and returns whether the state matches the expected target state.
// This is used in Real mode to verify that QMP commands took effect.
func (b *QEMUBackend) verifyQMPStatus(handle int32, expectedRunning bool) error {
	proc, ok := b.processes[handle]
	if !ok {
		return nil // No process — skip verification (emulated mode)
	}

	client, err := QMPDial(proc.socketPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("QMP connect for status query handle %d: %w", handle, err)
	}
	defer client.Close()

	status, err := client.QueryStatus()
	if err != nil {
		return fmt.Errorf("QMP query-status handle %d: %w", handle, err)
	}

	isRunning := (status == "running")
	if isRunning != expectedRunning {
		return fmt.Errorf("QMP state mismatch for handle %d: expected running=%v, got status=%q", handle, expectedRunning, status)
	}

	return nil
}
