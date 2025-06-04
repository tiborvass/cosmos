#!/usr/bin/env bash

# Tail the proxy logs from inside the running container

echo "=== Tailing Cosmos Proxy Logs ==="

# Find the running cosmos container
CONTAINER_ID=$(docker ps -q --filter "ancestor=cosmos" | head -1)

if [ -z "$CONTAINER_ID" ]; then
    echo "Error: No running cosmos container found"
    echo ""
    echo "Start cosmos first with: go run main.go claude"
    exit 1
fi

echo "Container: $CONTAINER_ID"
echo "Log file: /cosmos/proxy.log (inside container)"
echo ""

# Clear the log file if requested
if [ "$1" = "--clear" ]; then
    docker exec "$CONTAINER_ID" sh -c "> /cosmos/proxy.log"
    echo "Log file cleared"
fi

# Tail the logs from inside the container
docker exec "$CONTAINER_ID" tail -f /cosmos/proxy.log