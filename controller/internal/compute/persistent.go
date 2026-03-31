// Package compute — PersistentComputeService: etcd 기반 VM 상태 영속화 래퍼.
//
// # 패키지 목적
//
// ComputeService를 Decorator 패턴으로 래핑하여, 모든 VM 변경 작업
// (생성, 삭제, 생명주기 액션, 마이그레이션)을 etcd Store에 자동으로 저장한다.
// Controller 재시작 시 LoadFromStore()로 저장된 VM을 복원한다.
//
// # 저장소 키 형식
//
//	"vms/{handle}" → VMInfo JSON
//
// # 에러 처리
//
// Store 저장 실패는 로그로 기록하지만 VM 작업 자체는 성공으로 처리한다.
// (저장소 장애가 VM 운영을 중단시키면 안 되므로)
package compute

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/store"
)

// PersistentComputeService — ComputeService를 래핑하여 VM 상태를 Store에 영속화한다.
// ComputeProvider 인터페이스를 구현하므로 API 레이어에서 투명하게 교체 가능하다.
type PersistentComputeService struct {
	inner *ComputeService
	store store.Store
}

// NewPersistentComputeService — ComputeService에 영속화 래퍼를 생성한다.
//
// # 매개변수
//   - inner: 래핑할 ComputeService
//   - s: VM 상태를 저장할 Store (EtcdStore 또는 MemoryStore)
func NewPersistentComputeService(inner *ComputeService, s store.Store) *PersistentComputeService {
	return &PersistentComputeService{
		inner: inner,
		store: s,
	}
}

func vmStoreKey(id int32) string {
	return fmt.Sprintf("vms/%d", id)
}

// CreateVM creates a VM via the inner service, then persists it to the store.
func (p *PersistentComputeService) CreateVM(name string, vcpus uint32, memoryMB uint64, backendHint string) (*VMInfo, error) {
	vm, err := p.inner.CreateVM(name, vcpus, memoryMB, backendHint)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if putErr := p.store.Put(ctx, vmStoreKey(vm.ID), vm); putErr != nil {
		slog.Error("persistent: failed to store VM", "vm_id", vm.ID, "error", putErr)
	}
	return vm, nil
}

// DestroyVM removes a VM via the inner service, then deletes it from the store.
func (p *PersistentComputeService) DestroyVM(handle int32) error {
	if err := p.inner.DestroyVM(handle); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if delErr := p.store.Delete(ctx, vmStoreKey(handle)); delErr != nil {
		slog.Error("persistent: failed to delete VM from store", "vm_id", handle, "error", delErr)
	}
	return nil
}

// ActionVM performs a lifecycle action, then persists the updated VM state.
func (p *PersistentComputeService) ActionVM(handle int32, action string) (*VMInfo, error) {
	vm, err := p.inner.ActionVM(handle, action)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if putErr := p.store.Put(ctx, vmStoreKey(vm.ID), vm); putErr != nil {
		slog.Error("persistent: failed to update VM in store", "vm_id", vm.ID, "error", putErr)
	}
	return vm, nil
}

// GetVM delegates to the inner service.
func (p *PersistentComputeService) GetVM(handle int32) (*VMInfo, error) {
	return p.inner.GetVM(handle)
}

// ListVMs delegates to the inner service.
func (p *PersistentComputeService) ListVMs() []*VMInfo {
	return p.inner.ListVMs()
}

// ListBackends delegates to the inner service.
func (p *PersistentComputeService) ListBackends() []BackendInfo {
	return p.inner.ListBackends()
}

// GetMigrationStatus delegates to the inner service.
func (p *PersistentComputeService) GetMigrationStatus(handle int32) (*MigrationStatus, error) {
	return p.inner.GetMigrationStatus(handle)
}

// MigrateVM delegates to the inner service, then persists the updated VM state.
func (p *PersistentComputeService) MigrateVM(handle int32, targetNode string) error {
	if err := p.inner.MigrateVM(handle, targetNode); err != nil {
		return err
	}
	vm, err := p.inner.GetVM(handle)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if putErr := p.store.Put(ctx, vmStoreKey(vm.ID), vm); putErr != nil {
		slog.Error("persistent: failed to update VM after migration", "vm_id", vm.ID, "error", putErr)
	}
	return nil
}

// MigrateLive 은 비동기 라이브 마이그레이션을 inner 서비스에 위임한다.
//
// MigrateVM(동기)과 달리 즉시 반환되므로, 완료 시점을 알 수 없어
// 여기서는 Store 영속화를 하지 않는다. 마이그레이션 완료 후
// 노드 변경은 inner의 updateVMNode()에서 인메모리로 반영된다.
//
// 호출 시점: REST POST /api/v1/vms/{id}/migrate (TaskService 사용 시 비동기 모드)
func (p *PersistentComputeService) MigrateLive(handle int32, targetNode string) error {
	return p.inner.MigrateLive(handle, targetNode)
}

// CancelMigration 은 진행 중인 마이그레이션 취소를 inner 서비스에 위임한다.
//
// inner의 CancelMigration()이 context cancel을 호출하여
// doMigration goroutine이 다음 단계에서 MigrationFailed로 전이된다.
//
// 호출 시점: REST DELETE /api/v1/vms/{id}/migration (handleCancelMigration)
func (p *PersistentComputeService) CancelMigration(handle int32) error {
	return p.inner.CancelMigration(handle)
}

// LoadFromStore — 저장소에서 영속화된 모든 VM을 읽어 인메모리로 복원한다.
// Controller 시작 시 1회 호출된다. 개별 VM 복원 실패는 로그로 기록하고 건너뛴다.
//
// # 반환값
//   - nil: 성공 (일부 VM 복원 실패 포함)
//   - error: 저장소 목록 조회 자체가 실패한 경우
func (p *PersistentComputeService) LoadFromStore() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	kvs, err := p.store.List(ctx, "vms/")
	if err != nil {
		return fmt.Errorf("persistent: list stored VMs: %w", err)
	}

	if len(kvs) == 0 {
		slog.Info("persistent: no VMs found in store")
		return nil
	}

	loaded := 0
	for _, kv := range kvs {
		var vm VMInfo
		if err := json.Unmarshal(kv.Value, &vm); err != nil {
			slog.Warn("persistent: failed to unmarshal VM", "key", kv.Key, "error", err)
			continue
		}
		// Recreate the VM through the backend
		_, createErr := p.inner.CreateVM(vm.Name, vm.VCPUs, vm.MemoryMB, vm.Backend)
		if createErr != nil {
			slog.Error("persistent: failed to recreate VM", "vm_name", vm.Name, "error", createErr)
			continue
		}
		loaded++
	}

	slog.Info("persistent: loaded VMs from store", "loaded", loaded, "total", len(kvs))
	return nil
}
