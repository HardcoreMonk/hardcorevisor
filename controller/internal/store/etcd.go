// Package store — etcd 기반 키-값 상태 저장소
//
// 아키텍처 위치: Go Controller → Store → etcd v3 / 인메모리 폴백
//
// Controller의 상태(VM, 스토리지, HA 노드 등)를 영속화하는 KV 추상화이다.
// etcd를 사용할 수 없으면 인메모리 저장소로 자동 전환한다.
//
// 키 명명 규칙:
//   - "vms/{id}": VM 상태 (JSON 직렬화)
//   - "ha/nodes/{name}": HA 노드 상태
//   - "ha/fence-events/{id}": 펜싱 이벤트
//
// 직렬화 형식: JSON (encoding/json.Marshal/Unmarshal)
//
// 환경변수:
//   - HCV_ETCD_ENDPOINTS: etcd 엔드포인트 (쉼표 구분). 미설정 시 인메모리 사용.
//     예: "localhost:2379" 또는 "etcd1:2379,etcd2:2379,etcd3:2379"
//
// 키 접두사: 모든 키에 "/hcv/" 접두사가 자동 추가된다.
// 예: Put("vms/1", ...) → etcd 키는 "/hcv/vms/1"
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// Store 는 상태 영속화를 위한 인터페이스이다.
// 값은 JSON으로 직렬화하여 저장하고, 역직렬화하여 조회한다.
//
// 구현체:
//   - EtcdStore: etcd v3 기반 (분산 영속화)
//   - MemoryStore: 인메모리 (개발/테스트, 재시작 시 데이터 소실)
type Store interface {
	Put(ctx context.Context, key string, value any) error
	Get(ctx context.Context, key string, out any) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]KV, error)
	Close() error
}

// KV 는 키-값 쌍을 나타낸다. Value는 JSON 바이트 슬라이스이다.
type KV struct {
	Key   string
	Value []byte
}

// ── etcd 저장소 ──────────────────────────────────────

// EtcdStore 는 etcd v3 기반의 Store 구현체이다.
// prefix 필드에 키 네임스페이스를 저장한다 (기본값: "/hcv/").
type EtcdStore struct {
	client *clientv3.Client
	prefix string // key namespace, e.g. "/hcv/"
}

// EtcdConfig 는 etcd 연결 설정을 보관한다.
type EtcdConfig struct {
	Endpoints   []string      // e.g. ["localhost:2379"]
	DialTimeout time.Duration // default 5s
	Prefix      string        // key prefix, default "/hcv/"
}

// NewEtcdStore 는 etcd에 연결하고 Store를 반환한다.
//
// 기본값:
//   - Endpoints: ["localhost:2379"]
//   - DialTimeout: 5초
//   - Prefix: "/hcv/"
//
// 연결 후 즉시 health check를 수행하여 etcd 가용성을 확인한다.
// 에러 조건: 연결 실패, health check 실패
func NewEtcdStore(cfg EtcdConfig) (*EtcdStore, error) {
	if len(cfg.Endpoints) == 0 {
		cfg.Endpoints = []string{"localhost:2379"}
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 5 * time.Second
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "/hcv/"
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: cfg.DialTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("etcd connect: %w", err)
	}

	// Quick health check
	ctx, cancel := context.WithTimeout(context.Background(), cfg.DialTimeout)
	defer cancel()
	_, err = client.Status(ctx, cfg.Endpoints[0])
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("etcd health check: %w", err)
	}

	slog.Info("etcd store connected", "endpoints", cfg.Endpoints, "prefix", cfg.Prefix)
	return &EtcdStore{client: client, prefix: cfg.Prefix}, nil
}

// key 는 저장소 접두사를 붙여 실제 etcd 키를 생성한다.
// 예: key("vms/1") → "/hcv/vms/1"
func (s *EtcdStore) key(k string) string {
	return s.prefix + k
}

// Put 은 값을 JSON으로 직렬화하여 etcd에 저장한다.
func (s *EtcdStore) Put(ctx context.Context, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	_, err = s.client.Put(ctx, s.key(key), string(data))
	return err
}

// Get 은 etcd에서 값을 조회하여 JSON 역직렬화한다.
// 키 미존재 시 에러 반환.
func (s *EtcdStore) Get(ctx context.Context, key string, out any) error {
	resp, err := s.client.Get(ctx, s.key(key))
	if err != nil {
		return err
	}
	if len(resp.Kvs) == 0 {
		return fmt.Errorf("key not found: %s", key)
	}
	return json.Unmarshal(resp.Kvs[0].Value, out)
}

// Delete 는 etcd에서 키를 삭제한다.
func (s *EtcdStore) Delete(ctx context.Context, key string) error {
	_, err := s.client.Delete(ctx, s.key(key))
	return err
}

// List 는 주어진 접두사로 etcd의 키-값 쌍을 조회한다.
// 반환되는 키에서 저장소 접두사("/hcv/")는 제거된다.
func (s *EtcdStore) List(ctx context.Context, prefix string) ([]KV, error) {
	resp, err := s.client.Get(ctx, s.key(prefix), clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	result := make([]KV, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		// Strip the store prefix from the key
		k := strings.TrimPrefix(string(kv.Key), s.prefix)
		result = append(result, KV{Key: k, Value: kv.Value})
	}
	return result, nil
}

// Close 는 etcd 클라이언트 연결을 종료한다.
func (s *EtcdStore) Close() error {
	return s.client.Close()
}

// ── 인메모리 저장소 (폴백) ───────────────────────────

// MemoryStore 는 인메모리 맵으로 Store를 구현한다 (영속화 없음).
// etcd를 사용할 수 없을 때 자동으로 폴백되는 저장소이다.
// 프로세스 재시작 시 모든 데이터가 소실된다.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemoryStore 는 비영속 인메모리 저장소를 생성한다.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string][]byte)}
}

func (s *MemoryStore) Put(_ context.Context, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.data[key] = data
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) Get(_ context.Context, key string, out any) error {
	s.mu.RLock()
	data, ok := s.data[key]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("key not found: %s", key)
	}
	return json.Unmarshal(data, out)
}

func (s *MemoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.data, key)
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) List(_ context.Context, prefix string) ([]KV, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []KV
	for k, v := range s.data {
		if strings.HasPrefix(k, prefix) {
			result = append(result, KV{Key: k, Value: v})
		}
	}
	return result, nil
}

func (s *MemoryStore) Close() error { return nil }

// ── 팩토리 ──────────────────────────────────────────

// NewStore 는 endpoints가 제공되면 etcd 저장소를 생성하고,
// 없으면 인메모리 저장소로 폴백한다.
//
// endpoints 형식: 쉼표 구분 문자열 (예: "localhost:2379,node2:2379")
// 환경변수: HCV_ETCD_ENDPOINTS
//
// 폴백 동작:
//   - endpoints 빈 문자열 → 인메모리 사용
//   - etcd 연결 실패 → 인메모리로 자동 전환 (로그 출력)
func NewStore(endpoints string) Store {
	if endpoints == "" {
		slog.Info("store: using in-memory store (no etcd endpoints)")
		return NewMemoryStore()
	}
	eps := strings.Split(endpoints, ",")
	store, err := NewEtcdStore(EtcdConfig{Endpoints: eps})
	if err != nil {
		slog.Warn("store: etcd unavailable, falling back to in-memory", "error", err)
		return NewMemoryStore()
	}
	return store
}
