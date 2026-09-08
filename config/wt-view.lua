-- One explicitly targeted editor/diff/presentation view. No session-file
-- sourcing: recovery reads JSON data and uses native APIs, never saved commands.
local api = vim.api
local uv = vim.uv or vim.loop
local data = vim.json.decode(table.concat(vim.fn.readfile(vim.env.WT_VIEW_STATE), '\n'))
assert(data.state.version == 1, 'unsupported wt view version')
local root = assert(uv.fs_realpath(data.root), 'missing view root')
local state = data.state
local previous = package.loaded.wt_view
if previous and previous.close then previous.close() end
local renderer
local diff_autocmds = {}
local M = {}
function M.close()
  if renderer then renderer.close() end
  for _, id in ipairs(diff_autocmds) do pcall(api.nvim_del_autocmd, id) end
  diff_autocmds = {}
end
function M.refresh() if renderer then renderer.refresh() end end
package.loaded.wt_view = M
local function contained(path)
  if path:sub(1, 1) ~= '/' then path = root .. '/' .. path end
  local real = assert(uv.fs_realpath(path), 'missing view file: ' .. path)
  assert(real == root or real:sub(1, #root + 1) == root .. '/', 'view file escapes explicit target')
  return real
end
local function save_layout(node)
  if node[1] ~= 'leaf' then
    local children = {}
    for _, child in ipairs(node[2]) do children[#children + 1] = save_layout(child) end
    return { kind = node[1], children = children }
  end
  local win = node[2]
  local file = api.nvim_buf_get_name(api.nvim_win_get_buf(win))
  local view = api.nvim_win_call(win, vim.fn.winsaveview)
  return { kind = 'leaf', file = file ~= '' and contained(file) or nil,
    cursor = api.nvim_win_get_cursor(win), topline = view.topline, leftcol = view.leftcol,
    width = api.nvim_win_get_width(win), height = api.nvim_win_get_height(win) }
end
local function restore_layout(node, win, leaves)
  api.nvim_set_current_win(win)
  if node.kind == 'leaf' then
    if node.file then vim.cmd.edit(vim.fn.fnameescape(contained(node.file))) end
    if node.cursor then api.nvim_win_set_cursor(win, node.cursor) end
    vim.fn.winrestview({ topline = node.topline or 1, leftcol = node.leftcol or 0,
      lnum = node.cursor and node.cursor[1] or 1, col = node.cursor and node.cursor[2] or 0 })
    leaves[#leaves + 1] = { win = win, node = node }
    return
  end
  assert(node.kind == 'row' or node.kind == 'col', 'invalid editor layout')
  local wins = { win }
  for i = 2, #node.children do
    api.nvim_set_current_win(wins[#wins])
    vim.cmd(node.kind == 'row' and 'rightbelow vnew' or 'rightbelow new')
    wins[#wins + 1] = api.nvim_get_current_win()
  end
  for i, child in ipairs(node.children) do restore_layout(child, wins[i], leaves) end
end
function M.snapshot()
  vim.cmd('redraw') -- settle tabline/window dimensions before recording views
  local saved = vim.deepcopy(state)
  saved.files = {}
  if data.kind == 'editor' then
    saved.editor = { tabs = {}, active_tab = vim.fn.tabpagenr(), active_window = vim.fn.winnr() }
    for i, _ in ipairs(api.nvim_list_tabpages()) do saved.editor.tabs[i] = save_layout(vim.fn.winlayout(i)) end
    for _, buf in ipairs(api.nvim_list_bufs()) do
      if vim.bo[buf].buflisted and vim.bo[buf].buftype == '' then
        local path = api.nvim_buf_get_name(buf)
        if path ~= '' then saved.files[#saved.files + 1] = contained(path) end
      end
    end
  elseif data.kind == 'diff' then
    saved.diff = renderer and renderer.snapshot() or state.diff
  elseif data.kind == 'presentation' then
    local present = require('wt_present').snapshot()
    saved.deck, saved.slide = present.deck, present.slide
  end
  return vim.json.encode(saved)
end
local sequence = 0
local function checkpoint()
  if not vim.env.WT_VIEW_RUNTIME then return end
  local ok, encoded = pcall(M.snapshot)
  if not ok then
    if data.kind == 'diff' then vim.notify("WT diff checkpoint not saved: " .. tostring(encoded), vim.log.levels.ERROR) end
    return
  end
  sequence = sequence + 1
  local job = vim.fn.jobstart({ vim.env.WT_STATE, 'worktree', 'view-checkpoint',
    vim.env.WT_VIEW_ROOT_ID, vim.env.WT_VIEW_ID, vim.env.WT_VIEW_RUNTIME })
  if job > 0 then
    vim.fn.chansend(job, vim.json.encode({ seq = sequence, state = vim.json.decode(encoded) }))
    vim.fn.chanclose(job, 'stdin')
  end
end
api.nvim_create_autocmd('User', { pattern = 'WtPresentationChanged', callback = checkpoint })
function M.clear()
  require('wt_present').clear()
  return vim.json.encode({ seq = sequence, state = vim.json.decode(M.snapshot()) })
end
function M.reload()
  local next_data = vim.json.decode(table.concat(vim.fn.readfile(vim.env.WT_VIEW_STATE), '\n'))
  assert(next_data.state.version == 1 and next_data.root == data.root, 'invalid target change')
  state = next_data.state
  if data.kind == 'presentation' then
    if type(state.deck) == 'table' then
      state.deck.startIndex = state.slide or 1
      require('wt_present').deck_show(state.deck)
    else require('wt_present').clear() end
  end
  return vim.json.encode({ ok = true, seq = sequence, state = vim.json.decode(M.snapshot()) })
end
if data.kind == 'editor' then
  if state.editor and #state.editor.tabs > 0 then
    local tabs, tab_leaves = {}, {}
    for i, node in ipairs(state.editor.tabs) do
      if i > 1 then vim.cmd('tabnew') end
      local leaves = {}
      restore_layout(node, api.nvim_get_current_win(), leaves)
      tabs[i], tab_leaves[i] = api.nvim_get_current_tabpage(), leaves
    end
    -- Creating later tabs changes the available height (tabline). Apply sizes
    -- and scroll views only after the whole topology exists.
    for i, leaves in ipairs(tab_leaves) do
      api.nvim_set_current_tabpage(tabs[i])
      for _, leaf in ipairs(leaves) do
        if leaf.node.width then pcall(api.nvim_win_set_width, leaf.win, leaf.node.width) end
        if leaf.node.height then pcall(api.nvim_win_set_height, leaf.win, leaf.node.height) end
      end
      for _, leaf in ipairs(leaves) do
        api.nvim_win_call(leaf.win, function()
          vim.fn.winrestview({ topline = leaf.node.topline or 1, leftcol = leaf.node.leftcol or 0,
            lnum = leaf.node.cursor and leaf.node.cursor[1] or 1, col = leaf.node.cursor and leaf.node.cursor[2] or 0 })
        end)
      end
    end
    api.nvim_set_current_tabpage(tabs[state.editor.active_tab or 1] or tabs[1])
    local wins = api.nvim_tabpage_list_wins(0)
    api.nvim_set_current_win(wins[state.editor.active_window or 1] or wins[1])
  else
    for i, file in ipairs(state.files or {}) do
      if i > 1 then vim.cmd('tabnew') end
      vim.cmd.edit(vim.fn.fnameescape(contained(file)))
    end
  end
elseif data.kind == 'diff' then
  local module = vim.fn.fnamemodify(debug.getinfo(1, 'S').source:sub(2), ':h') .. '/wt-diff-renderer.lua'
  renderer = dofile(module).new({ root = root, base = state.base, position = state.diff })
  local function open()
    local ok, err = pcall(renderer.open)
    if not ok then
      renderer.close()
      local buf = api.nvim_create_buf(false, true)
      api.nvim_win_set_buf(0, buf)
      api.nvim_buf_set_lines(buf, 0, -1, false, { 'WT diff unavailable', '', tostring(err) })
      vim.bo[buf].modifiable = false
      vim.notify(tostring(err), vim.log.levels.ERROR)
    end
  end
  if vim.v.vim_did_enter == 1 then open()
  else diff_autocmds[#diff_autocmds + 1] = api.nvim_create_autocmd('VimEnter', { once = true, callback = open }) end
  diff_autocmds[#diff_autocmds + 1] = api.nvim_create_autocmd('CursorHold', { callback = checkpoint })
elseif data.kind == 'presentation' then
  dofile(vim.env.WT_PRESENT_MODULE)
  if type(state.deck) == 'table' then
    state.deck.startIndex = state.slide or 1
    require('wt_present').deck_show(state.deck)
  end
end
