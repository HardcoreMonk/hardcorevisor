// swagger.go — Swagger UI HTML + OpenAPI YAML 스펙 서빙 핸들러
//
// 아키텍처 위치: API 라우터(router.go)에서 등록, 인증 없이 접근 가능
//   GET /api/v1/docs             → handleSwaggerUI     (Swagger UI HTML 페이지)
//   GET /api/v1/docs/openapi.yaml → handleOpenAPISpec   (OpenAPI 3.0 YAML 스펙)
//
// Swagger UI는 unpkg.com CDN에서 swagger-ui-dist@5를 로드하며,
// Controller에 별도 정적 파일 번들링이 필요 없다.
// OpenAPI 스펙 파일(docs/openapi.yaml)은 디스크에서 한 번 로드 후 sync.Once로 캐시한다.
//
// RBAC 미들웨어에서 /api/v1/docs 경로는 인증 없이 통과하도록 설정되어 있다
// (rbac.go의 skip 경로 목록 참조).
//
// 의존성:
//   - docs/openapi.yaml: REST API 전체 스펙 파일 (1083줄+)
package api

import (
	"net/http"
	"os"
	"sync"
)

// swaggerUIHTML — Swagger UI CDN 기반 HTML 페이지.
// unpkg.com에서 swagger-ui-dist@5를 로드하여 /api/v1/docs/openapi.yaml을 렌더링한다.
const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>HardCoreVisor API Documentation</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
  <style>
    html { box-sizing: border-box; overflow-y: scroll; }
    *, *:before, *:after { box-sizing: inherit; }
    body { margin: 0; background: #fafafa; }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({
      url: '/api/v1/docs/openapi.yaml',
      dom_id: '#swagger-ui',
      deepLinking: true,
      presets: [
        SwaggerUIBundle.presets.apis,
        SwaggerUIBundle.SwaggerUIStandalonePreset
      ],
      layout: "BaseLayout"
    });
  </script>
</body>
</html>`

// openAPISpecPaths — OpenAPI 스펙 파일 탐색 경로 목록.
// Controller 바이너리의 실행 위치에 따라 다른 상대 경로를 시도한다.
// 순서대로 시도하여 첫 번째로 읽기 성공한 파일을 사용한다.
//
// 일반적인 경로:
//   - "docs/openapi.yaml": hardcorevisor/ 루트에서 실행 시
//   - "../docs/openapi.yaml": controller/ 디렉터리에서 실행 시
//   - "../../docs/openapi.yaml": controller/cmd/controller/ 에서 실행 시
var openAPISpecPaths = []string{
	"docs/openapi.yaml",
	"../docs/openapi.yaml",
	"../../docs/openapi.yaml",
}

var (
	cachedSpec     []byte     // 캐시된 OpenAPI 스펙 바이트 (nil이면 미로드 또는 파일 미존재)
	cachedSpecOnce sync.Once  // 스펙 파일을 한 번만 로드하도록 보장
)

// loadOpenAPISpec — OpenAPI 스펙 파일을 디스크에서 로드한다.
//
// openAPISpecPaths의 경로를 순서대로 시도하여 파일을 찾는다.
// sync.Once로 한 번만 실행되며, 이후 호출은 캐시된 결과를 반환한다.
//
// 반환값: 스펙 파일 바이트 슬라이스. 모든 경로에서 파일을 찾지 못하면 nil.
// 스레드 안전성: 안전 (sync.Once 사용)
func loadOpenAPISpec() []byte {
	cachedSpecOnce.Do(func() {
		for _, p := range openAPISpecPaths {
			data, err := os.ReadFile(p)
			if err == nil {
				cachedSpec = data
				return
			}
		}
	})
	return cachedSpec
}

// handleSwaggerUI — Swagger UI HTML 페이지를 서빙한다.
//
// GET /api/v1/docs → 200 text/html (Content-Type: text/html; charset=utf-8)
//
// HTML 페이지는 CDN에서 swagger-ui-dist@5를 로드하고,
// /api/v1/docs/openapi.yaml을 스펙 URL로 지정한다.
// 인증 없이 접근 가능 (RBAC skip 경로).
func handleSwaggerUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(swaggerUIHTML)) //nolint:errcheck
}

// handleOpenAPISpec — OpenAPI YAML 스펙 파일을 서빙한다.
//
// GET /api/v1/docs/openapi.yaml → 200 application/x-yaml
//
// 파일을 찾을 수 없으면 404를 반환한다.
// Access-Control-Allow-Origin: * 헤더를 설정하여 Swagger UI의
// cross-origin 요청을 허용한다 (Swagger UI가 CDN에서 로드되므로 필요).
func handleOpenAPISpec(w http.ResponseWriter, _ *http.Request) {
	spec := loadOpenAPISpec()
	if spec == nil {
		http.Error(w, "OpenAPI spec not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	w.Write(spec) //nolint:errcheck
}
