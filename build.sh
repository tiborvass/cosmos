#!/usr/bin/env bash

set -ueo pipefail

# Build the combined container with claude and proxy
docker buildx build -t cosmos .