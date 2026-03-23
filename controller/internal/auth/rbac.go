// Package auth — RBAC 접근 제어 + 감사 로깅 미들웨어
//
// 아키텍처 위치: HTTP 미들웨어 체인의 일부
//   RequestID → Audit → Logging → Metrics → RBAC → Security → CORS → Recovery → Handler
//
// 역할 계층 (Role Hierarchy):
//   - admin: 전체 권한 (모든 HTTP 메서드, 모든 경로)
//   - operator: 읽기 + 쓰기 (GET, POST, PUT, DELETE)
//   - viewer: 읽기 전용 (GET만 허용)
//
// 인증 없이 접근 가능한 경로:
//   - /healthz: 헬스 체크
//   - /metrics: Prometheus 메트릭
//   - /api/v1/auth/login: JWT 로그인 (인증 전 접근 필요)
//
// 인증 방식 우선순위:
//  1. Authorization: Bearer <JWT> — JWT 토큰 검증
//  2. Authorization: Basic <base64> — UserDB(bcrypt) 검증
//  3. Legacy: HCV_RBAC_USERS 환경변수 기반 평문 비밀번호 비교 (UserDB nil인 경우)
//
// 환경변수:
//   - HCV_RBAC_USERS: 사용자 정의 ("user1:pass1:admin,user2:pass2:viewer")
//   - 미설정 시 RBAC 비활성 (모든 요청 허용)
package auth

import (
	"net/http"
	"os"
	"strings"
)

// Role 은 사용자의 접근 권한 수준을 나타낸다.
// admin > operator > viewer 순으로 권한이 축소된다.
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

// RBACUser 는 사용자의 인증 정보와 역할을 보관한다.
type RBACUser struct {
	Username string
	Password string
	Role     Role
}

// RBACConfig holds optional dependencies for the RBAC middleware.
// When JWTService and/or UserDB are provided, they are used for
// Bearer and Basic Auth respectively. Otherwise, the legacy
// plaintext user map is used.
type RBACConfig struct {
	JWTService *JWTService
	UserDB     *UserDB
}

// HasPermission 은 역할이 지정된 HTTP 메서드와 경로에 접근 가능한지 확인한다.
//
// 역할별 권한:
//   - admin: 모든 작업 허용
//   - operator: 읽기 + 쓰기 (GET, POST, PUT, DELETE)
//   - viewer: 읽기 전용 (GET만 허용)
//
// 동시 호출 안전성: 안전 (상태 변경 없음)
func HasPermission(role Role, method, path string) bool {
	switch role {
	case RoleAdmin:
		return true
	case RoleOperator:
		if method == http.MethodGet {
			return true
		}
		if method == http.MethodPost || method == http.MethodPut || method == http.MethodDelete {
			return true
		}
		return false
	case RoleViewer:
		return method == http.MethodGet
	default:
		return false
	}
}

// RBACMiddleware 는 역할 기반 접근 제어를 적용하는 미들웨어를 반환한다.
//
// 처리 순서:
//  1. /healthz, /metrics, /api/v1/auth/login 경로는 인증 없이 통과
//  2. Authorization: Bearer 헤더가 있으면 JWT 토큰 검증 (JWTService 필요)
//  3. Authorization: Basic 헤더가 있으면 UserDB(bcrypt) 검증, 없으면 레거시 평문 비교
//  4. HasPermission으로 권한 확인 (실패 시 403)
func RBACMiddleware(users map[string]RBACUser, cfgs ...*RBACConfig) func(http.Handler) http.Handler {
	var cfg *RBACConfig
	if len(cfgs) > 0 {
		cfg = cfgs[0]
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for healthz, metrics, and auth login
			if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" || r.URL.Path == "/api/v1/auth/login" {
				next.ServeHTTP(w, r)
				return
			}

			var role Role
			authenticated := false

			authHeader := r.Header.Get("Authorization")

			// 1. Try JWT Bearer token
			if cfg != nil && cfg.JWTService != nil && strings.HasPrefix(authHeader, "Bearer ") {
				tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
				claims, err := cfg.JWTService.ValidateToken(tokenStr)
				if err == nil {
					role = Role(claims.Role)
					authenticated = true
				}
			}

			// 2. Try Basic Auth
			if !authenticated {
				username, password, ok := r.BasicAuth()
				if ok {
					// 2a. Try UserDB (bcrypt) if available
					if cfg != nil && cfg.UserDB != nil {
						user, err := cfg.UserDB.VerifyPassword(username, password)
						if err == nil {
							role = Role(user.Role)
							authenticated = true
						}
					}

					// 2b. Legacy plaintext fallback (when UserDB is nil)
					if !authenticated && users != nil {
						user, exists := users[username]
						if exists && user.Password == password {
							role = user.Role
							authenticated = true
						}
					}
				}
			}

			if !authenticated {
				w.Header().Set("WWW-Authenticate", `Bearer realm="hardcorevisor", Basic realm="hardcorevisor"`)
				http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
				return
			}

			if !HasPermission(role, r.Method, r.URL.Path) {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeadersMiddleware sets security-related HTTP response headers.
//
// Headers set:
//   - X-Content-Type-Options: nosniff
//   - X-Frame-Options: DENY
//   - X-XSS-Protection: 1; mode=block
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		next.ServeHTTP(w, r)
	})
}

// LoadUsers 는 HCV_RBAC_USERS 환경변수에서 RBAC 사용자를 읽는다.
// 형식: "user1:pass1:admin,user2:pass2:viewer"
// 환경변수 미설정 또는 빈 문자열이면 nil 반환 (RBAC 비활성).
func LoadUsers() map[string]RBACUser {
	return ParseUsers(os.Getenv("HCV_RBAC_USERS"))
}

// ParseUsers 는 RBAC 사용자 문자열을 파싱한다.
// 형식: "user1:pass1:admin,user2:pass2:viewer" (쉼표로 구분, 콜론으로 필드 분리)
// 유효하지 않은 항목(필드 부족, 알 수 없는 역할)은 무시한다.
// 입력이 빈 문자열이거나 유효한 항목이 없으면 nil을 반환한다.
func ParseUsers(raw string) map[string]RBACUser {
	if raw == "" {
		return nil
	}

	users := make(map[string]RBACUser)
	entries := strings.Split(raw, ",")
	for _, entry := range entries {
		parts := strings.SplitN(strings.TrimSpace(entry), ":", 3)
		if len(parts) != 3 {
			continue
		}
		role := Role(strings.TrimSpace(parts[2]))
		if role != RoleAdmin && role != RoleOperator && role != RoleViewer {
			continue
		}
		users[strings.TrimSpace(parts[0])] = RBACUser{
			Username: strings.TrimSpace(parts[0]),
			Password: strings.TrimSpace(parts[1]),
			Role:     role,
		}
	}

	if len(users) == 0 {
		return nil
	}
	return users
}
