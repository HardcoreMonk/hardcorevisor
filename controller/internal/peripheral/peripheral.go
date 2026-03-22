// Package peripheral — 주변기기(GPU/NIC/USB) 패스스루 관리 서비스
//
// 아키텍처 위치: Go Controller → Peripheral Service → PeripheralDriver
//
// 플러그어블 드라이버 패턴을 사용하여 다양한 디바이스 백엔드를 지원한다:
//   - MemoryDriver: 인메모리 (개발/테스트용, Mock 디바이스 제공)
//   - SysfsDriver: /sys/bus/pci/devices 스캔 (IOMMU 그룹 기반 실제 PCI 디바이스 탐색)
//
// 핵심 개념:
//   - Device: 호스트의 PCI/USB 주변기기 (GPU, NIC, USB, Disk)
//   - Attach: VM에 디바이스를 연결 (VFIO 바인딩 수행)
//   - Detach: VM에서 디바이스를 분리 (원래 드라이버 복원)
//   - IOMMU Group: 패스스루 가능한 디바이스 그룹 (동일 그룹은 함께 전달)
//
// 환경변수:
//   - HCV_PERIPHERAL_DRIVER: 드라이버 선택 ("memory", "sysfs"). 기본값: "memory"
//
// 스레드 안전성: 드라이버 내부에서 sync.RWMutex로 보호됨
package peripheral

// ── 타입 정의 ────────────────────────────────────────

// DeviceType 은 주변기기의 종류를 분류한다.
type DeviceType string

const (
	DeviceGPU  DeviceType = "gpu"
	DeviceNIC  DeviceType = "nic"
	DeviceUSB  DeviceType = "usb"
	DeviceDisk DeviceType = "disk"
)

// Device 는 패스스루 가능한 호스트 주변기기를 나타낸다.
// AttachedVM이 0이면 미연결 상태, 양수이면 해당 VM에 연결된 상태이다.
type Device struct {
	ID          string     `json:"id"`
	DeviceType  DeviceType `json:"device_type"`
	Description string     `json:"description"`
	PCIAddress  string     `json:"pci_address"`
	AttachedVM  int32      `json:"attached_vm"` // 0 = not attached
	IOMMU       string     `json:"iommu_group"`
	Driver      string     `json:"driver"` // vfio-pci, nvidia, etc.
}

// ── 서비스 ──────────────────────────────────────────

// Service 는 PCI 디바이스 패스스루 작업을 관리하는 서비스이다.
// 실제 백엔드 작업은 PeripheralDriver 인터페이스에 위임한다.
// 동시 호출 안전성: 드라이버 내부에서 보호
type Service struct {
	driver PeripheralDriver
}

// NewService 는 기본 인메모리 드라이버로 주변기기 서비스를 생성한다.
//
// 호출 시점: 개발/테스트 환경에서 사용
func NewService() *Service {
	return NewServiceWithDriver(NewMemoryDriver())
}

// NewServiceWithDriver 는 지정된 드라이버로 주변기기 서비스를 생성한다.
//
// 호출 시점: Controller 초기화 시 설정에 따라 적절한 드라이버를 주입
func NewServiceWithDriver(driver PeripheralDriver) *Service {
	return &Service{driver: driver}
}

// DriverName 은 현재 사용 중인 주변기기 드라이버의 이름을 반환한다.
//
// 반환값 예시: "memory", "sysfs"
func (s *Service) DriverName() string {
	return s.driver.Name()
}

// ListDevices 는 모든 디바이스를 반환하며, typeFilter로 종류별 필터링이 가능하다.
//
// 호출 시점: REST GET /api/v1/devices?type=gpu, gRPC ListDevices
// 동시 호출 안전성: 안전
func (s *Service) ListDevices(typeFilter DeviceType) []*Device {
	devices, _ := s.driver.ListDevices(typeFilter)
	return devices
}

// GetDevice 는 ID로 디바이스를 조회한다. 미존재 시 에러 반환.
func (s *Service) GetDevice(id string) (*Device, error) {
	return s.driver.GetDevice(id)
}

// AttachDevice 는 디바이스를 VM에 연결한다.
//
// 호출 시점: REST POST /api/v1/devices/{id}/attach
// 부작용: SysfsDriver인 경우 VFIO 바인딩 수행 (파일 시스템 변경)
// 에러 조건: 디바이스 미존재, 이미 다른 VM에 연결됨
func (s *Service) AttachDevice(deviceID string, vmHandle int32) error {
	return s.driver.AttachDevice(deviceID, vmHandle)
}

// DetachDevice 는 VM에서 디바이스를 분리한다.
//
// 호출 시점: REST POST /api/v1/devices/{id}/detach
// 에러 조건: 디바이스 미존재, 이미 분리된 상태
func (s *Service) DetachDevice(deviceID string) error {
	return s.driver.DetachDevice(deviceID)
}
