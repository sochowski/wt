-- Run from the repo root:
-- nvim --headless -u NONE -i NONE -l kits/review/tests/excerpt.lua
-- Diff: lines in slides, built from the PR's real diff (excerpt.lua), then
-- drawn in a real deck by review.lua.
local repo = vim.fn.getcwd()
local x = dofile('kits/review/nvim/excerpt.lua')
local function eq(a, b) assert(vim.deep_equal(a, b), vim.inspect(a) .. ' ~= ' .. vim.inspect(b)) end
local function contains(s, t) assert(s:find(t, 1, true), vim.inspect(s) .. ' missing ' .. t) end

local diff = {
  'diff --git a/app/other.py b/app/other.py',
  '@@ -1,1 +1,1 @@',
  '-old',
  '+new',
  'diff --git a/app/keys.py b/app/keys.py',
  'index 1..2 100644',
  '--- a/app/keys.py',
  '+++ b/app/keys.py',
  '@@ -10,4 +10,5 @@ def check(req):',
  ' a = 1',
  '-b = 2',
  '+b = 3',
  '+c = 4',
  ' d = 5',
  ' e = 6',
  '@@ -40,3 +41,2 @@',
  ' x = 1',
  '-y = 2',
  ' z = 3',
  'diff --git a/app/gone.bin b/app/gone.bin',
  'Binary files differ',
}
local function head()
  local t = {}
  for i = 1, 60 do t[i] = 'line ' .. i end
  t[10], t[11], t[12], t[13], t[14] = 'a = 1', 'b = 3', 'c = 4', 'd = 5', 'e = 6'
  t[41], t[42] = 'x = 1', 'z = 3'
  return t
end
local function marks(r, kind)
  local out = {}
  for _, m in ipairs(r.marks) do if m[kind] then out[#out + 1] = m end end
  return out
end

local ok, err = xpcall(function()
  -- The directive, in the forms decks use.
  eq(x.directive('Diff: #4512 repos/Runlayer/app/keys.py:10-14'),
    { pr = '4512', path = 'repos/Runlayer/app/keys.py', from = 10, to = 14 })
  eq(x.directive('**Diff:** `#4512 app/keys.py:11`'), { pr = '4512', path = 'app/keys.py', from = 11 })
  eq(x.directive('Diff: app/keys.py'), { path = 'app/keys.py' })
  eq(x.directive('Not a Diff: line'), nil)

  -- A line: the hunk around it. Added lines marked, removed ones inline
  -- above where they were, numbers from the new side.
  local r = assert(x.build({ diff = diff, path = 'app/keys.py', from = 11, head = head(), lang = 'python' }))
  eq(r.lines, { '````python', 'a = 1', 'b = 3', 'c = 4', 'd = 5', 'e = 6', '````' })
  eq({ r.from, r.to }, { 10, 14 })
  eq(vim.tbl_map(function(m) return m.row end, marks(r, 'add')), { 2, 3 })
  eq(marks(r, 'removed'), { { row = 2, removed = { 'b = 2' } } })
  eq(r.at[1], 10)
  eq(r.at[5], 14)

  -- A line outside every hunk: the nearest hunk (numbers from higher in the
  -- stack can be off by what other PRs moved).
  r = assert(x.build({ diff = diff, path = 'app/keys.py', from = 30, head = head() }))
  eq({ r.from, r.to }, { 41, 42 })
  -- Inside a long hunk (a new file is one): a window around the line.
  local new_file = { 'diff --git a/n.py b/n.py', '@@ -0,0 +1,100 @@' }
  for i = 1, 100 do new_file[#new_file + 1] = '+l' .. i end
  x.window = 20
  r = assert(x.build({ diff = new_file, path = 'n.py', from = 60 }))
  eq({ r.from, r.to }, { 55, 74 })
  x.window = 40
  -- A range reaches past the hunks: the rest comes from the file at head.
  r = assert(x.build({ diff = diff, path = 'app/keys.py', from = 8, to = 12, head = head() }))
  eq(r.lines, { '````', 'line 8', 'line 9', 'a = 1', 'b = 3', 'c = 4', '````' })
  -- Without the file at head, what the diff doesn't have is a gap.
  r = assert(x.build({ diff = diff, path = 'app/keys.py', from = 8, to = 12 }))
  eq(r.lines, { '````', '⋯', 'a = 1', 'b = 3', 'c = 4', '````' })

  -- Lines removed at the end of a hunk show below the last line.
  r = assert(x.build({ diff = diff, path = 'app/keys.py', from = 41, to = 41, head = head() }))
  eq(marks(r, 'removed'), { { row = 1, removed = { 'y = 2' }, below = true } })
  -- Notes sit on their line.
  r = assert(x.build({ diff = diff, path = 'app/keys.py', from = 41, to = 42, head = head(),
    notes = { { 42, 'z stays' } } }))
  eq(marks(r, 'note'), { { row = 2, note = 'z stays' } })
  -- No line: every hunk of the file.
  r = assert(x.build({ diff = diff, path = 'app/keys.py', head = head() }))
  eq({ r.from, r.to }, { 10, 42 })

  -- Too long: cut, and said so.
  x.max = 3
  r = assert(x.build({ diff = diff, path = 'app/keys.py', head = head() }))
  eq(#r.lines, 6)
  contains(r.lines[5], 'cut at 3 lines')
  x.max = 80

  -- What can't be shown says why.
  local none, why = x.build({ diff = diff, path = 'app/missing.py' })
  eq(none, nil)
  contains(why, 'not in this diff')
  none, why = x.build({ diff = diff, path = 'app/gone.bin' })
  contains(why, 'no line changes')

  -- In a deck: the Diff: line becomes the excerpt, and gf / gd on its code
  -- open the file or its change in the pane below, not a split here.
  local ws = vim.fn.tempname()
  vim.fn.mkdir(ws .. '/review/diffs', 'p')
  vim.fn.writefile({ '1\t77\tbase\taaaa\tbbbb\tme\tKeys\thttps://github.com/o/r/pull/77' }, ws .. '/review/layers.tsv')
  vim.fn.writefile(diff, ws .. '/review/diffs/77.diff')
  vim.fn.writefile({ '77\tapp/keys.py\t12\tc is new' }, ws .. '/review/annotations.tsv')
  vim.fn.writefile({ '# Keys', 'Diff: #77 repos/r/app/keys.py:11', '', 'After.' }, ws .. '/review/deck.md')
  vim.cmd.cd(ws)
  vim.env.REX_SESSION, vim.env.REX_BLOCK, vim.env.TMUX = nil, nil, nil
  local calls, notices = {}, {}
  vim.notify = function(m) notices[#notices + 1] = m end
  vim.system = function(argv, _, cb) calls[#calls + 1] = { argv = argv, cb = cb } end
  vim.g.wt_pane = 'wt-pane'
  local here = vim.fn.getcwd()
  local panel = assert(loadfile(repo .. '/kits/review/nvim/review.lua'))({ resolve = function() end })
  eq(here, ws)
  eq(panel.slides(ws .. '/review/deck.md', 1), 0)
  local lines = vim.api.nvim_buf_get_lines(0, 0, -1, false)
  eq(lines, { '# Keys', '*#77 · app/keys.py:10-14*', '````python',
    'a = 1', 'b = 3', 'c = 4', 'd = 5', 'e = 6', '````', '', 'After.' })
  local ns = vim.api.nvim_get_namespaces().wt_review
  local all = vim.api.nvim_buf_get_extmarks(0, ns, 0, -1, { details = true })
  local hl, virt = {}, {}
  for _, m in ipairs(all) do
    if m[4].line_hl_group then hl[#hl + 1] = m[2] end
    for _, v in ipairs(m[4].virt_lines or {}) do virt[#virt + 1] = v[#v][1] end
  end
  eq(hl, { 4, 5 }) -- b and c were added
  eq(virt, { 'b = 2', '     ◆ c is new' }) -- the removed line, then the agent's note
  eq(#vim.api.nvim_tabpage_list_wins(0), 1)
  -- gf on the code: the pane below, not a split.
  vim.api.nvim_win_set_cursor(0, { 4, 0 })
  vim.api.nvim_feedkeys('gf', 'x', false)
  eq(#vim.api.nvim_tabpage_list_wins(0), 1)
  contains(vim.fn.readfile(ws .. '/review/viewed.tsv')[1], '77\tapp/keys.py')
  contains(notices[#notices], 'no checkout') -- and says why it can't open
  vim.cmd.cd(repo)
  vim.fn.delete(ws, 'rf')
end, debug.traceback)

if not ok then
  io.stderr:write(err .. '\n')
  vim.cmd('cquit 1')
end
print('excerpt tests passed')
vim.cmd('qa!')
