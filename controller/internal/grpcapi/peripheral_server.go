package grpcapi

import (
	"context"
	"fmt"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/peripheral"
	pb "github.com/HardcoreMonk/hardcorevisor/controller/pkg/proto/peripheralpb"
)

// PeripheralServer 는 PeripheralManager gRPC 서비스를 구현한다.
// 내부 Peripheral Service에 위임하여 디바이스 목록, 연결, 분리를 관리한다.
type PeripheralServer struct {
	pb.UnimplementedPeripheralManagerServer
	svc *peripheral.Service
}

// NewPeripheralServer 는 Peripheral 서비스를 기반으로 gRPC 서버를 생성한다.
func NewPeripheralServer(svc *peripheral.Service) *PeripheralServer {
	return &PeripheralServer{svc: svc}
}

// ListDevices 는 디바이스 목록을 반환한다. TypeFilter로 종류별 필터링 가능.
func (s *PeripheralServer) ListDevices(ctx context.Context, req *pb.ListDevicesRequest) (*pb.ListDevicesResponse, error) {
	typeFilter := protoToDeviceType(req.TypeFilter)
	devices := s.svc.ListDevices(typeFilter)
	result := make([]*pb.DeviceInfo, 0, len(devices))
	for _, d := range devices {
		result = append(result, &pb.DeviceInfo{
			Id:          d.ID,
			DeviceType:  deviceTypeToProto(d.DeviceType),
			Description: d.Description,
			PciAddress:  d.PCIAddress,
			AttachedVm:  d.AttachedVM,
		})
	}
	return &pb.ListDevicesResponse{Devices: result}, nil
}

// AttachDevice 는 디바이스를 VM에 연결한다.
func (s *PeripheralServer) AttachDevice(ctx context.Context, req *pb.AttachDeviceRequest) (*pb.AttachDeviceResponse, error) {
	if err := s.svc.AttachDevice(req.DeviceId, req.VmHandle); err != nil {
		return &pb.AttachDeviceResponse{Success: false}, fmt.Errorf("attach: %w", err)
	}
	return &pb.AttachDeviceResponse{Success: true}, nil
}

// DetachDevice 는 VM에서 디바이스를 분리한다.
func (s *PeripheralServer) DetachDevice(ctx context.Context, req *pb.DetachDeviceRequest) (*pb.DetachDeviceResponse, error) {
	if err := s.svc.DetachDevice(req.DeviceId); err != nil {
		return &pb.DetachDeviceResponse{Success: false}, fmt.Errorf("detach: %w", err)
	}
	return &pb.DetachDeviceResponse{Success: true}, nil
}

// protoToDeviceType 은 Proto DeviceType을 내부 DeviceType으로 변환한다.
func protoToDeviceType(dt pb.DeviceType) peripheral.DeviceType {
	switch dt {
	case pb.DeviceType_DEVICE_TYPE_GPU:
		return peripheral.DeviceGPU
	case pb.DeviceType_DEVICE_TYPE_NIC:
		return peripheral.DeviceNIC
	case pb.DeviceType_DEVICE_TYPE_USB:
		return peripheral.DeviceUSB
	case pb.DeviceType_DEVICE_TYPE_DISK:
		return peripheral.DeviceDisk
	default:
		return ""
	}
}

// deviceTypeToProto 는 내부 DeviceType을 Proto DeviceType으로 변환한다.
func deviceTypeToProto(dt peripheral.DeviceType) pb.DeviceType {
	switch dt {
	case peripheral.DeviceGPU:
		return pb.DeviceType_DEVICE_TYPE_GPU
	case peripheral.DeviceNIC:
		return pb.DeviceType_DEVICE_TYPE_NIC
	case peripheral.DeviceUSB:
		return pb.DeviceType_DEVICE_TYPE_USB
	case peripheral.DeviceDisk:
		return pb.DeviceType_DEVICE_TYPE_DISK
	default:
		return pb.DeviceType_DEVICE_TYPE_UNSPECIFIED
	}
}
