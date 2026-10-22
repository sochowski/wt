-- The review panel: markdown slides and docs that reload while an agent
-- writes them. review-show starts nvim with this file and calls into it over
-- the --listen socket; the functions return 0 so --remote-expr stays quiet.
--
-- Slides are one markdown file split on lines of `---` outside code fences,
-- rendered by render-markdown (mermaid blocks too, when its mermaid plugin and
-- a renderer are installed), with treesitter highlighting fenced code.
--
-- Keys on a slide keep vim's meaning where they can: j / k, gg / G, ctrl-d
-- and friends move within the slide; l / h (or ]] / [[) go to the next and
-- previous slide and take a count; L / H go to the last and first; gf or
-- <CR> opens the path:line under the cursor beside the slides; q quits; g?
-- lists the keys. :SlideEdit opens the deck at the current slide, and
-- :Slide N jumps to slide N.

local M = {}
local uv = vim.uv or vim.loop
local root = vim.fn.getcwd()
local main = vim.api.nvim_get_current_win()
local deck = { path = nil, idx = 1, want = nil, count = 0, starts = {}, buf = nil, stamp = nil }
local code_win = nil

local function stamp(path)
  local st = path and uv.fs_stat(path)
  return st and (st.mtime.sec .. '.' .. st.mtime.nsec .. ':' .. st.size) or nil
end

local function trim(lines)
  while #lines > 0 and lines[1]:match('^%s*$') do table.remove(lines, 1) end
  while #lines > 0 and lines[#lines]:match('^%s*$') do table.remove(lines) end
  return lines
end

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

local function main_win()
  if not vim.api.nvim_win_is_valid(main) then main = vim.api.nvim_get_current_win() end
  return main
end

local function render()
  if not deck.path then return end
  local ok, lines = pcall(vim.fn.readfile, deck.path)
  local slides, starts = {}, {}
  if ok then slides, starts = split(lines) end
  if #slides == 0 then slides = { { '_No slides yet in ' .. vim.fn.fnamemodify(deck.path, ':~:.') .. '._' } } end
  deck.starts = starts
  deck.count = #slides
  if deck.want == 'last' then deck.idx = deck.count
  elseif tonumber(deck.want) then deck.idx = tonumber(deck.want) end
  deck.want = nil
  deck.idx = math.max(1, math.min(deck.idx, deck.count))

  local buf = deck.buf
  vim.bo[buf].modifiable = true
  vim.api.nvim_buf_set_lines(buf, 0, -1, false, slides[deck.idx])
  vim.bo[buf].modifiable = false
  vim.bo[buf].modified = false
  for _, win in ipairs(vim.fn.win_findbuf(buf)) do
    local name = vim.fn.fnamemodify(deck.path, ':t'):gsub('%%', '%%%%')
    vim.wo[win].winbar = string.format(' %s  %d/%d%%=h/l slide · H/L first/last · gf open · g? keys ', name, deck.idx, deck.count)
    vim.api.nvim_win_set_cursor(win, { 1, 0 })
  end
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

local keys_help = {
  'j k gg G ctrl-d ctrl-u   move within the slide',
  'l ]]  /  h [[            next / previous slide (with a count: 3l)',
  'L  /  H                  last / first slide',
  'gf  or  <CR>             open the path:line under the cursor beside the slides',
  'q                        quit the panel',
  ':Slide N  :SlideEdit     jump to slide N; edit the deck at this slide',
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

local function resolve(path)
  path = vim.fn.expand(path)
  if path:sub(1, 1) == '/' then return path end
  if uv.fs_stat(root .. '/' .. path) then return root .. '/' .. path end
  for _, repo in ipairs(vim.fn.glob(root .. '/repos/*', false, true)) do
    if uv.fs_stat(repo .. '/' .. path) then return repo .. '/' .. path end
  end
  return root .. '/' .. path
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
  local function map(keys, fn)
    for _, k in ipairs(keys) do vim.keymap.set('n', k, fn, { buffer = buf, nowait = true, silent = true }) end
  end
  map({ 'l', ']]' }, step(1))
  map({ 'h', '[[' }, step(-1))
  map({ 'L' }, go('last'))
  map({ 'H' }, go(1))
  map({ 'gf', '<CR>' }, function()
    local ref = ref_under_cursor()
    if ref then M.code(ref.path, ref.line) else vim.notify('no path under the cursor') end
  end)
  map({ 'g?' }, function() vim.notify(table.concat(keys_help, '\n')) end)
  map({ 'q' }, function() vim.cmd('qall') end)
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
  if tonumber(line) then pcall(vim.api.nvim_win_set_cursor, win, { tonumber(line), 0 }) end
  return 0
end

-- Open a file in a split beside the slides, reusing it on the next call.
function M.code(path, line)
  if not (code_win and vim.api.nvim_win_is_valid(code_win)) then
    vim.api.nvim_set_current_win(main_win())
    vim.cmd('rightbelow vsplit')
    code_win = vim.api.nvim_get_current_win()
  end
  vim.api.nvim_set_current_win(code_win)
  vim.cmd.edit(vim.fn.fnameescape(resolve(path)))
  vim.bo.autoread = true
  if tonumber(line) then
    pcall(vim.api.nvim_win_set_cursor, code_win, { tonumber(line), 0 })
    vim.cmd('normal! zz')
  end
  return 0
end

-- A range of a repo in its own tab: diffview when installed, else git diff.
function M.diff(repo, range)
  vim.cmd('tabnew')
  vim.cmd.tcd(vim.fn.fnameescape(resolve(repo)))
  if vim.fn.exists(':DiffviewOpen') == 2 then
    vim.cmd('DiffviewOpen ' .. range)
  else
    vim.cmd('terminal git --no-pager diff ' .. range)
  end
  return 0
end

-- The agent rewrites files underneath us; poll rather than fs_event, which
-- loses track of files replaced by rename.
local timer = uv.new_timer()
timer:start(700, 700, vim.schedule_wrap(function()
  local s = stamp(deck.path)
  if s and s ~= deck.stamp then
    deck.stamp = s
    if deck.buf and vim.api.nvim_buf_is_valid(deck.buf) then render() end
  end
  vim.cmd('silent! checktime')
end))

return M
