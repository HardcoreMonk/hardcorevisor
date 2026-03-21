// Package peripheral — pluggable peripheral backend driver interface
package peripheral

// PeripheralDriver is the interface for pluggable peripheral backends.
type PeripheralDriver interface {
	Name() string
	ListDevices(typeFilter DeviceType) ([]*Device, error)
	GetDevice(id string) (*Device, error)
	AttachDevice(deviceID string, vmHandle int32) error
	DetachDevice(deviceID string) error
}
