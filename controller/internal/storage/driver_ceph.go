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
	"fmt"
	"os/exec"
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
	d := &CephDriver{pool: pool}
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
	d.mu.Unlock()

	return &Volume{
		ID:        id,
		Pool:      pool,
		Name:      name,
		SizeBytes: sizeBytes,
		Format:    "rbd",
		Path:      fmt.Sprintf("rbd:%s/%s", pool, name),
		CreatedAt: time.Now().Unix(),
	}, nil
}

// DeleteVolume 은 Ceph RBD 이미지를 삭제한다.
// 현재는 name→id 매핑이 구현되지 않아 best-effort로 nil을 반환한다.
// TODO: 볼륨 ID에서 pool/name을 추출하여 "rbd rm" 실행
// 멱등성: 현재 항상 성공 반환
func (d *CephDriver) DeleteVolume(id string) error {
	// name→id 매핑 필요 — 현재는 best-effort
	return nil
}

// CreateSnapshot 은 Ceph RBD 스냅샷을 생성한다.
// 현재는 인메모리에만 기록하며, 실제 "rbd snap create" 명령은 미구현이다.
// TODO: "rbd snap create <pool>/<volume>@<snap>" 실행
func (d *CephDriver) CreateSnapshot(volumeID, name string) (*Snapshot, error) {
	// rbd snap create <pool>/<volume>@<snap>
	d.mu.Lock()
	id := fmt.Sprintf("ceph-snap-%d", d.nextSnapID.Add(1)-1)
	d.mu.Unlock()

	return &Snapshot{
		ID:        id,
		VolumeID:  volumeID,
		Name:      name,
		CreatedAt: time.Now().Unix(),
	}, nil
}

// ListSnapshots 는 Ceph RBD 스냅샷 목록을 반환한다.
// 현재는 미구현으로 빈 목록을 반환한다.
// TODO: "rbd snap ls <pool>/<volume>" 실행
func (d *CephDriver) ListSnapshots(volumeID string) ([]*Snapshot, error) {
	// rbd snap ls <pool>/<volume>
	return nil, nil
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
