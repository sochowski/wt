-- NVIM_LOG_FILE=/tmp/wt-review-tests.log nvim --headless -u NONE -i NONE -l kits/review/tests/recipient.lua
-- Which panes are agents, and in which tab, is wt pane's job (tests/wt-pane.sh).
-- Here: choosing among them, and never sending to a guess.
vim.g.wt_pane = 'wt-pane'
local recipient = dofile('kits/review/nvim/recipient.lua')
local calls, picker = {}, nil
vim.system = function(argv, _, cb) calls[#calls + 1] = { argv = argv, cb = cb } end
vim.ui.select = function(items, opts, cb) picker = { items = items, opts = opts, cb = cb } end
local function eq(a, b) assert(vim.deep_equal(a, b), vim.inspect(a) .. ' ~= ' .. vim.inspect(b)) end
local function answer(i, data, code, stderr)
  calls[i].cb({ code = code or 0, stdout = type(data) == 'string' and data or vim.json.encode(data),
    stderr = stderr or 'wt pane: gone' })
  vim.wait(15, function() return false end)
end
local function agent(ref, name, pid)
  return { ref = ref, agent = name or 'claude', pid = pid or 1, cwd = '/workspace', label = 'shell', window = 'w' }
end
local function shell(ref) return { ref = ref, agent = vim.NIL, pid = 2, cwd = '/workspace', label = 'shell' } end
local function begin(preferred, panes)
  calls, picker = {}, nil
  local result = {}
  recipient.resolve(preferred, function(err, selected) result.err, result.selected, result.done = err, selected, true end)
  eq(calls[1].argv, { 'wt-pane', 'agents' })
  answer(1, panes)
  return result
end
local ok, err = xpcall(function()
  -- A sole agent wins even when the saved recipient is gone.
  local result = begin('rex:old/dead', { agent('rex:s/agent', 'codex') })
  eq(result.selected.ref, 'rex:s/agent')
  eq(result.selected.agent, 'codex')
  eq(picker, nil)
  eq(#calls, 1)

  -- A valid saved recipient wins over other agents in the same tab, in either backend.
  result = begin('tmux:%2@/tmp/s', { agent('tmux:%1@/tmp/s'), agent('tmux:%2@/tmp/s', 'codex') })
  eq(result.selected.ref, 'tmux:%2@/tmp/s')
  eq(picker, nil)

  -- Ambiguity opens a picker; it cannot silently send to the first agent.
  result = begin(nil, { agent('rex:s/b', 'codex', 7), agent('rex:s/a', 'claude', 6) })
  eq(result.done, nil)
  eq(#picker.items, 2)
  eq(picker.items[1].ref, 'rex:s/a') -- stable order
  assert(picker.opts.format_item(picker.items[1]):find('claude', 1, true))
  picker.cb(picker.items[2])
  eq(result.done, nil) -- validate the selected process after the picker closes
  eq(calls[2].argv, { 'wt-pane', 'info', 'rex:s/b' })
  answer(2, agent('rex:s/b', 'codex', 7))
  eq(result.selected.ref, 'rex:s/b')

  -- The picked agent restarted (new pid) or a shell took over: don't send.
  result = begin(nil, { agent('rex:s/a', 'claude', 10), agent('rex:s/b', 'codex') })
  picker.cb(picker.items[1])
  answer(2, agent('rex:s/a', 'claude', 11))
  assert(result.err:find('Selected agent changed', 1, true))
  eq(result.selected, nil)

  result = begin(nil, { agent('rex:s/a'), agent('rex:s/b', 'codex') })
  picker.cb(picker.items[1])
  answer(2, shell('rex:s/a'))
  assert(result.err:find('Selected agent changed', 1, true))

  result = begin(nil, { agent('rex:s/a'), agent('rex:s/b', 'codex') })
  picker.cb(picker.items[1])
  answer(2, {}, 1, 'wt pane: no tmux pane %1')
  eq(result.err, 'no tmux pane %1')

  result = begin(nil, { agent('rex:s/a'), agent('rex:s/b', 'codex') })
  picker.cb(nil)
  assert(result.err:find('cancelled', 1, true))

  -- Non-agents in the listing never become recipients.
  result = begin(nil, { shell('rex:s/shell') })
  assert(result.err:find('No running agent', 1, true))
  result = begin(nil, {})
  assert(result.err:find('No running agent', 1, true))

  -- wt pane's errors (not in a multiplexer, an uninspectable pane) come through as they are.
  calls = {}
  recipient.resolve(nil, function(e) result = e end)
  answer(1, '', 1, 'wt pane: could not inspect every pane in this tab (Rex block x)\n')
  eq(result, 'could not inspect every pane in this tab (Rex block x)')

  calls = {}
  recipient.resolve(nil, function(e) result = e end)
  answer(1, 'not json')
  assert(result:find('Invalid response', 1, true))

  -- A missing wt-pane executable is an error, not a crash.
  vim.system = function() error('ENOENT: wt-pane') end
  recipient.resolve(nil, function(e) result = e end)
  assert(result:find('ENOENT', 1, true))
end, debug.traceback)
if not ok then io.stderr:write(err .. '\n'); vim.cmd('cquit 1') end
print('recipient tests passed')
vim.cmd('qa!')
