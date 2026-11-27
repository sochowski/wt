-- Find existing coding-agent terminals in nvim's own tab (a Rex tab or a tmux
-- window), through `wt pane agents`. Do not use focus, pane labels, screen
-- contents, or agents in other tabs as recipients.
local M = {}
local here = vim.fn.fnamemodify(debug.getinfo(1, 'S').source:sub(2), ':p:h')

local function wt_pane() return vim.g.wt_pane or vim.fn.fnamemodify(here .. '/../../../bin/wt-pane', ':p') end

local function pane(args, callback)
  local ok, err = pcall(vim.system, vim.list_extend({ wt_pane() }, args), { text = true, timeout = 15000 }, function(r)
    vim.schedule(function()
      if r.code ~= 0 then
        local msg = vim.trim(r.stderr or ''):gsub('^wt pane: ', '')
        return callback(msg ~= '' and msg or 'wt pane failed')
      end
      local decoded, result = pcall(vim.json.decode, r.stdout or '')
      if not decoded or type(result) ~= 'table' then return callback('Invalid response from wt pane') end
      callback(nil, result)
    end)
  end)
  if not ok then callback(tostring(err)) end
end

local function value(v) if v ~= vim.NIL then return v end end

local function candidate(info)
  local agent = value(info.agent)
  if type(info.ref) ~= 'string' or not agent then return end
  return { ref = info.ref, agent = agent, pid = value(info.pid), cwd = value(info.cwd), label = value(info.label) }
end

function M.find(callback)
  -- `agents` with no ref resolves nvim's own pane, not whichever pane has focus.
  pane({ 'agents' }, function(err, list)
    if err then return callback(err) end
    local candidates = {}
    for _, info in ipairs(list) do
      local c = type(info) == 'table' and candidate(info)
      if c then table.insert(candidates, c) end
    end
    table.sort(candidates, function(a, b) return a.ref < b.ref end)
    callback(nil, candidates)
  end)
end

function M.resolve(preferred, callback)
  M.find(function(err, candidates)
    if err then return callback(err) end
    if preferred then
      for _, c in ipairs(candidates) do
        if c.ref == preferred then return callback(nil, c) end
      end
    end
    if #candidates == 0 then return callback('No running agent found in this nvim tab; comments remain saved') end
    if #candidates == 1 then return callback(nil, candidates[1]) end
    vim.ui.select(candidates, {
      prompt = 'Send comments to which agent in this tab?',
      format_item = function(c)
        return string.format('%s · %s · %s', c.agent, c.cwd or c.label or '', c.ref)
      end,
    }, function(choice)
      if not choice then return callback('Reconnect cancelled; comments remain saved') end
      -- A picker can stay open while the selected agent exits. Check it again
      -- before sending input, so a shell that replaced it cannot receive it.
      pane({ 'info', choice.ref }, function(check_err, info)
        if check_err then return callback(check_err) end
        if value(info.agent) ~= choice.agent or (choice.pid and value(info.pid) ~= choice.pid) then
          return callback('Selected agent changed; reconnect again. Comments remain saved')
        end
        callback(nil, choice)
      end)
    end)
  end)
end

-- The program status of the pane at ref (working, done, ...), or nil when
-- there's none to read (tmux, or an error).
function M.status(ref, callback)
  local ok = pcall(vim.system, { wt_pane(), 'status', ref }, { text = true, timeout = 5000 }, function(r)
    vim.schedule(function() callback(r.code == 0 and vim.trim(r.stdout or '') or nil) end)
  end)
  if not ok then callback(nil) end
end

return M
