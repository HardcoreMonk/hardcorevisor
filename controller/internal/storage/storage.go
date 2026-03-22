// Package storage — 스토리지 관리 서비스
//
// 아키텍처 위치: Go Controller → Storage Service → StorageDriver
//
// 플러그어블 드라이버 패턴을 사용하여 다양한 스토리지 백엔드를 지원한다:
//   - MemoryDriver: 인메모리 (개발/테스트용)
//   - ZFSDriver: ZFS CLI (zpool/zfs 명령어)
//   - CephDriver: Ceph RBD CLI (rbd 명령어)
//
// 핵심 개념:
//   - Pool: 스토리지 풀 (ZFS pool, Ceph pool)
//   - Volume: 풀 내의 디스크 볼륨
//   - Snapshot: 볼륨의 시점 스냅샷
//
// 스레드 안전성: sync.RWMutex로 보호됨
//
// 환경변수:
//   - HCV_STORAGE_DRIVER: 드라이버 선택 ("memory", "zfs", "ceph"). 기본값: "memory"
//   - HCV_CEPH_POOL: Ceph 풀 이름. 기본값: "rbd"
package storage

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ── 타입 정의 ────────────────────────────────────────

// Pool 은 스토리지 풀을 나타낸다 (ZFS, Ceph, LVM 등).
// 풀은 여러 볼륨을 포함하며, 전체 용량과 사용량을 추적한다.
type Pool struct {
	Name       string `json:"name"`
	PoolType   string `json:"pool_type"` // zfs, ceph, lvm, dir
	TotalBytes uint64 `json:"total_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
	Health     string `json:"health"` // healthy, degraded, faulted
}

// Volume 은 풀 내의 스토리지 볼륨을 나타낸다.
// VM 디스크로 사용되며, qcow2/raw/zvol 포맷을 지원한다.
type Volume struct {
	ID        string `json:"id"`
	Pool      string `json:"pool"`
	Name      string `json:"name"`
	SizeBytes uint64 `json:"size_bytes"`
	Format    string `json:"format"` // qcow2, raw, zvol
	Path      string `json:"path"`
	CreatedAt int64  `json:"created_at"`
}

// Snapshot 은 볼륨의 시점 스냅샷을 나타낸다.
// 특정 시점의 볼륨 상태를 캡처하여 복원에 사용할 수 있다.
type Snapshot struct {
	ID        string `json:"id"`
	VolumeID  string `json:"volume_id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

// ── 서비스 ──────────────────────────────────────────

// Service 는 스토리지 풀, 볼륨, 스냅샷을 관리하는 서비스이다.
// 실제 백엔드 작업은 StorageDriver 인터페이스에 위임한다.
// 모든 메서드는 sync.RWMutex로 보호되므로 동시 호출에 안전하다.
type Service struct {
	driver     StorageDriver
	mu         sync.RWMutex
	pools      map[string]*Pool
	volumes    map[string]*Volume
	snapshots  map[string]*Snapshot
	nextVolID  atomic.Int32
	nextSnapID atomic.Int32
}

// NewService 는 기본 인메모리 드라이버로 스토리지 서비스를 생성한다.
//
// 호출 시점: 개발/테스트 환경에서 사용. 프로덕션에서는 NewServiceWithDriver 사용.
// 동시 호출 안전성: 안전 (내부에서 NewServiceWithDriver 호출)
func NewService() *Service {
	return NewServiceWithDriver(NewMemoryDriver())
}

// NewServiceWithDriver 는 지정된 드라이버로 스토리지 서비스를 생성한다.
//
// 호출 시점: Controller 초기화 시 설정에 따라 적절한 드라이버를 주입한다.
// MemoryDriver인 경우, 하위 호환성을 위해 기본 풀 2개를 미리 생성한다:
//   - "local-zfs": ZFS 풀 (3.2TiB 전체 / 2.1TiB 사용)
//   - "ceph-pool": Ceph 풀 (10TiB 전체 / 4TiB 사용)
//
// 동시 호출 안전성: 초기화 시 1회만 호출하므로 동시성 이슈 없음
func NewServiceWithDriver(driver StorageDriver) *Service {
	s := &Service{
		driver:    driver,
		pools:     make(map[string]*Pool),
		volumes:   make(map[string]*Volume),
		snapshots: make(map[string]*Snapshot),
	}
	s.nextVolID.Store(1)
	s.nextSnapID.Store(1)

	// For the memory driver, pre-populate pools for backward compatibility
	if _, ok := driver.(*MemoryDriver); ok {
		s.pools["local-zfs"] = &Pool{
			Name: "local-zfs", PoolType: "zfs",
			TotalBytes: 3435973836800, UsedBytes: 2302160486400,
			Health: "healthy",
		}
		s.pools["ceph-pool"] = &Pool{
			Name: "ceph-pool", PoolType: "ceph",
			TotalBytes: 10995116277760, UsedBytes: 4398046511104,
			Health: "healthy",
		}
	}

	return s
}

// DriverName 은 현재 사용 중인 스토리지 드라이버의 이름을 반환한다.
//
// 반환값 예시: "memory", "zfs", "ceph"
// 동시 호출 안전성: 안전 (드라이버는 초기화 후 변경되지 않음)
func (s *Service) DriverName() string {
	return s.driver.Name()
}

// ListPools 는 모든 스토리지 풀 목록을 반환한다.
//
// 하는 일: MemoryDriver인 경우 로컬 상태에서 조회, 그 외에는 드라이버에 위임.
// 호출 시점: REST GET /api/v1/storage/pools, gRPC ListPools
// 동시 호출 안전성: 안전 (RLock으로 보호)
// 에러 시 nil 반환 (드라이버 에러를 상위로 전파하지 않음)
func (s *Service) ListPools() []*Pool {
	if _, ok := s.driver.(*MemoryDriver); ok {
		s.mu.RLock()
		defer s.mu.RUnlock()
		result := make([]*Pool, 0, len(s.pools))
		for _, p := range s.pools {
			result = append(result, p)
		}
		return result
	}
	pools, err := s.driver.ListPools()
	if err != nil {
		return nil
	}
	return pools
}

// GetPool 은 이름으로 스토리지 풀을 조회한다.
//
// 하는 일: 로컬 맵에서 풀 이름으로 검색. 없으면 에러 반환.
// 동시 호출 안전성: 안전 (RLock으로 보호)
func (s *Service) GetPool(name string) (*Pool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.pools[name]
	if !ok {
		return nil, fmt.Errorf("pool not found: %s", name)
	}
	return p, nil
}

// CreateVolume 은 지정된 풀에 새 볼륨을 생성한다.
//
// 하는 일: 풀 존재 확인 → 볼륨 ID 자동 생성 → 볼륨 맵에 추가 → 풀 사용량 갱신
// 호출 시점: REST POST /api/v1/storage/volumes, gRPC CreateVolume
// 동시 호출 안전성: 안전 (Lock으로 보호, 볼륨 ID는 atomic 카운터)
// 부작용: ZFS/Ceph 드라이버인 경우 실제 디스크에 볼륨 생성 (파일 시스템 변경)
func (s *Service) CreateVolume(pool, name, format string, sizeBytes uint64) (*Volume, error) {
	if _, ok := s.driver.(*MemoryDriver); !ok {
		return s.driver.CreateVolume(pool, name, format, sizeBytes)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pools[pool]; !ok {
		return nil, fmt.Errorf("pool not found: %s", pool)
	}
	id := fmt.Sprintf("vol-%d", s.nextVolID.Add(1)-1)
	vol := &Volume{
		ID: id, Pool: pool, Name: name,
		SizeBytes: sizeBytes, Format: format,
		Path:      fmt.Sprintf("/dev/%s/%s", pool, name),
		CreatedAt: time.Now().Unix(),
	}
	s.volumes[id] = vol
	s.pools[pool].UsedBytes += sizeBytes
	return vol, nil
}

// ListVolumes 는 모든 볼륨을 반환하며, pool 파라미터로 풀별 필터링이 가능하다.
//
// 하는 일: pool이 빈 문자열이면 전체 반환, 아니면 해당 풀의 볼륨만 반환.
// 호출 시점: REST GET /api/v1/storage/volumes?pool=
// 동시 호출 안전성: 안전 (RLock으로 보호)
func (s *Service) ListVolumes(pool string) []*Volume {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Volume, 0)
	for _, v := range s.volumes {
		if pool == "" || v.Pool == pool {
			result = append(result, v)
		}
	}
	return result
}

// DeleteVolume 은 ID로 볼륨을 삭제한다.
//
// 하는 일: 볼륨 존재 확인 → 풀 사용량 차감 → 볼륨 맵에서 제거
// 호출 시점: REST DELETE /api/v1/storage/volumes/{id}
// 동시 호출 안전성: 안전 (Lock으로 보호)
// 부작용: ZFS/Ceph 드라이버인 경우 실제 디스크에서 볼륨 삭제 (파일 시스템 변경)
func (s *Service) DeleteVolume(id string) error {
	if _, ok := s.driver.(*MemoryDriver); !ok {
		return s.driver.DeleteVolume(id)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	vol, ok := s.volumes[id]
	if !ok {
		return fmt.Errorf("volume not found: %s", id)
	}
	if p, ok := s.pools[vol.Pool]; ok {
		if p.UsedBytes >= vol.SizeBytes {
			p.UsedBytes -= vol.SizeBytes
		}
	}
	delete(s.volumes, id)
	return nil
}

// CreateSnapshot 은 볼륨의 시점 스냅샷을 생성한다.
//
// 하는 일: 볼륨 존재 확인 → 스냅샷 ID 자동 생성 → 스냅샷 맵에 추가
// 호출 시점: REST/gRPC에서 스냅샷 생성 요청 시, 또는 백업 서비스에서 호출
// 동시 호출 안전성: 안전 (Lock으로 보호)
// 부작용: ZFS 드라이버인 경우 "zfs snapshot" 명령 실행 (파일 시스템 변경)
func (s *Service) CreateSnapshot(volumeID, name string) (*Snapshot, error) {
	if _, ok := s.driver.(*MemoryDriver); !ok {
		return s.driver.CreateSnapshot(volumeID, name)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.volumes[volumeID]; !ok {
		return nil, fmt.Errorf("volume not found: %s", volumeID)
	}
	id := fmt.Sprintf("snap-%d", s.nextSnapID.Add(1)-1)
	snap := &Snapshot{
		ID: id, VolumeID: volumeID, Name: name,
		CreatedAt: time.Now().Unix(),
	}
	s.snapshots[id] = snap
	return snap, nil
}

// ListSnapshots 는 스냅샷 목록을 반환하며, volumeID로 필터링이 가능하다.
//
// 하는 일: volumeID가 빈 문자열이면 전체 반환, 아니면 해당 볼륨의 스냅샷만 반환.
// 호출 시점: REST/gRPC에서 스냅샷 목록 조회 시
// 동시 호출 안전성: 안전 (RLock으로 보호)
func (s *Service) ListSnapshots(volumeID string) []*Snapshot {
	if _, ok := s.driver.(*MemoryDriver); !ok {
		snaps, err := s.driver.ListSnapshots(volumeID)
		if err != nil {
			return nil
		}
		return snaps
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Snapshot, 0)
	for _, snap := range s.snapshots {
		if volumeID == "" || snap.VolumeID == volumeID {
			result = append(result, snap)
		}
	}
	return result
}
