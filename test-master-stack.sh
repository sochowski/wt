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
tmux -L default -f /dev/null new-session -d -s sentinel -x 160 -y 50 'sleep 300'
wt new demo --cwd "$HOME" > "$priv/new.json"
root=$(jq -r .id "$priv/new.json")
master=$(jq -r '.views[0].id' "$priv/new.json")
master_pane=$(jq -r '.views[0].pane' "$priv/new.json")
# The source file, not a hand-written approximation of the bindings.
tmux source-file "$repo/config/tmux-panes.conf"
for n in 1 2 3; do wt view create demo editor root > "$priv/v$n.json"; done
v1=$(jq -r .id "$priv/v1.json"); v2=$(jq -r .id "$priv/v2.json"); v3=$(jq -r .id "$priv/v3.json")
p1=$(jq -r .pane "$priv/v1.json"); p2=$(jq -r .pane "$priv/v2.json"); p3=$(jq -r .pane "$priv/v3.json")
export root master master_pane v1 v2 v3 p1 p2 p3
geometry() {
 tmux list-panes -t "$1" -F '#{pane_id} #{pane_left} #{pane_top} #{pane_width} #{pane_height}' | python3 -c '
import sys
p=[x.split() for x in sys.stdin]; a=list(map(int,p[0][1:])); assert a[0]==0 and a[1] in (0,1),p
for i,row in enumerate(p[1:]):
 b=list(map(int,row[1:])); assert b[0]==a[2]+1,p
 if i: assert b[1]==int(p[i][2])+int(p[i][4])+1,p
assert abs(sum(int(row[4])+1 for row in p[1:])-1-a[3])<=1,p
'
}
geometry "$master_pane"
[[ $(tmux display-message -p -t demo '#{pane_id}') == "$master_pane" ]]
[[ $(tmux show-option -wqv -t "$master_pane" @wt-master-view) == "$master" ]]
wt view promote demo "$v2"
[[ $(tmux display-message -p -t demo '#{pane_id}') == "$master_pane" ]]
[[ $(tmux show-option -wqv -t "$p2" @wt-master-view) == "$v2" ]]
wt view master-width demo "$v2" 65
geometry "$p2"
window_width=$(tmux display-message -p -t "$p2" '#{window_width}')
[[ $(tmux display-message -p -t "$p2" '#{pane_width}') == $(( (window_width-1)*65/100 )) ]]
for opts in '--placement free' '--direction left' '--direction above' '--size 40' '--size 0'; do
 if wt view create demo editor root $opts 2>/dev/null; then exit 1; fi
done
# Both source and destination reflow; master and focus are not replaced on insert.
wt view place demo "$v3" --placement window
geometry "$p2"
wt view place demo "$v3" --anchor "$v2"
geometry "$p2"
wt view park demo "$v3"
geometry "$p2"
[[ $(tmux display-message -p -t "$p3" '#{pane_width}') == $(tmux display-message -p -t "$p3" '#{window_width}') ]]
wt view place demo "$v3" --anchor "$v2"
geometry "$p2"
wt view pin demo "$v2"
wt view promote demo "$v1"
wt view unpin demo "$v2"
# Hook handles raw topology and resize without restarting any pane.
raw=$(tmux split-window -d -P -F '#{pane_id}' -t "$p1" 'sleep 100')
sleep .5
geometry "$p1"
tmux kill-pane -t "$raw"
sleep .5
geometry "$p1"
# Legacy free geometry is normalized by the hook.
tmux select-layout -t "$p1" tiled >/dev/null
sleep .5
geometry "$p1"
# Real PTY presses: cycle repeats must not be swallowed by display-panes.
TERM=xterm python3 - <<'PY'
import os,pty,subprocess,time,fcntl,termios,struct
master,slave=pty.openpty(); fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',50,160,0,0))
p=subprocess.Popen(['tmux','attach-session','-t','demo'],stdin=slave,stdout=slave,stderr=slave); os.close(slave)
# Drain the terminal continuously so the client cannot block on its output.
import threading
def drain():
 try:
  while os.read(master,65536): pass
 except OSError: pass
threading.Thread(target=drain,daemon=True).start()
def tm(*args): return subprocess.check_output(['tmux',*args],text=True).strip()
def active(): return tm('display-message','-p','-t','demo','#{@wt-view}')
def key(k): os.write(master,k); time.sleep(.22)
time.sleep(.7)
order=tm('show-option','-wqv','-t','demo','@wt-stack').split()
tm('select-pane','-t',os.environ['p1']); assert active()==order[0],order
key(b'\x02j'); assert active()==order[1],('first j',active(),order)
key(b'j'); assert active()==order[2],('repeat j',active(),order)
key(b'k'); assert active()==order[1],('repeat k',active(),order)
key(b'k'); assert active()==order[0],('second repeat k',active(),order)
time.sleep(.8)
key(b'\x02l'); key(b'l'); assert tm('show-option','-wqv','-t','demo','@wt-master-percent')=='75'
time.sleep(.8)
key(b'\x02h'); key(b'h'); assert tm('show-option','-wqv','-t','demo','@wt-master-percent')=='65'
time.sleep(.8)
key(b'\x02j'); key(b'\r'); assert tm('show-option','-wqv','-t','demo','@wt-master-view')==order[1]
# Once repeat expires, ordinary typing must go to the pane, not cycle focus.
time.sleep(.8); before=active(); key(b'jkkj'); assert active()==before
key(b'\x02d'); p.wait(timeout=5); os.close(master)
PY
# Non-WT desktop fallback repeats too, without capturing ordinary typing.
ordinary=$(tmux new-session -d -s ordinary-repeat -P -F '#{pane_id}' 'bash --noprofile --norc -i')
tmux split-window -d -t "$ordinary" 'bash --noprofile --norc -i'
tmux split-window -d -t "$ordinary" 'bash --noprofile --norc -i'
TERM=xterm python3 - <<'PYTEST'
import os,pty,subprocess,time,threading
m,s=pty.openpty(); p=subprocess.Popen(['tmux','attach-session','-t','ordinary-repeat'],stdin=s,stdout=s,stderr=s); os.close(s)
def drain():
 try:
  while os.read(m,65536): pass
 except OSError: pass
threading.Thread(target=drain,daemon=True).start()
def tm(*a): return subprocess.check_output(['tmux',*a],text=True).strip()
def active(): return tm('display-message','-p','-t','ordinary-repeat','#{pane_id}')
def key(k): os.write(m,k); time.sleep(.18)
time.sleep(.5); panes=tm('list-panes','-t','ordinary-repeat','-F','#{pane_id}').split(); tm('select-pane','-t',panes[0])
for k,index in [(b'\x02j',1),(b'j',2),(b'k',1),(b'k',0)]:
 key(k); assert active()==panes[index],(k,active(),panes)
time.sleep(.8); key(b'echo REPEAT-EXPIRED-jkkj\r'); assert active()==panes[0]
assert 'REPEAT-EXPIRED-jkkj' in tm('capture-pane','-p','-t',panes[0])
key(b'\x02d'); p.wait(timeout=5); os.close(m)
PYTEST
tmux kill-session -t ordinary-repeat
geometry "$p1"
[[ -z $(tmux show-option -wqv -t sentinel @wt-stack-managed) ]]
# Explicit master close selects the first surviving stack entry deterministically.
current=$(tmux show-option -wqv -t "$p1" @wt-master-view)
successor=$(tmux show-option -wqv -t "$p1" @wt-stack | awk '{print $2}')
# Do not close the original conversation (close intentionally parks agents).
if [[ "$current" == "$master" ]]; then wt view promote demo "$v2"; current=$v2; successor=$(tmux show-option -wqv -t "$p1" @wt-stack | awk '{print $2}'); fi
wt view close demo "$current"
[[ $(tmux show-option -wqv -t "$master_pane" @wt-master-view) == "$successor" ]]
# Regression: an exited last pane has an empty pane_current_path. Snapshot,
# park and editor placement must keep that empty field, not reject the record.
wt view create demo shell root > "$priv/dead-shell.json"
dead_shell=$(jq -r .id "$priv/dead-shell.json")
dead_shell_pane=$(jq -r .pane "$priv/dead-shell.json")
wt view create demo editor root --placement window > "$priv/dead-editor.json"
dead_editor=$(jq -r .id "$priv/dead-editor.json")
dead_editor_pane=$(jq -r .pane "$priv/dead-editor.json")
for pane in "$dead_shell_pane" "$dead_editor_pane"; do
 tmux set-option -p -t "$pane" remain-on-exit on
 tmux respawn-pane -k -t "$pane" 'exit 0'
 for _ in {1..50}; do
  [[ $(tmux display-message -p -t "$pane" '#{pane_dead}') == 1 ]] && break
  sleep .1
 done
 [[ $(tmux display-message -p -t "$pane" '#{pane_dead}:#{pane_current_path}') == '1:' ]]
done
wt snapshot demo
wt view park demo "$dead_shell"
wt view park demo "$dead_editor"
wt view place demo "$dead_editor" --anchor "$successor"
[[ $(tmux display-message -p -t "$dead_editor_pane" '#{pane_dead}') == 1 ]]
wt view show demo "$dead_shell" | jq -e '.parked and .state.shell.cwd == ""' >/dev/null
wt view show demo "$dead_editor" | jq -e '.parked == false' >/dev/null
wt snapshot demo
wt roots demo | jq '.[0].layout.windows|map({master,master_percent,views})' > "$priv/before-layout.json"
tmux kill-server; sleep .4
wt restore demo > "$priv/restored.json"
jq '.layout.windows|map({master,master_percent,views})' "$priv/restored.json" > "$priv/after-layout.json"
# Raw closed human pane is kept as an idle recovery record in its own window;
# only compare the surviving original stack, not newly recovered offline views.
jq '.[0]' "$priv/before-layout.json" > "$priv/expected.json"
jq '.[0]' "$priv/after-layout.json" > "$priv/actual.json"
diff -u "$priv/expected.json" "$priv/actual.json"
restored_pane=$(wt view show demo "$successor" | jq -r .pane)
geometry "$restored_pane"
[[ $(tmux show-option -wqv -t "$restored_pane" @wt-master-percent) == 65 ]]
for view in "$dead_shell" "$dead_editor"; do
 pane=$(wt view show demo "$view" | jq -r .pane)
 [[ $(tmux display-message -p -t "$pane" '#{pane_dead}') == 0 ]]
 wt view show demo "$view" | jq -e '.problem == ""' >/dev/null
done
wt view place demo "$dead_editor" --placement window
wt view place demo "$dead_editor" --anchor "$successor"
wt snapshot demo
printf '%s\n' 'PASS snapshot/place recovery with dead last shell and parked editor panes'
# Upgrade a live projection via snapshot without reviving a missing view.
wt view create demo editor root --placement window > "$priv/offline.json"
offline_pane=$(jq -r .pane "$priv/offline.json")
tmux set-hook -u -t demo 'window-layout-changed[19731]'
tmux set-hook -u -t demo 'after-new-window[19731]'
tmux set-hook -u -t demo 'window-unlinked[19731]'
tmux kill-pane -t "$offline_pane"
pane_count=$(tmux list-panes -s -t demo | wc -l)
wt snapshot demo
[[ $(tmux list-panes -s -t demo | wc -l) == "$pane_count" ]]
tmux show-options -t demo 'window-layout-changed' | grep -q 'stack-hook'
# Delimiters inside a path remain malformed: do not relax the field count.
for bad_name in $'tab\tpath' $'newline\npath'; do
 mkdir -p "$priv/$bad_name"
 bad_pane=$(tmux new-window -d -P -F '#{pane_id}' -t demo -c "$priv/$bad_name" 'sleep 300')
 if wt snapshot demo > "$priv/bad-snapshot.log" 2>&1; then
  echo 'malformed pane metadata accepted' >&2; exit 1
 fi
 grep -q 'unsupported pane metadata' "$priv/bad-snapshot.log"
 tmux kill-pane -t "$bad_pane"
done
printf '%s\n' 'PASS mandatory master-stack geometry, topology hooks, master/width restore, live upgrade and actual PTY repeat bindings'
