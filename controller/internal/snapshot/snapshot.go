// Package snapshot — VM snapshot/restore management
//
// In-memory implementation for dev/test. Tracks snapshot metadata
// for point-in-time VM state capture and restore.
package snapshot

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// VMSnapshot represents a point-in-time VM snapshot.
type VMSnapshot struct {
	ID        string `json:"id"`
	VMID      int32  `json:"vm_id"`
	VMName    string `json:"vm_name"`
	State     string `json:"state"` // created, active, restoring
	CreatedAt int64  `json:"created_at"`
	SizeBytes uint64 `json:"size_bytes"`
}

// Service manages VM snapshots.
type Service struct {
	mu        sync.RWMutex
	snapshots map[string]*VMSnapshot
	nextID    atomic.Int32
}

// NewService creates a new snapshot service.
func NewService() *Service {
	s := &Service{
		snapshots: make(map[string]*VMSnapshot),
	}
	s.nextID.Store(1)
	return s
}

// Create creates a new snapshot for the given VM.
func (s *Service) Create(vmID int32, vmName string) (*VMSnapshot, error) {
	if vmName == "" {
		return nil, fmt.Errorf("vm_name is required")
	}

	id := fmt.Sprintf("snap-%d", s.nextID.Add(1)-1)
	snap := &VMSnapshot{
		ID:        id,
		VMID:      vmID,
		VMName:    vmName,
		State:     "created",
		CreatedAt: time.Now().Unix(),
		SizeBytes: 1073741824, // 1GB placeholder
	}

	s.mu.Lock()
	s.snapshots[id] = snap
	s.mu.Unlock()

	return snap, nil
}

// List returns all snapshots for a given VM ID. If vmID is 0, returns all.
func (s *Service) List(vmID int32) []*VMSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*VMSnapshot, 0, len(s.snapshots))
	for _, snap := range s.snapshots {
		if vmID == 0 || snap.VMID == vmID {
			result = append(result, snap)
		}
	}
	return result
}

// Get returns a snapshot by ID.
func (s *Service) Get(id string) (*VMSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.snapshots[id]
	if !ok {
		return nil, fmt.Errorf("snapshot not found: %s", id)
	}
	return snap, nil
}

// Delete removes a snapshot by ID.
func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.snapshots[id]; !ok {
		return fmt.Errorf("snapshot not found: %s", id)
	}
	delete(s.snapshots, id)
	return nil
}

// Restore marks a snapshot as "restoring" to simulate a restore operation.
func (s *Service) Restore(id string) (*VMSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.snapshots[id]
	if !ok {
		return nil, fmt.Errorf("snapshot not found: %s", id)
	}
	snap.State = "restoring"
	return snap, nil
}
