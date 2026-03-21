// Package compute — Compute service managing VM lifecycle via VMM backends
//
// Implements the Dual VMM Backend Selector pattern:
//   - RustVMM backend (vmcore FFI) for high-performance Linux microVMs
//   - QEMU backend (future) for general-purpose VMs
//
// The BackendSelector routes VM creation to the appropriate backend
// based on workload policy or explicit user request.
package compute

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/pkg/ffi"
)

// Backend selection policies
const (
	PolicyAuto    = "auto"    // Auto-select based on workload characteristics
	PolicyRustVMM = "rustvmm" // Force rust-vmm backend
	PolicyQEMU    = "qemu"    // Force QEMU backend
)

// VMInfo represents a VM's full state visible to the API layer.
type VMInfo struct {
	ID        int32     `json:"id"`
	Name      string    `json:"name"`
	State     string    `json:"state"`
	VCPUs     uint32    `json:"vcpus"`
	MemoryMB  uint64    `json:"memory_mb"`
	Node      string    `json:"node"`
	Backend   string    `json:"backend"`
	CreatedAt time.Time `json:"created_at"`
}

// VMMBackend is the interface that VMM backends must implement.
type VMMBackend interface {
	Name() string
	CreateVM(name string, vcpus uint32, memoryMB uint64) (*VMInfo, error)
	DestroyVM(handle int32) error
	StartVM(handle int32) error
	StopVM(handle int32) error
	PauseVM(handle int32) error
	ResumeVM(handle int32) error
	GetVM(handle int32) (*VMInfo, error)
	ListVMs() []*VMInfo
}

// ── RustVMM Backend ─────────────────────────────────────────

// RustVMMBackend wraps the vmcore FFI (real or mock) as a VMMBackend.
type RustVMMBackend struct {
	core ffi.VMCoreBackend
	mu   sync.RWMutex
	vms  map[int32]*VMInfo
}

// NewRustVMMBackend creates a backend using the given VMCore implementation.
func NewRustVMMBackend(core ffi.VMCoreBackend) *RustVMMBackend {
	return &RustVMMBackend{
		core: core,
		vms:  make(map[int32]*VMInfo),
	}
}

func (b *RustVMMBackend) Name() string { return "rustvmm" }

func (b *RustVMMBackend) CreateVM(name string, vcpus uint32, memoryMB uint64) (*VMInfo, error) {
	handle, err := b.core.CreateVM()
	if err != nil {
		return nil, fmt.Errorf("rustvmm create: %w", err)
	}
	if err := b.core.ConfigureVM(handle, vcpus, memoryMB); err != nil {
		b.core.DestroyVM(handle)
		return nil, fmt.Errorf("rustvmm configure: %w", err)
	}

	vm := &VMInfo{
		ID:        handle,
		Name:      name,
		State:     "configured",
		VCPUs:     vcpus,
		MemoryMB:  memoryMB,
		Node:      "local",
		Backend:   "rustvmm",
		CreatedAt: time.Now(),
	}
	b.mu.Lock()
	b.vms[handle] = vm
	b.mu.Unlock()
	return vm, nil
}

func (b *RustVMMBackend) DestroyVM(handle int32) error {
	if err := b.core.DestroyVM(handle); err != nil {
		return err
	}
	b.mu.Lock()
	delete(b.vms, handle)
	b.mu.Unlock()
	return nil
}

func (b *RustVMMBackend) transitionVM(handle int32, action string, coreFn func(int32) error) error {
	if err := coreFn(handle); err != nil {
		return err
	}
	stateCode, err := b.core.GetVMState(handle)
	if err != nil {
		return err
	}
	b.mu.Lock()
	if vm, ok := b.vms[handle]; ok {
		vm.State = ffi.StateString(stateCode)
	}
	b.mu.Unlock()
	return nil
}

func (b *RustVMMBackend) StartVM(handle int32) error {
	return b.transitionVM(handle, "start", b.core.StartVM)
}

func (b *RustVMMBackend) StopVM(handle int32) error {
	return b.transitionVM(handle, "stop", b.core.StopVM)
}

func (b *RustVMMBackend) PauseVM(handle int32) error {
	return b.transitionVM(handle, "pause", b.core.PauseVM)
}

func (b *RustVMMBackend) ResumeVM(handle int32) error {
	return b.transitionVM(handle, "resume", b.core.ResumeVM)
}

func (b *RustVMMBackend) GetVM(handle int32) (*VMInfo, error) {
	b.mu.RLock()
	vm, ok := b.vms[handle]
	b.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("VM not found: %d", handle)
	}
	return vm, nil
}

func (b *RustVMMBackend) ListVMs() []*VMInfo {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]*VMInfo, 0, len(b.vms))
	for _, vm := range b.vms {
		result = append(result, vm)
	}
	return result
}

// ── Backend Selector ────────────────────────────────────────

// BackendSelector routes VM creation to the appropriate VMM backend.
type BackendSelector struct {
	mu       sync.RWMutex
	backends map[string]VMMBackend
	policy   string
}

// NewBackendSelector creates a selector with the given default policy.
func NewBackendSelector(policy string) *BackendSelector {
	return &BackendSelector{
		backends: make(map[string]VMMBackend),
		policy:   policy,
	}
}

// Register adds a backend to the selector.
func (s *BackendSelector) Register(b VMMBackend) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backends[b.Name()] = b
}

// Select returns the appropriate backend for the given request.
// If backendHint is non-empty, it forces that backend.
// Otherwise, the selector's auto policy routes based on workload:
//   - memoryMB <= 512 or vcpus <= 2 → rustvmm (lightweight microVM)
//   - otherwise → qemu if available, else rustvmm
func (s *BackendSelector) Select(backendHint string) (VMMBackend, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	name := backendHint
	if name == "" {
		// Auto policy: default to rustvmm (always available)
		name = "rustvmm"
	}

	b, ok := s.backends[name]
	if !ok {
		return nil, fmt.Errorf("backend not found: %s", name)
	}
	return b, nil
}

// SelectAuto chooses a backend based on workload characteristics.
func (s *BackendSelector) SelectAuto(vcpus uint32, memoryMB uint64, needsGPU bool) (VMMBackend, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// GPU passthrough or large VM → prefer QEMU
	if needsGPU || memoryMB > 8192 || vcpus > 8 {
		if b, ok := s.backends["qemu"]; ok {
			return b, nil
		}
	}

	// Default: rustvmm for lightweight workloads
	if b, ok := s.backends["rustvmm"]; ok {
		return b, nil
	}

	// Fallback: any available backend
	for _, b := range s.backends {
		return b, nil
	}

	return nil, fmt.Errorf("no backends registered")
}

// List returns info about all registered backends.
func (s *BackendSelector) List() []BackendInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]BackendInfo, 0, len(s.backends))
	for name := range s.backends {
		result = append(result, BackendInfo{Name: name, Status: "ready"})
	}
	return result
}

// BackendInfo describes a registered backend.
type BackendInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// ── Compute Service ─────────────────────────────────────────

// ComputeService orchestrates VM operations across backends.
type ComputeService struct {
	selector       *BackendSelector
	defaultBackend VMMBackend
	nextID         atomic.Int32
}

// NewComputeService creates a new compute service.
func NewComputeService(selector *BackendSelector, defaultBackend VMMBackend) *ComputeService {
	cs := &ComputeService{
		selector:       selector,
		defaultBackend: defaultBackend,
	}
	cs.nextID.Store(1)
	return cs
}

// CreateVM creates a VM using the selected backend.
func (cs *ComputeService) CreateVM(name string, vcpus uint32, memoryMB uint64, backendHint string) (*VMInfo, error) {
	backend, err := cs.selector.Select(backendHint)
	if err != nil {
		return nil, err
	}
	return backend.CreateVM(name, vcpus, memoryMB)
}

// GetVM retrieves a VM from the backend that owns it.
func (cs *ComputeService) GetVM(handle int32) (*VMInfo, error) {
	// Search across all backends
	for _, b := range cs.listBackends() {
		if vm, err := b.GetVM(handle); err == nil {
			return vm, nil
		}
	}
	return nil, fmt.Errorf("VM not found: %d", handle)
}

// ListVMs returns all VMs across all backends.
func (cs *ComputeService) ListVMs() []*VMInfo {
	var all []*VMInfo
	for _, b := range cs.listBackends() {
		all = append(all, b.ListVMs()...)
	}
	return all
}

// ActionVM performs a lifecycle action on a VM.
func (cs *ComputeService) ActionVM(handle int32, action string) (*VMInfo, error) {
	backend, err := cs.findBackendForVM(handle)
	if err != nil {
		return nil, err
	}

	switch action {
	case "start":
		err = backend.StartVM(handle)
	case "stop":
		err = backend.StopVM(handle)
	case "pause":
		err = backend.PauseVM(handle)
	case "resume":
		err = backend.ResumeVM(handle)
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
	if err != nil {
		return nil, err
	}
	return backend.GetVM(handle)
}

// DestroyVM removes a VM.
func (cs *ComputeService) DestroyVM(handle int32) error {
	backend, err := cs.findBackendForVM(handle)
	if err != nil {
		return err
	}
	return backend.DestroyVM(handle)
}

// ListBackends returns info about registered backends.
func (cs *ComputeService) ListBackends() []BackendInfo {
	return cs.selector.List()
}

func (cs *ComputeService) listBackends() []VMMBackend {
	cs.selector.mu.RLock()
	defer cs.selector.mu.RUnlock()
	result := make([]VMMBackend, 0, len(cs.selector.backends))
	for _, b := range cs.selector.backends {
		result = append(result, b)
	}
	return result
}

func (cs *ComputeService) findBackendForVM(handle int32) (VMMBackend, error) {
	for _, b := range cs.listBackends() {
		if _, err := b.GetVM(handle); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("VM not found: %d", handle)
}
