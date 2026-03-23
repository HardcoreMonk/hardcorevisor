#!/bin/bash
# Build .deb package for HardCoreVisor
set -e
cd "$(dirname "$0")/.."

echo "=== HardCoreVisor .deb Package Builder ==="
echo ""

# Check for dpkg-buildpackage
if ! command -v dpkg-buildpackage &>/dev/null; then
    echo "Error: dpkg-buildpackage not found. Install with:"
    echo "  sudo apt-get install dpkg-dev debhelper"
    exit 1
fi

echo "Building .deb package..."
dpkg-buildpackage -us -uc -b

echo ""
echo "Package built successfully:"
ls -la ../*.deb 2>/dev/null || echo "  (check parent directory for .deb files)"
