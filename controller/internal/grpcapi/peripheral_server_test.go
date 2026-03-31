package grpcapi

import (
	"context"
	"testing"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/peripheral"
	pb "github.com/HardcoreMonk/hardcorevisor/controller/pkg/proto/peripheralpb"
)

// newTestPeripheralServer — 테스트용 PeripheralServer를 생성한다.
// 인메모리 드라이버를 사용하는 peripheral.NewService()를 사용한다.
func newTestPeripheralServer(t *testing.T) *PeripheralServer {
	t.Helper()
	svc := peripheral.NewService()
	return NewPeripheralServer(svc)
}

// TestPeripheralServer_ListDevices — ListDevices가 기본 디바이스를 1개 이상 반환하는지 검증한다.
func TestPeripheralServer_ListDevices(t *testing.T) {
	server := newTestPeripheralServer(t)
	ctx := context.Background()

	resp, err := server.ListDevices(ctx, &pb.ListDevicesRequest{})
	if err != nil {
		t.Fatalf("ListDevices 실패: %v", err)
	}

	if len(resp.Devices) < 1 {
		t.Errorf("기본 디바이스가 1개 이상이어야 한다: got=%d", len(resp.Devices))
	}
}

// TestPeripheralServer_AttachDevice — 디바이스를 VM에 연결하고 성공 응답을 검증한다.
func TestPeripheralServer_AttachDevice(t *testing.T) {
	server := newTestPeripheralServer(t)
	ctx := context.Background()

	resp, err := server.AttachDevice(ctx, &pb.AttachDeviceRequest{
		DeviceId: "gpu-0",
		VmHandle: 1,
	})
	if err != nil {
		t.Fatalf("AttachDevice 실패: %v", err)
	}

	if !resp.Success {
		t.Error("AttachDevice 응답의 Success가 true여야 한다")
	}
}

// TestPeripheralServer_DetachDevice — 디바이스 연결 후 분리가 성공하는지 검증한다.
func TestPeripheralServer_DetachDevice(t *testing.T) {
	server := newTestPeripheralServer(t)
	ctx := context.Background()

	// 먼저 연결
	_, err := server.AttachDevice(ctx, &pb.AttachDeviceRequest{
		DeviceId: "gpu-1",
		VmHandle: 2,
	})
	if err != nil {
		t.Fatalf("AttachDevice 실패: %v", err)
	}

	// 분리
	resp, err := server.DetachDevice(ctx, &pb.DetachDeviceRequest{
		DeviceId: "gpu-1",
	})
	if err != nil {
		t.Fatalf("DetachDevice 실패: %v", err)
	}

	if !resp.Success {
		t.Error("DetachDevice 응답의 Success가 true여야 한다")
	}
}
