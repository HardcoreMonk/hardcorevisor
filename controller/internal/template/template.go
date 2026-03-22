// Package template — VM 템플릿 관리 서비스
//
// 재사용 가능한 VM 설정 프리셋을 관리한다.
// 템플릿에서 VM을 배포하면 지정된 vCPU, 메모리, 디스크, 백엔드 설정이 적용된다.
//
// 기본 템플릿 3개:
//   - linux-small: 1 vCPU, 1GB RAM, 10GB 디스크 (rustvmm)
//   - linux-medium: 2 vCPU, 4GB RAM, 50GB 디스크 (rustvmm)
//   - windows-standard: 4 vCPU, 8GB RAM, 100GB 디스크 (qemu)
//
// 스레드 안전성: sync.RWMutex로 보호됨
package template

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Template 은 재사용 가능한 VM 설정 프리셋을 나타낸다.
// Backend: VM 백엔드 ("rustvmm" 또는 "qemu")
// OSType: 운영체제 종류 ("linux" 또는 "windows")
type Template struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	VCPUs       uint32    `json:"vcpus"`
	MemoryMB    uint64    `json:"memory_mb"`
	DiskSizeGB  uint64    `json:"disk_size_gb"`
	Backend     string    `json:"backend"` // rustvmm, qemu
	OSType      string    `json:"os_type"` // linux, windows
	CreatedAt   time.Time `json:"created_at"`
}

// Service 는 VM 템플릿을 관리하는 서비스이다.
// 동시 호출 안전성: sync.RWMutex로 보호됨
type Service struct {
	mu        sync.RWMutex
	templates map[string]*Template
	nextID    atomic.Int32
}

// NewService 는 기본 템플릿 3개가 포함된 템플릿 서비스를 생성한다.
//
// 호출 시점: Controller 초기화 시
func NewService() *Service {
	s := &Service{templates: make(map[string]*Template)}
	s.nextID.Store(4) // Start after default templates

	// Default templates
	s.templates["tpl-1"] = &Template{
		ID: "tpl-1", Name: "linux-small",
		Description: "Small Linux VM (1 vCPU, 1GB RAM, 10GB disk)",
		VCPUs: 1, MemoryMB: 1024, DiskSizeGB: 10,
		Backend: "rustvmm", OSType: "linux",
		CreatedAt: time.Now(),
	}
	s.templates["tpl-2"] = &Template{
		ID: "tpl-2", Name: "linux-medium",
		Description: "Medium Linux VM (2 vCPU, 4GB RAM, 50GB disk)",
		VCPUs: 2, MemoryMB: 4096, DiskSizeGB: 50,
		Backend: "rustvmm", OSType: "linux",
		CreatedAt: time.Now(),
	}
	s.templates["tpl-3"] = &Template{
		ID: "tpl-3", Name: "windows-standard",
		Description: "Standard Windows VM (4 vCPU, 8GB RAM, 100GB disk)",
		VCPUs: 4, MemoryMB: 8192, DiskSizeGB: 100,
		Backend: "qemu", OSType: "windows",
		CreatedAt: time.Now(),
	}

	return s
}

// List 는 모든 템플릿을 반환한다.
//
// 호출 시점: REST GET /api/v1/templates
// 동시 호출 안전성: 안전 (RLock 사용)
func (s *Service) List() []*Template {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Template, 0, len(s.templates))
	for _, t := range s.templates {
		result = append(result, t)
	}
	return result
}

// Get 은 ID로 템플릿을 조회한다. 미존재 시 에러 반환.
//
// 호출 시점: REST GET /api/v1/templates/{id}
func (s *Service) Get(id string) (*Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.templates[id]
	if !ok {
		return nil, fmt.Errorf("template not found: %s", id)
	}
	return t, nil
}

// Create 는 새 템플릿을 생성한다. name은 필수 필드이다.
//
// 호출 시점: REST POST /api/v1/templates
// 동시 호출 안전성: 안전 (Lock 사용, ID는 atomic 카운터)
func (s *Service) Create(name, description string, vcpus uint32, memoryMB, diskGB uint64, backend, osType string) (*Template, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	id := fmt.Sprintf("tpl-%d", s.nextID.Add(1)-1)
	t := &Template{
		ID:          id,
		Name:        name,
		Description: description,
		VCPUs:       vcpus,
		MemoryMB:    memoryMB,
		DiskSizeGB:  diskGB,
		Backend:     backend,
		OSType:      osType,
		CreatedAt:   time.Now(),
	}
	s.mu.Lock()
	s.templates[id] = t
	s.mu.Unlock()
	return t, nil
}

// Delete 는 ID로 템플릿을 삭제한다. 미존재 시 에러 반환.
//
// 호출 시점: REST DELETE /api/v1/templates/{id}
func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.templates[id]; !ok {
		return fmt.Errorf("template not found: %s", id)
	}
	delete(s.templates, id)
	return nil
}
