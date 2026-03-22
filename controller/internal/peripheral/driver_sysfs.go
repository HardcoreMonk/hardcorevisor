// sysfs PCI 디바이스 탐색 드라이버 — /sys/bus/pci/devices 기반
//
// 시스템의 실제 PCI 디바이스를 sysfs에서 스캔하여 IOMMU 그룹이 있는
// (패스스루 가능한) 디바이스를 자동 탐색한다.
//
// 스캔 대상 PCI 클래스:
//   - 03: 디스플레이 컨트롤러 (GPU) → DeviceGPU
//   - 02: 네트워크 컨트롤러 (NIC) → DeviceNIC
//   - 01: 대용량 스토리지 컨트롤러 (Disk) → DeviceDisk
//
// VFIO 바인딩 프로세스 (AttachDevice):
//  1. driver_override에 "vfio-pci" 기록
//  2. 현재 드라이버에서 unbind
//  3. drivers_probe로 vfio-pci 바인딩 트리거
//
// 폴백: /sys/bus/pci가 없으면 MemoryDriver의 Mock 데이터 사용
// 주의: VFIO 바인딩에는 root 권한이 필요할 수 있다.
package peripheral

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// SysfsDriver 는 /sys/bus/pci/devices에서 PCI 디바이스를 탐색하는 드라이버이다.
// MemoryDriver를 임베딩하여 Attach/Detach 상태 관리를 위임받는다.
// sysfs를 사용할 수 없는 환경에서는 MemoryDriver의 Mock 데이터로 폴백한다.
type SysfsDriver struct {
	*MemoryDriver
}

// NewSysfsDriver 는 SysfsDriver를 생성하고, 즉시 sysfs PCI 스캔을 수행한다.
// IOMMU 그룹이 있는 PCI 디바이스를 탐색하여 내부 맵에 저장한다.
// sysfs 접근 불가 시 디바이스 맵은 비어 있으며, ListDevices 호출 시 Mock 데이터로 폴백한다.
//
// 호출 시점: 설정에서 드라이버가 "sysfs"일 때 Controller 초기화 시
func NewSysfsDriver() *SysfsDriver {
	d := &SysfsDriver{
		MemoryDriver: &MemoryDriver{
			devices: make(map[string]*Device),
		},
	}
	d.scan()
	return d
}

// Name 은 드라이버 이름 "sysfs"를 반환한다.
func (d *SysfsDriver) Name() string { return "sysfs" }

// ListDevices 는 sysfs에서 탐색된 PCI 디바이스를 반환한다.
// 탐색된 디바이스가 없으면 MemoryDriver의 Mock 데이터로 폴백한다.
// typeFilter가 빈 문자열이면 전체 반환.
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

// GetDevice 는 ID로 디바이스를 조회한다. 탐색된 디바이스가 없으면 Mock 데이터로 폴백.
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

// scan 은 /sys/bus/pci/devices 디렉터리를 읽어 PCI 디바이스를 탐색한다.
// IOMMU 그룹이 있는 디바이스만 대상이며, PCI 클래스 코드로 종류를 분류한다.
// sysfs 접근 불가 시 디바이스 맵은 비어 있는 채로 유지된다 (폴백으로 Mock 사용).
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

// classifyPCIDevice 는 PCI 클래스 코드로 디바이스 종류를 판별한다.
// sysfs 클래스 코드 형식: "0x030000" (디스플레이), "0x020000" (네트워크) 등.
// 상위 2자리 hex로 분류: 03=GPU, 02=NIC, 01=Disk
// 해당하지 않는 클래스는 빈 문자열을 반환한다 (무시됨).
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

// BindVFIO 는 PCI 디바이스를 vfio-pci 드라이버에 바인딩하여 패스스루를 준비한다.
//
// 처리 순서:
//  1. driver_override에 "vfio-pci" 기록
//  2. 현재 드라이버에서 unbind (이미 언바운드면 무시)
//  3. drivers_probe로 vfio-pci 바인딩 트리거
//
// 부작용: sysfs 파일 쓰기 (시스템 드라이버 변경)
// 주의: root 권한 필요
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

// UnbindVFIO 는 디바이스를 vfio-pci에서 해제하고 원래 드라이버로 복원한다.
//
// 처리 순서:
//  1. driver_override 초기화 (빈 문자열 기록)
//  2. vfio-pci에서 unbind
//  3. drivers_probe로 원래 드라이버 바인딩 트리거
//
// 부작용: sysfs 파일 쓰기 (시스템 드라이버 변경)
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

// AttachDevice 는 MemoryDriver.AttachDevice를 오버라이드하여 VFIO 바인딩도 수행한다.
// PCI 디바이스인 경우 (USB 제외) VFIO 바인딩을 시도하되,
// 실패해도 에러를 반환하지 않는다 (best-effort, root 권한 없이 테스트 가능).
// 부작용: 성공 시 디바이스 드라이버가 vfio-pci로 변경됨
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

// readSysfsFile 은 sysfs 속성 파일에서 한 줄을 읽어 반환한다.
// 파일 읽기 실패 시 빈 문자열을 반환한다.
func readSysfsFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
