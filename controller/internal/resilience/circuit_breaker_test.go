package resilience

import (
	"errors"
	"testing"
	"time"
)

func TestCircuitBreaker_ClosedState(t *testing.T) {
	cb := NewCircuitBreaker("test", 3, 100*time.Millisecond)
	err := cb.Execute(func() error { return nil })
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if cb.GetState() != StateClosed {
		t.Fatalf("expected closed, got %s", cb.GetState())
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := NewCircuitBreaker("test", 3, 100*time.Millisecond)
	fail := errors.New("fail")
	for i := 0; i < 3; i++ {
		cb.Execute(func() error { return fail })
	}
	if cb.GetState() != StateOpen {
		t.Fatalf("expected open, got %s", cb.GetState())
	}
	err := cb.Execute(func() error { return nil })
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_HalfOpenRecovery(t *testing.T) {
	cb := NewCircuitBreaker("test", 2, 10*time.Millisecond)
	fail := errors.New("fail")
	cb.Execute(func() error { return fail })
	cb.Execute(func() error { return fail })
	if cb.GetState() != StateOpen {
		t.Fatalf("expected open, got %s", cb.GetState())
	}
	time.Sleep(15 * time.Millisecond)
	// 타임아웃 경과 후 성공하면 Closed로 복구
	err := cb.Execute(func() error { return nil })
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if cb.GetState() != StateClosed {
		t.Fatalf("expected closed after recovery, got %s", cb.GetState())
	}
}

func TestCircuitBreaker_StateString(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half-open"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %s, want %s", tt.state, got, tt.want)
		}
	}
}
