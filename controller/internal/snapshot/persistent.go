// Package snapshot — PersistentSnapshotService: etcd 기반 스냅샷 메타데이터 영속화 래퍼
//
// 아키텍처 위치: snapshot.Service를 Decorator 패턴으로 래핑
//   API/gRPC → PersistentSnapshotService → snapshot.Service (인메모리 CRUD)
//                                        → Store (etcd/메모리 영속화)
//
// PersistentComputeService 패턴을 동일하게 적용하여,
// 모든 스냅샷 변경 작업(생성, 삭제, 복원)을 Store에 자동으로 저장한다.
// Controller 재시작 시 LoadFromStore()로 저장된 스냅샷을 인메모리로 복원한다.
//
// 저장소 키 형식:
//
//	"snapshots/{id}" → VMSnapshot JSON
//
// 에러 처리 전략:
//
//	Store 저장/삭제 실패는 로그로 기록하지만 스냅샷 작업 자체는 성공으로 처리한다.
//	이는 Store가 일시적으로 불가용해도 서비스를 계속 운영하기 위한 설계이다.
//	(eventually consistent — 다음 변경 시 Store에 최신 상태가 반영됨)
//
// 스레드 안전성: inner Service의 mutex에 의존
//
// 의존성:
//   - internal/store: Store 인터페이스 (EtcdStore 또는 MemoryStore)
//   - snapshot.Service: 실제 스냅샷 CRUD 로직
package snapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/store"
)

// PersistentSnapshotService 는 snapshot.Service를 래핑하여 스냅샷 메타데이터를 Store에 영속화한다.
//
// 필드:
//   - inner: 실제 스냅샷 CRUD를 수행하는 Service (인메모리)
//   - store: 영속화 대상 Store (EtcdStore 또는 MemoryStore)
//
// 모든 변경 메서드(Create, Delete, Restore)는 inner에 위임한 후 Store에 저장한다.
// 읽기 메서드(List, Get)는 inner에만 위임한다 (인메모리에서 빠르게 조회).
type PersistentSnapshotService struct {
	inner *Service
	store store.Store
}

// NewPersistentSnapshotService 는 snapshot.Service에 영속화 래퍼를 생성한다.
//
// 매개변수:
//   - svc: 래핑할 snapshot.Service (인메모리 CRUD 담당)
//   - s: 스냅샷 메타데이터를 저장할 Store (EtcdStore 또는 MemoryStore)
//
// 호출 시점: Controller 초기화 시, etcd 연결이 설정된 경우
func NewPersistentSnapshotService(svc *Service, s store.Store) *PersistentSnapshotService {
	return &PersistentSnapshotService{
		inner: svc,
		store: s,
	}
}

// snapshotStoreKey 는 스냅샷 ID로부터 Store 키를 생성한다.
// 형식: "snapshots/{id}" — etcd의 키 prefix 기반 조회에 사용된다.
func snapshotStoreKey(id string) string {
	return fmt.Sprintf("snapshots/%s", id)
}

// Create 는 inner 서비스를 통해 스냅샷을 생성한 후 Store에 영속화한다.
//
// 처리 순서:
//  1. inner.Create()로 인메모리 스냅샷 생성
//  2. store.Put()으로 etcd에 JSON 직렬화하여 저장 (5초 타임아웃)
//
// Store 저장 실패는 로그로 기록하지만 스냅샷 생성 자체는 성공으로 처리한다.
func (p *PersistentSnapshotService) Create(vmID int32, vmName string) (*VMSnapshot, error) {
	snap, err := p.inner.Create(vmID, vmName)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if putErr := p.store.Put(ctx, snapshotStoreKey(snap.ID), snap); putErr != nil {
		slog.Error("persistent snapshot: failed to store snapshot", "snapshot_id", snap.ID, "error", putErr)
	}
	return snap, nil
}

// List 는 inner 서비스에 위임하여 인메모리 스냅샷 목록을 반환한다.
// Store를 직접 조회하지 않는다 (인메모리가 최신 상태를 유지).
func (p *PersistentSnapshotService) List(vmID int32) []*VMSnapshot {
	return p.inner.List(vmID)
}

// Get 은 inner 서비스에 위임하여 인메모리에서 스냅샷을 조회한다.
func (p *PersistentSnapshotService) Get(id string) (*VMSnapshot, error) {
	return p.inner.Get(id)
}

// Delete 는 inner 서비스에서 스냅샷을 삭제한 후 Store에서도 제거한다.
//
// 처리 순서:
//  1. inner.Delete()로 인메모리에서 삭제
//  2. store.Delete()로 etcd에서 키 삭제 (5초 타임아웃)
//
// Store 삭제 실패는 로그로 기록하지만 스냅샷 삭제 자체는 성공으로 처리한다.
func (p *PersistentSnapshotService) Delete(id string) error {
	if err := p.inner.Delete(id); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if delErr := p.store.Delete(ctx, snapshotStoreKey(id)); delErr != nil {
		slog.Error("persistent snapshot: failed to delete snapshot from store", "snapshot_id", id, "error", delErr)
	}
	return nil
}

// Restore 는 inner 서비스에서 스냅샷을 복원한 후 변경된 상태를 Store에 저장한다.
//
// 복원 후 스냅샷의 State가 "restoring"으로 변경되므로 Store에 갱신이 필요하다.
// Store 저장 실패는 로그로 기록하지만 복원 자체는 성공으로 처리한다.
func (p *PersistentSnapshotService) Restore(id string) (*VMSnapshot, error) {
	snap, err := p.inner.Restore(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if putErr := p.store.Put(ctx, snapshotStoreKey(snap.ID), snap); putErr != nil {
		slog.Error("persistent snapshot: failed to update snapshot in store", "snapshot_id", snap.ID, "error", putErr)
	}
	return snap, nil
}

// LoadFromStore 는 Store에서 영속화된 모든 스냅샷을 읽어 인메모리로 복원한다.
//
// 처리 순서:
//  1. store.List("snapshots/")로 모든 스냅샷 키-값 조회 (10초 타임아웃)
//  2. 각 값을 VMSnapshot으로 JSON 디시리얼라이즈
//  3. inner.snapshots 맵에 직접 삽입 (mutex 보호)
//
// 호출 시점: Controller 시작 시 1회 호출 (main.go 초기화 단계)
// 에러 처리: 개별 스냅샷 파싱 실패는 건너뛰고 계속 진행
func (p *PersistentSnapshotService) LoadFromStore() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	kvs, err := p.store.List(ctx, "snapshots/")
	if err != nil {
		return fmt.Errorf("persistent snapshot: list stored snapshots: %w", err)
	}

	if len(kvs) == 0 {
		slog.Info("persistent snapshot: no snapshots found in store")
		return nil
	}

	loaded := 0
	for _, kv := range kvs {
		var snap VMSnapshot
		if err := json.Unmarshal(kv.Value, &snap); err != nil {
			slog.Warn("persistent snapshot: failed to unmarshal snapshot", "key", kv.Key, "error", err)
			continue
		}
		p.inner.mu.Lock()
		p.inner.snapshots[snap.ID] = &snap
		p.inner.mu.Unlock()
		loaded++
	}

	slog.Info("persistent snapshot: loaded snapshots from store", "loaded", loaded, "total", len(kvs))
	return nil
}

// Inner 는 래핑된 inner Service를 반환한다.
// 영속화 없이 직접 Service에 접근해야 할 때 사용한다.
func (p *PersistentSnapshotService) Inner() *Service {
	return p.inner
}
