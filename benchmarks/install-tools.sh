#!/usr/bin/env bash

set -euo pipefail

BENCH_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$BENCH_ROOT/bin"
mkdir -p "$BIN_DIR"

echo "Installing Mutago v2.8.1..."
env GOBIN="$BIN_DIR" go install github.com/quality-gates/mutago/v2/cmd/mutago@v2.8.1

echo "Installing Gremlins v0.6.0..."
env GOBIN="$BIN_DIR" go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0

echo "Installed pinned competitor binaries in $BIN_DIR"
