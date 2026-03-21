package grpcapi

import (
	"context"
	"fmt"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/peripheral"
	pb "github.com/HardcoreMonk/hardcorevisor/controller/pkg/proto/peripheralpb"
)

// PeripheralServer implements the PeripheralManager gRPC service.
type PeripheralServer struct {
	pb.UnimplementedPeripheralManagerServer
	svc *peripheral.Service
}

func NewPeripheralServer(svc *peripheral.Service) *PeripheralServer {
	return &PeripheralServer{svc: svc}
}

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

func (s *PeripheralServer) AttachDevice(ctx context.Context, req *pb.AttachDeviceRequest) (*pb.AttachDeviceResponse, error) {
	if err := s.svc.AttachDevice(req.DeviceId, req.VmHandle); err != nil {
		return &pb.AttachDeviceResponse{Success: false}, fmt.Errorf("attach: %w", err)
	}
	return &pb.AttachDeviceResponse{Success: true}, nil
}

func (s *PeripheralServer) DetachDevice(ctx context.Context, req *pb.DetachDeviceRequest) (*pb.DetachDeviceResponse, error) {
	if err := s.svc.DetachDevice(req.DeviceId); err != nil {
		return &pb.DetachDeviceResponse{Success: false}, fmt.Errorf("detach: %w", err)
	}
	return &pb.DetachDeviceResponse{Success: true}, nil
}

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
