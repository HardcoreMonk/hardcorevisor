package auth

import (
	"context"
	"testing"
)

// TestOAuth2_GetAuthorizationURL — OAuth2 인증 URL 생성을 검증한다.
//
// 시나리오:
//   1. 시뮬레이션 모드에서 인증 URL 생성
//   2. 프로덕션 모드에서 인증 URL 생성
//   3. URL에 필수 파라미터(client_id, redirect_uri, state)가 포함되는지 확인
func TestOAuth2_GetAuthorizationURL(t *testing.T) {
	// 시뮬레이션 모드 (ProviderURL 미설정)
	provider, err := NewOAuth2Provider(OAuth2Config{
		ClientID:    "test-client",
		RedirectURL: "http://localhost:18080/api/v1/auth/oauth2/callback",
	})
	if err != nil {
		t.Fatalf("NewOAuth2Provider: %v", err)
	}

	url := provider.GetAuthorizationURL("test-state")
	if url == "" {
		t.Fatal("expected non-empty authorization URL")
	}
	// 시뮬레이션 모드: RedirectURL 기반 URL에 code와 state가 포함되어야 함
	if !contains(url, "code=") {
		t.Errorf("simulated URL should contain code= parameter: %s", url)
	}
	if !contains(url, "state=test-state") {
		t.Errorf("URL should contain state parameter: %s", url)
	}

	// 프로덕션 모드 (ProviderURL 설정)
	provider2, err := NewOAuth2Provider(OAuth2Config{
		ProviderURL: "https://keycloak.example.com/realms/hcv",
		ClientID:    "hcv-app",
		RedirectURL: "http://localhost:18080/api/v1/auth/oauth2/callback",
		Scopes:      []string{"openid", "profile"},
	})
	if err != nil {
		t.Fatalf("NewOAuth2Provider (production): %v", err)
	}

	url2 := provider2.GetAuthorizationURL("prod-state")
	if !contains(url2, "keycloak.example.com") {
		t.Errorf("URL should contain provider host: %s", url2)
	}
	if !contains(url2, "client_id=hcv-app") {
		t.Errorf("URL should contain client_id: %s", url2)
	}
	if !contains(url2, "state=prod-state") {
		t.Errorf("URL should contain state: %s", url2)
	}
	if !contains(url2, "response_type=code") {
		t.Errorf("URL should contain response_type=code: %s", url2)
	}
}

// TestOAuth2_ExchangeCode — 시뮬레이션 모드에서 인증 코드 교환을 검증한다.
//
// 시나리오:
//   1. 인증 URL 생성 (코드 등록)
//   2. 코드로 토큰 교환 (성공)
//   3. 같은 코드로 재교환 (실패 — 1회 사용)
//   4. 잘못된 코드로 교환 (실패)
func TestOAuth2_ExchangeCode(t *testing.T) {
	provider, err := NewOAuth2Provider(OAuth2Config{
		ClientID:    "test-client",
		RedirectURL: "http://localhost:18080/callback",
	})
	if err != nil {
		t.Fatalf("NewOAuth2Provider: %v", err)
	}

	// 1. 인증 URL 생성하여 시뮬레이션 코드 등록
	url := provider.GetAuthorizationURL("state1")
	// URL에서 code 파라미터 추출
	code := extractParam(url, "code")
	if code == "" {
		t.Fatalf("failed to extract code from URL: %s", url)
	}

	// 2. 코드 교환 — 성공
	ctx := context.Background()
	token, err := provider.ExchangeCode(ctx, code)
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if token.AccessToken == "" {
		t.Error("expected non-empty access token")
	}
	if token.RefreshToken == "" {
		t.Error("expected non-empty refresh token")
	}
	if token.UserInfo.Email != "user@example.com" {
		t.Errorf("expected email user@example.com, got %s", token.UserInfo.Email)
	}
	if token.UserInfo.Provider != "simulated" {
		t.Errorf("expected provider simulated, got %s", token.UserInfo.Provider)
	}

	// 3. 같은 코드로 재교환 — 실패 (1회 사용)
	_, err = provider.ExchangeCode(ctx, code)
	if err == nil {
		t.Error("expected error when reusing code")
	}

	// 4. 잘못된 코드 — 실패
	_, err = provider.ExchangeCode(ctx, "invalid-code")
	if err == nil {
		t.Error("expected error for invalid code")
	}
}

// TestOAuth2_ValidateIDToken — 시뮬레이션 ID 토큰 검증을 확인한다.
func TestOAuth2_ValidateIDToken(t *testing.T) {
	provider, err := NewOAuth2Provider(OAuth2Config{
		ClientID:    "test-client",
		RedirectURL: "http://localhost:18080/callback",
	})
	if err != nil {
		t.Fatalf("NewOAuth2Provider: %v", err)
	}

	// 유효한 토큰
	info, err := provider.ValidateIDToken("admin@hcv.io:Admin User:admin,devops")
	if err != nil {
		t.Fatalf("ValidateIDToken: %v", err)
	}
	if info.Email != "admin@hcv.io" {
		t.Errorf("expected email admin@hcv.io, got %s", info.Email)
	}
	if info.Name != "Admin User" {
		t.Errorf("expected name Admin User, got %s", info.Name)
	}
	if len(info.Groups) != 2 || info.Groups[0] != "admin" {
		t.Errorf("expected groups [admin devops], got %v", info.Groups)
	}

	// 유효하지 않은 토큰 형식
	_, err = provider.ValidateIDToken("invalid")
	if err == nil {
		t.Error("expected error for invalid token format")
	}
}

// TestOAuth2_ConfigValidation — OAuth2Config 유효성 검사를 확인한다.
func TestOAuth2_ConfigValidation(t *testing.T) {
	// ClientID 누락
	_, err := NewOAuth2Provider(OAuth2Config{
		RedirectURL: "http://localhost:18080/callback",
	})
	if err == nil {
		t.Error("expected error for missing client_id")
	}

	// RedirectURL 누락
	_, err = NewOAuth2Provider(OAuth2Config{
		ClientID: "test",
	})
	if err == nil {
		t.Error("expected error for missing redirect_url")
	}

	// 유효하지 않은 ProviderURL
	_, err = NewOAuth2Provider(OAuth2Config{
		ClientID:    "test",
		RedirectURL: "http://localhost:18080/callback",
		ProviderURL: "not a url",
	})
	if err == nil {
		t.Error("expected error for invalid provider_url")
	}

	// 기본 Scopes 자동 적용
	p, err := NewOAuth2Provider(OAuth2Config{
		ClientID:    "test",
		RedirectURL: "http://localhost:18080/callback",
	})
	if err != nil {
		t.Fatalf("NewOAuth2Provider: %v", err)
	}
	cfg := p.Config()
	if len(cfg.Scopes) != 3 {
		t.Errorf("expected 3 default scopes, got %d", len(cfg.Scopes))
	}
}

// contains 는 문자열에 부분 문자열이 포함되어 있는지 확인한다.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// extractParam 는 URL 쿼리 문자열에서 파라미터 값을 추출한다.
func extractParam(rawURL, param string) string {
	// "?" 이후의 쿼리 문자열을 추출
	qIdx := -1
	for i := range rawURL {
		if rawURL[i] == '?' {
			qIdx = i
			break
		}
	}
	if qIdx < 0 {
		return ""
	}
	query := rawURL[qIdx+1:]
	// "&"로 분리하여 각 파라미터 확인
	prefix := param + "="
	start := 0
	for start <= len(query) {
		end := start
		for end < len(query) && query[end] != '&' {
			end++
		}
		part := query[start:end]
		if len(part) >= len(prefix) && part[:len(prefix)] == prefix {
			return part[len(prefix):]
		}
		start = end + 1
	}
	return ""
}
