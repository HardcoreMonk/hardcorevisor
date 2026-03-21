// Package ffi — Mock VMCore backend for testing without libvmcore.a
//
// Provides the same interface as the real CGo bindings but uses
// pure Go in-memory state, enabling testing without Rust compilation.
package ffi

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// VMCoreBackend defines the interface for vmcore operations.
// Both real CGo and mock implementations satisfy this interface.
type VMCoreBackend interface {
	Init() error
	Shutdown()
	Version() string
	CreateVM() (int32, error)
	DestroyVM(handle int32) error
	ConfigureVM(handle int32, vcpus uint32, memoryMB uint64) error
	StartVM(handle int32) error
	StopVM(handle int32) error
	PauseVM(handle int32) error
	ResumeVM(handle int32) error
	GetVMState(handle int32) (int32, error)
	VMCount() int32
}

// VMState mirrors vmcore VmState enum
const (
	VMStateCreated    = 0
	VMStateConfigured = 1
	VMStateRunning    = 2
	VMStatePaused     = 3
	VMStateStopped    = 4
)

// mockVM represents an in-memory VM instance
type mockVM struct {
	handle   int32
	state    int32
	vcpus    uint32
	memoryMB uint64
}

// stateTransitionAllowed checks if the given transition is valid
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

// MockVMCore is a pure-Go mock implementation of VMCoreBackend.
type MockVMCore struct {
	mu         sync.RWMutex
	vms        map[int32]*mockVM
	nextHandle atomic.Int32
	initialized bool
}

// NewMockVMCore creates a new mock VMCore backend.
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

// StateString converts a VmState int to its string representation.
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
