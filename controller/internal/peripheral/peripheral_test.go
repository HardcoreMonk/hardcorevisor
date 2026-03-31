// peripheral 패키지 유닛 테스트
//
// 테스트 대상: NewService (인메모리 드라이버), ListDevices, AttachDevice, DetachDevice
package peripheral

import (
	"testing"
)

// TestNewService — 인메모리 드라이버로 서비스 생성 검증
func TestNewService(t *testing.T) {
	t.Parallel()
	svc := NewService()

	if svc == nil {
		t.Fatal("NewService()가 nil을 반환함")
	}
	if svc.DriverName() != "memory" {
		t.Errorf("드라이버 이름: got %q, want %q", svc.DriverName(), "memory")
	}
}

// TestListDevicesAll — 전체 디바이스 목록 조회 (Mock 4개)
func TestListDevicesAll(t *testing.T) {
	t.Parallel()
	svc := NewService()

	devices := svc.ListDevices("")
	if len(devices) != 4 {
		t.Errorf("전체 디바이스 수: got %d, want 4", len(devices))
	}
}

// TestListDevicesFilter — 타입별 디바이스 필터링 검증
func TestListDevicesFilter(t *testing.T) {
	t.Parallel()
	svc := NewService()

	tests := []struct {
		name     string
		filter   DeviceType
		wantLen  int
	}{
		{"GPU 필터", DeviceGPU, 2},
		{"NIC 필터", DeviceNIC, 1},
		{"USB 필터", DeviceUSB, 1},
		{"Disk 필터 (없음)", DeviceDisk, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			devices := svc.ListDevices(tc.filter)
			if len(devices) != tc.wantLen {
				t.Errorf("got %d, want %d", len(devices), tc.wantLen)
			}
		})
	}
}

// TestGetDevice — ID로 디바이스 조회 검증
func TestGetDevice(t *testing.T) {
	t.Parallel()
	svc := NewService()

	dev, err := svc.GetDevice("gpu-0")
	if err != nil {
		t.Fatalf("GetDevice() 에러: %v", err)
	}
	if dev.Description != "NVIDIA A100 80GB" {
		t.Errorf("Description: got %q, want %q", dev.Description, "NVIDIA A100 80GB")
	}
}

// TestGetDeviceNotFound — 존재하지 않는 디바이스 조회 시 에러 반환
func TestGetDeviceNotFound(t *testing.T) {
	t.Parallel()
	svc := NewService()

	_, err := svc.GetDevice("nonexistent")
	if err == nil {
		t.Fatal("존재하지 않는 디바이스에 대해 에러가 반환되어야 함")
	}
}

// TestAttachDevice — 디바이스를 VM에 연결 후 상태 검증
func TestAttachDevice(t *testing.T) {
	t.Parallel()
	svc := NewService()

	if err := svc.AttachDevice("gpu-0", 1); err != nil {
		t.Fatalf("AttachDevice() 에러: %v", err)
	}

	dev, err := svc.GetDevice("gpu-0")
	if err != nil {
		t.Fatalf("GetDevice() 에러: %v", err)
	}
	if dev.AttachedVM != 1 {
		t.Errorf("AttachedVM: got %d, want 1", dev.AttachedVM)
	}
}

// TestAttachDeviceAlreadyAttached — 이미 연결된 디바이스 재연결 시 에러 반환
func TestAttachDeviceAlreadyAttached(t *testing.T) {
	t.Parallel()
	svc := NewService()

	if err := svc.AttachDevice("gpu-0", 1); err != nil {
		t.Fatalf("AttachDevice() 에러: %v", err)
	}

	err := svc.AttachDevice("gpu-0", 2)
	if err == nil {
		t.Fatal("이미 연결된 디바이스에 대해 에러가 반환되어야 함")
	}
}

// TestAttachDeviceNotFound — 존재하지 않는 디바이스 연결 시 에러 반환
func TestAttachDeviceNotFound(t *testing.T) {
	t.Parallel()
	svc := NewService()

	err := svc.AttachDevice("nonexistent", 1)
	if err == nil {
		t.Fatal("존재하지 않는 디바이스 연결 시 에러가 반환되어야 함")
	}
}

// TestDetachDevice — VM에서 디바이스 분리 후 상태 검증
func TestDetachDevice(t *testing.T) {
	t.Parallel()
	svc := NewService()

	if err := svc.AttachDevice("gpu-0", 1); err != nil {
		t.Fatalf("AttachDevice() 에러: %v", err)
	}

	if err := svc.DetachDevice("gpu-0"); err != nil {
		t.Fatalf("DetachDevice() 에러: %v", err)
	}

	dev, err := svc.GetDevice("gpu-0")
	if err != nil {
		t.Fatalf("GetDevice() 에러: %v", err)
	}
	if dev.AttachedVM != 0 {
		t.Errorf("분리 후 AttachedVM: got %d, want 0", dev.AttachedVM)
	}
}

// TestDetachDeviceNotAttached — 이미 분리된 디바이스 분리 시 에러 반환
func TestDetachDeviceNotAttached(t *testing.T) {
	t.Parallel()
	svc := NewService()

	err := svc.DetachDevice("gpu-0")
	if err == nil {
		t.Fatal("이미 분리된 디바이스에 대해 에러가 반환되어야 함")
	}
}

// TestDetachDeviceNotFound — 존재하지 않는 디바이스 분리 시 에러 반환
func TestDetachDeviceNotFound(t *testing.T) {
	t.Parallel()
	svc := NewService()

	err := svc.DetachDevice("nonexistent")
	if err == nil {
		t.Fatal("존재하지 않는 디바이스 분리 시 에러가 반환되어야 함")
	}
}
