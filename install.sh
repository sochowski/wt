#!/usr/bin/env bash
# =============================================================================
#  wt installer - Sets up symlinks and merges config
#  Supports: Claude, Codex, Gemini, opencode, Pi
#  Run: ./install.sh
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$HOME/bin"
CONFIG_DIR="$HOME/.config/wt"

echo "Installing wt - Worktree Manager for Multi-Agent CLI"
echo "====================================================="
echo ""

# -----------------------------------------------------------------------------
#  Dependency checks
# -----------------------------------------------------------------------------
echo "Checking dependencies..."

missing=()
for cmd in git tmux fzf jq go; do
    if command -v "$cmd" &>/dev/null; then
        echo "  $cmd: ok"
    else
        echo "  $cmd: MISSING"
        missing+=("$cmd")
    fi
done

if command -v gh &>/dev/null; then
    echo "  gh: ok (optional)"
else
    echo "  gh: not found (optional - needed for PR lookup)"
fi

if [[ ${#missing[@]} -gt 0 ]]; then
    echo ""
    echo "Error: missing required commands: ${missing[*]}"
    if [[ "$OSTYPE" == "darwin"* ]]; then
        echo "  brew install ${missing[*]}"
    else
        echo "  pacman -S ${missing[*]}         # Arch"
        echo "  sudo apt install ${missing[*]}  # Debian/Ubuntu"
    fi
    exit 1
fi

# Unpublished paired-source contract: no guessed npm release and no bare-module
# resolution across Pi's separate package roots. Runtime acknowledgement remains
# authoritative even if this checkout is later removed/disabled or fails to load.
if command -v pi &>/dev/null; then
    if [[ -z "${WT_PI_SUBAGENTS_SOURCE:-}" ]]; then
        echo "Error: Pi native delegation requires a compatible local pi-subagents checkout."
        echo "Set WT_PI_SUBAGENTS_SOURCE to its absolute path (see docs/native-delegation.md)."
        exit 1
    fi
    WT_PI_SUBAGENTS_SOURCE="$(cd "$WT_PI_SUBAGENTS_SOURCE" && pwd -P)"
    if ! jq -e '.name == "pi-subagents" and .wtNativeProviderContract == 1 and .pi.extensions == ["./index.ts"]' "$WT_PI_SUBAGENTS_SOURCE/package.json" >/dev/null; then
        echo "Error: pi-subagents checkout does not declare the WT native-provider v1 contract."
        exit 1
    fi
    if [[ ! -f "$WT_PI_SUBAGENTS_SOURCE/index.ts" || ! -f "$WT_PI_SUBAGENTS_SOURCE/node_modules/jiti/package.json" ]]; then
        echo "Error: compatible pi-subagents source and installed runtime dependencies are required."
        exit 1
    fi
fi

# Check all entries before any install side effects; jq -e on a stream would
# inspect only the final package and miss an older package earlier in the list.
if [[ -n "${WT_PI_SUBAGENTS_SOURCE:-}" && -f "$HOME/.pi/agent/settings.json" ]]; then
    if ! has_old_pi_subagents="$(jq 'any(.packages[]?; (if type == "object" then .source else . end) | test("^npm:pi-subagents(@|$)"))' "$HOME/.pi/agent/settings.json")"; then
        echo "Error: cannot validate existing Pi package settings."
        exit 1
    fi
    if [[ "$has_old_pi_subagents" == true ]]; then
        echo "Error: remove the old npm pi-subagents entry before paired-source installation."
        exit 1
    fi
fi

# Build before agent detection so the registry really is the only roster wt
# maintains. Adding an agent should not require another hard-coded shell list.
echo "Building wt-state..."
( cd "$SCRIPT_DIR/state" && go build -o "$SCRIPT_DIR/bin/wt-state" . )
echo "  wt-state -> $SCRIPT_DIR/bin/wt-state"

agents=()
while IFS= read -r agent; do
    [[ -n "$agent" ]] && agents+=("$agent")
done < <("$SCRIPT_DIR/bin/wt-state" agents list --available)

if [[ ${#agents[@]} -eq 0 ]]; then
    supported_agents=()
    while IFS= read -r agent; do
        [[ -n "$agent" ]] && supported_agents+=("$agent")
    done < <("$SCRIPT_DIR/bin/wt-state" agents list)
    echo ""
    echo "Error: no agent CLI found. Install at least one of:"
    echo "  ${supported_agents[*]}"
    exit 1
fi
echo "  agents: ${agents[*]}"
echo ""

# -----------------------------------------------------------------------------
#  Create directories
# -----------------------------------------------------------------------------
echo "Creating directories..."
mkdir -p "$BIN_DIR"
mkdir -p "$CONFIG_DIR"
mkdir -p "$HOME/.claude"
mkdir -p "$HOME/.local/state/wt"
mkdir -p "$HOME/worktrees"

# -----------------------------------------------------------------------------
#  Symlink bin scripts
# -----------------------------------------------------------------------------
echo "Symlinking scripts to $BIN_DIR..."

for script in "$SCRIPT_DIR/bin/"*; do
    script_name=$(basename "$script")
    target="$BIN_DIR/$script_name"

    if [[ -L "$target" ]]; then
        rm "$target"
    elif [[ -e "$target" ]]; then
        echo "  Warning: $target exists and is not a symlink, backing up..."
        mv "$target" "$target.bak"
    fi

    ln -s "$script" "$target"
    chmod +x "$script"
    echo "  $script_name -> $target"
done

# -----------------------------------------------------------------------------
#  Migrate legacy .status files into the SQLite store (idempotent)
# -----------------------------------------------------------------------------
echo "Migrating existing session state into SQLite..."
"$SCRIPT_DIR/bin/wt-state" migrate || echo "  (nothing to migrate)"

# -----------------------------------------------------------------------------
#  Symlink tmux config
# -----------------------------------------------------------------------------
echo "Symlinking tmux config..."
target="$CONFIG_DIR/tmux-wt.conf"

if [[ -L "$target" ]]; then
    rm "$target"
elif [[ -e "$target" ]]; then
    mv "$target" "$target.bak"
fi

ln -s "$SCRIPT_DIR/config/tmux-wt.conf" "$target"
echo "  tmux-wt.conf -> $target"

# tmux-panes.conf — dwm/i3-style pane keybindings (sourced by tmux-wt.conf)
target="$CONFIG_DIR/tmux-panes.conf"
if [[ -L "$target" ]]; then
    rm "$target"
elif [[ -e "$target" ]]; then
    mv "$target" "$target.bak"
fi
ln -s "$SCRIPT_DIR/config/tmux-panes.conf" "$target"
echo "  tmux-panes.conf -> $target"

# -----------------------------------------------------------------------------
#  Symlink menu config
# -----------------------------------------------------------------------------
# wt-bind-menu reads this on tmux startup to generate the prefix+w action menu
# and the direct prefix+<key> bindings. Without it, those bindings never load.
echo "Symlinking menu config..."
target="$CONFIG_DIR/wt-menu.conf"

if [[ -L "$target" ]]; then
    rm "$target"
elif [[ -e "$target" ]]; then
    mv "$target" "$target.bak"
fi

ln -s "$SCRIPT_DIR/config/wt-menu.conf" "$target"
echo "  wt-menu.conf -> $target"

# -----------------------------------------------------------------------------
#  Merge Agent Hooks
# -----------------------------------------------------------------------------
# The per-agent wiring (which config format, which file, idempotency) lives in
# the wt-state agent registry. install-hooks iterates every installed agent and
# applies its hook spec, reading templates from config/.
echo "Configuring agent hooks..."
"$SCRIPT_DIR/bin/wt-state" agents install-hooks --template-dir "$SCRIPT_DIR/config"

if [[ -n "${WT_PI_SUBAGENTS_SOURCE:-}" ]]; then
    pi_settings="$HOME/.pi/agent/settings.json"
    mkdir -p "$(dirname "$pi_settings")"
    [[ -f "$pi_settings" ]] || printf '{}\n' > "$pi_settings"
    # Leave unrelated packages/settings untouched; duplicates were rejected in preflight.
    tmp_settings="$(mktemp "${pi_settings}.XXXXXX")"
    jq --arg source "$WT_PI_SUBAGENTS_SOURCE" '.packages = ((.packages // []) | if any(.[]; (if type == "object" then .source else . end) == $source) then . else . + [$source] end)' "$pi_settings" > "$tmp_settings"
    chmod 600 "$tmp_settings"
    mv "$tmp_settings" "$pi_settings"
    echo "  pi-subagents native-provider v1 -> $WT_PI_SUBAGENTS_SOURCE"
fi

# -----------------------------------------------------------------------------
#  Install Agent Skills
# -----------------------------------------------------------------------------
# Open-standard skills are shared by every harness. Codex, OpenCode, and Pi scan
# ~/.agents/skills; Claude and Gemini use their own user-level skill roots.
# Symlink instead of copying so updates in this checkout apply immediately.
# Remove only obsolete links owned by this checkout, never user skills.
for skill_root in "$HOME/.agents/skills" "$HOME/.claude/skills" "$HOME/.gemini/skills"; do
    for old_skill in wt-shells wt-presentations; do
        old_target="$skill_root/$old_skill"
        if [[ -L "$old_target" && "$(readlink -f "$old_target")" == "$SCRIPT_DIR/config/skills/$old_skill" ]]; then
            rm "$old_target"
        fi
    done
done
for skill_name in wt; do
    echo "Installing $skill_name agent skill..."
    skill_source="$SCRIPT_DIR/config/skills/$skill_name"
    skill_targets=(
        "$HOME/.agents/skills/$skill_name"
        "$HOME/.claude/skills/$skill_name"
        "$HOME/.gemini/skills/$skill_name"
    )
    for target in "${skill_targets[@]}"; do
        mkdir -p "$(dirname "$target")"
        if [[ -L "$target" ]]; then
            if [[ "$(readlink -f "$target")" != "$(readlink -f "$skill_source")" ]]; then
                echo "  Warning: $target is a symlink to another skill; leaving it unchanged"
                continue
            fi
            rm "$target"
        elif [[ -e "$target" ]]; then
            echo "  Warning: $target already exists; leaving it unchanged"
            continue
        fi
        ln -s "$skill_source" "$target"
        echo "  $skill_name -> $target"
    done
done

# -----------------------------------------------------------------------------
#  Check PATH
# -----------------------------------------------------------------------------
echo ""
if [[ ":$PATH:" != *":$BIN_DIR:"* ]]; then
    echo "Note: $BIN_DIR is not in your PATH."
    echo "Add this to your ~/.bashrc or ~/.zshrc:"
    echo ""
    echo "  export PATH=\"\$HOME/bin:\$PATH\""
    echo ""
fi

# -----------------------------------------------------------------------------
#  tmux config integration
# -----------------------------------------------------------------------------
TMUX_CONF="$HOME/.tmux.conf"
TMUX_SOURCE_LINE="source-file ~/.config/wt/tmux-wt.conf"
TMUX_STATUS_LINE='set -g status-right "#($HOME/bin/wt-tmux-status) | %H:%M"'

echo "Configuring tmux integration..."

if [[ -f "$TMUX_CONF" ]]; then
    if grep -qF "tmux-wt.conf" "$TMUX_CONF"; then
        echo "  tmux-wt.conf already sourced in $TMUX_CONF"
    else
        echo "" >> "$TMUX_CONF"
        echo "# Worktree manager" >> "$TMUX_CONF"
        echo "$TMUX_SOURCE_LINE" >> "$TMUX_CONF"
        echo "$TMUX_STATUS_LINE" >> "$TMUX_CONF"
        echo "  Added wt config to $TMUX_CONF"
    fi
else
    echo "# Worktree manager" > "$TMUX_CONF"
    echo "$TMUX_SOURCE_LINE" >> "$TMUX_CONF"
    echo "$TMUX_STATUS_LINE" >> "$TMUX_CONF"
    echo "  Created $TMUX_CONF with wt config"
fi

echo ""
echo "  Reload tmux config with: tmux source-file ~/.tmux.conf"

# -----------------------------------------------------------------------------
#  Done
# -----------------------------------------------------------------------------
echo ""
echo "====================================================="
echo "Installation complete!"
echo ""
echo "Quick start:"
echo "  wt ls           # List worktrees"
echo "  wt new          # Create new worktree (interactive)"
echo "  wt pick         # Switch between worktrees"
echo "  wt master       # Create master orchestrator session"
echo "  \$wt-shells      # Teach an agent to manage persistent shells"
echo ""
echo "Tmux keybindings:"
echo "  prefix + c      # Create a managed shell (inside worktree sessions)"
echo "  prefix + w      # Action menu (new, delete, PR, master)"
echo "  prefix + W      # Session switcher (choose-tree with status)"
echo "  prefix + M      # Jump to master session"
