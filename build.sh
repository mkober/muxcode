#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"

# Build Go binary and install everything (scripts, agents, configs)
make -C "$REPO_DIR" install

# Roll the new binary out to all running session daemons. Long-lived daemons
# keep executing the code loaded at their launch — without this, subsessions
# run stale daemon code until their next manual relaunch. Best-effort: never
# fail the build over it.
if command -v muxcode >/dev/null 2>&1; then
  muxcode upgrade-daemons || echo "Warning: daemon upgrade failed — running daemons remain on the old binary" >&2
fi
