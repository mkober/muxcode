#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"

# Build Go binary and install everything (scripts, agents, configs)
make -C "$REPO_DIR" install

# Roll the new binary out to all running session daemons. Long-lived daemons
# keep executing the code loaded at their launch — without this, subsessions
# run stale daemon code until their next manual relaunch. Best-effort: never
# fail the build over it.
# MUXCODE_SKIP_DAEMON_UPGRADE=1 suppresses the rollout. Required by any build
# that installs to a throwaway prefix (see scripts/test-install.sh): otherwise
# upgrade-daemons repoints the *live* session's daemons at the temporary binary,
# which then vanishes with the sandbox.
if [ "${MUXCODE_SKIP_DAEMON_UPGRADE:-}" = "1" ]; then
  echo "Skipping daemon upgrade (MUXCODE_SKIP_DAEMON_UPGRADE=1)"
elif command -v muxcode >/dev/null 2>&1; then
  muxcode upgrade-daemons || echo "Warning: daemon upgrade failed — running daemons remain on the old binary" >&2
fi
