// Package grpcapi — gRPC service implementations
//
// Bridges the generated proto service interfaces to the internal service layer.
// Each gRPC server wraps its corresponding internal service.
package grpcapi

import (
	"context"
	"fmt"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/compute"
	pb "github.com/HardcoreMonk/hardcorevisor/controller/pkg/proto/computepb"
)

// ComputeServer implements the ComputeService gRPC service.
type ComputeServer struct {
	pb.UnimplementedComputeServiceServer
	svc compute.ComputeProvider
}

// NewComputeServer creates a gRPC compute server backed by the compute service.
func NewComputeServer(svc compute.ComputeProvider) *ComputeServer {
	return &ComputeServer{svc: svc}
}

func (s *ComputeServer) CreateVM(ctx context.Context, req *pb.CreateVMRequest) (*pb.CreateVMResponse, error) {
	vm, err := s.svc.CreateVM(req.Name, req.Vcpus, req.MemoryMb, "")
	if err != nil {
		return nil, fmt.Errorf("create VM: %w", err)
	}
	return &pb.CreateVMResponse{Vm: vmToProto(vm)}, nil
}

func (s *ComputeServer) DestroyVM(ctx context.Context, req *pb.DestroyVMRequest) (*pb.DestroyVMResponse, error) {
	if err := s.svc.DestroyVM(req.Handle); err != nil {
		return nil, fmt.Errorf("destroy VM: %w", err)
	}
	return &pb.DestroyVMResponse{}, nil
}

func (s *ComputeServer) StartVM(ctx context.Context, req *pb.VMActionRequest) (*pb.VMActionResponse, error) {
	vm, err := s.svc.ActionVM(req.Handle, "start")
	if err != nil {
		return nil, fmt.Errorf("start VM: %w", err)
	}
	return &pb.VMActionResponse{NewState: stateToProto(vm.State)}, nil
}

func (s *ComputeServer) StopVM(ctx context.Context, req *pb.VMActionRequest) (*pb.VMActionResponse, error) {
	vm, err := s.svc.ActionVM(req.Handle, "stop")
	if err != nil {
		return nil, fmt.Errorf("stop VM: %w", err)
	}
	return &pb.VMActionResponse{NewState: stateToProto(vm.State)}, nil
}

func (s *ComputeServer) PauseVM(ctx context.Context, req *pb.VMActionRequest) (*pb.VMActionResponse, error) {
	vm, err := s.svc.ActionVM(req.Handle, "pause")
	if err != nil {
		return nil, fmt.Errorf("pause VM: %w", err)
	}
	return &pb.VMActionResponse{NewState: stateToProto(vm.State)}, nil
}

func (s *ComputeServer) ResumeVM(ctx context.Context, req *pb.VMActionRequest) (*pb.VMActionResponse, error) {
	vm, err := s.svc.ActionVM(req.Handle, "resume")
	if err != nil {
		return nil, fmt.Errorf("resume VM: %w", err)
	}
	return &pb.VMActionResponse{NewState: stateToProto(vm.State)}, nil
}

func (s *ComputeServer) ListVMs(ctx context.Context, req *pb.ListVMsRequest) (*pb.ListVMsResponse, error) {
	vms := s.svc.ListVMs()
	result := make([]*pb.VMInfo, 0, len(vms))
	for _, vm := range vms {
		result = append(result, vmToProto(vm))
	}
	return &pb.ListVMsResponse{Vms: result}, nil
}

func (s *ComputeServer) GetVM(ctx context.Context, req *pb.GetVMRequest) (*pb.VMInfo, error) {
	vm, err := s.svc.GetVM(req.Handle)
	if err != nil {
		return nil, fmt.Errorf("get VM: %w", err)
	}
	return vmToProto(vm), nil
}

func (s *ComputeServer) LiveMigrate(ctx context.Context, req *pb.LiveMigrateRequest) (*pb.LiveMigrateResponse, error) {
	if err := s.svc.MigrateVM(req.Handle, req.TargetNode); err != nil {
		return &pb.LiveMigrateResponse{
			Success: false,
			Message: fmt.Sprintf("migration failed: %v", err),
		}, nil
	}
	return &pb.LiveMigrateResponse{
		Success: true,
		Message: fmt.Sprintf("VM %d migrated to %s", req.Handle, req.TargetNode),
	}, nil
}

// ── Converters ───────────────────────────────────────

func vmToProto(vm *compute.VMInfo) *pb.VMInfo {
	return &pb.VMInfo{
		Handle:   vm.ID,
		Name:     vm.Name,
		State:    stateToProto(vm.State),
		Vcpus:    vm.VCPUs,
		MemoryMb: vm.MemoryMB,
		Node:     vm.Node,
	}
}

func stateToProto(state string) pb.VMState {
	switch state {
	case "created":
		return pb.VMState_VM_STATE_CREATED
	case "configured":
		return pb.VMState_VM_STATE_CONFIGURED
	case "running":
		return pb.VMState_VM_STATE_RUNNING
	case "paused":
		return pb.VMState_VM_STATE_PAUSED
	case "stopped":
		return pb.VMState_VM_STATE_STOPPED
	default:
		return pb.VMState_VM_STATE_UNSPECIFIED
	}
}
