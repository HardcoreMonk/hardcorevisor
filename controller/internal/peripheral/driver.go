// Package peripheral — 플러그어블 주변기기 백엔드 드라이버 인터페이스
package peripheral

// PeripheralDriver 는 플러그어블 주변기기 백엔드를 위한 인터페이스이다.
//
// 구현체:
//   - MemoryDriver: 인메모리 Mock (개발/테스트용, 시스템 변경 없음)
//   - SysfsDriver: /sys/bus/pci 기반 (실제 PCI 디바이스 탐색 + VFIO 바인딩)
//
// 구현 시 주의사항:
//   - 모든 메서드는 동시 호출에 안전해야 한다 (thread-safe)
//   - AttachDevice/DetachDevice는 상태 변경 메서드 — 동일 디바이스의 동시 호출 방지 필요
type PeripheralDriver interface {
	// Name 은 드라이버 이름을 반환한다 (예: "memory", "sysfs").
	Name() string

	// ListDevices 는 디바이스 목록을 반환한다. typeFilter가 빈 문자열이면 전체 반환.
	// 멱등성: 읽기 전용, 부작용 없음
	ListDevices(typeFilter DeviceType) ([]*Device, error)

	// GetDevice 는 ID로 디바이스를 조회한다.
	// 에러 조건: 디바이스 미존재
	GetDevice(id string) (*Device, error)

	// AttachDevice 는 디바이스를 VM에 연결한다.
	// 멱등성: 아님 — 이미 연결된 디바이스에 대해 에러 반환
	// 부작용: SysfsDriver는 VFIO 바인딩 수행 (sysfs 파일 쓰기)
	// 에러 조건: 디바이스 미존재, 이미 연결됨
	AttachDevice(deviceID string, vmHandle int32) error

	// DetachDevice 는 VM에서 디바이스를 분리한다.
	// 멱등성: 아님 — 이미 분리된 디바이스에 대해 에러 반환
	// 에러 조건: 디바이스 미존재, 이미 분리됨
	DetachDevice(deviceID string) error
}
