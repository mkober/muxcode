# Vim Diff Preview Fix

Correct `sil!` usage in vim pipe chains and separate jump-to-line timing for reliable diff previews.

## Requirements

- Every command in a vim `|` pipe chain prefixed with `sil!` (only suppresses the next command, not the full chain)
- Prevents E35 errors and "Press ENTER" prompts that break subsequent commands
- Jump-to-line sent as a separate `tmux send-keys` after 150ms delay
- Delay ensures scrollbind is fully active before jumping to the target line
- Uses `norm! {LINE}Gzz` instead of `:N` so scrollbind properly syncs both diff panes

## Key files

| File | Purpose |
|------|---------|
| `scripts/muxcode-preview-hook.sh` | Diff preview hook with corrected `sil!` chains and delayed jump |

## Status

Complete
