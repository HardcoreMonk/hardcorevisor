// JWT 토큰 서비스 — HardCoreVisor 인증용 토큰 발급/검증/폐기
//
// 아키텍처 위치: auth 패키지 내부, RBAC 미들웨어 및 인증 API 핸들러와 연동
//   로그인 → JWTService.GenerateToken() → 클라이언트에 토큰 반환
//   API 호출 → RBAC 미들웨어 → JWTService.ValidateToken() → 권한 확인
//   로그아웃 → JWTService.RevokeToken() → 블랙리스트에 JTI 등록
//
// 핵심 기능:
//   - HS256 서명 JWT 토큰 생성 (username, role, JTI 클레임 포함)
//   - 토큰 검증: 서명 확인 + 만료 확인 + 블랙리스트 확인
//   - 토큰 폐기: 인메모리 블랙리스트로 JTI(토큰 고유 ID) 관리
//
// 스레드 안전성: sync.RWMutex로 블랙리스트 접근 보호
//
// 의존성:
//   - github.com/golang-jwt/jwt/v5: JWT 토큰 라이브러리
//   - crypto/rand: JTI 랜덤 생성
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims 는 HardCoreVisor JWT 토큰의 커스텀 클레임을 나타낸다.
//
// 필드:
//   - Username: 사용자 이름 (로그인 식별자)
//   - Role: 역할 ("admin", "operator", "viewer") — RBAC 권한 판별에 사용
//   - JTI: 토큰 고유 식별자 (랜덤 16바이트 hex) — 폐기(블랙리스트) 시 사용
//   - RegisteredClaims: 표준 JWT 클레임 (sub, iat, exp, jti)
type Claims struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	JTI      string `json:"jti"`
	jwt.RegisteredClaims
}

// JWTService 는 JWT 토큰의 생성, 검증, 폐기를 관리한다.
//
// 필드:
//   - signingKey: HS256 서명 키 (비밀이 비어 있으면 랜덤 32바이트 자동 생성)
//   - tokenTTL: 토큰 유효 기간 (기본 24시간)
//   - blacklist: 폐기된 토큰의 JTI → 폐기 시각 맵 (인메모리)
//
// 스레드 안전성: mu(RWMutex)로 blacklist 접근 보호. signingKey/tokenTTL은 초기화 후 변경 안 됨.
// 주의: 블랙리스트는 인메모리이므로 Controller 재시작 시 초기화된다.
// 프로덕션에서는 Redis/etcd 기반 블랙리스트로 교체를 권장한다.
type JWTService struct {
	signingKey []byte
	tokenTTL   time.Duration

	mu        sync.RWMutex
	blacklist map[string]time.Time // jti -> expiry
}

// NewJWTService 는 새 JWT 서비스를 생성한다.
//
// 매개변수:
//   - secret: HS256 서명 키 문자열. 빈 문자열이면 랜덤 32바이트 키 자동 생성
//   - ttl: 토큰 유효 기간 (예: 24*time.Hour)
//
// 호출 시점: Controller 초기화 시 (cmd/controller/main.go)
// 보안 참고: 프로덕션에서는 반드시 충분히 긴 secret을 설정해야 한다.
// secret이 빈 문자열이면 재시작 시마다 키가 변경되어 기존 토큰이 무효화된다.
func NewJWTService(secret string, ttl time.Duration) *JWTService {
	key := []byte(secret)
	if len(key) == 0 {
		key = make([]byte, 32)
		rand.Read(key)
	}
	return &JWTService{
		signingKey: key,
		tokenTTL:   ttl,
		blacklist:  make(map[string]time.Time),
	}
}

// GenerateToken 은 지정된 사용자 이름과 역할에 대한 서명된 JWT 토큰을 생성한다.
//
// 토큰 구조 (HS256 서명):
//   - username: 사용자 이름
//   - role: 역할 (RBAC 권한 판별용)
//   - jti: 토큰 고유 ID (폐기 시 식별자)
//   - sub: username과 동일 (표준 클레임)
//   - iat: 발급 시각
//   - exp: 만료 시각 (iat + tokenTTL)
//
// 호출 시점: POST /api/v1/auth/login, POST /api/v1/auth/refresh
// 에러 조건: 서명 실패 (이론적으로 발생하지 않음)
func (j *JWTService) GenerateToken(username, role string) (string, error) {
	jti := generateJTI()
	now := time.Now()
	claims := &Claims{
		Username: username,
		Role:     role,
		JTI:      jti,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(j.tokenTTL)),
			ID:        jti,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(j.signingKey)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// ValidateToken 은 JWT 토큰 문자열을 파싱하고 검증한다.
//
// 검증 순서:
//  1. HS256 서명 확인 (다른 서명 방식이면 거부)
//  2. 토큰 만료 확인 (exp 클레임)
//  3. 블랙리스트 확인 (JTI가 폐기 목록에 있는지)
//
// 반환값: 유효한 토큰이면 Claims 포인터, 아니면 에러
// 호출 시점: RBAC 미들웨어 (Bearer 토큰 검증), refresh/logout 핸들러
// 에러 조건: 서명 불일치, 토큰 만료, 토큰 폐기됨, 잘못된 형식
func (j *JWTService) ValidateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return j.signingKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	if j.IsRevoked(claims.JTI) {
		return nil, fmt.Errorf("token has been revoked")
	}

	return claims, nil
}

// RevokeToken 은 주어진 JTI를 인메모리 블랙리스트에 추가하여 토큰을 폐기한다.
//
// 폐기된 토큰은 ValidateToken에서 거부된다.
// 호출 시점: POST /api/v1/auth/logout
// 스레드 안전성: 안전 (mu.Lock 사용)
// 주의: 인메모리이므로 Controller 재시작 시 블랙리스트가 초기화된다.
func (j *JWTService) RevokeToken(jti string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.blacklist[jti] = time.Now()
}

// IsRevoked 는 토큰 JTI가 블랙리스트에 있는지 (폐기되었는지) 확인한다.
//
// 스레드 안전성: 안전 (mu.RLock 사용)
func (j *JWTService) IsRevoked(jti string) bool {
	j.mu.RLock()
	defer j.mu.RUnlock()
	_, revoked := j.blacklist[jti]
	return revoked
}

// TokenTTL 은 설정된 토큰 유효 기간(Time-To-Live)을 반환한다.
// 로그인/갱신 응답에서 expires_at 계산에 사용된다.
func (j *JWTService) TokenTTL() time.Duration {
	return j.tokenTTL
}

// generateJTI 는 랜덤 16바이트를 hex 인코딩하여 토큰 고유 식별자(JTI)를 생성한다.
// 32문자 hex 문자열을 반환한다. crypto/rand를 사용하므로 예측 불가능하다.
func generateJTI() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
