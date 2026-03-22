package grpcapi

import (
	"context"
	"fmt"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
	pb "github.com/HardcoreMonk/hardcorevisor/controller/pkg/proto/storagepb"
)

// StorageServer 는 StorageAgent gRPC 서비스를 구현한다.
// 내부 Storage Service에 위임하여 풀, 볼륨, 스냅샷을 관리한다.
type StorageServer struct {
	pb.UnimplementedStorageAgentServer
	svc *storage.Service
}

// NewStorageServer 는 Storage 서비스를 기반으로 gRPC 서버를 생성한다.
func NewStorageServer(svc *storage.Service) *StorageServer {
	return &StorageServer{svc: svc}
}

// ListPools 는 모든 스토리지 풀을 반환한다.
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

// CreateVolume 은 지정된 풀에 볼륨을 생성한다.
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

// DeleteVolume 은 ID로 볼륨을 삭제한다.
func (s *StorageServer) DeleteVolume(ctx context.Context, req *pb.DeleteVolumeRequest) (*pb.DeleteVolumeResponse, error) {
	if err := s.svc.DeleteVolume(req.Id); err != nil {
		return nil, fmt.Errorf("delete volume: %w", err)
	}
	return &pb.DeleteVolumeResponse{}, nil
}

// CreateSnapshot 은 볼륨의 스냅샷을 생성한다.
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

// ListSnapshots 는 스냅샷 목록을 반환한다. VolumeId로 필터링 가능.
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
