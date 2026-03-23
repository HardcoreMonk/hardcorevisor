#!/bin/bash
# Generate man pages and markdown docs from hcvctl CLI
set -e
cd "$(dirname "$0")/.."

mkdir -p docs/man

echo "Generating man pages..."
cd controller
go run ./cmd/hcvctl gendoc --type man --dir ../docs/man
echo "Man pages generated in docs/man/"

# Optionally generate markdown docs
if [ "${1}" = "--markdown" ]; then
    mkdir -p ../docs/cli
    go run ./cmd/hcvctl gendoc --type markdown --dir ../docs/cli
    echo "Markdown docs generated in docs/cli/"
fi
