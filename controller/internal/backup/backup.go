// Package backup — VM 백업 관리 서비스
//
// 아키텍처 위치: Go Controller → Backup Service → Storage Service
//
// 스토리지 스냅샷을 기반으로 VM 백업을 생성하고 관리한다.
// 백업 메타데이터는 인메모리에 저장하며, 실제 데이터는 Storage Service를 통해 관리한다.
//
// 백업 생성 프로세스:
//  1. 지정된 풀에 백업 볼륨 생성 (1GB 플레이스홀더)
//  2. 백업 볼륨의 스냅샷 생성
//  3. 백업 메타데이터 기록 (VMID, 스냅샷 ID, 풀, 상태)
//
// 스레드 안전성: sync.RWMutex로 보호됨
package backup

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
)

// ── 타입 정의 ────────────────────────────────────────

// BackupInfo 는 VM 백업 레코드를 나타낸다.
// Status 상태: "pending" | "running" | "completed" | "failed"
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

// ── 서비스 ──────────────────────────────────────────

// Service 는 VM 백업을 관리하는 서비스이다.
// Storage Service에 의존하여 볼륨 생성과 스냅샷을 수행한다.
// 동시 호출 안전성: sync.RWMutex로 보호됨
type Service struct {
	mu         sync.RWMutex
	backups    map[string]*BackupInfo
	nextID     atomic.Int32
	storageSvc *storage.Service
}

// NewService 는 Storage Service를 기반으로 백업 서비스를 생성한다.
//
// 호출 시점: Controller 초기화 시
func NewService(storageSvc *storage.Service) *Service {
	s := &Service{
		backups:    make(map[string]*BackupInfo),
		storageSvc: storageSvc,
	}
	s.nextID.Store(1)
	return s
}

// CreateBackup 은 Storage 스냅샷을 사용하여 VM 백업을 생성한다.
//
// 하는 일: 백업 ID 생성 → 볼륨 생성 → 스냅샷 생성 → 메타데이터 저장
// 호출 시점: REST POST /api/v1/backups, hcvctl backup create
// 동시 호출 안전성: 안전 (Lock 사용, ID는 atomic 카운터)
// 에러 조건: 풀 미존재, 볼륨 생성 실패, 스냅샷 생성 실패
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

// ListBackups 는 모든 백업 목록을 반환한다.
//
// 호출 시점: REST GET /api/v1/backups
// 동시 호출 안전성: 안전 (RLock 사용)
func (s *Service) ListBackups() []*BackupInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*BackupInfo, 0, len(s.backups))
	for _, b := range s.backups {
		result = append(result, b)
	}
	return result
}

// GetBackup 은 ID로 백업을 조회한다. 미존재 시 에러 반환.
//
// 호출 시점: REST GET /api/v1/backups/{id}
func (s *Service) GetBackup(id string) (*BackupInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.backups[id]
	if !ok {
		return nil, fmt.Errorf("backup not found: %s", id)
	}
	return b, nil
}

// DeleteBackup 은 ID로 백업을 삭제한다. 미존재 시 에러 반환.
//
// 호출 시점: REST DELETE /api/v1/backups/{id}
// 주의: 메타데이터만 삭제하며, 스토리지의 스냅샷/볼륨은 별도 정리 필요
func (s *Service) DeleteBackup(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.backups[id]; !ok {
		return fmt.Errorf("backup not found: %s", id)
	}
	delete(s.backups, id)
	return nil
}
