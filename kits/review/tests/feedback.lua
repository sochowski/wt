-- Run from the repo root:
-- NVIM_LOG_FILE=/tmp/wt-review-tests.log nvim --headless -u NONE -i NONE -l kits/review/tests/feedback.lua
-- Real nvim buffers, mocked Rex transport: exercise asynchronous failures and
-- deck changes without typing into a live agent's terminal.
local tmp = vim.fn.tempname()
vim.fn.mkdir(tmp, 'p')
local calls, notices, reports = {}, {}, {}
local stdout = io.stdout
io.stdout = {
  write = function(_, s) reports[#reports + 1] = s end,
  flush = function() end,
}
vim.env.REX_SESSION = 'test-session'
vim.notify = function(msg) notices[#notices + 1] = msg end
vim.system = function(argv, opts, cb)
  assert(opts.timeout == 15000)
  calls[#calls + 1] = { argv = argv, cb = cb }
end
local function eq(actual, expected)
  assert(vim.deep_equal(actual, expected), vim.inspect(actual) .. ' ~= ' .. vim.inspect(expected))
end
local function contains(s, text) assert(s:find(text, 1, true), s .. ' missing ' .. text) end
local function reply(i, code)
  calls[i].cb({ code = code or 0, stderr = code and 'transport failed' or '' })
  vim.wait(30, function() return false end)
end
local function status(state, msg)
  local report = reports[#reports]
  contains(report, 'state=' .. state .. ':id=wt-review:app=wt')
  if msg then contains(vim.base64.decode(report:match(':msg=([^\27]+)')), msg) end
end
local function bar(text) contains(vim.wo.winbar, text) end
local function target(i, block, session)
  local argv = calls[i].argv
  eq({ unpack(argv, #argv - 3) }, { '-s', session, '-b', block })
end
local function write(name, lines)
  local path = tmp .. '/' .. name
  vim.fn.writefile(lines, path)
  return path
end

local resolve_recipient = function(preferred, cb)
  cb(nil, preferred or { block = 'fallback', session = 'test-session', agent = 'codex' })
end
local ok, err = xpcall(function()
  local panel = assert(loadfile('kits/review/nvim/review.lua'))({
    resolve = function(preferred, cb) resolve_recipient(preferred, cb) end,
  })
  local a = write('a.md', { '# Choice', '> **' .. vim.env.USER .. ':** first' })
  panel.slides(a, 1, 'agent-a', 'session-a')
  bar('1 unsent')
  assert(vim.fn.maparg('S', 'n', false, true).callback, 'S must keep sending comments')
  panel.status('waiting', 'Comments needed: A')
  -- Unsent feedback takes precedence over the original request.
  status('blocked', '1 comments unsent')
  local count = #reports
  panel.slides(a, 1, 'agent-a', 'session-a')
  eq(#reports, count) -- render does not repeatedly notify Rex

  vim.cmd('Send')
  eq(#calls, 1)
  target(1, 'agent-a', 'session-a')
  status('working', 'Sending comments')
  vim.cmd('Send')
  eq(#calls, 1) -- overlapping sends cannot duplicate a paste
  reply(1, 1)
  status('blocked', 'Send failed')
  bar('send failed')
  vim.cmd('Send')
  eq(calls[2].argv[2], 'send') -- failed paste remains retryable
  reply(2)
  eq(calls[3].argv[2], 'send-key')
  reply(3, 1)
  status('blocked', 'Send failed')
  bar('send failed') -- Enter failure must not mark comments sent
  contains(notices[#notices], 'Enter only')

  -- Edit the deck after the paste, then retry: only the original snapshot is
  -- submitted. The new comment must remain unsent.
  vim.fn.writefile({ '# Choice', '> **' .. vim.env.USER .. ':** first',
    '> **' .. vim.env.USER .. ':** second' }, a)
  panel.slides(a, 1, 'agent-a', 'session-a')
  vim.cmd('Send')
  eq(calls[4].argv[2], 'send-key')
  target(4, 'agent-a', 'session-a')
  reply(4)
  bar('1 unsent')
  status('blocked', '1 comments unsent')

  -- Changing decks during the async calls must not mark the new deck sent.
  vim.cmd('Send')
  contains(calls[5].argv[3], 'second')
  assert(not calls[5].argv[3]:find('"first"', 1, true))
  local b = write('b.md', { '<!-- agent: old-agent old-session -->', '# Other',
    '> **' .. vim.env.USER .. ':** other' })
  panel.slides(b, 1, 'agent-b', 'session-b')
  panel.status('waiting', 'Comments needed: B')
  reply(5)
  target(6, 'agent-a', 'session-a')
  reply(6)
  bar('1 unsent')
  vim.cmd('Send')
  target(7, 'agent-b', 'session-b') -- presenter overrides stale metadata
  reply(7)
  reply(8)
  status('clear')
  local before = #calls
  vim.cmd('Send')
  eq(#calls, before)
  panel.slides(a, 1)
  vim.cmd('Send')
  eq(#calls, before) -- callbacks marked a's batch, not b's

  -- Metadata is still supported for manually started panels.
  local c = write('c.md', { '<!-- agent: agent-c session-c -->', '# Legacy',
    '> **' .. vim.env.USER .. ':** legacy' })
  panel.slides(c, 1)
  vim.cmd('Send')
  target(9, 'agent-c', 'session-c')
  reply(9)
  reply(10)
  status('clear')
  panel.status('waiting', 'Comments needed: legacy\nfollowup')
  status('blocked', 'Comments needed: legacy followup')
  panel.status('idle', '')
  status('clear')

  -- A missing Rex executable also leaves feedback retryable.
  vim.fn.writefile({ '# Choice', '> **' .. vim.env.USER .. ':** third' }, a)
  panel.slides(a, 1)
  local system = vim.system
  vim.system = function() error('rex missing') end
  vim.cmd('Send')
  status('blocked', 'Send failed')
  vim.system = system
  vim.cmd('Send')
  reply(11)
  reply(12)
  status('clear')

  -- Accepted input is not an acknowledgement from the agent. A deliberate
  -- resend is available, but already answered comments must never reappear.
  vim.cmd('Send!')
  contains(calls[13].argv[3], 'third')
  reply(13)
  reply(14)
  vim.fn.writefile({ '# Choice', '> **' .. vim.env.USER .. ':** third',
    '> **claude:** handled' }, a)
  panel.slides(a, 1)
  vim.cmd('Send!')
  eq(#calls, 14)

  -- Completing an older submission must not clear a newly raised ask.
  vim.fn.writefile({ '# Choice', '> **' .. vim.env.USER .. ':** fourth' }, a)
  panel.slides(a, 1)
  panel.status('waiting', 'Old ask')
  vim.cmd('Send')
  panel.status('waiting', 'New ask')
  reply(15)
  reply(16)
  status('blocked', 'New ask')
  panel.status('idle', '')
  status('clear')

  -- A dead session leaves comments on disk. A replacement agent claims the
  -- deck, persists the new recipient and sends the full batch to that pane.
  local d = write('reconnect.md', { '<!-- agent: dead-pane dead-session -->',
    '# Saved question', '> **' .. vim.env.USER .. ':** keep this feedback' })
  panel.slides(d, 1)
  vim.cmd('Send')
  target(#calls, 'dead-pane', 'dead-session')
  reply(#calls, 1)
  status('blocked', 'Send failed')
  eq(panel.bind(d, 'replacement', 'new-session'), 0)
  eq(vim.fn.readfile(d)[1], '<!-- agent: replacement new-session -->')
  contains(table.concat(vim.fn.readfile(d), '\n'), 'keep this feedback')
  vim.cmd('Send')
  target(#calls, 'replacement', 'new-session')
  reply(#calls)
  reply(#calls, 1) -- pane disappears after text, before Enter
  status('blocked', 'Send failed')

  -- Presenting the saved deck from another agent reconnects it too. A pasted
  -- batch on the dead pane must not turn into an Enter-only retry on the new one.
  eq(panel.slides(d, 1, 'replacement-2', 'new-session-2'), 0)
  vim.cmd('Send')
  eq(calls[#calls].argv[2], 'send')
  target(#calls, 'replacement-2', 'new-session-2')
  contains(calls[#calls].argv[3], 'keep this feedback')
  local header = vim.fn.readfile(d)[1]
  local err = panel.bind(d, 'wrong-pane', 'wrong-session')
  contains(err, 'being sent')
  eq(vim.fn.readfile(d)[1], header) -- cannot redirect an in-flight batch
  reply(#calls)
  reply(#calls)

  -- Submitted but unacknowledged feedback is offered again to a new owner.
  eq(panel.bind(d, 'replacement-3', 'new-session-3'), 0)
  bar('1 unsent')
  vim.cmd('Send')
  reply(#calls)
  reply(#calls)

  -- The persisted header works without the in-memory owner table, as it would
  -- after restarting nvim. Recipient discovery is tested separately.
  local saved = write('restored.md', vim.fn.readfile(d))
  panel.slides(saved, 1)
  vim.cmd('Send')
  target(#calls, 'replacement-3', 'new-session-3')
  reply(#calls)
  reply(#calls)

  for _, name in ipairs({ 'agent', 'claude', 'ChatGPT', 'Codex' }) do
    local acknowledged = write(name .. '.md', { '# Answered',
      '> **' .. vim.env.USER .. ':** already handled', '> **' .. name .. ':** ✅ received' })
    panel.slides(acknowledged, 1)
    local before_send = #calls
    vim.cmd('Send!')
    eq(#calls, before_send) -- every supported agent label acknowledges feedback
    status('clear')
  end

  -- A :send preflight can discover an existing agent after the old pane dies,
  -- even when an Enter-only batch was left behind on that pane.
  local recovery = write('discovered.md', { '<!-- agent: old-pane old-session -->',
    '# Recovery', '> **' .. vim.env.USER .. ':** surviving feedback' })
  panel.slides(recovery, 1)
  vim.cmd('Send')
  reply(#calls)
  reply(#calls, 1)
  resolve_recipient = function(preferred, cb)
    eq(preferred, { block = 'old-pane', session = 'old-session' })
    cb(nil, { block = 'existing-agent', session = 'same-tab-session', agent = 'codex' })
  end
  vim.cmd('Send')
  eq(calls[#calls].argv[2], 'send') -- full text, not Enter on the replacement
  target(#calls, 'existing-agent', 'same-tab-session')
  reply(#calls)
  reply(#calls)

  local before_reconnect = #calls
  resolve_recipient = function(preferred, cb)
    eq(preferred, nil) -- explicit reconnect offers a choice, even with a saved owner
    cb('No running agent found in this nvim tab; comments remain saved')
  end
  vim.cmd('Reconnect')
  status('blocked', 'No running agent')
  eq(#calls, before_reconnect) -- discovery failure must never send input
  contains(table.concat(vim.fn.readfile(recovery), '\n'), 'surviving feedback')

  -- Browsing away while a picker is open cannot redirect a send to the new deck.
  local resolve_callback
  resolve_recipient = function(_, cb) resolve_callback = cb end
  vim.cmd('Send!')
  panel.slides(a, 1)
  resolve_callback(nil, { block = 'existing-agent', session = 'same-tab-session', agent = 'codex' })
  eq(#calls, before_reconnect)
  contains(notices[#notices], 'deck or recipient changed')
end, debug.traceback)

io.stdout = stdout
vim.fn.delete(tmp, 'rf')
if not ok then
  io.stderr:write(err .. '\n')
  vim.cmd('cquit 1')
end
print('feedback tests passed')
vim.cmd('qa!')
