package store

import (
	"context"
	"testing"
)

// testItem — 테스트용 간단한 구조체
type testItem struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

// TestMemoryStore_PutGet — Put 후 Get으로 동일한 데이터를 복원할 수 있는지 검증한다.
func TestMemoryStore_PutGet(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	item := testItem{Name: "web-01", Value: 42}
	if err := s.Put(ctx, "vms/1", item); err != nil {
		t.Fatalf("Put 실패: %v", err)
	}

	var got testItem
	if err := s.Get(ctx, "vms/1", &got); err != nil {
		t.Fatalf("Get 실패: %v", err)
	}

	if got.Name != item.Name {
		t.Errorf("Name 불일치: got=%q, want=%q", got.Name, item.Name)
	}
	if got.Value != item.Value {
		t.Errorf("Value 불일치: got=%d, want=%d", got.Value, item.Value)
	}
}

// TestMemoryStore_GetNotFound — 존재하지 않는 키 조회 시 에러를 반환한다.
func TestMemoryStore_GetNotFound(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	var got testItem
	err := s.Get(ctx, "nonexistent", &got)
	if err == nil {
		t.Fatal("존재하지 않는 키에 대해 에러가 반환되어야 한다")
	}
}

// TestMemoryStore_Delete — Put 후 Delete, 이후 Get이 실패하는지 검증한다.
func TestMemoryStore_Delete(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	item := testItem{Name: "to-delete", Value: 1}
	if err := s.Put(ctx, "vms/99", item); err != nil {
		t.Fatalf("Put 실패: %v", err)
	}

	if err := s.Delete(ctx, "vms/99"); err != nil {
		t.Fatalf("Delete 실패: %v", err)
	}

	var got testItem
	if err := s.Get(ctx, "vms/99", &got); err == nil {
		t.Fatal("삭제된 키에 대해 Get이 에러를 반환해야 한다")
	}
}

// TestMemoryStore_DeleteNotFound — 존재하지 않는 키 삭제 시 에러가 발생하지 않는다.
func TestMemoryStore_DeleteNotFound(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	if err := s.Delete(ctx, "nonexistent-key"); err != nil {
		t.Fatalf("존재하지 않는 키 삭제에서 에러 발생: %v", err)
	}
}

// TestMemoryStore_List — 접두사 기반 필터링이 올바르게 동작하는지 검증한다.
func TestMemoryStore_List(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	// "vms/" 접두사 3개, "nodes/" 접두사 1개 저장
	s.Put(ctx, "vms/1", testItem{Name: "vm-1", Value: 1})
	s.Put(ctx, "vms/2", testItem{Name: "vm-2", Value: 2})
	s.Put(ctx, "vms/3", testItem{Name: "vm-3", Value: 3})
	s.Put(ctx, "nodes/node-1", testItem{Name: "node-1", Value: 10})

	result, err := s.List(ctx, "vms/")
	if err != nil {
		t.Fatalf("List 실패: %v", err)
	}

	if len(result) != 3 {
		t.Errorf("vms/ 접두사 결과 수 불일치: got=%d, want=3", len(result))
	}

	// nodes/ 접두사 필터링 검증
	nodeResult, err := s.List(ctx, "nodes/")
	if err != nil {
		t.Fatalf("List(nodes/) 실패: %v", err)
	}
	if len(nodeResult) != 1 {
		t.Errorf("nodes/ 접두사 결과 수 불일치: got=%d, want=1", len(nodeResult))
	}
}

// TestMemoryStore_ListEmpty — 빈 접두사에 대해 빈 결과를 반환한다.
func TestMemoryStore_ListEmpty(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	result, err := s.List(ctx, "empty-prefix/")
	if err != nil {
		t.Fatalf("List 실패: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("빈 접두사에 대해 빈 결과를 기대했으나 %d개 반환됨", len(result))
	}
}

// TestMemoryStore_Close — Close는 nil을 반환해야 한다.
func TestMemoryStore_Close(t *testing.T) {
	s := NewMemoryStore()

	if err := s.Close(); err != nil {
		t.Fatalf("Close가 nil이 아닌 에러를 반환: %v", err)
	}
}

// TestNewStore_EmptyEndpoints — 빈 엔드포인트로 NewStore 호출 시 MemoryStore를 반환한다.
func TestNewStore_EmptyEndpoints(t *testing.T) {
	s := NewStore("")

	if _, ok := s.(*MemoryStore); !ok {
		t.Fatalf("빈 엔드포인트에서 *MemoryStore를 기대했으나 %T 반환됨", s)
	}
}

// TestNewStore_InvalidEndpoints — 잘못된 엔드포인트로 NewStore 호출 시 MemoryStore로 폴백한다.
func TestNewStore_InvalidEndpoints(t *testing.T) {
	s := NewStore("invalid:9999")

	if _, ok := s.(*MemoryStore); !ok {
		t.Fatalf("잘못된 엔드포인트에서 *MemoryStore 폴백을 기대했으나 %T 반환됨", s)
	}
}
