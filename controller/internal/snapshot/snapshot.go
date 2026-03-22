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
// SizeBytes는 현재 1GB 플레이스홀더이다.
type VMSnapshot struct {
	ID        string `json:"id"`
	VMID      int32  `json:"vm_id"`
	VMName    string `json:"vm_name"`
	State     string `json:"state"` // created, active, restoring
	CreatedAt int64  `json:"created_at"`
	SizeBytes uint64 `json:"size_bytes"`
}

// Service 는 VM 스냅샷을 관리하는 서비스이다.
// 동시 호출 안전성: sync.RWMutex로 보호됨
type Service struct {
	mu        sync.RWMutex
	snapshots map[string]*VMSnapshot
	nextID    atomic.Int32
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

// Restore 는 스냅샷의 상태를 "restoring"으로 변경하여 복원을 시뮬레이션한다.
//
// 호출 시점: REST POST /api/v1/snapshots/{id}/restore
// 현재는 상태만 변경하며, 실제 VM 복원 로직은 미구현이다.
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
