-- NVIM_LOG_FILE=/tmp/wt-review-tests.log nvim --headless -u NONE -i NONE -l kits/review/tests/recipient.lua
local recipient = dofile('kits/review/nvim/recipient.lua')
local calls, picker = {}, nil
vim.system = function(argv, _, cb) calls[#calls + 1] = { argv = argv, cb = cb } end
vim.ui.select = function(items, opts, cb) picker = { items = items, opts = opts, cb = cb } end
local function eq(a, b) assert(vim.deep_equal(a, b), vim.inspect(a) .. ' ~= ' .. vim.inspect(b)) end
local function answer(i, data, code)
  calls[i].cb({ code = code or 0, stdout = vim.json.encode(data), stderr = 'gone' })
  vim.wait(15, function() return false end)
end
local function pane(id, window, creator)
  return { block_id = id, window_id = window or 'this-tab',
    creator_name = creator or 'com.superlogical.terminal', label = 'shell' }
end
local function begin(preferred, panes)
  calls, picker = {}, nil
  local result = {}
  recipient.resolve(preferred, function(err, selected) result.err, result.selected, result.done = err, selected, true end)
  eq(calls[1].argv, { 'rex', 'whoami', '--json' })
  answer(1, { block_id = 'nvim', session_id = 'current-session', window_id = 'this-tab' })
  eq(calls[2].argv, { 'rex', 'block', 'ls', '-s', 'current-session', '-w', 'this-tab', '--json' })
  answer(2, { blocks = panes })
  return result
end
local function process(name)
  return { foreground = { name = name, cwd = '/workspace' } }
end
local ok, err = xpcall(function()
  -- Reject other tabs even if a server ever returns a wider listing, and never
  -- use labels to infer which terminal is an agent.
  local result = begin({ block = 'dead', session = 'old-session' }, {
    pane('nvim'), pane('agent'), pane('shell'), pane('other-tab-agent', 'another-tab'),
    pane('non-terminal', 'this-tab', 'some.other.block'),
  })
  eq(#calls, 4)
  eq(calls[3].argv, { 'rex', 'block', 'call', '-s', 'current-session', '-b', 'agent', 'process' })
  answer(3, process('codex'))
  answer(4, process('bash'))
  eq(result.selected.block, 'agent')
  eq(result.selected.session, 'current-session')
  eq(picker, nil)

  -- A valid saved recipient wins over other agents in the same tab.
  result = begin({ block = 'b', session = 'current-session' }, { pane('a'), pane('b') })
  answer(3, process('claude'))
  answer(4, process('codex'))
  eq(result.selected.block, 'b')
  eq(picker, nil)

  -- Ambiguity opens a picker; it cannot silently send to the first agent.
  result = begin(nil, { pane('a'), pane('b') })
  answer(3, process('claude'))
  answer(4, process('codex'))
  eq(result.done, nil)
  eq(#picker.items, 2)
  assert(picker.opts.format_item(picker.items[1]):find('claude', 1, true))
  picker.cb(picker.items[2])
  eq(result.done, nil) -- validate the selected process after the picker closes
  answer(5, process('codex'))
  eq(result.selected.block, 'b')

  result = begin(nil, { pane('a'), pane('b') })
  answer(3, { foreground = { name = 'claude', pid = 10 } })
  answer(4, process('codex'))
  picker.cb(picker.items[1])
  answer(5, { foreground = { name = 'claude', pid = 11 } })
  assert(result.err:find('Selected agent changed', 1, true))
  eq(result.selected, nil)

  result = begin(nil, { pane('a'), pane('b') })
  answer(3, process('claude'))
  answer(4, process('codex'))
  picker.cb(picker.items[1])
  answer(5, process('bash'))
  assert(result.err:find('Selected agent changed', 1, true))
  eq(result.selected, nil)

  result = begin(nil, { pane('a'), pane('b') })
  answer(3, process('claude'))
  answer(4, process('codex'))
  picker.cb(nil)
  assert(result.err:find('cancelled', 1, true))

  result = begin(nil, { pane('shell') })
  answer(3, process('bash'))
  assert(result.err:find('No running agent', 1, true))

  -- A disappearing/uninspectable pane must not create false certainty about
  -- which of the remaining panes should receive the comments.
  result = begin(nil, { pane('a'), pane('b') })
  answer(3, process('codex'))
  answer(4, {}, 1)
  assert(result.err:find('Could not inspect every pane', 1, true))
  eq(result.selected, nil)

  -- Interpreted agent CLIs expose the script rather than the node executable.
  result = begin(nil, { pane('a') })
  answer(3, { foreground = { name = 'node', script = '/pkg/@anthropic-ai/claude-code/cli.js' } })
  eq(result.selected.agent, 'claude')

  -- An old agent in the ancestry is insufficient if a shell has foreground.
  result = begin(nil, { pane('a') })
  answer(3, { foreground = { name = 'bash' }, ancestors = { { name = 'codex' } } })
  assert(result.err:find('No running agent', 1, true))

  result = begin(nil, { pane('nvim') })
  assert(result.err:find('No running agent', 1, true))
  eq(#calls, 2)

  calls = {}
  recipient.resolve(nil, function(e) result = e end)
  calls[1].cb({ code = 0, stdout = 'not json' })
  vim.wait(15, function() return false end)
  assert(result:find('Invalid response', 1, true))
end, debug.traceback)
if not ok then io.stderr:write(err .. '\n'); vim.cmd('cquit 1') end
print('recipient tests passed')
vim.cmd('qa!')
