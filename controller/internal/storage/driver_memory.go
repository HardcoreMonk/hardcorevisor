package storage

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// MemoryDriver is an in-memory storage driver for dev/test.
type MemoryDriver struct {
	mu         sync.RWMutex
	pools      map[string]*Pool
	volumes    map[string]*Volume
	snapshots  map[string]*Snapshot
	nextVolID  atomic.Int32
	nextSnapID atomic.Int32
}

// NewMemoryDriver creates a MemoryDriver with default pools.
func NewMemoryDriver() *MemoryDriver {
	d := &MemoryDriver{
		pools:     make(map[string]*Pool),
		volumes:   make(map[string]*Volume),
		snapshots: make(map[string]*Snapshot),
	}
	d.nextVolID.Store(1)
	d.nextSnapID.Store(1)

	// Default pools
	d.pools["local-zfs"] = &Pool{
		Name: "local-zfs", PoolType: "zfs",
		TotalBytes: 3435973836800, UsedBytes: 2302160486400, // 3.2TiB / 2.1TiB
		Health: "healthy",
	}
	d.pools["ceph-pool"] = &Pool{
		Name: "ceph-pool", PoolType: "ceph",
		TotalBytes: 10995116277760, UsedBytes: 4398046511104, // 10TiB / 4TiB
		Health: "healthy",
	}
	return d
}

func (d *MemoryDriver) Name() string { return "memory" }

func (d *MemoryDriver) ListPools() ([]*Pool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]*Pool, 0, len(d.pools))
	for _, p := range d.pools {
		result = append(result, p)
	}
	return result, nil
}

func (d *MemoryDriver) CreateVolume(pool, name, format string, sizeBytes uint64) (*Volume, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.pools[pool]; !ok {
		return nil, fmt.Errorf("pool not found: %s", pool)
	}
	id := fmt.Sprintf("vol-%d", d.nextVolID.Add(1)-1)
	vol := &Volume{
		ID: id, Pool: pool, Name: name,
		SizeBytes: sizeBytes, Format: format,
		Path:      fmt.Sprintf("/dev/%s/%s", pool, name),
		CreatedAt: time.Now().Unix(),
	}
	d.volumes[id] = vol
	d.pools[pool].UsedBytes += sizeBytes
	return vol, nil
}

func (d *MemoryDriver) DeleteVolume(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	vol, ok := d.volumes[id]
	if !ok {
		return fmt.Errorf("volume not found: %s", id)
	}
	if p, ok := d.pools[vol.Pool]; ok {
		if p.UsedBytes >= vol.SizeBytes {
			p.UsedBytes -= vol.SizeBytes
		}
	}
	delete(d.volumes, id)
	return nil
}

func (d *MemoryDriver) CreateSnapshot(volumeID, name string) (*Snapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.volumes[volumeID]; !ok {
		return nil, fmt.Errorf("volume not found: %s", volumeID)
	}
	id := fmt.Sprintf("snap-%d", d.nextSnapID.Add(1)-1)
	snap := &Snapshot{
		ID: id, VolumeID: volumeID, Name: name,
		CreatedAt: time.Now().Unix(),
	}
	d.snapshots[id] = snap
	return snap, nil
}

func (d *MemoryDriver) ListSnapshots(volumeID string) ([]*Snapshot, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]*Snapshot, 0)
	for _, snap := range d.snapshots {
		if volumeID == "" || snap.VolumeID == volumeID {
			result = append(result, snap)
		}
	}
	return result, nil
}
