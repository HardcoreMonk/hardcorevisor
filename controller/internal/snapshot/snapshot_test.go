// snapshot 패키지 유닛 테스트
//
// 테스트 대상: NewService, Create, List, Get, Delete, Restore
package snapshot

import (
	"testing"
)

// TestNewService — 스냅샷 서비스 생성 검증
func TestNewService(t *testing.T) {
	t.Parallel()
	svc := NewService()

	if svc == nil {
		t.Fatal("NewService()가 nil을 반환함")
	}

	// 초기 상태: 비어 있음
	if list := svc.List(0); len(list) != 0 {
		t.Errorf("초기 목록 길이: got %d, want 0", len(list))
	}
}

// TestCreateSnapshot — 스냅샷 생성 후 필드가 올바른지 검증
func TestCreateSnapshot(t *testing.T) {
	t.Parallel()
	svc := NewService()

	snap, err := svc.Create(1, "web-01")
	if err != nil {
		t.Fatalf("Create() 에러: %v", err)
	}
	if snap.ID == "" {
		t.Error("스냅샷 ID가 비어 있음")
	}
	if snap.VMID != 1 {
		t.Errorf("VMID: got %d, want 1", snap.VMID)
	}
	if snap.VMName != "web-01" {
		t.Errorf("VMName: got %q, want %q", snap.VMName, "web-01")
	}
	if snap.State != "created" {
		t.Errorf("State: got %q, want %q", snap.State, "created")
	}
	if snap.SizeBytes != 1073741824 {
		t.Errorf("SizeBytes: got %d, want %d", snap.SizeBytes, uint64(1073741824))
	}
}

// TestCreateSnapshotEmptyName — 빈 VM 이름으로 생성 시 에러 반환
func TestCreateSnapshotEmptyName(t *testing.T) {
	t.Parallel()
	svc := NewService()

	_, err := svc.Create(1, "")
	if err == nil {
		t.Fatal("빈 VM 이름으로 생성 시 에러가 반환되어야 함")
	}
}

// TestListSnapshots — 스냅샷 목록 조회 및 VM 필터링 검증
func TestListSnapshots(t *testing.T) {
	t.Parallel()
	svc := NewService()

	if _, err := svc.Create(1, "vm-01"); err != nil {
		t.Fatalf("Create() 에러: %v", err)
	}
	if _, err := svc.Create(2, "vm-02"); err != nil {
		t.Fatalf("Create() 에러: %v", err)
	}
	if _, err := svc.Create(1, "vm-01"); err != nil {
		t.Fatalf("Create() 에러: %v", err)
	}

	// 전체 목록
	all := svc.List(0)
	if len(all) != 3 {
		t.Errorf("전체 목록 길이: got %d, want 3", len(all))
	}

	// VM 필터
	vm1Snaps := svc.List(1)
	if len(vm1Snaps) != 2 {
		t.Errorf("VM 1 스냅샷 수: got %d, want 2", len(vm1Snaps))
	}

	vm2Snaps := svc.List(2)
	if len(vm2Snaps) != 1 {
		t.Errorf("VM 2 스냅샷 수: got %d, want 1", len(vm2Snaps))
	}
}

// TestGetSnapshot — ID로 스냅샷 조회 검증
func TestGetSnapshot(t *testing.T) {
	t.Parallel()
	svc := NewService()

	created, err := svc.Create(1, "vm-01")
	if err != nil {
		t.Fatalf("Create() 에러: %v", err)
	}

	got, err := svc.Get(created.ID)
	if err != nil {
		t.Fatalf("Get() 에러: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID: got %q, want %q", got.ID, created.ID)
	}
}

// TestGetSnapshotNotFound — 존재하지 않는 스냅샷 조회 시 에러 반환
func TestGetSnapshotNotFound(t *testing.T) {
	t.Parallel()
	svc := NewService()

	_, err := svc.Get("nonexistent")
	if err == nil {
		t.Fatal("존재하지 않는 스냅샷에 대해 에러가 반환되어야 함")
	}
}

// TestDeleteSnapshot — 스냅샷 삭제 후 목록에서 제거되는지 검증
func TestDeleteSnapshot(t *testing.T) {
	t.Parallel()
	svc := NewService()

	snap, err := svc.Create(1, "vm-01")
	if err != nil {
		t.Fatalf("Create() 에러: %v", err)
	}

	if err := svc.Delete(snap.ID); err != nil {
		t.Fatalf("Delete() 에러: %v", err)
	}

	if list := svc.List(0); len(list) != 0 {
		t.Errorf("삭제 후 목록 길이: got %d, want 0", len(list))
	}
}

// TestDeleteSnapshotNotFound — 존재하지 않는 스냅샷 삭제 시 에러 반환
func TestDeleteSnapshotNotFound(t *testing.T) {
	t.Parallel()
	svc := NewService()

	err := svc.Delete("nonexistent")
	if err == nil {
		t.Fatal("존재하지 않는 스냅샷 삭제 시 에러가 반환되어야 함")
	}
}

// TestRestoreSnapshot — 스냅샷 복원 후 상태가 "restoring"으로 변경되는지 검증
func TestRestoreSnapshot(t *testing.T) {
	t.Parallel()
	svc := NewService()

	snap, err := svc.Create(1, "vm-01")
	if err != nil {
		t.Fatalf("Create() 에러: %v", err)
	}

	restored, err := svc.Restore(snap.ID)
	if err != nil {
		t.Fatalf("Restore() 에러: %v", err)
	}
	if restored.State != "restoring" {
		t.Errorf("State: got %q, want %q", restored.State, "restoring")
	}
}

// TestRestoreSnapshotNotFound — 존재하지 않는 스냅샷 복원 시 에러 반환
func TestRestoreSnapshotNotFound(t *testing.T) {
	t.Parallel()
	svc := NewService()

	_, err := svc.Restore("nonexistent")
	if err == nil {
		t.Fatal("존재하지 않는 스냅샷 복원 시 에러가 반환되어야 함")
	}
}
