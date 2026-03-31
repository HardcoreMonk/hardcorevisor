// 통합 gRPC 서버 생성 — 모든 gRPC 서비스 등록 + reflection 활성화
package grpcapi

import (
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/compute"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/peripheral"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
	computepb "github.com/HardcoreMonk/hardcorevisor/controller/pkg/proto/computepb"
	peripheralpb "github.com/HardcoreMonk/hardcorevisor/controller/pkg/proto/peripheralpb"
	storagepb "github.com/HardcoreMonk/hardcorevisor/controller/pkg/proto/storagepb"
)

// Services 는 gRPC 서비스 등록에 필요한 내부 서비스 참조를 보관한다.
// nil인 서비스는 등록되지 않는다.
type Services struct {
	Compute    compute.ComputeProvider
	Storage    *storage.Service
	Peripheral *peripheral.Service
}

// NewServer 는 모든 서비스가 등록된 gRPC 서버를 생성한다.
//
// 등록 서비스:
//   - ComputeService: VM CRUD 및 생명주기 (9 RPC)
//   - StorageAgent: 풀/볼륨/스냅샷 관리 (5 RPC)
//   - PeripheralManager: 디바이스 목록/연결/분리 (3 RPC)
//
// gRPC reflection이 활성화되어 grpcurl/grpcui로 탐색 가능하다.
//
// 호출 시점: Controller main.go에서 서버 시작 시
func NewServer(svc *Services) *grpc.Server {
	srv := grpc.NewServer()

	if svc.Compute != nil {
		computepb.RegisterComputeServiceServer(srv, NewComputeServer(svc.Compute))
	}
	if svc.Storage != nil {
		storagepb.RegisterStorageAgentServer(srv, NewStorageServer(svc.Storage))
	}
	if svc.Peripheral != nil {
		peripheralpb.RegisterPeripheralManagerServer(srv, NewPeripheralServer(svc.Peripheral))
	}

	// Enable gRPC reflection for grpcurl/grpcui
	reflection.Register(srv)

	return srv
}

// ListenAndServe 는 지정된 주소에서 gRPC 서버를 시작한다.
//
// 기본 주소: ":19090" (환경변수 HCV_GRPC_ADDR로 변경 가능)
// 블로킹 호출 — 별도 고루틴에서 실행해야 한다.
func ListenAndServe(srv *grpc.Server, addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return srv.Serve(lis)
}
