package template

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Template defines a reusable VM configuration.
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

// Service manages VM templates.
type Service struct {
	mu        sync.RWMutex
	templates map[string]*Template
	nextID    atomic.Int32
}

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

// List returns all templates.
func (s *Service) List() []*Template {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Template, 0, len(s.templates))
	for _, t := range s.templates {
		result = append(result, t)
	}
	return result
}

// Get returns a template by ID.
func (s *Service) Get(id string) (*Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.templates[id]
	if !ok {
		return nil, fmt.Errorf("template not found: %s", id)
	}
	return t, nil
}

// Create creates a new template.
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

// Delete removes a template by ID.
func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.templates[id]; !ok {
		return fmt.Errorf("template not found: %s", id)
	}
	delete(s.templates, id)
	return nil
}
