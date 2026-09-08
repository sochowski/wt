#!/usr/bin/env bash
# Real Neovim adapter checks under empty config; never uses the user's editor.
set -euo pipefail
repo=$(cd "$(dirname "$0")" && pwd)
managed_plugins=${WT_TEST_PLUGINS:-}
priv=$(mktemp -d)
trap 'rm -rf "$priv"' EXIT
for inherited in ${!WT_@}; do unset "$inherited"; done
unset TMUX TMUX_PANE
export HOME="$priv/home" XDG_CONFIG_HOME="$priv/config" XDG_DATA_HOME="$priv/data" XDG_STATE_HOME="$priv/state" XDG_CACHE_HOME="$priv/cache"
export WT_VIEW_ROOT="$priv/target" WT_VIEW_STATE="$priv/view.json" WT_PRESENT_MODULE="$repo/config/wt-present.lua"
mkdir -p "$HOME" "$WT_VIEW_ROOT" "$priv/outside"
printf 'hello\n' > "$WT_VIEW_ROOT/one.txt"
printf 'secret\n' > "$priv/outside/secret.txt"
ln -s "$priv/outside" "$WT_VIEW_ROOT/escape"
cat > "$priv/check.lua" <<'LUA'
local data = vim.json.decode(table.concat(vim.fn.readfile(vim.env.WT_VIEW_STATE), '\n'))
dofile(vim.env.WT_VIEW_MODULE)
local view = require('wt_view')
if data.kind == 'editor' then
  local saved = vim.json.decode(view.snapshot())
  assert(saved.files[1] == vim.env.WT_VIEW_ROOT .. '/one.txt')
  vim.cmd.edit(vim.fn.fnameescape(vim.env.WT_VIEW_ROOT .. '/escape/secret.txt'))
  assert(not pcall(view.snapshot), 'escaped file must not enter snapshot')
elseif data.kind == 'presentation' then
  require('wt_present').deck_goto(2)
  local saved = vim.json.decode(view.snapshot())
  assert(saved.slide == 2 and #saved.deck.scenes == 2)
  assert(require('wt_present').context().active)
  assert(vim.wait(3000, function() return vim.fn.filereadable(vim.env.WT_CAPTURE_FILE .. '.2') == 1 end), 'slide checkpoint callback did not persist')
  local persisted = vim.json.decode(table.concat(vim.fn.readfile(vim.env.WT_CAPTURE_FILE .. '.2'), '\n'))
  assert(persisted.seq == 2 and persisted.state.slide == 2)
  local ok = pcall(require('wt_present').show, { artifact = { kind = 'file', path = 'escape/secret.txt' } })
  assert(not ok, 'explicit presentation root must retain symlink safety')
  -- Execute the real q mapping, not a synthetic persisted-state edit.
  vim.api.nvim_feedkeys('q', 'xt', false)
  local ended = vim.json.decode(view.snapshot())
  assert(ended.deck == nil, 'ended deck still captured')
  assert(vim.wait(3000, function() return vim.fn.filereadable(vim.env.WT_CAPTURE_FILE .. '.3') == 1 end), 'clear did not checkpoint')
  vim.fn.writefile({vim.json.encode({kind='presentation',root=vim.env.WT_VIEW_ROOT,state=ended})}, vim.env.WT_VIEW_STATE)
  dofile(vim.env.WT_VIEW_MODULE)
  assert(not require('wt_present').context().active, 'ended deck resurrected on reconstruction')
end
vim.cmd('qa!')
LUA
export WT_VIEW_ROOT_ID=fixture-root WT_VIEW_ID=fixture-view WT_VIEW_RUNTIME=fixture-runtime
export WT_CAPTURE_FILE="$priv/checkpoint"
cat > "$priv/checkpoint-writer" <<'SH'
#!/usr/bin/env bash
input=$(cat)
printf '%s' "$input" > "$WT_CAPTURE_FILE.$(jq -r .seq <<< "$input")"
SH
chmod +x "$priv/checkpoint-writer"
export WT_STATE="$priv/checkpoint-writer"
export WT_VIEW_MODULE="$repo/config/wt-view.lua"
jq -n --arg root "$WT_VIEW_ROOT" '{kind:"editor",root:$root,state:{version:1,restart:"never",files:["one.txt"]}}' > "$WT_VIEW_STATE"
nvim --headless -u NONE -i NONE -c "lua dofile('$priv/check.lua')" > "$priv/nvim.log" 2>&1 || { cat "$priv/nvim.log"; exit 1; }
jq -n --arg root "$WT_VIEW_ROOT" '{kind:"presentation",root:$root,state:{version:1,restart:"never",slide:1,deck:{version:1,title:"test",scenes:[{artifact:{kind:"markdown",content:"first"}},{artifact:{kind:"markdown",content:"second"}}]}}}' > "$WT_VIEW_STATE"
nvim --headless -u NONE -i NONE -c "lua dofile('$priv/check.lua')" > "$priv/nvim.log" 2>&1 || { cat "$priv/nvim.log"; exit 1; }
# Real editor reconstruction: nested horizontal/vertical splits, multiple tabs,
# distinct cursors/scroll offsets and active placement, without sourced commands.
seq 1 100 > "$WT_VIEW_ROOT/two.txt"
seq 1 100 > "$WT_VIEW_ROOT/three.txt"
cat > "$priv/editor-roundtrip.lua" <<'LUA'
vim.o.lines, vim.o.columns = 60, 140
local data = vim.json.decode(table.concat(vim.fn.readfile(vim.env.WT_VIEW_STATE), '\n'))
dofile(vim.env.WT_VIEW_MODULE)
if not data.state.editor then
  vim.cmd.edit(vim.fn.fnameescape(vim.env.WT_VIEW_ROOT .. '/two.txt'))
  vim.api.nvim_win_set_cursor(0, {25, 1})
  vim.cmd('rightbelow vsplit')
  vim.cmd.edit(vim.fn.fnameescape(vim.env.WT_VIEW_ROOT .. '/three.txt'))
  vim.api.nvim_win_set_cursor(0, {50, 0})
  vim.cmd('rightbelow split')
  vim.api.nvim_win_set_cursor(0, {75, 1})
  vim.cmd('tabnew')
  vim.cmd.edit(vim.fn.fnameescape(vim.env.WT_VIEW_ROOT .. '/two.txt'))
  vim.api.nvim_win_set_cursor(0, {90, 0})
  vim.cmd('tabprevious')
  vim.cmd('wincmd k')
  data.state = vim.json.decode(require('wt_view').snapshot())
  vim.fn.writefile({vim.json.encode(data)}, vim.env.WT_VIEW_STATE)
else
  local saved = vim.json.decode(require('wt_view').snapshot())
  assert(#saved.editor.tabs == 2, 'tabs lost')
  assert(vim.deep_equal(data.state.editor, saved.editor), 'layout/cursor mismatch: ' .. vim.inspect({before=data.state.editor,after=saved.editor}))
end
vim.cmd('qa!')
LUA
jq -n --arg root "$WT_VIEW_ROOT" '{kind:"editor",root:$root,state:{version:1,restart:"never",files:["one.txt"]}}' > "$WT_VIEW_STATE"
for pass in 1 2; do
 nvim --headless -u NONE -i NONE -c "lua local ok,e=pcall(dofile,'$priv/editor-roundtrip.lua'); if not ok then print(e); vim.cmd('cquit 1') end" > "$priv/nvim.log" 2>&1 || { cat "$priv/nvim.log"; exit 1; }
done
printf '%s\n' 'PASS real isolated Neovim: nested splits/tabs/cursors/active placement roundtrip, path safety, deck+slide+end checkpoints, inactive reconstruction'

# Optional real managed diff coverage uses only explicitly supplied plugin copies.
if [[ -n "$managed_plugins" ]]; then
  WT_TEST_PLUGINS="$managed_plugins" "$repo/test-managed-diff.sh"
  WT_TEST_PLUGINS="$managed_plugins" "$repo/test-managed-diff-review.sh"
fi
