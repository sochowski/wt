#!/usr/bin/env bash
# tests/wt-pane.sh — run from the repo root. The Rex backend runs against a fake
# rex; the tmux backend against a real tmux server of its own (skipped without
# tmux), so no live session is touched.
set -uo pipefail

pane="$PWD/bin/wt-pane"
tmp=$(mktemp -d); trap 'tmux -S "$tmp/tmux.sock" kill-server 2> /dev/null; rm -rf "$tmp"' EXIT
fails=0
ok()  { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n%s\n' "$1" "${2:-}"; fails=$((fails + 1)); }
eq()  { [[ "$2" == "$3" ]] && ok "$1" || bad "$1" "  want: $3"$'\n'"  got:  $2"; }
has() { [[ "$2" == *"$3"* ]] && ok "$1" || bad "$1" "  want to contain: $3"$'\n'"  got: $2"; }

# ── Rex, faked ───────────────────────────────────────────────────────────────
# Blocks in this tab: nvim (the caller), an agent, a shell over an agent, an
# agent in another tab, and a non-terminal block. Each process report lives in
# $tmp/rex/<block>.json; a missing one makes `block call` fail.
mkdir -p "$tmp/bin" "$tmp/rex"
cat > "$tmp/bin/rex" << 'EOF'
#!/usr/bin/env bash
d="$(dirname "$0")/../rex"
printf '%s\n' "$*" >> "$d/calls"
arg() { local want="$1"; shift; while (( $# )); do [[ "$1" == "$want" ]] && { echo "$2"; return; }; shift; done; }
case "$1 ${2:-}" in
  "whoami --json") cat "$d/whoami.json" ;;
  "block ls")
    w=$(arg -w "$@")
    if [[ -n "$w" ]]; then jq --arg w "$w" '{blocks: [.blocks[] | select(.window_id == $w)]}' "$d/blocks.json"
    else cat "$d/blocks.json"; fi ;;
  "block call") b=$(arg -b "$@"); [[ -f "$d/$b.json" ]] && cat "$d/$b.json" || { echo "no block $b" >&2; exit 1; } ;;
  split*)       echo '{"block_ids":["new-block"]}' ;;
  "window new") echo '{"window_id":"w2","block_ids":["tab-block"]}' ;;
  send*|"send-key"*|capture*|focus*) ;;
  *) echo "fake rex: $*" >&2; exit 2 ;;
esac
EOF
chmod +x "$tmp/bin/rex"
term=com.superlogical.terminal
cat > "$tmp/rex/blocks.json" << EOF
{"blocks":[
 {"block_id":"nvim","window_id":"w1","creator_name":"$term","label":"review"},
 {"block_id":"agent","window_id":"w1","creator_name":"$term","label":"shell"},
 {"block_id":"shell","window_id":"w1","creator_name":"$term","label":"shell"},
 {"block_id":"node","window_id":"w1","creator_name":"$term","label":""},
 {"block_id":"far","window_id":"w2","creator_name":"$term","label":"x"},
 {"block_id":"web","window_id":"w1","creator_name":"some.other.block","label":"x"}]}
EOF
echo '{"session_id":"s","block_id":"nvim","window_id":"w1"}' > "$tmp/rex/whoami.json"
echo '{"foreground":{"name":"codex","pid":7,"cwd":"/ws"}}' > "$tmp/rex/agent.json"
echo '{"foreground":{"name":"bash","pid":8},"ancestors":[{"name":"claude"}]}' > "$tmp/rex/shell.json"
echo '{"foreground":{"name":"node","pid":9,"script":"/pkg/@anthropic-ai/claude-code/cli.js"}}' > "$tmp/rex/node.json"
echo '{"foreground":{"name":"claude","pid":10}}' > "$tmp/rex/far.json"
echo '{"foreground":{"name":"claude","pid":11}}' > "$tmp/rex/web.json"
echo '{"foreground":{"name":"nvim","pid":12}}' > "$tmp/rex/nvim.json"

rx() { env -u TMUX -u TMUX_PANE PATH="$tmp/bin:$PATH" REX_SESSION=s REX_BLOCK=env-block "$pane" "$@"; }
eq "rex: backend" "$(rx backend)" rex
eq "rex: here asks whoami over the environment" "$(rx here)" "rex:s/nvim"
echo '{}' > "$tmp/rex/whoami.json.bad"; mv "$tmp/rex/whoami.json"{,.good}; mv "$tmp/rex/whoami.json.bad" "$tmp/rex/whoami.json"
eq "rex: here falls back to the environment" "$(rx here)" "rex:s/env-block"
mv "$tmp/rex/whoami.json.good" "$tmp/rex/whoami.json"
eq "rex: agents are this tab's terminals with an agent in front" \
  "$(rx agents | jq -c '[.[] | [.ref, .agent, .pid]]')" '[["rex:s/agent","codex",7],["rex:s/node","claude",9]]'
eq "rex: info" "$(rx info rex:s/agent | jq -c '{ref, window, agent, cwd}')" \
  '{"ref":"rex:s/agent","window":"w1","agent":"codex","cwd":"/ws"}'
eq "rex: info on a shell has no agent" "$(rx info rex:s/shell | jq -c .agent)" null
mv "$tmp/rex/shell.json" "$tmp/rex/shell.gone"
out=$(rx agents 2>&1); code=$?
[[ $code != 0 ]] && ok "rex: a pane that can't be inspected fails the listing" || bad "rex: uninspectable pane" "$out"
has "rex: and says which" "$out" "Rex block shell"
mv "$tmp/rex/shell.gone" "$tmp/rex/shell.json"
: > "$tmp/rex/calls"
eq "rex: split prints the new ref" "$(rx split --label review --cwd /ws -- nvim -i NONE)" "rex:s/new-block"
has "rex: split from here, unfocused" "$(cat "$tmp/rex/calls")" \
  "split -s s -b nvim --split=right --ratio 50 --focus=false -c /ws --json --label review -- nvim -i NONE"
eq "rex: window" "$(rx window sub --keep-open -- claude hi)" "rex:s/tab-block"
has "rex: window args" "$(cat "$tmp/rex/calls")" "window new -s s sub --focus=false -c $PWD --json --keep-open -- claude hi"
: > "$tmp/rex/calls"
rx send rex:s/agent 'hello "there"'; rx key rex:s/agent enter ctrl+c
eq "rex: send and keys" "$(cat "$tmp/rex/calls")" $'send -s s -b agent hello "there"\nsend-key -s s -b agent enter\nsend-key -s s -b agent ctrl+c'
rx alive rex:s/agent && ok "rex: alive" || bad "rex: alive"
rx alive rex:s/gone 2> /dev/null && bad "rex: dead pane is alive" || ok "rex: dead pane isn't alive"
echo '{"records":[{"id":"bg1","state":"working"},{"state":"done","app":"claude-code"}]}' > "$tmp/rex/status.json"
eq "rex: status is the program's own record, not a child's" "$(rx status rex:s/status)" done
echo '{"records":[]}' > "$tmp/rex/status.json"
eq "rex: status without a record" "$(rx status rex:s/status)" none

out=$(env -u TMUX -u TMUX_PANE -u REX_SESSION -u REX_BLOCK "$pane" here 2>&1); code=$?
[[ $code != 0 ]] && has "none: here fails outside a multiplexer" "$out" "not in a Rex or tmux pane" || bad "none: here" "$out"
out=$("$pane" send bogus x 2>&1) && bad "bad ref accepted" || has "a bad ref is refused" "$out" "not a pane ref"

# ── tmux, real ───────────────────────────────────────────────────────────────
if ! command -v tmux > /dev/null; then echo "skip tmux: not installed"; else
  sock="$tmp/tmux.sock"
  tmux -S "$sock" -f /dev/null new-session -d -s t -x 200 -y 50 'sh'
  first=$(tmux -S "$sock" display -p -t t '#{pane_id}')
  tx() { env -u REX_SESSION -u REX_BLOCK TMUX="$sock,1,0" TMUX_PANE="$first" "$pane" "$@"; }
  mkdir -p "$tmp/fake/node_modules/@openai/codex/bin"
  printf '#!/bin/sh\nwhile :; do read -r x || sleep 1; done\n' > "$tmp/fake/claude"; chmod +x "$tmp/fake/claude"
  printf 'process.stdin.resume()\n' > "$tmp/fake/node_modules/@openai/codex/bin/codex.js"

  eq "tmux: backend" "$(tx backend)" tmux
  eq "tmux: here" "$(tx here)" "tmux:$first@$sock"
  a=$(tx split --label agent --cwd "$tmp" -- "$tmp/fake/claude")
  [[ "$a" =~ ^tmux:%[0-9]+@ ]] && ok "tmux: split prints a ref" || bad "tmux: split" "$a"
  eq "tmux: split doesn't take focus" "$(tmux -S "$sock" display -p '#{pane_id}')" "$first"
  s=$(tx split --dir below -- sh)
  c=""; command -v node > /dev/null && c=$(tx split -- node "$tmp/fake/node_modules/@openai/codex/bin/codex.js")
  tx window other -- "$tmp/fake/claude" > /dev/null
  sleep 1
  want="[\"$a\",\"claude\"]"; [[ -n "$c" ]] && want="$want,[\"$c\",\"codex\"]"
  eq "tmux: agents are this window's panes with an agent in front" \
    "$(tx agents | jq -c '[.[] | [.ref, .agent]] | sort_by(.[0]) | .[]' | paste -sd, -)" "$want"
  eq "tmux: info" "$(tx info "$a" | jq -c '{agent, label, cwd}')" "{\"agent\":\"claude\",\"label\":\"agent\",\"cwd\":\"$tmp\"}"
  eq "tmux: a shell isn't an agent" "$(tx info "$s" | jq -c .agent)" null
  eq "tmux: agents from another pane exclude that pane" "$(tx agents "$a" | jq -r '.[].ref' | grep -c "$a")" 0

  tx send "$s" 'echo "wt $((6*7))"'; tx key "$s" enter; sleep 0.5
  has "tmux: send pastes the text as it is, key presses Enter" "$(tx capture "$s")" "wt 42"
  tx alive "$a" && ok "tmux: alive" || bad "tmux: alive"
  tx alive "tmux:%999@$sock" && bad "tmux: dead pane is alive" || ok "tmux: dead pane isn't alive"
  tx status "$a" > /dev/null 2>&1; eq "tmux: no program status" "$?" 3
  tmux -S "$sock" kill-server
fi

(( fails == 0 )) && echo "wt-pane tests passed" || { echo "$fails failed"; exit 1; }
