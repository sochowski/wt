-- Find existing coding-agent terminals in nvim's own Rex tab. Do not use
-- focus, pane labels, screen contents, or agents in other tabs as recipients.
local M = {}
local names = { claude = true, codex = true, opencode = true, pi = true }

local function agent(process)
  local p = process.foreground
  if type(p) ~= 'table' then return end
  for _, key in ipairs({ 'name', 'argv0', 'path', 'invoked_path', 'script' }) do
    local name = (p[key] or ''):match('([^/]+)$')
    if name and names[name] then return name end
  end
  local script = p.script or ''
  if script:find('/@anthropic-ai/claude-code/', 1, true) then return 'claude' end
  if script:find('/@openai/codex/', 1, true) then return 'codex' end
  if script:find('/pi-coding-agent/', 1, true) then return 'pi' end
end

local function rex(args, callback)
  local ok, err = pcall(vim.system, vim.list_extend({ 'rex' }, args), { text = true, timeout = 15000 }, function(r)
    vim.schedule(function()
      if r.code ~= 0 then return callback(vim.trim(r.stderr or 'Rex request failed')) end
      local decoded, result = pcall(vim.json.decode, r.stdout or '')
      if not decoded or type(result) ~= 'table' then return callback('Invalid response from Rex') end
      callback(nil, result)
    end)
  end)
  if not ok then callback(tostring(err)) end
end

function M.find(callback)
  -- whoami resolves this process's pane, not whichever pane the user focuses.
  rex({ 'whoami', '--json' }, function(err, here)
    if err then return callback(err) end
    if not here.block_id or not here.session_id or not here.window_id then
      return callback('Cannot identify this nvim pane in Rex')
    end
    rex({ 'block', 'ls', '-s', here.session_id, '-w', here.window_id, '--json' }, function(list_err, result)
      if list_err then return callback(list_err) end
      if type(result.blocks) ~= 'table' then return callback('Invalid pane list from Rex') end
      local panes = {}
      for _, pane in ipairs(result.blocks) do
        if pane.block_id ~= here.block_id and pane.window_id == here.window_id
          and pane.creator_name == 'com.superlogical.terminal' then
          table.insert(panes, pane)
        end
      end
      if #panes == 0 then return callback(nil, {}) end
      local remaining, candidates, errors = #panes, {}, {}
      for _, pane in ipairs(panes) do
        rex({ 'block', 'call', '-s', here.session_id, '-b', pane.block_id, 'process' }, function(process_err, process)
          if process_err then
            table.insert(errors, process_err)
          else
            local name = agent(process)
            if name then
              table.insert(candidates, { block = pane.block_id, session = here.session_id,
                agent = name, pid = process.foreground.pid, cwd = process.foreground.cwd, label = pane.label })
            end
          end
          remaining = remaining - 1
          if remaining == 0 then
            -- Failed inspections must not turn an ambiguous tab into a guessed
            -- sole recipient. Let the user retry once the layout settles.
            if #errors > 0 then return callback('Could not inspect every pane in this tab: ' .. errors[1]) end
            table.sort(candidates, function(a, b) return a.block < b.block end)
            callback(nil, candidates)
          end
        end)
      end
    end)
  end)
end

function M.resolve(preferred, callback)
  M.find(function(err, candidates)
    if err then return callback(err) end
    if preferred then
      for _, candidate in ipairs(candidates) do
        if candidate.block == preferred.block and (not preferred.session or candidate.session == preferred.session) then
          return callback(nil, candidate)
        end
      end
    end
    if #candidates == 0 then return callback('No running agent found in this nvim tab; comments remain saved') end
    if #candidates == 1 then return callback(nil, candidates[1]) end
    vim.ui.select(candidates, {
      prompt = 'Send comments to which agent in this tab?',
      format_item = function(c)
        return string.format('%s · %s · %s', c.agent, c.cwd or c.label or '', c.block)
      end,
    }, function(choice)
      if not choice then return callback('Reconnect cancelled; comments remain saved') end
      -- A picker can stay open while the selected agent exits. Check it again
      -- before sending input, so a shell that replaced it cannot receive it.
      rex({ 'block', 'call', '-s', choice.session, '-b', choice.block, 'process' }, function(check_err, process)
        if check_err then return callback(check_err) end
        if agent(process) ~= choice.agent or (choice.pid and process.foreground.pid ~= choice.pid) then
          return callback('Selected agent changed; reconnect again. Comments remain saved')
        end
        callback(nil, choice)
      end)
    end)
  end)
end

return M
