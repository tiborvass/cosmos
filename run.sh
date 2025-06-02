#!/usr/bin/env bash

set -xueo pipefail

#alias cosmos='dagger run ./cosmos'
go build
dagger run ./cosmos claude
