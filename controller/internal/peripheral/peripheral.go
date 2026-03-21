// Package peripheral — Peripheral Manager for GPU/NIC/USB passthrough
//
// Pluggable driver architecture: MemoryDriver (dev/test) and
// SysfsDriver (sysfs PCI discovery for IOMMU-capable devices).
// Default is in-memory for dev/test.
package peripheral

// ── Types ────────────────────────────────────────────

// DeviceType classifies the peripheral device
type DeviceType string

const (
	DeviceGPU  DeviceType = "gpu"
	DeviceNIC  DeviceType = "nic"
	DeviceUSB  DeviceType = "usb"
	DeviceDisk DeviceType = "disk"
)

// Device represents a host peripheral available for passthrough
type Device struct {
	ID          string     `json:"id"`
	DeviceType  DeviceType `json:"device_type"`
	Description string     `json:"description"`
	PCIAddress  string     `json:"pci_address"`
	AttachedVM  int32      `json:"attached_vm"` // 0 = not attached
	IOMMU       string     `json:"iommu_group"`
	Driver      string     `json:"driver"` // vfio-pci, nvidia, etc.
}

// ── Service ──────────────────────────────────────────

// Service manages PCI device passthrough operations.
// It delegates to a PeripheralDriver for the actual backend operations.
type Service struct {
	driver PeripheralDriver
}

// NewService creates a peripheral service with the default in-memory driver.
func NewService() *Service {
	return NewServiceWithDriver(NewMemoryDriver())
}

// NewServiceWithDriver creates a peripheral service with the given driver.
func NewServiceWithDriver(driver PeripheralDriver) *Service {
	return &Service{driver: driver}
}

// DriverName returns the name of the underlying peripheral driver.
func (s *Service) DriverName() string {
	return s.driver.Name()
}

// ListDevices returns all devices, optionally filtered by type.
func (s *Service) ListDevices(typeFilter DeviceType) []*Device {
	devices, _ := s.driver.ListDevices(typeFilter)
	return devices
}

// GetDevice returns a device by ID.
func (s *Service) GetDevice(id string) (*Device, error) {
	return s.driver.GetDevice(id)
}

// AttachDevice attaches a device to a VM.
func (s *Service) AttachDevice(deviceID string, vmHandle int32) error {
	return s.driver.AttachDevice(deviceID, vmHandle)
}

// DetachDevice detaches a device from a VM.
func (s *Service) DetachDevice(deviceID string) error {
	return s.driver.DetachDevice(deviceID)
}
