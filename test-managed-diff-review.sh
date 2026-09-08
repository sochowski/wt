#!/usr/bin/env bash
# Run from a disposable source copy, with copied plugins only.
set -euo pipefail
repo=$(cd "$(dirname "$0")" && pwd)
: "${WT_TEST_PLUGINS:?provide disposable copied plugins}"
plugins=$WT_TEST_PLUGINS
priv=$(mktemp -d)
echo "Managed diff review evidence: $priv"
for inherited in ${!WT_@}; do unset "$inherited"; done
unset TMUX TMUX_PANE GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_NO_LAZY_FETCH
export HOME="$priv/home" XDG_CONFIG_HOME="$priv/config" XDG_DATA_HOME="$priv/data" XDG_STATE_HOME="$priv/state" XDG_CACHE_HOME="$priv/cache"
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_COUNT=0
export WT_VIEW_ROOT="$priv/target" WT_VIEW_STATE="$priv/view.json" WT_VIEW_MODULE="$repo/config/wt-view.lua" WT_TEST_PLUGINS="$plugins"
mkdir -p "$HOME" "$WT_VIEW_ROOT"
git -C "$WT_VIEW_ROOT" init -q
printf 'old\n' > "$WT_VIEW_ROOT/A.txt"
printf 'deleted baseline\n' > "$WT_VIEW_ROOT/D.txt"
printf 'identical\n' > "$WT_VIEW_ROOT/same.txt"
git -C "$WT_VIEW_ROOT" add .
git -C "$WT_VIEW_ROOT" -c user.name=Test -c user.email=test@example.com commit -qm baseline
base=$(git -C "$WT_VIEW_ROOT" rev-parse HEAD)
printf 'changed\n' > "$WT_VIEW_ROOT/A.txt"
printf 'changed deletion\n' > "$WT_VIEW_ROOT/D.txt"
printf 'new\n' > "$WT_VIEW_ROOT/B.txt"
printf 'special\n' > "$WT_VIEW_ROOT/"$'before\nstartup.txt'
touch "$WT_VIEW_ROOT/same.txt"
sha256sum "$WT_VIEW_ROOT/.git/index" > "$priv/index.before"
jq -n --arg root "$WT_VIEW_ROOT" --arg base "$base" '{kind:"diff",root:$root,state:{version:1,base:$base}}' > "$WT_VIEW_STATE"
cat > "$priv/review.lua" <<'LUA'
local api = vim.api
vim.opt.rtp:append(vim.env.WT_TEST_PLUGINS .. '/unified.nvim')
vim.opt.rtp:append(vim.env.WT_TEST_PLUGINS .. '/nvim-web-devicons')
vim.cmd('filetype plugin on')
-- Fixture-only delay: real plugin Git queries run, but hold its first completion
-- until WT has rendered and the reader has selected a file.
local tree = require('unified.file_tree.tree')
local update, first, release = tree.update_git_status, true, nil
tree.update_git_status = function(self, root, only, base, callback)
 if first then
  first = false
  return update(self, root, only, base, function(ok) release = function() callback(ok) end end)
 end
 return update(self, root, only, base, callback)
end
local notices = {}
vim.notify = function(text) notices[#notices+1] = tostring(text) end
dofile(vim.env.WT_VIEW_MODULE)
api.nvim_create_autocmd('VimEnter', {once=true, callback=function() vim.schedule(function()
 local ok,err = pcall(function()
  local ts = require('unified.file_tree.state')
  local function node(file)
   for line,n in pairs(ts.line_to_node) do if n.path==vim.env.WT_VIEW_ROOT..'/'..file then return n,line end end
  end
  local function wait(fn,msg) assert(vim.wait(6000,fn,30),msg) end
  local function select(file)
   local n,line=node(file); assert(n,'missing '..file)
   api.nvim_set_current_win(ts.window); api.nvim_win_set_cursor(ts.window,{line+1,0})
   api.nvim_feedkeys('l','xt',false)
   local win=require('unified.state').get_main_window()
   return api.nvim_win_get_buf(win),win
  end
  wait(function() return release and node('before\nstartup.txt') and node('A.txt') end,'startup reconciliation')
  assert(vim.env.GIT_NO_LAZY_FETCH=='1')
  select('A.txt')
  local before=api.nvim_buf_get_lines(ts.buffer,0,-1,false)
  local cursor=api.nvim_win_get_cursor(ts.window)
  release(); vim.wait(200,function() return false end)
  assert(vim.deep_equal(before,api.nvim_buf_get_lines(ts.buffer,0,-1,false)),'late startup repainted tree')
  assert(vim.deep_equal(cursor,api.nvim_win_get_cursor(ts.window)),'late startup moved selection')
  assert(not node('same.txt'),'late startup restored stat-only false positive')
  local a=select('A.txt'); select('B.txt')
  vim.fn.writefile({'newest external'},vim.env.WT_VIEW_ROOT..'/A.txt')
  vim.wait(2200,function() return false end)
  assert(select('A.txt')==a)
  assert(api.nvim_buf_get_lines(a,0,1,false)[1]=='newest external','cached reselection stale')
  local d=select('D.txt')
  api.nvim_buf_set_lines(d,0,-1,false,{''})
  vim.fn.delete(vim.env.WT_VIEW_ROOT..'/D.txt'); select('B.txt')
  vim.wait(2200,function() return false end)
  assert(select('D.txt')==d)
  assert(vim.bo[d].modified and vim.deep_equal(api.nvim_buf_get_lines(d,0,-1,false),{''}),'unsaved deletion overwritten')
  vim.fn.writefile({'render retry'},vim.env.WT_VIEW_ROOT..'/C.txt')
  wait(function() return node('C.txt') end,'retry fixture missing')
  local diff=require('unified.diff'); local show=diff.show
  diff.show=function() return false end
  local c=select('C.txt')
  assert(table.concat(notices,'\n'):find('WT diff render failed',1,true),'render failure silent')
  diff.show=show; select('C.txt')
  assert(#api.nvim_buf_get_extmarks(c,require('unified.config').ns_id,0,-1,{})>0,'failed render cached as success')
 end)
 if not ok then print(err); vim.cmd('cquit 1') else print('PASS startup race, cached reselection, unsaved deletion, render failure retry'); vim.cmd('qa!') end
end) end})
LUA
nvim --headless -u NONE -i NONE -c "lua dofile('$priv/review.lua')" > "$priv/review.log" 2>&1 || { cat "$priv/review.log"; exit 1; }
cat "$priv/review.log"
sha256sum "$WT_VIEW_ROOT/.git/index" > "$priv/index.after"
cmp "$priv/index.before" "$priv/index.after"
# Actual partial clone with an unavailable promised blob. A configured external
# transport records any attempted implicit fetch, without contacting a network.
git -C "$WT_VIEW_ROOT" config uploadpack.allowFilter true
git clone -q --filter=blob:none --no-checkout "file://$WT_VIEW_ROOT" "$priv/partial"
cat > "$priv/fetch-sentinel" <<SH
#!/bin/sh
printf attempted > '$priv/fetch-attempted'
exit 86
SH
chmod +x "$priv/fetch-sentinel"
git -C "$priv/partial" remote set-url origin "ext::$priv/fetch-sentinel"
git -C "$priv/partial" config protocol.ext.allow always
export WT_VIEW_ROOT="$priv/partial"
printf 'current work\n' > "$WT_VIEW_ROOT/A.txt"
jq -n --arg root "$WT_VIEW_ROOT" --arg base "$base" '{kind:"diff",root:$root,state:{version:1,base:$base,diff:{file:"A.txt",line:1,column:0,topline:1,leftcol:0}}}' > "$WT_VIEW_STATE"
cat > "$priv/partial.lua" <<'LUA'
vim.opt.rtp:append(vim.env.WT_TEST_PLUGINS .. '/unified.nvim')
vim.opt.rtp:append(vim.env.WT_TEST_PLUGINS .. '/nvim-web-devicons')
vim.cmd('filetype plugin on')
dofile(vim.env.WT_VIEW_MODULE)
vim.api.nvim_create_autocmd('VimEnter',{once=true,callback=function() vim.schedule(function()
 local text=table.concat(vim.api.nvim_buf_get_lines(0,0,-1,false),'\n')
 local messages=vim.fn.execute('messages')
 if not text:find('WT diff unavailable',1,true) or not messages:find('baseline object unavailable',1,true) then
  print(text..'\n'..messages); vim.cmd('cquit 1')
 else print('PASS unavailable promised blob is visible, not a new-file diff'); vim.cmd('qa!') end
end) end})
LUA
nvim --headless -u NONE -i NONE -c "lua dofile('$priv/partial.lua')" > "$priv/partial.log" 2>&1 || { cat "$priv/partial.log"; exit 1; }
cat "$priv/partial.log"
test ! -e "$priv/fetch-attempted"
# Negative control proves this fixture would fetch without the suppression.
blob=$(git -C "$WT_VIEW_ROOT" ls-tree HEAD A.txt | awk '{print $3}')
git -C "$WT_VIEW_ROOT" cat-file -e "$blob" > "$priv/fetch-negative.log" 2>&1 && { echo 'expected missing blob'; exit 1; }
test -e "$priv/fetch-attempted"
echo 'PASS no implicit fetch; fetch sentinel negative control triggered'
