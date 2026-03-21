package grpcapi

import (
	"context"
	"fmt"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
	pb "github.com/HardcoreMonk/hardcorevisor/controller/pkg/proto/storagepb"
)

// StorageServer implements the StorageAgent gRPC service.
type StorageServer struct {
	pb.UnimplementedStorageAgentServer
	svc *storage.Service
}

func NewStorageServer(svc *storage.Service) *StorageServer {
	return &StorageServer{svc: svc}
}

func (s *StorageServer) ListPools(ctx context.Context, req *pb.ListPoolsRequest) (*pb.ListPoolsResponse, error) {
	pools := s.svc.ListPools()
	result := make([]*pb.StoragePool, 0, len(pools))
	for _, p := range pools {
		result = append(result, &pb.StoragePool{
			Name:       p.Name,
			PoolType:   p.PoolType,
			TotalBytes: p.TotalBytes,
			UsedBytes:  p.UsedBytes,
			Health:     p.Health,
		})
	}
	return &pb.ListPoolsResponse{Pools: result}, nil
}

func (s *StorageServer) CreateVolume(ctx context.Context, req *pb.CreateVolumeRequest) (*pb.Volume, error) {
	vol, err := s.svc.CreateVolume(req.Pool, req.Name, req.Format, req.SizeBytes)
	if err != nil {
		return nil, fmt.Errorf("create volume: %w", err)
	}
	return &pb.Volume{
		Id: vol.ID, Pool: vol.Pool, Name: vol.Name,
		SizeBytes: vol.SizeBytes, Path: vol.Path,
	}, nil
}

func (s *StorageServer) DeleteVolume(ctx context.Context, req *pb.DeleteVolumeRequest) (*pb.DeleteVolumeResponse, error) {
	if err := s.svc.DeleteVolume(req.Id); err != nil {
		return nil, fmt.Errorf("delete volume: %w", err)
	}
	return &pb.DeleteVolumeResponse{}, nil
}

func (s *StorageServer) CreateSnapshot(ctx context.Context, req *pb.CreateSnapshotRequest) (*pb.Snapshot, error) {
	snap, err := s.svc.CreateSnapshot(req.VolumeId, req.Name)
	if err != nil {
		return nil, fmt.Errorf("create snapshot: %w", err)
	}
	return &pb.Snapshot{
		Id: snap.ID, VolumeId: snap.VolumeID,
		Name: snap.Name, CreatedAt: snap.CreatedAt,
	}, nil
}

func (s *StorageServer) ListSnapshots(ctx context.Context, req *pb.ListSnapshotsRequest) (*pb.ListSnapshotsResponse, error) {
	snaps := s.svc.ListSnapshots(req.VolumeId)
	result := make([]*pb.Snapshot, 0, len(snaps))
	for _, snap := range snaps {
		result = append(result, &pb.Snapshot{
			Id: snap.ID, VolumeId: snap.VolumeID,
			Name: snap.Name, CreatedAt: snap.CreatedAt,
		})
	}
	return &pb.ListSnapshotsResponse{Snapshots: result}, nil
}
