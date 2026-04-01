#!/usr/bin/env bash
# HardCoreVisor Demo — 전체 스택을 한 번에 시작하고 샘플 데이터를 생성한다.
# Usage: ./scripts/demo.sh
set -euo pipefail

cd "$(dirname "$0")/.."

echo "╔══════════════════════════════════════════════╗"
echo "║     HardCoreVisor v0.1.0 — Demo Setup       ║"
echo "╚══════════════════════════════════════════════╝"
echo ""

# ── 1. 스택 시작 ──
echo "[1/4] Starting Docker stack..."
docker compose -f deploy/docker-compose.yml up -d --build 2>&1 | tail -5
echo ""

# ── 2. 헬스체크 대기 ──
echo "[2/4] Waiting for Controller..."
for i in $(seq 1 30); do
    if curl -sf http://localhost:18080/healthz > /dev/null 2>&1; then
        echo "  Controller ready!"
        break
    fi
    sleep 1
    printf "."
done
echo ""

# ── 3. 샘플 VM 생성 ──
echo "[3/4] Creating sample VMs..."
curl -sf -X POST http://localhost:18080/api/v1/vms \
    -H 'Content-Type: application/json' \
    -d '{"name":"web-server","vcpus":2,"memory_mb":4096}' | python3 -m json.tool 2>/dev/null || true

curl -sf -X POST http://localhost:18080/api/v1/vms \
    -H 'Content-Type: application/json' \
    -d '{"name":"db-server","vcpus":4,"memory_mb":8192,"backend":"qemu"}' | python3 -m json.tool 2>/dev/null || true

curl -sf -X POST http://localhost:18080/api/v1/vms \
    -H 'Content-Type: application/json' \
    -d '{"name":"app-container","vcpus":1,"memory_mb":512,"type":"container"}' | python3 -m json.tool 2>/dev/null || true

# VM 시작
curl -sf -X POST http://localhost:18080/api/v1/vms/1/start > /dev/null 2>&1 || true
curl -sf -X POST http://localhost:18080/api/v1/vms/2/start > /dev/null 2>&1 || true
echo ""

# ── 4. 결과 표시 ──
echo "[4/4] Demo ready!"
echo ""
echo "╔══════════════════════════════════════════════╗"
echo "║  REST API   http://localhost:18080           ║"
echo "║  Swagger UI http://localhost:18080/api/v1/docs║"
echo "║  Grafana    http://localhost:3000 (admin/admin)║"
echo "║  Prometheus http://localhost:9090             ║"
echo "║  gRPC       localhost:19090                  ║"
echo "╚══════════════════════════════════════════════╝"
echo ""
echo "Try:"
echo "  curl http://localhost:18080/api/v1/vms | python3 -m json.tool"
echo "  curl http://localhost:18080/api/v1/system/stats | python3 -m json.tool"
echo "  curl http://localhost:18080/api/v1/cluster/status | python3 -m json.tool"
echo ""
echo "Stop: docker compose -f deploy/docker-compose.yml down"
