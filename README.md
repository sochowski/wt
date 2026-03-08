# wt

Worktree manager for multi-agent CLI workflows. Each worktree gets a tmux session with agent + nvim + shell windows, live status tracking, and a native tmux UI.

Supports **Claude**, **Codex**, and **Gemini** — any session can use any agent.

## Features

- One worktree = one tmux session (agent, nvim, shell windows)
- Multi-agent: Claude, Codex, Gemini (auto-detects installed agents)
- MCP profiles: per-session MCP server configuration
- Native tmux UI: `choose-tree` switcher with live status icons
- Action menu via `display-menu` for quick session management
- fzf picker with status info and agent conversation preview
- Auto-connects Claude to Neovim via claudecode.nvim (`--ide`)
- Status tracking via dual-write (files + tmux session options)
- PR lookup: find sessions by GitHub PR number
- Master orchestrator session with its own MCP profile
- Desktop notifications when agent needs attention
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
- Merge hooks for detected agents:
  - Claude: `~/.claude/settings.json`
  - Gemini: `~/.gemini/settings.json`
  - Codex: `~/.codex/config.toml`

Requires: git, tmux, fzf, jq, and at least one agent CLI ([Claude Code](https://github.com/anthropics/claude-code), [Codex](https://github.com/openai/codex), or [Gemini CLI](https://github.com/google/gemini-cli))

Optional: [gh](https://cli.github.com/) (for PR lookup), [claudecode.nvim](https://github.com/anthropics/claudecode.nvim) (for Claude IDE integration)

## Usage

```bash
# Session management
wt new                                # Interactive: pick repo, branch, agent
wt new ~/code feature-x              # Direct: create worktree
wt new ~/code feature-x --agent codex # Use specific agent
wt pick                               # fzf picker (? to toggle preview)
wt ls                                 # List sessions with status
wt delete <session>                   # Delete session and worktree

# Master session
wt master                             # Create/attach master session
wt master --force                     # Replace existing master
wt master --agent gemini              # Master with specific agent

# PR integration
wt pr 42                              # Find session for PR #42
wt pr-pick                            # Interactive PR picker (fzf + gh)

# Status
wt sync-tmux                          # Restore tmux options after restart
```

## Tmux Keybindings

| Key | Action |
|-----|--------|
| `prefix + W` | Session switcher (native choose-tree with status icons) |
| `prefix + w` | Action menu (new, switch, delete, PR, master) |
| `prefix + M` | Jump to master orchestrator session |

## Status Icons

| Icon | Status | Description |
|------|--------|-------------|
| `●` | working | Agent is actively using tools (green) |
| `○` | idle | Agent finished, session ready (blue) |
| `⊘` | input | Agent is waiting for your input (yellow) |
| `✗` | error | Agent encountered an error (red) |
| `◆` | master | Master orchestrator session |

## MCP Profiles

Sessions get project-scoped MCP servers via `.mcp.json` files:

- **Worktree sessions**: copies `~/.config/wt/mcp-profiles/default.json` into the worktree (skipped if the repo already has `.mcp.json`)
- **Master session**: copies `~/.config/wt/mcp-profiles/master.json` into `~/worktrees/.master/`

Create profiles:
```bash
# Default profile (all worktree sessions)
cat > ~/.config/wt/mcp-profiles/default.json << 'EOF'
{ "mcpServers": { "my-server": { "type": "http", "url": "https://..." } } }
EOF

# Master profile (orchestrator session)
cat > ~/.config/wt/mcp-profiles/master.json << 'EOF'
{ "mcpServers": { "my-server": { "type": "http", "url": "https://..." } } }
EOF
```

## Architecture

```
Agent hook event (Claude/Codex/Gemini)
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
| `bin/wt-hook` | Agent hook handler (Claude, Codex, Gemini) |
| `bin/wt-tmux-status` | Tmux status bar segment |
| `bin/wt-agent-launch` | Multi-agent launcher with per-agent logic |
| `bin/claude-ide` | Backward-compat wrapper for wt-agent-launch |
| `config/tmux-wt.conf` | Tmux keybindings and hooks |
| `config/claude-hooks.json` | Claude Code hook configuration |
| `config/hooks-gemini.json` | Gemini CLI hook configuration |

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `WT_BASE_DIR` | `~/worktrees` | Base directory for worktrees |
| `WT_STATUS_DIR` | `~/.local/state/wt` | Status file directory |
| `WT_DEFAULT_AGENT` | `claude` | Default agent CLI (claude, codex, gemini) |

## License

GPL-3.0
