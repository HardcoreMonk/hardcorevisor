// Package image — VM 디스크 이미지 레지스트리 서비스
//
// VM에서 사용할 디스크 이미지(qcow2, raw, iso)를 등록하고 관리한다.
// 이미지 메타데이터는 인메모리에 저장하며, 실제 파일은 storeDir 경로에 위치한다.
//
// 기본 이미지 3개:
//   - img-1: ubuntu-24.04.qcow2 (2GB, Linux)
//   - img-2: debian-12.qcow2 (1GB, Linux)
//   - img-3: win2025.iso (5GB, Windows)
//
// 지원 포맷: qcow2, raw, iso
// 스레드 안전성: sync.RWMutex로 보호됨
package image

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Image 는 등록된 VM 디스크 이미지를 나타낸다.
// Format: "qcow2" | "raw" | "iso"
// OSType: "linux" | "windows"
type Image struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Format    string `json:"format"`     // qcow2, raw, iso
	SizeBytes uint64 `json:"size_bytes"`
	Path      string `json:"path"`
	OSType    string `json:"os_type"` // linux, windows
	CreatedAt int64  `json:"created_at"`
}

// Service 는 이미지 레지스트리를 관리하는 서비스이다.
// storeDir은 이미지 파일의 기본 디렉터리 경로이다.
// 동시 호출 안전성: sync.RWMutex로 보호됨
type Service struct {
	mu       sync.RWMutex
	images   map[string]*Image
	nextID   atomic.Int32
	storeDir string // base directory for images
}

// NewService 는 기본 이미지 3개가 포함된 이미지 레지스트리 서비스를 생성한다.
// storeDir은 이미지 파일의 기본 디렉터리 경로이다.
//
// 호출 시점: Controller 초기화 시
func NewService(storeDir string) *Service {
	s := &Service{
		images:   make(map[string]*Image),
		storeDir: storeDir,
	}
	s.nextID.Store(4) // Start after default images

	// Default images (mock)
	s.images["img-1"] = &Image{
		ID: "img-1", Name: "ubuntu-24.04", Format: "qcow2",
		SizeBytes: 2147483648, Path: filepath.Join(storeDir, "ubuntu-24.04.qcow2"),
		OSType: "linux", CreatedAt: time.Now().Unix(),
	}
	s.images["img-2"] = &Image{
		ID: "img-2", Name: "debian-12", Format: "qcow2",
		SizeBytes: 1073741824, Path: filepath.Join(storeDir, "debian-12.qcow2"),
		OSType: "linux", CreatedAt: time.Now().Unix(),
	}
	s.images["img-3"] = &Image{
		ID: "img-3", Name: "windows-server-2025", Format: "iso",
		SizeBytes: 5368709120, Path: filepath.Join(storeDir, "win2025.iso"),
		OSType: "windows", CreatedAt: time.Now().Unix(),
	}
	return s
}

// List 는 등록된 모든 이미지를 반환한다.
//
// 호출 시점: REST GET /api/v1/images
func (s *Service) List() []*Image {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Image, 0, len(s.images))
	for _, img := range s.images {
		result = append(result, img)
	}
	return result
}

// Get 은 ID로 이미지를 조회한다. 미존재 시 에러 반환.
func (s *Service) Get(id string) (*Image, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	img, ok := s.images[id]
	if !ok {
		return nil, fmt.Errorf("image not found: %s", id)
	}
	return img, nil
}

// Register 는 새 이미지를 레지스트리에 등록한다.
// 파일이 존재하면 실제 크기를 읽어오고, 없으면 SizeBytes=0이다.
//
// 호출 시점: REST POST /api/v1/images
// 에러 조건: name 또는 format이 빈 문자열
func (s *Service) Register(name, format, path, osType string) (*Image, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if format == "" {
		return nil, fmt.Errorf("format is required")
	}

	// Check if file exists (best-effort)
	var size uint64
	if info, err := os.Stat(path); err == nil {
		size = uint64(info.Size())
	}

	id := fmt.Sprintf("img-%d", s.nextID.Add(1)-1)
	img := &Image{
		ID:        id,
		Name:      name,
		Format:    format,
		SizeBytes: size,
		Path:      path,
		OSType:    osType,
		CreatedAt: time.Now().Unix(),
	}

	s.mu.Lock()
	s.images[id] = img
	s.mu.Unlock()

	return img, nil
}

// Delete 는 ID로 이미지를 레지스트리에서 삭제한다.
// 주의: 메타데이터만 삭제하며, 실제 이미지 파일은 삭제하지 않는다.
func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.images[id]; !ok {
		return fmt.Errorf("image not found: %s", id)
	}
	delete(s.images, id)
	return nil
}
