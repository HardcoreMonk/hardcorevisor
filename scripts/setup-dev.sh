#!/usr/bin/env bash
set -euo pipefail

echo "=== HardCoreVisor Dev Environment Check ==="
echo ""

check() { command -v "$1" &>/dev/null && echo "✓ $1: $($1 --version 2>&1 | head -1)" || echo "✗ $1 not found"; }

check rustc
check cargo
check go
check nvim
check just
check protoc
check docker
check git
check tmux

echo ""
[ -c /dev/kvm ] && echo "✓ KVM: /dev/kvm available" || echo "⚠ KVM not available"

echo ""
echo "=== Initializing Go dependencies ==="
cd controller && go mod tidy && cd ..
echo "✓ go mod tidy OK"

echo ""
echo "=== Building project ==="
just build && echo "✓ Build OK" || echo "✗ Build FAILED"
echo ""
echo "=== Running tests ==="
just test && echo "✓ Tests OK" || echo "✗ Tests FAILED"
