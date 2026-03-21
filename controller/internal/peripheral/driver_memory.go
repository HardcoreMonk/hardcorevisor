package peripheral

import (
	"fmt"
	"sync"
)

// MemoryDriver is an in-memory peripheral driver for dev/test.
type MemoryDriver struct {
	mu      sync.RWMutex
	devices map[string]*Device
}

// NewMemoryDriver creates a MemoryDriver with mock device inventory.
func NewMemoryDriver() *MemoryDriver {
	d := &MemoryDriver{
		devices: make(map[string]*Device),
	}

	d.devices["gpu-0"] = &Device{
		ID: "gpu-0", DeviceType: DeviceGPU,
		Description: "NVIDIA A100 80GB",
		PCIAddress: "0000:41:00.0", IOMMU: "group-12",
		Driver: "vfio-pci",
	}
	d.devices["gpu-1"] = &Device{
		ID: "gpu-1", DeviceType: DeviceGPU,
		Description: "NVIDIA A100 80GB",
		PCIAddress: "0000:42:00.0", IOMMU: "group-13",
		Driver: "vfio-pci",
	}
	d.devices["nic-0"] = &Device{
		ID: "nic-0", DeviceType: DeviceNIC,
		Description: "Mellanox ConnectX-6 100GbE",
		PCIAddress: "0000:18:00.0", IOMMU: "group-5",
		Driver: "mlx5_core",
	}
	d.devices["usb-0"] = &Device{
		ID: "usb-0", DeviceType: DeviceUSB,
		Description: "YubiKey 5 NFC",
		PCIAddress: "usb-1-2",
	}

	return d
}

func (d *MemoryDriver) Name() string { return "memory" }

func (d *MemoryDriver) ListDevices(typeFilter DeviceType) ([]*Device, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]*Device, 0, len(d.devices))
	for _, dev := range d.devices {
		if typeFilter == "" || dev.DeviceType == typeFilter {
			result = append(result, dev)
		}
	}
	return result, nil
}

func (d *MemoryDriver) GetDevice(id string) (*Device, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	dev, ok := d.devices[id]
	if !ok {
		return nil, fmt.Errorf("device not found: %s", id)
	}
	return dev, nil
}

func (d *MemoryDriver) AttachDevice(deviceID string, vmHandle int32) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	dev, ok := d.devices[deviceID]
	if !ok {
		return fmt.Errorf("device not found: %s", deviceID)
	}
	if dev.AttachedVM != 0 {
		return fmt.Errorf("device %s already attached to VM %d", deviceID, dev.AttachedVM)
	}
	dev.AttachedVM = vmHandle
	return nil
}

func (d *MemoryDriver) DetachDevice(deviceID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	dev, ok := d.devices[deviceID]
	if !ok {
		return fmt.Errorf("device not found: %s", deviceID)
	}
	if dev.AttachedVM == 0 {
		return fmt.Errorf("device %s is not attached", deviceID)
	}
	dev.AttachedVM = 0
	return nil
}
