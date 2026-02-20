#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/tools/muxcoder-agent-bus"

echo "=== go vet ==="
go vet ./...

echo "=== go test ==="
go test -v ./...
