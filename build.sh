#!/usr/bin/env bash

set -ueo pipefail

# Build the combined container with claude and proxy
GOOS=linux go build -o cosmos-proxy ./proxy
docker buildx build -t cosmos .
