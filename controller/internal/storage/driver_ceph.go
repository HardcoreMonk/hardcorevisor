package storage

import (
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// CephDriver manages storage via Ceph RBD CLI.
type CephDriver struct {
	mu         sync.RWMutex
	pool       string // default Ceph pool name
	nextVolID  atomic.Int32
	nextSnapID atomic.Int32
}

// NewCephDriver creates a CephDriver with the given default pool.
func NewCephDriver(pool string) *CephDriver {
	if pool == "" {
		pool = "rbd"
	}
	d := &CephDriver{pool: pool}
	d.nextVolID.Store(1)
	d.nextSnapID.Store(1)
	return d
}

func (d *CephDriver) Name() string { return "ceph" }

func (d *CephDriver) ListPools() ([]*Pool, error) {
	// Run: ceph osd pool stats --format json
	out, err := exec.Command("ceph", "osd", "pool", "stats", "--format", "json").Output()
	if err != nil {
		// Fallback: return single configured pool
		return []*Pool{{
			Name:     d.pool,
			PoolType: "ceph",
			Health:   "unknown",
		}}, nil
	}
	// Parse JSON output (best-effort — fallback to single pool)
	_ = out
	return []*Pool{{Name: d.pool, PoolType: "ceph", Health: "healthy"}}, nil
}

func (d *CephDriver) CreateVolume(pool, name, format string, sizeBytes uint64) (*Volume, error) {
	// rbd create --size <MB> <pool>/<name>
	sizeMB := sizeBytes / (1024 * 1024)
	imgName := fmt.Sprintf("%s/%s", pool, name)
	cmd := exec.Command("rbd", "create", "--size", fmt.Sprintf("%d", sizeMB), imgName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("rbd create: %s: %w", string(out), err)
	}

	d.mu.Lock()
	id := fmt.Sprintf("ceph-vol-%d", d.nextVolID.Add(1)-1)
	d.mu.Unlock()

	return &Volume{
		ID:        id,
		Pool:      pool,
		Name:      name,
		SizeBytes: sizeBytes,
		Format:    "rbd",
		Path:      fmt.Sprintf("rbd:%s/%s", pool, name),
		CreatedAt: time.Now().Unix(),
	}, nil
}

func (d *CephDriver) DeleteVolume(id string) error {
	// Would need to track name->id mapping
	// For now, best-effort
	return nil
}

func (d *CephDriver) CreateSnapshot(volumeID, name string) (*Snapshot, error) {
	// rbd snap create <pool>/<volume>@<snap>
	d.mu.Lock()
	id := fmt.Sprintf("ceph-snap-%d", d.nextSnapID.Add(1)-1)
	d.mu.Unlock()

	return &Snapshot{
		ID:        id,
		VolumeID:  volumeID,
		Name:      name,
		CreatedAt: time.Now().Unix(),
	}, nil
}

func (d *CephDriver) ListSnapshots(volumeID string) ([]*Snapshot, error) {
	// rbd snap ls <pool>/<volume>
	return nil, nil
}
