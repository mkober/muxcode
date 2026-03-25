#!/usr/bin/env bash
# muxcode-watcher-monitor.sh — monitors the bus watcher and restarts it if stale.
# Runs as a background loop alongside the watcher, launched by muxcode.sh.
#
# Usage: muxcode-watcher-monitor.sh <session>

SESSION="${1:?Usage: muxcode-watcher-monitor.sh <session>}"
KEEPALIVE="/tmp/muxcode-bus-${SESSION}/watcher.keepalive"
MAX_AGE=30  # seconds before considering keepalive stale

# Lifecycle logging helper (same as muxcode.sh)
lifecycle_log() {
  local level="$1" source="$2" event="$3" detail="${4:-}"
  local args=("$SESSION" "$level" "$source" "$event")
  [ -n "$detail" ] && args+=(--detail "$detail")
  muxcode lifecycle log "${args[@]}" 2>/dev/null || true
}

while true; do
  sleep 15

  # Exit if tmux session no longer exists
  if ! tmux has-session -t "$SESSION" 2>/dev/null; then
    lifecycle_log "info" "monitor" "session-gone" "tmux session $SESSION no longer exists"
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
    lifecycle_log "warn" "monitor" "stale-detected" "Keepalive age: ${age}s > ${MAX_AGE}s"

    # Kill stale watcher
    pkill -f "muxcode watch $SESSION" 2>/dev/null || true
    sleep 0.2

    # Relaunch watcher
    muxcode watch "$SESSION" &>/dev/null &
    lifecycle_log "info" "monitor" "watcher-restart" "PID: $!"
  fi
done
