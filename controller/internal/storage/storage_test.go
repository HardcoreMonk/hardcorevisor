// storage 패키지 유닛 테스트
//
// 테스트 대상: NewService (인메모리 드라이버), CreateVolume, ListVolumes,
// GetVolume, DeleteVolume, CreateSnapshot, DeleteSnapshot
package storage

import (
	"testing"
)

// TestNewService — 인메모리 드라이버로 서비스 생성 시 기본 풀 확인
func TestNewService(t *testing.T) {
	t.Parallel()
	svc := NewService()

	if svc == nil {
		t.Fatal("NewService()가 nil을 반환함")
	}
	if svc.DriverName() != "memory" {
		t.Errorf("드라이버 이름: got %q, want %q", svc.DriverName(), "memory")
	}

	// 기본 풀 2개 확인
	pools := svc.ListPools()
	if len(pools) != 2 {
		t.Fatalf("기본 풀 수: got %d, want 2", len(pools))
	}
}

// TestCreateVolume — 볼륨 생성 후 필드가 올바른지 검증
func TestCreateVolume(t *testing.T) {
	t.Parallel()
	svc := NewService()

	vol, err := svc.CreateVolume("local-zfs", "disk-01", "qcow2", 10737418240)
	if err != nil {
		t.Fatalf("CreateVolume() 에러: %v", err)
	}
	if vol.ID == "" {
		t.Error("볼륨 ID가 비어 있음")
	}
	if vol.Pool != "local-zfs" {
		t.Errorf("Pool: got %q, want %q", vol.Pool, "local-zfs")
	}
	if vol.Name != "disk-01" {
		t.Errorf("Name: got %q, want %q", vol.Name, "disk-01")
	}
	if vol.Format != "qcow2" {
		t.Errorf("Format: got %q, want %q", vol.Format, "qcow2")
	}
	if vol.SizeBytes != 10737418240 {
		t.Errorf("SizeBytes: got %d, want %d", vol.SizeBytes, uint64(10737418240))
	}
}

// TestCreateVolumeInvalidPool — 존재하지 않는 풀에 볼륨 생성 시 에러 반환
func TestCreateVolumeInvalidPool(t *testing.T) {
	t.Parallel()
	svc := NewService()

	_, err := svc.CreateVolume("nonexistent", "disk-01", "raw", 1024)
	if err == nil {
		t.Fatal("존재하지 않는 풀에 대해 에러가 반환되어야 함")
	}
}

// TestListVolumes — 볼륨 목록 조회 및 풀 필터링 검증
func TestListVolumes(t *testing.T) {
	t.Parallel()
	svc := NewService()

	// 초기 상태: 비어 있음
	if vols := svc.ListVolumes(""); len(vols) != 0 {
		t.Errorf("초기 목록 길이: got %d, want 0", len(vols))
	}

	// 서로 다른 풀에 볼륨 생성
	if _, err := svc.CreateVolume("local-zfs", "disk-01", "raw", 1024); err != nil {
		t.Fatalf("CreateVolume() 에러: %v", err)
	}
	if _, err := svc.CreateVolume("ceph-pool", "disk-02", "raw", 2048); err != nil {
		t.Fatalf("CreateVolume() 에러: %v", err)
	}

	// 전체 목록
	all := svc.ListVolumes("")
	if len(all) != 2 {
		t.Errorf("전체 목록 길이: got %d, want 2", len(all))
	}

	// 풀 필터
	zfsVols := svc.ListVolumes("local-zfs")
	if len(zfsVols) != 1 {
		t.Errorf("local-zfs 필터 결과: got %d, want 1", len(zfsVols))
	}
}

// TestGetVolume — ID로 볼륨 조회 검증
func TestGetVolume(t *testing.T) {
	t.Parallel()
	svc := NewService()

	created, err := svc.CreateVolume("local-zfs", "disk-01", "qcow2", 4096)
	if err != nil {
		t.Fatalf("CreateVolume() 에러: %v", err)
	}

	got, err := svc.GetVolume(created.ID)
	if err != nil {
		t.Fatalf("GetVolume() 에러: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID: got %q, want %q", got.ID, created.ID)
	}
}

// TestGetVolumeNotFound — 존재하지 않는 볼륨 조회 시 에러 반환
func TestGetVolumeNotFound(t *testing.T) {
	t.Parallel()
	svc := NewService()

	_, err := svc.GetVolume("nonexistent")
	if err == nil {
		t.Fatal("존재하지 않는 볼륨에 대해 에러가 반환되어야 함")
	}
}

// TestDeleteVolume — 볼륨 삭제 후 목록에서 제거되는지 검증
func TestDeleteVolume(t *testing.T) {
	t.Parallel()
	svc := NewService()

	vol, err := svc.CreateVolume("local-zfs", "disk-01", "raw", 1024)
	if err != nil {
		t.Fatalf("CreateVolume() 에러: %v", err)
	}

	if err := svc.DeleteVolume(vol.ID); err != nil {
		t.Fatalf("DeleteVolume() 에러: %v", err)
	}

	if vols := svc.ListVolumes(""); len(vols) != 0 {
		t.Errorf("삭제 후 목록 길이: got %d, want 0", len(vols))
	}
}

// TestDeleteVolumeNotFound — 존재하지 않는 볼륨 삭제 시 에러 반환
func TestDeleteVolumeNotFound(t *testing.T) {
	t.Parallel()
	svc := NewService()

	err := svc.DeleteVolume("nonexistent")
	if err == nil {
		t.Fatal("존재하지 않는 볼륨 삭제 시 에러가 반환되어야 함")
	}
}

// TestCreateAndDeleteSnapshot — 스냅샷 생성/삭제 검증
func TestCreateAndDeleteSnapshot(t *testing.T) {
	t.Parallel()
	svc := NewService()

	vol, err := svc.CreateVolume("local-zfs", "disk-01", "raw", 1024)
	if err != nil {
		t.Fatalf("CreateVolume() 에러: %v", err)
	}

	snap, err := svc.CreateSnapshot(vol.ID, "snap-test")
	if err != nil {
		t.Fatalf("CreateSnapshot() 에러: %v", err)
	}
	if snap.ID == "" {
		t.Error("스냅샷 ID가 비어 있음")
	}
	if snap.VolumeID != vol.ID {
		t.Errorf("VolumeID: got %q, want %q", snap.VolumeID, vol.ID)
	}

	if err := svc.DeleteSnapshot(snap.ID); err != nil {
		t.Fatalf("DeleteSnapshot() 에러: %v", err)
	}
}

// TestGetPool — 풀 이름으로 조회 검증
func TestGetPool(t *testing.T) {
	t.Parallel()
	svc := NewService()

	pool, err := svc.GetPool("local-zfs")
	if err != nil {
		t.Fatalf("GetPool() 에러: %v", err)
	}
	if pool.Name != "local-zfs" {
		t.Errorf("Name: got %q, want %q", pool.Name, "local-zfs")
	}
	if pool.Health != "healthy" {
		t.Errorf("Health: got %q, want %q", pool.Health, "healthy")
	}

	_, err = svc.GetPool("nonexistent")
	if err == nil {
		t.Fatal("존재하지 않는 풀 조회 시 에러가 반환되어야 함")
	}
}
