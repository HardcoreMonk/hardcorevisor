// 인메모리 스토리지 드라이버 — 개발/테스트 전용
//
// 실제 파일 시스템이나 외부 명령을 사용하지 않으며,
// 모든 상태를 Go 맵에 저장한다. 프로세스 재시작 시 데이터가 소실된다.
// sync.RWMutex로 동시 접근을 보호한다.
package storage

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// MemoryDriver 는 인메모리 스토리지 드라이버로, 개발/테스트 환경에서 사용한다.
// StorageDriver 인터페이스를 구현하며, 외부 의존성이 없다.
// 기본 풀 2개(local-zfs, ceph-pool)가 미리 생성된다.
type MemoryDriver struct {
	mu         sync.RWMutex
	pools      map[string]*Pool
	volumes    map[string]*Volume
	snapshots  map[string]*Snapshot
	nextVolID  atomic.Int32
	nextSnapID atomic.Int32
}

// NewMemoryDriver 는 기본 풀이 포함된 MemoryDriver를 생성한다.
//
// 기본 풀:
//   - local-zfs: ZFS 풀 (3.2TiB 전체 / 2.1TiB 사용)
//   - ceph-pool: Ceph 풀 (10TiB 전체 / 4TiB 사용)
//
// 호출 시점: NewService() 또는 설정에서 드라이버가 "memory"일 때
func NewMemoryDriver() *MemoryDriver {
	d := &MemoryDriver{
		pools:     make(map[string]*Pool),
		volumes:   make(map[string]*Volume),
		snapshots: make(map[string]*Snapshot),
	}
	d.nextVolID.Store(1)
	d.nextSnapID.Store(1)

	// Default pools
	d.pools["local-zfs"] = &Pool{
		Name: "local-zfs", PoolType: "zfs",
		TotalBytes: 3435973836800, UsedBytes: 2302160486400, // 3.2TiB / 2.1TiB
		Health: "healthy",
	}
	d.pools["ceph-pool"] = &Pool{
		Name: "ceph-pool", PoolType: "ceph",
		TotalBytes: 10995116277760, UsedBytes: 4398046511104, // 10TiB / 4TiB
		Health: "healthy",
	}
	return d
}

// Name 은 드라이버 이름 "memory"를 반환한다.
func (d *MemoryDriver) Name() string { return "memory" }

// ListPools 는 인메모리에 저장된 모든 풀을 반환한다.
// 동시 호출 안전성: 안전 (RLock 사용)
func (d *MemoryDriver) ListPools() ([]*Pool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]*Pool, 0, len(d.pools))
	for _, p := range d.pools {
		result = append(result, p)
	}
	return result, nil
}

// CreateVolume 은 인메모리에 볼륨을 생성한다.
// 풀 미존재 시 에러 반환. 풀의 UsedBytes를 증가시킨다.
// 동시 호출 안전성: 안전 (Lock 사용, ID는 atomic 카운터)
func (d *MemoryDriver) CreateVolume(pool, name, format string, sizeBytes uint64) (*Volume, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.pools[pool]; !ok {
		return nil, fmt.Errorf("pool not found: %s", pool)
	}
	id := fmt.Sprintf("vol-%d", d.nextVolID.Add(1)-1)
	vol := &Volume{
		ID: id, Pool: pool, Name: name,
		SizeBytes: sizeBytes, Format: format,
		Path:      fmt.Sprintf("/dev/%s/%s", pool, name),
		CreatedAt: time.Now().Unix(),
	}
	d.volumes[id] = vol
	d.pools[pool].UsedBytes += sizeBytes
	return vol, nil
}

// DeleteVolume 은 인메모리에서 볼륨을 삭제한다.
// 볼륨 미존재 시 에러 반환. 풀의 UsedBytes를 감소시킨다.
// 동시 호출 안전성: 안전 (Lock 사용)
func (d *MemoryDriver) DeleteVolume(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	vol, ok := d.volumes[id]
	if !ok {
		return fmt.Errorf("volume not found: %s", id)
	}
	if p, ok := d.pools[vol.Pool]; ok {
		if p.UsedBytes >= vol.SizeBytes {
			p.UsedBytes -= vol.SizeBytes
		}
	}
	delete(d.volumes, id)
	return nil
}

// CreateSnapshot 은 인메모리에 스냅샷을 생성한다.
// 볼륨 미존재 시 에러 반환.
// 동시 호출 안전성: 안전 (Lock 사용, ID는 atomic 카운터)
func (d *MemoryDriver) CreateSnapshot(volumeID, name string) (*Snapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.volumes[volumeID]; !ok {
		return nil, fmt.Errorf("volume not found: %s", volumeID)
	}
	id := fmt.Sprintf("snap-%d", d.nextSnapID.Add(1)-1)
	snap := &Snapshot{
		ID: id, VolumeID: volumeID, Name: name,
		CreatedAt: time.Now().Unix(),
	}
	d.snapshots[id] = snap
	return snap, nil
}

// ListSnapshots 는 인메모리 스냅샷 목록을 반환한다.
// volumeID가 빈 문자열이면 전체 반환.
// 동시 호출 안전성: 안전 (RLock 사용)
func (d *MemoryDriver) ListSnapshots(volumeID string) ([]*Snapshot, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]*Snapshot, 0)
	for _, snap := range d.snapshots {
		if volumeID == "" || snap.VolumeID == volumeID {
			result = append(result, snap)
		}
	}
	return result, nil
}
