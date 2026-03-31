// image 패키지 유닛 테스트
//
// 테스트 대상: NewService, Register, List, Get, Delete
package image

import (
	"testing"
)

// TestNewService — 기본 이미지 3개가 포함된 서비스 생성 검증
func TestNewService(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir())

	if svc == nil {
		t.Fatal("NewService()가 nil을 반환함")
	}

	images := svc.List()
	if len(images) != 3 {
		t.Fatalf("기본 이미지 수: got %d, want 3", len(images))
	}
}

// TestListImages — 기본 이미지 목록에 필수 항목이 포함되어 있는지 검증
func TestListImages(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir())

	images := svc.List()
	names := make(map[string]bool)
	for _, img := range images {
		names[img.Name] = true
	}

	expected := []string{"ubuntu-24.04", "debian-12", "windows-server-2025"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("기본 이미지 %q가 목록에 없음", name)
		}
	}
}

// TestRegisterImage — 새 이미지 등록 검증
func TestRegisterImage(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir())

	img, err := svc.Register("fedora-41", "qcow2", "/images/fedora-41.qcow2", "linux")
	if err != nil {
		t.Fatalf("Register() 에러: %v", err)
	}
	if img.ID == "" {
		t.Error("이미지 ID가 비어 있음")
	}
	if img.Name != "fedora-41" {
		t.Errorf("Name: got %q, want %q", img.Name, "fedora-41")
	}
	if img.Format != "qcow2" {
		t.Errorf("Format: got %q, want %q", img.Format, "qcow2")
	}
	if img.OSType != "linux" {
		t.Errorf("OSType: got %q, want %q", img.OSType, "linux")
	}

	// 목록에 추가되었는지 확인
	all := svc.List()
	if len(all) != 4 {
		t.Errorf("등록 후 목록 길이: got %d, want 4", len(all))
	}
}

// TestRegisterImageEmptyName — 이름 없이 등록 시 에러 반환
func TestRegisterImageEmptyName(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir())

	_, err := svc.Register("", "qcow2", "/images/test.qcow2", "linux")
	if err == nil {
		t.Fatal("빈 이름으로 등록 시 에러가 반환되어야 함")
	}
}

// TestRegisterImageEmptyFormat — 포맷 없이 등록 시 에러 반환
func TestRegisterImageEmptyFormat(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir())

	_, err := svc.Register("test", "", "/images/test.img", "linux")
	if err == nil {
		t.Fatal("빈 포맷으로 등록 시 에러가 반환되어야 함")
	}
}

// TestGetImage — ID로 이미지 조회 검증
func TestGetImage(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir())

	img, err := svc.Get("img-1")
	if err != nil {
		t.Fatalf("Get() 에러: %v", err)
	}
	if img.Name != "ubuntu-24.04" {
		t.Errorf("Name: got %q, want %q", img.Name, "ubuntu-24.04")
	}
}

// TestGetImageNotFound — 존재하지 않는 이미지 조회 시 에러 반환
func TestGetImageNotFound(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir())

	_, err := svc.Get("nonexistent")
	if err == nil {
		t.Fatal("존재하지 않는 이미지에 대해 에러가 반환되어야 함")
	}
}

// TestDeleteImage — 이미지 삭제 후 목록에서 제거되는지 검증
func TestDeleteImage(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir())

	if err := svc.Delete("img-1"); err != nil {
		t.Fatalf("Delete() 에러: %v", err)
	}

	all := svc.List()
	if len(all) != 2 {
		t.Errorf("삭제 후 목록 길이: got %d, want 2", len(all))
	}

	_, err := svc.Get("img-1")
	if err == nil {
		t.Fatal("삭제된 이미지가 여전히 조회됨")
	}
}

// TestDeleteImageNotFound — 존재하지 않는 이미지 삭제 시 에러 반환
func TestDeleteImageNotFound(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir())

	err := svc.Delete("nonexistent")
	if err == nil {
		t.Fatal("존재하지 않는 이미지 삭제 시 에러가 반환되어야 함")
	}
}
