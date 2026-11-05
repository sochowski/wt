-- The review panel: markdown slides and docs that reload while an agent
-- writes them, and a diff pane under them that follows the slides. review-show
-- starts nvim with this file and calls into it over the --listen socket; the
-- functions return 0 (or an error message) so --remote-expr stays quiet.
--
-- Slides are one markdown file split on lines of `---` outside code fences,
-- rendered by render-markdown (mermaid blocks too, when its mermaid plugin and
-- a renderer are installed), with treesitter highlighting fenced code.
--
-- Keys on a slide keep vim's meaning where they can: j / k, gg / G, ctrl-d
-- and friends move within the slide; J / K (or ]] / [[) go to the next and
-- previous slide and take a count; H / L go to the first and last; q quits;
-- g? lists the keys. :SlideEdit opens the deck at the current slide, and
-- :Slide N jumps to slide N.
--
-- The diff pane shows one layer of the stack (review/layers.tsv) at a time,
-- from review/diffs/<n>.diff. A slide with a line like
--   Diff: #4512 repos/Runlayer/proxy/verdict.py:88
-- moves the pane there when you land on the slide. In the pane, ]p / [p, ]f /
-- [f and ]c / [c page by PR, file and hunk; gf opens the file at the side of
-- the line under the cursor; gd opens the file in a tab, in unified.nvim
-- when the layer's head is the checkout, else diffview. Agent notes from
-- review/annotations.tsv (pr, path, line, note) show above the lines they're
-- about. Every file the cursor enters is appended to review/viewed.tsv, and
-- gu lists the changed files not viewed yet.

local M = {}
local uv = vim.uv or vim.loop
local root = vim.fn.getcwd()
local main = vim.api.nvim_get_current_win()
local deck = { path = nil, idx = 1, want = nil, count = 0, starts = {}, buf = nil, stamp = nil, anchored = nil }
local code_win = nil
local ns = vim.api.nvim_create_namespace('wt_review')
local pane = { win = nil, pr = nil, stacked = true }
local stack = { stamp = nil, list = {} }
local views = {} -- pr -> { buf, pr, stamp, files, info, cursor }
local notes = { stamp = nil, rows = {} }
local seen = nil -- 'pr\tpath' -> true
local blobs = {} -- 'rev:path' -> buf

local function stamp(path)
  local st = path and uv.fs_stat(path)
  return st and (st.mtime.sec .. '.' .. st.mtime.nsec .. ':' .. st.size) or nil
end

local function trim(lines)
  while #lines > 0 and lines[1]:match('^%s*$') do table.remove(lines, 1) end
  while #lines > 0 and lines[#lines]:match('^%s*$') do table.remove(lines) end
  return lines
end

local function valid(win) return win and vim.api.nvim_win_is_valid(win) end

-- Split on `---` lines outside fences, dropping empty slides. Also returns
-- the line each slide starts on in the file, for :SlideEdit.
local function split(lines)
  local slides, starts, cur, start, fence = {}, {}, {}, 1, false
  local function flush(next_start)
    if #trim(cur) > 0 then
      table.insert(slides, cur)
      table.insert(starts, start)
    end
    cur, start = {}, next_start
  end
  for i, l in ipairs(lines) do
    if l:match('^%s*```') or l:match('^%s*~~~') then fence = not fence end
    if not fence and l:match('^%-%-%-+%s*$') then
      flush(i + 1)
    else
      table.insert(cur, l)
    end
  end
  flush()
  return slides, starts
end

-- Comments. c on a slide appends a `> **you:**` line to that slide in the
-- deck file, named by vim.g.wt_review_name or $USER, and a then a letter
-- appends a pick. S (or :Send, or :send) types every
-- comment not yet sent into the agent's terminal, which the deck names with an
-- `<!-- agent: BLOCK [SESSION] -->` line, and ]n / [n jump between slides
-- still in discussion. The agent answers under each one
-- with `> **claude:**`; a comment with a reply below it counts as sent.
local ME = '> **' .. (vim.g.wt_review_name or vim.env.USER or 'me') .. ':**'
local AGENT = '> **claude:**'
local sent = {} -- deck path -> { [heading .. '\0' .. text] = true }

local function heading(slide, i)
  for _, l in ipairs(slide) do
    local h = l:match('^#+%s+(.+)')
    if h then return (h:match('^(.-)%s+·') or h) end
  end
  return 'slide ' .. i
end

-- The comments on one slide: { text, key, unsent } for each of yours.
local function comments(slide, i, path)
  local out, h = {}, heading(slide, i)
  for _, l in ipairs(slide) do
    if l:sub(1, #ME) == ME then
      local text = vim.trim(l:sub(#ME + 1))
      table.insert(out, { text = text, key = h .. '\0' .. text, heading = h })
    elseif l:sub(1, #AGENT) == AGENT then
      for _, c in ipairs(out) do c.replied = true end
    end
  end
  local done = sent[path] or {}
  for _, c in ipairs(out) do c.unsent = not (c.replied or done[c.key]) end
  return out
end

local function agent_target(lines)
  for _, l in ipairs(lines) do
    local block, session = l:match('<!%-%-%s*agent:%s*(%S+)%s*(%S*)%s*%-%->')
    if block then return block, session ~= '' and session or nil end
  end
end

local function main_win()
  if not valid(main) then
    for _, w in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
      if w ~= pane.win and w ~= code_win then main = w break end
    end
    if not valid(main) then main = vim.api.nvim_get_current_win() end
  end
  return main
end

local function resolve(path)
  path = vim.fn.expand(path)
  if path:sub(1, 1) == '/' then return path end
  if uv.fs_stat(root .. '/' .. path) then return root .. '/' .. path end
  for _, repo in ipairs(vim.fn.glob(root .. '/repos/*', false, true)) do
    if uv.fs_stat(repo .. '/' .. path) then return repo .. '/' .. path end
  end
  return root .. '/' .. path
end

-- The window files open in, reused on the next call: beside the diff when
-- it is under the slides (the slides are short then), else beside the slides.
local function code_window()
  if not valid(code_win) then
    vim.api.nvim_set_current_win((valid(pane.win) and pane.stacked) and pane.win or main_win())
    vim.cmd('rightbelow vsplit')
    code_win = vim.api.nvim_get_current_win()
  end
  vim.api.nvim_set_current_win(code_win)
  return code_win
end

local function jump(win, line, how)
  if not tonumber(line) then return end
  pcall(vim.api.nvim_win_set_cursor, win, { tonumber(line), 0 })
  vim.api.nvim_win_call(win, function() vim.cmd('normal! ' .. (how or 'zz')) end)
end

-- Stack and diffs ----------------------------------------------------------

local function review_file(name) return root .. '/review/' .. name end

local function read_tsv(path)
  local ok, lines = pcall(vim.fn.readfile, path)
  local rows = {}
  for _, l in ipairs(ok and lines or {}) do
    if l ~= '' then table.insert(rows, vim.split(l, '\t', { plain = true })) end
  end
  return rows
end

-- layers.tsv: order pr parent base_sha head_sha author title url
local function layers()
  local path = review_file('layers.tsv')
  local s = stamp(path)
  if s ~= stack.stamp then
    stack.stamp, stack.list = s, {}
    for _, r in ipairs(read_tsv(path)) do
      table.insert(stack.list, { pr = r[2], lo = r[4], hi = r[5], title = r[7] or '', url = r[8] or '' })
    end
  end
  return stack.list
end

local function layer_index(pr)
  pr = tostring(pr or ''):gsub('^#', '')
  for i, l in ipairs(layers()) do
    if l.pr == pr then return i end
  end
end

local function layer(pr) return layers()[layer_index(pr) or 0] end

-- The checkout a layer's repo is in: the one named after the repo, else any.
local checkouts = {}
local function checkout(l)
  local slug = l.url:match('github%.com/([^/]+/[^/]+)/pull/')
  if not slug then return nil end
  if checkouts[slug] == nil then
    checkouts[slug] = false
    local dirs = vim.fn.glob(root .. '/repos/*', false, true)
    table.insert(dirs, 1, root .. '/repos/' .. slug:match('/(.*)$'))
    for _, r in ipairs(dirs) do
      local u = vim.fn.systemlist({ 'git', '-C', r, 'remote', 'get-url', 'origin' })[1]
      if vim.v.shell_error == 0 and u then
        u = u:gsub('%.git$', ''):lower()
        local tail = u:sub(-#slug - 1)
        if tail == '/' .. slug:lower() or tail == ':' .. slug:lower() then
          checkouts[slug] = r
          break
        end
      end
    end
  end
  return checkouts[slug] or nil
end

-- Paths in slides and packages are repos/<repo>/<path> or repo-relative.
local function norm(path)
  path = path:gsub('^' .. vim.pesc(root) .. '/', '')
  return (path:gsub('^repos/[^/]+/', ''))
end

-- Index a unified diff: files with their header line, hunks and counts, and
-- for each buffer line its file, hunk and old/new line numbers.
local function parse(lines)
  local files, info = {}, {}
  local f, old, new
  for i, l in ipairs(lines) do
    local b = l:match('^diff %-%-git a/.- b/(.*)$')
    if b then
      f = { path = b, start = i, hunks = {}, hunk_new = {}, add = 0, del = 0 }
      table.insert(files, f)
      old, new = nil, nil
      info[i] = { file = #files }
    elseif f then
      local o, n = l:match('^@@ %-(%d+),?%d* %+(%d+),?%d* @@')
      local h = #f.hunks
      if o then
        old, new = tonumber(o), tonumber(n)
        table.insert(f.hunks, i)
        table.insert(f.hunk_new, new)
        info[i] = { file = #files, hunk = h + 1 }
      elseif not new then
        info[i] = { file = #files }
      else
        local c = l:sub(1, 1)
        if c == '+' then
          info[i] = { file = #files, hunk = h, new = new }
          new, f.add = new + 1, f.add + 1
        elseif c == '-' then
          info[i] = { file = #files, hunk = h, old = old }
          old, f.del = old + 1, f.del + 1
        elseif c == ' ' or l == '' then
          info[i] = { file = #files, hunk = h, new = new, old = old }
          new, old = new + 1, old + 1
        else
          info[i] = { file = #files, hunk = h }
        end
      end
    end
  end
  return files, info
end

local function file_at(v, lnum)
  local inf = v.info[lnum]
  if inf then return inf.file end
  return (#v.files > 0 and lnum > v.files[#v.files].start) and #v.files or nil
end

-- The buffer line for path (and line, on the new side) in a view: the line
-- itself, else the hunk around or before it, else the file's header.
local function locate(v, path, line)
  for fi, f in ipairs(v.files) do
    if f.path == path then
      if not line then return f.start end
      local stop = (v.files[fi + 1] and v.files[fi + 1].start or #v.info + 1) - 1
      for i = f.start, stop do
        local inf = v.info[i]
        if inf and inf.new == line and inf.old == nil then return i end
      end
      for i = f.start, stop do
        local inf = v.info[i]
        if inf and inf.new == line then return i end
      end
      local best = f.start
      for h, n in ipairs(f.hunk_new) do
        if n <= line then best = f.hunks[h] end
      end
      return best
    end
  end
end

local function load_seen()
  if not seen then
    seen = {}
    for _, r in ipairs(read_tsv(review_file('viewed.tsv'))) do
      seen[r[1] .. '\t' .. (r[2] or '')] = true
    end
  end
  return seen
end

local function mark_seen(pr, path)
  local k = pr .. '\t' .. path
  if load_seen()[k] then return end
  seen[k] = true
  pcall(vim.fn.writefile, { k }, review_file('viewed.tsv'), 'a')
end

local function wrap(text, width)
  local out, cur = {}, ''
  for word in text:gmatch('%S+') do
    if cur ~= '' and #cur + #word + 1 > width then
      table.insert(out, cur)
      cur = word
    else
      cur = cur == '' and word or cur .. ' ' .. word
    end
  end
  if cur ~= '' then table.insert(out, cur) end
  return out
end

-- annotations.tsv: pr path line note
local function draw_notes(v)
  vim.api.nvim_buf_clear_namespace(v.buf, ns, 0, -1)
  local width = math.max(40, (valid(pane.win) and vim.api.nvim_win_get_width(pane.win) or 100) - 8)
  for _, r in ipairs(notes.rows) do
    if r[1] and r[1]:gsub('^#', '') == v.pr and r[4] then
      local at = locate(v, norm(r[2]), tonumber(r[3]))
      if at then
        local virt = {}
        for i, l in ipairs(wrap(table.concat(r, ' ', 4), width)) do
          table.insert(virt, { { (i == 1 and '  ◆ ' or '    ') .. l, 'DiagnosticVirtualTextInfo' } })
        end
        vim.api.nvim_buf_set_extmark(v.buf, ns, at - 1, 0, { virt_lines = virt, virt_lines_above = true })
      end
    end
  end
end

-- Reload annotations.tsv when it changes and redraw every view's notes.
local function refresh_notes()
  local s = stamp(review_file('annotations.tsv'))
  if s == notes.stamp then return false end
  notes.stamp, notes.rows = s, read_tsv(review_file('annotations.tsv'))
  for _, v in pairs(views) do
    if v.files and vim.api.nvim_buf_is_valid(v.buf) then draw_notes(v) end
  end
  return true
end

local pane_keys -- set up below, once the navigation exists

-- The view of one layer, rebuilt when its diff file changes.
local function view(l)
  local path = review_file('diffs/' .. l.pr .. '.diff')
  local s = stamp(path)
  local v = views[l.pr]
  if v and v.files and vim.api.nvim_buf_is_valid(v.buf) and v.stamp == s then return v end
  local lines = s and vim.fn.readfile(path) or {}
  if not s then
    local co = checkout(l)
    if co then lines = vim.fn.systemlist({ 'git', '-C', co, 'diff', l.lo, l.hi }) end
  end
  if #lines == 0 then lines = { string.format('(no changes in #%s)', l.pr) } end
  if not (v and vim.api.nvim_buf_is_valid(v.buf)) then
    local buf = vim.api.nvim_create_buf(false, true)
    vim.api.nvim_buf_set_name(buf, 'review://diff/#' .. l.pr)
    vim.bo[buf].bufhidden = 'hide'
    vim.bo[buf].filetype = 'diff'
    pcall(vim.treesitter.start, buf, 'diff')
    v = { buf = buf, pr = l.pr }
    views[l.pr] = v
    pane_keys(buf)
  end
  v.stamp = s
  v.files, v.info = parse(lines)
  vim.bo[v.buf].modifiable = true
  vim.api.nvim_buf_set_lines(v.buf, 0, -1, false, lines)
  vim.bo[v.buf].modifiable = false
  vim.bo[v.buf].modified = false
  if not refresh_notes() then draw_notes(v) end
  return v
end

-- The layer to show path[:line] in: the top layer whose diff has that line,
-- else the top layer that touches the file. Line numbers at the top of the
-- stack match that layer's head, since no layer above it changes the file.
local function layer_for(path, line)
  local ls, touch = layers(), nil
  for i = #ls, 1, -1 do
    local v = view(ls[i])
    for fi, f in ipairs(v.files) do
      if f.path == path then
        touch = touch or ls[i]
        if not line then return touch end
        local stop = (v.files[fi + 1] and v.files[fi + 1].start or #v.info + 1) - 1
        for j = f.start, stop do
          if v.info[j] and v.info[j].new == line then return ls[i] end
        end
      end
    end
  end
  return touch
end

-- Size the slide above the pane to its text, up to 45% of the height.
local function fit()
  if not (valid(pane.win) and pane.stacked) then return end
  local win = main_win()
  if vim.api.nvim_win_get_buf(win) ~= deck.buf then return end
  local total = vim.api.nvim_win_get_height(win) + vim.api.nvim_win_get_height(pane.win)
  local ok, th = pcall(vim.api.nvim_win_text_height, win, {})
  local want = ok and th.all + 1 or 12
  vim.api.nvim_win_set_height(win, math.max(5, math.min(want, math.floor(total * 0.45))))
end

local function current()
  if not valid(pane.win) then return end
  local v = views[pane.pr]
  if not v or vim.api.nvim_win_get_buf(pane.win) ~= v.buf then return end
  return v, vim.api.nvim_win_get_cursor(pane.win)[1]
end

local function pane_bar()
  local v, lnum = current()
  if not v then return end
  local l = layer(v.pr)
  local parts = { string.format(' #%s (%d/%d) %s', v.pr, layer_index(v.pr) or 0, #layers(), l and l.title or '') }
  local fi = file_at(v, lnum)
  local f = fi and v.files[fi]
  if f then
    table.insert(parts, string.format('%s (%d/%d)', f.path, fi, #v.files))
    local h = v.info[lnum] and v.info[lnum].hunk or 0
    if #f.hunks > 0 and h > 0 then table.insert(parts, string.format('hunk %d/%d', h, #f.hunks)) end
    table.insert(parts, string.format('+%d −%d', f.add, f.del))
    mark_seen(v.pr, f.path)
  end
  v.cursor = lnum
  vim.wo[pane.win].winbar = table.concat(parts, ' · '):gsub('%%', '%%%%') .. '%=]p ]f ]c page · g? keys '
end

local function open_pane(buf, focus)
  local back = vim.api.nvim_get_current_win()
  if not valid(pane.win) then
    vim.api.nvim_set_current_win(main_win())
    vim.cmd(pane.stacked and 'botright split' or 'botright vsplit')
    pane.win = vim.api.nvim_get_current_win()
    if not pane.stacked then vim.api.nvim_win_set_width(pane.win, math.floor(vim.o.columns * 0.6)) end
  end
  vim.api.nvim_win_set_buf(pane.win, buf)
  for opt, val in pairs({ wrap = false, number = false, relativenumber = false, signcolumn = 'no',
                          cursorline = true, foldenable = false, list = false }) do
    vim.wo[pane.win][opt] = val
  end
  vim.api.nvim_set_current_win(focus and pane.win or (valid(back) and back or main_win()))
  fit()
end

-- Show layer pr (or the top layer touching path) at path:line. Returns an
-- error message, or 0.
local function show_diff(pr, path, line, focus)
  if #layers() == 0 then return 'no review/layers.tsv in ' .. root end
  path = path and path ~= '' and norm(path) or nil
  local l
  if pr and pr ~= '' then
    l = layer(pr)
    if not l then return '#' .. tostring(pr):gsub('^#', '') .. ' is not in review/layers.tsv' end
  elseif path then
    l = layer_for(path, tonumber(line))
    if not l then return path .. ' is not changed anywhere in the stack' end
  else
    l = layer(pane.pr) or layers()[1]
  end
  local v = view(l)
  local at = path and locate(v, path, tonumber(line))
  if path and not at then return path .. ' is not in the diff of #' .. l.pr end
  if pane.pr and pane.pr ~= l.pr and views[pane.pr] and valid(pane.win) then
    views[pane.pr].cursor = vim.api.nvim_win_get_cursor(pane.win)[1]
  end
  pane.pr = l.pr
  open_pane(v.buf, focus)
  jump(pane.win, at or v.cursor or 1, (at and not line) and 'zt' or 'zz')
  pane_bar()
  return 0
end

-- Pane navigation ----------------------------------------------------------

local function page_pr(d)
  return function()
    if not valid(pane.win) then return show_diff(pane.pr, nil, nil, false) end
    local i = layer_index(pane.pr) or 1
    local n = math.max(1, math.min(#layers(), i + d * vim.v.count1))
    if n ~= i or not valid(pane.win) then show_diff(layers()[n].pr, nil, nil, vim.api.nvim_get_current_win() == pane.win) end
  end
end

local function page_file(d)
  return function()
    if not current() then show_diff(nil, nil, nil, false) end
    local v, lnum = current()
    if not v or #v.files == 0 then return end
    local fi = file_at(v, lnum)
    local t
    if not fi then t = d > 0 and 1 or nil
    elseif d < 0 and lnum > v.files[fi].start then t = fi - (vim.v.count1 - 1)
    else t = fi + d * vim.v.count1 end
    if not t then return end
    t = math.max(1, math.min(#v.files, t))
    jump(pane.win, v.files[t].start, 'zt')
    pane_bar()
  end
end

local function page_hunk(d)
  return function()
    if not current() then show_diff(nil, nil, nil, false) end
    local v, lnum = current()
    if not v then return end
    local all = {}
    for _, f in ipairs(v.files) do vim.list_extend(all, f.hunks) end
    local target = lnum
    for _ = 1, vim.v.count1 do
      local nxt
      if d > 0 then
        for _, h in ipairs(all) do if h > target then nxt = h break end end
      else
        for i = #all, 1, -1 do if all[i] < target then nxt = all[i] break end end
      end
      if not nxt then break end
      target = nxt
    end
    if target ~= lnum then
      jump(pane.win, target, 'zt')
      pane_bar()
    end
  end
end

-- A file at one revision, read-only: the working tree when that revision is
-- the checkout's HEAD (so LSP and friends work), else a scratch buffer.
local function blob(l, rev, path)
  local co = checkout(l)
  if not co then return nil, 'no checkout of ' .. l.url end
  local head = vim.fn.systemlist({ 'git', '-C', co, 'rev-parse', 'HEAD' })[1]
  if head == rev and uv.fs_stat(co .. '/' .. path) then return co .. '/' .. path end
  local key = rev .. ':' .. path
  if blobs[key] and vim.api.nvim_buf_is_valid(blobs[key]) then return blobs[key] end
  local lines = vim.fn.systemlist({ 'git', '-C', co, 'show', key })
  if vim.v.shell_error ~= 0 then return nil, path .. ' does not exist at ' .. rev:sub(1, 7) end
  local buf = vim.api.nvim_create_buf(false, true)
  vim.api.nvim_buf_set_name(buf, 'review://' .. rev:sub(1, 7) .. '/' .. path)
  vim.api.nvim_buf_set_lines(buf, 0, -1, false, lines)
  vim.bo[buf].modifiable = false
  vim.bo[buf].bufhidden = 'hide'
  local ft = vim.filetype.match({ filename = path, buf = buf })
  if ft then vim.bo[buf].filetype = ft end
  blobs[key] = buf
  return buf
end

local function show_blob(target, line)
  local win = code_window()
  if type(target) == 'string' then
    vim.cmd.edit(vim.fn.fnameescape(target))
    vim.bo.autoread = true
  else
    vim.api.nvim_win_set_buf(win, target)
  end
  jump(win, line)
end

-- gf in the pane: the head side of + and context lines, the base side of -.
local function open_side()
  local v, lnum = current()
  if not v then return end
  local inf, fi = v.info[lnum], file_at(v, lnum)
  if not fi then return end
  local l, f = layer(v.pr), v.files[fi]
  local rev, line = l.hi, inf and inf.new or 1
  if inf and inf.old and not inf.new then rev, line = l.lo, inf.old end
  if inf and inf.hunk and not inf.new and not inf.old then line = f.hunk_new[inf.hunk] or 1 end
  local target, err = blob(l, rev, f.path)
  if not target then return vim.notify(err) end
  show_blob(target, line)
end

-- gd: the current file in a tab of its own. For the top layer, whose head is
-- the checkout, that is unified.nvim: the inline diff in the real file, with
-- its tree of the layer's other files. Unified diffs the working tree against
-- one ref, so a lower layer gets diffview, or vim's own two-way diff.
local function open_full()
  local v, lnum = current()
  local fi = v and file_at(v, lnum)
  if not fi then return end
  local l, f, inf = layer(v.pr), v.files[fi], v.info[lnum]
  local co = checkout(l)
  if not co then return vim.notify('no checkout of ' .. l.url) end
  local line = inf and (inf.new or (inf.hunk and f.hunk_new[inf.hunk])) or nil
  local head = vim.fn.systemlist({ 'git', '-C', co, 'rev-parse', 'HEAD' })[1]
  vim.cmd('tabnew')
  vim.cmd.tcd(vim.fn.fnameescape(co))
  local ok, ustate = pcall(require, 'unified.state')
  if ok and vim.fn.exists(':Unified') == 2 and head == l.hi and uv.fs_stat(co .. '/' .. f.path) then
    -- Unified keeps one view at a time; it resolves the ref asynchronously and
    -- then jumps to the first hunk, so wait for it before placing the cursor.
    if ustate.is_active() then vim.cmd('Unified reset') end
    vim.cmd.edit(vim.fn.fnameescape(co .. '/' .. f.path))
    local win = vim.api.nvim_get_current_win()
    vim.cmd('Unified ' .. l.lo)
    vim.wait(2000, function() return ustate.is_active() end, 20)
    if line and valid(win) then jump(win, line) end
    vim.notify(':DiffClose closes this tab')
    return
  end
  if vim.fn.exists(':DiffviewOpen') == 2 then
    vim.cmd('DiffviewOpen ' .. l.lo .. '..' .. l.hi .. ' -- ' .. vim.fn.fnameescape(f.path))
    return
  end
  local left = blob(l, l.lo, f.path) or vim.api.nvim_create_buf(false, true)
  local right = blob(l, l.hi, f.path) or vim.api.nvim_create_buf(false, true)
  local function put(t) if type(t) == 'string' then vim.cmd.edit(vim.fn.fnameescape(t)) else vim.api.nvim_win_set_buf(0, t) end end
  put(left)
  vim.cmd('diffthis | rightbelow vsplit')
  put(right)
  vim.cmd('diffthis')
end

-- gu: changed files the cursor has never entered in the pane.
local function unviewed()
  local items = {}
  for _, l in ipairs(layers()) do
    for _, f in ipairs(view(l).files) do
      if not load_seen()[l.pr .. '\t' .. f.path] then
        table.insert(items, { pr = l.pr, path = f.path, label = string.format('#%s  +%d −%d  %s', l.pr, f.add, f.del, f.path) })
      end
    end
  end
  if #items == 0 then return vim.notify('Every changed file in the stack has been viewed.') end
  vim.ui.select(items, {
    prompt = string.format('Not viewed yet (%d)', #items),
    format_item = function(it) return it.label end,
  }, function(it)
    if it then show_diff(it.pr, it.path, nil, true) end
  end)
end

local function toggle_layout()
  if not valid(pane.win) then return end
  pane.stacked = not pane.stacked
  -- The file split belongs to whichever side the diff was on; drop it rather
  -- than leave it stranded. The next gf opens it again.
  if valid(code_win) then vim.api.nvim_win_close(code_win, false) end
  vim.api.nvim_win_call(pane.win, function() vim.cmd(pane.stacked and 'wincmd J' or 'wincmd L') end)
  if pane.stacked then fit() else vim.api.nvim_win_set_width(pane.win, math.floor(vim.o.columns * 0.6)) end
  if current() then draw_notes(views[pane.pr]) end
end

-- Slides -------------------------------------------------------------------

-- `Diff: #4512 repos/Runlayer/path.py:88`, `Diff: #4512`, or `Diff: path:88`,
-- optionally bold or in backticks.
local function anchor(lines)
  for _, l in ipairs(lines) do
    local d = l:match('^%s*[%*_]*Diff[%*_]*:[%*_]*%s+(.*)$')
    if d then
      d = d:gsub('[`%*]', '')
      local pr, ref = d:match('^#(%d+)%s*(%S*)')
      if not pr then pr, ref = d:match('^(%d+)%s+(%S*)') end
      if not pr then pr, ref = d:match('^(%d+)%s*$'), '' end
      if not pr then ref = d:match('^(%S+)') end
      local path, ln = (ref or ''):match('^(.-):(%d+)$')
      if not path then path = ref ~= '' and ref or nil end
      return { pr = pr, path = path, line = tonumber(ln) }
    end
  end
end

local function render()
  if not deck.path then return end
  local ok, lines = pcall(vim.fn.readfile, deck.path)
  local slides, starts = {}, {}
  if ok then slides, starts = split(lines) end
  if #slides == 0 then slides = { { '_No slides yet in ' .. vim.fn.fnamemodify(deck.path, ':~:.') .. '._' } } end
  deck.starts = starts
  deck.count = #slides
  deck.threads, deck.unsent = {}, 0
  for i, s in ipairs(slides) do
    local cs = comments(s, i, deck.path)
    -- A slide the agent has marked ✅ is settled, and ]n / [n skip it.
    local settled = false
    for _, l in ipairs(s) do
      if l:sub(1, #AGENT) == AGENT and l:find('✅', 1, true) then settled = true end
    end
    deck.threads[i] = settled and 0 or #cs
    for _, c in ipairs(cs) do if c.unsent then deck.unsent = deck.unsent + 1 end end
  end
  if deck.want == 'last' then deck.idx = deck.count
  elseif tonumber(deck.want) then deck.idx = tonumber(deck.want) end
  deck.want = nil
  deck.idx = math.max(1, math.min(deck.idx, deck.count))

  local buf = deck.buf
  vim.bo[buf].modifiable = true
  -- The agent: line is for :Send, not for reading.
  local shown = vim.tbl_filter(function(l) return not l:match('^%s*<!%-%-%s*agent:') end, slides[deck.idx])
  vim.api.nvim_buf_set_lines(buf, 0, -1, false, shown)
  vim.bo[buf].modifiable = false
  vim.bo[buf].modified = false
  for _, win in ipairs(vim.fn.win_findbuf(buf)) do
    local name = vim.fn.fnamemodify(deck.path, ':t'):gsub('%%', '%%%%')
    local pending = deck.unsent > 0 and string.format('  💬 %d unsent, S to send', deck.unsent) or ''
    vim.wo[win].winbar = string.format(' %s  %d/%d%s%%=J/K slide · c comment · ]p ]f ]c diff · g? keys ', name, deck.idx, deck.count, pending)
    vim.api.nvim_win_set_cursor(win, { 1, 0 })
  end

  -- Follow the slide's anchor when arriving on it, not on every rewrite, so
  -- the agent appending slides doesn't yank the pane away from the human.
  local a = anchor(slides[deck.idx])
  local key = a and (deck.path .. '|' .. deck.idx .. '|' .. vim.inspect(a))
  if a and key ~= deck.anchored and #layers() > 0 then
    deck.anchored = key
    local err = show_diff(a.pr, a.path, a.line, false)
    if err ~= 0 then vim.notify('Diff anchor: ' .. err, vim.log.levels.WARN) end
  end
  fit()
end

local function step(d)
  return function()
    deck.idx = deck.idx + d * vim.v.count1
    render()
  end
end

local function go(n)
  return function()
    deck.idx = n == 'last' and deck.count or n
    render()
  end
end

-- Append a comment to the end of the current slide in the deck file.
local function add_comment(text)
  text = text and vim.trim(text)
  if not text or text == '' or not deck.starts[deck.idx] then return end
  local lines = vim.fn.readfile(deck.path)
  local last = deck.starts[deck.idx + 1] and deck.starts[deck.idx + 1] - 1 or #lines
  while last > deck.starts[deck.idx] and (lines[last]:match('^%s*$') or lines[last]:match('^%-%-%-+%s*$')) do
    last = last - 1
  end
  local new = { ME .. ' ' .. text }
  if not lines[last]:match('^> %*%*') then table.insert(new, 1, '') end
  for i, l in ipairs(new) do table.insert(lines, last + i, l) end
  vim.fn.writefile(lines, deck.path)
  deck.stamp = stamp(deck.path)
  render()
end

local function comment()
  vim.ui.input({ prompt = 'comment: ' }, add_comment)
end

local function pick()
  vim.api.nvim_echo({ { 'pick: a / b / c …  (esc cancels)' } }, false, {})
  local ok, ch = pcall(vim.fn.getcharstr)
  vim.api.nvim_echo({ { '' } }, false, {})
  if ok and ch:match('^%w$') then add_comment('pick ' .. ch) end
end

-- Next or previous unsettled slide that has comments on it.
local function thread(d)
  return function()
    for n = 1, deck.count - 1 do
      local i = (deck.idx - 1 + d * n) % deck.count + 1
      if (deck.threads[i] or 0) > 0 then deck.idx = i; return render() end
    end
    vim.notify('no other unsettled slides with comments')
  end
end

local function send()
  local lines = vim.fn.readfile(deck.path)
  local block, session = agent_target(lines)
  if not block then
    return vim.notify('this deck has no <!-- agent: BLOCK SESSION --> line, so there is nowhere to send', vim.log.levels.WARN)
  end
  local slides = split(lines)
  local fresh, parts = {}, {}
  for i, s in ipairs(slides) do
    for _, c in ipairs(comments(s, i, deck.path)) do
      if c.unsent then
        table.insert(fresh, c.key)
        table.insert(parts, string.format('%s: "%s"', c.heading, c.text))
      end
    end
  end
  if #fresh == 0 then return vim.notify('nothing new to send') end
  local msg = string.format('New comments in %s, reply under each one in the deck: %s',
    deck.path, table.concat(parts, ' · '))
  local target = { '-b', block }
  if session then target = { '-s', session, '-b', block } end
  local function rex(args, cb)
    vim.system(vim.list_extend({ 'rex' }, vim.list_extend(vim.deepcopy(args), target)), { text = true }, cb)
  end
  rex({ 'send', msg }, function(r)
    if r.code ~= 0 then
      return vim.schedule(function() vim.notify('send failed: ' .. (r.stderr or ''), vim.log.levels.ERROR) end)
    end
    rex({ 'send-key', 'enter' }, function()
      vim.schedule(function()
        sent[deck.path] = sent[deck.path] or {}
        for _, k in ipairs(fresh) do sent[deck.path][k] = true end
        render()
        vim.notify(string.format('sent %d comment%s', #fresh, #fresh == 1 and '' or 's'))
      end)
    end)
  end)
end

local keys_help = {
  'Slides',
  '  j k gg G ctrl-d ctrl-u   move within the slide',
  '  J ]]  /  K [[            next / previous slide (with a count: 3J)',
  '  H  /  L                  first / last slide',
  '  gf  or  <CR>             the path:line under the cursor: in the diff if it changed, else the file',
  '  gF                       the path:line under the cursor as a plain file',
  '  :Slide N  :SlideEdit     jump to slide N; edit the deck at this slide',
  'Comments',
  '  c                        comment on this slide',
  '  a then a letter          pick an option (a b c …)',
  '  ]n  /  [n                next / previous slide still in discussion',
  '  S  or  :send             send new comments to the agent (again whenever you add more)',
  'Diff pane (these work from the slides too)',
  '  ]p [p  ]f [f  ]c [c      next / previous PR, file, hunk (with a count)',
  '  gf  or  <CR>             open the file at this line (head side; base side on a - line)',
  '  gd                       this file in a tab: unified.nvim for the top PR, else diffview;',
  '                           :DiffClose closes the tab',
  '  gu                       changed files not viewed yet',
  '  gs                       diff under the slides / beside them',
  '  J  K                     next / previous slide',
  'q quits the panel (from the slides or the pane)',
}

-- First path[:line] on the current line, preferring the one under the cursor.
local function ref_under_cursor()
  local line, col = vim.api.nvim_get_current_line(), vim.api.nvim_win_get_cursor(0)[2] + 1
  local best
  for s, path, lnum, e in line:gmatch('()([%w%._%-/~]+%.[%w]+):?(%d*)()') do
    local hit = { path = path, line = tonumber(lnum) }
    if col >= s and col < e then return hit end
    best = best or hit
  end
  return best
end

-- gf on a slide or doc: a Diff: anchor, or a path the stack changes, opens
-- in the pane; anything else opens as a file.
local function follow()
  local a = anchor({ vim.api.nvim_get_current_line() })
  if a then
    local err = show_diff(a.pr, a.path, a.line, true)
    return err ~= 0 and vim.notify(err) or nil
  end
  local ref = ref_under_cursor()
  if not ref then return vim.notify('no path under the cursor') end
  if #layers() > 0 and layer_for(norm(ref.path)) and show_diff(nil, ref.path, ref.line, true) == 0 then return end
  M.code(ref.path, ref.line)
end

local function plain()
  local ref = ref_under_cursor()
  if ref then M.code(ref.path, ref.line) else vim.notify('no path under the cursor') end
end

local function map(buf, keys, fn)
  for _, k in ipairs(keys) do vim.keymap.set('n', k, fn, { buffer = buf, nowait = true, silent = true }) end
end

local function diff_keys(buf)
  map(buf, { ']p' }, page_pr(1))
  map(buf, { '[p' }, page_pr(-1))
  map(buf, { ']f' }, page_file(1))
  map(buf, { '[f' }, page_file(-1))
  map(buf, { ']c' }, page_hunk(1))
  map(buf, { '[c' }, page_hunk(-1))
  map(buf, { 'gu' }, unviewed)
  map(buf, { 'gs' }, toggle_layout)
  map(buf, { 'g?' }, function() vim.notify(table.concat(keys_help, '\n')) end)
end

pane_keys = function(buf)
  diff_keys(buf)
  map(buf, { 'q' }, function() vim.cmd('qall') end)
  map(buf, { 'gf', '<CR>' }, open_side)
  map(buf, { 'gd' }, open_full)
  map(buf, { 'J' }, function() if deck.buf then step(1)() end end)
  map(buf, { 'K' }, function() if deck.buf then step(-1)() end end)
  vim.api.nvim_create_autocmd('CursorMoved', {
    buffer = buf,
    callback = function() if vim.api.nvim_get_current_win() == pane.win then pane_bar() end end,
  })
end

local function deck_buf()
  if deck.buf and vim.api.nvim_buf_is_valid(deck.buf) then return deck.buf end
  local buf = vim.api.nvim_create_buf(false, true)
  vim.api.nvim_buf_set_name(buf, 'review://slides')
  vim.bo[buf].filetype = 'markdown'
  vim.bo[buf].bufhidden = 'hide'
  -- Highlight fenced code through markdown's language injections. Needs the
  -- languages' parsers; without them the blocks just stay plain.
  pcall(vim.treesitter.start, buf, 'markdown')
  diff_keys(buf)
  map(buf, { 'q' }, function() vim.cmd('qall') end)
  map(buf, { 'J', ']]' }, step(1))
  map(buf, { 'K', '[[' }, step(-1))
  map(buf, { 'H' }, go(1))
  map(buf, { 'L' }, go('last'))
  map(buf, { 'gf', '<CR>' }, follow)
  map(buf, { 'gF' }, plain)
  map(buf, { 'c' }, comment)
  map(buf, { 'a' }, pick)
  map(buf, { 'S' }, send)
  map(buf, { ']n' }, thread(1))
  map(buf, { '[n' }, thread(-1))
  vim.api.nvim_buf_create_user_command(buf, 'Send', send, {})
  vim.api.nvim_buf_call(buf, function()
    vim.cmd([[cnoreabbrev <buffer> <expr> send (getcmdtype() ==# ':' && getcmdline() ==# 'send') ? 'Send' : 'send']])
  end)
  vim.api.nvim_buf_create_user_command(buf, 'Slide', function(o) go(tonumber(o.args) or 1)() end, { nargs = 1 })
  vim.api.nvim_buf_create_user_command(buf, 'SlideEdit', function()
    vim.cmd.edit(vim.fn.fnameescape(deck.path))
    pcall(vim.api.nvim_win_set_cursor, 0, { deck.starts[deck.idx] or 1, 0 })
  end, {})
  deck.buf = buf
  return buf
end

function M.slides(path, n)
  deck.path = resolve(path)
  deck.want = n or 'last'
  deck.stamp = stamp(deck.path)
  deck.anchored = nil
  local win = main_win()
  vim.api.nvim_set_current_win(win)
  vim.api.nvim_win_set_buf(win, deck_buf())
  vim.wo[win].wrap = true
  vim.wo[win].linebreak = true
  render()
  return 0
end

function M.doc(path, line)
  local win = main_win()
  vim.api.nvim_set_current_win(win)
  vim.cmd.edit(vim.fn.fnameescape(resolve(path)))
  vim.bo.autoread = true
  vim.wo[win].wrap = true
  vim.wo[win].linebreak = true
  -- Findings in a package or checkpoint cite path:LINE; open those in the diff.
  if #layers() > 0 then
    map(0, { 'gf', '<CR>' }, follow)
    map(0, { 'gF' }, plain)
    diff_keys(0)
  end
  if tonumber(line) then pcall(vim.api.nvim_win_set_cursor, win, { tonumber(line), 0 }) end
  return 0
end

-- Open a file in a split beside the slides, reusing it on the next call.
function M.code(path, line)
  code_window()
  vim.cmd.edit(vim.fn.fnameescape(resolve(path)))
  vim.bo.autoread = true
  jump(code_win, line)
  return 0
end

-- A layer's diff in the pane, at path:line when given.
function M.diff(pr, path, line)
  return show_diff(pr, path, line, true)
end

-- Close a gd tab, whichever viewer is in it. (:Unified takes no |, so
-- `Unified reset | tabclose` would hand the tabclose to it as a ref.)
vim.api.nvim_create_user_command('DiffClose', function()
  local ok, ustate = pcall(require, 'unified.state')
  if ok and ustate.is_active() then vim.cmd('Unified reset') end
  if vim.fn.exists(':DiffviewClose') == 2 and vim.t.diffview_view_initialized then vim.cmd('DiffviewClose') end
  if vim.fn.tabpagenr('$') > 1 then vim.cmd('tabclose') end
end, {})

vim.api.nvim_create_autocmd('VimResized', { callback = function() vim.schedule(fit) end })

-- The agent rewrites files underneath us; poll rather than fs_event, which
-- loses track of files replaced by rename.
local timer = uv.new_timer()
timer:start(700, 700, vim.schedule_wrap(function()
  local s = stamp(deck.path)
  if s and s ~= deck.stamp then
    deck.stamp = s
    if deck.buf and vim.api.nvim_buf_is_valid(deck.buf) then render() end
  end
  if valid(pane.win) and layer(pane.pr) then
    view(layer(pane.pr))
    refresh_notes()
  end
  vim.cmd('silent! checktime')
end))

return M
