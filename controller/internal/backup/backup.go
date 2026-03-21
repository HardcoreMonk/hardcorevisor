// Package backup — Backup/Snapshot management for VMs
//
// In-memory implementation for dev/test. Creates backups by
// taking storage snapshots and tracking backup metadata.
package backup

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
)

// ── Types ────────────────────────────────────────────

// BackupInfo represents a VM backup record.
type BackupInfo struct {
	ID          string `json:"id"`
	VMID        int32  `json:"vm_id"`
	VMName      string `json:"vm_name"`
	SnapshotID  string `json:"snapshot_id"`
	Status      string `json:"status"` // pending, running, completed, failed
	CreatedAt   int64  `json:"created_at"`
	SizeBytes   uint64 `json:"size_bytes"`
	StoragePool string `json:"storage_pool"`
}

// ── Service ──────────────────────────────────────────

// Service manages VM backups.
type Service struct {
	mu         sync.RWMutex
	backups    map[string]*BackupInfo
	nextID     atomic.Int32
	storageSvc *storage.Service
}

// NewService creates a backup service backed by the given storage service.
func NewService(storageSvc *storage.Service) *Service {
	s := &Service{
		backups:    make(map[string]*BackupInfo),
		storageSvc: storageSvc,
	}
	s.nextID.Store(1)
	return s
}

// CreateBackup creates a backup for the given VM by taking a storage snapshot.
func (s *Service) CreateBackup(vmID int32, vmName, pool string) (*BackupInfo, error) {
	id := fmt.Sprintf("backup-%d", s.nextID.Add(1)-1)

	// Create a volume for the backup in the specified pool
	volName := fmt.Sprintf("backup-%s-%d", vmName, time.Now().Unix())
	vol, err := s.storageSvc.CreateVolume(pool, volName, "raw", 1073741824) // 1GB placeholder
	if err != nil {
		return nil, fmt.Errorf("failed to create backup volume: %w", err)
	}

	// Create a snapshot of the backup volume
	snap, err := s.storageSvc.CreateSnapshot(vol.ID, fmt.Sprintf("snap-%s", id))
	if err != nil {
		return nil, fmt.Errorf("failed to create backup snapshot: %w", err)
	}

	backup := &BackupInfo{
		ID:          id,
		VMID:        vmID,
		VMName:      vmName,
		SnapshotID:  snap.ID,
		Status:      "completed",
		CreatedAt:   time.Now().Unix(),
		SizeBytes:   vol.SizeBytes,
		StoragePool: pool,
	}

	s.mu.Lock()
	s.backups[id] = backup
	s.mu.Unlock()

	return backup, nil
}

// ListBackups returns all backups.
func (s *Service) ListBackups() []*BackupInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*BackupInfo, 0, len(s.backups))
	for _, b := range s.backups {
		result = append(result, b)
	}
	return result
}

// GetBackup returns a backup by ID.
func (s *Service) GetBackup(id string) (*BackupInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.backups[id]
	if !ok {
		return nil, fmt.Errorf("backup not found: %s", id)
	}
	return b, nil
}

// DeleteBackup removes a backup by ID.
func (s *Service) DeleteBackup(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.backups[id]; !ok {
		return fmt.Errorf("backup not found: %s", id)
	}
	delete(s.backups, id)
	return nil
}
