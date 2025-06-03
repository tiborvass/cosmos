#!/usr/bin/env bash

# Extract proxy logs from container (running or stopped)

echo "=== Extracting Cosmos Proxy Logs ==="

# Find container (running or stopped)
CONTAINER_ID=$(docker ps -aq --filter "ancestor=cosmos" | head -1)

if [ -z "$CONTAINER_ID" ]; then
    echo "Error: No cosmos container found (running or stopped)"
    exit 1
fi

# Check if container is running
if docker ps -q --filter "id=$CONTAINER_ID" | grep -q .; then
    echo "Container: $CONTAINER_ID (running)"
else
    echo "Container: $CONTAINER_ID (stopped)"
fi

# Copy logs to host
OUTPUT_FILE="cosmos-proxy-$(date +%Y%m%d-%H%M%S).log"
docker cp "$CONTAINER_ID:/tmp/cosmos-proxy.log" "$OUTPUT_FILE" 2>/dev/null

if [ $? -eq 0 ]; then
    echo "Logs extracted to: $OUTPUT_FILE"
    echo ""
    echo "Preview:"
    head -20 "$OUTPUT_FILE"
else
    echo "No logs found in container"
fi