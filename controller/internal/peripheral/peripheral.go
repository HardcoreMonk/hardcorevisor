// Package peripheral — Peripheral Manager for GPU/NIC/USB passthrough
//
// In-memory implementation for dev/test. Manages PCI device inventory,
// attach/detach operations, and SR-IOV virtual functions.
package peripheral

import (
	"fmt"
	"sync"
)

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
type Service struct {
	mu      sync.RWMutex
	devices map[string]*Device
}

// NewService creates a peripheral service with mock device inventory.
func NewService() *Service {
	s := &Service{
		devices: make(map[string]*Device),
	}

	// Mock device inventory
	s.devices["gpu-0"] = &Device{
		ID: "gpu-0", DeviceType: DeviceGPU,
		Description: "NVIDIA A100 80GB",
		PCIAddress: "0000:41:00.0", IOMMU: "group-12",
		Driver: "vfio-pci",
	}
	s.devices["gpu-1"] = &Device{
		ID: "gpu-1", DeviceType: DeviceGPU,
		Description: "NVIDIA A100 80GB",
		PCIAddress: "0000:42:00.0", IOMMU: "group-13",
		Driver: "vfio-pci",
	}
	s.devices["nic-0"] = &Device{
		ID: "nic-0", DeviceType: DeviceNIC,
		Description: "Mellanox ConnectX-6 100GbE",
		PCIAddress: "0000:18:00.0", IOMMU: "group-5",
		Driver: "mlx5_core",
	}
	s.devices["usb-0"] = &Device{
		ID: "usb-0", DeviceType: DeviceUSB,
		Description: "YubiKey 5 NFC",
		PCIAddress: "usb-1-2",
	}
	return s
}

// ListDevices returns all devices, optionally filtered by type.
func (s *Service) ListDevices(typeFilter DeviceType) []*Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Device, 0, len(s.devices))
	for _, d := range s.devices {
		if typeFilter == "" || d.DeviceType == typeFilter {
			result = append(result, d)
		}
	}
	return result
}

// GetDevice returns a device by ID.
func (s *Service) GetDevice(id string) (*Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.devices[id]
	if !ok {
		return nil, fmt.Errorf("device not found: %s", id)
	}
	return d, nil
}

// AttachDevice attaches a device to a VM.
func (s *Service) AttachDevice(deviceID string, vmHandle int32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[deviceID]
	if !ok {
		return fmt.Errorf("device not found: %s", deviceID)
	}
	if d.AttachedVM != 0 {
		return fmt.Errorf("device %s already attached to VM %d", deviceID, d.AttachedVM)
	}
	d.AttachedVM = vmHandle
	return nil
}

// DetachDevice detaches a device from a VM.
func (s *Service) DetachDevice(deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[deviceID]
	if !ok {
		return fmt.Errorf("device not found: %s", deviceID)
	}
	if d.AttachedVM == 0 {
		return fmt.Errorf("device %s is not attached", deviceID)
	}
	d.AttachedVM = 0
	return nil
}
