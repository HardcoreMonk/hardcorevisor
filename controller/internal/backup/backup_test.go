// backup 패키지 유닛 테스트
//
// 테스트 대상: NewService, CreateBackup, ListBackups, GetBackup, DeleteBackup
// 의존성: storage.Service (인메모리 드라이버)
package backup

import (
	"testing"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
)

// newTestService — 테스트용 백업 서비스를 생성한다 (인메모리 스토리지 기반).
func newTestService(t *testing.T) *Service {
	t.Helper()
	storageSvc := storage.NewService()
	return NewService(storageSvc)
}

// TestNewService — NewService가 nil이 아닌 서비스를 반환하는지 검증
func TestNewService(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	if svc == nil {
		t.Fatal("NewService()가 nil을 반환함")
	}
}

// TestCreateBackup — 백업 생성 후 필드가 올바른지 검증
func TestCreateBackup(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	backup, err := svc.CreateBackup(1, "web-01", "local-zfs")
	if err != nil {
		t.Fatalf("CreateBackup() 에러: %v", err)
	}
	if backup.ID == "" {
		t.Error("백업 ID가 비어 있음")
	}
	if backup.VMID != 1 {
		t.Errorf("VMID: got %d, want 1", backup.VMID)
	}
	if backup.VMName != "web-01" {
		t.Errorf("VMName: got %q, want %q", backup.VMName, "web-01")
	}
	if backup.Status != "completed" {
		t.Errorf("Status: got %q, want %q", backup.Status, "completed")
	}
	if backup.StoragePool != "local-zfs" {
		t.Errorf("StoragePool: got %q, want %q", backup.StoragePool, "local-zfs")
	}
	if backup.SnapshotID == "" {
		t.Error("SnapshotID가 비어 있음")
	}
}

// TestCreateBackupInvalidPool — 존재하지 않는 풀로 백업 생성 시 에러 반환
func TestCreateBackupInvalidPool(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	_, err := svc.CreateBackup(1, "vm-01", "nonexistent-pool")
	if err == nil {
		t.Fatal("존재하지 않는 풀에 대해 에러가 반환되어야 함")
	}
}

// TestListBackups — 백업 목록 조회 검증
func TestListBackups(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	// 초기 상태: 비어 있음
	if list := svc.ListBackups(); len(list) != 0 {
		t.Errorf("초기 목록 길이: got %d, want 0", len(list))
	}

	// 2개 생성 후 목록 확인
	if _, err := svc.CreateBackup(1, "vm-01", "local-zfs"); err != nil {
		t.Fatalf("CreateBackup() 에러: %v", err)
	}
	if _, err := svc.CreateBackup(2, "vm-02", "local-zfs"); err != nil {
		t.Fatalf("CreateBackup() 에러: %v", err)
	}

	list := svc.ListBackups()
	if len(list) != 2 {
		t.Errorf("목록 길이: got %d, want 2", len(list))
	}
}

// TestGetBackup — ID로 백업 조회 검증
func TestGetBackup(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	created, err := svc.CreateBackup(1, "vm-01", "local-zfs")
	if err != nil {
		t.Fatalf("CreateBackup() 에러: %v", err)
	}

	got, err := svc.GetBackup(created.ID)
	if err != nil {
		t.Fatalf("GetBackup() 에러: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID: got %q, want %q", got.ID, created.ID)
	}
}

// TestGetBackupNotFound — 존재하지 않는 백업 조회 시 에러 반환
func TestGetBackupNotFound(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	_, err := svc.GetBackup("nonexistent")
	if err == nil {
		t.Fatal("존재하지 않는 백업에 대해 에러가 반환되어야 함")
	}
}

// TestDeleteBackup — 백업 삭제 후 목록에서 제거되는지 검증
func TestDeleteBackup(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	created, err := svc.CreateBackup(1, "vm-01", "local-zfs")
	if err != nil {
		t.Fatalf("CreateBackup() 에러: %v", err)
	}

	if err := svc.DeleteBackup(created.ID); err != nil {
		t.Fatalf("DeleteBackup() 에러: %v", err)
	}

	if list := svc.ListBackups(); len(list) != 0 {
		t.Errorf("삭제 후 목록 길이: got %d, want 0", len(list))
	}
}

// TestDeleteBackupNotFound — 존재하지 않는 백업 삭제 시 에러 반환
func TestDeleteBackupNotFound(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	err := svc.DeleteBackup("nonexistent")
	if err == nil {
		t.Fatal("존재하지 않는 백업 삭제 시 에러가 반환되어야 함")
	}
}
