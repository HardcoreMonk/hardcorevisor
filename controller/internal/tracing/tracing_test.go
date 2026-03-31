// tracing 패키지 유닛 테스트
//
// 테스트 대상: Setup() — 빈 endpoint로 호출 시 no-op 동작 확인
package tracing

import (
	"testing"
)

// TestSetupNoOp — 빈 endpoint로 Setup() 호출 시 에러 없이 no-op shutdown 반환
func TestSetupNoOp(t *testing.T) {
	t.Parallel()

	shutdown, err := Setup("hardcorevisor-controller", "")
	if err != nil {
		t.Fatalf("Setup() 에러: %v (빈 endpoint에서 에러가 발생하면 안 됨)", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown 함수가 nil임")
	}

	// no-op shutdown 호출 시 패닉이 발생하지 않아야 한다
	shutdown()
}

// TestSetupNoOpMultipleCalls — no-op shutdown을 여러 번 호출해도 안전한지 검증
func TestSetupNoOpMultipleCalls(t *testing.T) {
	t.Parallel()

	shutdown, err := Setup("test-service", "")
	if err != nil {
		t.Fatalf("Setup() 에러: %v", err)
	}

	// 여러 번 호출해도 패닉 없어야 함
	shutdown()
	shutdown()
}
