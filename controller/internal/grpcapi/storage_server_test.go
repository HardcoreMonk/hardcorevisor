package grpcapi

import (
	"context"
	"testing"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
	pb "github.com/HardcoreMonk/hardcorevisor/controller/pkg/proto/storagepb"
)

// newTestStorageServer — 테스트용 StorageServer를 생성한다.
// 인메모리 드라이버를 사용하는 storage.NewService()를 사용한다.
func newTestStorageServer(t *testing.T) *StorageServer {
	t.Helper()
	svc := storage.NewService()
	return NewStorageServer(svc)
}

// TestStorageServer_ListPools — ListPools가 기본 풀을 1개 이상 반환하는지 검증한다.
func TestStorageServer_ListPools(t *testing.T) {
	server := newTestStorageServer(t)
	ctx := context.Background()

	resp, err := server.ListPools(ctx, &pb.ListPoolsRequest{})
	if err != nil {
		t.Fatalf("ListPools 실패: %v", err)
	}

	if len(resp.Pools) < 1 {
		t.Errorf("기본 풀이 1개 이상이어야 한다: got=%d", len(resp.Pools))
	}
}

// TestStorageServer_CreateVolume — 볼륨 생성 후 응답에 ID가 포함되는지 검증한다.
func TestStorageServer_CreateVolume(t *testing.T) {
	server := newTestStorageServer(t)
	ctx := context.Background()

	vol, err := server.CreateVolume(ctx, &pb.CreateVolumeRequest{
		Pool:      "local-zfs",
		Name:      "test-disk",
		SizeBytes: 10737418240, // 10GB
		Format:    "qcow2",
	})
	if err != nil {
		t.Fatalf("CreateVolume 실패: %v", err)
	}

	if vol.Id == "" {
		t.Error("볼륨 ID가 비어있다")
	}
	if vol.Name != "test-disk" {
		t.Errorf("이름 불일치: got=%q, want=%q", vol.Name, "test-disk")
	}
}

// TestStorageServer_DeleteVolume — 볼륨 생성 후 삭제가 성공하는지 검증한다.
func TestStorageServer_DeleteVolume(t *testing.T) {
	server := newTestStorageServer(t)
	ctx := context.Background()

	// 볼륨 생성
	vol, err := server.CreateVolume(ctx, &pb.CreateVolumeRequest{
		Pool:      "local-zfs",
		Name:      "delete-disk",
		SizeBytes: 1073741824, // 1GB
		Format:    "raw",
	})
	if err != nil {
		t.Fatalf("CreateVolume 실패: %v", err)
	}

	// 삭제
	_, err = server.DeleteVolume(ctx, &pb.DeleteVolumeRequest{Id: vol.Id})
	if err != nil {
		t.Fatalf("DeleteVolume 실패: %v", err)
	}
}

// TestStorageServer_CreateSnapshot — 볼륨 생성 후 스냅샷을 만들고 ID가 반환되는지 검증한다.
func TestStorageServer_CreateSnapshot(t *testing.T) {
	server := newTestStorageServer(t)
	ctx := context.Background()

	// 볼륨 생성
	vol, err := server.CreateVolume(ctx, &pb.CreateVolumeRequest{
		Pool:      "local-zfs",
		Name:      "snap-disk",
		SizeBytes: 5368709120, // 5GB
		Format:    "qcow2",
	})
	if err != nil {
		t.Fatalf("CreateVolume 실패: %v", err)
	}

	// 스냅샷 생성
	snap, err := server.CreateSnapshot(ctx, &pb.CreateSnapshotRequest{
		VolumeId: vol.Id,
		Name:     "snap-01",
	})
	if err != nil {
		t.Fatalf("CreateSnapshot 실패: %v", err)
	}

	if snap.Id == "" {
		t.Error("스냅샷 ID가 비어있다")
	}
	if snap.VolumeId != vol.Id {
		t.Errorf("VolumeId 불일치: got=%q, want=%q", snap.VolumeId, vol.Id)
	}
	if snap.Name != "snap-01" {
		t.Errorf("스냅샷 이름 불일치: got=%q, want=%q", snap.Name, "snap-01")
	}
}
