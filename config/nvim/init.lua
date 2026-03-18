-- muxcode neovim configuration
-- Managed by muxcode — loaded via NVIM_APPNAME=muxcode
-- User extensions: ~/.config/muxcode/nvim/lua/user/plugins.lua

-- ── Leader key ────────────────────────────────────────────────────────────────

vim.g.mapleader = ' '
vim.g.maplocalleader = ' '
vim.g.have_nerd_font = true

-- ── Core settings ─────────────────────────────────────────────────────────────

vim.opt.number = true
vim.opt.relativenumber = true
vim.opt.signcolumn = 'yes'
vim.opt.cursorline = true
vim.opt.termguicolors = true
vim.opt.wrap = false
vim.opt.scrolloff = 10
vim.opt.splitright = true
vim.opt.splitbelow = true
vim.opt.foldlevelstart = 99
vim.opt.updatetime = 250
vim.opt.timeoutlen = 300
vim.opt.mouse = 'a'
vim.opt.showmode = false
vim.opt.breakindent = true
vim.opt.undofile = true
vim.opt.ignorecase = true
vim.opt.smartcase = true
vim.opt.inccommand = 'split'
vim.opt.list = true
vim.opt.listchars = { tab = '» ', trail = '·', nbsp = '␣' }

-- Auto-equalize splits on resize (essential for tiling WMs / tmux resizes)
vim.api.nvim_create_autocmd('VimResized', {
  callback = function()
    vim.cmd('wincmd =')
  end,
})

-- Highlight on yank
vim.api.nvim_create_autocmd('TextYankPost', {
  callback = function()
    vim.hl.on_yank()
  end,
})

-- ── Keymaps ───────────────────────────────────────────────────────────────────

-- jk to exit insert mode and save
vim.keymap.set('i', 'jk', '<Esc>:update<CR>')

-- Clear search highlight on Escape
vim.keymap.set('n', '<Esc>', '<cmd>nohlsearch<CR>')

-- Window navigation (also handled by vim-tmux-navigator)
vim.keymap.set('n', '<C-h>', '<C-w><C-h>', { desc = 'Move focus left' })
vim.keymap.set('n', '<C-l>', '<C-w><C-l>', { desc = 'Move focus right' })
vim.keymap.set('n', '<C-j>', '<C-w><C-j>', { desc = 'Move focus down' })
vim.keymap.set('n', '<C-k>', '<C-w><C-k>', { desc = 'Move focus up' })

-- ── Treesitter compatibility shims ───────────────────────────────────────────
-- nvim-treesitter 1.0+ removed the legacy Lua API (parsers.ft_to_lang, configs
-- module).  Older Telescope versions still call these.  The module-level shims
-- are applied inside the nvim-treesitter config function (after plugin load)
-- so the modules are actually on the rtp.  The core Neovim API shim below
-- runs early since it doesn't depend on any plugin.

-- vim.treesitter.language.ft_to_lang → get_lang (Neovim 0.11+)
if not vim.treesitter.language.ft_to_lang then
  vim.treesitter.language.ft_to_lang = vim.treesitter.language.get_lang
end

-- ── Bootstrap lazy.nvim ───────────────────────────────────────────────────────

local lazypath = vim.fn.stdpath('data') .. '/lazy/lazy.nvim'
if not (vim.uv or vim.loop).fs_stat(lazypath) then
  vim.fn.system({
    'git', 'clone', '--filter=blob:none', '--branch=stable',
    'https://github.com/folke/lazy.nvim.git', lazypath,
  })
end
vim.opt.rtp:prepend(lazypath)

-- ── User plugin extensions ────────────────────────────────────────────────────
-- Users can create ~/.config/muxcode/nvim/lua/user/plugins.lua returning a
-- table of lazy.nvim plugin specs to extend the default muxcode config.

local user_plugins = {}
local user_spec_path = vim.fn.stdpath('config') .. '/lua/user/plugins.lua'
if vim.fn.filereadable(user_spec_path) == 1 then
  local ok, specs = pcall(require, 'user.plugins')
  if ok and type(specs) == 'table' then
    user_plugins = specs
  end
end

-- ── Plugins ───────────────────────────────────────────────────────────────────

require('lazy').setup({
  -- Colorscheme (Dracula — matches muxcode tmux/TUI theme)
  {
    'Mofiqul/dracula.nvim',
    priority = 1000,
    init = function()
      vim.cmd.colorscheme('dracula')
    end,
  },

  -- Seamless Ctrl-h/j/k/l navigation between tmux and nvim panes
  {
    'christoomey/vim-tmux-navigator',
    event = 'VeryLazy',
  },

  -- Treesitter (syntax highlighting, required by render-markdown)
  {
    'nvim-treesitter/nvim-treesitter',
    build = ':TSUpdate',
    config = function()
      -- Shim nvim-treesitter.parsers and nvim-treesitter.configs for
      -- Telescope compatibility.  Must run here (after plugin load) so
      -- the modules are actually on the rtp and require() succeeds.
      do
        local ok, p = pcall(require, 'nvim-treesitter.parsers')
        if ok and type(p) == 'table' then
          if not p.ft_to_lang then
            p.ft_to_lang = function(ft)
              return vim.treesitter.language.get_lang(ft) or ft
            end
          end
          if not p.get_parser then
            p.get_parser = function(bufnr, lang)
              return vim.treesitter.get_parser(bufnr, lang)
            end
          end
        end
      end
      do
        local ok, c = pcall(require, 'nvim-treesitter.configs')
        if not ok or type(c) ~= 'table' or not c.is_enabled then
          package.loaded['nvim-treesitter.configs'] = {
            is_enabled = function() return true end,
            get_module = function()
              return { additional_vim_regex_highlighting = false }
            end,
          }
        end
      end

      local parsers = {
        'markdown', 'markdown_inline',
        'lua', 'bash', 'go', 'python', 'typescript', 'javascript',
        'json', 'yaml', 'toml',
      }
      -- Try legacy API (nvim-treesitter < 1.0)
      local ok, configs = pcall(require, 'nvim-treesitter.configs')
      if ok and configs.setup then
        ---@diagnostic disable-next-line: missing-fields
        configs.setup({
          ensure_installed = parsers,
          auto_install = true,
          highlight = { enable = true },
          indent = { enable = true },
        })
      else
        -- New nvim-treesitter (1.0+): install parsers, highlight is built-in
        local ts = require('nvim-treesitter')
        ts.setup({})
        -- Install missing parsers in the background after startup
        -- TSInstall prints download progress that triggers "Press ENTER",
        -- so we use the parser install API directly with no UI output.
        local installed = ts.get_installed()
        local to_install = vim.tbl_filter(function(p)
          return not vim.list_contains(installed, p)
        end, parsers)
        -- Install missing parsers silently after startup.
        -- vim.treesitter.language.add() loads already-compiled parsers.
        -- TSInstall requires the tree-sitter CLI which may not be present,
        -- so we skip it — parsers are compiled via :TSUpdate on plugin
        -- install/update (lazy.nvim build step).
        if #to_install > 0 then
          vim.api.nvim_create_autocmd('VimEnter', {
            once = true,
            callback = function()
              vim.schedule(function()
                for _, lang in ipairs(to_install) do
                  pcall(vim.treesitter.language.add, lang)
                end
              end)
            end,
          })
        end
      end
    end,
  },

  -- Markdown visual rendering (headings, bullets, checkboxes, code blocks)
  {
    'MeanderingProgrammer/render-markdown.nvim',
    dependencies = { 'nvim-treesitter/nvim-treesitter' },
    ft = { 'markdown' },
    opts = {
      heading = {
        enabled = true,
        sign = false,
        icons = { '# ', '## ', '### ', '#### ', '##### ', '###### ' },
      },
      bullet = {
        enabled = true,
        icons = { '●', '○', '◆', '◇' },
      },
      code = {
        enabled = true,
        sign = false,
        style = 'full',
      },
      dash = { enabled = true },
      checkbox = { enabled = true },
      pipe_table = {
        style = 'full',
      },
      win_options = {
        wrap = {
          default = vim.o.wrap,
          rendered = false,
        },
      },
    },
    config = function(_, opts)
      local rm = require('render-markdown')
      rm.setup(opts)
      -- <leader>w toggles wrap in markdown buffers:
      --   wrap off (default) → rendered tables + all decorations
      --   wrap on  → tables disabled (box-drawing breaks with wrap),
      --              headings/bullets/code/checkboxes still render
      vim.api.nvim_create_autocmd('FileType', {
        pattern = 'markdown',
        callback = function(ev)
          vim.keymap.set('n', '<leader>w', function()
            local new_opts = vim.deepcopy(opts)
            if vim.wo.wrap then
              -- restore: nowrap + table rendering
              vim.wo.wrap = false
              new_opts.pipe_table = { enabled = true, style = 'full' }
            else
              -- wrap mode: disable only tables, keep everything else
              vim.wo.wrap = true
              new_opts.pipe_table = { enabled = false }
              new_opts.win_options = {}
            end
            rm.setup(new_opts)
            vim.cmd('RenderMarkdown enable')
          end, { buffer = ev.buf, desc = 'Toggle [W]rap + table rendering' })
        end,
      })
    end,
  },

  -- Telescope (fuzzy finder — used by startscreen shortcuts)
  {
    'nvim-telescope/telescope.nvim',
    event = 'VimEnter',
    dependencies = {
      'nvim-lua/plenary.nvim',
      {
        'nvim-telescope/telescope-fzf-native.nvim',
        build = 'make',
        cond = function()
          return vim.fn.executable('make') == 1
        end,
      },
      'nvim-telescope/telescope-ui-select.nvim',
    },
    config = function()
      require('telescope').setup({
        extensions = {
          ['ui-select'] = {
            require('telescope.themes').get_dropdown(),
          },
        },
      })
      pcall(require('telescope').load_extension, 'fzf')
      pcall(require('telescope').load_extension, 'ui-select')

      local builtin = require('telescope.builtin')
      vim.keymap.set('n', '<leader>sf', builtin.find_files, { desc = '[S]earch [F]iles' })
      vim.keymap.set('n', '<leader>sg', builtin.live_grep, { desc = '[S]earch by [G]rep' })
      vim.keymap.set('n', '<leader>sh', builtin.help_tags, { desc = '[S]earch [H]elp' })
      vim.keymap.set('n', '<leader>sr', builtin.resume, { desc = '[S]earch [R]esume' })
      vim.keymap.set('n', '<leader>s.', builtin.oldfiles, { desc = '[S]earch Recent Files' })
      vim.keymap.set('n', '<leader><leader>', builtin.buffers, { desc = '[ ] Find existing buffers' })
    end,
  },

  -- Auto-detect indentation
  'tpope/vim-sleuth',

  -- User extensions (from lua/user/plugins.lua)
  unpack(user_plugins),
}, {
  ui = {
    icons = vim.g.have_nerd_font and {} or {
      cmd = '⌘', config = '🛠', event = '📅', ft = '📂',
      init = '⚙', keys = '🗝', plugin = '🔌', runtime = '💻',
      require = '🌙', source = '📄', start = '🚀', task = '📌',
      lazy = '💤 ',
    },
  },
})
