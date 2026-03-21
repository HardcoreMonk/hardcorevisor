// Package compute — QEMU Backend for general-purpose VM workloads
//
// Implements VMMBackend using QEMU Machine Protocol (QMP) for VM lifecycle.
// Suitable for: Windows VMs, GPU passthrough, legacy OS, full device emulation.
//
// Architecture (ADR-006):
//
//	Go Controller
//	    │
//	    ├── QEMUBackend (this) ──→ QMP socket ──→ qemu-system-x86_64
//	    │                     ──→ libvirt (future)
//	    └── RustVMMBackend    ──→ vmcore FFI ──→ /dev/kvm
package compute

import (
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// qemuProcess tracks a QEMU process and its QMP socket path.
type qemuProcess struct {
	socketPath string
	cmd        *exec.Cmd
}

// QEMUBackend manages VMs via QEMU/QMP.
// Current implementation is in-memory for dev/test.
// Production will use QMP unix socket or libvirt API.
type QEMUBackend struct {
	mu        sync.RWMutex
	vms       map[int32]*VMInfo
	processes map[int32]*qemuProcess
	nextID    atomic.Int32
	qmpAddr   string // QMP socket base path, e.g. /var/run/hcv
	emulated  bool   // true = in-memory emulation (no real QEMU)
}

// QEMUConfig holds QEMU backend configuration.
type QEMUConfig struct {
	QMPSocket string // unix socket path, e.g. /var/run/qemu/qmp.sock
	Emulated  bool   // true = in-memory mock (no QEMU binary needed)
}

// NewQEMUBackend creates a QEMU backend.
// If config is nil or Emulated is true, uses in-memory emulation.
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
		if err := b.qmpCommand(handle, "quit"); err != nil {
			return fmt.Errorf("qemu destroy: %w", err)
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

// Valid QEMU state transitions (matches kvm_mgr.rs VmState)
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
