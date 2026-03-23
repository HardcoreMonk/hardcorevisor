// Package storage — 플러그어블 스토리지 백엔드 드라이버 인터페이스
package storage

// StorageDriver 는 플러그어블 스토리지 백엔드를 위한 인터페이스이다.
//
// 구현체:
//   - MemoryDriver: 인메모리 (개발/테스트용, 파일 시스템 변경 없음)
//   - ZFSDriver: ZFS CLI 기반 (zpool/zfs 명령어 실행)
//   - CephDriver: Ceph RBD CLI 기반 (rbd 명령어 실행)
//
// 구현 시 주의사항:
//   - 모든 메서드는 동시 호출에 안전해야 한다 (thread-safe)
//   - 에러 반환 시 래핑된 에러 형식을 사용한다 (fmt.Errorf("...: %w", err))
type StorageDriver interface {
	// Name 은 드라이버 이름을 반환한다 (예: "memory", "zfs", "ceph").
	// 멱등성: 항상 같은 값 반환
	Name() string

	// ListPools 는 사용 가능한 스토리지 풀 목록을 반환한다.
	// 멱등성: 읽기 전용, 부작용 없음
	// 에러 조건: ZFS/Ceph CLI 실행 실패 시
	ListPools() ([]*Pool, error)

	// CreateVolume 은 지정된 풀에 볼륨을 생성한다.
	// 멱등성: 아님 — 호출할 때마다 새 볼륨 생성
	// 부작용: ZFS는 "zfs create -V" 실행, Ceph는 "rbd create" 실행
	// 에러 조건: 풀 미존재, CLI 실행 실패, 용량 부족
	CreateVolume(pool, name, format string, sizeBytes uint64) (*Volume, error)

	// DeleteVolume 은 볼륨을 삭제한다.
	// 멱등성: 아님 — 이미 삭제된 볼륨에 대해 에러 반환
	// 부작용: ZFS는 "zfs destroy" 실행
	// 에러 조건: 볼륨 미존재, CLI 실행 실패
	DeleteVolume(id string) error

	// CreateSnapshot 은 볼륨의 시점 스냅샷을 생성한다.
	// 멱등성: 아님 — 호출할 때마다 새 스냅샷 생성
	// 부작용: ZFS는 "zfs snapshot" 실행
	// 에러 조건: 볼륨 미존재, CLI 실행 실패
	CreateSnapshot(volumeID, name string) (*Snapshot, error)

	// ListSnapshots 는 스냅샷 목록을 반환한다. volumeID가 빈 문자열이면 전체 반환.
	// 멱등성: 읽기 전용, 부작용 없음
	// 에러 조건: CLI 실행 실패
	ListSnapshots(volumeID string) ([]*Snapshot, error)

	// RollbackSnapshot 은 스냅샷을 롤백한다 (볼륨을 스냅샷 시점으로 되돌림).
	// 멱등성: 아님 — 볼륨 데이터가 스냅샷 시점으로 변경됨
	// 부작용: ZFS는 "zfs rollback" 실행
	// 에러 조건: 스냅샷 미존재, CLI 실행 실패
	RollbackSnapshot(snapshotID string) error

	// CloneSnapshot 은 스냅샷에서 새 볼륨을 복제한다.
	// 멱등성: 아님 — 호출할 때마다 새 볼륨 생성
	// 부작용: ZFS는 "zfs clone" 실행
	// 에러 조건: 스냅샷 미존재, CLI 실행 실패
	CloneSnapshot(snapshotID, newVolumeName string) (*Volume, error)

	// DeleteSnapshot 은 스냅샷을 삭제한다.
	// 멱등성: 아님 — 이미 삭제된 스냅샷에 대해 에러 반환
	// 부작용: ZFS는 "zfs destroy" 실행
	// 에러 조건: 스냅샷 미존재, CLI 실행 실패
	DeleteSnapshot(snapshotID string) error
}
