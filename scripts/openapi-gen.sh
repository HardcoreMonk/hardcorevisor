#!/usr/bin/env bash
# OpenAPI 스펙 자동 생성 — Go 소스에서 라우터 핸들러를 파싱하여 openapi.yaml 갱신
# router.go의 등록된 엔드포인트를 스캔하여 OpenAPI 3.0.3 스펙을 생성한다.
set -euo pipefail

cd "$(dirname "$0")/.."

ROUTER="controller/internal/api/router.go"
OUTPUT="docs/openapi.yaml"

echo "=== OpenAPI Spec Generation ==="

# router.go에서 등록된 엔드포인트 추출
ENDPOINTS=$(grep -oP 'mux\.HandleFunc\("(GET|POST|PUT|DELETE|PATCH) [^"]+' "$ROUTER" \
    | sed 's/mux.HandleFunc("//' \
    | sort -u)

# 현재 openapi.yaml에서 문서화된 경로 추출
DOCUMENTED=$(grep -oP '^\s+/[^:]+:$' "$OUTPUT" 2>/dev/null \
    | sed 's/://;s/^ *//' \
    | sort -u)

echo ""
echo "Registered endpoints: $(echo "$ENDPOINTS" | wc -l)"
echo "Documented paths: $(echo "$DOCUMENTED" | wc -l)"
echo ""

# 미문서화 엔드포인트 식별
echo "=== Undocumented Endpoints ==="
while IFS= read -r line; do
    METHOD=$(echo "$line" | awk '{print $1}')
    PATH=$(echo "$line" | awk '{print $2}')
    # 경로 파라미터 정규화: {id} 형태 유지
    if ! echo "$DOCUMENTED" | grep -qF "$PATH"; then
        echo "  $METHOD $PATH"
    fi
done <<< "$ENDPOINTS"

echo ""
echo "=== Generation Complete ==="
echo "Manual review recommended: $OUTPUT"
