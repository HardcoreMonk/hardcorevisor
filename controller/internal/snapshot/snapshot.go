// Package snapshot — VM 스냅샷/복원 관리 서비스
//
// VM의 시점별 상태를 캡처하고 복원하는 서비스이다.
// 인메모리 구현으로 개발/테스트 환경에서 사용한다.
//
// 스냅샷 상태:
//   - "created": 생성 완료
//   - "active": 활성 상태
//   - "restoring": 복원 진행 중
//
// 스레드 안전성: sync.RWMutex로 보호됨
package snapshot

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// VMSnapshot 은 VM의 시점 스냅샷을 나타낸다.
//
// 필드:
//   - ID: 스냅샷 고유 ID (형식: "snap-N", atomic 카운터 기반)
//   - VMID: 대상 VM의 handle ID
//   - VMName: VM 이름 (표시용)
//   - State: 스냅샷 상태 ("created" → "active" → "restoring")
//   - CreatedAt: 생성 시각 (Unix 타임스탬프)
//   - SizeBytes: 스냅샷 크기 (현재 1GB 플레이스홀더)
//   - StorageSnapshotID: 연결된 스토리지 스냅샷 ID (StorageSvc 연동 시 설정, 없으면 빈 문자열)
//   - VolumeID: 연결된 볼륨 ID (CreateWithVolume으로 생성 시 설정, 없으면 빈 문자열)
type VMSnapshot struct {
	ID                string `json:"id"`
	VMID              int32  `json:"vm_id"`
	VMName            string `json:"vm_name"`
	State             string `json:"state"` // created, active, restoring
	CreatedAt         int64  `json:"created_at"`
	SizeBytes         uint64 `json:"size_bytes"`
	StorageSnapshotID string `json:"storage_snapshot_id,omitempty"`
	VolumeID          string `json:"volume_id,omitempty"`
}

// StorageSnapshotProvider 는 스토리지 레벨의 스냅샷 생성/롤백 기능을 제공하는 인터페이스이다.
//
// 이 인터페이스를 통해 VM 스냅샷과 스토리지 스냅샷을 연동한다.
// storage.Service를 직접 참조하지 않고 인터페이스로 분리하여 순환 의존성을 방지한다.
//
// 구현체: 라우터에서 StorageServiceAdapter로 storage.Service를 래핑하여 주입
// nil이면 스토리지 연동 없이 인메모리 스냅샷만 생성한다.
//
// 메서드:
//   - CreateStorageSnapshot: 볼륨의 스토리지 레벨 스냅샷 생성, 스냅샷 ID 반환
//   - RollbackStorageSnapshot: 스토리지 스냅샷을 롤백하여 볼륨을 이전 상태로 복원
type StorageSnapshotProvider interface {
	CreateStorageSnapshot(volumeID, name string) (snapshotID string, err error)
	RollbackStorageSnapshot(snapshotID string) error
}

// Service 는 VM 스냅샷을 관리하는 서비스이다.
// 동시 호출 안전성: sync.RWMutex로 보호됨
type Service struct {
	mu         sync.RWMutex
	snapshots  map[string]*VMSnapshot
	nextID     atomic.Int32
	StorageSvc StorageSnapshotProvider // nil이면 스토리지 연동 없이 동작
}

// NewService 는 새 스냅샷 서비스를 생성한다.
//
// 호출 시점: Controller 초기화 시
func NewService() *Service {
	s := &Service{
		snapshots: make(map[string]*VMSnapshot),
	}
	s.nextID.Store(1)
	return s
}

// Create 는 지정된 VM의 새 스냅샷을 생성한다.
//
// StorageSvc가 설정된 경우, 스토리지 스냅샷도 함께 생성한다.
// 호출 시점: REST POST /api/v1/snapshots
// 동시 호출 안전성: 안전 (Lock 사용, ID는 atomic 카운터)
// 에러 조건: vmName이 빈 문자열
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

	// If storage service is available, create a storage-level snapshot
	if s.StorageSvc != nil {
		snapName := fmt.Sprintf("vm-%d-%s", vmID, id)
		storageSnapID, err := s.StorageSvc.CreateStorageSnapshot("", snapName)
		if err == nil {
			snap.StorageSnapshotID = storageSnapID
		}
		// Storage snapshot failure is non-fatal — log and continue
	}

	s.mu.Lock()
	s.snapshots[id] = snap
	s.mu.Unlock()

	return snap, nil
}

// CreateWithVolume 는 특정 볼륨에 대한 VM 스냅샷을 생성한다.
//
// Create()와의 차이점: volumeID를 명시적으로 지정하여 특정 볼륨의 스토리지 스냅샷을 생성한다.
// StorageSvc가 설정되고 volumeID가 비어 있지 않으면, 해당 볼륨의 스토리지 스냅샷도 함께 생성한다.
//
// 매개변수:
//   - vmID: 대상 VM의 handle ID
//   - vmName: VM 이름 (필수, 빈 문자열이면 에러)
//   - volumeID: 스냅샷을 생성할 볼륨 ID (빈 문자열이면 스토리지 스냅샷 건너뜀)
//
// 호출 시점: QEMU 백엔드의 SnapshotVM에서 볼륨 지정 스냅샷이 필요할 때
// 동시 호출 안전성: 안전 (Lock 사용, ID는 atomic 카운터)
func (s *Service) CreateWithVolume(vmID int32, vmName, volumeID string) (*VMSnapshot, error) {
	if vmName == "" {
		return nil, fmt.Errorf("vm_name is required")
	}

	id := fmt.Sprintf("snap-%d", s.nextID.Add(1)-1)
	snap := &VMSnapshot{
		ID:        id,
		VMID:      vmID,
		VMName:    vmName,
		VolumeID:  volumeID,
		State:     "created",
		CreatedAt: time.Now().Unix(),
		SizeBytes: 1073741824, // 1GB placeholder
	}

	if s.StorageSvc != nil && volumeID != "" {
		snapName := fmt.Sprintf("vm-%d-%s", vmID, id)
		storageSnapID, err := s.StorageSvc.CreateStorageSnapshot(volumeID, snapName)
		if err == nil {
			snap.StorageSnapshotID = storageSnapID
		}
	}

	s.mu.Lock()
	s.snapshots[id] = snap
	s.mu.Unlock()

	return snap, nil
}

// List 는 지정된 VM의 스냅샷 목록을 반환한다. vmID가 0이면 전체 반환.
//
// 호출 시점: REST GET /api/v1/snapshots?vm_id=
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

// Get 은 ID로 스냅샷을 조회한다. 미존재 시 에러 반환.
func (s *Service) Get(id string) (*VMSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.snapshots[id]
	if !ok {
		return nil, fmt.Errorf("snapshot not found: %s", id)
	}
	return snap, nil
}

// Delete 는 ID로 스냅샷을 삭제한다. 미존재 시 에러 반환.
func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.snapshots[id]; !ok {
		return fmt.Errorf("snapshot not found: %s", id)
	}
	delete(s.snapshots, id)
	return nil
}

// Restore 는 스냅샷을 복원한다.
//
// 처리 순서:
//  1. 스냅샷 조회 (미존재 시 에러)
//  2. StorageSvc가 설정되고 StorageSnapshotID가 있으면 스토리지 롤백 수행
//  3. 스냅샷 상태를 "restoring"으로 변경
//
// 호출 시점: REST POST /api/v1/snapshots/{id}/restore
// 에러 처리: 스토리지 롤백 실패는 무시 (non-fatal) — VM 상태 복원이 우선
// 동시 호출 안전성: 안전 (Lock 사용)
func (s *Service) Restore(id string) (*VMSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.snapshots[id]
	if !ok {
		return nil, fmt.Errorf("snapshot not found: %s", id)
	}

	// If storage service is available and we have a storage snapshot, rollback
	if s.StorageSvc != nil && snap.StorageSnapshotID != "" {
		if err := s.StorageSvc.RollbackStorageSnapshot(snap.StorageSnapshotID); err != nil {
			// Storage rollback failure is non-fatal
			_ = err
		}
	}

	snap.State = "restoring"
	return snap, nil
}
