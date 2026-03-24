// Package auth — OAuth2/OIDC 인증 프로바이더
//
// OAuth2 Authorization Code Flow를 지원하는 인증 프로바이더이다.
// 외부 IdP(Keycloak, Google, GitHub 등)와 연동하여 SSO를 구현한다.
//
// 동작 모드:
//   - 시뮬레이션 모드: ProviderURL이 빈 문자열이면 인메모리로 토큰 교환을 시뮬레이션
//   - 프로덕션 모드: ProviderURL이 설정되면 실제 HTTP 호출로 토큰 교환 (향후 구현)
//
// 환경변수:
//   - HCV_OAUTH2_PROVIDER: OIDC 프로바이더 URL (예: https://keycloak.example.com/realms/hcv)
//   - HCV_OAUTH2_CLIENT_ID: OAuth2 클라이언트 ID
//   - HCV_OAUTH2_CLIENT_SECRET: OAuth2 클라이언트 시크릿
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OAuth2Config 는 OAuth2/OIDC 프로바이더 연결 설정을 보관한다.
//
// ProviderURL이 빈 문자열이면 시뮬레이션 모드로 동작한다.
// Scopes가 비어 있으면 기본값 ["openid", "profile", "email"]을 사용한다.
type OAuth2Config struct {
	ProviderURL  string   `yaml:"provider_url" json:"provider_url"`   // OIDC Discovery URL
	ClientID     string   `yaml:"client_id" json:"client_id"`         // OAuth2 클라이언트 ID
	ClientSecret string   `yaml:"client_secret" json:"-"`             // OAuth2 클라이언트 시크릿 (JSON 직렬화 제외)
	RedirectURL  string   `yaml:"redirect_url" json:"redirect_url"`   // 콜백 URL
	Scopes       []string `yaml:"scopes" json:"scopes"`               // 요청 스코프 (기본: openid, profile, email)
}

// OAuth2Token 은 OAuth2 토큰 교환 결과를 나타낸다.
// AccessToken은 API 접근에, RefreshToken은 토큰 갱신에 사용된다.
type OAuth2Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	UserInfo     UserInfo  `json:"user_info"`
}

// UserInfo 는 OIDC에서 가져온 사용자 정보를 나타낸다.
// Groups 필드는 RBAC 역할 매핑에 사용된다.
type UserInfo struct {
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Groups   []string `json:"groups"`
	Provider string   `json:"provider"`
}

// OAuth2Provider 는 OAuth2/OIDC 인증 흐름을 관리하는 프로바이더이다.
//
// 시뮬레이션 모드에서는 인메모리 코드 저장소를 사용하여 토큰 교환을 시뮬레이션한다.
// 프로덕션 모드에서는 실제 IdP의 토큰 엔드포인트로 HTTP 요청을 보낸다.
//
// 동시 호출 안전성: 안전 (sync.RWMutex로 보호)
type OAuth2Provider struct {
	config OAuth2Config

	// 시뮬레이션 모드: 인메모리 코드 저장소 (code → UserInfo 매핑)
	mu    sync.RWMutex
	codes map[string]*UserInfo // authorization code → user info
}

// NewOAuth2Provider 는 OAuth2Config를 검증하고 OAuth2Provider를 생성한다.
//
// 검증 규칙:
//   - ClientID는 필수
//   - RedirectURL은 필수
//   - ProviderURL이 설정된 경우 유효한 URL이어야 함
//   - Scopes가 비어 있으면 기본값 적용
//
// 에러 조건: ClientID 또는 RedirectURL이 빈 문자열, ProviderURL이 유효하지 않은 URL
func NewOAuth2Provider(cfg OAuth2Config) (*OAuth2Provider, error) {
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("oauth2: client_id is required")
	}
	if cfg.RedirectURL == "" {
		return nil, fmt.Errorf("oauth2: redirect_url is required")
	}
	if cfg.ProviderURL != "" {
		if _, err := url.ParseRequestURI(cfg.ProviderURL); err != nil {
			return nil, fmt.Errorf("oauth2: invalid provider_url: %w", err)
		}
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	}
	return &OAuth2Provider{
		config: cfg,
		codes:  make(map[string]*UserInfo),
	}, nil
}

// GetAuthorizationURL 은 OAuth2 인증 요청 URL을 생성한다.
//
// state 파라미터는 CSRF 방지를 위한 임의 문자열이다.
// URL 형식: {ProviderURL}/protocol/openid-connect/auth?response_type=code&client_id=...&redirect_uri=...&scope=...&state=...
//
// ProviderURL이 빈 문자열(시뮬레이션 모드)이면 RedirectURL 기반 시뮬레이션 URL을 반환한다.
func (p *OAuth2Provider) GetAuthorizationURL(state string) string {
	baseURL := p.config.ProviderURL
	if baseURL == "" {
		// 시뮬레이션 모드: 콜백 URL에 시뮬레이션 코드를 직접 포함
		simCode := p.generateSimulatedCode(state)
		return fmt.Sprintf("%s?code=%s&state=%s", p.config.RedirectURL, simCode, state)
	}

	// 프로덕션 모드: OIDC Authorization Endpoint 구성
	authURL := strings.TrimRight(baseURL, "/") + "/protocol/openid-connect/auth"
	params := url.Values{
		"response_type": {"code"},
		"client_id":     {p.config.ClientID},
		"redirect_uri":  {p.config.RedirectURL},
		"scope":         {strings.Join(p.config.Scopes, " ")},
		"state":         {state},
	}
	return authURL + "?" + params.Encode()
}

// ExchangeCode 는 인증 코드를 토큰으로 교환한다.
//
// 시뮬레이션 모드: 인메모리 코드 저장소에서 사용자 정보를 조회하여 토큰 생성.
// 프로덕션 모드: IdP 토큰 엔드포인트로 HTTP POST 요청 (향후 구현).
//
// 에러 조건: 유효하지 않은 코드, 만료된 코드, IdP 연결 실패
// 동시 호출 안전성: 안전 (Lock으로 보호, 코드는 1회 사용 후 삭제)
func (p *OAuth2Provider) ExchangeCode(_ context.Context, code string) (*OAuth2Token, error) {
	// 시뮬레이션 모드: 인메모리 코드 교환
	p.mu.Lock()
	userInfo, ok := p.codes[code]
	if ok {
		delete(p.codes, code) // 코드는 1회 사용
	}
	p.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("oauth2: invalid or expired authorization code")
	}

	// 시뮬레이션 토큰 생성
	accessToken := generateRandomToken()
	refreshToken := generateRandomToken()

	return &OAuth2Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(1 * time.Hour),
		UserInfo:     *userInfo,
	}, nil
}

// ValidateIDToken 은 ID 토큰 문자열을 검증하고 사용자 정보를 추출한다.
//
// 시뮬레이션 모드: 토큰 문자열을 "email:name:group1,group2" 형식으로 파싱한다.
// 프로덕션 모드: JWT 서명 검증 + OIDC 클레임 디코딩 (향후 구현).
//
// 에러 조건: 유효하지 않은 토큰 형식, 서명 검증 실패
func (p *OAuth2Provider) ValidateIDToken(tokenStr string) (*UserInfo, error) {
	// 시뮬레이션 모드: "email:name:group1,group2" 형식 파싱
	parts := strings.SplitN(tokenStr, ":", 3)
	if len(parts) < 2 {
		return nil, fmt.Errorf("oauth2: invalid token format (expected email:name[:groups])")
	}

	info := &UserInfo{
		Email:    parts[0],
		Name:     parts[1],
		Provider: p.ProviderName(),
	}
	if len(parts) == 3 && parts[2] != "" {
		info.Groups = strings.Split(parts[2], ",")
	}
	return info, nil
}

// ProviderName 은 프로바이더 식별 이름을 반환한다.
// ProviderURL이 설정되면 호스트명, 아니면 "simulated"를 반환한다.
func (p *OAuth2Provider) ProviderName() string {
	if p.config.ProviderURL == "" {
		return "simulated"
	}
	u, err := url.Parse(p.config.ProviderURL)
	if err != nil {
		return "unknown"
	}
	return u.Host
}

// Config 는 현재 OAuth2 설정을 반환한다 (읽기 전용).
func (p *OAuth2Provider) Config() OAuth2Config {
	return p.config
}

// generateSimulatedCode 는 시뮬레이션 모드에서 인증 코드를 생성하고
// 인메모리 저장소에 기본 사용자 정보를 등록한다.
func (p *OAuth2Provider) generateSimulatedCode(state string) string {
	code := generateRandomToken()[:16]

	p.mu.Lock()
	p.codes[code] = &UserInfo{
		Email:    "user@example.com",
		Name:     "Simulated User",
		Groups:   []string{"admin"},
		Provider: "simulated",
	}
	p.mu.Unlock()

	_ = state // state는 CSRF 검증용으로 클라이언트가 관리
	return code
}

// generateRandomToken 은 암호학적으로 안전한 랜덤 토큰을 생성한다.
func generateRandomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// 폴백: 타임스탬프 기반 (보안성 낮음, 극히 드문 경우)
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
