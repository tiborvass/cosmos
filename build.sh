#!/usr/bin/env bash

set -ueo pipefail

docker buildx build --target claude -t cosmos-agent:claude .
docker buildx build --target manager -t cosmos-manager .

#docker buildx build --push --target coding-proxy -t tiborvass/coding-proxy --platform linux/amd64,linux/arm64 .
