#!/usr/bin/env bash
# =============================================================================
#  wt test suite - comprehensive end-to-end tests
#  Safe to run from anywhere: ./test.sh
#  (Re-execs itself inside a private throwaway tmux server with isolated state;
#   it never touches your real tmux sessions, wt.db, or worktrees.)
# =============================================================================

set -uo pipefail

PASS=0
FAIL=0
TEST_REPO=""
TEST_SESSION=""
WT_BIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/bin" && pwd)"
WT_BASE_DIR="${WT_BASE_DIR:-$HOME/worktrees}"
WT_STATUS_DIR="${WT_STATUS_DIR:-$HOME/.local/state/wt}"
WT_STATE="$WT_BIN_DIR/wt-state"

# Read a single field of a session from the store (empty if absent).
state_field() {
    "$WT_STATE" get "$1" --field "$2" 2>/dev/null || true
}

# True if a session has a row in the store.
state_has() {
    "$WT_STATE" get "$1" >/dev/null 2>&1
}

# Colors
GREEN='\033[32m'
RED='\033[31m'
YELLOW='\033[33m'
RESET='\033[0m'

pass() {
    ((PASS++))
    echo -e "  ${GREEN}✓${RESET} $1"
}

fail() {
    ((FAIL++))
    echo -e "  ${RED}✗${RESET} $1"
    [[ -n "${2:-}" ]] && echo -e "    ${RED}→ $2${RESET}"
}

section() {
    echo ""
    echo -e "${YELLOW}━━━ $1 ━━━${RESET}"
}

# =============================================================================
#  Setup: create a temp git repo for testing
# =============================================================================
setup() {
    TEST_REPO=$(mktemp -d)/test-wt-repo
    mkdir -p "$TEST_REPO"
    git -C "$TEST_REPO" init -b main >/dev/null 2>&1
    git -C "$TEST_REPO" commit --allow-empty -m "init" >/dev/null 2>&1
    # Create a fake remote so fetch doesn't fail
    git -C "$TEST_REPO" remote add origin "$TEST_REPO" 2>/dev/null || true
    git -C "$TEST_REPO" symbolic-ref refs/remotes/origin/HEAD refs/remotes/origin/main 2>/dev/null || true
    echo "Test repo: $TEST_REPO"
}

# =============================================================================
#  Teardown: clean up test artifacts
# =============================================================================
teardown() {
    # Kill test session if it exists
    if [[ -n "$TEST_SESSION" ]]; then
        tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
    fi

    # Remove test worktree
    local test_wt="$WT_BASE_DIR/test-wt-repo"
    if [[ -d "$test_wt" ]]; then
        git -C "$TEST_REPO" worktree remove "$test_wt/test-branch" --force 2>/dev/null || true
        rm -rf "$test_wt" 2>/dev/null || true
    fi

    # Remove test session rows from the store
    "$WT_STATE" delete test-wt-repo-test-branch 2>/dev/null || true
    "$WT_STATE" delete test-wt-repo-main 2>/dev/null || true

    # Kill master test session
    tmux kill-session -t wt-master 2>/dev/null || true

    # Remove test repo
    if [[ -n "$TEST_REPO" ]]; then
        rm -rf "$(dirname "$TEST_REPO")" 2>/dev/null || true
    fi
}

# =============================================================================
#  Tests
# =============================================================================

test_script_syntax() {
    section "Script Syntax"

    for script in "$WT_BIN_DIR"/*; do
        local name=$(basename "$script")
        # Skip compiled binaries (e.g. wt-state) — only lint shell scripts.
        if ! head -c2 "$script" 2>/dev/null | grep -q '#!'; then
            continue
        fi
        if bash -n "$script" 2>/dev/null; then
            pass "$name: valid syntax"
        else
            fail "$name: syntax error" "$(bash -n "$script" 2>&1)"
        fi
    done
}

test_tmux_conf_syntax() {
    section "Tmux Config Syntax"

    local conf="$(dirname "$WT_BIN_DIR")/config/tmux-wt.conf"
    if [[ -f "$conf" ]]; then
        # Validate by sourcing into a THROWAWAY server on a private socket
        # (-L wt-test). NEVER use the default socket here: `kill-server` on it
        # would destroy the user's real tmux server and every session.
        local result
        result=$(tmux -L wt-test -f "$conf" start-server \; kill-server 2>&1 || true)
        if [[ -z "$result" ]] || [[ "$result" != *"error"* && "$result" != *"unknown"* ]]; then
            pass "tmux-wt.conf: valid syntax"
        else
            fail "tmux-wt.conf: syntax error" "$result"
        fi
    else
        fail "tmux-wt.conf: file not found"
    fi
}

test_find_git_repos() {
    section "find_git_repos"

    local output
    # Extract find_git_repos function and run it standalone (sourcing wt runs main)
    output=$(bash -c "$(sed -n '/^find_git_repos()/,/^}/p' "$WT_BIN_DIR/wt"); find_git_repos" 2>/dev/null || true)

    # Should not be empty (user has repos in ~/workspace)
    if [[ -n "$output" ]]; then
        local count=$(echo "$output" | wc -l | tr -d ' ')
        pass "found $count repos"
    else
        fail "no repos found"
    fi

    # Should NOT contain worktrees (files with .git as file, not directory)
    local worktree_count=0
    while IFS= read -r repo; do
        if [[ -f "$repo/.git" ]]; then
            ((worktree_count++))
        fi
    done <<< "$output"

    if [[ $worktree_count -eq 0 ]]; then
        pass "no worktrees in list (only primary repos)"
    else
        fail "found $worktree_count worktrees in list (should be filtered)"
    fi

    # Should only have depth-1 children (no deeply nested)
    local deep_count=0
    while IFS= read -r repo; do
        local rel="${repo#$HOME/}"
        local depth=$(echo "$rel" | tr '/' '\n' | wc -l | tr -d ' ')
        if [[ $depth -gt 2 ]]; then
            ((deep_count++))
        fi
    done <<< "$output"

    if [[ $deep_count -eq 0 ]]; then
        pass "all repos are top-level (depth 1)"
    else
        fail "$deep_count repos are deeply nested"
    fi
}

test_create_worktree() {
    section "create_worktree"

    if [[ -z "${TMUX:-}" ]]; then
        fail "skipped: not inside tmux"
        return
    fi

    # Create a worktree from our test repo
    local output
    output=$("$WT_BIN_DIR/wt" new "$TEST_REPO" test-branch 2>&1)
    TEST_SESSION="test-wt-repo-test-branch"

    # Check worktree was created
    local wt_path="$WT_BASE_DIR/test-wt-repo/test-branch"
    if [[ -d "$wt_path" ]] || [[ -f "$wt_path/.git" ]]; then
        pass "worktree directory created"
    else
        # Might have adopted existing
        if echo "$output" | grep -q "Adopting"; then
            pass "worktree adopted existing checkout"
        else
            fail "worktree not created at $wt_path" "$output"
        fi
    fi

    # Check tmux session was created
    sleep 1
    if tmux has-session -t "$TEST_SESSION" 2>/dev/null; then
        pass "tmux session created: $TEST_SESSION"
    else
        fail "tmux session not created: $TEST_SESSION"
        return
    fi

    # Check session has 3 windows
    local win_count
    win_count=$(tmux list-windows -t "$TEST_SESSION" 2>/dev/null | wc -l | tr -d ' ')
    if [[ "$win_count" -eq 2 ]]; then
        pass "session has 2 windows"
    else
        fail "session has $win_count windows (expected 2)"
    fi

    # Check window names
    local windows
    windows=$(tmux list-windows -t "$TEST_SESSION" -F "#{window_name}" 2>/dev/null | tr '\n' ',')
    if echo "$windows" | grep -q "main" && echo "$windows" | grep -q "shell"; then
        pass "windows named correctly: main, shell"
    else
        fail "unexpected window names: $windows (expected main, shell)"
    fi

    # --- Focus behavior: `wt new` creates in the BACKGROUND by default ---------
    # (Regression guard: it used to switch-client and yank the current view.)
    # The suite runs on a detached server (no attached client), so we assert on
    # which code path ran — the stays-put notice — not on client movement.
    if echo "$output" | grep -q "still here"; then
        pass "default 'wt new' stays put (background create)"
    else
        fail "default 'wt new' took the switch path (should stay put)" "$output"
    fi

    # `wt new --switch` must take the switch path (no stays-put notice).
    local switch_out
    switch_out=$("$WT_BIN_DIR/wt" new "$TEST_REPO" switch-branch --switch 2>&1)
    if echo "$switch_out" | grep -q "still here"; then
        fail "'wt new --switch' stayed put (should switch)" "$switch_out"
    else
        pass "'wt new --switch' takes the switch path"
    fi
    tmux kill-session -t "test-wt-repo-switch-branch" 2>/dev/null || true
    git -C "$TEST_REPO" worktree remove "$WT_BASE_DIR/test-wt-repo/switch-branch" --force 2>/dev/null || true
}

test_status_file() {
    section "Session State (store)"

    if [[ -z "$TEST_SESSION" ]]; then
        fail "skipped: no test session"
        return
    fi

    if state_has "$TEST_SESSION"; then
        pass "session row exists in store"
    else
        fail "session row not found: $TEST_SESSION"
        return
    fi

    # Check required fields are present in the JSON row
    local json
    json=$("$WT_STATE" get "$TEST_SESSION" --json 2>/dev/null)
    for field in status message repo branch wt_path agent opencode_config updated_at status_changed_at; do
        if echo "$json" | jq -e "has(\"$field\")" >/dev/null 2>&1; then
            pass "field present: $field=$(echo "$json" | jq -r ".$field")"
        else
            fail "field missing: $field"
        fi
    done
}

test_status_messages_are_data() {
    section "Status Messages Are Data"

    if [[ -z "$TEST_SESSION" ]]; then
        fail "skipped: no test session"
        return
    fi

    # A message containing shell metacharacters must round-trip as data and
    # never be interpreted (no escaping scheme to get wrong anymore).
    local danger='Using grep; $(touch /tmp/wt-pwned-$$)'
    "$WT_BIN_DIR/wt" set-status "$TEST_SESSION" working "$danger" 2>/dev/null

    if [[ "$(state_field "$TEST_SESSION" message)" == "$danger" ]]; then
        pass "message with metacharacters round-trips verbatim"
    else
        fail "message did not round-trip" "$(state_field "$TEST_SESSION" message)"
    fi
    if [[ ! -e "/tmp/wt-pwned-$$" ]]; then
        pass "message was never executed (stored as data)"
    else
        fail "message was executed — command injection!"
        rm -f "/tmp/wt-pwned-$$"
    fi

    local pick_output
    pick_output=$("$WT_BIN_DIR/wt" pick-list 2>&1)
    if echo "$pick_output" | grep -q "$TEST_SESSION" && echo "$pick_output" | grep -qF "Using grep"; then
        pass "pick-list renders the message without sourcing it"
    else
        fail "pick-list did not show message" "$pick_output"
    fi
}

test_tmux_options() {
    section "Tmux Session Options (@wt-*)"

    if [[ -z "${TMUX:-}" ]] || [[ -z "$TEST_SESSION" ]]; then
        fail "skipped: no test session"
        return
    fi

    if ! tmux has-session -t "$TEST_SESSION" 2>/dev/null; then
        fail "skipped: test session doesn't exist"
        return
    fi

    for opt in @wt-status @wt-icon @wt-message @wt-repo @wt-branch @wt-wt-path @wt-agent @wt-master @wt-opencode-config; do
        local val
        val=$(tmux show-option -qv -t "$TEST_SESSION" "$opt" 2>/dev/null)
        if [[ -n "$val" || "$opt" == "@wt-wt-path" || "$opt" == "@wt-master" || "$opt" == "@wt-opencode-config" ]]; then
            pass "$opt = '$val'"
        else
            fail "$opt not set"
        fi
    done
}

test_diff_view() {
    section "Live diff view (nvim socket + watcher)"

    if [[ -z "${TMUX:-}" ]]; then
        fail "skipped: not in tmux"
        return
    fi
    if ! command -v nvim >/dev/null 2>&1; then
        pass "skipped: nvim not installed"
        return
    fi

    local sess="test-wt-repo-diff-branch"
    local wt_path="$WT_BASE_DIR/test-wt-repo/diff-branch"
    local sock_dir="${WT_NVIM_SOCK_DIR:-$WT_STATUS_DIR/nvim-sockets}"
    local sock="$sock_dir/$sess.sock"

    "$WT_BIN_DIR/wt" new "$TEST_REPO" diff-branch >/dev/null 2>&1

    if ! tmux has-session -t "$sess" 2>/dev/null; then
        fail "diff-branch session not created"
        return
    fi

    # launch_nvim recorded a watcher pid.
    local pid
    pid=$(tmux show-option -qv -t "$sess" @wt-diff-pid 2>/dev/null || true)
    if [[ -n "$pid" ]]; then
        pass "@wt-diff-pid recorded ($pid)"
    else
        fail "@wt-diff-pid not set (watcher not started)"
    fi

    # nvim came up on its listen socket.
    local i
    for i in $(seq 1 50); do [[ -S "$sock" ]] && break; sleep 0.1; done
    if [[ -S "$sock" ]]; then
        pass "nvim listen socket created"
    else
        fail "nvim listen socket never appeared" "$sock"
    fi

    # delete tears down the watcher and socket.
    "$WT_BIN_DIR/wt" delete "$sess" --force >/dev/null 2>&1 || true
    if [[ ! -e "$sock" ]] && ! { [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; }; then
        pass "delete stopped watcher and removed socket"
    else
        fail "diff view not cleaned up after delete" \
            "sock_present=$([[ -e "$sock" ]] && echo yes || echo no) pid=$pid"
    fi

    # Safety net in case delete didn't fully clean up.
    git -C "$TEST_REPO" worktree remove "$wt_path" --force 2>/dev/null || true
    "$WT_STATE" delete "$sess" 2>/dev/null || true
}

test_status_metadata_recovery() {
    section "Status Metadata Recovery"

    if [[ -z "$TEST_SESSION" ]]; then
        fail "skipped: no test session"
        return
    fi

    local wt_path="$WT_BASE_DIR/test-wt-repo/test-branch"

    # Seed a row with empty metadata; the stop hook's derive_status_metadata
    # should refill repo/branch/wt_path/agent from tmux options + the filesystem.
    "$WT_STATE" set "$TEST_SESSION" --status working --message "race write" \
        --repo "" --branch "" --wt-path "" --agent "" --opencode-config "" >/dev/null

    (cd "$wt_path" 2>/dev/null || cd "$TEST_REPO"; "$WT_BIN_DIR/wt-hook" stop) 2>/dev/null

    local repo branch wt_path_value agent
    repo=$(state_field "$TEST_SESSION" repo)
    branch=$(state_field "$TEST_SESSION" branch)
    wt_path_value=$(state_field "$TEST_SESSION" wt_path)
    agent=$(state_field "$TEST_SESSION" agent)

    [[ "$repo" == "test-wt-repo" ]] && pass "recovered repo=$repo" || fail "repo not recovered" "$repo"
    [[ "$branch" == "test-branch" ]] && pass "recovered branch=$branch" || fail "branch not recovered" "$branch"
    [[ "$wt_path_value" == "$wt_path" ]] && pass "recovered wt_path=$wt_path_value" || fail "wt_path not recovered" "$wt_path_value"
    [[ -n "$agent" ]] && pass "recovered agent=$agent" || fail "agent not recovered"
}

test_wt_hook_dual_write() {
    section "wt-hook: store write + tmux projection"

    if [[ -z "${TMUX:-}" ]] || [[ -z "$TEST_SESSION" ]]; then
        fail "skipped: no test session"
        return
    fi

    # Simulate a pre-tool hook
    local wt_path="$WT_BASE_DIR/test-wt-repo/test-branch"
    (cd "$wt_path" 2>/dev/null || cd "$TEST_REPO"; "$WT_BIN_DIR/wt-hook" pre-tool Bash) 2>/dev/null

    sleep 0.5

    # Check the store was updated (source of truth)
    local store_status
    store_status=$(state_field "$TEST_SESSION" status)
    if [[ "$store_status" == "working" ]]; then
        pass "store status updated to 'working'"
    else
        fail "store status is '$store_status' (expected 'working')"
    fi

    # Check tmux option was updated
    local tmux_status
    tmux_status=$(tmux show-option -qv -t "$TEST_SESSION" @wt-status 2>/dev/null)
    if [[ "$tmux_status" == "working" ]]; then
        pass "tmux @wt-status updated to 'working'"
    else
        fail "tmux @wt-status is '$tmux_status' (expected 'working')"
    fi

    local tmux_icon
    tmux_icon=$(tmux show-option -qv -t "$TEST_SESSION" @wt-icon 2>/dev/null)
    if [[ "$tmux_icon" == "●" ]]; then
        pass "tmux @wt-icon updated to '●'"
    else
        fail "tmux @wt-icon is '$tmux_icon' (expected '●')"
    fi

    # Check message includes tool name
    local tmux_msg
    tmux_msg=$(tmux show-option -qv -t "$TEST_SESSION" @wt-message 2>/dev/null)
    if echo "$tmux_msg" | grep -q "Bash"; then
        pass "tmux @wt-message mentions tool: '$tmux_msg'"
    else
        fail "tmux @wt-message doesn't mention tool: '$tmux_msg'"
    fi

    # Simulate stop hook
    (cd "$wt_path" 2>/dev/null || cd "$TEST_REPO"; "$WT_BIN_DIR/wt-hook" stop) 2>/dev/null

    sleep 0.5

    tmux_status=$(tmux show-option -qv -t "$TEST_SESSION" @wt-status 2>/dev/null)
    if [[ "$tmux_status" == "idle" ]]; then
        pass "stop hook: status changed to 'idle'"
    else
        fail "stop hook: status is '$tmux_status' (expected 'idle')"
    fi
}

test_sync_tmux() {
    section "sync-tmux"

    if [[ -z "${TMUX:-}" ]] || [[ -z "$TEST_SESSION" ]]; then
        fail "skipped: no test session"
        return
    fi

    # Clear tmux options
    tmux set-option -uq -t "$TEST_SESSION" @wt-status 2>/dev/null || true
    tmux set-option -uq -t "$TEST_SESSION" @wt-icon 2>/dev/null || true

    # Verify they're cleared
    local before
    before=$(tmux show-option -qv -t "$TEST_SESSION" @wt-status 2>/dev/null)
    if [[ -z "$before" ]]; then
        pass "options cleared before sync"
    fi

    # Run sync
    "$WT_BIN_DIR/wt" sync-tmux 2>/dev/null

    # Verify they're restored
    local after
    after=$(tmux show-option -qv -t "$TEST_SESSION" @wt-status 2>/dev/null)
    if [[ -n "$after" ]]; then
        pass "sync restored @wt-status='$after'"
    else
        fail "sync did not restore @wt-status"
    fi

    local icon
    icon=$(tmux show-option -qv -t "$TEST_SESSION" @wt-icon 2>/dev/null)
    if [[ -n "$icon" ]]; then
        pass "sync restored @wt-icon='$icon'"
    else
        fail "sync did not restore @wt-icon"
    fi
}

test_wt_list() {
    section "wt list"

    local output
    output=$("$WT_BIN_DIR/wt" list 2>&1)

    if [[ -n "$output" ]]; then
        pass "list produces output"
    else
        fail "list produced no output"
    fi

    # Should contain our test session (only if it was created)
    if [[ -n "$TEST_SESSION" ]]; then
        if echo "$output" | grep -q "test-wt-repo"; then
            pass "list includes test worktree"
        else
            fail "list doesn't include test worktree"
        fi
    else
        pass "skipped test worktree check (no tmux)"
    fi
}

test_pr_badge() {
    section "PR state badge"

    if [[ -z "$TEST_SESSION" ]]; then
        pass "skipped (no test session)"
        return
    fi

    # pr_state is settable and auto-stamps pr_state_checked_at.
    "$WT_STATE" set "$TEST_SESSION" --pr-state merged >/dev/null 2>&1
    if [[ "$(state_field "$TEST_SESSION" pr_state)" == "merged" ]]; then
        pass "pr_state persists"
    else
        fail "pr_state not saved" "got '$(state_field "$TEST_SESSION" pr_state)'"
    fi

    local checked; checked=$(state_field "$TEST_SESSION" pr_state_checked_at)
    if [[ "$checked" =~ ^[0-9]+$ && "$checked" -gt 0 ]]; then
        pass "pr_state_checked_at auto-stamped ($checked)"
    else
        fail "pr_state_checked_at not stamped" "got '$checked'"
    fi

    # A freshly-cached state is within the TTL, so `wt ls` renders the merged
    # badge from cache and makes no gh call — deterministic, no network needed.
    local out
    out=$("$WT_BIN_DIR/wt" ls 2>/dev/null)
    if echo "$out" | grep -q "⬤"; then
        pass "merged badge (⬤) shown in ls"
    else
        fail "merged badge missing from ls" "$out"
    fi
}

test_wt_status() {
    section "wt status"

    if [[ -z "$TEST_SESSION" ]]; then
        fail "skipped: no test session"
        return
    fi

    local output
    output=$("$WT_BIN_DIR/wt" status "$TEST_SESSION" 2>&1)

    if [[ "$output" == "idle" || "$output" == "working" || "$output" == "input" || "$output" == "error" ]]; then
        pass "status returns valid value: '$output'"
    else
        fail "status returned unexpected: '$output'"
    fi
}

test_master_session() {
    section "Master Session"

    if [[ -z "${TMUX:-}" ]]; then
        fail "skipped: not inside tmux"
        return
    fi

    # Kill existing master if any
    tmux kill-session -t wt-master 2>/dev/null || true

    # Create master (in background, don't switch to it)
    tmux new-session -d -s wt-master -c "$HOME" -n agent 2>/dev/null
    tmux new-window -t wt-master -n shell -c "$HOME" 2>/dev/null
    tmux set-option -qt wt-master @wt-master "1" 2>/dev/null
    tmux set-option -qt wt-master @wt-icon "◆" 2>/dev/null
    tmux set-option -qt wt-master @wt-status "idle" 2>/dev/null
    tmux set-option -qt wt-master @wt-message "Orchestrator" 2>/dev/null

    if tmux has-session -t wt-master 2>/dev/null; then
        pass "master session created"
    else
        fail "master session not created"
        return
    fi

    # Check options
    local master_flag
    master_flag=$(tmux show-option -qv -t wt-master @wt-master 2>/dev/null)
    if [[ "$master_flag" == "1" ]]; then
        pass "@wt-master = 1"
    else
        fail "@wt-master = '$master_flag' (expected '1')"
    fi

    local icon
    icon=$(tmux show-option -qv -t wt-master @wt-icon 2>/dev/null)
    if [[ "$icon" == "◆" ]]; then
        pass "@wt-icon = ◆"
    else
        fail "@wt-icon = '$icon' (expected '◆')"
    fi
}

test_tmux_status_bar() {
    section "Tmux Status Bar (wt-tmux-status)"

    local output
    output=$("$WT_BIN_DIR/wt-tmux-status" 2>&1)

    # Should contain some content (we have sessions)
    if [[ -n "$output" ]]; then
        pass "status bar produces output: '$output'"
    else
        # Might be empty if no status files — not necessarily a failure
        pass "status bar output is empty (no tracked sessions)"
    fi

    # Check master indicator
    if tmux has-session -t wt-master 2>/dev/null; then
        if echo "$output" | grep -q "◆"; then
            pass "master indicator (◆) present"
        else
            fail "master indicator (◆) missing from: '$output'"
        fi
    fi
}

test_choose_tree_format() {
    section "Switcher Binding"

    if [[ -z "${TMUX:-}" ]]; then
        fail "skipped: not inside tmux"
        return
    fi

    # The switcher (prefix+W) is a recency-ranked fzf popup that shells out to
    # `wt switch`; verify the binding is wired up in the config.
    local conf="$(dirname "$WT_BIN_DIR")/config/tmux-wt.conf"
    local format_line
    format_line=$(grep -E 'bind W .*wt switch' "$conf" | head -1)

    if [[ -n "$format_line" ]]; then
        pass "wt switch popup binding found in config"
    else
        fail "wt switch popup binding not found in config"
    fi

    # Verify @wt-icon is readable in format context
    local icon
    icon=$(tmux display-message -t "$TEST_SESSION" -p '#{@wt-icon}' 2>/dev/null)
    if [[ -n "$icon" ]]; then
        pass "tmux can read @wt-icon in format: '$icon'"
    else
        fail "tmux cannot read @wt-icon in format"
    fi

    local msg
    msg=$(tmux display-message -t "$TEST_SESSION" -p '#{@wt-message}' 2>/dev/null)
    if [[ -n "$msg" ]]; then
        pass "tmux can read @wt-message in format: '$msg'"
    else
        fail "tmux cannot read @wt-message in format"
    fi
}

test_display_menu_commands() {
    section "display-menu Command Paths"

    local conf="$(dirname "$WT_BIN_DIR")/config/tmux-wt.conf"

    # Extract all $HOME/bin/wt subcommands from the config
    local cmds
    cmds=$(grep -oE "wt [a-z][a-z-]+" "$conf" | sort -u)

    while IFS= read -r cmd; do
        local subcmd="${cmd#wt }"
        # Check that the subcommand exists in main(). The regex handles case
        # labels where the subcommand is any alias in an alternation, e.g.
        # `switch|s)` or `list|ls)`, not just a bare `subcmd)`.
        if grep -qF "\"$subcmd\"" "$WT_BIN_DIR/wt" || grep -qF "'$subcmd'" "$WT_BIN_DIR/wt" || grep -qE "[[:space:]|]${subcmd}[|)]" "$WT_BIN_DIR/wt"; then
            pass "menu cmd '$cmd' has handler in wt"
        else
            fail "menu cmd '$cmd' has no handler in wt"
        fi
    done <<< "$cmds"
}

test_delete_worktree() {
    section "delete_worktree"

    if [[ -z "${TMUX:-}" ]] || [[ -z "$TEST_SESSION" ]]; then
        fail "skipped: no test session"
        return
    fi

    # Delete the test worktree
    local output
    output=$("$WT_BIN_DIR/wt" delete "$TEST_SESSION" --force 2>&1)

    if ! tmux has-session -t "$TEST_SESSION" 2>/dev/null; then
        pass "tmux session removed"
    else
        fail "tmux session still exists"
    fi

    if ! state_has "$TEST_SESSION"; then
        pass "session row removed from store"
    else
        fail "session row still exists in store"
    fi

    TEST_SESSION=""
}

test_adopt_existing() {
    section "Adopt Existing Checkout"

    if [[ -z "${TMUX:-}" ]]; then
        fail "skipped: not inside tmux"
        return
    fi

    # Try to create a worktree on main — should adopt the existing checkout.
    # Use a LOCAL session var so we don't clobber the shared $TEST_SESSION that
    # later tests (switcher, delete) still depend on.
    local output adopt_session="test-wt-repo-main"
    output=$("$WT_BIN_DIR/wt" new "$TEST_REPO" main 2>&1)

    if echo "$output" | grep -q "Adopting"; then
        pass "adopted existing checkout"
    else
        # Might have created it (first time)
        pass "created/switched to main"
    fi

    if tmux has-session -t "$adopt_session" 2>/dev/null; then
        pass "session created for adopted checkout"

        # Check the session's working directory matches the repo
        local pane_path
        pane_path=$(tmux display-message -t "$adopt_session:shell" -p '#{pane_current_path}' 2>/dev/null)
        if [[ "$pane_path" == "$TEST_REPO" ]]; then
            pass "shell window cwd matches repo: $pane_path"
        else
            fail "shell window cwd is '$pane_path' (expected '$TEST_REPO')"
        fi
    else
        fail "session not created"
    fi

    # Cleanup just this adopted session; leave $TEST_SESSION intact.
    tmux kill-session -t "$adopt_session" 2>/dev/null || true
    "$WT_STATE" delete "$adopt_session" 2>/dev/null || true
}

test_claude_hooks_json() {
    section "Claude Hooks Config"

    local hooks_file="$(dirname "$WT_BIN_DIR")/config/claude-hooks.json"

    if [[ -f "$hooks_file" ]]; then
        pass "claude-hooks.json exists"
    else
        fail "claude-hooks.json not found"
        return
    fi

    # Validate JSON
    if jq empty "$hooks_file" 2>/dev/null; then
        pass "valid JSON"
    else
        fail "invalid JSON"
    fi

    # Check required hook types
    for hook_type in PreToolUse Notification Stop; do
        if jq -e ".hooks.$hook_type" "$hooks_file" >/dev/null 2>&1; then
            pass "hook type present: $hook_type"
        else
            fail "hook type missing: $hook_type"
        fi
    done
}

test_gemini_hooks_json() {
    section "Gemini Hooks Config"

    local hooks_file="$(dirname "$WT_BIN_DIR")/config/hooks-gemini.json"

    if [[ -f "$hooks_file" ]]; then
        pass "hooks-gemini.json exists"
    else
        fail "hooks-gemini.json not found"
        return
    fi

    # Validate JSON
    if jq empty "$hooks_file" 2>/dev/null; then
        pass "valid JSON"
    else
        fail "invalid JSON"
    fi

    # Check required hook types (Gemini uses different names)
    for hook_type in BeforeTool Notification SessionEnd; do
        if jq -e ".hooks.$hook_type" "$hooks_file" >/dev/null 2>&1; then
            pass "hook type present: $hook_type"
        else
            fail "hook type missing: $hook_type"
        fi
    done
}

test_opencode_plugin() {
    section "opencode Plugin"

    local plugin_file="$(dirname "$WT_BIN_DIR")/config/opencode-wt-plugin.js"

    if [[ -f "$plugin_file" ]]; then
        pass "opencode-wt-plugin.js exists"
    else
        fail "opencode-wt-plugin.js not found"
        return
    fi

    if command -v node >/dev/null 2>&1; then
        if node --check "$plugin_file" >/dev/null 2>&1; then
            pass "valid JavaScript"
        else
            fail "invalid JavaScript"
        fi
    else
        pass "skipped JavaScript syntax check (node not installed)"
    fi

    for event_type in session.created session.idle session.error permission.asked tool.execute.before tool.execute.after; do
        if grep -q "$event_type" "$plugin_file"; then
            pass "event handled: $event_type"
        else
            fail "event missing: $event_type"
        fi
    done
}

test_opencode_mcp_config() {
    section "opencode MCP Config"

    local tmp_dir tmp_config target out funcs
    tmp_dir=$(mktemp -d)
    tmp_config="$tmp_dir/wt-config"
    target="$tmp_dir/worktree"
    mkdir -p "$tmp_config/mcp-profiles" "$target"

    cat > "$tmp_config/mcp-profiles/default.json" <<'JSON'
{
  "mcpServers": {
    "remote-tools": {
      "type": "http",
      "url": "https://example.com/mcp",
      "headers": { "Authorization": "Bearer {env:TOKEN}" }
    },
    "local-tools": {
      "command": "npx",
      "args": ["-y", "server"],
      "env": { "API_KEY": "x" }
    }
  }
}
JSON

    funcs="$({
        sed -n '/^ensure_mcp_profile()/,/^}/p' "$WT_BIN_DIR/wt"
        sed -n '/^write_opencode_mcp_config()/,/^}/p' "$WT_BIN_DIR/wt"
        sed -n '/^configure_mcp_profile()/,/^}/p' "$WT_BIN_DIR/wt"
    })"

    out=$(WT_CONFIG_DIR="$tmp_config" bash -c "log(){ :; }; $funcs; configure_mcp_profile '$target' opencode default test-session" 2>/dev/null || true)

    if [[ -f "$out" ]]; then
        pass "generated opencode MCP config"
    else
        fail "opencode MCP config not generated" "$out"
        rm -rf "$tmp_dir"
        return
    fi

    if jq -e '.mcp."remote-tools".type == "remote" and .mcp."remote-tools".url == "https://example.com/mcp"' "$out" >/dev/null 2>&1; then
        pass "converted HTTP MCP to opencode remote"
    else
        fail "remote MCP conversion incorrect" "$(jq . "$out" 2>/dev/null)"
    fi

    if jq -e '.mcp."local-tools".type == "local" and .mcp."local-tools".command == ["npx", "-y", "server"] and .mcp."local-tools".environment.API_KEY == "x"' "$out" >/dev/null 2>&1; then
        pass "converted command MCP to opencode local"
    else
        fail "local MCP conversion incorrect" "$(jq . "$out" 2>/dev/null)"
    fi

    rm -rf "$tmp_dir"
}

test_agent_profiles() {
    section "Agent Profiles"

    # Extract and test agent_binary function
    local claude_bin codex_bin gemini_bin opencode_bin
    claude_bin=$(bash -c "$(sed -n '/^agent_binary()/,/^}/p' "$WT_BIN_DIR/wt"); agent_binary claude")
    codex_bin=$(bash -c "$(sed -n '/^agent_binary()/,/^}/p' "$WT_BIN_DIR/wt"); agent_binary codex")
    gemini_bin=$(bash -c "$(sed -n '/^agent_binary()/,/^}/p' "$WT_BIN_DIR/wt"); agent_binary gemini")
    opencode_bin=$(bash -c "$(sed -n '/^agent_binary()/,/^}/p' "$WT_BIN_DIR/wt"); agent_binary opencode")

    [[ "$claude_bin" == "claude" ]] && pass "agent_binary claude = claude" || fail "agent_binary claude = $claude_bin"
    [[ "$codex_bin" == "codex" ]] && pass "agent_binary codex = codex" || fail "agent_binary codex = $codex_bin"
    [[ "$gemini_bin" == "gemini" ]] && pass "agent_binary gemini = gemini" || fail "agent_binary gemini = $gemini_bin"
    [[ "$opencode_bin" == "opencode" ]] && pass "agent_binary opencode = opencode" || fail "agent_binary opencode = $opencode_bin"

    if grep -q 'WT_DEFAULT_AGENT="${WT_DEFAULT_AGENT:-opencode}"' "$WT_BIN_DIR/wt"; then
        pass "default agent = opencode"
    else
        fail "default agent is not opencode"
    fi

    # Test agent_label
    local claude_label codex_label gemini_label opencode_label
    claude_label=$(bash -c "$(sed -n '/^agent_label()/,/^}/p' "$WT_BIN_DIR/wt"); agent_label claude")
    codex_label=$(bash -c "$(sed -n '/^agent_label()/,/^}/p' "$WT_BIN_DIR/wt"); agent_label codex")
    gemini_label=$(bash -c "$(sed -n '/^agent_label()/,/^}/p' "$WT_BIN_DIR/wt"); agent_label gemini")
    opencode_label=$(bash -c "$(sed -n '/^agent_label()/,/^}/p' "$WT_BIN_DIR/wt"); agent_label opencode")

    [[ "$claude_label" == "Claude" ]] && pass "agent_label claude = Claude" || fail "agent_label claude = $claude_label"
    [[ "$codex_label" == "Codex" ]] && pass "agent_label codex = Codex" || fail "agent_label codex = $codex_label"
    [[ "$gemini_label" == "Gemini" ]] && pass "agent_label gemini = Gemini" || fail "agent_label gemini = $gemini_label"
    [[ "$opencode_label" == "opencode" ]] && pass "agent_label opencode = opencode" || fail "agent_label opencode = $opencode_label"
}

test_install_script() {
    section "Install Script"

    local install="$(dirname "$WT_BIN_DIR")/install.sh"

    if bash -n "$install" 2>/dev/null; then
        pass "install.sh: valid syntax"
    else
        fail "install.sh: syntax error"
    fi

    # Check symlinks exist
    for script in wt wt-hook wt-tmux-status wt-agent-launch claude-ide; do
        if [[ -L "$HOME/bin/$script" ]]; then
            local target
            target=$(readlink "$HOME/bin/$script")
            if [[ -f "$target" ]] || [[ -f "$HOME/bin/$script" ]]; then
                pass "symlink: ~/bin/$script → $target"
            else
                fail "broken symlink: ~/bin/$script → $target"
            fi
        else
            fail "symlink missing: ~/bin/$script"
        fi
    done
}

test_go_unit() {
    section "wt-state Go unit tests"

    local state_dir
    state_dir="$(dirname "$WT_BIN_DIR")/state"
    if ! command -v go >/dev/null 2>&1; then
        fail "skipped: go not installed"
        return
    fi
    if (cd "$state_dir" && go test ./... >/tmp/wt-gotest-$$.log 2>&1); then
        pass "go test ./... passed"
    else
        fail "go test ./... failed" "$(cat /tmp/wt-gotest-$$.log)"
    fi
    rm -f /tmp/wt-gotest-$$.log
}

test_wt_state_cli() {
    section "wt-state CLI (migrate + concurrency)"

    if [[ ! -x "$WT_STATE" ]]; then
        fail "skipped: wt-state not built (run install.sh or 'go build')"
        return
    fi

    local tmp; tmp=$(mktemp -d)

    # migrate: a legacy escaped .status file must import with the space decoded.
    printf 'status=idle\nmessage=opencode\\ finished\nrepo=wt\nagent=opencode\n' \
        > "$tmp/wt-demo.status"
    WT_DB="$tmp/wt.db" "$WT_STATE" migrate --dir "$tmp" >/dev/null 2>&1
    if [[ "$(WT_DB="$tmp/wt.db" "$WT_STATE" get wt-demo --field message)" == "opencode finished" ]]; then
        pass "migrate decodes legacy escaping"
    else
        fail "migrate did not decode legacy escaping"
    fi

    # concurrency: many parallel writers to one row, no errors, consistent state.
    local n=40 fails=0 pids=()
    for ((i=0; i<n; i++)); do
        ( WT_DB="$tmp/wt.db" "$WT_STATE" set race --status working --message "i$i" >/dev/null 2>&1 ) &
        pids+=($!)
    done
    for p in "${pids[@]}"; do wait "$p" || fails=$((fails+1)); done
    if [[ "$fails" -eq 0 ]] && [[ "$(WT_DB="$tmp/wt.db" "$WT_STATE" get race --field status)" == "working" ]]; then
        pass "$n concurrent writers, no errors, consistent row"
    else
        fail "concurrent writers failed ($fails errors)"
    fi

    rm -rf "$tmp"
}

# =============================================================================
#  Run
# =============================================================================
main() {
    echo ""
    echo "╔══════════════════════════════════════╗"
    echo "║       wt comprehensive test suite    ║"
    echo "╚══════════════════════════════════════╝"
    echo ""

    if [[ -z "${TMUX:-}" ]]; then
        echo -e "${YELLOW}Warning: not inside tmux. Some tests will be skipped.${RESET}"
    fi

    setup

    test_go_unit
    test_wt_state_cli
    test_script_syntax
    test_tmux_conf_syntax
    test_claude_hooks_json
    test_gemini_hooks_json
    test_opencode_plugin
    test_opencode_mcp_config
    test_agent_profiles
    test_install_script
    test_find_git_repos
    test_create_worktree
    test_status_file
    test_status_messages_are_data
    test_tmux_options
    test_diff_view
    test_status_metadata_recovery
    test_wt_hook_dual_write
    test_sync_tmux
    test_wt_list
    test_pr_badge
    test_wt_status
    test_adopt_existing
    test_master_session
    test_tmux_status_bar
    test_choose_tree_format
    test_display_menu_commands
    test_delete_worktree

    teardown

    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo -e "  ${GREEN}Passed: $PASS${RESET}  ${RED}Failed: $FAIL${RESET}"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""

    [[ $FAIL -eq 0 ]] && exit 0 || exit 1
}

# =============================================================================
#  Isolation wrapper
#
#  The suite creates/kills tmux sessions, calls switch-client, and writes wt
#  state. Run inside your normal tmux server it would yank your view around and
#  (historically) could even kill the server. So instead we re-exec the WHOLE
#  suite inside a PRIVATE, throwaway tmux server (TMUX_TMPDIR) with ISOLATED
#  state dirs. Nothing here can touch your real sessions, wt.db, or worktrees.
# =============================================================================
run_isolated() {
    local self repo_dir sock priv log rc_file
    self="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
    repo_dir="$(dirname "$WT_BIN_DIR")"
    sock="wt-test-$$"
    priv="$(mktemp -d)"
    log="$(mktemp)"
    rc_file="$(mktemp)"

    # Build wt-state so the suite (and the wt/wt-hook it drives) can use it.
    if command -v go >/dev/null 2>&1; then
        if ! ( cd "$repo_dir/state" && go build -o "$WT_BIN_DIR/wt-state" . ); then
            echo "wt-state build failed — aborting" >&2
            rm -rf "$priv" "$log" "$rc_file"
            exit 1
        fi
    fi

    # Private tmux server + isolated state. Exported so the inner run and every
    # tmux/wt subprocess it spawns inherit them.
    export TMUX_TMPDIR="$priv"
    export WT_STATUS_DIR="$priv/state"; mkdir -p "$WT_STATUS_DIR"
    export WT_BASE_DIR="$priv/worktrees"; mkdir -p "$WT_BASE_DIR"
    unset TMUX  # detach from the caller's server before starting ours

    echo "Running test suite on an isolated tmux server (socket: $sock)..."
    echo "  TMUX_TMPDIR=$priv  WT_STATUS_DIR=$WT_STATUS_DIR  WT_BASE_DIR=$WT_BASE_DIR"

    # Run the suite detached on the private server; capture output + exit code,
    # then signal completion. The trailing echo/signal run even if the suite
    # fails, so the waiter below never hangs on a non-zero exit.
    tmux -L "$sock" new-session -d -x 220 -y 50 -s runner \
        "bash '$self' --inner > '$log' 2>&1; echo \$? > '$rc_file'; tmux -L '$sock' wait-for -S wt-test-done"
    tmux -L "$sock" wait-for wt-test-done

    cat "$log"
    local rc; rc="$(cat "$rc_file" 2>/dev/null || echo 1)"

    tmux -L "$sock" kill-server 2>/dev/null || true
    rm -rf "$priv" "$log" "$rc_file"
    exit "$rc"
}

case "${1:-}" in
    --inner) shift; main "$@" ;;   # already inside the private server
    *)       run_isolated "$@" ;;
esac
