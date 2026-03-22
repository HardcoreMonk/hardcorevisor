// 인메모리 주변기기 드라이버 — 개발/테스트 전용
//
// Mock 디바이스 인벤토리를 제공하며, 실제 하드웨어 변경 없이
// Attach/Detach 상태만 인메모리에서 관리한다.
// SysfsDriver의 기반 드라이버로도 임베딩되어 사용된다.
package peripheral

import (
	"fmt"
	"sync"
)

// MemoryDriver 는 인메모리 주변기기 드라이버로, 개발/테스트 환경에서 사용한다.
// PeripheralDriver 인터페이스를 구현하며, 외부 의존성이 없다.
// 기본 Mock 디바이스: GPU 2개 (NVIDIA A100), NIC 1개 (ConnectX-6), USB 1개 (YubiKey)
type MemoryDriver struct {
	mu      sync.RWMutex
	devices map[string]*Device
}

// NewMemoryDriver 는 Mock 디바이스 인벤토리가 포함된 MemoryDriver를 생성한다.
//
// 기본 디바이스:
//   - gpu-0, gpu-1: NVIDIA A100 80GB (vfio-pci, IOMMU group-12/13)
//   - nic-0: Mellanox ConnectX-6 100GbE (mlx5_core, IOMMU group-5)
//   - usb-0: YubiKey 5 NFC (USB, IOMMU 없음)
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

// Name 은 드라이버 이름 "memory"를 반환한다.
func (d *MemoryDriver) Name() string { return "memory" }

// ListDevices 는 인메모리 디바이스 목록을 반환한다. typeFilter가 빈 문자열이면 전체 반환.
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

// GetDevice 는 ID로 디바이스를 조회한다. 미존재 시 에러 반환.
func (d *MemoryDriver) GetDevice(id string) (*Device, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	dev, ok := d.devices[id]
	if !ok {
		return nil, fmt.Errorf("device not found: %s", id)
	}
	return dev, nil
}

// AttachDevice 는 인메모리에서 디바이스를 VM에 연결한다.
// 이미 연결된 디바이스에 대해서는 에러를 반환한다.
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

// DetachDevice 는 인메모리에서 VM으로부터 디바이스를 분리한다.
// 이미 분리된 디바이스에 대해서는 에러를 반환한다.
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
