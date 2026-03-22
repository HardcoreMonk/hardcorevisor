// versioning.go — API 버전 관리 미들웨어.
//
// 모든 응답에 X-API-Version 헤더를 부여하고,
// 향후 v2 도입 시 폐기된 v1 경로에 X-API-Deprecated 헤더를 추가한다.
package api

import (
	"net/http"
	"strings"
)

const (
	// CurrentAPIVersion — 현재 API 버전
	CurrentAPIVersion = "v1"
	// HeaderAPIVersion — API 버전을 알리는 응답 헤더명
	HeaderAPIVersion = "X-API-Version"
	// HeaderDeprecated — 폐기된 API 경로임을 알리는 응답 헤더명
	HeaderDeprecated = "X-API-Deprecated"
)

// versionMiddleware — 모든 응답에 API 버전 헤더를 추가하는 미들웨어.
// 폐기된 경로 접근 시 X-API-Deprecated: true 및 Sunset 헤더도 추가한다.
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

// isDeprecatedPath — 주어진 경로가 폐기된 API 경로인지 판별한다.
// 현재는 폐기된 경로가 없다. v2 도입 시 여기에 v1 경로를 등록한다.
func isDeprecatedPath(path string) bool {
	_ = strings.HasPrefix(path, "/api/v1/")
	return false
}
