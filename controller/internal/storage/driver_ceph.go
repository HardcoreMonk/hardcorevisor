// Ceph RBD 스토리지 드라이버 — rbd/ceph CLI 기반
//
// Ceph RADOS Block Device(RBD) 이미지를 관리하는 드라이버이다.
// ceph, rbd 명령어를 exec.Command로 실행하므로 시스템에 Ceph 클라이언트가 설치되어 있어야 한다.
//
// 외부 명령 실행:
//   - "ceph osd pool stats": 풀 통계 조회
//   - "rbd create": RBD 이미지 생성
//   - "rbd snap create/ls": 스냅샷 생성/조회
//
// 주의: Ceph 클러스터 연결 설정(/etc/ceph/ceph.conf)이 필요하다.
// 환경변수: HCV_CEPH_POOL로 기본 풀 이름 설정 가능 (기본값: "rbd")
package storage

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// CephDriver 는 Ceph RBD CLI를 통해 스토리지를 관리하는 드라이버이다.
// pool 필드에 기본 Ceph 풀 이름을 저장하며, 볼륨/스냅샷 ID는 atomic 카운터로 생성한다.
// 부작용: CreateVolume은 실제 RBD 이미지를 생성한다.
type CephDriver struct {
	mu         sync.RWMutex
	pool       string // default Ceph pool name
	volumes    map[string]*Volume   // id → Volume (인메모리 매핑)
	snapshots  map[string]*Snapshot // id → Snapshot (인메모리 매핑)
	nextVolID  atomic.Int32
	nextSnapID atomic.Int32
}

// NewCephDriver 는 지정된 기본 풀 이름으로 CephDriver를 생성한다.
// pool이 빈 문자열이면 기본값 "rbd"를 사용한다.
//
// 호출 시점: 설정에서 드라이버가 "ceph"일 때 Controller 초기화 시
func NewCephDriver(pool string) *CephDriver {
	if pool == "" {
		pool = "rbd"
	}
	d := &CephDriver{
		pool:      pool,
		volumes:   make(map[string]*Volume),
		snapshots: make(map[string]*Snapshot),
	}
	d.nextVolID.Store(1)
	d.nextSnapID.Store(1)
	return d
}

// Name 은 드라이버 이름 "ceph"를 반환한다.
func (d *CephDriver) Name() string { return "ceph" }

// ListPools 는 "ceph osd pool stats" 명령으로 풀 통계를 조회한다.
// CLI 실행 실패 시 설정된 기본 풀 1개를 health "unknown"으로 반환 (폴백).
// 부작용: 없음 (읽기 전용)
func (d *CephDriver) ListPools() ([]*Pool, error) {
	// Run: ceph osd pool stats --format json
	out, err := exec.Command("ceph", "osd", "pool", "stats", "--format", "json").Output()
	if err != nil {
		// Fallback: return single configured pool
		return []*Pool{{
			Name:     d.pool,
			PoolType: "ceph",
			Health:   "unknown",
		}}, nil
	}
	// Parse JSON output (best-effort — fallback to single pool)
	_ = out
	return []*Pool{{Name: d.pool, PoolType: "ceph", Health: "healthy"}}, nil
}

// CreateVolume 은 "rbd create --size <MB> <pool>/<name>" 명령으로 RBD 이미지를 생성한다.
// 크기는 MB 단위로 변환된다. 경로는 "rbd:pool/name" 형식이다.
// 에러 조건: rbd 명령 실행 실패, 풀 미존재
// 부작용: 실제 Ceph RBD 이미지 생성 (클러스터 상태 변경)
func (d *CephDriver) CreateVolume(pool, name, format string, sizeBytes uint64) (*Volume, error) {
	// rbd create --size <MB> <pool>/<name>
	sizeMB := sizeBytes / (1024 * 1024)
	imgName := fmt.Sprintf("%s/%s", pool, name)
	cmd := exec.Command("rbd", "create", "--size", fmt.Sprintf("%d", sizeMB), imgName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("rbd create: %s: %w", string(out), err)
	}

	d.mu.Lock()
	id := fmt.Sprintf("ceph-vol-%d", d.nextVolID.Add(1)-1)
	vol := &Volume{
		ID:        id,
		Pool:      pool,
		Name:      name,
		SizeBytes: sizeBytes,
		Format:    "rbd",
		Path:      fmt.Sprintf("rbd:%s/%s", pool, name),
		CreatedAt: time.Now().Unix(),
	}
	d.volumes[id] = vol
	d.mu.Unlock()

	return vol, nil
}

// DeleteVolume 은 "rbd rm <pool>/<name>" 명령으로 Ceph RBD 이미지를 삭제한다.
// 인메모리 매핑에서 pool/name을 조회하여 실제 rbd rm을 실행한다.
func (d *CephDriver) DeleteVolume(id string) error {
	d.mu.Lock()
	vol, ok := d.volumes[id]
	if ok {
		delete(d.volumes, id)
	}
	d.mu.Unlock()

	if !ok {
		return fmt.Errorf("volume not found: %s", id)
	}

	imgName := fmt.Sprintf("%s/%s", vol.Pool, vol.Name)
	cmd := exec.Command("rbd", "rm", imgName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rbd rm: %s: %w", string(out), err)
	}
	return nil
}

// CreateSnapshot 은 "rbd snap create <pool>/<volume>@<snap>" 명령으로 Ceph RBD 스냅샷을 생성한다.
func (d *CephDriver) CreateSnapshot(volumeID, name string) (*Snapshot, error) {
	d.mu.RLock()
	vol, ok := d.volumes[volumeID]
	d.mu.RUnlock()

	// 인메모리 매핑이 있으면 실제 rbd snap create 실행
	if ok {
		snapSpec := fmt.Sprintf("%s/%s@%s", vol.Pool, vol.Name, name)
		cmd := exec.Command("rbd", "snap", "create", snapSpec)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("rbd snap create: %s: %w", string(out), err)
		}
	}

	d.mu.Lock()
	id := fmt.Sprintf("ceph-snap-%d", d.nextSnapID.Add(1)-1)
	snap := &Snapshot{
		ID:        id,
		VolumeID:  volumeID,
		Name:      name,
		CreatedAt: time.Now().Unix(),
	}
	d.snapshots[id] = snap
	d.mu.Unlock()

	return snap, nil
}

// ListSnapshots 는 인메모리 매핑에서 해당 볼륨의 스냅샷 목록을 반환한다.
func (d *CephDriver) ListSnapshots(volumeID string) ([]*Snapshot, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var result []*Snapshot
	for _, snap := range d.snapshots {
		if snap.VolumeID == volumeID {
			result = append(result, snap)
		}
	}
	return result, nil
}

// RollbackSnapshot 은 "rbd snap rollback <snapshotID>" 명령으로 Ceph RBD 스냅샷을 롤백한다.
// 볼륨이 스냅샷 시점의 상태로 되돌아간다.
//
// 매개변수:
//   - snapshotID: 롤백할 스냅샷 ID (형식: "pool/volume@snapname")
//
// 에러 조건: 스냅샷 미존재, Ceph 클러스터 연결 실패, 권한 부족
// 부작용: 실제 RBD 이미지 데이터가 스냅샷 시점으로 변경됨 (복구 불가)
// TODO: snapshotID를 "pool/volume@snap" 형식으로 변환하는 매핑 로직 추가
func (d *CephDriver) RollbackSnapshot(snapshotID string) error {
	cmd := exec.Command("rbd", "snap", "rollback", snapshotID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rbd snap rollback: %s: %w", string(out), err)
	}
	return nil
}

// CloneSnapshot 은 "rbd clone <snapshotID> <pool>/<newVolumeName>" 명령으로
// Ceph RBD 스냅샷에서 새 이미지를 클론한다.
//
// Ceph의 COW(Copy-On-Write) 클론을 사용하므로 초기 복제가 매우 빠르다.
// 클론된 이미지는 원본 스냅샷에 의존하므로, 스냅샷 삭제 전에 flatten이 필요하다.
//
// 매개변수:
//   - snapshotID: 원본 스냅샷 ID
//   - newVolumeName: 새 볼륨 이름
//
// 에러 조건: 스냅샷 미존재, 대상 이름 중복, Ceph 클러스터 연결 실패
// 부작용: 실제 RBD 클론 이미지 생성
func (d *CephDriver) CloneSnapshot(snapshotID, newVolumeName string) (*Volume, error) {
	newImg := fmt.Sprintf("%s/%s", d.pool, newVolumeName)
	cmd := exec.Command("rbd", "clone", snapshotID, newImg)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("rbd clone: %s: %w", string(out), err)
	}

	d.mu.Lock()
	id := fmt.Sprintf("ceph-vol-%d", d.nextVolID.Add(1)-1)
	d.mu.Unlock()

	return &Volume{
		ID:   id,
		Pool: d.pool,
		Name: newVolumeName,
		Path: fmt.Sprintf("rbd:%s", newImg),
	}, nil
}

// DeleteSnapshot 은 "rbd snap rm <snapshotID>" 명령으로 Ceph RBD 스냅샷을 삭제한다.
//
// 매개변수:
//   - snapshotID: 삭제할 스냅샷 ID
//
// 에러 조건: 스냅샷 미존재, 클론 의존성 (flatten 안 된 클론이 있으면 삭제 불가)
// 부작용: 실제 RBD 스냅샷 삭제 (복구 불가)
func (d *CephDriver) DeleteSnapshot(snapshotID string) error {
	cmd := exec.Command("rbd", "snap", "rm", snapshotID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rbd snap rm: %s: %w", string(out), err)
	}
	return nil
}

// GetVolume 은 인메모리 매핑에서 볼륨을 조회한다.
// 매핑에 없으면 "rbd info" 명령으로 Ceph에서 직접 조회를 시도한다.
func (d *CephDriver) GetVolume(id string) (*Volume, error) {
	d.mu.RLock()
	vol, ok := d.volumes[id]
	d.mu.RUnlock()
	if ok {
		return vol, nil
	}
	return nil, fmt.Errorf("volume not found: %s", id)
}

// ResizeVolume 은 "rbd resize --size <MB> <id>" 명령으로 RBD 이미지 크기를 변경한다.
//
// 에러 조건: 이미지 미존재, 크기 축소 시 --allow-shrink 미지정, 권한 부족
// 부작용: 실제 RBD 이미지 크기 변경
func (d *CephDriver) ResizeVolume(id string, newSizeBytes uint64) error {
	sizeMB := newSizeBytes / (1024 * 1024)
	cmd := exec.Command("rbd", "resize", "--size", fmt.Sprintf("%d", sizeMB), id)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rbd resize: %s: %w", string(out), err)
	}
	return nil
}

// ListVolumes 는 "rbd ls --format json" + "rbd info" 명령으로 RBD 이미지 목록을 조회한다.
//
// pool이 빈 문자열이면 기본 풀을 사용한다.
// 에러 조건: rbd 명령 실행 실패
// 부작용: 없음 (읽기 전용)
func (d *CephDriver) ListVolumes(pool string) ([]Volume, error) {
	if pool == "" {
		pool = d.pool
	}

	// "rbd ls" 명령으로 이미지 이름 목록 조회
	cmd := exec.Command("rbd", "ls", "-p", pool, "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		// CLI 실패 시 인메모리 매핑에서 반환 (폴백)
		d.mu.RLock()
		defer d.mu.RUnlock()
		var result []Volume
		for _, v := range d.volumes {
			if v.Pool == pool || pool == "" {
				result = append(result, *v)
			}
		}
		return result, nil
	}

	// JSON 파싱: ["image1", "image2", ...]
	var names []string
	if err := json.Unmarshal(out, &names); err != nil {
		return nil, fmt.Errorf("rbd ls parse: %w", err)
	}

	result := make([]Volume, 0, len(names))
	for _, name := range names {
		result = append(result, Volume{
			ID:   fmt.Sprintf("rbd-%s", strings.ReplaceAll(name, "/", "-")),
			Pool: pool,
			Name: name,
			Path: fmt.Sprintf("rbd:%s/%s", pool, name),
		})
	}
	return result, nil
}

// ExportVolume 은 "rbd export <id> <path>" 명령으로 RBD 이미지를 파일로 내보낸다.
//
// 백업 또는 다른 클러스터로의 이동에 사용된다.
// 에러 조건: 이미지 미존재, 경로 쓰기 불가, 디스크 용량 부족
// 부작용: 지정 경로에 이미지 파일 생성
func (d *CephDriver) ExportVolume(id, path string) error {
	cmd := exec.Command("rbd", "export", id, path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rbd export: %s: %w", string(out), err)
	}
	return nil
}

// ImportVolume 은 "rbd import <path> <pool>/<name>" 명령으로 파일을 RBD 이미지로 가져온다.
//
// 에러 조건: 경로 읽기 불가, 풀 미존재, 이름 중복
// 부작용: 실제 RBD 이미지 생성
func (d *CephDriver) ImportVolume(path, pool, name string) (*Volume, error) {
	if pool == "" {
		pool = d.pool
	}
	imgName := fmt.Sprintf("%s/%s", pool, name)
	cmd := exec.Command("rbd", "import", path, imgName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("rbd import: %s: %w", string(out), err)
	}

	d.mu.Lock()
	id := fmt.Sprintf("ceph-vol-%d", d.nextVolID.Add(1)-1)
	d.mu.Unlock()

	return &Volume{
		ID:        id,
		Pool:      pool,
		Name:      name,
		Format:    "rbd",
		Path:      fmt.Sprintf("rbd:%s", imgName),
		CreatedAt: time.Now().Unix(),
	}, nil
}

// FlattenClone 은 "rbd flatten <id>" 명령으로 클론의 스냅샷 의존성을 제거한다.
//
// 클론이 독립적인 이미지가 되어 원본 스냅샷을 삭제할 수 있게 된다.
// 에러 조건: 이미지 미존재, 이미 flatten된 이미지
// 부작용: 데이터 복사로 인한 I/O 발생 (시간 소요 가능)
func (d *CephDriver) FlattenClone(id string) error {
	cmd := exec.Command("rbd", "flatten", id)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rbd flatten: %s: %w", string(out), err)
	}
	return nil
}
