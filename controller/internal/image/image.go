// Package image — Image Registry for managing VM disk images (qcow2, raw, iso).
package image

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Image represents a registered VM disk image.
type Image struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Format    string `json:"format"`     // qcow2, raw, iso
	SizeBytes uint64 `json:"size_bytes"`
	Path      string `json:"path"`
	OSType    string `json:"os_type"` // linux, windows
	CreatedAt int64  `json:"created_at"`
}

// Service manages the image registry.
type Service struct {
	mu       sync.RWMutex
	images   map[string]*Image
	nextID   atomic.Int32
	storeDir string // base directory for images
}

// NewService creates a new image registry service.
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

// List returns all registered images.
func (s *Service) List() []*Image {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Image, 0, len(s.images))
	for _, img := range s.images {
		result = append(result, img)
	}
	return result
}

// Get returns an image by ID.
func (s *Service) Get(id string) (*Image, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	img, ok := s.images[id]
	if !ok {
		return nil, fmt.Errorf("image not found: %s", id)
	}
	return img, nil
}

// Register adds a new image to the registry.
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

// Delete removes an image from the registry by ID.
func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.images[id]; !ok {
		return fmt.Errorf("image not found: %s", id)
	}
	delete(s.images, id)
	return nil
}
