-- wt-diff.lua — the live diff view for wt-managed nvim sessions.
--
-- Sourced only by nvim instances that wt launches, via:
--   nvim --listen <sock> -c 'luafile <this file>'
-- with WT_DIFF_BASE set to the merge-base of the branch against origin/HEAD.
--
-- On startup it opens diffview.nvim showing the working tree vs that base (the
-- full "what has this branch/agent done" diff, including uncommitted edits).
-- It registers as the `wt_diff` module so the watcher (bin/wt-diff-watch) can
-- poke it over nvim's RPC socket with:
--   nvim --server <sock> --remote-expr 'luaeval("...require([[wt_diff]]).refresh()...")'
--
-- Everything degrades gracefully: if diffview.nvim isn't installed, nvim just
-- opens as a normal editor and refresh() is a no-op.

local M = {}

local function has_diffview()
  return pcall(require, 'diffview')
end

-- Open the branch diff: working tree vs `base` (defaults to $WT_DIFF_BASE).
function M.open(base)
  if not has_diffview() then return end
  base = base or vim.env.WT_DIFF_BASE
  if not base or base == '' then return end
  pcall(vim.cmd, 'DiffviewOpen ' .. base)
end

-- Refresh the open diff view (no-op if none is open). Called on file changes.
function M.refresh()
  vim.schedule(function()
    pcall(vim.cmd, 'checktime')
    pcall(vim.cmd, 'DiffviewRefresh')
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
