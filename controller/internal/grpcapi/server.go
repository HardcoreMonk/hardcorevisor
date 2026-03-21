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

// Services holds the internal services for gRPC registration.
type Services struct {
	Compute    compute.ComputeProvider
	Storage    *storage.Service
	Peripheral *peripheral.Service
}

// NewServer creates a gRPC server with all services registered.
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

// ListenAndServe starts the gRPC server on the given address.
func ListenAndServe(srv *grpc.Server, addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return srv.Serve(lis)
}
