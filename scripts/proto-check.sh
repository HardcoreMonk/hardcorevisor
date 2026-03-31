#!/usr/bin/env bash
# Proto 동기화 검증 — proto-gen 실행 후 diff가 있으면 실패
# CI에서 proto 정의와 생성 코드의 불일치를 감지한다.
set -euo pipefail

cd "$(dirname "$0")/.."

echo "=== Proto Sync Check ==="

# proto-gen 실행
./scripts/proto-gen.sh

# 생성 코드에 변경이 있는지 확인
if ! git diff --exit-code controller/pkg/proto/ > /dev/null 2>&1; then
    echo "FAIL: Proto generated code is out of sync."
    echo "Run 'just proto-gen' and commit the changes."
    git diff --stat controller/pkg/proto/
    exit 1
fi

echo "PASS: Proto generated code is in sync."
