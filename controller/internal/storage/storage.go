// Package storage — Storage Agent managing ZFS/Ceph/PBS pools
//
// In-memory implementation for dev/test. Manages storage pools,
// volumes, and snapshots with concurrent-safe operations.
package storage

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ── Types ────────────────────────────────────────────

// Pool represents a storage pool (ZFS, Ceph, LVM, etc.)
type Pool struct {
	Name       string `json:"name"`
	PoolType   string `json:"pool_type"` // zfs, ceph, lvm, dir
	TotalBytes uint64 `json:"total_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
	Health     string `json:"health"` // healthy, degraded, faulted
}

// Volume represents a storage volume within a pool
type Volume struct {
	ID        string `json:"id"`
	Pool      string `json:"pool"`
	Name      string `json:"name"`
	SizeBytes uint64 `json:"size_bytes"`
	Format    string `json:"format"` // qcow2, raw, zvol
	Path      string `json:"path"`
	CreatedAt int64  `json:"created_at"`
}

// Snapshot represents a point-in-time copy of a volume
type Snapshot struct {
	ID        string `json:"id"`
	VolumeID  string `json:"volume_id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

// ── Service ──────────────────────────────────────────

// Service manages storage pools, volumes, and snapshots.
type Service struct {
	mu        sync.RWMutex
	pools     map[string]*Pool
	volumes   map[string]*Volume
	snapshots map[string]*Snapshot
	nextVolID atomic.Int32
	nextSnapID atomic.Int32
}

// NewService creates a storage service with default pools.
func NewService() *Service {
	s := &Service{
		pools:     make(map[string]*Pool),
		volumes:   make(map[string]*Volume),
		snapshots: make(map[string]*Snapshot),
	}
	s.nextVolID.Store(1)
	s.nextSnapID.Store(1)

	// Default pools
	s.pools["local-zfs"] = &Pool{
		Name: "local-zfs", PoolType: "zfs",
		TotalBytes: 3435973836800, UsedBytes: 2302160486400, // 3.2TiB / 2.1TiB
		Health: "healthy",
	}
	s.pools["ceph-pool"] = &Pool{
		Name: "ceph-pool", PoolType: "ceph",
		TotalBytes: 10995116277760, UsedBytes: 4398046511104, // 10TiB / 4TiB
		Health: "healthy",
	}
	return s
}

// ListPools returns all storage pools.
func (s *Service) ListPools() []*Pool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Pool, 0, len(s.pools))
	for _, p := range s.pools {
		result = append(result, p)
	}
	return result
}

// GetPool returns a pool by name.
func (s *Service) GetPool(name string) (*Pool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.pools[name]
	if !ok {
		return nil, fmt.Errorf("pool not found: %s", name)
	}
	return p, nil
}

// CreateVolume creates a new volume in the specified pool.
func (s *Service) CreateVolume(pool, name, format string, sizeBytes uint64) (*Volume, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pools[pool]; !ok {
		return nil, fmt.Errorf("pool not found: %s", pool)
	}
	id := fmt.Sprintf("vol-%d", s.nextVolID.Add(1)-1)
	vol := &Volume{
		ID: id, Pool: pool, Name: name,
		SizeBytes: sizeBytes, Format: format,
		Path:      fmt.Sprintf("/dev/%s/%s", pool, name),
		CreatedAt: time.Now().Unix(),
	}
	s.volumes[id] = vol
	s.pools[pool].UsedBytes += sizeBytes
	return vol, nil
}

// ListVolumes returns all volumes, optionally filtered by pool.
func (s *Service) ListVolumes(pool string) []*Volume {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Volume, 0)
	for _, v := range s.volumes {
		if pool == "" || v.Pool == pool {
			result = append(result, v)
		}
	}
	return result
}

// DeleteVolume removes a volume by ID.
func (s *Service) DeleteVolume(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	vol, ok := s.volumes[id]
	if !ok {
		return fmt.Errorf("volume not found: %s", id)
	}
	if p, ok := s.pools[vol.Pool]; ok {
		if p.UsedBytes >= vol.SizeBytes {
			p.UsedBytes -= vol.SizeBytes
		}
	}
	delete(s.volumes, id)
	return nil
}

// CreateSnapshot creates a snapshot of a volume.
func (s *Service) CreateSnapshot(volumeID, name string) (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.volumes[volumeID]; !ok {
		return nil, fmt.Errorf("volume not found: %s", volumeID)
	}
	id := fmt.Sprintf("snap-%d", s.nextSnapID.Add(1)-1)
	snap := &Snapshot{
		ID: id, VolumeID: volumeID, Name: name,
		CreatedAt: time.Now().Unix(),
	}
	s.snapshots[id] = snap
	return snap, nil
}

// ListSnapshots returns snapshots, optionally filtered by volume.
func (s *Service) ListSnapshots(volumeID string) []*Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Snapshot, 0)
	for _, snap := range s.snapshots {
		if volumeID == "" || snap.VolumeID == volumeID {
			result = append(result, snap)
		}
	}
	return result
}
