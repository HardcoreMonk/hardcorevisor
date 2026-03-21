package auth

import (
	"log/slog"
	"net/http"
	"time"
)

// AuditEntry represents a single audit log record.
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

// AuditLogger writes structured audit log entries.
type AuditLogger struct{}

// NewAuditLogger creates a new AuditLogger.
func NewAuditLogger() *AuditLogger {
	return &AuditLogger{}
}

// Log writes an audit entry using slog structured logging.
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

// statusCapture wraps http.ResponseWriter to capture the status code.
type statusCapture struct {
	http.ResponseWriter
	code int
}

func (sc *statusCapture) WriteHeader(code int) {
	sc.code = code
	sc.ResponseWriter.WriteHeader(code)
}

// AuditMiddleware returns middleware that logs every request as a structured audit entry.
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
