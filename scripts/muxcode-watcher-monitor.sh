#!/usr/bin/env bash
# muxcode-watcher-monitor.sh — monitors the bus watcher and restarts it if stale.
# Runs as a background loop alongside the watcher, launched by muxcode.sh.
#
# Usage: muxcode-watcher-monitor.sh <session>

SESSION="${1:?Usage: muxcode-watcher-monitor.sh <session>}"
KEEPALIVE="/tmp/muxcode-bus-${SESSION}/watcher.keepalive"
MAX_AGE=30  # seconds before considering keepalive stale

while true; do
  sleep 15

  # Exit if tmux session no longer exists
  if ! tmux has-session -t "$SESSION" 2>/dev/null; then
    exit 0
  fi

  # Skip if keepalive file doesn't exist yet (watcher may be starting)
  if [ ! -f "$KEEPALIVE" ]; then
    continue
  fi

  # Read timestamp and check staleness
  ts=$(cat "$KEEPALIVE" 2>/dev/null)
  if [ -z "$ts" ]; then
    continue
  fi

  now=$(date +%s)
  age=$(( now - ts ))

  if [ "$age" -gt "$MAX_AGE" ]; then
    echo "  [monitor] Watcher keepalive stale (${age}s > ${MAX_AGE}s) — restarting"

    # Kill stale watcher
    pkill -f "muxcode-agent-bus watch $SESSION" 2>/dev/null || true
    sleep 0.2

    # Relaunch watcher
    muxcode-agent-bus watch "$SESSION" &>/dev/null &
  fi
done
