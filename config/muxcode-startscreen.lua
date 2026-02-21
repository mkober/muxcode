-- muxcode-startscreen.lua
-- Auto-loaded from ~/.local/share/nvim/site/plugin/
-- Only activates inside a muxcode tmux session (MUXCODE=1)

if not vim.env.MUXCODE then
  return
end

local M = {}

M.header = {
  "",
  "███╗   ███╗██╗   ██╗██╗  ██╗   ██████╗ ██████╗ ██████╗ ███████╗",
  "████╗ ████║██║   ██║╚██╗██╔╝  ██╔════╝██╔═══██╗██╔══██╗██╔════╝",
  "██╔████╔██║██║   ██║ ╚███╔╝   ██║     ██║   ██║██║  ██║█████╗  ",
  "██║╚██╔╝██║██║   ██║ ██╔██╗   ██║     ██║   ██║██║  ██║██╔══╝  ",
  "██║ ╚═╝ ██║╚██████╔╝██╔╝ ██╗  ╚██████╗╚██████╔╝██████╔╝███████╗",
  "╚═╝     ╚═╝ ╚═════╝ ╚═╝  ╚═╝   ╚═════╝ ╚═════╝ ╚═════╝ ╚══════╝",
  "",
  "multi-agent coding environment",
  "",
}

M.shortcuts = {
  { key = "e", desc = "New file",     cmd = ":enew<CR>" },
  { key = "f", desc = "Find file",    cmd = ":Telescope find_files<CR>",  fallback = ":edit .<CR>" },
  { key = "r", desc = "Recent files", cmd = ":Telescope oldfiles<CR>",    fallback = ":browse oldfiles<CR>" },
  { key = "g", desc = "Grep text",    cmd = ":Telescope live_grep<CR>",   fallback = ":vimgrep " },
  { key = "q", desc = "Quit",         cmd = ":qa<CR>" },
}

local ns = vim.api.nvim_create_namespace("muxcode_start")

local function has_telescope()
  local ok, _ = pcall(require, "telescope")
  return ok
end

local function center_line(line, width)
  local pad = math.max(0, math.floor((width - vim.fn.strdisplaywidth(line)) / 2))
  return string.rep(" ", pad) .. line
end

local function open_start()
  -- Only show on empty startup (no files, no stdin)
  if vim.fn.argc() > 0 or vim.fn.line2byte("$") ~= -1 then
    return
  end

  -- Don't clobber other dashboards (alpha, dashboard-nvim, etc.)
  if vim.bo.filetype ~= "" then
    return
  end

  local buf = vim.api.nvim_create_buf(false, true)
  vim.api.nvim_set_current_buf(buf)

  local width = vim.api.nvim_win_get_width(0)
  local height = vim.api.nvim_win_get_height(0)
  local lines = {}
  local hl_ranges = {} -- {line_idx, hl_group}

  -- Vertical padding to center content
  local content_height = #M.header + #M.shortcuts + 2
  local top_pad = math.max(0, math.floor((height - content_height) / 2))
  for _ = 1, top_pad do
    table.insert(lines, "")
  end

  -- Header (ASCII art) — center as a uniform block
  local max_art_width = 0
  for _, line in ipairs(M.header) do
    local w = vim.fn.strdisplaywidth(line)
    if w > max_art_width then
      max_art_width = w
    end
  end
  local art_pad = string.rep(" ", math.max(0, math.floor((width - max_art_width) / 2)))

  local header_start = #lines
  for _, line in ipairs(M.header) do
    local centered
    if vim.fn.strdisplaywidth(line) == 0 then
      centered = ""
    elseif line:find("[█╗╔╚╝║═]") then
      -- ASCII art line: uniform left padding so the block stays aligned
      centered = art_pad .. line
    else
      -- Subtitle or other text: center individually
      centered = center_line(line, width)
    end
    table.insert(lines, centered)
    table.insert(hl_ranges, { #lines - 1, "MuxcodeHeader" })
  end

  -- Blank separator
  table.insert(lines, "")

  -- Center menu as a block below the subtitle
  local menu_items = {}
  local max_menu_width = 0
  local telescope = has_telescope()
  for _, s in ipairs(M.shortcuts) do
    local text = string.format("[%s]  %s", s.key, s.desc)
    table.insert(menu_items, { text = text, shortcut = s })
    local w = vim.fn.strdisplaywidth(text)
    if w > max_menu_width then
      max_menu_width = w
    end
  end
  local menu_pad = string.rep(" ", math.max(0, math.floor((width - max_menu_width) / 2)))

  -- Shortcuts (centered as a block)
  for _, m in ipairs(menu_items) do
    local entry = menu_pad .. m.text
    table.insert(lines, entry)
    table.insert(hl_ranges, { #lines - 1, "MuxcodeShortcut" })

    -- Bind the key in this buffer
    local s = m.shortcut
    local cmd = (telescope or not s.fallback) and s.cmd or s.fallback
    vim.keymap.set("n", s.key, function()
      vim.api.nvim_buf_delete(buf, { force = true })
      local keys = vim.api.nvim_replace_termcodes(cmd, true, false, true)
      vim.api.nvim_feedkeys(keys, "n", false)
    end, { buffer = buf, nowait = true, silent = true })
  end

  -- Fill remaining height
  local remaining = math.max(0, height - #lines)
  for _ = 1, remaining do
    table.insert(lines, "")
  end

  -- Set buffer contents
  vim.api.nvim_buf_set_lines(buf, 0, -1, false, lines)

  -- Highlights
  vim.api.nvim_set_hl(0, "MuxcodeHeader", { fg = "#7dcfff", bold = true, default = true })
  vim.api.nvim_set_hl(0, "MuxcodeSubtitle", { fg = "#565f89", italic = true, default = true })
  vim.api.nvim_set_hl(0, "MuxcodeShortcut", { fg = "#9ece6a", default = true })

  for _, hl in ipairs(hl_ranges) do
    vim.api.nvim_buf_add_highlight(buf, ns, hl[2], hl[1], 0, -1)
  end

  -- Mark subtitle line
  local subtitle_idx = header_start + #M.header - 2 -- "multi-agent coding environment"
  if subtitle_idx >= 0 and subtitle_idx < #lines then
    vim.api.nvim_buf_add_highlight(buf, ns, "MuxcodeSubtitle", subtitle_idx, 0, -1)
  end

  -- Buffer settings
  vim.bo[buf].modifiable = false
  vim.bo[buf].bufhidden = "wipe"
  vim.bo[buf].buftype = "nofile"
  vim.bo[buf].swapfile = false
  vim.bo[buf].filetype = "muxcode"

  -- Clean window
  local win = vim.api.nvim_get_current_win()
  vim.wo[win].number = false
  vim.wo[win].relativenumber = false
  vim.wo[win].signcolumn = "no"
  vim.wo[win].statuscolumn = ""
  vim.wo[win].foldcolumn = "0"
  vim.wo[win].cursorline = false
  vim.wo[win].colorcolumn = ""
  vim.wo[win].list = false

  -- Close on buffer switch
  vim.api.nvim_create_autocmd("BufLeave", {
    buffer = buf,
    once = true,
    callback = function()
      if vim.api.nvim_buf_is_valid(buf) then
        vim.api.nvim_buf_delete(buf, { force = true })
      end
    end,
  })
end

vim.api.nvim_create_autocmd("VimEnter", {
  group = vim.api.nvim_create_augroup("MuxcodeStart", { clear = true }),
  once = true,
  callback = function()
    -- Defer so other plugins (lazy.nvim, etc.) finish loading first
    vim.schedule(open_start)
  end,
})

return M
