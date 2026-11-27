-- Attention for an nvim panel beside an agent. When the agent puts something
-- in the panel while the user is looking elsewhere, the pane asks for them;
-- once they look, it stops asking. Agents don't have to remember to.
--
-- In Rex the pane reports through Rex's program status protocol (OSC 7501,
-- https://www.superlogical.com/rex/docs/build/program-status.md) under its own
-- id, and Rex decides how that shows and whether it notifies. tmux has no
-- such protocol: a bell on becoming blocked flags the window, and the pane
-- title carries the message.
--
-- The user is looking when the pane has focus (terminal focus events: Rex
-- sends them, tmux with `focus-events on`), or when they typed, clicked or
-- scrolled in it in the last few seconds, since a pane can be scrolled
-- without focus. Keys an agent runs through --remote-expr aren't typed, so
-- they don't count, and nothing the user does in the pane raises attention.
--
-- Two ways to use it:
--   dofile(path).panel()     a plain panel: showing a file in a window, a
--                            new tab, or a reload from disk is presenting.
--   dofile(path).new(opts)   the pieces, for a panel with its own states
--                            (the review kit): present(), pending(), report().

local M = {}
local uv = vim.uv or vim.loop
local function now() return uv.hrtime() / 1e6 end
-- How long after the user's last input they still count as looking, in seconds.
local function recent() return (tonumber(vim.g.wt_panel_recent) or 5) * 1000 end

local function clean(msg)
  return vim.fn.strcharpart(msg:gsub('%c', ' '):gsub('\194[\128-\159]', ' '), 0, 256)
end

-- opts: id and app (Rex's record), title (tmux pane title when clear), and
-- on_change, called when looking takes back what was presented.
function M.new(opts)
  local a = { focused = false }
  local shown, last, input = nil, nil, nil
  local on_change = opts.on_change or function() end

  -- One state per call; repeats are dropped, so callers can report freely.
  function a.report(state, msg)
    msg = msg and clean(msg) or nil
    local out
    if vim.env.REX_SESSION or vim.env.REX_BLOCK then
      local r = 'state=' .. state .. ':id=' .. opts.id .. ':app=' .. opts.app
      if state == 'blocked' then r = r .. ':kind=question' end
      if msg then r = r .. ':msg=' .. vim.base64.encode(msg) end
      if r == last then return end
      out = '\27]7501;' .. r .. '\27\\'
      last = r
    elseif vim.env.TMUX then
      local r = state .. ':' .. (msg or '')
      if r == last then return end
      local was_blocked = (last or ''):sub(1, 8) == 'blocked:'
      last = r
      local title = state == 'clear' and opts.title or (opts.title .. ': ' .. (msg or state))
      out = '\27]2;' .. title .. '\27\\' .. ((state == 'blocked' and not was_blocked) and '\7' or '')
    else
      return
    end
    io.stdout:write(out)
    io.stdout:flush()
  end

  function a.looking()
    return a.focused or (input ~= nil and now() - input < recent())
  end

  -- Something new is in front of the user. Returns whether it now waits for
  -- them; callers report the state themselves.
  function a.present(msg)
    if a.looking() then return false end
    shown = msg
    return true
  end

  function a.pending() return shown end
  function a.dismiss() shown = nil end

  local function seen()
    if not shown then return end
    shown = nil
    on_change()
  end

  local group = vim.api.nvim_create_augroup('wt_attention_' .. opts.id, { clear = true })
  vim.api.nvim_create_autocmd('FocusGained', { group = group, callback = function()
    a.focused = true
    seen()
  end })
  vim.api.nvim_create_autocmd('FocusLost', { group = group, callback = function() a.focused = false end })
  vim.api.nvim_create_autocmd('VimLeavePre', { group = group, callback = function() a.report('clear') end })
  -- `typed` is empty for keys from :normal or a remote call, and missing on
  -- nvim before 0.10, where input can't be told apart and is ignored.
  vim.on_key(function(_, typed)
    if not typed or typed == '' then return end
    local k = vim.fn.keytrans(typed)
    if k == '<FocusGained>' or k == '<FocusLost>' then return end
    input = now()
    if shown then vim.schedule(seen) end
  end, vim.api.nvim_create_namespace('wt_attention_' .. opts.id))
  return a
end

-- A plain panel, as `wt panel` starts it.
function M.panel(opts)
  opts = opts or {}
  local a
  local function update()
    local msg = a.pending()
    a.report(msg and 'blocked' or 'clear', msg)
  end
  a = M.new({ id = opts.id or 'wt-panel', app = opts.app or 'wt', title = opts.title or 'nvim', on_change = update })

  -- An Ex line like `tabnew | edit f | Unified base` fires several events;
  -- report once, after it, for the last file it showed. Only a named file
  -- counts: plugins' popups, trees and scratch buffers aren't something shown,
  -- and they may set 'buftype' only after their window opens.
  local queued, bufs = false, {}
  local function presented(ev)
    if vim.v.vim_did_enter == 0 or vim.api.nvim_win_get_config(0).relative ~= '' then return end
    bufs[#bufs + 1] = ev.event == 'TabEnter' and vim.api.nvim_get_current_buf() or ev.buf
    if queued then return end
    queued = true
    vim.schedule(function()
      queued = false
      local seen = bufs
      bufs = {}
      for i = #seen, 1, -1 do
        local buf = seen[i]
        local name = vim.api.nvim_buf_is_valid(buf) and vim.bo[buf].buftype == '' and vim.api.nvim_buf_get_name(buf) or ''
        if name ~= '' then
          if a.present('Look at ' .. vim.fn.fnamemodify(name, ':~:.')) then update() end
          return
        end
      end
    end)
  end
  local group = vim.api.nvim_create_augroup('wt_panel_' .. (opts.id or 'wt-panel'), { clear = true })
  vim.api.nvim_create_autocmd({ 'BufWinEnter', 'TabEnter', 'FileChangedShellPost' }, { group = group, callback = presented })
  return a
end

return M
