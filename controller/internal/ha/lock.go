// 분산 잠금 — etcd 기반 또는 인메모리 폴백
//
// etcd가 사용 가능한 경우 etcd concurrency.Mutex를 사용하여 분산 잠금을 제공한다.
// etcd가 없는 경우 인메모리 sync.Mutex를 사용하여 로컬 잠금을 제공한다.
//
// 사용:
//
//	lm := NewLockManager(endpoints)
//	unlock, err := lm.Acquire(ctx, "vm-migration-42", 30*time.Second)
//	defer unlock()
//
//	lm.WithLock(ctx, "key", ttl, func() error { ... })
package ha

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

// LockManager 은 분산 잠금을 관리한다.
// etcd가 없는 환경에서는 인메모리 sync.Mutex로 폴백한다.
type LockManager struct {
	mu       sync.Mutex
	client   *clientv3.Client
	locks    map[string]*sync.Mutex
	inMemory bool
}

// NewLockManager 는 분산 잠금 관리자를 생성한다.
// endpoints가 비어 있거나 etcd 연결에 실패하면 인메모리 모드를 사용한다.
func NewLockManager(endpoints []string) *LockManager {
	lm := &LockManager{
		locks: make(map[string]*sync.Mutex),
	}

	if len(endpoints) == 0 {
		slog.Warn("no etcd endpoints for lock manager, using in-memory locks")
		lm.inMemory = true
		return lm
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		slog.Warn("failed to connect to etcd for lock manager, using in-memory locks",
			"error", err)
		lm.inMemory = true
		return lm
	}

	// Quick health check
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = client.Status(ctx, endpoints[0])
	if err != nil {
		client.Close()
		slog.Warn("etcd health check failed for lock manager, using in-memory locks",
			"error", err)
		lm.inMemory = true
		return lm
	}

	lm.client = client
	return lm
}

// Acquire 는 지정된 키에 대한 잠금을 획득한다.
// 반환된 unlock 함수를 호출하여 잠금을 해제해야 한다.
// TTL은 잠금이 자동으로 만료되는 시간이다 (etcd 모드에서만 적용).
func (lm *LockManager) Acquire(ctx context.Context, key string, ttl time.Duration) (unlock func(), err error) {
	if lm.inMemory {
		return lm.acquireInMemory(key)
	}
	return lm.acquireEtcd(ctx, key, ttl)
}

// TryAcquire 는 비차단 잠금 획득을 시도한다.
// 잠금을 즉시 획득할 수 없으면 ok=false를 반환한다.
func (lm *LockManager) TryAcquire(key string, ttl time.Duration) (unlock func(), ok bool) {
	if lm.inMemory {
		return lm.tryAcquireInMemory(key)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	unlock, err := lm.acquireEtcd(ctx, key, ttl)
	if err != nil {
		return nil, false
	}
	return unlock, true
}

// WithLock 은 잠금을 획득하고 fn을 실행한 후 잠금을 해제한다.
func (lm *LockManager) WithLock(ctx context.Context, key string, ttl time.Duration, fn func() error) error {
	unlock, err := lm.Acquire(ctx, key, ttl)
	if err != nil {
		return fmt.Errorf("acquire lock %q: %w", key, err)
	}
	defer unlock()
	return fn()
}

// Close 은 잠금 관리자를 정리한다.
func (lm *LockManager) Close() error {
	if lm.client != nil {
		return lm.client.Close()
	}
	return nil
}

func (lm *LockManager) acquireInMemory(key string) (func(), error) {
	lm.mu.Lock()
	mtx, ok := lm.locks[key]
	if !ok {
		mtx = &sync.Mutex{}
		lm.locks[key] = mtx
	}
	lm.mu.Unlock()

	mtx.Lock()
	return func() { mtx.Unlock() }, nil
}

func (lm *LockManager) tryAcquireInMemory(key string) (func(), bool) {
	lm.mu.Lock()
	mtx, ok := lm.locks[key]
	if !ok {
		mtx = &sync.Mutex{}
		lm.locks[key] = mtx
	}
	lm.mu.Unlock()

	if mtx.TryLock() {
		return func() { mtx.Unlock() }, true
	}
	return nil, false
}

func (lm *LockManager) acquireEtcd(ctx context.Context, key string, ttl time.Duration) (func(), error) {
	ttlSec := int(ttl.Seconds())
	if ttlSec < 1 {
		ttlSec = 5
	}

	session, err := concurrency.NewSession(lm.client, concurrency.WithTTL(ttlSec))
	if err != nil {
		return nil, fmt.Errorf("create etcd session: %w", err)
	}

	lockKey := "/hcv/locks/" + key
	mtx := concurrency.NewMutex(session, lockKey)

	if err := mtx.Lock(ctx); err != nil {
		session.Close()
		return nil, fmt.Errorf("acquire etcd lock %q: %w", key, err)
	}

	unlock := func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := mtx.Unlock(unlockCtx); err != nil {
			slog.Warn("failed to unlock etcd lock", "key", key, "error", err)
		}
		session.Close()
	}
	return unlock, nil
}
