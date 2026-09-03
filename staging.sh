#!/usr/bin/env bash
# =============================================================================
#  wt staging environment  —  EXECUTE this file:   ./staging.sh
#
#  Full end-to-end sandbox. Runs the REAL install.sh under a throwaway $HOME, so
#  it exercises the entire install path — symlinks, hook merge, tmux.conf
#  integration, the wt-state build — with ZERO contact with your real setup.
#  Then drops you into an isolated tmux server where you can drive wt for real.
#
#  Because the fake $HOME/bin/wt symlinks back to THIS checkout's bin/, editing
#  wt here is reflected in the sandbox immediately — no reinstall needed.
#
#    ./staging.sh          set up (idempotent) and attach
#    ./staging.sh --clean  tear the sandbox down (kill server, remove $HOME)
# =============================================================================

set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STAGE="${WT_STAGING_HOME:-${TMPDIR:-/tmp}/wt-staging}"
SOCKET="wt-staging"
STUB_BIN="$STAGE/stub-agents"

if [[ "${1:-}" == "--clean" ]]; then
    HOME="$STAGE" tmux -L "$SOCKET" kill-server 2>/dev/null || true
    # Go writes its module cache read-only; make the tree writable before rm.
    chmod -R u+w "$STAGE" 2>/dev/null || true
    rm -rf "$STAGE"
    echo "Staging sandbox removed: $STAGE"
    exit 0
fi

echo "Setting up wt staging under a fake HOME: $STAGE"
mkdir -p "$STAGE" "$STUB_BIN"

# --- Stub agents: shadow the real CLIs so install.sh detects "agents" and so
#     sessions launch the no-op stub instead of a real claude/codex/etc. -------
chmod +x "$REPO/staging/stub-agent"
for a in claude codex gemini opencode pi; do
    ln -sf "$REPO/staging/stub-agent" "$STUB_BIN/$a"
done

# --- Seed a throwaway repo so `wt new` has something to pick. wt scans
#     $HOME/src, $HOME/code, ... — put it where it will be found. -------------
DEMO="$STAGE/src/demo-repo"
if [[ ! -d "$DEMO/.git" ]]; then
    mkdir -p "$DEMO"
    git -C "$DEMO" init -q -b main
    git -C "$DEMO" commit -q --allow-empty -m "init"
    git -C "$DEMO" remote add origin "$DEMO" 2>/dev/null || true
    git -C "$DEMO" symbolic-ref refs/remotes/origin/HEAD refs/remotes/origin/main 2>/dev/null || true
fi

# --- Run the REAL installer under the fake HOME. Only HOME is redirected (so
#     everything install.sh writes lands under $STAGE); the ambient env is kept
#     so Go reuses your module/build caches instead of re-downloading. Stub
#     agents go first on PATH so agent detection passes without your real CLIs,
#     and any WT_* overrides (e.g. from a sourced dev.sh) are dropped so they
#     can't redirect the install's wt-state migrate. --------------------------
# Reuse your real Go module/build caches (GOPATH/GOCACHE otherwise follow the
# fake HOME, re-downloading modules and dropping a read-only cache in $STAGE).
GOMODCACHE="$(go env GOMODCACHE 2>/dev/null || true)"
GOCACHE="$(go env GOCACHE 2>/dev/null || true)"

echo "Running install.sh into the sandbox..."
env -u WT_BASE_DIR -u WT_STATUS_DIR -u WT_CONFIG_DIR -u WT_LOG_FILE \
    -u WT_STATE -u WT_DB -u WT_DEFAULT_AGENT -u TMUX -u TMUX_TMPDIR \
    HOME="$STAGE" PATH="$STUB_BIN:$PATH" \
    GOMODCACHE="$GOMODCACHE" GOCACHE="$GOCACHE" \
    bash "$REPO/install.sh"

cat <<EOF

Staging ready.  Everything below lives under $STAGE — nothing touched your real setup.
  fake HOME     = $STAGE
  wt binary     = $STAGE/bin/wt  ->  $REPO/bin/wt   (edits here apply live)
  state / db    = $STAGE/.local/state/wt
  hooks         = $STAGE/.claude/settings.json  (isolated copy)
                  $STAGE/.pi/agent/extensions/wt
  tmux config   = $STAGE/.tmux.conf             (isolated copy)
  demo repo     = $DEMO
  agent         = stub (no real CLI launched)

Reset just the data:  ./staging.sh --clean && ./staging.sh
EOF

# --- Launch an isolated tmux server inside the fake HOME and land in it. ------
# A tmux server's environment is sticky: sessions inherit whatever env the
# server was STARTED with, ignoring later clients. So we (a) pin every wt path
# explicitly — not just HOME — so runtime state can never fall back to your real
# ~/.local/state/wt or ~/worktrees, and (b) refuse to reuse a server we didn't
# start with these vars (a stale one would silently point at the wrong paths).
run() {
    env HOME="$STAGE" PATH="$STAGE/bin:$STUB_BIN:$PATH" \
        WT_DEFAULT_AGENT=opencode \
        WT_BASE_DIR="$STAGE/worktrees" \
        WT_STATUS_DIR="$STAGE/.local/state/wt" \
        WT_CONFIG_DIR="$STAGE/.config/wt" \
        WT_LOG_FILE="$STAGE/.local/state/wt/wt.log" \
        "$@"
}

# If a server is already up but wasn't started with our sandbox env, it's stale
# (e.g. left over from a previous $STAGE) — kill it so we start clean.
if run tmux -L "$SOCKET" has-session 2>/dev/null; then
    server_base="$(run tmux -L "$SOCKET" show-environment -g WT_BASE_DIR 2>/dev/null | cut -d= -f2-)"
    if [[ "$server_base" != "$STAGE/worktrees" ]]; then
        echo "Killing stale staging server (env pointed at: ${server_base:-unknown})"
        run tmux -L "$SOCKET" kill-server 2>/dev/null || true
    fi
fi

if ! run tmux -L "$SOCKET" has-session -t main 2>/dev/null; then
    run tmux -L "$SOCKET" new-session -d -s main -c "$DEMO"
fi

if [[ -t 1 ]]; then
    echo ""
    echo "Attaching to the staging tmux server (prefix + w for the wt menu)..."
    exec run tmux -L "$SOCKET" attach -t main
else
    echo ""
    echo "Not a TTY. Drive it non-interactively, e.g.:"
    echo "  ./staging.sh   # from a real terminal to attach"
fi
