# Preview Fold Fix

Persistent `foldlevel=99` in nvim diff previews replacing one-shot `zR`.

## Requirements

- Nvim diff preview buffers use `setlocal foldlevel=99` for persistent unfolding
- Replaces previous one-shot `zR` approach which did not persist across buffer changes
- Applied in the preview hook script when setting up diff split views
- Prevents code folds from collapsing during diff navigation
- Works with scrollbind active between diff panes

## Key files

| File | Purpose |
|------|---------|
| `scripts/muxcode-preview-hook.sh` | Preview hook with persistent foldlevel setting |

## Status

Complete
