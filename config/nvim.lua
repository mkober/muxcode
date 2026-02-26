-- muxcode nvim configuration snippet
-- Add relevant parts to your nvim config (init.lua or lazy.nvim plugin spec)

-- Vim-tmux-navigator plugin (for lazy.nvim)
-- Enables seamless Ctrl-h/j/k/l navigation between tmux and nvim panes
-- Uncomment and add to your lazy.nvim plugin list:
--
-- {
--   'christoomey/vim-tmux-navigator',
--   event = 'VeryLazy',
-- },

-- Auto-equalize splits on resize
-- Essential when using a tiling window manager (AeroSpace, i3, sway, etc.)
-- Add this autocmd to your init.lua:
--
-- vim.api.nvim_create_autocmd('VimResized', {
--   callback = function()
--     vim.cmd('wincmd =')
--   end,
-- })

-- Show line numbers
-- Recommended — hooks rely on your nvim config for line numbers.
-- Add this to your init.lua if not already set:
--
-- vim.opt.number = true

-- Open all folds by default
-- Prevents code blocks from being folded when files open in the diff preview.
-- Set foldlevelstart high so no folds are closed on BufRead.
-- Add this to your init.lua:
--
-- vim.opt.foldlevelstart = 99

-- Optional: jk to exit insert mode and save
-- Convenient for quick edits when reviewing agent-proposed changes
--
-- vim.keymap.set('i', 'jk', '<Esc>:update<CR>')
