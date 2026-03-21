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
	"sync"
	"sync/atomic"
	"time"
)

// QEMUBackend manages VMs via QEMU/QMP.
// Current implementation is in-memory for dev/test.
// Production will use QMP unix socket or libvirt API.
type QEMUBackend struct {
	mu       sync.RWMutex
	vms      map[int32]*VMInfo
	nextID   atomic.Int32
	qmpAddr  string // QMP socket path (future)
	emulated bool   // true = in-memory emulation (no real QEMU)
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
		vms:      make(map[int32]*VMInfo),
		emulated: true,
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

// ── QMP Protocol (stubs for future implementation) ───

// qmpCreateVM launches a QEMU process with QMP enabled.
// Production: exec qemu-system-x86_64 -qmp unix:/path,server,nowait ...
func (b *QEMUBackend) qmpCreateVM(handle int32, name string, vcpus uint32, memoryMB uint64) error {
	// TODO: Launch QEMU process with:
	//   qemu-system-x86_64 \
	//     -name <name> \
	//     -machine q35,accel=kvm \
	//     -smp <vcpus> \
	//     -m <memoryMB> \
	//     -qmp unix:/var/run/hcv/qmp-<handle>.sock,server,nowait \
	//     -nographic
	return fmt.Errorf("real QMP not yet implemented (handle=%d)", handle)
}

// qmpCommand sends a QMP command to a running QEMU instance.
// Production: connect to unix socket, send {"execute": "<cmd>"}, parse response.
func (b *QEMUBackend) qmpCommand(handle int32, command string) error {
	// TODO: Connect to /var/run/hcv/qmp-<handle>.sock
	//   Send: {"execute": "<command>"}
	//   Read: {"return": {}}
	return fmt.Errorf("real QMP not yet implemented (handle=%d, cmd=%s)", handle, command)
}
