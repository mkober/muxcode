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

-- Optional: jk to exit insert mode and save
-- Convenient for quick edits when reviewing agent-proposed changes
--
-- vim.keymap.set('i', 'jk', '<Esc>:update<CR>')
