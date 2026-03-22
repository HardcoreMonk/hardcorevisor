// Package ffi — Mock VMCore 백엔드: libvmcore.a 없이 테스트하기 위한 순수 Go 구현
//
// # 패키지 목적
//
// 실제 CGo 바인딩(vmcore.go)과 동일한 VMCoreBackend 인터페이스를 구현하되,
// 순수 Go 인메모리 상태 머신으로 동작한다. Rust 컴파일 없이 테스트 가능.
//
// # 아키텍처 위치
//
//	ComputeService
//	    └── RustVMMBackend
//	            │ VMCoreBackend 인터페이스
//	            ├── MockVMCore (이 파일, 기본 사용)
//	            └── vmcore.go  (빌드 태그 cgo_vmcore 시 사용)
//
// # VM 상태 머신
//
//	Created(0) → Configured(1) → Running(2) ⇄ Paused(3)
//	                           ↘ Stopped(4)  ↙
//
// stateTransitionAllowed()가 유효한 전이를 검증한다.
package ffi

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// VMCoreBackend — vmcore 작업을 위한 인터페이스.
// 실제 CGo 바인딩(vmcore.go)과 Mock 구현(이 파일) 모두 이 인터페이스를 구현한다.
type VMCoreBackend interface {
	// Init — vmcore 라이브러리를 초기화한다.
	Init() error
	// Shutdown — vmcore를 종료하고 모든 VM을 정리한다.
	Shutdown()
	// Version — vmcore 버전 문자열을 반환한다 (예: "mock-0.1.0").
	Version() string
	// CreateVM — 새 VM을 생성하고 handle(양의 정수)을 반환한다.
	CreateVM() (int32, error)
	// DestroyVM — VM을 삭제한다. handle이 없으면 ErrNotFound.
	DestroyVM(handle int32) error
	// ConfigureVM — VM의 vCPU와 메모리를 설정한다 (Created → Configured 전이).
	ConfigureVM(handle int32, vcpus uint32, memoryMB uint64) error
	// StartVM — VM을 시작한다 (Configured → Running 전이).
	StartVM(handle int32) error
	// StopVM — VM을 중지한다 (Running/Paused → Stopped 전이).
	StopVM(handle int32) error
	// PauseVM — VM을 일시정지한다 (Running → Paused 전이).
	PauseVM(handle int32) error
	// ResumeVM — VM을 재개한다 (Paused → Running 전이).
	ResumeVM(handle int32) error
	// GetVMState — VM의 현재 상태 코드를 반환한다 (VMState* 상수).
	GetVMState(handle int32) (int32, error)
	// VMCount — 현재 관리 중인 VM 수를 반환한다.
	VMCount() int32
}

// VMState 상수 — vmcore의 VmState 열거형을 미러링한다.
// Rust 측 panic_barrier.rs의 ErrorCode와 동기화되어야 한다.
const (
	VMStateCreated    = 0
	VMStateConfigured = 1
	VMStateRunning    = 2
	VMStatePaused     = 3
	VMStateStopped    = 4
)

// mockVM — 인메모리 VM 인스턴스 (Mock 전용).
type mockVM struct {
	handle   int32
	state    int32
	vcpus    uint32
	memoryMB uint64
}

// stateTransitionAllowed — 주어진 상태 전이가 유효한지 검사한다.
// vmcore의 kvm_mgr.rs::VmState::can_transition_to()와 동일한 규칙을 적용한다.
func stateTransitionAllowed(from, to int32) bool {
	switch {
	case from == VMStateCreated && to == VMStateConfigured:
		return true
	case from == VMStateConfigured && to == VMStateRunning:
		return true
	case from == VMStateConfigured && to == VMStateStopped:
		return true
	case from == VMStateRunning && to == VMStatePaused:
		return true
	case from == VMStateRunning && to == VMStateStopped:
		return true
	case from == VMStatePaused && to == VMStateRunning:
		return true
	case from == VMStatePaused && to == VMStateStopped:
		return true
	default:
		return false
	}
}

// MockVMCore — VMCoreBackend의 순수 Go Mock 구현체.
// mutex로 동시 접근을 보호하며, nextHandle로 고유 핸들을 할당한다.
type MockVMCore struct {
	mu         sync.RWMutex
	vms        map[int32]*mockVM
	nextHandle atomic.Int32
	initialized bool
}

// NewMockVMCore — 새 Mock VMCore 백엔드를 생성한다.
// Handle은 1부터 순차적으로 할당된다.
func NewMockVMCore() *MockVMCore {
	m := &MockVMCore{
		vms: make(map[int32]*mockVM),
	}
	m.nextHandle.Store(1)
	return m
}

func (m *MockVMCore) Init() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initialized = true
	return nil
}

func (m *MockVMCore) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initialized = false
	m.vms = make(map[int32]*mockVM)
}

func (m *MockVMCore) Version() string {
	return "mock-0.1.0"
}

func (m *MockVMCore) CreateVM() (int32, error) {
	handle := m.nextHandle.Add(1) - 1
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vms[handle] = &mockVM{
		handle: handle,
		state:  VMStateCreated,
	}
	return handle, nil
}

func (m *MockVMCore) DestroyVM(handle int32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.vms[handle]; !ok {
		return &FFIError{Code: ErrNotFound, Op: "vm_destroy"}
	}
	delete(m.vms, handle)
	return nil
}

func (m *MockVMCore) ConfigureVM(handle int32, vcpus uint32, memoryMB uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[handle]
	if !ok {
		return &FFIError{Code: ErrNotFound, Op: "vm_configure"}
	}
	if !stateTransitionAllowed(vm.state, VMStateConfigured) {
		return &FFIError{Code: ErrInvalidState, Op: "vm_configure"}
	}
	vm.state = VMStateConfigured
	vm.vcpus = vcpus
	vm.memoryMB = memoryMB
	return nil
}

func (m *MockVMCore) transitionVM(handle int32, targetState int32, op string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[handle]
	if !ok {
		return &FFIError{Code: ErrNotFound, Op: op}
	}
	if !stateTransitionAllowed(vm.state, targetState) {
		return &FFIError{Code: ErrInvalidState, Op: op}
	}
	vm.state = targetState
	return nil
}

func (m *MockVMCore) StartVM(handle int32) error {
	return m.transitionVM(handle, VMStateRunning, "vm_start")
}

func (m *MockVMCore) StopVM(handle int32) error {
	return m.transitionVM(handle, VMStateStopped, "vm_stop")
}

func (m *MockVMCore) PauseVM(handle int32) error {
	return m.transitionVM(handle, VMStatePaused, "vm_pause")
}

func (m *MockVMCore) ResumeVM(handle int32) error {
	// Resume is only valid from Paused state (not from Configured)
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[handle]
	if !ok {
		return &FFIError{Code: ErrNotFound, Op: "vm_resume"}
	}
	if vm.state != VMStatePaused {
		return &FFIError{Code: ErrInvalidState, Op: "vm_resume"}
	}
	vm.state = VMStateRunning
	return nil
}

func (m *MockVMCore) GetVMState(handle int32) (int32, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	vm, ok := m.vms[handle]
	if !ok {
		return -1, &FFIError{Code: ErrNotFound, Op: "vm_get_state"}
	}
	return vm.state, nil
}

func (m *MockVMCore) VMCount() int32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return int32(len(m.vms))
}

// StateString — VmState 정수 코드를 문자열로 변환한다.
// API 응답에서 사용된다 (예: 2 → "running").
func StateString(state int32) string {
	switch state {
	case VMStateCreated:
		return "created"
	case VMStateConfigured:
		return "configured"
	case VMStateRunning:
		return "running"
	case VMStatePaused:
		return "paused"
	case VMStateStopped:
		return "stopped"
	default:
		return fmt.Sprintf("unknown(%d)", state)
	}
}
