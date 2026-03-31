// resilient.go — Circuit Breaker가 적용된 Store 래퍼.
//
// etcd 장애 시 연쇄 실패를 방지한다.
// 5회 연속 실패 → 30초 동안 요청 차단 → 시험 요청으로 복구 확인.
//
// 사용법:
//
//	store := store.NewResilientStore(etcdStore)
//	err := store.Put(ctx, "vms/1", vm) // circuit breaker 보호
package store

import (
	"context"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/resilience"
)

// ResilientStore — Circuit Breaker로 보호되는 Store 데코레이터.
// 내부 Store에 대한 모든 작업을 CB를 통해 실행한다.
type ResilientStore struct {
	inner Store
	cb    *resilience.CircuitBreaker
}

// NewResilientStore — Circuit Breaker가 적용된 Store를 생성한다.
// threshold: 5회 연속 실패 시 Open, timeout: 30초 후 HalfOpen.
func NewResilientStore(inner Store) *ResilientStore {
	return &ResilientStore{
		inner: inner,
		cb:    resilience.NewCircuitBreaker("etcd-store", 5, 30*time.Second),
	}
}

func (s *ResilientStore) Put(ctx context.Context, key string, value any) error {
	return s.cb.Execute(func() error {
		return s.inner.Put(ctx, key, value)
	})
}

func (s *ResilientStore) Get(ctx context.Context, key string, out any) error {
	return s.cb.Execute(func() error {
		return s.inner.Get(ctx, key, out)
	})
}

func (s *ResilientStore) Delete(ctx context.Context, key string) error {
	return s.cb.Execute(func() error {
		return s.inner.Delete(ctx, key)
	})
}

func (s *ResilientStore) List(ctx context.Context, prefix string) ([]KV, error) {
	var result []KV
	err := s.cb.Execute(func() error {
		var e error
		result, e = s.inner.List(ctx, prefix)
		return e
	})
	return result, err
}

func (s *ResilientStore) Close() error {
	return s.inner.Close()
}

// CircuitState — 현재 Circuit Breaker 상태를 반환한다.
func (s *ResilientStore) CircuitState() string {
	return s.cb.GetState().String()
}
