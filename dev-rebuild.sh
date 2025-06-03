#!/usr/bin/env bash

set -e

echo "=== Rebuilding Cosmos ==="

# Stop any running containers
echo "Stopping any running cosmos containers..."
docker ps -q --filter "ancestor=cosmos" | xargs -r docker stop

# Rebuild
echo "Building new image..."
./build.sh

echo ""
echo "✓ Build complete"
echo ""
echo "To test:"
echo "  Terminal 1: ./dev-tail-logs.sh"
echo "  Terminal 2: go run main.go claude"