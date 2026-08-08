#!/usr/bin/env bash
# Run this checkout's mobile UI against a disposable wt/tmux environment.
# Nothing is installed and no default wt paths or tmux sockets are used.

set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SANDBOX="${WT_MOBILE_DEV_DIR:-${TMPDIR:-/tmp}/wt-mobile-dev-${USER:-user}}"
FAKE_HOME="$SANDBOX/home"
STATE_DIR="$SANDBOX/state"
BASE_DIR="$SANDBOX/worktrees"
CONFIG_DIR="$SANDBOX/config"
TMUX_DIR="$SANDBOX/tmux"
STUB_DIR="$SANDBOX/stub-bin"
STATE_BIN="$SANDBOX/bin/wt-state"
DEMO_REPO="$FAKE_HOME/src/demo-repo"
DEMO_SESSION="demo-repo-mobile-demo"

case "$SANDBOX" in
    /tmp/wt-mobile-dev-*|/private/tmp/wt-mobile-dev-*) ;;
    *)
        echo "Refusing unsafe WT_MOBILE_DEV_DIR: $SANDBOX" >&2
        echo "Use a path beginning with /tmp/wt-mobile-dev-." >&2
        exit 2
        ;;
esac

mobile_env() {
    env -u TMUX \
        HOME="$FAKE_HOME" \
        PATH="$REPO/bin:$STUB_DIR:$PATH" \
        TMUX_TMPDIR="$TMUX_DIR" \
        WT_BASE_DIR="$BASE_DIR" \
        WT_STATUS_DIR="$STATE_DIR" \
        WT_CONFIG_DIR="$CONFIG_DIR" \
        WT_LOG_FILE="$STATE_DIR/wt.log" \
        WT_STATE="$STATE_BIN" \
        WT_DEFAULT_AGENT=opencode \
        WT_REPO_DIRS="$DEMO_REPO" \
        WT_DIFF_VIEW=0 \
        "$@"
}

if [[ "${1:-}" == "--clean" ]]; then
    if [[ -d "$SANDBOX" ]]; then
        mkdir -p "$TMUX_DIR"
        mobile_env tmux kill-server 2>/dev/null || true
        chmod -R u+w "$SANDBOX" 2>/dev/null || true
        rm -rf -- "$SANDBOX"
    fi
    echo "Removed mobile sandbox: $SANDBOX"
    exit 0
fi

mkdir -p "$FAKE_HOME/src" "$STATE_DIR" "$BASE_DIR" "$CONFIG_DIR" \
    "$TMUX_DIR" "$STUB_DIR" "$SANDBOX/bin"

# Build the state binary inside the sandbox, reusing the caller's Go caches so
# fake HOME does not trigger downloads or create a second module cache.
GOMODCACHE_REAL=$(go env GOMODCACHE 2>/dev/null || true)
GOCACHE_SANDBOX="$SANDBOX/go-cache"
mkdir -p "$GOCACHE_SANDBOX"
if [[ ! -x "$STATE_BIN" ]] \
    || [[ -n "$(find "$REPO/state" -name '*.go' -newer "$STATE_BIN" -print -quit 2>/dev/null)" ]]; then
    (
        cd "$REPO/state"
        env HOME="$FAKE_HOME" GOMODCACHE="$GOMODCACHE_REAL" GOCACHE="$GOCACHE_SANDBOX" \
            go build -o "$STATE_BIN" .
    )
fi

# Real agent CLIs and Neovim are shadowed. The sandbox can never launch a paid
# agent, load credentials, or open the checkout's editor configuration.
for command_name in claude codex gemini opencode nvim; do
    ln -sf "$REPO/staging/stub-agent" "$STUB_DIR/$command_name"
done
chmod +x "$REPO/staging/stub-agent"

if [[ ! -d "$DEMO_REPO/.git" ]]; then
    mkdir -p "$DEMO_REPO"
    git -C "$DEMO_REPO" init -q -b main
    printf '# Mobile demo\n' > "$DEMO_REPO/README.md"
    git -C "$DEMO_REPO" add README.md
    git -C "$DEMO_REPO" -c user.name=wt -c user.email=wt@example.invalid \
        commit -q -m "Initial demo"
    git -C "$DEMO_REPO" remote add origin "$DEMO_REPO"
    git -C "$DEMO_REPO" symbolic-ref refs/remotes/origin/HEAD refs/remotes/origin/main
fi

if ! mobile_env tmux has-session -t "=$DEMO_SESSION" 2>/dev/null; then
    mobile_env "$REPO/bin/wt" new "$DEMO_REPO" mobile-demo --agent opencode >/dev/null
    DEMO_WORKTREE="$BASE_DIR/demo-repo/mobile-demo"
    printf '%s\n' \
        '# Phone review' \
        '' \
        'This untracked file demonstrates the mobile Changes view.' \
        > "$DEMO_WORKTREE/mobile-notes.md"

    # Leave the stub agent at a realistic text prompt so reply/send can be
    # exercised without starting any real agent CLI.
    printf -v demo_command \
        'printf "\\nMobile demo: should this change be kept?\\n"; IFS= read -r reply; printf "Received: %%s\\n" "$reply"; %q set-status %q idle %q' \
        "$REPO/bin/wt" "$DEMO_SESSION" "Demo reply received"
    sleep 0.3
    mobile_env "$REPO/bin/wt" session send "$DEMO_SESSION" --text "$demo_command" --enter
    mobile_env "$REPO/bin/wt" set-status "$DEMO_SESSION" input "Waiting for a demo reply" >/dev/null
fi

if [[ -t 1 && "${1:-}" != "--once" ]]; then
    printf 'Mobile sandbox: %s\n' "$SANDBOX"
    printf 'No installed wt files, live tmux sessions, or real agents are used.\n\n'
fi

exec env -u TMUX \
    HOME="$FAKE_HOME" \
    PATH="$REPO/bin:$STUB_DIR:$PATH" \
    TMUX_TMPDIR="$TMUX_DIR" \
    WT_BASE_DIR="$BASE_DIR" \
    WT_STATUS_DIR="$STATE_DIR" \
    WT_CONFIG_DIR="$CONFIG_DIR" \
    WT_LOG_FILE="$STATE_DIR/wt.log" \
    WT_STATE="$STATE_BIN" \
    WT_DEFAULT_AGENT=opencode \
    WT_REPO_DIRS="$DEMO_REPO" \
    WT_DIFF_VIEW=0 \
    "$REPO/bin/wt" mobile "$@"
