// Package compute — Dual VMM 백엔드 셀렉터 패턴 기반 VM 생명주기 관리 서비스
//
// # 패키지 목적
//
// VM 생명주기(생성/시작/중지/일시정지/재개/삭제)를 VMM 백엔드를 통해 관리한다.
//
// # 아키텍처 위치
//
//	API/gRPC 레이어
//	    │
//	    ▼
//	ComputeService (또는 PersistentComputeService)
//	    │
//	    ▼
//	BackendSelector — 워크로드 정책에 따라 백엔드 선택
//	    ├── RustVMMBackend (vmcore FFI, 고성능 Linux microVM)
//	    └── QEMUBackend (QMP, 범용 VM: Windows/GPU/레거시)
//
// # 사용된 패턴
//
//   - Strategy 패턴: VMMBackend 인터페이스로 백엔드 교체 가능
//   - Decorator 패턴: PersistentComputeService가 ComputeService를 래핑하여 영속화
//   - Provider 패턴: ComputeProvider 인터페이스로 API 레이어에서 투명하게 사용
//
// # Handle 범위 규약
//
//   - RustVMM: 1 ~ 9999
//   - QEMU: 10000+
package compute

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/pkg/ffi"
)

// 백엔드 선택 정책 상수
const (
	PolicyAuto    = "auto"    // 워크로드 특성에 따라 자동 선택
	PolicyRustVMM = "rustvmm" // RustVMM 백엔드 강제 사용
	PolicyQEMU    = "qemu"    // QEMU 백엔드 강제 사용
)

// VMInfo — API 레이어에 노출되는 VM의 전체 상태.
// JSON 직렬화되어 REST/gRPC 응답으로 반환된다.
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

// VMMBackend — VMM 백엔드가 구현해야 하는 인터페이스.
//
// RustVMMBackend와 QEMUBackend가 이 인터페이스를 구현한다.
// BackendSelector가 이 인터페이스를 통해 백엔드를 다형적으로 사용한다.
type VMMBackend interface {
	// Name — 백엔드의 고유 이름을 반환한다 ("rustvmm" 또는 "qemu").
	Name() string
	// CreateVM — 새 VM을 생성한다. 초기 상태는 "configured".
	CreateVM(name string, vcpus uint32, memoryMB uint64) (*VMInfo, error)
	// DestroyVM — VM을 삭제한다. VM이 없으면 에러.
	DestroyVM(handle int32) error
	// StartVM — VM을 시작한다 (configured/stopped → running).
	StartVM(handle int32) error
	// StopVM — VM을 중지한다 (running/paused → stopped).
	StopVM(handle int32) error
	// PauseVM — VM을 일시정지한다 (running → paused).
	PauseVM(handle int32) error
	// ResumeVM — VM을 재개한다 (paused → running).
	ResumeVM(handle int32) error
	// GetVM — VM 정보를 조회한다. VM이 없으면 에러.
	GetVM(handle int32) (*VMInfo, error)
	// ListVMs — 이 백엔드가 관리하는 모든 VM 목록을 반환한다.
	ListVMs() []*VMInfo
}

// ── RustVMM Backend ─────────────────────────────────────────

// RustVMMBackend — vmcore FFI(실제 CGo 또는 Mock)를 VMMBackend로 래핑한다.
// 고성능 Linux microVM 용도이며, Handle 범위는 1~9999이다.
type RustVMMBackend struct {
	core ffi.VMCoreBackend
	mu   sync.RWMutex
	vms  map[int32]*VMInfo
}

// NewRustVMMBackend — VMCore 구현체를 사용하는 RustVMM 백엔드를 생성한다.
//
// # 매개변수
//   - core: VMCoreBackend 인터페이스 (MockVMCore 또는 실제 CGo 바인딩)
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

// BackendSelector — 등록된 VMM 백엔드 중 적합한 것을 선택하는 라우터.
// Select()로 명시적 선택, SelectAuto()로 워크로드 기반 자동 선택을 지원한다.
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

// ComputeProvider — ComputeService와 PersistentComputeService가 공통으로 구현하는 인터페이스.
// API 레이어와 gRPC 레이어에서 이 인터페이스를 사용하여 영속화 래퍼를 투명하게 교체할 수 있다.
type ComputeProvider interface {
	// CreateVM — VM을 생성한다. backendHint가 비어 있으면 기본 백엔드(rustvmm) 사용.
	CreateVM(name string, vcpus uint32, memoryMB uint64, backendHint string) (*VMInfo, error)
	// GetVM — VM 정보를 조회한다. 모든 백엔드를 검색한다.
	GetVM(handle int32) (*VMInfo, error)
	// ListVMs — 모든 백엔드의 VM 목록을 합쳐 반환한다.
	ListVMs() []*VMInfo
	// ActionVM — VM 생명주기 액션 수행 (start/stop/pause/resume).
	ActionVM(handle int32, action string) (*VMInfo, error)
	// DestroyVM — VM을 삭제한다.
	DestroyVM(handle int32) error
	// ListBackends — 등록된 VMM 백엔드 정보 목록을 반환한다.
	ListBackends() []BackendInfo
	// MigrateVM — VM을 다른 노드로 마이그레이션한다 (현재 시뮬레이션).
	MigrateVM(handle int32, targetNode string) error
}

// ── Compute Service ─────────────────────────────────────────

// ComputeService — 여러 백엔드에 걸친 VM 작업을 오케스트레이션하는 서비스.
// BackendSelector를 통해 VM 생성 시 적합한 백엔드를 선택하고,
// findBackendForVM()으로 기존 VM의 소유 백엔드를 찾아 작업을 위임한다.
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

// MigrateVM performs a simulated live migration by updating the VM's node field.
func (cs *ComputeService) MigrateVM(handle int32, targetNode string) error {
	backend, err := cs.findBackendForVM(handle)
	if err != nil {
		return err
	}

	vm, err := backend.GetVM(handle)
	if err != nil {
		return err
	}

	vm.Node = targetNode
	return nil
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
