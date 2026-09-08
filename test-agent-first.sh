#!/usr/bin/env bash
# Server-loss acceptance test. The driver stays OUTSIDE the disposable server.
set -euo pipefail
repo=$(cd "$(dirname "$0")" && pwd)
priv=$(mktemp -d)
export GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)"
for inherited in ${!WT_@}; do unset "$inherited"; done
unset TMUX TMUX_PANE
export HOME="$priv/home" TMUX_TMPDIR="$priv/tmux"
export WT_STATUS_DIR="$priv/state" WT_DB="$priv/state/wt.db"
export WT_BASE_DIR="$priv/worktrees" WT_CONFIG_DIR="$priv/config" WT_LOG_FILE="$priv/state/wt.log"
export WT_STATE="$priv/bin/wt-state" WT_SOURCE_CONFIG="$repo/config" WT_STUB_NATIVE=1 WT_DEFAULT_AGENT=pi
export WT_NVIM_SOCK_DIR="$priv/nvim" WT_SHELL_DIR="$priv/shells"
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL="$HOME/.gitconfig"
mkdir -p "$HOME" "$TMUX_TMPDIR" "$WT_STATUS_DIR" "$priv/bin"
trap 'tmux -L default kill-server 2>/dev/null || true; rm -rf "$priv"' EXIT
(cd "$repo/state" && go build -o "$WT_STATE" .)
for agent in claude codex gemini opencode pi; do ln -s "$repo/staging/stub-agent" "$priv/bin/$agent"; done
ln -s "$repo/staging/nvim-view-stub.js" "$priv/bin/nvim"
export PATH="$priv/bin:$repo/bin:$PATH"
git config --global user.name 'wt acceptance'; git config --global user.email test@example.invalid
for name in a b; do
 mkdir -p "$priv/$name"; git -C "$priv/$name" init -q -b main
 echo "$name" > "$priv/$name/file.txt"; git -C "$priv/$name" add .; git -C "$priv/$name" commit -qm initial
done
wt() { "$repo/bin/wt" "$@"; }
wait_agents() {
 local expected=$1
 for _ in {1..100}; do
  if [[ $(wt agents list demo | jq '[.[]|select(.native_id != "")]|length') == "$expected" ]]; then return; fi
  sleep .1
 done
 echo 'stub agents did not report readiness' >&2; exit 1
}
# Pin private server config before any worktree command starts it.
tmux -L default -f /dev/null new-session -d -s sentinel 'sleep 300'
wt new demo --cwd "$HOME" > "$priv/new.json"
root=$(jq -r .id "$priv/new.json"); first=$(jq -r '.agents[0].id' "$priv/new.json")
[[ $(tmux list-panes -s -t demo | wc -l) == 1 ]]
[[ $(wt checkout list demo | jq length) == 0 ]]
wait_agents 1
wt checkout attach demo alpha "$priv/a" > "$priv/a.json"
wt checkout attach demo beta "$priv/b" > "$priv/b.json"
focus=$(tmux display-message -p -t demo '#{window_id}')
wt agents create demo reviewer --parent main --cwd beta --task "review the two repositories" > "$priv/peer.json"
peer=$(jq -r .id "$priv/peer.json")
wait_agents 2
[[ $(tmux display-message -p -t demo '#{window_id}') == "$focus" ]]
[[ $(tmux list-windows -t demo -F '#{window_name}' | grep -cx reviewer) == 1 ]]
[[ $(tmux list-panes -t demo:reviewer | wc -l) == 1 ]]
wt agents read demo reviewer | grep -q 'Interactive Pi stub'
wt message send demo "$first" "$peer" 'review alpha and beta' --id review-1 >/dev/null
wt message send demo "$first" "$peer" 'review alpha and beta' --id review-1 >/dev/null
[[ $(wt message list demo reviewer | jq length) == 2 ]]
before_windows=$(tmux list-windows -t demo | wc -l)
wt view create demo diff alpha --base HEAD --placement split --direction stack > "$priv/diff.json"
[[ $(tmux list-windows -t demo | wc -l) == "$before_windows" ]]
[[ $(tmux display-message -p -t demo '#{window_id}') == "$focus" ]]
wt view create demo shell beta --command "touch '$priv/unsafe-replay'" > "$priv/shell.json"
for _ in {1..30}; do [[ -f "$priv/unsafe-replay" ]] && break; sleep .1; done
[[ -f "$priv/unsafe-replay" ]]
sleep .2
# Existing shell CLI and the actual prefix+c binding in a no-repository root.
wt shell --session demo new notes --detach
mkdir -p "$HOME/bin"
ln -s "$repo/bin/wt" "$HOME/bin/wt"
sed -n '/^bind c /,+2p' "$repo/config/tmux-wt.conf" > "$priv/binding.conf"
tmux source-file "$priv/binding.conf"
TERM=xterm python3 - <<'PY'
import os, pty, subprocess, time
master, slave = pty.openpty()
p = subprocess.Popen(['tmux', 'attach-session', '-t', 'demo'], stdin=slave, stdout=slave, stderr=slave)
os.close(slave)
time.sleep(.4)
os.write(master, b'\x02c')
time.sleep(.8)
os.write(master, b'\x02d')
p.wait(timeout=5)
os.close(master)
PY
[[ $(wt shell --session demo ls --json | jq length) == 2 ]]
wt shell --session demo send notes --text "cd '$priv/b'; printf 'SHELL-LOG-BEFORE\\n'"
wt shell --session demo send notes --key Enter
for _ in {1..30}; do wt shell --session demo read notes --raw | grep -q SHELL-LOG-BEFORE && break; sleep .1; done
sleep .3
# Pre-adapter managed shells lacked an explicit pane log option; capture the
# established log path rather than dropping legacy logging metadata.
legacy_shell_pane=$(wt shell --session demo ls --json | jq -r '.[]|select(.name=="notes")|.pane_id')
tmux set-option -pu -t "$legacy_shell_pane" @wt-shell-log
wt snapshot demo
wt roots demo > "$priv/before.json"
# Exercise a manual split: unknown process becomes a stopped human shell.
shell_pane=$(jq -r .pane "$priv/shell.json")
tmux split-window -d -t "$shell_pane" "sleep 300"
wt snapshot demo
wt roots demo > "$priv/before.json"
rm "$priv/unsafe-replay"
tmux -L default kill-server
sleep .3
wt ls simple | grep -q demo
wt restore demo > "$priv/restored.json"
sleep .4
wt restore demo > "$priv/repeated.json"
[[ ! -e "$priv/unsafe-replay" ]]
restored_diff=$(wt view show demo "$(jq -r .id "$priv/diff.json")" | jq -r .pane)
restored_main=$(wt view list demo | jq -r --arg id "$first" '.[] | select(.kind=="agent" and .target==$id) | .pane')
[[ $(tmux display-message -p -t "$restored_diff" '#{window_id}') == $(tmux display-message -p -t "$restored_main" '#{window_id}') ]]
[[ $(tmux list-sessions -F '#{session_name}') == demo ]]
[[ $(wc -l < "$WT_STATUS_DIR/stub-launches.jsonl") == 4 ]]
jq -s -e '.[0][0] as $before | .[1] as $after |
 ($before.id == $after.id) and
 ([$before.agents[]|{id,name,parent_agent_id,native_id,adapter}] == [$after.agents[]|{id,name,parent_agent_id,native_id,adapter}]) and
 ($before.checkouts == $after.checkouts) and
 ([$before.views[]|{id,kind,target,manager,state}] == [$after.views[]|{id,kind,target,manager,state}])' "$priv/before.json" "$priv/restored.json" >/dev/null
jq -s -e '.[2:]|all(.args[0] == "--session" and (.args|index("--continue")|not))' "$WT_STATUS_DIR/stub-launches.jsonl" >/dev/null
[[ $(tmux list-panes -s -t demo | wc -l) == 7 ]]
[[ $(wt shell --session demo ls --json | jq length) == 2 ]]
notes_pane=$(wt shell --session demo ls --json | jq -r '.[]|select(.name=="notes")|.pane_id')
[[ $(tmux display-message -p -t "$notes_pane" '#{pane_current_path}') == "$priv/b" ]]
wt shell --session demo read notes --raw | grep -q SHELL-LOG-BEFORE
wt shell --session demo send notes --text "printf 'SHELL-LOG-AFTER\\n'"
wt shell --session demo send notes --key Enter
sleep .3
wt shell --session demo read notes --raw | grep -q SHELL-LOG-AFTER
# Lock contention and concurrent restore: either lock rejection or reconciliation,
# never a second process for a live conversation.
wt restore demo > "$priv/concurrent-a.json" 2> "$priv/concurrent-a.err" & pa=$!
wt restore demo > "$priv/concurrent-b.json" 2> "$priv/concurrent-b.err" & pb=$!
wait "$pa" || grep -q locked "$priv/concurrent-a.err"
wait "$pb" || grep -q locked "$priv/concurrent-b.err"
[[ $(wc -l < "$WT_STATUS_DIR/stub-launches.jsonl") == 4 ]]
[[ $(wt roots demo | jq '.[0].wake_enabled') == false ]]
wt message wake demo --budget 7
wt restore demo >/dev/null
[[ $(wt roots demo | jq '.[0].wake_budget') == 7 ]]
# Agent-scoped controls cannot steal human views or mutate other roots.
peer_runtime=$(wt agents show demo "$peer" | jq -r .runtime)
if WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view close demo "$(jq -r .id "$priv/shell.json")" 2>/dev/null; then exit 1; fi
if WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID=stale wt agents create demo forbidden 2>/dev/null; then exit 1; fi
if wt agents reparent demo "$first" "$peer" 2>/dev/null; then exit 1; fi
# Managed present lazily acquires an explicitly targeted view and persists deck.
peer_runtime=$(wt agents show demo reviewer | jq -r .runtime)
printf '%s' '{"version":1,"scenes":[{"artifact":{"kind":"markdown","content":"review"}}]}' | WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" "$WT_STATE" worktree present demo alpha > "$priv/present.json"
[[ $(wt view list demo | jq '[.[]|select(.kind=="presentation")]|length') == 1 ]]
[[ $(wt view list demo | jq -r '.[]|select(.kind=="presentation")|.target') == "$(jq -r .id "$priv/a.json")" ]]
# Missing native file remains an unresolved view, never a new conversation.
peer_file=$(wt agents show demo "$peer" | jq -r .adapter.file)
cp "$peer_file" "$priv/saved-transcript"
tmux -L default kill-server; sleep .3
rm "$peer_file"
wt restore demo > "$priv/missing.json" 2> "$priv/missing.err"
grep -q 'missing transcript' "$priv/missing.err"
sleep .3
[[ $(wc -l < "$WT_STATUS_DIR/stub-launches.jsonl") == 5 ]]
wt restore demo >/dev/null
[[ $(wc -l < "$WT_STATUS_DIR/stub-launches.jsonl") == 5 ]]
[[ -e "$priv/a/file.txt" && -e "$priv/b/file.txt" ]]
cp "$priv/saved-transcript" "$peer_file"
wt agents resume demo reviewer
sleep .4
[[ $(wc -l < "$WT_STATUS_DIR/stub-launches.jsonl") == 6 ]]
wt agents resume demo reviewer
[[ $(wc -l < "$WT_STATUS_DIR/stub-launches.jsonl") == 6 ]]
# Current supervision, not creator history, controls stop. Attached clients
# protect the active pane even from its current supervisor.
first_runtime=$(wt agents show demo main | jq -r .runtime)
WT_ROOT_ID="$root" WT_AGENT_ID="$first" WT_RUNTIME_ID="$first_runtime" wt agents create demo delegated --parent main > "$priv/delegated.json"
wt agents reparent demo delegated reviewer
if WT_ROOT_ID="$root" WT_AGENT_ID="$first" WT_RUNTIME_ID="$first_runtime" wt agents stop demo delegated 2>/dev/null; then exit 1; fi
peer_runtime=$(wt agents show demo reviewer | jq -r .runtime)
tmux select-window -t demo:delegated
sleep 5 | tmux -C attach-session -t demo > "$priv/control-client.log" 2>&1 & client=$!
for _ in {1..30}; do [[ -n "$(tmux list-clients 2>/dev/null)" ]] && break; sleep .1; done
if WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt agents stop demo delegated 2>/dev/null; then exit 1; fi
kill "$client"; wait "$client" || true
sleep .1
WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt agents stop demo delegated
[[ $(wt agents show demo delegated | jq .stopped) == true ]]
old_native=$(wt agents show demo delegated | jq -r .native_id)
wt agents resume demo delegated
sleep .4
[[ $(wt agents show demo delegated | jq .stopped) == false ]]
[[ $(wt agents show demo delegated | jq -r .native_id) == "$old_native" ]]
launches=$(wc -l < "$WT_STATUS_DIR/stub-launches.jsonl")
wt agents resume demo delegated
[[ $(wc -l < "$WT_STATUS_DIR/stub-launches.jsonl") == "$launches" ]]
[[ $(wt roots demo | jq '.[0].wake_enabled') == false ]]
# Close/park cannot kill or rearrange an unpinned manager-owned editor in human use.
peer_runtime=$(wt agents show demo reviewer | jq -r .runtime)
WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view create demo editor alpha --file file.txt > "$priv/editor.json"
editor=$(jq -r .id "$priv/editor.json"); editor_pane=$(jq -r .pane "$priv/editor.json")
# Owned editor can split beside its caller with a real attached client, without focus loss.
peer_pane=$(wt view list demo | jq -r --arg peer "$peer" '.[] | select(.kind=="agent" and .target==$peer) | .pane')
tmux select-window -t "$peer_pane"; tmux select-pane -t "$peer_pane"
sleep 10 | tmux -C attach-session -t demo > "$priv/layout-client.log" 2>&1 & layout_client=$!
for _ in {1..30}; do [[ -n "$(tmux list-clients 2>/dev/null)" ]] && break; sleep .1; done
WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view place demo "$editor" --placement split --anchor caller --direction stack
[[ $(tmux display-message -p -t "$editor_pane" '#{window_id}') == $(tmux display-message -p -t "$peer_pane" '#{window_id}') ]]
[[ $(tmux list-clients -F '#{pane_id}') == "$peer_pane" ]]
# Focus an unrelated human-owned editor, not the calling agent's pane.
wt view create demo editor alpha --file file.txt > "$priv/human-anchor.json"
human_anchor=$(jq -r .id "$priv/human-anchor.json"); human_pane=$(jq -r .pane "$priv/human-anchor.json")
tmux select-window -t "$human_pane"; tmux select-pane -t "$human_pane"
# Direct focused creation may reflow a pinned human anchor: no ownership or
# focus transfer. Arbitrary directions are no longer a supported layout mode.
wt view pin demo "$human_anchor"
layout_windows=$(tmux list-windows -t demo | wc -l)
WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view create demo editor alpha --file file.txt --placement split --anchor focused --direction stack > "$priv/focused-editor.json"
focused_editor=$(jq -r .id "$priv/focused-editor.json"); focused_pane=$(jq -r .pane "$priv/focused-editor.json")
[[ $(tmux list-windows -t demo | wc -l) == "$layout_windows" ]]
[[ $(tmux list-clients -F '#{pane_id}') == "$human_pane" ]]
[[ $(tmux display-message -p -t "$focused_pane" '#{window_id}') == $(tmux display-message -p -t "$human_pane" '#{window_id}') ]]
WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view promote demo "$human_anchor"
WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view master-width demo "$human_anchor" 65
[[ $(wt view show demo "$human_anchor" | jq '.manager == "human" and .pinned') == true ]]
[[ $(tmux list-clients -F '#{pane_id}') == "$human_pane" ]]
if WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view cycle demo "$human_anchor" next 2>/dev/null; then exit 1; fi
wt view unpin demo "$human_anchor"
tmux select-window -t "$peer_pane"; tmux select-pane -t "$peer_pane"
for direction in left above below; do
 if WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view place demo "$focused_editor" --placement split --anchor "$editor" --direction "$direction" 2>/dev/null; then exit 1; fi
done
WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view place demo "$focused_editor" --anchor "$editor"
[[ $(tmux list-clients -F '#{pane_id}') == "$peer_pane" ]]
[[ $(tmux display-message -p -t "$editor_pane" '#{pane_left}') == $(tmux display-message -p -t "$focused_pane" '#{pane_left}') ]]
[[ $(tmux display-message -p -t "$editor_pane" '#{pane_top}') -lt $(tmux display-message -p -t "$focused_pane" '#{pane_top}') ]]
WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view place demo "$focused_editor" --placement split --anchor "$peer_pane" --direction stack
WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view close demo "$focused_editor"
wt view close demo "$human_anchor"
if WT_ROOT_ID="$root" WT_AGENT_ID="$first" WT_RUNTIME_ID="$first_runtime" wt view place demo "$editor" stack 2>/dev/null; then exit 1; fi
wt view pin demo "$editor"
if WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view place demo "$editor" window 2>/dev/null; then exit 1; fi
wt view unpin demo "$editor"
WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view place demo "$editor" window
[[ $(tmux list-clients -F '#{pane_id}') == "$peer_pane" ]]
kill "$layout_client"; wait "$layout_client" || true; sleep .1
# Invalid/offline/cross-root anchors and invalid sizing cannot leak view rows.
view_count=$(wt view list demo | jq length)
foreign_pane=$(tmux new-session -d -s placement-foreign -P -F '#{pane_id}' 'sleep 60')
for opts in '--anchor focused' '--anchor missing-view' "--anchor $foreign_pane" '--size 0' '--size 100'; do
 if WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view create demo editor alpha --file file.txt --placement split $opts 2>/dev/null; then exit 1; fi
 [[ $(wt view list demo | jq length) == "$view_count" ]]
done
tmux kill-session -t placement-foreign
tmux select-window -t "$editor_pane"
sleep 10 | tmux -C attach-session -t demo > "$priv/editor-client.log" 2>&1 & client=$!
for _ in {1..30}; do [[ -n "$(tmux list-clients 2>/dev/null)" ]] && break; sleep .1; done
for op in close park; do
 if WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view "$op" demo "$editor" 2>/dev/null; then exit 1; fi
done
[[ $(tmux display-message -p -t "$editor_pane" '#{pane_dead}') == 0 ]]
kill "$client"; wait "$client" || true; sleep .1
wt view pin demo "$editor"
if WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view close demo "$editor" 2>/dev/null; then exit 1; fi
wt view unpin demo "$editor"
WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view close demo "$editor"
# Repaired live resource placeholder retries in place, including dead editors.
WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt view create demo editor alpha --file file.txt > "$priv/repair-editor.json"
repair=$(jq -r .id "$priv/repair-editor.json"); rp=$(jq -r .pane "$priv/repair-editor.json")
tmux respawn-pane -k -t "$rp" 'exit 0'; sleep .1
mv "$priv/a/file.txt" "$priv/a/saved.txt"
wt view resume demo "$repair" 2> "$priv/repair.err"
[[ -n $(wt view show demo "$repair" | jq -r .problem) ]]
mv "$priv/a/saved.txt" "$priv/a/file.txt"
wt view resume demo "$repair"
sleep .2
[[ $(wt view show demo "$repair" | jq -r .problem) == '' ]]
repair_runtime=$(wt view show demo "$repair" | jq -r .runtime)
wt view resume demo "$repair"
[[ $(wt view show demo "$repair" | jq -r .runtime) == "$repair_runtime" ]]
# Durable last-presentation selection is root/manager scoped across CLI reloads.
for target in alpha beta; do
 printf '%s' '{"version":1,"scenes":[{"artifact":{"kind":"markdown","content":"keep"}}]}' | WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" "$WT_STATE" worktree present demo "$target" > "$priv/present-$target.json"
done
last_view=$(jq -r .view_id "$priv/present-beta.json")
wt view pin demo "$last_view"
if WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" "$WT_STATE" worktree present-clear demo 2>/dev/null; then exit 1; fi
wt view unpin demo "$last_view"
last_pane=$(wt view show demo "$last_view" | jq -r .pane)
tmux select-window -t "$last_pane"; tmux select-pane -t "$last_pane"
sleep 10 | tmux -C attach-session -t demo > "$priv/present-client.log" 2>&1 & client=$!
for _ in {1..30}; do [[ -n "$(tmux list-clients 2>/dev/null)" ]] && break; sleep .1; done
if WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" "$WT_STATE" worktree present-clear demo 2>/dev/null; then exit 1; fi
kill "$client"; wait "$client" || true; sleep .1
WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" "$WT_STATE" worktree present-clear demo
[[ $(wt view show demo "$last_view" | jq '.state.deck == null') == true ]]
[[ $(wt view show demo "$(jq -r .view_id "$priv/present-alpha.json")" | jq '.state.deck != null') == true ]]
# In-use detach rejects; unused borrowed attachment removal never removes files.
if wt checkout detach demo alpha 2>/dev/null; then exit 1; fi
wt checkout attach demo unused "$priv/a" >/dev/null
wt checkout detach demo unused
[[ -e "$priv/a/file.txt" ]]
# Persistent roots can exist before tmux and before any transcript exists.
wt new blank --offline > "$priv/blank.json"
[[ $(wt roots blank | jq '.[0].agents[0].native_id') == '""' ]]
wt ls simple | grep -q blank
wt new another --offline >/dev/null
wt ls | grep -q blank
wt ls | grep -q another
cat > "$priv/bin/fzf" <<'SH'
#!/usr/bin/env bash
cat | grep '^another$'
SH
chmod +x "$priv/bin/fzf"
printf 'y\n' | wt delete-pick > "$priv/delete-pick.log"
! wt ls session | grep -qx another
wt ls session | grep -qx blank
# Existing wt-shells skill works for authenticated agents within their root.
agent_shell() { WT_ROOT_ID="$root" WT_AGENT_ID="$peer" WT_RUNTIME_ID="$peer_runtime" wt shell "$@"; }
focus=$(tmux display-message -p -t demo '#{window_id}')
agent_shell new peer-shell --detach
agent_shell ls --json | jq -e '.[]|select(.name=="peer-shell")' >/dev/null
[[ $(wt view list demo | jq -r '.[]|select(.state.shell.name=="peer-shell")|.manager') == "$peer" ]]
[[ $(tmux display-message -p -t demo '#{window_id}') == "$focus" ]]
agent_shell send peer-shell --text "printf 'PEER-SHELL-OUTPUT\\n'"
agent_shell send peer-shell --key Enter
agent_shell wait peer-shell --match PEER-SHELL-OUTPUT --timeout 5s >/dev/null
agent_shell read peer-shell --raw | grep -q PEER-SHELL-OUTPUT
agent_shell run peer-job -- bash -c 'printf "PEER-JOB-READY\\n"; sleep 30'
agent_shell wait peer-job --match PEER-JOB-READY --timeout 5s >/dev/null
if agent_shell --session blank ls --json 2>/dev/null; then exit 1; fi
if agent_shell open peer-shell 2>/dev/null; then exit 1; fi
if agent_shell new focus-thief --switch 2>/dev/null; then exit 1; fi
if agent_shell send notes --text nope 2>/dev/null; then exit 1; fi
if agent_shell send main --text nope 2>/dev/null; then exit 1; fi
peer_shell_pane=$(agent_shell ls --json | jq -r '.[]|select(.name=="peer-shell")|.pane_id')
tmux select-window -t "$peer_shell_pane"
sleep 10 | tmux -C attach-session -t demo > "$priv/shell-client.log" 2>&1 & client=$!
for _ in {1..30}; do [[ -n "$(tmux list-clients 2>/dev/null)" ]] && break; sleep .1; done
if agent_shell stop peer-shell 2>/dev/null; then exit 1; fi
if agent_shell rm peer-shell 2>/dev/null; then exit 1; fi
agent_shell read peer-shell --raw >/dev/null
kill "$client"; wait "$client" || true; sleep .1
# A shell's stable pane must remain the control target after a human splits its
# window. Input cannot follow the active pane, and removal cannot kill siblings.
human_pane=$(tmux split-window -d -P -F '#{pane_id}' -t "$peer_shell_pane" 'bash --noprofile --norc -i')
tmux select-pane -t "$human_pane"
sleep 10 | tmux -C attach-session -t demo > "$priv/mixed-client.log" 2>&1 & client=$!
for _ in {1..30}; do [[ -n "$(tmux list-clients 2>/dev/null)" ]] && break; sleep .1; done
agent_shell send peer-shell --text "printf 'MIXED-SHELL-ONLY\\n'"
agent_shell send peer-shell --key Enter
agent_shell wait peer-shell --match MIXED-SHELL-ONLY --timeout 3s >/dev/null
agent_shell stop peer-shell
sleep .1
! tmux capture-pane -p -t "$human_pane" | grep -Eq 'MIXED-SHELL-ONLY|\^C'
if agent_shell rm peer-shell 2>/dev/null; then echo 'removed mixed shell window' >&2; exit 1; fi
[[ $(tmux display-message -p -t "$human_pane" '#{pane_dead}') == 0 ]]
[[ $(tmux display-message -p -t "$peer_shell_pane" '#{pane_dead}') == 0 ]]
kill "$client"; wait "$client" || true; sleep .1
tmux kill-pane -t "$human_pane"
# Moving an actual agent view into the shell window must not expose its terminal.
main_pane=$(wt view list demo | jq -r --arg main "$first" '.[]|select(.kind=="agent" and .target==$main)|.pane')
tmux join-pane -d -s "$main_pane" -t "$peer_shell_pane"
tmux select-pane -t "$main_pane"
agent_shell send peer-shell --text "printf 'MIXED-NOT-AGENT\\n'"
agent_shell send peer-shell --key Enter
agent_shell wait peer-shell --match MIXED-NOT-AGENT --timeout 3s >/dev/null
agent_shell stop peer-shell
sleep .1
! tmux capture-pane -p -t "$main_pane" | grep -q MIXED-NOT-AGENT
[[ $(tmux display-message -p -t "$main_pane" '#{pane_dead}') == 0 ]]
if agent_shell rm peer-shell 2>/dev/null; then echo 'removed sibling agent pane' >&2; exit 1; fi
tmux break-pane -d -s "$main_pane" -n main
agent_shell stop peer-job
agent_shell rm peer-job
agent_shell rm peer-shell
! wt view list demo | jq -e '.[]|select(.state.shell.name=="peer-shell")' >/dev/null
# Forget a filesystem-discovered legacy root, retaining its checkout across restart.
mkdir -p "$WT_BASE_DIR/legacy"
git -C "$priv/a" worktree add -qb forgotten "$WT_BASE_DIR/legacy/forgotten"
wt ls session | grep -qx legacy-forgotten
wt delete legacy-forgotten --force > "$priv/forget.log"
[[ -e "$WT_BASE_DIR/legacy/forgotten/file.txt" ]]
! wt ls session | grep -qx legacy-forgotten
wt snapshot demo
tmux -L default kill-server; sleep .3
! wt ls session | grep -qx legacy-forgotten
wt restore demo > "$priv/ended-restored.json"
[[ $(wt view show demo "$last_view" | jq '.state.deck == null and .problem == ""') == true ]]
[[ $(wt view show demo "$(jq -r .view_id "$priv/present-alpha.json")" | jq '.state.deck != null') == true ]]
printf '%s\n' 'PASS agent-first private restart: named interactive peer windows, two borrowed repos, typed views/layout, exact transcripts, dedup, scope, missing resources, concurrent/repeated restore, no unsafe replay'
