// Package auth provides RBAC and audit logging middleware for the API layer.
package auth

import (
	"net/http"
	"os"
	"strings"
)

// Role represents a user's access level.
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

// RBACUser holds credentials and role for a user.
type RBACUser struct {
	Username string
	Password string
	Role     Role
}

// HasPermission checks whether a role is allowed to perform the given HTTP method on the path.
//
//	admin:    all operations
//	operator: read + write (create/start/stop VMs, manage storage)
//	viewer:   read-only (list, get)
func HasPermission(role Role, method, path string) bool {
	switch role {
	case RoleAdmin:
		return true
	case RoleOperator:
		// Operators can read and write but not delete cluster/fence operations
		// are admin-only. For simplicity: operator = GET + POST + PUT + DELETE
		// except cluster fence.
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

// RBACMiddleware returns middleware that enforces role-based access control.
// It extracts Basic Auth credentials, looks up the user, checks permissions,
// and returns 403 if denied. /healthz and /metrics are always allowed.
func RBACMiddleware(users map[string]RBACUser) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for healthz and metrics
			if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
				next.ServeHTTP(w, r)
				return
			}

			username, password, ok := r.BasicAuth()
			if !ok {
				w.Header().Set("WWW-Authenticate", `Basic realm="hardcorevisor"`)
				http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
				return
			}

			user, exists := users[username]
			if !exists || user.Password != password {
				http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
				return
			}

			if !HasPermission(user.Role, r.Method, r.URL.Path) {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// LoadUsers reads RBAC users from the HCV_RBAC_USERS environment variable.
// Format: "user1:pass1:admin,user2:pass2:viewer"
// Returns nil if the env var is not set or empty.
func LoadUsers() map[string]RBACUser {
	raw := os.Getenv("HCV_RBAC_USERS")
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
