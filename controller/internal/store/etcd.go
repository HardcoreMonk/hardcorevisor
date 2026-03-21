// Package store — etcd-backed key-value state store
//
// Provides a simple KV abstraction over etcd v3 for persisting
// Controller state (VMs, storage, network config, etc.).
// Falls back to in-memory store if etcd is unavailable.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// Store is the interface for state persistence.
type Store interface {
	Put(ctx context.Context, key string, value any) error
	Get(ctx context.Context, key string, out any) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]KV, error)
	Close() error
}

// KV represents a key-value pair.
type KV struct {
	Key   string
	Value []byte
}

// ── etcd Store ───────────────────────────────────────

// EtcdStore implements Store backed by etcd v3.
type EtcdStore struct {
	client *clientv3.Client
	prefix string // key namespace, e.g. "/hcv/"
}

// EtcdConfig holds etcd connection settings.
type EtcdConfig struct {
	Endpoints   []string      // e.g. ["localhost:2379"]
	DialTimeout time.Duration // default 5s
	Prefix      string        // key prefix, default "/hcv/"
}

// NewEtcdStore connects to etcd and returns a Store.
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

	log.Printf("etcd store connected: endpoints=%v prefix=%s", cfg.Endpoints, cfg.Prefix)
	return &EtcdStore{client: client, prefix: cfg.Prefix}, nil
}

func (s *EtcdStore) key(k string) string {
	return s.prefix + k
}

func (s *EtcdStore) Put(ctx context.Context, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	_, err = s.client.Put(ctx, s.key(key), string(data))
	return err
}

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

func (s *EtcdStore) Delete(ctx context.Context, key string) error {
	_, err := s.client.Delete(ctx, s.key(key))
	return err
}

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

func (s *EtcdStore) Close() error {
	return s.client.Close()
}

// ── In-memory Store (fallback) ───────────────────────

// MemoryStore implements Store as an in-memory map (no persistence).
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemoryStore creates a non-persistent in-memory store.
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

// ── Factory ──────────────────────────────────────────

// NewStore creates an etcd store if endpoints are provided,
// otherwise falls back to in-memory store.
func NewStore(endpoints string) Store {
	if endpoints == "" {
		log.Println("store: using in-memory store (no etcd endpoints)")
		return NewMemoryStore()
	}
	eps := strings.Split(endpoints, ",")
	store, err := NewEtcdStore(EtcdConfig{Endpoints: eps})
	if err != nil {
		log.Printf("store: etcd unavailable (%v), falling back to in-memory", err)
		return NewMemoryStore()
	}
	return store
}
