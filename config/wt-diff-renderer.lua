-- Managed checkout renderer. Unified owns diff semantics; WT owns the target,
-- polling lifetime and inert recovery data. No saved Ex commands are executed.
local api, uv = vim.api, vim.uv or vim.loop
local M = {}
function M.new(opts)
  local self = { alive = true }
  local root, base = opts.root, opts.base
  local timer, poll, busy, pending, fingerprint, candidate
  local rendered, jobs = {}, {}
  local index_path
  local group = api.nvim_create_augroup('WtManagedDiff', { clear = true })
  local tool = vim.env.WT_DIFF_TOOL == 'diffview' and 'diffview' or 'unified'
  local function path(file)
    assert(type(file) == 'string' and file ~= '' and not file:find('%z'), 'invalid diff file')
    local absolute = vim.fs.normalize(file:sub(1, 1) == '/' and file or root .. '/' .. file)
    assert(absolute:sub(1, #root + 1) == root .. '/', 'diff file escapes target')
    local parent = absolute
    while not uv.fs_lstat(parent) do parent = vim.fs.dirname(parent) end
    local real = assert(uv.fs_realpath(parent), 'invalid diff file symlink')
    assert(real == root or real:sub(1, #root + 1) == root .. '/', 'diff file escapes target')
    return absolute
  end
  local function position(value)
    assert(type(value) == 'table', 'invalid diff position')
    path(value.file)
    assert(not value.file:find('[\r\n]'), 'newline diff paths cannot be checkpointed')
    for _, k in ipairs({ 'line', 'column', 'topline', 'leftcol' }) do
      local n = value[k]
      assert(type(n) == 'number' and n == math.floor(n) and n >= ((k == 'line' or k == 'topline') and 1 or 0), 'invalid diff position')
    end
    return value
  end
  function self.close()
    if not self.alive then return end
    self.alive = false
    if timer then timer:stop(); timer:close(); timer = nil end
    if poll then poll:kill(15); poll = nil end
    for job in pairs(jobs) do job:kill(15) end
    jobs = {}
    pcall(api.nvim_del_augroup_by_id, group)
    if tool == 'unified' and self.tree_buffer then
      local ts = require('unified.file_tree.state')
      if api.nvim_win_is_valid(self.tree_window)
          and api.nvim_win_get_buf(self.tree_window) == self.tree_buffer then
        pcall(api.nvim_win_close, self.tree_window, true)
      end
      if api.nvim_buf_is_valid(self.tree_buffer) then
        pcall(api.nvim_buf_delete, self.tree_buffer, { force = true })
      end
      if ts.buffer == self.tree_buffer then require('unified.state').set_active(false) end
    end
  end
  function self.snapshot()
    if not self.ready then return opts.position end
    local win, file
    if tool == 'diffview' then
      local view = require('diffview.lib').get_current_view()
      if not view or not view.cur_entry or not view.cur_layout then return nil end
      win, file = view.cur_layout:get_main_win().id, path(view.cur_entry.path)
    else win = require('unified.state').get_main_window() end
    if not win then return nil end
    file = file or api.nvim_buf_get_name(api.nvim_win_get_buf(win))
    if file == '' then return nil end
    local view = api.nvim_win_call(win, vim.fn.winsaveview)
    return position({ file = path(file):sub(#root + 2), line = view.lnum,
      column = view.col, topline = view.topline, leftcol = view.leftcol })
  end
  local function baseline_available(file)
    -- Unified treats git-show exit 128 as a new file, including a missing blob.
    -- Distinguish absence from unavailable objects before asking it to render.
    local listing = vim.system({ 'git', '--literal-pathspecs', 'ls-tree', '-z',
      base, '--', file:sub(#root + 2) }, { cwd = root }):wait()
    assert(listing.code == 0, 'baseline tree unavailable: ' .. (listing.stderr or ''))
    if listing.stdout ~= '' then
      local object = assert(listing.stdout:match('^%d+ %w+ (%x+)\t'), 'invalid baseline entry')
      local available = vim.system({ 'git', 'cat-file', '-e', object }, { cwd = root }):wait()
      assert(available.code == 0, 'baseline object unavailable; fetch explicitly before retrying')
    end
  end
  local function selected_refresh()
    local win = require('unified.state').get_main_window()
    if not win then return end
    local buf = api.nvim_win_get_buf(win)
    local name = api.nvim_buf_get_name(buf)
    if name == '' or not pcall(path, name) or vim.bo[buf].modified then return end
    local stat = uv.fs_stat(name)
    local signature = (stat and table.concat({stat.size, stat.mtime.sec, stat.mtime.nsec,
      stat.ctime.sec, stat.ctime.nsec}, ':') or 'missing')
    local key = signature .. ':' .. api.nvim_buf_get_changedtick(buf)
    if rendered[buf] == key then return true end
    local view = api.nvim_win_call(win, vim.fn.winsaveview)
    local ok, err = pcall(function()
      baseline_available(name)
      if stat then
        if vim.b[buf].unified_deleted_view then
          vim.b[buf].unified_deleted_view = nil
          vim.bo[buf].modifiable, vim.bo[buf].readonly = true, false
        end
        -- checktime reloads only unmodified buffers. Never :edit! a reader's work.
        api.nvim_buf_call(buf, function()
          local autoread = vim.bo[buf].autoread
          vim.bo[buf].autoread = true
          vim.cmd('checktime ' .. buf)
          vim.bo[buf].autoread = autoread
        end)
      elseif not vim.b[buf].unified_deleted_view then
        vim.bo[buf].modifiable = true
        api.nvim_buf_set_lines(buf, 0, -1, false, {})
        vim.bo[buf].modified = false
      end
      assert(require('unified.diff').show(base, buf) == true, 'Unified could not render this file')
      rendered[buf] = signature .. ':' .. api.nvim_buf_get_changedtick(buf)
    end)
    if not ok then vim.notify('WT diff render failed: ' .. tostring(err), vim.log.levels.ERROR) end
    if api.nvim_win_is_valid(win) then
      api.nvim_win_call(win, function() vim.fn.winrestview(view) end)
    end
    return ok
  end
  local function open_node(node)
    if not self.alive or not node or node.is_dir then return end
    local file = path(node.path)
    local state = require('unified.state')
    local win = state.get_main_window()
    assert(win and api.nvim_win_is_valid(win), 'WT diff content window unavailable')
    -- Unified's current action escapes an Ex filename before bufadd(), which
    -- opens the wrong file for spaces/backslashes/newlines. These are native
    -- APIs: keep the raw, contained path, and let Unified render the diff.
    local buf = vim.fn.bufadd(file)
    vim.fn.bufload(buf)
    api.nvim_win_set_buf(win, buf)
    state.main_win = win
    -- Loaded buffers can be stale, or contain unsaved text (including an empty
    -- deletion). Use the same guarded disk reconciliation as background refresh.
    if vim.bo[buf].modified or not selected_refresh() then return end
    if require('unified.config').values.jump_to_first_hunk then
      require('unified.navigation').jump_to_first_hunk(win, buf)
    end
  end
  local function bind_tree()
    local ts = require('unified.file_tree.state')
    local function select()
      local node = ts.line_to_node[api.nvim_win_get_cursor(ts.window)[1] - 1]
      local ok, err = pcall(open_node, node)
      if not ok then vim.notify('WT diff open failed: ' .. tostring(err), vim.log.levels.ERROR) end
    end
    for _, key in ipairs({ 'l', '<CR>' }) do
      vim.keymap.set('n', key, select, { buffer = ts.buffer, desc = 'Open exact checkout path' })
    end
    vim.keymap.set('n', 'R', function() self.refresh() end, { buffer = ts.buffer, desc = 'Refresh checkout diff' })
  end
  function self.refresh()
    if not self.alive or not self.ready then return end
    if tool == 'diffview' then
      vim.cmd('DiffviewRefresh')
      return
    end
    if busy then pending = true; return end
    local ts = require('unified.file_tree.state')
    local buf, win = ts.buffer, ts.window
    if not require('unified.state').is_active() or not win or not api.nvim_win_is_valid(win)
        or not buf or not api.nvim_buf_is_valid(buf) then self.close(); return end
    busy = true
    local tree = require('unified.file_tree.tree').new(root)
    -- Unified's name-status query can mistake stat-only changes for content
    -- changes when index refresh writes are disabled. Reconcile its nodes using
    -- read-only numstat + porcelain paths, never compute/render a diff here.
    local function query(args, callback)
      local job
      job = vim.system(args, { cwd = root }, function(result)
        vim.schedule(function()
          jobs[job] = nil
          if not self.alive then return end
          callback(result)
        end)
      end)
      jobs[job] = true
    end
    local function finish(ok)
      busy = false
      if not self.alive or ts.buffer ~= buf or ts.window ~= win
          or not api.nvim_buf_is_valid(buf) or not api.nvim_win_is_valid(win) then return end
      if ok then
        -- Capture at completion: a reader may have moved while Git ran.
        local view = api.nvim_win_call(win, vim.fn.winsaveview)
        local node = ts.line_to_node[view.lnum - 1]
        -- Unified derives labels from paths. Render an escaped projection,
        -- then restore authoritative node identities for all navigation actions.
        local projection = vim.deepcopy(tree)
        local originals = {}
        local function escape(text)
          return text:gsub('\\', '\\\\'):gsub('[%c]', function(c)
            return string.format('\\x%02x', string.byte(c))
          end)
        end
        local function project(raw, display)
          originals[display] = raw
          display.name, display.path = escape(raw.name), escape(raw.path)
          for key, child in pairs(raw.children or {}) do project(child, display.children[key]) end
        end
        project(tree.root, projection.root)
        require('unified.file_tree.render').render_tree(projection, buf)
        for line, display in pairs(ts.line_to_node) do ts.line_to_node[line] = originals[display] end
        ts.current_tree = tree
        if node then
          for line, next_node in pairs(ts.line_to_node) do
            if next_node.path == node.path then view.lnum = line + 1; break end
          end
        end
        api.nvim_win_call(win, function() vim.fn.winrestview(view) end)
        selected_refresh()
      end
      if pending then pending = false; self.refresh() end
    end
    tree:update_git_status(root, true, base, function(ok)
      if not self.alive then return end
      if not ok then finish(false); return end
      query({ 'git', 'diff', '--numstat', '--no-renames', '-z', base, '--' }, function(diff)
        if diff.code ~= 0 then vim.notify('WT diff refresh failed: ' .. (diff.stderr or ''), vim.log.levels.ERROR); finish(false); return end
        local paths = {}
        for entry in (diff.stdout or ''):gmatch('[^%z]+') do
          local file = entry:match('^[^\t]+\t[^\t]+\t(.*)$')
          if file then paths[root .. '/' .. file] = 'M ' end
        end
        query({ 'git', 'status', '--porcelain', '-z', '--untracked-files=all' }, function(status)
          if status.code ~= 0 then vim.notify('WT diff refresh failed: ' .. (status.stderr or ''), vim.log.levels.ERROR); finish(false); return end
          local skip = false
          for entry in (status.stdout or ''):gmatch('[^%z]+') do
            if skip then skip = false
            else
              local code, file = entry:sub(1, 2), entry:sub(4)
              paths[root .. '/' .. file] = code
              skip = code:find('[RC]') ~= nil -- porcelain -z puts destination first
            end
          end
          local function prune(node)
            for name, child in pairs(node.children or {}) do
              if child.is_dir then prune(child)
              elseif not paths[child.path] then node.children[name] = nil
              else paths[child.path] = nil end
            end
            node.ordered_children = {}
            for _, child in pairs(node.children or {}) do
              node.ordered_children[#node.ordered_children + 1] = child
            end
          end
          prune(tree.root)
          for file, code in pairs(paths) do
            -- FileTree:add_file treats backslashes as separators even on Unix.
            -- Missing exact paths get native Unified nodes; its renderer groups
            -- by node.path, so no filesystem-path reconstruction is necessary.
            local node = require('unified.file_tree.node').new(file:sub(#root + 2), false)
            node.path, node.status = file, code
            tree.root:add_child(node)
          end
          tree:update_parent_statuses(tree.root)
          tree.root:sort()
          finish(true)
        end)
      end)
    end)
  end
  local function scan()
    if not self.alive or poll then return end
    -- Enumerate paths, not contents: large untracked files are only diffed when
    -- selected and changed. Disable optional Git index refresh writes.
    poll = vim.system({ 'git', '-C', root, 'ls-files', '-z', '--cached', '--others', '--exclude-standard' },
      { env = { GIT_OPTIONAL_LOCKS = '0' } }, function(result)
        vim.schedule(function()
          poll = nil
          if not self.alive then return end
          if result.code ~= 0 then return end
          local entries = {}
          for file in (result.stdout or ''):gmatch('[^%z]+') do
            local stat = uv.fs_stat(root .. '/' .. file)
            entries[#entries + 1] = file .. ':' .. (stat and table.concat({ stat.size,
              stat.mtime.sec, stat.mtime.nsec, stat.ctime.sec, stat.ctime.nsec }, ':') or 'missing')
          end
          local index = index_path and uv.fs_stat(index_path)
          if index then entries[#entries + 1] = table.concat({index.size, index.mtime.sec, index.mtime.nsec}, ':') end
          local next_value = table.concat(entries, '\0')
          -- Two stable samples debounce bursts without dropping newly added files.
          if candidate == next_value and fingerprint ~= next_value then
            fingerprint = next_value
            self.refresh()
          end
          candidate = next_value
        end)
      end)
  end
  function self.open()
    if not self.alive then return end
    -- Applies before cat-file and to every subsequent plugin Git subprocess.
    vim.env.GIT_NO_LAZY_FETCH = '1'
    assert(type(base) == 'string' and (#base == 40 or #base == 64) and base:match('^%x+$'), 'diff requires an exact base SHA')
    local result = vim.system({ 'git', '-C', root, 'cat-file', '-e', base .. '^{commit}' }):wait()
    assert(result.code == 0, 'diff base unavailable in target checkout: ' .. base)
    local index = vim.system({ 'git', '-C', root, 'rev-parse', '--path-format=absolute', '--git-path', 'index' }):wait()
    assert(index.code == 0, 'diff index path unavailable')
    index_path = vim.trim(index.stdout)
    if opts.position then position(opts.position) end
    vim.api.nvim_set_current_dir(root)
    vim.env.GIT_OPTIONAL_LOCKS = '0'
    -- git diff's autoRefreshIndex can still write stat-cache entries even with
    -- optional locks disabled. Apply a process-local Git config, never git config
    -- on the checkout or user's HOME (Unified issues its own Git argv).
    local count = tonumber(vim.env.GIT_CONFIG_COUNT) or 0
    vim.env['GIT_CONFIG_KEY_' .. count] = 'diff.autoRefreshIndex'
    vim.env['GIT_CONFIG_VALUE_' .. count] = 'false'
    vim.env.GIT_CONFIG_COUNT = tostring(count + 1)
    if tool == 'diffview' then
      local ok, plugin = pcall(require, 'diffview')
      assert(ok, 'WT diff: diffview.nvim is not installed')
      local args = { base }
      if opts.position then
        args[#args + 1] = '--selected-file=' .. path(opts.position.file)
        local restored = false
        api.nvim_create_autocmd('User', { group = group, pattern = 'DiffviewDiffBufWinEnter', callback = function()
          vim.schedule(function()
            if not self.alive or restored then return end
            local view = require('diffview.lib').get_current_view()
            if not view or not view.cur_entry or view.cur_entry.path ~= opts.position.file then return end
            local win = view.cur_layout:get_main_win().id
            if not api.nvim_win_is_valid(win) then return end
            api.nvim_win_call(win, function()
              vim.fn.winrestview({lnum=opts.position.line, col=opts.position.column,
                topline=opts.position.topline, leftcol=opts.position.leftcol})
            end)
            restored = true
          end)
        end })
      end
      plugin.open(args)
      vim.notify('WT diff: Diffview live refresh can fail on stat-only or staged/restored changes. '
        .. 'Unified is the supported live renderer (WT_DIFF_TOOL=unified). Index safety remains enforced.', vim.log.levels.WARN)
    else
      local ok, plugin = pcall(require, 'unified')
      assert(ok, 'WT diff: unified.nvim is not installed')
      -- Use the native tree, not the optional picker/tab backend, in this
      -- dedicated managed editor. Preserve all other renderer preferences.
      local config = require('unified.config')
      plugin.setup(vim.tbl_deep_extend('force', config.values, {
        tab = false, file_tree = { enabled = true, focus = false }, auto_refresh = false,
      }))
      local initial = api.nvim_create_buf(false, true)
      api.nvim_win_set_buf(0, initial)
      local state = require('unified.state')
      state.set_active(true)
      state.set_backend('default')
      state.main_win = api.nvim_get_current_win()
      -- Keep Unified's native window setup, but supersede its initial buffer:
      -- the plugin's unversioned async startup callback captures that buffer.
      -- Invalidating it prevents a late raw render from replacing our tree.
      state.set_commit_base(base)
      local ts = require('unified.file_tree.state')
      local old = assert(ts.buffer, 'Unified tree buffer unavailable')
      local name = api.nvim_buf_get_name(old)
      local replacement = api.nvim_create_buf(false, true)
      self.tree_buffer, self.tree_window = replacement, assert(ts.window)
      ts.buffer, state.file_tree_buf = replacement, replacement
      ts.line_to_node, ts.current_tree = {}, nil
      api.nvim_win_set_buf(ts.window, replacement)
      if api.nvim_buf_is_valid(old) then api.nvim_buf_delete(old, { force = true }) end
      api.nvim_buf_set_name(replacement, name)
      vim.bo[replacement].buftype, vim.bo[replacement].bufhidden = 'nofile', 'hide'
      vim.bo[replacement].swapfile = false
      vim.bo[replacement].filetype = 'unified_tree'
      bind_tree()
      if opts.position then
        local file = path(opts.position.file)
        local buf = vim.fn.bufadd(file)
        vim.fn.bufload(buf)
        api.nvim_win_set_buf(state.main_win, buf)
        assert(selected_refresh(), 'WT diff recovery could not render selected file')
        api.nvim_win_call(state.main_win, function()
          vim.fn.winrestview({ lnum = opts.position.line, col = opts.position.column,
            topline = opts.position.topline, leftcol = opts.position.leftcol })
        end)
      else
        api.nvim_buf_set_lines(0, 0, -1, false, { 'WT checkout diff', '',
          'Select a file in the Unified tree (l / Enter).',
          'Includes untracked files; external changes refresh automatically.',
          'An empty tree means no checkout changes against the selected base.' })
        vim.bo.modified = false
      end
    end
    self.ready = true
    if tool == 'unified' then self.refresh() end
    timer = uv.new_timer()
    timer:start(500, 500, vim.schedule_wrap(scan))
    api.nvim_create_autocmd({ 'VimLeavePre' }, { group = group, callback = self.close })
    if tool == 'diffview' then
      api.nvim_create_autocmd('User', { group = group, pattern = 'DiffviewViewClosed', callback = self.close })
    end
    api.nvim_create_autocmd({ 'BufWritePost', 'InsertLeave' }, { group = group, callback = self.refresh })
    api.nvim_create_autocmd('WinClosed', { group = group, callback = function()
      if tool == 'unified' then
        local win = require('unified.file_tree.state').window
        if win and tostring(win) == vim.fn.expand('<amatch>') then self.close() end
      end
    end })
  end
  return self
end
return M
