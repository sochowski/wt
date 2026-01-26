# wt

Manage multiple git worktrees with async Claude Code sessions. Each worktree gets a tmux session with nvim + claude + shell windows.

## Features

- One worktree = one tmux session (nvim, claude, shell windows)
- Auto-connects Claude to the correct Neovim via claudecode.nvim
- Status tracking: working / idle / needs input
- Desktop notifications when Claude needs attention
- Dashboard TUI + fzf picker
- Cross-platform (Linux / macOS)

## Install

```bash
git clone https://github.com/youruser/wt.git ~/src/wt
~/src/wt/install.sh
```

Add to `~/.tmux.conf`:
```bash
source-file ~/.config/wt/tmux-wt.conf
set -g status-right "#($HOME/bin/wt-tmux-status) | %H:%M"
```

Requires: git, tmux, fzf, jq, [Claude Code](https://github.com/anthropics/claude-code), [claudecode.nvim](https://github.com/anthropics/claudecode.nvim)

## Usage

```bash
wt              # Dashboard
wt new          # Create worktree (interactive)
wt pick         # Switch worktrees (fzf)
```

Tmux: `prefix+w` dashboard, `prefix+W` picker

## Status Icons

- `●` working (green)
- `○` idle (blue)
- `⊘` needs input (yellow)
- `✗` error (red)

## License

GPL-3.0
