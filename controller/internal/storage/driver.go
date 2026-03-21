// Package storage — pluggable storage backend driver interface
package storage

// StorageDriver is the interface for pluggable storage backends.
type StorageDriver interface {
	Name() string
	ListPools() ([]*Pool, error)
	CreateVolume(pool, name, format string, sizeBytes uint64) (*Volume, error)
	DeleteVolume(id string) error
	CreateSnapshot(volumeID, name string) (*Snapshot, error)
	ListSnapshots(volumeID string) ([]*Snapshot, error)
}
