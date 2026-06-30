#!/usr/bin/env bash
# =============================================================================
#  wt installer - Sets up symlinks and merges config
#  Supports: Claude, Codex, Gemini, opencode
#  Run: ./install.sh
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$HOME/bin"
CONFIG_DIR="$HOME/.config/wt"
CLAUDE_SETTINGS="$HOME/.claude/settings.json"
GEMINI_SETTINGS="$HOME/.gemini/settings.json"
CODEX_CONFIG="$HOME/.codex/config.toml"
OPENCODE_PLUGIN_DIR="$HOME/.config/opencode/plugins"

echo "Installing wt - Worktree Manager for Multi-Agent CLI"
echo "====================================================="
echo ""

# -----------------------------------------------------------------------------
#  Dependency checks
# -----------------------------------------------------------------------------
echo "Checking dependencies..."

missing=()
for cmd in git tmux fzf jq; do
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

agents=()
for agent in claude codex gemini opencode; do
    command -v "$agent" &>/dev/null && agents+=("$agent")
done

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

if [[ ${#agents[@]} -eq 0 ]]; then
    echo ""
    echo "Error: no agent CLI found. Install at least one of:"
    echo "  claude, codex, gemini, opencode"
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
echo "Configuring agent hooks..."

if command -v jq &>/dev/null; then
    # --- Claude ---
    if command -v claude &>/dev/null; then
        mkdir -p "$HOME/.claude"
        if [[ -f "$CLAUDE_SETTINGS" ]]; then
            jq -s '.[0] * .[1]' "$CLAUDE_SETTINGS" "$SCRIPT_DIR/config/claude-hooks.json" > "$CLAUDE_SETTINGS.tmp"
            mv "$CLAUDE_SETTINGS.tmp" "$CLAUDE_SETTINGS"
            echo "  Claude: merged hooks into $CLAUDE_SETTINGS"
        else
            cp "$SCRIPT_DIR/config/claude-hooks.json" "$CLAUDE_SETTINGS"
            echo "  Claude: created $CLAUDE_SETTINGS"
        fi
    else
        echo "  Claude: not installed, skipping hooks"
    fi

    # --- Gemini ---
    if command -v gemini &>/dev/null; then
        mkdir -p "$HOME/.gemini"
        if [[ -f "$GEMINI_SETTINGS" ]]; then
            jq -s '.[0] * .[1]' "$GEMINI_SETTINGS" "$SCRIPT_DIR/config/hooks-gemini.json" > "$GEMINI_SETTINGS.tmp"
            mv "$GEMINI_SETTINGS.tmp" "$GEMINI_SETTINGS"
            echo "  Gemini: merged hooks into $GEMINI_SETTINGS"
        else
            cp "$SCRIPT_DIR/config/hooks-gemini.json" "$GEMINI_SETTINGS"
            echo "  Gemini: created $GEMINI_SETTINGS"
        fi
    else
        echo "  Gemini: not installed, skipping hooks"
    fi

    # --- Codex ---
    if command -v codex &>/dev/null; then
        mkdir -p "$HOME/.codex"
        if [[ -f "$CODEX_CONFIG" ]]; then
            if ! grep -q 'wt-hook' "$CODEX_CONFIG" 2>/dev/null; then
                cat >> "$CODEX_CONFIG" <<'TOML'

[notify]
command = "$HOME/bin/wt-hook stop"
TOML
                echo "  Codex: added notify hook to $CODEX_CONFIG"
            else
                echo "  Codex: hooks already configured"
            fi
        else
            cat > "$CODEX_CONFIG" <<'TOML'
[notify]
command = "$HOME/bin/wt-hook stop"
TOML
            echo "  Codex: created $CODEX_CONFIG"
        fi
    else
        echo "  Codex: not installed, skipping hooks"
    fi
else
    echo "  Warning: jq not found. Please manually merge hook configs."
    echo "  Install jq with:"
    if [[ "$OSTYPE" == "darwin"* ]]; then
        echo "    brew install jq"
    else
        echo "    pacman -S jq"
    fi
fi

# --- opencode ---
if command -v opencode &>/dev/null; then
    mkdir -p "$OPENCODE_PLUGIN_DIR"
    target="$OPENCODE_PLUGIN_DIR/wt-status.js"

    if [[ -L "$target" ]]; then
        rm "$target"
    elif [[ -e "$target" ]]; then
        echo "  Warning: $target exists and is not a symlink, backing up..."
        mv "$target" "$target.bak"
    fi

    ln -s "$SCRIPT_DIR/config/opencode-wt-plugin.js" "$target"
    echo "  opencode: installed status plugin at $target"
else
    echo "  opencode: not installed, skipping plugin"
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
echo ""
echo "Tmux keybindings:"
echo "  prefix + w      # Action menu (new, delete, PR, master)"
echo "  prefix + W      # Session switcher (choose-tree with status)"
echo "  prefix + M      # Jump to master session"
