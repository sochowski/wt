-- wt-present.lua — generic presentation canvas for wt-managed Neovim.
--
-- Loaded by every managed Neovim instance. Agent integrations send versioned
-- scenes through bin/wt-present; this module renders the visual artifact while
-- the agent remains responsible for narration and user interaction.
--
-- The renderer registry is intentionally independent of Pi and Unified.nvim.
-- File, diff, tree, and markdown are the initial artifact types; additional
-- renderers can be registered with require('wt_present').register_renderer().

local M = {}
local api = vim.api
local uv = vim.uv or vim.loop

local namespace = api.nvim_create_namespace('wt_present')
local tree_namespace = api.nvim_create_namespace('wt_present_tree_focus')
local renderers = {}
local state = {
  active = false,
  mode = nil,
  scene = nil,
  deck = nil,
  deck_index = 1,
  snapshot = nil,
  win = nil,
  buf = nil,
  scratch_buffers = {},
  mapped_buffers = {},
  nvim_tree_opened = false,
  nvim_tree_win = nil,
  nvim_tree_peer_win = nil,
  nvim_tree_artifact = nil,
}

vim.cmd('highlight default link WtPresentRange Visual')
vim.cmd('highlight default link WtPresentLabel DiagnosticInfo')
vim.cmd('highlight default link WtPresentTree Visual')

local function display_width(text)
  local ok, width = pcall(vim.fn.strdisplaywidth, text)
  if ok and tonumber(width) then return width end
  return #text
end

local function wrap_text(text, width)
  width = math.max(10, tonumber(width) or 80)
  local lines = {}

  local function push_wrapped(raw)
    local current = ''
    for word in tostring(raw):gmatch('%S+') do
      if current == '' then
        current = word
      elseif display_width(current .. ' ' .. word) <= width then
        current = current .. ' ' .. word
      else
        lines[#lines + 1] = current
        current = word
      end

      while display_width(current) > width do
        lines[#lines + 1] = current:sub(1, width)
        current = current:sub(width + 1)
      end
    end
    lines[#lines + 1] = current
  end

  for raw in tostring(text):gmatch('([^\n]*)\n?') do
    if raw == '' then
      lines[#lines + 1] = ''
    else
      push_wrapped(raw)
    end
  end
  if #lines > 0 and lines[#lines] == '' and tostring(text):sub(-1) ~= '\n' then
    table.remove(lines)
  end
  if #lines == 0 then lines[1] = '' end
  return lines
end

local function annotation_virt_lines(label, win)
  if type(label) ~= 'string' or label == '' then return nil end
  local available = 80
  if win and api.nvim_win_is_valid(win) then available = api.nvim_win_get_width(win) - 8 end
  available = math.max(24, available)

  local first_prefix = '  ← '
  local next_prefix = '    '
  local wrapped = wrap_text(label, available - display_width(first_prefix))
  local virt_lines = {}
  for index, line in ipairs(wrapped) do
    local prefix = index == 1 and first_prefix or next_prefix
    virt_lines[#virt_lines + 1] = { { prefix .. line, 'WtPresentLabel' } }
  end
  return virt_lines
end

local function add_annotation(opts, label, win)
  local virt_lines = annotation_virt_lines(label, win)
  if not virt_lines then return end
  opts.virt_lines = virt_lines
  opts.virt_lines_above = false
end

local function valid_win(win)
  return win and api.nvim_win_is_valid(win)
end

local function valid_buf(buf)
  return buf and api.nvim_buf_is_valid(buf)
end

local function project_root()
  local root = vim.env.WT_WORKTREE
  if not root or root == '' then root = vim.fn.getcwd() end
  return vim.fs.normalize(root)
end

local function is_within(path, root)
  return path == root or path:sub(1, #root + 1) == root .. '/'
end

local function resolve_path(path, want_directory)
  if type(path) ~= 'string' or path == '' then
    error('artifact path is required')
  end

  local root = uv.fs_realpath(project_root()) or project_root()
  local candidate = path:sub(1, 1) == '/' and vim.fs.normalize(path)
    or vim.fs.normalize(root .. '/' .. path)
  local resolved = uv.fs_realpath(candidate)

  if not resolved then error('artifact path does not exist: ' .. path) end
  if not is_within(resolved, root) then
    error('artifact path is outside the worktree: ' .. path)
  end

  local stat = uv.fs_stat(resolved)
  if want_directory and (not stat or stat.type ~= 'directory') then
    error('artifact path is not a directory: ' .. path)
  elseif not want_directory and (not stat or stat.type ~= 'file') then
    error('artifact path is not a file: ' .. path)
  end

  return resolved, root
end

local function relative_path(path, root)
  if path == root then return '.' end
  if is_within(path, root) then return path:sub(#root + 2) end
  return path
end

local function current_main_window()
  if valid_win(state.win) then return state.win end

  local ok, unified_state = pcall(require, 'unified.state')
  if ok then
    local got, win = pcall(unified_state.get_main_window)
    if got and valid_win(win) then return win end
  end

  local current = api.nvim_get_current_win()
  if valid_win(current) and api.nvim_win_get_config(current).relative == '' then
    return current
  end

  local tab = api.nvim_get_current_tabpage()
  for _, win in ipairs(api.nvim_tabpage_list_wins(tab)) do
    if api.nvim_win_get_config(win).relative == '' then return win end
  end

  error('no editor window is available')
end

local function save_snapshot(win)
  if state.active then return end

  local view = {}
  pcall(api.nvim_win_call, win, function() view = vim.fn.winsaveview() end)
  state.snapshot = {
    tab = api.nvim_get_current_tabpage(),
    current_win = api.nvim_get_current_win(),
    target_win = win,
    target_buf = api.nvim_win_get_buf(win),
    target_cursor = api.nvim_win_get_cursor(win),
    target_view = view,
    target_winbar = vim.wo[win].winbar,
  }
end

local function clear_marks()
  for _, buf in ipairs(api.nvim_list_bufs()) do
    if valid_buf(buf) then
      pcall(api.nvim_buf_clear_namespace, buf, namespace, 0, -1)
      pcall(api.nvim_buf_clear_namespace, buf, tree_namespace, 0, -1)
    end
  end
end

local function clear_keymaps()
  for buf, _ in pairs(state.mapped_buffers) do
    if valid_buf(buf) then
      for _, lhs in ipairs({ 'H', 'L', '<Left>', '<Right>', 'q', '?' }) do
        pcall(vim.keymap.del, 'n', lhs, { buffer = buf })
      end
    end
  end
  state.mapped_buffers = {}
end

local function deck_key(action)
  return function()
    local ok, result = pcall(action)
    if not ok then
      vim.notify('wt presentation: ' .. tostring(result), vim.log.levels.ERROR)
    elseif type(result) == 'table' and result.ok == false then
      vim.notify('wt presentation: ' .. tostring(result.error or 'navigation failed'), vim.log.levels.ERROR)
    end
  end
end

local function set_deck_keymaps(buf)
  if not valid_buf(buf) or state.mode ~= 'deck' then return end
  if state.mapped_buffers[buf] then return end
  local opts = { buffer = buf, nowait = true, silent = true }
  vim.keymap.set('n', 'H', deck_key(M.deck_prev), opts)
  vim.keymap.set('n', '<Left>', deck_key(M.deck_prev), opts)
  vim.keymap.set('n', 'L', deck_key(M.deck_next), opts)
  vim.keymap.set('n', '<Right>', deck_key(M.deck_next), opts)
  vim.keymap.set('n', 'q', deck_key(M.clear), opts)
  vim.keymap.set('n', '?', deck_key(M.deck_help), opts)
  state.mapped_buffers[buf] = true
end

local function close_nvim_tree()
  if not state.nvim_tree_opened then return end
  local ok, tree = pcall(require, 'nvim-tree.api')
  if ok then pcall(tree.tree.close) end
  state.nvim_tree_opened = false
  state.nvim_tree_win = nil
  state.nvim_tree_peer_win = nil
  state.nvim_tree_artifact = nil
end

local function refresh_nvim_tree_highlights()
  if not valid_buf(state.buf) or not state.nvim_tree_artifact then return end
  vim.defer_fn(function()
    if valid_buf(state.buf) then highlight_nvim_tree_focus(state.buf, state.nvim_tree_artifact) end
  end, 30)
end

local function open_nvim_tree_node(tree)
  local node = tree.tree.get_node_under_cursor()
  if not node then return end
  if node.type == 'file' and node.absolute_path then
    local tree_win = api.nvim_get_current_win()
    local peer = valid_win(state.nvim_tree_peer_win) and state.nvim_tree_peer_win or nil
    if not peer then
      vim.cmd('rightbelow vertical new')
      peer = api.nvim_get_current_win()
      state.nvim_tree_peer_win = peer
    end
    api.nvim_set_current_win(peer)
    vim.cmd.edit(vim.fn.fnameescape(node.absolute_path))
    api.nvim_set_current_win(tree_win)
  else
    tree.node.open.edit()
  end
  refresh_nvim_tree_highlights()
end

local function set_nvim_tree_keymaps(buf, tree)
  if not valid_buf(buf) then return end
  local opts = { buffer = buf, nowait = true, silent = true, desc = 'wt presentation tree' }
  pcall(vim.keymap.set, 'n', 'l', function() open_nvim_tree_node(tree) end, opts)
  pcall(vim.keymap.set, 'n', 'h', function()
    tree.node.navigate.parent_close()
    refresh_nvim_tree_highlights()
  end, opts)
end

function highlight_nvim_tree_focus(buf, artifact)
  if not valid_buf(buf) then return end
  pcall(api.nvim_buf_clear_namespace, buf, tree_namespace, 0, -1)
  local focus = type(artifact.focus) == 'table' and artifact.focus or {}
  if #focus == 0 then return end

  local lines = api.nvim_buf_get_lines(buf, 0, -1, false)
  for _, entry in ipairs(focus) do
    if type(entry) == 'table' and type(entry.path) == 'string' then
      local name = entry.path:match('[^/]+$') or entry.path
      for index, line in ipairs(lines) do
        if line:find(name, 1, true) then
          local opts = { line_hl_group = 'WtPresentTree', priority = 210 }
          add_annotation(opts, entry.label, state.nvim_tree_win or api.nvim_get_current_win())
          api.nvim_buf_set_extmark(buf, tree_namespace, index - 1, 0, opts)
          break
        end
      end
    end
  end
end

local function focus_range(win, buf, artifact)
  if not valid_win(win) or not valid_buf(buf) then return end

  local count = math.max(1, api.nvim_buf_line_count(buf))
  local first = math.max(1, math.min(tonumber(artifact.startLine) or 1, count))
  local last = math.max(first, math.min(tonumber(artifact.endLine) or first, count))
  -- Avoid creating thousands of extmarks from a malformed or over-broad scene.
  last = math.min(last, first + 499)

  for line = first, last do
    local opts = {
      line_hl_group = 'WtPresentRange',
      priority = 210,
    }
    if line == first then add_annotation(opts, artifact.label, win) end
    api.nvim_buf_set_extmark(buf, namespace, line - 1, 0, opts)
  end

  pcall(api.nvim_win_set_cursor, win, { first, 0 })
  pcall(api.nvim_win_call, win, function() vim.cmd('normal! zz') end)
end

local function scratch_buffer(name, lines, filetype)
  local buf = api.nvim_create_buf(false, true)
  state.scratch_buffers[#state.scratch_buffers + 1] = buf
  pcall(api.nvim_buf_set_name, buf, name)
  vim.bo[buf].buftype = 'nofile'
  vim.bo[buf].bufhidden = 'hide'
  vim.bo[buf].swapfile = false
  vim.bo[buf].modifiable = true
  api.nvim_buf_set_lines(buf, 0, -1, false, lines)
  vim.bo[buf].modifiable = false
  vim.bo[buf].filetype = filetype
  return buf
end

local function render_file(artifact)
  local path, root = resolve_path(artifact.path, false)
  local win = current_main_window()
  save_snapshot(win)

  local buf = vim.fn.bufadd(path)
  vim.fn.bufload(buf)
  api.nvim_win_set_buf(win, buf)

  local diff_applied = false
  if artifact.kind == 'diff' or artifact.display == 'diff' then
    local base = vim.env.WT_DIFF_BASE
    if not base or base == '' then base = 'HEAD' end
    local ok, diff = pcall(require, 'unified.diff')
    if ok then
      local shown, result = pcall(diff.show, base, buf)
      diff_applied = shown and result ~= false
      local refresh_ok, auto_refresh = pcall(require, 'unified.auto_refresh')
      if refresh_ok then pcall(auto_refresh.setup, buf) end
      local state_ok, unified_state = pcall(require, 'unified.state')
      if state_ok then unified_state.main_win = win end
    end
  end

  state.win = win
  state.buf = buf
  focus_range(win, buf, artifact)
  set_deck_keymaps(buf)

  return {
    path = relative_path(path, root),
    line = api.nvim_win_get_cursor(win)[1],
    diffApplied = diff_applied,
  }
end

local function render_markdown(artifact, scene)
  local lines
  local source
  if type(artifact.content) == 'string' then
    lines = vim.split(artifact.content, '\n', { plain = true })
  elseif type(artifact.path) == 'string' and artifact.path ~= '' then
    local path, root = resolve_path(artifact.path, false)
    local ok, file_lines = pcall(vim.fn.readfile, path, '', 2000)
    if not ok then error('unable to read markdown artifact: ' .. artifact.path) end
    lines = file_lines
    source = relative_path(path, root)
  else
    lines = vim.split(scene.narrative or '', '\n', { plain = true })
  end
  local win = current_main_window()
  save_snapshot(win)
  local buf = scratch_buffer('[wt presentation] ' .. (scene.title or ''), lines, 'markdown')
  api.nvim_win_set_buf(win, buf)
  state.win = win
  state.buf = buf
  focus_range(win, buf, artifact)
  set_deck_keymaps(buf)
  return {
    line = api.nvim_win_get_cursor(win)[1],
    lines = #lines,
    path = source,
  }
end

local function render_nvim_tree(artifact)
  local root_path, project = resolve_path(artifact.root or '.', true)
  local ok, tree = pcall(require, 'nvim-tree.api')
  if not ok then return nil end

  local win = current_main_window()
  save_snapshot(win)
  tree.tree.open({ path = root_path, focus = true })
  pcall(tree.tree.reload)

  local focus = type(artifact.focus) == 'table' and artifact.focus or {}
  local focused
  local focus_paths = {}
  for _, entry in ipairs(focus) do
    if type(entry) == 'table' and type(entry.path) == 'string' then
      local candidate = vim.fs.normalize(root_path .. '/' .. entry.path)
      if is_within(candidate, root_path) then
        focus_paths[#focus_paths + 1] = candidate
        -- Make every highlighted path visible by expanding parent folders.
        pcall(tree.tree.find_file, { buf = candidate, focus = false, open = true })
      end
    end
  end
  if focus_paths[1] then
    pcall(tree.tree.find_file, { buf = focus_paths[1], focus = true, open = true })
    focused = relative_path(focus_paths[1], project)
  end

  state.win = api.nvim_get_current_win()
  state.buf = api.nvim_win_get_buf(state.win)
  state.nvim_tree_opened = true
  state.nvim_tree_win = state.win
  state.nvim_tree_peer_win = valid_win(win) and win or nil
  state.nvim_tree_artifact = artifact
  set_nvim_tree_keymaps(state.buf, tree)
  set_deck_keymaps(state.buf)
  highlight_nvim_tree_focus(state.buf, artifact)

  return {
    root = relative_path(root_path, project),
    explorer = 'nvim-tree',
    focused = focused,
  }
end

local function render_tree(artifact, scene)
  if artifact.view == 'explorer' then
    local rendered = render_nvim_tree(artifact)
    if rendered then return rendered end
  end

  local root_path, project = resolve_path(artifact.root or '.', true)
  local heading = relative_path(root_path, project)
  if heading == '.' then heading = vim.fn.fnamemodify(project, ':t') end

  local lines = { heading .. '/', '' }
  local focus_lines = {}
  local focus = type(artifact.focus) == 'table' and artifact.focus or {}

  if #focus > 0 then
    table.sort(focus, function(a, b)
      return tostring(a.path or '') < tostring(b.path or '')
    end)
    for _, entry in ipairs(focus) do
      if type(entry) == 'table' and type(entry.path) == 'string' then
        local candidate = vim.fs.normalize(root_path .. '/' .. entry.path)
        if is_within(candidate, root_path) then
          local depth = 0
          for _ in entry.path:gmatch('/') do depth = depth + 1 end
          local stat = uv.fs_stat(candidate)
          local suffix = stat and stat.type == 'directory' and '/' or ''
          lines[#lines + 1] = string.rep('  ', depth + 1) .. '• ' .. entry.path:match('[^/]+$') .. suffix
          focus_lines[#focus_lines + 1] = { line = #lines, label = entry.label or '' }
        end
      end
    end
  else
    local entries = {}
    for name, kind in vim.fs.dir(root_path) do
      if name ~= '.git' then entries[#entries + 1] = { name = name, kind = kind } end
      if #entries >= 200 then break end
    end
    table.sort(entries, function(a, b)
      if a.kind == b.kind then return a.name < b.name end
      return a.kind == 'directory'
    end)
    for _, entry in ipairs(entries) do
      lines[#lines + 1] = '  ' .. (entry.kind == 'directory' and '▸ ' or '  ') .. entry.name
        .. (entry.kind == 'directory' and '/' or '')
    end
  end

  local win = current_main_window()
  save_snapshot(win)
  local buf = scratch_buffer('[wt tree] ' .. (scene.title or heading), lines, 'wt_present_tree')
  api.nvim_win_set_buf(win, buf)
  state.win = win
  state.buf = buf
  set_deck_keymaps(buf)

  for _, item in ipairs(focus_lines) do
    local opts = { line_hl_group = 'WtPresentTree', priority = 210 }
    add_annotation(opts, item.label, win)
    api.nvim_buf_set_extmark(buf, namespace, item.line - 1, 0, opts)
  end
  if focus_lines[1] then
    pcall(api.nvim_win_set_cursor, win, { focus_lines[1].line, 0 })
    pcall(api.nvim_win_call, win, function() vim.cmd('normal! zz') end)
  end

  return { root = relative_path(root_path, project), entries = #lines - 2 }
end

function M.register_renderer(kind, renderer)
  assert(type(kind) == 'string' and kind ~= '', 'renderer kind is required')
  assert(type(renderer) == 'function', 'renderer must be a function')
  renderers[kind] = renderer
end

function M.show(scene)
  if type(scene) ~= 'table' then error('scene must be an object') end
  if scene.version ~= nil and tonumber(scene.version) ~= 1 then
    error('unsupported presentation protocol version: ' .. tostring(scene.version))
  end
  if type(scene.artifact) ~= 'table' then error('scene artifact is required') end

  local kind = scene.artifact.kind
  if kind ~= 'tree' or scene.artifact.view ~= 'explorer' then close_nvim_tree() end
  local renderer = renderers[kind]
  if not renderer then error('unsupported artifact kind: ' .. tostring(kind)) end

  clear_marks()
  state.scene = vim.deepcopy(scene)
  local rendered = renderer(scene.artifact, scene) or {}
  state.active = true

  return {
    ok = true,
    active = true,
    kind = kind,
    title = scene.title or '',
    rendered = rendered,
  }
end

local function deck_status(rendered)
  if state.mode ~= 'deck' or not state.deck then return nil end
  return {
    index = state.deck_index,
    count = #state.deck.scenes,
    title = state.deck.title or '',
    rendered = rendered,
  }
end

local function status_escape(text)
  return tostring(text or ''):gsub('%%', '%%%%')
end

local function apply_deck_chrome()
  if state.mode ~= 'deck' or not state.deck or not valid_win(state.win) then return end
  local title = state.deck.title or 'wt presentation'
  local counter = string.format('%d / %d', state.deck_index, #state.deck.scenes)
  vim.wo[state.win].winbar = status_escape(title .. '    ' .. counter .. '    H prev · L next · q quit · ? help')
  if valid_buf(state.buf) then
    api.nvim_buf_set_extmark(state.buf, namespace, 0, 0, {
      virt_lines = { { { '', 'Normal' } } },
      virt_lines_above = true,
      priority = 219,
    })
  end
end

function M.deck_render()
  if not state.deck or type(state.deck.scenes) ~= 'table' then error('no active deck') end
  local scene = state.deck.scenes[state.deck_index]
  if type(scene) ~= 'table' then error('deck scene is missing: ' .. tostring(state.deck_index)) end
  scene = vim.deepcopy(scene)
  if scene.version == nil then scene.version = state.deck.version or 1 end
  local result = M.show(scene)
  apply_deck_chrome()
  result.deck = deck_status(result.rendered)
  return result
end

function M.deck_show(deck)
  if type(deck) ~= 'table' then error('deck must be an object') end
  if deck.version ~= nil and tonumber(deck.version) ~= 1 then
    error('unsupported deck protocol version: ' .. tostring(deck.version))
  end
  if type(deck.scenes) ~= 'table' or #deck.scenes == 0 then error('deck scenes are required') end

  state.mode = 'deck'
  state.deck = vim.deepcopy(deck)
  state.deck_index = math.max(1, math.min(tonumber(deck.startIndex) or 1, #state.deck.scenes))
  return M.deck_render()
end

function M.deck_next()
  if not state.deck then return { ok = false, error = 'no active deck' } end
  state.deck_index = math.min(state.deck_index + 1, #state.deck.scenes)
  return M.deck_render()
end

function M.deck_prev()
  if not state.deck then return { ok = false, error = 'no active deck' } end
  state.deck_index = math.max(state.deck_index - 1, 1)
  return M.deck_render()
end

function M.deck_goto(index)
  if not state.deck then return { ok = false, error = 'no active deck' } end
  local next_index = tonumber(index)
  if not next_index then error('deck index must be a number') end
  state.deck_index = math.max(1, math.min(next_index, #state.deck.scenes))
  return M.deck_render()
end

function M.deck_help()
  local lines = {
    '# wt presentation controls',
    '',
    'H / Left   previous slide',
    'L / Right  next slide',
    'q          close presentation and restore editor',
    '?          show this help',
  }
  local win = current_main_window()
  local buf = scratch_buffer('[wt presentation help]', lines, 'markdown')
  api.nvim_win_set_buf(win, buf)
  state.win = win
  state.buf = buf
  set_deck_keymaps(buf)
  return { ok = true, active = true, help = true }
end

function M.reapply()
  if not state.active or not state.scene or not valid_win(state.win) or not valid_buf(state.buf) then return end
  clear_marks()
  focus_range(state.win, state.buf, state.scene.artifact)
end

function M.context()
  local win = valid_win(state.win) and state.win or api.nvim_get_current_win()
  local buf = api.nvim_win_get_buf(win)
  local cursor = api.nvim_win_get_cursor(win)
  local name = api.nvim_buf_get_name(buf)
  local root = project_root()
  local context = {
    ok = true,
    active = state.active,
    path = name ~= '' and relative_path(vim.fs.normalize(name), root) or '',
    line = cursor[1],
    column = cursor[2] + 1,
    filetype = vim.bo[buf].filetype,
  }

  local first = api.nvim_buf_get_mark(buf, '<')
  local last = api.nvim_buf_get_mark(buf, '>')
  if first[1] > 0 and last[1] > 0 then
    local start_line, end_line = first[1], last[1]
    local start_col, end_col = first[2], last[2]
    if start_line > end_line or (start_line == end_line and start_col > end_col) then
      start_line, end_line = end_line, start_line
      start_col, end_col = end_col, start_col
    end
    local start_text = api.nvim_buf_get_lines(buf, start_line - 1, start_line, false)[1] or ''
    local end_text = api.nvim_buf_get_lines(buf, end_line - 1, end_line, false)[1] or ''
    start_col = math.max(0, math.min(start_col, #start_text))
    end_col = math.max(0, math.min(end_col + 1, #end_text))
    local got_text, selected = pcall(
      api.nvim_buf_get_text,
      buf,
      start_line - 1,
      start_col,
      end_line - 1,
      end_col,
      {}
    )
    if got_text then
      local text = table.concat(selected, '\n')
      if #text > 16384 then text = text:sub(1, 16384) .. '\n[selection truncated]' end
      context.selection = {
        startLine = start_line,
        endLine = end_line,
        text = text,
      }
    end
  end

  return context
end

function M.clear()
  clear_keymaps()
  clear_marks()
  local snapshot = state.snapshot

  if snapshot then
    if snapshot.tab and api.nvim_tabpage_is_valid(snapshot.tab) then
      pcall(api.nvim_set_current_tabpage, snapshot.tab)
    end
    if valid_win(snapshot.target_win) and valid_buf(snapshot.target_buf) then
      pcall(api.nvim_win_set_buf, snapshot.target_win, snapshot.target_buf)
      pcall(api.nvim_win_set_cursor, snapshot.target_win, snapshot.target_cursor)
      pcall(function() vim.wo[snapshot.target_win].winbar = snapshot.target_winbar or '' end)
      pcall(api.nvim_win_call, snapshot.target_win, function()
        if snapshot.target_view then vim.fn.winrestview(snapshot.target_view) end
      end)
    end
    if valid_win(snapshot.current_win) then pcall(api.nvim_set_current_win, snapshot.current_win) end
  end

  close_nvim_tree()

  for _, buf in ipairs(state.scratch_buffers) do
    if valid_buf(buf) then pcall(api.nvim_buf_delete, buf, { force = true }) end
  end

  state.active = false
  state.mode = nil
  state.scene = nil
  state.deck = nil
  state.deck_index = 1
  state.snapshot = nil
  state.win = nil
  state.buf = nil
  state.scratch_buffers = {}
  state.mapped_buffers = {}
  state.nvim_tree_opened = false
  state.nvim_tree_win = nil
  state.nvim_tree_peer_win = nil
  state.nvim_tree_artifact = nil
  return { ok = true, active = false }
end

function M.dispatch(request)
  if type(request) ~= 'table' then return { ok = false, error = 'request must be an object' } end
  local action = request.action
  local ok, result = pcall(function()
    if action == 'show' then return M.show(request.scene) end
    if action == 'deck_show' then return M.deck_show(request.deck) end
    if action == 'deck_next' then return M.deck_next() end
    if action == 'deck_prev' then return M.deck_prev() end
    if action == 'deck_goto' then return M.deck_goto(request.index) end
    if action == 'context' then return M.context() end
    if action == 'clear' then return M.clear() end
    if action == 'ping' then return { ok = true, active = state.active, version = 1 } end
    error('unknown presentation action: ' .. tostring(action))
  end)
  if ok then return result end
  return { ok = false, error = tostring(result) }
end

M.register_renderer('file', render_file)
M.register_renderer('diff', render_file)
M.register_renderer('markdown', render_markdown)
M.register_renderer('tree', render_tree)

package.loaded['wt_present'] = M

-- Unified refreshes its buffers asynchronously and may move the cursor back to
-- the first hunk. Reapply the active scene after its base update has settled.
api.nvim_create_autocmd('User', {
  pattern = 'UnifiedBaseCommitUpdated',
  callback = function()
    if state.active then vim.schedule(M.reapply) end
  end,
})

return M
