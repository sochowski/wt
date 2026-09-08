#!/usr/bin/env bash
# Real renderer checks. Supply a disposable COPY of installed plugins, not a
# production runtimepath. This script itself must run in a disposable source copy.
set -euo pipefail
repo=$(cd "$(dirname "$0")" && pwd)
: "${WT_TEST_PLUGINS:?set to disposable copied plugins directory}"
plugins=$WT_TEST_PLUGINS
diffview_probe=${WT_TEST_DIFFVIEW_PROBE:-0}
priv=$(mktemp -d)
echo "Managed diff evidence: $priv"
for inherited in ${!WT_@}; do unset "$inherited"; done
unset TMUX TMUX_PANE
export HOME="$priv/home" XDG_CONFIG_HOME="$priv/config" XDG_DATA_HOME="$priv/data" XDG_STATE_HOME="$priv/state" XDG_CACHE_HOME="$priv/cache"
export WT_VIEW_ROOT="$priv/target" WT_VIEW_STATE="$priv/view.json" WT_VIEW_MODULE="$repo/config/wt-view.lua" WT_TEST_PLUGINS="$plugins"
mkdir -p "$HOME" "$WT_VIEW_ROOT"
git -C "$WT_VIEW_ROOT" init -q
git -C "$WT_VIEW_ROOT" -c user.name=Test -c user.email=test@example.com commit --allow-empty -qm initial
# Fixture index creation is setup only; no index writes occur during the views.
seq 1 100 > "$WT_VIEW_ROOT/tracked.txt"
printf 'delete me\n' > "$WT_VIEW_ROOT/deleted.txt"
git -C "$WT_VIEW_ROOT" add .
git -C "$WT_VIEW_ROOT" -c user.name=Test -c user.email=test@example.com commit -qm baseline
export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=wt.fixture GIT_CONFIG_VALUE_0=retained
base=$(git -C "$WT_VIEW_ROOT" rev-parse HEAD)
sha256sum "$WT_VIEW_ROOT/.git/index" > "$priv/index.before"
sha256sum "$WT_VIEW_ROOT/.git/config" > "$priv/config.before"
git -C "$WT_VIEW_ROOT" show-ref > "$priv/refs.before"
python3 - "$WT_VIEW_ROOT/KJV.txt" <<'PYFIXTURE'
import sys
with open(sys.argv[1], 'w') as f:
    for i in range(31102): f.write(f'{i+1}: ' + 'KJV-sized disposable verse content. ' * 5 + '\n')
PYFIXTURE
jq -n --arg root "$WT_VIEW_ROOT" --arg base "$base" '{kind:"diff",root:$root,state:{version:1,restart:"never",base:$base}}' > "$WT_VIEW_STATE"
cat > "$priv/check.lua" <<'LUA'
local api = vim.api
vim.o.lines, vim.o.columns = 60, 140
vim.opt.rtp:append(vim.env.WT_TEST_PLUGINS .. '/unified.nvim')
vim.opt.rtp:append(vim.env.WT_TEST_PLUGINS .. '/nvim-web-devicons')
vim.cmd('filetype plugin on')
dofile(vim.env.WT_VIEW_MODULE)
api.nvim_create_autocmd('VimEnter', { once = true, callback = function() vim.schedule(function()
local ok, err = pcall(function()
  local ts = require('unified.file_tree.state')
  local function node(file)
    for line, n in pairs(ts.line_to_node) do if n.path == vim.env.WT_VIEW_ROOT .. '/' .. file then return n, line end end
  end
  local function wait(f, message) assert(vim.wait(6000, f, 30), message) end
  wait(function() return node('KJV.txt') end, 'startup untracked missing')
  assert(vim.fn.getcwd() == vim.env.WT_VIEW_ROOT)
  assert(vim.env.GIT_CONFIG_KEY_0 == 'wt.fixture' and vim.env.GIT_CONFIG_VALUE_0 == 'retained')
  assert(tonumber(vim.env.GIT_CONFIG_COUNT) == 2)
  local function select(file)
    local n, line = node(file)
    assert(n, 'missing node ' .. file)
    api.nvim_set_current_win(ts.window)
    api.nvim_win_set_cursor(ts.window, { line + 1, 0 })
    api.nvim_feedkeys('l', 'xt', false)
    return require('unified.state').get_main_window()
  end
  local started = vim.uv.hrtime()
  local win = select('KJV.txt')
  print(string.format('KJV-sized initial selection: %.1f ms', (vim.uv.hrtime()-started)/1e6))
  local buf = api.nvim_win_get_buf(win)
  assert(#api.nvim_buf_get_extmarks(buf, require('unified.config').ns_id, 0, -1, {}) > 0, 'untracked inline diff missing')
  api.nvim_win_call(win, function() vim.fn.winrestview({lnum=55,col=0,topline=45}) end)
  local view = api.nvim_win_call(win, vim.fn.winsaveview)
  local focus = api.nvim_get_current_win()
  vim.fn.writefile({'new'}, vim.env.WT_VIEW_ROOT .. '/after.txt')
  wait(function() return node('after.txt') end, 'external create missing')
  assert(api.nvim_get_current_win() == focus, 'refresh stole focus')
  assert(ts.line_to_node[api.nvim_win_get_cursor(ts.window)[1]-1].path:match('/KJV.txt$'), 'tree selection moved')
  assert(vim.deep_equal(view, api.nvim_win_call(win, vim.fn.winsaveview)), 'reading position reset')
  local lines = vim.fn.readfile(vim.env.WT_VIEW_ROOT .. '/KJV.txt'); lines[1] = 'external'
  vim.fn.writefile(lines, vim.env.WT_VIEW_ROOT .. '/KJV.txt')
  wait(function() return api.nvim_buf_get_lines(buf,0,1,false)[1] == 'external' end, 'external content stale')
  api.nvim_buf_set_lines(buf,0,1,false,{'unsaved'})
  lines[1] = 'another external'; vim.fn.writefile(lines, vim.env.WT_VIEW_ROOT .. '/KJV.txt')
  vim.wait(2200, function() return false end)
  assert(vim.bo[buf].modified and api.nvim_buf_get_lines(buf,0,1,false)[1] == 'unsaved', 'unsaved edits overwritten')
  local saved = vim.json.decode(require('wt_view').snapshot())
  assert(saved.diff.file == 'KJV.txt' and saved.diff.line == 55, vim.inspect(saved.diff))
  vim.fn.writefile({vim.json.encode({kind='diff',root=vim.env.WT_VIEW_ROOT,state=saved})}, vim.env.WT_VIEW_STATE)
  for _, name in ipairs({'with space.txt', 'with\nnewline.txt', 'literal\\n.txt', 'binary.dat'}) do
    local f = assert(io.open(vim.env.WT_VIEW_ROOT .. '/' .. name, 'wb')); f:write('binary\0data'); f:close()
  end
  wait(function() return node('with space.txt') and node('with\nnewline.txt') and node('binary.dat') end, 'special paths missing')
  for _, name in ipairs({'with space.txt', 'literal\\n.txt'}) do
    local selected = select(name)
    assert(api.nvim_buf_get_name(api.nvim_win_get_buf(selected)) == vim.env.WT_VIEW_ROOT .. '/' .. name, 'wrong raw path ' .. name)
  end
  local special = select('with\nnewline.txt')
  assert(api.nvim_buf_get_name(api.nvim_win_get_buf(special)) == vim.env.WT_VIEW_ROOT .. '/with\nnewline.txt', 'plugin opened wrong raw newline path')
  assert(not pcall(require('wt_view').snapshot), 'newline recovery must fail closed')
  require('wt_view').refresh()
  vim.wait(600, function() return false end)
  assert(api.nvim_buf_get_name(api.nvim_win_get_buf(special)) == vim.env.WT_VIEW_ROOT .. '/with\nnewline.txt', 'refresh changed raw selected path')
  -- Tracked edit, checkout rename (delete + untracked add), deletion, clean tree.
  vim.fn.writefile({'changed'}, vim.env.WT_VIEW_ROOT .. '/tracked.txt')
  vim.fn.delete(vim.env.WT_VIEW_ROOT .. '/deleted.txt')
  wait(function() return node('tracked.txt') and node('deleted.txt') end, 'tracked change/delete absent')
  local deleted = select('deleted.txt')
  assert(vim.b[api.nvim_win_get_buf(deleted)].unified_deleted_view, 'deleted baseline not rendered')
  vim.uv.fs_rename(vim.env.WT_VIEW_ROOT .. '/tracked.txt', vim.env.WT_VIEW_ROOT .. '/renamed.txt')
  wait(function() return node('renamed.txt') end, 'rename absent')
  vim.fn.writefile(vim.fn.systemlist({'git','-C',vim.env.WT_VIEW_ROOT,'show',saved.base .. ':tracked.txt'}), vim.env.WT_VIEW_ROOT .. '/tracked.txt')
  vim.fn.writefile({'delete me'}, vim.env.WT_VIEW_ROOT .. '/deleted.txt')
  for _, f in ipairs({'KJV.txt','after.txt','renamed.txt','with space.txt','with\nnewline.txt','literal\\n.txt','binary.dat'}) do vim.fn.delete(vim.env.WT_VIEW_ROOT .. '/' .. f) end
  wait(function() return not node('tracked.txt') and not node('deleted.txt') and not node('KJV.txt') end, 'clean tree stale')
  -- Close with an in-flight tree refresh. Late callbacks cannot paint or reopen.
  local treebuf = ts.buffer
  local renderer = require('wt_view')
  renderer.refresh()
  api.nvim_set_current_win(ts.window)
  api.nvim_feedkeys('q', 'xt', false)
  vim.fn.writefile({'late'}, vim.env.WT_VIEW_ROOT .. '/late.txt')
  local windows = #api.nvim_list_wins()
  vim.wait(1800, function() return false end)
  assert(#api.nvim_list_wins() == windows and not api.nvim_buf_is_valid(treebuf), 'refresh after close')
end)
if not ok then print(err); vim.cmd('cquit 1') else print('PASS managed Unified startup/external refresh/position/unsaved/tracked/delete/rename/clean/close'); vim.cmd('qa!') end
end) end })
LUA
nvim --headless -u NONE -i NONE -c "lua dofile('$priv/check.lua')" > "$priv/nvim.log" 2>&1 || { cat "$priv/nvim.log"; exit 1; }
cat "$priv/nvim.log"
# Reconstruct the saved selected file + cursor in a fresh Neovim.
seq 1 100 > "$WT_VIEW_ROOT/KJV.txt"
cat > "$priv/recover.lua" <<'LUA'
vim.opt.rtp:append(vim.env.WT_TEST_PLUGINS .. '/unified.nvim')
vim.opt.rtp:append(vim.env.WT_TEST_PLUGINS .. '/nvim-web-devicons')
vim.cmd('filetype plugin on')
dofile(vim.env.WT_VIEW_MODULE)
vim.api.nvim_create_autocmd('VimEnter', {once=true, callback=function() vim.schedule(function()
local ok,err=pcall(function()
 local saved=vim.json.decode(require('wt_view').snapshot())
 assert(saved.diff.file=='KJV.txt' and saved.diff.line==55, vim.inspect(saved))
end)
if not ok then print(err); vim.cmd('cquit 1') else print('PASS selected-file recovery'); vim.cmd('qa!') end
end) end})
LUA
nvim --headless -u NONE -i NONE -c "lua dofile('$priv/recover.lua')" > "$priv/recover.log" 2>&1 || { cat "$priv/recover.log"; exit 1; }
cat "$priv/recover.log"
cat > "$priv/error.lua" <<'LUA'
vim.cmd('filetype plugin on')
dofile(vim.env.WT_VIEW_MODULE)
vim.api.nvim_create_autocmd('VimEnter',{once=true,callback=function() vim.schedule(function()
local text=table.concat(vim.api.nvim_buf_get_lines(0,0,-1,false),'\n')
if not text:find('WT diff unavailable',1,true) then print(text); vim.cmd('cquit 1') else print('PASS visible error: '..text); vim.cmd('qa!') end
end) end})
LUA
nvim --headless -u NONE -i NONE -c "lua dofile('$priv/error.lua')" > "$priv/missing.log" 2>&1 || { cat "$priv/missing.log"; exit 1; }
jq '.state.base="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"' "$WT_VIEW_STATE" > "$priv/invalid.json"; mv "$priv/invalid.json" "$WT_VIEW_STATE"
nvim --headless -u NONE -i NONE -c "lua dofile('$priv/error.lua')" > "$priv/base.log" 2>&1 || { cat "$priv/base.log"; exit 1; }
sha256sum "$WT_VIEW_ROOT/.git/index" > "$priv/index.after"
git -C "$WT_VIEW_ROOT" show-ref > "$priv/refs.after"
cmp "$priv/index.before" "$priv/index.after"
cmp "$priv/refs.before" "$priv/refs.after"
sha256sum "$WT_VIEW_ROOT/.git/config" > "$priv/config.after"
cmp "$priv/config.before" "$priv/config.after"
echo 'PASS index and refs unchanged; missing plugin/base visible'

# Separate staged-then-restored fixture: setup writes its own index before the
# view starts. A working file equal to base must retain its genuine staged entry.
printf 'staged content\n' > "$WT_VIEW_ROOT/tracked.txt"
git -C "$WT_VIEW_ROOT" add tracked.txt
git -C "$WT_VIEW_ROOT" show "$base:tracked.txt" > "$WT_VIEW_ROOT/tracked.txt"
sha256sum "$WT_VIEW_ROOT/.git/index" > "$priv/staged-index.before"
jq -n --arg root "$WT_VIEW_ROOT" --arg base "$base" '{kind:"diff",root:$root,state:{version:1,restart:"never",base:$base}}' > "$WT_VIEW_STATE"
cat > "$priv/staged.lua" <<'LUA'
vim.opt.rtp:append(vim.env.WT_TEST_PLUGINS .. '/unified.nvim')
vim.opt.rtp:append(vim.env.WT_TEST_PLUGINS .. '/nvim-web-devicons')
vim.cmd('filetype plugin on')
dofile(vim.env.WT_VIEW_MODULE)
vim.api.nvim_create_autocmd('VimEnter', {once=true,callback=function() vim.schedule(function()
local ok,err=pcall(function()
 vim.wait(2000,function() return false end)
 local found=false
 for _,node in pairs(require('unified.file_tree.state').line_to_node) do
  if node.path == vim.env.WT_VIEW_ROOT .. '/tracked.txt' then found=true; assert(node.status:find('M')) end
 end
 assert(found,'staged then restored path incorrectly filtered')
end)
if not ok then print(err); vim.cmd('cquit 1') else print('PASS staged-then-restored preserved'); vim.cmd('qa!') end
end) end})
LUA
nvim --headless -u NONE -i NONE -c "lua dofile('$priv/staged.lua')" > "$priv/staged.log" 2>&1 || { cat "$priv/staged.log"; exit 1; }
cat "$priv/staged.log"
sha256sum "$WT_VIEW_ROOT/.git/index" > "$priv/staged-index.after"
cmp "$priv/staged-index.before" "$priv/staged-index.after"
# Known-failing compatibility probe, not part of Unified acceptance. Keep the
# runtime defect executable and visible; do not silently turn it into a pass.
if [[ "$diffview_probe" != 1 ]]; then
  echo 'Diffview compatibility probe not run (WT_TEST_DIFFVIEW_PROBE=1; known refresh limitation)'
  exit 0
fi
# Retain the opt-in side-by-side renderer, without old tmux window management.
jq '.state.diff={file:"KJV.txt",line:55,column:0,topline:45,leftcol:0}' "$WT_VIEW_STATE" > "$priv/diffview.json"; mv "$priv/diffview.json" "$WT_VIEW_STATE"
cat > "$priv/diffview.lua" <<'LUA'
for _,p in ipairs({'diffview.nvim','plenary.nvim','nvim-web-devicons'}) do vim.opt.rtp:append(vim.env.WT_TEST_PLUGINS .. '/' .. p) end
vim.cmd('runtime plugin/diffview.lua')
dofile(vim.env.WT_VIEW_MODULE)
vim.api.nvim_create_autocmd('VimEnter',{once=true,callback=function() vim.schedule(function()
local ok,err=pcall(function()
 assert(vim.wait(6000,function()
  local ok,s=pcall(require('wt_view').snapshot)
  if not ok then return false end
  local pos=vim.json.decode(s).diff
  return pos and pos.file=='KJV.txt' and pos.line==55
 end,50),'Diffview selected-file/position recovery failed')
 local view=require('diffview.lib').get_current_view()
 local focus=vim.api.nvim_get_current_win()
 vim.fn.writefile({'external'},vim.env.WT_VIEW_ROOT .. '/diffview-after.txt')
 assert(vim.wait(6000,function()
  for _,f in view.files:iter() do if f.path=='diffview-after.txt' then return true end end
 end,50),'Diffview external create stale')
 assert(vim.api.nvim_get_current_win()==focus,'Diffview refresh stole focus')
 vim.cmd('DiffviewClose')
 vim.wait(1200,function() return false end)
 assert(require('diffview.lib').get_current_view()==nil,'Diffview reopened after close')
end)
if not ok then print(err); vim.cmd('cquit 1') else print('PASS Diffview preference/recovery/external refresh/close'); vim.cmd('qa!') end
end) end})
LUA
WT_DIFF_TOOL=diffview nvim --headless -u NONE -i NONE -c "lua dofile('$priv/diffview.lua')" > "$priv/diffview.log" 2>&1 || { cat "$priv/diffview.log"; exit 1; }
cat "$priv/diffview.log"
sha256sum "$WT_VIEW_ROOT/.git/index" > "$priv/diffview-index.after"
cmp "$priv/staged-index.before" "$priv/diffview-index.after"
