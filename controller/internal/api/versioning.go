package api

import (
	"net/http"
	"strings"
)

const (
	CurrentAPIVersion = "v1"
	HeaderAPIVersion  = "X-API-Version"
	HeaderDeprecated  = "X-API-Deprecated"
)

// versionMiddleware adds API version headers to responses
func versionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set version header
		w.Header().Set(HeaderAPIVersion, CurrentAPIVersion)

		// Check if request uses deprecated paths (future v2 migration)
		if isDeprecatedPath(r.URL.Path) {
			w.Header().Set(HeaderDeprecated, "true")
			w.Header().Set("Sunset", "2027-01-01")
		}

		next.ServeHTTP(w, r)
	})
}

func isDeprecatedPath(path string) bool {
	// Currently no deprecated paths
	// When v2 is introduced, v1 paths will be marked here
	_ = strings.HasPrefix(path, "/api/v1/")
	return false
}
