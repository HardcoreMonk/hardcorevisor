// ResilientStore 유닛 테스트 — Circuit Breaker 래핑 검증
package store

import (
	"context"
	"testing"
)

// TestResilientStore_PassThrough — 정상 동작 시 내부 MemoryStore로 요청 전달 확인
func TestResilientStore_PassThrough(t *testing.T) {
	inner := NewMemoryStore()
	rs := NewResilientStore(inner)
	ctx := context.Background()

	type item struct{ Name string }

	// Put + Get
	if err := rs.Put(ctx, "test/1", item{Name: "hello"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	var out item
	if err := rs.Get(ctx, "test/1", &out); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if out.Name != "hello" {
		t.Errorf("expected 'hello', got %q", out.Name)
	}

	// List
	kvs, err := rs.List(ctx, "test/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(kvs) != 1 {
		t.Errorf("expected 1 kv, got %d", len(kvs))
	}

	// Delete
	if err := rs.Delete(ctx, "test/1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Close
	if err := rs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestResilientStore_CircuitState — 초기 상태는 closed
func TestResilientStore_CircuitState(t *testing.T) {
	inner := NewMemoryStore()
	rs := NewResilientStore(inner)
	if rs.CircuitState() != "closed" {
		t.Errorf("expected closed, got %s", rs.CircuitState())
	}
}
