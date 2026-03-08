# wt

Manage multiple git worktrees with async Claude Code sessions. Each worktree gets a tmux session with nvim + claude + shell windows, live status tracking, and a native tmux UI.

## Features

- One worktree = one tmux session (nvim, claude, shell windows)
- Native tmux UI: `choose-tree` session switcher with live status icons
- Action menu via `display-menu` for quick worktree management
- Fullscreen fzf picker with live Claude conversation preview
- Auto-connects Claude to Neovim via claudecode.nvim
- Status tracking via dual-write (files + tmux session options)
- PR lookup: find worktree sessions by GitHub PR number
- Master orchestrator session for managing all worktrees
- Desktop notifications when Claude needs attention
- Cross-platform (macOS / Linux)

## Install

```bash
git clone https://github.com/youruser/wt.git ~/src/wt
~/src/wt/install.sh
```

The installer will:
- Symlink scripts to `~/bin/`
- Set up tmux config at `~/.config/wt/tmux-wt.conf`
- Add tmux source line and status bar to `~/.tmux.conf`
- Merge Claude Code hooks into `~/.claude/settings.json`

Requires: git, tmux, fzf, jq, [Claude Code](https://github.com/anthropics/claude-code), [claudecode.nvim](https://github.com/anthropics/claudecode.nvim)

Optional: [gh](https://cli.github.com/) (for PR lookup)

## Usage

```bash
wt                    # Action menu (in tmux) or attach tmux (outside)
wt new                # Create worktree (interactive: pick repo, branch)
wt pick               # Fullscreen fzf picker with Claude conversation preview
wt ls                 # List all worktrees with status
wt master             # Create/attach master orchestrator session
wt pr 42              # Find worktree for PR #42 and switch to it
wt pr-pick            # Interactive PR picker (fzf + gh)
wt delete-pick        # Interactive delete picker
wt sync-tmux          # Restore tmux options from status files (after restart)
```

## Tmux Keybindings

| Key | Action |
|-----|--------|
| `prefix + W` | Session switcher (native choose-tree with status icons) |
| `prefix + w` | Action menu (new, switch, delete, PR lookup, master) |
| `prefix + M` | Jump to master orchestrator session |

## Status Icons

| Icon | Status | Description |
|------|--------|-------------|
| `●` | working | Claude is actively using tools (green) |
| `○` | idle | Claude finished, session ready (blue) |
| `⊘` | input | Claude is waiting for your input (yellow) |
| `✗` | error | Claude encountered an error (red) |
| `◆` | master | Master orchestrator session |

## Architecture

```
Claude Code hook event
  ├── wt-hook writes ~/.local/state/wt/<session>.status
  ├── wt-hook writes tmux @wt-* session options
  └── wt-hook calls tmux refresh-client -S (instant redraw)

prefix+W → choose-tree reads @wt-icon, @wt-status, @wt-message
prefix+w → display-menu launches wt subcommands in popups
Status bar → wt-tmux-status aggregates counts from .status files
```

## Files

| Path | Purpose |
|------|---------|
| `bin/wt` | Main CLI |
| `bin/wt-hook` | Claude Code hook handler |
| `bin/wt-tmux-status` | Tmux status bar segment |
| `bin/claude-ide` | Claude launcher with Neovim IDE integration |
| `config/tmux-wt.conf` | Tmux keybindings and hooks |
| `config/claude-hooks.json` | Claude Code hook configuration |

## License

GPL-3.0
