#!/usr/bin/env bash
# Quick pre-commit check — runs in <30 seconds
# Usage: ./scripts/quick-check.sh
set -euo pipefail

cd "$(dirname "$0")/.."

echo "=== Quick Check (HardCoreVisor) ==="
echo ""

PASS=0
FAIL=0

step() {
    local name="$1"; shift
    echo -n "[$((PASS+FAIL+1))/8] $name... "
    if "$@" > /dev/null 2>&1; then
        echo "OK"
        PASS=$((PASS+1))
    else
        echo "FAIL"
        FAIL=$((FAIL+1))
    fi
}

step "Rust fmt"     cargo fmt --all -- --check
step "Rust clippy"  cargo clippy --workspace --all-targets -- -D warnings
step "Rust test"    cargo test -p vmcore -- --test-threads=1 -q
step "hcvtui build" cargo build -p hcvtui
step "Go build"     bash -c "cd controller && go build ./..."
step "Go vet"       bash -c "cd controller && go vet ./..."
step "Go test"      bash -c "cd controller && go test -race -count=1 ./..."
step "Go E2E"       bash -c "cd controller && go test -race -count=1 -run TestE2E ./tests/"

echo ""
echo "=== Results: $PASS passed, $FAIL failed (of $((PASS+FAIL))) ==="

if [ "$FAIL" -gt 0 ]; then
    echo "FAILED"
    exit 1
else
    echo "ALL PASSED"
fi
