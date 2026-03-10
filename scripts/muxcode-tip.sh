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
  "Ctrl+h/j/k/l ≡ Switch Panes"
  "Prefix+c ≡ New Window"
  "Prefix+n/p ≡ Next/Prev Window"
  "Prefix+w ≡ Window List"
  "Prefix+s ≡ Session List"
  "Prefix+x ≡ Close Pane"
  "Prefix+% ≡ Split Horizontal"
  "Prefix+\" ≡ Split Vertical"
  "Prefix+: ≡ Command Prompt"
  "Prefix+? ≡ List All Keybindings"
)

idx=$(( $(date +%s) / 600 % ${#tips[@]} ))
echo "${tips[$idx]}"
