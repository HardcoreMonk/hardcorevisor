package grpcapi

import (
	"context"
	"testing"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/compute"
	"github.com/HardcoreMonk/hardcorevisor/controller/pkg/ffi"
	pb "github.com/HardcoreMonk/hardcorevisor/controller/pkg/proto/computepb"
)

// newTestComputeServer — 테스트용 ComputeServer를 생성한다.
// MockVMCore → RustVMMBackend → BackendSelector → ComputeService → ComputeServer
func newTestComputeServer(t *testing.T) *ComputeServer {
	t.Helper()

	core := ffi.NewMockVMCore()
	core.Init()
	backend := compute.NewRustVMMBackend(core)
	selector := compute.NewBackendSelector(compute.PolicyAuto)
	selector.Register(backend)
	svc := compute.NewComputeService(selector, backend)
	return NewComputeServer(svc)
}

// TestComputeServer_CreateVM — VM 생성 후 응답에 handle > 0이 포함되는지 검증한다.
func TestComputeServer_CreateVM(t *testing.T) {
	server := newTestComputeServer(t)
	ctx := context.Background()

	resp, err := server.CreateVM(ctx, &pb.CreateVMRequest{
		Name:     "test-vm",
		Vcpus:    2,
		MemoryMb: 4096,
	})
	if err != nil {
		t.Fatalf("CreateVM 실패: %v", err)
	}

	if resp.Vm == nil {
		t.Fatal("응답에 VM 정보가 없다")
	}
	if resp.Vm.Handle <= 0 {
		t.Errorf("handle이 양수여야 한다: got=%d", resp.Vm.Handle)
	}
	if resp.Vm.Name != "test-vm" {
		t.Errorf("이름 불일치: got=%q, want=%q", resp.Vm.Name, "test-vm")
	}
}

// TestComputeServer_ListVMs — VM 2개 생성 후 ListVMs가 2개를 반환하는지 검증한다.
func TestComputeServer_ListVMs(t *testing.T) {
	server := newTestComputeServer(t)
	ctx := context.Background()

	// VM 2개 생성
	for _, name := range []string{"vm-1", "vm-2"} {
		_, err := server.CreateVM(ctx, &pb.CreateVMRequest{
			Name:     name,
			Vcpus:    1,
			MemoryMb: 512,
		})
		if err != nil {
			t.Fatalf("CreateVM(%s) 실패: %v", name, err)
		}
	}

	resp, err := server.ListVMs(ctx, &pb.ListVMsRequest{})
	if err != nil {
		t.Fatalf("ListVMs 실패: %v", err)
	}

	if len(resp.Vms) != 2 {
		t.Errorf("VM 수 불일치: got=%d, want=2", len(resp.Vms))
	}
}

// TestComputeServer_GetVM — VM 생성 후 handle로 조회하여 이름이 일치하는지 검증한다.
func TestComputeServer_GetVM(t *testing.T) {
	server := newTestComputeServer(t)
	ctx := context.Background()

	createResp, err := server.CreateVM(ctx, &pb.CreateVMRequest{
		Name:     "lookup-vm",
		Vcpus:    4,
		MemoryMb: 8192,
	})
	if err != nil {
		t.Fatalf("CreateVM 실패: %v", err)
	}

	vm, err := server.GetVM(ctx, &pb.GetVMRequest{Handle: createResp.Vm.Handle})
	if err != nil {
		t.Fatalf("GetVM 실패: %v", err)
	}

	if vm.Name != "lookup-vm" {
		t.Errorf("이름 불일치: got=%q, want=%q", vm.Name, "lookup-vm")
	}
	if vm.Vcpus != 4 {
		t.Errorf("vCPU 불일치: got=%d, want=4", vm.Vcpus)
	}
}

// TestComputeServer_GetVM_NotFound — 존재하지 않는 handle 조회 시 에러를 반환한다.
func TestComputeServer_GetVM_NotFound(t *testing.T) {
	server := newTestComputeServer(t)
	ctx := context.Background()

	_, err := server.GetVM(ctx, &pb.GetVMRequest{Handle: 99999})
	if err == nil {
		t.Fatal("존재하지 않는 VM에 대해 에러가 반환되어야 한다")
	}
}

// TestComputeServer_StartStopVM — Create → Start → Stop 생명주기를 검증한다.
func TestComputeServer_StartStopVM(t *testing.T) {
	server := newTestComputeServer(t)
	ctx := context.Background()

	// VM 생성 (configured 상태)
	createResp, err := server.CreateVM(ctx, &pb.CreateVMRequest{
		Name:     "lifecycle-vm",
		Vcpus:    2,
		MemoryMb: 2048,
	})
	if err != nil {
		t.Fatalf("CreateVM 실패: %v", err)
	}
	handle := createResp.Vm.Handle

	// Start
	startResp, err := server.StartVM(ctx, &pb.VMActionRequest{Handle: handle})
	if err != nil {
		t.Fatalf("StartVM 실패: %v", err)
	}
	if startResp.NewState != pb.VMState_VM_STATE_RUNNING {
		t.Errorf("Start 후 상태 불일치: got=%v, want=RUNNING", startResp.NewState)
	}

	// Stop
	stopResp, err := server.StopVM(ctx, &pb.VMActionRequest{Handle: handle})
	if err != nil {
		t.Fatalf("StopVM 실패: %v", err)
	}
	if stopResp.NewState != pb.VMState_VM_STATE_STOPPED {
		t.Errorf("Stop 후 상태 불일치: got=%v, want=STOPPED", stopResp.NewState)
	}
}

// TestComputeServer_DestroyVM — VM 생성 후 삭제, ListVMs가 비어있는지 검증한다.
func TestComputeServer_DestroyVM(t *testing.T) {
	server := newTestComputeServer(t)
	ctx := context.Background()

	createResp, err := server.CreateVM(ctx, &pb.CreateVMRequest{
		Name:     "destroy-vm",
		Vcpus:    1,
		MemoryMb: 256,
	})
	if err != nil {
		t.Fatalf("CreateVM 실패: %v", err)
	}

	_, err = server.DestroyVM(ctx, &pb.DestroyVMRequest{Handle: createResp.Vm.Handle})
	if err != nil {
		t.Fatalf("DestroyVM 실패: %v", err)
	}

	listResp, err := server.ListVMs(ctx, &pb.ListVMsRequest{})
	if err != nil {
		t.Fatalf("ListVMs 실패: %v", err)
	}
	if len(listResp.Vms) != 0 {
		t.Errorf("삭제 후 VM이 남아있다: got=%d, want=0", len(listResp.Vms))
	}
}
