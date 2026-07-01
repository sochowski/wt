-- wt-diff.lua — the live diff view for wt-managed nvim sessions.
--
-- Sourced only by nvim instances that wt launches, via:
--   nvim --listen <sock> -c 'luafile <this file>'
-- with WT_DIFF_BASE set to the merge-base of the branch against origin/HEAD,
-- and WT_DIFF_TOOL set to the plugin to use ("unified" or "diffview").
--
-- On startup it opens the working tree vs that base (the full "what has this
-- branch/agent done" diff, including uncommitted edits). It registers as the
-- `wt_diff` module so the watcher (bin/wt-diff-watch) can poke it over nvim's
-- RPC socket with:
--   nvim --server <sock> --remote-expr 'luaeval("...require([[wt_diff]]).refresh()...")'
--
-- Everything degrades gracefully: if the chosen plugin isn't installed, nvim
-- just opens as a normal editor and refresh() is a no-op.

local M = {}

-- Which plugin to drive. Defaults to unified.nvim; set WT_DIFF_TOOL=diffview
-- for the side-by-side view.
local function tool()
  local t = vim.env.WT_DIFF_TOOL
  return (t and t ~= '') and t or 'unified'
end

local function base_ref()
  local b = vim.env.WT_DIFF_BASE
  return (b and b ~= '') and b or nil
end

local function has(mod)
  return pcall(require, mod)
end

-- Open the branch diff: working tree vs `base` (defaults to $WT_DIFF_BASE).
function M.open(base)
  base = base or base_ref()
  if not base then return end
  if tool() == 'diffview' then
    if not has('diffview') then return end
    pcall(vim.cmd, 'DiffviewOpen ' .. base)
  else
    if not has('unified') then return end
    -- `:Unified <ref>` diffs the working tree against <ref> (inline, with a
    -- file-tree side panel), matching DiffviewOpen's semantics.
    pcall(vim.cmd, 'Unified ' .. base)
  end
end

-- Refresh the open diff view (no-op if none is open). Called on file changes.
-- checktime first so buffers reflect the agent's on-disk edits, then re-run the
-- diff against the same base.
function M.refresh()
  vim.schedule(function()
    pcall(vim.cmd, 'checktime')
    if tool() == 'diffview' then
      pcall(vim.cmd, 'DiffviewRefresh')
    else
      local base = base_ref()
      if base then pcall(vim.cmd, 'Unified ' .. base) end
    end
  end)
end

-- Make `require('wt_diff')` resolve to this module even though we were loaded
-- via :luafile rather than off the runtimepath.
package.loaded['wt_diff'] = M

-- Open once nvim has finished starting. `-c` commands can run before or after
-- VimEnter depending on startup ordering, so guard on vim_did_enter.
if vim.v.vim_did_enter == 1 then
  M.open()
else
  vim.api.nvim_create_autocmd('VimEnter', {
    once = true,
    callback = function() M.open() end,
  })
end

return M
