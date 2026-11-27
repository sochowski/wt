-- The review panel: markdown slides and docs that reload while an agent
-- writes them, with the code they're about drawn inside them. review-show
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
-- A line in a slide like
--   Diff: #4512 repos/Runlayer/proxy/verdict.py:80-120
-- shows those lines of the PR's change in place (see excerpt.lua), from
-- review/layers.tsv and review/diffs/<n>.diff, with the agent's notes from
-- review/annotations.tsv (pr, path, line, note) above their lines. gf on a
-- line of it opens the file there, gd opens the file's change in unified.nvim
-- (diffview for a PR below the checkout's head), both in a pane of their own
-- below the slides. Files shown or opened are appended to review/viewed.tsv,
-- and gu lists the changed files not viewed yet.

local M = {}
-- The optional dependency lets delivery tests control discovery independently.
local here = vim.fn.fnamemodify(debug.getinfo(1, 'S').source:sub(2), ':p:h')
local recipient = ... or dofile(here .. '/recipient.lua')
local excerpt = dofile(here .. '/excerpt.lua')
-- Panes are reached through wt's multiplexer adapter, so Rex and tmux work alike.
local function wt_pane() return vim.g.wt_pane or vim.fn.fnamemodify(here .. '/../../../bin/wt-pane', ':p') end
local panel_attention = dofile(vim.fn.fnamemodify(here .. '/../../../nvim/attention.lua', ':p'))
local uv = vim.uv or vim.loop
local root = vim.fn.getcwd()
local main = vim.api.nvim_get_current_win()
-- at: slide buffer row -> { l, path, line } for the excerpts on the slide.
local deck = { path = nil, idx = 1, want = nil, count = 0, starts = {}, buf = nil, stamp = nil, at = {} }
local ns = vim.api.nvim_create_namespace('wt_review')
local stack = { stamp = nil, list = {} }
local diffs = {} -- pr -> { stamp, lines }
local warned = {} -- Diff: problems already reported, by deck, slide and message
local notes = { stamp = nil, rows = {} }
local seen = nil -- 'pr\tpath' -> true
local blobs = {} -- 'rev:path' -> lines, or false when unreadable

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
-- comment not yet sent into the presenting agent's terminal (falling back to
-- `<!-- agent: BLOCK [SESSION] -->` in the deck), and ]n / [n jump between slides
-- still in discussion. The agent answers under each one
-- with `> **agent:**` (or their name); a comment with a reply below it counts as sent.
local ME = '> **' .. (vim.g.wt_review_name or vim.env.USER or 'me') .. ':**'
local agent_names = { agent = true, claude = true, chatgpt = true, codex = true }
local function agent_reply(line)
  local name = line:match('^> %*%*([^:]+):%*%*')
  return name and agent_names[name:lower()] and line:sub(1, #ME) ~= ME
end
local sent = {} -- deck path -> { [heading .. '\0' .. text] = true }
local owners = {} -- deck path -> pane ref (rex:SESSION/BLOCK or tmux:%PANE@SOCKET), set by review-show
-- One batch at a time. Keep a pasted batch on Enter failure so :send can
-- retry submission without typing it twice. Callbacks always use this snapshot.
local delivery = nil
-- An explicit ask from the agent (review-show status waiting), kept until
-- answered or withdrawn.
local attention = nil
local finding = false
local recipient_error = nil
local update_activity
-- Presenting something asks for a reply: the pane stays blocked until the
-- user sends comments, says go on (C), or answers the agent in its own pane,
-- or the agent presents something else. Looking alone doesn't clear it
-- (nvim/attention.lua, sticky).
local look = panel_attention.new({ id = 'wt-review', app = 'wt', title = 'review', sticky = true,
  on_change = function() update_activity() end })

local function feedback_status()
  if finding then return 'working', 'Finding an agent in this tab' end
  if recipient_error then return 'blocked', recipient_error end
  if delivery then
    local name = vim.fn.fnamemodify(delivery.path, ':t')
    if delivery.busy then return 'working', 'Sending comments: ' .. name end
    return 'blocked', 'Send failed: ' .. name .. ' — :send to retry'
  end
  if (deck.unsent or 0) > 0 then
    return 'blocked', string.format('%d comments unsent — :send', deck.unsent)
  end
  if attention then return 'blocked', attention.msg end
  if look.pending() then return 'blocked', look.pending() end
  return 'clear'
end

function update_activity()
  look.report(feedback_status())
end

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
    elseif agent_reply(l) then
      for _, c in ipairs(out) do c.replied = true end
    end
  end
  local done = sent[path] or {}
  for _, c in ipairs(out) do c.unsent = not (c.replied or done[c.key]) end
  return out
end

local function is_ref(s)
  return type(s) == 'string' and (s:match('^rex:[^/]+/.+$') or s:match('^tmux:%%[^@]+@.+$')) ~= nil
end

-- A pane ref from review-show's arguments: a ref, or the older Rex BLOCK SESSION pair.
local function as_ref(block, session)
  if not block or block == '' then return nil end
  if is_ref(block) then return block end
  session = session ~= '' and session or vim.env.REX_SESSION
  if session and session ~= '' then return 'rex:' .. session .. '/' .. block end
end

-- The deck's saved recipient: <!-- agent: REF -->, or <!-- agent: BLOCK SESSION --> from before refs.
local function agent_target(lines)
  for _, l in ipairs(lines) do
    local a, b = l:match('<!%-%-%s*agent:%s*(%S+)%s*(%S*)%s*%-%->')
    if a then return as_ref(a, b) end
  end
end

local function main_win()
  if not valid(main) then main = vim.api.nvim_get_current_win() end
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

-- Stack and diffs ----------------------------------------------------------

local function review_file(name) return root .. '/review/' .. name end

local function read_tsv(path)
  local rows = {}
  local ok, lines = pcall(vim.fn.readfile, path)
  if not ok then return rows end
  for _, l in ipairs(lines) do
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

local function head_of(co) return vim.fn.systemlist({ 'git', '-C', co, 'rev-parse', 'HEAD' })[1] end

-- Paths in slides and packages are repos/<repo>/<path> or repo-relative.
local function norm(path)
  path = path:gsub('^' .. vim.pesc(root) .. '/', '')
  return (path:gsub('^repos/[^/]+/', ''))
end

-- A layer's diff: review/diffs/<pr>.diff, else from its checkout.
local function diff_lines(l)
  local path = review_file('diffs/' .. l.pr .. '.diff')
  local s = stamp(path)
  local c = diffs[l.pr]
  if c and c.stamp == s and (s or c.lines) then return c.lines end
  local lines = s and vim.fn.readfile(path) or nil
  if not lines then
    local co = checkout(l)
    lines = co and vim.fn.systemlist({ 'git', '-C', co, 'diff', l.lo, l.hi }) or {}
  end
  diffs[l.pr] = { stamp = s, lines = lines }
  return lines
end

-- The layer to show path[:line] in: the top layer whose diff has that line,
-- else the top layer that touches the file. Line numbers at the top of the
-- stack match that layer's head, since no layer above it changes the file.
local function layer_for(path, line)
  local ls, touch = layers(), nil
  for i = #ls, 1, -1 do
    local hunks = excerpt.file(diff_lines(ls[i]), path)
    if hunks then
      touch = touch or ls[i]
      if not line then return touch end
      for _, h in ipairs(hunks) do
        for _, r in ipairs(h) do
          if r.new == line then return ls[i] end
        end
      end
    end
  end
  return touch
end

-- A file at one revision, as lines, or nil when there's no checkout to read
-- it from.
local function file_at(l, rev, path)
  local key = rev .. ':' .. path
  if blobs[key] ~= nil then return blobs[key] or nil end
  local co = checkout(l)
  local lines = false
  if co then
    local out = vim.fn.systemlist({ 'git', '-C', co, 'show', key })
    if vim.v.shell_error == 0 then lines = out end
  end
  blobs[key] = lines
  return lines or nil
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
local function notes_for(pr, path)
  local s = stamp(review_file('annotations.tsv'))
  if s ~= notes.stamp then notes.stamp, notes.rows = s, read_tsv(review_file('annotations.tsv')) end
  local out = {}
  for _, r in ipairs(notes.rows) do
    if r[1] and r[1]:gsub('^#', '') == pr and r[2] and norm(r[2]) == path and tonumber(r[3]) and r[4] then
      table.insert(out, { tonumber(r[3]), table.concat(r, ' ', 4) })
    end
  end
  return out
end

-- The file pane ----------------------------------------------------------
--
-- Files and full diffs open in a second nvim, in a pane of its own below the
-- slides (wt pane split: Rex or tmux), never in a split of this one. The
-- slides drive it over its socket. Closed, it opens again on the next use.

local side = { sock = nil, ch = nil, ref = nil, starting = false, queue = {} }

-- Runs in the file pane's nvim with one argument: what to show.
local SHOW = [[
local a = ...
local ok, err = pcall(function()
  local okd, ustate = pcall(require, 'unified.state')
  if okd and ustate.is_active() then vim.cmd('Unified reset') end
  if vim.t.diffview_view_initialized and vim.fn.exists(':DiffviewClose') == 2 then vim.cmd('DiffviewClose') end
  if a.cwd then vim.cmd.tcd(vim.fn.fnameescape(a.cwd)) end
  if a.range and vim.fn.exists(':DiffviewOpen') == 2 then
    vim.cmd('DiffviewOpen ' .. a.range .. ' -- ' .. vim.fn.fnameescape(a.rel))
    return
  end
  vim.cmd.edit(vim.fn.fnameescape(a.file))
  if a.readonly then vim.bo.readonly, vim.bo.modifiable = true, false end
  local win = vim.api.nvim_get_current_win()
  if a.base and okd and vim.fn.exists(':Unified') == 2 then
    -- Unified resolves the ref asynchronously, then jumps to the first hunk.
    vim.cmd('Unified ' .. a.base)
    vim.wait(2000, function() return ustate.is_active() end, 20)
  end
  if a.line and vim.api.nvim_win_is_valid(win) then
    pcall(vim.api.nvim_win_set_cursor, win, { a.line, 0 })
    vim.cmd('normal! zz')
  end
end)
if not ok then vim.notify('review: ' .. tostring(err), vim.log.levels.ERROR) end
]]

local function side_sock()
  local s = root .. '/.wt/review-files.sock'
  if #s >= 100 then s = (vim.env.TMPDIR or '/tmp') .. '/wt-review-files-' .. vim.fn.sha256(root):sub(1, 12) .. '.sock' end
  return s
end

local function side_connect()
  if side.ch then
    -- A closed channel has no info left. (No request: a file pane stuck at a
    -- prompt would never answer it.)
    if next(vim.api.nvim_get_chan_info(side.ch)) then return true end
    side.ch = nil
  end
  if not uv.fs_stat(side.sock) then return false end
  local ok, ch = pcall(vim.fn.sockconnect, 'pipe', side.sock, { rpc = true })
  if ok and ch > 0 then side.ch = ch return true end
  return false
end

local function side_flush()
  local q = side.queue
  side.queue = {}
  for _, item in ipairs(q) do
    vim.rpcnotify(side.ch, 'nvim_exec_lua', SHOW, { item.args })
    if item.focus and side.ref then vim.system({ wt_pane(), 'focus', side.ref }) end
  end
end

-- Show something in the file pane, opening the pane first if it isn't up.
local function side_show(args, focus)
  side.sock = side.sock or side_sock()
  table.insert(side.queue, { args = args, focus = focus })
  if side_connect() then return side_flush() end
  if side.starting then return end
  side.starting = true
  pcall(vim.fn.delete, side.sock)
  local argv = { wt_pane(), 'split', '--dir', 'below', '--size', '60', '--label', 'review files', '--cwd', root,
    '--', 'nvim', '-i', 'NONE', '--listen', side.sock }
  local ok, err = pcall(vim.system, argv, { text = true, timeout = 15000 }, function(r)
    vim.schedule(function()
      if r.code ~= 0 then
        side.starting, side.queue = false, {}
        return vim.notify('No pane for files: ' .. vim.trim(r.stderr or ''), vim.log.levels.WARN)
      end
      side.ref = vim.trim(r.stdout or '')
      local tries = 0
      local t = uv.new_timer()
      t:start(100, 100, vim.schedule_wrap(function()
        tries = tries + 1
        if side_connect() then
          t:stop(); t:close(); side.starting = false
          side_flush()
        elseif tries > 50 then
          t:stop(); t:close(); side.starting, side.queue = false, {}
          vim.notify('The file pane did not come up on ' .. side.sock, vim.log.levels.WARN)
        end
      end))
    end)
  end)
  if not ok then side.starting, side.queue = false, {}; vim.notify(tostring(err), vim.log.levels.WARN) end
end

-- The file pane goes when the panel does (unless it has unsaved edits).
vim.api.nvim_create_autocmd('VimLeavePre', { callback = function()
  if side.ch and next(vim.api.nvim_get_chan_info(side.ch)) then pcall(vim.rpcnotify, side.ch, 'nvim_command', 'qa') end
end })

-- A layer's file at its head, for gf: the working file when the checkout is
-- at that head (so LSP and friends work), else a read-only copy.
local function open_file(l, path, line, focus)
  local co = checkout(l)
  if co and head_of(co) == l.hi and uv.fs_stat(co .. '/' .. path) then
    side_show({ cwd = co, file = co .. '/' .. path, line = line }, focus)
  else
    local lines = file_at(l, l.hi, path)
    if not lines then return vim.notify(path .. ' at ' .. l.hi:sub(1, 7) .. ': no checkout to read it from') end
    local copy = review_file('.files/' .. l.hi:sub(1, 12) .. '/' .. path)
    vim.fn.mkdir(vim.fn.fnamemodify(copy, ':h'), 'p')
    vim.fn.writefile(lines, copy)
    side_show({ cwd = co, file = copy, line = line, readonly = true }, focus)
  end
  mark_seen(l.pr, path)
end

-- gd: the file's change in unified.nvim, the inline diff in the real file.
-- Unified diffs the working tree against one ref, so that needs the checkout
-- at the layer's head; any other layer gets diffview.
local function open_diff(l, path, line, focus)
  local co = checkout(l)
  if not co then return open_file(l, path, line, focus) end
  if head_of(co) == l.hi and uv.fs_stat(co .. '/' .. path) then
    side_show({ cwd = co, file = co .. '/' .. path, line = line, base = l.lo }, focus)
  else
    side_show({ cwd = co, rel = path, range = l.lo .. '..' .. l.hi, file = co .. '/' .. path, line = line }, focus)
  end
  mark_seen(l.pr, path)
end

-- gu: changed files not shown in an excerpt or opened yet.
local function unviewed()
  local items = {}
  for _, l in ipairs(layers()) do
    for _, line in ipairs(diff_lines(l)) do
      local f = line:match('^diff %-%-git a/.- b/(.*)$')
      if f and not load_seen()[l.pr .. '\t' .. f] then
        table.insert(items, { l = l, path = f, label = string.format('#%s  %s', l.pr, f) })
      end
    end
  end
  if #items == 0 then return vim.notify('Every changed file in the stack has been viewed.') end
  vim.ui.select(items, {
    prompt = string.format('Not viewed yet (%d)', #items),
    format_item = function(it) return it.label end,
  }, function(it)
    if it then open_diff(it.l, it.path, nil, true) end
  end)
end

-- Slides -------------------------------------------------------------------

-- A Diff: line, expanded: the excerpt's lines and marks, or a warning line.
local function expand(d)
  local path = d.path and norm(d.path)
  if not path then return nil, 'a Diff: line needs a path' end
  local l = d.pr and layer(d.pr) or layer_for(path, d.from)
  if not l then
    return nil, d.pr and ('#' .. d.pr .. ' is not in review/layers.tsv') or (path .. ' is not changed anywhere in the stack')
  end
  local lang = vim.filetype.match({ filename = path }) or ''
  local x, err = excerpt.build({ diff = diff_lines(l), path = path, from = d.from, to = d.to,
    head = file_at(l, l.hi, path), notes = notes_for(l.pr, path), lang = lang })
  if not x then return nil, err .. ' (#' .. l.pr .. ')' end
  x.l, x.path = l, path
  local range = x.from == x.to and tostring(x.from) or (x.from .. '-' .. x.to)
  x.caption = string.format('*#%s · %s:%s*', l.pr, path, range)
  return x
end

local function draw(buf, marks, width)
  for _, m in ipairs(marks) do
    if m.number then
      vim.api.nvim_buf_set_extmark(buf, ns, m.row, 0, { virt_text = { { string.format('%4d ', m.number), 'LineNr' } },
        virt_text_pos = 'inline', priority = 200 })
    elseif m.add then
      vim.api.nvim_buf_set_extmark(buf, ns, m.row, 0, { line_hl_group = 'DiffAdd', priority = 200 })
    elseif m.removed then
      local virt = {}
      for _, t in ipairs(m.removed) do
        table.insert(virt, { { '   - ', 'DiffDelete' }, { t, 'DiffDelete' } })
      end
      vim.api.nvim_buf_set_extmark(buf, ns, m.row, 0, { virt_lines = virt, virt_lines_above = not m.below })
    elseif m.note then
      local virt = {}
      for i, t in ipairs(wrap(m.note, math.max(30, width - 10))) do
        table.insert(virt, { { (i == 1 and '     ◆ ' or '       ') .. t, 'DiagnosticVirtualTextInfo' } })
      end
      vim.api.nvim_buf_set_extmark(buf, ns, m.row, 0, { virt_lines = virt, virt_lines_above = true })
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
      if agent_reply(l) and l:find('✅', 1, true) then settled = true end
    end
    deck.threads[i] = settled and 0 or #cs
    for _, c in ipairs(cs) do if c.unsent then deck.unsent = deck.unsent + 1 end end
  end
  if deck.want == 'last' then deck.idx = deck.count
  elseif tonumber(deck.want) then deck.idx = tonumber(deck.want) end
  deck.want = nil
  deck.idx = math.max(1, math.min(deck.idx, deck.count))
  update_activity()

  local buf = deck.buf
  -- The agent: line is for :Send, not for reading. Diff: lines become the
  -- code they name.
  local shown, marks, at = {}, {}, {}
  local width = 80
  for _, win in ipairs(vim.fn.win_findbuf(buf)) do width = vim.api.nvim_win_get_width(win) end
  for _, l in ipairs(slides[deck.idx]) do
    local d = not l:match('^%s*<!%-%-%s*agent:') and excerpt.directive(l)
    if d and #layers() > 0 then
      local x, err = expand(d)
      if x then
        table.insert(shown, x.caption)
        local base = #shown
        vim.list_extend(shown, x.lines)
        for _, m in ipairs(x.marks) do table.insert(marks, vim.tbl_extend('force', m, { row = m.row + base })) end
        for row, n in pairs(x.at) do at[row + base] = { l = x.l, path = x.path, line = n } end
        mark_seen(x.l.pr, x.path)
      else
        table.insert(shown, '> ⚠ ' .. l:gsub('^%s+', '') .. ': ' .. err)
        local key = deck.path .. '|' .. deck.idx .. '|' .. err
        if not warned[key] then
          warned[key] = true
          vim.notify('Diff: ' .. err, vim.log.levels.WARN)
        end
      end
    elseif not l:match('^%s*<!%-%-%s*agent:') then
      table.insert(shown, l)
    end
  end
  vim.bo[buf].modifiable = true
  vim.api.nvim_buf_set_lines(buf, 0, -1, false, shown)
  vim.bo[buf].modifiable = false
  vim.bo[buf].modified = false
  vim.api.nvim_buf_clear_namespace(buf, ns, 0, -1)
  draw(buf, marks, width)
  deck.at = at
  for _, win in ipairs(vim.fn.win_findbuf(buf)) do
    local name = vim.fn.fnamemodify(deck.path, ':t'):gsub('%%', '%%%%')
    local pending = deck.unsent > 0 and string.format('  💬 %d unsent, S to send', deck.unsent) or ''
    if finding then
      pending = '  finding an agent in this tab…'
    elseif recipient_error then
      pending = '  reconnect needed, :Reconnect'
    elseif delivery then
      pending = delivery.busy and '  sending comments…' or '  send failed, :send to retry'
    elseif attention then
      pending = pending .. '  comments needed'
    elseif look.pending() and deck.unsent == 0 then
      pending = '  your turn · C go on'
    end
    vim.wo[win].winbar = string.format(' %s  %d/%d%s%%=g? keys ', name, deck.idx, deck.count, pending)
    vim.api.nvim_win_set_cursor(win, { 1, 0 })
  end
  -- Lines set from a remote call fire none of the events render-markdown
  -- redraws on, so ask it directly (or nudge it, for other setups).
  local okr, rm = pcall(require, 'render-markdown.api')
  if okr and rm.render then pcall(rm.render, { buf = buf })
  else pcall(vim.api.nvim_exec_autocmds, 'TextChanged', { buffer = buf }) end
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

local function deliver(force)
  if delivery and delivery.busy then return vim.notify('comments are already being sent') end
  if delivery and delivery.path ~= deck.path then
    return vim.notify('retry :send in ' .. delivery.path .. ' first; its previous send is unfinished', vim.log.levels.WARN)
  end
  -- Reuse a pasted batch and its target, even if the deck has been edited.
  if not (delivery and delivery.pasted) then
    local lines = vim.fn.readfile(deck.path)
    local ref = owners[deck.path] or agent_target(lines)
    if not ref then
      return vim.notify('this deck has no <!-- agent: PANE --> line, so there is nowhere to send', vim.log.levels.WARN)
    end
    local slides = split(lines)
    local fresh, parts = {}, {}
    for i, s in ipairs(slides) do
      for _, c in ipairs(comments(s, i, deck.path)) do
        if c.unsent or (force and not c.replied) then
          table.insert(fresh, c.key)
          table.insert(parts, string.format('%s: "%s"', c.heading, c.text))
        end
      end
    end
    if #fresh == 0 then render(); return vim.notify('nothing new to send') end
    local msg = string.format('New comments in %s, reply under each one in the deck: %s',
      deck.path, table.concat(parts, ' · '))
    delivery = { path = deck.path, keys = fresh, target = ref, msg = msg, pasted = false, attention = attention,
      which = look.which() }
  end
  local batch = delivery
  batch.busy = true
  render()
  local function failed(step, r)
    batch.busy = false
    render()
    vim.notify(step .. ' failed: ' .. (r.stderr or '') .. '\n'
      .. (batch.pasted and 'Text is already in the agent pane; :send retries Enter only.'
        or 'Check the agent pane before :send retries; a failed request may have partially arrived.')
      .. '\nIf the agent is gone, :send checks for another in this tab; :Reconnect lets you choose.', vim.log.levels.ERROR)
  end
  local function pane(verb, arg, cb)
    local argv = { wt_pane(), verb, batch.target, arg }
    local ok, err = pcall(vim.system, argv, { text = true, timeout = 15000 }, function(r)
      vim.schedule(function() cb(r) end)
    end)
    if not ok then failed('wt pane', { stderr = tostring(err) }) end
  end
  local function submit()
    pane('key', 'enter', function(r)
      if r.code ~= 0 then return failed('Enter', r) end
      sent[batch.path] = sent[batch.path] or {}
      for _, k in ipairs(batch.keys) do sent[batch.path][k] = true end
      delivery = nil
      -- A request for another deck may have appeared while this one was sending.
      if attention == batch.attention then attention = nil end
      -- Comments are a reply: the ask they answer is over. A newer one stays.
      if batch.which then look.dismiss(batch.which) end
      render()
      vim.notify(string.format('submitted %d comment%s to the agent pane', #batch.keys, #batch.keys == 1 and '' or 's'))
    end)
  end
  if batch.pasted then return submit() end
  pane('send', batch.msg, function(r)
    if r.code ~= 0 then return failed('Send', r) end
    batch.pasted = true
    submit()
  end)
end

local function connect(then_send, choose)
  if finding or (delivery and delivery.busy) then return vim.notify('a send or reconnect is already in progress') end
  if delivery and delivery.path ~= deck.path then
    return vim.notify('finish reconnecting ' .. delivery.path .. ' first', vim.log.levels.WARN)
  end
  local path, owner = deck.path, owners[deck.path]
  local saved = owner or agent_target(vim.fn.readfile(path))
  local preferred = not choose and saved or nil
  finding, recipient_error = true, nil
  render()
  recipient.resolve(preferred, function(err, candidate)
    if deck.path ~= path or owners[path] ~= owner then
      finding = false
      render()
      return vim.notify('deck or recipient changed; run :send or :Reconnect again', vim.log.levels.WARN)
    end
    if not err then
      local result = M.bind(path, candidate.ref)
      if result ~= 0 then err = result end
    end
    finding, recipient_error = false, err
    if not err and then_send then return then_send() end
    render()
    if err then return vim.notify(err, vim.log.levels.WARN) end
    vim.notify('comments connected to ' .. candidate.agent .. ' in this tab')
  end)
end

local function send(force)
  connect(function() deliver(force) end)
end

-- C: tell the agent to go on, from the panel. A reply like comments are.
local function go_on()
  if (deck.unsent or 0) > 0 then
    return vim.notify(string.format('%d comment%s unsent: S sends them', deck.unsent, deck.unsent == 1 and ' is' or 's are'))
  end
  if delivery and delivery.busy then return vim.notify('comments are being sent') end
  local which, path, idx, count = look.which(), deck.path, deck.idx, deck.count
  connect(function()
    render()
    local ref = owners[path]
    local msg = string.format('Go on (from the review panel: %s, slide %d/%d).', vim.fn.fnamemodify(path, ':t'), idx, count)
    local function call(argv, cb)
      local ok, err = pcall(vim.system, argv, { text = true, timeout = 15000 }, function(r)
        vim.schedule(function() cb(r) end)
      end)
      if not ok then vim.notify(tostring(err), vim.log.levels.ERROR) end
    end
    call({ wt_pane(), 'send', ref, msg }, function(r)
      if r.code ~= 0 then return vim.notify('Go on failed: ' .. (r.stderr or ''), vim.log.levels.ERROR) end
      call({ wt_pane(), 'key', ref, 'enter' }, function(r2)
        if r2.code ~= 0 then return vim.notify('Go on failed at Enter: ' .. (r2.stderr or ''), vim.log.levels.ERROR) end
        if which then look.dismiss(which) end
        render()
        vim.notify('told the agent to go on')
      end)
    end)
  end)
end

local keys_help = {
  'Slides',
  '  j k gg G ctrl-d ctrl-u   move within the slide',
  '  J ]]  /  K [[            next / previous slide (with a count: 3J)',
  '  H  /  L                  first / last slide',
  '  :Slide N  :SlideEdit     jump to slide N; edit the deck at this slide',
  'Code (in the pane below the slides, opened on first use)',
  '  gf  or  <CR>             on code in a slide: the file at that line, at the PR\'s head;',
  '                           on a path:line: that file',
  '  gd                       the same file\'s change in unified.nvim (diffview for a PR',
  '                           below the checkout\'s head)',
  '  gF                       the path:line under the cursor as a plain file',
  '  gu                       changed files not viewed yet',
  'Replying',
  '  c                        comment on this slide',
  '  a then a letter          pick an option (a b c …)',
  '  ]n  /  [n                next / previous slide still in discussion',
  '  S  or  :send             send new comments to the agent (again whenever you add more)',
  '  C                        tell the agent to go on',
  '  :send!                   resend unreplied comments if the agent missed a submission',
  '  :Reconnect               find an existing agent in this tab; choose if several',
  'q quits the panel',
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

-- gf on a slide or doc: on code from an excerpt, the file at that line;
-- else the path:line under the cursor.
local function follow()
  local hit = vim.api.nvim_get_current_buf() == deck.buf and deck.at[vim.api.nvim_win_get_cursor(0)[1] - 1]
  if hit then return open_file(hit.l, hit.path, hit.line, true) end
  local ref = ref_under_cursor()
  if not ref then return vim.notify('no code or path under the cursor') end
  local l = #layers() > 0 and layer_for(norm(ref.path), ref.line)
  if l then return open_file(l, norm(ref.path), ref.line, true) end
  side_show({ file = resolve(ref.path), line = ref.line }, true)
end

-- gd: the change to the file under the cursor, in unified.nvim.
local function diff_here()
  local hit = vim.api.nvim_get_current_buf() == deck.buf and deck.at[vim.api.nvim_win_get_cursor(0)[1] - 1]
  if hit then return open_diff(hit.l, hit.path, hit.line, true) end
  local ref = ref_under_cursor()
  local l = ref and #layers() > 0 and layer_for(norm(ref.path), ref.line)
  if not l then return vim.notify('no changed code or path under the cursor') end
  open_diff(l, norm(ref.path), ref.line, true)
end

local function plain()
  local ref = ref_under_cursor()
  if ref then side_show({ file = resolve(ref.path), line = ref.line }, true) else vim.notify('no path under the cursor') end
end

local function map(buf, keys, fn)
  for _, k in ipairs(keys) do vim.keymap.set('n', k, fn, { buffer = buf, nowait = true, silent = true }) end
end

local function code_keys(buf)
  map(buf, { 'gf', '<CR>' }, follow)
  map(buf, { 'gd' }, diff_here)
  map(buf, { 'gF' }, plain)
  map(buf, { 'gu' }, unviewed)
  map(buf, { 'g?' }, function() vim.notify(table.concat(keys_help, '\n')) end)
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
  code_keys(buf)
  map(buf, { 'q' }, function() vim.cmd('qall') end)
  map(buf, { 'J', ']]' }, step(1))
  map(buf, { 'K', '[[' }, step(-1))
  map(buf, { 'H' }, go(1))
  map(buf, { 'L' }, go('last'))
  map(buf, { 'c' }, comment)
  map(buf, { 'a' }, pick)
  map(buf, { 'S' }, send)
  map(buf, { 'C' }, go_on)
  map(buf, { ']n' }, thread(1))
  map(buf, { '[n' }, thread(-1))
  vim.api.nvim_buf_create_user_command(buf, 'Send', function(o) send(o.bang) end, { bang = true })
  vim.api.nvim_buf_create_user_command(buf, 'Reconnect', function() connect(nil, true) end, {})
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

-- A replacement agent explicitly claims the deck; never guess a recipient from
-- focus or a pane label. Persist the recipient so a restarted panel can find it.
function M.bind(path, block, session)
  path = resolve(path)
  local ref = as_ref(block, session)
  if not ref then return 'bind needs a pane ref (from wt pane here)' end
  local ok, lines = pcall(vim.fn.readfile, path)
  if not ok then return 'cannot read deck: ' .. path end
  local old = owners[path] or agent_target(lines)
  local changed = old ~= ref
  if changed and delivery and delivery.path == path and delivery.busy then
    return 'comments are being sent; wait for the send to finish before reconnecting this deck'
  end
  local header = '<!-- agent: ' .. ref .. ' -->'
  local found, dirty = false, false
  for i, l in ipairs(lines) do
    if l:match('^%s*<!%-%-%s*agent:') then
      found, dirty = true, l ~= header
      lines[i] = header
      break
    end
  end
  if not found then table.insert(lines, 1, header); dirty = true end
  if dirty then
    local wrote, result = pcall(vim.fn.writefile, lines, path)
    if not wrote or result ~= 0 then return 'cannot save deck recipient: ' .. path end
  end
  owners[path] = ref
  recipient_error = nil
  if changed then
    -- An Enter retry belongs to the old pane. The replacement must receive
    -- the whole batch, including submitted comments it never acknowledged.
    if delivery and delivery.path == path then delivery = nil end
    sent[path] = nil
  end
  if path == deck.path then deck.stamp = stamp(path); render() end
  return 0
end

function M.slides(path, n, block, session)
  path = resolve(path)
  if block and block ~= '' then
    local err = M.bind(path, block, session)
    if err ~= 0 then return err end
  end
  if path ~= deck.path then attention, recipient_error = nil, nil end
  deck.path = path
  deck.want = n or 'last'
  deck.stamp = stamp(deck.path)
  local win = main_win()
  vim.api.nvim_set_current_win(win)
  vim.api.nvim_win_set_buf(win, deck_buf())
  vim.wo[win].wrap = true
  vim.wo[win].linebreak = true
  look.present('Look at the slides: ' .. vim.fn.fnamemodify(path, ':t'))
  render()
  return 0
end

-- Presenting asks for the user by itself (see `look`). This is the explicit
-- override: waiting asks until the comments are sent or withdrawn, whether or
-- not they look; idle withdraws every ask, including the automatic one.
function M.status(state, msg)
  if state ~= 'waiting' and state ~= 'idle' then return 'status must be waiting or idle' end
  attention = state == 'waiting' and { msg = msg and msg ~= '' and msg or 'Comments needed' } or nil
  if state == 'idle' then look.dismiss() end
  if deck.buf then render() else update_activity() end
  return 0
end

-- What a path is called in a message: relative to the workspace.
local function shown(path, line)
  local rel = path:sub(1, #root + 1) == root .. '/' and path:sub(#root + 2) or vim.fn.fnamemodify(path, ':~')
  return rel .. (tonumber(line) and (':' .. line) or '')
end

local function presented(msg)
  if look.present(msg) then update_activity() end
end

function M.doc(path, line)
  local win = main_win()
  vim.api.nvim_set_current_win(win)
  vim.cmd.edit(vim.fn.fnameescape(resolve(path)))
  vim.bo.autoread = true
  vim.wo[win].wrap = true
  vim.wo[win].linebreak = true
  -- Findings in a package or checkpoint cite path:LINE; gf and gd open those.
  code_keys(0)
  if tonumber(line) then pcall(vim.api.nvim_win_set_cursor, win, { tonumber(line), 0 }) end
  presented('Look at ' .. shown(resolve(path), line))
  return 0
end

-- A file in the pane below the slides.
function M.code(path, line)
  side_show({ file = resolve(path), line = tonumber(line) }, false)
  presented('Look at ' .. shown(resolve(path), line))
  return 0
end

-- A PR's change to one file, in unified.nvim in the pane below the slides.
function M.diff(pr, path, line)
  if #layers() == 0 then return 'no review/layers.tsv in ' .. root end
  if not path or path == '' then return 'diff needs a path (whole diffs go in slides, as Diff: lines)' end
  local rel = norm(path)
  local l
  if pr and pr ~= '' then
    l = layer(pr)
    if not l then return '#' .. tostring(pr):gsub('^#', '') .. ' is not in review/layers.tsv' end
    if not excerpt.file(diff_lines(l), rel) then return path .. ' is not in the diff of #' .. l.pr end
  else
    l = layer_for(rel, tonumber(line))
    if not l then return path .. ' is not changed anywhere in the stack' end
  end
  open_diff(l, rel, tonumber(line), false)
  presented('Look at the diff: #' .. l.pr .. ' ' .. shown(resolve(path), line))
  return 0
end

-- The agent answering means the user replied to it in its own pane: its
-- status goes back to working after it had stopped (Rex's program status;
-- tmux has none, so there only the other replies count).
local watch = { which = nil, armed = false, busy = false }
local function check_reply()
  local which = look.which()
  if not which or not recipient.status or not deck.path or watch.busy then return end
  if watch.which ~= which then watch.which, watch.armed = which, false end
  local okr, lines = pcall(vim.fn.readfile, deck.path)
  local ref = owners[deck.path] or (okr and agent_target(lines))
  if not ref or not ref:match('^rex:') then return end
  watch.busy = true
  recipient.status(ref, function(state)
    watch.busy = false
    if watch.which ~= look.which() then return end
    if state ~= 'working' then
      watch.armed = true
    elseif watch.armed then
      look.dismiss(watch.which)
      render()
    end
  end)
end

-- The agent rewrites files underneath us; poll rather than fs_event, which
-- loses track of files replaced by rename.
local ticks = 0
local timer = uv.new_timer()
timer:start(700, 700, vim.schedule_wrap(function()
  ticks = ticks + 1
  local s = stamp(deck.path)
  local n = stamp(review_file('annotations.tsv'))
  if (s and s ~= deck.stamp) or n ~= notes.seen then
    deck.stamp, notes.seen = s or deck.stamp, n
    if deck.buf and vim.api.nvim_buf_is_valid(deck.buf) then render() end
  end
  if ticks % 3 == 0 then check_reply() end
  vim.cmd('silent! checktime')
end))

return M
