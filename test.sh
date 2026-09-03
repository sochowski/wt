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

    for script in "$WT_BIN_DIR"/* "$(dirname "$WT_BIN_DIR")/mobile-dev.sh"; do
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
    local pane_conf="$(dirname "$WT_BIN_DIR")/config/tmux-panes.conf"
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

    if [[ -f "$pane_conf" ]]; then
        local pane_result
        pane_result=$(tmux -L wt-pane-test -f /dev/null start-server \
            \; source-file "$pane_conf" \; kill-server 2>&1 || true)
        if [[ -z "$pane_result" ]] || [[ "$pane_result" != *"error"* && "$pane_result" != *"unknown"* ]]; then
            pass "tmux-panes.conf: valid syntax"
        else
            fail "tmux-panes.conf: syntax error" "$pane_result"
        fi
    else
        fail "tmux-panes.conf: file not found"
    fi
}

test_find_git_repos() {
    section "find_git_repos"

    # Build a hermetic search tree so the test doesn't depend on the real home:
    #   scanroot/               <- itself a primary repo (dotfiles-style)
    #   scanroot/childrepo/.git <- primary repo child
    #   scanroot/childtree/.git <- worktree (.git file), must be skipped
    #   scanroot/notarepo/      <- plain dir, must be skipped
    local scanroot; scanroot=$(mktemp -d)/scanroot
    mkdir -p "$scanroot/.git" "$scanroot/childrepo/.git" \
             "$scanroot/childtree" "$scanroot/notarepo"
    echo "gitdir: /elsewhere" > "$scanroot/childtree/.git"

    local output
    # Extract find_git_repos function and run it standalone (sourcing wt runs
    # main), passing WT_REPO_DIRS explicitly since the standalone shell has no
    # config block.
    output=$(WT_REPO_DIRS="$scanroot" bash -c "$(sed -n '/^find_git_repos()/,/^}/p' "$WT_BIN_DIR/wt"); find_git_repos" 2>/dev/null || true)

    if [[ -n "$output" ]]; then
        local count=$(echo "$output" | wc -l | tr -d ' ')
        pass "found $count repos"
    else
        fail "no repos found"
    fi

    # The search dir itself is a primary repo, so it must be included.
    if echo "$output" | grep -qx "$scanroot"; then
        pass "search dir included when it is itself a repo"
    else
        fail "search dir not included when it is itself a repo"
    fi

    # A primary-repo child must be included.
    if echo "$output" | grep -qx "$scanroot/childrepo"; then
        pass "primary-repo child included"
    else
        fail "primary-repo child not included"
    fi

    # A plain non-repo child must not appear.
    if echo "$output" | grep -qx "$scanroot/notarepo"; then
        fail "non-repo child should not be listed"
    else
        pass "non-repo child skipped"
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

    # Repos must be either the search dir itself or one of its immediate
    # children — never more deeply nested.
    local deep_count=0
    while IFS= read -r repo; do
        [[ -n "$repo" ]] || continue
        local rel="${repo#$scanroot}"
        rel="${rel#/}"
        # rel is "" (the search dir) or a single path component (a child).
        if [[ "$rel" == */* ]]; then
            ((deep_count++))
        fi
    done <<< "$output"

    if [[ $deep_count -eq 0 ]]; then
        pass "all repos are top-level (search dir or immediate children)"
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

    # Worktree sessions start with only the fixed editor + agent window. Shells
    # are created on demand through `wt shell` / prefix+c.
    local win_count
    win_count=$(tmux list-windows -t "$TEST_SESSION" 2>/dev/null | wc -l | tr -d ' ')
    if [[ "$win_count" -eq 1 ]]; then
        pass "session starts with only the main window"
    else
        fail "session has $win_count windows (expected 1)"
    fi

    # Check window names
    local windows
    windows=$(tmux list-windows -t "$TEST_SESSION" -F "#{window_name}" 2>/dev/null | tr '\n' ',')
    if [[ "$windows" == "main," ]]; then
        pass "only main window is created by default"
    else
        fail "unexpected default windows: $windows (expected main)"
    fi

    local editor_role agent_role
    editor_role=$(tmux show-option -pqv -t "$TEST_SESSION:main.0" @wt-pane-role 2>/dev/null)
    agent_role=$(tmux show-option -pqv -t "$TEST_SESSION:main.1" @wt-pane-role 2>/dev/null)
    [[ "$editor_role" == editor && "$agent_role" == agent ]] \
        && pass "primary panes have semantic editor/agent roles" \
        || fail "primary pane roles missing" "editor=$editor_role agent=$agent_role"

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

test_mobile_session_control() {
    section "Mobile session control"

    if [[ -z "${TMUX:-}" || -z "$TEST_SESSION" ]]; then
        fail "skipped: no isolated test session"
        return
    fi

    local json pane capture payload command wt_path diff_files diff_output
    local mobile_once mobile_switch_binding picker_source
    json=$("$WT_BIN_DIR/wt" session list 2>&1)
    if jq -e --arg name "$TEST_SESSION" \
        '.[] | select(.name == $name and .live == true and .effective_status != "offline")' \
        <<<"$json" >/dev/null 2>&1; then
        pass "session list exposes live mobile metadata"
    else
        fail "session list omitted the live test session" "$json"
    fi

    pane=$("$WT_BIN_DIR/wt" session pane "$TEST_SESSION" 2>/dev/null || true)
    if [[ "$pane" == %* ]]; then
        pass "session pane resolves the semantic agent pane"
    else
        fail "session pane did not resolve an agent pane" "$pane"
        return
    fi

    picker_source=$(sed -n '/^session_picker_rows()/,/^}/p; /^session_picker()/,/^}/p; /^mobile_mode()/,/^}/p' "$WT_BIN_DIR/wt")
    if grep -Fq 'selection=$(session_picker all true)' "$WT_BIN_DIR/wt" \
        && grep -Fq 'pick_worktree' <<<"$picker_source" \
        && grep -Fq "preview_window='down,50%,border-top,wrap'" <<<"$picker_source" \
        && [[ ! -e "$WT_BIN_DIR/wt-mobile" ]]; then
        pass "mobile and prefix+s share the responsive session picker"
    else
        fail "mobile still has a separate session-menu implementation" "$picker_source"
    fi

    if grep -Fq 'session_picker_rows "$scope" | fzf' <<<"$picker_source" \
        && ! grep -Fq 'rows=$(pick_list)' <<<"$picker_source"; then
        pass "shared picker starts fzf concurrently with candidate generation"
    else
        fail "shared picker buffers candidates before starting fzf" "$picker_source"
    fi

    local pane_conf mobile_bindings
    pane_conf="$(dirname "$WT_BIN_DIR")/config/tmux-panes.conf"
    tmux source-file "$pane_conf"
    mobile_bindings=$(tmux list-keys -T prefix 2>/dev/null | grep -E ' prefix (h|j|k|l) ' || true)
    if grep -Fq 'no-detach-on-destroy' <<<"$mobile_bindings" \
        && grep -Fq 'select-pane -L' <<<"$mobile_bindings" \
        && grep -Fq 'select-pane -D' <<<"$mobile_bindings" \
        && grep -Fq 'select-pane -U' <<<"$mobile_bindings" \
        && grep -Fq 'select-pane -R' <<<"$mobile_bindings" \
        && grep -Fq 'resize-pane -Z' <<<"$mobile_bindings"; then
        pass "mobile prefix+h/j/k/l bindings select direction and zoom"
    else
        fail "mobile directional fullscreen bindings are incomplete" "$mobile_bindings"
    fi

    if grep -Fq 'select-pane -t \"{next}\"' <<<"$mobile_bindings" \
        && grep -Fq 'select-pane -t \"{previous}\"' <<<"$mobile_bindings" \
        && grep -Fq 'resize-pane -t \"{top-left}\" -L 3' <<<"$mobile_bindings" \
        && grep -Fq 'resize-pane -t \"{top-left}\" -R 3' <<<"$mobile_bindings"; then
        pass "desktop prefix+h/j/k/l fallbacks retain focus and resize behavior"
    else
        fail "mobile bindings changed the desktop pane fallbacks" "$mobile_bindings"
    fi

    # Replace the isolated agent process with a controlled shell so capture and
    # literal-paste behavior are deterministic and never invoke a real agent.
    tmux respawn-pane -k -t "$pane" "bash --noprofile --norc"
    sleep 0.1
    tmux send-keys -t "$pane" -l 'printf "mobile-capture-ok\n"'
    tmux send-keys -t "$pane" Enter
    sleep 0.1
    capture=$("$WT_BIN_DIR/wt" session capture "$TEST_SESSION" --lines 30 --plain 2>&1)
    if grep -Fq "mobile-capture-ok" <<<"$capture"; then
        pass "session capture reads recent agent output"
    else
        fail "session capture missed pane output" "$capture"
    fi
    payload='hello "quotes" $HOME ; still-text'
    printf -v command 'printf %q %q' 'mobile-reply=<%s>\n' "$payload"
    printf '%s' "$command" | "$WT_BIN_DIR/wt" session send "$TEST_SESSION" --enter
    sleep 0.1
    capture=$("$WT_BIN_DIR/wt" session capture "$TEST_SESSION" --lines 30 --plain 2>&1)
    if grep -Fq "mobile-reply=<$payload>" <<<"$capture"; then
        pass "session send pastes quotes and shell syntax literally"
    else
        fail "session send changed literal input" "$capture"
    fi

    if "$WT_BIN_DIR/wt" session key "$TEST_SESSION" definitely-not-a-key >/dev/null 2>&1; then
        fail "session key accepted an unsupported key"
    else
        pass "session key rejects unsupported key names"
    fi

    wt_path="$WT_BASE_DIR/test-wt-repo/test-branch"
    printf 'mobile diff content\n' > "$wt_path/mobile-api.txt"
    diff_files=$("$WT_BIN_DIR/wt" session diff "$TEST_SESSION" --files 2>&1)
    diff_output=$("$WT_BIN_DIR/wt" session diff "$TEST_SESSION" --file mobile-api.txt --no-color 2>&1)
    if grep -Fq $'?\tmobile-api.txt' <<<"$diff_files" \
        && grep -Fq "mobile diff content" <<<"$diff_output"; then
        pass "shared picker diff includes untracked file contents"
    else
        fail "session diff did not render the untracked test file" "$diff_files / $diff_output"
    fi
    rm -f "$wt_path/mobile-api.txt"

    mobile_once=$(COLUMNS=48 "$WT_BIN_DIR/wt" mobile --once 2>&1)
    if grep -Fq "test-wt-repo@test-branch" <<<"$mobile_once"; then
        pass "mobile --once emits the shared picker rows"
    else
        fail "mobile did not reuse shared picker rows" "$mobile_once"
    fi

    mobile_switch_binding=$(tmux list-keys -T prefix 2>/dev/null \
        | grep -E ' prefix s +if-shell ' || true)
    if grep -Fq 'no-detach-on-destroy' <<<"$mobile_switch_binding" \
        && grep -Fq "$WT_BIN_DIR/wt" <<<"$mobile_switch_binding" \
        && grep -Fq ' pick' <<<"$mobile_switch_binding"; then
        pass "mobile prefix+s opens the same checkout picker"
    else
        fail "mobile prefix+s is not wired to the shared picker" "$mobile_switch_binding"
    fi
}

test_managed_shells() {
    section "Managed Shells"

    local wt=("$WT_BIN_DIR/wt" shell --session "$TEST_SESSION")
    local wt_path="$WT_BASE_DIR/test-wt-repo/test-branch"

    local shell_new_output
    if shell_new_output=$("${wt[@]}" new scratch --detach 2>&1); then
        pass "shell new creates a detached managed shell"
    else
        fail "shell new failed" "$shell_new_output"
        return
    fi

    local json cwd managed role
    json=$("${wt[@]}" ls --json 2>/dev/null)
    if jq -e '.[] | select(.name == "scratch" and .kind == "interactive")' <<< "$json" >/dev/null; then
        pass "shell ls --json exposes stable metadata"
    else
        fail "scratch missing from shell list" "$json"
    fi
    cwd=$(tmux display-message -p -t "$TEST_SESSION:scratch" '#{pane_current_path}' 2>/dev/null)
    managed=$(tmux show-option -wqv -t "$TEST_SESSION:scratch" @wt-managed-shell 2>/dev/null)
    role=$(tmux show-option -pqv -t "$TEST_SESSION:scratch" @wt-pane-role 2>/dev/null)
    [[ "$cwd" == "$wt_path" && "$managed" == 1 && "$role" == shell ]] \
        && pass "managed shell uses worktree cwd and tmux metadata" \
        || fail "managed shell metadata/cwd wrong" "cwd=$cwd managed=$managed role=$role"

    "${wt[@]}" send scratch --text 'printf "shell-send-ok\\n"' >/dev/null
    "${wt[@]}" send scratch --key Enter >/dev/null
    if "${wt[@]}" wait scratch --match shell-send-ok --timeout 5 >/dev/null 2>&1; then
        pass "shell send and wait --match work"
    else
        fail "shell send/wait did not observe output" "$("${wt[@]}" read scratch --lines 30 2>&1)"
    fi

    local clean raw
    clean=$("${wt[@]}" read scratch --lines 30 2>/dev/null)
    raw=$("${wt[@]}" read scratch --lines 30 --raw 2>/dev/null)
    if [[ "$clean" == *shell-send-ok* && "$clean" != *'printf "shell-send-ok'* && "$raw" == *'printf "shell-send-ok'* ]]; then
        pass "normalized reads hide sent command echoes; --raw preserves them"
    else
        fail "normalized/raw output projection incorrect" "clean=$clean raw=$raw"
    fi

    local first second
    first=$("${wt[@]}" read scratch --new 2>/dev/null)
    second=$("${wt[@]}" read scratch --new 2>/dev/null)
    [[ "$first" == *shell-send-ok* && -z "$second" ]] \
        && pass "persistent log supports unread cursor" \
        || fail "unread cursor did not advance" "first=$first second=$second"

    "${wt[@]}" run job -- sh -c 'sleep 1; printf "job-ready\\n"; exit 7' >/dev/null 2>&1
    local early_rc=0
    "${wt[@]}" wait job --match job-ready --timeout 0 >/dev/null 2>&1 || early_rc=$?
    [[ "$early_rc" -eq 124 ]] \
        && pass "wait --match ignores the echoed launch command" \
        || fail "wait matched command echo before process output" "rc=$early_rc"
    if "${wt[@]}" wait job --match job-ready --timeout 5 >/dev/null 2>&1; then
        pass "shell run output is persistently readable"
    else
        fail "shell run output missing" "$("${wt[@]}" read job --lines 30 2>&1)"
    fi
    local rc=0
    "${wt[@]}" wait job --exit --timeout 5 >/dev/null 2>&1 || rc=$?
    [[ "$rc" -eq 7 ]] && pass "shell wait --exit returns command status" || fail "wait --exit status = $rc (expected 7)"

    if "${wt[@]}" wait scratch --quiet 1s --timeout 5 >/dev/null 2>&1; then
        pass "shell wait --quiet observes a stable output period"
    else
        fail "shell wait --quiet timed out"
    fi

    "${wt[@]}" run stopper -- sh -c 'trap "exit 130" INT; while :; do sleep 1; done' >/dev/null 2>&1
    sleep 0.2
    "${wt[@]}" stop stopper >/dev/null 2>&1
    rc=0
    "${wt[@]}" wait stopper --exit --timeout 5 >/dev/null 2>&1 || rc=$?
    [[ "$rc" -eq 130 ]] && pass "shell stop interrupts a running command" || fail "stopper exit status = $rc (expected 130)"

    "${wt[@]}" watch scratch --match watched-failure >/dev/null 2>&1
    if grep -q 'tmux run-shell -b' "$WT_BIN_DIR/wt"; then
        pass "watcher is launched and owned by the tmux server"
    else
        fail "watcher is not tmux-owned"
    fi
    "${wt[@]}" send scratch --text 'printf "watched-failure\\n"' >/dev/null
    "${wt[@]}" send scratch --key Enter >/dev/null
    local events="" i
    for i in $(seq 1 30); do
        events=$("${wt[@]}" events --json 2>/dev/null)
        jq -e '.[] | select(.shell == "scratch" and .kind == "output_match")' <<< "$events" >/dev/null && break
        sleep 0.2
    done
    # Give a second chunk/cooldown cycle time to surface accidental duplicates.
    sleep 1
    events=$("${wt[@]}" events --json 2>/dev/null)
    local matching_events
    matching_events=$(jq '[.[] | select(.shell == "scratch" and .kind == "output_match")] | length' <<< "$events")
    if [[ "$matching_events" -eq 1 ]] \
        && jq -e '.[] | select(.excerpt == "watched-failure")' <<< "$events" >/dev/null 2>&1; then
        pass "shell watch queues one normalized, echo-free event"
    else
        fail "watch event missing, duplicated, or noisy" "$events"
    fi

    "${wt[@]}" run failjob -- sh -c 'sleep 0.5; exit 9' >/dev/null 2>&1
    "${wt[@]}" watch failjob --on exit-failure >/dev/null 2>&1
    rc=0
    "${wt[@]}" wait failjob --exit --timeout 5 >/dev/null 2>&1 || rc=$?
    events=""
    for i in $(seq 1 30); do
        events=$("${wt[@]}" events --json 2>/dev/null)
        jq -e '.[] | select(.shell == "failjob" and .kind == "exit_failure")' <<< "$events" >/dev/null && break
        sleep 0.2
    done
    [[ "$rc" -eq 9 ]] && jq -e '.[] | select(.shell == "failjob" and .excerpt == "Exited with status 9")' <<< "$events" >/dev/null 2>&1 \
        && pass "exit-failure watch records the command status" \
        || fail "exit-failure watch missing" "rc=$rc events=$events"

    "${wt[@]}" events --consume >/dev/null 2>&1
    [[ "$("${wt[@]}" events --json 2>/dev/null)" == "[]" ]] \
        && pass "shell events --consume clears the queue" \
        || fail "event queue was not consumed"

    "${wt[@]}" rm scratch >/dev/null 2>&1
    "${wt[@]}" rm job >/dev/null 2>&1
    "${wt[@]}" rm stopper >/dev/null 2>&1
    "${wt[@]}" rm failjob >/dev/null 2>&1
    [[ "$("${wt[@]}" ls --json 2>/dev/null)" == "[]" ]] \
        && pass "shell rm cleans up managed windows" \
        || fail "managed shells remain after rm"
}

test_wt_shells_skill() {
    section "wt-shells Agent Skill"

    local skill_dir="$(dirname "$WT_BIN_DIR")/config/skills/wt-shells"
    if [[ -f "$skill_dir/SKILL.md" ]] \
        && grep -q '^name: wt-shells$' "$skill_dir/SKILL.md" \
        && grep -q '^description:' "$skill_dir/SKILL.md"; then
        pass "wt-shells has valid required skill metadata"
    else
        fail "wt-shells skill metadata missing"
    fi

    if [[ -f "$skill_dir/agents/openai.yaml" ]] \
        && grep -q 'display_name: "WT Managed Shells"' "$skill_dir/agents/openai.yaml"; then
        pass "wt-shells includes Codex UI metadata"
    else
        fail "wt-shells openai.yaml missing"
    fi

    local install="$(dirname "$WT_BIN_DIR")/install.sh"
    local path
    for path in '.agents/skills/wt-shells' '.claude/skills/wt-shells' '.gemini/skills/wt-shells'; do
        grep -q "$path" "$install" \
            && pass "installer wires $path" \
            || fail "installer missing $path"
    done
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
    for field in status message repo branch wt_path agent opencode_config kind workspace_path updated_at status_changed_at; do
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

    for opt in @wt-status @wt-icon @wt-message @wt-repo @wt-branch @wt-wt-path @wt-agent @wt-master @wt-opencode-config @wt-session; do
        local val
        val=$(tmux show-option -qv -t "$TEST_SESSION" "$opt" 2>/dev/null)
        if [[ -n "$val" || "$opt" == "@wt-wt-path" || "$opt" == "@wt-master" || "$opt" == "@wt-opencode-config" ]]; then
            pass "$opt = '$val'"
        else
            fail "$opt not set"
        fi
    done

    local session_env
    session_env=$(tmux show-environment -t "=$TEST_SESSION" WT_SESSION 2>/dev/null || true)
    if [[ "$session_env" == "WT_SESSION=$TEST_SESSION" ]]; then
        pass "tmux session environment carries WT_SESSION=$TEST_SESSION"
    else
        fail "tmux WT_SESSION identity missing" "$session_env"
    fi
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

    # Agent adapters such as Pi use the semantic protocol: native session
    # identity changes independently of working/idle activity.
    WT_SESSION="$TEST_SESSION" "$WT_BIN_DIR/wt-hook" session-id pi-native-123 2>/dev/null
    if [[ "$(state_field "$TEST_SESSION" agent_session_id)" == "pi-native-123" ]]; then
        pass "semantic session-id records native conversation"
    else
        fail "semantic session-id was not recorded" "$(state_field "$TEST_SESSION" agent_session_id)"
    fi

    WT_SESSION="$TEST_SESSION" "$WT_BIN_DIR/wt-hook" working "Pi working" 2>/dev/null
    local semantic_status semantic_message
    semantic_status=$(state_field "$TEST_SESSION" status)
    semantic_message=$(state_field "$TEST_SESSION" message)
    if [[ "$semantic_status" == "working" && "$semantic_message" == "Pi working" ]]; then
        pass "semantic working status preserves adapter message"
    else
        fail "semantic working status incorrect" "status=$semantic_status message=$semantic_message"
    fi

    WT_SESSION="$TEST_SESSION" "$WT_BIN_DIR/wt-hook" idle "Pi finished" 2>/dev/null
    semantic_status=$(state_field "$TEST_SESSION" status)
    semantic_message=$(state_field "$TEST_SESSION" message)
    if [[ "$semantic_status" == "idle" && "$semantic_message" == "Pi finished" ]]; then
        pass "semantic idle status preserves adapter message"
    else
        fail "semantic idle status incorrect" "status=$semantic_status message=$semantic_message"
    fi
}

test_wt_hook_stable_session_identity() {
    section "wt-hook: stable session identity"

    if [[ -z "$TEST_SESSION" ]]; then
        fail "skipped: no test session"
        return
    fi

    # A future workspace agent can run tools outside its original checkout.
    # The exported identity must keep those hook writes on the owning session.
    local outside
    outside=$(mktemp -d)
    (cd "$outside" && WT_SESSION="$TEST_SESSION" "$WT_BIN_DIR/wt-hook" pre-tool CrossRepo) 2>/dev/null

    local status message
    status=$(state_field "$TEST_SESSION" status)
    message=$(state_field "$TEST_SESSION" message)
    if [[ "$status" == "working" && "$message" == "Using CrossRepo" ]]; then
        pass "WT_SESSION routes hooks independently of cwd"
    else
        fail "stable hook identity did not update owning session" "status=$status message=$message"
    fi

    # An agent that predates this feature cannot inherit a newly set tmux env
    # variable. Run a hook in a target-session pane with WT_SESSION explicitly
    # removed; the @wt-session/tracked-tmux fallback must still route it.
    local pane command i
    pane=$(tmux new-window -d -P -F '#{pane_id}' -t "$TEST_SESSION" \
        -n hook-identity -c "$outside")
    printf -v command 'unset WT_SESSION; %q pre-tool LegacyCrossRepo' "$WT_BIN_DIR/wt-hook"
    tmux send-keys -t "$pane" -l "$command"
    tmux send-keys -t "$pane" Enter
    for i in $(seq 1 20); do
        message=$(state_field "$TEST_SESSION" message)
        [[ "$message" == "Using LegacyCrossRepo" ]] && break
        sleep 0.1
    done
    tmux kill-window -t "$TEST_SESSION:hook-identity" 2>/dev/null || true
    if [[ "$message" == "Using LegacyCrossRepo" ]]; then
        pass "tracked tmux fallback routes already-running agents"
    else
        fail "tracked tmux fallback did not route hook" "$message"
    fi
    rm -rf "$outside"
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

    # Regression: an empty pr/opencode_config must not collapse when the store
    # row is split, which previously shifted every column left and landed
    # is_master ("0") in @wt-agent (breaking agent launch/resume). Set a known
    # agent with those fields empty, sync, and require @wt-agent verbatim.
    "$WT_STATE" set "$TEST_SESSION" --agent claude --pr "" --opencode-config "" >/dev/null 2>&1
    "$WT_BIN_DIR/wt" sync-tmux 2>/dev/null
    local synced_agent
    synced_agent=$(tmux show-option -qv -t "$TEST_SESSION" @wt-agent 2>/dev/null)
    if [[ "$synced_agent" == "claude" ]]; then
        pass "sync mapped @wt-agent='claude' with empty pr/opencode_config"
    else
        fail "sync @wt-agent='$synced_agent', want 'claude' (empty-field column shift?)"
    fi

    # Regression: a junk store row whose name is a PREFIX of a live session
    # (e.g. "wt-" from a detached-HEAD hook) must not clobber that session's
    # options — tmux -t resolves prefixes, so sync's writes for the junk name
    # landed on the real session (@wt-agent=opencode on a claude session).
    local junk_row="${TEST_SESSION%-*}"   # a strict prefix of TEST_SESSION
    "$WT_STATE" set "$junk_row" --agent opencode --status idle >/dev/null 2>&1
    "$WT_BIN_DIR/wt" sync-tmux 2>/dev/null
    synced_agent=$(tmux show-option -qv -t "$TEST_SESSION" @wt-agent 2>/dev/null)
    "$WT_STATE" delete "$junk_row" >/dev/null 2>&1
    if [[ "$synced_agent" == "claude" ]]; then
        pass "junk prefix row '$junk_row' did not clobber live session options"
    else
        fail "junk prefix row clobbered @wt-agent='$synced_agent', want 'claude'"
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

    if grep -q 'WT_MASTER_TUI_CMD="${WT_MASTER_TUI_CMD-linear-curses}"' "$WT_BIN_DIR/wt"; then
        pass "default master TUI = linear-curses"
    else
        fail "default master TUI is not linear-curses"
    fi

    if [[ -z "${TMUX:-}" ]]; then
        fail "skipped: not inside tmux"
        return
    fi

    # Kill existing master if any
    tmux kill-session -t wt-master 2>/dev/null || true

    # Create the master layout directly on the private test server. Use the
    # production pane helper with a harmless shell builtin; never launch a real
    # TUI or agent from the regression suite.
    tmux new-session -d -s wt-master -c "$HOME" -n agent 2>/dev/null
    local helper agent_pane tui_pane
    helper=$(sed -n '/^create_master_tui_pane()/,/^}/p' "$WT_BIN_DIR/wt")
    eval "$helper"
    agent_pane=$(tmux display-message -p -t wt-master:agent '#{pane_id}')
    tmux set-option -pqt "$agent_pane" @wt-pane-role agent
    tui_pane=$(WT_MASTER_TUI_CMD="printf 'master-tui-ready\\n'" \
        WT_MASTER_TUI_WIDTH=40 WT_MASTER_TUI_CWD="$HOME" \
        create_master_tui_pane wt-master "$agent_pane" "$HOME")
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

    local pane_count tui_role agent_role tui_left agent_left
    pane_count=$(tmux list-panes -t wt-master:agent | wc -l | tr -d ' ')
    tui_role=$(tmux show-option -pqv -t "$tui_pane" @wt-pane-role 2>/dev/null)
    agent_role=$(tmux show-option -pqv -t "$agent_pane" @wt-pane-role 2>/dev/null)
    tui_left=$(tmux display-message -p -t "$tui_pane" '#{pane_left}')
    agent_left=$(tmux display-message -p -t "$agent_pane" '#{pane_left}')
    if [[ "$pane_count" == "2" && "$tui_role" == "tui" && "$agent_role" == "agent" \
        && "$tui_left" -lt "$agent_left" ]]; then
        pass "master layout is TUI left, agent right with semantic roles"
    else
        fail "master split layout is incorrect" \
            "panes=$pane_count tui_role=$tui_role agent_role=$agent_role left=$tui_left/$agent_left"
    fi

    local before disabled after
    before="$pane_count"
    disabled=$(WT_MASTER_TUI_CMD= WT_MASTER_TUI_WIDTH=50 WT_MASTER_TUI_CWD="$HOME" \
        create_master_tui_pane wt-master "$agent_pane" "$HOME")
    after=$(tmux list-panes -t wt-master:agent | wc -l | tr -d ' ')
    if [[ -z "$disabled" && "$before" == "$after" ]]; then
        pass "empty WT_MASTER_TUI_CMD disables the TUI pane"
    else
        fail "empty WT_MASTER_TUI_CMD created a pane" "before=$before after=$after output=$disabled"
    fi

    # Session-control commands must resolve the semantic agent pane, not pane 0
    # (which is now the TUI).
    "$WT_STATE" set wt-master --is-master 1 --status idle --message Orchestrator \
        --agent test >/dev/null 2>&1
    local resolved
    resolved=$("$WT_BIN_DIR/wt" session pane wt-master 2>/dev/null || true)
    if [[ "$resolved" == "$agent_pane" ]]; then
        pass "master session control resolves the right-side agent pane"
    else
        fail "master session control resolved '$resolved'" "expected $agent_pane"
    fi
    "$WT_STATE" delete wt-master >/dev/null 2>&1 || true

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

    if grep -q 'bind c if-shell' "$conf" && grep -q 'wt shell --session' "$conf"; then
        pass "prefix+c conditionally creates a managed shell"
    else
        fail "conditional managed-shell binding missing"
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
        pane_path=$(tmux display-message -t "$adopt_session:main" -p '#{pane_current_path}' 2>/dev/null)
        if [[ "$pane_path" == "$TEST_REPO" ]]; then
            pass "main window cwd matches repo: $pane_path"
        else
            fail "main window cwd is '$pane_path' (expected '$TEST_REPO')"
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

test_pi_extension() {
    section "Pi Extension"

    local extension_dir="$(dirname "$WT_BIN_DIR")/config/pi-wt"
    local extension_file="$extension_dir/index.js"
    if [[ -f "$extension_file" ]]; then
        pass "pi-wt extension exists"
    else
        fail "pi-wt extension not found"
        return
    fi

    if ! command -v node >/dev/null 2>&1; then
        pass "skipped Pi extension runtime test (node not installed)"
        return
    fi
    if node --check "$extension_file" >/dev/null 2>&1; then
        pass "Pi extension has valid JavaScript"
    else
        fail "Pi extension has invalid JavaScript" "$(node --check "$extension_file" 2>&1)"
        return
    fi

    # Exercise the extension without invoking Pi or touching real wt state. A
    # mock ExtensionAPI delivers native events to a stub WT_HOOK, and the copy
    # gets an .mjs suffix so Node loads the dependency-free source as ESM.
    local tmp; tmp=$(mktemp -d)
    cp "$extension_file" "$tmp/index.mjs"
    cat > "$tmp/wt-hook" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$WT_TEST_PI_EVENTS"
SH
    chmod +x "$tmp/wt-hook"
    : > "$tmp/events"

    WT_SESSION=wt-pi-test WT_HOOK="$tmp/wt-hook" WT_TEST_PI_EVENTS="$tmp/events" \
        node --input-type=module - "$tmp/index.mjs" "$tmp" <<'JS'
import { pathToFileURL } from "node:url"

const extensionPath = process.argv[2]
const cwd = process.argv[3]
const handlers = new Map()
const pi = { on: (name, handler) => handlers.set(name, handler) }
const { default: extension } = await import(pathToFileURL(extensionPath))
extension(pi)

let idle = true
const ctx = {
  cwd,
  isIdle: () => idle,
  sessionManager: { getSessionId: () => "pi-session-123" },
}
await handlers.get("session_start")({ type: "session_start", reason: "startup" }, ctx)
idle = false
await handlers.get("agent_start")({ type: "agent_start" }, ctx)
await handlers.get("tool_execution_start")({ type: "tool_execution_start", toolName: "bash" }, ctx)
await handlers.get("ui_prompt_start")({ type: "ui_prompt_start", kind: "confirm", title: "Approve?" }, ctx)
await handlers.get("ui_prompt_end")({ type: "ui_prompt_end", kind: "confirm", title: "Approve?" }, ctx)
idle = true
await handlers.get("agent_settled")({ type: "agent_settled" }, ctx)
await handlers.get("session_shutdown")({ type: "session_shutdown", reason: "quit" }, ctx)
JS
    local node_rc=$?

    cat > "$tmp/expected" <<'EOF'
session-id pi-session-123
idle Pi ready
working Pi working
working Using bash
input Waiting: Approve?
working Pi working
idle Pi finished
idle Pi stopped
EOF
    if [[ $node_rc -eq 0 ]] && cmp -s "$tmp/expected" "$tmp/events"; then
        pass "Pi lifecycle maps to semantic wt-hook events"
    else
        fail "Pi lifecycle event mapping failed" "$(diff -u "$tmp/expected" "$tmp/events" 2>&1 || true)"
    fi

    # The extension is installed globally but must not register handlers for a
    # Pi process that was not launched by wt.
    env -u WT_SESSION WT_HOOK="$tmp/wt-hook" node --input-type=module - "$tmp/index.mjs" <<'JS'
import { pathToFileURL } from "node:url"
const handlers = []
const { default: extension } = await import(pathToFileURL(process.argv[2]))
extension({ on: (name) => handlers.push(name) })
if (handlers.length !== 0) process.exit(1)
JS
    if [[ $? -eq 0 ]]; then
        pass "Pi extension stays inert outside wt"
    else
        fail "Pi extension registered outside a wt launch"
    fi

    # Exercise the real registry installer against a fake HOME with only a Pi
    # stub on PATH. The target must be the auto-discovered extension directory.
    local stub_bin="$tmp/bin"
    mkdir -p "$stub_bin"
    ln -s "$(dirname "$WT_BIN_DIR")/staging/stub-agent" "$stub_bin/pi"
    PATH="$stub_bin:/usr/bin:/bin" "$WT_STATE" agents install-hooks \
        --template-dir "$(dirname "$WT_BIN_DIR")/config" \
        --home "$tmp/home" --state-dir "$tmp/state" >/dev/null 2>&1
    local installed="$tmp/home/.pi/agent/extensions/wt"
    if [[ -L "$installed" && "$(readlink -f "$installed")" == "$(readlink -f "$extension_dir")" ]]; then
        pass "installer links Pi extension into fake HOME"
    else
        fail "Pi extension install target is missing or incorrect" "$installed"
    fi

    rm -rf "$tmp"
}

test_opencode_mcp_config() {
    section "opencode MCP Config"

    local tmp_dir tmp_config target out
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

    # session-setup is the real path bin/wt drives: seed .mcp.json from the
    # profile and generate the opencode config. It prints the config path.
    out=$("$WT_STATE" agent opencode session-setup \
            --dir "$target" --session test-session \
            --config-dir "$tmp_config" --profile default 2>/dev/null || true)

    if [[ -n "$out" && -f "$out" ]]; then
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

    # Binary lookup comes from the wt-state registry now.
    local a bin
    for a in claude codex gemini opencode pi; do
        bin=$("$WT_STATE" agent "$a" --field binary 2>/dev/null)
        [[ "$bin" == "$a" ]] && pass "agent binary $a = $a" || fail "agent binary $a = $bin"
    done

    # The full roster is listed in registry order.
    local list
    list=$("$WT_STATE" agents list 2>/dev/null | tr '\n' ' ')
    if [[ "$list" == *claude* && "$list" == *codex* && "$list" == *gemini* && "$list" == *opencode* && "$list" == *pi* ]]; then
        pass "agents list includes all five"
    else
        fail "agents list = $list"
    fi

    # Unknown agents fall back to their own name as the binary.
    local unk
    unk=$("$WT_STATE" agent frobnicate --field binary 2>/dev/null)
    [[ "$unk" == "frobnicate" ]] && pass "unknown agent binary = name" || fail "unknown agent binary = $unk"

    if grep -q 'WT_DEFAULT_AGENT="${WT_DEFAULT_AGENT:-opencode}"' "$WT_BIN_DIR/wt"; then
        pass "default agent = opencode"
    else
        fail "default agent is not opencode"
    fi

    # An agent's name is its display string — there is no separate label.
    local name
    name=$("$WT_STATE" agent claude --field name 2>/dev/null)
    [[ "$name" == "claude" ]] && pass "agent name is display label" || fail "agent name = $name"
}

# Verify the launch plan the registry emits for each agent.
test_agent_launch_plan() {
    section "Agent Launch Plan"

    # Claude with no IDE locks → bare binary, no --ide.
    local plan
    plan=$("$WT_STATE" agent claude launch-plan --sh --home "$(mktemp -d)" 2>/dev/null)
    if grep -q "WT_LAUNCH_BINARY='claude'" <<<"$plan" && grep -q 'WT_LAUNCH_ARGS=()' <<<"$plan"; then
        pass "claude launch-plan: bare binary when no IDE lock"
    else
        fail "claude launch-plan unexpected" "$plan"
    fi

    # Unknown agent → exec its own name directly.
    plan=$("$WT_STATE" agent frobnicate launch-plan --sh 2>/dev/null)
    grep -q "WT_LAUNCH_BINARY='frobnicate'" <<<"$plan" \
        && pass "unknown agent launch-plan execs its name" \
        || fail "unknown launch-plan unexpected" "$plan"

    # Pi launches directly and revives an exact captured conversation.
    plan=$("$WT_STATE" agent pi launch-plan --resume \
        --resume-session-id pi-session-123 --sh 2>/dev/null)
    if grep -q "WT_LAUNCH_BINARY='pi'" <<<"$plan" \
        && grep -q "WT_LAUNCH_ARGS=('--session' 'pi-session-123')" <<<"$plan"; then
        pass "Pi launch-plan resumes exact native session"
    else
        fail "Pi launch-plan unexpected" "$plan"
    fi
}

# Guard the hook wiring: tool data arrives on stdin, not via a made-up env var,
# and installs are surgical + idempotent + versioned.
test_hook_format_and_install() {
    section "Hook Format & Install"

    local cfg_dir="$(dirname "$WT_BIN_DIR")/config"

    # The $CLAUDE_TOOL_NAME / $TOOL_NAME env vars don't exist — commands must not
    # reference them (wt-hook parses tool_name from stdin JSON instead).
    if grep -q 'CLAUDE_TOOL_NAME\|\$TOOL_NAME' "$cfg_dir/claude-hooks.json" "$cfg_dir/hooks-gemini.json" 2>/dev/null; then
        fail "hook templates still reference a bogus tool-name env var"
    else
        pass "hook templates pass no bogus tool-name env var"
    fi

    # wt-hook reads tool_name from stdin JSON (regression guard for the fix).
    if grep -q 'tool_name' "$WT_BIN_DIR/wt-hook"; then
        pass "wt-hook parses tool_name from stdin"
    else
        fail "wt-hook does not parse tool_name from stdin"
    fi

    # Surgical + idempotent install: seed a settings file with a foreign hook and
    # a legacy wt entry; install twice; expect the legacy entry gone, the foreign
    # one kept, and exactly one wt entry (no duplicates).
    local hd; hd=$(mktemp -d)
    mkdir -p "$hd/.claude"
    cat > "$hd/.claude/settings.json" <<'JSON'
{ "model": "x", "hooks": { "PreToolUse": [
  {"matcher":"","hooks":[{"type":"command","command":"/opt/foreign/hook"}]},
  {"matcher":"","hooks":[{"type":"command","command":"$HOME/bin/wt-hook pre-tool $CLAUDE_TOOL_NAME"}]}
]}}
JSON
    "$WT_STATE" agents install-hooks --template-dir "$cfg_dir" --home "$hd" --state-dir "$hd/state" >/dev/null 2>&1
    "$WT_STATE" agents install-hooks --template-dir "$cfg_dir" --home "$hd" --state-dir "$hd/state" >/dev/null 2>&1

    local foreign wt_entries legacy model
    model=$(jq -r '.model' "$hd/.claude/settings.json")
    foreign=$(jq '[.hooks.PreToolUse[].hooks[].command | select(. == "/opt/foreign/hook")] | length' "$hd/.claude/settings.json")
    wt_entries=$(jq '[.hooks.PreToolUse[].hooks[].command | select(test("wt-hook"))] | length' "$hd/.claude/settings.json")
    legacy=$(jq '[.hooks.PreToolUse[].hooks[].command | select(test("CLAUDE_TOOL_NAME"))] | length' "$hd/.claude/settings.json")

    [[ "$model" == "x" ]] && pass "install preserves foreign settings keys" || fail "foreign key lost"
    [[ "$foreign" == "1" ]] && pass "install preserves foreign hooks" || fail "foreign hook count = $foreign"
    [[ "$wt_entries" == "1" ]] && pass "install is idempotent (one wt entry)" || fail "wt entry count = $wt_entries"
    [[ "$legacy" == "0" ]] && pass "install cleans up legacy wt entries" || fail "legacy entries remain = $legacy"

    rm -rf "$hd"
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
    test_pi_extension
    test_opencode_mcp_config
    test_agent_profiles
    test_agent_launch_plan
    test_hook_format_and_install
    test_install_script
    test_find_git_repos
    test_create_worktree
    test_mobile_session_control
    test_managed_shells
    test_wt_shells_skill
    test_status_file
    test_status_messages_are_data
    test_tmux_options
    test_diff_view
    test_status_metadata_recovery
    test_wt_hook_dual_write
    test_wt_hook_stable_session_identity
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
