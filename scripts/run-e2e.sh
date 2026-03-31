#!/usr/bin/env bash
# HardCoreVisor — E2E Integration Test Runner
#
# Usage:
#   ./scripts/run-e2e.sh              # Run all E2E tests
#   ./scripts/run-e2e.sh --with-stack # Start Docker stack first
#   ./scripts/run-e2e.sh --rust-only  # Run only Rust vmcore tests
#   ./scripts/run-e2e.sh --go-only    # Run only Go controller tests

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$PROJECT_ROOT"

log()  { echo -e "${CYAN}[E2E]${NC} $*"; }
pass() { echo -e "${GREEN}[PASS]${NC} $*"; }
fail() { echo -e "${RED}[FAIL]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }

TOTAL=0
PASSED=0
FAILED=0

run_step() {
    local name="$1"
    shift
    TOTAL=$((TOTAL + 1))
    log "Running: $name"
    if "$@"; then
        pass "$name"
        PASSED=$((PASSED + 1))
    else
        fail "$name"
        FAILED=$((FAILED + 1))
    fi
}

# ── Parse args ────────────────────────────────────────────

WITH_STACK=false
RUST_ONLY=false
GO_ONLY=false

for arg in "$@"; do
    case $arg in
        --with-stack) WITH_STACK=true ;;
        --rust-only)  RUST_ONLY=true ;;
        --go-only)    GO_ONLY=true ;;
    esac
done

echo ""
echo "═══════════════════════════════════════════════════════"
echo "  HardCoreVisor E2E Integration Tests"
echo "═══════════════════════════════════════════════════════"
echo ""

# ── Phase 1: Rust vmcore ──────────────────────────────────

if [ "$GO_ONLY" = false ]; then
    log "Phase 1: Rust vmcore tests"

    run_step "vmcore build (staticlib + rlib)" \
        cargo build -p vmcore --release

    run_step "vmcore unit tests" \
        cargo test -p vmcore -- --test-threads=1

    run_step "vmcore clippy" \
        cargo clippy -p vmcore -- -D warnings

    run_step "vmcore security audit" \
        cargo audit 2>/dev/null || warn "cargo-audit not installed, skipping"

    # Verify cbindgen header
    if [ -f vmcore/vmcore.h ]; then
        run_step "vmcore.h header exists" \
            test -s vmcore/vmcore.h
        FFI_COUNT=$(grep -c 'hcv_' vmcore/vmcore.h || echo 0)
        log "  vmcore.h contains $FFI_COUNT FFI declarations"
    else
        warn "vmcore.h not found (cbindgen may not have run)"
    fi

    # Verify staticlib (workspace builds to target/ at project root)
    if [ -f target/release/libvmcore.a ]; then
        SIZE=$(du -h target/release/libvmcore.a | cut -f1)
        log "  libvmcore.a size: $SIZE"
        run_step "libvmcore.a exists" \
            test -s target/release/libvmcore.a
    fi

    echo ""
fi

# ── Phase 2: Go Controller ───────────────────────────────

if [ "$RUST_ONLY" = false ]; then
    log "Phase 2: Go Controller tests"

    run_step "go mod tidy" \
        bash -c "cd controller && go mod tidy"

    run_step "go build (all packages)" \
        bash -c "cd controller && go build ./..."

    run_step "go unit tests (race detector)" \
        bash -c "cd controller && go test -race -count=1 ./..."

    run_step "go vet" \
        bash -c "cd controller && go vet ./..."

    # golangci-lint (optional)
    if command -v golangci-lint &>/dev/null; then
        run_step "golangci-lint" \
            bash -c "cd controller && golangci-lint run ./..."
    else
        warn "golangci-lint not installed, skipping"
    fi

    # govulncheck (optional)
    if command -v govulncheck &>/dev/null; then
        run_step "govulncheck" \
            bash -c "cd controller && govulncheck ./..."
    else
        warn "govulncheck not installed, skipping"
    fi

    echo ""
fi

# ── Phase 3: E2E Integration Tests ───────────────────────

if [ "$RUST_ONLY" = false ]; then
    log "Phase 3: E2E Integration Tests"

    # Copy E2E tests into controller if not already there
    if [ -d tests ]; then
        mkdir -p controller/tests
        cp tests/e2e_vm_lifecycle_test.go controller/tests/ 2>/dev/null || true
    fi

    run_step "E2E: VM lifecycle + backend selection + concurrency" \
        bash -c "cd controller && go test -race -count=1 -v -run 'TestE2E' ./tests/..."

    echo ""
fi

# ── Phase 4: Docker Stack (optional) ─────────────────────

if [ "$WITH_STACK" = true ]; then
    log "Phase 4: Docker Compose Stack"

    run_step "docker compose build" \
        docker compose -f deploy/docker-compose.yml build

    run_step "docker compose up" \
        docker compose -f deploy/docker-compose.yml up -d

    log "Waiting for services to start..."
    sleep 5

    # Health checks
    run_step "controller healthz" \
        curl -sf http://localhost:18080/healthz

    run_step "etcd health" \
        curl -sf http://localhost:2379/health

    run_step "prometheus targets" \
        curl -sf http://localhost:9090/-/ready

    run_step "grafana health" \
        curl -sf http://localhost:3000/api/health

    # E2E against running stack
    run_step "E2E against live controller" \
        bash -c "
            curl -sf http://localhost:18080/api/v1/version &&
            curl -sf -X POST http://localhost:18080/api/v1/vms \
                -H 'Content-Type: application/json' \
                -d '{\"name\":\"e2e-live\",\"vcpus\":2,\"memory_mb\":4096}' &&
            curl -sf http://localhost:18080/api/v1/vms
        "

    echo ""
    log "Stack is running. To stop: docker compose -f deploy/docker-compose.yml down"
fi

# ── Summary ───────────────────────────────────────────────

echo ""
echo "═══════════════════════════════════════════════════════"
echo "  Results: $PASSED/$TOTAL passed, $FAILED failed"
echo "═══════════════════════════════════════════════════════"

if [ $FAILED -gt 0 ]; then
    fail "E2E tests FAILED"
    exit 1
else
    pass "All E2E tests PASSED"
    exit 0
fi
