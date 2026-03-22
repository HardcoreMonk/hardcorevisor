// 감사 로깅 — 모든 API 호출을 구조화 JSON으로 기록
//
// 감사 로그 형식 (slog JSON 출력 시):
//
//	{"audit":true, "ts":"2026-01-01T00:00:00Z", "user":"admin",
//	 "method":"POST", "path":"/api/v1/vms", "status":201, "duration_ms":1.2,
//	 "remote_addr":"192.168.1.1:12345"}
//
// 감사 로그는 slog 기본 로거를 통해 출력되므로,
// logging.Setup()에서 설정한 형식(text/json)과 레벨이 적용된다.
package auth

import (
	"log/slog"
	"net/http"
	"time"
)

// AuditEntry 는 단일 감사 로그 레코드를 나타낸다.
type AuditEntry struct {
	Timestamp  time.Time `json:"ts"`
	User       string    `json:"user,omitempty"`
	Role       string    `json:"role,omitempty"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	StatusCode int       `json:"status"`
	DurationMs float64   `json:"duration_ms"`
	RemoteAddr string    `json:"remote_addr"`
}

// AuditLogger 는 구조화된 감사 로그 항목을 출력한다.
type AuditLogger struct{}

// NewAuditLogger 는 새 AuditLogger를 생성한다.
func NewAuditLogger() *AuditLogger {
	return &AuditLogger{}
}

// Log 은 slog 구조화 로깅으로 감사 항목을 출력한다.
// audit=true 필드로 일반 로그와 감사 로그를 구분할 수 있다.
func (a *AuditLogger) Log(entry AuditEntry) {
	attrs := []any{
		slog.Bool("audit", true),
		slog.String("ts", entry.Timestamp.Format(time.RFC3339Nano)),
		slog.String("user", entry.User),
		slog.String("method", entry.Method),
		slog.String("path", entry.Path),
		slog.Int("status", entry.StatusCode),
		slog.Float64("duration_ms", entry.DurationMs),
		slog.String("remote_addr", entry.RemoteAddr),
	}
	if entry.Role != "" {
		attrs = append(attrs, slog.String("role", entry.Role))
	}
	slog.Info("audit", attrs...)
}

// statusCapture 는 HTTP 상태 코드를 캡처하기 위해 http.ResponseWriter를 래핑한다.
type statusCapture struct {
	http.ResponseWriter
	code int
}

func (sc *statusCapture) WriteHeader(code int) {
	sc.code = code
	sc.ResponseWriter.WriteHeader(code)
}

// AuditMiddleware 는 모든 요청을 구조화된 감사 로그로 기록하는 미들웨어를 반환한다.
//
// 기록 항목: 타임스탬프, 사용자(Basic Auth), 메서드, 경로, 상태 코드, 소요 시간, 원격 주소
// 호출 시점: API 라우터 초기화 시 미들웨어 체인에 등록
func AuditMiddleware(logger *AuditLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sc := &statusCapture{ResponseWriter: w, code: http.StatusOK}

			next.ServeHTTP(sc, r)

			duration := time.Since(start)
			user, _, _ := r.BasicAuth()

			logger.Log(AuditEntry{
				Timestamp:  start,
				User:       user,
				Method:     r.Method,
				Path:       r.URL.Path,
				StatusCode: sc.code,
				DurationMs: float64(duration.Microseconds()) / 1000.0,
				RemoteAddr: r.RemoteAddr,
			})
		})
	}
}
