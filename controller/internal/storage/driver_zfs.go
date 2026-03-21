package storage

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode"
)

// ZFSDriver implements StorageDriver using ZFS command-line tools.
type ZFSDriver struct{}

func (d *ZFSDriver) Name() string { return "zfs" }

func (d *ZFSDriver) ListPools() ([]*Pool, error) {
	// Run: zpool list -H -o name,size,alloc,health
	out, err := exec.Command("zpool", "list", "-H", "-o", "name,size,alloc,health").Output()
	if err != nil {
		return nil, fmt.Errorf("zpool list: %w", err)
	}
	// Parse tab-separated output
	var pools []*Pool
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pools = append(pools, &Pool{
			Name:       fields[0],
			PoolType:   "zfs",
			TotalBytes: parseSize(fields[1]),
			UsedBytes:  parseSize(fields[2]),
			Health:     strings.ToLower(fields[3]),
		})
	}
	return pools, nil
}

func (d *ZFSDriver) CreateVolume(pool, name, format string, sizeBytes uint64) (*Volume, error) {
	fullName := fmt.Sprintf("%s/%s", pool, name)
	sizeStr := fmt.Sprintf("%d", sizeBytes)

	// zfs create -V <size> <pool>/<name>
	cmd := exec.Command("zfs", "create", "-V", sizeStr, fullName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("zfs create %s: %s: %w", fullName, strings.TrimSpace(string(out)), err)
	}

	vol := &Volume{
		ID:        fullName,
		Pool:      pool,
		Name:      name,
		SizeBytes: sizeBytes,
		Format:    format,
		Path:      fmt.Sprintf("/dev/zvol/%s", fullName),
		CreatedAt: time.Now().Unix(),
	}
	return vol, nil
}

func (d *ZFSDriver) DeleteVolume(id string) error {
	// zfs destroy <pool>/<name>
	cmd := exec.Command("zfs", "destroy", id)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("zfs destroy %s: %s: %w", id, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (d *ZFSDriver) CreateSnapshot(volumeID, name string) (*Snapshot, error) {
	snapName := fmt.Sprintf("%s@%s", volumeID, name)

	// zfs snapshot <pool>/<name>@<snap>
	cmd := exec.Command("zfs", "snapshot", snapName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("zfs snapshot %s: %s: %w", snapName, strings.TrimSpace(string(out)), err)
	}

	snap := &Snapshot{
		ID:        snapName,
		VolumeID:  volumeID,
		Name:      name,
		CreatedAt: time.Now().Unix(),
	}
	return snap, nil
}

func (d *ZFSDriver) ListSnapshots(volumeID string) ([]*Snapshot, error) {
	// zfs list -t snapshot -H -o name,creation <pool>/<name>
	args := []string{"list", "-t", "snapshot", "-H", "-o", "name,creation"}
	if volumeID != "" {
		args = append(args, "-r", volumeID)
	}

	out, err := exec.Command("zfs", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("zfs list snapshots: %w", err)
	}

	var snapshots []*Snapshot
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) < 1 {
			continue
		}
		fullName := fields[0]
		// Extract snapshot name from pool/vol@snapname
		parts := strings.SplitN(fullName, "@", 2)
		if len(parts) != 2 {
			continue
		}
		snapshots = append(snapshots, &Snapshot{
			ID:        fullName,
			VolumeID:  parts[0],
			Name:      parts[1],
			CreatedAt: time.Now().Unix(),
		})
	}
	return snapshots, nil
}

// parseSize converts ZFS size strings (e.g., "1.5T", "500G", "100M") to bytes.
func parseSize(s string) uint64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}

	// Find where the numeric part ends and the suffix begins
	i := 0
	for i < len(s) && (unicode.IsDigit(rune(s[i])) || s[i] == '.') {
		i++
	}
	if i == 0 {
		return 0
	}

	numStr := s[:i]
	suffix := strings.ToUpper(strings.TrimSpace(s[i:]))

	var num float64
	if _, err := fmt.Sscanf(numStr, "%f", &num); err != nil {
		return 0
	}

	multiplier := uint64(1)
	switch suffix {
	case "K":
		multiplier = 1024
	case "M":
		multiplier = 1024 * 1024
	case "G":
		multiplier = 1024 * 1024 * 1024
	case "T":
		multiplier = 1024 * 1024 * 1024 * 1024
	case "P":
		multiplier = 1024 * 1024 * 1024 * 1024 * 1024
	}

	return uint64(num * float64(multiplier))
}
