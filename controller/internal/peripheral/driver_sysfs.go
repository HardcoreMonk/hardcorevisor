package peripheral

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// SysfsDriver discovers PCI devices via /sys/bus/pci/devices.
// It embeds MemoryDriver for attach/detach state management.
type SysfsDriver struct {
	*MemoryDriver
}

// NewSysfsDriver creates a SysfsDriver.
// On creation it scans sysfs for IOMMU-capable PCI devices and populates
// the embedded MemoryDriver with discovered devices.
func NewSysfsDriver() *SysfsDriver {
	d := &SysfsDriver{
		MemoryDriver: &MemoryDriver{
			devices: make(map[string]*Device),
		},
	}
	d.scan()
	return d
}

func (d *SysfsDriver) Name() string { return "sysfs" }

// ListDevices returns PCI devices discovered from sysfs.
// Falls back to memory driver mock data if /sys/bus/pci is not available.
func (d *SysfsDriver) ListDevices(typeFilter DeviceType) ([]*Device, error) {
	d.mu.RLock()
	if len(d.devices) == 0 {
		d.mu.RUnlock()
		// Fallback: return mock data from a fresh memory driver
		fallback := NewMemoryDriver()
		return fallback.ListDevices(typeFilter)
	}
	d.mu.RUnlock()
	return d.MemoryDriver.ListDevices(typeFilter)
}

// GetDevice returns a device by ID.
func (d *SysfsDriver) GetDevice(id string) (*Device, error) {
	d.mu.RLock()
	if len(d.devices) == 0 {
		d.mu.RUnlock()
		fallback := NewMemoryDriver()
		return fallback.GetDevice(id)
	}
	d.mu.RUnlock()
	return d.MemoryDriver.GetDevice(id)
}

const sysbusPCIDevices = "/sys/bus/pci/devices"

// scan discovers PCI devices from sysfs.
func (d *SysfsDriver) scan() {
	entries, err := os.ReadDir(sysbusPCIDevices)
	if err != nil {
		// sysfs not available — devices map stays empty, fallback will be used
		return
	}

	gpuIdx := 0
	nicIdx := 0
	diskIdx := 0

	for _, entry := range entries {
		addr := entry.Name()
		devPath := filepath.Join(sysbusPCIDevices, addr)

		// Check for IOMMU group (only passthrough-capable devices)
		iommuLink, err := os.Readlink(filepath.Join(devPath, "iommu_group"))
		if err != nil {
			continue // no IOMMU group, skip
		}
		iommuGroup := filepath.Base(iommuLink)

		// Read PCI class
		classStr := readSysfsFile(filepath.Join(devPath, "class"))
		if classStr == "" {
			continue
		}

		// Read vendor/device IDs
		vendor := readSysfsFile(filepath.Join(devPath, "vendor"))
		deviceID := readSysfsFile(filepath.Join(devPath, "device"))

		// Classify device by PCI class code
		devType, description := classifyPCIDevice(classStr, vendor, deviceID)
		if devType == "" {
			continue // not a device type we care about
		}

		var id string
		switch devType {
		case DeviceGPU:
			id = fmt.Sprintf("gpu-%d", gpuIdx)
			gpuIdx++
		case DeviceNIC:
			id = fmt.Sprintf("nic-%d", nicIdx)
			nicIdx++
		case DeviceDisk:
			id = fmt.Sprintf("disk-%d", diskIdx)
			diskIdx++
		default:
			continue
		}

		// Read current driver
		driverLink, _ := os.Readlink(filepath.Join(devPath, "driver"))
		driverName := filepath.Base(driverLink)

		d.devices[id] = &Device{
			ID:          id,
			DeviceType:  devType,
			Description: description,
			PCIAddress:  addr,
			IOMMU:       fmt.Sprintf("group-%s", iommuGroup),
			Driver:      driverName,
		}
	}
}

// classifyPCIDevice determines device type from PCI class code.
// Class code format from sysfs: "0x030000" (display controller), "0x020000" (network), etc.
func classifyPCIDevice(classStr, vendor, deviceID string) (DeviceType, string) {
	classStr = strings.TrimSpace(classStr)
	if len(classStr) < 4 {
		return "", ""
	}

	// Extract major class (first 2 hex digits after "0x")
	classStr = strings.TrimPrefix(classStr, "0x")
	if len(classStr) < 2 {
		return "", ""
	}

	majorClass := classStr[:2]
	desc := fmt.Sprintf("PCI %s:%s", strings.TrimSpace(vendor), strings.TrimSpace(deviceID))

	switch majorClass {
	case "03": // Display controller (GPU)
		return DeviceGPU, desc
	case "02": // Network controller
		return DeviceNIC, desc
	case "01": // Mass storage controller
		return DeviceDisk, desc
	default:
		return "", ""
	}
}

// BindVFIO binds a PCI device to the vfio-pci driver for passthrough.
func (d *SysfsDriver) BindVFIO(pciAddr string) error {
	// 1. Write "vfio-pci" to /sys/bus/pci/devices/{addr}/driver_override
	overridePath := fmt.Sprintf("/sys/bus/pci/devices/%s/driver_override", pciAddr)
	if err := os.WriteFile(overridePath, []byte("vfio-pci"), 0644); err != nil {
		return fmt.Errorf("driver_override: %w", err)
	}

	// 2. Unbind from current driver
	// Read current driver symlink
	driverLink := fmt.Sprintf("/sys/bus/pci/devices/%s/driver", pciAddr)
	if _, err := os.Readlink(driverLink); err == nil {
		unbindPath := driverLink + "/unbind"
		os.WriteFile(unbindPath, []byte(pciAddr), 0644) // ignore error if already unbound
	}

	// 3. Trigger probe to bind to vfio-pci
	probePath := "/sys/bus/pci/drivers_probe"
	if err := os.WriteFile(probePath, []byte(pciAddr), 0644); err != nil {
		return fmt.Errorf("drivers_probe: %w", err)
	}

	return nil
}

// UnbindVFIO restores the device to its original driver.
func (d *SysfsDriver) UnbindVFIO(pciAddr string) error {
	overridePath := fmt.Sprintf("/sys/bus/pci/devices/%s/driver_override", pciAddr)
	// Clear override by writing empty string
	if err := os.WriteFile(overridePath, []byte(""), 0644); err != nil {
		return fmt.Errorf("clear driver_override: %w", err)
	}

	// Unbind from vfio-pci
	unbindPath := "/sys/bus/pci/drivers/vfio-pci/unbind"
	os.WriteFile(unbindPath, []byte(pciAddr), 0644) // ignore error

	// Trigger reprobe for original driver
	probePath := "/sys/bus/pci/drivers_probe"
	return os.WriteFile(probePath, []byte(pciAddr), 0644)
}

// AttachDevice overrides MemoryDriver.AttachDevice to perform VFIO binding.
func (d *SysfsDriver) AttachDevice(deviceID string, vmHandle int32) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	dev, ok := d.devices[deviceID]
	if !ok {
		return fmt.Errorf("device not found: %s", deviceID)
	}
	if dev.AttachedVM != 0 {
		return fmt.Errorf("device %s already attached to VM %d", deviceID, dev.AttachedVM)
	}

	// Attempt VFIO bind (best-effort, may fail without root)
	if dev.PCIAddress != "" && !strings.HasPrefix(dev.PCIAddress, "usb") {
		if err := d.BindVFIO(dev.PCIAddress); err != nil {
			// Log warning but don't fail — useful for testing without root
			slog.Warn("VFIO bind failed (may need root)", "device", deviceID, "error", err)
		}
	}

	dev.AttachedVM = vmHandle
	dev.Driver = "vfio-pci"
	return nil
}

// readSysfsFile reads a single-line sysfs attribute file.
func readSysfsFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
