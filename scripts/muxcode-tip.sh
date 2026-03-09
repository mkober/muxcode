#!/usr/bin/env bash
# Rotating MUXcode tips for the tmux status bar.
# Cycles through tips every 10 minutes based on epoch time.

tips=(
  "Prefix+b ≡ Menu"
  "F1-F10 Switch Windows"
  "Prefix+C ≡ New Session"
  "Prefix+z ≡ Zoom Pane"
  "Prefix+[ ≡ Scroll Mode"
  "Prefix+d ≡ Detach"
  "Send → Delegate To Agents"
  "Memory → Persistent Context"
  "Spawn → Temporary Agents"
  "Cron → Scheduled Tasks"
)

idx=$(( $(date +%s) / 600 % ${#tips[@]} ))
echo "Mux Tip: ${tips[$idx]}"
