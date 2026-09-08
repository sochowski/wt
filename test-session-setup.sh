#!/usr/bin/env bash
# Real keyboard popup acceptance; private HOME/state/tmux, local remotes/stubs.
# Run from a disposable source copy alongside test.sh. Keep artifacts for review.
set -euo pipefail
repo=$(cd "$(dirname "$0")" && pwd)
vim_mode=${WT_FZF_VIM:-0}
artifacts=${WT_SETUP_TEST_ARTIFACTS:-$(mktemp -d /tmp/wt-session-setup-test.XXXXXX)}
mkdir -p "$artifacts"
priv="$artifacts/sandbox"
export GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)"
for inherited in ${!WT_@}; do unset "$inherited"; done
unset TMUX TMUX_PANE
export HOME="$priv/home" TMUX_TMPDIR="$priv/tmux"
export XDG_CONFIG_HOME="$HOME/.config" XDG_STATE_HOME="$HOME/.local/state" XDG_CACHE_HOME="$HOME/.cache"
export WT_STATUS_DIR="$priv/state" WT_DB="$priv/state/wt.db"
export WT_BASE_DIR="$priv/worktrees" WT_CONFIG_DIR="$priv/config" WT_LOG_FILE="$priv/state/wt.log"
export WT_STATE="$priv/bin/wt-state" WT_SOURCE_CONFIG="$repo/config" WT_STUB_NATIVE=1 WT_DEFAULT_AGENT=pi
export WT_NVIM_SOCK_DIR="$priv/nvim" WT_SHELL_DIR="$priv/shells"
export WT_FZF_VIM="$vim_mode"
export WT_REPO_DIRS="$priv/repos" WT_PR_TTL=999999 TERM=xterm-256color
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_SYSTEM=/dev/null GIT_CONFIG_GLOBAL="$HOME/.gitconfig"
export GIT_CONFIG_COUNT=0 GIT_CONFIG_PARAMETERS=''
mkdir -p "$HOME/bin" "$HOME/.config/wt" "$TMUX_TMPDIR" "$WT_STATUS_DIR" "$priv/bin" "$priv/repos"
trap 'tmux -L default kill-server 2>/dev/null || true; echo "Artifacts: $artifacts"' EXIT
(cd "$repo/state" && go build -o "$WT_STATE" .)
# Trace state invocations to prove local keyboard filtering stays local.
export WT_TEST_STATE_REAL="$WT_STATE" WT_TEST_STATE_CALLS="$artifacts/state-calls.log"
cat > "$priv/bin/state-trace" <<'SH'
#!/usr/bin/env bash
printf '%q ' "$@" >> "$WT_TEST_STATE_CALLS"
printf '\n' >> "$WT_TEST_STATE_CALLS"
exec "$WT_TEST_STATE_REAL" "$@"
SH
chmod +x "$priv/bin/state-trace"
export WT_STATE="$priv/bin/state-trace"
for helper in "$repo"/bin/*; do [[ $(basename "$helper") == wt-state ]] || ln -s "$helper" "$HOME/bin/$(basename "$helper")"; done
ln -s "$WT_STATE" "$HOME/bin/wt-state"
for agent in claude codex gemini opencode pi; do ln -s "$repo/staging/stub-agent" "$priv/bin/$agent"; done
ln -s "$repo/staging/nvim-view-stub.js" "$priv/bin/nvim"
export PATH="$priv/bin:$HOME/bin:$PATH"
git config --global user.name 'wt setup acceptance'; git config --global user.email test@example.invalid
for pair in alpha:trunk beta:custom-default gamma:main; do
 name=${pair%%:*}; branch=${pair#*:}
 git init -q --bare -b "$branch" "$priv/$name.git"
 git clone -q "$priv/$name.git" "$priv/repos/$name"
 echo initial > "$priv/repos/$name/file.txt"
 git -C "$priv/repos/$name" add .; git -C "$priv/repos/$name" commit -qm initial
 git -C "$priv/repos/$name" push -qu origin "$branch"
 git -C "$priv/repos/$name" symbolic-ref refs/remotes/origin/HEAD "refs/remotes/origin/$branch"
done
# Originals contain distinct staged/unstaged changes before setup.
echo staged > "$priv/repos/alpha/file.txt"; git -C "$priv/repos/alpha" add .
echo dirty > "$priv/repos/alpha/file.txt"
git -C "$priv/repos/alpha" status --porcelain=v1 > "$artifacts/original-before.txt"
git -C "$priv/repos/alpha" write-tree > "$artifacts/index-before.txt"
# Include a manual-only source path containing shell/transport punctuation.
mv "$priv/repos/gamma" "$priv/manual : ' gamma"
cp "$repo/config/wt-menu.conf" "$HOME/.config/wt/wt-menu.conf"
tmux -L default -f /dev/null new-session -d -s sentinel -x 150 -y 45 'sleep 600'
"$repo/bin/wt-bind-menu"
python3 "$repo/staging/session-setup-keyboard.py" "$artifacts"
git -C "$priv/repos/alpha" status --porcelain=v1 > "$artifacts/original-after.txt"
git -C "$priv/repos/alpha" write-tree > "$artifacts/index-after.txt"
cmp "$artifacts/original-before.txt" "$artifacts/original-after.txt"
cmp "$artifacts/index-before.txt" "$artifacts/index-after.txt"
echo 'PASS session setup: real prefix+n/prefix+R/prefix+s keyboard, cancellation, multi-repo, manual path, stable conversations, dirty originals'
