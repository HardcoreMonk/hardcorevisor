#!/usr/bin/env bash
# HardCoreVisor — Docker Stack Smoke Test
#
# Verifies all services are healthy and REST API responds correctly.
# Usage: ./scripts/stack-smoke-test.sh [--build] [--down]
#
#   --build   Build Docker images before starting
#   --down    Tear down stack after test

set -euo pipefail

cd "$(dirname "$0")/.."

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log()  { echo -e "${CYAN}[STACK]${NC} $*"; }
pass() { echo -e "${GREEN}[PASS]${NC} $*"; }
fail() { echo -e "${RED}[FAIL]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }

TOTAL=0
PASSED=0
FAILED=0

run_test() {
    local name="$1"; shift
    TOTAL=$((TOTAL + 1))
    if "$@" > /dev/null 2>&1; then
        pass "$name"
        PASSED=$((PASSED + 1))
    else
        fail "$name"
        FAILED=$((FAILED + 1))
    fi
}

DO_BUILD=false
DO_DOWN=false
for arg in "$@"; do
    case $arg in
        --build) DO_BUILD=true ;;
        --down)  DO_DOWN=true ;;
    esac
done

COMPOSE="docker compose -f deploy/docker-compose.yml"

echo ""
echo "═══════════════════════════════════════════════════════"
echo "  HardCoreVisor Docker Stack Smoke Test"
echo "═══════════════════════════════════════════════════════"
echo ""

# ── Phase 1: Build & Start ────────────────────────────

if [ "$DO_BUILD" = true ]; then
    log "Building Docker images..."
    $COMPOSE build 2>&1 | tail -5
fi

log "Starting stack..."
$COMPOSE up -d 2>&1

# ── Phase 2: Wait for services ────────────────────────

log "Waiting for services to be ready..."
MAX_WAIT=60
WAITED=0
while [ $WAITED -lt $MAX_WAIT ]; do
    if curl -sf http://localhost:18080/healthz > /dev/null 2>&1; then
        log "Controller ready after ${WAITED}s"
        break
    fi
    sleep 2
    WAITED=$((WAITED + 2))
    echo -n "."
done
echo ""

if [ $WAITED -ge $MAX_WAIT ]; then
    fail "Controller did not become ready within ${MAX_WAIT}s"
    log "Container logs:"
    $COMPOSE logs controller 2>&1 | tail -20
    exit 1
fi

# ── Phase 3: Service Health Checks ────────────────────

log "Phase 1: Service Health Checks"

run_test "Controller healthz" \
    curl -sf http://localhost:18080/healthz

run_test "etcd health" \
    curl -sf http://localhost:2379/health

run_test "Prometheus ready" \
    curl -sf http://localhost:9090/-/ready

run_test "Grafana health" \
    curl -sf http://localhost:3000/api/health

echo ""

# ── Phase 4: REST API Smoke Tests ─────────────────────

log "Phase 2: REST API Smoke Tests"

run_test "GET /api/v1/version" \
    curl -sf http://localhost:18080/api/v1/version

run_test "GET /api/v1/backends" \
    curl -sf http://localhost:18080/api/v1/backends

# VM lifecycle
run_test "POST /api/v1/vms (create)" \
    curl -sf -X POST http://localhost:18080/api/v1/vms \
        -H 'Content-Type: application/json' \
        -d '{"name":"smoke-test-vm","vcpus":2,"memory_mb":4096}'

run_test "GET /api/v1/vms (list)" \
    curl -sf http://localhost:18080/api/v1/vms

run_test "POST /api/v1/vms/1/start" \
    curl -sf -X POST http://localhost:18080/api/v1/vms/1/start

run_test "POST /api/v1/vms/1/stop" \
    curl -sf -X POST http://localhost:18080/api/v1/vms/1/stop

# QEMU backend
run_test "POST /api/v1/vms (qemu backend)" \
    curl -sf -X POST http://localhost:18080/api/v1/vms \
        -H 'Content-Type: application/json' \
        -d '{"name":"qemu-smoke","vcpus":4,"memory_mb":8192,"backend":"qemu"}'

# Services
run_test "GET /api/v1/storage/pools" \
    curl -sf http://localhost:18080/api/v1/storage/pools

run_test "GET /api/v1/network/zones" \
    curl -sf http://localhost:18080/api/v1/network/zones

run_test "GET /api/v1/devices" \
    curl -sf http://localhost:18080/api/v1/devices

run_test "GET /api/v1/cluster/status" \
    curl -sf http://localhost:18080/api/v1/cluster/status

run_test "GET /api/v1/cluster/nodes" \
    curl -sf http://localhost:18080/api/v1/cluster/nodes

run_test "GET /api/v1/nodes" \
    curl -sf http://localhost:18080/api/v1/nodes

# Backup API
run_test "POST /api/v1/backups (create)" \
    curl -sf -X POST http://localhost:18080/api/v1/backups \
        -H 'Content-Type: application/json' \
        -d '{"vm_id":1,"vm_name":"smoke-test-vm","pool":"local-zfs"}'

run_test "GET /api/v1/backups (list)" \
    curl -sf http://localhost:18080/api/v1/backups

# System
run_test "GET /api/v1/system/stats" \
    curl -sf http://localhost:18080/api/v1/system/stats

run_test "GET /api/v1/api-info" \
    curl -sf http://localhost:18080/api/v1/api-info

# Network details
run_test "GET /api/v1/network/vnets" \
    curl -sf http://localhost:18080/api/v1/network/vnets

run_test "GET /api/v1/network/firewall" \
    curl -sf http://localhost:18080/api/v1/network/firewall

# VM Migration
run_test "POST /api/v1/vms/1/migrate" \
    curl -sf -X POST http://localhost:18080/api/v1/vms/1/migrate \
        -H 'Content-Type: application/json' \
        -d '{"target_node":"node-02"}'

echo ""

# ── Phase 5: Container status ─────────────────────────

log "Phase 3: Container Status"
$COMPOSE ps --format "table {{.Name}}\t{{.Status}}\t{{.Ports}}" 2>/dev/null || $COMPOSE ps

echo ""

# ── Phase 6: Teardown (optional) ──────────────────────

if [ "$DO_DOWN" = true ]; then
    log "Tearing down stack..."
    $COMPOSE down 2>&1
fi

# ── Summary ───────────────────────────────────────────

echo ""
echo "═══════════════════════════════════════════════════════"
echo "  Results: $PASSED/$TOTAL passed, $FAILED failed"
echo "═══════════════════════════════════════════════════════"

if [ $FAILED -gt 0 ]; then
    fail "Stack smoke test FAILED"
    exit 1
else
    pass "All stack smoke tests PASSED"
fi
