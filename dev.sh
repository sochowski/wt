#!/usr/bin/env bash
# =============================================================================
#  wt dev sandbox  —  SOURCE this file:   source ./dev.sh
#
#  Lightweight inner-loop sandbox. Runs wt STRAIGHT FROM THIS CHECKOUT with all
#  state redirected into a throwaway dir and a private tmux socket. No install,
#  no symlinks, no hook merge — so it does NOT exercise install.sh or tmux/agent
#  integration (use ./staging.sh for that). Fast iteration on wt behaviour.
#
#  After sourcing:   wt ls / wt new / wt pick ...   run from ./bin
#                    tmux -L "$WT_DEV_SOCKET"       private isolated server
#                    wt-dev-reset                   wipe sandbox state
#                    wt-dev-off                     restore your shell
# =============================================================================

# Guard: must be sourced, not executed (otherwise exports vanish immediately).
if ! (return 0 2>/dev/null); then
    echo "dev.sh must be sourced:  source ./dev.sh" >&2
    exit 1
fi

# Guard: refuse to activate inside an existing tmux session. The sandbox is a
# PRIVATE server (tmux -L "$WT_DEV_SOCKET"); sourcing here inside your real
# session would leak TMUX_TMPDIR/WT_* into this shell — and a tmux server
# inherits the env of the shell that started it, so plain `tmux`/`wt` would
# then point at the sandbox socket and /tmp state, making real sessions vanish.
if [[ -n "${TMUX:-}" ]]; then
    echo "dev.sh: refusing to activate inside a tmux session (\$TMUX is set)." >&2
    echo "        The sandbox is a private server; sourcing here would leak" >&2
    echo "        TMUX_TMPDIR/WT_* into your real shell and hide live sessions." >&2
    echo "        Run this from a shell OUTSIDE tmux instead." >&2
    return 1
fi

_wt_dev_repo="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export WT_DEV_DIR="${WT_DEV_DIR:-${TMPDIR:-/tmp}/wt-dev}"
export WT_DEV_SOCKET="wt-dev"

# Build the Go state binary into the checkout's bin/ (gitignored).
if command -v go >/dev/null 2>&1; then
    ( cd "$_wt_dev_repo/state" && go build -o "$_wt_dev_repo/bin/wt-state" . ) \
        || echo "dev.sh: warning — wt-state build failed" >&2
fi

# Redirect every wt path into the sandbox.
export WT_BASE_DIR="$WT_DEV_DIR/worktrees"
export WT_STATUS_DIR="$WT_DEV_DIR/state"
export WT_CONFIG_DIR="$WT_DEV_DIR/config"
export WT_LOG_FILE="$WT_DEV_DIR/state/wt.log"
export WT_STATE="$_wt_dev_repo/bin/wt-state"
mkdir -p "$WT_BASE_DIR" "$WT_STATUS_DIR" "$WT_CONFIG_DIR"

# Private tmux server so `wt` never touches your real sessions.
export TMUX_TMPDIR="$WT_DEV_DIR/tmux"
mkdir -p "$TMUX_TMPDIR"

# Stub agent: shadow the real CLIs so sessions launch the no-op stub.
mkdir -p "$WT_DEV_DIR/stub-bin"
for _a in claude codex gemini opencode; do
    ln -sf "$_wt_dev_repo/staging/stub-agent" "$WT_DEV_DIR/stub-bin/$_a"
done
chmod +x "$_wt_dev_repo/staging/stub-agent"
export WT_DEFAULT_AGENT="opencode"   # any name works; all resolve to the stub

# PATH: checkout bin first (so `wt`/`wt-state`/`wt-agent-launch` come from here),
# then the stub agents. Recorded so wt-dev-off can undo it cleanly.
export _WT_DEV_PATH_SAVED="${_WT_DEV_PATH_SAVED:-$PATH}"
export PATH="$_wt_dev_repo/bin:$WT_DEV_DIR/stub-bin:$_WT_DEV_PATH_SAVED"

# Visible marker so a leaked/forgotten sandbox shell is obvious at a glance.
# Saved so wt-dev-off can restore the original prompt cleanly.
export _WT_DEV_PS1_SAVED="${_WT_DEV_PS1_SAVED-$PS1}"
export PS1="(wt-dev) ${_WT_DEV_PS1_SAVED}"

# Seed a throwaway repo so `wt new` has something to pick (wt scans $HOME/src etc,
# but here we just point you at it explicitly).
_wt_dev_demo="$WT_DEV_DIR/src/demo-repo"
if [[ ! -d "$_wt_dev_demo/.git" ]]; then
    mkdir -p "$_wt_dev_demo"
    git -C "$_wt_dev_demo" init -q -b main
    git -C "$_wt_dev_demo" commit -q --allow-empty -m "init"
    git -C "$_wt_dev_demo" remote add origin "$_wt_dev_demo" 2>/dev/null || true
    git -C "$_wt_dev_demo" symbolic-ref refs/remotes/origin/HEAD refs/remotes/origin/main 2>/dev/null || true
fi

wt-dev-reset() {
    tmux -L "$WT_DEV_SOCKET" kill-server 2>/dev/null || true
    rm -rf "$WT_STATUS_DIR" "$WT_BASE_DIR" "$TMUX_TMPDIR"
    mkdir -p "$WT_STATUS_DIR" "$WT_BASE_DIR" "$TMUX_TMPDIR"
    echo "wt dev sandbox reset (state, worktrees, tmux socket cleared)."
}

wt-dev-off() {
    tmux -L "$WT_DEV_SOCKET" kill-server 2>/dev/null || true
    [[ -n "${_WT_DEV_PATH_SAVED:-}" ]] && export PATH="$_WT_DEV_PATH_SAVED"
    [[ -n "${_WT_DEV_PS1_SAVED+x}" ]] && export PS1="$_WT_DEV_PS1_SAVED"
    unset WT_BASE_DIR WT_STATUS_DIR WT_CONFIG_DIR WT_LOG_FILE WT_STATE \
          WT_DEFAULT_AGENT TMUX_TMPDIR _WT_DEV_PATH_SAVED _WT_DEV_PS1_SAVED
    unset -f wt-dev-reset wt-dev-off 2>/dev/null || true
    echo "wt dev sandbox deactivated; shell restored."
}

cat <<EOF
wt dev sandbox active  →  running from $_wt_dev_repo/bin
  WT_BASE_DIR   = $WT_BASE_DIR
  WT_STATUS_DIR = $WT_STATUS_DIR
  WT_CONFIG_DIR = $WT_CONFIG_DIR
  agent         = stub (no real CLI launched)
  tmux server   = tmux -L $WT_DEV_SOCKET   (TMUX_TMPDIR=$TMUX_TMPDIR)
  demo repo     = $_wt_dev_demo

Try:  wt ls   |   wt new   |   tmux -L $WT_DEV_SOCKET attach
Undo: wt-dev-reset (wipe state)   |   wt-dev-off (restore shell)
EOF
