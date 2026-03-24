// ZFS 스토리지 드라이버 — zpool/zfs CLI 기반
//
// 실제 ZFS 파일 시스템을 관리하는 드라이버이다.
// zpool, zfs 명령어를 exec.Command로 실행하므로 시스템에 ZFS가 설치되어 있어야 한다.
//
// 외부 명령 실행:
//   - "zpool list": 풀 목록 조회
//   - "zfs create -V": zvol(블록 볼륨) 생성
//   - "zfs destroy": 볼륨/스냅샷 삭제
//   - "zfs snapshot": 시점 스냅샷 생성
//   - "zfs list -t snapshot": 스냅샷 목록 조회
//
// 주의: root 권한 또는 ZFS 위임 권한이 필요할 수 있다.
package storage

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode"
)

// ZFSDriver 는 ZFS 명령줄 도구를 사용하여 StorageDriver를 구현한다.
// 상태를 내부에 보관하지 않으며 (stateless), 매 호출마다 ZFS CLI를 직접 실행한다.
// 부작용: 모든 쓰기 메서드는 실제 파일 시스템을 변경한다.
type ZFSDriver struct{}

// Name 은 드라이버 이름 "zfs"를 반환한다.
func (d *ZFSDriver) Name() string { return "zfs" }

// ListPools 는 "zpool list -H -o name,size,alloc,health" 명령을 실행하여
// 시스템의 ZFS 풀 목록을 조회한다.
// 에러 조건: zpool 명령 미설치, 권한 부족
// 부작용: 없음 (읽기 전용)
func (d *ZFSDriver) ListPools() ([]*Pool, error) {
	// Run: zpool list -H -o name,size,alloc,health
	out, err := exec.Command("zpool", "list", "-H", "-o", "name,size,alloc,health").Output()
	if err != nil {
		return nil, fmt.Errorf("zpool list: %w", err)
	}
	// Parse tab-separated output
	var pools []*Pool
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pools = append(pools, &Pool{
			Name:       fields[0],
			PoolType:   "zfs",
			TotalBytes: parseSize(fields[1]),
			UsedBytes:  parseSize(fields[2]),
			Health:     strings.ToLower(fields[3]),
		})
	}
	return pools, nil
}

// CreateVolume 은 "zfs create -V <size> <pool>/<name>" 명령으로 zvol을 생성한다.
// 볼륨 ID는 "pool/name" 형식이며, 경로는 "/dev/zvol/pool/name"이다.
// 에러 조건: 풀 미존재, 권한 부족, 용량 부족
// 부작용: 실제 ZFS zvol 생성 (파일 시스템 변경)
func (d *ZFSDriver) CreateVolume(pool, name, format string, sizeBytes uint64) (*Volume, error) {
	fullName := fmt.Sprintf("%s/%s", pool, name)
	sizeStr := fmt.Sprintf("%d", sizeBytes)

	// zfs create -V <size> <pool>/<name>
	cmd := exec.Command("zfs", "create", "-V", sizeStr, fullName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("zfs create %s: %s: %w", fullName, strings.TrimSpace(string(out)), err)
	}

	vol := &Volume{
		ID:        fullName,
		Pool:      pool,
		Name:      name,
		SizeBytes: sizeBytes,
		Format:    format,
		Path:      fmt.Sprintf("/dev/zvol/%s", fullName),
		CreatedAt: time.Now().Unix(),
	}
	return vol, nil
}

// DeleteVolume 은 "zfs destroy <id>" 명령으로 zvol을 삭제한다.
// id는 "pool/name" 형식이어야 한다.
// 에러 조건: 볼륨 미존재, 스냅샷 의존성, 권한 부족
// 부작용: 실제 ZFS zvol 삭제 (파일 시스템 변경, 복구 불가)
func (d *ZFSDriver) DeleteVolume(id string) error {
	// zfs destroy <pool>/<name>
	cmd := exec.Command("zfs", "destroy", id)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("zfs destroy %s: %s: %w", id, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// CreateSnapshot 은 "zfs snapshot <pool>/<name>@<snap>" 명령으로 스냅샷을 생성한다.
// 스냅샷 ID는 "pool/volume@snapname" 형식이다.
// 에러 조건: 볼륨 미존재, 동명 스냅샷 이미 존재, 권한 부족
// 부작용: 실제 ZFS 스냅샷 생성 (파일 시스템 변경)
func (d *ZFSDriver) CreateSnapshot(volumeID, name string) (*Snapshot, error) {
	snapName := fmt.Sprintf("%s@%s", volumeID, name)

	// zfs snapshot <pool>/<name>@<snap>
	cmd := exec.Command("zfs", "snapshot", snapName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("zfs snapshot %s: %s: %w", snapName, strings.TrimSpace(string(out)), err)
	}

	snap := &Snapshot{
		ID:        snapName,
		VolumeID:  volumeID,
		Name:      name,
		CreatedAt: time.Now().Unix(),
	}
	return snap, nil
}

// ListSnapshots 는 "zfs list -t snapshot" 명령으로 스냅샷 목록을 조회한다.
// volumeID가 지정되면 해당 볼륨의 스냅샷만 반환 (-r 옵션 사용).
// 스냅샷 이름은 "pool/volume@snapname" 형식에서 "@" 뒤의 부분이다.
// 에러 조건: zfs 명령 실행 실패
// 부작용: 없음 (읽기 전용)
func (d *ZFSDriver) ListSnapshots(volumeID string) ([]*Snapshot, error) {
	// zfs list -t snapshot -H -o name,creation <pool>/<name>
	args := []string{"list", "-t", "snapshot", "-H", "-o", "name,creation"}
	if volumeID != "" {
		args = append(args, "-r", volumeID)
	}

	out, err := exec.Command("zfs", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("zfs list snapshots: %w", err)
	}

	var snapshots []*Snapshot
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) < 1 {
			continue
		}
		fullName := fields[0]
		// Extract snapshot name from pool/vol@snapname
		parts := strings.SplitN(fullName, "@", 2)
		if len(parts) != 2 {
			continue
		}
		snapshots = append(snapshots, &Snapshot{
			ID:        fullName,
			VolumeID:  parts[0],
			Name:      parts[1],
			CreatedAt: time.Now().Unix(),
		})
	}
	return snapshots, nil
}

// RollbackSnapshot 은 "zfs rollback <snapshotID>" 명령으로 스냅샷을 롤백한다.
// 볼륨이 스냅샷 시점의 상태로 되돌아간다.
// 에러 조건: 스냅샷 미존재, 권한 부족
// 부작용: 실제 ZFS 볼륨 데이터 변경 (복구 불가)
func (d *ZFSDriver) RollbackSnapshot(snapshotID string) error {
	cmd := exec.Command("zfs", "rollback", snapshotID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("zfs rollback %s: %s: %w", snapshotID, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// CloneSnapshot 은 "zfs clone <snapshotID> <pool>/<newVolName>" 명령으로
// 스냅샷에서 새 볼륨을 복제한다.
// snapshotID는 "pool/volume@snapname" 형식이어야 하며, 풀 이름을 자동 추출한다.
// 에러 조건: 스냅샷 미존재, 권한 부족
// 부작용: 실제 ZFS clone 생성 (파일 시스템 변경)
func (d *ZFSDriver) CloneSnapshot(snapshotID, newVolName string) (*Volume, error) {
	// Extract pool from snapshotID (format: pool/volume@snapname)
	parts := strings.SplitN(snapshotID, "/", 2)
	pool := parts[0]
	targetName := fmt.Sprintf("%s/%s", pool, newVolName)

	cmd := exec.Command("zfs", "clone", snapshotID, targetName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("zfs clone %s %s: %s: %w", snapshotID, targetName, strings.TrimSpace(string(out)), err)
	}

	vol := &Volume{
		ID:        targetName,
		Pool:      pool,
		Name:      newVolName,
		Format:    "zvol",
		Path:      fmt.Sprintf("/dev/zvol/%s", targetName),
		CreatedAt: time.Now().Unix(),
	}
	return vol, nil
}

// DeleteSnapshot 은 "zfs destroy <snapshotID>" 명령으로 스냅샷을 삭제한다.
// snapshotID는 "pool/volume@snapname" 형식이어야 한다.
// 에러 조건: 스냅샷 미존재, 클론 의존성, 권한 부족
// 부작용: 실제 ZFS 스냅샷 삭제 (복구 불가)
func (d *ZFSDriver) DeleteSnapshot(snapshotID string) error {
	cmd := exec.Command("zfs", "destroy", snapshotID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("zfs destroy snapshot %s: %s: %w", snapshotID, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// ListVolumes 는 "zfs list -H -o name,used,avail,refer -t volume" 명령으로
// ZFS 볼륨 목록을 조회한다. pool이 빈 문자열이 아니면 해당 풀로 필터링한다.
// 에러 조건: zfs 명령 실행 실패
// 부작용: 없음 (읽기 전용)
func (d *ZFSDriver) ListVolumes(pool string) ([]Volume, error) {
	args := []string{"list", "-H", "-o", "name,used,avail,refer", "-t", "volume"}
	if pool != "" {
		args = append(args, "-r", pool)
	}

	out, err := exec.Command("zfs", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("zfs list volumes: %w", err)
	}

	var volumes []Volume
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		fullName := fields[0]
		nameParts := strings.SplitN(fullName, "/", 2)
		volPool := nameParts[0]
		volName := ""
		if len(nameParts) > 1 {
			volName = nameParts[1]
		}
		volumes = append(volumes, Volume{
			ID:        fullName,
			Pool:      volPool,
			Name:      volName,
			SizeBytes: parseSize(fields[3]), // refer = logical size
			Format:    "zvol",
			Path:      fmt.Sprintf("/dev/zvol/%s", fullName),
			CreatedAt: time.Now().Unix(),
		})
	}
	return volumes, nil
}

// GetVolume 은 ZFS 볼륨 정보를 조회한다.
// "zfs list -H -o name,used,refer -t volume <id>" 명령을 실행한다.
// id는 "pool/name" 형식이어야 한다.
// 에러 조건: 볼륨 미존재, zfs 명령 실행 실패
func (d *ZFSDriver) GetVolume(id string) (*Volume, error) {
	out, err := exec.Command("zfs", "list", "-H", "-o", "name,used,refer", "-t", "volume", id).Output()
	if err != nil {
		return nil, fmt.Errorf("volume not found: %s", id)
	}
	line := strings.TrimSpace(string(out))
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return nil, fmt.Errorf("volume not found: %s", id)
	}
	parts := strings.SplitN(fields[0], "/", 2)
	pool := parts[0]
	name := ""
	if len(parts) > 1 {
		name = parts[1]
	}
	return &Volume{
		ID:        id,
		Pool:      pool,
		Name:      name,
		SizeBytes: parseSize(fields[2]),
		Format:    "zvol",
		Path:      fmt.Sprintf("/dev/zvol/%s", id),
		CreatedAt: time.Now().Unix(),
	}, nil
}

// ResizeVolume 은 "zfs set volsize=<size> <id>" 명령으로 ZFS 볼륨 크기를 변경한다.
// 에러 조건: 볼륨 미존재, 크기 축소 불가, 권한 부족
// 부작용: 실제 ZFS zvol 크기 변경
func (d *ZFSDriver) ResizeVolume(id string, newSizeBytes uint64) error {
	sizeStr := fmt.Sprintf("volsize=%d", newSizeBytes)
	cmd := exec.Command("zfs", "set", sizeStr, id)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("zfs resize %s: %s: %w", id, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// parseSize 는 ZFS 크기 문자열을 바이트 단위로 변환한다.
// 예: "1.5T" → 1649267441664, "500G" → 536870912000, "100M" → 104857600
// 지원 단위: K, M, G, T, P (1024 기반)
// 파싱 실패 시 0을 반환한다.
func parseSize(s string) uint64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}

	// Find where the numeric part ends and the suffix begins
	i := 0
	for i < len(s) && (unicode.IsDigit(rune(s[i])) || s[i] == '.') {
		i++
	}
	if i == 0 {
		return 0
	}

	numStr := s[:i]
	suffix := strings.ToUpper(strings.TrimSpace(s[i:]))

	var num float64
	if _, err := fmt.Sscanf(numStr, "%f", &num); err != nil {
		return 0
	}

	multiplier := uint64(1)
	switch suffix {
	case "K":
		multiplier = 1024
	case "M":
		multiplier = 1024 * 1024
	case "G":
		multiplier = 1024 * 1024 * 1024
	case "T":
		multiplier = 1024 * 1024 * 1024 * 1024
	case "P":
		multiplier = 1024 * 1024 * 1024 * 1024 * 1024
	}

	return uint64(num * float64(multiplier))
}
