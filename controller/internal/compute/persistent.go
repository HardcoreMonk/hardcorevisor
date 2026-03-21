// Package compute — PersistentComputeService wraps ComputeService with etcd-backed persistence.
//
// On every VM mutation (create, destroy, action), the updated state is
// written to the store so VMs survive controller restarts.
package compute

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/store"
)

// PersistentComputeService wraps a ComputeService and persists VM state to a Store.
type PersistentComputeService struct {
	inner *ComputeService
	store store.Store
}

// NewPersistentComputeService creates a persistent wrapper around the given ComputeService.
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
		log.Printf("persistent: failed to store VM %d: %v", vm.ID, putErr)
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
		log.Printf("persistent: failed to delete VM %d from store: %v", handle, delErr)
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
		log.Printf("persistent: failed to update VM %d in store: %v", vm.ID, putErr)
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

// LoadFromStore reads all persisted VMs from the store and recreates them
// in-memory via the inner ComputeService. Called once at startup.
func (p *PersistentComputeService) LoadFromStore() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	kvs, err := p.store.List(ctx, "vms/")
	if err != nil {
		return fmt.Errorf("persistent: list stored VMs: %w", err)
	}

	if len(kvs) == 0 {
		log.Println("persistent: no VMs found in store")
		return nil
	}

	loaded := 0
	for _, kv := range kvs {
		var vm VMInfo
		if err := json.Unmarshal(kv.Value, &vm); err != nil {
			log.Printf("persistent: failed to unmarshal VM from key %s: %v", kv.Key, err)
			continue
		}
		// Recreate the VM through the backend
		_, createErr := p.inner.CreateVM(vm.Name, vm.VCPUs, vm.MemoryMB, vm.Backend)
		if createErr != nil {
			log.Printf("persistent: failed to recreate VM %q: %v", vm.Name, createErr)
			continue
		}
		loaded++
	}

	log.Printf("persistent: loaded %d/%d VMs from store", loaded, len(kvs))
	return nil
}
