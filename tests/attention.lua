-- Run from the repo root:
-- nvim --headless -u NONE -i NONE -l tests/attention.lua
-- A plain panel (nvim/attention.lua's panel()) in a real nvim, with stdout
-- captured instead of reaching the terminal.
local tmp = vim.fn.tempname()
vim.fn.mkdir(tmp, 'p')
local reports = {}
local stdout = io.stdout
io.stdout = {
  write = function(_, s) reports[#reports + 1] = s end,
  flush = function() end,
}
vim.env.REX_SESSION, vim.env.REX_BLOCK, vim.env.TMUX = 'test-session', 'test-block', nil
vim.g.wt_panel_recent = 0.05

local function eq(actual, expected)
  assert(vim.deep_equal(actual, expected), vim.inspect(actual) .. ' ~= ' .. vim.inspect(expected))
end
local function contains(s, text) assert(s:find(text, 1, true), vim.inspect(s) .. ' missing ' .. text) end
local function settle() vim.wait(30, function() return false end) end
local function focus(on) vim.api.nvim_exec_autocmds(on and 'FocusGained' or 'FocusLost', {}) end
local function status(state, msg)
  local report = reports[#reports]
  contains(report, '\27]7501;state=' .. state .. ':id=wt-panel:app=wt')
  if msg then contains(vim.base64.decode(report:match(':msg=([^\27]+)')), msg) end
end
local function file(name)
  local path = tmp .. '/' .. name
  vim.fn.writefile({ name, 'second line' }, path)
  return path
end

local ok, err = xpcall(function()
  dofile('nvim/attention.lua').panel()
  local a, b = file('a.txt'), file('b.txt')

  -- Shown while the user looks elsewhere: blocked, naming the file, once for
  -- the whole Ex line however many events it fires.
  vim.cmd('tabnew | edit ' .. a .. ' | normal! G')
  settle()
  eq(#reports, 1)
  status('blocked', 'Look at ' .. vim.fn.fnamemodify(a, ':~:.'))
  contains(reports[1], ':kind=question')
  vim.cmd('edit ' .. a)
  settle()
  eq(#reports, 1) -- the same ask isn't repeated

  focus(true)
  status('clear')
  local n = #reports
  vim.cmd('edit ' .. b) -- shown while they look: nothing to ask
  settle()
  eq(#reports, n)
  vim.cmd('tabnext')
  settle()
  eq(#reports, n)

  -- Popups and scratch buffers from plugins aren't something shown.
  focus(false)
  local scratch = vim.api.nvim_create_buf(false, true)
  local float = vim.api.nvim_open_win(scratch, false,
    { relative = 'editor', row = 1, col = 1, width = 10, height = 2 })
  settle()
  vim.api.nvim_win_close(float, true)
  vim.cmd('botright new')
  vim.bo.buftype = 'nofile'
  settle()
  eq(#reports, n)
  vim.cmd('close')

  -- A reload from disk (the agent's checktime) is presenting too.
  vim.cmd('edit ' .. b)
  settle()
  status('blocked', 'Look at ' .. vim.fn.fnamemodify(b, ':~:.'))
  focus(true)
  focus(false)
  vim.bo.autoread = true
  vim.fn.writefile({ 'changed' }, b)
  vim.cmd('sleep 20m')
  vim.uv.fs_utime(b, os.time() + 5, os.time() + 5)
  vim.cmd('checktime')
  settle()
  status('blocked', 'Look at ' .. vim.fn.fnamemodify(b, ':~:.'))

  -- Typing, clicking or scrolling in the pane without focus is looking: it
  -- takes the ask back, and shortly after it, showing doesn't ask.
  vim.api.nvim_feedkeys('j', 'xt', false)
  settle()
  status('clear')
  n = #reports
  vim.cmd('edit ' .. a)
  settle()
  eq(#reports, n)
  vim.wait(80, function() return false end)
  vim.cmd('edit ' .. b)
  settle()
  status('blocked', 'b.txt')
  -- The agent's keys through :normal or the socket aren't the user's.
  vim.cmd('normal! gg')
  settle()
  status('blocked', 'b.txt')

  vim.api.nvim_exec_autocmds('VimLeavePre', {})
  status('clear')

  -- tmux: the bell rings once on becoming blocked, and the title carries it.
  vim.env.REX_SESSION, vim.env.REX_BLOCK, vim.env.TMUX = nil, nil, '/tmp/tmux-test,1,0'
  reports = {}
  dofile('nvim/attention.lua').panel()
  vim.cmd('edit ' .. a)
  settle()
  eq(#reports, 1)
  contains(reports[1], '\27]2;nvim: Look at ')
  contains(reports[1], '\7')
  vim.cmd('edit ' .. b)
  settle()
  eq(#reports, 2)
  contains(reports[2], 'b.txt')
  assert(not reports[2]:find('\7', 1, true), 'a changed ask must not ring again')
  focus(true)
  eq(reports[3], '\27]2;nvim\27\\')
end, debug.traceback)

io.stdout = stdout
vim.fn.delete(tmp, 'rf')
if not ok then
  io.stderr:write(err .. '\n')
  vim.cmd('cquit 1')
end
print('attention tests passed')
vim.cmd('qa!')
