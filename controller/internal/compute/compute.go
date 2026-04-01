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
	"context"
	"fmt"
	"log/slog"
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
	PolicyLXC     = "lxc"     // LXC 백엔드 강제 사용 (컨테이너)
)

// MigrationPhase 상수 — 라이브 마이그레이션의 각 단계를 나타낸다.
//
// 단계 진행 순서:
//
//	MigrationPending → MigrationPreCheck → MigrationTransfer → MigrationSwitchover → MigrationCompleted
//	                                                                                → MigrationFailed
//
// 각 단계에서 context 취소(CancelMigration) 시 MigrationFailed로 전이된다.
// MigrationStatus.Phase 필드에 저장되어 REST/gRPC 응답으로 반환된다.
const (
	MigrationPending    = "pending"    // 마이그레이션 요청됨, 아직 시작 전
	MigrationPreCheck   = "pre-check"  // VM 상태 및 대상 노드 사전 검증 중
	MigrationTransfer   = "transfer"   // 메모리/디스크 전송 중 (Progress 10→30→60→90)
	MigrationSwitchover = "switchover" // 소스 일시정지 → 최종 동기화 → 노드 전환 (Progress 95)
	MigrationCompleted  = "completed"  // 마이그레이션 성공 완료 (Progress 100)
	MigrationFailed     = "failed"     // 마이그레이션 실패 또는 취소됨
)

// VMInfo — API 레이어에 노출되는 VM의 전체 상태.
// JSON 직렬화되어 REST/gRPC 응답으로 반환된다.
//
// 필드:
//   - ID: VM 핸들 (RustVMM: 1~9999, QEMU: 10000+)
//   - State: 현재 상태 ("created", "configured", "running", "paused", "stopped")
//   - Node: VM이 실행 중인 노드 이름 (마이그레이션 시 변경됨)
//   - Backend: VM을 관리하는 백엔드 이름 ("rustvmm" 또는 "qemu")
//   - Type: 인스턴스 타입 ("vm" 또는 "ct" — 컨테이너)
//   - RestartPolicy: 장애 복구 정책 ("always", "on-failure", "never")
//   - Snapshots: VM 스냅샷 이름→생성시각 맵 (선택)
//   - RootFS: LXC 컨테이너용 루트 파일시스템 경로 (선택)
//   - Template: VM 생성에 사용된 템플릿 이름 (선택)
//   - Arch: CPU 아키텍처 ("x86_64", "aarch64" 등, 선택)
//   - IPAddress: VM에 할당된 IP 주소 (선택)
type VMInfo struct {
	ID            int32              `json:"id"`
	Name          string             `json:"name"`
	State         string             `json:"state"`
	VCPUs         uint32             `json:"vcpus"`
	MemoryMB      uint64             `json:"memory_mb"`
	Node          string             `json:"node"`
	Backend       string             `json:"backend"`
	Type          string             `json:"type"`
	RestartPolicy string             `json:"restart_policy"`
	CreatedAt     time.Time          `json:"created_at"`
	Snapshots     map[string]time.Time `json:"snapshots,omitempty"`
	RootFS        string             `json:"rootfs,omitempty"`
	Template      string             `json:"template,omitempty"`
	Arch          string             `json:"arch,omitempty"`
	IPAddress     string             `json:"ip_address,omitempty"`
	DiskPath      string             `json:"disk_path,omitempty"`
	ISOPath       string             `json:"iso_path,omitempty"`
	NetworkMode   string             `json:"network_mode,omitempty"`
	VNCPort       int                `json:"vnc_port,omitempty"`
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
		ID:            handle,
		Name:          name,
		State:         "configured",
		VCPUs:         vcpus,
		MemoryMB:      memoryMB,
		Node:          "local",
		Backend:       "rustvmm",
		Type:          "vm",
		RestartPolicy: "always",
		CreatedAt:     time.Now(),
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

// transitionVM 은 vmcore FFI를 통해 VM 상태 전이를 수행하고, 로컬 상태를 동기화하는 내부 헬퍼이다.
// coreFn 실행 후 GetVMState()로 최신 상태를 읽어 vms 맵에 반영한다.
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
	if !ok {
		b.mu.RUnlock()
		return nil, fmt.Errorf("VM not found: %d", handle)
	}
	cp := *vm
	b.mu.RUnlock()
	return &cp, nil
}

func (b *RustVMMBackend) ListVMs() []*VMInfo {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]*VMInfo, 0, len(b.vms))
	for _, vm := range b.vms {
		cp := *vm
		result = append(result, &cp)
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

// NewBackendSelector 는 지정된 기본 정책으로 BackendSelector를 생성한다.
//
// 매개변수:
//   - policy: 기본 백엔드 선택 정책 (PolicyAuto, PolicyRustVMM, PolicyQEMU)
//
// 호출 시점: Controller 초기화 시 ComputeService 생성 전
func NewBackendSelector(policy string) *BackendSelector {
	return &BackendSelector{
		backends: make(map[string]VMMBackend),
		policy:   policy,
	}
}

// Register 는 백엔드를 셀렉터에 등록한다.
// 같은 이름의 백엔드가 이미 있으면 덮어쓴다.
//
// 호출 시점: Controller 초기화 시, RustVMMBackend와 QEMUBackend를 순서대로 등록
// 동시 호출 안전성: 안전 (내부 Lock)
func (s *BackendSelector) Register(b VMMBackend) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backends[b.Name()] = b
}

// Select 는 요청에 적합한 백엔드를 반환한다.
//
// backendHint가 비어 있지 않으면 해당 백엔드를 강제 선택하고,
// 비어 있으면 기본값인 "rustvmm"을 사용한다.
//
// 에러 조건: 지정된 이름의 백엔드가 등록되어 있지 않은 경우
// 동시 호출 안전성: 안전 (내부 RLock)
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

// SelectAuto 는 워크로드 특성에 따라 최적의 백엔드를 자동 선택한다.
//
// 선택 기준:
//   - GPU 패스스루 필요, 메모리 > 8GB, vCPU > 8개 → QEMU (범용/대형 VM)
//   - 그 외 → rustvmm (경량 microVM)
//   - 선택된 백엔드가 없으면 사용 가능한 아무 백엔드 반환
//
// 에러 조건: 등록된 백엔드가 하나도 없는 경우
// 동시 호출 안전성: 안전 (내부 RLock)
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

// List 는 등록된 모든 백엔드의 정보를 반환한다.
//
// 호출 시점: REST GET /api/v1/backends
// 동시 호출 안전성: 안전 (내부 RLock)
func (s *BackendSelector) List() []BackendInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]BackendInfo, 0, len(s.backends))
	for name := range s.backends {
		result = append(result, BackendInfo{Name: name, Status: "ready"})
	}
	return result
}

// BackendInfo 는 등록된 VMM 백엔드의 이름과 상태를 나타낸다.
// REST API 응답으로 JSON 직렬화된다.
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
	// MigrateVM — VM을 다른 노드로 동기 마이그레이션한다.
	MigrateVM(handle int32, targetNode string) error
	// MigrateLive — VM을 다른 노드로 비동기 라이브 마이그레이션한다.
	MigrateLive(handle int32, targetNode string) error
	// CancelMigration — 진행 중인 마이그레이션을 취소한다.
	CancelMigration(handle int32) error
	// GetMigrationStatus — VM 마이그레이션 진행 상태를 조회한다.
	GetMigrationStatus(handle int32) (*MigrationStatus, error)
}

// ── Compute Service ─────────────────────────────────────────

// MigrationStatus — VM 마이그레이션 진행 상태를 나타낸다.
// JSON 직렬화되어 REST/gRPC 응답으로 반환된다.
//
// Phase 진행 순서: "pre-check" → "transferring" → "completed" (또는 "failed")
// Progress: 0~100 퍼센트 (pre-check=0, transferring=50, completed=100)
type MigrationStatus struct {
	VMID        int32     `json:"vm_id"`              // 마이그레이션 대상 VM ID
	SourceNode  string    `json:"source_node"`         // 원래 노드 이름
	TargetNode  string    `json:"target_node"`         // 대상 노드 이름
	Phase       string    `json:"phase"`               // 현재 단계: "pre-check", "transferring", "completed", "failed"
	Progress    int       `json:"progress"`            // 진행률 (0~100)
	StartedAt   time.Time `json:"started_at"`          // 마이그레이션 시작 시각
	CompletedAt time.Time `json:"completed_at,omitempty"` // 완료 시각 (진행 중이면 zero)
	Error       string    `json:"error,omitempty"`     // 실패 시 에러 메시지
}

// ComputeService — 여러 백엔드에 걸친 VM 작업을 오케스트레이션하는 서비스.
// BackendSelector를 통해 VM 생성 시 적합한 백엔드를 선택하고,
// findBackendForVM()으로 기존 VM의 소유 백엔드를 찾아 작업을 위임한다.
type ComputeService struct {
	selector         *BackendSelector
	defaultBackend   VMMBackend
	nextID           atomic.Int32
	migrationsMu     sync.RWMutex
	migrations       map[int32]*MigrationStatus
	activeMigrations map[int32]context.CancelFunc
}

// NewComputeService 는 BackendSelector와 기본 백엔드로 ComputeService를 생성한다.
//
// 매개변수:
//   - selector: 백엔드 라우팅을 담당하는 BackendSelector
//   - defaultBackend: 힌트 없을 때 사용하는 기본 백엔드 (보통 RustVMMBackend)
//
// 호출 시점: Controller 초기화 시
func NewComputeService(selector *BackendSelector, defaultBackend VMMBackend) *ComputeService {
	cs := &ComputeService{
		selector:         selector,
		defaultBackend:   defaultBackend,
		migrations:       make(map[int32]*MigrationStatus),
		activeMigrations: make(map[int32]context.CancelFunc),
	}
	cs.nextID.Store(1)
	return cs
}

// CreateVM 은 선택된 백엔드를 통해 VM을 생성한다.
//
// backendHint가 비어 있으면 기본 백엔드(rustvmm)를 사용한다.
// "qemu"를 지정하면 QEMU 백엔드로 VM을 생성한다.
//
// 호출 시점: REST POST /api/v1/vms
// 에러 조건: 백엔드 선택 실패, 백엔드의 VM 생성 실패
func (cs *ComputeService) CreateVM(name string, vcpus uint32, memoryMB uint64, backendHint string) (*VMInfo, error) {
	backend, err := cs.selector.Select(backendHint)
	if err != nil {
		return nil, err
	}
	return backend.CreateVM(name, vcpus, memoryMB)
}

// GetVM 은 모든 백엔드를 검색하여 VM 정보를 조회한다.
//
// 호출 시점: REST GET /api/v1/vms/{id}
// 에러 조건: 모든 백엔드에서 해당 handle의 VM을 찾지 못한 경우 (404)
func (cs *ComputeService) GetVM(handle int32) (*VMInfo, error) {
	// 모든 백엔드에서 순차적으로 검색
	for _, b := range cs.listBackends() {
		if vm, err := b.GetVM(handle); err == nil {
			return vm, nil
		}
	}
	return nil, fmt.Errorf("VM not found: %d", handle)
}

// ListVMs 는 모든 백엔드의 VM 목록을 합쳐서 반환한다.
//
// 호출 시점: REST GET /api/v1/vms
// 동시 호출 안전성: 안전 (각 백엔드 내부 RLock)
func (cs *ComputeService) ListVMs() []*VMInfo {
	var all []*VMInfo
	for _, b := range cs.listBackends() {
		all = append(all, b.ListVMs()...)
	}
	return all
}

// ActionVM 은 VM에 생명주기 액션을 수행한다.
//
// 지원 액션: "start", "stop", "pause", "resume"
// 처리 순서: findBackendForVM()으로 소유 백엔드 찾기 → 액션 실행 → 최신 상태 반환
//
// 호출 시점: REST POST /api/v1/vms/{id}/{action}
// 에러 조건: VM 미존재, 알 수 없는 액션, 잘못된 상태 전이 (409)
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

// DestroyVM 은 VM을 삭제한다.
//
// 호출 시점: REST DELETE /api/v1/vms/{id}
// 에러 조건: VM 미존재
func (cs *ComputeService) DestroyVM(handle int32) error {
	backend, err := cs.findBackendForVM(handle)
	if err != nil {
		return err
	}
	return backend.DestroyVM(handle)
}

// ListBackends 는 등록된 VMM 백엔드 정보 목록을 반환한다.
//
// 호출 시점: REST GET /api/v1/backends
func (cs *ComputeService) ListBackends() []BackendInfo {
	return cs.selector.List()
}

// MigrateVM 은 동기적 라이브 마이그레이션을 수행한다.
//
// 마이그레이션 단계:
//  1. PreCheck: VM running 확인, 대상 노드 확인
//  2. Transfer: 메모리 전송 시뮬레이션 (10% → 30% → 60% → 90%)
//  3. Switchover: 소스 VM 일시정지, 최종 동기화, 노드 전환, 대상에서 재개
//  4. Complete: status=completed, progress=100
//
// 호출 시점: REST POST /api/v1/vms/{id}/migrate (동기 모드)
// 에러 조건: VM 미존재, VM이 running 상태가 아님, 대상=소스 노드
func (cs *ComputeService) MigrateVM(handle int32, targetNode string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return cs.doMigration(ctx, handle, targetNode)
}

// MigrateLive 은 비동기 라이브 마이그레이션을 goroutine에서 실행한다.
//
// MigrateVM(동기)과 달리, 즉시 반환하고 백그라운드 goroutine에서 마이그레이션을 수행한다.
// 진행 상태는 GetMigrationStatus()로 폴링할 수 있고,
// CancelMigration()으로 context를 취소하여 중단할 수 있다.
//
// 처리 순서:
//  1. 동기 사전 검증: VM 존재 여부, running 상태, 동일 노드 확인 (즉시 에러 반환)
//  2. MigrationStatus 초기화 (pending), activeMigrations에 cancel 함수 등록
//  3. goroutine 시작: doMigration() 실행 → 완료 시 activeMigrations에서 제거
//
// 매개변수:
//   - handle: 마이그레이션할 VM ID
//   - targetNode: 대상 노드 이름
//
// 에러 조건 (동기, 즉시 반환):
//   - VM 미존재, VM이 running 상태가 아님, 대상=소스 노드
//
// 호출 시점: REST POST /api/v1/vms/{id}/migrate (TaskService가 있는 경우 비동기 모드)
func (cs *ComputeService) MigrateLive(handle int32, targetNode string) error {
	// Pre-validation (synchronous) so caller gets immediate error
	backend, err := cs.findBackendForVM(handle)
	if err != nil {
		return fmt.Errorf("VM not found: %d", handle)
	}
	vm, err := backend.GetVM(handle)
	if err != nil {
		return fmt.Errorf("VM not found: %d", handle)
	}
	if vm.State != "running" {
		return fmt.Errorf("VM %d must be running to migrate, current state: %s", handle, vm.State)
	}
	if vm.Node == targetNode {
		return fmt.Errorf("VM %d is already on node %s", handle, targetNode)
	}

	// Set initial pending status
	status := &MigrationStatus{
		VMID:       handle,
		SourceNode: vm.Node,
		TargetNode: targetNode,
		Phase:      MigrationPending,
		Progress:   0,
		StartedAt:  time.Now(),
	}
	cs.migrationsMu.Lock()
	cs.migrations[handle] = status
	cs.migrationsMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cs.migrationsMu.Lock()
	cs.activeMigrations[handle] = cancel
	cs.migrationsMu.Unlock()

	go func() {
		defer func() {
			cs.migrationsMu.Lock()
			delete(cs.activeMigrations, handle)
			cs.migrationsMu.Unlock()
		}()
		if err := cs.doMigration(ctx, handle, targetNode); err != nil {
			slog.Warn("async migration failed", "vm_id", handle, "error", err)
		}
	}()

	return nil
}

// CancelMigration 은 진행 중인 비동기 마이그레이션을 취소한다.
//
// activeMigrations 맵에서 해당 VM의 cancel 함수를 찾아 호출한다.
// cancel() 호출 시 doMigration 내부의 context.Done() 채널이 닫히고,
// 다음 단계 전환 시점에서 MigrationFailed 상태로 전이된다.
//
// 매개변수:
//   - handle: 마이그레이션 취소할 VM ID
//
// 에러 조건: 해당 VM에 진행 중인 마이그레이션이 없는 경우
// 호출 시점: REST DELETE /api/v1/vms/{id}/migration (handleCancelMigration)
func (cs *ComputeService) CancelMigration(handle int32) error {
	cs.migrationsMu.Lock()
	cancel, ok := cs.activeMigrations[handle]
	cs.migrationsMu.Unlock()
	if !ok {
		return fmt.Errorf("no active migration for VM %d", handle)
	}
	cancel()
	return nil
}

// doMigration 은 마이그레이션의 실제 단계를 실행하는 내부 메서드이다.
//
// MigrateVM(동기)과 MigrateLive(비동기) 양쪽에서 호출된다.
// context가 취소되면 다음 단계 전환 시점에서 MigrationFailed로 전이하고 에러를 반환한다.
//
// 마이그레이션 단계:
//  1. PreCheck: VM running 확인, 대상 노드 ≠ 소스 노드 확인
//  2. Transfer: 메모리 전송 시뮬레이션 (Emulated 모드: 5ms 간격으로 10→30→60→90%)
//     - QEMU Real 모드: 여기서 QMP "migrate" 명령을 보냄 (향후 구현)
//  3. Switchover: 소스 VM 일시정지, 최종 동기화, updateVMNode()으로 노드 전환
//  4. Complete: Phase=completed, Progress=100, CompletedAt 설정
//
// 매개변수:
//   - ctx: 취소 가능한 context (CancelMigration에서 cancel() 호출 시 Done)
//   - handle: 마이그레이션 대상 VM ID
//   - targetNode: 대상 노드 이름
//
// 에러 조건: VM 미존재, running 아님, 동일 노드, context 취소
func (cs *ComputeService) doMigration(ctx context.Context, handle int32, targetNode string) error {
	backend, err := cs.findBackendForVM(handle)
	if err != nil {
		return fmt.Errorf("VM not found: %d", handle)
	}

	vm, err := backend.GetVM(handle)
	if err != nil {
		return fmt.Errorf("VM not found: %d", handle)
	}

	// Phase 1: PreCheck
	if vm.State != "running" {
		status := &MigrationStatus{
			VMID:       handle,
			SourceNode: vm.Node,
			TargetNode: targetNode,
			Phase:      MigrationFailed,
			Progress:   0,
			StartedAt:  time.Now(),
			Error:      fmt.Sprintf("VM must be running to migrate, current state: %s", vm.State),
		}
		cs.migrationsMu.Lock()
		cs.migrations[handle] = status
		cs.migrationsMu.Unlock()
		return fmt.Errorf("VM %d must be running to migrate, current state: %s", handle, vm.State)
	}
	if vm.Node == targetNode {
		return fmt.Errorf("VM %d is already on node %s", handle, targetNode)
	}

	status := &MigrationStatus{
		VMID:       handle,
		SourceNode: vm.Node,
		TargetNode: targetNode,
		Phase:      MigrationPreCheck,
		Progress:   0,
		StartedAt:  time.Now(),
	}
	cs.migrationsMu.Lock()
	cs.migrations[handle] = status
	cs.migrationsMu.Unlock()

	// Phase 2: Transfer — simulate memory transfer with progress updates
	// For QEMU Real mode: would send QMP "migrate" command here
	// For Emulated mode: simulate with short sleeps
	transferSteps := []int{10, 30, 60, 90}
	cs.migrationsMu.Lock()
	status.Phase = MigrationTransfer
	cs.migrationsMu.Unlock()

	for _, progress := range transferSteps {
		select {
		case <-ctx.Done():
			cs.migrationsMu.Lock()
			status.Phase = MigrationFailed
			status.Error = "migration cancelled"
			status.CompletedAt = time.Now()
			cs.migrationsMu.Unlock()
			return fmt.Errorf("migration cancelled for VM %d", handle)
		default:
		}
		cs.migrationsMu.Lock()
		status.Progress = progress
		cs.migrationsMu.Unlock()
		time.Sleep(5 * time.Millisecond) // Emulated transfer delay
	}

	// Phase 3: Switchover — pause source, final sync, update node, resume on target
	select {
	case <-ctx.Done():
		cs.migrationsMu.Lock()
		status.Phase = MigrationFailed
		status.Error = "migration cancelled"
		status.CompletedAt = time.Now()
		cs.migrationsMu.Unlock()
		return fmt.Errorf("migration cancelled for VM %d", handle)
	default:
	}

	cs.migrationsMu.Lock()
	status.Phase = MigrationSwitchover
	status.Progress = 95
	cs.migrationsMu.Unlock()

	// Update node field (simulated switchover) — protected by backend lock
	cs.updateVMNode(handle, targetNode)

	// Phase 4: Complete
	cs.migrationsMu.Lock()
	status.Phase = MigrationCompleted
	status.Progress = 100
	status.CompletedAt = time.Now()
	cs.migrationsMu.Unlock()

	return nil
}

// GetMigrationStatus 는 VM의 마이그레이션 진행 상태를 반환한다.
//
// 호출 시점: REST GET /api/v1/vms/{id}/migration 또는 gRPC
// 에러 조건: 해당 VM에 대한 마이그레이션 기록이 없는 경우
// 동시 호출 안전성: 안전 (내부 RLock)
func (cs *ComputeService) GetMigrationStatus(handle int32) (*MigrationStatus, error) {
	cs.migrationsMu.RLock()
	defer cs.migrationsMu.RUnlock()
	status, ok := cs.migrations[handle]
	if !ok {
		return nil, fmt.Errorf("no migration status for VM %d", handle)
	}
	// Return a copy to avoid data races
	cp := *status
	return &cp, nil
}

// listBackends 는 셀렉터에 등록된 모든 백엔드를 슬라이스로 반환한다.
// 내부 헬퍼 함수로, GetVM/ListVMs/ActionVM 등에서 전체 백엔드 순회에 사용된다.
func (cs *ComputeService) listBackends() []VMMBackend {
	cs.selector.mu.RLock()
	defer cs.selector.mu.RUnlock()
	result := make([]VMMBackend, 0, len(cs.selector.backends))
	for _, b := range cs.selector.backends {
		result = append(result, b)
	}
	return result
}

// updateVMNode 은 VM의 Node 필드를 안전하게 업데이트한다.
//
// 각 백엔드 타입(RustVMMBackend, QEMUBackend, LXCBackend)에 대해
// 타입 어서션으로 내부 vms 맵에 직접 접근하여 Node 필드를 변경한다.
// 백엔드의 내부 mu.Lock을 사용하여 동시 읽기와의 race condition을 방지한다.
//
// 매개변수:
//   - handle: 대상 VM ID
//   - node: 새 노드 이름
//
// 호출 시점: doMigration Phase 3 (Switchover)에서 노드 전환 시
// 스레드 안전성: 안전 (각 백엔드의 mu.Lock 사용)
func (cs *ComputeService) updateVMNode(handle int32, node string) {
	for _, b := range cs.listBackends() {
		switch backend := b.(type) {
		case *RustVMMBackend:
			backend.mu.Lock()
			if vm, ok := backend.vms[handle]; ok {
				vm.Node = node
			}
			backend.mu.Unlock()
			return
		case *QEMUBackend:
			backend.mu.Lock()
			if vm, ok := backend.vms[handle]; ok {
				vm.Node = node
			}
			backend.mu.Unlock()
			return
		case *LXCBackend:
			backend.mu.Lock()
			if vm, ok := backend.vms[handle]; ok {
				vm.Node = node
			}
			backend.mu.Unlock()
			return
		default:
			// For other backends, try GetVM then direct update
			if vm, err := b.GetVM(handle); err == nil {
				vm.Node = node
				return
			}
		}
	}
}

// findBackendForVM 은 주어진 handle의 VM을 소유한 백엔드를 찾아 반환한다.
// 모든 백엔드를 순회하며 GetVM() 성공 여부로 소유권을 판별한다.
// 에러 조건: 어떤 백엔드에서도 해당 VM을 찾지 못한 경우
func (cs *ComputeService) findBackendForVM(handle int32) (VMMBackend, error) {
	for _, b := range cs.listBackends() {
		if _, err := b.GetVM(handle); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("VM not found: %d", handle)
}
