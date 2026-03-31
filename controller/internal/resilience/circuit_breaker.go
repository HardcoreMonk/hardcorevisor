// Package resilience — 장애 격리 패턴 (Circuit Breaker)
//
// 외부 의존성(etcd, QEMU, LXC) 장애 시 연쇄 실패를 방지한다.
// 3상태 모델: Closed (정상) → Open (차단) → HalfOpen (시험)
//
// 사용법:
//
//	cb := resilience.NewCircuitBreaker("etcd", 5, 30*time.Second)
//	err := cb.Execute(func() error {
//	    return etcdClient.Put(ctx, key, value)
//	})
package resilience

import (
	"errors"
	"sync"
	"time"
)

// State — 서킷 브레이커 상태
type State int

const (
	StateClosed   State = iota // 정상 — 요청 통과
	StateOpen                  // 차단 — 요청 즉시 거부
	StateHalfOpen              // 시험 — 1개 요청만 통과하여 복구 확인
)

var ErrCircuitOpen = errors.New("circuit breaker is open")

// CircuitBreaker — 3상태 서킷 브레이커.
// failureThreshold회 연속 실패 시 Open으로 전환하고,
// resetTimeout 경과 후 HalfOpen으로 전환하여 복구를 시험한다.
type CircuitBreaker struct {
	mu               sync.Mutex
	name             string
	state            State
	failureCount     int
	failureThreshold int
	resetTimeout     time.Duration
	lastFailure      time.Time
}

// NewCircuitBreaker — 새 서킷 브레이커를 생성한다.
func NewCircuitBreaker(name string, threshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		name:             name,
		state:            StateClosed,
		failureThreshold: threshold,
		resetTimeout:     timeout,
	}
}

// Execute — 보호된 함수를 실행한다.
// Open 상태이면 ErrCircuitOpen을 즉시 반환한다.
func (cb *CircuitBreaker) Execute(fn func() error) error {
	cb.mu.Lock()

	switch cb.state {
	case StateOpen:
		// resetTimeout 경과 시 HalfOpen으로 전환
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			cb.state = StateHalfOpen
		} else {
			cb.mu.Unlock()
			return ErrCircuitOpen
		}
	case StateHalfOpen:
		// HalfOpen 상태에서는 1개만 통과
	}

	cb.mu.Unlock()

	err := fn()

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.failureCount++
		cb.lastFailure = time.Now()
		if cb.failureCount >= cb.failureThreshold {
			cb.state = StateOpen
		}
		return err
	}

	// 성공 시 상태 리셋
	cb.failureCount = 0
	cb.state = StateClosed
	return nil
}

// State — 현재 상태를 반환한다.
func (cb *CircuitBreaker) GetState() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Name — 서킷 브레이커 이름을 반환한다.
func (cb *CircuitBreaker) Name() string {
	return cb.name
}

// StateString — 상태를 문자열로 반환한다.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}
