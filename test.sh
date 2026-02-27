#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"

for moddir in "$REPO_DIR"/tools/*/; do
  [ -f "$moddir/go.mod" ] || continue
  name="$(basename "$moddir")"
  echo "=== $name: go vet ==="
  (cd "$moddir" && go vet ./...)
  echo "=== $name: go test ==="
  (cd "$moddir" && go test -v ./...)
done
