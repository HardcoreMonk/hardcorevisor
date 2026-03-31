// 인증 API 핸들러 — JWT 로그인, 토큰 갱신, 로그아웃, 사용자 관리
//
// # 아키텍처 위치
//
// RBAC 미들웨어와 연동하여 JWT 기반 인증을 처리한다.
// /api/v1/auth/login은 RBAC 미들웨어에서 인증 없이 통과하도록 예외 처리되어 있다.
//
// # 인증 흐름
//
//	클라이언트 → POST /api/v1/auth/login (username, password)
//	           ← JWT 토큰 발급
//	클라이언트 → Authorization: Bearer <JWT> 로 API 호출
//	           ← RBAC 미들웨어가 토큰 검증 + 역할 확인
//
// # 엔드포인트
//
//   - POST   /api/v1/auth/login             — 사용자 인증 후 JWT 토큰 발급
//   - POST   /api/v1/auth/refresh           — 기존 토큰의 만료 시간 갱신
//   - POST   /api/v1/auth/logout            — 현재 토큰 폐기 (JTI 블랙리스트)
//   - GET    /api/v1/auth/users             — 사용자 목록 조회 (admin 전용)
//   - POST   /api/v1/auth/users             — 사용자 생성 (admin 전용)
//   - DELETE /api/v1/auth/users/{username}  — 사용자 삭제 (admin 전용)
//
// # 의존성
//
//   - auth.UserDB: bbolt 기반 사용자 DB (bcrypt 해시 비밀번호)
//   - auth.JWTService: HMAC-SHA256 JWT 토큰 생성/검증/폐기
package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// loginLimiter — 로그인 엔드포인트 전용 rate limiter.
// 분당 10회로 제한하여 브루트포스 공격을 방어한다.
var loginLimiter = NewRateLimiter(10.0/60.0, 10) // 10 req/min, burst 10

// handleAuthLogin — 사용자 인증 후 JWT 토큰을 발급한다 (POST /api/v1/auth/login).
//
// 요청 본문: {"username": "...", "password": "..."}
// 성공 응답: {"token": "eyJ...", "expires_at": "2026-03-24T00:00:00Z"}
//
// 처리 순서:
//  1. UserDB.VerifyPassword()로 bcrypt 해시 비밀번호 검증
//  2. JWTService.GenerateToken()으로 HMAC-SHA256 JWT 발급
//  3. 토큰과 만료 시각을 JSON으로 반환
//
// 에러 응답:
//   - 400: 요청 본문 파싱 실패 또는 필수 필드 누락
//   - 401: 잘못된 인증 정보 (사용자 없음 또는 비밀번호 불일치)
//   - 500: JWT 토큰 생성 실패 (내부 에러)
func (svc *Services) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !loginLimiter.Allow() {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, ErrCodeTooManyRequests,
			"login rate limit exceeded, try again in 60 seconds")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Username == "" || req.Password == "" {
		http.Error(w, `{"error":"username and password required"}`, http.StatusBadRequest)
		return
	}

	user, err := svc.Auth.UserDB.VerifyPassword(req.Username, req.Password)
	if err != nil {
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	token, err := svc.Auth.JWTService.GenerateToken(user.Username, user.Role)
	if err != nil {
		http.Error(w, `{"error":"failed to generate token"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":      token,
		"expires_at": time.Now().Add(svc.Auth.JWTService.TokenTTL()).Format(time.RFC3339),
	})
}

// handleAuthRefresh — 기존 JWT 토큰의 만료 시간을 갱신한다 (POST /api/v1/auth/refresh).
//
// Authorization: Bearer <기존토큰> 헤더 필요.
// 기존 토큰의 claims(사용자, 역할)을 유지하면서 새로운 만료 시간이 적용된 토큰을 발급한다.
//
// 에러 응답:
//   - 401: Bearer 토큰 누락 또는 만료/무효
//   - 500: 새 토큰 생성 실패
func (svc *Services) handleAuthRefresh(w http.ResponseWriter, r *http.Request) {
	tokenStr := extractBearerToken(r)
	if tokenStr == "" {
		http.Error(w, `{"error":"bearer token required"}`, http.StatusUnauthorized)
		return
	}

	claims, err := svc.Auth.JWTService.ValidateToken(tokenStr)
	if err != nil {
		http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
		return
	}

	newToken, err := svc.Auth.JWTService.GenerateToken(claims.Username, claims.Role)
	if err != nil {
		http.Error(w, `{"error":"failed to generate token"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":      newToken,
		"expires_at": time.Now().Add(svc.Auth.JWTService.TokenTTL()).Format(time.RFC3339),
	})
}

// handleAuthLogout — 현재 JWT 토큰을 폐기한다 (POST /api/v1/auth/logout).
//
// Authorization: Bearer <토큰> 헤더 필요.
// 토큰의 JTI(JWT ID)를 블랙리스트에 추가하여 이후 사용을 차단한다.
// 블랙리스트는 인메모리이므로 Controller 재시작 시 초기화된다.
//
// 에러 응답:
//   - 401: Bearer 토큰 누락 또는 만료/무효
func (svc *Services) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	tokenStr := extractBearerToken(r)
	if tokenStr == "" {
		http.Error(w, `{"error":"bearer token required"}`, http.StatusUnauthorized)
		return
	}

	claims, err := svc.Auth.JWTService.ValidateToken(tokenStr)
	if err != nil {
		http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
		return
	}

	svc.Auth.JWTService.RevokeToken(claims.JTI)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// handleAuthListUsers — 등록된 사용자 목록을 반환한다 (GET /api/v1/auth/users).
//
// admin 역할만 접근 가능하다 (requireAdminFromRequest 검증).
// 비밀번호 해시는 json:"-" 태그로 응답에서 제외된다.
func (svc *Services) handleAuthListUsers(w http.ResponseWriter, r *http.Request) {
	if !svc.requireAdminFromRequest(w, r) {
		return
	}

	users, err := svc.Auth.UserDB.ListUsers()
	if err != nil {
		http.Error(w, `{"error":"failed to list users"}`, http.StatusInternalServerError)
		return
	}

	// Return users without password hashes (json:"-" tag handles this)
	writeJSON(w, http.StatusOK, users)
}

// handleAuthCreateUser — 새 사용자를 생성한다 (POST /api/v1/auth/users).
//
// admin 역할만 접근 가능하다.
// 요청 본문: {"username": "...", "password": "...", "role": "admin|operator|viewer"}
// role 미지정 시 기본값 "viewer"를 사용한다.
// 비밀번호는 bcrypt로 해시되어 UserDB(bbolt)에 저장된다.
//
// 에러 응답:
//   - 400: 필수 필드 누락
//   - 401/403: 인증 실패 또는 권한 부족
//   - 409: 동일 username이 이미 존재
func (svc *Services) handleAuthCreateUser(w http.ResponseWriter, r *http.Request) {
	if !svc.requireAdminFromRequest(w, r) {
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Username == "" || req.Password == "" {
		http.Error(w, `{"error":"username and password required"}`, http.StatusBadRequest)
		return
	}
	if req.Role == "" {
		req.Role = "viewer"
	}

	if err := svc.Auth.UserDB.CreateUser(req.Username, req.Password, req.Role); err != nil {
		http.Error(w, `{"error":"failed to create user: `+err.Error()+`"}`, http.StatusConflict)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"username": req.Username,
		"role":     req.Role,
		"status":   "created",
	})
}

// handleAuthDeleteUser — 사용자를 삭제한다 (DELETE /api/v1/auth/users/{username}).
//
// admin 역할만 접근 가능하다.
// 성공 시 204 No Content를 반환한다. 존재하지 않는 사용자면 404를 반환한다.
func (svc *Services) handleAuthDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !svc.requireAdminFromRequest(w, r) {
		return
	}

	username := r.PathValue("username")
	if username == "" {
		http.Error(w, `{"error":"username required"}`, http.StatusBadRequest)
		return
	}

	if err := svc.Auth.UserDB.DeleteUser(username); err != nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// extractBearerToken — Authorization 헤더에서 Bearer 토큰 문자열을 추출한다.
// "Bearer " 접두사가 없으면 빈 문자열을 반환한다.
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// requireAdminFromRequest — 요청이 admin 역할의 사용자로부터 온 것인지 검증한다.
//
// 인증 확인 순서:
//  1. JWT Bearer 토큰 → JWTService.ValidateToken()으로 검증
//  2. Basic Auth → UserDB.VerifyPassword()로 검증
//
// admin이 아니면 403 Forbidden, 인증 실패 시 401 Unauthorized를 응답에 쓰고 false를 반환한다.
// 호출자는 false 반환 시 추가 처리 없이 즉시 반환해야 한다.
func (svc *Services) requireAdminFromRequest(w http.ResponseWriter, r *http.Request) bool {
	// Try JWT Bearer token first
	if tokenStr := extractBearerToken(r); tokenStr != "" {
		claims, err := svc.Auth.JWTService.ValidateToken(tokenStr)
		if err != nil {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return false
		}
		if claims.Role != "admin" {
			http.Error(w, `{"error":"admin access required"}`, http.StatusForbidden)
			return false
		}
		return true
	}

	// Try Basic Auth fallback
	username, password, ok := r.BasicAuth()
	if !ok {
		http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
		return false
	}
	user, err := svc.Auth.UserDB.VerifyPassword(username, password)
	if err != nil {
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return false
	}
	if user.Role != "admin" {
		http.Error(w, `{"error":"admin access required"}`, http.StatusForbidden)
		return false
	}
	return true
}
