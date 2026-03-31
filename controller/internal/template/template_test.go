// template 패키지 유닛 테스트
//
// 테스트 대상: NewService, List, Get, Create, Delete
package template

import (
	"testing"
)

// TestNewService — 기본 템플릿 3개가 포함된 서비스 생성 검증
func TestNewService(t *testing.T) {
	t.Parallel()
	svc := NewService()

	if svc == nil {
		t.Fatal("NewService()가 nil을 반환함")
	}

	templates := svc.List()
	if len(templates) != 3 {
		t.Fatalf("기본 템플릿 수: got %d, want 3", len(templates))
	}
}

// TestListTemplates — 기본 템플릿 목록에 필수 항목이 포함되어 있는지 검증
func TestListTemplates(t *testing.T) {
	t.Parallel()
	svc := NewService()

	templates := svc.List()
	names := make(map[string]bool)
	for _, tpl := range templates {
		names[tpl.Name] = true
	}

	expected := []string{"linux-small", "linux-medium", "windows-standard"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("기본 템플릿 %q가 목록에 없음", name)
		}
	}
}

// TestGetTemplate — ID로 템플릿 조회 검증
func TestGetTemplate(t *testing.T) {
	t.Parallel()
	svc := NewService()

	tpl, err := svc.Get("tpl-1")
	if err != nil {
		t.Fatalf("Get() 에러: %v", err)
	}
	if tpl.Name != "linux-small" {
		t.Errorf("Name: got %q, want %q", tpl.Name, "linux-small")
	}
	if tpl.VCPUs != 1 {
		t.Errorf("VCPUs: got %d, want 1", tpl.VCPUs)
	}
	if tpl.Backend != "rustvmm" {
		t.Errorf("Backend: got %q, want %q", tpl.Backend, "rustvmm")
	}
}

// TestGetTemplateNotFound — 존재하지 않는 템플릿 조회 시 에러 반환
func TestGetTemplateNotFound(t *testing.T) {
	t.Parallel()
	svc := NewService()

	_, err := svc.Get("nonexistent")
	if err == nil {
		t.Fatal("존재하지 않는 템플릿에 대해 에러가 반환되어야 함")
	}
}

// TestCreateTemplate — 새 템플릿 생성 검증
func TestCreateTemplate(t *testing.T) {
	t.Parallel()
	svc := NewService()

	tpl, err := svc.Create("custom-gpu", "GPU 워크로드 템플릿", 8, 32768, 200, "qemu", "linux")
	if err != nil {
		t.Fatalf("Create() 에러: %v", err)
	}
	if tpl.ID == "" {
		t.Error("템플릿 ID가 비어 있음")
	}
	if tpl.Name != "custom-gpu" {
		t.Errorf("Name: got %q, want %q", tpl.Name, "custom-gpu")
	}
	if tpl.VCPUs != 8 {
		t.Errorf("VCPUs: got %d, want 8", tpl.VCPUs)
	}
	if tpl.MemoryMB != 32768 {
		t.Errorf("MemoryMB: got %d, want 32768", tpl.MemoryMB)
	}

	// 목록에 추가되었는지 확인
	all := svc.List()
	if len(all) != 4 {
		t.Errorf("생성 후 목록 길이: got %d, want 4", len(all))
	}
}

// TestCreateTemplateEmptyName — 이름 없이 템플릿 생성 시 에러 반환
func TestCreateTemplateEmptyName(t *testing.T) {
	t.Parallel()
	svc := NewService()

	_, err := svc.Create("", "설명", 1, 1024, 10, "rustvmm", "linux")
	if err == nil {
		t.Fatal("빈 이름으로 생성 시 에러가 반환되어야 함")
	}
}

// TestDeleteTemplate — 템플릿 삭제 검증
func TestDeleteTemplate(t *testing.T) {
	t.Parallel()
	svc := NewService()

	if err := svc.Delete("tpl-1"); err != nil {
		t.Fatalf("Delete() 에러: %v", err)
	}

	all := svc.List()
	if len(all) != 2 {
		t.Errorf("삭제 후 목록 길이: got %d, want 2", len(all))
	}

	_, err := svc.Get("tpl-1")
	if err == nil {
		t.Fatal("삭제된 템플릿이 여전히 조회됨")
	}
}

// TestDeleteTemplateNotFound — 존재하지 않는 템플릿 삭제 시 에러 반환
func TestDeleteTemplateNotFound(t *testing.T) {
	t.Parallel()
	svc := NewService()

	err := svc.Delete("nonexistent")
	if err == nil {
		t.Fatal("존재하지 않는 템플릿 삭제 시 에러가 반환되어야 함")
	}
}
