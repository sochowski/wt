#!/usr/bin/env bash
# =============================================================================
#  wt installer - Sets up symlinks and merges config
#  Run: ./install.sh
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$HOME/bin"
CONFIG_DIR="$HOME/.config/wt"
CLAUDE_SETTINGS="$HOME/.claude/settings.json"

echo "Installing wt - Worktree Manager for Claude Code"
echo "================================================="
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

# -----------------------------------------------------------------------------
#  Merge Claude hooks
# -----------------------------------------------------------------------------
echo "Configuring Claude Code hooks..."

if command -v jq &>/dev/null; then
    # Use jq for proper JSON merge
    if [[ -f "$CLAUDE_SETTINGS" ]]; then
        # Merge hooks into existing settings
        jq -s '.[0] * .[1]' "$CLAUDE_SETTINGS" "$SCRIPT_DIR/config/claude-hooks.json" > "$CLAUDE_SETTINGS.tmp"
        mv "$CLAUDE_SETTINGS.tmp" "$CLAUDE_SETTINGS"
        echo "  Merged hooks into existing $CLAUDE_SETTINGS"
    else
        # Create new settings file
        cp "$SCRIPT_DIR/config/claude-hooks.json" "$CLAUDE_SETTINGS"
        echo "  Created $CLAUDE_SETTINGS"
    fi
else
    echo "  Warning: jq not found. Please manually merge config/claude-hooks.json"
    echo "  into $CLAUDE_SETTINGS"
    echo ""
    echo "  Install jq with:"
    if [[ "$OSTYPE" == "darwin"* ]]; then
        echo "    brew install jq"
    else
        echo "    pacman -S jq"
    fi
fi

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
#  tmux config reminder
# -----------------------------------------------------------------------------
echo "To enable tmux integration, add this to your ~/.tmux.conf:"
echo ""
echo "  # Worktree manager"
echo "  source-file ~/.config/wt/tmux-wt.conf"
echo ""
echo "  # Add to your status-right (example):"
echo "  set -g status-right \"#(\$HOME/bin/wt-tmux-status) | %H:%M\""
echo ""

# -----------------------------------------------------------------------------
#  Done
# -----------------------------------------------------------------------------
echo "================================================="
echo "Installation complete!"
echo ""
echo "Quick start:"
echo "  wt              # Open dashboard"
echo "  wt new          # Create new worktree (interactive)"
echo "  wt pick         # Switch between worktrees"
echo ""
echo "Tmux keybindings (after adding source-file):"
echo "  prefix + w      # Open dashboard popup"
echo "  prefix + W      # Quick worktree picker"
