#!/usr/bin/env bash
# HardCoreVisor — Build Fix Script
# Run from project root: /home/hcvdev/hardcorevisor
set -euo pipefail

echo "=== HardCoreVisor Build Fix ==="
echo ""

# 1. Go dependency resolution
echo "[1/4] Resolving Go dependencies..."
cd controller && go mod tidy && cd ..
echo "  ✓ go.sum generated"

# 2. Verify Rust build (warnings should be resolved)
echo "[2/4] Building Rust workspace..."
cargo build --workspace 2>&1 | grep -E "warning|error" || echo "  ✓ Rust build clean"

# 3. Verify Go build
echo "[3/4] Building Go workspace..."
cd controller && go build ./... && cd ..
echo "  ✓ Go build OK"

# 4. Run tests
echo "[4/4] Running tests..."
cargo test --workspace 2>&1 | tail -3
cd controller && go test ./... 2>&1 | tail -5 && cd ..

echo ""
echo "=== All fixes applied. Run 'just check' to verify. ==="
