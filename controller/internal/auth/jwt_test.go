package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWT_GenerateAndValidate(t *testing.T) {
	svc := NewJWTService("test-secret-key-32bytes-long!!!!", 1*time.Hour)

	token, err := svc.GenerateToken("alice", "admin")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	claims, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if claims.Username != "alice" {
		t.Errorf("expected username alice, got %s", claims.Username)
	}
	if claims.Role != "admin" {
		t.Errorf("expected role admin, got %s", claims.Role)
	}
	if claims.JTI == "" {
		t.Error("expected non-empty JTI")
	}
	if claims.Subject != "alice" {
		t.Errorf("expected subject alice, got %s", claims.Subject)
	}
}

func TestJWT_Expired(t *testing.T) {
	svc := NewJWTService("test-secret", 0) // 0 TTL = already expired

	// Manually create a token that's already expired
	now := time.Now().Add(-2 * time.Hour)
	claims := &Claims{
		Username: "bob",
		Role:     "viewer",
		JTI:      "test-jti",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "bob",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(1 * time.Second)),
			ID:        "test-jti",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	_, err = svc.ValidateToken(signed)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestJWT_Revoked(t *testing.T) {
	svc := NewJWTService("test-secret-for-revoke", 1*time.Hour)

	token, err := svc.GenerateToken("carol", "operator")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	// Validate before revocation — should succeed
	claims, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken before revoke: %v", err)
	}

	// Revoke and try again
	svc.RevokeToken(claims.JTI)

	if !svc.IsRevoked(claims.JTI) {
		t.Error("expected JTI to be revoked")
	}

	_, err = svc.ValidateToken(token)
	if err == nil {
		t.Error("expected error for revoked token")
	}
}

func TestJWT_InvalidSignature(t *testing.T) {
	svc1 := NewJWTService("key-one", 1*time.Hour)
	svc2 := NewJWTService("key-two-different", 1*time.Hour)

	token, err := svc1.GenerateToken("dave", "admin")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	// Validate with wrong key
	_, err = svc2.ValidateToken(token)
	if err == nil {
		t.Error("expected error for invalid signature")
	}
}

func TestJWT_AutoGenerateKey(t *testing.T) {
	// Empty secret should auto-generate a random key
	svc := NewJWTService("", 1*time.Hour)

	token, err := svc.GenerateToken("eve", "viewer")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	claims, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if claims.Username != "eve" {
		t.Errorf("expected eve, got %s", claims.Username)
	}
}
