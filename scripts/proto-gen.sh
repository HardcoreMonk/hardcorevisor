#!/usr/bin/env bash
set -euo pipefail

PROTO_DIR="proto"
GO_OUT="controller/pkg/proto"

echo "=== Generating Go code from proto files ==="

for proto in "$PROTO_DIR"/*.proto; do
    name=$(basename "$proto" .proto)
    outdir="${GO_OUT}/${name}pb"
    mkdir -p "$outdir"
    protoc \
        --proto_path="$PROTO_DIR" \
        --go_out="$outdir" --go_opt=paths=source_relative \
        --go-grpc_out="$outdir" --go-grpc_opt=paths=source_relative \
        "$proto"
    echo "  ✓ ${name}.proto → ${outdir}/"
done

echo "=== Proto generation complete ==="
