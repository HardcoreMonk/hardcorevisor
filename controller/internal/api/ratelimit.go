// ratelimit.go — 토큰 버킷 기반 API 속도 제한.
//
// HCV_RATE_LIMIT 환경변수로 초당 허용 요청 수를 설정한다.
// /healthz와 /metrics 경로는 속도 제한에서 제외된다.
// 제한 초과 시 429 Too Many Requests + Retry-After 헤더를 반환한다.
package api

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimiter — 토큰 버킷 알고리즘 기반의 속도 제한기.
//
// 시간이 경과하면 refillRate만큼 토큰이 보충된다.
// 요청 1개당 토큰 1개를 소비하며, 토큰이 부족하면 요청을 거부한다.
type RateLimiter struct {
	mu         sync.Mutex
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
}

// NewRateLimiter — 속도 제한기를 생성한다.
//
// # 매개변수
//   - rate: 초당 토큰 보충 속도 (초당 허용 요청 수)
//   - burst: 최대 버스트 크기 (한 번에 허용되는 최대 요청 수)
func NewRateLimiter(rate float64, burst int) *RateLimiter {
	return &RateLimiter{
		tokens:     float64(burst),
		maxTokens:  float64(burst),
		refillRate: rate,
		lastRefill: time.Now(),
	}
}

// Allow — 요청을 허용할지 판단한다. 토큰을 1개 소비하고 true를 반환한다.
// 토큰이 부족하면 false를 반환한다 (요청 거부).
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastRefill).Seconds()
	rl.lastRefill = now

	rl.tokens += elapsed * rl.refillRate
	if rl.tokens > rl.maxTokens {
		rl.tokens = rl.maxTokens
	}

	if rl.tokens < 1 {
		return false
	}
	rl.tokens--
	return true
}

// Remaining — 현재 남은 토큰 수를 반환한다 (X-RateLimit-Remaining 헤더용).
func (rl *RateLimiter) Remaining() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return int(rl.tokens)
}

// RateLimitMiddleware — 속도 제한 미들웨어를 반환한다.
// /healthz와 /metrics 경로는 속도 제한에서 제외한다.
// 응답 헤더에 X-RateLimit-Limit과 X-RateLimit-Remaining을 항상 포함한다.
// 제한 초과 시 429 + Retry-After: 1 헤더를 반환한다.
func RateLimitMiddleware(limiter *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip health and metrics
			if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(int(limiter.maxTokens)))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(limiter.Remaining()))

			if !limiter.Allow() {
				w.Header().Set("Retry-After", "1")
				writeError(w, http.StatusTooManyRequests, ErrCodeTooManyRequests, "rate limit exceeded")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
