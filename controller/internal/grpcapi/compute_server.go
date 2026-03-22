// Package grpcapi — gRPC 서비스 구현체
//
// 아키텍처 위치: gRPC 클라이언트 → Proto 서비스 인터페이스 → 내부 서비스 레이어
//
// proto 코드 생성기가 만든 서비스 인터페이스와 내부 서비스를 연결하는 브릿지이다.
// 각 gRPC 서버는 대응하는 내부 서비스를 래핑한다.
//
// gRPC 서비스 목록:
//   - ComputeService (hardcorevisor.compute.v1): VM CRUD, 생명주기, 마이그레이션 (9 RPC)
//   - StorageAgent (hardcorevisor.storage.v1): 풀, 볼륨, 스냅샷 (5 RPC)
//   - PeripheralManager (hardcorevisor.peripheral.v1): 디바이스 목록, 연결, 분리 (3 RPC)
package grpcapi

import (
	"context"
	"fmt"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/compute"
	pb "github.com/HardcoreMonk/hardcorevisor/controller/pkg/proto/computepb"
)

// ComputeServer 는 ComputeService gRPC 서비스를 구현한다.
// ComputeProvider 인터페이스를 통해 ComputeService 또는 PersistentComputeService와 연결된다.
type ComputeServer struct {
	pb.UnimplementedComputeServiceServer
	svc compute.ComputeProvider
}

// NewComputeServer 는 Compute 서비스를 기반으로 gRPC 서버를 생성한다.
func NewComputeServer(svc compute.ComputeProvider) *ComputeServer {
	return &ComputeServer{svc: svc}
}

// CreateVM 은 gRPC CreateVM 요청을 내부 서비스로 위임한다.
func (s *ComputeServer) CreateVM(ctx context.Context, req *pb.CreateVMRequest) (*pb.CreateVMResponse, error) {
	vm, err := s.svc.CreateVM(req.Name, req.Vcpus, req.MemoryMb, "")
	if err != nil {
		return nil, fmt.Errorf("create VM: %w", err)
	}
	return &pb.CreateVMResponse{Vm: vmToProto(vm)}, nil
}

// DestroyVM 은 gRPC DestroyVM 요청을 내부 서비스로 위임한다.
func (s *ComputeServer) DestroyVM(ctx context.Context, req *pb.DestroyVMRequest) (*pb.DestroyVMResponse, error) {
	if err := s.svc.DestroyVM(req.Handle); err != nil {
		return nil, fmt.Errorf("destroy VM: %w", err)
	}
	return &pb.DestroyVMResponse{}, nil
}

// StartVM 은 gRPC StartVM 요청을 내부 서비스의 "start" 액션으로 위임한다.
func (s *ComputeServer) StartVM(ctx context.Context, req *pb.VMActionRequest) (*pb.VMActionResponse, error) {
	vm, err := s.svc.ActionVM(req.Handle, "start")
	if err != nil {
		return nil, fmt.Errorf("start VM: %w", err)
	}
	return &pb.VMActionResponse{NewState: stateToProto(vm.State)}, nil
}

// StopVM 은 gRPC StopVM 요청을 내부 서비스의 "stop" 액션으로 위임한다.
func (s *ComputeServer) StopVM(ctx context.Context, req *pb.VMActionRequest) (*pb.VMActionResponse, error) {
	vm, err := s.svc.ActionVM(req.Handle, "stop")
	if err != nil {
		return nil, fmt.Errorf("stop VM: %w", err)
	}
	return &pb.VMActionResponse{NewState: stateToProto(vm.State)}, nil
}

// PauseVM 은 gRPC PauseVM 요청을 내부 서비스의 "pause" 액션으로 위임한다.
func (s *ComputeServer) PauseVM(ctx context.Context, req *pb.VMActionRequest) (*pb.VMActionResponse, error) {
	vm, err := s.svc.ActionVM(req.Handle, "pause")
	if err != nil {
		return nil, fmt.Errorf("pause VM: %w", err)
	}
	return &pb.VMActionResponse{NewState: stateToProto(vm.State)}, nil
}

// ResumeVM 은 gRPC ResumeVM 요청을 내부 서비스의 "resume" 액션으로 위임한다.
func (s *ComputeServer) ResumeVM(ctx context.Context, req *pb.VMActionRequest) (*pb.VMActionResponse, error) {
	vm, err := s.svc.ActionVM(req.Handle, "resume")
	if err != nil {
		return nil, fmt.Errorf("resume VM: %w", err)
	}
	return &pb.VMActionResponse{NewState: stateToProto(vm.State)}, nil
}

// ListVMs 는 모든 VM 목록을 반환한다.
func (s *ComputeServer) ListVMs(ctx context.Context, req *pb.ListVMsRequest) (*pb.ListVMsResponse, error) {
	vms := s.svc.ListVMs()
	result := make([]*pb.VMInfo, 0, len(vms))
	for _, vm := range vms {
		result = append(result, vmToProto(vm))
	}
	return &pb.ListVMsResponse{Vms: result}, nil
}

// GetVM 은 handle로 특정 VM을 조회한다.
func (s *ComputeServer) GetVM(ctx context.Context, req *pb.GetVMRequest) (*pb.VMInfo, error) {
	vm, err := s.svc.GetVM(req.Handle)
	if err != nil {
		return nil, fmt.Errorf("get VM: %w", err)
	}
	return vmToProto(vm), nil
}

// LiveMigrate 는 VM을 대상 노드로 라이브 마이그레이션한다.
// 실패 시에도 gRPC 에러가 아닌 응답의 Success=false로 반환한다.
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

// ── 변환 함수 ───────────────────────────────────────

// vmToProto 는 내부 VMInfo를 Proto VMInfo로 변환한다.
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

// stateToProto 는 문자열 상태를 Proto VMState 열거형으로 변환한다.
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
