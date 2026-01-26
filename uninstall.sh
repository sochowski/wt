#!/usr/bin/env bash
# =============================================================================
#  wt uninstaller - Removes symlinks (leaves data intact)
#  Run: ./uninstall.sh
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$HOME/bin"
CONFIG_DIR="$HOME/.config/wt"
CLAUDE_SETTINGS="$HOME/.claude/settings.json"

echo "Uninstalling wt - Worktree Manager"
echo "==================================="
echo ""

# -----------------------------------------------------------------------------
#  Remove bin symlinks
# -----------------------------------------------------------------------------
echo "Removing symlinks from $BIN_DIR..."

for script in "$SCRIPT_DIR/bin/"*; do
    script_name=$(basename "$script")
    target="$BIN_DIR/$script_name"

    if [[ -L "$target" ]]; then
        rm "$target"
        echo "  Removed $target"
    fi
done

# -----------------------------------------------------------------------------
#  Remove config symlink
# -----------------------------------------------------------------------------
echo "Removing config symlinks..."
target="$CONFIG_DIR/tmux-wt.conf"

if [[ -L "$target" ]]; then
    rm "$target"
    echo "  Removed $target"
fi

rmdir "$CONFIG_DIR" 2>/dev/null || true

# -----------------------------------------------------------------------------
#  Claude hooks
# -----------------------------------------------------------------------------
echo ""
echo "Note: Claude Code hooks in $CLAUDE_SETTINGS were not removed."
echo "You may want to manually remove the 'hooks' section if desired."
echo ""

# -----------------------------------------------------------------------------
#  Data directories
# -----------------------------------------------------------------------------
echo "The following directories were NOT removed (contain your data):"
echo "  ~/worktrees/           - Your worktree directories"
echo "  ~/.local/state/wt/     - Status tracking files"
echo ""
echo "Remove them manually if you want a complete uninstall."
echo ""
echo "==================================="
echo "Uninstall complete!"
