#!/usr/bin/env bash
# io_uring I/O Benchmark
# Runs the Rust criterion benchmarks and summarizes
set -euo pipefail

echo "=== io_uring I/O Benchmark ==="
echo ""
cargo bench -p vmcore -- io_engine 2>&1 | grep -E "time:|thrpt:" || echo "Run 'cargo bench -p vmcore' for full results"
echo ""
echo "=== Benchmark Complete ==="
