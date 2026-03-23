// Package api — Swagger UI 서빙 핸들러
//
// /api/v1/docs 경로에서 Swagger UI HTML 페이지를 서빙하고,
// /api/v1/docs/openapi.yaml 경로에서 OpenAPI 스펙 파일을 제공한다.
// Swagger UI는 CDN(unpkg.com)에서 로드되며, 별도 번들링이 필요 없다.
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
// Controller 바이너리의 실행 위치에 따라 다른 경로를 시도한다.
var openAPISpecPaths = []string{
	"docs/openapi.yaml",
	"../docs/openapi.yaml",
	"../../docs/openapi.yaml",
}

var (
	cachedSpec     []byte
	cachedSpecOnce sync.Once
)

// loadOpenAPISpec — OpenAPI 스펙 파일을 디스크에서 로드한다.
// 여러 상대 경로를 시도하여 파일을 찾고, 한 번 로드되면 캐시한다.
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
// GET /api/v1/docs → 200 text/html
func handleSwaggerUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(swaggerUIHTML)) //nolint:errcheck
}

// handleOpenAPISpec — OpenAPI YAML 스펙 파일을 서빙한다.
//
// GET /api/v1/docs/openapi.yaml → 200 application/x-yaml
// 파일을 찾을 수 없으면 404를 반환한다.
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
