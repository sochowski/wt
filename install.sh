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

# -----------------------------------------------------------------------------
#  Install Agent Skill
# -----------------------------------------------------------------------------
# One open-standard skill is shared by every harness. Codex, OpenCode, and Pi
# scan ~/.agents/skills; Claude and Gemini use their own user-level skill roots.
# Symlink instead of copying so updates in this checkout apply immediately.
echo "Installing wt-shells agent skill..."
skill_source="$SCRIPT_DIR/config/skills/wt-shells"
skill_targets=(
    "$HOME/.agents/skills/wt-shells"
    "$HOME/.claude/skills/wt-shells"
    "$HOME/.gemini/skills/wt-shells"
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
    echo "  wt-shells -> $target"
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
