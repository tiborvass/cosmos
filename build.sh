#!/usr/bin/env bash

set -ueo pipefail

docker buildx build --push --target coding-proxy -t tiborvass/coding-proxy --platform linux/amd64,linux/arm64 .
