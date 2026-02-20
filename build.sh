#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"

# Build Go binary and install everything (scripts, agents, configs)
make -C "$REPO_DIR" install
