#!/usr/bin/env bash

set -ueo pipefail

docker build --push -t tiborvass/coding-proxy .
