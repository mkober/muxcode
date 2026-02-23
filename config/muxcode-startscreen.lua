-- muxcode-startscreen.lua
-- Auto-loaded from ~/.local/share/nvim/site/plugin/
-- Only activates inside a muxcode tmux session (MUXCODE=1)

if not vim.env.MUXCODE then
  return
end

local M = {}

-- ── Logo ──────────────────────────────────────────────────────────────────────

M.header = {
  "",
  "███╗   ███╗██╗   ██╗██╗  ██╗   ██████╗ ██████╗ ██████╗ ███████╗",
  "████╗ ████║██║   ██║╚██╗██╔╝  ██╔════╝██╔═══██╗██╔══██╗██╔════╝",
  "██╔████╔██║██║   ██║ ╚███╔╝   ██║     ██║   ██║██║  ██║█████╗  ",
  "██║╚██╔╝██║██║   ██║ ██╔██╗   ██║     ██║   ██║██║  ██║██╔══╝  ",
  "██║ ╚═╝ ██║╚██████╔╝██╔╝ ██╗  ╚██████╗╚██████╔╝██████╔╝███████╗",
  "╚═╝     ╚═╝ ╚═════╝ ╚═╝  ╚═╝   ╚═════╝ ╚═════╝ ╚═════╝ ╚══════╝",
  "",
}

-- ── Helpers ───────────────────────────────────────────────────────────────────

local ns = vim.api.nvim_create_namespace("muxcode_start")

local function has_telescope()
  local ok, _ = pcall(require, "telescope")
  return ok
end

-- Returns (padded_line, pad_byte_len).
-- In non-tail position (not the last/only argument in a call), Lua adjusts
-- multi-return functions to a single value, so only the line string is passed.
local function center(line, width)
  local pad = math.max(0, math.floor((width - vim.fn.strdisplaywidth(line)) / 2))
  return string.rep(" ", pad) .. line, pad
end

local function get_session()
  return vim.env.BUS_SESSION
    or vim.env.SESSION
    or vim.fn.fnamemodify(vim.fn.getcwd(), ":t")
end

local function get_branch()
  local out = vim.fn.system(
    "git -C " .. vim.fn.shellescape(vim.fn.getcwd()) .. " branch --show-current 2>/dev/null"
  ):gsub("\n", "")
  return out ~= "" and out or nil
end

-- Read lock files to determine which agents are busy.
local function get_agents(session)
  local lock_dir = "/tmp/muxcode-bus-" .. session .. "/lock"
  local roles    = { "edit", "build", "test", "review", "commit", "analyze" }
  local result   = {}
  for _, role in ipairs(roles) do
    local f    = io.open(lock_dir .. "/" .. role .. ".lock", "r")
    local busy = f ~= nil
    if f then f:close() end
    table.insert(result, { role = role, busy = busy })
  end
  return result
end

-- Recent files under cwd, up to max_n entries.
local function get_recent_files(max_n)
  local files      = {}
  local cwd_prefix = vim.fn.getcwd() .. "/"
  for _, path in ipairs(vim.v.oldfiles or {}) do
    if #files >= max_n then break end
    if vim.fn.filereadable(path) == 1
      and path:sub(1, #cwd_prefix) == cwd_prefix
    then
      table.insert(files, vim.fn.fnamemodify(path, ":."))
    end
  end
  return files
end

-- ── Renderer ──────────────────────────────────────────────────────────────────

local function open_start()
  -- Skip if opened with a file argument
  if vim.fn.argc() > 0 then return end
  -- Skip if the buffer already has content (e.g. stdin was piped in)
  if vim.fn.line("$") > 1 or vim.fn.getline(1) ~= "" then return end
  -- Skip if another dashboard plugin already set a filetype
  if vim.bo.filetype ~= "" then return end

  local buf = vim.api.nvim_create_buf(false, true)
  vim.api.nvim_set_current_buf(buf)

  local width  = vim.api.nvim_win_get_width(0)
  local height = vim.api.nvim_win_get_height(0)

  -- First pass: render content into these tables.
  -- top_pad is computed from #content_lines after the render loop, so adding
  -- or removing sections here never requires a manual line-count update.
  local content_lines = {}
  local content_hls   = {}  -- { line_idx(0-based in content), group, col_start, col_end }

  local function push(line, hl_group)
    table.insert(content_lines, line)
    if hl_group then
      table.insert(content_hls, { #content_lines - 1, hl_group, 0, -1 })
    end
  end

  local function blank() table.insert(content_lines, "") end

  -- ── Collect runtime data ──────────────────────────────────────────────────

  local session    = get_session()
  local branch     = get_branch()
  local agents     = get_agents(session)
  local recent     = get_recent_files(5)
  local has_recent = #recent > 0
  local telescope  = has_telescope()

  local shortcuts = {
    { key = "e", desc = "new file", cmd = ":enew<CR>" },
    { key = "f", desc = "find",     cmd = ":Telescope find_files<CR>", fallback = ":edit .<CR>" },
    { key = "r", desc = "recent",   cmd = ":Telescope oldfiles<CR>",   fallback = ":browse oldfiles<CR>" },
    { key = "g", desc = "grep",     cmd = ":Telescope live_grep<CR>",  fallback = ":vimgrep " },
    { key = "q", desc = "quit",     cmd = ":qa<CR>" },
  }

  -- ── ASCII logo ────────────────────────────────────────────────────────────

  local max_art_w = 0
  for _, l in ipairs(M.header) do
    local w = vim.fn.strdisplaywidth(l)
    if w > max_art_w then max_art_w = w end
  end
  local art_pad = string.rep(" ", math.max(0, math.floor((width - max_art_w) / 2)))

  for _, l in ipairs(M.header) do
    if vim.fn.strdisplaywidth(l) == 0 then
      blank()
    elseif l:find("[█╗╔╚╝║═]") then
      push(art_pad .. l, "MuxcodeHeader")
    else
      push(center(l, width), "MuxcodeSubtitle")
    end
  end

  -- ── Subtitle ─────────────────────────────────────────────────────────────

  push(center("multi-agent coding environment", width), "MuxcodeSubtitle")

  -- ── Session / branch info ─────────────────────────────────────────────────

  do
    local info = "session: " .. session
      .. (branch and ("  │  branch: " .. branch) or "")
    push(center(info, width), "MuxcodeInfo")
  end

  -- ── Divider helper ────────────────────────────────────────────────────────

  local div_w = math.min(60, width - 8)

  local function divider()
    blank()
    push(center(string.rep("┄", div_w), width), "MuxcodeDivider")
  end

  -- ── Agents ───────────────────────────────────────────────────────────────

  divider()
  blank()
  push(center("AGENTS", width), "MuxcodeSection")

  do
    -- Build inner string and record per-icon byte offsets for colored dots.
    local parts     = {}
    local icon_meta = {}  -- { inner_byte_start, icon_byte_len, busy }
    local cursor    = 0

    for i, a in ipairs(agents) do
      local icon  = a.busy and "●" or "○"  -- each is 3 UTF-8 bytes
      local label = " " .. a.role
      table.insert(icon_meta, { cursor, #icon, a.busy })
      table.insert(parts, icon .. label)
      cursor = cursor + #icon + #label
      if i < #agents then cursor = cursor + 3 end  -- "   " separator
    end

    local inner    = table.concat(parts, "   ")
    local row, pad = center(inner, width)
    local row_idx  = #content_lines  -- 0-based index of the line about to be pushed

    push(row)

    -- Per-icon highlights (green = idle, orange = busy)
    for _, m in ipairs(icon_meta) do
      local s  = pad + m[1]
      local e  = s + m[2]
      local hl = m[3] and "MuxcodeAgentBusy" or "MuxcodeAgentIdle"
      table.insert(content_hls, { row_idx, hl, s, e })
    end
  end

  -- ── Shortcuts ─────────────────────────────────────────────────────────────

  divider()
  blank()

  do
    local parts    = {}
    local key_meta = {}  -- { inner_byte_start, bracket_end, part_end }
    local cursor   = 0

    for i, s in ipairs(shortcuts) do
      local bracket = "[" .. s.key .. "] "
      local part    = bracket .. s.desc
      table.insert(key_meta, { cursor, cursor + #bracket, cursor + #part })
      table.insert(parts, part)
      cursor = cursor + #part
      if i < #shortcuts then cursor = cursor + 3 end
    end

    local inner    = table.concat(parts, "   ")
    local row, pad = center(inner, width)
    local row_idx  = #content_lines

    push(row)

    for i, m in ipairs(key_meta) do
      -- "[X] " in purple, description in green
      table.insert(content_hls, { row_idx, "MuxcodeKey",      pad + m[1], pad + m[2] })
      table.insert(content_hls, { row_idx, "MuxcodeShortcut", pad + m[2], pad + m[3] })

      -- Bind key in this buffer
      local sc  = shortcuts[i]
      local cmd = (telescope or not sc.fallback) and sc.cmd or sc.fallback
      vim.keymap.set("n", sc.key, function()
        vim.api.nvim_buf_delete(buf, { force = true })
        local k = vim.api.nvim_replace_termcodes(cmd, true, false, true)
        vim.api.nvim_feedkeys(k, "n", false)
      end, { buffer = buf, nowait = true, silent = true })
    end
  end

  -- ── Recent files ──────────────────────────────────────────────────────────

  if has_recent then
    divider()
    blank()
    push(center("RECENT", width), "MuxcodeSection")
    -- Align bullet items to the left edge of the divider block
    local file_pad = string.rep(" ", math.max(0, math.floor((width - div_w) / 2)) + 2)
    for _, f in ipairs(recent) do
      push(file_pad .. "• " .. f, "MuxcodeFile")
    end
  end

  -- ── Second pass: assemble final buffer with padding ───────────────────────
  -- min(1) guarantees at least one blank row above content so it never presses
  -- hard against the top edge; on very short terminals the bottom is clipped
  -- rather than pushing content upward.
  local top_pad = math.max(1, math.floor((height - #content_lines) / 2))
  local lines   = {}
  for _ = 1, top_pad do table.insert(lines, "") end
  for _, l in ipairs(content_lines) do table.insert(lines, l) end
  for _ = 1, math.max(0, height - #lines) do table.insert(lines, "") end

  -- ── Commit buffer contents ────────────────────────────────────────────────

  vim.api.nvim_buf_set_lines(buf, 0, -1, false, lines)

  -- ── Highlight groups (Dracula palette) ───────────────────────────────────

  local function def(name, opts)
    opts.default = true
    vim.api.nvim_set_hl(0, name, opts)
  end

  def("MuxcodeHeader",    { fg = "#7dcfff", bold = true   })
  def("MuxcodeSubtitle",  { fg = "#565f89", italic = true })
  def("MuxcodeInfo",      { fg = "#6272a4"                })
  def("MuxcodeDivider",   { fg = "#3d4461"                })
  def("MuxcodeSection",   { fg = "#ff79c6", bold = true   })
  def("MuxcodeAgentIdle", { fg = "#50fa7b"                })
  def("MuxcodeAgentBusy", { fg = "#ffb86c"                })
  def("MuxcodeKey",       { fg = "#bd93f9", bold = true   })
  def("MuxcodeShortcut",  { fg = "#9ece6a"                })
  def("MuxcodeFile",      { fg = "#8be9fd"                })

  -- Apply highlights; offset content-relative indices by top_pad
  for _, h in ipairs(content_hls) do
    vim.api.nvim_buf_add_highlight(buf, ns, h[2], h[1] + top_pad, h[3], h[4])
  end

  -- ── Buffer & window settings ──────────────────────────────────────────────

  vim.bo[buf].modifiable = false
  vim.bo[buf].bufhidden  = "wipe"
  vim.bo[buf].buftype    = "nofile"
  vim.bo[buf].swapfile   = false
  vim.bo[buf].filetype   = "muxcode"

  local win = vim.api.nvim_get_current_win()
  vim.wo[win].number         = false
  vim.wo[win].relativenumber = false
  vim.wo[win].signcolumn     = "no"
  vim.wo[win].statuscolumn   = ""
  vim.wo[win].foldcolumn     = "0"
  vim.wo[win].cursorline     = false
  vim.wo[win].colorcolumn    = ""
  vim.wo[win].list           = false

  -- Auto-wipe when the user opens a file or switches buffer
  vim.api.nvim_create_autocmd("BufLeave", {
    buffer = buf,
    once   = true,
    callback = function()
      if vim.api.nvim_buf_is_valid(buf) then
        vim.api.nvim_buf_delete(buf, { force = true })
      end
    end,
  })
end

-- ── Entry point ───────────────────────────────────────────────────────────────

vim.api.nvim_create_autocmd("VimEnter", {
  group = vim.api.nvim_create_augroup("MuxcodeStart", { clear = true }),
  once  = true,
  callback = function()
    -- Defer so lazy.nvim and other plugins finish loading first
    vim.schedule(open_start)
  end,
})

return M
