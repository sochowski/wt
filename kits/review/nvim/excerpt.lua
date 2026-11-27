-- Diff excerpts for slides: a `Diff:` line in a deck becomes the real lines
-- of one file's change, drawn the way unified.nvim draws a diff. The new code
-- is a fenced block in the file's language, so it keeps its highlighting;
-- added lines get an add background, removed lines sit inline above where
-- they were, and the agent's notes sit above the lines they're about.
--
-- The lines come from the PR's diff, and from the file at the PR's head when
-- that's readable, so an excerpt can't drift from what the PR changes.

local M = {}
M.max = 80 -- lines of code in one excerpt before it's cut short
M.window = 40 -- lines shown around a line inside a longer hunk

-- `Diff: #4512 repos/Runlayer/path.py:88-120`, `…:88`, `Diff: path`, with or
-- without the PR, optionally bold or in backticks. Nil for any other line.
function M.directive(line)
  local d = line:match('^%s*[%*_]*Diff[%*_]*:[%*_]*%s+(.*)$')
  if not d then return end
  d = d:gsub('[`%*]', '')
  local pr, ref = d:match('^#(%d+)%s*(%S*)')
  if not pr then pr, ref = d:match('^(%d+)%s+(%S*)') end
  if not pr then pr, ref = d:match('^(%d+)%s*$'), '' end
  if not pr then ref = d:match('^(%S+)') end
  ref = ref or ''
  local path, from, to = ref:match('^(.-):(%d+)%-(%d+)$')
  if not path then path, from = ref:match('^(.-):(%d+)$') end
  if not path then path = ref ~= '' and ref or nil end
  return { pr = pr, path = path, from = tonumber(from), to = tonumber(to) }
end

-- One file's part of a unified diff: its hunks as rows of
-- { kind = ' ' | '+' | '-', text, new = line on the new side (not for '-') }.
function M.file(diff, path)
  local hunks, h, on, new = {}, nil, false, nil
  for _, l in ipairs(diff) do
    local b = l:match('^diff %-%-git a/.- b/(.*)$')
    if b then
      if on then break end
      on = b == path
    elseif on then
      local n = l:match('^@@ %-%d+,?%d* %+(%d+),?%d* @@')
      if n then
        new, h = tonumber(n), {}
        table.insert(hunks, h)
      elseif h then
        local c = l:sub(1, 1)
        if c == '+' then
          table.insert(h, { kind = '+', text = l:sub(2), new = new }); new = new + 1
        elseif c == '-' then
          table.insert(h, { kind = '-', text = l:sub(2) })
        elseif c == ' ' or l == '' then
          table.insert(h, { kind = ' ', text = l:sub(2), new = new }); new = new + 1
        end
      end
    end
  end
  return on and hunks or nil
end

-- The new-side span of a hunk.
local function span(h)
  local first, last
  for _, r in ipairs(h) do
    if r.new then first = first or r.new; last = r.new end
  end
  return first, last
end

-- Build an excerpt. o: diff (the PR's diff lines), path (repo-relative), from,
-- to (new-side lines; either may be missing), head (the file at the PR's head,
-- as lines, or nil), notes ({ line, text } rows), lang (fence language).
-- Returns { lines, marks, at } or nil and an error:
--   lines  markdown lines: a fence, the code, a closing fence
--   marks  { row, add = true } | { row, removed = { text… }, below = bool }
--          | { row, note = text } | { row, number = n }   (rows 0-based)
--   at     row (0-based) -> new-side line, for gf
function M.build(o)
  local hunks = M.file(o.diff, o.path)
  if not hunks then return nil, o.path .. ' is not in this diff' end
  if #hunks == 0 then return nil, o.path .. ' has no line changes (renamed, binary or mode only)' end

  -- Which lines: the range given; else the hunk around a line; else every hunk.
  local from, to = o.from, o.to
  if from and not to then
    -- The hunk around the line, else the nearest one: a deck's line numbers
    -- can come from higher in the stack, where other PRs have moved them.
    local best, dist
    for _, h in ipairs(hunks) do
      local a, b = span(h)
      if a then
        local d = from < a and a - from or (from > b and from - b or 0)
        if not dist or d < dist then best, dist = { a, b }, d end
      end
    end
    if best then
      local line = from
      from, to = best[1], best[2]
      -- A long hunk (a new file is one) shows a window around the line.
      if to - from + 1 > M.window and line >= from and line <= to then
        from = math.max(from, line - math.floor(M.window / 4))
        to = math.min(to, from + M.window - 1)
      end
    end
  elseif not from then
    from = span(hunks[1])
    local _, b = span(hunks[#hunks])
    to = b
  end
  if not from or not to then return nil, o.path .. ': nothing on the new side to show' end
  if to < from then from, to = to, from end

  -- What's known about each new-side line, and what was removed before it.
  local text, added, removed = {}, {}, {}
  for _, h in ipairs(hunks) do
    local pending = {}
    for _, r in ipairs(h) do
      if r.kind == '-' then
        table.insert(pending, r.text)
      else
        text[r.new] = r.text
        if r.kind == '+' then added[r.new] = true end
        if #pending > 0 then removed[r.new] = pending; pending = {} end
      end
    end
    -- Removed at the end of a hunk: they sat before the line after it.
    if #pending > 0 then
      local _, last = span(h)
      local at = (last or 0) + 1
      removed[at] = vim.list_extend(removed[at] or {}, pending)
    end
  end
  if o.head then
    for n, l in ipairs(o.head) do text[n] = l end
  end

  local lines, marks, at = { '````' .. (o.lang or '') }, {}, {}
  local cut = to - from + 1 > M.max
  if cut then to = from + M.max - 1 end
  local gap = false
  for n = from, to do
    local row = #lines
    if text[n] then
      gap = false
      table.insert(lines, text[n])
      at[row] = n
      table.insert(marks, { row = row, number = n })
      if added[n] then table.insert(marks, { row = row, add = true }) end
      if removed[n] then table.insert(marks, { row = row, removed = removed[n] }) end
      for _, note in ipairs(o.notes or {}) do
        if note[1] == n then table.insert(marks, { row = row, note = note[2] }) end
      end
    elseif not gap then
      -- Outside every hunk, with no head to read it from.
      gap = true
      table.insert(lines, '⋯')
      if removed[n] then table.insert(marks, { row = row, removed = removed[n] }) end
    end
  end
  -- Lines removed after the last line shown.
  if removed[to + 1] and #lines > 1 then
    table.insert(marks, { row = #lines - 1, removed = removed[to + 1], below = true })
  end
  if cut then
    table.insert(lines, string.format('⋯ cut at %d lines; give the Diff: line a narrower range', M.max))
  end
  table.insert(lines, '````')
  return { lines = lines, marks = marks, at = at, from = from, to = to }
end

return M
